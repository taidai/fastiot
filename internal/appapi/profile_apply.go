package appapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/ems/pointconfig"
	"fastiot/internal/ems/profile"
)

// 批量应用 Profile：把一个厂商 Profile 应用到一批站号，自动按逻辑点名称匹配物理点位，
// 并给出逐点判定（exact / scaled / mismatch / missing）与因此不可用的控制。
// 点位类型、数据格式、单位、缩放、偏移或读写方向不等价的映射逐项列出；
// 缺回读、点位只读、未映射或不等价的控制会被明确阻断，不会用默认点冒充。
// 换厂商后模型进入待重新验证状态，控制命令在人工复核前不可用。

type profilePointVerdict struct {
	Name         string                   `json:"name"`
	PointID      string                   `json:"point_id,omitempty"`
	Verdict      string                   `json:"verdict"` // exact | scaled | mismatch | missing
	ProfileScale float64                  `json:"profile_scale"`
	PointScale   float64                  `json:"point_scale,omitempty"`
	ReadOnly     bool                     `json:"read_only,omitempty"`
	Reason       string                   `json:"reason,omitempty"`
	Differences  []profilePointDifference `json:"differences,omitempty"`
}

// profilePointDifference is one mapping dimension the new profile and the
// existing physical point do not agree on. It names the register so an
// operator can correct or explicitly accept it before control resumes.
type profilePointDifference struct {
	Point    string `json:"point"`
	Field    string `json:"field"` // 类型 | 数据格式 | 单位 | 缩放 | 偏移 | 读写方向
	Profile  string `json:"profile"`
	Existing string `json:"existing"`
}

// profileReplacement records a vendor change on one unit model. It is stored
// with the model so the next operator can see what changed and why control
// waits for re-verification.
type profileReplacement struct {
	From        string                   `json:"from"`
	To          string                   `json:"to"`
	At          string                   `json:"at"`
	Equivalent  bool                     `json:"equivalent"`
	Differences []profilePointDifference `json:"differences,omitempty"`
	Missing     []string                 `json:"missing,omitempty"`
}

type profileApplyResult struct {
	Server      int                   `json:"server"`
	ProfileID   string                `json:"profile_id"`
	Mapped      int                   `json:"mapped"`
	Exact       int                   `json:"exact"`
	Scaled      int                   `json:"scaled"`
	Mismatched  int                   `json:"mismatched"`
	Missing     []string              `json:"missing"`
	Blocked     []string              `json:"blocked"`
	Points      []profilePointVerdict `json:"points"`
	Replacement *profileReplacement   `json:"replacement,omitempty"`
	Model       unitModelConfig       `json:"-"`
}

type profileApplyRequest struct {
	ProfileID string `json:"profile_id"`
	Servers   []int  `json:"servers"`
	DryRun    bool   `json:"dry_run"`
}

type profileApplyResponse struct {
	ProfileID string               `json:"profile_id"`
	DryRun    bool                 `json:"dry_run"`
	Results   []profileApplyResult `json:"results"`
	Applied   int                  `json:"applied"`
}

func normalizePointName(name string) string {
	replacer := strings.NewReplacer(" ", "", "\t", "", "　", "", "／", "/")
	return strings.ToLower(replacer.Replace(strings.TrimSpace(name)))
}

