package appapi

import (
	"encoding/json"
	"github.com/gorilla/websocket"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"fastiot/internal/decision"
	"github.com/nats-io/nats.go"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

func TestTodayExecutionsLatestCheckDoesNotInventExecutionIdentity(t *testing.T) {
	base, admin, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, admin)
	recipient := inboxAccount(t, base, admin, root, "held@fixture.local", "private-fixture")
	document := decision.Document{Scope: "self", Table: decision.Table{Rows: []decision.Row{{ID: "held", HoldMS: 60000, Actions: []decision.Action{{ActionID: "notice", Notification: &decision.RuleNotification{RecipientIDs: []string{recipient}, Title: "Not fired"}}}}}}}
	var created struct {
		ID       string `json:"id"`
		Revision string `json:"revision"`
	}
	status, text := call(t, "POST", base+"/rules", admin, map[string]any{"parent": root, "description": "Held observation", "document": document}, &created)
	if status != 200 {
		t.Fatal(text)
	}
	document.Revision = created.Revision
	status, text = call(t, "POST", base+"/rules/"+created.ID+"/publish", admin, document, nil)
	if status != 200 {
		t.Fatal(text)
	}
	manager := client.NewManager(h.nc, func(nc *nats.Conn, c decision.DecisionTable) client.Client { return h.NewRuleClient(nc, c) }, RuleParentTypes())
	done := make(chan error, 1)
	go func() { done <- manager.Run() }()
	defer func() { manager.Stop(nil); <-done }()
	status, text = call(t, "POST", base+"/rules/"+created.ID+"/enable", admin, map[string]bool{"enabled": true}, nil)
	if status != 200 {
		t.Fatal(text)
	}
	var page executionPage
	waitFor(t, func() bool {
		status, _ := call(t, "GET", base+"/overview/executions?layer="+root+"&timezone=UTC", admin, nil, &page)
		return status == 200 && len(page.Items) == 1
	}, "latest held evaluation")
	if page.ExecutionCount != 0 || page.Coverage.Complete || page.Items[0].Counted || page.Items[0].ExecutionID != "" || len(page.Items[0].Actions) != 1 || page.Items[0].Actions[0].ID != "" {
		t.Fatalf("latest-only observation invented execution identity: %+v", page)
	}
}

