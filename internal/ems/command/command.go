// Package command runs device commands: idempotent submission, ordered
// writes, readback confirmation, timeout and "result unknown".
package command

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"fastiot/internal/ems/device"
	"fastiot/internal/ems/safety"
	"fastiot/internal/ems/signal"
)

// State is the lifecycle of one command request.
type State string

var ErrJournal = errors.New("command journal unavailable")

const (
	Accepted   State = "accepted"
	Executing  State = "executing"
	Confirmed  State = "confirmed"
	Rejected   State = "rejected"
	Failed     State = "failed"
	Unknown    State = "unknown"
	Superseded State = "superseded"
	// Submitted means the adapter accepted the write but there is no mapped
	// device readback to prove it. It is terminal and never presented as
	// confirmed device output.
	Submitted State = "submitted"
)

// Request is one submission. ID is supplied by the caller and deduplicates
// retries.
//
// Either a named command (CommandID + Parameters) or a single point write
// (Point + Value) is submitted. A point write may opt into SafeFallback: on
// timeout the manager writes the last confirmed value (or 0) once as a new,
// separately tracked request. That is not a retry of the original command.
type Request struct {
	ID           string
	DeviceID     string
	CommandID    string
	Actor        string
	Parameters   map[string]float64
	Point        string
	Value        signal.Value
	SafeFallback bool
	DispatchID   string
	Source       string
	Stage        string
	// FallbackOf is set by the manager when it creates a safety fallback
	// request after a timeout.
	FallbackOf string
	// NoReadback opts a point write out of device confirmation: the outcome
	// stops at Submitted. Use it only when no truthful readback is mapped.
	NoReadback bool
}

// Observed is one readback sample seen when a request confirmed.
type Observed struct {
	Point string
	Value signal.Value
	At    time.Time
}

// Outcome is the tracked result of one request.
type Outcome struct {
	RequestID    string
	DeviceID     string
	CommandID    string
	Actor        string
	State        State
	StartedAt    time.Time
	UpdatedAt    time.Time
	Deadline     time.Time
	Writes       []device.Write
	Readback     []device.Readback
	Reason       string
	DispatchID   string
	Source       string
	Stage        string
	FallbackOf   string
	SafeFallback bool
	Fingerprint  string
	Observed     []Observed
	// Restored marks an outcome replayed from a previous run: shown in
	// history, deduplicated by request id, but never re-raising alarms.
	Restored bool
	// External marks a request the caller dispatches and confirms itself, such
	// as a manual point write applied through the node store. The manager keeps
	// its one canonical outcome but writes nothing; Tick and Supersede never
	// advance it, the caller reports progress with SetState.
	External bool
}

// Dispatcher writes command values to a device. It must not retry internally.
// requestID is carried through so queued writes and logs can be correlated.
type Dispatcher interface {
	Write(deviceID, requestID string, writes []device.Write) error
}

// GuardRequest is the normalized command before it reaches a protocol writer.
type GuardRequest struct {
	DeviceID  string
	Point     string
	Writes    []device.Write
	Automatic bool
}

// Manager tracks commands until they confirm, fail or time out. A timeout is
// reported as Unknown: the device may or may not have executed the command.
type Manager struct {
	Mu       sync.Mutex
	Default  time.Duration
	Profiles func(deviceID string) (device.Profile, bool)
	Samples  func(deviceID string, now time.Time) map[string]signal.Sample
	MaxAge   time.Duration
	Writer   Dispatcher
	Guard    func(GuardRequest) safety.Verdict
	// Persist, when set, receives every stored state change. It is called
	// under the manager lock, so it must not call back into the manager.
	Persist func(Outcome) error
	// Lookup reads evicted identities from the same durable command journal.
	// Both callbacks run under Mu, including the pre-dispatch reservation.
	Lookup func(string) (Outcome, bool, error)
	orders map[string]Outcome
	order  []string
	// confirmed remembers the last confirmed point-write value per
	// device+point, used as the safe fallback value.
	confirmed map[string]float64
}

