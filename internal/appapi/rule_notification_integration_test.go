package appapi

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"fastiot/internal/decision"
	"fastiot/internal/notification"
	"github.com/nats-io/nats.go"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

func TestRuleNotificationPackageRebindsLocalAccounts(t *testing.T) {
	document := decision.Document{Scope: "self", Revision: "v1", Table: decision.Table{Rows: []decision.Row{{ID: "r", Actions: []decision.Action{{ActionID: "n", Notification: &decision.RuleNotification{RecipientIDs: []string{"local-account"}, Title: "Keep engineering text"}}}}}}}
	raw, _ := json.Marshal(document)
	point, keep, unresolved := rewritePackagePoint(data.NodeEdge{Type: "decisionTable"}, data.NewPointString("draft", "0", string(raw)), map[string]string{})
	if !keep || point == nil || len(unresolved) != 1 || unresolved[0] != "local-account" {
		t.Fatalf("account rebinding not reported: %v %v", keep, unresolved)
	}
	var copied decision.Document
	if err := json.Unmarshal([]byte(point.Txt()), &copied); err != nil {
		t.Fatal(err)
	}
	notice := copied.Table.Rows[0].Actions[0].Notification
	if notice.Title != "Keep engineering text" || len(notice.RecipientIDs) != 0 || copied.Revision != "v1" || copied.Validate() == nil {
		t.Fatalf("copy must preserve definition but refuse execution until rebind: %+v", copied)
	}
}