func TestTodayExecutionsRuleCaptureSurvivesProjectionLossAndRestart(t *testing.T) {
	dir, password := t.TempDir(), data.NewID()
	base, admin, h, stop := startInputServer(t, dir, password)
	root := snapshotRoot(t, base, admin)
	scope := createNodeAPI(t, base, admin, "site", root, "Rule capture", 0)
	recipient := inboxAccount(t, base, admin, scope, "today@fixture.local", password)
	document := decision.Document{Scope: "self", Table: decision.Table{Rows: []decision.Row{{ID: "notice", Actions: []decision.Action{{ActionID: "notice", Notification: &decision.RuleNotification{RecipientIDs: []string{recipient}, Title: "Private test notice"}}}}}}}
	var created struct {
		ID       string `json:"id"`
		Revision string `json:"revision"`
	}
	must := func(path string, body any) {
		t.Helper()
		status, text := call(t, "POST", base+path, admin, body, &created)
		if status != 200 {
			t.Fatalf("%s: %d %s", path, status, text)
		}
	}
	must("/rules", map[string]any{"parent": scope, "description": "Today notice", "document": document})
	id := created.ID
	document.Revision = created.Revision
	must("/rules/"+id+"/publish", document)
	startManager := func() func() {
		m := client.NewManager(h.nc, func(nc *nats.Conn, c decision.DecisionTable) client.Client { return h.NewRuleClient(nc, c) }, RuleParentTypes())
		done := make(chan error, 1)
		go func() { done <- m.Run() }()
		return func() { m.Stop(nil); <-done }
	}
	stopManager := startManager()
	defer func() {
		if stopManager != nil {
			stopManager()
		}
	}()
	must("/rules/"+id+"/enable", map[string]bool{"enabled": true})
	waitFor(t, func() bool {
		nodes, _ := client.GetNodes(h.nc, "all", id, "", false)
		if len(nodes) == 0 {
			return false
		}
		text, _ := nodes[0].Points.Text("lastCheck", "")
		var report decision.CheckReport
		_ = json.Unmarshal([]byte(text), &report)
		return len(report.Actions) == 1 && report.Actions[0].Status == "created"
	}, "real rule receipt")
	query := func() executionPage {
		t.Helper()
		var page executionPage
		status, text := call(t, "GET", base+"/overview/executions?layer="+scope+"&timezone=UTC&source=rule", admin, nil, &page)
		if status != 200 {
			t.Fatalf("query: %d %s", status, text)
		}
		return page
	}
	page := query()
	if page.ExecutionCount != 1 || len(page.Items) != 1 || len(page.Items[0].Actions) != 1 || page.Items[0].Actions[0].NotificationID == "" {
		t.Fatalf("real rule execution not captured: %+v", page)
	}
	original := page.Items[0]
	stopManager()
	stopManager = nil
	if err := client.SendNodePoint(h.nc, id, data.NewPointString("lastCheck", "", ""), true); err != nil {
		t.Fatal(err)
	}
	page = query()
	if page.ExecutionCount != 1 || page.Items[0].ID != original.ID || !page.Items[0].At.Equal(original.At) {
		t.Fatalf("latest projection erased immutable execution: %+v", page)
	}
	_, snapshot := call(t, "GET", base+"/snapshot", admin, nil, nil)
	if strings.Contains(snapshot, "ruleExecutionFact") {
		t.Fatal("raw execution facts escaped snapshot")
	}
	ws, _, err := websocket.DefaultDialer.Dial(strings.Replace(base, "http://", "ws://", 1)+"/ws", http.Header{"Authorization": []string{"Bearer " + admin}})
	if err != nil {
		t.Fatal(err)
	}
	_, message, err := ws.ReadMessage()
	ws.Close()
	if err != nil || strings.Contains(string(message), "ruleExecutionFact") {
		t.Fatalf("raw execution facts escaped websocket: %v", err)
	}
	stop()
	base, admin, h, _ = startInputServer(t, dir, password)
	stopManager = startManager()
	page = query()
	if page.ExecutionCount != 1 || page.Total != 1 || page.Items[0].ID != original.ID || page.Items[0].Actions[0].NotificationID != original.Actions[0].NotificationID || page.Items[0].Actions[0].Status != "created" {
		t.Fatalf("restart changed counted fact: %+v", page)
	}
	stopManager()
	stopManager = nil
	outside := createNodeAPI(t, base, admin, "site", root, "New mount", 0)
	f := &pointConfigFixture{base: base, token: admin, handler: h}
	viewer := inputAccount(t, f, outside, "viewer", nil)
	if err := client.MoveNode(h.nc, id, scope, outside, "fixture"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return h.users.IsUnder(id, outside) }, "source moved but old receipt scope unchanged")
	status, text := call(t, "GET", base+"/overview/executions?layer="+outside+"&timezone=UTC", viewer, nil, &page)
	if status != 200 || page.Total != 0 || !page.Restricted || strings.Contains(text, original.Actions[0].NotificationID) {
		t.Fatalf("old receipt scope bypassed by new mount: %d %s", status, text)
	}
}

