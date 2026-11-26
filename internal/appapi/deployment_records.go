package appapi

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/audit"
)

// 部署记录：把一份不可变版本与某个运行实例的"实际应用 + 采集恢复"绑在一起。
// 记录只由服务端根据版本内容、当前工程结构、真实采集健康与验证证据推导，
// 客户端提交的状态字段一律忽略；"运行中"必须有当前实例的采集依据。
const (
	deploymentRecordAction = "部署记录"
	deploymentStatusApplied = "applied"
	deploymentStatusRunning = "running"
)

type deploymentApplyEvidence struct {
	Kind      string `json:"kind"`
	Reference string `json:"reference"`
}

type deploymentAcquisitionEvidence struct {
	ChannelID      string `json:"channel_id"`
	DeviceID       string `json:"device_id"`
	Health         string `json:"health"`
	VerificationAt string `json:"verification_at"`
	Level          string `json:"verification_level"`
	Basis          string `json:"verification_basis"`
}

type deploymentRecord struct {
	ID                   string                          `json:"id"`
	ProjectID            string                          `json:"project_id"`
	SiteName             string                          `json:"site_name"`
	VersionID            string                          `json:"version_id"`
	SourceVersion        string                          `json:"source_version"`
	SourceFingerprint    string                          `json:"source_fingerprint"`
	Actor                string                          `json:"actor"`
	RecordedAt           time.Time                       `json:"recorded_at"`
	Status               string                          `json:"status"`
	Reason               string                          `json:"reason"`
	ConfigurationApplied bool                            `json:"configuration_applied"`
	AcquisitionRestored  bool                            `json:"acquisition_restored"`
	ControlAuthorized    bool                            `json:"control_authorized"`
	AcquisitionGaps      []string                        `json:"acquisition_gaps"`
	ApplyEvidence        deploymentApplyEvidence         `json:"apply_evidence"`
	AcquisitionEvidence  []deploymentAcquisitionEvidence `json:"acquisition_evidence"`
}

// 工程结构指纹：节点身份、类型与挂载位置。连接地址、轮询参数与站点描述都是
// 现场重绑会合法改变的内容，不属于"这份版本是否仍对应本工程"的判据。
func deploymentStructureFingerprint(version projectVersion) string {
	structure := make([]versionNode, len(version.Nodes))
	for i, node := range version.Nodes {
		structure[i] = versionNode{ID: node.ID, Type: node.Type, Parent: node.Parent}
	}
	return engineeringFingerprint(structure, nil)
}

func (h *Handler) deploymentRecordPath(siteID string) string {
	return filepath.Join(h.dataDir, "deployments", siteID+".jsonl")
}

