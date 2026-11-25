package appapi

import (
	"encoding/json"
	"testing"
	"time"

	"fastiot/internal/ems/runtime"
	"fastiot/internal/emsapi"
	"fastiot/internal/notification"

	"fastiot/internal/decision"
	"github.com/nats-io/nats.go"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

func TestTodayExecutionsTerminalFactsSurviveSummaryLossAndRestart(t *testing.T) {
	for _, mode := range []string{"disable", "stop", "applied", "condition", "move", "readback", "crash", "crash-no-summary", "readback-crash"} {
		t.Run(mode, func(t *testing.T) {
			dir, password := t.TempDir(), data.NewID()
			base, admin, h, stopServer := startInputServer(t, dir, password)
			startRuntime := func() *runtime.Service {
				s := runtime.NewService(nil, time.Minute, time.Second, 1000)
				h.SetEMS(emsapi.New(s, time.Minute))
				WireCommandHistory(dir, s.Commands())
				return s
			}
			service := startRuntime()
			defer func() { service.Close() }()
			root := snapshotRoot(t, base, admin)
			recipient := inboxAccount(t, base, admin, root, "terminal@fixture.local", password)
			actions := []decision.Action{{ActionID: "created", Notification: &decision.RuleNotification{RecipientIDs: []string{recipient}, Title: "Original creation receipt"}}, {ActionID: "pending", DelayMS: 60000, Notification: &decision.RuleNotification{RecipientIDs: []string{recipient}, Title: "Never dispatched"}}}
			doc := decision.Document{Scope: "self", Table: decision.Table{Rows: []decision.Row{{ID: "row", Actions: actions}}}}
			expectedWait, index, expectedNotices := "waiting-delay", 1, 1
			permit := ""
			if mode == "readback" || mode == "readback-crash" {
				target := createNodeAPI(t, base, admin, "variable", root, "Private software request without acknowledgment", 0)
				doc.Table.Rows[0].Actions = []decision.Action{{ActionID: "pending", Value: 4, Readback: &decision.Readback{TimeoutMS: 60000}}}
				doc.Bindings.Actions = map[string]decision.Target{"pending": {NodeID: target}}
				expectedWait, index, expectedNotices = "waiting-readback", 0, 0
			}
			if mode == "readback-crash" {
				acknowledged := createNodeAPI(t, base, admin, "variable", root, "Explicit software acknowledgment", 0)
				stopAck, err := client.SubscribePoints(h.nc, acknowledged, func(points []data.Point) {
					for _, point := range points {
						if point.Type == data.PointTypeValueSet {
							value, _ := point.ValueFloat()
							_ = client.SendNodePoint(h.nc, acknowledged, data.NewPointFloat(data.PointTypeValue, "", value), true)
						}
					}
				})
				if err != nil {
					t.Fatal(err)
				}
				defer stopAck()
				doc.Bindings.Actions["confirmed"] = decision.Target{NodeID: acknowledged}
				doc.Table.Rows[0].Actions = append([]decision.Action{{ActionID: "confirmed", Value: 3, Readback: &decision.Readback{TimeoutMS: 60000}}}, doc.Table.Rows[0].Actions...)
				index = 1
			}
			if mode == "condition" {
				permit = createNodeAPI(t, base, admin, "variable", root, "Private observed permit", 1)
				doc.Table.Rows[0].Conditions = []decision.Condition{{PointID: "permit", Operator: "eq", Value: 1}}
				doc.Bindings.Points = map[string]decision.Target{"permit": {NodeID: permit}}
			}
			var created struct {
				ID       string `json:"id"`
				Revision string `json:"revision"`
			}
			status, text := call(t, "POST", base+"/rules", admin, map[string]any{"parent": root, "description": "Terminal " + mode, "document": doc}, &created)
			if status != 200 {
				t.Fatal(text)
			}
			doc.Revision = created.Revision
			status, text = call(t, "POST", base+"/rules/"+created.ID+"/publish", admin, doc, nil)
			if status != 200 {
				t.Fatal(text)
			}
			startManager := func() func() {
				m := client.NewManager(h.nc, func(nc *nats.Conn, c decision.DecisionTable) client.Client { return h.NewRuleClient(nc, c) }, RuleParentTypes())
				done := make(chan error, 1)
				go func() { done <- m.Run() }()
				running := true
				return func() {
					if running {
						m.Stop(nil)
						<-done
						running = false
					}
				}
			}
			status, text = call(t, "POST", base+"/rules/"+created.ID+"/enable", admin, map[string]bool{"enabled": true}, nil)
			if status != 200 {
				t.Fatal(text)
			}
			// Start from the persisted enabled configuration, with no activation event
			// racing the manager's initial discovery snapshot.
			stopManager := startManager()
			defer func() { stopManager() }()
			query := func() executionPage {
				t.Helper()
				var page executionPage
				status, text := call(t, "GET", base+"/overview/executions?layer="+root+"&timezone=UTC&source=rule", admin, nil, &page)
				if status != 200 {
					t.Fatalf("query: %d %s", status, text)
				}
				return page
			}
			var original executionView
			defer func() {
				if t.Failed() {
					nodes, _ := client.GetNodes(h.nc, "all", created.ID, "", false)
					if len(nodes) == 1 {
						for _, typ := range []string{"lastCheck", "error", "lastExecution", "enabled"} {
							point, _ := nodes[0].Points.Find(typ, "")
							t.Logf("terminal diagnostic %s: %s %+v", typ, point.Txt(), point)
						}
					}
					t.Logf("terminal query: %+v", query())
				}
			}()
			waitFor(t, func() bool {
				for _, row := range query().Items {
					if row.Counted && len(row.Actions) > index && row.Actions[index].Status == expectedWait {
						original = row
						return true
					}
				}
				return false
			}, "real producer "+expectedWait)
			if original.ExecutionID == "" || original.SourceRevision != created.Revision {
				t.Fatalf("missing source identity: %+v", original)
			}
			if expectedNotices == 1 && (original.Actions[0].Status != "created" || original.Actions[0].NotificationID == "") {
				t.Fatalf("missing actual creation receipt: %+v", original)
			}
			if mode == "readback-crash" && (original.Actions[0].Status != "confirmed" || original.Actions[0].CommandID == "" || original.Actions[0].CanonicalStatus != "confirmed" || original.Actions[0].Confirmation != "software") {
				t.Fatalf("missing original software confirmation: %+v", original)
			}
			switch mode {
			case "crash", "crash-no-summary", "readback-crash":
				if mode == "crash-no-summary" {
					if err := client.SendNodePoint(h.nc, created.ID, data.NewPointString("lastCheck", "", ""), true); err != nil {
						t.Fatal(err)
					}
				}
				// Lose the producer connection before it can journal abandonment. This
				// exercises real persistence/recovery, not a fabricated lastCheck payload.
				h.nc.Close()
				stopManager()
				service.Close()
				stopServer()
				base, admin, h, stopServer = startInputServer(t, dir, password)
				service = startRuntime()
				stopManager = startManager()
			case "stop":
				stopManager()
			case "applied":
				// The public gate stays intact; exercise the trusted configuration-event
				// path used by internal configuration replacement, not an HTTP bypass.
				status, text = call(t, "POST", base+"/rules/"+created.ID+"/publish", admin, doc, nil)
				if status != 409 {
					t.Fatalf("publish gate changed: %d %s", status, text)
				}
				doc.Revision = data.NewID()
				raw, _ := json.Marshal(doc)
				if err := client.SendNodePoint(h.nc, created.ID, data.NewPointString("applied", "", string(raw)), true); err != nil {
					t.Fatal(err)
				}
			case "condition":
				if err := client.SendNodePoint(h.nc, permit, data.NewPointFloat(data.PointTypeValue, "", 0), true); err != nil {
					t.Fatal(err)
				}
			case "move":
				destination := createNodeAPI(t, base, admin, "group", root, "Moved source", 0)
				// Exercise the trusted edge-event path, without changing HTTP move guards.
				if err := client.MoveNode(h.nc, created.ID, root, destination, "private-fixture"); err != nil {
					t.Fatal(err)
				}
			default:
				status, text = call(t, "POST", base+"/rules/"+created.ID+"/enable", admin, map[string]bool{"enabled": false}, nil)
				if status != 200 {
					t.Fatal(text)
				}
			}
			waitFor(t, func() bool {
				for _, row := range query().Items {
					if row.ExecutionID == original.ExecutionID && row.Status == "unknown" && len(row.Actions) > index && row.Actions[index].Status == "unknown" {
						return true
					}
				}
				return false
			}, "retained terminal outcome")
			check := func() {
				t.Helper()
				page := query()
				if page.ExecutionCount != 1 {
					t.Fatalf("execution counted again: %+v", page)
				}
				found := false
				for _, row := range page.Items {
					if row.ExecutionID != original.ExecutionID {
						continue
					}
					found = true
					if row.ID != original.ID || row.SourceRevision != original.SourceRevision || !row.At.Equal(original.At) || row.Status != "unknown" || row.Actions[index].Status != "unknown" {
						t.Fatalf("terminal identity/result changed: %+v", row)
					}
					if mode == "readback-crash" && (row.Actions[0].Status != "confirmed" || row.Actions[0].CommandID != original.Actions[0].CommandID || row.Actions[0].CanonicalStatus != "confirmed" || row.Actions[0].Confirmation != "software") {
						t.Fatalf("prior software confirmation overwritten: %+v", row)
					}
					if expectedNotices == 1 && (row.Actions[0].Status != "created" || row.Actions[0].NotificationID != original.Actions[0].NotificationID) {
						t.Fatalf("prior receipt overwritten: %+v", row)
					}
				}
				if !found {
					t.Fatal("terminal fact disappeared")
				}
			}
			check()
			stopManager()
			if err := client.SendNodePoint(h.nc, created.ID, data.NewPointString("lastCheck", "", ""), true); err != nil {
				t.Fatal(err)
			}
			check()
			service.Close()
			stopServer()
			base, admin, h, _ = startInputServer(t, dir, password)
			service = startRuntime()
			stopManager = startManager()
			check()
			token := loginAPI(t, base, "terminal@fixture.local", password)
			var inbox notification.Page
			status, text = call(t, "GET", base+"/notifications", token, nil, &inbox)
			if status != 200 || len(inbox.Items) != expectedNotices {
				t.Fatalf("stopped work replayed: %d %s", status, text)
			}
		})
	}
}

// Independent review: a real producer starts a delayed notification, then is
// disabled before dispatch. Its terminal lastCheck must not be masked forever
// by an older captured waiting-delay fact. No physical or notification writes.
func TestReview16StoppedSequenceRetainsTerminalOutcome(t *testing.T) {
	base, admin, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, admin)
	recipient := inboxAccount(t, base, admin, root, "review-stop@fixture.local", "private-fixture")
	doc := decision.Document{Scope: "self", Table: decision.Table{Rows: []decision.Row{{ID: "delayed", Actions: []decision.Action{{ActionID: "notice", DelayMS: 60000, Notification: &decision.RuleNotification{RecipientIDs: []string{recipient}, Title: "Must never dispatch"}}}}}}}
	var created struct {
		ID       string `json:"id"`
		Revision string `json:"revision"`
	}
	status, text := call(t, "POST", base+"/rules", admin, map[string]any{"parent": root, "description": "Review stop", "document": doc}, &created)
	if status != 200 {
		t.Fatal(text)
	}
	doc.Revision = created.Revision
	status, text = call(t, "POST", base+"/rules/"+created.ID+"/publish", admin, doc, nil)
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
	var before decision.CheckReport
	readReport := func() decision.CheckReport {
		nodes, err := client.GetNodes(h.nc, "all", created.ID, "", false)
		if err != nil || len(nodes) != 1 {
			t.Fatalf("read source: %v", err)
		}
		raw, _ := nodes[0].Points.Text("lastCheck", "")
		var report decision.CheckReport
		_ = json.Unmarshal([]byte(raw), &report)
		return report
	}
	waitFor(t, func() bool {
		before = readReport()
		return before.ExecutionID != "" && len(before.Actions) == 1 && before.Actions[0].Status == "waiting-delay"
	}, "real delayed execution")
	status, text = call(t, "POST", base+"/rules/"+created.ID+"/enable", admin, map[string]bool{"enabled": false}, nil)
	if status != 200 {
		t.Fatal(text)
	}
	waitFor(t, func() bool {
		r := readReport()
		return r.ExecutionID == before.ExecutionID && r.State == "unknown" && r.Actions[0].Status == "unknown"
	}, "persisted canonical stop report")
	var page executionPage
	status, text = call(t, "GET", base+"/overview/executions?layer="+root+"&timezone=UTC&source=rule", admin, nil, &page)
	if status != 200 || len(page.Items) != 1 {
		t.Fatalf("query: %d %s", status, text)
	}
	if page.Items[0].Status != "unknown" || len(page.Items[0].Actions) != 1 || page.Items[0].Actions[0].Status != "unknown" {
		t.Fatalf("terminal persisted lastCheck is unknown, but retained query masks it: %s", text)
	}
}
