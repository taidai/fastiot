//go:build linux

package pumphouse

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/simpleiot/simpleiot/modbus"

	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/framelog"
	"fastiot/internal/ems/modbusrt"
	"fastiot/internal/ems/signal"
)

// The pty pair is a real byte stream: the master side is the station's serial
// line, the slave path is what the master station opens like /dev/ttyUSB0.
// Neither side knows it is talking to a simulator; framing, CRC and station
// addressing all go through the production code paths.
const (
	rtuMasterTimeout = 200 * time.Millisecond
	rtuServerTimeout = 500 * time.Millisecond
)

type rtuHarness struct {
	clock   *clock
	plant   *Plant
	server  *RTUServer
	path    string
	bus     *closingBus
	channel *channel.Channel

	mu     sync.Mutex
	buses  []*closingBus
	closed bool
}

// closingBus makes Close idempotent so a test can pull the cable and cleanup
// still has something safe to call.
type closingBus struct {
	*modbusrt.Bus
	once sync.Once
}

func (b *closingBus) Close() error {
	var err error
	b.once.Do(func() { err = b.Bus.Close() })
	return err
}

func openVirtualSerial(t *testing.T) (device *os.File, busPath string) {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatalf("open pty pair: %v", err)
	}
	busPath = slave.Name()
	if err := slave.Close(); err != nil {
		t.Fatalf("release pty slave handle: %v", err)
	}
	t.Cleanup(func() { _ = master.Close() })
	return master, busPath
}

func newRTUHarness(t *testing.T) *rtuHarness {
	t.Helper()
	plant, c := newTestPlant(t)
	device, path := openVirtualSerial(t)
	server := plant.ServeRTU(device, rtuServerTimeout)
	t.Cleanup(func() { _ = server.Close() })
	h := &rtuHarness{clock: c, plant: plant, server: server, path: path}
	h.open(t)
	t.Cleanup(h.disconnect)
	return h
}

// open dials the virtual serial line the way the runtime does for an RTU
// channel address (port@baud).
func (h *rtuHarness) open(t *testing.T) {
	t.Helper()
	bus, err := modbusrt.OpenRTUConfig(modbusrt.SerialConfig{Port: h.path, Baud: 9600}, rtuMasterTimeout)
	if err != nil {
		t.Fatalf("open RTU master on %s: %v", h.path, err)
	}
	ch, err := channel.New("rtu-virtual", bus, []channel.Device{{
		ID: "pumphouse", ServerID: StationID,
		Points: []channel.Point{
			{ID: "level", Function: channel.HoldingRegister, Address: AddressLevel, Format: "uint16", Scale: 0.1, Kind: signal.Number},
			{ID: "pressure", Function: channel.HoldingRegister, Address: AddressPressure, Format: "uint16", Scale: 0.01, Kind: signal.Number},
			{ID: "pump1Run", Function: channel.DiscreteInput, Address: BitPump1Run, Kind: signal.Bool},
		},
	}})
	if err != nil {
		t.Fatalf("build channel: %v", err)
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		_ = bus.Close()
		t.Fatal("harness closed")
	}
	wrapped := &closingBus{Bus: bus}
	h.buses = append(h.buses, wrapped)
	h.bus, h.channel = wrapped, ch
	h.mu.Unlock()
}

func (h *rtuHarness) disconnect() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	buses := h.buses
	h.mu.Unlock()
	for _, bus := range buses {
		_ = bus.Close()
	}
}

// unplug closes the master station's serial port and leaves the station
// running, which is what a pulled cable looks like to the polling channel.
func (h *rtuHarness) unplug() {
	_ = h.bus.Close()
}

func (h *rtuHarness) run(t *testing.T) []channel.Result {
	t.Helper()
	results, _ := h.channel.RunOnce(context.Background(), time.Now)
	return results
}

