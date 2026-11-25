package appapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/audit"
	"fastiot/internal/industry"
)

// 升级：把旧基线（冻结版本）、现场定制（当前工程配置）与新模板（项目包）做三方对照，
// 只写入无冲突项。冲突由人员逐项选择保留现场或采用新版，未解决前不部署，不静默覆盖。
// 不建设自动合并引擎：每一处双方都改过的参数都原样列出，由人决定。
//
// 运行参数（96 点计划、站点/电站目标、分时策略）不参与合并：升级不重写现场正在使用的值。
// 但新模板必须仍具备解释这些参数的能力（EMS 行业包），否则升级会留下无人解释的运行目标，
// 因此阻止升级并点名参数。
//
// 升级是版本级操作，与离线灾难恢复（整体替换数据目录）不是同一件事：回退只重放工程配置，
// 升级期间新增的报警、命令与审计历史保留不删。

const (
	projectUpgradeAction = "项目升级"

	upgradeResolutionLocal    = "local"
	upgradeResolutionTemplate = "template"

	upgradeNodeMatched           = "matched"
	upgradeNodeAddedByTemplate   = "added_by_template"
	upgradeNodeSiteOnly          = "site_only"
	upgradeNodeRemovedByTemplate = "removed_by_template"
	upgradeNodeDeletedLocally    = "deleted_locally"

	upgradePointTemplateChange  = "template_change"
	upgradePointTemplateAdd     = "template_add"
	upgradePointSiteChange      = "site_change"
	upgradePointSiteOnly        = "site_only"
	upgradePointSiteRemoved     = "site_removed"
	upgradePointTemplateRemoved = "template_removed"
	upgradePointConflict        = "conflict"

	upgradeConflictBothChanged    = "both_changed"
	upgradeConflictBothAdded      = "both_added"
	upgradeConflictDeletedLocally = "deleted_locally"
	upgradeConflictSiteRemovedTpl = "removed_locally_template_changed"

	upgradeIncompatibleRuntime    = "runtime_parameter"
	upgradeIncompatibleDependency = "dependency"
)

// upgradeRuntimePoints 是现场人员按范围调整的运行参数：不参与三方合并，也不被升级重写。
var upgradeRuntimePoints = map[string]bool{
	plan96Point:          true,
	plantStrategyPoint:   true,
	PointTypeSiteTarget:  true,
	PointTypePlantTarget: true,
}

type upgradeRequest struct {
	Site        string            `json:"site"`
	Base        string            `json:"base,omitempty"`
	Package     json.RawMessage   `json:"package"`
	Resolutions map[string]string `json:"resolutions,omitempty"`
}

type upgradeBaseline struct {
	Version   string    `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	Operator  string    `json:"operator"`
	Note      string    `json:"note,omitempty"`
}

type upgradeTemplateInfo struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	Version       int                 `json:"version"`
	CreatedAt     time.Time           `json:"created_at"`
	Operator      string              `json:"operator"`
	Dependencies  []packageDependency `json:"dependencies"`
	SourceVersion string              `json:"source_version,omitempty"`
}

type upgradeSummary struct {
	Changed      int `json:"changed"`
	Added        int `json:"added"`
	SiteOnly     int `json:"site_only"`
	Conflicts    int `json:"conflicts"`
	Incompatible int `json:"incompatible"`
}

type upgradePointDiff struct {
	Point    string `json:"point"`
	Key      string `json:"key,omitempty"`
	Status   string `json:"status"`
	Base     string `json:"base,omitempty"`
	Local    string `json:"local,omitempty"`
	Template string `json:"template,omitempty"`
	Conflict string `json:"conflict,omitempty"`
}

type upgradeNodeDiff struct {
	Key      string             `json:"key"`
	Name     string             `json:"name"`
	Type     string             `json:"type"`
	Status   string             `json:"status"`
	Points   []upgradePointDiff `json:"points"`
	Conflict string             `json:"conflict,omitempty"`
}

type upgradeConflict struct {
	ID       string `json:"id"`
	Node     string `json:"node"`
	Point    string `json:"point,omitempty"`
	Reason   string `json:"reason"`
	Base     string `json:"base,omitempty"`
	Local    string `json:"local,omitempty"`
	Template string `json:"template,omitempty"`
}

type upgradeIncompatible struct {
	Kind   string `json:"kind"`
	Object string `json:"object,omitempty"`
	Detail string `json:"detail"`
}

type upgradePreview struct {
	Site         string                `json:"site"`
	SiteName     string                `json:"site_name"`
	Baseline     upgradeBaseline       `json:"baseline"`
	Template     upgradeTemplateInfo   `json:"template"`
	Summary      upgradeSummary        `json:"summary"`
	Nodes        []upgradeNodeDiff     `json:"nodes"`
	Conflicts    []upgradeConflict     `json:"conflicts"`
	Incompatible []upgradeIncompatible `json:"incompatible"`
	Blocked      bool                  `json:"blocked"`
	Blockers     []string              `json:"blockers"`
	Warnings     []string              `json:"warnings,omitempty"`
	Note         string                `json:"note"`
}

type upgradeResult struct {
	Site      string   `json:"site"`
	Base      string   `json:"base"`
	Template  string   `json:"template"`
	Updated   int      `json:"updated"`
	Created   int      `json:"created"`
	Kept      int      `json:"kept"`
	Conflicts int      `json:"conflicts_resolved"`
	Before    string   `json:"before_version"`
	Version   string   `json:"version"`
	Preflight string   `json:"preflight_status,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
}

