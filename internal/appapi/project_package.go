package appapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/audit"
	"fastiot/internal/calculation"
	"fastiot/internal/decision"
	"fastiot/internal/deepseek"
	"fastiot/internal/genscreen"
	"fastiot/internal/industry"
	"fastiot/internal/virtualinput"
)

// 项目包：把工程模型（站点/电站/储能柜/设备/点位）、适配配置（通道、Profile、
// 点位映射）与规则（决策表、能力级规则/分配器）收进一个带版本号的可移植资产，
// 在干净实例导入后即可部署运行。
//
// 包内不携带现场绑定：地址、凭据、控制启动与旧站运行状态在导出时剥离并列入
// stripped；导入时再按同一规则清洗一次，手改过的包也不会把旧站秘密带进来。
// 引用的节点用包内 ref 表示，导入时换成新 ID，所以不依赖源站任何外部状态。

const (
	projectPackageFormat = "fastiot.project-package"
	// Version 2 pins every dependency at an exact version; a version 1 package
	// carries dependency IDs without versions and is refused rather than
	// imported with unpinned semantics.
	projectPackageFormatVersion = 2
	projectExportAction         = "项目导出"
	projectImportAction         = "项目导入"

	dependencyKindIndustry = "industry"
	dependencyKindProfile  = "profile"
)

// packageDependency pins one thing the project needs to run with the same
// semantics it was built with. Profile versions are content fingerprints so a
// substituted mapping is a different version, not the same one.
type packageDependency struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Version string `json:"version"`
}

type packageNode struct {
	Ref    string      `json:"ref"`
	Type   string      `json:"type"`
	Parent string      `json:"parent,omitempty"`
	Points data.Points `json:"points"`
}

type packageStrip struct {
	Node   string `json:"node"`
	Point  string `json:"point,omitempty"`
	Reason string `json:"reason"`
}

// packageScreen is a confirmed generated screen as the project carries it: the
// model's source with every bound point replaced by a stable token, the
// token → ref map that restores identity on import, and the fingerprint of the
// frame this source builds together with the pins that frame depends on.
type packageScreen struct {
	Node     string            `json:"node"`
	Source   string            `json:"source"`
	Bindings map[string]string `json:"bindings"`
	Artifact string            `json:"artifact"`
	Build    genscreen.Build   `json:"build"`
}

type projectPackage struct {
	Format            string              `json:"format"`
	FormatVersion     int                 `json:"format_version"`
	ID                string              `json:"id"`
	Name              string              `json:"name"`
	Version           int                 `json:"version"`
	SourceVersion     string              `json:"source_version,omitempty"`
	SourceFingerprint string              `json:"source_fingerprint,omitempty"`
	Deliverable       bool                `json:"deliverable"`
	Integrity         string              `json:"integrity,omitempty"`
	Manifest          map[string]string   `json:"manifest,omitempty"`
	ScreenCount       int                 `json:"screen_count"`
	CreatedAt         time.Time           `json:"created_at"`
	Operator          string              `json:"operator"`
	Dependencies      []packageDependency `json:"dependencies"`
	Nodes             []packageNode       `json:"nodes"`
	PointCount        int                 `json:"point_count"`
	StrategyCount     int                 `json:"strategy_count"`
	Screens           []packageScreen     `json:"screens"`
	Stripped          []packageStrip      `json:"stripped"`
	Warnings          []string            `json:"warnings"`
}

type importProjectRequest struct {
	Package json.RawMessage `json:"package"`
	Parent  string          `json:"parent"`
	Name    string          `json:"name"`
}

type importProjectResult struct {
	SiteID    string         `json:"site_id"`
	Name      string         `json:"name"`
	Created   int            `json:"created"`
	Model     int            `json:"model"`
	Channels  int            `json:"channels"`
	Rules     int            `json:"rules"`
	Profiles  []string       `json:"profiles"`
	VersionID string         `json:"version_id,omitempty"`
	NodeIDs   []string       `json:"node_ids"`
	Ignored   []packageStrip `json:"ignored"`
	Warnings  []string       `json:"warnings"`
}

// 现场绑定与运行状态不是工程资产：导出与导入都按这张表剥离。
var packageRuntimePoints = map[string]bool{
	PointTypeSiteTarget: true, PointTypePlantTarget: true,
	PointTypeActualPower: true, PointTypePVAvailableKW: true, PointTypePVLimitKW: true,
	PointTypePVForecastKW: true, PointTypeEVMaxLoadKW: true, PointTypeEVCurtailableKW: true,
	PointTypeEVOnlineRatio: true,
	"valueSet":             true, "ruleAccess": true, "easyemsAllocHistory": true,
	"businessId": true, "easyemsInput": true, "easyemsDerivedObservation": true, "ruleExecutionFact": true,
}

func packageSecretKey(key string) bool {
	key = strings.ToLower(strings.NewReplacer("-", "", "_", "", " ", "").Replace(key))
	return isSecretPoint(key) || key == "authorization" || key == "cookie" || key == "setcookie"
}

func packageStripReason(node data.NodeEdge, p data.Point) string {
	switch {
	case packageSecretKey(p.Type):
		return "凭据"
	case reuseRuntimePoints[p.Type]:
		return "运行状态"
	case packageRuntimePoints[p.Type]:
		return "实例身份、授权或运行数据"
	case node.Type == data.NodeTypeModbus && reuseConnectionPoints[p.Type]:
		return "连接地址"
	case node.Type == "decisionTable" && p.Type == "enabled":
		return "控制启动"
	case node.Type == "decisionTable" && (p.Type == "lastMatch" || p.Type == "error" || p.Type == "history"):
		return "运行记录"
	}
	return ""
}

