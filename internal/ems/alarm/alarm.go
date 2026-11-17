// Package alarm tracks alarm lifecycles: raise, deduplicate, acknowledge,
// resolve and close, with history-friendly timestamps.
package alarm

import (
	"crypto/rand"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Severity is the operational weight of an alarm.
type Severity string

const (
	Info     Severity = "info"
	Warning  Severity = "warning"
	Critical Severity = "critical"
)

// State is the lifecycle state. A resolved alarm stays visible until closed.
type State string

const (
	Active       State = "active"
	Acknowledged State = "acknowledged"
	Resolved     State = "resolved"
	Closed       State = "closed"
)

// Alarm is one deduplicated issue on one object.
type Alarm struct {
	ID                string    `json:"id"`
	Key               string    `json:"key"`
	Severity          Severity  `json:"severity"`
	Title             string    `json:"title"`
	Detail            string    `json:"detail"`
	DeviceID          string    `json:"device_id,omitempty"`
	PointID           string    `json:"point_id,omitempty"`
	RootCause         string    `json:"root_cause,omitempty"`
	GroupID           string    `json:"group_id,omitempty"`
	Active            bool      `json:"active"`
	State             State     `json:"state"`
	InputState        string    `json:"input_state,omitempty"`
	InputReason       string    `json:"input_reason,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	AckedAt           time.Time `json:"acked_at,omitzero"`
	ResolvedAt        time.Time `json:"resolved_at,omitzero"`
	ClosedAt          time.Time `json:"closed_at,omitzero"`
	Revision          uint64    `json:"revision"`
	Count             int       `json:"count,omitempty"`
	LastRaisedAt      time.Time `json:"last_raised_at,omitempty"`
	AckedBy           string    `json:"acked_by,omitempty"`
	AckNote           string    `json:"ack_note,omitempty"`
	MaintenanceUntil  time.Time `json:"maintenance_until,omitempty"`
	MaintenanceReason string    `json:"maintenance_reason,omitempty"`
	MaintainedBy      string    `json:"maintained_by,omitempty"`
}

// Engine holds alarms by key. Safe for concurrent use.
type Maintenance struct {
	Until  time.Time `json:"until"`
	Reason string    `json:"reason"`
	By     string    `json:"by"`
}

type Engine struct {
	OnChange    func(Alarm, string)
	mu          sync.Mutex
	alarms      map[string]*Alarm
	current     map[string]string
	maintenance map[string]Maintenance
	roots       map[string]string
	order       []string
}

// New returns an empty alarm engine.
func New() *Engine {
	return &Engine{
		alarms:      make(map[string]*Alarm),
		current:     make(map[string]string),
		maintenance: make(map[string]Maintenance),
		roots:       make(map[string]string),
	}
}

// Raise updates a continuous condition or creates a new occurrence after recovery.
func (e *Engine) Raise(now time.Time, key string, severity Severity, title, detail, deviceID, pointID string) (Alarm, error) {
	if key == "" {
		return Alarm{}, errors.New("alarm key is empty")
	}
	if severity != Info && severity != Warning && severity != Critical {
		return Alarm{}, fmt.Errorf("invalid severity %q", severity)
	}
	rootCause := pointID
	if rootCause == "" {
		rootCause = deviceID
	}
	var notify Alarm
	var transition string
	notifyChange := false
	defer func() {
		if notifyChange && e.OnChange != nil {
			e.OnChange(notify, transition)
		}
	}()
	e.mu.Lock()
	defer e.mu.Unlock()
	existing, found := e.alarms[e.current[key]]
	if e.maintained(now, key) {
		if found {
			return *existing, nil
		}
		return Alarm{Key: key}, nil
	}
	if !found || !existing.Active {
		created := &Alarm{
			ID: "alarm-" + rand.Text(), Key: key, Severity: severity, Title: title, Detail: detail,
			DeviceID: deviceID, PointID: pointID, RootCause: rootCause,
			Active: true, State: Active, InputState: "abnormal", Count: 1, LastRaisedAt: now,
			CreatedAt: now, UpdatedAt: now, Revision: 1,
		}
		e.alarms[created.ID] = created
		e.current[key] = created.ID
		e.order = append(e.order, created.ID)
		if rootCause != "" {
			if primary, grouped := e.roots[rootCause]; grouped && e.alarms[primary].Active {
				created.GroupID = primary
			} else {
				e.roots[rootCause] = created.ID
			}
		}
		notify, transition, notifyChange = *created, "raised", true
		return *created, nil
	}
	changed := existing.Severity != severity || existing.Title != title || existing.Detail != detail || existing.InputState != "abnormal"
	existing.InputState, existing.InputReason = "abnormal", ""
	if changed || now.After(existing.LastRaisedAt) {
		existing.Revision++
	}
	if now.After(existing.LastRaisedAt) {
		existing.LastRaisedAt = now
		existing.UpdatedAt = now
		existing.Count++
	}
	if changed {
		existing.Severity = severity
		existing.Title = title
		existing.Detail = detail
		existing.UpdatedAt = now
		notify, transition, notifyChange = *existing, "changed", true
	}
	return *existing, nil
}

// maintained reports whether the key is inside a maintenance window.
func (e *Engine) maintained(now time.Time, key string) bool {
	window, ok := e.maintenance[key]
	return ok && window.Until.After(now)
}

// ExportMaintenance returns a copy of all active maintenance windows.
func (e *Engine) ExportMaintenance() map[string]Maintenance {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[string]Maintenance, len(e.maintenance))
	for key, window := range e.maintenance {
		out[key] = window
	}
	return out
}

// ImportMaintenance restores persisted maintenance windows.
func (e *Engine) ImportMaintenance(windows map[string]Maintenance) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for key, window := range windows {
		if window.Until.After(time.Now()) {
			e.maintenance[key] = window
		}
	}
}

// Shelve suppresses one known alarm until the given time. A maintained alarm
// remains listed but is not re-raised or newly activated while the window lasts.
func (e *Engine) Shelve(now time.Time, key, reason, user string, until time.Time) (Alarm, error) {
	if key == "" {
		return Alarm{}, errors.New("alarm key is empty")
	}
	if !until.After(now) {
		return Alarm{}, errors.New("maintenance end must be in the future")
	}
	if strings.TrimSpace(reason) == "" {
		return Alarm{}, errors.New("maintenance reason is required")
	}
	var notify Alarm
	notifyChange := false
	defer func() {
		if notifyChange && e.OnChange != nil {
			e.OnChange(notify, "maintenance")
		}
	}()
	e.mu.Lock()
	defer e.mu.Unlock()
	alarm, found := e.alarms[key]
	if !found || e.current[alarm.Key] != key {
		return Alarm{}, errors.New("unknown or superseded alarm occurrence")
	}
	e.maintenance[alarm.Key] = Maintenance{Until: until, Reason: strings.TrimSpace(reason), By: user}
	alarm.MaintenanceUntil = until
	alarm.MaintenanceReason = strings.TrimSpace(reason)
	alarm.MaintainedBy = user
	alarm.UpdatedAt = now
	alarm.Revision++
	notify, notifyChange = *alarm, true
	return *alarm, nil
}

// Release ends a maintenance window.
func (e *Engine) Release(now time.Time, key string) (Alarm, error) {
	var notify Alarm
	notifyChange := false
	defer func() {
		if notifyChange && e.OnChange != nil {
			e.OnChange(notify, "released")
		}
	}()
	e.mu.Lock()
	defer e.mu.Unlock()
	alarm, found := e.alarms[key]
	if !found || e.current[alarm.Key] != key {
		return Alarm{}, errors.New("unknown or superseded alarm occurrence")
	}
	delete(e.maintenance, alarm.Key)
	alarm.MaintenanceUntil = time.Time{}
	alarm.MaintenanceReason = ""
	alarm.MaintainedBy = ""
	alarm.UpdatedAt = now
	alarm.Revision++
	notify, notifyChange = *alarm, true
	return *alarm, nil
}

func (e *Engine) Retire(now time.Time, key, reason string) {
	var notify Alarm
	changed := false
	defer func() {
		if changed && e.OnChange != nil {
			e.OnChange(notify, "retired")
		}
	}()
	e.mu.Lock()
	defer e.mu.Unlock()
	current := e.alarms[e.current[key]]
	if current == nil || current.State == Closed {
		return
	}
	current.Active, current.State = false, Closed
	current.InputState, current.InputReason = "unknown", reason
	current.ClosedAt, current.UpdatedAt = now, now
	current.Revision++
	notify, changed = *current, true
}

func (e *Engine) MarkUnknown(now time.Time, key, reason string) {
	var notify Alarm
	changed := false
	defer func() {
		if changed && e.OnChange != nil {
			e.OnChange(notify, "input_unknown")
		}
	}()
	e.mu.Lock()
	defer e.mu.Unlock()
	current := e.alarms[e.current[key]]
	if current == nil || !current.Active || (current.InputState == "unknown" && current.InputReason == reason) {
		return
	}
	current.InputState, current.InputReason = "unknown", reason
	current.UpdatedAt = now
	current.Revision++
	notify, changed = *current, true
}

// Clear marks an alarm as resolved. It stays listed until closed.
func (e *Engine) Clear(now time.Time, key string) (Alarm, error) {
	var notify Alarm
	notifyChange := false
	defer func() {
		if notifyChange && e.OnChange != nil {
			e.OnChange(notify, "resolved")
		}
	}()
	e.mu.Lock()
	defer e.mu.Unlock()
	alarm, found := e.alarms[e.current[key]]
	if !found {
		return Alarm{}, fmt.Errorf("unknown alarm %s", key)
	}
	if !alarm.Active {
		return *alarm, nil
	}
	alarm.Active = false
	alarm.State = Resolved
	alarm.InputState, alarm.InputReason = "normal", ""
	alarm.ResolvedAt = now
	alarm.UpdatedAt = now
	alarm.Revision++
	notify, notifyChange = *alarm, true
	return *alarm, nil
}

// Acknowledge records who accepted an active alarm. It does not clear it.
func (e *Engine) Acknowledge(now time.Time, key, user string) (Alarm, error) {
	return e.AcknowledgeWithNote(now, key, user, "")
}

// AcknowledgeWithNote records the operator and an optional handling note.
func (e *Engine) AcknowledgeWithNote(now time.Time, key, user, note string) (Alarm, error) {
	var notify Alarm
	notifyChange := false
	defer func() {
		if notifyChange && e.OnChange != nil {
			e.OnChange(notify, "acknowledged")
		}
	}()
	e.mu.Lock()
	defer e.mu.Unlock()
	alarm, found := e.alarms[key]
	if !found {
		return Alarm{}, fmt.Errorf("unknown alarm %s", key)
	}
	if user == "" {
		return Alarm{}, errors.New("acknowledging user is required")
	}
	if !alarm.Active || alarm.State == Acknowledged {
		return Alarm{}, fmt.Errorf("alarm %s is not active", key)
	}
	alarm.State = Acknowledged
	alarm.AckedAt = now
	alarm.AckedBy = user
	alarm.AckNote = strings.TrimSpace(note)
	alarm.UpdatedAt = now
	alarm.Revision++
	notify, notifyChange = *alarm, true
	return *alarm, nil
}

// Close removes a resolved alarm from the active list. An active alarm must be
// cleared first; closing is not a way to hide a live condition.
func (e *Engine) Close(now time.Time, key string) (Alarm, error) {
	var notify Alarm
	notifyChange := false
	defer func() {
		if notifyChange && e.OnChange != nil {
			e.OnChange(notify, "closed")
		}
	}()
	e.mu.Lock()
	defer e.mu.Unlock()
	alarm, found := e.alarms[key]
	if !found {
		return Alarm{}, fmt.Errorf("unknown alarm %s", key)
	}
	if alarm.Active {
		return Alarm{}, fmt.Errorf("alarm %s is still active", key)
	}
	if alarm.State == Closed {
		return *alarm, nil
	}
	alarm.State = Closed
	alarm.ClosedAt = now
	alarm.UpdatedAt = now
	alarm.Revision++
	notify, notifyChange = *alarm, true
	return *alarm, nil
}

// Get returns one alarm.
func (e *Engine) Get(key string) (Alarm, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	alarm, ok := e.alarms[key]
	if !ok {
		return Alarm{}, false
	}
	return *alarm, true
}

// List returns alarms with active ones first, then by creation time.
func (e *Engine) List() []Alarm {
	e.mu.Lock()
	defer e.mu.Unlock()
	alarms := make([]Alarm, 0, len(e.order))
	for _, key := range e.order {
		alarms = append(alarms, *e.alarms[key])
	}
	sort.SliceStable(alarms, func(i, j int) bool {
		if alarms[i].Active != alarms[j].Active {
			return alarms[i].Active
		}
		return alarms[i].CreatedAt.Before(alarms[j].CreatedAt)
	})
	return alarms
}

// Active returns the number of alarms that still describe a live condition.
func (e *Engine) Active() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	count := 0
	for _, alarm := range e.alarms {
		if alarm.Active {
			count++
		}
	}
	return count
}