// New returns an empty manager with a default command timeout.
func New(writer Dispatcher, profiles func(string) (device.Profile, bool), samples func(string, time.Time) map[string]signal.Sample, maxAge, timeout time.Duration) *Manager {
	return &Manager{
		Default:   timeout,
		Profiles:  profiles,
		Samples:   samples,
		MaxAge:    maxAge,
		Writer:    writer,
		orders:    make(map[string]Outcome),
		confirmed: make(map[string]float64),
	}
}

// Submit accepts a command once per request id. A repeated id with identical
// normalized semantics returns the existing outcome without writing again; the
// same id with different device, command/point or parameters is a conflict.
func (m *Manager) Submit(now time.Time, request Request) (Outcome, error) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	return m.submitLocked(now, request)
}

// Existing returns a retry's canonical result without reevaluating current
// device state. Callers must still authorize the actor and target on every call.
func (m *Manager) Existing(request Request) (Outcome, bool, error) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	return m.existing(request)
}

func (m *Manager) existing(request Request) (Outcome, bool, error) {
	existing, found := m.orders[request.ID]
	if !found && m.Lookup != nil {
		var err error
		existing, found, err = m.Lookup(request.ID)
		if err != nil {
			return Outcome{}, false, fmt.Errorf("%w: %v", ErrJournal, err)
		}
	}
	if !found {
		return Outcome{}, false, nil
	}
	fingerprint, err := fingerprintOf(request)
	if err != nil {
		return Outcome{}, false, err
	}
	if !strings.HasPrefix(existing.Fingerprint, "v2:") {
		// Old outcomes stay immutable. Ambiguous legacy parameter encodings
		// fail closed instead of claiming that two different requests match.
		for name := range request.Parameters {
			if strings.ContainsAny(name, ";=") {
				return Outcome{}, false, errors.New("ambiguous legacy request parameters")
			}
		}
		fingerprint = legacyFingerprintOf(request)
	}
	if existing.DeviceID != request.DeviceID || existing.Actor != request.Actor || existing.Source != request.Source || existing.CommandID != request.CommandID || existing.Fingerprint != fingerprint {
		return Outcome{}, false, fmt.Errorf("request id %s already used with different actor, target or parameters", request.ID)
	}
	return existing, true, nil
}

