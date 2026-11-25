package appapi

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"fastiot/internal/notification"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

func inboxAccount(t *testing.T, base, admin, parent, email, password string) string {
	t.Helper()
	var result struct {
		ID string `json:"id"`
	}
	status, body := call(t, "POST", base+"/users", admin, map[string]any{"parent": parent, "email": email, "password": password, "role": "viewer"}, &result)
	if status != 200 {
		t.Fatalf("account: %d %s", status, body)
	}
	return result.ID
}
func inboxRule(t *testing.T, h *Handler, scope string) string {
	t.Helper()
	id := data.NewID()
	if err := client.SendNode(h.nc, data.NodeEdge{ID: id, Parent: scope, Type: "decisionTable"}, "fixture"); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestNotificationsHTTPCursorAuthorization(t *testing.T) {
	password := "isolated-cursor-fixture"
	base, admin, h, _ := startInputServer(t, t.TempDir(), password)
	root := snapshotRoot(t, base, admin)
	a := createNodeAPI(t, base, admin, "site", root, "A", 0)
	b := createNodeAPI(t, base, admin, "site", root, "B", 0)
	moved := createNodeAPI(t, base, admin, "area", a, "Movable scope", 0)
	aliceID := inboxAccount(t, base, admin, a, "alice@example.test", password)
	bobID := inboxAccount(t, base, admin, a, "bob@example.test", password)
	alice := loginAPI(t, base, "alice@example.test", password)
	rules := map[string]string{a: inboxRule(t, h, a), moved: inboxRule(t, h, moved)}
	create := func(action, recipient, scope string) string {
		t.Helper()
		created, err := h.CreateRuleNotification(notification.Request{RuleID: rules[scope], SourceRevision: "v1", ExecutionID: "execution", ActionID: action, ScopeNodeID: scope, RecipientIDs: []string{recipient}, Title: action})
		if err != nil {
			t.Fatal(err)
		}
		return created.ID
	}
	oldest := create("oldest", aliceID, a)
	older := create("older", aliceID, a)
	foreign := create("foreign", bobID, a)
	revoked := create("revoked", aliceID, moved)
	newest := create("newest", aliceID, a)
	status, body := call(t, "POST", base+"/nodes/"+moved+"/move", admin, map[string]any{"old_parent": a, "new_parent": b}, nil)
	if status != 200 {
		t.Fatalf("move: %d %s", status, body)
	}
	unknownStatus, unknownBody := call(t, "GET", base+"/notifications?limit=1&before=unknown", alice, nil, nil)
	if unknownStatus != 200 {
		t.Fatalf("unknown cursor: %d %s", unknownStatus, unknownBody)
	}
	for _, cursor := range []string{foreign, revoked} {
		status, body := call(t, "GET", base+"/notifications?limit=1&before="+cursor, alice, nil, nil)
		if status != unknownStatus || body != unknownBody {
			t.Errorf("hidden cursor differs from unknown: %s: %d %s; unknown: %d %s", cursor, status, body, unknownStatus, unknownBody)
		}
	}
	before := ""
	for i, id := range []string{newest, older, oldest} {
		var page notification.Page
		status, body := call(t, "GET", base+"/notifications?limit=1&before="+before, alice, nil, &page)
		next := id
		if i == 2 {
			next = ""
		}
		if status != 200 || len(page.Items) != 1 || page.Items[0].ID != id || page.NextBefore != next {
			t.Fatalf("visible page %d: %d %s", i, status, body)
		}
		before = page.NextBefore
	}
	status, body = call(t, "POST", base+"/nodes/"+moved+"/move", admin, map[string]any{"old_parent": b, "new_parent": a}, nil)
	if status != 200 {
		t.Fatalf("restore: %d %s", status, body)
	}
	var restored notification.Page
	status, body = call(t, "GET", base+"/notifications?limit=1&before="+revoked, alice, nil, &restored)
	if status != 200 || len(restored.Items) != 1 || restored.Items[0].ID != older || restored.NextBefore != older {
		t.Fatalf("restored cursor: %d %s", status, body)
	}
}

func TestNotificationsHTTPAccountsScopeAndRestart(t *testing.T) {
	dir, password := t.TempDir(), "isolated-inbox-fixture"
	base, admin, h, stop := startInputServer(t, dir, password)
	root := snapshotRoot(t, base, admin)
	a := createNodeAPI(t, base, admin, "site", root, "A", 0)
	b := createNodeAPI(t, base, admin, "site", root, "B", 0)
	aliceID := inboxAccount(t, base, admin, a, "alice@example.test", password)
	bobID := inboxAccount(t, base, admin, a, "bob@example.test", password)
	outsideID := inboxAccount(t, base, admin, b, "outside@example.test", password)
	alice := loginAPI(t, base, "alice@example.test", password)
	bob := loginAPI(t, base, "bob@example.test", password)
	outside := loginAPI(t, base, "outside@example.test", password)
	scope := createNodeAPI(t, base, admin, "area", a, "Notice scope", 0)
	rule := inboxRule(t, h, scope)
	req := notification.Request{RuleID: rule, SourceRevision: "revision-1", ExecutionID: "run-1", ActionID: "step-0", ScopeNodeID: scope, RecipientIDs: []string{aliceID, bobID}, Title: "Inspection required", Body: "Literal <script> text"}
	created, err := h.CreateRuleNotification(req)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			retry, err := h.CreateRuleNotification(req)
			if err != nil || retry.ID != created.ID || !retry.Duplicate {
				t.Errorf("concurrent retry: %+v %v", retry, err)
			}
		}()
	}
	wg.Wait()
	forged := req
	forged.ScopeNodeID = b
	if _, err := h.CreateRuleNotification(forged); !errors.Is(err, notification.ErrForbidden) {
		t.Fatal("forged rule mount", err)
	}
	var page notification.Page
	status, body := call(t, "GET", base+"/notifications", alice, nil, &page)
	if status != 200 || len(page.Items) != 1 || page.Items[0].ReadAt != nil {
		t.Fatalf("own unread: %d %s", status, body)
	}
	for _, path := range []string{"/notifications?user_id=" + aliceID, "/notifications?limit=101"} {
		if status, _ := call(t, "GET", base+path, outside, nil, nil); status != 400 {
			t.Fatalf("query override accepted: %s %d", path, status)
		}
	}
	status, body = call(t, "GET", base+"/notifications", outside, nil, &page)
	if status != 200 || len(page.Items) != 0 {
		t.Fatalf("outsider: %d %s", status, body)
	}
	if status, _ := call(t, "POST", base+"/notifications/"+created.ID+"/read", outside, map[string]any{}, nil); status != 403 {
		t.Fatal("outsider read", status)
	}
	if status, _ := call(t, "POST", base+"/notifications/"+created.ID+"/read", alice, map[string]any{"user_id": bobID}, nil); status != 400 {
		t.Fatal("body recipient override", status)
	}
	if status, _ := call(t, "POST", base+"/notifications/"+created.ID+"/read", alice, map[string]any{}, nil); status != 200 {
		t.Fatal("read", status)
	}
	status, body = call(t, "GET", base+"/notifications", bob, nil, &page)
	if status != 200 || page.Items[0].ReadAt != nil {
		t.Fatalf("bob read changed: %s", body)
	}
	status, body = call(t, "GET", base+"/notification-recipients?scope_node_id="+a, admin, nil, nil)
	if status != 200 || !strings.Contains(string(body), aliceID) || strings.Contains(string(body), outsideID) || strings.Contains(string(body), "email") || strings.Contains(string(body), "policy") {
		t.Fatalf("directory: %d %s", status, body)
	}
	if status, _ := call(t, "GET", base+"/notification-recipients?scope_node_id="+a, alice, nil, nil); status != 403 {
		t.Fatal("viewer picker", status)
	}
	req.RecipientIDs = []string{outsideID}
	if _, err := h.CreateRuleNotification(req); !errors.Is(err, notification.ErrForbidden) {
		t.Fatal("ineligible recipient", err)
	}
	req.RecipientIDs = []string{aliceID, bobID}
	req.Body = "changed"
	if _, err := h.CreateRuleNotification(req); !errors.Is(err, notification.ErrConflict) {
		t.Fatal("payload conflict", err)
	}
	req.Body = "Literal <script> text"
	stop()
	base, admin, h, _ = startInputServer(t, dir, password)
	alice = loginAPI(t, base, "alice@example.test", password)
	replay, err := h.CreateRuleNotification(req)
	if err != nil || replay.ID != created.ID || !replay.Duplicate {
		t.Fatal("restart replay", replay, err)
	}
	status, body = call(t, "GET", base+"/notifications", alice, nil, &page)
	if status != 200 || len(page.Items) != 1 || page.Items[0].ReadAt == nil {
		t.Fatalf("restart persistence: %d %s", status, body)
	}
	// Moving the scope out of the recipient's actual tree revokes visibility and read mutation.
	status, body = call(t, "POST", base+"/nodes/"+scope+"/move", admin, map[string]any{"old_parent": a, "new_parent": b}, nil)
	if status != 200 {
		t.Fatalf("move scope: %d %s", status, body)
	}
	if _, err := h.CreateRuleNotification(req); !errors.Is(err, notification.ErrForbidden) {
		t.Fatal("moved scope accepted old recipients", err)
	}
	status, body = call(t, "GET", base+"/notifications", alice, nil, &page)
	if status != 200 || len(page.Items) != 0 {
		t.Fatalf("moved scope visible: %d %s", status, body)
	}
	if status, _ := call(t, "POST", base+"/notifications/"+created.ID+"/read", alice, map[string]any{}, nil); status != 403 {
		t.Fatal("moved scope read", status)
	}
	status, body = call(t, "POST", base+"/nodes/"+scope+"/move", admin, map[string]any{"old_parent": b, "new_parent": a}, nil)
	if status != 200 {
		t.Fatalf("restore scope: %d %s", status, body)
	}
	status, body = call(t, "GET", base+"/notifications", alice, nil, &page)
	if status != 200 || len(page.Items) != 1 || page.Items[0].ReadAt == nil {
		t.Fatalf("scope restore lost history: %d %s", status, body)
	}
	// Revoke account roots without changing its authenticated session.
	status, body = call(t, "PUT", base+"/users/"+aliceID, admin, map[string]any{"role": "viewer", "parent": b}, nil)
	if status != 200 {
		t.Fatalf("revoke: %d %s", status, body)
	}
	if status, body := call(t, "POST", base+"/notifications/"+created.ID+"/read", alice, map[string]any{}, nil); status != 403 {
		t.Fatalf("revoked authority: %d %s", status, body)
	}
	status, body = call(t, "GET", base+"/notifications", alice, nil, &page)
	if status != 200 || len(page.Items) != 0 {
		t.Fatalf("account move retained access: %d %s", status, body)
	}
}