type upgradeNodeView struct {
	ID     string
	Parent string
	Type   string
	Name   string
	Key    string
	Points map[string]data.Point
}

type upgradeNodeAction struct {
	LocalID string
	Points  data.Points
	Choices map[string]data.Point
}

type upgradeCreate struct {
	ID       string
	Type     string
	Parent   string
	Points   data.Points
	Conflict string
}

type upgradePlan struct {
	siteID     string
	base       projectVersion
	pkg        projectPackage
	preview    upgradePreview
	actions    []*upgradeNodeAction
	creates    []upgradeCreate
	conflicts  map[string]upgradeConflict
	localIDs   map[string]bool
	rootParent string
}

func upgradePointKey(point data.Point) string {
	key := point.Key
	if key == "" {
		key = "0"
	}
	return point.Type + "\x00" + key
}

func upgradePointLabel(point data.Point) string {
	if point.Key == "" || point.Key == "0" {
		return point.Type
	}
	return point.Type + "[" + point.Key + "]"
}

func upgradeDiffKey(point data.Point) string {
	if point.Key == "0" {
		return ""
	}
	return point.Key
}

func upgradePointText(point data.Point) string {
	text := point.Txt()
	if text == "" && point.Numeric() {
		text = strconv.FormatFloat(point.Val(), 'g', -1, 64)
	}
	if text == "" && len(point.Data) > 0 {
		text = fmt.Sprintf("<%d 字节>", len(point.Data))
	}
	if len(text) > 320 {
		text = text[:320] + "…"
	}
	return text
}

func sameUpgradePoint(a, b data.Point) bool {
	if a.DataType != b.DataType || len(a.Data) != len(b.Data) {
		return false
	}
	for i := range a.Data {
		if a.Data[i] != b.Data[i] {
			return false
		}
	}
	return true
}

// upgradeEngineeringPoints 只取工程配置点：现场凭据、连接地址、运行状态与运行记录都不是
// 模板与现场之间的可对照内容。空 key 统一成存储使用的默认 key，模板点才能与现场点对上。
func upgradeEngineeringPoints(node data.NodeEdge) map[string]data.Point {
	points := make(map[string]data.Point, len(node.Points))
	for _, point := range node.Points {
		if point.Tombstone%2 != 0 || packageStripReason(node, point) != "" {
			continue
		}
		if point.Key == "" {
			point.Key = "0"
		}
		points[upgradePointKey(point)] = point
	}
	return points
}

func upgradeNodeName(points map[string]data.Point) string {
	if point, ok := points[upgradePointKey(data.Point{Type: data.PointTypeDescription})]; ok {
		return point.Txt()
	}
	return ""
}

func upgradeView(nodes []data.NodeEdge) []*upgradeNodeView {
	views := make([]*upgradeNodeView, 0, len(nodes))
	for _, node := range nodes {
		points := upgradeEngineeringPoints(node)
		name := upgradeNodeName(points)
		if name == "" {
			name = nodeName(node)
		}
		views = append(views, &upgradeNodeView{
			ID: node.ID, Parent: node.Parent, Type: node.Type, Name: name, Points: points,
		})
	}
	assignUpgradeKeys(views)
	return views
}