func (m *Manager) submitLocked(now time.Time, request Request) (Outcome, error) {
	if request.ID == "" || request.DeviceID == "" || (request.CommandID == "" && request.Point == "") {
		return Outcome{}, errors.New("request id and device and a command or point are required")
	}
	if request.CommandID != "" && request.Point != "" {
		return Outcome{}, errors.New("a request is either a named command or a point write")
	}
	fingerprint, err := fingerprintOf(request)
	if err != nil {
		return Outcome{}, err
	}
	if existing, found, err := m.existing(request); found || err != nil {
		return existing, err
	}

	var writes []device.Write
	var readback []device.Readback
	timeout := m.Default
	if request.Point != "" {
		if !request.Value.Valid() {
			return Outcome{}, errors.New("point write value is not finite")
		}
		writes = []device.Write{{Point: request.Point, Value: request.Value}}
		readback = []device.Readback{{Point: request.Point, Op: "eq", Value: request.Value}}
	} else {
		profile, ok := m.Profiles(request.DeviceID)
		if !ok {
			return Outcome{}, fmt.Errorf("unknown device %s", request.DeviceID)
		}
		command, ok := profile.Command(request.CommandID)
		if !ok {
			return Outcome{}, fmt.Errorf("device %s has no command %s", request.DeviceID, request.CommandID)
		}
		if err := profile.Validate(); err != nil {
			return Outcome{}, err
		}
		if command.Timeout > 0 {
			timeout = command.Timeout
		}
		writes = append([]device.Write(nil), command.Writes...)
		readback = append([]device.Readback(nil), command.Readback...)
		for i := range readback {
			readback[i].Tolerance = command.ReadbackTolerance
		}
		allowed := make(map[string]bool, len(command.Parameters))
		for _, parameter := range command.Parameters {
			allowed[parameter.Name] = true
			raw, supplied := request.Parameters[parameter.Name]
			if !supplied {
				raw = parameter.Default
			}
			if parameter.HasRange && (raw < parameter.Min || raw > parameter.Max) {
				return Outcome{}, fmt.Errorf("parameter %s is outside [%g, %g]", parameter.Name, parameter.Min, parameter.Max)
			}
			if parameter.Binary && raw != 0 && raw != 1 {
				return Outcome{}, fmt.Errorf("parameter %s must be 0 or 1", parameter.Name)
			}
			if !signal.Num(raw).Valid() {
				return Outcome{}, fmt.Errorf("parameter %s is not finite", parameter.Name)
			}
			var value signal.Value
			if parameter.Kind == signal.Bool {
				if raw != 0 && raw != 1 {
					return Outcome{}, fmt.Errorf("parameter %s must be 0 or 1", parameter.Name)
				}
				value = signal.Flag(raw != 0)
			} else {
				value = signal.Num(raw)
			}
			writes = append(writes, device.Write{Point: parameter.Point, Value: value})
			hasReadback := false
			for _, condition := range readback {
				if condition.Point == parameter.Point {
					hasReadback = true
					break
				}
			}
			if !hasReadback {
				readback = append(readback, device.Readback{Point: parameter.Point, Op: "eq", Value: value, Tolerance: command.ReadbackTolerance})
			}
		}
		for name := range request.Parameters {
			if !allowed[name] {
				return Outcome{}, fmt.Errorf("unknown parameter %s", name)
			}
		}
	}

	outcome := Outcome{
		RequestID: request.ID, DeviceID: request.DeviceID, CommandID: request.CommandID,
		Actor: request.Actor, State: Accepted, StartedAt: now, UpdatedAt: now,
		Writes: writes, Readback: readback, Deadline: now.Add(timeout),
		DispatchID: request.DispatchID, Source: request.Source, Stage: request.Stage,
		FallbackOf: request.FallbackOf, Fingerprint: fingerprint, SafeFallback: request.SafeFallback,
	}
	if m.Guard != nil {
		for i := range writes {
			original := writes[i].Value
			verdict := m.Guard(GuardRequest{
				DeviceID: request.DeviceID, Point: writes[i].Point, Writes: []device.Write{writes[i]},
				Automatic: request.Source != "" && request.Source != "manual",
			}).Effective()
			if verdict.Decision == safety.DecisionRefused {
				outcome.State = Failed
				if request.CommandID != "" {
					outcome.State = Rejected
				}
				outcome.Reason = verdict.Reason
				if err := m.store(outcome); err != nil {
					return m.orders[request.ID], err
				}
				return outcome, nil
			}
			if verdict.Decision != safety.DecisionClamped {
				continue
			}
			writes[i].Value = signal.Num(verdict.Value)
			for j := range readback {
				if readback[j].Point == writes[i].Point && readback[j].Op == "eq" && readback[j].Value == original {
					readback[j].Value = writes[i].Value
				}
			}
		}
		outcome.Writes = writes
		outcome.Readback = readback
	}
	if m.Writer == nil {
		outcome.State = Failed
		outcome.Reason = "no device writer"
		m.store(outcome)
		return outcome, errors.New("no device writer")
	}
	// Reserve the identity durably before the adapter can enqueue physical I/O.
	if err := m.store(outcome); err != nil {
		return Outcome{}, fmt.Errorf("%w: %v", ErrJournal, err)
	}
	if err := m.Writer.Write(request.DeviceID, request.ID, writes); err != nil {
		outcome.State = Failed
		outcome.Reason = "write failed: " + err.Error()
		outcome.UpdatedAt = now
		if err := m.store(outcome); err != nil {
			return m.orders[request.ID], err
		}
		return outcome, nil
	}
	if request.NoReadback {
		outcome.State = Submitted
		outcome.Reason = "write accepted; no mapped device readback"
	}
	if err := m.store(outcome); err != nil {
		return m.orders[request.ID], err
	}
	return outcome, nil
}

