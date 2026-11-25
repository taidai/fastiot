package appapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"fastiot/internal/decision"
	"github.com/gorilla/websocket"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

func TestRuleReadSurfacesRedactMovedHistoricalReferences(t *testing.T) {
	base, admin, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, admin)
	own := createNodeAPI(t, base, admin, "group", root, "Rule reader", 0)
	other := createNodeAPI(t, base, admin, "group", root, "Other branch", 0)
	private := createNodeAPI(t, base, admin, "variable", own, "Old input", 97531.25)
	public := createNodeAPI(t, base, admin, "variable", own, "Current input", 3)
	viewer := inputAccount(t, &pointConfigFixture{base: base, token: admin}, own, "viewer", nil)
	doc := decision.Document{Scope: "descendants", Table: decision.Table{Rows: []decision.Row{{ID: "r", Conditions: []decision.Condition{{PointID: "p", Operator: "gt", Value: 0}}}}}, Bindings: decision.BindingSet{Points: map[string]decision.Target{"p": {NodeID: private}}}}
	var created struct {
		ID       string `json:"id"`
		Revision string `json:"revision"`
	}
	status, body := call(t, http.MethodPost, base+"/rules", admin, map[string]any{"parent": own, "description": "Visible rule", "document": doc}, &created)
	if status != 200 {
		t.Fatalf("create: %d %s", status, body)
	}
	doc.Revision = created.Revision
	status, body = call(t, http.MethodPost, base+"/rules/"+created.ID+"/publish", admin, doc, nil)
	if status != 200 {
		t.Fatalf("first publish: %d %s", status, body)
	}
	oldRevision := doc.Revision
	doc.Bindings.Points["p"] = decision.Target{NodeID: public}
	var saved struct {
		Revision string `json:"revision"`
	}
	status, body = call(t, http.MethodPut, base+"/rules/"+created.ID+"/draft", admin, doc, &saved)
	if status != 200 {
		t.Fatalf("save: %d %s", status, body)
	}
	doc.Revision = saved.Revision
	status, body = call(t, http.MethodPost, base+"/rules/"+created.ID+"/publish", admin, doc, nil)
	if status != 200 {
		t.Fatalf("second publish: %d %s", status, body)
	}
	trace, _ := json.Marshal(decision.CheckReport{RuleID: created.ID, SourceRevision: oldRevision, ExecutionID: "historical-execution", At: time.Now(), Row: "r", State: "fired", Inputs: []decision.CheckInput{{Input: "p", Value: 97531.25, Quality: "good", Target: decision.Target{NodeID: private}, SourceTime: time.Now()}}})
	if err := client.SendNodePoint(h.nc, created.ID, data.NewPointString("lastCheck", "", string(trace)), true); err != nil {
		t.Fatal(err)
	}
	status, before := call(t, http.MethodGet, base+"/snapshot", viewer, nil, nil)
	if status != 200 {
		t.Fatalf("snapshot before move: %d %s", status, before)
	}
	var beforeSnapshot struct {
		Nodes []data.NodeEdge `json:"nodes"`
	}
	_ = json.Unmarshal([]byte(before), &beforeSnapshot)
	visibleTrace := ""
	for _, node := range beforeSnapshot.Nodes {
		if node.ID == created.ID {
			visibleTrace, _ = node.Points.Text("lastCheck", "")
		}
	}
	if !strings.Contains(visibleTrace, "97531.25") {
		t.Fatal("authorized trace hidden before move")
	}
	status, body = call(t, http.MethodPost, base+"/nodes/"+private+"/move", admin, map[string]string{"old_parent": own, "new_parent": other}, nil)
	if status != 200 {
		t.Fatalf("move: %d %s", status, body)
	}
	check := func(label string, raw string) {
		if strings.Contains(raw, private) || strings.Contains(raw, "97531.25") {
			t.Errorf("%s leaked moved reference or historical value: %s", label, raw)
		}
		var snapshot struct {
			Nodes []data.NodeEdge `json:"nodes"`
		}
		if json.Unmarshal([]byte(raw), &snapshot) != nil {
			t.Fatalf("invalid %s snapshot", label)
		}
		for _, node := range snapshot.Nodes {
			if node.ID == created.ID {
				for _, typ := range []string{"draft", "applied"} {
					text, _ := node.Points.Text(typ, "")
					var current decision.Document
					if json.Unmarshal([]byte(text), &current) != nil || current.Revision != doc.Revision || current.Bindings.Points["p"].NodeID != public {
						t.Errorf("%s damaged allowed current %s", label, typ)
					}
				}
				if access, _ := node.Points.Text("ruleAccess", ""); !strings.Contains(access, "restricted") {
					t.Errorf("%s did not explain restricted history", label)
				}
				return
			}
		}
		t.Errorf("%s removed the visible rule", label)
	}
	status, body = call(t, http.MethodGet, base+"/snapshot", viewer, nil, nil)
	if status != 200 {
		t.Fatal(body)
	}
	check("HTTP", body)
	headers := http.Header{"Authorization": []string{"Bearer " + viewer}}
	conn, _, err := websocket.DefaultDialer.Dial(strings.Replace(base, "http://", "ws://", 1)+"/ws", headers)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, frame, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	check("WS", string(frame))
	for _, kind := range []string{"lastCheck", "history"} {
		status, _ = call(t, http.MethodGet, base+"/history?node_id="+created.ID+"&type="+kind, viewer, nil, nil)
		if status != 403 {
			t.Errorf("generic %s history exposed: %d", kind, status)
		}
	}
	status, body = call(t, http.MethodPost, base+"/rules/"+created.ID+"/replay", viewer, map[string]any{"version_id": oldRevision}, nil)
	if status != 403 {
		t.Errorf("private publication replay exposed: %d %s", status, body)
	}
	nodes, _ := client.GetNodes(h.nc, "all", created.ID, "", false)
	stored, _ := nodes[0].Points.Text("lastCheck", "")
	if !strings.Contains(stored, "97531.25") {
		t.Fatal("outbound redaction mutated persisted execution evidence")
	}
}
