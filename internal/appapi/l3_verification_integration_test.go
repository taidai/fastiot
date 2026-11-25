package appapi

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"

	"fastiot/internal/contract"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/industry"
	"github.com/simpleiot/simpleiot/data"
	"time"

	"fastiot/internal/ems/bridge"
	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/modbusrt"
	"fastiot/internal/emsapi"
)

const l3Profile = `{"id":"l3-sim","vendor":"Private simulator","model":"Safe verification","protocol":"modbus","point_specs":[{"name":"Target","function":"holdingRegister","address":10,"format":"uint16","scale":1,"read_only":false,"note":"L3_SAFE"}],"command_specs":[{"id":"safe","name":"安全定值验证","point":"Target","value":20,"readback":"Target","readback_value":20,"timeout_ms":1000},{"id":"ordinary","name":"普通操作","parameters":[{"name":"target","point":"Target","kind":"number","default":10}]}]}`

// Only the private TCP station is reachable; all writes are counted on the wire.
func l3Fixture(t *testing.T) (*pointConfigFixture, string, *operationSimulator) {
	t.Helper()
	return l3FixtureWithProfile(t, l3Profile)
}

func l3FixtureWithProfile(t *testing.T, definition string) (*pointConfigFixture, string, *operationSimulator) {
	t.Helper()
	f := newPointConfigFixture(t)
	f.handler.SetEMS(emsapi.New(f.runtime, time.Second))
	loadSingleDeviceProfile(t, f.handler, definition)
	listener, err := net.Listen("tcp", "127.0.0.1:18118")
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
	f.bridge = bridge.New(f.runtime, bridge.NewNodeSource(f.handler.nc), func(protocol, address string, timeout time.Duration) (channel.Bus, error) {
		if protocol != "tcp" || address != "127.0.0.1:18118" {
			return nil, fmt.Errorf("non-fixture address refused")
		}
		return modbusrt.OpenTCP(address, timeout)
	})
	site := createNodeAPI(t, f.base, f.token, "site", snapshotRoot(t, f.base, f.token), "L3 site", 0)
	business := createNodeAPI(t, f.base, f.token, "device", site, "安全验证模拟设备", 0)
	var bus struct {
		ID string `json:"id"`
	}
	status, body := call(t, http.MethodPost, f.base+"/modbus", f.token, ModbusConnectionInput{Parent: site, Description: "Private L3 simulator", Protocol: "TCP", URI: "127.0.0.1:18118", PollPeriod: 50, Timeout: 100}, &bus)
	if status != 200 {
		t.Fatalf("bus: %d %s", status, body)
	}
	input := map[string]any{"profile_id": "l3-sim", "servers": []int{1}, "business_node_id": business, "create_points": true, "dry_run": true}
	var preview struct {
		Version string `json:"preview_version"`
	}
	status, body = call(t, http.MethodPost, f.base+"/modbus/"+bus.ID+"/units/apply", f.token, input, &preview)
	if status != 200 {
		t.Fatalf("preview: %d %s", status, body)
	}
	input["dry_run"] = false
	input["preview_version"] = preview.Version
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
	waitFor(t, func() bool { return f.runtime.State.Health(bus.ID+"/1") == "healthy" }, "L3 first sample")
	WireCommandHistory(f.dir, f.runtime.Commands())
	return f, bus.ID, sim
}

type l3Result struct {
	Passed bool `json:"passed"`
	Record struct {
		At, Actor, Basis string
		RequestID        string `json:"request_id"`
		ProfileVersion   string `json:"profile_version"`
		MappingVersion   string `json:"mapping_version"`
		Current          bool
	} `json:"record"`
	Command contract.CommandOutcome `json:"command"`
}

func l3Input(t *testing.T, f *pointConfigFixture, bus, id string) map[string]any {
	t.Helper()
	var result l3Result
	status, body := call(t, http.MethodPost, f.base+"/modbus/"+bus+"/units/1/verify", f.token, map[string]any{"level": "L2"}, &result)
	if status != 200 || !result.Passed || result.Record.Basis == "" {
		t.Fatalf("L2 basis: %d %s", status, body)
	}
	return map[string]any{"level": "L3", "request_id": id, "basis": result.Record.Basis, "command_id": "safe", "confirm": true}
}

