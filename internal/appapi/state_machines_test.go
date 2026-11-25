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
	"github.com/simpleiot/simpleiot/api"
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
	var authority api.UserAuthority
	opts.AppHandlerFactory = func(nc *nats.Conn, users api.UserAuthority) http.Handler {
		authority = users
		return New(nc, users, new(atomic.Bool))
	}
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
	h := New(nc, authority, new(atomic.Bool))
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
		"event": "start", "revision": published.Revision, "confirm": true, "request_id": "start-unknown",
	})
	if status != 200 || executed.Execution == nil || executed.Execution.Status != "unknown" || executed.CurrentState != "stopped" {
		t.Fatalf("unconfirmed readback changed state or was misreported: %d %s", status, body)
	}
	if _, ok := stateMachinePoint(t, nc, "pump", data.PointTypeValueSet); !ok {
		t.Fatal("execution did not send the declared command")
	}

	beforeRetry, _ := stateMachinePoint(t, nc, "pump", data.PointTypeValueSet)
	h = New(nc, authority, new(atomic.Bool))
	status, replay, body := callStateMachineAPI(t, h, identity, http.MethodPost, []string{created.ID, "execute"}, map[string]any{
		"event": "start", "revision": published.Revision, "confirm": true, "request_id": "start-unknown",
	})
	afterRetry, _ := stateMachinePoint(t, nc, "pump", data.PointTypeValueSet)
	if status != 200 || replay.Execution == nil || replay.Execution.Status != "unknown" || !beforeRetry.Time.Equal(afterRetry.Time) {
		t.Fatalf("unknown retry dispatched again after handler restart: %d %s", status, body)
	}

	if err := siotclient.SendNodePoint(nc, "pump", data.NewPointFloat("easyemsManual", "", 1), true); err != nil {
		t.Fatal(err)
	}
	status, refused, body := callStateMachineAPI(t, h, identity, http.MethodPost, []string{created.ID, "execute"}, map[string]any{
		"event": "start", "revision": executed.Revision, "confirm": true, "request_id": "manual-refusal",
	})
	afterRefusal, _ := stateMachinePoint(t, nc, "pump", data.PointTypeValueSet)
	if status != 200 || refused.Execution == nil || refused.Execution.Status != "rejected" || !afterRefusal.Time.Equal(afterRetry.Time) || refused.CurrentState != "stopped" {
		t.Fatalf("state machine bypassed manual takeover: %d %s", status, body)
	}
	executed = refused
	if err := siotclient.SendNodePoint(nc, "pump", data.NewPointFloat("easyemsManual", "", 0), true); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(40 * time.Millisecond)
		point := data.NewPointFloat(data.PointTypeValue, "", 1)
		point.Origin = "test-readback"
		_ = siotclient.SendNodePoint(nc, "pump", point, true)
	}()
	status, executed, body = callStateMachineAPI(t, h, identity, http.MethodPost, []string{created.ID, "execute"}, map[string]any{
		"event": "start", "revision": executed.Revision, "confirm": true, "request_id": "start-confirmed",
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
		"event": "stop", "revision": executed.Revision, "confirm": true, "request_id": "stop-outside",
	})
	if status != 400 || !strings.Contains(body, "target_out_of_scope") {
		t.Fatalf("self scope survived a target move to a child: %d %s", status, body)
	}
}

