package appapi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"fastiot/internal/decision"
	"fastiot/internal/emsapi"
	"github.com/nats-io/nats.go"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

func TestTodayExecutionsAncestorMachineTargetsAndUnassociatedEvents(t *testing.T) {
	f := newPointConfigFixture(t)
	root := snapshotRoot(t, f.base, f.token)
	a := createNodeAPI(t, f.base, f.token, "group", root, "Child A", 0)
	b := createNodeAPI(t, f.base, f.token, "group", root, "Child B", 0)
	left := createNodeAPI(t, f.base, f.token, "variable", a, "No software feedback A", 0)
	right := createNodeAPI(t, f.base, f.token, "variable", b, "No software feedback B", 0)
	definition := decision.StateMachineDefinition{Scope: "subtree", InitialState: "idle", States: []string{"idle"}, Transitions: []decision.StateTransition{{ID: "mixed", From: "idle", To: "idle", Event: "go", Actions: []decision.Action{{ActionID: "left", Value: 1, Readback: &decision.Readback{TimeoutMS: 60}}, {ActionID: "right", Value: 2, Readback: &decision.Readback{TimeoutMS: 60}}}}}, Bindings: decision.BindingSet{Actions: map[string]decision.Target{"left": {NodeID: left}, "right": {NodeID: right}}}}
	var machine stateMachineResponse
	must := func(path string, body any) {
		t.Helper()
		status, text := call(t, "POST", f.base+path, f.token, body, &machine)
		if status != 200 {
			t.Fatalf("%s: %d %s", path, status, text)
		}
	}
	must("/state-machines", map[string]any{"parent": root, "name": "Mixed ancestor process", "definition": definition})
	path := "/state-machines/" + machine.ID
	must(path+"/publish", map[string]any{"revision": machine.Revision})
	must(path+"/execute", map[string]any{"request_id": "machine-mixed", "event": "go", "revision": machine.Revision, "confirm": true})
	must(path+"/execute", map[string]any{"request_id": "machine-held", "event": "unmatched", "revision": machine.Revision, "confirm": true})
	query := func(layer string) (executionPage, string) {
		t.Helper()
		var page executionPage
		status, text := call(t, "GET", f.base+"/overview/executions?layer="+layer+"&timezone=UTC&source=machine", f.token, nil, &page)
		if status != 200 {
			t.Fatal(text)
		}
		return page, text
	}
	first, text := query(a)
	if first.Total != 1 || first.ExecutionCount != 1 || first.Items[0].ExecutionID != "machine-mixed" || first.Items[0].Status != "unknown" || len(first.Items[0].Actions) != 1 || first.Items[0].Actions[0].Target != left || strings.Contains(text, right) || strings.Contains(text, "machine-held") {
		t.Fatalf("ancestor machine guessed targets or exposed sibling: %s", text)
	}
	second, text := query(b)
	if second.Total != 1 || second.Items[0].ID != first.Items[0].ID || len(second.Items[0].Actions) != 1 || second.Items[0].Actions[0].Status != "skipped" || strings.Contains(text, left) {
		t.Fatalf("same event must be scoped once, not a new execution: %s", text)
	}
	definition.Bindings.Actions["left"] = decision.Target{NodeID: right}
	must(path+"/draft", map[string]any{"revision": machine.Revision, "definition": definition})
	must(path+"/publish", map[string]any{"revision": machine.Revision})
	page, text := query(a)
	if page.Total != 1 || page.Items[0].ID != first.Items[0].ID || page.Items[0].SourceRevision != "1" || page.Items[0].Actions[0].Target != left {
		t.Fatalf("current machine binding replaced historical target: %s", text)
	}
}

