package appapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

// Real HTTP and isolated persisted users; no device clients are started.
func TestAccountsManagementScope(t *testing.T) {
	dir, adminPassword := t.TempDir(), data.NewID()
	base, admin, _, stop := startInputServer(t, dir, adminPassword)
	root := snapshotRoot(t, base, admin)
	inside := createNodeAPI(t, base, admin, "group", root, "inside", 0)
	outside := createNodeAPI(t, base, admin, "group", root, "secret-outside", 0)
	create := func(email, parent, role string, actions []string) (string, string) {
		t.Helper()
		var user struct {
			ID string `json:"id"`
		}
		password := data.NewID()
		status, body := call(t, http.MethodPost, base+"/users", admin, map[string]any{"parent": parent, "email": email, "password": password, "role": role, "actions": actions}, &user)
		if status != 200 {
			t.Fatalf("create: %d %s", status, body)
		}
		return user.ID, loginAPI(t, base, email, password)
	}
	_, manager := create("manager@example.test", inside, "viewer", []string{"account_manage"})
	hiddenID, _ := create("hidden@example.test", outside, "viewer", nil)
	userID, userToken := create("local@example.test", inside, "operator", nil)
	status, body := call(t, http.MethodGet, base+"/users", manager, nil, nil)
	if status != 200 || strings.Contains(body, hiddenID) || strings.Contains(body, "hidden@example.test") || strings.Contains(body, outside) || strings.Contains(body, `"admin":true`) {
		t.Fatalf("account list leaked: %d %s", status, body)
	}
	for _, route := range []struct {
		method, path string
		input        any
	}{
		{"PUT", "/users/" + hiddenID, map[string]any{"role": "viewer"}},
		{"POST", "/users/" + hiddenID + "/password", map[string]any{"password": data.NewID()}},
		{"GET", "/users/" + hiddenID + "/authorization?target=" + inside, nil},
		{"GET", "/users/" + userID + "/authorization?target=" + outside, nil},
		{"PUT", "/users/" + userID, map[string]any{"role": "viewer", "parent": outside}},
		{"PUT", "/users/" + userID, map[string]any{"role": "viewer", "actions": []string{"command", "account_manage"}}},
	} {
		status, body = call(t, route.method, base+route.path, manager, route.input, nil)
		if status != 403 || strings.Contains(body, hiddenID) || strings.Contains(body, "hidden@example.test") {
			t.Fatalf("out of scope leaked: %d %s", status, body)
		}
	}
	for _, role := range []string{"admin", "operator"} {
		status, _ = call(t, "PUT", base+"/users/"+userID, manager, map[string]any{"role": role}, nil)
		if status != 403 {
			t.Fatalf("delegated escalation %s: %d", role, status)
		}
	}
	status, _ = call(t, "POST", base+"/users", manager, map[string]any{"parent": inside, "email": "escalation@example.test", "password": data.NewID(), "role": "admin"}, nil)
	if status != 403 {
		t.Fatalf("delegated admin creation: %d", status)
	}
	status, _ = call(t, "POST", base+"/users", admin, map[string]any{"parent": "missing", "email": "missing@example.test", "password": data.NewID(), "role": "viewer"}, nil)
	if status < 400 {
		t.Fatalf("unknown parent accepted: %d", status)
	}
	status, _ = call(t, "POST", base+"/users/"+userID+"/password", manager, map[string]any{"password": data.NewID()}, nil)
	if status != 403 {
		t.Fatalf("password reset could take over a more privileged account: %d", status)
	}
	// Existing sessions must observe persisted role, whitelist, and moved scope.
	status, body = call(t, "PUT", base+"/users/"+userID, admin, map[string]any{"role": "implementer", "actions": []string{"command"}, "parent": outside}, nil)
	if status != 200 {
		t.Fatalf("update: %d %s", status, body)
	}
	waitFor(t, func() bool {
		status, body := call(t, "GET", base+"/snapshot", userToken, nil, nil)
		return status == 200 && strings.Contains(body, outside) && !strings.Contains(body, inside) && strings.Contains(body, `"actions":["command"]`)
	}, "existing session observes moved scope and absolute whitelist")
	status, _ = call(t, "PATCH", base+"/nodes/"+outside, userToken, map[string]any{"description": "not permitted"}, nil)
	if status != 403 {
		t.Fatalf("whitelist did not replace implementer defaults: %d", status)
	}
	stop()
	base, admin, _, _ = startInputServer(t, dir, adminPassword)
	status, body = call(t, "GET", base+"/users", admin, nil, nil)
	if status != 200 || !strings.Contains(body, `"root_node_ids":["`+outside+`"]`) || !strings.Contains(body, `"actions":["command"]`) {
		t.Fatalf("policy did not survive restart: %d %s", status, body)
	}
	status, body = call(t, "GET", base+"/users/"+userID+"/authorization?target="+outside+"&operation=command", admin, nil, nil)
	if status != 200 || !strings.Contains(body, `"allowed":true`) {
		t.Fatalf("restarted permission matrix: %d %s", status, body)
	}
}

