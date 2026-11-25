package appapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fastiot/internal/decision"
	"github.com/nats-io/nats.go"
	siotclient "github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
	"github.com/simpleiot/simpleiot/server"
)

type stateMachineTestResponse struct {
	ID           string                           `json:"id"`
	Revision     uint64                           `json:"revision"`
	CurrentState string                           `json:"current_state"`
	Execution    *decision.StateMachineExecution  `json:"execution,omitempty"`
	Simulation   *decision.StateMachineSimulation `json:"simulation,omitempty"`
}

func callStateMachineAPI(t *testing.T, h *Handler, identity Identity, method string, path []string, body any) (int, stateMachineTestResponse, string) {
	t.Helper()
	encoded, _ := json.Marshal(body)
	request := httptest.NewRequest(method, "/api/state-machines", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	h.stateMachines(recorder, request, identity, path)
	var response stateMachineTestResponse
	_ = json.Unmarshal(recorder.Body.Bytes(), &response)
	return recorder.Code, response, recorder.Body.String()
}

func TestStateMachineAPISimulateExecuteAndUnknown(t *testing.T) {
	opts := server.TestServerOptions
	opts.ID = "easyems-state-machine-api-test"
	opts.DataDir = filepath.Join(t.TempDir(), "data")
	opts.NatsPort = 19720
	opts.NatsServer = "nats://127.0.0.1:19720"
	opts.HTTPPort = "9720"
	opts.NatsMonitorPort = 19722
	opts.NatsWSPort = 19723
	opts.NatsMQTTPort = 0
	nc, root, stopServer, err := server.TestServerOpts(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer stopServer()
	for _, item := range []struct {
		id    string
		value float64
	}{{"permit", 1}, {"pump", 0}} {
		if err := siotclient.SendNode(nc, data.NodeEdge{ID: item.id, Type: data.NodeTypeVariable, Parent: root.ID,
			Points: data.Points{data.NewPointFloat(data.PointTypeValue, "", item.value)}}, "test"); err != nil {
			t.Fatal(err)
		}
	}
	h := New(nc, nil, new(atomic.Bool))
	identity := Identity{UserID: "admin", Policy: AccessPolicy{Admin: true}, Anchors: []string{root.ID}}
	definition := decision.StateMachineDefinition{
		InitialState: "stopped", Scope: "self", States: []string{"stopped", "running"},
		Transitions: []decision.StateTransition{{
			ID: "start", From: "stopped", Event: "start", To: "running",
			Conditions: []decision.Condition{{PointID: "permit", Operator: "eq", Kind: "bool", Value: 1}},
			Actions:    []decision.Action{{ActionID: "pump", Value: 1, Readback: &decision.Readback{TimeoutMS: 120}}},
		}, {
			ID: "stop", From: "running", Event: "stop", To: "stopped",
			Actions: []decision.Action{{ActionID: "pump", Value: 0, Readback: &decision.Readback{TimeoutMS: 120}}},
		}},
		Bindings: decision.BindingSet{
			Points:  map[string]decision.Target{"permit": {NodeID: "permit"}},
			Actions: map[string]decision.Target{"pump": {NodeID: "pump"}},
		},
	}
	status, created, body := callStateMachineAPI(t, h, identity, http.MethodPost, nil, map[string]any{
		"parent": root.ID, "name": "pump process", "definition": definition,
	})
	if status != 200 || created.ID == "" {
		t.Fatalf("create: %d %s", status, body)
	}
	status, simulated, body := callStateMachineAPI(t, h, identity, http.MethodPost, []string{created.ID, "simulate"}, map[string]any{
		"event": "start", "inputs": map[string]float64{"permit": 1},
	})
	if status != 200 || simulated.Simulation == nil || simulated.Simulation.Status != "matched" {
		t.Fatalf("simulate: %d %s", status, body)
	}
	if _, ok := stateMachinePoint(t, nc, "pump", data.PointTypeValueSet); ok {
		t.Fatal("simulation wrote a device command")
	}
	unsaved := definition
	unsaved.Transitions[0].Conditions[0].Value = 0
	status, simulated, body = callStateMachineAPI(t, h, identity, http.MethodPost, []string{created.ID, "simulate"}, map[string]any{
		"event": "start", "definition": unsaved, "inputs": map[string]float64{"permit": 1},
	})
	if status != 200 || simulated.Simulation == nil || simulated.Simulation.Status != "held" {
		t.Fatalf("unsaved definition was not used by pure simulation: %d %s", status, body)
	}
	status, published, body := callStateMachineAPI(t, h, identity, http.MethodPost, []string{created.ID, "publish"}, map[string]any{"revision": created.Revision})
	if status != 200 {
		t.Fatalf("publish: %d %s", status, body)
	}
	status, executed, body := callStateMachineAPI(t, h, identity, http.MethodPost, []string{created.ID, "execute"}, map[string]any{
		"event": "start", "revision": published.Revision, "confirm": true,
	})
	if status != 200 || executed.Execution == nil || executed.Execution.Status != "unknown" || executed.CurrentState != "stopped" {
		t.Fatalf("unconfirmed readback changed state or was misreported: %d %s", status, body)
	}
	if _, ok := stateMachinePoint(t, nc, "pump", data.PointTypeValueSet); !ok {
		t.Fatal("execution did not send the declared command")
	}

	go func() {
		time.Sleep(40 * time.Millisecond)
		point := data.NewPointFloat(data.PointTypeValue, "", 1)
		point.Origin = "test-readback"
		_ = siotclient.SendNodePoint(nc, "pump", point, true)
	}()
	status, executed, body = callStateMachineAPI(t, h, identity, http.MethodPost, []string{created.ID, "execute"}, map[string]any{
		"event": "start", "revision": executed.Revision, "confirm": true,
	})
	if status != 200 || executed.Execution == nil || executed.Execution.Status != "confirmed" || executed.CurrentState != "running" {
		t.Fatalf("confirmed readback did not commit transition: %d %s", status, body)
	}
	if len(executed.Execution.Actions) != 1 || executed.Execution.Actions[0].Reason != "" {
		t.Fatalf("successful action contains a spurious error: %+v", executed.Execution.Actions)
	}
	if err := siotclient.SendNode(nc, data.NodeEdge{ID: "nested", Type: data.NodeTypeGroup, Parent: root.ID}, "test"); err != nil {
		t.Fatal(err)
	}
	if err := siotclient.MoveNode(nc, "pump", root.ID, "nested", "test"); err != nil {
		t.Fatal(err)
	}
	status, _, body = callStateMachineAPI(t, h, identity, http.MethodPost, []string{created.ID, "execute"}, map[string]any{
		"event": "stop", "revision": executed.Revision, "confirm": true,
	})
	if status != 400 || !strings.Contains(body, "target_out_of_scope") {
		t.Fatalf("self scope survived a target move to a child: %d %s", status, body)
	}
}

func stateMachinePoint(t *testing.T, nc *nats.Conn, id, pointType string) (data.Point, bool) {
	t.Helper()
	nodes, err := siotclient.GetNodes(nc, "all", id, "", false)
	if err != nil || len(nodes) == 0 {
		t.Fatalf("get node: %v", err)
	}
	return nodes[0].Points.Find(pointType, "")
}
