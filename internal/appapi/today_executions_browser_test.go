package appapi

import (
	"fmt"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"encoding/json"
	"fastiot/internal/decision"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/emsapi"
	"github.com/nats-io/nats.go"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

// Private, opt-in fixture. No bus, external service, or physical writer is started.
func TestTodayExecutionsBrowserBackend(t *testing.T) {
	if os.Getenv("FASTIOT_TODAY_BROWSER") != "1" {
		t.Skip("private browser fixture only")
	}
	dir := os.Getenv("FASTIOT_TODAY_STORE")
	if dir == "" {
		t.Fatal("isolated store required")
	}
	base, token, h, stop := startInputServer(t, dir, "ticket16-private-only")
	defer func() { stop() }()
	startRuntime := func() *runtime.Service {
		service := runtime.NewService(nil, time.Minute, time.Second, 1000)
		h.SetEMS(emsapi.New(service, time.Minute))
		WireCommandHistory(dir, service.Commands())
		return service
	}
	service := startRuntime()
	defer func() { service.Close() }()
	root := snapshotRoot(t, base, token)
	scope := createNodeAPI(t, base, token, "site", root, "Today private site", 0)
	empty := createNodeAPI(t, base, token, "site", root, "No retained executions", 0)
	input := createNodeAPI(t, base, token, "variable", scope, "Historical guard", 7)
	output := createNodeAPI(t, base, token, "variable", scope, "Explicit software acknowledgment", 0)
	stopFeedback, err := client.SubscribePoints(h.nc, output, func(points []data.Point) {
		for _, point := range points {
			if point.Type == data.PointTypeValueSet {
				value, _ := point.ValueFloat()
				_ = client.SendNodePoint(h.nc, output, data.NewPointFloat(data.PointTypeValue, "", value), true)
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stopFeedback()
	recipient := inboxAccount(t, base, token, scope, "today-viewer@fixture.local", "ticket16-private-only")
	definition := decision.StateMachineDefinition{InitialState: "idle", States: []string{"idle"}, Transitions: []decision.StateTransition{{ID: "guarded", From: "idle", To: "idle", Event: "tick", Conditions: []decision.Condition{{PointID: "guard", Operator: "eq", Value: 99}}}}, Bindings: decision.BindingSet{Points: map[string]decision.Target{"guard": {NodeID: input}}}}
	definition.Transitions = append(definition.Transitions, decision.StateTransition{ID: "software", From: "idle", To: "idle", Event: "software", Actions: []decision.Action{{ActionID: "set", Value: 1, Readback: &decision.Readback{TimeoutMS: 1000}}}})
	definition.Bindings.Actions = map[string]decision.Target{"set": {NodeID: output}}
	var machine stateMachineResponse
	must := func(path string, body any) {
		t.Helper()
		status, text := call(t, "POST", base+path, token, body, &machine)
		if status != 200 {
			t.Fatalf("%s: %d %s", path, status, text)
		}
	}
	must("/state-machines", map[string]any{"parent": scope, "name": "Today saved process", "definition": definition})
	path := "/state-machines/" + machine.ID
	must(path+"/publish", map[string]any{"revision": machine.Revision})
	for i := 0; i < 7; i++ {
		must(path+"/execute", map[string]any{"request_id": fmt.Sprintf("browser-event-%d", i), "event": "tick", "revision": machine.Revision, "confirm": true})
	}
	must(path+"/execute", map[string]any{"request_id": "browser-software-event", "event": "software", "revision": machine.Revision, "confirm": true})
	if machine.Execution == nil || machine.Execution.Status != "confirmed" {
		t.Fatalf("software fixture not confirmed: %+v", machine.Execution)
	}
	notice := decision.Document{Scope: "self", Table: decision.Table{Rows: []decision.Row{{ID: "notice", Actions: []decision.Action{{ActionID: "notify", Notification: &decision.RuleNotification{RecipientIDs: []string{recipient}, Title: "Today private notification"}}}}}}}
	var rule struct {
		ID       string `json:"id"`
		Revision string `json:"revision"`
	}
	status, text := call(t, "POST", base+"/rules", token, map[string]any{"parent": scope, "description": "Today notification rule", "document": notice}, &rule)
	if status != 200 {
		t.Fatal(text)
	}
	notice.Revision = rule.Revision
	status, text = call(t, "POST", base+"/rules/"+rule.ID+"/publish", token, notice, nil)
	if status != 200 {
		t.Fatal(text)
	}
	startRules := func() func() {
		m := client.NewManager(h.nc, func(nc *nats.Conn, c decision.DecisionTable) client.Client { return h.NewRuleClient(nc, c) }, RuleParentTypes())
		done := make(chan error, 1)
		go func() { done <- m.Run() }()
		return func() { m.Stop(nil); <-done }
	}
	stopRules := startRules()
	defer func() { stopRules() }()
	status, text = call(t, "POST", base+"/rules/"+rule.ID+"/enable", token, map[string]bool{"enabled": true}, nil)
	if status != 200 {
		t.Fatal(text)
	}
	waitFor(t, func() bool {
		nodes, _ := client.GetNodes(h.nc, "all", rule.ID, "", false)
		if len(nodes) == 0 {
			return false
		}
		raw, _ := nodes[0].Points.Text("lastCheck", "")
		var report decision.CheckReport
		_ = json.Unmarshal([]byte(raw), &report)
		return len(report.Actions) == 1 && report.Actions[0].Status == "created"
	}, "private rule notification")
	var mu sync.RWMutex
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		mu.RLock()
		defer mu.RUnlock()
		http.StripPrefix("/api", h).ServeHTTP(w, r)
	})
	mux.Handle("/uniapp/", http.StripPrefix("/uniapp/", http.FileServer(http.Dir("../web/dist/uniapp"))))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/uniapp/", 302) })
	mux.HandleFunc("GET /__fixture", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"fixture": "ticket16-private", "root": root, "scope": scope, "empty": empty, "machine": machine.ID, "input": input})
	})
	mux.HandleFunc("POST /__restart", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		stopRules()
		service.Close()
		stop()
		base, token, h, stop = startInputServer(t, dir, "ticket16-private-only")
		service = startRuntime()
		stopRules = startRules()
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /__revoke", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if err := client.MoveNode(h.nc, input, scope, empty, "private-fixture"); err != nil {
			http.Error(w, "move failed", 500)
			return
		}
		w.WriteHeader(204)
	})
	done := make(chan struct{}, 1)
	mux.HandleFunc("POST /__finish", func(w http.ResponseWriter, r *http.Request) {
		select {
		case done <- struct{}{}:
		default:
		}
		w.WriteHeader(204)
	})
	srv := &http.Server{Addr: ":18160", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Error(err)
			done <- struct{}{}
		}
	}()
	defer srv.Close()
	select {
	case <-done:
	case <-time.After(30 * time.Minute):
		t.Fatal("browser fixture did not finish")
	}
}
