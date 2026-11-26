package appapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/audit"
)

// 项目版本：把当前站点的工程配置冻结成一个可回退的版本。
// 发布只写版本文件与审计，不改变正在运行的配置，因此不存在"半个版本"；
// 回退/升级按版本重放配置点，历史（审计、报警、命令）不动，也不会回放旧命令。

const projectVersionAction = "项目发布"
const projectRollbackAction = "项目回退"

type versionNode struct {
	ID     string      `json:"id"`
	Type   string      `json:"type"`
	Parent string      `json:"parent"`
	Points data.Points `json:"points"`
}

type projectVersion struct {
	ID          string           `json:"id"`
	SiteID      string           `json:"site_id"`
	SiteName    string           `json:"site_name"`
	CreatedAt   time.Time        `json:"created_at"`
	Operator    string           `json:"operator"`
	Note        string           `json:"note,omitempty"`
	Preflight   string           `json:"preflight_status,omitempty"`
	PreflightID string           `json:"preflight_id,omitempty"`
	Nodes       []versionNode    `json:"nodes"`
	Deliverable bool             `json:"deliverable"`
	Fingerprint string           `json:"fingerprint,omitempty"`
	Integrity   string           `json:"integrity,omitempty"`
	Report      *preflightReport `json:"report,omitempty"`
	Package     *projectPackage  `json:"package,omitempty"`
}

type versionSummary struct {
	ID          string    `json:"id"`
	SiteID      string    `json:"site_id"`
	SiteName    string    `json:"site_name"`
	CreatedAt   time.Time `json:"created_at"`
	Operator    string    `json:"operator"`
	Note        string    `json:"note,omitempty"`
	Preflight   string    `json:"preflight_status,omitempty"`
	Nodes       int       `json:"nodes"`
	Deliverable bool      `json:"deliverable"`
	Fingerprint string    `json:"fingerprint,omitempty"`
}

type rollbackResult struct {
	Version   string `json:"version"`
	Baseline  string `json:"baseline"`
	Updated   int    `json:"updated"`
	Created   int    `json:"created"`
	Extra     int    `json:"extra"`
	Preflight string `json:"preflight_status,omitempty"`
}

type rollbackBlocker struct {
	Code     string `json:"code"`
	Scope    string `json:"scope"`
	NodeID   string `json:"node_id"`
	NodeType string `json:"node_type"`
}

func unsupportedRollbackResource(id, typ, scope string) (rollbackBlocker, bool) {
	code := ""
	switch typ {
	case calculationType:
		code = "calculation_rollback_unsupported"
	case virtualInputType:
		code = "virtual_input_rollback_unsupported"
	}
	return rollbackBlocker{Code: code, Scope: scope, NodeID: id, NodeType: typ}, code != ""
}

func writeRollbackBlockers(w http.ResponseWriter, blockers []rollbackBlocker) {
	writeJSONStatus(w, http.StatusConflict, map[string]any{
		"error": blockers[0].Code, "blockers": blockers, "retryable": true, "writes": 0,
	})
}

type publishRequest struct {
	Site                string `json:"site"`
	Note                string `json:"note"`
	Confirm             bool   `json:"confirm"`
	ExpectedFingerprint string `json:"expected_fingerprint"`
}

type rollbackRequest struct {
	Site    string `json:"site"`
	Version string `json:"version"`
}

func (h *Handler) projectDir(siteID string) string {
	return filepath.Join(h.dataDir, "projects", siteID)
}

// snapshotSite 冻结当前工程配置；运行时值与凭据不进入版本。
func (h *Handler) snapshotSite(identity Identity, siteID, note string) (projectVersion, error) {
	nodes, _, _, err := h.collectProjectPackageNodes(siteID)
	if err != nil {
		return projectVersion{}, err
	}
	return h.snapshotNodes(identity, siteID, note, nodes)
}

