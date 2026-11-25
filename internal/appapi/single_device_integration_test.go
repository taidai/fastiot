package appapi

import (
	"encoding/json"
	"fastiot/internal/ems/bridge"
	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/modbusrt"
	"fastiot/internal/ems/profile"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/sim/pumphouse"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fastiot/internal/emsapi"
)

// Exercise saved model evidence through authenticated HTTP, not store internals.
func TestSingleDeviceMappingChangeInvalidatesVerification(t *testing.T) {
	f := newPointConfigFixture(t)
	f.handler.SetEMS(emsapi.New(f.runtime, time.Minute))
	bus := f.createBus(t, "isolated access")
	p1 := f.createPoint(t, bus, ModbusIOInput{Description: "有功功率", ServerID: 1, Address: 1, IOType: "modbusHoldingRegister", DataFormat: "uint16", ReadOnly: true, Scale: 1})
	p2 := f.createPoint(t, bus, ModbusIOInput{Description: "replacement", ServerID: 1, Address: 2, IOType: "modbusHoldingRegister", DataFormat: "uint16", ReadOnly: true, Scale: 1})
	url := f.base + "/modbus/" + bus + "/units/1/model"
	input := unitModelConfig{Name: "meter", ProfileID: "modbus-generic", PointMap: map[string]string{"有功功率": p1}}
	var view unitModelView
	save := func() {
		t.Helper()
		status, body := call(t, http.MethodPut, url, f.token, input, &view)
		if status != 200 {
			t.Fatalf("save: %d %s", status, body)
		}
	}
	save()
	input = *view.Model
	input.Verified, input.Verifier, input.VerifiedAt, input.DeviceSerial = true, "fixture", "2026-11-25", "simulator"
	save()
	input = *view.Model
	input.PointMap["有功功率"] = p2
	save()
	status, body := call(t, http.MethodGet, url, f.token, nil, &view)
	if status != 200 || view.Model.Verified || !view.Model.VerificationRequired {
		t.Fatalf("mapping edit retained old verification: %d %s", status, body)
	}
}

type accessReadOnlyBus struct {
	*modbusrt.Bus
	writes *atomic.Int32
}

func (b *accessReadOnlyBus) WriteCoil(byte, uint16, bool) error {
	b.writes.Add(1)
	return fmt.Errorf("fixture forbids writes")
}
func (b *accessReadOnlyBus) WriteHoldingRegister(byte, uint16, uint16) error {
	b.writes.Add(1)
	return fmt.Errorf("fixture forbids writes")
}

func singleDeviceFixture(t *testing.T) (*pointConfigFixture, string, string, func()) {
	t.Helper()
	f := newPointConfigFixture(t)
	f.handler.SetEMS(emsapi.New(f.runtime, time.Minute))
	loadSingleDeviceProfile(t, f.handler, singleDeviceProfile)
	plant := pumphouse.New(pumphouse.DefaultConfig())
	simulator, err := plant.ServeTCP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { simulator.Close() })
	writes := new(atomic.Int32)
	f.bridge = bridge.New(f.runtime, bridge.NewNodeSource(f.handler.nc), func(protocol, address string, timeout time.Duration) (channel.Bus, error) {
		if protocol != "tcp" || address != simulator.Addr() {
			return nil, fmt.Errorf("non-fixture connection refused")
		}
		bus, err := modbusrt.OpenTCP(address, timeout)
		if err != nil {
			return nil, err
		}
		return &accessReadOnlyBus{bus, writes}, nil
	})
	t.Cleanup(func() {
		if writes.Load() != 0 {
			t.Errorf("read-only access attempted %d writes", writes.Load())
		}
	})
	root := snapshotRoot(t, f.base, f.token)
	site := createNodeAPI(t, f.base, f.token, "site", root, "Access fixture", 0)
	device := createNodeAPI(t, f.base, f.token, "device", site, "模拟仪表", 0)
	var created struct {
		ID string `json:"id"`
	}
	status, body := call(t, http.MethodPost, f.base+"/modbus", f.token, ModbusConnectionInput{Parent: site, Description: "私有模拟器", Protocol: "TCP", URI: simulator.Addr(), PollPeriod: 100, Timeout: 200}, &created)
	if status != 200 {
		t.Fatalf("channel: %d %s", status, body)
	}
	return f, created.ID, device, func() { simulator.Close() }
}

