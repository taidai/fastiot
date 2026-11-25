package appapi

import (
	"encoding/json"
	"github.com/gorilla/websocket"
	"net/http"
	"strings"
	"testing"
	"time"

	"fastiot/internal/decision"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

func TestStateMachineWriteResponseRechecksScopeDuringExecution(t *testing.T) {
	f := newPointConfigFixture(t)
	root := snapshotRoot(t, f.base, f.token)
	site := createNodeAPI(t, f.base, f.token, "site", root, "Visible site", 0)
	outside := createNodeAPI(t, f.base, f.token, "site", root, "Other site", 0)
	input := createNodeAPI(t, f.base, f.token, "variable", site, "Private input", 731)
	output := createNodeAPI(t, f.base, f.token, "variable", site, "Software output", 0)
	operator := inputAccount(t, f, site, "implementer", []string{ActionConfigure, ActionRulePublish, ActionCommand})
	definition := decision.StateMachineDefinition{InitialState: "idle", States: []string{"idle"}, Transitions: []decision.StateTransition{{ID: "move", From: "idle", To: "idle", Event: "start", Conditions: []decision.Condition{{PointID: "private", Operator: "eq", Value: 731}}, Actions: []decision.Action{{ActionID: "output", Value: 1, DelayMS: 2000, Readback: &decision.Readback{TimeoutMS: 100}}}}}, Bindings: decision.BindingSet{Points: map[string]decision.Target{"private": {NodeID: input}}, Actions: map[string]decision.Target{"output": {NodeID: output}}}}
	var machine stateMachineResponse
	status, text := call(t, "POST", f.base+"/state-machines", operator, map[string]any{"parent": site, "name": "Moving source", "definition": definition}, &machine)
	if status != 200 {
		t.Fatalf("create: %d %s", status, text)
	}
	path := f.base + "/state-machines/" + machine.ID
	status, text = call(t, "POST", path+"/publish", operator, map[string]any{"revision": machine.Revision}, &machine)
	if status != 200 {
		t.Fatalf("publish: %d %s", status, text)
	}
	type reply struct {
		status int
		text   string
	}
	done := make(chan reply, 1)
	go func() {
		defer close(done)
		status, text := call(t, "POST", path+"/execute", operator, map[string]any{"request_id": "moving-event", "event": "start", "revision": machine.Revision, "confirm": true}, nil)
		done <- reply{status, text}
	}()
	waitFor(t, func() bool {
		var current stateMachineResponse
		status, _ := call(t, "GET", path, f.token, nil, &current)
		return status == 200 && current.Pending != nil
	}, "durable intent before action delay")
	if err := client.MoveNode(f.handler.nc, input, site, outside, "fixture"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !f.handler.users.IsUnder(input, site) }, "revoked source before response")
	result := <-done
	var response stateMachineResponse
	if err := json.Unmarshal([]byte(result.text), &response); err != nil {
		t.Fatal(err)
	}
	if result.status != 200 || response.Execution != nil || response.LastExecution != nil || response.Restricted["execution"] == "" || strings.Contains(result.text, input) || strings.Contains(result.text, `"value":731`) {
		t.Fatalf("write response exposed newly revoked source: %d %s", result.status, result.text)
	}
	var admin stateMachineResponse
	status, text = call(t, "GET", path, f.token, nil, &admin)
	if status != 200 || admin.LastExecution == nil || admin.LastExecution.Status != "rejected" || admin.CurrentState != "idle" {
		t.Fatalf("response filtering changed actual outcome: %d %s", status, text)
	}
}

