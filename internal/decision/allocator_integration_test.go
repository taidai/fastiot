package decision

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fastiot/internal/ems/alloc"
	"fastiot/internal/ems/safety"
	"github.com/nats-io/nats.go"
	siotclient "github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
	"github.com/simpleiot/simpleiot/server"
)

func TestAllocatorRunsSequentialReadback(t *testing.T) {
	opts := server.TestServerOptions
	opts.ID = "easyems-allocation-test"
	opts.DataDir = filepath.Join(t.TempDir(), "data")
	opts.NatsPort = 19700
	opts.NatsServer = "nats://127.0.0.1:19700"
	opts.HTTPPort = "9701"
	opts.NatsMonitorPort = 19702
	opts.NatsWSPort = 19703
	opts.NatsMQTTPort = 0
	nc, root, stopServer, err := server.TestServerOpts(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer stopServer()
	_, stopManager := runManager(t, nc)
	defer stopManager()

	createVariable(t, nc, root.ID, "grid", 50)
	group := data.NodeEdge{ID: "ess", Type: data.NodeTypeGroup, Parent: root.ID}
	if err := siotclient.SendNode(nc, group, "test"); err != nil {
		t.Fatal(err)
	}
	createVariable(t, nc, group.ID, "pcs1", 0)
	createVariable(t, nc, group.ID, "pcs2", 0)
	createVariable(t, nc, group.ID, "pcs3", 0)
	createVariable(t, nc, group.ID, "pcs4", 0)
	createVariable(t, nc, group.ID, "cap1", 200)
	createVariable(t, nc, group.ID, "cap2", 200)
	createVariable(t, nc, group.ID, "cap3", 150)
	createVariable(t, nc, group.ID, "cap4", 200)
	publish(t, nc, "pcs4", "health", "offline")

	config := AllocatorConfig{
		TimeoutMS: 500,
		Members: []AllocatorMember{
			allocatorTestMember("CAB01", "pcs1", "cap1", 200),
			allocatorTestMember("CAB02", "pcs2", "cap2", 200),
			allocatorTestMember("CAB03", "pcs3", "cap3", 150),
			allocatorTestMember("CAB04", "pcs4", "cap4", 200),
		},
	}
	publish(t, nc, "ess", allocatorPointType, marshal(t, config))

	document := Document{
		Table: Table{Rows: []Row{{
			ID:         "discharge",
			Conditions: []Condition{{PointID: "grid", Operator: "gt", Value: 0}},
			Actions:    []Action{{ActionID: "ess-power", Value: 800}},
		}}},
		Bindings: BindingSet{
			Points:  map[string]Target{"grid": {NodeID: "grid"}},
			Actions: map[string]Target{"ess-power": {NodeID: "ess"}},
		},
	}
	if err := siotclient.SendNodeType(nc, DecisionTable{
		ID: "ess-rule", Parent: root.ID, Description: "储能分配",
		Applied: marshal(t, document), Enabled: true,
	}, "test"); err != nil {
		t.Fatal(err)
	}

	waitForValue(t, nc, "pcs1", 200)
	if got := pointValue(t, nc, "pcs2", data.PointTypeValueSet); got != -1 {
		t.Fatalf("pcs2 was written before pcs1 readback: %v", got)
	}
	sendFloat(t, nc, "pcs1", data.PointTypeValue, 200)
	waitForValue(t, nc, "pcs2", 200)
	if got := pointValue(t, nc, "pcs3", data.PointTypeValueSet); got != -1 {
		t.Fatalf("pcs3 was written before pcs2 readback: %v", got)
	}
	sendFloat(t, nc, "pcs2", data.PointTypeValue, 200)
	waitForValue(t, nc, "pcs3", 150)
	sendFloat(t, nc, "pcs3", data.PointTypeValue, 150)
	if got := pointValue(t, nc, "pcs4", data.PointTypeValueSet); got != -1 {
		t.Fatalf("offline member was written: %v", got)
	}

	report := waitCheck(t, nc, "ess-rule", func(report CheckReport) bool {
		return len(report.Actions) == 1 && report.Actions[0].Status == "confirmed"
	})
	action := report.Actions[0]
	if len(action.Shares) != 4 {
		t.Fatalf("unexpected shares: %+v", action.Shares)
	}
	for i, want := range []float64{200, 200, 150} {
		if action.Shares[i].Setpoint != want || !action.Shares[i].Confirmed || action.Shares[i].Actual != want {
			t.Errorf("share %d not confirmed: %+v", i, action.Shares[i])
		}
	}
	if !action.Shares[3].Excluded || action.Shares[3].Reason != "offline" {
		t.Errorf("offline member not excluded: %+v", action.Shares[3])
	}

	// A new version requests 400 kW. pcs1 confirms, pcs2 never readbacks;
	// pcs3 must not receive its new target and the result becomes unknown.
	document.Table.Rows[0].Actions[0].Value = 400
	wantShare := 400.0 * 200.0 / 550.0
	publish(t, nc, "ess-rule", "applied", marshal(t, document))
	waitForValue(t, nc, "pcs1", wantShare)
	sendFloat(t, nc, "pcs1", data.PointTypeValue, wantShare)
	waitForValue(t, nc, "pcs2", wantShare)
	waitCheck(t, nc, "ess-rule", func(report CheckReport) bool {
		return len(report.Actions) == 1 && report.Actions[0].Status == "unknown"
	})
	if got := pointValue(t, nc, "pcs3", data.PointTypeValueSet); got != 150 {
		t.Fatalf("pcs3 advanced after pcs2 timeout: %v", got)
	}
}

func allocatorTestMember(id, target, capability string, max float64) AllocatorMember {
	return AllocatorMember{
		ID: id, Target: Target{NodeID: target},
		MaxDischarge: max, MaxCharge: max,
		AvailableDischarge: &Target{NodeID: capability},
		AvailableCharge:    &Target{NodeID: capability},
	}
}

func TestBusHealthyUsesSharedVerdict(t *testing.T) {
	opts := server.TestServerOptions
	opts.ID = "easyems-bus-health-test"
	opts.DataDir = filepath.Join(t.TempDir(), "data")
	opts.NatsPort = 19710
	opts.NatsServer = "nats://127.0.0.1:19710"
	opts.HTTPPort = "9702"
	opts.NatsMonitorPort = 19712
	opts.NatsWSPort = 19713
	opts.NatsMQTTPort = 0
	nc, _, stopServer, err := server.TestServerOpts(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer stopServer()
	if err := siotclient.SendNode(nc, data.NodeEdge{ID: "bus", Type: data.NodeTypeModbus, Parent: "root"}, "test"); err != nil {
		t.Fatal(err)
	}
	if busHealthy(nc, "bus") {
		t.Fatal("no observation must not be healthy")
	}
	now := time.Now()
	for _, point := range []data.Point{
		data.NewPointFloat(data.PointTypePollPeriod, "", 1000),
		data.NewPointFloat(data.PointTypeTimeout, "", 1000),
		data.NewPointFloat("lastGoodPoll", "", float64(now.Unix())),
	} {
		if err := siotclient.SendNodePoint(nc, "bus", point, true); err != nil {
			t.Fatal(err)
		}
	}
	if !busHealthy(nc, "bus") {
		t.Fatal("fresh observation must be eligible")
	}
	stalePoints := data.Points{
		data.NewPointFloat(data.PointTypePollPeriod, "", 1000),
		data.NewPointFloat(data.PointTypeTimeout, "", 1000),
		data.NewPointFloat("lastGoodPoll", "", float64(now.Add(-time.Minute).Unix())),
	}
	for i := range stalePoints {
		stalePoints[i].Time = now.Add(-time.Minute)
	}
	if err := siotclient.SendNode(nc, data.NodeEdge{ID: "stale", Type: data.NodeTypeModbus, Parent: "root", Points: stalePoints}, "test"); err != nil {
		t.Fatal(err)
	}
	if busHealthy(nc, "stale") {
		t.Fatal("expired observation must be offline")
	}
}

func TestAllocationMemberRejectsUnknownHealth(t *testing.T) {
	opts := server.TestServerOptions
	opts.ID = "easyems-allocation-health-test"
	opts.DataDir = filepath.Join(t.TempDir(), "data")
	opts.NatsPort = 19711
	opts.NatsServer = "nats://127.0.0.1:19711"
	opts.HTTPPort = "9703"
	opts.NatsMonitorPort = 19714
	opts.NatsWSPort = 19715
	opts.NatsMQTTPort = 0
	nc, _, stopServer, err := server.TestServerOpts(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer stopServer()
	bus := data.NodeEdge{ID: "bus-health", Type: data.NodeTypeModbus, Parent: "root"}
	if err := siotclient.SendNode(nc, bus, "test"); err != nil {
		t.Fatal(err)
	}
	point := data.NodeEdge{ID: "io-health", Type: data.NodeTypeModbusIO, Parent: bus.ID, Points: data.Points{data.NewPointString("health", "", "unknown")}}
	if err := siotclient.SendNode(nc, point, "test"); err != nil {
		t.Fatal(err)
	}
	member, err := allocationMember(nc, AllocatorMember{Target: Target{NodeID: point.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if member.Online {
		t.Fatal("unknown health must not be allocated")
	}
	if err := siotclient.SendNodePoint(nc, point.ID, data.NewPointString("health", "", "degraded"), true); err != nil {
		t.Fatal(err)
	}
	member, err = allocationMember(nc, AllocatorMember{Target: Target{NodeID: point.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if !member.Online {
		t.Fatal("degraded health must remain eligible")
	}
}

// A device without a health point is unknown, not online, and its capacity
// must not be offered. Variables have no health contract at all and keep the
// documented eligible default.
func TestAllocationMemberHealthStates(t *testing.T) {
	opts := server.TestServerOptions
	opts.ID = "easyems-allocation-health-states-test"
	opts.DataDir = filepath.Join(t.TempDir(), "data")
	opts.NatsPort = 19730
	opts.NatsServer = "nats://127.0.0.1:19730"
	opts.HTTPPort = "9705"
	opts.NatsMonitorPort = 19732
	opts.NatsWSPort = 19733
	opts.NatsMQTTPort = 0
	nc, root, stopServer, err := server.TestServerOpts(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer stopServer()
	bus := data.NodeEdge{ID: "bus-states", Type: data.NodeTypeModbus, Parent: root.ID}
	if err := siotclient.SendNode(nc, bus, "test"); err != nil {
		t.Fatal(err)
	}
	for id, state := range map[string]string{"io-online": "healthy", "io-offline": "offline", "io-unknown": "unknown"} {
		points := data.Points{data.NewPointString("health", "", state)}
		if err := siotclient.SendNode(nc, data.NodeEdge{ID: id, Type: data.NodeTypeModbusIO, Parent: bus.ID, Points: points}, "test"); err != nil {
			t.Fatal(err)
		}
	}
	if err := siotclient.SendNode(nc, data.NodeEdge{ID: "io-missing", Type: data.NodeTypeModbusIO, Parent: bus.ID}, "test"); err != nil {
		t.Fatal(err)
	}
	createVariable(t, nc, root.ID, "virtual-member", 0)

	members := []AllocatorMember{
		{ID: "online", Target: Target{NodeID: "io-online"}, MaxCharge: 100, MaxDischarge: 100},
		{ID: "offline", Target: Target{NodeID: "io-offline"}, MaxCharge: 100, MaxDischarge: 100},
		{ID: "unknown", Target: Target{NodeID: "io-unknown"}, MaxCharge: 100, MaxDischarge: 100},
		{ID: "missing", Target: Target{NodeID: "io-missing"}, MaxCharge: 100, MaxDischarge: 100},
		{ID: "virtual", Target: Target{NodeID: "virtual-member"}, MaxCharge: 100, MaxDischarge: 100},
	}
	resolved := make([]alloc.Member, 0, len(members))
	for _, member := range members {
		value, err := allocationMember(nc, member)
		if err != nil {
			t.Fatal(err)
		}
		resolved = append(resolved, value)
	}
	for i, want := range []bool{true, false, false, false, true} {
		if resolved[i].Online != want {
			t.Errorf("%s online = %v, want %v", members[i].ID, resolved[i].Online, want)
		}
	}
	result, err := alloc.Allocate(500, resolved)
	if err != nil {
		t.Fatal(err)
	}
	if result.Delivered != 200 || result.Shortfall != 300 {
		t.Fatalf("unknown capacity was counted: delivered %v shortfall %v", result.Delivered, result.Shortfall)
	}
	for i, want := range []string{"", "offline", "health unknown", "health unknown", ""} {
		if result.Shares[i].Excluded != (want != "") || result.Shares[i].Reason != want {
			t.Errorf("%s share = %+v, want reason %q", members[i].ID, result.Shares[i], want)
		}
	}
}

func TestAllocatorRefusesMissingSafetyInput(t *testing.T) {
	opts := server.TestServerOptions
	opts.ID = "easyems-allocation-safety-test"
	opts.DataDir = filepath.Join(t.TempDir(), "data")
	opts.NatsPort = 19720
	opts.NatsServer = "nats://127.0.0.1:19720"
	opts.HTTPPort = "9704"
	opts.NatsMonitorPort = 19721
	opts.NatsWSPort = 19722
	opts.NatsMQTTPort = 0
	nc, root, stopServer, err := server.TestServerOpts(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer stopServer()
	config := safety.Config{Conditions: []safety.ConditionConfig{{PointID: "interlock", Operator: "eq", Value: 1, Kind: "bool"}}}
	encoded, _ := json.Marshal(config)
	points := data.Points{
		data.NewPointFloat(data.PointTypeValue, "", 0),
		data.NewPointString("easyemsSafety", "", string(encoded)),
	}
	if err := siotclient.SendNode(nc, data.NodeEdge{ID: "pcs-safety", Type: data.NodeTypeVariable, Parent: root.ID, Points: points}, "test"); err != nil {
		t.Fatal(err)
	}
	if err := siotclient.SendNode(nc, data.NodeEdge{ID: "interlock", Type: data.NodeTypeVariable, Parent: root.ID}, "test"); err != nil {
		t.Fatal(err)
	}
	run := newAllocationRun(root.ID, AllocatorConfig{Members: []AllocatorMember{{ID: "pcs", Target: Target{NodeID: "pcs-safety"}, MaxDischarge: 100}}})
	now := time.Now()
	if err := run.start(nc, 80, now); err != nil {
		t.Fatal(err)
	}
	run.step(nc, now)
	if run.status != "failed" || !strings.Contains(run.reason, "safety input interlock is missing") {
		t.Fatalf("allocator safety = %s %s", run.status, run.reason)
	}
	if got := pointValue(t, nc, "pcs-safety", data.PointTypeValueSet); got != -1 {
		t.Fatalf("unsafe allocator write = %v", got)
	}
}

func createVariable(t *testing.T, nc *nats.Conn, parent, id string, value float64) {
	t.Helper()
	if err := siotclient.SendNode(nc, data.NodeEdge{
		ID: id, Type: data.NodeTypeVariable, Parent: parent,
		Points: data.Points{data.NewPointFloat(data.PointTypeValue, "", value)},
	}, "test"); err != nil {
		t.Fatal(err)
	}
}

func waitCheck(t *testing.T, nc *nats.Conn, ruleID string, predicate func(CheckReport) bool) CheckReport {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	last := ""
	for time.Now().Before(deadline) {
		nodes, err := siotclient.GetNodes(nc, "all", ruleID, "", false)
		if err == nil && len(nodes) == 1 {
			point, ok := nodes[0].Points.Find("lastCheck", "")
			if ok {
				last = point.Txt()
				var report CheckReport
				if json.Unmarshal([]byte(last), &report) == nil && predicate(report) {
					return report
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("lastCheck did not reach expected state: %s", last)
	return CheckReport{}
}

func TestAllocatorRejectsSharedOwnership(t *testing.T) {
	opts := server.TestServerOptions
	opts.DataDir = filepath.Join(t.TempDir(), "data")
	opts.NatsPort = 19700
	opts.NatsServer = "nats://127.0.0.1:19700"
	opts.HTTPPort = "9701"
	opts.NatsMonitorPort = 19702
	opts.NatsWSPort = 19703
	opts.NatsMQTTPort = 0
	nc, root, stopServer, err := server.TestServerOpts(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer stopServer()
	createVariable(t, nc, root.ID, "pcs1", 0)
	if err := siotclient.SendNode(nc, data.NodeEdge{ID: "ess", Type: data.NodeTypeGroup, Parent: root.ID}, "test"); err != nil {
		t.Fatal(err)
	}
	publish(t, nc, "ess", allocatorPointType, marshal(t, AllocatorConfig{
		Members: []AllocatorMember{{ID: "CAB01", Target: Target{NodeID: "pcs1"}, MaxDischarge: 100, MaxCharge: 100}},
	}))
	document := Document{
		Table: Table{Rows: []Row{{
			ID:      "r",
			Actions: []Action{{ActionID: "a", Value: 10}},
		}}},
		Bindings: BindingSet{Actions: map[string]Target{"a": {NodeID: "ess"}}},
	}
	if err := siotclient.SendNodeType(nc, DecisionTable{ID: "one", Parent: root.ID, Applied: marshal(t, document), Enabled: true}, "test"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAllocationOwnership(nc, "two", "ess"); err == nil || !strings.Contains(err.Error(), "already owned") {
		t.Fatalf("expected ownership conflict, got %v", err)
	}
	if err := ValidateAllocationOwnership(nc, "one", "ess"); err != nil {
		t.Fatalf("owner rejected itself: %v", err)
	}
}