func bindSingleDevice(t *testing.T, f *pointConfigFixture, bus, device string) {
	t.Helper()
	url := f.base + "/modbus/" + bus + "/units/apply"
	input := map[string]any{"profile_id": "access-sim", "servers": []int{1}, "business_node_id": device, "create_points": true, "dry_run": true}
	var result struct {
		Version string `json:"preview_version"`
	}
	status, body := call(t, http.MethodPost, url, f.token, input, &result)
	if status != 200 {
		t.Fatalf("preview: %d %s", status, body)
	}
	input["dry_run"], input["preview_version"] = false, result.Version
	status, body = call(t, http.MethodPost, url, f.token, input, nil)
	if status != 200 {
		t.Fatalf("apply: %d %s", status, body)
	}
}

func waitAccessSample(t *testing.T, f *pointConfigFixture, bus, name, quality string) {
	t.Helper()
	var model unitModelView
	call(t, http.MethodGet, f.base+"/modbus/"+bus+"/units/1/model", f.token, nil, &model)
	id := model.Model.PointMap[name]
	waitFor(t, func() bool {
		var state struct {
			Values []struct {
				PointID string `json:"point_id"`
				Quality string `json:"quality"`
			} `json:"values"`
		}
		status, _ := call(t, http.MethodGet, f.base+"/ems/state", f.token, nil, &state)
		if status != 200 {
			return false
		}
		for _, value := range state.Values {
			if value.PointID == id && value.Quality == quality {
				return true
			}
		}
		return false
	}, "real observation quality "+quality+" for "+name)
}

