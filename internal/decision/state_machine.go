package decision

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	siotclient "github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/ems/signal"
)

type StateMachineDefinition struct {
	InitialState    string                                `json:"initial_state"`
	Scope           string                                `json:"scope,omitempty"` // self (default) or subtree
	States          []string                              `json:"states"`
	Transitions     []StateTransition                     `json:"transitions"`
	Bindings        BindingSet                            `json:"bindings"`
	StateActions    map[string]StateActions               `json:"state_actions,omitempty"`
	CommandBindings map[string]StateMachineCommandBinding `json:"command_bindings,omitempty"`
}

// DeviceID is a stable business device identity, never a transport address.
type StateMachineCommandBinding struct {
	DeviceID   string             `json:"device_id"`
	CommandID  string             `json:"command_id"`
	Parameters map[string]float64 `json:"parameters,omitempty"`
}

// Reject null rather than letting encoding/json silently turn it into a zero
// command parameter. Keep unknown-field rejection at this custom decode boundary.
func (b *StateMachineCommandBinding) UnmarshalJSON(raw []byte) error {
	var input struct {
		DeviceID   string              `json:"device_id"`
		CommandID  string              `json:"command_id"`
		Parameters map[string]*float64 `json:"parameters,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return err
	}
	parameters := map[string]float64{}
	for key, value := range input.Parameters {
		if value == nil {
			return fmt.Errorf("command parameter %s must be a number", key)
		}
		parameters[key] = *value
	}
	*b = StateMachineCommandBinding{DeviceID: input.DeviceID, CommandID: input.CommandID, Parameters: parameters}
	return nil
}

// StateActions share the same bounded, readback-confirmed action vocabulary as
// transition actions. A transition plans exit, transition, then entry actions.
type StateActions struct {
	Entry []Action `json:"entry,omitempty"`
	Exit  []Action `json:"exit,omitempty"`
}

type StateMachineTrace struct {
	TransitionID string `json:"transition_id"`
	Status       string `json:"status"`
	Reason       string `json:"reason,omitempty"`
}

type StateTransition struct {
	ID         string      `json:"transition_id"`
	From       string      `json:"from"` // "*" matches any current state
	Event      string      `json:"event"`
	To         string      `json:"to"`
	Conditions []Condition `json:"conditions"`
	Match      string      `json:"match,omitempty"`
	Actions    []Action    `json:"actions"`
}

type StateMachineSimulation struct {
	Status       string              `json:"status"` // matched or held
	TransitionID string              `json:"transition_id,omitempty"`
	From         string              `json:"from"`
	To           string              `json:"to"`
	Reason       string              `json:"reason,omitempty"`
	Actions      []SimulationAction  `json:"actions,omitempty"`
	Trace        []StateMachineTrace `json:"trace"`
}

type StateMachineCommand struct {
	RequestID string `json:"request_id"`
	NodeID    string `json:"node_id"`
	Status    string `json:"status,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type StateMachineExecution struct {
	RequestID    string                `json:"request_id,omitempty"`
	Version      uint64                `json:"version"`
	Trace        []StateMachineTrace   `json:"trace,omitempty"`
	Commands     []StateMachineCommand `json:"commands,omitempty"`
	At           time.Time             `json:"at"`
	Event        string                `json:"event"`
	Status       string                `json:"status"` // confirmed, failed, unknown or held
	TransitionID string                `json:"transition_id,omitempty"`
	From         string                `json:"from"`
	To           string                `json:"to"`
	Reason       string                `json:"reason,omitempty"`
	Actions      []CheckAction         `json:"actions,omitempty"`
}

type StateMachineDocument struct {
	Schema        int                               `json:"schema"`
	Revision      uint64                            `json:"revision"`
	Version       uint64                            `json:"version"`
	Draft         StateMachineDefinition            `json:"draft"`
	Applied       *StateMachineDefinition           `json:"applied,omitempty"`
	Versions      map[uint64]StateMachineDefinition `json:"versions,omitempty"`
	CurrentState  string                            `json:"current_state"`
	LastExecution *StateMachineExecution            `json:"last_execution,omitempty"`
}

func cloneStateMachineDefinition(d StateMachineDefinition) StateMachineDefinition {
	encoded, _ := json.Marshal(d)
	var clone StateMachineDefinition
	_ = json.Unmarshal(encoded, &clone)
	return clone
}

func (d StateMachineDefinition) Validate() error {
	if d.InitialState == "" || (d.Scope != "" && d.Scope != "self" && d.Scope != "subtree") {
		return fmt.Errorf("invalid state machine initial state or scope")
	}
	states := map[string]bool{}
	for _, state := range d.States {
		if state == "" || states[state] {
			return fmt.Errorf("invalid or duplicate state %q", state)
		}
		states[state] = true
	}
	if !states[d.InitialState] {
		return fmt.Errorf("initial state is not defined")
	}
	ids := map[string]bool{}
	for _, transition := range d.Transitions {
		if transition.ID == "" || ids[transition.ID] || transition.Event == "" ||
			(transition.From != "*" && !states[transition.From]) || !states[transition.To] {
			return fmt.Errorf("invalid transition %q", transition.ID)
		}
		ids[transition.ID] = true
	}
	for state := range d.StateActions {
		if !states[state] {
			return fmt.Errorf("actions for undefined state %q", state)
		}
	}
	bindings := BindingSet{Points: d.Bindings.Points, Actions: map[string]Target{}}
	for id, target := range d.Bindings.Actions {
		bindings.Actions[id] = target
	}
	for id, binding := range d.CommandBindings {
		if binding.DeviceID == "" || binding.CommandID == "" || id == "" {
			return fmt.Errorf("invalid command binding")
		}
		if _, ambiguous := bindings.Actions[id]; ambiguous {
			return fmt.Errorf("ambiguous action binding %s", id)
		}
		for _, value := range binding.Parameters {
			if !finite(value) {
				return fmt.Errorf("nonfinite command parameter")
			}
		}
		bindings.Actions[id] = Target{NodeID: binding.DeviceID}
	}
	for _, row := range stateMachineRows(d) {
		document := Document{Table: Table{Rows: []Row{row}}, Bindings: bindings}
		if err := document.Validate(); err != nil {
			return err
		}
		for _, action := range row.Actions {
			_, named := d.CommandBindings[action.ActionID]
			if action.Readback == nil && !named {
				return fmt.Errorf("%s: every point action needs readback", row.ID)
			}
			if named && action.Readback != nil && action.Readback.Target.NodeID == "" {
				return fmt.Errorf("command process readback needs an explicit target")
			}
		}
	}
	return nil
}

func NewStateMachineDocument(draft StateMachineDefinition) (StateMachineDocument, error) {
	if err := draft.Validate(); err != nil {
		return StateMachineDocument{}, err
	}
	return StateMachineDocument{Schema: 1, Revision: 1, Draft: cloneStateMachineDefinition(draft), CurrentState: draft.InitialState}, nil
}

func (d StateMachineDocument) SaveDraft(draft StateMachineDefinition, revision uint64) (StateMachineDocument, error) {
	if d.Revision != revision {
		return d, fmt.Errorf("revision_conflict")
	}
	if err := draft.Validate(); err != nil {
		return d, err
	}
	d.Draft = cloneStateMachineDefinition(draft)
	if d.Version == 0 && d.Applied == nil && d.LastExecution == nil {
		d.CurrentState = draft.InitialState
	}
	d.Revision++
	return d, nil
}

func (d StateMachineDocument) Publish(revision uint64) (StateMachineDocument, error) {
	if d.Revision != revision {
		return d, fmt.Errorf("revision_conflict")
	}
	if err := d.Draft.Validate(); err != nil {
		return d, err
	}
	valid := false
	for _, state := range d.Draft.States {
		valid = valid || state == d.CurrentState
	}
	if !valid {
		return d, fmt.Errorf("current_state_not_defined")
	}
	versions := make(map[uint64]StateMachineDefinition, len(d.Versions)+1)
	for version, definition := range d.Versions {
		versions[version] = cloneStateMachineDefinition(definition)
	}
	if d.Applied != nil {
		versions[d.Version] = cloneStateMachineDefinition(*d.Applied)
	}
	applied := cloneStateMachineDefinition(d.Draft)
	d.Applied = &applied
	d.Version++
	versions[d.Version] = cloneStateMachineDefinition(applied)
	d.Versions = versions
	d.Revision++
	return d, nil
}

func (d StateMachineDefinition) Simulate(current, event string, inputs map[string]float64, texts map[string]string, now time.Time) (StateMachineSimulation, error) {
	if err := d.Validate(); err != nil {
		return StateMachineSimulation{}, err
	}
	valid := false
	for _, state := range d.States {
		valid = valid || current == state
	}
	if !valid {
		return StateMachineSimulation{}, fmt.Errorf("current_state_not_defined")
	}
	samples := SamplesFromValues(inputs, texts, now)
	result := StateMachineSimulation{Status: "held", From: current, To: current, Reason: "no transition for event", Trace: []StateMachineTrace{}}
	for _, transition := range d.Transitions {
		trace := StateMachineTrace{TransitionID: transition.ID, Status: "skipped"}
		if transition.Event != event || (transition.From != "*" && transition.From != current) {
			trace.Reason = "state or event does not match"
			result.Trace = append(result.Trace, trace)
			continue
		}
		matched := transition.Match != "any" || len(transition.Conditions) == 0
		reasons := []string{}
		for _, condition := range transition.Conditions {
			ok, reason := conditionMatches(condition, samples, now)
			if transition.Match == "any" {
				matched = matched || ok
			} else {
				matched = matched && ok
			}
			if reason != "" {
				reasons = append(reasons, reason)
			}
		}
		if !matched {
			trace.Status, trace.Reason = "held", joinReasons(reasons)
			result.Trace = append(result.Trace, trace)
			// A different transition for the same event may still be the
			// intended path; keep looking and report the first failure.
			if result.TransitionID == "" {
				result.TransitionID = transition.ID
				result.Reason = joinReasons(reasons)
			}
			continue
		}
		result.Status = "matched"
		result.TransitionID = transition.ID
		result.To = transition.To
		result.Reason = ""
		trace.Status = "matched"
		result.Trace = append(result.Trace, trace)
		for _, action := range d.TransitionActions(current, transition) {
			result.Actions = append(result.Actions, SimulationAction{ActionID: action.ActionID, Value: action.Value, DelayMS: action.DelayMS})
		}
		return result, nil
	}
	return result, nil
}

func (d StateMachineDocument) Plan(event string, inputs map[string]float64, texts map[string]string, now time.Time) (StateMachineSimulation, error) {
	if d.Applied == nil {
		return StateMachineSimulation{}, fmt.Errorf("publish_required")
	}
	return d.Applied.Simulate(d.CurrentState, event, inputs, texts, now)
}

func (d StateMachineDefinition) TransitionActions(current string, transition StateTransition) []Action {
	actions := append([]Action{}, d.StateActions[current].Exit...)
	actions = append(actions, transition.Actions...)
	return append(actions, d.StateActions[transition.To].Entry...)
}

func stateMachineRows(d StateMachineDefinition) []Row {
	rows := make([]Row, 0, len(d.Transitions))
	for _, transition := range d.Transitions {
		rows = append(rows, Row{ID: transition.ID, Conditions: transition.Conditions, Match: transition.Match, Actions: transition.Actions})
	}
	for _, state := range d.States {
		actions := d.StateActions[state]
		rows = append(rows, Row{ID: "entry:" + state, Actions: actions.Entry}, Row{ID: "exit:" + state, Actions: actions.Exit})
	}
	return rows
}

func (d StateMachineDefinition) DecisionDocument() Document {
	return Document{Table: Table{Rows: stateMachineRows(d)}, Bindings: d.Bindings}
}

// MapReferences visits engineering references, never event or command identities.
// Callers own the decoded definition; published runtime snapshots are untouched.
func (d *StateMachineDefinition) MapReferences(mapID func(string) string) {
	for key, target := range d.Bindings.Points {
		if target.NodeID != "" {
			target.NodeID = mapID(target.NodeID)
			d.Bindings.Points[key] = target
		}
	}
	for key, target := range d.Bindings.Actions {
		if target.NodeID != "" {
			target.NodeID = mapID(target.NodeID)
			d.Bindings.Actions[key] = target
		}
	}
	for key, binding := range d.CommandBindings {
		binding.DeviceID = mapID(binding.DeviceID)
		d.CommandBindings[key] = binding
	}
	mapActions := func(actions []Action) {
		for i := range actions {
			if actions[i].Readback != nil && actions[i].Readback.Target.NodeID != "" {
				actions[i].Readback.Target.NodeID = mapID(actions[i].Readback.Target.NodeID)
			}
		}
	}
	for i := range d.Transitions {
		mapActions(d.Transitions[i].Actions)
	}
	for _, actions := range d.StateActions {
		mapActions(actions.Entry)
		mapActions(actions.Exit)
	}
}

func (d *StateMachineDocument) MapReferences(mapID func(string) string) {
	d.Draft.MapReferences(mapID)
	if d.Applied != nil {
		d.Applied.MapReferences(mapID)
	}
	for version, definition := range d.Versions {
		definition.MapReferences(mapID)
		d.Versions[version] = definition
	}
}

// ReadLiveInputs uses the same scope, point decoding and freshness checks as
// decision-table execution. Missing or stale data is an error, never a zero.
func ReadLiveInputs(nc *nats.Conn, parent string, bindings map[string]Target) (map[string]float64, map[string]string, error) {
	values := map[string]float64{}
	texts := map[string]string{}
	for pointID, target := range bindings {
		if err := ValidateTarget(nc, parent, target, false, nil); err != nil {
			return nil, nil, fmt.Errorf("point %s: %w", pointID, err)
		}
		if err := validateFreshInput(nc, pointID, target); err != nil {
			return nil, nil, err
		}
		nodes, err := siotclient.GetNodes(nc, "all", target.NodeID, "", false)
		if err != nil || len(nodes) == 0 {
			return nil, nil, fmt.Errorf("point %s: input unavailable", pointID)
		}
		if target.PointType == "" {
			target.PointType = data.PointTypeValue
		}
		point, ok := nodes[0].Points.Find(target.PointType, target.Key)
		if !ok || point.Tombstone%2 != 0 {
			return nil, nil, fmt.Errorf("point %s: input unavailable", pointID)
		}
		value, err := pointSignal(point)
		if err != nil {
			return nil, nil, fmt.Errorf("point %s: %w", pointID, err)
		}
		switch value.Kind {
		case signal.Number:
			values[pointID] = value.Num
		case signal.Bool:
			values[pointID] = data.BoolToFloat(value.Bool)
		case signal.Enum:
			texts[pointID] = value.Enum
		default:
			return nil, nil, fmt.Errorf("point %s: unsupported input type", pointID)
		}
	}
	return values, texts, nil
}