func TestAccountsAdminDeclaredScopeDoesNotExpandThroughAnotherEdge(t *testing.T) {
	base, admin, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, admin)
	inside := createNodeAPI(t, base, admin, "group", root, "admin inside", 0)
	outside := createNodeAPI(t, base, admin, "group", root, "admin outside", 0)
	var user struct {
		ID string `json:"id"`
	}
	password := data.NewID()
	status, body := call(t, "POST", base+"/users", admin, map[string]any{"parent": inside, "email": "scoped-admin@example.test", "password": password, "role": "admin"}, &user)
	if status != 200 {
		t.Fatalf("create: %d %s", status, body)
	}
	// A mirrored/stale edge must not broaden an explicitly declared policy root.
	if err := client.MirrorNode(h.nc, user.ID, inside, outside, "fixture"); err != nil {
		t.Fatal(err)
	}
	token := loginAPI(t, base, "scoped-admin@example.test", password)
	status, body = call(t, "GET", base+"/snapshot", token, nil, nil)
	if status != 200 || strings.Contains(body, "admin outside") {
		t.Fatalf("admin policy scope leaked: %d %s", status, body)
	}
	status, body = call(t, "GET", base+"/users/"+user.ID+"/authorization?target="+outside+"&operation=command", admin, nil, nil)
	if status != 200 || strings.Contains(body, `"allowed":true`) {
		t.Fatalf("admin scope explanation broadened: %d %s", status, body)
	}
}

func TestAccountsRoleActionMatrix(t *testing.T) {
	base, admin, _, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, admin)
	inside := createNodeAPI(t, base, admin, "group", root, "matrix inside", 0)
	outside := createNodeAPI(t, base, admin, "group", root, "matrix outside", 0)
	for _, tc := range []struct {
		role    string
		actions []string
		allowed map[string]bool
	}{
		{"admin", []string{"command"}, map[string]bool{"overview": true, "command": true, "configure": true, "publish": true, "account_manage": true, "audit_view": true}},
		{"implementer", nil, map[string]bool{"overview": true, "command": true, "configure": true, "publish": true}},
		{"operator", nil, map[string]bool{"overview": true, "command": true}},
		{"viewer", nil, map[string]bool{"overview": true}},
		{"viewer", []string{"configure"}, map[string]bool{"overview": true, "configure": true, "publish": true}},
		{"implementer", []string{"command"}, map[string]bool{"overview": true, "command": true}},
	} {
		var user struct {
			ID string `json:"id"`
		}
		status, body := call(t, "POST", base+"/users", admin, map[string]any{"parent": inside, "email": data.NewID() + "@example.test", "password": data.NewID(), "role": tc.role, "actions": tc.actions}, &user)
		if status != 200 {
			t.Fatalf("create: %d %s", status, body)
		}
		for _, target := range []string{inside, outside} {
			var result struct {
				Checks []authorizationCheck `json:"checks"`
			}
			status, body = call(t, "GET", base+"/users/"+user.ID+"/authorization?target="+target, admin, nil, &result)
			if status != 200 || strings.Contains(body, `"token"`) {
				t.Fatalf("matrix: %d %s", status, body)
			}
			for _, check := range result.Checks {
				want := target == inside && tc.allowed[check.Operation]
				if check.Allowed != want {
					t.Fatalf("role=%s whitelist=%v target=%s check=%+v", tc.role, tc.actions, target, check)
				}
			}
		}
	}
	status, _ := call(t, "POST", base+"/users", admin, map[string]any{"parent": inside, "email": "unknown@example.test", "password": data.NewID(), "role": "viewer", "actions": []string{"undeclared_action"}}, nil)
	if status != 400 {
		t.Fatalf("undeclared action accepted: %d", status)
	}
	status, body := call(t, "GET", base+"/snapshot", admin, nil, nil)
	if status != 200 || !strings.Contains(body, `"email":"admin"`) {
		t.Fatalf("explanation changed caller identity: %d %s", status, body)
	}
}

