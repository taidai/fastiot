package decision

import (
	"encoding/json"
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

func TestStateMachineEntryExitPlanAndTrace(t *testing.T) {
	definition := testStateMachineDefinition()
	encoded, _ := json.Marshal(definition)
	var wire map[string]any
	_ = json.Unmarshal(encoded, &wire)
	wire["state_actions"] = map[string]any{
		"stopped": map[string]any{"exit": []Action{{ActionID: "pump", Value: 0, Readback: &Readback{TimeoutMS: 100}}}},
		"running": map[string]any{"entry": []Action{{ActionID: "pump", Value: 2, Readback: &Readback{TimeoutMS: 100}}}},
	}
	encoded, _ = json.Marshal(wire)
	_ = json.Unmarshal(encoded, &definition)
	result, err := definition.Simulate("stopped", "start", map[string]float64{"permit": 1}, nil, time.Now())
	if err != nil || len(result.Actions) != 3 || result.Actions[0].Value != 0 || result.Actions[1].Value != 1 || result.Actions[2].Value != 2 {
		t.Fatalf("exit -> transition -> entry must share one ordered plan: %+v %v", result, err)
	}
	encoded, _ = json.Marshal(result)
	var response struct {
		Trace []struct {
			Status string `json:"status"`
		} `json:"trace"`
	}
	_ = json.Unmarshal(encoded, &response)
	if len(response.Trace) != 1 || response.Trace[0].Status != "matched" {
		t.Fatalf("simulation must explain evaluated transitions: %s", encoded)
	}
}

func TestStateMachineDeclaredCommandSimulationIsPure(t *testing.T) {
	var definition StateMachineDefinition
	if err := json.Unmarshal([]byte(`{"initial_state":"idle","states":["idle","active"],"transitions":[{"transition_id":"start","from":"idle","event":"start","to":"active","actions":[{"action_id":"start-device"}]}],"command_bindings":{"start-device":{"device_id":"business-device","command_id":"set","parameters":{"target":20}}}}`), &definition); err != nil {
		t.Fatal(err)
	}
	result, err := definition.Simulate("idle", "start", nil, nil, time.Now())
	if err != nil || result.Status != "matched" || len(result.Actions) != 1 {
		t.Fatalf("declared command simulation: %+v %v", result, err)
	}
}

func TestStateMachinePublishedVersionsRemainImmutable(t *testing.T) {
	doc, _ := NewStateMachineDocument(testStateMachineDefinition())
	doc, _ = doc.Publish(doc.Revision)
	draft := testStateMachineDefinition()
	draft.Transitions[0].Actions[0].Value = 2
	doc, _ = doc.SaveDraft(draft, doc.Revision)
	doc, _ = doc.Publish(doc.Revision)
	encoded, _ := json.Marshal(doc)
	var response struct {
		Versions map[uint64]StateMachineDefinition `json:"versions"`
	}
	_ = json.Unmarshal(encoded, &response)
	if len(response.Versions) != 2 || response.Versions[1].Transitions[0].Actions[0].Value != 1 || response.Versions[2].Transitions[0].Actions[0].Value != 2 {
		t.Fatalf("published definitions lost or overwritten: %s", encoded)
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
