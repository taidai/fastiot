package decision

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestRuleNotificationIsTypedPureSimulationNotNumericWrite(t *testing.T) {
	const source = `{"scope":"self","table":{"rows":[{"row_id":"notice","conditions":[],"actions":[{"action_id":"n1","kind":"notify","delay_ms":20,"notification":{"recipient_ids":["account-a"],"title":"Check unit","body":"Literal rule notice"}}]}]},"bindings":{"points":{},"actions":{}}}`
	var document Document
	if err := json.Unmarshal([]byte(source), &document); err != nil {
		t.Fatalf("notification declaration rejected: %v", err)
	}
	if err := document.Validate(); err != nil {
		t.Fatal(err)
	}
	result, err := document.Simulate(nil, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Actions []map[string]json.RawMessage `json:"actions"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Actions) != 1 || string(wire.Actions[0]["kind"]) != `"notify"` || len(wire.Actions[0]["notification"]) == 0 {
		t.Fatalf("notification plan lost type: %s", raw)
	}
	if _, found := wire.Actions[0]["value"]; found {
		t.Fatalf("notification pretends to be a zero write: %s", raw)
	}
	stored, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var restored Document
	if err := json.Unmarshal(stored, &restored); err != nil || restored.Validate() != nil {
		t.Fatalf("notification round trip: %s %v", stored, err)
	}
}

func TestRuleNotificationRejectsMalformedAndNumericImpersonation(t *testing.T) {
	for _, action := range []string{
		`{"action_id":"n","kind":"notify","notification":null}`,
		`{"action_id":"n","kind":"notify","notification":{"title":"notice","recipient_ids":[]}}`,
		`{"action_id":"n","kind":"notify","notification":{"title":"notice","recipient_ids":[" "]}}`,
		`{"action_id":"n","kind":"notify","notification":{"title":"","recipient_ids":["u"]}}`,
		`{"action_id":"n","kind":"notify","value":0,"notification":{"title":"notice","recipient_ids":["u"]}}`,
		`{"action_id":"n","kind":"notify","readback":null,"notification":{"title":"notice","recipient_ids":["u"]}}`,
		`{"action_id":"n","kind":"notify","delay_ms":null,"notification":{"title":"notice","recipient_ids":["u"]}}`,
		`{"action_id":"n","kind":"notify","notification":{"title":"notice","recipient_ids":["u"],"webhook":"https://example.test"}}`,
		`{"action_id":"n","kind":"script","value":0}`,
		`{"action_id":"n","notification":{"title":"notice","recipient_ids":["u"]}}`,
	} {
		var document Document
		err := json.Unmarshal([]byte(`{"scope":"self","table":{"rows":[{"row_id":"r","actions":[`+action+`]}]}}`), &document)
		if err == nil && document.Validate() == nil {
			t.Errorf("invalid notification accepted: %s", action)
		}
	}
}

func TestRuleNotificationScenarioDistinguishesCreationPlanFromZeroWrite(t *testing.T) {
	notice := &RuleNotification{RecipientIDs: []string{"u"}, Title: "Notice"}
	document := Document{Table: Table{Rows: []Row{{ID: "r", Actions: []Action{{ActionID: "n", Notification: notice}}}}}, Scenarios: []Scenario{{ID: "s", ExpectRow: "r", ExpectActions: []Action{{ActionID: "n", Notification: notice}}}}}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var restored Document
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if result := restored.RunScenarios(time.Now()); len(result) != 1 || !result[0].Passed {
		t.Fatalf("typed notification scenario: %+v", result)
	}
	restored.Scenarios[0].ExpectActions[0].Notification = nil
	if restored.RunScenarios(time.Now())[0].Passed {
		t.Fatal("notification matched numeric zero expectation")
	}
	original := document
	changed := document
	changed.Table.Rows = []Row{{ID: "r", Actions: []Action{{ActionID: "n", Notification: &RuleNotification{RecipientIDs: []string{"u"}, Title: "Changed"}}}}}
	if len(DiffDocuments(original, changed)) == 0 {
		t.Fatal("notification payload change invisible to version diff")
	}
}

func TestNotificationDoesNotExtendStateMachineActionWire(t *testing.T) {
	var action Action
	decoder := json.NewDecoder(bytes.NewBufferString(`{"action_id":"n1","kind":"notify","notification":{"recipient_ids":["u"],"title":"notice"}}`))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&action) == nil {
		t.Fatal("rule notification leaked into shared state-machine action schema")
	}
}