func rollbackConfigurationPoint(node data.NodeEdge, point data.Point) bool {
	if reuseRuntimePoints[point.Type] || packageRuntimePoints[point.Type] || packageSecretKey(point.Type) {
		return false
	}
	// 启用状态是当前实例的控制授权，不是可回放的工程定义。
	if node.Type == "decisionTable" && point.Type == "enabled" {
		return false
	}
	return node.Type != stateMachineType || point.Type != data.PointTypeDisabled
}

func (h *Handler) snapshotNodes(identity Identity, siteID, note string, nodes []data.NodeEdge) (projectVersion, error) {
	var err error
	skipped := skippedNodes(nodes)
	version := projectVersion{
		ID:        "pv-" + data.NewID(),
		SiteID:    siteID,
		SiteName:  nodeName(nodes[0]),
		CreatedAt: time.Now(),
		Operator:  identity.Email,
		Note:      strings.TrimSpace(note),
	}
	if version.Operator == "" {
		version.Operator = identity.UserID
	}
	for _, node := range orderByDepth(nodes) {
		if skipped[node.ID] {
			continue
		}
		points := make(data.Points, 0, len(node.Points))
		for _, p := range node.Points {
			if !rollbackConfigurationPoint(node, p) {
				continue
			}
			if node.Type == virtualInputType && p.Type == virtualInputDocument {
				encoded, err := inputConfiguration(node, false)
				if err != nil {
					return projectVersion{}, err
				}
				p = data.NewPointString(virtualInputDocument, "0", encoded)
			}
			if node.Type == stateMachineType && p.Type == stateMachineDocumentPoint {
				p, err = machineConfigurationPoint(p)
				if err != nil {
					return projectVersion{}, err
				}
			}
			points = append(points, packageWirePoint(p))
		}
		version.Nodes = append(version.Nodes, versionNode{ID: node.ID, Type: node.Type, Parent: node.Parent, Points: points})
	}
	return version, nil
}

func (h *Handler) saveVersion(version projectVersion) error {
	if h.dataDir == "" {
		return fmt.Errorf("data_dir_unavailable")
	}
	dir := h.projectDir(version.SiteID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	version.Integrity = ""
	version.Integrity = contentDigest(version)
	encoded, err := json.MarshalIndent(version, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".version-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(encoded); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Link publishes the complete file atomically and refuses an existing ID.
	return os.Link(tmp.Name(), filepath.Join(dir, version.ID+".json"))
}

func (h *Handler) loadVersion(siteID, versionID string) (projectVersion, error) {
	if versionID == "" || strings.ContainsAny(versionID, "/\\.") {
		return projectVersion{}, fmt.Errorf("invalid_version")
	}
	raw, err := os.ReadFile(filepath.Join(h.projectDir(siteID), versionID+".json"))
	if err != nil {
		return projectVersion{}, err
	}
	var version projectVersion
	if err := json.Unmarshal(raw, &version); err != nil {
		return projectVersion{}, err
	}
	if version.ID != versionID || version.SiteID != siteID {
		return projectVersion{}, fmt.Errorf("version_identity_mismatch")
	}
	if version.Integrity != "" {
		digest := version.Integrity
		version.Integrity = ""
		if contentDigest(version) != digest {
			return projectVersion{}, fmt.Errorf("version_integrity_mismatch")
		}
		version.Integrity = digest
	} else if version.Deliverable {
		return projectVersion{}, fmt.Errorf("version_integrity_missing")
	}
	return version, nil
}

func (h *Handler) listVersions(siteID string) ([]versionSummary, error) {
	entries, err := os.ReadDir(h.projectDir(siteID))
	if err != nil {
		if os.IsNotExist(err) {
			return []versionSummary{}, nil
		}
		return nil, err
	}
	summaries := make([]versionSummary, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		version, err := h.loadVersion(siteID, strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, versionSummary{
			ID: version.ID, SiteID: version.SiteID, SiteName: version.SiteName,
			CreatedAt: version.CreatedAt, Operator: version.Operator, Note: version.Note,
			Preflight: version.Preflight, Nodes: len(version.Nodes), Deliverable: version.Deliverable, Fingerprint: version.Fingerprint,
		})
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].CreatedAt.After(summaries[j].CreatedAt) })
	return summaries, nil
}

