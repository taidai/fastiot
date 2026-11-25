package decision

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestRuleMixedJoinsFoldLeftWithoutChangingTimeOrHold(t *testing.T) {
	const source = `{"table":{"rows":[{"row_id":"mixed","conditions":[{"kind":"time","start":"08:00","end":"09:00"},{"point_id":"a","operator":"eq","value":1},{"point_id":"b","operator":"eq","value":1,"join":"OR"},{"point_id":"c","operator":"eq","value":1,"join":"AND"}],"hold_ms":1000,"actions":[]},{"row_id":"fallback","conditions":[],"actions":[]}]},"bindings":{"points":{"a":{"node_id":"a"},"b":{"node_id":"b"},"c":{"node_id":"c"}},"actions":{}}}`
	var document Document
	if err := json.Unmarshal([]byte(source), &document); err != nil {
		t.Fatalf("prototype mixed rule rejected: %v", err)
	}
	for _, tc := range []struct {
		name       string
		inputs     map[string]float64
		hour, held int
		row, state string
	}{
		{"(1 OR 0) AND 0", map[string]float64{"a": 1, "b": 0, "c": 0}, 8, 1000, "fallback", "fired"},
		{"(0 OR 1) AND 1", map[string]float64{"a": 0, "b": 1, "c": 1}, 8, 1000, "mixed", "fired"},
		{"missing first input is unsatisfied", map[string]float64{"b": 1, "c": 1}, 8, 1000, "mixed", "fired"},
		{"missing final input is unsatisfied", map[string]float64{"a": 1, "b": 0}, 8, 1000, "fallback", "fired"},
		{"window stays independent", map[string]float64{"a": 0, "b": 1, "c": 1}, 12, 1000, "fallback", "fired"},
		{"hold stays required", map[string]float64{"a": 0, "b": 1, "c": 1}, 8, 0, "mixed", "waiting-hold"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			document.Simulation = &SimulationOptions{HeldMS: tc.held}
			result, err := document.Simulate(tc.inputs, nil, time.Date(2026, 11, 25, tc.hour, 30, 0, 0, time.UTC))
			if err != nil || result.RowID != tc.row || result.State != tc.state {
				t.Fatalf("result=%+v error=%v", result, err)
			}
		})
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var restored Document
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	restored.Simulation = &SimulationOptions{HeldMS: 1000}
	result, err := restored.Simulate(map[string]float64{"a": 1, "b": 0, "c": 0}, nil, time.Date(2026, 11, 25, 8, 30, 0, 0, time.UTC))
	if err != nil || result.RowID != "fallback" {
		t.Fatalf("mixed semantics lost on round trip: %+v %v", result, err)
	}
}

func TestRuleMixedJoinsRejectMalformedAndTimeJoins(t *testing.T) {
	for _, source := range []string{
		`{"table":{"rows":[{"row_id":"r","conditions":[{"point_id":"a","operator":"eq","value":1,"join":null}]}]},"bindings":{"points":{"a":{"node_id":"a"}}}}`,
		`{"table":{"rows":[{"row_id":"r","conditions":[{"point_id":"a","operator":"eq","value":1,"join":"XOR"}]}]},"bindings":{"points":{"a":{"node_id":"a"}}}}`,
		`{"table":{"rows":[{"row_id":"r","conditions":[{"kind":"time","join":"OR"}]}]}}`,
		`{"table":{"rows":[{"row_id":"r","conditions":[{"conditions":[],"join":"AND"}]}]}}`,
	} {
		var document Document
		if err := json.Unmarshal([]byte(source), &document); err == nil && document.Validate() == nil {
			t.Errorf("malformed rule join accepted: %s", source)
		}
	}
}

func TestMixedJoinChangesAreVisibleInVersionDiff(t *testing.T) {
	conditions := []Condition{{PointID: "a", Operator: "eq", Value: 1}, {PointID: "b", Operator: "eq", Value: 1}}
	before := Document{Table: Table{Rows: []Row{{ID: "r", Conditions: conditions}}}}
	after := Document{Table: Table{Rows: []Row{{ID: "r", Conditions: conditions, Joins: []string{"", "OR"}}}}}
	if diffs := DiffDocuments(before, after); len(diffs) != 1 || diffs[0].Kind != "changed" {
		t.Fatalf("join change missing from publication diff: %+v", diffs)
	}
}

func TestRuleJoinDoesNotExtendSharedGuardConditionWireContract(t *testing.T) {
	var condition Condition
	decoder := json.NewDecoder(bytes.NewBufferString(`{"point_id":"a","operator":"eq","value":1,"join":"OR"}`))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&condition) == nil {
		t.Fatal("rule-only join leaked into shared state-machine guard schema")
	}
}
