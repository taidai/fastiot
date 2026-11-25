package appapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fastiot/internal/decision"
	"fastiot/internal/ems/alarm"
	"fastiot/internal/ems/history"
	"fastiot/internal/ems/profile"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/ems/signal"
	"fastiot/internal/emsapi"
	"github.com/nats-io/nats.go"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

func TestDeliveryPreflightAcceptsConfiguredSoftwareOnlyProject(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	h.SetEMS(emsapi.New(service, time.Minute))
	root := snapshotRoot(t, base, token)
	site := createNodeAPI(t, base, token, NodeTypeSite, root, "Software-only project", 0)
	parent := createNodeAPI(t, base, token, "group", site, "Software definitions", 0)
	var input virtualInputResponse
	code, body := call(t, http.MethodPost, base+"/virtual-inputs", token, map[string]any{"parent": parent, "name": "Configured capacity", "source": "configuration", "definition": virtualCapacity("120"), "operation_id": "create"}, &input)
	if code != 200 {
		t.Fatal(body)
	}
	code, body = call(t, http.MethodPost, base+"/virtual-inputs/"+input.ID+"/apply", token, map[string]any{"revision": input.Revision, "operation_id": "apply"}, &input)
	if code != 200 {
		t.Fatal(body)
	}
	var report preflightReport
	code, body = call(t, http.MethodGet, base+"/delivery/gates?site="+site, token, nil, &report)
	if code != 200 || report.Status == "fail" || report.Content == nil || report.Content.Points != 1 || len(report.Content.Dependencies) != 0 {
		t.Fatalf("software-only definition blocked or miscounted: %d %s", code, body)
	}
}

