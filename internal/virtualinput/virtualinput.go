package virtualinput

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"slices"
	"strings"
	"time"
)

type Definition struct {
	Kind            string          `json:"kind"`
	Unit            string          `json:"unit"`
	Purpose         string          `json:"purpose"`
	Min             *float64        `json:"min,omitempty"`
	Max             *float64        `json:"max,omitempty"`
	Choices         []string        `json:"choices,omitempty"`
	Validity        string          `json:"validity"`
	TTLSeconds      int64           `json:"ttl_seconds,omitempty"`
	RuntimeEditable bool            `json:"runtime_editable"`
	Value           json.RawMessage `json:"value,omitempty"`
}

type Context struct {
	Actor            string
	OperationID      string
	ExpectedRevision uint64
	Now              time.Time
	Epoch            string
	Monotonic        time.Duration
}

type Entry struct {
	Value      json.RawMessage `json:"value"`
	Definition Definition      `json:"definition"`
	Actor      string          `json:"actor"`
	EnteredAt  time.Time       `json:"entered_at"`
	ExpiresAt  time.Time       `json:"expires_at,omitempty"`
	Epoch      string          `json:"epoch,omitempty"`
	Deadline   time.Duration   `json:"deadline,omitempty"`
}

type Change struct {
	ID          string          `json:"id"`
	Action      string          `json:"action"`
	Actor       string          `json:"actor"`
	At          time.Time       `json:"at"`
	Fingerprint string          `json:"fingerprint"`
	Before      json.RawMessage `json:"before"`
	After       json.RawMessage `json:"after"`
}

type Document struct {
	Schema     int         `json:"schema"`
	Revision   uint64      `json:"revision"`
	Source     string      `json:"source"`
	Draft      Definition  `json:"draft"`
	Applied    *Definition `json:"applied,omitempty"`
	Current    *Entry      `json:"current,omitempty"`
	Retained   *Entry      `json:"retained,omitempty"`
	LastChange *Change     `json:"last_change,omitempty"`
}

type View struct {
	Source   string          `json:"source"`
	Quality  string          `json:"quality"`
	Reason   string          `json:"reason,omitempty"`
	Value    json.RawMessage `json:"value,omitempty"`
	Retained *Entry          `json:"retained,omitempty"`
}

func (d Document) SetValue(value json.RawMessage, c Context) (Document, error) {
	fp, duplicate, err := d.checkOperation("value", value, c)
	if err != nil || duplicate {
		return d, err
	}
	if (d.Source != "manual" && d.Source != "simulation") || d.Applied == nil || !d.Applied.RuntimeEditable {
		return d, errors.New("input_not_writable")
	}
	if !d.Applied.validValue(value) {
		return d, errors.New("invalid_input_value")
	}
	ttl := time.Duration(d.Applied.TTLSeconds) * time.Second
	if c.Epoch == "" || c.Monotonic < 0 || c.Monotonic > time.Duration(1<<63-1)-ttl {
		return d, errors.New("invalid_clock")
	}
	before := d.Current
	d.Current = &Entry{Value: value, Definition: *d.Applied, Actor: c.Actor, EnteredAt: c.Now.UTC(), ExpiresAt: c.Now.Add(ttl).UTC(), Epoch: c.Epoch, Deadline: c.Monotonic + ttl}
	d.changed("value", fp, before, d.Current, c)
	return d, nil
}

func validDefinition(source string, d Definition) bool {
	if strings.TrimSpace(d.Unit) == "" || strings.TrimSpace(d.Purpose) == "" {
		return false
	}
	switch source {
	case "configuration":
		if d.Validity != "constant" || d.RuntimeEditable || d.TTLSeconds != 0 {
			return false
		}
	case "manual", "simulation":
		if d.Validity != "timed" || d.TTLSeconds <= 0 || d.TTLSeconds > int64((time.Duration(1<<63-1))/time.Second) || len(d.Value) != 0 {
			return false
		}
	default:
		return false
	}
	switch d.Kind {
	case "number":
		if d.Min == nil || d.Max == nil || math.IsNaN(*d.Min) || math.IsNaN(*d.Max) || math.IsInf(*d.Min, 0) || math.IsInf(*d.Max, 0) || *d.Min > *d.Max {
			return false
		}
	case "bool":
	case "enum":
		if len(d.Choices) == 0 {
			return false
		}
	default:
		return false
	}
	return source != "configuration" || d.validValue(d.Value)
}

func (d Definition) validValue(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	switch d.Kind {
	case "number":
		n, ok := value.(float64)
		return ok && !math.IsNaN(n) && !math.IsInf(n, 0) && d.Min != nil && d.Max != nil && n >= *d.Min && n <= *d.Max
	case "bool":
		_, ok := value.(bool)
		return ok
	case "enum":
		s, ok := value.(string)
		return ok && slices.Contains(d.Choices, s)
	}
	return false
}