func TestRuleNotificationExecutesIntoDurableScopedInbox(t *testing.T) {
	dir, password := t.TempDir(), data.NewID()
	base, admin, h, stop := startInputServer(t, dir, password)
	root := snapshotRoot(t, base, admin)
	scope := createNodeAPI(t, base, admin, "site", root, "Notification scope", 0)
	other := createNodeAPI(t, base, admin, "site", root, "Other scope", 0)
	aliceID := inboxAccount(t, base, admin, scope, "alice@example.test", password)
	inboxAccount(t, base, admin, scope, "bob@example.test", password)
	outsideID := inboxAccount(t, base, admin, other, "outside@example.test", password)
	alice, bob := loginAPI(t, base, "alice@example.test", password), loginAPI(t, base, "bob@example.test", password)
	document := decision.Document{Scope: "self", Table: decision.Table{Rows: []decision.Row{{ID: "notice", Actions: []decision.Action{{ActionID: "notice", Notification: &decision.RuleNotification{RecipientIDs: []string{aliceID}, Title: "Check unit", Body: "Literal notice"}}}}}}}
	var created struct {
		ID       string `json:"id"`
		Revision string `json:"revision"`
	}
	status, body := call(t, "POST", base+"/rules", admin, map[string]any{"parent": scope, "description": "Notify only", "document": document}, &created)
	if status != 200 {
		t.Fatalf("create: %d %s", status, body)
	}
	document.Revision = created.Revision
	status, body = call(t, "POST", base+"/rules/"+created.ID+"/publish", admin, document, nil)
	if status != 200 {
		t.Fatalf("publish: %d %s", status, body)
	}
	for _, path := range []string{"test", "scenarios"} {
		input := any(map[string]any{"document": document, "inputs": map[string]float64{}})
		if path == "scenarios" {
			input = map[string]any{"document": document}
		}
		status, body = call(t, "POST", base+"/rules/"+created.ID+"/"+path, admin, input, nil)
		if status != 200 {
			t.Fatalf("pure %s: %d %s", path, status, body)
		}
	}
	inbox := func(token string) notification.Page {
		t.Helper()
		var page notification.Page
		status, body := call(t, "GET", base+"/notifications", token, nil, &page)
		if status != 200 {
			t.Fatalf("inbox: %d %s", status, body)
		}
		return page
	}
	if len(inbox(alice).Items) != 0 {
		t.Fatal("save/publish/simulate created a notice")
	}
	// An otherwise visible account is not a valid recipient for another scope.
	bad := document
	bad.Table.Rows = append([]decision.Row(nil), document.Table.Rows...)
	bad.Table.Rows[0].Actions = []decision.Action{{ActionID: "notice", Notification: &decision.RuleNotification{RecipientIDs: []string{outsideID}, Title: "Forbidden"}}}
	status, _ = call(t, "POST", base+"/rules/"+created.ID+"/test", admin, map[string]any{"document": bad}, nil)
	if status != 403 {
		t.Fatalf("out-of-scope recipient simulation: %d", status)
	}
	manager := client.NewManager(h.nc, func(nc *nats.Conn, config decision.DecisionTable) client.Client { return h.NewRuleClient(nc, config) }, RuleParentTypes())
	done := make(chan error, 1)
	go func() { done <- manager.Run() }()
	running := true
	defer func() {
		if running {
			manager.Stop(nil)
			<-done
		}
	}()
	status, body = call(t, "POST", base+"/rules/"+created.ID+"/enable", admin, map[string]bool{"enabled": true}, nil)
	if status != 200 {
		t.Fatalf("enable: %d %s", status, body)
	}
	defer func() {
		if t.Failed() {
			nodes, _ := client.GetNodes(h.nc, "all", created.ID, "", false)
			if len(nodes) > 0 {
				for _, kind := range []string{"error", "lastCheck", "lastExecution", "enabled"} {
					p, _ := nodes[0].Points.Find(kind, "")
					t.Logf("rule diagnostic %s: %s %+v", kind, p.Txt(), p)
				}
			}
		}
	}()
	waitFor(t, func() bool { return len(inbox(alice).Items) == 1 }, "actual rule creates durable notice")
	item := inbox(alice).Items[0]
	if item.ReadAt != nil || item.Title != "Check unit" || len(inbox(bob).Items) != 0 {
		t.Fatalf("recipient semantics: %+v", item)
	}
	waitFor(t, func() bool {
		nodes, _ := client.GetNodes(h.nc, "all", created.ID, "", false)
		if len(nodes) == 0 {
			return false
		}
		text, _ := nodes[0].Points.Text("lastCheck", "")
		var report decision.CheckReport
		_ = json.Unmarshal([]byte(text), &report)
		return len(report.Actions) == 1 && report.Actions[0].Status == "created"
	}, "created receipt recorded as notification, not device feedback")
	nodes, _ := client.GetNodes(h.nc, "all", created.ID, "", false)
	var report decision.CheckReport
	text, _ := nodes[0].Points.Text("lastCheck", "")
	_ = json.Unmarshal([]byte(text), &report)
	if report.ExecutionID == "" || report.SourceRevision != created.Revision {
		t.Fatalf("source identity: %+v", report)
	}
	var wire map[string]json.RawMessage
	raw, _ := json.Marshal(report.Actions[0])
	_ = json.Unmarshal(raw, &wire)
	if string(wire["status"]) != `"created"` || string(wire["notification_id"]) != `"`+item.ID+`"` || len(wire["command_id"]) > 0 || len(wire["applied"]) > 0 {
		t.Fatalf("notification falsely reported as device confirmation: %s", raw)
	}
	status, _ = call(t, "POST", base+"/notifications/"+item.ID+"/read", bob, map[string]any{}, nil)
	if status != 403 {
		t.Fatalf("nonrecipient read: %d", status)
	}
	status, body = call(t, "POST", base+"/notifications/"+item.ID+"/read", alice, map[string]any{}, nil)
	if status != 200 {
		t.Fatalf("read: %d %s", status, body)
	}
	input := decision.RuleCommand{RuleID: created.ID, Revision: created.Revision, ExecutionID: report.ExecutionID, RequestID: report.ExecutionID + "-0", RowID: "notice", Step: 0, Action: document.Table.Rows[0].Actions[0]}
	duplicate, err := h.submitRuleNotification(input)
	if err != nil || !duplicate.Duplicate || duplicate.ID != item.ID {
		t.Fatalf("retry changed identity: %+v %v", duplicate, err)
	}
	if _, err := h.CreateRuleNotification(notification.Request{RuleID: created.ID, SourceRevision: created.Revision, ExecutionID: input.ExecutionID, ActionID: input.RequestID, ScopeNodeID: scope, RecipientIDs: []string{aliceID}, Title: "Check unit", Body: "changed"}); !errors.Is(err, notification.ErrConflict) {
		t.Fatalf("same runtime step changed immutable payload: %v", err)
	}
	forged := input
	notice := *input.Action.Notification
	notice.Body = "changed"
	forged.Action.Notification = &notice
	if _, err := h.submitRuleNotification(forged); err == nil {
		t.Fatal("changed producer payload accepted")
	}
	forged = input
	forged.ExecutionID = "unreserved"
	forged.RequestID = "unreserved-0"
	if _, err := h.submitRuleNotification(forged); err == nil {
		t.Fatal("unreserved execution accepted")
	}
	manager.Stop(nil)
	<-done
	running = false
	stop()
	base, admin, h, _ = startInputServer(t, dir, password)
	alice = loginAPI(t, base, "alice@example.test", password)
	if page := inbox(alice); len(page.Items) != 1 || page.Items[0].ID != item.ID || page.Items[0].ReadAt == nil {
		t.Fatalf("restart lost durable/read state: %+v", page)
	}
	manager = client.NewManager(h.nc, func(nc *nats.Conn, config decision.DecisionTable) client.Client { return h.NewRuleClient(nc, config) }, RuleParentTypes())
	done = make(chan error, 1)
	go func() { done <- manager.Run() }()
	running = true
	time.Sleep(250 * time.Millisecond)
	if len(inbox(alice).Items) != 1 {
		t.Fatal("restart duplicated notification")
	}
	duplicate, err = h.submitRuleNotification(input)
	if err != nil || !duplicate.Duplicate || duplicate.ID != item.ID {
		t.Fatalf("restart retry: %+v %v", duplicate, err)
	}
	status, body = call(t, "PUT", base+"/users/"+aliceID, admin, map[string]any{"role": "viewer", "parent": other}, nil)
	if status != 200 {
		t.Fatalf("recipient migration: %d %s", status, body)
	}
	if _, err := h.submitRuleNotification(input); err == nil {
		t.Fatal("recipient lost scope but callback still accepted")
	}
	if len(inbox(alice).Items) != 0 {
		t.Fatal("recipient scope migration leaked inbox")
	}
	status, body = call(t, "PUT", base+"/users/"+aliceID, admin, map[string]any{"role": "viewer", "parent": scope}, nil)
	if status != 200 {
		t.Fatal(body)
	}
	status, body = call(t, "POST", base+"/rules/"+created.ID+"/enable", admin, map[string]bool{"enabled": false}, nil)
	if status != 200 {
		t.Fatal(body)
	}
	if _, err := h.submitRuleNotification(input); err == nil {
		t.Fatal("disabled rule callback accepted")
	}
}
