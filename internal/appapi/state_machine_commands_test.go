package appapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"testing"

	"fastiot/internal/ems/command"
	"time"

	"fastiot/internal/decision"
	"fastiot/internal/ems/bridge"
	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/modbusrt"
	"fastiot/internal/emsapi"
)

// Reuse the 07 Modbus wire simulator on ticket 14's private, loopback-only port.
func machineCommandFixture(t *testing.T) (*pointConfigFixture, string, string, *operationSimulator) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:18114")
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
	f := newPointConfigFixture(t)
	f.handler.SetEMS(emsapi.New(f.runtime, time.Second))
	loadSingleDeviceProfile(t, f.handler, operationProfile)
	f.bridge = bridge.New(f.runtime, bridge.NewNodeSource(f.handler.nc), func(protocol, address string, timeout time.Duration) (channel.Bus, error) {
		if protocol != "tcp" || address != "127.0.0.1:18114" {
			return nil, fmt.Errorf("non-fixture address refused")
		}
		return modbusrt.OpenTCP(address, timeout)
	})
	root := snapshotRoot(t, f.base, f.token)
	site := createNodeAPI(t, f.base, f.token, "site", root, "State machine fixture", 0)
	business := createNodeAPI(t, f.base, f.token, "device", site, "Declared command simulator", 0)
	var bus struct {
		ID string `json:"id"`
	}
	status, body := call(t, http.MethodPost, f.base+"/modbus", f.token, ModbusConnectionInput{Parent: site, Description: "ticket14-private", Protocol: "TCP", URI: "127.0.0.1:18114", PollPeriod: 50, Timeout: 100}, &bus)
	if status != 200 {
		t.Fatalf("bus: %d %s", status, body)
	}
	input := map[string]any{"profile_id": "operation-sim", "servers": []int{1}, "business_node_id": business, "create_points": true, "dry_run": true}
	var preview struct {
		Version string `json:"preview_version"`
	}
	status, body = call(t, http.MethodPost, f.base+"/modbus/"+bus.ID+"/units/apply", f.token, input, &preview)
	if status != 200 {
		t.Fatalf("preview: %d %s", status, body)
	}
	input["dry_run"], input["preview_version"] = false, preview.Version
	status, body = call(t, http.MethodPost, f.base+"/modbus/"+bus.ID+"/units/apply", f.token, input, nil)
	if status != 200 {
		t.Fatalf("apply: %d %s", status, body)
	}
	if err := f.bridge.Sync(time.Now()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = f.runtime.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, func() bool { return f.runtime.State.Health(bus.ID+"/1") == "healthy" }, "ticket14 simulator samples")
	WireCommandHistory(f.dir, f.runtime.Commands())
	return f, site, business, sim
}

func TestStateMachineStateCommitFailureNeverReplaysConfirmedCommand(t *testing.T) {
	f, site, business, sim := machineCommandFixture(t)
	// Break only the API's storage connection, not the simulator or canonical
	// journal. The actual command still writes and confirms through 07.
	storageOptions := f.handler.nc.Opts
	storageOptions.ClosedCB = nil
	storageOptions.DisconnectedErrCB = nil
	storageOptions.ReconnectedCB = nil
	apiConnection, err := storageOptions.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer apiConnection.Close()
	f.handler.nc = apiConnection
	definition := decision.StateMachineDefinition{InitialState: "idle", Scope: "subtree", States: []string{"idle", "active"}, Transitions: []decision.StateTransition{{ID: "start", From: "idle", Event: "start", To: "active", Actions: []decision.Action{{ActionID: "set"}}}}, CommandBindings: map[string]decision.StateMachineCommandBinding{"set": {DeviceID: business, CommandID: "set", Parameters: map[string]float64{"target": 20}}}}
	var machine stateMachineResponse
	status, body := call(t, "POST", f.base+"/state-machines", f.token, map[string]any{"parent": site, "name": "Commit failure", "definition": definition}, &machine)
	if status != 200 {
		t.Fatalf("create: %d %s", status, body)
	}
	url := f.base + "/state-machines/" + machine.ID
	status, body = call(t, "POST", url+"/publish", f.token, map[string]any{"revision": machine.Revision}, &machine)
	if status != 200 {
		t.Fatalf("publish: %d %s", status, body)
	}
	manager := f.runtime.Commands()
	manager.Mu.Lock()
	persist := manager.Persist
	var once sync.Once
	manager.Persist = func(outcome command.Outcome) error {
		if err := persist(outcome); err != nil {
			return err
		}
		if outcome.State == command.Confirmed {
			once.Do(apiConnection.Close)
		}
		return nil
	}
	manager.Mu.Unlock()
	request := map[string]any{"request_id": "commit-failure", "event": "start", "revision": machine.Revision, "confirm": true}
	status, body = call(t, "POST", url+"/execute", f.token, request, &machine)
	_ = json.Unmarshal([]byte(body), &machine)
	if status != 503 || machine.Execution == nil || machine.Execution.Status != "unknown" || len(sim.written()) != 1 {
		t.Fatalf("state persistence failure claimed completion: %d %s", status, body)
	}
	replacement, err := storageOptions.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	f.handler.nc = replacement
	status, body = call(t, "POST", url+"/execute", f.token, request, &machine)
	if status != 200 || machine.Execution == nil || machine.Execution.Status != "unknown" || machine.CurrentState != "idle" || len(sim.written()) != 1 {
		t.Fatalf("recovery replayed command or guessed state: %d %s", status, body)
	}
	status, body = call(t, "GET", url, f.token, nil, &machine)
	if status != 200 || machine.LastExecution == nil || machine.LastExecution.RequestID != "commit-failure" || machine.LastExecution.Status != "unknown" {
		t.Fatalf("uncertain process result not queryable: %d %s", status, body)
	}
}

