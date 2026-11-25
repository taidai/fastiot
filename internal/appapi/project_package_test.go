package appapi

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	siotclient "github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/decision"
	"fastiot/internal/ems/profile"
	"fastiot/internal/emsapi"
	"fastiot/internal/industry"
)

func TestPackagePointRewrite(t *testing.T) {
	idMap := map[string]string{"old-unit": "new-unit", "old-io": "new-io"}
	node := data.NodeEdge{ID: "device", Type: data.NodeTypeDevice, Points: data.Points{
		data.NewPointString(PointTypePowerSetpoint, "", "old-io"),
		data.NewPointString("description", "", "PCS"),
	}}
	points := packagePoints(node, idMap, new([]packageStrip), new([]string))
	if len(points) != 2 {
		t.Fatalf("points = %d, want 2", len(points))
	}
	if points[0].Type != PointTypePowerSetpoint || points[0].Txt() != "new-io" {
		t.Fatalf("setpoint = %#v, want new-io", points[0])
	}

	rule := data.NodeEdge{ID: "unit", Type: NodeTypeEnergyUnit, Points: data.Points{
		data.NewPointString(capabilityRulePoint, "", `{"id":"r1","state":"published","conditions":[{"ref":{"energy_unit_id":"old-unit","field":"rated_power"},"operator":"gt","value":1}],"actions":[{"energy_unit_id":"old-unit","request_kw":20}]}`),
	}}
	points = packagePoints(rule, idMap, new([]packageStrip), new([]string))
	var capability decision.CapabilityRule
	if err := json.Unmarshal([]byte(points[0].Txt()), &capability); err != nil {
		t.Fatal(err)
	}
	if capability.State != decision.RuleDraft {
		t.Fatalf("capability state = %q, want draft (control start is not carried)", capability.State)
	}
	if capability.Conditions[0].Ref.EnergyUnitID != "new-unit" || capability.Actions[0].EnergyUnitID != "new-unit" {
		t.Fatalf("capability refs = %#v", capability)
	}

	// 引用包外对象的点必须被移除，不能保留旧 ID 冒充可用。
	outside := data.NodeEdge{ID: "device", Type: data.NodeTypeDevice, Points: data.Points{
		data.NewPointString(PointTypePowerSetpoint, "", "ghost"),
	}}
	if points := packagePoints(outside, idMap, new([]packageStrip), new([]string)); len(points) != 0 {
		t.Fatalf("external reference kept: %#v", points)
	}
}

func TestPackageStateMachineRewrite(t *testing.T) {
	idMap := map[string]string{"old-unit": "new-unit", "old-io": "new-io"}
	document := decision.StateMachineDocument{
		Schema:   1,
		Revision: 3,
		Draft: decision.StateMachineDefinition{
			InitialState: "stopped",
			Scope:        "self",
			States:       []string{"stopped", "running"},
			Bindings: decision.BindingSet{
				Points:  map[string]decision.Target{"p1": {NodeID: "old-unit", PointType: data.PointTypeValue}},
				Actions: map[string]decision.Target{"a1": {NodeID: "old-io", PointType: data.PointTypeValueSet}},
			},
		},
		CurrentState:  "running",
		LastExecution: &decision.StateMachineExecution{At: time.Now(), Event: "start", Status: "confirmed"},
	}
	encoded, _ := json.Marshal(document)
	node := data.NodeEdge{ID: "sm", Type: stateMachineType, Points: data.Points{
		data.NewPointString(stateMachineDocumentPoint, "", string(encoded)),
	}}
	points := packagePoints(node, idMap, new([]packageStrip), new([]string))
	if len(points) != 1 {
		t.Fatalf("points = %d, want 1", len(points))
	}
	var rewritten decision.StateMachineDocument
	if err := json.Unmarshal([]byte(points[0].Txt()), &rewritten); err != nil {
		t.Fatal(err)
	}
	if rewritten.Draft.Bindings.Points["p1"].NodeID != "new-unit" {
		t.Fatalf("draft point binding = %q, want new-unit", rewritten.Draft.Bindings.Points["p1"].NodeID)
	}
	if rewritten.Draft.Bindings.Actions["a1"].NodeID != "new-io" {
		t.Fatalf("draft action binding = %q, want new-io", rewritten.Draft.Bindings.Actions["a1"].NodeID)
	}
	if rewritten.CurrentState != "stopped" {
		t.Fatalf("current state = %q, want reset to initial stopped", rewritten.CurrentState)
	}
	if rewritten.LastExecution != nil {
		t.Fatalf("last execution not stripped: %#v", rewritten.LastExecution)
	}
}

