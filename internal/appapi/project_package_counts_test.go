package appapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"fastiot/internal/ems/profile"
	"fastiot/internal/emsapi"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

// The delivery page states the package contents as "N nodes · M devices · K
// bound · R rules · S state machines". Those counts must come from the same
// package the export produces, not from a second frontend pass over nodes.
func TestProjectPackageSummaryCountsDevicesAndStrategies(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	h.SetEMS(&emsapi.Handler{Profiles: profile.Default()})

	root := snapshotRoot(t, base, token)
	site := createNodeAPI(t, base, token, NodeTypeSite, root, "Counted project", 0)
	boundDevice := createNodeAPI(t, base, token, data.NodeTypeDevice, site, "Bound pump", 0)
	createNodeAPI(t, base, token, data.NodeTypeDevice, site, "Unbound pump", 0)
	bus, err := CreateDomainNode(h.nc, root, data.NodeTypeModbus, map[string]string{"description": "Counted bus"})
	if err != nil {
		t.Fatal(err)
	}
	point, err := CreateDomainNode(h.nc, bus, data.NodeTypeModbusIO, map[string]string{"description": "Counted point"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(unitModelConfig{BusinessNodeID: boundDevice, ProfileID: "enjo-pcs", PointMap: map[string]string{"power": point}})
	if err := client.SendNodePoint(h.nc, bus, data.NewPointString(unitModelPoint, "1", string(raw)), true); err != nil {
		t.Fatal(err)
	}
	waitForNodePoints(t, h.nc, bus, unitModelPoint)
	if _, err := CreateDomainNode(h.nc, site, "decisionTable", map[string]string{"description": "Counted rule"}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateDomainNode(h.nc, site, stateMachineType, map[string]string{"description": "Counted machine"}); err != nil {
		t.Fatal(err)
	}

	var pkg projectPackage
	code, body := call(t, http.MethodGet, base+"/projects/export?site="+site, token, nil, &pkg)
	if code != http.StatusOK {
		t.Fatalf("export: %d %s", code, body)
	}
	if pkg.DeviceCount == nil || *pkg.DeviceCount != 2 || pkg.BoundDeviceCount == nil || *pkg.BoundDeviceCount != 1 {
		t.Fatalf("device summary = %v devices, %v bound; want 2 devices, 1 bound", pkg.DeviceCount, pkg.BoundDeviceCount)
	}
	if pkg.RuleCount == nil || *pkg.RuleCount != 1 || pkg.StateMachineCount == nil || *pkg.StateMachineCount != 1 {
		t.Fatalf("strategy summary = %v rules, %v state machines; want 1 rule, 1 state machine", pkg.RuleCount, pkg.StateMachineCount)
	}
	if pkg.StrategyCount != 2 {
		t.Fatalf("combined strategy count = %d; want rules + state machines = 2", pkg.StrategyCount)
	}
	var wire map[string]any
	if err := json.Unmarshal([]byte(body), &wire); err != nil {
		t.Fatal(err)
	}
	for field, want := range map[string]float64{"devices": 2, "bound_devices": 1, "rules": 1, "state_machines": 1} {
		if wire[field] != want {
			t.Fatalf("exported %s = %v; want %v", field, wire[field], want)
		}
	}
}

// A package without devices must report a real zero, not an absent field.
func TestProjectPackageSummaryCountsEmptyPackage(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	h.SetEMS(&emsapi.Handler{Profiles: profile.Default()})

	root := snapshotRoot(t, base, token)
	site := createNodeAPI(t, base, token, NodeTypeSite, root, "Empty project", 0)

	var pkg projectPackage
	code, body := call(t, http.MethodGet, base+"/projects/export?site="+site, token, nil, &pkg)
	if code != http.StatusOK {
		t.Fatalf("export: %d %s", code, body)
	}
	if pkg.DeviceCount == nil || *pkg.DeviceCount != 0 || pkg.BoundDeviceCount == nil || *pkg.BoundDeviceCount != 0 {
		t.Fatalf("device summary = %v devices, %v bound; want real zeros", pkg.DeviceCount, pkg.BoundDeviceCount)
	}
	if pkg.RuleCount == nil || *pkg.RuleCount != 0 || pkg.StateMachineCount == nil || *pkg.StateMachineCount != 0 {
		t.Fatalf("strategy summary = %v rules, %v state machines; want real zeros", pkg.RuleCount, pkg.StateMachineCount)
	}
	var wire map[string]any
	if err := json.Unmarshal([]byte(body), &wire); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"devices", "bound_devices", "rules", "state_machines"} {
		value, present := wire[field]
		if !present || value != float64(0) {
			t.Fatalf("exported %s = %v (present %v); want an explicit 0", field, value, present)
		}
	}
}