// validate 只检查包自身的一致性；依赖是否齐备要对照目标实例另行检查。
func (pkg projectPackage) validate() error {
	if pkg.Format != projectPackageFormat {
		return fmt.Errorf("invalid_package_format")
	}
	if pkg.FormatVersion != projectPackageFormatVersion {
		return fmt.Errorf("unsupported_package_version:%d", pkg.FormatVersion)
	}
	if pkg.Deliverable || pkg.Integrity != "" {
		if err := pkg.verifyIntegrity(); err != nil {
			return err
		}
		if pkg.Deliverable && (pkg.SourceVersion == "" || len(pkg.SourceFingerprint) != 64) {
			return fmt.Errorf("package_source_fingerprint_missing")
		}
		points, strategies := packageContentCounts(pkg.Nodes)
		if points != pkg.PointCount || strategies != pkg.StrategyCount || packageScreenCount(pkg.Nodes, pkg.Screens) != pkg.ScreenCount {
			return fmt.Errorf("package_counts_mismatch")
		}
	}
	if len(pkg.Nodes) == 0 {
		return fmt.Errorf("invalid_package_structure:empty")
	}
	declared := map[string]bool{}
	for _, dependency := range pkg.Dependencies {
		if dependency.Kind != dependencyKindIndustry && dependency.Kind != dependencyKindProfile {
			return fmt.Errorf("invalid_package_structure:dependency_kind")
		}
		if strings.TrimSpace(dependency.ID) == "" || strings.TrimSpace(dependency.Version) == "" {
			return fmt.Errorf("invalid_package_structure:dependency_version")
		}
		key := dependency.Kind + ":" + dependency.ID
		if declared[key] {
			return fmt.Errorf("invalid_package_structure:dependency_duplicate")
		}
		declared[key] = true
	}
	refs := map[string]bool{}
	sites := 0
	for _, node := range pkg.Nodes {
		if node.Ref == "" || refs[node.Ref] {
			return fmt.Errorf("invalid_package_structure:invalid_ref")
		}
		refs[node.Ref] = true
		if node.Type == "" {
			return fmt.Errorf("invalid_package_structure:missing_type")
		}
		// 账号、凭据与登记令牌属于旧站身份，项目包不承载，手改的包也不接受。
		if reuseSkipTypes[node.Type] {
			return fmt.Errorf("invalid_package_structure:credential_node:%s", node.Type)
		}
		if node.Type == NodeTypeSite && node.Parent == "" {
			sites++
		}
	}
	for _, node := range pkg.Nodes {
		for _, point := range node.Points {
			if point.Tombstone%2 != 0 {
				continue
			}
			if point.Type == generatedScreenDraftType {
				carried := false
				for _, screen := range pkg.Screens {
					carried = carried || screen.Node == node.Ref
				}
				if !carried {
					return fmt.Errorf("missing_screen:%s", node.Ref)
				}
				continue // only the validated screens entry is authoritative on import
			}
			if packageJSONDefinition(data.NodeEdge{Type: node.Type}, point) && (!json.Valid([]byte(point.Txt())) || point.Txt() == "null") {
				return fmt.Errorf("invalid_package_definition:%s", point.Type)
			}
			if pkg.Deliverable && packageStripReason(data.NodeEdge{Type: node.Type}, point) != "" {
				return fmt.Errorf("package_contains_runtime_or_secret")
			}
			if structuredPackagePoint(point.Type) {
				if _, err := mapPackageReferences(point, func(id string) string { return id }); err != nil {
					return err
				}
			}
			for _, ref := range packageReferences(data.NodeEdge{Type: node.Type}, point) {
				if !refs[ref] {
					return fmt.Errorf("invalid_package_structure:missing_reference")
				}
			}
		}
	}
	if sites != 1 {
		return fmt.Errorf("invalid_package_structure:site_root")
	}
	screened := map[string]bool{}
	for _, screen := range pkg.Screens {
		if screen.Node == "" || !refs[screen.Node] {
			return fmt.Errorf("invalid_package_structure:screen_node")
		}
		if screened[screen.Node] {
			return fmt.Errorf("invalid_package_structure:screen_duplicate")
		}
		screened[screen.Node] = true
		if strings.TrimSpace(screen.Source) == "" {
			return fmt.Errorf("missing_screen:%s", screen.Node)
		}
		if len(screen.Source) > maxGeneratedScreenSource {
			return fmt.Errorf("invalid_package_structure:screen_too_large")
		}
		if screen.Artifact == "" || screen.Build.Runtime == "" {
			return fmt.Errorf("invalid_package_structure:screen_artifact")
		}
		for token, ref := range screen.Bindings {
			if token == "" || !refs[ref] {
				return fmt.Errorf("missing_screen_binding:%s", screen.Node)
			}
		}
		for _, binding := range genscreen.Parse(screen.Source).FindBindings() {
			if binding.Declared && screen.Bindings[binding.ID] == "" {
				return fmt.Errorf("missing_screen_binding:%s", screen.Node)
			}
		}
		if screen.Build != genscreen.BuildFor(screen.Source) {
			return fmt.Errorf("screen_build_mismatch:%s", screen.Node)
		}
		if screen.Artifact != genscreen.Fingerprint(screen.Source) {
			return fmt.Errorf("screen_artifact_mismatch:%s", screen.Node)
		}
	}
	if _, err := orderPackageRefs(pkg.Nodes); err != nil {
		return err
	}
	return nil
}

