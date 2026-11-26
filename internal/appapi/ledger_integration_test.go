package appapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	domainaccess "fastiot/internal/domain/access"
	"fastiot/internal/ems/profile"

	"github.com/simpleiot/simpleiot/data"
)

// All observations use the authenticated HTTP boundary, including reloads.
func ledgerSnapshot(t *testing.T, base, token, project string) []data.NodeEdge {
	t.Helper()
	var result struct {
		Nodes []data.NodeEdge `json:"nodes"`
	}
	status, body := projectCall(t, http.MethodGet, base+"/snapshot", token, project, nil, &result)
	if status != 200 {
		t.Fatalf("snapshot: %d %s", status, body)
	}
	return result.Nodes
}

func TestLedgerMemberEditsRejectStaleVersionAcrossRestart(t *testing.T) {
	dir, password := t.TempDir(), data.NewID()
	base, first, _, stop := startInputServer(t, dir, password)
	second := loginAPI(t, base, "admin", password)
	root := snapshotRoot(t, base, first)
	unit := createNodeAPI(t, base, first, "unit", root, "Supply", 0)
	input := map[string]any{"description": "Supply", "members_version": 0, "members": []map[string]any{{"role": "drive", "required": true}}}
	var saved struct {
		Version int      `json:"members_version"`
		Missing []string `json:"missing_required"`
	}
	status, body := projectCall(t, http.MethodPatch, base+"/nodes/"+unit, first, root, input, &saved)
	if status != 200 || saved.Version != 1 || len(saved.Missing) != 1 || saved.Missing[0] != "drive" {
		t.Fatalf("first save must return revision 1 and explicit required gap: %d %s", status, body)
	}
	status, body = projectCall(t, http.MethodPatch, base+"/nodes/"+unit, second, root, input, nil)
	if status != 409 || !strings.Contains(body, "members_version_conflict") {
		t.Fatalf("stale editor: %d %s", status, body)
	}
	stop()
	base, first, _, _ = startInputServer(t, dir, password)
	status, body = projectCall(t, http.MethodPatch, base+"/nodes/"+unit, first, root, input, nil)
	if status != 409 {
		t.Fatalf("restart lost revision: %d %s", status, body)
	}
	savedNode := ledgerNamed(t, ledgerSnapshot(t, base, first, root), root, "Supply")
	version, _ := savedNode.Points.Value(domainaccess.MembersVersionPoint, "")
	if version != 1 {
		t.Fatalf("reload version = %v", version)
	}
	delete(input, "members_version")
	status, body = projectCall(t, http.MethodPatch, base+"/nodes/"+unit, first, root, input, nil)
	if status != 400 || !strings.Contains(body, "members_version_required") {
		t.Fatalf("unversioned overwrite: %d %s", status, body)
	}
}

func ledgerNamed(t *testing.T, nodes []data.NodeEdge, parent, name string) data.NodeEdge {
	t.Helper()
	for _, node := range nodes {
		if node.Parent == parent && node.Desc() == name {
			return node
		}
	}
	t.Fatalf("missing %s under %s", name, parent)
	return data.NodeEdge{}
}

func TestLedgerTemplatesCreateOnlyUnboundStructure(t *testing.T) {
	base, admin, _, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, admin)
	for template, count := range map[string]int{"blank": 1, "pump-house": 5, "storage": 6} {
		var created struct {
			ID      string `json:"id"`
			Created int    `json:"created"`
		}
		status, body := projectCall(t, http.MethodPost, base+"/nodes", admin, root, map[string]any{"parent": root, "description": template, "site_template": template}, &created)
		if status != 200 || created.Created != count {
			t.Fatalf("%s template: %d %s", template, status, body)
		}
	}
	for _, node := range ledgerSnapshot(t, base, admin, root) {
		if node.Parent == "root" || node.Type == "user" {
			continue
		}
		for _, point := range node.Points {
			switch point.Type {
			case "description", "nodeType":
			case domainaccess.MembersPoint:
				var members []domainaccess.MemberRole
				if json.Unmarshal([]byte(point.Txt()), &members) != nil {
					t.Fatal("malformed template members")
				}
				for _, member := range members {
					if member.NodeID != "" {
						t.Fatal("template carried an old binding")
					}
				}
			default:
				t.Fatalf("template carried address, credential or runtime metadata: %s", point.Type)
			}
		}
	}
}