func assignUpgradeKeys(views []*upgradeNodeView) {
	byID := make(map[string]*upgradeNodeView, len(views))
	for _, view := range views {
		byID[view.ID] = view
	}
	var walk func(view *upgradeNodeView, depth int) string
	walk = func(view *upgradeNodeView, depth int) string {
		segment := view.Type + ":" + view.Name
		parent, ok := byID[view.Parent]
		if !ok || parent == view || depth > 32 {
			return segment
		}
		return walk(parent, depth+1) + "/" + segment
	}
	ordered := append([]*upgradeNodeView(nil), views...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Type != ordered[j].Type {
			return ordered[i].Type < ordered[j].Type
		}
		if ordered[i].Name != ordered[j].Name {
			return ordered[i].Name < ordered[j].Name
		}
		return ordered[i].ID < ordered[j].ID
	})
	seen := map[string]int{}
	for _, view := range ordered {
		base := walk(view, 0)
		seen[base]++
		view.Key = base
		if count := seen[base]; count > 1 {
			view.Key = base + "#" + strconv.Itoa(count)
		}
	}
}

func upgradePointMap(view *upgradeNodeView) map[string]data.Point {
	if view == nil {
		return map[string]data.Point{}
	}
	return view.Points
}

func sortedUpgradePointKeys(maps ...map[string]data.Point) []string {
	seen := map[string]bool{}
	keys := make([]string, 0)
	for _, points := range maps {
		for key := range points {
			if seen[key] {
				continue
			}
			seen[key] = true
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func diffUpgradePoints(local, base, template *upgradeNodeView, nodeKey, nodeName string) ([]upgradePointDiff, []upgradeConflict, *upgradeNodeAction) {
	localPoints, basePoints, templatePoints := upgradePointMap(local), upgradePointMap(base), upgradePointMap(template)
	action := &upgradeNodeAction{Choices: map[string]data.Point{}}
	diffs := []upgradePointDiff{}
	conflicts := []upgradeConflict{}
	addConflict := func(point data.Point, reason string) string {
		id := nodeKey + "||" + upgradePointKey(point)
		conflicts = append(conflicts, upgradeConflict{
			ID: id, Node: nodeName, Point: upgradePointLabel(point), Reason: reason,
			Base:     upgradePointText(basePoints[upgradePointKey(point)]),
			Local:    upgradePointText(localPoints[upgradePointKey(point)]),
			Template: upgradePointText(templatePoints[upgradePointKey(point)]),
		})
		return id
	}
	for _, key := range sortedUpgradePointKeys(localPoints, basePoints, templatePoints) {
		point := templatePoints[key]
		if _, ok := templatePoints[key]; !ok {
			point = localPoints[key]
		}
		if upgradeRuntimePoints[point.Type] {
			continue
		}
		b, hasBase := basePoints[key]
		l, hasLocal := localPoints[key]
		t, hasTemplate := templatePoints[key]
		diff := upgradePointDiff{Point: point.Type, Key: upgradeDiffKey(point)}
		switch {
		case hasTemplate && hasLocal && sameUpgradePoint(l, t):
			// 现场与新模板已一致，升级没有需要写入的差异。
			continue
		case hasTemplate && hasLocal && hasBase && sameUpgradePoint(l, b):
			diff.Status = upgradePointTemplateChange
			action.Points = append(action.Points, t)
		case hasTemplate && hasLocal && hasBase && sameUpgradePoint(t, b):
			diff.Status = upgradePointSiteChange
		case hasTemplate && hasLocal:
			reason := upgradeConflictBothChanged
			if !hasBase {
				reason = upgradeConflictBothAdded
			}
			diff.Status = upgradePointConflict
			diff.Conflict = addConflict(t, reason)
			action.Choices[diff.Conflict] = t
		case hasTemplate && !hasLocal && hasBase:
			if sameUpgradePoint(t, b) {
				diff.Status = upgradePointSiteRemoved
			} else {
				diff.Status = upgradePointConflict
				diff.Conflict = addConflict(t, upgradeConflictSiteRemovedTpl)
				action.Choices[diff.Conflict] = t
			}
		case hasTemplate && !hasLocal:
			diff.Status = upgradePointTemplateAdd
			action.Points = append(action.Points, t)
		case hasLocal && !hasBase:
			diff.Status = upgradePointSiteOnly
		case hasLocal && hasBase:
			diff.Status = upgradePointTemplateRemoved
		default:
			continue
		}
		diff.Base = upgradePointText(b)
		diff.Local = upgradePointText(l)
		diff.Template = upgradePointText(t)
		diffs = append(diffs, diff)
	}
	return diffs, conflicts, action
}

func upgradeSiteOnlyPoints(view *upgradeNodeView, base *upgradeNodeView, status string) []upgradePointDiff {
	if view == nil {
		return nil
	}
	basePoints := upgradePointMap(base)
	keys := sortedUpgradePointKeys(view.Points)
	diffs := make([]upgradePointDiff, 0, len(keys))
	for _, key := range keys {
		point := view.Points[key]
		if upgradeRuntimePoints[point.Type] {
			continue
		}
		diffs = append(diffs, upgradePointDiff{
			Point: point.Type, Key: upgradeDiffKey(point), Status: status,
			Base: upgradePointText(basePoints[key]), Local: upgradePointText(point),
		})
	}
	return diffs
}

func upgradeRuntimeParameterChecks(nodes []data.NodeEdge, templateHasEMS bool) []upgradeIncompatible {
	checks := []upgradeIncompatible{}
	if templateHasEMS {
		return checks
	}
	// 运行参数从现场节点原样读取：它们被项目包剥离，不在三方对照的工程点集合里。
	flag := func(node data.NodeEdge, pointType, label string) {
		checks = append(checks, upgradeIncompatible{
			Kind: upgradeIncompatibleRuntime, Object: nodeName(node),
			Detail: fmt.Sprintf("%s（%s：%s）仍在使用，但新模板未声明 EMS 行业包（industry:%s）；升级后该参数没有可解释它的能力，不能盲目沿用。请先换成包含 EMS 的模板，或清除该运行参数后重试。",
				label, nodeName(node), pointType, industry.EMSPackageID),
		})
	}
	for _, node := range nodes {
		switch node.Type {
		case NodeTypeSite:
			if plan, found := loadPlan96(node); found {
				for _, slot := range plan.Slots {
					if slot != nil {
						flag(node, plan96Point, "96 点计划")
						break
					}
				}
			}
			if value, ok := node.Points.Value(PointTypeSiteTarget, ""); ok && value != 0 {
				flag(node, PointTypeSiteTarget, "站点总目标")
			}
		case NodeTypePlant:
			if value, ok := node.Points.Value(PointTypePlantTarget, ""); ok && value != 0 {
				flag(node, PointTypePlantTarget, "电站总目标")
			}
			if point, ok := node.Points.Find(plantStrategyPoint, ""); ok && point.Txt() != "" {
				flag(node, plantStrategyPoint, "电站分时策略")
			}
		}
	}
	return checks
}

func (h *Handler) planProjectUpgrade(identity Identity, siteID, baseID string, pkg projectPackage) (*upgradePlan, error) {
	if siteID == "" {
		return nil, fmt.Errorf("site_required")
	}
	if !h.allows(identity, siteID) {
		return nil, fmt.Errorf("source_forbidden")
	}
	if err := pkg.validate(); err != nil {
		return nil, err
	}
	if baseID == "" {
		versions, err := h.listVersions(siteID)
		if err != nil {
			return nil, err
		}
		if len(versions) == 0 {
			return nil, fmt.Errorf("baseline_required")
		}
		baseID = versions[0].ID
	}
	base, err := h.loadVersion(siteID, baseID)
	if err != nil || base.SiteID != siteID {
		return nil, fmt.Errorf("baseline_not_found")
	}

	localNodes, _, _, err := h.collectProjectPackageNodes(siteID)
	if err != nil {
		return nil, err
	}
	localViews := upgradeView(localNodes)
	baseViews := upgradeView(versionNodeEdges(base))
	templateViews := upgradeView(packageNodeEdges(pkg))
	// 计算链与虚拟输入有各自的草稿/应用生命周期，按点重放会绕过它；与版本回退一致，直接拒绝。
	for _, view := range append(append([]*upgradeNodeView{}, localViews...), templateViews...) {
		if view.Type == calculationType {
			return nil, fmt.Errorf("calculation_upgrade_unsupported")
		}
		if view.Type == virtualInputType {
			return nil, fmt.Errorf("virtual_input_upgrade_unsupported")
		}
	}

	localByID := map[string]*upgradeNodeView{}
	localByKey := map[string]*upgradeNodeView{}
	for _, view := range localViews {
		localByID[view.ID] = view
		localByKey[view.Key] = view
	}
	baseByID := map[string]*upgradeNodeView{}
	baseByKey := map[string]*upgradeNodeView{}
	for _, view := range baseViews {
		baseByID[view.ID] = view
		baseByKey[view.Key] = view
	}
	templateByRef := map[string]*upgradeNodeView{}
	for _, view := range templateViews {
		templateByRef[view.ID] = view
	}

	// 1) 名字与类型一致即同一对象：现场与模板的节点 ID 不同，不能按 ID 对。
	matchOfTemplate := map[string]*upgradeNodeView{}
	templateOfLocal := map[string]*upgradeNodeView{}
	for _, view := range templateViews {
		local := localByKey[view.Key]
		if local == nil || templateOfLocal[local.ID] != nil {
			continue
		}
		matchOfTemplate[view.ID] = local
		templateOfLocal[local.ID] = view
	}

	// 模板描述的必须是同一个站点：站点根对不上时，整个模板属于另一个项目，
	// 不能把它的内容当成"新模板新增"而另建一棵站点树。
	siteMatched := false
	for _, view := range templateViews {
		if view.Type == NodeTypeSite && view.Parent == "" {
			siteMatched = matchOfTemplate[view.ID] != nil
		}
	}
	if !siteMatched {
		return nil, fmt.Errorf("template_site_unmatched")
	}

	// 2) 改名不猜测：现场或模板改了对象名后，两侧同样类型同样父节点的对象可能就是同一个，
	//    也可能是删一个、建一个。这里不做启发式配对——改名会显示成"现场保留的对象"与
	//    "新模板新增的对象"，由人看到并自行处理，不会被静默当成同一对象合并。

	// 3) 引用改写：模板里的 ref 换成现场的节点 ID；需要新建的对象先分配新 ID。
	refMap := map[string]string{}
	newIDs := map[string]string{}
	for _, view := range templateViews {
		if local := matchOfTemplate[view.ID]; local != nil {
			refMap[view.ID] = local.ID
			continue
		}
		id := data.NewID()
		refMap[view.ID] = id
		newIDs[view.ID] = id
	}
	rewrittenWarnings := []string{}
	for _, view := range templateViews {
		node := data.NodeEdge{ID: view.ID, Type: view.Type, Points: sortedPoints(view.Points)}
		points := packagePoints(node, refMap, nil, &rewrittenWarnings)
		byKey := make(map[string]data.Point, len(points))
		for _, point := range points {
			if upgradeRuntimePoints[point.Type] {
				addPackageWarning(&rewrittenWarnings, fmt.Sprintf("新模板中「%s」的 %s 属于运行参数，升级不写入现场", view.Name, point.Type))
				continue
			}
			byKey[upgradePointKey(point)] = point
		}
		view.Points = byKey
	}

	plan := &upgradePlan{
		siteID: siteID, base: base, pkg: pkg,
		conflicts: map[string]upgradeConflict{}, localIDs: map[string]bool{},
	}
	for _, view := range localViews {
		plan.localIDs[view.ID] = true
		if view.Type == NodeTypeSite {
			plan.rootParent = view.Parent
		}
	}
	plan.preview = upgradePreview{
		Site: siteID, SiteName: base.SiteName,
		Baseline: upgradeBaseline{Version: base.ID, CreatedAt: base.CreatedAt, Operator: base.Operator, Note: base.Note},
		Template: upgradeTemplateInfo{
			ID: pkg.ID, Name: pkg.Name, Version: pkg.Version, CreatedAt: pkg.CreatedAt,
			Operator: pkg.Operator, Dependencies: pkg.Dependencies, SourceVersion: pkg.SourceVersion,
		},
		Nodes: []upgradeNodeDiff{}, Conflicts: []upgradeConflict{},
		Incompatible: []upgradeIncompatible{}, Blockers: []string{},
		Note: "升级是版本级操作：只重放工程配置，报警、命令与审计历史保留；整目录灾难恢复是另一个操作，恢复会替换数据快照。",
	}
	recordDiffs := func(node upgradeNodeDiff) {
		plan.preview.Nodes = append(plan.preview.Nodes, node)
		recorded := map[string]bool{}
		if node.Conflict != "" {
			if conflict, ok := plan.conflicts[node.Conflict]; ok {
				plan.preview.Conflicts = append(plan.preview.Conflicts, conflict)
				recorded[conflict.ID] = true
			}
		}
		for _, point := range node.Points {
			if point.Conflict == "" || recorded[point.Conflict] {
				continue
			}
			if conflict, ok := plan.conflicts[point.Conflict]; ok {
				plan.preview.Conflicts = append(plan.preview.Conflicts, conflict)
				recorded[conflict.ID] = true
			}
		}
	}

	// 4) 有基线的现场节点逐个对照；现场独有的节点保留并列出。
	for _, local := range localViews {
		baseView := baseByID[local.ID]
		templateView := templateOfLocal[local.ID]
		node := upgradeNodeDiff{Key: local.Key, Name: local.Name, Type: local.Type, Status: upgradeNodeMatched}
		var action *upgradeNodeAction
		if templateView == nil {
			if baseView != nil {
				node.Status = upgradeNodeRemovedByTemplate
				node.Points = upgradeSiteOnlyPoints(local, baseView, upgradePointTemplateRemoved)
			} else {
				node.Status = upgradeNodeSiteOnly
				node.Points = upgradeSiteOnlyPoints(local, nil, upgradePointSiteOnly)
			}
			plan.preview.Summary.SiteOnly++
			recordDiffs(node)
			continue
		}
		diffs, conflicts, nodeAction := diffUpgradePoints(local, baseView, templateView, local.Key, local.Name)
		node.Points = diffs
		nodeAction.LocalID = local.ID
		action = nodeAction
		for _, conflict := range conflicts {
			plan.conflicts[conflict.ID] = conflict
			plan.preview.Blockers = append(plan.preview.Blockers, "conflict:"+conflict.ID)
		}
		for _, point := range diffs {
			switch point.Status {
			case upgradePointTemplateChange:
				plan.preview.Summary.Changed++
			case upgradePointTemplateAdd:
				plan.preview.Summary.Added++
			}
		}
		if len(action.Points) > 0 || len(action.Choices) > 0 {
			plan.actions = append(plan.actions, action)
		}
		recordDiffs(node)
	}

	// 5) 新模板独有的节点：现场已删掉的算冲突，其余新建。
	packageOrder, _ := orderPackageRefs(pkg.Nodes)
	for _, ref := range packageOrder {
		view := templateByRef[ref]
		if view == nil {
			continue
		}
		if matchOfTemplate[ref] != nil {
			continue
		}
		node := upgradeNodeDiff{Key: view.Key, Name: view.Name, Type: view.Type, Status: upgradeNodeAddedByTemplate}
		if baseView := baseByKey[view.Key]; baseView != nil {
			node.Status = upgradeNodeDeletedLocally
			node.Conflict = view.Key + "||node"
			plan.conflicts[node.Conflict] = upgradeConflict{
				ID: node.Conflict, Node: view.Name, Reason: upgradeConflictDeletedLocally,
				Base: "存在", Local: "现场已删除", Template: "存在",
			}
			plan.preview.Blockers = append(plan.preview.Blockers, "conflict:"+node.Conflict)
		}
		for _, point := range view.Points {
			node.Points = append(node.Points, upgradePointDiff{
				Point: point.Type, Key: upgradeDiffKey(point), Status: upgradePointTemplateAdd, Template: upgradePointText(point),
			})
			plan.preview.Summary.Added++
		}
		parent := refMap[view.Parent]
		if view.Parent == "" {
			// 根级的通道/规则在模板里没有父引用；挂到本实例根节点，而不是空父级。
			parent = plan.rootParent
		}
		create := upgradeCreate{ID: newIDs[ref], Type: view.Type, Parent: parent, Points: sortedPoints(view.Points), Conflict: node.Conflict}
		plan.creates = append(plan.creates, create)
		recordDiffs(node)
	}

	// 6) 依赖与运行参数：两者不兼容都阻止升级，点名对象与原因。
	profilesUsed := map[string]bool{}
	needsIndustry := false
	for _, view := range templateViews {
		switch view.Type {
		case NodeTypePlant, NodeTypeEnergyUnit, NodeTypeResourceGroup:
			needsIndustry = true
		case data.NodeTypeModbus:
			for _, point := range view.Points {
				if point.Type != unitModelPoint {
					continue
				}
				var model unitModelConfig
				if json.Unmarshal([]byte(point.Txt()), &model) == nil && model.ProfileID != "" {
					profilesUsed[model.ProfileID] = true
				}
			}
		}
	}
	templateHasEMS := false
	for _, dependency := range pkg.Dependencies {
		if dependency.Kind == dependencyKindIndustry && dependency.ID == industry.EMSPackageID {
			templateHasEMS = true
		}
	}
	runtimeChecks := upgradeRuntimeParameterChecks(localNodes, templateHasEMS)
	if err := h.requireDependencies(pkg.Dependencies, profilesUsed, needsIndustry); err != nil {
		plan.preview.Incompatible = append(plan.preview.Incompatible, upgradeIncompatible{
			Kind: upgradeIncompatibleDependency, Detail: err.Error(),
		})
	}
	plan.preview.Incompatible = append(plan.preview.Incompatible, runtimeChecks...)
	seenBlockers := map[string]bool{}
	blockers := plan.preview.Blockers[:0]
	for _, blocker := range plan.preview.Blockers {
		if seenBlockers[blocker] {
			continue
		}
		seenBlockers[blocker] = true
		blockers = append(blockers, blocker)
	}
	for _, check := range plan.preview.Incompatible {
		blocker := check.Kind + ":" + fallback(check.Object, check.Detail)
		if !seenBlockers[blocker] {
			seenBlockers[blocker] = true
			blockers = append(blockers, blocker)
		}
	}
	plan.preview.Blockers = blockers
	plan.preview.Warnings = rewrittenWarnings

	plan.preview.Summary.Conflicts = len(plan.conflicts)
	plan.preview.Summary.Incompatible = len(plan.preview.Incompatible)
	plan.preview.Blocked = len(plan.preview.Blockers) > 0
	return plan, nil
}

func sortedPoints(points map[string]data.Point) data.Points {
	keys := sortedUpgradePointKeys(points)
	out := make(data.Points, 0, len(keys))
	for _, key := range keys {
		out = append(out, points[key])
	}
	return out
}

func versionNodeEdges(version projectVersion) []data.NodeEdge {
	nodes := make([]data.NodeEdge, 0, len(version.Nodes))
	for _, node := range version.Nodes {
		nodes = append(nodes, data.NodeEdge{ID: node.ID, Type: node.Type, Parent: node.Parent, Points: node.Points})
	}
	return nodes
}

func packageNodeEdges(pkg projectPackage) []data.NodeEdge {
	nodes := make([]data.NodeEdge, 0, len(pkg.Nodes))
	for _, node := range pkg.Nodes {
		nodes = append(nodes, data.NodeEdge{ID: node.Ref, Type: node.Type, Parent: node.Parent, Points: node.Points})
	}
	return nodes
}

func (h *Handler) upgradePreviewHandler(w http.ResponseWriter, r *http.Request, identity Identity) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
	var input upgradeRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	pkg, err := decodeUpgradePackage(input.Package)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan, err := h.planProjectUpgrade(identity, input.Site, input.Base, pkg)
	if err != nil {
		writeUpgradeError(w, err)
		return
	}
	writeJSON(w, plan.preview)
}

func (h *Handler) upgradeApplyHandler(w http.ResponseWriter, r *http.Request, identity Identity) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
	var input upgradeRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	pkg, err := decodeUpgradePackage(input.Package)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	for id, choice := range input.Resolutions {
		if choice != upgradeResolutionLocal && choice != upgradeResolutionTemplate {
			writeError(w, http.StatusBadRequest, "invalid_resolution:"+id)
			return
		}
	}
	plan, err := h.planProjectUpgrade(identity, input.Site, input.Base, pkg)
	if err != nil {
		writeUpgradeError(w, err)
		return
	}

	unresolved := 0
	for id := range plan.conflicts {
		if input.Resolutions[id] == "" {
			unresolved++
		}
	}
	runtimeBlocked, dependencyBlocked := false, false
	for _, check := range plan.preview.Incompatible {
		switch check.Kind {
		case upgradeIncompatibleRuntime:
			runtimeBlocked = true
		case upgradeIncompatibleDependency:
			dependencyBlocked = true
		}
	}
	switch {
	case runtimeBlocked:
		writeUpgradeBlocked(w, "runtime_parameter_incompatible", plan.preview)
		return
	case dependencyBlocked:
		writeUpgradeBlocked(w, "dependency_incompatible", plan.preview)
		return
	case unresolved > 0:
		writeUpgradeBlocked(w, "upgrade_blocked", plan.preview)
		return
	}

	result, err := h.applyProjectUpgrade(identity, plan, input.Resolutions)
	if err != nil {
		status := http.StatusBadGateway
		if err.Error() == "version_save_failed" {
			status = http.StatusInternalServerError
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, result)
}

func decodeUpgradePackage(raw json.RawMessage) (projectPackage, error) {
	if len(raw) == 0 {
		return projectPackage{}, fmt.Errorf("invalid_package")
	}
	var pkg projectPackage
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return projectPackage{}, fmt.Errorf("invalid_package")
	}
	return pkg, nil
}

func writeUpgradeError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch err.Error() {
	case "source_forbidden", "site_required":
		status = http.StatusForbidden
	case "calculation_upgrade_unsupported", "virtual_input_upgrade_unsupported":
		status = http.StatusConflict
	}
	writeError(w, status, err.Error())
}

