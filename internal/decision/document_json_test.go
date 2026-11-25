package decision

import (
	"encoding/json"
	"testing"
)

func TestRuleDocumentNumericPresenceContract(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"explicit zero", `{"table":{"rows":[{"row_id":"r","actions":[{"action_id":"a","value":0}]}]}}`, true},
		{"optional timing omitted", `{"table":{"rows":[{"row_id":"r","conditions":[{"kind":"time"}]}]}}`, true},
		{"missing numeric condition", `{"table":{"rows":[{"row_id":"r","conditions":[{"point_id":"p","operator":"eq"}]}]}}`, false},
		{"null condition", `{"table":{"rows":[{"row_id":"r","conditions":[{"point_id":"p","operator":"eq","value":null}]}]}}`, false},
		{"case folded null action", `{"TABLE":{"ROWS":[{"ROW_ID":"r","ACTIONS":[{"ACTION_ID":"a","VALUE":null}]}]}}`, false},
		{"null delay", `{"table":{"rows":[{"row_id":"r","actions":[{"action_id":"a","value":1,"delay_ms":null}]}]}}`, false},
		{"null tolerance", `{"table":{"rows":[{"row_id":"r","actions":[{"action_id":"a","value":1,"readback":{"timeout_ms":1000,"tolerance":null}}]}]}}`, false},
		{"null time window", `{"table":{"rows":[{"row_id":"r","conditions":[{"kind":"time","start":null}]}]}}`, false},
		{"unknown action field", `{"table":{"rows":[{"row_id":"r","actions":[{"action_id":"a","value":1,"script":"write()"}]}]}}`, false},
		{"NaN", `{"table":{"rows":[{"row_id":"r","actions":[{"action_id":"a","value":NaN}]}]}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var doc Document
			err := json.Unmarshal([]byte(tc.raw), &doc)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t error=%v", tc.valid, err)
			}
		})
	}
}

func TestSimulationInputPresenceContract(t *testing.T) {
	for _, raw := range []string{`null`, `{"p":null}`, `{"p":"NaN"}`} {
		var inputs SimulationInputs
		if json.Unmarshal([]byte(raw), &inputs) == nil {
			t.Errorf("accepted invalid simulation inputs %s", raw)
		}
	}
	var inputs SimulationInputs
	if err := json.Unmarshal([]byte(`{"p":0}`), &inputs); err != nil || inputs["p"] != 0 {
		t.Fatalf("explicit zero rejected: %v %+v", err, inputs)
	}
}