func TestL3StableIdentityAndCanonicalEvidence(t *testing.T) {
	f, bus, sim := l3Fixture(t)
	input := l3Input(t, f, bus, "one-safe-validation")
	url := f.base + "/modbus/" + bus + "/units/1"
	for i := 0; i < 2; i++ {
		var result l3Result
		status, body := call(t, http.MethodPost, url+"/verify", f.token, input, &result)
		if status != 200 || !result.Passed || result.Command.RequestID != "one-safe-validation" || result.Command.State != "confirmed" {
			t.Fatalf("canonical validation: %d %s", status, body)
		}
		if result.Record.RequestID != result.Command.RequestID || result.Record.Actor == "" || result.Record.At == "" || result.Record.ProfileVersion == "" || result.Record.MappingVersion == "" || result.Record.Basis != input["basis"] {
			t.Fatalf("missing evidence: %s", body)
		}
	}
	if writes := sim.written(); len(writes) != 1 || writes[0] != [3]uint16{1, 10, 20} {
		t.Fatalf("wrong writes: %v", writes)
	}
	var history struct {
		Records []struct {
			RequestID string `json:"request_id"`
		}
	}
	status, body := call(t, http.MethodGet, url+"/verify", f.token, nil, &history)
	if status != 200 || len(history.Records) != 2 || history.Records[1].RequestID != "one-safe-validation" {
		t.Fatalf("persisted evidence: %d %s", status, body)
	}
	var canonical pumpCommandOutcome
	status, body = call(t, http.MethodGet, url+"/commands/one-safe-validation", f.token, nil, &canonical)
	if status != 200 || canonical.State != "confirmed" {
		t.Fatalf("canonical query: %d %s", status, body)
	}
	var model unitModelView
	call(t, http.MethodGet, url+"/model", f.token, nil, &model)
	model.Model.Name = "Updated evidence revision"
	status, body = call(t, http.MethodPut, url+"/model", f.token, model.Model, nil)
	if status != 200 {
		t.Fatal(status, body)
	}
	var updated struct{ Basis string }
	call(t, http.MethodGet, url+"/verify", f.token, nil, &updated)
	input["basis"] = updated.Basis
	status, body = call(t, http.MethodPost, url+"/verify", f.token, input, nil)
	if status != 409 || !strings.Contains(body, "l3_request_basis_conflict") {
		t.Fatalf("same ID accepted a new basis: %d %s", status, body)
	}
	if len(sim.written()) != 1 {
		t.Fatal("basis conflict reissued a command")
	}
}

func TestL3GuardsNeverWrite(t *testing.T) {
	f, bus, sim := l3Fixture(t)
	base := l3Input(t, f, bus, "guard")
	root := snapshotRoot(t, f.base, f.token)
	configureOnly := inputAccount(t, f, root, "implementer", []string{ActionConfigure})
	commandOnly := inputAccount(t, f, root, "operator", []string{ActionCommand})
	busOnly := inputAccount(t, f, bus, "implementer", []string{ActionConfigure, ActionCommand})
	viewer := inputAccount(t, f, root, "viewer", nil)
	for _, tc := range []struct {
		name, token, key string
		value            any
		want             int
	}{
		{"missing identity", f.token, "request_id", "", 400},
		{"missing basis", f.token, "basis", "", 400},
		{"old basis", f.token, "basis", "old", 409},
		{"missing confirmation", f.token, "confirm", false, 400},
		{"undeclared", f.token, "command_id", "ordinary", 400},
		{"client value", f.token, "value", 99, 400},
		{"client point", f.token, "point", "other", 400},
		{"client exact flag", f.token, "exact_writes", false, 400},
		{"missing action", configureOnly, "command_id", "safe", 403},
		{"missing configure", commandOnly, "command_id", "safe", 403},
		{"business scope", busOnly, "command_id", "safe", 403},
		{"viewer", viewer, "command_id", "safe", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := map[string]any{}
			for k, v := range base {
				input[k] = v
			}
			input["request_id"] = tc.name
			input[tc.key] = tc.value
			status, body := call(t, http.MethodPost, f.base+"/modbus/"+bus+"/units/1/verify", tc.token, input, nil)
			if status != tc.want {
				t.Errorf("guard: %d %s want %d", status, body, tc.want)
			}
			if writes := sim.written(); len(writes) != 0 {
				t.Errorf("denied request wrote: %v", writes)
			}
		})
	}
}