func TestLedgerTemplateCopyBindingsAndScopePersist(t *testing.T) {
	dir, password := t.TempDir(), data.NewID()
	base, admin, _, stop := startInputServer(t, dir, password)
	root := snapshotRoot(t, base, admin)
	var site struct {
		ID      string `json:"id"`
		Created int    `json:"created"`
	}
	status, body := projectCall(t, http.MethodPost, base+"/nodes", admin, root, map[string]any{"parent": root, "description": "Pump Site", "site_template": "pump-house"}, &site)
	if status != 200 || site.Created != 5 {
		t.Fatalf("template: %d %s", status, body)
	}
	nodes := ledgerSnapshot(t, base, admin, root)
	area := ledgerNamed(t, nodes, site.ID, "泵房")
	unit := ledgerNamed(t, nodes, area.ID, "供水单元")
	drive := ledgerNamed(t, nodes, unit.ID, "泵驱动")
	pressure := ledgerNamed(t, nodes, area.ID, "总管压力测量")
	// New templates contain only structure, descriptions and unbound member roles.
	for _, node := range []data.NodeEdge{area, unit, drive, pressure} {
		for _, p := range node.Points {
			if p.Type != "description" && p.Type != "nodeType" && p.Type != domainaccess.MembersPoint {
				t.Fatalf("template carried nonstructural point: %s", p.Type)
			}
		}
	}
	createNodeAPI(t, base, admin, "variable", drive.ID, "Test observation", 42)
	members := []domainaccess.MemberRole{{Role: "drive", NodeID: drive.ID, Required: true}, {Role: "pressure", NodeID: pressure.ID, Required: true}, {Role: "optional"}}
	input := map[string]any{"description": "供水单元", "members_version": 0, "members": members}
	status, body = projectCall(t, http.MethodPatch, base+"/nodes/"+unit.ID, admin, root, input, nil)
	if status != 200 {
		t.Fatalf("bind: %d %s", status, body)
	}
	var copy struct {
		ID      string   `json:"id"`
		Created int      `json:"created"`
		Confirm []string `json:"confirm"`
	}
	status, body = projectCall(t, http.MethodPost, base+"/nodes/"+unit.ID+"/copy", admin, root, map[string]any{"parent": area.ID, "description": "Supply copy"}, &copy)
	if status != 200 || copy.Created != 3 || len(copy.Confirm) != 1 || copy.Confirm[0] != pressure.ID {
		t.Fatalf("copy: %d %s", status, body)
	}
	checkCopy := func() {
		t.Helper()
		nodes = ledgerSnapshot(t, base, admin, root)
		copiedUnit := ledgerNamed(t, nodes, area.ID, "Supply copy")
		copiedDrive := ledgerNamed(t, nodes, copiedUnit.ID, "泵驱动")
		copiedPoint := ledgerNamed(t, nodes, copiedDrive.ID, "Test observation")
		if _, found := copiedPoint.Points.Value("value", ""); found {
			t.Fatal("copy carried runtime observation")
		}
		version, _ := copiedUnit.Points.Value(domainaccess.MembersVersionPoint, "")
		if version != 0 {
			t.Fatalf("new independent copy inherited old edit revision: %v", version)
		}
		raw, _ := copiedUnit.Points.Text(domainaccess.MembersPoint, "")
		var roles []domainaccess.MemberRole
		if json.Unmarshal([]byte(raw), &roles) != nil || len(roles) != 3 || roles[0].NodeID != copiedDrive.ID || roles[0].NodeID == drive.ID || roles[1].NodeID != "" || !roles[1].Required {
			t.Fatalf("copy references: %s", raw)
		}
	}
	checkCopy()
	for _, typ := range []string{"site", "area", "unit", "device", "group"} {
		status, body = projectCall(t, http.MethodPost, base+"/nodes", admin, root, map[string]any{"parent": drive.ID, "type": typ, "description": "illegal child"}, nil)
		if status != 400 || !strings.Contains(body, "invalid_parent_type") {
			t.Fatalf("illegal %s beneath device: %d %s", typ, status, body)
		}
	}
	implementer := inputAccount(t, &pointConfigFixture{base: base, token: admin}, unit.ID, "implementer", nil)
	input["members_version"] = 1
	status, body = projectCall(t, http.MethodPatch, base+"/nodes/"+unit.ID, implementer, root, input, nil)
	if status != 403 {
		t.Fatalf("out-of-scope member reference: %d %s", status, body)
	}
	status, body = projectCall(t, http.MethodPost, base+"/nodes/"+unit.ID+"/copy", implementer, root, map[string]any{"parent": area.ID, "description": "forbidden copy"}, nil)
	if status != 400 || !strings.Contains(body, "invalid_copy") {
		t.Fatalf("out-of-scope destination: %d %s", status, body)
	}
	members[0].NodeID = area.ID
	input["members"] = members
	status, body = projectCall(t, http.MethodPatch, base+"/nodes/"+unit.ID, admin, root, input, nil)
	if status != 400 || !strings.Contains(body, "member_requires_device_or_point") {
		t.Fatalf("illegal member kind: %d %s", status, body)
	}
	stop()
	base, admin, _, _ = startInputServer(t, dir, password)
	checkCopy()
}

