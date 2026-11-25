package appapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"fastiot/internal/decision"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

func TestRulePublicationRefusesUnsupportedExecutionAdapter(t *testing.T) {
	base, token, _, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, token)
	target := createNodeAPI(t, base, token, "variable", root, "legacy output", 0)
	doc := decision.Document{Scope: "self", Table: decision.Table{Rows: []decision.Row{{ID: "r", Actions: []decision.Action{{ActionID: "a", Value: 1}}}}}, Bindings: decision.BindingSet{Actions: map[string]decision.Target{"a": {NodeID: target}}}}
	var created struct {
		ID       string `json:"id"`
		Revision string `json:"revision"`
	}
	status, body := call(t, http.MethodPost, base+"/rules", token, map[string]any{"parent": root, "description": "legacy adapter", "document": doc}, &created)
	if status != 200 {
		t.Fatalf("draft: %d %s", status, body)
	}
	doc.Revision = created.Revision
	status, body = call(t, http.MethodPost, base+"/rules/"+created.ID+"/publish", token, doc, nil)
	if status != 409 {
		t.Fatalf("unsupported adapter published: %d %s", status, body)
	}
}

func TestRuleScopeUsesUnitMembersAndMappedPointsWithoutGrantingAuthority(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, token)
	unit, device, bus, io := data.NewID(), data.NewID(), data.NewID(), data.NewID()
	model, _ := json.Marshal(unitModelConfig{BusinessNodeID: device, PointMap: map[string]string{"pressure": io}})
	members := `[{"role":"pump","node_id":"` + device + `","required":true}]`
	for _, node := range []data.NodeEdge{
		{ID: unit, Parent: root, Type: "unit", Points: data.Points{data.NewPointString("easyemsUnitMembers", "", members)}},
		{ID: device, Parent: root, Type: "device"},
		{ID: bus, Parent: root, Type: "modbus", Points: data.Points{data.NewPointString("easyemsUnitModel", "1", string(model))}},
		{ID: io, Parent: bus, Type: "modbusIo", Points: data.Points{data.NewPointFloat("id", "", 1), data.NewPointString("modbusIOType", "", "modbusHoldingRegister")}},
	} {
		if err := client.SendNode(h.nc, node, "fixture"); err != nil {
			t.Fatal(err)
		}
	}
	doc := decision.Document{Scope: "self", Table: decision.Table{Rows: []decision.Row{{ID: "r", Conditions: []decision.Condition{{PointID: "p", Operator: "gt", Value: 0}}}}}, Bindings: decision.BindingSet{Points: map[string]decision.Target{"p": {NodeID: io}}}}
	payload := map[string]any{"parent": unit, "description": "member input", "document": doc}
	status, body := call(t, http.MethodPost, base+"/rules", token, payload, nil)
	if status != 200 {
		t.Fatalf("declared member mapping rejected: %d %s", status, body)
	}
	restricted := inputAccount(t, &pointConfigFixture{base: base, token: token}, unit, "implementer", nil)
	status, body = call(t, http.MethodPost, base+"/rules", restricted, payload, nil)
	if status != 403 {
		t.Fatalf("business membership granted hidden channel authority: %d %s", status, body)
	}
}

func TestRuleSimulationChecksEveryReferencedTarget(t *testing.T) {
	base, token, _, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, token)
	parent := createNodeAPI(t, base, token, "group", root, "scope", 0)
	outside := createNodeAPI(t, base, token, "variable", root, "outside", 1)
	doc := decision.Document{Scope: "descendants", Table: decision.Table{Rows: []decision.Row{{ID: "r"}}}}
	var created struct {
		ID       string `json:"id"`
		Revision string `json:"revision"`
	}
	status, body := call(t, http.MethodPost, base+"/rules", token, map[string]any{"parent": parent, "description": "scope checks", "document": doc}, &created)
	if status != 200 {
		t.Fatalf("create: %d %s", status, body)
	}
	doc.Revision = created.Revision
	doc.Bindings.Points = map[string]decision.Target{"hidden": {NodeID: outside}}
	for _, route := range []string{"test", "scenarios", "deps"} {
		status, body = call(t, http.MethodPost, base+"/rules/"+created.ID+"/"+route, token, map[string]any{"document": doc}, nil)
		if status != 403 {
			t.Errorf("%s accepted out-of-scope binding: %d %s", route, status, body)
		}
	}
}