func TestPackageStateMachineReferences(t *testing.T) {
	document := decision.StateMachineDocument{
		Draft: decision.StateMachineDefinition{
			InitialState: "stopped",
			States:       []string{"stopped"},
			Bindings: decision.BindingSet{
				Points: map[string]decision.Target{"p1": {NodeID: "unit-1"}},
			},
		},
		Applied: &decision.StateMachineDefinition{
			InitialState: "stopped",
			States:       []string{"stopped"},
			Bindings: decision.BindingSet{
				Points: map[string]decision.Target{"p1": {NodeID: "unit-2"}},
			},
		},
	}
	encoded, _ := json.Marshal(document)
	node := data.NodeEdge{ID: "sm", Type: stateMachineType}
	refs := packageReferences(node, data.NewPointString(stateMachineDocumentPoint, "", string(encoded)))
	if !slices.Contains(refs, "unit-1") || !slices.Contains(refs, "unit-2") {
		t.Fatalf("state machine references = %v, want unit-1 and unit-2", refs)
	}
}

func TestPackageContentCounts(t *testing.T) {
	nodes := []packageNode{
		{Ref: "n1", Type: NodeTypeSite, Points: data.Points{data.NewPointString("description", "", "站点")}},
		{Ref: "n2", Type: data.NodeTypeModbusIO, Parent: "n1"},
		{Ref: "n3", Type: "decisionTable", Parent: "n1"},
		{Ref: "n4", Type: "stateMachine", Parent: "n1"},
		{Ref: "n5", Type: NodeTypeEnergyUnit, Parent: "n1", Points: data.Points{
			data.NewPointString(capabilityRulePoint, "", `{"id":"capability"}`),
		}},
	}
	points, strategies := packageContentCounts(nodes)
	if points != 1 || strategies != 3 {
		t.Fatalf("counts = %d points, %d strategies; want 1 and 3", points, strategies)
	}
}