func TestSingleDeviceReadOnlyEvidenceTracksProfileAndMapping(t *testing.T) {
	f, bus, device, offline := singleDeviceFixture(t)
	bindSingleDevice(t, f, bus, device)
	var initial struct {
		Passed bool `json:"passed"`
	}
	status, body := call(t, http.MethodPost, f.base+"/modbus/"+bus+"/units/1/verify", f.token, map[string]any{"level": "L2"}, &initial)
	if status != 200 || initial.Passed || !strings.Contains(body, "无实时值") {
		t.Fatalf("L2 before first sample: %d %s", status, body)
	}
	f.cycle(t)
	waitAccessSample(t, f, bus, "水箱液位", "good")
	url := f.base + "/modbus/" + bus + "/units/1"
	var result struct {
		Passed  bool             `json:"passed"`
		Reasons []string         `json:"reasons"`
		Points  []map[string]any `json:"points"`
		Record  struct {
			At, Actor, Basis string
			Current          bool
		} `json:"record"`
	}
	for _, level := range []string{"L1", "L2"} {
		status, body := call(t, http.MethodPost, url+"/verify", f.token, map[string]any{"level": level}, &result)
		if status != 200 || !result.Passed {
			t.Fatalf("%s: %d %s", level, status, body)
		}
		if level == "L2" && len(result.Points) != 1 {
			t.Fatalf("L2 ignored required:false: %s", body)
		}
		if result.Record.At == "" || result.Record.Actor == "" || result.Record.Basis == "" || !result.Record.Current {
			t.Fatalf("missing versioned actor/time evidence: %s", body)
		}
	}
	offline()
	waitAccessSample(t, f, bus, "水箱液位", "invalid")
	status, body = call(t, http.MethodPost, url+"/verify", f.token, map[string]any{"level": "L2"}, &result)
	if status != 200 || result.Passed || result.Points[0]["quality"] == "good" {
		t.Fatalf("failed read passed: %d %s", status, body)
	}
	var view unitModelView
	call(t, http.MethodGet, url+"/model", f.token, nil, &view)
	input := *view.Model
	delete(input.PointMap, "水箱液位")
	status, body = call(t, http.MethodPut, url+"/model", f.token, input, nil)
	if status != 200 {
		t.Fatalf("save gap: %d %s", status, body)
	}
	var history struct {
		Records []struct {
			Current bool `json:"current"`
		} `json:"records"`
	}
	call(t, http.MethodGet, url+"/verify", f.token, nil, &history)
	for _, record := range history.Records {
		if record.Current {
			t.Fatal("old mapping evidence remained current")
		}
	}
	status, body = call(t, http.MethodPost, url+"/verify", f.token, map[string]any{"level": "L2"}, &result)
	if status != 200 || result.Passed || !strings.Contains(body, "必需点未映射") {
		t.Fatalf("missing required point: %d %s", status, body)
	}
	var ledger DeviceLedgerResponse
	status, body = call(t, http.MethodGet, f.base+"/device-ledger", f.token, nil, &ledger)
	if status != 200 {
		t.Fatal(body)
	}
	for _, item := range ledger.Devices {
		if item.BusinessNodeID == device && (item.Bound || item.Health == "healthy") {
			t.Fatal("incomplete mapping appeared online/bound")
		}
	}
	offline()
	status, body = call(t, http.MethodPost, url+"/verify", f.token, map[string]any{"level": "L1"}, &result)
	if status != 200 || result.Passed || len(result.Reasons) == 0 {
		t.Fatalf("offline: %d %s", status, body)
	}
	viewer := inputAccount(t, f, snapshotRoot(t, f.base, f.token), "viewer", nil)
	for _, level := range []string{"L1", "L2"} {
		status, _ := call(t, http.MethodPost, url+"/verify", viewer, map[string]any{"level": level}, nil)
		if status != 403 {
			t.Fatalf("viewer %s: %d", level, status)
		}
	}
}

func TestSingleDeviceRejectsChangedProfileAndAddress(t *testing.T) {
	f, bus, device, _ := singleDeviceFixture(t)
	bindSingleDevice(t, f, bus, device)
	f.cycle(t)
	waitAccessSample(t, f, bus, "水箱液位", "good")
	url := f.base + "/modbus/" + bus + "/units/1"
	var result struct {
		Passed bool `json:"passed"`
	}
	loadSingleDeviceProfile(t, f.handler, strings.Replace(singleDeviceProfile, `"scale":0.1`, `"scale":0.2`, 1))
	status, body := call(t, http.MethodPost, url+"/verify", f.token, map[string]any{"level": "L2"}, &result)
	if status != 200 || result.Passed || !strings.Contains(body, "Profile") {
		t.Fatalf("changed Profile accepted old sample: %d %s", status, body)
	}
	loadSingleDeviceProfile(t, f.handler, singleDeviceProfile)
	var view unitModelView
	call(t, http.MethodGet, url+"/model", f.token, nil, &view)
	signed := *view.Model
	signed.Verified, signed.Verifier, signed.VerifiedAt, signed.DeviceSerial = true, "fixture", "2026-11-25", "simulator"
	call(t, http.MethodPut, url+"/model", f.token, signed, nil)
	pointID := view.Model.PointMap["水箱液位"]
	changed := ModbusIOInput{Parent: bus, Description: "水箱液位", ServerID: 1, Address: 101, IOType: "modbusHoldingRegister", DataFormat: "uint16", Scale: 0.1, Units: "%", ReadOnly: true}
	status, body = call(t, http.MethodPut, f.base+"/modbus/io/"+pointID, f.token, changed, nil)
	if status != 200 {
		t.Fatalf("edit address: %d %s", status, body)
	}
	call(t, http.MethodGet, url+"/model", f.token, nil, &view)
	if view.Model.Verified || !view.Model.VerificationRequired {
		t.Fatal("physical mapping edit retained old field verification")
	}
	f.cycle(t)
	status, body = call(t, http.MethodPost, url+"/verify", f.token, map[string]any{"level": "L2"}, &result)
	if status != 200 || result.Passed || !strings.Contains(body, "地址") {
		t.Fatalf("wrong address passed: %d %s", status, body)
	}
}