func TestDeliveryPackageIncludesBoundAcquisition(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	h.SetEMS(&emsapi.Handler{Profiles: profile.Default()})
	root := snapshotRoot(t, base, token)
	site := createNodeAPI(t, base, token, NodeTypeSite, root, "Bound project", 0)
	device := createNodeAPI(t, base, token, "device", site, "Bound device", 0)
	bus, err := CreateDomainNode(h.nc, root, data.NodeTypeModbus, map[string]string{"description": "Bound bus"})
	if err != nil {
		t.Fatal(err)
	}
	point, err := CreateDomainNode(h.nc, bus, data.NodeTypeModbusIO, map[string]string{"description": "Bound point"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(unitModelConfig{BusinessNodeID: device, ProfileID: "enjo-pcs", PointMap: map[string]string{"power": point}})
	if err := client.SendNodePoint(h.nc, bus, data.NewPointString(unitModelPoint, "1", string(raw)), true); err != nil {
		t.Fatal(err)
	}
	var pkg projectPackage
	code, body := call(t, http.MethodGet, base+"/projects/export?site="+site, token, nil, &pkg)
	if code != 200 || len(pkg.Nodes) != 4 || pkg.PointCount != 1 || len(pkg.Dependencies) != 1 || pkg.Dependencies[0].ID != "enjo-pcs" {
		t.Fatalf("bound acquisition omitted: %d %s", code, body)
	}
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	ems := emsapi.New(service, time.Minute)
	ems.Profiles = profile.Default()
	h.SetEMS(ems)
	password := data.NewID()
	code, body = call(t, http.MethodPost, base+"/users", token, map[string]any{"parent": site, "email": "scoped@fixture.local", "password": password, "role": "implementer"}, nil)
	if code != 200 {
		t.Fatal(body)
	}
	scoped := loginAPI(t, base, "scoped@fixture.local", password)
	for _, path := range []string{"/projects/export?site=", "/delivery/gates?site="} {
		code, body = call(t, http.MethodGet, base+path+site, scoped, nil, nil)
		if code != 403 {
			t.Fatalf("hidden acquisition scope: %d %s", code, body)
		}
	}
	code, body = call(t, http.MethodPost, base+"/projects/publish", scoped, map[string]any{"site": site, "expected_fingerprint": "stale"}, nil)
	if code != 403 {
		t.Fatalf("publication crossed hidden acquisition scope: %d %s", code, body)
	}
}

func TestDeliveryPreflightProvidesRepairForRequiredMember(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	h.SetEMS(emsapi.New(service, time.Minute))
	root := snapshotRoot(t, base, token)
	site := createNodeAPI(t, base, token, NodeTypeSite, root, "Repairable project", 0)
	unit := createNodeAPI(t, base, token, "unit", site, "Required member", 0)
	createNodeAPI(t, base, token, "variable", site, "Software input", 0)
	if err := client.SendNodePoint(h.nc, unit, data.NewPointString("easyemsUnitMembers", "0", `[{"role":"measurement","required":true}]`), true); err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	code, body := call(t, http.MethodGet, base+"/delivery/gates?site="+site, token, nil, &report)
	if code != 200 || report["status"] != "fail" {
		t.Fatalf("missing required member not blocked: %d %s", code, body)
	}
	found := false
	for _, raw := range report["gates"].([]any) {
		gate := raw.(map[string]any)
		if gate["key"] == "engineering" && gate["status"] == "fail" && gate["repair_node"] == unit && gate["repair_step"] == "access" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing scoped repair link: %s", body)
	}
	if err := client.SendNodePoint(h.nc, unit, data.NewPointString("easyemsUnitMembers", "0", "[]"), true); err != nil {
		t.Fatal(err)
	}
	code, body = call(t, http.MethodGet, base+"/delivery/gates?site="+site, token, nil, &report)
	if code != 200 || report["status"] == "fail" {
		t.Fatalf("repair not reflected: %d %s", code, body)
	}
	otherSite := createNodeAPI(t, base, token, NodeTypeSite, root, "Other project's evidence", 0)
	otherPoint := createNodeAPI(t, base, token, "variable", otherSite, "Other reading", 0)
	if _, err := service.Alarms.Raise(time.Now(), "other-only", alarm.Severity("warning"), "Other alarm", "", otherSite, otherPoint); err != nil {
		t.Fatal(err)
	}
	service.History.Record([]history.Sample{{DeviceID: otherSite, PointID: otherPoint, SourceTime: time.Now(), ReceivedTime: time.Now(), Value: signal.Num(1), Quality: signal.Good}})
	code, body = call(t, http.MethodGet, base+"/delivery/gates?site="+site, token, nil, &report)
	if code != 200 {
		t.Fatal(body)
	}
	for _, raw := range report["gates"].([]any) {
		gate := raw.(map[string]any)
		if gate["key"] == "alarms" && gate["status"] != "pass" {
			t.Fatal("preflight counted another site's alarm")
		}
		if gate["key"] == "history" && gate["status"] != "warn" {
			t.Fatal("preflight counted another site's history")
		}
	}
}

func TestDeliveryPackageRejectsMissingGeneratedScreenReference(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, token)
	site := createNodeAPI(t, base, token, NodeTypeSite, root, "Screen reference", 0)
	raw, _ := json.Marshal(map[string]any{"source": `<span data-fastiot-point="old-instance-point">--</span>`})
	if err := client.SendNodePoint(h.nc, site, data.NewPointString(generatedScreenDraftType, "", string(raw)), true); err != nil {
		t.Fatal(err)
	}
	code, body := call(t, http.MethodGet, base+"/projects/export?site="+site, token, nil, nil)
	if code == 200 || !strings.Contains(body, "reference") {
		t.Fatalf("unverifiable screen exported: %d %s", code, body)
	}
}

func TestDeliveryPackageRejectsUnsafeDefinitions(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, token)
	foreignSite := createNodeAPI(t, base, token, NodeTypeSite, root, "Other site", 0)
	foreign := createNodeAPI(t, base, token, "variable", foreignSite, "Foreign input", 0)
	for _, tc := range []struct{ name, typ, text, reason string }{
		{"foreign reference", dailyStatsPoint, `{"version":1,"time_zone":"UTC","freshness":{"point_id":"` + foreign + `","max_age_seconds":60}}`, "reference"},
		{"missing reference", dailyStatsPoint, `{"version":1,"time_zone":"UTC","freshness":{"point_id":"missing-id","max_age_seconds":60}}`, "reference"},
		{"malformed definition", dailyStatsPoint, `{broken`, "definition"},
		{"hidden credential", "metadata", `{"connection":{"password":"hidden-secret-20"}}`, "secret"},
		{"hidden authorization header", "metadata", `{"headers":{"Authorization":"Bearer hidden-secret-20"}}`, "secret"},
		{"hidden api key", "metadata", `{"headers":{"X-API-Key":"hidden-secret-20"}}`, "secret"},
		{"missing profile", PointTypeProfileID, "not-installed-profile", "dependency"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			site := createNodeAPI(t, base, token, NodeTypeSite, root, tc.name, 0)
			createNodeAPI(t, base, token, "variable", site, "Input", 0)
			if err := client.SendNodePoint(h.nc, site, data.NewPointString(tc.typ, "0", tc.text), true); err != nil {
				t.Fatal(err)
			}
			code, body := call(t, http.MethodGet, base+"/projects/export?site="+site, token, nil, nil)
			if code == 200 || !strings.Contains(body, tc.reason) {
				t.Fatalf("unsafe artifact returned: %d %s", code, body)
			}
			if strings.Contains(body, "hidden-secret-20") || strings.Contains(body, foreign) {
				t.Fatal("rejection leaked secret or foreign identifier")
			}
		})
	}
}