func TestRulePublishRequiresSavedRevisionAndSeparateControlPermission(t *testing.T) {
	base, token, _, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, token)
	doc := decision.Document{Scope: "self", Table: decision.Table{Rows: []decision.Row{{ID: "first"}}}}
	var created struct {
		ID       string `json:"id"`
		Revision string `json:"revision"`
	}
	status, body := call(t, http.MethodPost, base+"/rules", token, map[string]any{"parent": root, "description": "publish", "document": doc}, &created)
	if status != 200 {
		t.Fatalf("create: %d %s", status, body)
	}
	doc.Revision = created.Revision
	doc.Table.Rows[0].ID = "unsaved"
	status, body = call(t, http.MethodPost, base+"/rules/"+created.ID+"/publish", token, doc, nil)
	if status != 409 {
		t.Fatalf("unsaved document published: %d %s", status, body)
	}
	doc.Table.Rows[0].ID = "first"
	status, body = call(t, http.MethodPost, base+"/rules/"+created.ID+"/publish", token, doc, nil)
	if status != 200 {
		t.Fatalf("saved publication: %d %s", status, body)
	}
	publisher := inputAccount(t, &pointConfigFixture{base: base, token: token}, root, "viewer", []string{ActionRulePublish})
	status, body = call(t, http.MethodPost, base+"/rules/"+created.ID+"/enable", publisher, map[string]bool{"enabled": true}, nil)
	if status != 403 {
		t.Fatalf("publish permission enabled control: %d %s", status, body)
	}
	status, body = call(t, http.MethodPost, base+"/rules/"+created.ID+"/enable", token, map[string]bool{"enabled": true}, nil)
	if status != 200 {
		t.Fatalf("authorized enable: %d %s", status, body)
	}
	status, body = call(t, http.MethodPost, base+"/rules/"+created.ID+"/publish", token, doc, nil)
	if status != 409 {
		t.Fatalf("publication modified enabled runtime: %d %s", status, body)
	}
}

func TestRuleDraftRevisionSurvivesRestartAndRejectsStaleSave(t *testing.T) {
	dir, password := t.TempDir(), data.NewID()
	base, token, _, stop := startInputServer(t, dir, password)
	root := snapshotRoot(t, base, token)
	doc := decision.Document{Scope: "self", Table: decision.Table{Rows: []decision.Row{{ID: "first"}}}}
	var created struct {
		ID       string `json:"id"`
		Revision string `json:"revision"`
	}
	status, body := call(t, http.MethodPost, base+"/rules", token, map[string]any{"parent": root, "description": "versioned draft", "document": doc}, &created)
	if status != 200 || created.Revision == "" {
		t.Fatalf("missing durable revision: %d %s", status, body)
	}
	doc.Revision = created.Revision
	var saved struct {
		Revision string `json:"revision"`
	}
	doc.Table.Rows[0].ID = "changed"
	status, body = call(t, http.MethodPut, base+"/rules/"+created.ID+"/draft", token, doc, &saved)
	if status != 200 || saved.Revision == "" || saved.Revision == doc.Revision {
		t.Fatalf("save did not advance revision: %d %s", status, body)
	}
	status, body = call(t, http.MethodPut, base+"/rules/"+created.ID+"/draft", token, doc, nil)
	if status != 409 {
		t.Fatalf("stale save accepted: %d %s", status, body)
	}
	stop()
	base, token, _, _ = startInputServer(t, dir, password)
	var snapshot struct {
		Nodes []data.NodeEdge `json:"nodes"`
	}
	status, body = call(t, http.MethodGet, base+"/snapshot", token, nil, &snapshot)
	if status != 200 {
		t.Fatalf("reload: %d %s", status, body)
	}
	for _, node := range snapshot.Nodes {
		if node.ID != created.ID {
			continue
		}
		raw, _ := node.Points.Text("draft", "")
		var restored decision.Document
		if err := json.Unmarshal([]byte(raw), &restored); err != nil {
			t.Fatal(err)
		}
		if restored.Revision != saved.Revision || restored.Table.Rows[0].ID != "changed" {
			t.Fatalf("draft lost on restart: %+v", restored)
		}
		applied, _ := node.Points.Text("applied", "")
		enabled, _ := node.Points.Value("enabled", "")
		if applied != "" || enabled != 0 {
			t.Fatal("save published or enabled runtime")
		}
		return
	}
	t.Fatal("draft missing after restart")
}

// The public HTTP seam: a draft must never store a hidden or undeclared target.
func TestRuleDraftRequiresExplicitAuthorizedScope(t *testing.T) {
	base, token, _, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, token)
	parent := createNodeAPI(t, base, token, "group", root, "rules", 0)
	child := createNodeAPI(t, base, token, "group", parent, "child", 0)
	input := createNodeAPI(t, base, token, "variable", child, "input", 1)
	document := map[string]any{"table": map[string]any{"rows": []any{map[string]any{
		"row_id": "r1", "conditions": []any{map[string]any{"point_id": "input", "operator": "gt", "value": 0}}, "actions": []any{},
	}}}, "bindings": map[string]any{"points": map[string]any{"input": map[string]any{"node_id": input}}}}
	status, body := call(t, http.MethodPost, base+"/rules", token, map[string]any{"parent": parent, "description": "explicit scope", "document": document}, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("undeclared scope saved: %d %s", status, body)
	}
	document["scope"] = "self"
	status, body = call(t, http.MethodPost, base+"/rules", token, map[string]any{"parent": parent, "description": "explicit scope", "document": document}, nil)
	if status != http.StatusForbidden {
		t.Fatalf("self scope reached child: %d %s", status, body)
	}
	document["scope"] = "descendants"
	status, body = call(t, http.MethodPost, base+"/rules", token, map[string]any{"parent": parent, "description": "explicit scope", "document": document}, nil)
	if status != http.StatusOK {
		t.Fatalf("declared descendants rejected: %d %s", status, body)
	}
}