// Track records a point write the caller dispatches and confirms itself. The
// manager keeps the one canonical outcome for the request but writes nothing
// and never advances it; the caller reports each state change with SetState.
// A repeated id with identical semantics returns the existing outcome; the
// same id with another device or value is a conflict.
func (m *Manager) Track(now time.Time, request Request) (Outcome, error) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	if request.ID == "" || request.DeviceID == "" || request.Point == "" || request.CommandID != "" {
		return Outcome{}, errors.New("tracked requests need an id, a device and one point")
	}
	if !request.Value.Valid() {
		return Outcome{}, errors.New("point write value is not finite")
	}
	fingerprint, err := fingerprintOf(request)
	if err != nil {
		return Outcome{}, err
	}
	if existing, found, err := m.existing(request); found || err != nil {
		return existing, err
	}
	outcome := Outcome{
		RequestID: request.ID, DeviceID: request.DeviceID,
		Actor: request.Actor, State: Accepted, StartedAt: now, UpdatedAt: now,
		Writes: []device.Write{{Point: request.Point, Value: request.Value}},
		Source: request.Source, Stage: request.Stage, DispatchID: request.DispatchID,
		Fingerprint: fingerprint, External: true,
	}
	if err := m.store(outcome); err != nil {
		return Outcome{}, err
	}
	return outcome, nil
}

// SetState advances an externally tracked request. Requests the manager
// dispatched itself are advanced by Tick and cannot be changed here.
func (m *Manager) SetState(now time.Time, requestID string, state State, reason string) (Outcome, bool) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	outcome, ok := m.orders[requestID]
	if !ok || !outcome.External {
		return Outcome{}, false
	}
	outcome.State = state
	outcome.Reason = reason
	outcome.UpdatedAt = now
	if err := m.store(outcome); err != nil {
		return m.orders[requestID], true
	}
	return outcome, true
}

// FailWrite marks an accepted request failed because the device refused the
// write. The outcome is terminal: later readback cannot confirm a command the
// device never accepted. Caller-owned (External) requests are left alone.
func (m *Manager) FailWrite(now time.Time, requestID, reason string) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	outcome, ok := m.orders[requestID]
	if !ok || outcome.External || (outcome.State != Accepted && outcome.State != Executing) {
		return
	}
	outcome.State = Failed
	outcome.Reason = reason
	outcome.UpdatedAt = now
	m.store(outcome)
}

// fingerprintOf renders the normalized semantics used for duplicate detection.
func fingerprintOf(request Request) (string, error) {
	// encoding/json sorts map keys and escapes names, unlike delimiter joining.
	encoded, err := json.Marshal(struct {
		Command                  string
		Parameters               map[string]float64 `json:",omitempty"`
		Point                    string
		Value                    signal.Value
		NoReadback, SafeFallback bool
	}{request.CommandID, request.Parameters, request.Point, request.Value, request.NoReadback, request.SafeFallback})
	if err != nil {
		return "", fmt.Errorf("invalid command identity: %w", err)
	}
	return "v2:" + string(encoded), nil
}

func legacyFingerprintOf(request Request) string {
	if request.Point != "" {
		return "point:" + request.Point + "=" + request.Value.String()
	}
	names := make([]string, 0, len(request.Parameters))
	for name := range request.Parameters {
		names = append(names, name)
	}
	sort.Strings(names)
	var builder strings.Builder
	builder.WriteString("command:" + request.CommandID)
	for _, name := range names {
		builder.WriteString(";")
		builder.WriteString(name)
		builder.WriteString("=")
		builder.WriteString(strconv.FormatFloat(request.Parameters[name], 'g', -1, 64))
	}
	return builder.String()
}

func confirmedKey(deviceID, point string) string { return deviceID + "\x00" + point }

// LastConfirmed returns the last confirmed value written to a device point.
func (m *Manager) LastConfirmed(deviceID, point string) (float64, bool) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	value, ok := m.confirmed[confirmedKey(deviceID, point)]
	return value, ok
}

