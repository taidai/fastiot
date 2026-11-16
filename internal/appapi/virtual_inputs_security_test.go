package appapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

func TestVirtualInputWebSocketHonorsRevocationAndExpiry(t *testing.T) {
	for _, mode := range []string{"reset", "expiry"} {
		t.Run(mode, func(t *testing.T) {
			base, admin, h, _ := startInputServer(t, t.TempDir(), data.NewID())
			f := &pointConfigFixture{base: base, token: admin}
			token := inputAccount(t, f, snapshotRoot(t, base, admin), "viewer", nil)
			var snapshot struct {
				User Identity `json:"user"`
			}
			if status, _ := call(t, http.MethodGet, base+"/snapshot", token, nil, &snapshot); status != 200 {
				t.Fatal(status)
			}
			if mode == "expiry" {
				h.SetSessionPolicy(100*time.Millisecond, 5)
			}
			ws, _, err := websocket.DefaultDialer.Dial(strings.Replace(base, "http://", "ws://", 1)+"/ws", http.Header{"Authorization": []string{"Bearer " + token}})
			if err != nil {
				t.Fatal(err)
			}
			defer ws.Close()
			ws.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, _, err := ws.ReadMessage(); err != nil {
				t.Fatal(err)
			}
			if mode == "reset" {
				status, body := call(t, http.MethodPost, base+"/users/"+snapshot.User.UserID+"/password", admin, map[string]any{"password": data.NewID()}, nil)
				if status != 200 {
					t.Fatalf("reset: %d %s", status, body)
				}
			}
			if _, _, err := ws.ReadMessage(); err == nil {
				t.Fatal("unauthorized session still receives websocket snapshots")
			}
		})
	}
}

func TestVirtualInputGenericOperationsHideRestrictedReferences(t *testing.T) {
	base, admin, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	f := &pointConfigFixture{base: base, token: admin}
	root := snapshotRoot(t, base, admin)
	a := createNodeAPI(t, base, admin, "group", root, "A", 0)
	b := createNodeAPI(t, base, admin, "group", root, "B", 0)
	destination := createNodeAPI(t, base, admin, "group", a, "destination", 0)
	editor := inputAccount(t, f, a, "implementer", nil)
	var input inputResponse
	status, body := call(t, http.MethodPost, base+"/virtual-inputs", admin, map[string]any{"parent": a, "name": "capacity", "source": "configuration", "definition": virtualCapacity("100"), "operation_id": "create"}, &input)
	if status != 200 {
		t.Fatalf("create: %d %s", status, body)
	}
	document, _ := json.Marshal(map[string]any{"table": map[string]any{"rows": []any{}}, "bindings": map[string]any{"points": map[string]any{"input": map[string]any{"node_id": input.ID}}}})
	if err := client.SendNode(h.nc, data.NodeEdge{ID: data.NewID(), Parent: b, Type: "decisionTable", Points: data.Points{data.NewPointString("description", "0", "Private strategy"), data.NewPointString("draft", "0", string(document))}}, "test"); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"delete", "move"} {
		method, url, payload := http.MethodDelete, base+"/nodes/"+input.ID, map[string]any{"parent": a}
		if operation == "move" {
			method, url, payload = http.MethodPost, url+"/move", map[string]any{"old_parent": a, "new_parent": destination}
		}
		status, body = call(t, method, url, editor, payload, nil)
		if status != 409 || strings.Contains(body, "Private strategy") || strings.Contains(body, b) {
			t.Errorf("%s disclosed restricted dependency: %d %s", operation, status, body)
		}
	}
}