func TestStateMachineEventIdentitySurvivesLaterEventsAndHandlerRestart(t *testing.T) {
	opts := server.TestServerOptions
	var authority api.UserAuthority
	opts.AppHandlerFactory = func(nc *nats.Conn, users api.UserAuthority) http.Handler {
		authority = users
		return New(nc, users, new(atomic.Bool))
	}
	opts.ID = "ticket14-event-identity"
	opts.DataDir = filepath.Join(t.TempDir(), "store")
	opts.NatsPort, opts.HTTPPort = 19730, "9730"
	opts.NatsServer = "nats://127.0.0.1:19730"
	opts.NatsMonitorPort, opts.NatsWSPort, opts.NatsMQTTPort = 19732, 19733, 0
	nc, root, stop, err := server.TestServerOpts(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { stop() }()
	h := New(nc, authority, new(atomic.Bool))
	identity := Identity{UserID: "operator", Policy: AccessPolicy{Admin: true}, Anchors: []string{root.ID}}
	definition := decision.StateMachineDefinition{InitialState: "idle", States: []string{"idle", "active"}, Transitions: []decision.StateTransition{
		{ID: "start", From: "idle", Event: "start", To: "active"},
		{ID: "stop", From: "active", Event: "stop", To: "idle"},
	}}
	status, created, body := callStateMachineAPI(t, h, identity, "POST", nil, map[string]any{"parent": root.ID, "name": "logical-only", "definition": definition})
	if status != 200 {
		t.Fatalf("create: %d %s", status, body)
	}
	status, published, body := callStateMachineAPI(t, h, identity, "POST", []string{created.ID, "publish"}, map[string]any{"revision": created.Revision})
	if status != 200 {
		t.Fatalf("publish: %d %s", status, body)
	}
	start := map[string]any{"event": "start", "revision": published.Revision, "request_id": "event-start", "confirm": true}
	status, started, body := callStateMachineAPI(t, h, identity, "POST", []string{created.ID, "execute"}, start)
	if status != 200 || started.CurrentState != "active" {
		t.Fatalf("start: %d %s", status, body)
	}
	status, stopped, body := callStateMachineAPI(t, h, identity, "POST", []string{created.ID, "execute"}, map[string]any{"event": "stop", "revision": started.Revision, "request_id": "event-stop", "confirm": true})
	if status != 200 || stopped.CurrentState != "idle" {
		t.Fatalf("stop: %d %s", status, body)
	}
	stop()
	nc, root, stop, err = server.TestServerOptsKeepStore(opts)
	if err != nil {
		t.Fatal(err)
	}
	h = New(nc, authority, new(atomic.Bool))
	status, retried, body := callStateMachineAPI(t, h, identity, "POST", []string{created.ID, "execute"}, start)
	if status != 200 || retried.Revision != stopped.Revision || retried.CurrentState != "idle" || retried.Execution == nil || retried.Execution.Event != "start" || retried.Execution.To != "active" {
		t.Fatalf("old event must return its original fact without advancing current process state: %d %s", status, body)
	}
	start["event"] = "stop"
	status, _, body = callStateMachineAPI(t, h, identity, "POST", []string{created.ID, "execute"}, start)
	if status != 409 || !strings.Contains(body, "request_id_conflict") {
		t.Fatalf("identity conflict: %d %s", status, body)
	}
	statuses := make(chan int, 2)
	for range 2 {
		go func() {
			code, _, _ := callStateMachineAPI(t, h, identity, "POST", []string{created.ID, "draft"}, map[string]any{"revision": stopped.Revision, "definition": definition})
			statuses <- code
		}()
	}
	first, second := <-statuses, <-statuses
	if !((first == 200 && second == 409) || (first == 409 && second == 200)) {
		t.Fatalf("concurrent revisions both committed: %d %d", first, second)
	}
	// State lifecycle actions are not allowed to escape the machine scope.
	outside := data.NodeEdge{ID: "outside-output", Type: data.NodeTypeVariable, Parent: root.ID, Points: data.Points{data.NewPointFloat(data.PointTypeValue, "", 0)}}
	if err := siotclient.SendNode(nc, outside, "test"); err != nil {
		t.Fatal(err)
	}
	if err := siotclient.SendNode(nc, data.NodeEdge{ID: "machine-scope", Type: data.NodeTypeGroup, Parent: root.ID}, "test"); err != nil {
		t.Fatal(err)
	}
	bad := definition
	bad.Bindings.Actions = map[string]decision.Target{"outside": {NodeID: outside.ID}}
	bad.StateActions = map[string]decision.StateActions{"active": {Entry: []decision.Action{{ActionID: "outside", Value: 1, Readback: &decision.Readback{TimeoutMS: 100}}}}}
	status, _, body = callStateMachineAPI(t, h, identity, "POST", nil, map[string]any{"parent": "machine-scope", "name": "invalid", "definition": bad})
	if status != 400 || !strings.Contains(body, "target_out_of_scope") {
		t.Fatalf("entry action escaped scope: %d %s", status, body)
	}
}

func TestStateMachineReuseKeepsDefinitionsNotExecutionIdentity(t *testing.T) {
	definition := decision.StateMachineDefinition{InitialState: "idle", States: []string{"idle"},
		CommandBindings: map[string]decision.StateMachineCommandBinding{"set": {DeviceID: "old-device", CommandID: "set"}},
		StateActions:    map[string]decision.StateActions{"idle": {Entry: []decision.Action{{ActionID: "set", Readback: &decision.Readback{Target: decision.Target{NodeID: "old-feedback"}, TimeoutMS: 100}}}}},
	}
	document := decision.StateMachineDocument{Schema: 1, Revision: 3, Version: 1, Draft: definition, Applied: &definition, Versions: map[uint64]decision.StateMachineDefinition{1: definition}, CurrentState: "active", LastExecution: &decision.StateMachineExecution{RequestID: "old-event"}}
	encoded, _ := json.Marshal(document)
	node := data.NodeEdge{ID: "machine", Type: stateMachineType, Points: data.Points{data.NewPointString(stateMachineDocumentPoint, "", string(encoded)), data.NewPointString(stateMachineRunPoint, "old-run", "private-runtime")}}
	idMap := map[string]string{"old-device": "new-device", "old-feedback": "new-feedback"}
	for _, copy := range []struct {
		name   string
		points data.Points
	}{
		{"package", packagePoints(node, idMap, new([]packageStrip), new([]string))},
		{"reuse", func() data.Points {
			points := copyConfigPoints(node)
			rewritten, _ := rewriteReferences(node, idMap)
			return append(points, rewritten...)
		}()},
	} {
		if len(copy.points) != 1 || copy.points[0].Type != stateMachineDocumentPoint {
			t.Fatalf("%s copied runtime identity: %+v", copy.name, copy.points)
		}
		var got decision.StateMachineDocument
		if err := json.Unmarshal([]byte(copy.points[0].Txt()), &got); err != nil {
			t.Fatal(err)
		}
		if got.CurrentState != "idle" || got.LastExecution != nil || got.Draft.CommandBindings["set"].DeviceID != "new-device" || got.Versions[1].StateActions["idle"].Entry[0].Readback.Target.NodeID != "new-feedback" {
			t.Fatalf("%s retained old control refs or state: %+v", copy.name, got)
		}
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