func (h *Handler) publishHandler(w http.ResponseWriter, identity Identity, siteID, note string, confirm bool, expected ...string) {
	// Publication, inspection and packaging share this one captured definition set.
	nodes, pkg, report, err := h.captureRelease(identity, siteID)
	if err != nil {
		writePreflightError(w, err)
		return
	}
	if report.Status == "fail" {
		writeError(w, http.StatusConflict, "preflight_failed")
		return
	}
	if len(expected) == 0 || expected[0] == "" {
		writeError(w, http.StatusConflict, "fingerprint_required")
		return
	}
	if expected[0] != report.Fingerprint {
		writeError(w, http.StatusConflict, "version_conflict")
		return
	}
	version, err := h.snapshotNodes(identity, siteID, note, nodes)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Recheck external/store edits as well as the HTTP mutation lock.
	_, _, current, err := h.captureRelease(identity, siteID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "preflight_unavailable")
		return
	}
	if current.Fingerprint != report.Fingerprint {
		writeError(w, http.StatusConflict, "version_conflict")
		return
	}
	if current.Status == "fail" {
		writeError(w, http.StatusConflict, "preflight_failed")
		return
	}
	version.Deliverable, version.Fingerprint = true, report.Fingerprint
	version.Preflight, version.PreflightID, version.Report = report.Status, report.ID, &report
	pkg.SourceVersion, pkg.SourceFingerprint, pkg.Deliverable = version.ID, report.Fingerprint, true
	pkg.CreatedAt, pkg.ID = version.CreatedAt, "pp-"+version.ID
	versions, err := h.listVersions(siteID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "version_list_failed")
		return
	}
	pkg.Version = len(versions) + 1
	pkg.seal()
	version.Package = &pkg
	if err := h.saveVersion(version); err != nil {
		writeError(w, http.StatusInternalServerError, "version_save_failed")
		return
	}
	if h.audit != nil {
		_ = h.audit.Append(audit.Record{
			Time: version.CreatedAt, Actor: version.Operator, Action: projectVersionAction,
			Object: version.ID,
			Detail: fmt.Sprintf("%s · %d 个节点 · 预检 %s", version.SiteName, len(version.Nodes), fallback(version.Preflight, "未运行")),
			Result: "success",
		})
	}
	writeJSON(w, versionSummary{
		ID: version.ID, SiteID: version.SiteID, SiteName: version.SiteName,
		CreatedAt: version.CreatedAt, Operator: version.Operator, Note: version.Note,
		Preflight: version.Preflight, Nodes: len(version.Nodes), Deliverable: version.Deliverable, Fingerprint: version.Fingerprint,
	})
}

func (h *Handler) versionsHandler(w http.ResponseWriter, identity Identity, siteID string) {
	if siteID == "" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	summaries, err := h.listVersions(siteID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "version_list_failed")
		return
	}
	for _, summary := range summaries {
		version, err := h.loadVersion(siteID, summary.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "version_read_failed")
			return
		}
		for _, node := range version.Nodes {
			if !h.allows(identity, node.ID) {
				writeError(w, http.StatusForbidden, "source_forbidden")
				return
			}
		}
	}
	writeJSON(w, map[string]any{"versions": summaries})
}