func writeUpgradeBlocked(w http.ResponseWriter, code string, preview upgradePreview) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": code, "blockers": preview.Blockers,
		"conflicts": preview.Conflicts, "incompatible": preview.Incompatible,
	})
}

// applyProjectUpgrade 只写已经决定的内容：无冲突的模板改动、已选择新版的冲突项、
// 新模板新增的节点。写入前先冻结当前配置，写入后再冻结一次作为下一次升级的基线。
func (h *Handler) applyProjectUpgrade(identity Identity, plan *upgradePlan, resolutions map[string]string) (upgradeResult, error) {
	result := upgradeResult{Site: plan.siteID, Base: plan.base.ID, Template: plan.pkg.ID, Warnings: []string{}}
	before, err := h.snapshotSite(identity, plan.siteID, "升级前自动快照")
	if err != nil {
		return result, fmt.Errorf("version_save_failed")
	}
	if err := h.saveVersion(before); err != nil {
		return result, fmt.Errorf("version_save_failed")
	}
	result.Before = before.ID

	createdIDs := map[string]bool{}
	available := map[string]bool{}
	for id := range plan.localIDs {
		available[id] = true
	}
	for _, create := range plan.creates {
		if create.Conflict != "" && resolutions[create.Conflict] != upgradeResolutionTemplate {
			continue
		}
		if create.Parent != "" && !available[create.Parent] {
			addUpgradeWarning(&result.Warnings, fmt.Sprintf("新模板对象「%s」的父节点不在现场，已跳过", create.Type))
			continue
		}
		node := data.NodeEdge{ID: create.ID, Type: create.Type, Parent: create.Parent, Points: create.Points}
		now := time.Now()
		for i := range node.Points {
			node.Points[i].Time = now
			node.Points[i].Origin = identity.UserID
		}
		if err := client.SendNode(h.nc, node, identity.UserID); err != nil {
			h.deleteUpgradeCreates(createdIDs, identity)
			detail := fmt.Sprintf("基线 %s → 模板「%s」(%s)：创建新模板对象失败，已撤销本次新建", plan.base.ID, plan.pkg.Name, plan.pkg.ID)
			_ = h.auditUpgrade(identity, result, "failed", detail)
			return result, fmt.Errorf("upgrade_failed")
		}
		createdIDs[create.ID] = true
		available[create.ID] = true
		result.Created++
	}

	now := time.Now()
	for _, action := range plan.actions {
		points := make(data.Points, 0, len(action.Points)+len(action.Choices))
		points = append(points, action.Points...)
		for id, point := range action.Choices {
			if resolutions[id] == upgradeResolutionTemplate {
				points = append(points, point)
			}
		}
		if len(points) == 0 {
			continue
		}
		for i := range points {
			points[i].Time = now
			points[i].Origin = identity.UserID
		}
		// The new baseline and success response must follow persisted configuration.
		if err := client.SendPoints(h.nc, client.SubjectNodePoints(action.LocalID), points, true); err != nil {
			_ = h.auditUpgrade(identity, result, "failed", "写入工程配置失败，现场可能处于部分升级状态")
			return result, fmt.Errorf("upgrade_failed")
		}
		result.Updated++
	}
	for _, node := range plan.preview.Nodes {
		if node.Status == upgradeNodeSiteOnly || node.Status == upgradeNodeRemovedByTemplate {
			result.Kept++
		}
	}
	for id, choice := range resolutions {
		if plan.conflicts[id].ID != "" && choice == upgradeResolutionTemplate {
			result.Conflicts++
		}
	}

	after, err := h.snapshotSite(identity, plan.siteID, "升级到模板 "+plan.pkg.Name+" 后自动冻结")
	if err == nil {
		if err := h.saveVersion(after); err == nil {
			result.Version = after.ID
		} else {
			addUpgradeWarning(&result.Warnings, "升级已应用，但新基线版本保存失败")
		}
	} else {
		addUpgradeWarning(&result.Warnings, "升级已应用，但新基线冻结失败")
	}
	if report, err := h.buildPreflight(identity, plan.siteID); err == nil {
		result.Preflight = report.Status
	}
	detail := fmt.Sprintf("基线 %s → 模板「%s」(%s)：更新 %d、新建 %d、保留现场 %d、人员裁决 %d 处冲突",
		plan.base.ID, plan.pkg.Name, plan.pkg.ID, result.Updated, result.Created, result.Kept, result.Conflicts)
	_ = h.auditUpgrade(identity, result, "success", detail)
	return result, nil
}

func (h *Handler) deleteUpgradeCreates(created map[string]bool, identity Identity) {
	for id := range created {
		nodes, err := client.GetNodes(h.nc, "all", id, "", false)
		if err != nil || len(nodes) == 0 {
			continue
		}
		_ = client.DeleteNode(h.nc, id, nodes[0].Parent, identity.UserID)
	}
}

func (h *Handler) auditUpgrade(identity Identity, result upgradeResult, status, detail string) error {
	if h.audit == nil {
		return nil
	}
	actor := identity.Email
	if actor == "" {
		actor = identity.UserID
	}
	return h.audit.Append(audit.Record{
		Time: time.Now(), Actor: actor, Action: projectUpgradeAction, Object: result.Site,
		Detail: detail, Result: status,
	})
}

func addUpgradeWarning(warnings *[]string, message string) {
	for _, existing := range *warnings {
		if existing == message {
			return
		}
	}
	*warnings = append(*warnings, message)
}