func TestDeliveryPackageReferencesAndRuntimeBoundary(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, token)
	site := createNodeAPI(t, base, token, NodeTypeSite, root, "Portable configuration", 0)
	point := createNodeAPI(t, base, token, "variable", site, "Source", 0)
	device := createNodeAPI(t, base, token, "device", site, "Command device", 0)
	excludedAccount, err := CreateDomainNode(h.nc, site, "user", map[string]string{"email": "excluded@fixture.local"})
	if err != nil {
		t.Fatal(err)
	}
	machine, err := CreateDomainNode(h.nc, site, stateMachineType, map[string]string{"description": "Process"})
	if err != nil {
		t.Fatal(err)
	}
	store := func(id, typ string, value any) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := client.SendNodePoint(h.nc, id, data.NewPointString(typ, "0", string(raw)), true); err != nil {
			t.Fatal(err)
		}
	}
	store(site, "easyemsSafety", map[string]any{"conditions": []any{map[string]any{"point_id": point, "operator": "gt", "value": 0}}})
	store(site, "easyemsAllocator", decision.AllocatorConfig{Members: []decision.AllocatorMember{{ID: "member", Target: decision.Target{NodeID: point}, MaxCharge: 10, MaxDischarge: 10}}})
	store(site, dailyStatsPoint, map[string]any{"version": 1, "time_zone": "UTC", "freshness": map[string]any{"point_id": point, "max_age_seconds": 60}})
	store(site, screenPointType, screenDocument{Layer: site, Version: 1, Columns: 2, PointIDs: []string{point}, Slots: map[string][]string{"top": {}, "main": {"recent"}, "side": {"trend"}}, Blocks: []screenBlock{{Type: "recent", Span: 1, Binding: map[string]any{"point_ids": []string{point}, "columns": []string{"value"}, "rows": 3}}, {Type: "trend", Span: 1, Binding: map[string]any{"point_id": point}}}})
	rule, err := CreateDomainNode(h.nc, site, "decisionTable", map[string]string{"description": "Readback rule"})
	if err != nil {
		t.Fatal(err)
	}
	store(rule, "draft", decision.Document{Table: decision.Table{Rows: []decision.Row{{ID: "r1", Actions: []decision.Action{{ActionID: "a1", Readback: &decision.Readback{Target: decision.Target{NodeID: point}, TimeoutMS: 1000}}}}}}, Bindings: decision.BindingSet{Actions: map[string]decision.Target{"a1": {NodeID: point}}}})
	definition := decision.StateMachineDefinition{InitialState: "idle", States: []string{"idle"}, CommandBindings: map[string]decision.StateMachineCommandBinding{"start": {DeviceID: device, CommandID: "start"}}}
	store(machine, stateMachineDocumentPoint, decision.StateMachineDocument{Schema: 1, Revision: 1, Draft: definition, CurrentState: "runtime-state", LastExecution: &decision.StateMachineExecution{RequestID: "runtime-command-id"}})
	for _, typ := range []string{"value", "valueSet", "ruleAccess", "easyemsAllocHistory", stateMachineRunPoint, "easyemsInput", "easyemsDerivedObservation", "ruleExecutionFact"} {
		if err := client.SendNodePoint(h.nc, machine, data.NewPointString(typ, "0", "runtime-"+typ), true); err != nil {
			t.Fatal(err)
		}
	}
	var pkg projectPackage
	code, body := call(t, http.MethodGet, base+"/projects/export?site="+site, token, nil, &pkg)
	if code != 200 {
		t.Fatalf("export: %d %s", code, body)
	}
	for _, forbidden := range []string{site, point, device, excludedAccount, "runtime-state", "runtime-command-id", "runtime-valueSet", "runtime-ruleAccess", "runtime-easyemsAllocHistory", "runtime-easyemsInput", "runtime-easyemsDerivedObservation", "runtime-ruleExecutionFact"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("package retained source/runtime identity %q", forbidden)
		}
	}
	if pkg.ScreenCount != 1 || pkg.PointCount != 1 || pkg.StrategyCount != 2 {
		t.Fatalf("counts: screens=%d points=%d strategies=%d", pkg.ScreenCount, pkg.PointCount, pkg.StrategyCount)
	}
	if err := pkg.validate(); err != nil {
		t.Fatal(err)
	}
	refs := map[string]bool{}
	for _, node := range pkg.Nodes {
		refs[node.Ref] = true
	}
	for _, node := range pkg.Nodes {
		for _, p := range node.Points {
			for _, ref := range packageReferences(data.NodeEdge{Type: node.Type}, p) {
				if !refs[ref] {
					t.Fatalf("unresolvable package reference %s", ref)
				}
			}
		}
	}
}