func (h *Handler) validateRollbackNodes(identity Identity, siteID string, nodes []versionNode) error {
	if len(nodes) == 0 || nodes[0].ID != siteID || nodes[0].Type != NodeTypeSite {
		return fmt.Errorf("invalid_version_structure")
	}
	ids := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		if node.ID == "" || node.Type == "" || ids[node.ID] {
			return fmt.Errorf("invalid_version_structure")
		}
		ids[node.ID] = true
	}
	for _, node := range nodes {
		if node.Parent == "" || ids[node.Parent] {
			continue
		}
		parents, err := client.GetNodes(h.nc, "all", node.Parent, "", false)
		if err != nil || len(parents) == 0 {
			return fmt.Errorf("rollback_parent_missing:%s", node.ID)
		}
	}
	validationIdentity := identity
	validationIdentity.Anchors = append([]string{}, identity.Anchors...)
	for _, node := range nodes {
		validationIdentity.Anchors = append(validationIdentity.Anchors, node.ID)
	}
	pkg, err := h.packageFromNodes(validationIdentity, versionNodeEdges(projectVersion{Nodes: nodes}), nil, nil)
	if err != nil {
		return err
	}
	return pkg.validate()
}

func versionPackageResourcesMatch(version projectVersion, pkg projectPackage) bool {
	if len(version.Nodes) != len(pkg.Nodes) {
		return false
	}
	refs := make(map[string]string, len(version.Nodes))
	for i, node := range version.Nodes {
		refs[node.ID] = fmt.Sprintf("n%d", i+1)
	}
	for i, node := range version.Nodes {
		packaged := pkg.Nodes[i]
		parent := ""
		if node.ID != version.Nodes[0].ID {
			parent = refs[node.Parent]
		}
		if packaged.Ref != refs[node.ID] || packaged.Type != node.Type || packaged.Parent != parent {
			return false
		}
	}
	return true
}

func validateRollbackPackageDependencies(h *Handler, pkg projectPackage) error {
	profiles := map[string]bool{}
	for _, node := range pkg.Nodes {
		for _, point := range node.Points {
			if point.Type == PointTypeProfileID && point.Txt() != "" {
				profiles[point.Txt()] = true
			}
			if node.Type == data.NodeTypeModbus && point.Type == unitModelPoint {
				var model unitModelConfig
				if json.Unmarshal([]byte(point.Txt()), &model) == nil && model.ProfileID != "" {
					profiles[model.ProfileID] = true
				}
			}
		}
	}
	return h.requireDependencies(pkg.Dependencies, profiles, packageNeedsIndustry(packageNodeEdges(pkg)))
}

func (h *Handler) validateRollbackVersion(identity Identity, siteID string, version projectVersion) error {
	if version.Integrity == "" {
		return fmt.Errorf("version_integrity_missing")
	}
	if err := h.validateRollbackNodes(identity, siteID, version.Nodes); err != nil {
		return err
	}
	if version.Package == nil {
		if version.Deliverable {
			return fmt.Errorf("version_package_missing")
		}
		return nil
	}
	var pkg projectPackage
	pkg = *version.Package
	if version.Deliverable && (pkg.SourceVersion != version.ID || pkg.SourceFingerprint != version.Fingerprint) {
		return fmt.Errorf("version_fingerprint_mismatch")
	}
	if !version.Deliverable && pkg.SourceVersion != "" && pkg.SourceVersion != version.ID {
		return fmt.Errorf("version_fingerprint_mismatch")
	}
	if engineeringFingerprint(version.Nodes, pkg.Dependencies) != version.Fingerprint {
		return fmt.Errorf("version_fingerprint_mismatch")
	}
	if err := pkg.verifyIntegrity(); err != nil {
		return err
	}
	if err := pkg.validate(); err != nil {
		return err
	}
	if !versionPackageResourcesMatch(version, pkg) {
		return fmt.Errorf("version_package_mismatch")
	}
	return validateRollbackPackageDependencies(h, pkg)
}

func (h *Handler) prepareRollbackVersion(version projectVersion, currentNodes []data.NodeEdge) (projectVersion, error) {
	live := make(map[string]data.NodeEdge, len(currentNodes))
	for _, node := range currentNodes {
		live[node.ID] = node
	}
	for i, saved := range version.Nodes {
		current, exists := live[saved.ID]
		if exists && (current.Type != saved.Type || current.Parent != saved.Parent) {
			return version, fmt.Errorf("rollback_node_identity_conflict:%s", saved.ID)
		}
		if saved.Type != stateMachineType {
			continue
		}
		prepared, err := h.prepareMachineVersionRestore(saved, current)
		if err != nil {
			return version, err
		}
		version.Nodes[i] = prepared
	}
	return version, nil
}