func (h *Handler) readDeploymentRecords(siteID string) ([]deploymentRecord, error) {
	file, err := os.Open(h.deploymentRecordPath(siteID))
	if err != nil {
		if os.IsNotExist(err) {
			return []deploymentRecord{}, nil
		}
		return nil, err
	}
	defer file.Close()
	records := []deploymentRecord{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var record deploymentRecord
		if json.Unmarshal(line, &record) != nil {
			return nil, fmt.Errorf("deployment journal: %w", err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func (h *Handler) appendDeploymentRecord(record deploymentRecord) error {
	if h.dataDir == "" {
		return fmt.Errorf("data_dir_unavailable")
	}
	path := h.deploymentRecordPath(record.ProjectID)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	line, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(line, '\n')); err != nil {
		return err
	}
	return file.Sync()
}

// 采集证据只认当前实例的真实健康与通过了当前依据的有效验证记录。
func (h *Handler) deploymentAcquisition(siteID string, nodes []data.NodeEdge) ([]deploymentAcquisitionEvidence, []string) {
	evidence := []deploymentAcquisitionEvidence{}
	gaps := []string{}
	name := func(node data.NodeEdge) string {
		if node.Desc() != "" {
			return node.Desc()
		}
		return node.ID
	}
	for _, node := range nodes {
		if node.Type != data.NodeTypeModbus {
			continue
		}
		if textPoint(node.Points, data.PointTypeClientServer) == data.PointValueServer {
			continue
		}
		servers := []int{}
		for _, point := range node.Points {
			if point.Type != unitModelPoint || point.Tombstone%2 != 0 {
				continue
			}
			server, err := strconv.Atoi(point.Key)
			if err == nil && server > 0 {
				servers = append(servers, server)
			}
		}
		sort.Ints(servers)
		if len(servers) == 0 {
			gaps = append(gaps, fmt.Sprintf("通道 %s 尚未配置设备模型", name(node)))
			continue
		}
		for _, server := range servers {
			deviceID := fmt.Sprintf("%s/%d", node.ID, server)
			health := "unknown"
			if h.ems != nil && h.ems.Service != nil {
				if reported := h.ems.Service.State.Health(deviceID); reported != "" {
					health = reported
				}
			}
			var verified *deploymentAcquisitionEvidence
			records := accessVerificationHistory(node, server)
			for i := len(records) - 1; i >= 0; i-- {
				record := records[i]
				if !record.Passed || !record.Current {
					continue
				}
				verified = &deploymentAcquisitionEvidence{
					ChannelID: node.ID, DeviceID: deviceID, Health: health,
					VerificationAt: record.At, Level: record.Level, Basis: record.Basis,
				}
				break
			}
			switch {
			case verified != nil && health == "healthy":
				evidence = append(evidence, *verified)
			case verified != nil:
				gaps = append(gaps, fmt.Sprintf("通道 %s 站号 %d 最近一次验证通过，但当前通信健康为 %s", name(node), server, health))
			default:
				gaps = append(gaps, fmt.Sprintf("通道 %s 站号 %d 缺少当前依据下通过的采集验证", name(node), server))
			}
		}
	}
	if len(evidence) == 0 && len(gaps) == 0 {
		gaps = append(gaps, "本工程没有可采集的通道，采集恢复无法确认")
	}
	return evidence, gaps
}

// createDeploymentRecord 由服务端推导记录内容。请求体只取 site/version 的取值，
// 任何客户端状态声明都被忽略。
func (h *Handler) createDeploymentRecord(w http.ResponseWriter, r *http.Request, identity Identity) {
	var input struct {
		Site    string `json:"site"`
		Version string `json:"version"`
	}
	// 这个入口刻意不使用 decodeJSON：部署状态必须在服务端推导，
	// 客户端附带的 status/flag 字段既不被信任也不应让请求失败。
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.Site == "" || input.Version == "" {
		writeError(w, http.StatusBadRequest, "invalid_deployment")
		return
	}
	if !canAction(identity, ActionConfigure) || !h.allows(identity, input.Site) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	version, err := h.loadVersion(input.Site, input.Version)
	if err != nil {
		switch err.Error() {
		case "version_integrity_missing", "version_integrity_mismatch", "version_identity_mismatch", "invalid_version":
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeError(w, http.StatusNotFound, "version_not_found")
		}
		return
	}
	currentNodes, err := h.collectProjectPackageNodes(input.Site)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	current, err := h.snapshotNodes(identity, input.Site, "", currentNodes)
	if err != nil {
		writeError(w, http.StatusBadGateway, "deployment_check_failed")
		return
	}
	if deploymentStructureFingerprint(current) != deploymentStructureFingerprint(version) {
		writeJSONStatus(w, http.StatusConflict, map[string]any{
			"error": "version_fingerprint_mismatch", "retryable": true, "writes": 0,
		})
		return
	}

	evidence, gaps := h.deploymentAcquisition(input.Site, currentNodes)
	record := deploymentRecord{
		ID: "dp-" + data.NewID(), ProjectID: input.Site, SiteName: nodeName(currentNodes[0]),
		VersionID: version.ID, Actor: identityEmail(identity), RecordedAt: time.Now().UTC(),
		ConfigurationApplied: true, ControlAuthorized: false,
		AcquisitionGaps: gaps, AcquisitionEvidence: evidence,
		Status: deploymentStatusApplied,
	}
	if version.Import != nil {
		record.SourceVersion = version.Import.SourceVersion
		record.SourceFingerprint = version.Import.SourceFingerprint
		record.ApplyEvidence = deploymentApplyEvidence{Kind: "project_import", Reference: version.ID}
	} else {
		record.SourceVersion = version.ID
		record.SourceFingerprint = version.Fingerprint
		record.ApplyEvidence = deploymentApplyEvidence{Kind: "version_publish", Reference: version.ID}
	}
	if record.SourceVersion == "" {
		record.SourceVersion = version.ID
	}
	if record.SourceFingerprint == "" {
		record.SourceFingerprint = deploymentStructureFingerprint(version)
	}
	record.AcquisitionRestored = len(evidence) > 0 && len(gaps) == 0
	if record.AcquisitionRestored {
		record.Status = deploymentStatusRunning
		record.Reason = "配置已应用，当前实例采集已恢复；控制授权仍需单独确认"
	} else {
		record.Reason = fmt.Sprintf("配置已应用，采集尚未恢复：%d 项缺口；控制授权未授予", len(gaps))
	}
	if err := h.appendDeploymentRecord(record); err != nil {
		writeError(w, http.StatusInternalServerError, "deployment_record_failed")
		return
	}
	if h.audit != nil {
		_ = h.audit.Append(audit.Record{
			Time: record.RecordedAt, Actor: record.Actor, Action: deploymentRecordAction, Object: record.ID,
			Detail: fmt.Sprintf("版本 %s → %s；状态 %s；采集缺口 %d；控制未授权", record.VersionID, record.ProjectID, record.Status, len(gaps)),
			Result: "success",
		})
	}
	writeJSON(w, record)
}

func (h *Handler) listDeploymentRecords(w http.ResponseWriter, r *http.Request, identity Identity) {
	siteID := r.URL.Query().Get("site")
	if siteID == "" || !h.allows(identity, siteID) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	records, err := h.readDeploymentRecords(siteID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "deployment_read_failed")
		return
	}
	for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 {
		records[i], records[j] = records[j], records[i]
	}
	writeJSON(w, map[string]any{"records": records})
}

func (h *Handler) readDeploymentRecord(w http.ResponseWriter, r *http.Request, identity Identity, id string) {
	siteID := r.URL.Query().Get("site")
	if siteID == "" || !h.allows(identity, siteID) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	records, err := h.readDeploymentRecords(siteID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "deployment_read_failed")
		return
	}
	for _, record := range records {
		if record.ID == id {
			writeJSON(w, record)
			return
		}
	}
	writeError(w, http.StatusNotFound, "deployment_not_found")
}

func identityEmail(identity Identity) string {
	if identity.Email != "" {
		return identity.Email
	}
	return identity.UserID
}
