package appapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"fastiot/internal/decision"
	"fastiot/internal/deepseek"
	domainaccess "fastiot/internal/domain/access"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

// A content summary is computed from exactly the package being inspected, not
// from a second live query. It is not a claim of hardware acceptance.
type releaseContent struct {
	Nodes        int                 `json:"nodes"`
	Points       int                 `json:"points"`
	Strategies   int                 `json:"strategies"`
	Screens      int                 `json:"screens"`
	Dependencies []packageDependency `json:"dependencies"`
}

// SIOT's JSON decoder normalizes legacy/empty point encodings. Hash the same
// representation consumers receive, rather than a pre-wire in-memory variant.
func packageWirePoint(point data.Point) data.Point {
	raw, err := json.Marshal(point)
	if err != nil {
		return point
	}
	var wire data.Point
	if json.Unmarshal(raw, &wire) != nil {
		return point
	}
	return wire
}

func contentDigest(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func engineeringFingerprint(nodes []versionNode, dependencies []packageDependency) string {
	// Point transport timestamps/origins are not engineering revisions.
	copied := make([]versionNode, len(nodes))
	for i, node := range nodes {
		node.Points = append(data.Points{}, node.Points...)
		for j := range node.Points {
			node.Points[j].Time = time.Time{}
			node.Points[j].Origin = ""
		}
		sort.Slice(node.Points, func(i, j int) bool {
			if node.Points[i].Type != node.Points[j].Type {
				return node.Points[i].Type < node.Points[j].Type
			}
			return node.Points[i].Key < node.Points[j].Key
		})
		copied[i] = node
	}
	sort.Slice(copied, func(i, j int) bool { return copied[i].ID < copied[j].ID })
	return contentDigest(struct {
		Nodes        []versionNode
		Dependencies []packageDependency
	}{copied, dependencies})
}

func (pkg *projectPackage) seal() {
	pkg.Manifest = map[string]string{}
	for _, node := range pkg.Nodes {
		pkg.Manifest["node:"+node.Ref] = contentDigest(node)
	}
	for _, screen := range pkg.Screens {
		pkg.Manifest["screen:"+screen.Node] = contentDigest(screen)
	}
	pkg.Manifest["dependencies"] = contentDigest(pkg.Dependencies)
	pkg.Integrity = ""
	pkg.Integrity = contentDigest(pkg)
}

func (pkg projectPackage) verifyIntegrity() error {
	if pkg.Integrity == "" {
		return fmt.Errorf("package_integrity_missing")
	}
	expected := pkg.Integrity
	pkg.Integrity = ""
	if contentDigest(pkg) != expected {
		return fmt.Errorf("package_integrity_mismatch")
	}
	manifest := contentDigest(pkg.Manifest)
	pkg.seal()
	if contentDigest(pkg.Manifest) != manifest || pkg.Integrity != expected {
		return fmt.Errorf("package_manifest_mismatch")
	}
	return nil
}

func (h *Handler) captureRelease(identity Identity, siteID string) ([]data.NodeEdge, projectPackage, preflightReport, error) {
	if siteID == "" || !h.allows(identity, siteID) {
		return nil, projectPackage{}, preflightReport{}, fmt.Errorf("source_forbidden")
	}
	if h.ems == nil || h.ems.Service == nil {
		return nil, projectPackage{}, preflightReport{}, fmt.Errorf("runtime_unavailable")
	}
	nodes, strips, warnings, err := h.collectProjectPackageNodes(siteID)
	if err != nil {
		return nil, projectPackage{}, preflightReport{}, err
	}
	for _, node := range nodes {
		if !h.allows(identity, node.ID) {
			return nil, projectPackage{}, preflightReport{}, fmt.Errorf("source_forbidden")
		}
	}
	frozen, err := h.snapshotNodes(identity, siteID, "", nodes)
	if err != nil {
		return nil, projectPackage{}, preflightReport{}, err
	}
	report := h.preflightNodes(siteID, nodes)
	// A software-only IoT project does not need a physical Modbus channel.
	physical, software := false, false
	for _, node := range nodes {
		physical = physical || node.Type == data.NodeTypeModbus || node.Type == data.NodeTypeModbusIO
		software = software || node.Type == data.NodeTypeVariable || node.Type == virtualInputType || node.Type == calculationType
	}
	if !physical && software {
		report.Gates[0] = preflightGate{Key: "channels", Title: "通道", Status: "pass", Detail: "工程仅使用软件点位，无物理采集通道依赖"}
	}
	pkg, packageErr := h.packageFromNodes(identity, nodes, strips, warnings)
	if packageErr == nil {
		packageErr = pkg.validate()
	}
	report.Fingerprint = engineeringFingerprint(frozen.Nodes, pkg.Dependencies)
	report.ID = "pf-" + data.NewID()
	gate := preflightGate{Key: "package", Title: "工程包与依赖", Status: "pass", Detail: "工程引用与确切依赖已检查；发布不启动控制"}
	if packageErr != nil {
		gate.Status, gate.Detail, gate.NextStep = "fail", packageErr.Error(), "修复工程引用或依赖后重新预检"
	} else {
		report.Content = &releaseContent{Nodes: len(pkg.Nodes), Points: pkg.PointCount, Strategies: pkg.StrategyCount, Screens: pkg.ScreenCount, Dependencies: pkg.Dependencies}
	}
	report.Gates = append(report.Gates, gate, h.engineeringGate(identity, nodes))
	for i := range report.Gates {
		gate := &report.Gates[i]
		if gate.Status == "pass" || gate.Informational || gate.RepairNode != "" {
			continue
		}
		gate.RepairNode, gate.RepairStep = siteID, "tree"
		switch gate.Key {
		case "channels", "points", "commands":
			gate.RepairStep = "access"
		case "rules":
			gate.RepairStep = "strategy"
		}
	}
	report.Summary = map[string]int{"pass": 0, "warn": 0, "fail": 0}
	report.Status = "pass"
	for _, gate := range report.Gates {
		if gate.Informational {
			continue
		}
		report.Summary[gate.Status]++
		if gate.Status == "fail" {
			report.Status = "fail"
		} else if gate.Status == "warn" && report.Status == "pass" {
			report.Status = "warn"
		}
	}
	return nodes, pkg, report, nil
}

func (h *Handler) engineeringGate(identity Identity, nodes []data.NodeEdge) preflightGate {
	gate := preflightGate{Key: "engineering", Title: "工程完整性", Status: "pass", Detail: "必需成员、设备绑定、策略与画面定义已检查"}
	fail := func(node data.NodeEdge, step, detail string) preflightGate {
		gate.Status, gate.Object, gate.Detail = "fail", nodeName(node), detail
		gate.RepairNode, gate.RepairStep, gate.NextStep = node.ID, step, "修复后重新预检"
		return gate
	}
	bound := map[string]bool{}
	for _, node := range nodes {
		if node.Type != data.NodeTypeModbus {
			continue
		}
		for _, point := range node.Points {
			if point.Type != unitModelPoint || point.Tombstone%2 != 0 {
				continue
			}
			var model unitModelConfig
			if json.Unmarshal([]byte(point.Txt()), &model) == nil && model.ProfileID != "" && len(model.PointMap) > 0 {
				bound[model.BusinessNodeID] = true
			}
		}
	}
	for _, node := range nodes {
		for _, point := range node.Points {
			if point.Tombstone%2 != 0 {
				continue
			}
			switch point.Type {
			case domainaccess.MembersPoint:
				var members []domainaccess.MemberRole
				if json.Unmarshal([]byte(point.Txt()), &members) != nil {
					return fail(node, "tree", "成员声明无效")
				}
				for _, member := range members {
					if member.Required && member.NodeID == "" {
						return fail(node, "access", "必需成员「"+member.Role+"」尚未绑定")
					}
				}
			case screenPointType:
				var document screenDocument
				if json.Unmarshal([]byte(point.Txt()), &document) != nil || h.validateScreenDocument(identity, node, &document) != nil {
					return fail(node, "ui", "画面定义或绑定无效")
				}
			case dailyStatsPoint:
				config, err := readDailyConfig(node)
				if err != nil || config.Validate() != nil {
					return fail(node, "ui", "每日统计来源定义无效")
				}
			}
		}
		if node.Type == data.NodeTypeDevice && !bound[node.ID] {
			return fail(node, "access", "设备尚未绑定通道、Profile 与点位，不计为可交付设备")
		}
		if node.Type == "decisionTable" {
			text, _ := node.Points.Text("applied", "")
			var document decision.Document
			if text == "" || json.Unmarshal([]byte(text), &document) != nil || document.Validate() != nil {
				return fail(node, "strategy", "规则尚未发布有效定义")
			}
			if document.Scope != "" && !h.ruleReferencesAllowed(identity, node.Parent, document) {
				return fail(node, "strategy", "规则引用已超出声明范围或不可用")
			}
		}
		if node.Type == stateMachineType {
			document, err := decodeStateMachine(node)
			if err != nil || document.Applied == nil {
				return fail(node, "strategy", "状态机尚未发布有效定义")
			}
			if document.Applied.Validate() != nil || h.validateStateMachineScope(node.Parent, *document.Applied) != nil {
				return fail(node, "strategy", "状态机定义或引用无效")
			}
		}
	}
	return gate
}

func (h *Handler) validatePackageSource(identity Identity, nodes []data.NodeEdge, refs map[string]string) error {
	for _, node := range nodes {
		// Root-level acquisition objects may be shared; another Site's objects may not.
		parent, seen := node.Parent, map[string]bool{}
		for parent != "" && parent != "root" && !seen[parent] {
			seen[parent] = true
			ancestors, err := client.GetNodes(h.nc, "all", parent, "", false)
			if err != nil {
				return fmt.Errorf("package_scope_unavailable")
			}
			if len(ancestors) == 0 {
				break
			}
			if ancestors[0].Type == NodeTypeSite {
				if ancestors[0].ID != nodes[0].ID {
					return fmt.Errorf("package_reference_foreign_scope")
				}
				break
			}
			parent = ancestors[0].Parent
		}
		for _, point := range node.Points {
			if point.Tombstone%2 != 0 || packageStripReason(node, point) != "" {
				continue
			}
			if containsPackageSecret(string(point.Data)) {
				return fmt.Errorf("package_contains_secret")
			}
			if packageJSONDefinition(node, point) && (!json.Valid([]byte(point.Txt())) || point.Txt() == "null") {
				return fmt.Errorf("invalid_package_definition:%s", point.Type)
			}
			if structuredPackagePoint(point.Type) {
				if _, err := mapPackageReferences(point, func(id string) string { return id }); err != nil {
					return err
				}
			}
			for _, ref := range packageReferences(node, point) {
				if refs[ref] == "" {
					return fmt.Errorf("package_reference_unresolved")
				}
				if !h.allows(identity, ref) {
					return fmt.Errorf("source_forbidden")
				}
			}
		}
	}
	return nil
}

// Reject credential-shaped content inside otherwise innocuous JSON point fields;
// deleting it silently could change the meaning of an engineering definition.
func containsPackageSecret(text string) bool {
	if len(deepseek.ScanCredentials("package", "definition", text)) > 0 {
		return true
	}
	var value any
	if json.Unmarshal([]byte(text), &value) != nil {
		return false
	}
	var secret func(any) bool
	secret = func(value any) bool {
		switch v := value.(type) {
		case map[string]any:
			for key, child := range v {
				if packageSecretKey(key) {
					return true
				}
				if secret(child) {
					return true
				}
			}
		case []any:
			for _, child := range v {
				if secret(child) {
					return true
				}
			}
		case string:
			return len(deepseek.ScanCredentials("package", "definition", v)) > 0
		}
		return false
	}
	return secret(value)
}

func (h *Handler) frozenProjectPackage(identity Identity, siteID, versionID string) (projectPackage, error) {
	if !h.allows(identity, siteID) {
		return projectPackage{}, fmt.Errorf("source_forbidden")
	}
	version, err := h.loadVersion(siteID, versionID)
	if err != nil {
		return projectPackage{}, err
	}
	if !version.Deliverable || version.Package == nil {
		return projectPackage{}, fmt.Errorf("version_not_deliverable")
	}
	for _, node := range version.Nodes {
		if !h.allows(identity, node.ID) {
			return projectPackage{}, fmt.Errorf("source_forbidden")
		}
	}
	pkg := *version.Package
	if pkg.SourceVersion != version.ID || pkg.SourceFingerprint != version.Fingerprint || engineeringFingerprint(version.Nodes, pkg.Dependencies) != version.Fingerprint {
		return projectPackage{}, fmt.Errorf("version_fingerprint_mismatch")
	}
	if err := pkg.verifyIntegrity(); err != nil {
		return projectPackage{}, err
	}
	if err := pkg.validate(); err != nil {
		return projectPackage{}, err
	}
	return pkg, nil
}
