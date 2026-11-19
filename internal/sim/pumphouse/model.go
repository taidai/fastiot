// Package pumphouse simulates one two-pump tank station over Modbus. It is a
// sample device, not a vendor driver: every value comes from a small process
// model so start/stop takes time and a faulted pump refuses to move.
package pumphouse

import (
	"errors"
	"math"
	"sync"
	"time"
)

// StationID is the Modbus slave address of the simulated controller.
const StationID byte = 1

// Holding-register map. Telemetry is read-only in the sample point table;
// 104/105 are the writable per-pump start/stop command words.
const (
	AddressLevel        uint16 = 100 // 0.1 %
	AddressPressure     uint16 = 101 // 0.01 MPa
	AddressPump1Current uint16 = 102 // 0.1 A
	AddressPump2Current uint16 = 103 // 0.1 A
	AddressPump1Command uint16 = 104 // 1 = start, 0 = stop
	AddressPump2Command uint16 = 105
	AddressPump1Starts  uint16 = 106
	AddressPump2Starts  uint16 = 107
)

// Discrete-input map: run feedback, local/remote mode (1 = remote) and fault.
const (
	BitPump1Run    uint16 = 0
	BitPump1Remote uint16 = 1
	BitPump1Fault  uint16 = 2
	BitPump2Run    uint16 = 8
	BitPump2Remote uint16 = 9
	BitPump2Fault  uint16 = 10
)

// Model errors. The bus and the TCP server map them to a refused write or a
// Modbus exception; they are sentinels so the mapping cannot drift.
var (
	ErrRefused        = errors.New("pump protection refused the command")
	ErrIllegalAddress = errors.New("illegal data address")
	ErrIllegalValue   = errors.New("illegal data value")
)

// Config describes the simulated process. Durations model the motor start,
// the feedback contact lagging the motor, and the run-down on stop.
type Config struct {
	Station               byte
	StartDelay            time.Duration
	FeedbackDelay         time.Duration
	StopDelay             time.Duration
	DrainPercentPerSecond float64
	PumpPercentPerSecond  float64
	InitialLevel          float64
	PressureBase          float64
	PressurePerPump       float64
	CurrentAmps           float64
	Now                   func() time.Time
}

// DefaultConfig is the sample's documented behaviour: a pump needs about 2 s
// to start, its run contact follows 0.8 s later, and stopping takes 1.5 s.
func DefaultConfig() Config {
	return Config{
		Station:               StationID,
		StartDelay:            2 * time.Second,
		FeedbackDelay:         800 * time.Millisecond,
		StopDelay:             1500 * time.Millisecond,
		DrainPercentPerSecond: 0.15,
		PumpPercentPerSecond:  0.6,
		InitialLevel:          55,
		PressureBase:          0.08,
		PressurePerPump:       0.22,
		CurrentAmps:           11.5,
		Now:                   time.Now,
	}
}

func (c Config) withDefaults() Config {
	defaults := DefaultConfig()
	if c.Station == 0 {
		c.Station = defaults.Station
	}
	if c.StartDelay < 0 {
		c.StartDelay = 0
	}
	if c.FeedbackDelay < 0 {
		c.FeedbackDelay = 0
	}
	if c.StopDelay < 0 {
		c.StopDelay = 0
	}
	if c.DrainPercentPerSecond <= 0 {
		c.DrainPercentPerSecond = defaults.DrainPercentPerSecond
	}
	if c.PumpPercentPerSecond <= 0 {
		c.PumpPercentPerSecond = defaults.PumpPercentPerSecond
	}
	if c.InitialLevel <= 0 {
		c.InitialLevel = defaults.InitialLevel
	}
	if c.PressureBase <= 0 {
		c.PressureBase = defaults.PressureBase
	}
	if c.PressurePerPump <= 0 {
		c.PressurePerPump = defaults.PressurePerPump
	}
	if c.CurrentAmps <= 0 {
		c.CurrentAmps = defaults.CurrentAmps
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}

type pump struct {
	fault   bool
	local   bool
	command uint16

	running  bool
	feedback bool
	starts   int

	pendingStart bool
	startAt      time.Time
	feedbackAt   time.Time
	pendingStop  bool
	stopAt       time.Time
}

// PumpState is one pump as a human or test reads it.
type PumpState struct {
	Running  bool
	Feedback bool
	Remote   bool
	Local    bool
	Fault    bool
	Current  float64
	Starts   int
}

// Snapshot is the current plant state.
type Snapshot struct {
	Level    float64
	Pressure float64
	Pumps    [2]PumpState
}

// Plant is the simulated process. All access is serialized; the Modbus bus
// and TCP server share one model.
type Plant struct {
	cfg      Config
	now      func() time.Time
	mu       sync.Mutex
	last     time.Time
	level    float64
	pressure float64
	pumps    [2]*pump
}

// New builds a plant and advances it once to establish the starting level.
func New(cfg Config) *Plant {
	cfg = cfg.withDefaults()
	plant := &Plant{cfg: cfg, now: cfg.Now, level: cfg.InitialLevel, pressure: cfg.PressureBase}
	plant.last = cfg.Now()
	plant.pumps[0] = &pump{}
	plant.pumps[1] = &pump{}
	return plant
}

// Station is the configured slave address.
func (p *Plant) Station() byte { return p.cfg.Station }

// SetFault sets or clears the protection fault of one pump (1 or 2). A fault
// trips a running pump immediately; clearing it never restarts the pump.
func (p *Plant) SetFault(pump int, active bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.advanceLocked(p.now())
	target := p.pumpLocked(pump)
	target.fault = active
	if active && target.running {
		target.running = false
		target.feedback = false
		target.pendingStart = false
	}
	if active {
		target.pendingStart = false
	}
}

// SetLocal switches one pump between local and remote control. Local mode
// still accepts protocol writes but ignores them, like a field switch.
func (p *Plant) SetLocal(pump int, active bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.advanceLocked(p.now())
	p.pumpLocked(pump).local = active
}

// Snapshot returns the current process state, advancing it to now.
func (p *Plant) Snapshot() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.advanceLocked(p.now())
	snapshot := Snapshot{Level: round1(p.level), Pressure: round2(p.pressure)}
	for i, pump := range p.pumps {
		snapshot.Pumps[i] = PumpState{
			Running: pump.running, Feedback: pump.feedback,
			Remote: !pump.local, Local: pump.local, Fault: pump.fault,
			Starts: pump.starts,
		}
		if pump.running {
			snapshot.Pumps[i].Current = p.cfg.CurrentAmps
		}
	}
	return snapshot
}

