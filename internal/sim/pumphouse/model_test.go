package pumphouse

import (
	"errors"
	"testing"
	"time"

	"fastiot/internal/ems/channel"
)

// clock is a manually advanced time source so process delays are tested
// without sleeping.
type clock struct{ now time.Time }

func newClock() *clock                   { return &clock{now: time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC)} }
func (c *clock) Now() time.Time          { return c.now }
func (c *clock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func newTestPlant(t *testing.T) (*Plant, *clock) {
	t.Helper()
	c := newClock()
	cfg := DefaultConfig()
	cfg.StartDelay = 2 * time.Second
	cfg.FeedbackDelay = 500 * time.Millisecond
	cfg.StopDelay = time.Second
	cfg.Now = c.Now
	return New(cfg), c
}

func writeCommand(t *testing.T, p *Plant, address uint16, value uint16) error {
	t.Helper()
	return p.Bus().WriteHoldingRegister(StationID, address, value)
}

func pumpState(t *testing.T, p *Plant, pump int) PumpState {
	t.Helper()
	return p.Snapshot().Pumps[pump-1]
}

func TestStartProcessDelaysRunFeedback(t *testing.T) {
	p, c := newTestPlant(t)

	if err := writeCommand(t, p, AddressPump1Command, 1); err != nil {
		t.Fatalf("start command: %v", err)
	}
	if pumpState(t, p, 1).Feedback {
		t.Fatal("run feedback must not appear immediately after the command")
	}
	c.Advance(1900 * time.Millisecond)
	if pumpState(t, p, 1).Feedback {
		t.Fatal("run feedback appeared before the start process finished")
	}
	c.Advance(200 * time.Millisecond) // t+2.1s: motor runs, feedback delay still pending
	if !pumpState(t, p, 1).Running {
		t.Fatal("pump should be running after the start delay")
	}
	if pumpState(t, p, 1).Feedback {
		t.Fatal("run feedback must lag the motor start")
	}
	runningLevel := p.Snapshot().Level
	c.Advance(500 * time.Millisecond) // t+2.6s
	pump := pumpState(t, p, 1)
	if !pump.Feedback {
		t.Fatal("run feedback should be on after the feedback delay")
	}
	if pump.Current <= 0 {
		t.Fatalf("running pump current = %v, want > 0", pump.Current)
	}
	if p.Snapshot().Level <= runningLevel {
		t.Fatalf("level = %v, want above %v while the pump runs", p.Snapshot().Level, runningLevel)
	}
}

func TestStopProcessDelaysFeedback(t *testing.T) {
	p, c := newTestPlant(t)
	if err := writeCommand(t, p, AddressPump1Command, 1); err != nil {
		t.Fatal(err)
	}
	c.Advance(3 * time.Second)
	if !pumpState(t, p, 1).Feedback {
		t.Fatal("precondition: pump should be running")
	}
	if err := writeCommand(t, p, AddressPump1Command, 0); err != nil {
		t.Fatal(err)
	}
	c.Advance(900 * time.Millisecond)
	if !pumpState(t, p, 1).Feedback {
		t.Fatal("run feedback must stay on during the stop process")
	}
	c.Advance(200 * time.Millisecond)
	if pumpState(t, p, 1).Feedback {
		t.Fatal("run feedback should be off after the stop delay")
	}
	if pumpState(t, p, 1).Current != 0 {
		t.Fatal("stopped pump current should be zero")
	}
}

func TestFaultRefusesStartAndTripsRunningPump(t *testing.T) {
	p, c := newTestPlant(t)
	if err := writeCommand(t, p, AddressPump1Command, 1); err != nil {
		t.Fatal(err)
	}
	c.Advance(3 * time.Second)
	p.SetFault(1, true)
	if pumpState(t, p, 1).Feedback {
		t.Fatal("a fault must trip a running pump immediately")
	}
	err := writeCommand(t, p, AddressPump1Command, 1)
	if err == nil {
		t.Fatal("start command on a faulted pump must be refused")
	}
	var refusal channel.Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("error = %v, want channel.Refusal", err)
	}
	c.Advance(5 * time.Second)
	if pumpState(t, p, 1).Feedback || pumpState(t, p, 1).Running {
		t.Fatal("a refused start must not make the pump run")
	}
	// Clearing the fault does not restart the pump by itself.
	p.SetFault(1, false)
	c.Advance(5 * time.Second)
	if pumpState(t, p, 1).Feedback {
		t.Fatal("clearing a fault must not start the pump")
	}
}

func TestLocalModeAcceptsCommandButDoesNotRun(t *testing.T) {
	p, c := newTestPlant(t)
	p.SetLocal(1, true)
	if err := writeCommand(t, p, AddressPump1Command, 1); err != nil {
		t.Fatalf("local mode should accept the write, got %v", err)
	}
	c.Advance(5 * time.Second)
	if pumpState(t, p, 1).Running || pumpState(t, p, 1).Feedback {
		t.Fatal("a pump in local mode must not act on a remote start")
	}
	if pumpState(t, p, 1).Local != true {
		t.Fatal("local/remote status should read back as local")
	}
}

func TestSnapshotReportsModesAndLevelDynamics(t *testing.T) {
	p, c := newTestPlant(t)
	if !pumpState(t, p, 2).Remote {
		t.Fatal("pumps start in remote mode")
	}
	level := p.Snapshot().Level
	c.Advance(2 * time.Second) // no pump running: tank drains
	if p.Snapshot().Level >= level {
		t.Fatalf("level = %v, want below %v while no pump runs", p.Snapshot().Level, level)
	}
	if p.Snapshot().Pressure <= 0 {
		t.Fatal("static pressure should be reported")
	}
	if err := writeCommand(t, p, AddressPump2Command, 1); err != nil {
		t.Fatal(err)
	}
	c.Advance(3 * time.Second)
	if got := pumpState(t, p, 2).Starts; got != 1 {
		t.Fatalf("start count = %d, want 1", got)
	}
}

func TestBusReadsConfiguredAreas(t *testing.T) {
	p, _ := newTestPlant(t)
	bus := p.Bus()

	regs, err := bus.ReadHoldingRegisters(StationID, AddressLevel, 6)
	if err != nil {
		t.Fatalf("read holding registers: %v", err)
	}
	if len(regs) != 6 {
		t.Fatalf("register count = %d, want 6", len(regs))
	}
	bits, err := bus.ReadDiscreteInputs(StationID, BitPump2Run, 3)
	if err != nil {
		t.Fatalf("read discrete inputs: %v", err)
	}
	if bits[1] != true {
		t.Fatal("remote mode bit should read true")
	}
	if _, err := bus.ReadHoldingRegisters(StationID+1, AddressLevel, 1); err == nil {
		t.Fatal("unknown station must fail")
	}
	if _, err := bus.ReadHoldingRegisters(StationID, 900, 1); err == nil {
		t.Fatal("unmapped register must fail")
	}
}