func TestStateMachineDeclaredCommandHTTPAndNoDuplicateWireWrites(t *testing.T) {
	f, site, business, sim := machineCommandFixture(t)
	definition := decision.StateMachineDefinition{InitialState: "idle", Scope: "subtree", States: []string{"idle", "active"},
		Transitions:     []decision.StateTransition{{ID: "start", From: "*", Event: "start", To: "active", Actions: []decision.Action{{ActionID: "set"}}}},
		CommandBindings: map[string]decision.StateMachineCommandBinding{"set": {DeviceID: business, CommandID: "set", Parameters: map[string]float64{"target": 20}}},
	}
	var machine stateMachineResponse
	status, body := call(t, "POST", f.base+"/state-machines", f.token, map[string]any{"parent": site, "name": "Profile machine", "definition": definition}, &machine)
	if status != 200 {
		t.Fatalf("create: %d %s", status, body)
	}
	url := f.base + "/state-machines/" + machine.ID
	status, body = call(t, "POST", url+"/simulate", f.token, map[string]any{"event": "start"}, &machine)
	if status != 200 || machine.Simulation == nil || machine.Simulation.Status != "matched" || len(sim.written()) != 0 {
		t.Fatalf("pure simulation: %d %s writes=%v", status, body, sim.written())
	}
	status, body = call(t, "POST", url+"/publish", f.token, map[string]any{"revision": machine.Revision}, &machine)
	if status != 200 || len(sim.written()) != 0 {
		t.Fatalf("publish activated control: %d %s", status, body)
	}
	if status, body := call(t, "POST", url+"/execute", f.token, map[string]any{"request_id": "unconfirmed", "event": "start", "revision": machine.Revision}, nil); status != 400 || len(sim.written()) != 0 {
		t.Fatalf("missing explicit confirmation dispatched: %d %s", status, body)
	}
	viewer := inputAccount(t, f, site, "viewer", nil)
	request := map[string]any{"request_id": "machine-start", "event": "start", "revision": machine.Revision, "confirm": true}
	if status, body := call(t, "POST", url+"/execute", viewer, request, nil); status != 403 || len(sim.written()) != 0 {
		t.Fatalf("viewer executed: %d %s", status, body)
	}
	status, body = call(t, "POST", url+"/execute", f.token, request, &machine)
	if status != 200 || machine.Execution == nil || machine.Execution.Status != "confirmed" || machine.CurrentState != "active" || len(machine.Execution.Commands) != 1 {
		t.Fatalf("declared command not confirmed: %d %s", status, body)
	}
	if writes := sim.written(); len(writes) != 1 || writes[0] != [3]uint16{1, 10, 20} {
		t.Fatalf("unexpected physical writes: %v", writes)
	}
	firstRevision := machine.Revision
	status, body = call(t, "POST", url+"/execute", f.token, request, &machine)
	if status != 200 || machine.Revision != firstRevision || len(sim.written()) != 1 {
		t.Fatalf("retry wrote or advanced again: %d %s", status, body)
	}
	status, body = call(t, "GET", url, f.token, nil, &machine)
	if status != 200 || machine.CurrentState != "active" || machine.LastExecution == nil || machine.LastExecution.Version != 1 {
		t.Fatalf("process state query: %d %s", status, body)
	}
	// Process completion is a separate observation; a command can confirm while
	// its process condition times out, and that must not rewrite canonical status.
	feedback := createNodeAPI(t, f.base, f.token, "variable", site, "Process feedback", 99)
	definition.Transitions[0].To = "idle"
	definition.Transitions[0].Actions[0].Readback = &decision.Readback{Target: decision.Target{NodeID: feedback}, TimeoutMS: 100}
	status, body = call(t, "POST", url+"/draft", f.token, map[string]any{"revision": machine.Revision, "definition": definition}, &machine)
	if status != 200 {
		t.Fatalf("process draft: %d %s", status, body)
	}
	status, body = call(t, "POST", url+"/publish", f.token, map[string]any{"revision": machine.Revision}, &machine)
	if status != 200 {
		t.Fatalf("process publish: %d %s", status, body)
	}
	status, body = call(t, "POST", url+"/execute", f.token, map[string]any{"request_id": "process-timeout", "event": "start", "revision": machine.Revision, "confirm": true}, &machine)
	if status != 200 || machine.Execution.Status != "unknown" || machine.CurrentState != "active" || machine.Execution.Commands[0].Status != "confirmed" {
		t.Fatalf("process condition rewrote command confirmation or advanced state: %d %s", status, body)
	}
	// The same event name is a different explicit operation only with a new ID.
	sim.mu.Lock()
	sim.writeMode = "missing"
	sim.mu.Unlock()
	request = map[string]any{"request_id": "machine-unknown", "event": "start", "revision": machine.Revision, "confirm": true}
	status, body = call(t, "POST", url+"/execute", f.token, request, &machine)
	if status != 200 || machine.Execution == nil || machine.Execution.Status != "unknown" || machine.CurrentState != "active" {
		t.Fatalf("unknown became completed: %d %s", status, body)
	}
	status, body = call(t, "POST", url+"/execute", f.token, request, &machine)
	if status != 200 || len(sim.written()) != 3 {
		t.Fatalf("unknown replay repeated side effect: %d %s writes=%v", status, body, sim.written())
	}
}