func TestSingleDeviceEvidenceSurvivesStoreRestart(t *testing.T) {
	dir, password := t.TempDir(), "isolated-restart-fixture"
	start := func() (*pointConfigFixture, func()) {
		base, token, h, stop := startInputServer(t, dir, password)
		service := runtime.NewService(nil, time.Minute, time.Second, 100)
		t.Cleanup(service.Close)
		h.SetEMS(emsapi.New(service, time.Minute))
		loadSingleDeviceProfile(t, h, singleDeviceProfile)
		return &pointConfigFixture{base: base, token: token, handler: h, runtime: service}, stop
	}
	f, stop := start()
	simulator, err := pumphouse.New(pumphouse.DefaultConfig()).ServeTCP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer simulator.Close()
	root := snapshotRoot(t, f.base, f.token)
	site := createNodeAPI(t, f.base, f.token, "site", root, "restart", 0)
	device := createNodeAPI(t, f.base, f.token, "device", site, "meter", 0)
	var created struct {
		ID string `json:"id"`
	}
	status, body := call(t, http.MethodPost, f.base+"/modbus", f.token, ModbusConnectionInput{Parent: site, Description: "restart simulator", Protocol: "TCP", URI: simulator.Addr(), PollPeriod: 100, Timeout: 200}, &created)
	if status != 200 {
		t.Fatalf("channel: %d %s", status, body)
	}
	bindSingleDevice(t, f, created.ID, device)
	path := "/modbus/" + created.ID + "/units/1"
	var verified struct {
		Passed bool `json:"passed"`
	}
	status, body = call(t, http.MethodPost, f.base+path+"/verify", f.token, map[string]any{"level": "L1"}, &verified)
	if status != 200 || !verified.Passed {
		t.Fatalf("L1: %d %s", status, body)
	}
	var before unitModelView
	call(t, http.MethodGet, f.base+path+"/model", f.token, nil, &before)
	stop()
	f, _ = start()
	var after unitModelView
	call(t, http.MethodGet, f.base+path+"/model", f.token, nil, &after)
	if after.Model == nil || after.Model.BusinessNodeID != device || after.Model.PointMap["水箱液位"] != before.Model.PointMap["水箱液位"] || after.Model.ProfileVersion != before.Model.ProfileVersion {
		t.Fatal("restart lost mapping or Profile version")
	}
	var history struct {
		Records []struct {
			Current, Passed bool
			Actor, At       string
		} `json:"records"`
	}
	status, body = call(t, http.MethodGet, f.base+path+"/verify", f.token, nil, &history)
	if status != 200 || len(history.Records) != 1 || !history.Records[0].Current || !history.Records[0].Passed || history.Records[0].Actor == "" || history.Records[0].At == "" {
		t.Fatalf("restart lost evidence: %d %s", status, body)
	}
}