func TestProjectPackageValidation(t *testing.T) {
	valid := projectPackage{
		Format: projectPackageFormat, FormatVersion: projectPackageFormatVersion,
		Nodes: []packageNode{{Ref: "n1", Type: NodeTypeSite}},
	}
	if err := valid.validate(); err != nil {
		t.Fatalf("valid package rejected: %v", err)
	}
	cases := []struct {
		name string
		pkg  projectPackage
		want string
	}{
		{"format", projectPackage{Format: "other", FormatVersion: projectPackageFormatVersion, Nodes: valid.Nodes}, "invalid_package_format"},
		{"version", projectPackage{Format: projectPackageFormat, FormatVersion: 99, Nodes: valid.Nodes}, "unsupported_package_version"},
		{"empty", projectPackage{Format: projectPackageFormat, FormatVersion: projectPackageFormatVersion}, "invalid_package_structure"},
		{"missing_parent", projectPackage{Format: projectPackageFormat, FormatVersion: projectPackageFormatVersion, Nodes: []packageNode{
			{Ref: "n1", Type: NodeTypeSite}, {Ref: "n2", Type: NodeTypePlant, Parent: "ghost"},
		}}, "invalid_package_structure"},
		{"duplicate_ref", projectPackage{Format: projectPackageFormat, FormatVersion: projectPackageFormatVersion, Nodes: []packageNode{
			{Ref: "n1", Type: NodeTypeSite}, {Ref: "n1", Type: NodeTypePlant, Parent: "n1"},
		}}, "invalid_package_structure"},
		{"credential_node", projectPackage{Format: projectPackageFormat, FormatVersion: projectPackageFormatVersion, Nodes: []packageNode{
			{Ref: "n1", Type: NodeTypeSite}, {Ref: "n2", Type: "authToken", Parent: "n1"},
		}}, "invalid_package_structure"},
		// 依赖必须带确定版本，未知种类和重复声明都在结构检查阶段拒绝。
		{"dependency_kind", projectPackage{Format: projectPackageFormat, FormatVersion: projectPackageFormatVersion, Nodes: valid.Nodes,
			Dependencies: []packageDependency{{Kind: "plugin", ID: "x", Version: "1.0.0"}}}, "invalid_package_structure"},
		{"dependency_version", projectPackage{Format: projectPackageFormat, FormatVersion: projectPackageFormatVersion, Nodes: valid.Nodes,
			Dependencies: []packageDependency{{Kind: dependencyKindProfile, ID: "x"}}}, "invalid_package_structure"},
		{"dependency_duplicate", projectPackage{Format: projectPackageFormat, FormatVersion: projectPackageFormatVersion, Nodes: valid.Nodes,
			Dependencies: []packageDependency{
				{Kind: dependencyKindProfile, ID: "x", Version: "a"}, {Kind: dependencyKindProfile, ID: "x", Version: "a"},
			}}, "invalid_package_structure"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := test.pkg.validate()
			if err == nil || !strings.HasPrefix(err.Error(), test.want) {
				t.Fatalf("validate = %v, want %s", err, test.want)
			}
		})
	}
}

