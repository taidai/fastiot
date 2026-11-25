package appapi

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"fastiot/internal/emsapi"

	"fastiot/internal/decision"
)

func TestStateMachineProjectRollbackPreservesCurrentProcessAndRejectsIncompatibleDefinition(t *testing.T) {
	f := newPointConfigFixture(t)
	f.handler.SetDataDir(f.dir)
	f.handler.SetEMS(emsapi.New(f.runtime, time.Second))
	root := snapshotRoot(t, f.base, f.token)
	site := createNodeAPI(t, f.base, f.token, "site", root, "Machine version fixture", 0)
	definition := decision.StateMachineDefinition{InitialState: "idle", States: []string{"idle"}}
	var machine stateMachineResponse
	must := func(path string, body any, result any) {
		t.Helper()
		status, response := call(t, "POST", f.base+path, f.token, body, result)
		if status != 200 {
			t.Fatalf("%s: %d %s", path, status, response)
		}
	}
	must("/state-machines", map[string]any{"parent": site, "name": "Process memory", "definition": definition}, &machine)
	path := "/state-machines/" + machine.ID
	must(path+"/publish", map[string]any{"revision": machine.Revision}, &machine)
	var incompatible versionSummary
	must("/projects/publish", map[string]any{"site": site, "confirm": true}, &incompatible)
	definition.States = []string{"idle", "active"}
	definition.Transitions = []decision.StateTransition{{ID: "start", From: "idle", Event: "start", To: "active"}}
	must(path+"/draft", map[string]any{"revision": machine.Revision, "definition": definition}, &machine)
	must(path+"/publish", map[string]any{"revision": machine.Revision}, &machine)
	var compatible versionSummary
	must("/projects/publish", map[string]any{"site": site, "confirm": true}, &compatible)
	event := map[string]any{"revision": machine.Revision, "request_id": "pre-rollback-event", "event": "start", "confirm": true}
	must(path+"/execute", event, &machine)
	version, revision := machine.Version, machine.Revision
	must("/projects/rollback", map[string]any{"site": site, "version": compatible.ID}, nil)
	status, body := call(t, http.MethodGet, f.base+path, f.token, nil, &machine)
	if status != 200 || machine.CurrentState != "active" || machine.LastExecution == nil || machine.LastExecution.RequestID != "pre-rollback-event" || machine.Version <= version || machine.Revision <= revision {
		t.Fatalf("rollback restored historical process state/identity: %d %s", status, body)
	}
	must(path+"/execute", event, &machine)
	if machine.CurrentState != "active" {
		t.Fatal("old event retried after rollback advanced process")
	}
	status, body = call(t, "POST", f.base+"/projects/rollback", f.token, map[string]any{"site": site, "version": incompatible.ID}, nil)
	if status != 409 || !strings.Contains(body, "state_machine_current_state_not_defined") {
		t.Fatalf("incompatible process version silently applied: %d %s", status, body)
	}
}