// applyProfile 计算一个站号的映射结果；existing 用于保持业务节点等不随换厂商变化的绑定。
func applyProfile(server int, prof profile.Profile, points []unitModelPointView, existing unitModelConfig) profileApplyResult {
	replaced := existing.ProfileID != "" && existing.ProfileID != prof.ID
	result := profileApplyResult{
		Server: server, ProfileID: prof.ID,
		Missing: []string{}, Blocked: []string{}, Points: []profilePointVerdict{},
		Model: unitModelConfig{
			Name: prof.Vendor + " " + prof.Model, ProfileID: prof.ID,
			BusinessNodeID: existing.BusinessNodeID,
			PointMap:       map[string]string{}, Verified: false,
		},
	}
	if replaced {
		result.Model.ReplacedFrom = existing.ProfileID
		result.Model.VerificationRequired = true
	}
	byName := make(map[string]unitModelPointView)
	for _, point := range points {
		if point.ServerID != server {
			continue
		}
		key := normalizePointName(point.Name)
		if key == "" {
			continue
		}
		if _, exists := byName[key]; !exists {
			byName[key] = point
		}
	}
	readOnly := make(map[string]bool)
	mismatched := make(map[string][]profilePointDifference)
	for _, spec := range prof.PointSpecs {
		point, ok := byName[normalizePointName(spec.Name)]
		verdict := profilePointVerdict{Name: spec.Name, ProfileScale: spec.Scale}
		if !ok {
			verdict.Verdict = "missing"
			verdict.Reason = "没有同名物理点位"
			result.Missing = append(result.Missing, spec.Name)
		} else {
			verdict.PointID = point.ID
			verdict.PointScale = point.Scale
			verdict.ReadOnly = point.ReadOnly
			verdict.Differences = pointDifferences(spec, point)
			switch {
			case hasSemanticDifference(verdict.Differences):
				verdict.Verdict = "mismatch"
				result.Mismatched++
				mismatched[spec.Name] = verdict.Differences
			case len(verdict.Differences) > 0:
				verdict.Verdict = "scaled"
				result.Scaled++
			default:
				verdict.Verdict = "exact"
				result.Exact++
			}
			if len(verdict.Differences) > 0 {
				verdict.Reason = describeDifferences(verdict.Differences)
			}
			result.Mapped++
			result.Model.PointMap[spec.Name] = point.ID
			readOnly[spec.Name] = point.ReadOnly
		}
		result.Points = append(result.Points, verdict)
	}

	blocked := make(map[string]bool)
	addBlocked := func(message string) {
		if !blocked[message] {
			blocked[message] = true
			result.Blocked = append(result.Blocked, message)
		}
	}
	checkPoint := func(owner, pointName string) {
		if pointName == "" {
			return
		}
		if _, ok := result.Model.PointMap[pointName]; !ok {
			addBlocked(fmt.Sprintf("%s：未映射 %s", owner, pointName))
			return
		}
		if readOnly[pointName] {
			addBlocked(fmt.Sprintf("%s：点位只读 %s", owner, pointName))
		}
		if differences, ok := mismatched[pointName]; ok {
			addBlocked(fmt.Sprintf("%s：映射不等价 %s（%s）", owner, pointName, differenceFields(differences)))
		}
	}
	for _, command := range prof.CommandSpecs {
		owner := fmt.Sprintf("命令《%s》", command.Name)
		if command.Point == "" && len(command.Parameters) == 0 {
			addBlocked(owner + "：未配置写点")
			continue
		}
		checkPoint(owner, command.Point)
		for _, parameter := range command.Parameters {
			checkPoint(owner, parameter.Point)
		}
	}

	if replaced {
		replacement := &profileReplacement{
			From: existing.ProfileID, To: prof.ID,
			At:         time.Now().UTC().Format(time.RFC3339),
			Equivalent: result.Mismatched == 0 && result.Scaled == 0 && len(result.Missing) == 0,
			Missing:    append([]string(nil), result.Missing...),
		}
		for _, verdict := range result.Points {
			replacement.Differences = append(replacement.Differences, verdict.Differences...)
		}
		result.Replacement = replacement
		result.Model.Replacement = replacement
	}
	return result
}