// TestProjectPackageRoundTrip 证明单一资产闭环：源实例导出 → 干净实例导入 →
// 模型/适配/规则可用，且包内无旧地址、凭据与控制授权。
func TestProjectPackageRoundTrip(t *testing.T) {
	baseA, tokenA, handlerA, _ := startInputServer(t, t.TempDir(), data.NewID())
	handlerA.SetEMS(&emsapi.Handler{Profiles: profile.Default()})
	baseB, tokenB, handlerB, _ := startInputServer(t, t.TempDir(), data.NewID())
	handlerB.SetEMS(&emsapi.Handler{Profiles: profile.Default()})

	rootA := snapshotRoot(t, baseA, tokenA)
	siteA, err := CreateDomainNode(handlerA.nc, rootA, NodeTypeSite, map[string]string{"description": "一期工程"})
	if err != nil {
		t.Fatal(err)
	}
	plantA, _ := CreateDomainNode(handlerA.nc, siteA, NodeTypePlant, map[string]string{"description": "A 电站", PointTypeAssetCode: "P-A"})
	unitA, _ := CreateDomainNode(handlerA.nc, plantA, NodeTypeEnergyUnit, map[string]string{
		"description": "1 号储能柜", PointTypeAssetCode: "EU-A",
		PointTypeRatedPowerKW: "250", PointTypeEnergyType: EnergyTypeStorage,
	})
	channelA, _ := CreateDomainNode(handlerA.nc, rootA, data.NodeTypeModbus, map[string]string{
		"description": "一号总线", "uri": "tcp://10.20.30.40:502", "port": "502", "baud": "9600",
	})
	ioA, _ := CreateDomainNode(handlerA.nc, channelA, data.NodeTypeModbusIO, map[string]string{
		"description": "有功功率设定", "id": "1", "address": "10", "scale": "0.1", "units": "kW",
	})
	_, _ = CreateDomainNode(handlerA.nc, unitA, data.NodeTypeDevice, map[string]string{
		"description": "1 号 PCS", PointTypeAssetCode: "PCS-A",
		PointTypeRatedPowerKW: "100", PointTypePowerSetpoint: ioA,
	})
	// 无关的第二条总线不能随项目包漂移。
	_, _ = CreateDomainNode(handlerA.nc, rootA, data.NodeTypeModbus, map[string]string{"description": "二号总线"})

	modelJSON, _ := json.Marshal(unitModelConfig{
		Name: "恩玖 PCS", ProfileID: "enjo-pcs", BusinessNodeID: unitA,
		PointMap: map[string]string{"有功功率": ioA}, Verified: true,
		Verifier: "old-site-engineer", DeviceSerial: "SN-OLD-001",
	})
	modelPoint := data.NewPointString(unitModelPoint, "1", string(modelJSON))
	modelPoint.Origin = "easyems"
	if err := siotclient.SendNodePoint(handlerA.nc, channelA, modelPoint, true); err != nil {
		t.Fatal(err)
	}

	pcc, _ := json.Marshal(decision.PCCFeedback{MeterPointID: ioA})
	if err := siotclient.SendPoints(handlerA.nc, siotclient.SubjectNodePoints(plantA), data.Points{
		data.NewPointString(PointTypePCCFeedback, "0", string(pcc)),
	}, false); err != nil {
		t.Fatal(err)
	}

	capability := decision.CapabilityRule{
		ID: "cap-1", State: decision.RulePublished,
		Conditions: []decision.CapabilityCondition{{
			Ref: decision.CapabilityRef{EnergyUnitID: unitA, Field: decision.FieldRatedPower}, Operator: "gt", Value: 100,
		}},
		Actions: []decision.CapabilityAction{{EnergyUnitID: unitA, RequestKW: 150}},
	}
	capabilityText, _ := json.Marshal(capability)
	if err := siotclient.SendPoints(handlerA.nc, siotclient.SubjectNodePoints(unitA), data.Points{
		data.NewPointString(capabilityRulePoint, "", string(capabilityText)),
	}, false); err != nil {
		t.Fatal(err)
	}

	ruleA, _ := CreateDomainNode(handlerA.nc, siteA, "decisionTable", map[string]string{"description": "放电规则"})
	document := decision.Document{
		Table: decision.Table{Rows: []decision.Row{{
			ID:         "r1",
			Conditions: []decision.Condition{{PointID: "p1", Operator: "gt", Value: 0}},
			Actions:    []decision.Action{{ActionID: "a1", Value: 1}},
		}}},
		Bindings: decision.BindingSet{
			Points:  map[string]decision.Target{"p1": {NodeID: unitA, PointType: PointTypeRatedPowerKW}},
			Actions: map[string]decision.Target{"a1": {NodeID: unitA, PointType: data.PointTypeValue}},
		},
	}
	applied, _ := json.Marshal(document)
	if err := siotclient.SendPoints(handlerA.nc, siotclient.SubjectNodePoints(ruleA), data.Points{
		data.NewPointString("applied", "0", string(applied)),
		data.NewPointFloat("enabled", "", 1),
	}, false); err != nil {
		t.Fatal(err)
	}
	// 旧站凭据与运行目标不得进入项目包。
	if err := siotclient.SendPoints(handlerA.nc, siotclient.SubjectNodePoints(siteA), data.Points{
		data.NewPointString("apiToken", "", "secret-value"),
		data.NewPointFloat(PointTypeSiteTarget, "", 50),
	}, false); err != nil {
		t.Fatal(err)
	}
	waitForNodePoints(t, handlerA.nc, siteA, "apiToken")
	waitForNodePoints(t, handlerA.nc, channelA, unitModelPoint)

	var exported projectPackage
	status, body := call(t, http.MethodGet, baseA+"/projects/export?site="+siteA, tokenA, nil, &exported)
	if status != http.StatusOK {
		t.Fatalf("export: %d %s", status, body)
	}
	if exported.Format != projectPackageFormat || exported.FormatVersion != projectPackageFormatVersion || exported.Version < 1 {
		t.Fatalf("package header = %#v", exported)
	}
	enjoVersion, fingerprinted := profile.Default().Fingerprint("enjo-pcs")
	if !fingerprinted {
		t.Fatal("built-in profile enjo-pcs missing")
	}
	wantDependencies := []packageDependency{
		{Kind: dependencyKindIndustry, ID: industry.EMSPackageID, Version: industry.EMSPackageVersion},
		{Kind: dependencyKindProfile, ID: "enjo-pcs", Version: enjoVersion},
	}
	if exported.Name != "一期工程" || !reflect.DeepEqual(exported.Dependencies, wantDependencies) {
		t.Fatalf("package meta = %#v", exported)
	}
	if exported.PointCount != 1 || exported.StrategyCount != 2 {
		t.Fatalf("package content counts = points %d, strategies %d; want 1 physical point and 2 strategies", exported.PointCount, exported.StrategyCount)
	}
	encoded, _ := json.Marshal(exported)
	for _, forbidden := range []string{"10.20.30.40", "secret-value", "old-site-engineer", "SN-OLD-001", siteA, plantA, unitA, channelA, ioA, ruleA} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("package leaked %q", forbidden)
		}
	}
	if strings.Contains(string(encoded), "二号总线") {
		t.Fatal("package carried an unrelated channel")
	}
	for _, node := range exported.Nodes {
		for _, p := range node.Points {
			if p.Type == "enabled" || p.Type == "apiToken" || p.Type == PointTypeSiteTarget || p.Type == data.PointTypeURI {
				t.Fatalf("package kept site-bound point %s on %s", p.Type, node.Type)
			}
		}
		if point, ok := node.Points.Find(unitModelPoint, "1"); ok {
			var model unitModelConfig
			if err := json.Unmarshal([]byte(point.Txt()), &model); err != nil {
				t.Fatal(err)
			}
			if model.DeviceSerial != "" || model.Verifier != "" || model.Verified {
				t.Fatalf("package kept site acceptance info: %#v", model)
			}
		}
	}
	if !stripHasReason(exported.Stripped, "凭据") || !stripHasReason(exported.Stripped, "连接地址") ||
		!stripHasReason(exported.Stripped, "控制启动") || !stripHasReason(exported.Stripped, "现场验收信息") {
		t.Fatalf("stripped report = %#v", exported.Stripped)
	}

	// 干净实例导入。
	var imported importProjectResult
	status, body = call(t, http.MethodPost, baseB+"/projects/import", tokenB, map[string]any{"package": exported}, &imported)
	if status != http.StatusOK {
		t.Fatalf("import: %d %s", status, body)
	}
	if imported.SiteID == "" || imported.SiteID == siteA || imported.Model < 3 || imported.Channels != 1 || imported.Rules != 1 {
		t.Fatalf("import result = %#v", imported)
	}
	if len(imported.NodeIDs) != imported.Created {
		t.Fatalf("import node IDs = %d, created = %d; runtime guidance needs the exact imported scope", len(imported.NodeIDs), imported.Created)
	}
	subtrees, err := handlerB.loadSubtree(imported.SiteID)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]data.NodeEdge{}
	ids := map[string]bool{}
	for _, node := range subtrees {
		ids[node.ID] = true
		byName[node.Desc()] = node
	}
	newPlant, newUnit, newDevice := byName["A 电站"], byName["1 号储能柜"], byName["1 号 PCS"]
	if newPlant.ID == "" || newUnit.ID == "" || newDevice.ID == "" {
		t.Fatalf("imported model missing: %v", byName)
	}

	channels, _ := siotclient.GetNodes(handlerB.nc, "all", "all", data.NodeTypeModbus, false)
	var newChannel data.NodeEdge
	for _, channel := range channels {
		if channel.Desc() == "一号总线" {
			newChannel = channel
		}
	}
	if newChannel.ID == "" || newChannel.ID == channelA {
		t.Fatalf("imported channel = %#v", newChannel)
	}
	if _, ok := newChannel.Points.Find(data.PointTypeURI, ""); ok {
		t.Fatal("imported channel kept the old address")
	}
	ios, _ := siotclient.GetNodes(handlerB.nc, newChannel.ID, "all", data.NodeTypeModbusIO, false)
	if len(ios) != 1 {
		t.Fatalf("imported io points = %d", len(ios))
	}
	newIO := ios[0]
	if !slices.Contains(imported.NodeIDs, newChannel.ID) || !slices.Contains(imported.NodeIDs, newIO.ID) {
		t.Fatalf("import node IDs do not include the rebound channel and point: %#v", imported.NodeIDs)
	}

	if setpoint, _ := newDevice.Points.Text(PointTypePowerSetpoint, ""); setpoint != newIO.ID {
		t.Fatalf("power setpoint = %q, want %q", setpoint, newIO.ID)
	}
	modelPoint, ok := newChannel.Points.Find(unitModelPoint, "1")
	if !ok {
		t.Fatal("imported unit model missing")
	}
	var model unitModelConfig
	if err := json.Unmarshal([]byte(modelPoint.Txt()), &model); err != nil {
		t.Fatal(err)
	}
	if model.BusinessNodeID != newUnit.ID || model.PointMap["有功功率"] != newIO.ID || model.ProfileID != "enjo-pcs" {
		t.Fatalf("imported unit model = %#v", model)
	}
	binding, ok := loadPCCFeedback(newPlant)
	if !ok || binding.MeterPointID != newIO.ID {
		t.Fatalf("imported pcc binding = %#v", binding)
	}
	if state, _ := newUnit.Points.Text(PointTypeCabinetState, ""); state != string(decision.CabinetStandby) {
		t.Fatalf("imported cabinet state = %q, want standby", state)
	}
	importedCapability, ok := loadCapabilityRule(newUnit)
	if !ok || importedCapability.State != decision.RuleDraft || importedCapability.Actions[0].EnergyUnitID != newUnit.ID {
		t.Fatalf("imported capability rule = %#v", importedCapability)
	}

	rules, _ := siotclient.GetNodes(handlerB.nc, "all", "all", "decisionTable", false)
	var newRule data.NodeEdge
	for _, rule := range rules {
		if rule.Desc() == "放电规则" {
			newRule = rule
		}
	}
	if newRule.ID == "" || newRule.ID == ruleA {
		t.Fatalf("imported rule = %#v", newRule)
	}
	if _, ok := newRule.Points.Find("enabled", ""); ok {
		t.Fatal("imported rule auto-started control")
	}
	var importedDocument decision.Document
	appliedText, _ := newRule.Points.Text("applied", "")
	if err := json.Unmarshal([]byte(appliedText), &importedDocument); err != nil {
		t.Fatal(err)
	}
	if importedDocument.Bindings.Actions["a1"].NodeID != newUnit.ID || importedDocument.Bindings.Points["p1"].NodeID != newUnit.ID {
		t.Fatalf("imported rule bindings = %#v", importedDocument.Bindings)
	}
	for _, node := range subtrees {
		if _, ok := node.Points.Find("apiToken", ""); ok {
			t.Fatal("imported site kept credentials")
		}
	}
	versions, err := handlerB.listVersions(imported.SiteID)
	if err != nil || len(versions) != 1 {
		t.Fatalf("imported baseline version = %#v, %v", versions, err)
	}

	// 导入完成即具备运行能力：显式发布并启动柜体后，能力级调度把设定值写到
	// 导入后的功率点位，全程不依赖源实例。
	importedCapability.State = decision.RulePublished
	status, body = call(t, http.MethodPut, baseB+"/energy-units/"+newUnit.ID+"/rule", tokenB, importedCapability, nil)
	if status != http.StatusOK {
		t.Fatalf("publish imported rule: %d %s", status, body)
	}
	if err := SetCabinetState(handlerB.nc, newUnit.ID, decision.CabinetRunning); err != nil {
		t.Fatal(err)
	}
	dispatches, err := handlerB.RunCapabilityDispatch(newUnit.ID)
	if err != nil || len(dispatches) == 0 {
		t.Fatalf("dispatch after import = %#v, %v", dispatches, err)
	}
}