// The ledger must expose required-point L2 facts without turning the manual
// UnitModel sign-off into evidence: ADR 0011 needs 未绑定 / 待验证 / 已接入 to be
// derivable from real records.
func TestLedgerReportsRequiredPointVerificationFacts(t *testing.T) {
	f, bus, device, _ := singleDeviceFixture(t)
	bindSingleDevice(t, f, bus, device)

	ledgerDevice := func() LedgerDevice {
		t.Helper()
		var ledger DeviceLedgerResponse
		status, body := call(t, http.MethodGet, f.base+"/device-ledger", f.token, nil, &ledger)
		if status != 200 {
			t.Fatalf("ledger: %d %s", status, body)
		}
		for _, item := range ledger.Devices {
			if item.ChannelID == bus && item.ServerID == 1 {
				return item
			}
		}
		t.Fatalf("ledger missing %s/1: %s", bus, body)
		return LedgerDevice{}
	}

	// access-sim declares one required point (水箱液位) and one optional point
	// (远程模式), so only the required point may ever be counted as verified.
	noL2 := ledgerDevice()
	if noL2.RequiredPoints != 1 || noL2.VerifiedPoints != 0 || noL2.L2VerifiedAt != "" {
		t.Fatalf("without L2 evidence: %+v", noL2)
	}
	t.Logf("ledger without L2: required_points=%d verified_points=%d l2_verified_at=%q verified=%v",
		noL2.RequiredPoints, noL2.VerifiedPoints, noL2.L2VerifiedAt, noL2.Verified)
	if noL2.Verified {
		t.Fatalf("manual sign-off invented without operator confirmation: %+v", noL2)
	}

	f.cycle(t)
	waitAccessSample(t, f, bus, "水箱液位", "good")
	var l1 struct {
		Passed bool `json:"passed"`
	}
	status, body := call(t, http.MethodPost, f.base+"/modbus/"+bus+"/units/1/verify", f.token, map[string]any{"level": "L1"}, &l1)
	if status != 200 || !l1.Passed {
		t.Fatalf("L1: %d %s", status, body)
	}
	if afterL1 := ledgerDevice(); afterL1.VerifiedPoints != 0 || afterL1.L2VerifiedAt != "" {
		t.Fatalf("L1 counted as point evidence: %+v", afterL1)
	}

	var l2 struct {
		Passed bool `json:"passed"`
	}
	status, body = call(t, http.MethodPost, f.base+"/modbus/"+bus+"/units/1/verify", f.token, map[string]any{"level": "L2"}, &l2)
	if status != 200 || !l2.Passed {
		t.Fatalf("L2: %d %s", status, body)
	}
	passing := ledgerDevice()
	if passing.RequiredPoints != 1 || passing.VerifiedPoints != 1 || passing.L2VerifiedAt == "" {
		t.Fatalf("full L2 evidence: %+v", passing)
	}
	t.Logf("ledger after L2: required_points=%d verified_points=%d l2_verified_at=%q bound=%v verified=%v",
		passing.RequiredPoints, passing.VerifiedPoints, passing.L2VerifiedAt, passing.Bound, passing.Verified)
	if !passing.Bound {
		t.Fatalf("complete mapping must stay bound: %+v", passing)
	}
	if passing.Verified {
		t.Fatalf("L2 evidence must not fake the manual sign-off: %+v", passing)
	}
	var history struct {
		Records []struct {
			Level  string `json:"level"`
			Passed bool   `json:"passed"`
			Points []struct {
				Name   string `json:"name"`
				Passed bool   `json:"passed"`
			} `json:"points"`
		} `json:"records"`
	}
	status, body = call(t, http.MethodGet, f.base+"/modbus/"+bus+"/units/1/verify", f.token, nil, &history)
	if status != 200 {
		t.Fatalf("history: %d %s", status, body)
	}
	persisted := false
	for _, record := range history.Records {
		if record.Level != "L2" || record.Points == nil {
			continue
		}
		persisted = true
		if len(record.Points) != 1 || record.Points[0].Name != "水箱液位" || !record.Points[0].Passed {
			t.Fatalf("persisted L2 point evidence: %s", body)
		}
	}
	if !persisted {
		t.Fatalf("L2 point evidence not persisted: %s", body)
	}

	// Changing the mapping changes the access basis, so the earlier L2 run
	// stops counting until the new configuration is re-verified.
	var view unitModelView
	call(t, http.MethodGet, f.base+"/modbus/"+bus+"/units/1/model", f.token, nil, &view)
	input := *view.Model
	delete(input.PointMap, "水箱液位")
	status, body = call(t, http.MethodPut, f.base+"/modbus/"+bus+"/units/1/model", f.token, input, nil)
	if status != 200 {
		t.Fatalf("mapping edit: %d %s", status, body)
	}
	stale := ledgerDevice()
	if stale.RequiredPoints != 1 || stale.VerifiedPoints != 0 || stale.L2VerifiedAt != "" {
		t.Fatalf("stale L2 evidence counted: %+v", stale)
	}
}