// Supersede marks every nonterminal request of a device as Superseded, so a
// replaced automatic request cannot confirm or fall back later. It returns how
// many requests were superseded.
func (m *Manager) Supersede(deviceID string) int {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	count := 0
	now := time.Now()
	for _, id := range m.order {
		outcome := m.orders[id]
		if outcome.DeviceID != deviceID || (outcome.State != Accepted && outcome.State != Executing) || outcome.External {
			continue
		}
		outcome.State = Superseded
		outcome.Reason = "superseded by a newer request"
		outcome.UpdatedAt = now
		m.store(outcome)
		count++
	}
	return count
}

// maxOrders bounds the in-memory history; oldest terminal requests are dropped
// first so a 1-second dispatch loop cannot grow memory without bound.
const maxOrders = 2000

func (m *Manager) store(outcome Outcome) error {
	if m.Persist != nil {
		if err := m.Persist(outcome); err != nil {
			if previous, exists := m.orders[outcome.RequestID]; exists {
				previous.State, previous.Reason, previous.UpdatedAt = Unknown, "command_journal_unavailable", outcome.UpdatedAt
				m.orders[outcome.RequestID] = previous
			}
			return fmt.Errorf("%w: %v", ErrJournal, err)
		}
	}
	if _, exists := m.orders[outcome.RequestID]; !exists {
		m.order = append(m.order, outcome.RequestID)
	}
	m.orders[outcome.RequestID] = outcome
	if len(m.order) <= maxOrders {
		return nil
	}
	drop := len(m.order) - maxOrders
	kept := make([]string, 0, len(m.order))
	for _, id := range m.order {
		if drop > 0 {
			outcome := m.orders[id]
			switch outcome.State {
			case Confirmed, Submitted, Superseded:
				delete(m.orders, id)
				drop--
				continue
			case Accepted, Executing:
				// A caller-advanced request that never reached a terminal state
				// (for example a variable write) must not pin memory forever.
				if outcome.External {
					delete(m.orders, id)
					drop--
					continue
				}
			}
		}
		kept = append(kept, id)
	}
	m.order = kept
	return nil
}

// Preload seeds the manager with outcomes from a previous run. Anything not
// yet terminal becomes Unknown: after a restart nobody can confirm it.
func (m *Manager) Preload(outcomes []Outcome) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	for _, outcome := range outcomes {
		if outcome.RequestID == "" {
			continue
		}
		if _, exists := m.orders[outcome.RequestID]; exists {
			continue
		}
		if outcome.State == Accepted || outcome.State == Executing {
			outcome.State = Unknown
			outcome.Reason = "进程重启，结果未知"
		}
		outcome.Restored = true
		m.order = append(m.order, outcome.RequestID)
		m.orders[outcome.RequestID] = outcome
	}
}

// SetProfiles replaces the profile resolver used by submissions and ticks.
func (m *Manager) SetProfiles(profiles func(deviceID string) (device.Profile, bool)) {
	m.Mu.Lock()
	m.Profiles = profiles
	m.Mu.Unlock()
}