func TestL3UnknownNeverReissues(t *testing.T) {
	for _, mode := range []string{"wrong", "missing", "stale", "disconnect"} {
		t.Run(mode, func(t *testing.T) {
			f, bus, sim := l3Fixture(t)
			input := l3Input(t, f, bus, "uncertain")
			url := f.base + "/modbus/" + bus + "/units/1"
			if mode == "stale" {
				sim.mu.Lock()
				sim.values[1] = 20
				sim.mu.Unlock()
				var model unitModelView
				call(t, http.MethodGet, url+"/model", f.token, nil, &model)
				waitFor(t, func() bool {
					v, ok := pumpNumber(t, f.base, f.token, model.Model.PointMap["Target"])
					return ok && v == 20
				}, "old matching observation")
			}
			sim.mu.Lock()
			sim.writeMode = mode
			if mode == "stale" {
				sim.writeMode = "missing"
			}
			sim.mu.Unlock()
			for i := 0; i < 2; i++ {
				var result l3Result
				status, body := call(t, http.MethodPost, url+"/verify", f.token, input, &result)
				if status != 200 || result.Passed || result.Command.State != "unknown" || result.Command.RequestID != "uncertain" {
					t.Fatalf("unknown/retry: %d %s", status, body)
				}
			}
			// Restore the canonical owner over the same journal; it must never replay.
			restored := runtime.NewService(nil, time.Minute, time.Second, 100)
			t.Cleanup(restored.Close)
			api := emsapi.New(restored, time.Minute)
			api.Profiles = f.handler.ems.Profiles
			WireCommandHistory(f.dir, restored.Commands())
			f.handler.SetEMS(api)
			var outcome pumpCommandOutcome
			status, body := call(t, http.MethodGet, url+"/commands/uncertain", f.token, nil, &outcome)
			if status != 200 || outcome.State != "unknown" {
				t.Fatalf("restored canonical: %d %s", status, body)
			}
			status, body = call(t, http.MethodPost, url+"/verify", f.token, input, nil)
			if status != 200 {
				t.Fatalf("restored retry: %d %s", status, body)
			}
			if writes := sim.written(); len(writes) != 1 || writes[0] != [3]uint16{1, 10, 20} {
				t.Fatalf("unknown reissued/fell back: %v", writes)
			}
		})
	}
}

func TestL3EvidenceInvalidatesOnEveryConfigurationBasis(t *testing.T) {
	for _, change := range []string{"profile", "mapping", "physical"} {
		t.Run(change, func(t *testing.T) {
			f, bus, sim := l3Fixture(t)
			input := l3Input(t, f, bus, "basis-validation")
			url := f.base + "/modbus/" + bus + "/units/1"
			status, body := call(t, http.MethodPost, url+"/verify", f.token, input, nil)
			if status != 200 {
				t.Fatal(status, body)
			}
			var view unitModelView
			call(t, http.MethodGet, url+"/model", f.token, nil, &view)
			switch change {
			case "profile":
				loadSingleDeviceProfile(t, f.handler, strings.Replace(l3Profile, "Safe verification", "Replacement verification", 1))
			case "mapping":
				delete(view.Model.PointMap, "Target")
				status, body = call(t, http.MethodPut, url+"/model", f.token, view.Model, nil)
				if status != 200 {
					t.Fatal(status, body)
				}
			case "physical":
				status, body = call(t, http.MethodPut, f.base+"/modbus/io/"+view.Model.PointMap["Target"], f.token, ModbusIOInput{Parent: bus, Description: "Target", ServerID: 1, Address: 11, IOType: "modbusHoldingRegister", DataFormat: "uint16", Scale: 1}, nil)
				if status != 200 {
					t.Fatal(status, body)
				}
			}
			var history struct {
				Basis   string
				Records []struct{ Current bool }
			}
			status, body = call(t, http.MethodGet, url+"/verify", f.token, nil, &history)
			if status != 200 || history.Basis == input["basis"] {
				t.Fatalf("basis not changed: %d %s", status, body)
			}
			for _, record := range history.Records {
				if record.Current {
					t.Fatal("old evidence still current")
				}
			}
			input["request_id"] = "new-on-old-basis"
			status, body = call(t, http.MethodPost, url+"/verify", f.token, input, nil)
			if status != 409 {
				t.Fatalf("old basis accepted: %d %s", status, body)
			}
			if writes := sim.written(); len(writes) != 1 {
				t.Fatalf("old basis wrote: %v", writes)
			}
		})
	}
}