// ledgerVerificationFacts must derive the counts from persisted L2 evidence
// only: a pass proves every required point, a later failed run may prove only
// some, and a record from an older access basis counts for nothing.
func TestLedgerVerificationFactsUsesCurrentL2PointEvidence(t *testing.T) {
	registry := profile.Default()
	prof, ok := registry.Get("enjo-pcs")
	if !ok {
		t.Fatal("enjo-pcs profile missing")
	}
	required := domainaccess.RequiredPointSpecs(prof)
	if len(required) < 2 {
		t.Fatalf("required points = %d, need at least 2", len(required))
	}
	modelText, err := json.Marshal(unitModelConfig{ProfileID: "enjo-pcs"})
	if err != nil {
		t.Fatal(err)
	}
	points := []unitModelPointView{}
	channel := data.NodeEdge{ID: "bus-1", Type: data.NodeTypeModbus, Points: data.Points{
		data.NewPointString(unitModelPoint, "1", string(modelText)),
	}}
	basis := accessBasisFromPoints(channel, 1, points, registry)
	if basis == "" {
		t.Fatal("empty access basis")
	}
	withHistory := func(records []domainaccess.VerificationRecord) data.NodeEdge {
		t.Helper()
		raw, err := json.Marshal(records)
		if err != nil {
			t.Fatal(err)
		}
		node := channel
		node.Points = append(append(data.Points{}, channel.Points...), data.NewPointString(domainaccess.VerificationPoint, "1", string(raw)))
		return node
	}

	if verified, at := ledgerVerificationFacts(channel, 1, required, points, registry); verified != 0 || at != "" {
		t.Fatalf("no L2 records = %d/%q", verified, at)
	}
	l1Only := withHistory([]domainaccess.VerificationRecord{{Level: "L1", At: "l1-time", Basis: basis, Passed: true}})
	if verified, at := ledgerVerificationFacts(l1Only, 1, required, points, registry); verified != 0 || at != "" {
		t.Fatalf("L1 only = %d/%q", verified, at)
	}
	passedAt := "2026-11-26T10:00:00Z"
	fullPass := withHistory([]domainaccess.VerificationRecord{{Level: "L2", At: passedAt, Basis: basis, Passed: true}})
	if verified, at := ledgerVerificationFacts(fullPass, 1, required, points, registry); verified != len(required) || at != passedAt {
		t.Fatalf("full pass = %d/%q", verified, at)
	}
	partial := []domainaccess.VerificationRecord{
		{Level: "L2", At: passedAt, Basis: basis, Passed: true},
		{Level: "L2", At: "failed-time", Basis: basis, Passed: false, Points: []domainaccess.PointVerification{
			{Name: required[0].Name, Passed: true},
			{Name: required[1].Name, Passed: false},
		}},
	}
	if verified, at := ledgerVerificationFacts(withHistory(partial), 1, required, points, registry); verified != 1 || at != "" {
		t.Fatalf("partial failure = %d/%q", verified, at)
	}
	staleBasis := append(append([]domainaccess.VerificationRecord{}, partial...), domainaccess.VerificationRecord{Level: "L2", At: "stale-time", Basis: "older-basis", Passed: true})
	if verified, at := ledgerVerificationFacts(withHistory(staleBasis), 1, required, points, registry); verified != 1 || at != "" {
		t.Fatalf("stale basis = %d/%q", verified, at)
	}
}