func (p *Plant) pumpLocked(index int) *pump {
	if index < 1 || index > 2 {
		panic("pumphouse: pump index must be 1 or 2")
	}
	return p.pumps[index-1]
}

// advanceLocked applies every pending process transition up to now, then
// integrates level and pressure over the elapsed time.
func (p *Plant) advanceLocked(now time.Time) {
	elapsed := now.Sub(p.last)
	if elapsed < 0 {
		elapsed = 0
	}
	p.last = now
	running := 0
	for _, pump := range p.pumps {
		if pump.fault && pump.running {
			pump.running = false
			pump.feedback = false
			pump.pendingStart = false
		}
		if pump.pendingStart && !pump.startAt.After(now) {
			pump.pendingStart = false
			pump.running = true
			pump.feedbackAt = pump.startAt.Add(p.cfg.FeedbackDelay)
		}
		if pump.running && !pump.feedback && !pump.feedbackAt.IsZero() && !pump.feedbackAt.After(now) {
			pump.feedback = true
		}
		if pump.pendingStop && !pump.stopAt.After(now) {
			pump.pendingStop = false
			pump.running = false
			pump.feedback = false
			pump.feedbackAt = time.Time{}
		}
		if pump.running {
			running++
		}
	}
	seconds := elapsed.Seconds()
	p.level += (float64(running)*p.cfg.PumpPercentPerSecond - p.cfg.DrainPercentPerSecond) * seconds
	if p.level < 0 {
		p.level = 0
	}
	if p.level > 100 {
		p.level = 100
	}
	target := p.cfg.PressureBase + float64(running)*p.cfg.PressurePerPump
	follow := seconds / 2
	if follow > 1 {
		follow = 1
	}
	p.pressure += (target - p.pressure) * follow
}

func (p *Plant) holdingLocked(address uint16) (uint16, error) {
	switch address {
	case AddressLevel:
		return uint16(round1(p.level) * 10), nil
	case AddressPressure:
		return uint16(round2(p.pressure) * 100), nil
	case AddressPump1Current:
		return p.currentLocked(p.pumps[0]), nil
	case AddressPump2Current:
		return p.currentLocked(p.pumps[1]), nil
	case AddressPump1Command:
		return p.pumps[0].command, nil
	case AddressPump2Command:
		return p.pumps[1].command, nil
	case AddressPump1Starts:
		return uint16(minInt(p.pumps[0].starts, 65535)), nil
	case AddressPump2Starts:
		return uint16(minInt(p.pumps[1].starts, 65535)), nil
	}
	return 0, ErrIllegalAddress
}

func (p *Plant) currentLocked(pump *pump) uint16 {
	if !pump.running {
		return 0
	}
	return uint16(p.cfg.CurrentAmps * 10)
}

func (p *Plant) bitLocked(address uint16) (bool, error) {
	switch address {
	case BitPump1Run:
		return p.pumps[0].feedback, nil
	case BitPump1Remote:
		return !p.pumps[0].local, nil
	case BitPump1Fault:
		return p.pumps[0].fault, nil
	case BitPump2Run:
		return p.pumps[1].feedback, nil
	case BitPump2Remote:
		return !p.pumps[1].local, nil
	case BitPump2Fault:
		return p.pumps[1].fault, nil
	}
	return false, ErrIllegalAddress
}

// writeHoldingLocked applies one command word. A start on a faulted pump is
// refused, so the protocol exchange itself reports the rejection.
func (p *Plant) writeHoldingLocked(address, value uint16) error {
	index := 0
	switch address {
	case AddressPump1Command:
		index = 1
	case AddressPump2Command:
		index = 2
	default:
		return ErrIllegalAddress
	}
	if value > 1 {
		return ErrIllegalValue
	}
	pump := p.pumpLocked(index)
	pump.command = value
	if value == 1 && pump.fault {
		return ErrRefused
	}
	if pump.local {
		return nil
	}
	now := p.now()
	if value == 1 {
		if pump.running || pump.pendingStart {
			return nil
		}
		pump.pendingStart = true
		pump.startAt = now.Add(p.cfg.StartDelay)
		pump.pendingStop = false
		pump.starts++
		return nil
	}
	// Stop (or abort a start that has not completed yet).
	pump.pendingStart = false
	if pump.running {
		pump.pendingStop = true
		pump.stopAt = now.Add(p.cfg.StopDelay)
	}
	return nil
}

func round1(value float64) float64 { return math.Round(value*10) / 10 }
func round2(value float64) float64 { return math.Round(value*100) / 100 }

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
