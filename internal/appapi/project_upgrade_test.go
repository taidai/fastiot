package appapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/simpleiot/simpleiot/api"
	siotclient "github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
	"github.com/simpleiot/simpleiot/server"

	"fastiot/internal/audit"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/emsapi"
	"fastiot/internal/industry"
)

// 升级对照：旧基线（冻结版本）、现场定制（当前配置）、新模板（项目包）三方比较；
// 冲突由人员逐项选择，未解决不写入；新模板不再具备解释某个运行参数的能力时阻止升级。

type upgradeFixture struct {
	handler *Handler
	nc      *nats.Conn
	rootID  string
	admin   Identity
	siteID  string
	plantID string
}

func startUpgradeFixture(t *testing.T, port string) *upgradeFixture {
	t.Helper()
	dataDir := filepath.Join(t.TempDir(), "data")
	var users api.UserAuthority
	number, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	natsPort := strconv.Itoa(number + 1)
	options := server.Options{
		ID: "easyems-upgrade", DataDir: dataDir, HTTPPort: port,
		NatsPort: number + 1, NatsServer: "nats://127.0.0.1:" + natsPort, AuthToken: data.NewID(),
		DeviceAuth: server.DeviceAuthRequired, CustomUIFS: fstest.MapFS{"index.html": {Data: []byte("easyems")}},
		AppOnly: true, NatsLoopback: true, ProvisioningDisable: true,
		AppHandlerFactory: func(nc *nats.Conn, u api.UserAuthority) http.Handler {
			users = u
			return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
		},
	}
	siot, nc, err := server.NewServer(options)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- siot.Run() }()
	t.Cleanup(func() { siot.Stop(nil); <-done })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	if err := siot.WaitStart(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	var root data.NodeEdge
	for {
		root, err = siotclient.GetRootNode(nc)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("store not ready")
		}
		time.Sleep(50 * time.Millisecond)
	}

	siteID, err := CreateDomainNode(nc, root.ID, NodeTypeSite, map[string]string{"description": "升级站"})
	if err != nil {
		t.Fatal(err)
	}
	plantID, err := CreateDomainNode(nc, siteID, NodeTypePlant, map[string]string{
		"description": "A 电站", PointTypeAssetCode: "PLANT-A",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateDomainNode(nc, plantID, "variable", map[string]string{"description": "计量点"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)

	handler := New(nc, users, nil)
	handler.SetDataDir(dataDir)
	handler.SetAudit(audit.New(filepath.Join(dataDir, "audit.jsonl")))
	handler.SetEMS(emsapi.New(runtime.NewService(nil, 15*time.Second, 5*time.Second, 100), 15*time.Second))
	return &upgradeFixture{
		handler: handler, nc: nc, rootID: root.ID, siteID: siteID, plantID: plantID,
		admin: Identity{UserID: "admin", Policy: AccessPolicy{Admin: true}, Anchors: []string{root.ID}},
	}
}

func (f *upgradeFixture) sendPoints(t *testing.T, nodeID string, points ...data.Point) {
	t.Helper()
	if err := siotclient.SendPoints(f.nc, siotclient.SubjectNodePoints(nodeID), points, true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
}

func (f *upgradeFixture) publish(t *testing.T, note string) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	f.handler.publishHandler(recorder, f.admin, f.siteID, note, true)
	if recorder.Code != 200 {
		t.Fatalf("publish = %d %s", recorder.Code, recorder.Body.String())
	}
	var published versionSummary
	if err := json.Unmarshal(recorder.Body.Bytes(), &published); err != nil {
		t.Fatal(err)
	}
	return published.ID
}

func (f *upgradeFixture) preview(t *testing.T, site, base string, pkg projectPackage) (int, upgradePreview, string) {
	t.Helper()
	raw, err := json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(upgradeRequest{Site: site, Base: base, Package: raw})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	httpRequest := httptest.NewRequest(http.MethodPost, "/api/projects/upgrade/preview", bytes.NewReader(encoded))
	f.handler.upgradePreviewHandler(recorder, httpRequest, f.admin)
	var preview upgradePreview
	_ = json.Unmarshal(recorder.Body.Bytes(), &preview)
	return recorder.Code, preview, recorder.Body.String()
}

func (f *upgradeFixture) apply(t *testing.T, site, base string, pkg projectPackage, resolutions map[string]string) (int, string) {
	t.Helper()
	raw, err := json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(upgradeRequest{Site: site, Base: base, Package: raw, Resolutions: resolutions})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	httpRequest := httptest.NewRequest(http.MethodPost, "/api/projects/upgrade", bytes.NewReader(encoded))
	f.handler.upgradeApplyHandler(recorder, httpRequest, f.admin)
	return recorder.Code, recorder.Body.String()
}

func (f *upgradeFixture) node(t *testing.T, id string) data.NodeEdge {
	t.Helper()
	nodes, err := siotclient.GetNodes(f.nc, "all", id, "", false)
	if err != nil || len(nodes) == 0 {
		t.Fatalf("load node %s: %v", id, err)
	}
	return nodes[0]
}

func (f *upgradeFixture) plantPoints(t *testing.T) map[string]string {
	t.Helper()
	node := f.node(t, f.plantID)
	values := map[string]string{}
	for _, point := range node.Points {
		if point.Tombstone%2 != 0 {
			continue
		}
		values[point.Type] = point.Txt()
	}
	return values
}

func (f *upgradeFixture) childByName(t *testing.T, parent, name string) (data.NodeEdge, bool) {
	t.Helper()
	children, err := siotclient.GetNodes(f.nc, parent, "all", "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, child := range children {
		if child.Desc() == name {
			return child, true
		}
	}
	return data.NodeEdge{}, false
}

func upgradeTestTemplate(nodes []packageNode, dependencies []packageDependency) projectPackage {
	return projectPackage{
		Format: projectPackageFormat, FormatVersion: projectPackageFormatVersion,
		ID: "pp-upgrade", Name: "升级站", Version: 2, CreatedAt: time.Now().UTC(),
		Operator: "交付", Dependencies: dependencies, Nodes: nodes,
		Stripped: []packageStrip{}, Warnings: []string{},
	}
}

func sitePoint(typ, value string) data.Points {
	return data.Points{data.NewPointString(typ, "", value)}
}

func emsDependency(version string) []packageDependency {
	return []packageDependency{{Kind: dependencyKindIndustry, ID: industry.EMSPackageID, Version: version}}
}

func findNodeDiff(t *testing.T, preview upgradePreview, name string) upgradeNodeDiff {
	t.Helper()
	for _, node := range preview.Nodes {
		if node.Name == name {
			return node
		}
	}
	t.Fatalf("diff has no node %q: %+v", name, preview.Nodes)
	return upgradeNodeDiff{}
}

func findPointDiff(t *testing.T, node upgradeNodeDiff, pointType string) upgradePointDiff {
	t.Helper()
	for _, point := range node.Points {
		if point.Point == pointType {
			return point
		}
	}
	t.Fatalf("node %q has no point diff for %q: %+v", node.Name, pointType, node.Points)
	return upgradePointDiff{}
}

func TestProjectUpgradeShowsBaselineSiteAndTemplateValues(t *testing.T) {
	fixture := startUpgradeFixture(t, "9860")
	baseline := fixture.publish(t, "基线 A")

	// 现场定制 B：改电站资产号，并新增一个现场自有变量。
	fixture.sendPoints(t, fixture.plantID, data.NewPointString(PointTypeAssetCode, "", "PLANT-B"))
	if _, err := CreateDomainNode(fixture.nc, fixture.plantID, "variable", map[string]string{"description": "现场新增"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)

	// 新模板 C：同一处资产号改成另一个值，新增区域字段，并带来一个新变量。
	template := upgradeTestTemplate([]packageNode{
		{Ref: "n1", Type: NodeTypeSite, Points: sitePoint("description", "升级站")},
		{Ref: "n2", Type: NodeTypePlant, Parent: "n1", Points: data.Points{
			data.NewPointString("description", "", "A 电站"),
			data.NewPointString(PointTypeAssetCode, "", "PLANT-C"),
			data.NewPointString("region", "", "华东"),
		}},
		{Ref: "n3", Type: "variable", Parent: "n2", Points: sitePoint("description", "计量点")},
		{Ref: "n4", Type: "variable", Parent: "n2", Points: sitePoint("description", "模板新增")},
	}, emsDependency(industry.EMSPackageVersion))

	status, preview, body := fixture.preview(t, fixture.siteID, baseline, template)
	if status != 200 {
		t.Fatalf("preview = %d %s", status, body)
	}
	if preview.Baseline.Version != baseline {
		t.Fatalf("preview baseline = %q, want %q", preview.Baseline.Version, baseline)
	}
	if preview.Template.ID != template.ID || preview.Template.Name != "升级站" {
		t.Fatalf("preview template = %+v", preview.Template)
	}

	plant := findNodeDiff(t, preview, "A 电站")
	asset := findPointDiff(t, plant, PointTypeAssetCode)
	if asset.Status != upgradePointConflict || asset.Base != "PLANT-A" || asset.Local != "PLANT-B" || asset.Template != "PLANT-C" {
		t.Fatalf("three-way asset diff = %+v", asset)
	}
	if asset.Conflict == "" {
		t.Fatal("conflicting point carries no conflict id")
	}
	region := findPointDiff(t, plant, "region")
	if region.Status != upgradePointTemplateAdd || region.Template != "华东" {
		t.Fatalf("region diff = %+v", region)
	}

	if node := findNodeDiff(t, preview, "模板新增"); node.Status != upgradeNodeAddedByTemplate {
		t.Fatalf("template node = %+v", node)
	}
	if node := findNodeDiff(t, preview, "现场新增"); node.Status != upgradeNodeSiteOnly {
		t.Fatalf("site-only node = %+v", node)
	}
	if len(preview.Conflicts) != 1 || preview.Conflicts[0].Reason != upgradeConflictBothChanged {
		t.Fatalf("conflicts = %+v", preview.Conflicts)
	}
	if !preview.Blocked || len(preview.Blockers) == 0 || preview.Summary.Conflicts != 1 {
		t.Fatalf("preview not blocked by the unresolved conflict: %+v", preview)
	}
}

func TestProjectUpgradeRefusesUnresolvedConflictUntilSomeoneChooses(t *testing.T) {
	fixture := startUpgradeFixture(t, "9862")
	baseline := fixture.publish(t, "基线 A")
	fixture.sendPoints(t, fixture.plantID, data.NewPointString(PointTypeAssetCode, "", "PLANT-B"))

	template := upgradeTestTemplate([]packageNode{
		{Ref: "n1", Type: NodeTypeSite, Points: sitePoint("description", "升级站")},
		{Ref: "n2", Type: NodeTypePlant, Parent: "n1", Points: data.Points{
			data.NewPointString("description", "", "A 电站"),
			data.NewPointString(PointTypeAssetCode, "", "PLANT-C"),
		}},
		{Ref: "n3", Type: "variable", Parent: "n2", Points: data.Points{
			data.NewPointString("description", "", "计量点"),
			data.NewPointFloat("value", "", 42),
		}},
	}, emsDependency(industry.EMSPackageVersion))

	_, preview, _ := fixture.preview(t, fixture.siteID, baseline, template)
	conflictID := preview.Conflicts[0].ID

	status, body := fixture.apply(t, fixture.siteID, baseline, template, nil)
	if status != http.StatusConflict || !strings.Contains(body, "upgrade_blocked") {
		t.Fatalf("apply with unresolved conflict = %d %s", status, body)
	}
	if value := fixture.plantPoints(t)["assetCode"]; value != "PLANT-B" {
		t.Fatalf("blocked upgrade changed the live value: %q", value)
	}
	meter, found := fixture.childByName(t, fixture.plantID, "计量点")
	if !found {
		t.Fatal("blocked upgrade removed the existing meter node")
	}
	if _, has := meter.Points.Find("value", ""); has {
		t.Fatal("blocked upgrade wrote a runtime value into the live project")
	}

	writes, err := fixture.nc.SubscribeSync(siotclient.SubjectNodePoints(fixture.plantID) + ".assetCode.0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writes.Unsubscribe() })
	if err := fixture.nc.Flush(); err != nil {
		t.Fatal(err)
	}
	status, body = fixture.apply(t, fixture.siteID, baseline, template, map[string]string{conflictID: upgradeResolutionTemplate})
	if status != 200 {
		t.Fatalf("resolved apply = %d %s", status, body)
	}
	write, err := writes.NextMsg(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if write.Reply == "" {
		t.Fatal("upgrade configuration write must request storage acknowledgement before freezing its version")
	}
	var result upgradeResult
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatal(err)
	}
	if value := fixture.plantPoints(t)["assetCode"]; value != "PLANT-C" {
		t.Fatalf("resolved upgrade did not take the template value: %q", value)
	}
	if result.Before == "" || result.Version == "" || result.Before == result.Version {
		t.Fatalf("upgrade did not freeze before/after versions: %+v", result)
	}
	versions, err := fixture.handler.listVersions(fixture.siteID)
	if err != nil || len(versions) < 3 {
		t.Fatalf("versions after upgrade = %d, %v", len(versions), err)
	}
	frozen, err := fixture.handler.loadVersion(fixture.siteID, result.Version)
	if err != nil {
		t.Fatal(err)
	}
	frozenAsset := ""
	for _, node := range frozen.Nodes {
		if node.ID == fixture.plantID {
			if point, found := node.Points.Find(PointTypeAssetCode, ""); found {
				frozenAsset = point.Txt()
			}
		}
	}
	if frozenAsset != "PLANT-C" {
		t.Fatalf("new baseline froze stale configuration: %q", frozenAsset)
	}
	meter, found = fixture.childByName(t, fixture.plantID, "计量点")
	if !found {
		t.Fatal("upgrade removed the existing meter node")
	}
	if _, has := meter.Points.Find("value", ""); has {
		t.Fatal("upgrade wrote the template's runtime value point into the live project")
	}
	records, err := fixture.handler.audit.Query(audit.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	upgraded := false
	for _, record := range records {
		if record.Action == projectUpgradeAction {
			upgraded = true
		}
	}
	if !upgraded {
		t.Fatal("upgrade is not in the audit log")
	}
}

func TestProjectUpgradeKeepsSiteChoiceWhenSomeoneKeepsTheFieldValue(t *testing.T) {
	fixture := startUpgradeFixture(t, "9864")
	baseline := fixture.publish(t, "基线 A")
	fixture.sendPoints(t, fixture.plantID, data.NewPointString(PointTypeAssetCode, "", "PLANT-B"))
	template := upgradeTestTemplate([]packageNode{
		{Ref: "n1", Type: NodeTypeSite, Points: sitePoint("description", "升级站")},
		{Ref: "n2", Type: NodeTypePlant, Parent: "n1", Points: data.Points{
			data.NewPointString("description", "", "A 电站"),
			data.NewPointString(PointTypeAssetCode, "", "PLANT-C"),
		}},
	}, emsDependency(industry.EMSPackageVersion))

	_, preview, _ := fixture.preview(t, fixture.siteID, baseline, template)
	status, body := fixture.apply(t, fixture.siteID, baseline, template, map[string]string{preview.Conflicts[0].ID: upgradeResolutionLocal})
	if status != 200 {
		t.Fatalf("apply keeping the field value = %d %s", status, body)
	}
	if value := fixture.plantPoints(t)["assetCode"]; value != "PLANT-B" {
		t.Fatalf("site choice was overwritten: %q", value)
	}
}

func TestProjectUpgradeBlocksIncompatibleRuntimeParameter(t *testing.T) {
	fixture := startUpgradeFixture(t, "9866")
	baseline := fixture.publish(t, "基线 A")

	slots := make([]*float64, 96)
	value := 120.0
	slots[0] = &value
	plan, _ := json.Marshal(plan96{Version: 1, Slots: slots})
	fixture.sendPoints(t, fixture.siteID,
		data.NewPointString(plan96Point, "", string(plan)),
		data.NewPointFloat(PointTypeSiteTarget, "", 80))

	// 新模板是无 EMS 的通用底座模板：不再具备解释 96 点计划的能力。
	template := upgradeTestTemplate([]packageNode{
		{Ref: "n1", Type: NodeTypeSite, Points: sitePoint("description", "升级站")},
		{Ref: "n2", Type: "variable", Parent: "n1", Points: sitePoint("description", "通用变量")},
	}, nil)

	status, preview, body := fixture.preview(t, fixture.siteID, baseline, template)
	if status != 200 {
		t.Fatalf("preview = %d %s", status, body)
	}
	seen := map[string]bool{}
	for _, check := range preview.Incompatible {
		if check.Kind != upgradeIncompatibleRuntime {
			continue
		}
		if strings.Contains(check.Detail, plan96Point) {
			seen[plan96Point] = true
		}
		if strings.Contains(check.Detail, PointTypeSiteTarget) {
			seen[PointTypeSiteTarget] = true
		}
	}
	if !seen[plan96Point] || !seen[PointTypeSiteTarget] || !preview.Blocked {
		t.Fatalf("runtime parameters were not reported as incompatible: %+v", preview.Incompatible)
	}

	status, body = fixture.apply(t, fixture.siteID, baseline, template, nil)
	if status != http.StatusConflict || !strings.Contains(body, "runtime_parameter_incompatible") {
		t.Fatalf("apply with a live plan = %d %s", status, body)
	}
	if _, ok := fixture.node(t, fixture.siteID).Points.Find(plan96Point, ""); !ok {
		t.Fatal("blocked upgrade removed the live plan")
	}

	// 清除两个运行参数后同一个模板可以升级；升级不改写现场正在使用的值。
	cleared := plan96{Version: 1, Slots: make([]*float64, 96)}
	encoded, _ := json.Marshal(cleared)
	fixture.sendPoints(t, fixture.siteID,
		data.NewPointString(plan96Point, "", string(encoded)),
		data.NewPointFloat(PointTypeSiteTarget, "", 0))
	status, body = fixture.apply(t, fixture.siteID, baseline, template, nil)
	if status != 200 {
		t.Fatalf("apply after clearing the plan = %d %s", status, body)
	}
	point, ok := fixture.node(t, fixture.siteID).Points.Find(plan96Point, "")
	if !ok || point.Txt() != string(encoded) {
		t.Fatalf("upgrade rewrote the runtime plan point: %q", point.Txt())
	}
	fixture.node(t, fixture.plantID)
}

func TestProjectUpgradeBlocksDependencyTheTemplatePinsToAnotherVersion(t *testing.T) {
	fixture := startUpgradeFixture(t, "9868")
	baseline := fixture.publish(t, "基线 A")
	template := upgradeTestTemplate([]packageNode{
		{Ref: "n1", Type: NodeTypeSite, Points: sitePoint("description", "升级站")},
		{Ref: "n2", Type: NodeTypePlant, Parent: "n1", Points: sitePoint("description", "A 电站")},
	}, emsDependency("9.9.9"))

	_, preview, _ := fixture.preview(t, fixture.siteID, baseline, template)
	found := false
	for _, check := range preview.Incompatible {
		if check.Kind == upgradeIncompatibleDependency && strings.Contains(check.Detail, "incompatible_dependency:industry:ems") {
			found = true
		}
	}
	if !found || !preview.Blocked {
		t.Fatalf("dependency mismatch was not reported: %+v", preview.Incompatible)
	}
	status, body := fixture.apply(t, fixture.siteID, baseline, template, nil)
	if status != http.StatusConflict || !strings.Contains(body, "dependency_incompatible") {
		t.Fatalf("apply with an unsatisfiable dependency = %d %s", status, body)
	}
}

func TestProjectUpgradeNeedsAFrozenBaseline(t *testing.T) {
	fixture := startUpgradeFixture(t, "9870")
	template := upgradeTestTemplate([]packageNode{
		{Ref: "n1", Type: NodeTypeSite, Points: sitePoint("description", "升级站")},
	}, emsDependency(industry.EMSPackageVersion))

	status, _, body := fixture.preview(t, fixture.siteID, "", template)
	if status != http.StatusBadRequest || !strings.Contains(body, "baseline_required") {
		t.Fatalf("preview without a baseline = %d %s", status, body)
	}
}

func TestProjectUpgradeRefusesObjectsWithTheirOwnLifecycle(t *testing.T) {
	fixture := startUpgradeFixture(t, "9872")
	fixture.publish(t, "基线 A")
	if err := siotclient.SendNode(fixture.nc, data.NodeEdge{
		ID: data.NewID(), Type: virtualInputType, Parent: fixture.plantID,
		Points: data.Points{data.NewPointString("description", "", "虚拟输入")},
	}, "test"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)

	template := upgradeTestTemplate([]packageNode{
		{Ref: "n1", Type: NodeTypeSite, Points: sitePoint("description", "升级站")},
	}, emsDependency(industry.EMSPackageVersion))
	status, _, body := fixture.preview(t, fixture.siteID, "", template)
	if status != http.StatusConflict || !strings.Contains(body, "virtual_input_upgrade_unsupported") {
		t.Fatalf("preview with a virtual input = %d %s", status, body)
	}
}

func TestProjectUpgradeRefusesTemplateForAnotherSite(t *testing.T) {
	fixture := startUpgradeFixture(t, "9874")
	baseline := fixture.publish(t, "基线 A")
	template := upgradeTestTemplate([]packageNode{
		{Ref: "n1", Type: NodeTypeSite, Points: sitePoint("description", "另一个站")},
	}, emsDependency(industry.EMSPackageVersion))
	status, _, body := fixture.preview(t, fixture.siteID, baseline, template)
	if status != http.StatusBadRequest || !strings.Contains(body, "template_site_unmatched") {
		t.Fatalf("preview with another site's template = %d %s", status, body)
	}
}