func TestAccountsAuthorizationExplanation(t *testing.T) {
	base, admin, _, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, admin)
	inside := createNodeAPI(t, base, admin, "group", root, "inside", 0)
	outside := createNodeAPI(t, base, admin, "group", root, "outside", 0)
	var user struct {
		ID string `json:"id"`
	}
	password := data.NewID()
	status, body := call(t, http.MethodPost, base+"/users", admin, map[string]any{
		"parent": inside, "email": "operator@example.test", "password": password, "role": "operator",
	}, &user)
	if status != 200 {
		t.Fatalf("create: %d %s", status, body)
	}
	operator := loginAPI(t, base, "operator@example.test", password)
	var result struct {
		ReadOnly bool `json:"read_only"`
		Checks   []struct {
			Operation string `json:"operation"`
			Allowed   bool   `json:"allowed"`
			Reason    string `json:"reason"`
		} `json:"checks"`
	}
	for _, tc := range []struct {
		target  string
		command bool
	}{{inside, true}, {outside, false}} {
		status, body = call(t, http.MethodGet, base+"/users/"+user.ID+"/authorization?target="+tc.target, admin, nil, &result)
		if status != 200 || !result.ReadOnly || len(result.Checks) != 6 {
			t.Fatalf("explanation: %d %s", status, body)
		}
		for _, check := range result.Checks {
			want := (check.Operation == "overview" || check.Operation == "command") && tc.command
			if check.Allowed != want {
				t.Fatalf("%s target %s: %+v", check.Operation, tc.target, check)
			}
		}
	}
	// Repeated checks must not consume the subject's login-session allowance.
	for range 6 {
		status, body = call(t, http.MethodGet, base+"/users/"+user.ID+"/authorization?target="+inside, admin, nil, nil)
		if status != 200 || strings.Contains(body, `"token"`) {
			t.Fatalf("explanation issued a session: %d %s", status, body)
		}
	}
	_ = loginAPI(t, base, "operator@example.test", password)
	// The explanation must not change the current identity or enable a denied request.
	status, _ = call(t, http.MethodGet, base+"/users", operator, nil, nil)
	if status != 403 {
		t.Fatalf("operator gained account access: %d", status)
	}
	status, _ = call(t, http.MethodPatch, base+"/nodes/"+inside, operator, map[string]any{"description": "forbidden"}, nil)
	if status != 403 {
		t.Fatalf("operator gained configure: %d", status)
	}
	for _, query := range []string{"target=missing", "target=" + inside + "&operation=undeclared"} {
		status, _ = call(t, http.MethodGet, base+"/users/"+user.ID+"/authorization?"+query, admin, nil, nil)
		if status < 400 {
			t.Fatalf("unknown target/operation accepted: %d", status)
		}
	}
}
