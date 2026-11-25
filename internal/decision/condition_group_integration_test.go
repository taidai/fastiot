package decision

import (
	"encoding/json"
	"path/filepath"
	"testing"

	siotclient "github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
	"github.com/simpleiot/simpleiot/server"
)

func TestDecisionORRunsWithMissingSiblingValue(t *testing.T) {
	opts := server.TestServerOptions
	opts.ID = "easyems-decision-or-test"
	opts.DataDir = filepath.Join(t.TempDir(), "data")
	opts.NatsPort = 19730
	opts.NatsServer = "nats://127.0.0.1:19730"
	opts.HTTPPort = "9730"
	opts.NatsMonitorPort = 19732
	opts.NatsWSPort = 19733
	opts.NatsMQTTPort = 0
	nc, root, stopServer, err := server.TestServerOpts(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer stopServer()
	_, stopManager := runManager(t, nc)
	defer stopManager()
	for _, node := range []data.NodeEdge{
		{ID: "pressure", Type: data.NodeTypeVariable, Parent: root.ID},
		{ID: "level", Type: data.NodeTypeVariable, Parent: root.ID, Points: data.Points{data.NewPointFloat(data.PointTypeValue, "", 60)}},
		{ID: "pump", Type: data.NodeTypeVariable, Parent: root.ID, Points: data.Points{data.NewPointFloat(data.PointTypeValue, "", 0)}},
	} {
		if err := siotclient.SendNode(nc, node, "test"); err != nil {
			t.Fatal(err)
		}
	}
	document := Document{
		Table: Table{Rows: []Row{{
			ID: "start", Match: "any",
			Conditions: []Condition{{PointID: "pressure", Operator: "lt", Value: 0.35}, {PointID: "level", Operator: "gt", Value: 50}},
			Actions:    []Action{{ActionID: "pump", Value: 1}},
		}}},
		Bindings: BindingSet{
			Points:  map[string]Target{"pressure": {NodeID: "pressure"}, "level": {NodeID: "level"}},
			Actions: map[string]Target{"pump": {NodeID: "pump"}},
		},
	}
	encoded, _ := json.Marshal(document)
	if err := siotclient.SendNodeType(nc, DecisionTable{ID: "comfort-policy", Parent: root.ID, Applied: string(encoded), Enabled: true}, "test"); err != nil {
		t.Fatal(err)
	}
	waitForValue(t, nc, "pump", 1)
}