func TestStateMachineResponseScopeAfterReferenceMove(t *testing.T) {
	f := newPointConfigFixture(t)
	root := snapshotRoot(t, f.base, f.token)
	site := createNodeAPI(t, f.base, f.token, "site", root, "Visible site", 0)
	outside := createNodeAPI(t, f.base, f.token, "site", root, "Other site", 0)
	input := createNodeAPI(t, f.base, f.token, "variable", site, "Private input", 731)
	viewer := inputAccount(t, f, site, "viewer", nil)
	operator := inputAccount(t, f, site, "implementer", []string{ActionConfigure, ActionRulePublish, ActionCommand})
	definition := decision.StateMachineDefinition{InitialState: "idle", States: []string{"idle"}, Transitions: []decision.StateTransition{{ID: "held", From: "idle", To: "idle", Event: "start", Conditions: []decision.Condition{{PointID: "private", Operator: "eq", Value: 999}}}}, Bindings: decision.BindingSet{Points: map[string]decision.Target{"private": {NodeID: input}}}}
	var machine stateMachineResponse
	must := func(path, token string, body any) string {
		t.Helper()
		status, text := call(t, "POST", f.base+path, token, body, &machine)
		if status != 200 {
			t.Fatalf("%s: %d %s", path, status, text)
		}
		return text
	}
	must("/state-machines", operator, map[string]any{"parent": site, "name": "Scoped process", "definition": definition})
	path := "/state-machines/" + machine.ID
	must(path+"/publish", operator, map[string]any{"revision": machine.Revision})
	oldEvent := map[string]any{"request_id": "old-event", "event": "start", "revision": machine.Revision, "confirm": true}
	must(path+"/execute", operator, oldEvent)
	if machine.Execution == nil || machine.Execution.Status != "held" {
		t.Fatal("missing original held fact")
	}
	if err := client.MoveNode(f.handler.nc, input, site, outside, "fixture"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !f.handler.users.IsUnder(input, site) }, "current authority excludes moved input")
	assertSafe := func(text string) map[string]json.RawMessage {
		t.Helper()
		for _, secret := range []string{input, `"value":999`, `"private":731`, "private-actor", "private-pending"} {
			if strings.Contains(text, secret) {
				t.Fatalf("response disclosed revoked source %q: %s", secret, text)
			}
		}
		var response map[string]json.RawMessage
		if err := json.Unmarshal([]byte(text), &response); err != nil {
			t.Fatal(err)
		}
		if len(response["restricted"]) == 0 {
			t.Fatalf("restricted history was silently made absent: %s", text)
		}
		return response
	}
	status, text := call(t, "POST", f.base+path+"/simulate", viewer, map[string]any{"event": "start", "inputs": map[string]float64{"private": 731}}, nil)
	if status != 403 {
		t.Fatalf("revoked draft could still simulate and expose guards: %d %s", status, text)
	}
	for _, op := range []string{"publish", "execute"} {
		status, text := call(t, "POST", f.base+path+"/"+op, operator, map[string]any{"request_id": "denied-new-event", "event": "start", "revision": machine.Revision, "confirm": true}, nil)
		if status != 403 || strings.Contains(text, "private") {
			t.Fatalf("revoked %s exposed source validation details: %d %s", op, status, text)
		}
	}
	clean := decision.StateMachineDefinition{InitialState: "idle", States: []string{"idle"}, Transitions: []decision.StateTransition{{ID: "tick", From: "idle", To: "idle", Event: "tick"}}}
	assertSafe(must(path+"/draft", operator, map[string]any{"revision": machine.Revision, "definition": clean}))
	assertSafe(must(path+"/publish", operator, map[string]any{"revision": machine.Revision}))
	checkReads := func(pending bool) {
		t.Helper()
		for _, route := range []string{path, "/state-machines"} {
			status, text := call(t, "GET", f.base+route, viewer, nil, nil)
			if status != 200 {
				t.Fatalf("clean v2 denied: %d %s", status, text)
			}
			if route == "/state-machines" {
				var list struct {
					Machines []json.RawMessage `json:"state_machines"`
				}
				if err := json.Unmarshal([]byte(text), &list); err != nil || len(list.Machines) != 1 {
					t.Fatalf("list: %s", text)
				}
				text = string(list.Machines[0])
			}
			response := assertSafe(text)
			if string(response["version"]) != "2" || len(response["applied"]) == 0 {
				t.Fatalf("clean v2 lost: %s", text)
			}
			var versions map[string]json.RawMessage
			_ = json.Unmarshal(response["versions"], &versions)
			if len(versions["1"]) != 0 || len(versions["2"]) == 0 {
				t.Fatalf("version scope: %s", text)
			}
			var restricted map[string]string
			_ = json.Unmarshal(response["restricted"], &restricted)
			if restricted["versions/1"] == "" {
				t.Fatalf("hidden version falsely appears nonexistent: %s", text)
			}
			if !pending && (len(response["last_execution"]) != 0 || restricted["last_execution"] == "") {
				t.Fatalf("historical trace disclosure/status: %s", text)
			}
			if pending && (len(response["pending"]) != 0 || !strings.Contains(string(response["restricted"]), "pending")) {
				t.Fatalf("pending disclosure/status: %s", text)
			}
		}
	}
	checkReads(false)
	status, text = call(t, "POST", f.base+path+"/execute", operator, oldEvent, nil)
	if status != 403 {
		t.Fatalf("old execution replay escaped current scope: %d %s", status, text)
	}
	cleanEvent := map[string]any{"request_id": "clean-event", "event": "tick", "revision": machine.Revision, "confirm": true}
	assertSafe(must(path+"/execute", operator, cleanEvent))
	run := stateMachineRun{RequestID: "private-pending", Actor: "private-actor", Revision: machine.Revision, Source: definition, Inputs: map[string]float64{"private": 731}, Execution: decision.StateMachineExecution{RequestID: "private-pending", Version: 1, At: time.Now(), Event: "start", From: "idle", To: "idle", Status: "pending"}}
	if err := f.handler.storeMachineRun(machine.ID, run); err != nil {
		t.Fatal(err)
	}
	checkReads(true)
	assertSafe(must(path+"/execute", operator, cleanEvent)) // safe replay must filter the rest of its envelope too
	assertSafe(must(path+"/simulate", operator, map[string]any{"event": "tick"}))
	status, text = call(t, "POST", f.base+path+"/execute", operator, map[string]any{"request_id": "new-event", "event": "tick", "revision": machine.Revision, "confirm": true}, nil)
	if status != 409 || !strings.Contains(text, "previous_execution_unresolved") {
		t.Fatalf("filter changed persisted pending gate: %d %s", status, text)
	}
	var admin stateMachineResponse
	status, text = call(t, "GET", f.base+path, f.token, nil, &admin)
	if status != 200 || admin.Versions[1].Bindings.Points["private"].NodeID != input || admin.Pending == nil || admin.Pending.Inputs["private"] != 731 || admin.Pending.Actor != "private-actor" || admin.Pending.Finished {
		t.Fatalf("restricted projection changed original facts or admin view: %d %s", status, text)
	}
	var snapshot struct {
		Nodes []data.NodeEdge `json:"nodes"`
	}
	status, text = call(t, "GET", f.base+"/snapshot", viewer, nil, &snapshot)
	if status != 200 {
		t.Fatalf("snapshot: %d %s", status, text)
	}
	checkSnapshot := func(nodes []data.NodeEdge) {
		t.Helper()
		found := false
		for _, node := range nodes {
			if node.ID == input {
				t.Fatal("fixture did not revoke input visibility")
			}
			if node.ID != machine.ID {
				continue
			}
			found = true
			marker, marked := node.Points.Text("stateMachineDetails", "")
			if !marked || marker != "restricted_to_scoped_api" {
				t.Fatal("raw projection hid details without an explicit scoped-API marker")
			}
			for _, point := range append(append(data.Points{}, node.Points...), node.EdgePoints...) {
				if point.Type == stateMachineDocumentPoint || point.Type == stateMachineRunPoint {
					t.Fatalf("raw projection bypasses scoped machine API: %s", point.Type)
				}
			}
		}
		if !found {
			t.Fatal("authorized machine metadata disappeared from raw projection")
		}
	}
	checkSnapshot(snapshot.Nodes)
	ws, _, err := websocket.DefaultDialer.Dial(strings.Replace(f.base, "http://", "ws://", 1)+"/ws", http.Header{"Authorization": []string{"Bearer " + viewer}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	snapshot.Nodes = nil
	if err := ws.ReadJSON(&snapshot); err != nil {
		t.Fatal(err)
	}
	checkSnapshot(snapshot.Nodes)
	// Snapshot/WebSocket projection also must not rewrite the shared ledger.
	status, text = call(t, "GET", f.base+path, f.token, nil, &admin)
	if status != 200 || admin.Pending == nil || admin.Pending.Inputs["private"] != 731 || admin.Versions[1].Bindings.Points["private"].NodeID != input {
		t.Fatalf("raw projection mutated original evidence: %d %s", status, text)
	}
}