func TestL3EvidenceIsNotReusableProjectConfiguration(t *testing.T) {
	f, bus, _ := l3Fixture(t)
	input := l3Input(t, f, bus, "evidence-export")
	status, body := call(t, http.MethodPost, f.base+"/modbus/"+bus+"/units/1/verify", f.token, input, nil)
	if status != 200 {
		t.Fatal(status, body)
	}
	site := ""
	for _, node := range snapshotNodes(t, f.base, f.token) {
		if node.ID == bus {
			site = node.Parent
		}
	}
	var exported struct {
		Nodes []struct {
			Points data.Points `json:"points"`
		} `json:"nodes"`
	}
	status, body = call(t, http.MethodGet, f.base+"/projects/export?site="+site, f.token, nil, &exported)
	if status != 200 {
		t.Fatal(status, body)
	}
	for _, node := range exported.Nodes {
		for _, point := range node.Points {
			if point.Type == "easyemsAccessVerification" {
				t.Fatal("verification/command evidence leaked into reusable project")
			}
		}
	}
}

func TestL3OnlyExactDeclaredValuesReachSimulator(t *testing.T) {
	for _, constraint := range []string{"missing declaration", "enum", "safety clamp"} {
		t.Run(constraint, func(t *testing.T) {
			definition := l3Profile
			if constraint == "missing declaration" {
				definition = strings.Replace(definition, "L3_SAFE", "", 1)
			}
			if constraint == "enum" {
				definition = strings.Replace(definition, `"note":"L3_SAFE"`, `"note":"L3_SAFE","enum":{"10":"initial"}`, 1)
			}
			f, bus, sim := l3FixtureWithProfile(t, definition)
			input := l3Input(t, f, bus, "constrained")
			if constraint == "safety clamp" {
				var model unitModelView
				call(t, http.MethodGet, f.base+"/modbus/"+bus+"/units/1/model", f.token, nil, &model)
				status, body := call(t, http.MethodPut, f.base+"/safety", f.token, map[string]any{"node": model.Model.PointMap["Target"], "safety": map[string]any{"min": 0, "max": 15}}, nil)
				if status != 200 {
					t.Fatal(status, body)
				}
			}
			var result l3Result
			status, body := call(t, http.MethodPost, f.base+"/modbus/"+bus+"/units/1/verify", f.token, input, &result)
			if constraint == "safety clamp" {
				if status != 200 || result.Command.State != "rejected" {
					t.Errorf("clamped fixed L3: %d %s", status, body)
				}
			} else if status != 400 {
				t.Errorf("invalid declaration: %d %s", status, body)
			}
			if writes := sim.written(); len(writes) != 0 {
				t.Fatalf("undeclared value reached device: %v", writes)
			}
		})
	}
}

func TestL3RejectsRemappedPhysicalPointEvenAfterManualSignoff(t *testing.T) {
	f, bus, sim := l3Fixture(t)
	input := l3Input(t, f, bus, "physical-mismatch")
	url := f.base + "/modbus/" + bus + "/units/1"
	var model unitModelView
	call(t, http.MethodGet, url+"/model", f.token, nil, &model)
	status, body := call(t, http.MethodPut, f.base+"/modbus/io/"+model.Model.PointMap["Target"], f.token, ModbusIOInput{Parent: bus, Description: "Target", ServerID: 1, Address: 11, IOType: "modbusHoldingRegister", DataFormat: "uint16", Scale: 1}, nil)
	if status != 200 {
		t.Fatal(status, body)
	}
	call(t, http.MethodGet, url+"/model", f.token, nil, &model)
	model.Model.Verified = true
	model.Model.Verifier = "fixture"
	model.Model.VerifiedAt = "2026-11-25"
	model.Model.DeviceSerial = "private-simulator"
	status, body = call(t, http.MethodPut, url+"/model", f.token, model.Model, nil)
	if status != 200 {
		t.Fatal(status, body)
	}
	var history struct{ Basis string }
	call(t, http.MethodGet, url+"/verify", f.token, nil, &history)
	input["basis"] = history.Basis
	status, body = call(t, http.MethodPost, url+"/verify", f.token, input, nil)
	if status != 409 {
		t.Errorf("different physical mapping accepted: %d %s", status, body)
	}
	if writes := sim.written(); len(writes) != 0 {
		t.Fatalf("mismapped point wrote: %v", writes)
	}
}