func readValue(t *testing.T, results []channel.Result, pointID string) (channel.Result, bool) {
	t.Helper()
	for _, result := range results {
		if result.PointID == pointID {
			return result, true
		}
	}
	t.Fatalf("point %s missing from results %+v", pointID, results)
	return channel.Result{}, false
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func recordedFrame(t *testing.T, direction string, function byte) []byte {
	t.Helper()
	for _, frame := range framelog.Default.List(64) {
		if frame.Direction != direction {
			continue
		}
		raw, err := hex.DecodeString(frame.Hex)
		if err != nil {
			t.Fatalf("decode recorded frame %q: %v", frame.Hex, err)
		}
		if len(raw) > 1 && raw[1] == function {
			return raw
		}
	}
	t.Fatalf("no %s frame with function %d in %+v", direction, function, framelog.Default.List(64))
	return nil
}

func assertCRC(t *testing.T, label string, frame []byte) {
	t.Helper()
	if err := modbus.CheckRtuCrc(frame); err != nil {
		t.Fatalf("%s frame % x has no valid RTU CRC: %v", label, frame, err)
	}
}

func TestRTUVirtualSerialFramesCRCAndStation(t *testing.T) {
	h := newRTUHarness(t)
	framelog.Default.Clear()

	results := h.run(t)
	level, _ := readValue(t, results, "level")
	if level.Value.Num != 55 {
		t.Fatalf("level = %+v, want 55%% from register 550", level.Value)
	}
	pressure, _ := readValue(t, results, "pressure")
	if pressure.Value.Num != 0.08 {
		t.Fatalf("pressure = %+v, want 0.08 MPa from register 8", pressure.Value)
	}
	if got := h.channel.Health("pumphouse", time.Now(), time.Minute); got != channel.HealthHealthy {
		t.Fatalf("health = %s, want healthy after a valid exchange", got)
	}

	request := recordedFrame(t, "tx", 3)
	assertCRC(t, "request", request)
	if request[0] != StationID {
		t.Fatalf("request station = %d, want %d", request[0], StationID)
	}
	if got := binary.BigEndian.Uint16(request[2:4]); got != AddressLevel {
		t.Fatalf("request address = %d, want %d", got, AddressLevel)
	}
	if got := binary.BigEndian.Uint16(request[4:6]); got != 2 {
		t.Fatalf("request register count = %d, want 2 (level+pressure block)", got)
	}

	response := recordedFrame(t, "rx", 3)
	assertCRC(t, "response", response)
	if response[0] != StationID {
		t.Fatalf("response station = %d, want %d", response[0], StationID)
	}
	if response[2] != 4 {
		t.Fatalf("response byte count = %d, want 4", response[2])
	}
	if got := binary.BigEndian.Uint16(response[3:5]); got != 550 {
		t.Fatalf("response level register = %d, want 550", got)
	}
	t.Logf("holding-register request  % x", request)
	t.Logf("holding-register response % x", response)

	bits := recordedFrame(t, "rx", 2)
	assertCRC(t, "discrete-input response", bits)
	if bits[0] != StationID || bits[2] != 1 {
		t.Fatalf("discrete-input response = % x", bits)
	}

	// A frame for a different station must be validated and dropped, not
	// answered: the slave stays silent and the channel reports a timeout.
	absent, err := channel.New("absent", h.bus, []channel.Device{{
		ID: "absent", ServerID: 9,
		Points: []channel.Point{{ID: "level", Function: channel.HoldingRegister, Address: AddressLevel, Format: "uint16", Kind: signal.Number}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ignored := h.server.Stats().IgnoredStation
	_, _ = absent.RunOnce(context.Background(), time.Now)
	if stats := absent.Stats("absent"); !strings.Contains(stats.LastError, "EOF") {
		t.Fatalf("absent station error = %q, want a serial timeout", stats.LastError)
	}
	if got := h.server.Stats().IgnoredStation; got != ignored+1 {
		t.Fatalf("slave ignored %d foreign frames, want %d", got, ignored+1)
	}
}

func TestRTUVirtualSerialTimeoutAndRecovery(t *testing.T) {
	h := newRTUHarness(t)
	if got := h.run(t); len(got) == 0 {
		t.Fatal("baseline poll returned nothing")
	}

	h.server.SetFaults(RTUFaults{Mute: true})
	started := time.Now()
	results := h.run(t)
	if elapsed := time.Since(started); elapsed < rtuMasterTimeout {
		t.Fatalf("muted station returned in %s, want a %s timeout", elapsed, rtuMasterTimeout)
	}
	for _, result := range results {
		if result.Quality == signal.Good {
			t.Fatalf("muted station still produced a good value: %+v", result)
		}
	}
	stats := h.channel.Stats("pumphouse")
	if stats.Failures == 0 || !strings.Contains(stats.LastError, "EOF") {
		t.Fatalf("timeout was not recorded: %+v", stats)
	}
	if got := h.channel.Health("pumphouse", time.Now(), time.Minute); got != channel.HealthDegraded {
		t.Fatalf("health = %s, want degraded after one timeout", got)
	}

	h.server.SetFaults(RTUFaults{})
	results = h.run(t)
	level, _ := readValue(t, results, "level")
	if level.Quality != signal.Good || level.Value.Num != 55 {
		t.Fatalf("recovery read = %+v, want level 55", level)
	}
	if got := h.channel.Health("pumphouse", time.Now(), time.Minute); got != channel.HealthHealthy {
		t.Fatalf("health = %s, want healthy after recovery", got)
	}
}

func TestRTUVirtualSerialLateReadback(t *testing.T) {
	h := newRTUHarness(t)
	if results := h.run(t); len(results) == 0 {
		t.Fatal("baseline poll returned nothing")
	}

	h.server.SetFaults(RTUFaults{ResponseDelay: 2 * rtuMasterTimeout})
	late := h.server.Stats().Responses
	started := time.Now()
	results := h.run(t)
	if elapsed := time.Since(started); elapsed < rtuMasterTimeout || elapsed > 3*rtuMasterTimeout {
		t.Fatalf("late response returned in %s, want the %s master timeout", elapsed, rtuMasterTimeout)
	}
	for _, result := range results {
		if result.Quality == signal.Good {
			t.Fatalf("late readback was accepted as fresh: %+v", result)
		}
	}
	// The station still answers, just too late: the frame is now waiting on
	// the wire for the next transaction.
	waitFor(t, "the late frame to be written", func() bool {
		return h.server.Stats().Responses > late
	})

	// Change the process state so an accepted late frame is visible as a
	// stale value: the delayed frame carries 550, the next request must
	// return 535.
	h.clock.Advance(10 * time.Second)
	h.server.SetFaults(RTUFaults{})
	results = h.run(t)
	level, _ := readValue(t, results, "level")
	if level.Quality != signal.Good {
		t.Fatalf("recovery read = %+v", level)
	}
	if level.Value.Num != 53.5 {
		t.Fatalf("level after recovery = %v, want 53.5 (the late 55.0 frame must be flushed)", level.Value.Num)
	}
}

func TestRTUVirtualSerialDisconnectAndRecover(t *testing.T) {
	h := newRTUHarness(t)
	if results := h.run(t); len(results) == 0 {
		t.Fatal("baseline poll returned nothing")
	}

	h.unplug()
	results := h.run(t)
	for _, result := range results {
		if result.Quality == signal.Good {
			t.Fatalf("read succeeded with the cable pulled: %+v", result)
		}
	}
	if stats := h.channel.Stats("pumphouse"); stats.Failures == 0 {
		t.Fatalf("disconnect was not recorded: %+v", stats)
	}

	h.open(t)
	results = h.run(t)
	level, _ := readValue(t, results, "level")
	if level.Quality != signal.Good || level.Value.Num != 55 {
		t.Fatalf("after reconnect = %+v, want level 55", level)
	}
	if got := h.channel.Health("pumphouse", time.Now(), time.Minute); got != channel.HealthHealthy {
		t.Fatalf("health = %s, want healthy after reconnect", got)
	}
}

func TestRTUVirtualSerialResponseStationGuard(t *testing.T) {
	h := newRTUHarness(t)
	if results := h.run(t); len(results) == 0 {
		t.Fatal("baseline poll returned nothing")
	}

	// A well-formed, correctly checksummed answer that carries another
	// station's address must be rejected by the master, not decoded.
	h.server.SetFaults(RTUFaults{ResponseStation: 7})
	results := h.run(t)
	for _, result := range results {
		if result.Quality == signal.Good {
			t.Fatalf("answer from station 7 was accepted as station 1: %+v", result)
		}
	}
	stats := h.channel.Stats("pumphouse")
	if !strings.Contains(stats.LastError, "does not match request station") {
		t.Fatalf("foreign-station error = %q, want a station mismatch", stats.LastError)
	}

	h.server.SetFaults(RTUFaults{})
	results = h.run(t)
	level, _ := readValue(t, results, "level")
	if level.Quality != signal.Good || level.Value.Num != 55 {
		t.Fatalf("recovery read = %+v, want level 55", level)
	}
}

func TestRTUVirtualSerialCRCRejection(t *testing.T) {
	h := newRTUHarness(t)
	if results := h.run(t); len(results) == 0 {
		t.Fatal("baseline poll returned nothing")
	}

	// A corrupted response must be rejected by the master, not decoded.
	h.server.SetFaults(RTUFaults{CorruptResponseCRC: true})
	results := h.run(t)
	for _, result := range results {
		if result.Quality == signal.Good {
			t.Fatalf("corrupted response was accepted: %+v", result)
		}
	}
	stats := h.channel.Stats("pumphouse")
	if !strings.Contains(stats.LastError, "CRC") {
		t.Fatalf("corrupted response error = %q, want a CRC error", stats.LastError)
	}

	// A corrupted request must be rejected by the slave, which stays silent.
	h.server.SetFaults(RTUFaults{CorruptRequestCRC: true})
	crcErrors := h.server.Stats().CRCErrors
	results = h.run(t)
	if got := h.server.Stats().CRCErrors; got <= crcErrors {
		t.Fatalf("slave CRC errors = %d, want more than %d", got, crcErrors)
	}
	for _, result := range results {
		if result.Quality == signal.Good {
			t.Fatalf("a corrupted request produced a value: %+v", result)
		}
	}
	if err := h.channel.Stats("pumphouse").LastError; !strings.Contains(err, "EOF") {
		t.Fatalf("silent slave error = %q, want a timeout", err)
	}

	h.server.SetFaults(RTUFaults{})
	results = h.run(t)
	level, _ := readValue(t, results, "level")
	if level.Quality != signal.Good || level.Value.Num != 55 {
		t.Fatalf("recovery read = %+v, want level 55", level)
	}
}