func New(source string, draft Definition, c Context) (Document, error) {
	if !validDefinition(source, draft) {
		return Document{}, errors.New("invalid_input_definition")
	}
	fp, err := operationFingerprint("create", struct {
		Source string
		Draft  Definition
	}{source, draft}, c)
	if err != nil {
		return Document{}, err
	}
	d := Document{Schema: 1, Source: source, Draft: draft}
	d.changed("create", fp, nil, draft, c)
	return d, nil
}
func operationFingerprint(action string, payload any, c Context) (string, error) {
	if c.Actor == "" || c.OperationID == "" || len(c.OperationID) > 128 || c.Now.IsZero() {
		return "", errors.New("invalid_operation")
	}
	body, err := json.Marshal(struct {
		Action   string
		Actor    string
		Revision uint64
		Payload  any
	}{action, c.Actor, c.ExpectedRevision, payload})
	if err != nil {
		return "", errors.New("invalid_input_definition")
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func (d Document) checkOperation(action string, payload any, c Context) (string, bool, error) {
	fp, err := operationFingerprint(action, payload, c)
	if err != nil {
		return "", false, err
	}
	if d.LastChange != nil && d.LastChange.ID == c.OperationID {
		if d.LastChange.Fingerprint != fp {
			return "", false, errors.New("operation_conflict")
		}
		return fp, true, nil
	}
	if c.ExpectedRevision != d.Revision {
		return "", false, errors.New("revision_conflict")
	}
	return fp, false, nil
}

func (d *Document) changed(action, fp string, before, after any, c Context) {
	old, _ := json.Marshal(before)
	next, _ := json.Marshal(after)
	d.Revision++
	d.LastChange = &Change{ID: c.OperationID, Action: action, Actor: c.Actor, At: c.Now.UTC(), Fingerprint: fp, Before: old, After: next}
}

func (d Document) SaveDraft(draft Definition, c Context) (Document, error) {
	fp, duplicate, err := d.checkOperation("draft", draft, c)
	if err != nil || duplicate {
		return d, err
	}
	if !validDefinition(d.Source, draft) {
		return d, errors.New("invalid_input_definition")
	}
	before := d.Draft
	d.Draft = draft
	d.changed("draft", fp, before, draft, c)
	return d, nil
}
func (d Document) Apply(c Context) (Document, error) {
	fp, duplicate, err := d.checkOperation("apply", nil, c)
	if err != nil || duplicate {
		return d, err
	}
	if !validDefinition(d.Source, d.Draft) {
		return d, errors.New("invalid_input_definition")
	}
	if d.Applied != nil && reflect.DeepEqual(*d.Applied, d.Draft) {
		return d, nil
	}
	before := d.Applied
	applied := d.Draft
	d.Applied = &applied
	if d.Source == "configuration" {
		d.Current = &Entry{Value: applied.Value, Definition: applied, Actor: c.Actor, EnteredAt: c.Now.UTC()}
	} else if d.Current != nil {
		d.Retained, d.Current = d.Current, nil
	}
	d.changed("apply", fp, before, applied, c)
	return d, nil
}
func (d Document) Validate() error {
	if d.Schema != 1 || d.Revision == 0 || !validDefinition(d.Source, d.Draft) || (d.Applied != nil && !validDefinition(d.Source, *d.Applied)) {
		return errors.New("invalid_input_document")
	}
	for _, entry := range []*Entry{d.Current, d.Retained} {
		if entry == nil {
			continue
		}
		if !validDefinition(d.Source, entry.Definition) || !entry.Definition.validValue(entry.Value) || entry.Actor == "" || entry.EnteredAt.IsZero() {
			return errors.New("invalid_input_document")
		}
		if d.Source != "configuration" && (entry.Epoch == "" || !entry.ExpiresAt.Equal(entry.EnteredAt.Add(time.Duration(entry.Definition.TTLSeconds)*time.Second)) || entry.Deadline < time.Duration(entry.Definition.TTLSeconds)*time.Second) {
			return errors.New("invalid_input_document")
		}
	}
	if d.Current != nil && (d.Applied == nil || !reflect.DeepEqual(*d.Applied, d.Current.Definition)) {
		return errors.New("invalid_input_document")
	}
	return nil
}

func (d Document) Evaluate(c Context) View {
	v := View{Source: d.Source, Quality: "uninitialized", Reason: "not_applied"}
	if d.Applied == nil {
		return v
	}
	if d.Current == nil {
		v.Reason, v.Retained = "not_entered", d.Retained
		return v
	}
	v.Retained = d.Current
	if !reflect.DeepEqual(d.Current.Definition, *d.Applied) {
		v.Quality, v.Reason = "bad", "definition_changed"
		return v
	}
	if d.Source != "configuration" {
		if c.Epoch == "" || c.Epoch != d.Current.Epoch || c.Now.Before(d.Current.EnteredAt) {
			v.Quality, v.Reason = "bad", "pending_verification"
			return v
		}
		if !c.Now.Before(d.Current.ExpiresAt) || c.Monotonic >= d.Current.Deadline {
			v.Quality, v.Reason = "stale", "expired"
			return v
		}
	}
	v.Value, v.Quality, v.Reason = d.Current.Value, "good", ""
	return v
}
