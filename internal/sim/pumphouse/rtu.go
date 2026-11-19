package pumphouse

import (
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/simpleiot/simpleiot/modbus"
	"github.com/simpleiot/simpleiot/respreader"
)

// RTUFaults injects the link faults an acceptance run has to be able to
// repeat on demand. Clearing every field restores normal service.
type RTUFaults struct {
	// Mute accepts requests and never answers, like a station that is off.
	Mute bool
	// ResponseDelay holds each answer back, so a master can time out first.
	ResponseDelay time.Duration
	// CorruptResponseCRC flips a byte of the checksum on the way out.
	CorruptResponseCRC bool
	// CorruptRequestCRC flips a byte of the checksum on the way in, the way
	// electrical noise would, before the station checks it.
	CorruptRequestCRC bool
	// ResponseStation answers under another station address (0 keeps the
	// configured one), like a duplicated or misaddressed slave on the line.
	ResponseStation byte
}

// RTUStats is what the station observed on the line.
type RTUStats struct {
	Requests       int
	Responses      int
	CRCErrors      int
	IgnoredStation int
}

// RTUServer serves the plant as a Modbus RTU slave over a serial byte stream.
// The transport is the same one the master uses (modbus.NewRTU), so frames and
// CRCs are built and checked by production code.
type RTUServer struct {
	plant     *Plant
	transport modbus.Transport
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once

	mu     sync.Mutex
	faults RTUFaults
	stats  RTUStats
}

// ServeRTU runs the plant as an RTU slave on port. timeout is how long the
// frame reader waits for a request before it treats the line as idle.
func (p *Plant) ServeRTU(port io.ReadWriteCloser, timeout time.Duration) *RTUServer {
	if timeout <= 0 {
		timeout = 500 * time.Millisecond
	}
	reader := respreader.NewReadWriteCloser(port, timeout, 20*time.Millisecond)
	server := &RTUServer{
		plant:     p,
		transport: modbus.NewRTU(reader),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	go server.listen()
	return server
}

// Close stops the serving loop and releases the port.
func (s *RTUServer) Close() error {
	s.closeOnce.Do(func() {
		close(s.stop)
		_ = s.transport.Close()
	})
	<-s.done
	return nil
}

// SetFaults replaces the injected link faults.
func (s *RTUServer) SetFaults(faults RTUFaults) {
	s.mu.Lock()
	s.faults = faults
	s.mu.Unlock()
}

// Faults returns the faults currently injected.
func (s *RTUServer) Faults() RTUFaults { return s.faultsNow() }

// Stats returns a copy of the line counters.
func (s *RTUServer) Stats() RTUStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

func (s *RTUServer) count(update func(*RTUStats)) {
	s.mu.Lock()
	update(&s.stats)
	s.mu.Unlock()
}

func (s *RTUServer) faultsNow() RTUFaults {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.faults
}

func (s *RTUServer) listen() {
	defer close(s.done)
	for {
		select {
		case <-s.stop:
			return
		default:
		}
		buffer := make([]byte, 256)
		count, err := s.transport.Read(buffer)
		if err != nil {
			if errors.Is(err, io.EOF) {
				continue // an idle line is a framing timeout, not a fault
			}
			select {
			case <-s.stop:
				return
			case <-time.After(20 * time.Millisecond):
			}
			continue
		}
		if count <= 0 {
			continue
		}
		s.handle(buffer[:count])
	}
}

func (s *RTUServer) handle(packet []byte) {
	faults := s.faultsNow()
	if faults.CorruptRequestCRC {
		packet = flipLastByte(packet)
	}
	station, pdu, err := s.transport.Decode(packet)
	if err != nil {
		s.count(func(stats *RTUStats) { stats.CRCErrors++ })
		return
	}
	if station != s.plant.cfg.Station {
		// On a shared line every slave hears everyone else's request; only
		// the addressed one answers.
		s.count(func(stats *RTUStats) { stats.IgnoredStation++ })
		return
	}
	s.count(func(stats *RTUStats) { stats.Requests++ })

	request := make([]byte, 0, 1+len(pdu.Data))
	request = append(request, byte(pdu.FunctionCode))
	request = append(request, pdu.Data...)
	response := s.plant.handlePDU(station, request)
	if faults.Mute {
		return
	}
	if faults.ResponseDelay > 0 {
		select {
		case <-s.stop:
			return
		case <-time.After(faults.ResponseDelay):
		}
	}
	frame, err := s.transport.Encode(station, modbus.PDU{
		FunctionCode: modbus.FunctionCode(response[0]),
		Data:         response[1:],
	})
	if err != nil {
		return
	}
	if faults.CorruptResponseCRC {
		frame = flipLastByte(frame)
	}
	if faults.ResponseStation != 0 && faults.ResponseStation != station {
		frame[0] = faults.ResponseStation
		binary.BigEndian.PutUint16(frame[len(frame)-2:], modbus.RtuCrc(frame[:len(frame)-2]))
	}
	if _, err := s.transport.Write(frame); err != nil {
		return
	}
	s.count(func(stats *RTUStats) { stats.Responses++ })
}

func flipLastByte(packet []byte) []byte {
	corrupted := append([]byte(nil), packet...)
	corrupted[len(corrupted)-1] ^= 0xFF
	return corrupted
}
