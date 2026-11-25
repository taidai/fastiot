package decision

import (
	"testing"
	"time"
)

func testStateMachineDefinition() StateMachineDefinition {
	return StateMachineDefinition{
		InitialState: "stopped", Scope: "self", States: []string{"stopped", "running"},
		Transitions: []StateTransition{{
			ID: "start", From: "stopped", Event: "start", To: "running",
			Conditions: []Condition{{PointID: "permit", Operator: "eq", Kind: "bool", Value: 1}},
			Actions:    []Action{{ActionID: "pump", Value: 1, Readback: &Readback{TimeoutMS: 100}}},
		}},
		Bindings: BindingSet{
			Points:  map[string]Target{"permit": {NodeID: "permit"}},
			Actions: map[string]Target{"pump": {NodeID: "pump"}},
		},
	}
}

func TestStateMachineSimulationExplainsGuardHold(t *testing.T) {
	definition := testStateMachineDefinition()
	result, err := definition.Simulate("stopped", "start", map[string]float64{"permit": 0}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "held" || result.From != "stopped" || result.To != "stopped" || len(result.Actions) != 0 {
		t.Fatalf("failed guard must hold without actions: %+v", result)
	}
	result, err = definition.Simulate("stopped", "start", map[string]float64{"permit": 1}, nil, time.Now())
	if err != nil || result.Status != "matched" || result.To != "running" || len(result.Actions) != 1 {
		t.Fatalf("valid guard must produce a plan only: %+v, %v", result, err)
	}
}

func TestStateMachineTriesLaterTransitionsForSameEvent(t *testing.T) {
	definition := testStateMachineDefinition()
	definition.Transitions[0].Conditions[0].Value = 0 // first guard fails for permit = 1
	definition.Transitions = append(definition.Transitions, StateTransition{
		ID: "slow-start", From: "stopped", Event: "start", To: "running",
		Conditions: []Condition{{PointID: "permit", Operator: "eq", Kind: "bool", Value: 1}},
		Actions:    []Action{{ActionID: "pump", Value: 2, Readback: &Readback{TimeoutMS: 100}}},
	})
	result, err := definition.Simulate("stopped", "start", map[string]float64{"permit": 1}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "matched" || result.TransitionID != "slow-start" {
		t.Fatalf("an earlier false guard must not hide a later true transition: %+v", result)
	}
}

func TestStateMachineDraftPublishAndScopeValidation(t *testing.T) {
	document, err := NewStateMachineDocument(testStateMachineDefinition())
	if err != nil {
		t.Fatal(err)
	}
	if document.CurrentState != "stopped" || document.Applied != nil || document.Revision != 1 {
		t.Fatalf("unexpected new document: %+v", document)
	}
	if _, err := document.Publish(0); err == nil {
		t.Fatal("stale revision published")
	}
	published, err := document.Publish(1)
	if err != nil || published.Version != 1 || published.Applied == nil {
		t.Fatalf("publish failed: %+v, %v", published, err)
	}
	changed := testStateMachineDefinition()
	changed.InitialState = "missing"
	if _, err := published.SaveDraft(changed, published.Revision); err == nil {
		t.Fatal("invalid initial state accepted")
	}
	changed = testStateMachineDefinition()
	changed.Scope = "nearby"
	if _, err := NewStateMachineDocument(changed); err == nil {
		t.Fatal("invalid scope accepted")
	}
}