// pointDifferences lists every mapping dimension where the new profile and the
// existing point disagree. Empty point fields mean "not configured yet" and
// are not treated as a disagreement, so a fresh import is not drowned in noise.
func pointDifferences(spec profile.PointSpec, point unitModelPointView) []profilePointDifference {
	differences := []profilePointDifference{}
	add := func(field, want, got string) {
		differences = append(differences, profilePointDifference{
			Point: spec.Name, Field: field, Profile: want, Existing: got,
		})
	}
	wantType := profile.IOTypeFor(spec.Function)
	if point.IOType != "" && point.IOType != wantType {
		add("类型", typeName(wantType), typeName(point.IOType))
	}
	if point.IOType != "" && pointconfig.IsRegister(point.IOType) && point.DataFormat != "" && point.DataFormat != spec.Format {
		add("数据格式", spec.Format, point.DataFormat)
	}
	if spec.Unit != "" && point.Unit != spec.Unit {
		unit := point.Unit
		if unit == "" {
			unit = "未设置"
		}
		add("单位", spec.Unit, unit)
	}
	if point.Scale != spec.Scale {
		add("缩放", formatNumber(spec.Scale), formatNumber(point.Scale))
	}
	if point.Offset != spec.Offset {
		add("偏移", formatNumber(spec.Offset), formatNumber(point.Offset))
	}
	if point.ReadOnly != spec.ReadOnly {
		add("读写方向", directionName(spec.ReadOnly), directionName(point.ReadOnly))
	}
	return differences
}

// hasSemanticDifference reports differences that change what a value means or
// whether it may be written. Scale and offset alone are convertible and stay
// reported as "scaled".
func hasSemanticDifference(differences []profilePointDifference) bool {
	for _, difference := range differences {
		switch difference.Field {
		case "缩放", "偏移":
		default:
			return true
		}
	}
	return false
}

func describeDifferences(differences []profilePointDifference) string {
	parts := make([]string, 0, len(differences))
	for _, difference := range differences {
		parts = append(parts, fmt.Sprintf("%s：Profile %s，点位 %s", difference.Field, difference.Profile, difference.Existing))
	}
	return strings.Join(parts, "；")
}

func differenceFields(differences []profilePointDifference) string {
	fields := make([]string, 0, len(differences))
	for _, difference := range differences {
		fields = append(fields, difference.Field)
	}
	return strings.Join(fields, "、")
}

func typeName(ioType string) string {
	if label := pointconfig.TypeLabel(ioType); label != "" {
		return label
	}
	return ioType
}

func directionName(readOnly bool) string {
	if readOnly {
		return "只读"
	}
	return "可写"
}

func formatNumber(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

// applyProfilesHandler 处理 POST /api/modbus/{channel}/units/apply。
func (h *Handler) applyProfilesHandler(w http.ResponseWriter, r *http.Request, identity Identity, channelID string) {
	channel, ok := h.routeNode(w, channelID)
	if !ok {
		return
	}
	if channel.Type != data.NodeTypeModbus {
		writeError(w, http.StatusBadRequest, "invalid_modbus_bus")
		return
	}
	if h.ems == nil || h.ems.Profiles == nil {
		writeError(w, http.StatusServiceUnavailable, "ems_unavailable")
		return
	}
	var input profileApplyRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	prof, ok := h.ems.Profiles.Get(input.ProfileID)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown_profile")
		return
	}
	if len(input.Servers) == 0 {
		writeError(w, http.StatusBadRequest, "servers_required")
		return
	}
	points := h.unitPoints(channel.ID)
	response := profileApplyResponse{ProfileID: prof.ID, DryRun: input.DryRun, Results: []profileApplyResult{}}
	seen := make(map[int]bool)
	for _, server := range input.Servers {
		if server < 1 || server > 247 || seen[server] {
			continue
		}
		seen[server] = true
		existing, _ := unitModelFor(channel, server)
		result := applyProfile(server, prof, points, existing)
		if !input.DryRun {
			if err := h.saveUnitModel(channel, server, result.Model, points); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			response.Applied++
		}
		result.Model = unitModelConfig{}
		response.Results = append(response.Results, result)
	}
	if len(response.Results) == 0 {
		writeError(w, http.StatusBadRequest, "servers_required")
		return
	}
	writeJSON(w, response)
}