func TestSingleDeviceProfileDefaultsReadOnlyAndValidatesEnums(t *testing.T) {
	f, bus, device, _ := singleDeviceFixture(t)
	definition := strings.Replace(singleDeviceProfile, `"read_only":true,`, "", 1)
	loadSingleDeviceProfile(t, f.handler, definition)
	var catalog struct {
		Profiles []profile.Profile `json:"profiles"`
	}
	status, body := call(t, http.MethodGet, f.base+"/ems/profiles", f.token, nil, &catalog)
	if status != 200 {
		t.Fatal(body)
	}
	for _, prof := range catalog.Profiles {
		if prof.ID == "access-sim" && !prof.PointSpecs[0].ReadOnly {
			t.Fatal("undeclared write direction became writable")
		}
	}
	definition = strings.Replace(singleDeviceProfile, `"required":false`, `"required":true`, 1)
	definition = strings.Replace(definition, `"1":"远程"`, `"2":"其他"`, 1)
	loadSingleDeviceProfile(t, f.handler, definition)
	bindSingleDevice(t, f, bus, device)
	f.cycle(t)
	waitAccessSample(t, f, bus, "远程模式", "good")
	var result struct {
		Passed bool `json:"passed"`
	}
	status, body = call(t, http.MethodPost, f.base+"/modbus/"+bus+"/units/1/verify", f.token, map[string]any{"level": "L2"}, &result)
	if status != 200 || result.Passed || !strings.Contains(body, "枚举") {
		t.Fatalf("out-of-enum quality accepted: %d %s", status, body)
	}
}

func TestSingleDeviceRejectsStaleMappingAndForeignBinding(t *testing.T) {
	f, bus, device, _ := singleDeviceFixture(t)
	bindSingleDevice(t, f, bus, device)
	url := f.base + "/modbus/" + bus + "/units/1/model"
	var first, stale unitModelView
	call(t, http.MethodGet, url, f.token, nil, &first)
	call(t, http.MethodGet, url, f.token, nil, &stale)
	delete(first.Model.PointMap, "远程模式")
	status, body := call(t, http.MethodPut, url, f.token, first.Model, nil)
	if status != 200 {
		t.Fatalf("first edit: %d %s", status, body)
	}
	status, body = call(t, http.MethodPut, url, f.token, stale.Model, nil)
	if status != 409 || !strings.Contains(body, "mapping_version_conflict") {
		t.Fatalf("stale mapping overwrite: %d %s", status, body)
	}
	stale.Model.MappingVersion = ""
	status, body = call(t, http.MethodPut, url, f.token, stale.Model, nil)
	if status != 409 {
		t.Fatalf("unversioned bound mapping overwrite: %d %s", status, body)
	}
	scoped := inputAccount(t, f, bus, "implementer", []string{"configure"})
	payload := map[string]any{"profile_id": "access-sim", "servers": []int{2}, "business_node_id": device, "create_points": true, "dry_run": true}
	status, body = call(t, http.MethodPost, f.base+"/modbus/"+bus+"/units/apply", scoped, payload, nil)
	if status != 403 {
		t.Fatalf("foreign business reference accepted: %d %s", status, body)
	}
	status, body = call(t, http.MethodPost, f.base+"/modbus/"+bus+"/units/apply", f.token, payload, nil)
	if status != 409 {
		t.Fatalf("device bound twice: %d %s", status, body)
	}
}

func TestSingleDeviceRejectsExpiredRequiredObservations(t *testing.T) {
	f, bus, device, _ := singleDeviceFixture(t)
	// Configure freshness before any acquisition runner starts.
	f.runtime.MaxAge = time.Nanosecond
	f.handler.ems.MaxAge = time.Nanosecond
	bindSingleDevice(t, f, bus, device)
	f.cycle(t)
	waitAccessSample(t, f, bus, "水箱液位", "stale")
	var result struct {
		Passed bool `json:"passed"`
		Points []struct {
			Quality string `json:"quality"`
		} `json:"points"`
	}
	status, body := call(t, http.MethodPost, f.base+"/modbus/"+bus+"/units/1/verify", f.token, map[string]any{"level": "L2"}, &result)
	if status != 200 || result.Passed || len(result.Points) != 1 || result.Points[0].Quality != "stale" {
		t.Fatalf("stale required point passed: %d %s", status, body)
	}
}