func TestDeliveryReleaseFreezesOneArtifact(t *testing.T) {
	dir, password := t.TempDir(), data.NewID()
	base, token, h, stop := startInputServer(t, dir, password)
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	h.SetEMS(emsapi.New(service, time.Minute))
	root := snapshotRoot(t, base, token)
	site := createNodeAPI(t, base, token, NodeTypeSite, root, "Release fixture", 0)
	check := func() map[string]any {
		t.Helper()
		var report map[string]any
		code, body := call(t, http.MethodGet, base+"/delivery/gates?site="+site, token, nil, &report)
		if code != 200 {
			t.Fatalf("preflight %d %s", code, body)
		}
		return report
	}
	blocked := check()
	if blocked["status"] != "fail" {
		t.Fatalf("empty project passed: %v", blocked)
	}
	var published map[string]any
	code, body := call(t, http.MethodPost, base+"/projects/publish", token, map[string]any{"site": site, "confirm": true, "expected_fingerprint": blocked["fingerprint"]}, &published)
	if code != 409 {
		t.Fatalf("blocker bypass: %d %s", code, body)
	}
	// An explicit software input is valid base IoT content, not an EMS dependency.
	point := createNodeAPI(t, base, token, "variable", site, "Software observation", 0)
	report := check()
	if report["status"] == "fail" {
		t.Fatalf("base IoT project blocked: %v", report)
	}
	fingerprint, _ := report["fingerprint"].(string)
	if fingerprint == "" {
		t.Fatal("preflight omitted engineering fingerprint")
	}
	// An edit after inspection must fail CAS, even when the new content is valid.
	extra := createNodeAPI(t, base, token, "variable", site, "Second observation", 0)
	code, body = call(t, http.MethodPost, base+"/projects/publish", token, map[string]any{"site": site, "expected_fingerprint": fingerprint}, &published)
	if code != 409 || !strings.Contains(body, "version_conflict") {
		t.Fatalf("stale publish: %d %s", code, body)
	}
	for _, typ := range []string{"ruleAccess", "valueSet"} {
		if err := client.SendNodePoint(h.nc, site, data.NewPointString(typ, "0", "old-runtime-20"), true); err != nil {
			t.Fatal(err)
		}
	}
	report = check()
	var writes atomic.Int64
	var runtimeConfigurationChanges atomic.Int64
	h.ems.BeginAlarmConfiguration = func() func() { runtimeConfigurationChanges.Add(1); return func() {} }
	sub, err := h.nc.Subscribe(client.SubjectNodePoints("*")+".>", func(_ *nats.Msg) { writes.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()
	if err := h.nc.Flush(); err != nil {
		t.Fatal(err)
	}
	_ = extra
	_ = point
	code, body = call(t, http.MethodPost, base+"/projects/publish", token, map[string]any{"site": site, "expected_fingerprint": report["fingerprint"]}, &published)
	if code != 200 {
		t.Fatalf("publish: %d %s", code, body)
	}
	if published["deliverable"] != true {
		t.Fatalf("release not deliverable: %v", published)
	}
	version := published["id"].(string)
	stored, err := os.ReadFile(filepath.Join(h.projectDir(site), version+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), "old-runtime-20") {
		t.Fatal("frozen version retained runtime/grant evidence")
	}
	sealed, err := h.loadVersion(site, version)
	if err != nil {
		t.Fatal(err)
	}
	sealed.Note = "attempted replacement"
	if err := h.saveVersion(sealed); err == nil {
		t.Fatal("published version ID was overwritten")
	}
	var damaged map[string]any
	if err := json.Unmarshal(stored, &damaged); err != nil {
		t.Fatal(err)
	}
	damaged["note"] = "tampered on disk"
	corrupted, err := json.Marshal(damaged)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(h.projectDir(site), version+".json")
	if err := os.WriteFile(file, corrupted, 0600); err != nil {
		t.Fatal(err)
	}
	badCode, badBody := call(t, http.MethodGet, base+"/projects/export?site="+site+"&version="+version, token, nil, nil)
	if badCode == 200 || !strings.Contains(badBody, "integrity") {
		t.Fatalf("corrupted persisted version accepted: %d %s", badCode, badBody)
	}
	if err := os.WriteFile(file, stored, 0600); err != nil {
		t.Fatal(err)
	}
	download := func() string {
		t.Helper()
		var pkg map[string]any
		code, body := call(t, http.MethodGet, base+"/projects/export?site="+site+"&version="+version, token, nil, &pkg)
		if code != 200 {
			t.Fatalf("export: %d %s", code, body)
		}
		if pkg["source_version"] != version || pkg["source_fingerprint"] != report["fingerprint"] {
			t.Fatalf("wrong frozen source: %s", body)
		}
		if pkg["point_count"] != float64(2) || len(pkg["dependencies"].([]any)) != 0 {
			t.Fatalf("wrong frozen counts/dependencies: %s", body)
		}
		canonical, _ := json.Marshal(pkg)
		return string(canonical)
	}
	artifact := download()
	for _, mutate := range []func(*projectPackage){
		func(pkg *projectPackage) { pkg.Manifest["node:n1"] = "tampered" },
		func(pkg *projectPackage) { pkg.PointCount++ },
		func(pkg *projectPackage) { pkg.ScreenCount++; pkg.seal() },
		func(pkg *projectPackage) { pkg.SourceFingerprint = "tampered" },
		func(pkg *projectPackage) { pkg.Integrity = "" },
		func(pkg *projectPackage) {
			pkg.Nodes[0].Points = append(pkg.Nodes[0].Points, data.NewPointString(dailyStatsPoint, "0", `{"time_zone":"UTC","freshness":{"point_id":"old-instance-id","max_age_seconds":60}}`))
			pkg.seal()
		},
	} {
		var tampered projectPackage
		if err := json.Unmarshal([]byte(artifact), &tampered); err != nil {
			t.Fatal(err)
		}
		mutate(&tampered)
		if err := tampered.validate(); err == nil {
			t.Fatal("tampered package passed validation")
		}
	}
	if err := h.nc.Flush(); err != nil {
		t.Fatal(err)
	}
	if runtimeConfigurationChanges.Load() != 0 {
		t.Fatal("publication entered runtime reconfiguration")
	}
	if writes.Load() != 0 {
		t.Fatalf("publish/export wrote engineering/runtime points: %d", writes.Load())
	}
	if err := sub.Unsubscribe(); err != nil {
		t.Fatal(err)
	}
	// Concurrent HTTP edits either win first (CAS conflict) or follow a release
	// of the inspected content. They cannot enter the middle of a publication.
	type response struct {
		code    int
		body    string
		version versionSummary
	}
	publishedRace := make(chan response, 1)
	go func() {
		result := response{}
		defer func() { publishedRace <- result }()
		result.code, result.body = call(t, http.MethodPost, base+"/projects/publish", token, map[string]any{"site": site, "expected_fingerprint": report["fingerprint"]}, &result.version)
	}()
	editCode, editBody := call(t, http.MethodPatch, base+"/nodes/"+point, token, map[string]any{"description": "Concurrent changed observation"}, nil)
	if editCode != 200 {
		t.Fatalf("concurrent edit: %d %s", editCode, editBody)
	}
	race := <-publishedRace
	if race.code == 200 {
		var frozen projectPackage
		code, body := call(t, http.MethodGet, base+"/projects/export?site="+site+"&version="+race.version.ID, token, nil, &frozen)
		if code != 200 || strings.Contains(body, "Concurrent changed observation") || frozen.SourceFingerprint != report["fingerprint"] || frozen.Version != 2 {
			t.Fatalf("mixed concurrent release: %d %s", code, body)
		}
	} else if race.code != 409 || !strings.Contains(race.body, "version_conflict") {
		t.Fatalf("concurrent publication: %d %s", race.code, race.body)
	}
	createNodeAPI(t, base, token, "variable", site, "Unpublished edit", 0)
	if got := download(); got != artifact {
		t.Fatal("published artifact changed after live edit")
	}
	stop()
	base, token, h, _ = startInputServer(t, dir, password)
	h.SetEMS(emsapi.New(service, time.Minute))
	if got := download(); got != artifact {
		t.Fatal("published artifact changed after restart")
	}
}
