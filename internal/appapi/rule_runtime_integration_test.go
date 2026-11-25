package appapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"fastiot/internal/decision"
	"fastiot/internal/ems/bridge"
	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/command"
	"fastiot/internal/ems/modbusrt"
	"fastiot/internal/emsapi"
	"fastiot/internal/web"
	"github.com/nats-io/nats.go"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

// Reuse the private wire protocol simulator, on ticket13's dedicated port only.
func ruleSimulator(t *testing.T) *operationSimulator {
	listener, err := net.Listen("tcp", "127.0.0.1:18113")
	if err != nil {
		t.Fatal(err)
	}
	sim := &operationSimulator{values: map[byte]uint16{1: 10}, listener: listener}
	t.Cleanup(func() {
		listener.Close()
		sim.mu.Lock()
		defer sim.mu.Unlock()
		for _, c := range sim.conns {
			c.Close()
		}
	})
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			sim.mu.Lock()
			sim.conns = append(sim.conns, c)
			sim.mu.Unlock()
			go sim.serve(c)
		}
	}()
	return sim
}

func TestRuleSimulationZeroWritesAndCanonicalSequence(t *testing.T) {
	f := newPointConfigFixture(t)
	sim := ruleSimulator(t)
	f.handler.SetDataDir(f.dir)
	f.handler.SetEMS(emsapi.New(f.runtime, time.Second))
	WireCommandHistory(f.dir, f.runtime.Commands())
	f.bridge = bridge.New(f.runtime, bridge.NewNodeSource(f.handler.nc), func(protocol, address string, timeout time.Duration) (channel.Bus, error) {
		if protocol != "tcp" || address != "127.0.0.1:18113" {
			return nil, fmt.Errorf("non-fixture address refused")
		}
		return modbusrt.OpenTCP(address, timeout)
	})
	root := snapshotRoot(t, f.base, f.token)
	var bus struct {
		ID string `json:"id"`
	}
	status, body := call(t, http.MethodPost, f.base+"/modbus", f.token, ModbusConnectionInput{Parent: root, Description: "Rule private simulator", Protocol: "TCP", URI: "127.0.0.1:18113", PollPeriod: 50, Timeout: 100}, &bus)
	if status != 200 {
		t.Fatalf("bus: %d %s", status, body)
	}
	point := f.createPoint(t, bus.ID, ModbusIOInput{Description: "Target", ServerID: 1, Address: 10, IOType: "modbusHoldingRegister", DataFormat: "uint16", Scale: 1})
	loadSingleDeviceProfile(t, f.handler, operationProfile)
	status, body = call(t, http.MethodPut, fmt.Sprintf("%s/modbus/%s/units/1/model", f.base, bus.ID), f.token, unitModelConfig{Name: "Rule simulator", ProfileID: "operation-sim", PointMap: map[string]string{"Target": point}}, nil)
	if status != 200 {
		t.Fatalf("command mapping: %d %s", status, body)
	}
	if err := f.bridge.Sync(time.Now()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = f.runtime.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, func() bool { return f.runtime.State.Health(bus.ID+"/1") == "healthy" }, "rule simulator sample")
	doc := decision.Document{Scope: "descendants", Table: decision.Table{Rows: []decision.Row{{ID: "first", Conditions: []decision.Condition{{Kind: "time", Weekdays: 127}}, Actions: []decision.Action{
		{ActionID: "target", Value: 20, Readback: &decision.Readback{TimeoutMS: 5000}},
		{ActionID: "target", Value: 30, DelayMS: 250, Readback: &decision.Readback{TimeoutMS: 5000}},
	}}, {ID: "later"}}}, Bindings: decision.BindingSet{Actions: map[string]decision.Target{"target": {NodeID: point, CommandID: "set", Parameter: "target"}}}}
	var created struct {
		ID       string `json:"id"`
		Revision string `json:"revision"`
	}
	status, body = call(t, http.MethodPost, f.base+"/rules", f.token, map[string]any{"parent": root, "description": "canonical sequence", "document": doc}, &created)
	if status != 200 {
		t.Fatalf("create: %d %s", status, body)
	}
	doc.Revision = created.Revision
	manager := client.NewManager(f.handler.nc, func(nc *nats.Conn, config decision.DecisionTable) client.Client {
		return f.handler.NewRuleClient(nc, config)
	}, RuleParentTypes())
	managerDone := make(chan error, 1)
	go func() { managerDone <- manager.Run() }()
	t.Cleanup(func() { manager.Stop(nil); <-managerDone })
	for i, payload := range []any{map[string]any{"document": doc}, map[string]any{"document": doc, "inputs": map[string]any{"bad": "NaN"}}, map[string]any{"document": doc, "at": "not-a-time"}, map[string]any{"document": doc, "inputs": map[string]any{"bad": nil}}} {
		var result decision.SimulationResult
		status, body := call(t, http.MethodPost, f.base+"/rules/"+created.ID+"/test", f.token, payload, &result)
		want := 400
		if i == 0 {
			want = 200
		}
		if status != want {
			t.Fatalf("simulation case %d: %d %s", i, status, body)
		}
		if i == 0 && (result.RowID != "first" || len(result.Actions) != 2) {
			t.Fatalf("simulation did not evaluate the actual rule: %+v", result)
		}
	}
	ruleNodes, _ := client.GetNodes(f.handler.nc, "all", created.ID, "", false)
	published, _ := ruleNodes[0].Points.Text("applied", "")
	enabled, _ := ruleNodes[0].Points.Value("enabled", "")
	if published != "" || enabled != 0 {
		t.Fatal("simulation changed publication or authorization")
	}
	if len(sim.written()) != 0 || len(f.runtime.Commands().List()) != 0 {
		t.Fatal("simulation wrote or issued a real command")
	}
	status, body = call(t, http.MethodPost, f.base+"/rules/"+created.ID+"/publish", f.token, doc, nil)
	if status != 200 {
		t.Fatalf("publish: %d %s", status, body)
	}
	if len(sim.written()) != 0 {
		t.Fatal("publication wrote simulator")
	}
	if os.Getenv("FASTIOT_RULE_BROWSER") == "1" {
		t.Log("browser fixture setup only: no enable/run assertions; normal test separately covers canonical commands")
		serveRuleBrowser(t, f, sim, created.ID, root)
		return
	}
	status, body = call(t, http.MethodPost, f.base+"/rules/"+created.ID+"/enable", f.token, map[string]bool{"enabled": true}, nil)
	if status != 200 {
		t.Fatalf("enable: %d %s", status, body)
	}
	t.Cleanup(func() {
		if t.Failed() {
			nodes, _ := client.GetNodes(f.handler.nc, "all", created.ID, "", false)
			t.Logf("runtime diagnostics: nodes=%+v commands=%+v writes=%v", nodes, f.runtime.Commands().List(), sim.written())
		}
	})
	waitFor(t, func() bool {
		outcomes := f.runtime.Commands().List()
		return len(outcomes) == 2 && outcomes[0].State == command.Confirmed && outcomes[1].State == command.Confirmed
	}, "two canonical confirmed sequence commands")
	writes := sim.written()
	if len(writes) != 2 || writes[0][2] != 20 || writes[1][2] != 30 {
		t.Fatalf("wire sequence: %v", writes)
	}
	for _, outcome := range f.runtime.Commands().List() {
		if outcome.DispatchID == "" || !strings.Contains(outcome.Source, created.ID) || !strings.Contains(outcome.Source, doc.Revision) {
			t.Fatalf("lost source identity: %+v", outcome)
		}
	}
	// Exercise the production command callback with explicitly reserved fixture
	// executions. Same-ID retries must never reach the private wire twice.
	reserve := func() decision.RuleCommand {
		id := data.NewID()
		encoded, _ := json.Marshal(decision.ExecutionReservation{ID: id, Row: "first", Revision: doc.Revision, At: time.Now()})
		if err := client.SendNodePoint(f.handler.nc, created.ID, data.NewPointString("lastExecution", "", string(encoded)), true); err != nil {
			t.Fatal(err)
		}
		return decision.RuleCommand{RuleID: created.ID, Revision: doc.Revision, ExecutionID: id, RequestID: id + "-0", RowID: "first", Step: 0, Target: doc.Bindings.Actions["target"], Action: doc.Table.Rows[0].Actions[0]}
	}
	sim.mu.Lock()
	sim.readFailure = true
	sim.mu.Unlock()
	unknown := reserve()
	first, err := f.handler.submitRuleCommand(unknown)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := f.handler.submitRuleCommand(unknown)
	if err != nil || retry.RequestID != first.RequestID {
		t.Fatalf("accepted retry: %+v %v", retry, err)
	}
	waitFor(t, func() bool {
		outcome, ok := f.runtime.Commands().Get(first.RequestID)
		return ok && outcome.State == command.Unknown
	}, "canonical unknown timeout")
	sim.mu.Lock()
	sim.readFailure = false
	sim.mu.Unlock()
	waitFor(t, func() bool { return f.runtime.State.Health(bus.ID+"/1") == "healthy" }, "late simulator readback")
	retry, err = f.handler.submitRuleCommand(unknown)
	if err != nil || retry.State != command.Unknown || len(sim.written()) != 3 {
		t.Fatalf("unknown replay or late promotion: %+v %v writes=%v", retry, err, sim.written())
	}
	failedIntent := reserve()
	commands := f.runtime.Commands()
	persist := commands.Persist
	commands.Persist = func(command.Outcome) error { return errors.New("fixture journal failure") }
	_, err = f.handler.submitRuleCommand(failedIntent)
	commands.Persist = persist
	if err == nil || len(sim.written()) != 3 {
		t.Fatalf("journal failure reached IO: %v %v", err, sim.written())
	}
	status, body = call(t, http.MethodPost, f.base+"/nodes/"+point+"/manual", f.token, map[string]bool{"manual": true}, nil)
	if status != 200 {
		t.Fatalf("manual takeover: %d %s", status, body)
	}
	manual := reserve()
	rejected, err := f.handler.submitRuleCommand(manual)
	if err != nil || rejected.State != command.Rejected || len(sim.written()) != 3 {
		t.Fatalf("automatic actor bypassed takeover: %+v %v writes=%v", rejected, err, sim.written())
	}
}

