package appapi

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"fastiot/internal/decision"
	"fastiot/internal/ems/command"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/emsapi"
	"github.com/nats-io/nats.go"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

func TestRuleMixedJoinsPersistAndSelectSameRuntimeRow(t *testing.T) {
	dir, password := t.TempDir(), data.NewID()
	base, token, _, stop := startInputServer(t, dir, password)
	root := snapshotRoot(t, base, token)
	ids := map[string]string{}
	for key, value := range map[string]float64{"a": 1, "b": 0, "c": 0, "out": 0} {
		ids[key] = createNodeAPI(t, base, token, "variable", root, key, value)
	}
	document := map[string]any{"scope": "self", "table": map[string]any{"rows": []any{
		map[string]any{"row_id": "mixed", "hold_ms": 150, "conditions": []any{
			map[string]any{"point_id": "a", "operator": "eq", "value": 1},
			map[string]any{"point_id": "b", "operator": "eq", "value": 1, "join": "OR"},
			map[string]any{"point_id": "c", "operator": "eq", "value": 1, "join": "AND"},
			map[string]any{"kind": "time", "weekday_mask": 127},
		}, "actions": []any{map[string]any{"action_id": "out", "value": 8, "readback": map[string]any{"timeout_ms": 5000}}}},
		map[string]any{"row_id": "fallback", "conditions": []any{}, "actions": []any{}},
	}}, "bindings": map[string]any{"points": map[string]any{"a": map[string]string{"node_id": ids["a"]}, "b": map[string]string{"node_id": ids["b"]}, "c": map[string]string{"node_id": ids["c"]}}, "actions": map[string]any{"out": map[string]string{"node_id": ids["out"]}}}}
	var created struct {
		ID       string `json:"id"`
		Revision string `json:"revision"`
	}
	status, body := call(t, http.MethodPost, base+"/rules", token, map[string]any{"parent": root, "description": "Mixed left fold", "document": document}, &created)
	if status != 200 {
		t.Fatalf("mixed create: %d %s", status, body)
	}
	document["revision"] = created.Revision
	status, body = call(t, http.MethodPost, base+"/rules/"+created.ID+"/publish", token, document, nil)
	if status != 200 {
		t.Fatalf("mixed publish: %d %s", status, body)
	}
	stop()
	base, token, h, _ := startInputServer(t, dir, password)
	service := runtime.NewService(nil, time.Minute, time.Second, 20)
	t.Cleanup(service.Close)
	h.SetEMS(emsapi.New(service, time.Minute))
	WireCommandHistory(dir, service.Commands())
	var writes atomic.Int32
	unsubscribe, err := client.SubscribePoints(h.nc, ids["out"], func(points []data.Point) {
		for _, point := range points {
			if point.Type == "valueSet" {
				writes.Add(1)
				value, _ := point.ValueFloat()
				_ = client.SendNodePoint(h.nc, ids["out"], data.NewPointFloat("value", "", value), true)
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	var snapshot struct {
		Nodes []data.NodeEdge `json:"nodes"`
	}
	status, body = call(t, http.MethodGet, base+"/snapshot", token, nil, &snapshot)
	if status != 200 {
		t.Fatal(body)
	}
	var restored decision.Document
	for _, node := range snapshot.Nodes {
		if node.ID == created.ID {
			text, _ := node.Points.Text("applied", "")
			if err := json.Unmarshal([]byte(text), &restored); err != nil {
				t.Fatal(err)
			}
		}
	}
	if restored.Revision != created.Revision {
		t.Fatal("mixed document identity lost on restart")
	}
	for _, tc := range []struct {
		values map[string]float64
		want   string
	}{{map[string]float64{"a": 1, "b": 0, "c": 0}, "fallback"}, {map[string]float64{"a": 0, "b": 1, "c": 1}, "mixed"}, {map[string]float64{"a": 1, "b": 0}, "fallback"}} {
		simulation := restored
		simulation.Simulation = &decision.SimulationOptions{HeldMS: 150}
		var result decision.SimulationResult
		status, body = call(t, http.MethodPost, base+"/rules/"+created.ID+"/test", token, map[string]any{"document": simulation, "inputs": tc.values}, &result)
		if status != 200 || result.RowID != tc.want {
			t.Fatalf("simulation row: %d %s", status, body)
		}
	}
	if writes.Load() != 0 || len(service.Commands().List()) != 0 {
		t.Fatal("mixed simulation issued control")
	}
	manager := client.NewManager(h.nc, func(nc *nats.Conn, config decision.DecisionTable) client.Client { return h.NewRuleClient(nc, config) }, nil)
	done := make(chan error, 1)
	go func() { done <- manager.Run() }()
	defer func() { manager.Stop(nil); <-done }()
	status, body = call(t, http.MethodPost, base+"/rules/"+created.ID+"/enable", token, map[string]bool{"enabled": true}, nil)
	if status != 200 {
		t.Fatalf("mixed enable: %d %s", status, body)
	}
	report := func() decision.CheckReport {
		nodes, _ := client.GetNodes(h.nc, "all", created.ID, "", false)
		var value decision.CheckReport
		if len(nodes) > 0 {
			text, _ := nodes[0].Points.Text("lastCheck", "")
			_ = json.Unmarshal([]byte(text), &value)
		}
		return value
	}
	waitFor(t, func() bool { return report().Row == "fallback" }, "runtime (1 OR 0) AND 0 chooses fallback")
	if writes.Load() != 0 {
		t.Fatal("runtime used any instead of left fold")
	}
	for _, key := range []string{"a", "b", "c"} {
		value := 1.0
		if key == "a" {
			value = 0
		}
		if err := client.SendNodePoint(h.nc, ids[key], data.NewPointFloat("value", "", value), true); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, func() bool {
		outcomes := service.Commands().List()
		return len(outcomes) == 1 && outcomes[0].State == command.Confirmed
	}, "runtime (0 OR 1) AND 1 confirmed software action")
	if writes.Load() != 1 || report().Row != "mixed" {
		t.Fatalf("runtime parity: writes=%d report=%+v", writes.Load(), report())
	}
}
