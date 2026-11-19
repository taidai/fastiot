package pumphouse

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
)

// Server serves the plant over Modbus TCP so the sample uses the production
// transport path (modbusrt.OpenTCP) instead of an in-process shortcut.
type Server struct {
	plant    *Plant
	listener net.Listener
	mu       sync.Mutex
	conns    map[net.Conn]struct{}
	closed   bool
}

// ServeTCP listens on addr (host:port; port 0 picks a free port) and returns
// the running server. Close stops the listener and every open connection.
func (p *Plant) ServeTCP(addr string) (*Server, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	server := &Server{plant: p, listener: listener, conns: map[net.Conn]struct{}{}}
	go server.accept()
	return server, nil
}

// Addr is the listener address, useful when port 0 was requested.
func (s *Server) Addr() string { return s.listener.Addr().String() }

// Close stops accepting and closes every connection.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	conns := make([]net.Conn, 0, len(s.conns))
	for conn := range s.conns {
		conns = append(conns, conn)
	}
	s.conns = map[net.Conn]struct{}{}
	s.mu.Unlock()
	err := s.listener.Close()
	for _, conn := range conns {
		_ = conn.Close()
	}
	return err
}

func (s *Server) accept() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = conn.Close()
			return
		}
		s.conns[conn] = struct{}{}
		s.mu.Unlock()
		go s.serveConn(conn)
	}
}

func (s *Server) serveConn(conn net.Conn) {
	defer func() {
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		_ = conn.Close()
	}()
	header := make([]byte, 7)
	for {
		if _, err := io.ReadFull(conn, header); err != nil {
			return
		}
		transaction := binary.BigEndian.Uint16(header[0:2])
		protocol := binary.BigEndian.Uint16(header[2:4])
		length := int(binary.BigEndian.Uint16(header[4:6]))
		unit := header[6]
		if protocol != 0 || length < 2 || length > 254 {
			return
		}
		pdu := make([]byte, length-1)
		if _, err := io.ReadFull(conn, pdu); err != nil {
			return
		}
		response := s.handle(unit, pdu)
		packet := make([]byte, 7+len(response))
		binary.BigEndian.PutUint16(packet[0:2], transaction)
		binary.BigEndian.PutUint16(packet[2:4], 0)
		binary.BigEndian.PutUint16(packet[4:6], uint16(1+len(response)))
		packet[6] = unit
		copy(packet[7:], response)
		if _, err := conn.Write(packet); err != nil {
			return
		}
	}
}

func exception(function byte, code byte) []byte {
	return []byte{function | 0x80, code}
}

func exceptionFor(function byte, err error) []byte {
	switch {
	case errors.Is(err, ErrRefused):
		return exception(function, 4) // server device failure
	case errors.Is(err, ErrIllegalValue):
		return exception(function, 3) // illegal data value
	default:
		return exception(function, 2) // illegal data address
	}
}

func (s *Server) handle(unit byte, pdu []byte) []byte {
	function := pdu[0]
	if unit != s.plant.cfg.Station {
		return exception(function, 2)
	}
	switch function {
	case 1, 2:
		if len(pdu) != 5 {
			return exception(function, 3)
		}
		address := binary.BigEndian.Uint16(pdu[1:3])
		count := binary.BigEndian.Uint16(pdu[3:5])
		if count < 1 || count > 2000 || int(address)+int(count)-1 > 65535 {
			return exception(function, 3)
		}
		bits, err := s.readBits(address, count)
		if err != nil {
			return exceptionFor(function, err)
		}
		return append([]byte{function, byte((len(bits) + 7) / 8)}, packBits(bits)...)
	case 3, 4:
		if len(pdu) != 5 {
			return exception(function, 3)
		}
		address := binary.BigEndian.Uint16(pdu[1:3])
		count := binary.BigEndian.Uint16(pdu[3:5])
		if count < 1 || count > 125 || int(address)+int(count)-1 > 65535 {
			return exception(function, 3)
		}
		if function == 4 {
			return exception(function, 2) // input registers are not mapped
		}
		words, err := s.readHolding(address, count)
		if err != nil {
			return exceptionFor(function, err)
		}
		response := make([]byte, 2, 2+len(words)*2)
		response[0] = function
		response[1] = byte(len(words) * 2)
		for _, word := range words {
			response = binary.BigEndian.AppendUint16(response, word)
		}
		return response
	case 5:
		if len(pdu) != 5 {
			return exception(function, 3)
		}
		return exception(function, 2) // coils are not mapped
	case 6:
		if len(pdu) != 5 {
			return exception(function, 3)
		}
		address := binary.BigEndian.Uint16(pdu[1:3])
		value := binary.BigEndian.Uint16(pdu[3:5])
		if err := s.writeHolding(address, value); err != nil {
			return exceptionFor(function, err)
		}
		return append([]byte(nil), pdu[:5]...)
	default:
		return exception(function, 1) // illegal function
	}
}

func (s *Server) readBits(address, count uint16) ([]bool, error) {
	s.plant.mu.Lock()
	defer s.plant.mu.Unlock()
	s.plant.advanceLocked(s.plant.now())
	bits := make([]bool, count)
	for i := range bits {
		value, err := s.plant.bitLocked(address + uint16(i))
		if err != nil {
			return nil, err
		}
		bits[i] = value
	}
	return bits, nil
}

func (s *Server) readHolding(address, count uint16) ([]uint16, error) {
	s.plant.mu.Lock()
	defer s.plant.mu.Unlock()
	s.plant.advanceLocked(s.plant.now())
	words := make([]uint16, count)
	for i := range words {
		value, err := s.plant.holdingLocked(address + uint16(i))
		if err != nil {
			return nil, err
		}
		words[i] = value
	}
	return words, nil
}

func (s *Server) writeHolding(address, value uint16) error {
	s.plant.mu.Lock()
	defer s.plant.mu.Unlock()
	s.plant.advanceLocked(s.plant.now())
	return s.plant.writeHoldingLocked(address, value)
}

func packBits(bits []bool) []byte {
	packed := make([]byte, (len(bits)+7)/8)
	for i, bit := range bits {
		if bit {
			packed[i/8] |= 1 << (uint(i) % 8)
		}
	}
	return packed
}