const singleDeviceProfile = `{"id":"access-sim","vendor":"Fixture","model":"只读模拟仪表","protocol":"modbus","point_specs":[{"name":"水箱液位","function":"holdingRegister","address":100,"format":"uint16","scale":0.1,"unit":"%","read_only":true,"required":true},{"name":"远程模式","function":"discreteInput","address":1,"format":"uint16","scale":1,"read_only":true,"required":false,"enum":{"0":"就地","1":"远程"}}]}`

func loadSingleDeviceProfile(t *testing.T, h *Handler, definition string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "profile.json"), []byte(definition), 0600); err != nil {
		t.Fatal(err)
	}
	h.ems.Profiles = profile.Load(dir)
	if len(h.ems.Profiles.LoadErrors) > 0 {
		t.Fatal(h.ems.Profiles.LoadErrors)
	}
}

func TestSingleDeviceProfileCreatesPointsOnlyAfterPreview(t *testing.T) {
	f := newPointConfigFixture(t)
	f.handler.SetEMS(emsapi.New(f.runtime, time.Minute))
	loadSingleDeviceProfile(t, f.handler, singleDeviceProfile)
	root := snapshotRoot(t, f.base, f.token)
	site := createNodeAPI(t, f.base, f.token, "site", root, "site", 0)
	device := createNodeAPI(t, f.base, f.token, "device", site, "meter", 0)
	bus := f.createBus(t, "isolated access")
	url := f.base + "/modbus/" + bus + "/units/apply"
	input := map[string]any{"profile_id": "access-sim", "servers": []int{1}, "business_node_id": device, "create_points": true, "dry_run": true}
	var preview struct {
		PreviewVersion string               `json:"preview_version"`
		Applied        int                  `json:"applied"`
		Results        []profileApplyResult `json:"results"`
	}
	status, body := call(t, http.MethodPost, url, f.token, input, &preview)
	if status != 200 || preview.PreviewVersion == "" || preview.Applied != 0 {
		t.Fatalf("single preview: %d %s", status, body)
	}
	var view unitModelView
	modelURL := f.base + "/modbus/" + bus + "/units/1/model"
	call(t, http.MethodGet, modelURL, f.token, nil, &view)
	if view.Model != nil || len(view.Points) != 0 {
		t.Fatal("dry-run persisted configuration")
	}
	status, body = call(t, http.MethodPut, modelURL, f.token, unitModelConfig{ProfileID: "access-sim", BusinessNodeID: device, PointMap: map[string]string{}}, nil)
	if status != 409 {
		t.Fatalf("model save bypassed Profile preview: %d %s", status, body)
	}
	input["dry_run"] = false
	status, _ = call(t, http.MethodPost, url, f.token, input, nil)
	if status != 409 {
		t.Fatalf("apply without reviewed version: %d", status)
	}
	input["preview_version"] = preview.PreviewVersion
	status, body = call(t, http.MethodPost, url, f.token, input, nil)
	if status != 200 {
		t.Fatalf("apply: %d %s", status, body)
	}
	call(t, http.MethodGet, modelURL, f.token, nil, &view)
	if view.Model == nil || view.Model.BusinessNodeID != device || len(view.Model.PointMap) != 2 || len(view.Points) != 2 {
		t.Fatalf("saved mapping: %+v", view)
	}
	if view.Points[0].Scale != 0.1 && view.Points[1].Scale != 0.1 {
		t.Fatal("lost Profile scale")
	}
	status, _ = call(t, http.MethodPost, url, f.token, input, nil)
	if status != 409 {
		t.Fatalf("stale preview accepted: %d", status)
	}
	// Same ID with a new definition must invalidate the saved version on read.
	loadSingleDeviceProfile(t, f.handler, strings.Replace(singleDeviceProfile, `"scale":0.1`, `"scale":0.2`, 1))
	call(t, http.MethodGet, modelURL, f.token, nil, &view)
	if !view.Model.VerificationRequired {
		raw, _ := json.Marshal(view.Model)
		t.Fatalf("profile substitution not invalidated: %s", raw)
	}
}