func rollbackPoints(node versionNode, current data.NodeEdge, origin string, now time.Time) data.Points {
	currentPoints := map[string]data.Point{}
	for _, point := range current.Points {
		currentPoints[upgradePointKey(point)] = point
	}
	targetKeys := map[string]bool{}
	points := make(data.Points, 0, len(node.Points)+len(current.Points))
	for _, point := range node.Points {
		if !rollbackConfigurationPoint(data.NodeEdge{Type: node.Type}, point) {
			continue
		}
		key := upgradePointKey(point)
		targetKeys[key] = true
		if live, ok := currentPoints[key]; ok && live.Tombstone > point.Tombstone {
			point.Tombstone = live.Tombstone + 1
		}
		// 版本里的点是冻结时的旧时间戳；存储会忽略过期写入，回退必须用当前时间。
		point.Time = now
		points = append(points, point)
	}
	for _, point := range current.Points {
		if point.Tombstone%2 != 0 || targetKeys[upgradePointKey(point)] ||
			!rollbackConfigurationPoint(current, point) {
			continue
		}
		tombstone := data.NewPointString(point.Type, point.Key, "")
		tombstone.Tombstone = point.Tombstone + 1
		tombstone.Time, tombstone.Origin = now, origin
		points = append(points, tombstone)
	}
	return points
}

func (h *Handler) writeRollbackVersion(identity Identity, version projectVersion, currentNodes []data.NodeEdge) (rollbackResult, []string, error) {
	existing := make(map[string]data.NodeEdge, len(currentNodes))
	for _, node := range currentNodes {
		existing[node.ID] = node
	}
	result := rollbackResult{Version: version.ID}
	restored := make(map[string]bool, len(version.Nodes))
	created := []string{}
	for _, node := range version.Nodes {
		current := existing[node.ID]
		points := rollbackPoints(node, current, identity.UserID, time.Now())
		if current.ID != "" {
			if len(points) > 0 {
				if err := client.SendPoints(h.nc, client.SubjectNodePoints(node.ID), points, true); err != nil {
					return result, created, err
				}
				if node.Type == stateMachineType {
					expected, _ := points.Text(stateMachineDocumentPoint, "")
					if h.verifyStateMachine(node.ID, expected) != nil {
						return result, created, fmt.Errorf("state_machine_persistence_unconfirmed")
					}
				}
			}
			result.Updated++
		} else {
			created = append(created, node.ID)
			if err := client.SendNode(h.nc, data.NodeEdge{ID: node.ID, Type: node.Type, Parent: node.Parent, Points: points}, identity.UserID); err != nil {
				return result, created, err
			}
			result.Created++
		}
		restored[node.ID] = true
	}
	for _, node := range currentNodes {
		if !restored[node.ID] {
			result.Extra++
		}
	}
	return result, created, nil
}

func (h *Handler) recoverRollbackBaseline(identity Identity, baseline projectVersion, created []string) error {
	for i := len(created) - 1; i >= 0; i-- {
		nodes, err := client.GetNodes(h.nc, "all", created[i], "", false)
		if err != nil {
			return err
		}
		if len(nodes) > 0 {
			if err := client.DeleteNode(h.nc, created[i], nodes[0].Parent, identity.UserID); err != nil {
				return err
			}
		}
	}
	currentNodes, _, _, err := h.collectProjectPackageNodes(baseline.SiteID)
	if err != nil {
		return err
	}
	prepared, err := h.prepareRollbackVersion(baseline, currentNodes)
	if err != nil {
		return err
	}
	_, _, err = h.writeRollbackVersion(identity, prepared, currentNodes)
	return err
}

func (h *Handler) auditRollback(identity Identity, object, result, detail string) {
	if h.audit == nil {
		return
	}
	actor := identity.Email
	if actor == "" {
		actor = identity.UserID
	}
	_ = h.audit.Append(audit.Record{
		Time: time.Now(), Actor: actor, Action: projectRollbackAction, Object: object,
		Detail: detail, Result: result,
	})
}