// orderPackageRefs 保证父节点先于子节点，并拒绝悬空父引用与环。
func orderPackageRefs(nodes []packageNode) ([]string, error) {
	byRef := make(map[string]packageNode, len(nodes))
	for _, node := range nodes {
		byRef[node.Ref] = node
	}
	state := make(map[string]int, len(nodes))
	order := make([]string, 0, len(nodes))
	var visit func(ref string) error
	visit = func(ref string) error {
		switch state[ref] {
		case 2:
			return nil
		case 1:
			return fmt.Errorf("invalid_package_structure:cycle:%s", ref)
		}
		state[ref] = 1
		node := byRef[ref]
		if node.Parent != "" {
			if _, ok := byRef[node.Parent]; !ok {
				return fmt.Errorf("invalid_package_structure:missing_parent:%s", node.Parent)
			}
			if err := visit(node.Parent); err != nil {
				return err
			}
		}
		state[ref] = 2
		order = append(order, ref)
		return nil
	}
	refs := make([]string, 0, len(nodes))
	for _, node := range nodes {
		refs = append(refs, node.Ref)
	}
	sort.Strings(refs)
	for _, ref := range refs {
		if err := visit(ref); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// packageReferences 列出一个点指向的节点 ID；导出据此把通道、规则等接入层
// 对象一起收进项目包。
func packageReferences(node data.NodeEdge, p data.Point) []string {
	if p.Type == generatedScreenDraftType {
		var draft struct {
			Source string `json:"source"`
		}
		if json.Unmarshal([]byte(p.Txt()), &draft) != nil {
			return nil
		}
		var refs []string
		for _, binding := range genscreen.Parse(draft.Source).FindBindings() {
			if binding.Declared && binding.ID != "" {
				refs = append(refs, binding.ID)
			}
		}
		return refs
	}
	if structuredPackagePoint(p.Type) {
		var refs []string
		_, _ = mapPackageReferences(p, func(id string) string {
			if id != "" {
				refs = append(refs, id)
			}
			return id
		})
		return refs
	}
	if node.Type == data.NodeTypeModbus && p.Type == unitModelPoint {
		var model unitModelConfig
		if json.Unmarshal([]byte(p.Txt()), &model) != nil {
			return nil
		}
		refs := []string{}
		if model.BusinessNodeID != "" {
			refs = append(refs, model.BusinessNodeID)
		}
		for _, id := range model.PointMap {
			if id != "" {
				refs = append(refs, id)
			}
		}
		return refs
	}
	switch p.Type {
	case PointTypePowerSetpoint, reuseSetpointPoint, PointTypePCCFeedback, capabilityInputsPoint,
		forecastInputsPoint, capabilityRulePoint, capabilityAllocatorPoint,
		PointTypeGroupMembers, aggregatePoint, calculationDocument:
	case "applied", "draft":
		if node.Type != "decisionTable" {
			return nil
		}
	case stateMachineDocumentPoint:
		if node.Type != stateMachineType {
			return nil
		}
	default:
		return nil
	}
	var refs []string
	switch p.Type {
	case PointTypePowerSetpoint, reuseSetpointPoint:
		refs = append(refs, p.Txt())
	case PointTypePCCFeedback:
		var binding decision.PCCFeedback
		if json.Unmarshal([]byte(p.Txt()), &binding) != nil {
			return nil
		}
		refs = append(refs, binding.MeterPointID)
		refs = append(refs, binding.PhasePointIDs...)
	case capabilityInputsPoint:
		var inputs CapabilityInputs
		if json.Unmarshal([]byte(p.Txt()), &inputs) != nil {
			return nil
		}
		refs = append(refs, inputs.SOCPointID, inputs.TempPointID, inputs.ChargeLimitPointID, inputs.DischargeLimitPointID)
	case forecastInputsPoint:
		var inputs ForecastInputs
		if json.Unmarshal([]byte(p.Txt()), &inputs) != nil {
			return nil
		}
		refs = append(refs, inputs.SourcePointID)
	case capabilityRulePoint:
		var rule decision.CapabilityRule
		if json.Unmarshal([]byte(p.Txt()), &rule) != nil {
			return nil
		}
		for _, condition := range rule.Conditions {
			refs = append(refs, condition.Ref.EnergyUnitID)
		}
		for _, action := range rule.Actions {
			refs = append(refs, action.EnergyUnitID)
		}
	case capabilityAllocatorPoint:
		var members []decision.EnergyMember
		if json.Unmarshal([]byte(p.Txt()), &members) != nil {
			return nil
		}
		for _, member := range members {
			refs = append(refs, member.EnergyUnitID)
		}
	case PointTypeGroupMembers:
		var ids []string
		if json.Unmarshal([]byte(p.Txt()), &ids) != nil {
			return nil
		}
		refs = append(refs, ids...)
	case aggregatePoint:
		var config AggregateConfig
		if json.Unmarshal([]byte(p.Txt()), &config) != nil {
			return nil
		}
		refs = append(refs, config.Members...)
	case calculationDocument:
		var document calculation.Document
		if json.Unmarshal([]byte(p.Txt()), &document) != nil {
			return nil
		}
		refs = append(refs, document.Draft.Inputs...)
		if document.Applied != nil {
			refs = append(refs, document.Applied.Inputs...)
		}
	case "applied", "draft":
		var document decision.Document
		if json.Unmarshal([]byte(p.Txt()), &document) != nil {
			return nil
		}
		for _, target := range document.Bindings.Points {
			refs = append(refs, target.NodeID)
		}
		for _, target := range document.Bindings.Actions {
			refs = append(refs, target.NodeID)
		}
		mapRuleReadbacks(&document, func(id string) string { refs = append(refs, id); return id })
	case stateMachineDocumentPoint:
		var document decision.StateMachineDocument
		if json.Unmarshal([]byte(p.Txt()), &document) != nil {
			return nil
		}
		document.MapReferences(func(id string) string { refs = append(refs, id); return id })
	}
	out := refs[:0]
	for _, ref := range refs {
		if ref != "" {
			out = append(out, ref)
		}
	}
	return out
}

func mapRuleReadbacks(document *decision.Document, mapID func(string) string) {
	mapActions := func(actions []decision.Action) {
		for i := range actions {
			if actions[i].Readback != nil && actions[i].Readback.Target.NodeID != "" {
				actions[i].Readback.Target.NodeID = mapID(actions[i].Readback.Target.NodeID)
			}
		}
	}
	for i := range document.Table.Rows {
		mapActions(document.Table.Rows[i].Actions)
	}
	for i := range document.Scenarios {
		mapActions(document.Scenarios[i].ExpectActions)
	}
}

// Structured definitions keep their schema while every identity-bearing field
// is visited by the same mapper for collection, validation and import rewriting.
func packageJSONDefinition(node data.NodeEdge, point data.Point) bool {
	if structuredPackagePoint(point.Type) {
		return true
	}
	switch point.Type {
	case PointTypePCCFeedback, capabilityInputsPoint, forecastInputsPoint, capabilityRulePoint, capabilityAllocatorPoint, PointTypeGroupMembers, aggregatePoint, calculationDocument, virtualInputDocument, generatedScreenDraftType:
		return true
	case unitModelPoint:
		return node.Type == data.NodeTypeModbus
	case "draft", "applied":
		return node.Type == "decisionTable"
	case stateMachineDocumentPoint:
		return node.Type == stateMachineType
	}
	return false
}

func structuredPackagePoint(typ string) bool {
	switch typ {
	case screenPointType, dailyStatsPoint, "easyemsUnitMembers", PointTypePointMap, "easyemsAllocator", "easyemsSafety":
		return true
	}
	return false
}

func mapPackageReferences(p data.Point, mapID func(string) string) (data.Point, error) {
	var document any
	if err := json.Unmarshal([]byte(p.Txt()), &document); err != nil {
		return p, fmt.Errorf("invalid_package_definition:%s", p.Type)
	}
	var visit func(any, string) any
	visit = func(value any, key string) any {
		switch v := value.(type) {
		case map[string]any:
			for name, child := range v {
				v[name] = visit(child, name)
			}
		case []any:
			for i, child := range v {
				v[i] = visit(child, key)
			}
		case string:
			if p.Type == PointTypePointMap || key == "point_id" || key == "point_ids" || key == "node_id" || key == "communication_point_id" || p.Type == screenPointType && key == "layer" {
				if v != "" {
					return mapID(v)
				}
			}
		}
		return value
	}
	document = visit(document, "")
	encoded, err := json.Marshal(document)
	if err != nil {
		return p, err
	}
	return data.NewPointString(p.Type, p.Key, string(encoded)), nil
}

// packagePoints 把节点配置写成包内点：剥离现场绑定，引用改写成包内 ref。
// 包外引用会被移除并在 warnings 里说明，绝不留下旧 ID 冒充可用。
func packagePoints(node data.NodeEdge, idMap map[string]string, strips *[]packageStrip, warnings *[]string) data.Points {
	label := idMap[node.ID]
	if label == "" {
		label = node.Type
	}
	points := make(data.Points, 0, len(node.Points))
	for _, p := range node.Points {
		if p.Tombstone%2 != 0 {
			continue
		}
		if p.Type == generatedScreenDraftType {
			// 画面随 screens 条目单独承载：源码、绑定 token 与构建产物一起走。
			continue
		}
		if reason := packageStripReason(node, p); reason != "" {
			if strips != nil {
				*strips = append(*strips, packageStrip{Node: label, Point: p.Type, Reason: reason})
			}
			continue
		}
		if strips != nil {
			switch {
			case node.Type == data.NodeTypeModbus && p.Type == unitModelPoint:
				var model unitModelConfig
				if json.Unmarshal([]byte(p.Txt()), &model) == nil &&
					(model.Verified || model.Verifier != "" || model.VerifiedAt != "" || model.Verification != "" || model.DeviceSerial != "") {
					*strips = append(*strips, packageStrip{Node: label, Point: unitModelPoint, Reason: "现场验收信息"})
				}
			case p.Type == capabilityRulePoint:
				var rule decision.CapabilityRule
				if json.Unmarshal([]byte(p.Txt()), &rule) == nil && rule.State != decision.RuleDraft {
					*strips = append(*strips, packageStrip{Node: label, Point: capabilityRulePoint, Reason: "控制启动"})
				}
			}
		}
		rewritten, keep, unresolved := rewritePackagePoint(node, p, idMap)
		if len(unresolved) > 0 && warnings != nil {
			addPackageWarning(warnings, fmt.Sprintf("「%s」的现场引用需在目标实例重新绑定；通知接收账号须显式选择，不继承权限", label))
		}
		if !keep {
			continue
		}
		rewritten.Time = time.Time{}
		rewritten.Origin = ""
		points = append(points, packageWirePoint(*rewritten))
	}
	return points
}

func packageScreenCount(nodes []packageNode, screens []packageScreen) int {
	count := len(screens)
	for _, node := range nodes {
		for _, point := range node.Points {
			if point.Type == screenPointType && point.Tombstone%2 == 0 {
				count++
				break
			}
		}
	}
	return count
}

func packageContentCounts(nodes []packageNode) (points, strategies int) {
	for _, node := range nodes {
		switch node.Type {
		case data.NodeTypeModbusIO, data.NodeTypeVariable, NodeTypeOpcuaPoint, calculationType, virtualInputType:
			points++
		case "decisionTable", "stateMachine":
			strategies++
		}
		for _, point := range node.Points {
			if point.Type == aggregatePoint && point.Tombstone%2 == 0 {
				points++
			}
			if point.Type == capabilityRulePoint && point.Tombstone%2 == 0 {
				strategies++
			}
		}
	}
	return points, strategies
}

func addPackageWarning(warnings *[]string, message string) {
	for _, existing := range *warnings {
		if existing == message {
			return
		}
	}
	*warnings = append(*warnings, message)
}

func rewritePackagePoint(node data.NodeEdge, p data.Point, idMap map[string]string) (*data.Point, bool, []string) {
	if structuredPackagePoint(p.Type) {
		var unresolved []string
		rewritten, err := mapPackageReferences(p, func(id string) string {
			if id == "" {
				return ""
			}
			if mapped, ok := idMap[id]; ok {
				return mapped
			}
			unresolved = append(unresolved, id)
			return ""
		})
		if err != nil {
			return &p, false, []string{"invalid_document"}
		}
		return &rewritten, true, unresolved
	}
	point := p
	remap := func(id string) (string, bool) {
		if id == "" {
			return "", true
		}
		mapped, ok := idMap[id]
		return mapped, ok
	}
	switch p.Type {
	case calculationDocument:
		if node.Type != calculationType {
			return &point, true, nil
		}
		var document calculation.Document
		if json.Unmarshal([]byte(p.Txt()), &document) != nil {
			return &point, true, nil
		}
		document.LastChange = nil
		var unresolved []string
		rewriteInputs := func(inputs []string) []string {
			out := make([]string, len(inputs))
			for i, id := range inputs {
				if mapped, ok := remap(id); ok {
					out[i] = mapped
				} else {
					unresolved = append(unresolved, id)
				}
			}
			return out
		}
		document.Draft.Inputs = rewriteInputs(document.Draft.Inputs)
		if document.Applied != nil {
			document.Applied.Inputs = rewriteInputs(document.Applied.Inputs)
		}
		encoded, err := json.Marshal(document)
		if err != nil {
			return &point, true, nil
		}
		rewritten := data.NewPointString(calculationDocument, p.Key, string(encoded))
		return &rewritten, true, unresolved
	case virtualInputDocument:
		if node.Type != virtualInputType {
			return &point, true, nil
		}
		var document virtualinput.Document
		if json.Unmarshal([]byte(p.Txt()), &document) != nil {
			return &point, true, nil
		}
		// 当前值与保留值是旧站运行状态，不属于工程资产。
		document.Current = nil
		document.Retained = nil
		document.LastChange = nil
		encoded, err := json.Marshal(document)
		if err != nil {
			return &point, true, nil
		}
		rewritten := data.NewPointString(virtualInputDocument, p.Key, string(encoded))
		return &rewritten, true, nil
	case PointTypePowerSetpoint, reuseSetpointPoint:
		ref := p.Txt()
		if ref == "" {
			return &point, true, nil
		}
		if mapped, ok := idMap[ref]; ok {
			rewritten := data.NewPointString(p.Type, p.Key, mapped)
			return &rewritten, true, nil
		}
		return &point, false, []string{ref}
	case PointTypePCCFeedback:
		var binding decision.PCCFeedback
		if json.Unmarshal([]byte(p.Txt()), &binding) != nil {
			return &point, true, nil
		}
		var unresolved []string
		if binding.MeterPointID != "" {
			if mapped, ok := idMap[binding.MeterPointID]; ok {
				binding.MeterPointID = mapped
			} else {
				unresolved = append(unresolved, binding.MeterPointID)
				binding.MeterPointID = ""
			}
		}
		phases := make([]string, 0, len(binding.PhasePointIDs))
		for _, id := range binding.PhasePointIDs {
			if mapped, ok := idMap[id]; ok {
				phases = append(phases, mapped)
			} else if id != "" {
				unresolved = append(unresolved, id)
			}
		}
		binding.PhasePointIDs = phases
		encoded, _ := json.Marshal(binding)
		rewritten := data.NewPointString(PointTypePCCFeedback, p.Key, string(encoded))
		return &rewritten, true, unresolved
	case capabilityInputsPoint:
		var inputs CapabilityInputs
		if json.Unmarshal([]byte(p.Txt()), &inputs) != nil {
			return &point, true, nil
		}
		var unresolved []string
		rewrite := func(id string) string {
			if id == "" {
				return ""
			}
			if mapped, ok := idMap[id]; ok {
				return mapped
			}
			unresolved = append(unresolved, id)
			return ""
		}
		inputs.SOCPointID = rewrite(inputs.SOCPointID)
		inputs.TempPointID = rewrite(inputs.TempPointID)
		inputs.ChargeLimitPointID = rewrite(inputs.ChargeLimitPointID)
		inputs.DischargeLimitPointID = rewrite(inputs.DischargeLimitPointID)
		encoded, _ := json.Marshal(inputs)
		rewritten := data.NewPointString(capabilityInputsPoint, p.Key, string(encoded))
		return &rewritten, true, unresolved
	case forecastInputsPoint:
		var inputs ForecastInputs
		if json.Unmarshal([]byte(p.Txt()), &inputs) != nil {
			return &point, true, nil
		}
		var unresolved []string
		if inputs.SourcePointID != "" {
			if mapped, ok := idMap[inputs.SourcePointID]; ok {
				inputs.SourcePointID = mapped
			} else {
				unresolved = append(unresolved, inputs.SourcePointID)
				inputs.SourcePointID = ""
			}
		}
		encoded, _ := json.Marshal(inputs)
		rewritten := data.NewPointString(forecastInputsPoint, p.Key, string(encoded))
		return &rewritten, true, unresolved
	case capabilityRulePoint:
		var rule decision.CapabilityRule
		if json.Unmarshal([]byte(p.Txt()), &rule) != nil {
			return &point, true, nil
		}
		// 控制启动与发布时刻不随包迁移，导入后由交付人员显式发布。
		rule.State = decision.RuleDraft
		rule.PublishedAt = ""
		var unresolved []string
		for i := range rule.Conditions {
			id := rule.Conditions[i].Ref.EnergyUnitID
			mapped, ok := remap(id)
			if !ok {
				unresolved = append(unresolved, id)
			}
			rule.Conditions[i].Ref.EnergyUnitID = mapped
		}
		for i := range rule.Actions {
			id := rule.Actions[i].EnergyUnitID
			mapped, ok := remap(id)
			if !ok {
				unresolved = append(unresolved, id)
			}
			rule.Actions[i].EnergyUnitID = mapped
		}
		if len(unresolved) > 0 {
			return &point, false, unresolved
		}
		encoded, _ := json.Marshal(rule)
		rewritten := data.NewPointString(capabilityRulePoint, p.Key, string(encoded))
		return &rewritten, true, nil
	case capabilityAllocatorPoint:
		var members []decision.EnergyMember
		if json.Unmarshal([]byte(p.Txt()), &members) != nil {
			return &point, true, nil
		}
		var unresolved []string
		kept := make([]decision.EnergyMember, 0, len(members))
		for _, member := range members {
			mapped, ok := remap(member.EnergyUnitID)
			if !ok {
				unresolved = append(unresolved, member.EnergyUnitID)
				continue
			}
			member.EnergyUnitID = mapped
			kept = append(kept, member)
		}
		encoded, _ := json.Marshal(kept)
		rewritten := data.NewPointString(capabilityAllocatorPoint, p.Key, string(encoded))
		return &rewritten, true, unresolved
	case PointTypeGroupMembers:
		var ids []string
		if json.Unmarshal([]byte(p.Txt()), &ids) != nil {
			return &point, true, nil
		}
		var unresolved []string
		kept := make([]string, 0, len(ids))
		for _, id := range ids {
			if mapped, ok := idMap[id]; ok {
				kept = append(kept, mapped)
			} else if id != "" {
				unresolved = append(unresolved, id)
			}
		}
		encoded, _ := json.Marshal(kept)
		rewritten := data.NewPointString(PointTypeGroupMembers, p.Key, string(encoded))
		return &rewritten, true, unresolved
	case aggregatePoint:
		var config AggregateConfig
		if json.Unmarshal([]byte(p.Txt()), &config) != nil {
			return &point, true, nil
		}
		var unresolved []string
		kept := make([]string, 0, len(config.Members))
		for _, id := range config.Members {
			if mapped, ok := idMap[id]; ok {
				kept = append(kept, mapped)
			} else if id != "" {
				unresolved = append(unresolved, id)
			}
		}
		config.Members = kept
		encoded, _ := json.Marshal(config)
		rewritten := data.NewPointString(aggregatePoint, p.Key, string(encoded))
		return &rewritten, true, unresolved
	case unitModelPoint:
		if node.Type != data.NodeTypeModbus {
			return &point, true, nil
		}
		var model unitModelConfig
		if json.Unmarshal([]byte(p.Txt()), &model) != nil {
			return &point, true, nil
		}
		var unresolved []string
		if model.BusinessNodeID != "" {
			if mapped, ok := idMap[model.BusinessNodeID]; ok {
				model.BusinessNodeID = mapped
			} else {
				unresolved = append(unresolved, model.BusinessNodeID)
				model.BusinessNodeID = ""
			}
		}
		pointMap := make(map[string]string, len(model.PointMap))
		for name, id := range model.PointMap {
			if mapped, ok := idMap[id]; ok {
				pointMap[name] = mapped
			} else {
				unresolved = append(unresolved, id)
			}
		}
		model.PointMap = pointMap
		// 现场验收信息绑定旧站设备，不随包迁移。
		model.Verified = false
		model.Verifier = ""
		model.VerifiedAt = ""
		model.Verification = ""
		model.DeviceSerial = ""
		encoded, _ := json.Marshal(model)
		rewritten := data.NewPointString(unitModelPoint, p.Key, string(encoded))
		return &rewritten, true, unresolved
	case "applied", "draft":
		if node.Type != "decisionTable" {
			return &point, true, nil
		}
		var document decision.Document
		if json.Unmarshal([]byte(p.Txt()), &document) != nil {
			return &point, true, nil
		}
		var unresolved []string
		rewriteTargets := func(targets map[string]decision.Target) map[string]decision.Target {
			updated := make(map[string]decision.Target, len(targets))
			for key, target := range targets {
				if target.NodeID == "" {
					updated[key] = target
					continue
				}
				if mapped, ok := idMap[target.NodeID]; ok {
					target.NodeID = mapped
					updated[key] = target
				} else {
					unresolved = append(unresolved, target.NodeID)
				}
			}
			return updated
		}
		document.Bindings.Points = rewriteTargets(document.Bindings.Points)
		document.Bindings.Actions = rewriteTargets(document.Bindings.Actions)
		mapRuleReadbacks(&document, func(id string) string {
			if mapped, ok := idMap[id]; ok {
				return mapped
			}
			unresolved = append(unresolved, id)
			return ""
		})
		document.Simulation = nil
		unresolved = append(unresolved, document.ClearNotificationRecipients()...)
		encoded, _ := json.Marshal(document)
		rewritten := data.NewPointString(p.Type, p.Key, string(encoded))
		return &rewritten, true, unresolved
	case stateMachineDocumentPoint:
		if node.Type != stateMachineType {
			return &point, true, nil
		}
		var document decision.StateMachineDocument
		if json.Unmarshal([]byte(p.Txt()), &document) != nil {
			return &point, true, nil
		}
		var unresolved []string
		document.MapReferences(func(id string) string {
			if mapped, ok := idMap[id]; ok {
				return mapped
			}
			unresolved = append(unresolved, id)
			return ""
		})
		// 运行状态不随包迁移：重置到初始状态并剥离执行证据。
		document.CurrentState = document.Draft.InitialState
		document.LastExecution = nil
		encoded, _ := json.Marshal(document)
		rewritten := data.NewPointString(p.Type, p.Key, string(encoded))
		return &rewritten, true, unresolved
	}
	return &point, true, nil
}

// collectProjectPackageNodes 取出站点子树，以及被它引用的通道与规则。
// 无关通道、其他站点的对象不会被卷进来。
func (h *Handler) collectProjectPackageNodes(siteID string) ([]data.NodeEdge, []packageStrip, []string, error) {
	subtree, err := h.loadReuseSource(siteID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("source_not_found")
	}
	skipped := skippedNodes(subtree)
	strips := []packageStrip{}
	index := make(map[string]data.NodeEdge, len(subtree))
	included := make(map[string]bool, len(subtree))
	for _, node := range subtree {
		if skipped[node.ID] {
			strips = append(strips, packageStrip{Node: node.Type, Point: node.Type, Reason: "凭据与账号"})
			continue
		}
		index[node.ID] = node
		included[node.ID] = true
	}

	channelOf := map[string]string{}
	channelMembers := map[string][]data.NodeEdge{}
	channels, err := client.GetNodes(h.nc, "all", "all", data.NodeTypeModbus, false)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, channel := range channels {
		members, err := h.loadSubtree(channel.ID)
		if err != nil {
			continue
		}
		channelMembers[channel.ID] = members
		for _, member := range members {
			index[member.ID] = member
			channelOf[member.ID] = channel.ID
		}
	}
	rules, err := client.GetNodes(h.nc, "all", "all", "decisionTable", false)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, rule := range rules {
		index[rule.ID] = rule
	}

	warnings := []string{}
	unresolved := map[string]bool{}
	includeChannel := func(channelID string) bool {
		changed := false
		for _, member := range channelMembers[channelID] {
			if !included[member.ID] {
				included[member.ID] = true
				changed = true
			}
		}
		return changed
	}
	for changed := true; changed; {
		changed = false
		// Access bindings live on the channel, not the business device. Include
		// their definitions even when no control/setpoint reference exists.
		for channelID, members := range channelMembers {
			for _, member := range members {
				for _, point := range member.Points {
					if point.Type != unitModelPoint || point.Tombstone%2 != 0 {
						continue
					}
					var model unitModelConfig
					if json.Unmarshal([]byte(point.Txt()), &model) == nil && included[model.BusinessNodeID] {
						if includeChannel(channelID) {
							changed = true
						}
					}
				}
			}
		}
		for id := range included {
			node, ok := index[id]
			if !ok {
				continue
			}
			for _, p := range node.Points {
				if p.Tombstone%2 != 0 {
					continue
				}
				for _, ref := range packageReferences(node, p) {
					if ref == "" || included[ref] {
						continue
					}
					target, ok := index[ref]
					if !ok {
						if !unresolved[node.ID] {
							unresolved[node.ID] = true
							addPackageWarning(&warnings, fmt.Sprintf("「%s」引用了项目包外的对象，导入后需重新绑定", nodeName(node)))
						}
						continue
					}
					if channelID, isChannelPoint := channelOf[ref]; isChannelPoint {
						if includeChannel(channelID) {
							changed = true
						}
						continue
					}
					if target.Type == "decisionTable" {
						included[ref] = true
						changed = true
						continue
					}
					if !unresolved[node.ID] {
						unresolved[node.ID] = true
						addPackageWarning(&warnings, fmt.Sprintf("「%s」引用了项目包外的对象，导入后需重新绑定", nodeName(node)))
					}
				}
			}
		}
		for id, node := range index {
			if included[id] || node.Parent == "" {
				continue
			}
			if included[node.Parent] {
				included[id] = true
				changed = true
			}
		}
	}

	selected := make([]data.NodeEdge, 0, len(included))
	for id := range included {
		selected = append(selected, index[id])
	}
	return orderPackageNodes(selected), strips, warnings, nil
}

// orderPackageNodes 深度优先排序，站点根在前，其余根与同级节点按 ID 排序，
// 保证同一配置导出的包稳定可比。
func orderPackageNodes(nodes []data.NodeEdge) []data.NodeEdge {
	byID := make(map[string]data.NodeEdge, len(nodes))
	children := make(map[string][]data.NodeEdge)
	var roots []data.NodeEdge
	for _, node := range nodes {
		byID[node.ID] = node
	}
	for _, node := range nodes {
		if _, ok := byID[node.Parent]; ok {
			children[node.Parent] = append(children[node.Parent], node)
			continue
		}
		roots = append(roots, node)
	}
	sort.Slice(roots, func(i, j int) bool {
		if (roots[i].Type == NodeTypeSite) != (roots[j].Type == NodeTypeSite) {
			return roots[i].Type == NodeTypeSite
		}
		return roots[i].ID < roots[j].ID
	})
	for parent := range children {
		sort.Slice(children[parent], func(i, j int) bool { return children[parent][i].ID < children[parent][j].ID })
	}
	ordered := make([]data.NodeEdge, 0, len(nodes))
	seen := make(map[string]bool, len(nodes))
	var walk func(node data.NodeEdge)
	walk = func(node data.NodeEdge) {
		if seen[node.ID] {
			return
		}
		seen[node.ID] = true
		ordered = append(ordered, node)
		for _, child := range children[node.ID] {
			walk(child)
		}
	}
	for _, root := range roots {
		walk(root)
	}
	return ordered
}

// screenDraftPoint finds the newest confirmed screen on a node. The point key
// is not reliable here: the draft is stored with an empty key.
func screenDraftPoint(points data.Points) (data.Point, bool) {
	var found data.Point
	ok := false
	for _, p := range points {
		if p.Type == generatedScreenDraftType && p.Tombstone%2 == 0 {
			found = p
			ok = true
		}
	}
	return found, ok
}

// collectProjectScreens turns each confirmed screen draft into a portable
// screen. A draft that cannot be represented, or that carries a credential, is
// refused: the package never ships a page the target could not rebuild or must
// not copy.
func collectProjectScreens(nodes []data.NodeEdge, idMap map[string]string) ([]packageScreen, error) {
	screens := make([]packageScreen, 0, 1)
	for _, node := range nodes {
		point, ok := screenDraftPoint(node.Points)
		if !ok {
			continue
		}
		var draft struct {
			Source string `json:"source"`
		}
		if json.Unmarshal([]byte(point.Txt()), &draft) != nil || strings.TrimSpace(draft.Source) == "" {
			return nil, fmt.Errorf("missing_screen:%s", nodeName(node))
		}
		if hits := deepseek.ScanCredentials("screen", "source", draft.Source); len(hits) > 0 {
			return nil, fmt.Errorf("screen_contains_secret:%s", nodeName(node))
		}
		source, bindings, unresolved := packageScreenBindings(genscreen.Parse(draft.Source).FindBindings(), draft.Source, idMap)
		if len(unresolved) > 0 {
			return nil, fmt.Errorf("missing_screen_binding:%s", idMap[node.ID])
		}
		screens = append(screens, packageScreen{
			Node: idMap[node.ID], Source: source, Bindings: bindings,
			Artifact: genscreen.Fingerprint(source), Build: genscreen.BuildFor(source),
		})
	}
	sort.Slice(screens, func(i, j int) bool { return screens[i].Node < screens[j].Node })
	return screens, nil
}

// packageScreenBindings replaces the source instance's identifiers with stable
// tokens assigned by first appearance, so the same screen exports
// byte-identically from any deployment. A declared binding to an object
// outside the package still becomes a token, but has no ref in the map: the
// target shows it as unbound instead of silently pointing at an old site.
func packageScreenBindings(bindings []genscreen.Binding, source string, idMap map[string]string) (string, map[string]string, []string) {
	mapping := map[string]string{}
	tokens := map[string]string{}
	unresolved := []string{}
	var out strings.Builder
	cursor := 0
	for _, binding := range bindings {
		if binding.Start < cursor {
			continue
		}
		token, seen := mapping[binding.ID]
		ref, inPackage := idMap[binding.ID]
		if !seen {
			// 脚本文本里与点位无关的字面量原样保留。
			if !inPackage && !binding.Declared {
				continue
			}
			token = fmt.Sprintf("id%d", len(mapping)+1)
			mapping[binding.ID] = token
		}
		if inPackage {
			tokens[token] = ref
		} else if binding.Declared {
			unresolved = append(unresolved, binding.ID)
		}
		out.WriteString(source[cursor:binding.Start])
		out.WriteString(token)
		cursor = binding.End
	}
	out.WriteString(source[cursor:])
	return out.String(), tokens, unresolved
}

func (h *Handler) exportProjectPackage(identity Identity, siteID string) (projectPackage, error) {
	if siteID == "" {
		return projectPackage{}, fmt.Errorf("site_required")
	}
	if !h.allows(identity, siteID) {
		return projectPackage{}, fmt.Errorf("source_forbidden")
	}
	nodes, strips, warnings, err := h.collectProjectPackageNodes(siteID)
	if err != nil {
		return projectPackage{}, err
	}
	pkg, err := h.packageFromNodes(identity, nodes, strips, warnings)
	if err != nil {
		return projectPackage{}, err
	}
	frozen, err := h.snapshotNodes(identity, siteID, "", nodes)
	if err != nil {
		return projectPackage{}, err
	}
	pkg.SourceFingerprint = engineeringFingerprint(frozen.Nodes, pkg.Dependencies)
	pkg.seal()
	return pkg, pkg.validate()
}

// The caller owns the captured node set; package construction never rereads it.
func (h *Handler) packageFromNodes(identity Identity, nodes []data.NodeEdge, strips []packageStrip, warnings []string) (projectPackage, error) {
	idMap := make(map[string]string, len(nodes))
	for i, node := range nodes {
		if !h.allows(identity, node.ID) {
			return projectPackage{}, fmt.Errorf("source_forbidden")
		}
		idMap[node.ID] = fmt.Sprintf("n%d", i+1)
	}
	operator := identity.Email
	if operator == "" {
		operator = identity.UserID
	}
	pkg := projectPackage{
		Format: projectPackageFormat, FormatVersion: projectPackageFormatVersion,
		ID: "pp-" + data.NewID(), Name: nodeName(nodes[0]), CreatedAt: time.Now().UTC(),
		Operator: operator, Dependencies: []packageDependency{}, Stripped: strips, Warnings: warnings,
	}
	if err := h.validatePackageSource(identity, nodes, idMap); err != nil {
		return projectPackage{}, err
	}
	screens, err := collectProjectScreens(nodes, idMap)
	if err != nil {
		return projectPackage{}, err
	}
	pkg.Screens = screens
	profiles := map[string]bool{}
	dependencies := map[string]packageDependency{}
	for _, node := range nodes {
		points := packagePoints(node, idMap, &pkg.Stripped, &pkg.Warnings)
		parent := ""
		if node.ID != nodes[0].ID {
			parent = idMap[node.Parent]
		}
		pkg.Nodes = append(pkg.Nodes, packageNode{Ref: idMap[node.ID], Type: node.Type, Parent: parent, Points: points})
		for _, point := range points {
			if point.Type == PointTypeProfileID && point.Txt() != "" {
				profiles[point.Txt()] = true
			}
		}
		if node.Type != data.NodeTypeModbus {
			continue
		}
		for _, p := range points {
			if p.Type != unitModelPoint {
				continue
			}
			var model unitModelConfig
			if json.Unmarshal([]byte(p.Txt()), &model) == nil && model.ProfileID != "" {
				profiles[model.ProfileID] = true
			}
		}
	}
	pkg.PointCount, pkg.StrategyCount = packageContentCounts(pkg.Nodes)
	for id := range profiles {
		version, ok := h.profileVersion(id)
		if !ok {
			return projectPackage{}, fmt.Errorf("missing_dependency:profile:%s", id)
		}
		dependencies[dependencyKindProfile+":"+id] = packageDependency{Kind: dependencyKindProfile, ID: id, Version: version}
	}
	if packageNeedsIndustry(nodes) {
		if err := h.packagesOrBundled().Check(industry.EMSPackageID, industry.EMSPackageVersion); err != nil {
			return projectPackage{}, err
		}
		dependencies[dependencyKindIndustry+":"+industry.EMSPackageID] = packageDependency{
			Kind: dependencyKindIndustry, ID: industry.EMSPackageID, Version: industry.EMSPackageVersion,
		}
	}
	pkg.Dependencies = make([]packageDependency, 0, len(dependencies))
	for _, dependency := range dependencies {
		pkg.Dependencies = append(pkg.Dependencies, dependency)
	}
	sort.Slice(pkg.Dependencies, func(i, j int) bool {
		if pkg.Dependencies[i].Kind != pkg.Dependencies[j].Kind {
			return pkg.Dependencies[i].Kind < pkg.Dependencies[j].Kind
		}
		return pkg.Dependencies[i].ID < pkg.Dependencies[j].ID
	})
	// A draft export is never labelled as the latest published version.
	pkg.Version = 1
	pkg.ScreenCount = packageScreenCount(pkg.Nodes, pkg.Screens)
	return pkg, nil
}

func (h *Handler) rollbackImportedNodes(created []data.NodeEdge, identity Identity) {
	for i := len(created) - 1; i >= 0; i-- {
		_ = client.DeleteNode(h.nc, created[i].ID, created[i].Parent, identity.UserID)
	}
}

// packageNeedsIndustry reports whether the exported content is owned by the
// EMS industry package. A project with no energy model pins no industry
// package and stays installable on the bare base product.
func packageNeedsIndustry(nodes []data.NodeEdge) bool {
	for _, node := range nodes {
		if len(industryConfiguration(node)) > 0 {
			return true
		}
	}
	return false
}

func (h *Handler) profileVersion(id string) (string, bool) {
	if h.ems == nil || h.ems.Profiles == nil {
		return "", false
	}
	return h.ems.Profiles.Fingerprint(id)
}

// requireDependencies refuses an import whose dependencies this site cannot
// satisfy at the pinned version. Content that uses a profile or an industry
// model without pinning it is refused too: resolving it at whatever version
// happens to be loaded would silently run a different mapping than the source
// project.
func (h *Handler) requireDependencies(declared []packageDependency, usedProfiles map[string]bool, needsIndustry bool) error {
	pinned := map[string]string{}
	for _, dependency := range declared {
		pinned[dependency.Kind+":"+dependency.ID] = dependency.Version
	}
	undeclared := make([]string, 0, len(usedProfiles)+1)
	for id := range usedProfiles {
		if _, ok := pinned[dependencyKindProfile+":"+id]; !ok {
			undeclared = append(undeclared, id)
		}
	}
	if len(undeclared) > 0 {
		sort.Strings(undeclared)
		return fmt.Errorf("undeclared_dependency:profile:%s", undeclared[0])
	}
	if needsIndustry {
		if _, ok := pinned[dependencyKindIndustry+":"+industry.EMSPackageID]; !ok {
			return fmt.Errorf("undeclared_dependency:industry:%s", industry.EMSPackageID)
		}
	}
	for _, dependency := range declared {
		switch dependency.Kind {
		case dependencyKindIndustry:
			if err := h.packagesOrBundled().Check(dependency.ID, dependency.Version); err != nil {
				return err
			}
		case dependencyKindProfile:
			if h.ems == nil || h.ems.Profiles == nil {
				return fmt.Errorf("profile_registry_unavailable")
			}
			current, ok := h.ems.Profiles.Fingerprint(dependency.ID)
			if !ok {
				return fmt.Errorf("missing_dependency:profile:%s@%s", dependency.ID, dependency.Version)
			}
			if current != dependency.Version {
				return fmt.Errorf("incompatible_dependency:profile:%s:required@%s:current@%s", dependency.ID, dependency.Version, current)
			}
		}
	}
	return nil
}

// importedScreenDraft restores a packaged screen into the target instance: the
// stable tokens become this instance's IDs, the operable target set is derived
// from the imported model, and the package's artifact and build pins stay
// attached as the screen's record. Source-instance evidence (browser checks,
// old node IDs, timestamps) is never carried.
func importedScreenDraft(screen packageScreen, idMap map[string]string, model map[string]genscreen.Point) map[string]any {
	ids := make(map[string]string, len(screen.Bindings))
	tokens := make([]string, 0, len(screen.Bindings))
	for token, ref := range screen.Bindings {
		ids[token] = idMap[ref]
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	var out strings.Builder
	cursor := 0
	for _, binding := range genscreen.Parse(screen.Source).FindBindings() {
		id, known := ids[binding.ID]
		if !known || binding.Start < cursor {
			continue
		}
		out.WriteString(screen.Source[cursor:binding.Start])
		out.WriteString(id)
		cursor = binding.End
	}
	out.WriteString(screen.Source[cursor:])
	points := make([]genscreen.Point, 0, len(tokens))
	for _, token := range tokens {
		points = append(points, model[ids[token]])
	}
	return map[string]any{
		"source": out.String(), "points": points, "report": genscreen.Report{},
		"checked_at": nil, "simulator": "preview", "node_id": idMap[screen.Node],
		"artifact": screen.Artifact, "build": screen.Build,
	}
}

func (h *Handler) importProjectPackage(identity Identity, pkg projectPackage, parent, name string) (importProjectResult, error) {
	if err := pkg.validate(); err != nil {
		return importProjectResult{}, err
	}
	if parent == "" {
		root, err := client.GetRootNode(h.nc)
		if err != nil {
			return importProjectResult{}, fmt.Errorf("target_parent_unavailable")
		}
		parent = root.ID
	}
	if !h.allows(identity, parent) {
		return importProjectResult{}, fmt.Errorf("target_parent_unavailable")
	}
	order, err := orderPackageRefs(pkg.Nodes)
	if err != nil {
		return importProjectResult{}, err
	}
	byRef := make(map[string]packageNode, len(pkg.Nodes))
	screensByRef := make(map[string]packageScreen, len(pkg.Screens))
	siteRef := ""
	for _, node := range pkg.Nodes {
		byRef[node.Ref] = node
		if node.Type == NodeTypeSite && node.Parent == "" {
			siteRef = node.Ref
		}
	}
	// 画面：构建输入必须与本软件携带的一致，产物必须由这份源码、这些输入
	// 构建出来，源码不得含凭据。任一项对不上就拒绝，不静默降级。
	for _, screen := range pkg.Screens {
		screensByRef[screen.Node] = screen
		if hits := deepseek.ScanCredentials("screen", "source", screen.Source); len(hits) > 0 {
			return importProjectResult{}, fmt.Errorf("screen_contains_secret:%s", screen.Node)
		}
		if screen.Build != genscreen.BuildFor(screen.Source) {
			return importProjectResult{}, fmt.Errorf("screen_build_mismatch:%s", screen.Node)
		}
		if genscreen.Fingerprint(screen.Source) != screen.Artifact {
			return importProjectResult{}, fmt.Errorf("screen_artifact_mismatch:%s", screen.Node)
		}
	}

	// 校验与清洗都在创建前完成：缺依赖不会留下半成品节点。
	idMap := make(map[string]string, len(pkg.Nodes))
	for _, node := range pkg.Nodes {
		idMap[node.Ref] = data.NewID()
	}
	projectName := strings.TrimSpace(name)
	if projectName == "" {
		projectName = strings.TrimSpace(pkg.Name)
	}
	if projectName == "" {
		projectName = "导入项目"
	}
	profilesUsed := map[string]bool{}
	ignored := []packageStrip{}
	warnings := []string{}
	prepared := make([]data.NodeEdge, 0, len(order))
	for _, ref := range order {
		packaged := byRef[ref]
		if _, carried := screenDraftPoint(packaged.Points); carried {
			// 画面必须随 screens 条目携带源码、绑定与构建产物；只有点位里
			// 一份草稿的包没有可复现的产物，按缺画面拒绝。
			if _, ok := screensByRef[ref]; !ok {
				return importProjectResult{}, fmt.Errorf("missing_screen:%s", ref)
			}
		}
		source := data.NodeEdge{ID: idMap[ref], Type: packaged.Type, Points: packaged.Points}
		points := packagePoints(source, idMap, &ignored, &warnings)
		if packaged.Type == data.NodeTypeModbus {
			for _, p := range points {
				if p.Type != unitModelPoint {
					continue
				}
				var model unitModelConfig
				if json.Unmarshal([]byte(p.Txt()), &model) == nil && model.ProfileID != "" {
					profilesUsed[model.ProfileID] = true
				}
			}
		}
		node := data.NodeEdge{ID: idMap[ref], Type: packaged.Type, Parent: parent, Points: points}
		if packaged.Parent != "" {
			node.Parent = idMap[packaged.Parent]
		}
		if ref == siteRef {
			node.Points = setDescription(node.Points, projectName)
		}
		if packaged.Type == NodeTypeEnergyUnit {
			// 新站从待机开始；控制启动必须由交付人员显式操作。
			node.Points = append(node.Points, data.NewPointString(PointTypeCabinetState, "0", string(decision.CabinetStandby)))
		}
		prepared = append(prepared, node)
	}
	model := make(map[string]genscreen.Point, len(prepared))
	preparedAt := make(map[string]int, len(prepared))
	for i, node := range prepared {
		model[node.ID] = generatedScreenPoint(node)
		preparedAt[node.ID] = i
	}
	for _, screen := range pkg.Screens {
		encoded, err := json.Marshal(importedScreenDraft(screen, idMap, model))
		if err != nil {
			return importProjectResult{}, fmt.Errorf("screen_encode_failed")
		}
		at := preparedAt[idMap[screen.Node]]
		prepared[at].Points = append(prepared[at].Points, data.NewPointString(generatedScreenDraftType, "", string(encoded)))
	}
	if len(pkg.Screens) > 0 {
		warnings = append(warnings, "画面已随工程导入；本实例尚未运行运行时与布局检查")
	}
	if err := h.requireDependencies(pkg.Dependencies, profilesUsed, packageNeedsIndustry(prepared)); err != nil {
		return importProjectResult{}, err
	}

	created := make([]data.NodeEdge, 0, len(prepared))
	now := time.Now()
	for _, node := range prepared {
		points := make(data.Points, 0, len(node.Points))
		for _, p := range node.Points {
			if p.Tombstone%2 != 0 {
				continue
			}
			p.Time = now
			p.Origin = identity.UserID
			points = append(points, p)
		}
		node.Points = points
		if err := client.SendNode(h.nc, node, identity.UserID); err != nil {
			h.rollbackImportedNodes(created, identity)
			return importProjectResult{}, fmt.Errorf("import_failed:%s", node.Type)
		}
		created = append(created, node)
	}

	result := importProjectResult{
		SiteID: idMap[siteRef], Name: projectName, Created: len(created),
		Profiles: make([]string, 0, len(profilesUsed)), NodeIDs: make([]string, 0, len(created)), Ignored: ignored, Warnings: warnings,
	}
	for id := range profilesUsed {
		result.Profiles = append(result.Profiles, id)
	}
	sort.Strings(result.Profiles)
	for _, node := range prepared {
		result.NodeIDs = append(result.NodeIDs, node.ID)
		switch node.Type {
		case data.NodeTypeModbus:
			result.Channels++
		case "decisionTable":
			result.Rules++
		default:
			result.Model++
		}
	}
	// 导入即成为可版本化的项目：用既有项目版本机制冻结基线，便于复用与回退。
	if version, err := h.snapshotSite(identity, result.SiteID, "由项目包导入"); err == nil {
		if err := h.saveVersion(version); err == nil {
			result.VersionID = version.ID
		} else {
			addPackageWarning(&result.Warnings, "导入成功，但基线版本保存失败")
		}
	} else {
		addPackageWarning(&result.Warnings, "导入成功，但基线版本冻结失败")
	}
	if h.audit != nil {
		actor := identity.Email
		if actor == "" {
			actor = identity.UserID
		}
		_ = h.audit.Append(audit.Record{
			Time: time.Now(), Actor: actor, Action: projectImportAction, Object: result.SiteID,
			Detail: fmt.Sprintf("%s · %d 个节点 · 通道 %d · 规则 %d", projectName, result.Created, result.Channels, result.Rules),
			Result: "success",
		})
	}
	return result, nil
}

func (h *Handler) exportProjectHandler(w http.ResponseWriter, identity Identity, siteID string, versionID ...string) {
	var pkg projectPackage
	var err error
	if len(versionID) > 0 && versionID[0] != "" {
		pkg, err = h.frozenProjectPackage(identity, siteID, versionID[0])
	} else {
		pkg, err = h.exportProjectPackage(identity, siteID)
	}
	if err != nil {
		status := http.StatusBadRequest
		if err.Error() == "source_forbidden" {
			status = http.StatusForbidden
		}
		writeError(w, status, err.Error())
		return
	}
	if h.audit != nil {
		actor := identity.Email
		if actor == "" {
			actor = identity.UserID
		}
		_ = h.audit.Append(audit.Record{
			Time: time.Now(), Actor: actor, Action: projectExportAction, Object: pkg.ID,
			Detail: fmt.Sprintf("%s · %d 个节点 · 剥离 %d 项", pkg.Name, len(pkg.Nodes), len(pkg.Stripped)),
			Result: "success",
		})
	}
	w.Header().Set("Content-Disposition", `attachment; filename="project.package.json"`)
	writeJSON(w, pkg)
}

func (h *Handler) importProjectHandler(w http.ResponseWriter, r *http.Request, identity Identity) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	var input importProjectRequest
	if err := json.Unmarshal(raw, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if len(input.Package) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_package")
		return
	}
	var pkg projectPackage
	if err := json.Unmarshal(input.Package, &pkg); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_package")
		return
	}
	result, err := h.importProjectPackage(identity, pkg, input.Parent, input.Name)
	if err != nil {
		status := http.StatusBadRequest
		if err.Error() == "target_parent_unavailable" {
			status = http.StatusForbidden
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, result)
}