func TestProjectPackageImportFailureLeavesNoNodes(t *testing.T) {
	baseB, tokenB, handlerB, _ := startInputServer(t, t.TempDir(), data.NewID())
	handlerB.SetEMS(&emsapi.Handler{Profiles: profile.Default()})
	rootB := snapshotRoot(t, baseB, tokenB)
	before, _ := siotclient.GetNodes(handlerB.nc, "all", "all", "", false)

	broken := projectPackage{
		Format: projectPackageFormat, FormatVersion: projectPackageFormatVersion, Name: "坏包",
		Nodes: []packageNode{
			{Ref: "n1", Type: NodeTypeSite, Points: data.Points{data.NewPointString("description", "", "坏包")}},
			{Ref: "n2", Type: NodeTypePlant, Parent: "ghost", Points: data.Points{data.NewPointString("description", "", "孤儿电站")}},
		},
	}
	status, body := call(t, http.MethodPost, baseB+"/projects/import", tokenB, map[string]any{"package": broken}, nil)
	if status != http.StatusBadRequest || !strings.Contains(body, "invalid_package_structure") {
		t.Fatalf("broken package import = %d %s", status, body)
	}

	missingDependency := unitModelPackage("no-such-profile")
	missingDependency.Dependencies = []packageDependency{{Kind: dependencyKindProfile, ID: "no-such-profile", Version: "1.0.0"}}
	status, body = call(t, http.MethodPost, baseB+"/projects/import", tokenB, map[string]any{"package": missingDependency, "parent": rootB}, nil)
	if status != http.StatusBadRequest || !strings.Contains(body, "missing_dependency:profile:no-such-profile@1.0.0") {
		t.Fatalf("missing dependency import = %d %s", status, body)
	}

	after, _ := siotclient.GetNodes(handlerB.nc, "all", "all", "", false)
	if len(after) != len(before) {
		t.Fatalf("failed import left %d extra nodes", len(after)-len(before))
	}
}

