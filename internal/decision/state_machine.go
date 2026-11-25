package decision

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	siotclient "github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/ems/signal"
)

type StateMachineDefinition struct {
	InitialState string            `json:"initial_state"`
	Scope        string            `json:"scope,omitempty"` // self (default) or subtree
	States       []string          `json:"states"`
	Transitions  []StateTransition `json:"transitions"`
	Bindings     BindingSet        `json:"bindings"`
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
	Status       string             `json:"status"` // matched or held
	TransitionID string             `json:"transition_id,omitempty"`
	From         string             `json:"from"`
	To           string             `json:"to"`
	Reason       string             `json:"reason,omitempty"`
	Actions      []SimulationAction `json:"actions,omitempty"`
}

type StateMachineExecution struct {
	At           time.Time     `json:"at"`
	Event        string        `json:"event"`
	Status       string        `json:"status"` // confirmed, failed, unknown or held
	TransitionID string        `json:"transition_id,omitempty"`
	From         string        `json:"from"`
	To           string        `json:"to"`
	Reason       string        `json:"reason,omitempty"`
	Actions      []CheckAction `json:"actions,omitempty"`
}

type StateMachineDocument struct {
	Schema        int                     `json:"schema"`
	Revision      uint64                  `json:"revision"`
	Version       uint64                  `json:"version"`
	Draft         StateMachineDefinition  `json:"draft"`
	Applied       *StateMachineDefinition `json:"applied,omitempty"`
	CurrentState  string                  `json:"current_state"`
	LastExecution *StateMachineExecution  `json:"last_execution,omitempty"`
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
		row := Row{ID: transition.ID, Conditions: transition.Conditions, Match: transition.Match, Actions: transition.Actions}
		document := Document{Table: Table{Rows: []Row{row}}, Bindings: d.Bindings}
		if err := document.Validate(); err != nil {
			return err
		}
		for _, action := range transition.Actions {
			if action.Readback == nil {
				return fmt.Errorf("transition %s: every state machine action needs readback", transition.ID)
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
	applied := cloneStateMachineDefinition(d.Draft)
	d.Applied = &applied
	d.Version++
	d.Revision++
	return d, nil
}

func (d StateMachineDefinition) Simulate(current, event string, inputs map[string]float64, texts map[string]string, now time.Time) (StateMachineSimulation, error) {
	if err := d.Validate(); err != nil {
		return StateMachineSimulation{}, err
	}
	samples := SamplesFromValues(inputs, texts, now)
	result := StateMachineSimulation{Status: "held", From: current, To: current, Reason: "no transition for event"}
	for _, transition := range d.Transitions {
		if transition.Event != event || (transition.From != "*" && transition.From != current) {
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
		for _, action := range transition.Actions {
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

func stateMachineRows(d StateMachineDefinition) []Row {
	rows := make([]Row, 0, len(d.Transitions))
	for _, transition := range d.Transitions {
		rows = append(rows, Row{ID: transition.ID, Conditions: transition.Conditions, Match: transition.Match, Actions: transition.Actions})
	}
	return rows
}

func (d StateMachineDefinition) DecisionDocument() Document {
	return Document{Table: Table{Rows: stateMachineRows(d)}, Bindings: d.Bindings}
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