func TestL3DeclaredToleranceUsesFreshCanonicalReadback(t *testing.T) {
	for _, offset := range []uint16{1, 3} {
		t.Run(fmt.Sprint(offset), func(t *testing.T) {
			definition := strings.Replace(l3Profile, `"scale":1`, `"scale":0.1`, 1)
			definition = strings.Replace(definition, `"timeout_ms":1000`, `"timeout_ms":1000,"readback_tolerance":0.25`, 1)
			f, bus, sim := l3FixtureWithProfile(t, definition)
			input := l3Input(t, f, bus, "tolerance")
			sim.mu.Lock()
			sim.readOffset = offset
			sim.mu.Unlock()
			var result l3Result
			status, body := call(t, http.MethodPost, f.base+"/modbus/"+bus+"/units/1/verify", f.token, input, &result)
			want := "confirmed"
			if offset == 3 {
				want = "unknown"
			}
			if status != 200 || result.Command.State != want || result.Passed != (offset == 1) {
				t.Fatalf("tolerance: %d %s", status, body)
			}
			if offset == 1 {
				if len(result.Command.Observed) != 1 {
					t.Fatal("missing canonical observation")
				}
				sample := result.Command.Observed[0]
				if sample.Value != "20.1" || !sample.At.After(result.Command.StartedAt) || sample.At.After(result.Command.Deadline) {
					t.Fatalf("not fresh post-write evidence: %s", body)
				}
			}
			if writes := sim.written(); len(writes) != 1 || writes[0] != [3]uint16{1, 10, 200} {
				t.Fatalf("wrong scaled declared write: %v", writes)
			}
		})
	}
}

func TestL3IndustryDependencyPreservesReadOnlyValidation(t *testing.T) {
	f, bus, sim := l3Fixture(t)
	input := l3Input(t, f, bus, "missing-industry")
	registry, err := industry.Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	f.handler.SetPackages(registry)
	if _, err = registry.Uninstall(industry.EMSPackageID); err != nil {
		t.Fatal(err)
	}
	if _, err = CreateDomainNode(f.handler.nc, snapshotRoot(t, f.base, f.token), NodeTypeEnergyUnit, map[string]string{"description": "retained dependency"}); err != nil {
		t.Fatal(err)
	}
	url := f.base + "/modbus/" + bus + "/units/1/verify"
	for _, level := range []string{"L1", "L2"} {
		var result l3Result
		status, body := call(t, http.MethodPost, url, f.token, map[string]string{"level": level}, &result)
		if status != 200 || !result.Passed {
			t.Fatalf("read-only verification: %d %s", status, body)
		}
	}
	status, body := call(t, http.MethodPost, url, f.token, input, nil)
	if status != 409 || !strings.Contains(body, "project_industry_dependency_unavailable") {
		t.Fatalf("missing dependency wrote: %d %s", status, body)
	}
	if writes := sim.written(); len(writes) != 0 {
		t.Fatalf("dependency/read-only writes: %v", writes)
	}
}

func TestL3RequiresBoundBusinessDevice(t *testing.T) {
	f, bus, sim := l3Fixture(t)
	point := f.createPoint(t, bus, ModbusIOInput{Description: "Target", ServerID: 2, Address: 10, IOType: "modbusHoldingRegister", DataFormat: "uint16", Scale: 1})
	url := f.base + "/modbus/" + bus + "/units/2"
	status, body := call(t, http.MethodPut, url+"/model", f.token, unitModelConfig{Name: "Unbound station", ProfileID: "l3-sim", PointMap: map[string]string{"Target": point}}, nil)
	if status != 200 {
		t.Fatal(status, body)
	}
	if err := f.bridge.Sync(time.Now()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return f.runtime.State.Health(bus+"/2") == "healthy" }, "station 2 observation")
	var history struct{ Basis string }
	call(t, http.MethodGet, url+"/verify", f.token, nil, &history)
	status, body = call(t, http.MethodPost, url+"/verify", f.token, map[string]any{"level": "L3", "request_id": "unbound", "basis": history.Basis, "command_id": "safe", "confirm": true}, nil)
	if status != 409 {
		t.Errorf("unbound station accepted: %d %s", status, body)
	}
	if writes := sim.written(); len(writes) != 0 {
		t.Fatalf("unbound station writes: %v", writes)
	}
}

func TestL3RequiresCurrentBasisBeforeAnyWrite(t *testing.T) {
	f, bus, sim := l3Fixture(t)
	status, body := call(t, http.MethodPost, f.base+"/modbus/"+bus+"/units/1/verify", f.token, map[string]any{"level": "L3", "command_id": "safe", "confirm": true}, nil)
	if status != http.StatusBadRequest {
		t.Errorf("missing durable identity/basis: %d %s", status, body)
	}
	if writes := sim.written(); len(writes) != 0 {
		t.Fatalf("missing basis wrote device: %v", writes)
	}
}