// Tick advances executing commands: readback confirms, timeout becomes unknown.
// A point write that opted into SafeFallback then gets one separate fallback
// request to the last confirmed value (or 0). The fallback never falls back
// again.
func (m *Manager) Tick(now time.Time) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	type pendingFallback struct {
		request Request
	}
	fallbacks := []pendingFallback{}
	for _, id := range m.order {
		outcome := m.orders[id]
		if outcome.State != Accepted && outcome.State != Executing {
			continue
		}
		if outcome.External {
			continue
		}
		deadline := outcome.Deadline
		if deadline.IsZero() {
			deadline = outcome.StartedAt.Add(m.Default)
		}
		// Judge the observation and arrival, not the scheduling of this Tick.
		// A sample first received after the deadline cannot retroactively confirm.
		samples := make(map[string]signal.Sample)
		for point, sample := range m.Samples(outcome.DeviceID, now) {
			// Keep monotonic timeout checks, and independently require the
			// serialized observation/arrival evidence to fit the wall-clock
			// window. A host clock step must not yield contradictory proof.
			observed, received := sample.SourceTime.Round(0), sample.ReceivedTime.Round(0)
			monotonicWindow := sample.SourceTime.After(outcome.StartedAt) && sample.ReceivedTime.After(outcome.StartedAt) && !sample.SourceTime.After(deadline) && !sample.ReceivedTime.After(deadline) && !sample.SourceTime.After(now) && !sample.ReceivedTime.After(now)
			wallWindow := observed.After(outcome.StartedAt.Round(0)) && received.After(outcome.StartedAt.Round(0)) && !observed.After(deadline.Round(0)) && !received.After(deadline.Round(0)) && !observed.After(now.Round(0)) && !received.After(now.Round(0)) && !received.Before(observed)
			if monotonicWindow != wallWindow && outcome.Reason != "clock_basis_inconsistent" {
				outcome.Reason = "clock_basis_inconsistent"
				m.store(outcome)
			}
			if !monotonicWindow || !wallWindow {
				continue
			}
			samples[point] = sample
		}
		if confirmed, reason := device.CheckReadbackSince(outcome.Readback, samples, now, m.MaxAge, outcome.StartedAt); confirmed {
			outcome.State = Confirmed
			outcome.Reason = ""
			outcome.UpdatedAt = now
			outcome.Observed = observe(outcome.Readback, samples)
			if outcome.CommandID == "" && len(outcome.Writes) == 1 {
				m.confirmed[confirmedKey(outcome.DeviceID, outcome.Writes[0].Point)] = outcome.Writes[0].Value.Num
			}
			m.store(outcome)
			continue
		} else if !now.Before(deadline) {
			outcome.State = Unknown
			if outcome.Reason != "clock_basis_inconsistent" {
				outcome.Reason = "no readback before timeout: " + reason
			}
			outcome.UpdatedAt = now
			m.store(outcome)
			if outcome.CommandID == "" && outcome.SafeFallback && outcome.FallbackOf == "" && len(outcome.Writes) == 1 {
				point := outcome.Writes[0].Point
				value, ok := m.confirmed[confirmedKey(outcome.DeviceID, point)]
				if !ok {
					value = 0
				}
				fallbacks = append(fallbacks, pendingFallback{request: Request{
					ID: outcome.RequestID + "-fallback", DeviceID: outcome.DeviceID,
					Point: point, Value: signal.Num(value),
					Actor: outcome.Actor, DispatchID: outcome.DispatchID,
					Source: outcome.Source, Stage: "fallback", FallbackOf: outcome.RequestID,
				}})
			}
		}
	}
	for _, pending := range fallbacks {
		_, _ = m.submitLocked(now, pending.request)
	}
}

// observe captures the actual samples that satisfied a readback set.
func observe(readback []device.Readback, samples map[string]signal.Sample) []Observed {
	observed := make([]Observed, 0, len(readback))
	for _, condition := range readback {
		sample, found := samples[condition.Point]
		if !found {
			continue
		}
		at := sample.SourceTime
		if at.IsZero() {
			at = sample.ReceivedTime
		}
		observed = append(observed, Observed{Point: condition.Point, Value: sample.Value, At: at})
	}
	return observed
}

// Get returns a tracked outcome.
func (m *Manager) Get(id string) (Outcome, bool) {
	outcome, found, _ := m.Find(id)
	return outcome, found
}

// Find also resolves a cold request from durable history and surfaces I/O errors.
func (m *Manager) Find(id string) (Outcome, bool, error) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	outcome, ok := m.orders[id]
	if !ok && m.Lookup != nil {
		return m.Lookup(id)
	}
	return outcome, ok, nil
}

// List returns outcomes in submission order.
func (m *Manager) List() []Outcome {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	outcomes := make([]Outcome, 0, len(m.order))
	for _, id := range m.order {
		outcomes = append(outcomes, m.orders[id])
	}
	return outcomes
}