func managedPackage(nodes ...packageNode) projectPackage {
	return projectPackage{
		Format: projectPackageFormat, FormatVersion: projectPackageFormatVersion,
		Name: "依赖测试", Nodes: nodes,
	}
}

func unitModelPackage(profileID string) projectPackage {
	return managedPackage(
		packageNode{Ref: "n1", Type: NodeTypeSite, Points: data.Points{data.NewPointString("description", "", "依赖测试")}},
		packageNode{Ref: "c1", Type: data.NodeTypeModbus, Parent: "n1", Points: data.Points{
			data.NewPointString("description", "", "总线"),
			data.NewPointString(unitModelPoint, "1", `{"name":"x","profile_id":"`+profileID+`","point_map":{}}`),
		}},
	)
}

// 依赖版本锁定：缺行业包、缺所需版本、Profile 被替换或未声明时，导入必须
// 在创建任何节点前拒绝，并说明缺什么、需要哪个版本。
func TestProjectImportRefusesUnmetDependenciesBeforeCreatingNodes(t *testing.T) {
	base, token, handler, _ := startInputServer(t, t.TempDir(), data.NewID())
	handler.SetEMS(&emsapi.Handler{Profiles: profile.Default()})
	registry, err := industry.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Uninstall(industry.EMSPackageID); err != nil {
		t.Fatal(err)
	}
	handler.SetPackages(registry)

	industryPackage := func(id, version string) projectPackage {
		pkg := managedPackage(packageNode{Ref: "n1", Type: NodeTypeSite,
			Points: data.Points{data.NewPointString("description", "", "缺行业包")}})
		pkg.Dependencies = []packageDependency{{Kind: dependencyKindIndustry, ID: id, Version: version}}
		return pkg
	}
	profilePackage := func(version string, declared bool) projectPackage {
		pkg := unitModelPackage("enjo-pcs")
		if declared {
			pkg.Dependencies = []packageDependency{{Kind: dependencyKindProfile, ID: "enjo-pcs", Version: version}}
		}
		return pkg
	}

	cases := []struct {
		name string
		pkg  projectPackage
		want string
	}{
		{"industry_not_installed", industryPackage(industry.EMSPackageID, industry.EMSPackageVersion),
			"missing_dependency:industry:ems@" + industry.EMSPackageVersion + ":not_installed"},
		{"industry_not_carried", industryPackage("plant-optimizer", "1.0.0"), "missing_dependency:industry:plant-optimizer@1.0.0"},
		{"industry_version_unavailable", industryPackage(industry.EMSPackageID, "0.9.0"),
			"incompatible_dependency:industry:ems:required@0.9.0:available@" + industry.EMSPackageVersion},
		{"industry_not_declared", managedPackage(
			packageNode{Ref: "n1", Type: NodeTypeSite, Points: data.Points{data.NewPointString("description", "", "缺声明")}},
			packageNode{Ref: "p1", Type: NodeTypePlant, Parent: "n1"},
		), "undeclared_dependency:industry:ems"},
		{"profile_version_changed", profilePackage("000000000000", true), "incompatible_dependency:profile:enjo-pcs"},
		{"profile_not_declared", profilePackage("", false), "undeclared_dependency:profile:enjo-pcs"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			before, _ := siotclient.GetNodes(handler.nc, "all", "all", "", false)
			status, body := call(t, http.MethodPost, base+"/projects/import", token, map[string]any{"package": test.pkg}, nil)
			if status != http.StatusBadRequest || !strings.Contains(body, test.want) {
				t.Fatalf("import = %d %s, want %s", status, body, test.want)
			}
			after, _ := siotclient.GetNodes(handler.nc, "all", "all", "", false)
			if len(after) != len(before) {
				t.Fatalf("refused import left %d extra nodes", len(after)-len(before))
			}
		})
	}
}