func TestTodayExecutionsUnknownAndCurrentHistoricalScope(t *testing.T) {
	f := newPointConfigFixture(t)
	root := snapshotRoot(t, f.base, f.token)
	scope := createNodeAPI(t, f.base, f.token, "site", root, "Visible", 0)
	outside := createNodeAPI(t, f.base, f.token, "site", root, "Other", 0)
	output := createNodeAPI(t, f.base, f.token, "variable", scope, "Software only", 0)
	feedback := createNodeAPI(t, f.base, f.token, "variable", scope, "Missing process feedback", 0)
	viewer := inputAccount(t, f, scope, "viewer", nil)
	definition := decision.StateMachineDefinition{Scope: "self", InitialState: "idle", States: []string{"idle", "done"}, Transitions: []decision.StateTransition{{ID: "start", From: "idle", To: "done", Event: "start", Actions: []decision.Action{{ActionID: "write", Value: 1, Readback: &decision.Readback{TimeoutMS: 60, Target: decision.Target{NodeID: feedback}}}}}}, Bindings: decision.BindingSet{Actions: map[string]decision.Target{"write": {NodeID: output}}}}
	var machine stateMachineResponse
	must := func(path string, body any) {
		t.Helper()
		status, text := call(t, "POST", f.base+path, f.token, body, &machine)
		if status != 200 {
			t.Fatalf("%s: %d %s", path, status, text)
		}
	}
	must("/state-machines", map[string]any{"parent": scope, "name": "Unknown process", "definition": definition})
	path := "/state-machines/" + machine.ID
	must(path+"/publish", map[string]any{"revision": machine.Revision})
	must(path+"/execute", map[string]any{"request_id": "unknown-event", "event": "start", "revision": machine.Revision, "confirm": true})
	var page executionPage
	query := f.base + "/overview/executions?layer=" + scope + "&timezone=UTC"
	status, text := call(t, "GET", query, viewer, nil, &page)
	if status != 200 || page.Total != 1 || page.Items[0].Status != "unknown" || len(page.Items[0].Actions) != 1 || page.Items[0].Actions[0].CanonicalStatus != "unknown" || page.Items[0].Actions[0].Status != "unknown" {
		t.Fatalf("unknown process promoted by software command: %d %s", status, text)
	}
	if err := client.MoveNode(f.handler.nc, feedback, scope, outside, "fixture"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !f.handler.users.IsUnder(feedback, scope) }, "historical feedback scope revoked")
	status, text = call(t, "GET", query, viewer, nil, &page)
	if status != 200 || page.Total != 0 || !page.Restricted || strings.Contains(text, output) || strings.Contains(text, "unknown-event") {
		t.Fatalf("historical scope leaked: %d %s", status, text)
	}
	status, text = call(t, "GET", query, f.token, nil, &page)
	if status != 200 || page.Total != 1 || page.Items[0].Status != "unknown" {
		t.Fatalf("scope projection mutated immutable fact: %d %s", status, text)
	}
	status, _ = call(t, "GET", f.base+"/overview/executions?layer="+outside+"&timezone=UTC", viewer, nil, nil)
	if status != 403 {
		t.Fatalf("forbidden layer: %d", status)
	}
	for _, suffix := range []string{"&offset=-1", "&limit=101", "&date=not-a-date", "&source=bogus"} {
		status, text = call(t, "GET", query+suffix, f.token, nil, nil)
		if status != 400 {
			t.Fatalf("invalid query %s: %d %s", suffix, status, text)
		}
	}
}

func TestTodayExecutionsMachinePaginationAndRetry(t *testing.T) {
	f := newPointConfigFixture(t)
	root := snapshotRoot(t, f.base, f.token)
	definition := decision.StateMachineDefinition{InitialState: "idle", States: []string{"idle"}, Transitions: []decision.StateTransition{{ID: "tick", From: "idle", To: "idle", Event: "tick"}}}
	var machine stateMachineResponse
	must := func(path string, body any) {
		t.Helper()
		status, text := call(t, "POST", f.base+path, f.token, body, &machine)
		if status != 200 {
			t.Fatalf("%s: %d %s", path, status, text)
		}
	}
	must("/state-machines", map[string]any{"parent": root, "name": "Today process", "definition": definition})
	path := "/state-machines/" + machine.ID
	must(path+"/publish", map[string]any{"revision": machine.Revision})
	event := map[string]any{"request_id": "first-event", "event": "tick", "revision": machine.Revision, "confirm": true}
	must(path+"/execute", event)
	must(path+"/execute", event)
	must(path+"/execute", map[string]any{"request_id": "held-event", "event": "unmatched", "revision": machine.Revision, "confirm": true})
	var page struct {
		Items []struct {
			ID          string `json:"id"`
			ExecutionID string `json:"execution_id"`
			SourceID    string `json:"source_id"`
			Status      string `json:"status"`
		} `json:"items"`
		Total          int `json:"total"`
		ExecutionCount int `json:"execution_count"`
	}
	query := f.base + "/overview/executions?layer=" + url.QueryEscape(root) + "&date=" + time.Now().UTC().Format("2006-01-02") + "&timezone=UTC&limit=1"
	status, text := call(t, "GET", query, f.token, nil, &page)
	if status != 200 || page.Total != 2 || page.ExecutionCount != 2 || len(page.Items) != 1 {
		t.Fatalf("unified durable executions unavailable: %d %s", status, text)
	}
	first := page.Items[0].ID
	status, text = call(t, "GET", query+"&offset=1", f.token, nil, &page)
	if status != 200 || len(page.Items) != 1 || page.Items[0].ID == first || page.Items[0].SourceID != machine.ID {
		t.Fatalf("pagination: %d %s", status, text)
	}
	status, text = call(t, "GET", query+"&offset=2", f.token, nil, &page)
	if status != 200 || len(page.Items) != 0 {
		t.Fatalf("end page: %d %s", status, text)
	}
	status, text = call(t, "GET", f.base+"/overview/executions?layer="+root+"&date=2000-01-01&timezone=UTC", f.token, nil, &page)
	if status != 200 || page.Total != 0 {
		t.Fatalf("date filter: %d %s", status, text)
	}
}
