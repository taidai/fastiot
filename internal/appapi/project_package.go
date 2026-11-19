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
	projectPackageFormat        = "fastiot.project-package"
	projectPackageFormatVersion = 1
	projectExportAction         = "项目导出"
	projectImportAction         = "项目导入"
)

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

type projectPackage struct {
	Format        string         `json:"format"`
	FormatVersion int            `json:"format_version"`
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Version       int            `json:"version"`
	SourceVersion string         `json:"source_version,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	Operator      string         `json:"operator"`
	Profiles      []string       `json:"profiles"`
	Nodes         []packageNode  `json:"nodes"`
	Stripped      []packageStrip `json:"stripped"`
	Warnings      []string       `json:"warnings"`
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
	Ignored   []packageStrip `json:"ignored"`
	Warnings  []string       `json:"warnings"`
}

// 现场绑定与运行状态不是工程资产：导出与导入都按这张表剥离。
var packageRuntimePoints = map[string]bool{
	PointTypeSiteTarget: true, PointTypePlantTarget: true,
	PointTypeActualPower: true, PointTypePVAvailableKW: true, PointTypePVLimitKW: true,
	PointTypePVForecastKW: true, PointTypeEVMaxLoadKW: true, PointTypeEVCurtailableKW: true,
	PointTypeEVOnlineRatio: true,
}

func packageStripReason(node data.NodeEdge, p data.Point) string {
	switch {
	case isSecretPoint(p.Type):
		return "凭据"
	case reuseRuntimePoints[p.Type]:
		return "运行状态"
	case packageRuntimePoints[p.Type]:
		return "运行目标"
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
	if len(pkg.Nodes) == 0 {
		return fmt.Errorf("invalid_package_structure:empty")
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
	if sites != 1 {
		return fmt.Errorf("invalid_package_structure:site_root")
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
	if node.Type == data.NodeTypeModbus && p.Type == unitModelPoint {
		var model unitModelConfig
		if json.Unmarshal([]byte(p.Txt()), &model) != nil {
			return nil
		}
		refs := []string{model.BusinessNodeID}
		for _, id := range model.PointMap {
			refs = append(refs, id)
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
	}
	out := refs[:0]
	for _, ref := range refs {
		if ref != "" {
			out = append(out, ref)
		}
	}
	return out
}

// packagePoints 把节点配置写成包内点：剥离现场绑定，引用改写成包内 ref。
// 包外引用会被移除并在 warnings 里说明，绝不留下旧 ID 冒充可用。
func packagePoints(node data.NodeEdge, idMap map[string]string, strips *[]packageStrip, warnings *[]string) data.Points {
	points := make(data.Points, 0, len(node.Points))
	for _, p := range node.Points {
		if p.Tombstone%2 != 0 {
			continue
		}
		if reason := packageStripReason(node, p); reason != "" {
			if strips != nil {
				*strips = append(*strips, packageStrip{Node: nodeName(node), Point: p.Type, Reason: reason})
			}
			continue
		}
		if strips != nil {
			switch {
			case node.Type == data.NodeTypeModbus && p.Type == unitModelPoint:
				var model unitModelConfig
				if json.Unmarshal([]byte(p.Txt()), &model) == nil &&
					(model.Verified || model.Verifier != "" || model.VerifiedAt != "" || model.Verification != "" || model.DeviceSerial != "") {
					*strips = append(*strips, packageStrip{Node: nodeName(node), Point: unitModelPoint, Reason: "现场验收信息"})
				}
			case p.Type == capabilityRulePoint:
				var rule decision.CapabilityRule
				if json.Unmarshal([]byte(p.Txt()), &rule) == nil && rule.State != decision.RuleDraft {
					*strips = append(*strips, packageStrip{Node: nodeName(node), Point: capabilityRulePoint, Reason: "控制启动"})
				}
			}
		}
		rewritten, keep, unresolved := rewritePackagePoint(node, p, idMap)
		if len(unresolved) > 0 && warnings != nil {
			addPackageWarning(warnings, fmt.Sprintf("「%s」引用的对象不在项目包内，导入后需重新绑定", nodeName(node)))
		}
		if !keep {
			continue
		}
		rewritten.Time = time.Time{}
		rewritten.Origin = ""
		points = append(points, *rewritten)
	}
	return points
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
			strips = append(strips, packageStrip{Node: nodeName(node), Point: node.Type, Reason: "凭据与账号"})
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
	idMap := make(map[string]string, len(nodes))
	for i, node := range nodes {
		idMap[node.ID] = fmt.Sprintf("n%d", i+1)
	}
	operator := identity.Email
	if operator == "" {
		operator = identity.UserID
	}
	pkg := projectPackage{
		Format: projectPackageFormat, FormatVersion: projectPackageFormatVersion,
		ID: "pp-" + data.NewID(), Name: nodeName(nodes[0]), CreatedAt: time.Now().UTC(),
		Operator: operator, Profiles: []string{}, Stripped: strips, Warnings: warnings,
	}
	profiles := map[string]bool{}
	for _, node := range nodes {
		points := packagePoints(node, idMap, &pkg.Stripped, &pkg.Warnings)
		parent := ""
		if node.ID != nodes[0].ID {
			parent = idMap[node.Parent]
		}
		pkg.Nodes = append(pkg.Nodes, packageNode{Ref: idMap[node.ID], Type: node.Type, Parent: parent, Points: points})
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
	for id := range profiles {
		pkg.Profiles = append(pkg.Profiles, id)
	}
	sort.Strings(pkg.Profiles)
	versions, err := h.listVersions(siteID)
	if err == nil && len(versions) > 0 {
		pkg.SourceVersion = versions[0].ID
	}
	pkg.Version = len(versions) + 1
	return pkg, nil
}

func (h *Handler) rollbackImportedNodes(created []data.NodeEdge, identity Identity) {
	for i := len(created) - 1; i >= 0; i-- {
		_ = client.DeleteNode(h.nc, created[i].ID, created[i].Parent, identity.UserID)
	}
}

func (h *Handler) requireProfileDependencies(declared []string, used map[string]bool) error {
	required := map[string]bool{}
	for _, id := range declared {
		if id != "" {
			required[id] = true
		}
	}
	for id := range used {
		required[id] = true
	}
	if len(required) == 0 {
		return nil
	}
	if h.ems == nil || h.ems.Profiles == nil {
		return fmt.Errorf("profile_registry_unavailable")
	}
	missing := []string{}
	for id := range required {
		if _, ok := h.ems.Profiles.Get(id); !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("missing_dependency:profile:%s", strings.Join(missing, ","))
	}
	return nil
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
	siteRef := ""
	for _, node := range pkg.Nodes {
		byRef[node.Ref] = node
		if node.Type == NodeTypeSite && node.Parent == "" {
			siteRef = node.Ref
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
	if err := h.requireProfileDependencies(pkg.Profiles, profilesUsed); err != nil {
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
		Profiles: make([]string, 0, len(profilesUsed)), Ignored: ignored, Warnings: warnings,
	}
	for id := range profilesUsed {
		result.Profiles = append(result.Profiles, id)
	}
	sort.Strings(result.Profiles)
	for _, node := range prepared {
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

func (h *Handler) exportProjectHandler(w http.ResponseWriter, identity Identity, siteID string) {
	pkg, err := h.exportProjectPackage(identity, siteID)
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
