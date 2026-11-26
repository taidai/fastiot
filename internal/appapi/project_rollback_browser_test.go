package appapi

import (
	"net/http"
	"os"
	"testing"
	"time"

	"fastiot/internal/calculation"
	"fastiot/internal/ems/alarm"
	"fastiot/internal/notification"
	"fastiot/internal/web"
	"github.com/simpleiot/simpleiot/data"
)

// Opt-in private browser fixture. The only device is the existing loopback
// operation simulator; notifications are in-app records, never external sends.
func TestTicket23BrowserBackend(t *testing.T) {
	if os.Getenv("FASTIOT_TICKET23_BROWSER") != "1" {
		t.Skip("ticket23 isolated browser fixture is opt-in")
	}
	f, bus, sim := operationFixture(t)
	f.handler.SetDataDir(f.dir)
	root := snapshotRoot(t, f.base, f.token)
	site := ""
	for _, node := range snapshotNodes(t, f.base, f.token) {
		if node.Type == NodeTypeSite && node.Desc() == "Operation site" {
			site = node.ID
		}
	}
	if site == "" {
		t.Fatal("operation site not found")
	}
	variable := createNodeAPI(t, f.base, f.token, "variable", site, "Rollback software history", 0)
	publish := func(note string) string {
		var report preflightReport
		status, body := call(t, http.MethodGet, f.base+"/delivery/gates?site="+site, f.token, nil, &report)
		if status != http.StatusOK || report.Status == "fail" || report.Fingerprint == "" {
			t.Fatalf("preflight %s: %d %s", note, status, body)
		}
		var version versionSummary
		status, body = call(t, http.MethodPost, f.base+"/projects/publish", f.token, map[string]any{
			"site": site, "note": note, "expected_fingerprint": report.Fingerprint,
		}, &version)
		if status != http.StatusOK {
			t.Fatalf("publish %s: %d %s", note, status, body)
		}
		return version.ID
	}
	goodVersion := publish("ticket23 supported baseline")

	group := createNodeAPI(t, f.base, f.token, "group", site, "Unsupported target resource", 0)
	var calculated calcResponse
	definition := calculation.Definition{Operator: "add", Inputs: []string{variable, variable}, Kind: "number", Unit: "kW"}
	status, body := call(t, http.MethodPost, f.base+"/calculations", f.token, map[string]any{
		"parent": group, "name": "Rollback unsupported calculation", "definition": definition, "operation_id": "ticket23-calc-create",
	}, &calculated)
	if status != http.StatusOK {
		t.Fatalf("calculation create: %d %s", status, body)
	}
	status, body = call(t, http.MethodPost, f.base+"/calculations/"+calculated.ID+"/apply", f.token, map[string]any{
		"revision": calculated.Revision, "operation_id": "ticket23-calc-apply",
	}, &calculated)
	if status != http.StatusOK {
		t.Fatalf("calculation apply: %d %s", status, body)
	}
	badVersion := publish("ticket23 unsupported calculation")
	status, body = call(t, http.MethodDelete, f.base+"/nodes/"+calculated.ID, f.token, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("remove live calculation: %d %s", status, body)
	}
	status, body = call(t, http.MethodPatch, f.base+"/nodes/"+site, f.token, map[string]any{"description": "Current before rollback"}, nil)
	if status != http.StatusOK {
		t.Fatalf("change current configuration: %d %s", status, body)
	}

	var model unitModelView
	status, body = call(t, http.MethodGet, f.base+"/modbus/"+bus+"/units/1/model", f.token, nil, &model)
	if status != http.StatusOK {
		t.Fatalf("command model: %d %s", status, body)
	}
	target := model.Model.PointMap["Target"]
	status, body = call(t, http.MethodPut, f.base+"/safety", f.token, map[string]any{
		"node": target, "safety": map[string]any{"conditions": []any{map[string]any{"point_id": target, "operator": "eq", "value": 99}}},
	}, nil)
	if status != http.StatusOK {
		t.Fatalf("interlock fixture: %d %s", status, body)
	}
	commandRequestID := "ticket23-rejected-command"
	var rejected pumpCommandOutcome
	status, body = call(t, http.MethodPost, f.base+"/modbus/"+bus+"/units/1/commands", f.token, map[string]any{
		"request_id": commandRequestID, "command_id": "set", "parameters": map[string]float64{"target": 20},
	}, &rejected)
	if status != http.StatusOK || rejected.State != "rejected" || len(sim.written()) != 0 {
		t.Fatalf("rejected command fixture: %d %s %+v writes=%v", status, body, rejected, sim.written())
	}
	for _, value := range []float64{12.5, 13.5} {
		status, body = call(t, http.MethodPost, f.base+"/nodes/"+variable+"/value", f.token, map[string]any{"value": value, "point_type": "value"}, nil)
		if status != http.StatusOK {
			t.Fatalf("history fixture: %d %s", status, body)
		}
	}
	var history struct {
		Total int `json:"total_matched"`
	}
	status, body = call(t, http.MethodGet, f.base+"/history?node_id="+variable+"&type=value", f.token, nil, &history)
	if status != http.StatusOK || history.Total == 0 {
		t.Fatalf("history readback: %d %s", status, body)
	}
	occurrence, err := f.runtime.Alarms.Raise(time.Now(), "ticket23-alarm", alarm.Severity("warning"), "Ticket23 preserved alarm", "", site, variable)
	if err != nil {
		t.Fatal(err)
	}

	username := "rollback@fixture.local"
	password := data.NewID()
	var account struct {
		ID string `json:"id"`
	}
	status, body = call(t, http.MethodPost, f.base+"/users", f.token, map[string]any{
		"parent": root, "email": username, "password": password, "role": "admin",
	}, &account)
	if status != http.StatusOK {
		t.Fatalf("browser account: %d %s", status, body)
	}
	browserToken := loginAPI(t, f.base, username, password)
	created, err := f.handler.CreateRuleNotification(Identity{UserID: "fixture-admin", Policy: AccessPolicy{Admin: true}, Anchors: []string{root}}, notification.Request{
		SourceRevision: "1", RuleID: "ticket23-rule", ExecutionID: "ticket23-execution", ActionID: "ticket23-action",
		ScopeNodeID: site, RecipientIDs: []string{account.ID}, Title: "Ticket23 preserved notification", Body: "in-app only",
	})
	if err != nil {
		t.Fatal(err)
	}
	status, body = call(t, http.MethodPost, f.base+"/notifications/"+created.ID+"/read", browserToken, map[string]any{}, nil)
	if status != http.StatusOK {
		t.Fatalf("notification read state: %d %s", status, body)
	}

	done := make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", f.handler))
	mux.HandleFunc("GET /__fixture", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"fixture": "ticket23-private", "username": username, "password": password,
			"site": site, "variable": variable, "bus": bus, "target": target,
			"good_version": goodVersion, "bad_version": badVersion, "calculation": calculated.ID,
			"command_request": commandRequestID, "notification": created.ID, "alarm": occurrence.ID,
			"physical_writes": len(sim.written()),
		})
	})
	mux.HandleFunc("POST /__finish", func(w http.ResponseWriter, r *http.Request) {
		select {
		case done <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("/", http.FileServer(http.FS(web.FS())))
	server := &http.Server{Addr: "127.0.0.1:18230", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Error(err)
			done <- struct{}{}
		}
	}()
	defer server.Close()
	select {
	case <-done:
	case <-time.After(20 * time.Minute):
		t.Fatal("ticket23 browser did not finish")
	}
	if writes := sim.written(); len(writes) != 0 {
		t.Fatalf("browser workflow issued physical writes: %v", writes)
	}
}