func TestTodayExecutionsAncestorTargetsUseRecordedBindings(t *testing.T) {
	f := newPointConfigFixture(t)
	f.handler.SetDataDir(f.dir)
	f.handler.SetEMS(emsapi.New(f.runtime, time.Second))
	WireCommandHistory(f.dir, f.runtime.Commands())
	root := snapshotRoot(t, f.base, f.token)
	parent := createNodeAPI(t, f.base, f.token, "site", root, "Parent strategies", 0)
	a := createNodeAPI(t, f.base, f.token, "group", parent, "Selected child", 0)
	b := createNodeAPI(t, f.base, f.token, "group", parent, "Sibling child", 0)
	outside := createNodeAPI(t, f.base, f.token, "group", root, "Outside viewer", 0)
	left := createNodeAPI(t, f.base, f.token, "variable", a, "Left software target", 0)
	right := createNodeAPI(t, f.base, f.token, "variable", b, "Right software target", 0)
	viewer := inputAccount(t, f, parent, "viewer", nil)
	for _, target := range []string{left, right} {
		target := target
		stop, err := client.SubscribePoints(f.handler.nc, target, func(points []data.Point) {
			for _, p := range points {
				if p.Type == data.PointTypeValueSet {
					v, _ := p.ValueFloat()
					_ = client.SendNodePoint(f.handler.nc, target, data.NewPointFloat(data.PointTypeValue, "", v), true)
				}
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		defer stop()
	}
	doc := decision.Document{Scope: "descendants", Table: decision.Table{Rows: []decision.Row{{ID: "mixed", Actions: []decision.Action{{ActionID: "left", Value: 1, Readback: &decision.Readback{TimeoutMS: 1000}}, {ActionID: "right", Value: 2, Readback: &decision.Readback{TimeoutMS: 1000}}}}}}, Bindings: decision.BindingSet{Actions: map[string]decision.Target{"left": {NodeID: left}, "right": {NodeID: right}}}}
	create := func(name string, d decision.Document) (string, decision.Document) {
		t.Helper()
		var result struct {
			ID       string `json:"id"`
			Revision string `json:"revision"`
		}
		status, text := call(t, "POST", f.base+"/rules", f.token, map[string]any{"parent": parent, "description": name, "document": d}, &result)
		if status != 200 {
			t.Fatal(text)
		}
		d.Revision = result.Revision
		status, text = call(t, "POST", f.base+"/rules/"+result.ID+"/publish", f.token, d, nil)
		if status != 200 {
			t.Fatal(text)
		}
		return result.ID, d
	}
	recipient := inboxAccount(t, f.base, f.token, parent, "ancestor-recipient@fixture.local", "private-ancestor-only")
	doc.Table.Rows[0].Actions = append(doc.Table.Rows[0].Actions, decision.Action{ActionID: "parent-notice", Notification: &decision.RuleNotification{RecipientIDs: []string{recipient}, Title: "Parent notification only"}})
	mixed, doc := create("Mixed ancestor", doc)
	siblingDoc := decision.Document{Scope: "descendants", Table: decision.Table{Rows: []decision.Row{{ID: "sibling", Actions: []decision.Action{{ActionID: "right", Value: 3, Readback: &decision.Readback{TimeoutMS: 1000}}}}}}, Bindings: decision.BindingSet{Actions: map[string]decision.Target{"right": {NodeID: right}}}}
	sibling, _ := create("Unrelated ancestor", siblingDoc)
	defer func() {
		if t.Failed() {
			for _, id := range []string{mixed, sibling} {
				nodes, _ := client.GetNodes(f.handler.nc, "all", id, "", false)
				if len(nodes) == 1 {
					for _, typ := range []string{"lastCheck", "error"} {
						text, _ := nodes[0].Points.Text(typ, "")
						t.Logf("%s %s: %s", id, typ, text)
					}
				}
			}
		}
	}()
	manager := client.NewManager(f.handler.nc, func(nc *nats.Conn, c decision.DecisionTable) client.Client { return f.handler.NewRuleClient(nc, c) }, RuleParentTypes())
	done := make(chan error, 1)
	go func() { done <- manager.Run() }()
	running := true
	defer func() {
		if running {
			manager.Stop(nil)
			<-done
		}
	}()
	for _, id := range []string{mixed, sibling} {
		status, text := call(t, "POST", f.base+"/rules/"+id+"/enable", f.token, map[string]bool{"enabled": true}, nil)
		if status != 200 {
			t.Fatal(text)
		}
		waitFor(t, func() bool {
			nodes, _ := client.GetNodes(f.handler.nc, "all", id, "", false)
			if len(nodes) != 1 {
				return false
			}
			raw, _ := nodes[0].Points.Text("lastCheck", "")
			var report decision.CheckReport
			_ = json.Unmarshal([]byte(raw), &report)
			if report.ExecutionID == "" || len(report.Actions) == 0 {
				return false
			}
			for _, action := range report.Actions {
				if action.Status != "confirmed" && action.Status != "created" {
					return false
				}
			}
			return true
		}, "actual ancestor software execution")
		status, text = call(t, "POST", f.base+"/rules/"+id+"/enable", f.token, map[string]bool{"enabled": false}, nil)
		if status != 200 {
			t.Fatal(text)
		}
	}
	query := f.base + "/overview/executions?layer=" + a + "&timezone=UTC"
	var page executionPage
	status, text := call(t, "GET", query, viewer, nil, &page)
	if status != 200 || page.ExecutionCount != 1 || page.Total != 1 || len(page.Items[0].Actions) != 1 || page.Items[0].Actions[0].Target != left || strings.Contains(text, right) || strings.Contains(text, sibling) || strings.Contains(text, "notification_id") {
		t.Fatalf("ancestor target query missing or disclosed sibling actions: %d %s", status, text)
	}
	executionID := page.Items[0].ExecutionID
	manager.Stop(nil)
	<-done
	running = false
	status, text = call(t, "POST", f.base+"/rules/"+mixed+"/enable", f.token, map[string]bool{"enabled": false}, nil)
	if status != 200 {
		t.Fatal(text)
	}
	doc.Bindings.Actions["left"] = decision.Target{NodeID: right}
	var saved struct {
		Revision string `json:"revision"`
	}
	status, text = call(t, "PUT", f.base+"/rules/"+mixed+"/draft", f.token, doc, &saved)
	if status != 200 {
		t.Fatal(text)
	}
	doc.Revision = saved.Revision
	status, text = call(t, "POST", f.base+"/rules/"+mixed+"/publish", f.token, doc, nil)
	if status != 200 {
		t.Fatal(text)
	}
	status, text = call(t, "GET", query, viewer, nil, &page)
	if status != 200 || page.ExecutionCount != 1 || page.Items[0].ExecutionID != executionID || page.Items[0].Actions[0].Target != left {
		t.Fatalf("new bindings reinterpreted old execution: %d %s", status, text)
	}
	if err := client.MoveNode(f.handler.nc, right, b, outside, "fixture"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !f.handler.users.IsUnder(right, parent) }, "revoke historical sibling target")
	status, text = call(t, "GET", query, viewer, nil, &page)
	if status != 200 || page.Total != 0 || !page.Restricted || strings.Contains(text, executionID) || strings.Contains(text, right) {
		t.Fatalf("historical ancestor permissions bypassed: %d %s", status, text)
	}
}
