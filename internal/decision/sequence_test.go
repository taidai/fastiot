package decision

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	siotclient "github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
	"github.com/simpleiot/simpleiot/server"
)

func TestSequenceSimulationPreservesRepeatedActionOrder(t *testing.T) {
	document := Document{
		Table: Table{Rows: []Row{{
			ID: "sequence", Conditions: []Condition{{PointID: "permit", Operator: "eq", Value: 1}},
			Actions: []Action{
				{ActionID: "pump", Value: 1, Readback: &Readback{TimeoutMS: 100}},
				{ActionID: "pump", Value: 0, DelayMS: 20, Readback: &Readback{TimeoutMS: 100}},
			},
		}}},
		Bindings: BindingSet{
			Points:  map[string]Target{"permit": {NodeID: "permit"}},
			Actions: map[string]Target{"pump": {NodeID: "pump"}},
		},
	}
	result, err := document.Simulate(map[string]float64{"permit": 1}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Actions) != 2 || result.Actions[0].Value != 1 || result.Actions[1].Value != 0 {
		t.Fatalf("sequence order was lost: %+v", result.Actions)
	}
}

func TestDecisionSequenceReadbackTimeoutStopsRemainingActions(t *testing.T) {
	opts := server.TestServerOptions
	opts.ID = "easyems-decision-sequence-test"
	opts.DataDir = filepath.Join(t.TempDir(), "data")
	opts.NatsPort = 19710
	opts.NatsServer = "nats://127.0.0.1:19710"
	opts.HTTPPort = "9712"
	opts.NatsMonitorPort = 19712
	opts.NatsWSPort = 19713
	opts.NatsMQTTPort = 0
	nc, root, stopServer, err := server.TestServerOpts(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer stopServer()
	_, stopManager := runManager(t, nc)
	defer stopManager()

	for _, item := range []struct {
		id    string
		value float64
	}{{"permit", 1}, {"first", -1}, {"second", -1}, {"third", -1}} {
		if err := siotclient.SendNode(nc, data.NodeEdge{ID: item.id, Type: data.NodeTypeVariable, Parent: root.ID,
			Points: data.Points{data.NewPointFloat(data.PointTypeValue, "", item.value)}}, "test"); err != nil {
			t.Fatal(err)
		}
	}
	document := Document{
		Table: Table{Rows: []Row{{
			ID: "run", Conditions: []Condition{{PointID: "permit", Operator: "eq", Value: 1}},
			Actions: []Action{
				{ActionID: "first", Value: 1, DelayMS: 80, Readback: &Readback{TimeoutMS: 500}},
				{ActionID: "second", Value: 2, DelayMS: 20, Readback: &Readback{TimeoutMS: 120}},
				{ActionID: "third", Value: 3, Readback: &Readback{TimeoutMS: 100}},
			},
		}}},
		Bindings: BindingSet{
			Points: map[string]Target{"permit": {NodeID: "permit"}},
			Actions: map[string]Target{
				"first": {NodeID: "first"}, "second": {NodeID: "second"}, "third": {NodeID: "third"},
			},
		},
	}
	encoded, _ := json.Marshal(document)
	if err := siotclient.SendNodeType(nc, DecisionTable{ID: "comfort-policy", Parent: root.ID,
		Applied: string(encoded), Enabled: true}, "test"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if got := pointValue(t, nc, "first", data.PointTypeValueSet); got != -1 {
		t.Fatalf("delay was bypassed: %v", got)
	}
	waitForValue(t, nc, "first", 1)
	if got := pointValue(t, nc, "second", data.PointTypeValueSet); got != -1 {
		t.Fatalf("second action ran before first readback: %v", got)
	}
	sendFloat(t, nc, "first", data.PointTypeValue, 1)
	waitForValue(t, nc, "second", 2)
	report := waitCheck(t, nc, "comfort-policy", func(report CheckReport) bool {
		return len(report.Actions) == 3 && report.Actions[1].Status == "unknown"
	})
	if report.Actions[0].Status != "confirmed" || report.Actions[2].Status != "skipped" {
		t.Fatalf("sequence statuses are not truthful: %+v", report.Actions)
	}
	if got := pointValue(t, nc, "third", data.PointTypeValueSet); got != -1 {
		t.Fatalf("action after unknown readback ran: %v", got)
	}
}
