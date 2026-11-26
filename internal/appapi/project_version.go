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
	ID          string                 `json:"id"`
	SiteID      string                 `json:"site_id"`
	SiteName    string                 `json:"site_name"`
	CreatedAt   time.Time              `json:"created_at"`
	Operator    string                 `json:"operator"`
	Note        string                 `json:"note,omitempty"`
	Preflight   string                 `json:"preflight_status,omitempty"`
	PreflightID string                 `json:"preflight_id,omitempty"`
	Nodes       []versionNode          `json:"nodes"`
	Deliverable bool                   `json:"deliverable"`
	Fingerprint string                 `json:"fingerprint,omitempty"`
	Integrity   string                 `json:"integrity,omitempty"`
	Report      *preflightReport       `json:"report,omitempty"`
	Package     *projectPackage        `json:"package,omitempty"`
	Import      *projectImportEvidence `json:"import,omitempty"`
}

type versionSummary struct {
	ID          string                 `json:"id"`
	SiteID      string                 `json:"site_id"`
	SiteName    string                 `json:"site_name"`
	CreatedAt   time.Time              `json:"created_at"`
	Operator    string                 `json:"operator"`
	Note        string                 `json:"note,omitempty"`
	Preflight   string                 `json:"preflight_status,omitempty"`
	Nodes       int                    `json:"nodes"`
	Deliverable bool                   `json:"deliverable"`
	Fingerprint string                 `json:"fingerprint,omitempty"`
	Import      *projectImportEvidence `json:"import,omitempty"`
}

type rollbackResult struct {
	Version   string `json:"version"`
	Updated   int    `json:"updated"`
	Created   int    `json:"created"`
	Extra     int    `json:"extra"`
	Preflight string `json:"preflight_status,omitempty"`
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
	nodes, err := h.loadReuseSource(siteID)
	if err != nil {
		return projectVersion{}, err
	}
	return h.snapshotNodes(identity, siteID, note, nodes)
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
			if reuseRuntimePoints[p.Type] || packageRuntimePoints[p.Type] || packageSecretKey(p.Type) {
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
			Import: version.Import,
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

func (h *Handler) rollbackHandler(w http.ResponseWriter, identity Identity, siteID, versionID string) {
	h.stateMachineMu.Lock()
	defer h.stateMachineMu.Unlock()
	if siteID == "" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	version, err := h.loadVersion(siteID, versionID)
	if err != nil || version.SiteID != siteID {
		writeError(w, http.StatusNotFound, "version_not_found")
		return
	}
	for _, node := range version.Nodes {
		if node.Type == calculationType {
			writeError(w, 409, "calculation_rollback_unsupported")
			return
		}
		if node.Type == virtualInputType {
			writeError(w, 409, "virtual_input_rollback_unsupported")
			return
		}
	}
	currentNodes, err := h.loadReuseSource(siteID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	for _, node := range currentNodes {
		if node.Type == calculationType {
			writeError(w, 409, "calculation_rollback_unsupported")
			return
		}
		if node.Type == virtualInputType {
			writeError(w, 409, "virtual_input_rollback_unsupported")
			return
		}
	}
	for i, saved := range version.Nodes {
		if saved.Type != stateMachineType {
			continue
		}
		var live data.NodeEdge
		for _, node := range currentNodes {
			if node.ID == saved.ID {
				live = node
				break
			}
		}
		prepared, err := h.prepareMachineVersionRestore(saved, live)
		if err != nil {
			writeError(w, 409, err.Error())
			return
		}
		version.Nodes[i] = prepared
	}
	// 回退前先冻结当前配置，回退失败或需要反悔时有据可查。
	if current, err := h.snapshotSite(identity, siteID, "回退前自动快照"); err == nil {
		_ = h.saveVersion(current)
	}
	existing := make(map[string]bool, len(currentNodes))
	for _, node := range currentNodes {
		existing[node.ID] = true
	}
	result := rollbackResult{Version: version.ID}
	restored := make(map[string]bool, len(version.Nodes))
	for _, node := range version.Nodes {
		points := make(data.Points, 0, len(node.Points))
		for _, p := range node.Points {
			if reuseRuntimePoints[p.Type] || isSecretPoint(p.Type) {
				continue
			}
			// 版本里的点是冻结时的旧时间戳；存储会忽略过期写入，回退必须用当前时间。
			p.Time = time.Now()
			points = append(points, p)
		}
		if existing[node.ID] {
			if len(points) > 0 {
				if err := client.SendPoints(h.nc, client.SubjectNodePoints(node.ID), points, node.Type == stateMachineType); err != nil {
					writeError(w, http.StatusBadGateway, "restore_failed")
					return
				}
				if node.Type == stateMachineType {
					expected, _ := points.Text(stateMachineDocumentPoint, "")
					if h.verifyStateMachine(node.ID, expected) != nil {
						writeError(w, 503, "state_machine_persistence_unconfirmed")
						return
					}
				}
			}
			result.Updated++
		} else {
			if err := client.SendNode(h.nc, data.NodeEdge{ID: node.ID, Type: node.Type, Parent: node.Parent, Points: points}, identity.UserID); err != nil {
				writeError(w, http.StatusBadGateway, "restore_failed")
				return
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
	if report, err := h.buildPreflight(identity, siteID); err == nil {
		result.Preflight = report.Status
	}
	if h.audit != nil {
		actor := identity.Email
		if actor == "" {
			actor = identity.UserID
		}
		_ = h.audit.Append(audit.Record{
			Time: time.Now(), Actor: actor, Action: projectRollbackAction, Object: version.ID,
			Detail: fmt.Sprintf("更新 %d、创建 %d、保留多余 %d；历史不回滚", result.Updated, result.Created, result.Extra),
			Result: "success",
		})
	}
	writeJSON(w, result)
}

func fallback(value, other string) string {
	if value == "" {
		return other
	}
	return value
}