// 未安装行业包时底座功能完整可用：不含行业依赖的项目照常导入。
func TestBaseProjectImportsWithIndustryPackageUninstalled(t *testing.T) {
	base, token, handler, _ := startInputServer(t, t.TempDir(), data.NewID())
	registry, err := industry.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Uninstall(industry.EMSPackageID); err != nil {
		t.Fatal(err)
	}
	handler.SetPackages(registry)

	pkg := managedPackage(
		packageNode{Ref: "n1", Type: NodeTypeSite, Points: data.Points{data.NewPointString("description", "", "底座工程")}},
		packageNode{Ref: "c1", Type: data.NodeTypeModbus, Parent: "n1",
			Points: data.Points{data.NewPointString("description", "", "一号总线")}},
		packageNode{Ref: "r1", Type: "decisionTable", Parent: "n1",
			Points: data.Points{data.NewPointString("description", "", "底座规则")}},
	)
	pkg.Dependencies = []packageDependency{}
	var imported importProjectResult
	status, body := call(t, http.MethodPost, base+"/projects/import", token, map[string]any{"package": pkg}, &imported)
	if status != http.StatusOK || imported.SiteID == "" || imported.Channels != 1 || imported.Rules != 1 {
		t.Fatalf("base-only import = %d %s", status, body)
	}
}

func stripHasReason(strips []packageStrip, reason string) bool {
	for _, strip := range strips {
		if strip.Reason == reason {
			return true
		}
	}
	return false
}

func waitForNodePoints(t *testing.T, nc *nats.Conn, id, typ string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var available []string
	for {
		nodes, _ := siotclient.GetNodes(nc, "all", id, "", false)
		if len(nodes) > 0 {
			available = available[:0]
			for _, p := range nodes[0].Points {
				available = append(available, p.Type+"#"+p.Key)
			}
			if _, ok := nodes[0].Points.Find(typ, ""); ok {
				return
			}
			for _, p := range nodes[0].Points {
				if p.Type == typ && p.Tombstone%2 == 0 {
					return
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("point %s on %s not ready (points: %v)", typ, id, available)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