func writeRollbackValidationError(w http.ResponseWriter, err error) {
	status := http.StatusConflict
	if err.Error() == "source_forbidden" {
		status = http.StatusForbidden
	}
	writeError(w, status, err.Error())
}

func (h *Handler) rollbackHandler(w http.ResponseWriter, identity Identity, siteID, versionID string) {
	h.stateMachineMu.Lock()
	defer h.stateMachineMu.Unlock()
	if siteID == "" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	version, err := h.loadVersion(siteID, versionID)
	if err != nil {
		switch err.Error() {
		case "version_integrity_missing", "version_integrity_mismatch", "version_identity_mismatch":
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeError(w, http.StatusNotFound, "version_not_found")
		}
		return
	}
	if err := h.validateRollbackVersion(identity, siteID, version); err != nil {
		writeRollbackValidationError(w, err)
		return
	}
	blockers := []rollbackBlocker{}
	for _, node := range version.Nodes {
		if blocker, unsupported := unsupportedRollbackResource(node.ID, node.Type, "version"); unsupported {
			blockers = append(blockers, blocker)
		}
	}
	currentNodes, _, _, err := h.collectProjectPackageNodes(siteID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	for _, node := range currentNodes {
		if blocker, unsupported := unsupportedRollbackResource(node.ID, node.Type, "current"); unsupported {
			blockers = append(blockers, blocker)
		}
	}
	if len(blockers) > 0 {
		writeRollbackBlockers(w, blockers)
		return
	}
	version, err = h.prepareRollbackVersion(version, currentNodes)
	if err != nil {
		writeRollbackValidationError(w, err)
		return
	}
	// 回退前先冻结并回读当前配置；没有可恢复的基线就不能写入。
	current, err := h.snapshotSite(identity, siteID, "回退前自动快照")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "rollback_baseline_failed")
		return
	}
	if err := h.validateRollbackNodes(identity, siteID, current.Nodes); err != nil {
		writeJSONStatus(w, http.StatusConflict, map[string]any{
			"error": "rollback_baseline_unrestorable", "cause": err.Error(), "retryable": true, "writes": 0,
		})
		return
	}
	if err := h.saveVersion(current); err != nil {
		writeError(w, http.StatusInternalServerError, "rollback_baseline_failed")
		return
	}
	if _, err := h.loadVersion(siteID, current.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "rollback_baseline_failed")
		return
	}
	result, created, writeErr := h.writeRollbackVersion(identity, version, currentNodes)
	result.Baseline = current.ID
	if writeErr != nil {
		recoveryErr := h.recoverRollbackBaseline(identity, current, created)
		if recoveryErr != nil {
			detail := fmt.Sprintf("目标写入失败：%v；基线 %s 恢复未确认：%v", writeErr, current.ID, recoveryErr)
			h.auditRollback(identity, version.ID, "failed", detail)
			writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{
				"error": "rollback_recovery_failed", "cause": detail, "baseline": current.ID,
				"recovered": false, "retryable": false,
			})
			return
		}
		detail := fmt.Sprintf("目标写入失败：%v；已恢复回退前基线 %s", writeErr, current.ID)
		h.auditRollback(identity, version.ID, "failed", detail)
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{
			"error": "rollback_failed_recovered", "cause": detail, "baseline": current.ID,
			"recovered": true, "retryable": true,
		})
		return
	}
	if report, err := h.buildPreflight(identity, siteID); err == nil {
		result.Preflight = report.Status
	}
	h.auditRollback(identity, version.ID, "success", fmt.Sprintf("基线 %s；更新 %d、创建 %d、保留多余 %d；历史不回滚", current.ID, result.Updated, result.Created, result.Extra))
	writeJSON(w, result)
}

func fallback(value, other string) string {
	if value == "" {
		return other
	}
	return value
}