func serveRuleBrowser(t *testing.T, f *pointConfigFixture, sim *operationSimulator, ruleID, root string) {
	status, body := call(t, http.MethodPost, f.base+"/rules/"+ruleID+"/enable", f.token, map[string]bool{"enabled": false}, nil)
	if status != 200 {
		t.Fatalf("browser fixture disable: %d %s", status, body)
	}
	done := make(chan struct{}, 1)
	child := createNodeAPI(t, f.base, f.token, "group", root, "Rule descendant", 0)
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", f.handler))
	mux.HandleFunc("GET /__fixture", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"fixture": "ticket13-private-simulator", "rule": ruleID, "root": root, "child": child, "token": f.token, "writes": len(sim.written())})
	})
	mux.HandleFunc("POST /__finish", func(w http.ResponseWriter, r *http.Request) {
		select {
		case done <- struct{}{}:
		default:
		}
		w.WriteHeader(204)
	})
	mux.Handle("/", http.FileServer(http.FS(web.FS())))
	server := &http.Server{Addr: ":18013", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Error(err)
			done <- struct{}{}
		}
	}()
	defer server.Close()
	t.Log("ticket13 browser fixture ready on port 18013; simulator private on 18113")
	select {
	case <-done:
	case <-time.After(15 * time.Minute):
		t.Fatal("rule browser fixture timed out")
	}
}
