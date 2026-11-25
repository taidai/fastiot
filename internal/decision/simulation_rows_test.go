package decision

import (
	"testing"
	"time"
)

func TestSimulationExplainsRowsAfterFirstMatch(t *testing.T) {
	doc := Document{Table: Table{Rows: []Row{
		{ID: "first", Conditions: []Condition{{PointID: "x", Operator: "gt", Value: 0}}, HoldMS: 1000},
		{ID: "fallback"},
	}}, Bindings: BindingSet{Points: map[string]Target{"x": {NodeID: "input"}}}}
	result, err := doc.Simulate(map[string]float64{"x": 1}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 2 || result.Rows[0].State != "waiting-hold" || result.Rows[1].State != "skipped" || len(result.Rows[1].Reasons) == 0 {
		t.Fatalf("missing first-match explanation: %+v", result)
	}
}
