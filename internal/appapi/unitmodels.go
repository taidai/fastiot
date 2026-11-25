package appapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/contract"
	"fastiot/internal/ems/bridge"
	"fastiot/internal/ems/command"
	"fastiot/internal/ems/device"
	"fastiot/internal/ems/profile"
	"fastiot/internal/ems/signal"
)

const unitModelPoint = "easyemsUnitModel"

type unitModelConfig struct {
	Name           string            `json:"name"`
	ProfileID      string            `json:"profile_id"`
	ProfileVersion string            `json:"profile_version,omitempty"`
	MappingVersion string            `json:"mapping_version,omitempty"`
	BusinessNodeID string            `json:"business_node_id,omitempty"`
	PointMap       map[string]string `json:"point_map"`
	Verified       bool              `json:"verified"`
	Verifier       string            `json:"verifier,omitempty"`
	VerifiedAt     string            `json:"verified_at,omitempty"`
	DeviceSerial   string            `json:"device_serial,omitempty"`
	Verification   string            `json:"verification,omitempty"`
	// ReplacedFrom records the profile this device was mapped with before the
	// current one. A non-empty value means the vendor changed after install.
	ReplacedFrom string `json:"replaced_from,omitempty"`
	// VerificationRequired blocks control until an operator confirms the new
	// mapping against the physical device. It is set by a profile replacement
	// and cleared only by a verified save.
	VerificationRequired bool                `json:"verification_required,omitempty"`
	Replacement          *profileReplacement `json:"replacement,omitempty"`
}

func modelMappingVersion(model unitModelConfig, registry *profile.Registry, points []unitModelPointView) string {
	version, _ := registry.Fingerprint(model.ProfileID)
	mapped := map[string]unitModelPointView{}
	for name, id := range model.PointMap {
		point, _ := findPoint(points, id)
		mapped[name] = point
	}
	return accessHash(struct {
		Version, Business string
		Mapping           map[string]string
		Points            map[string]unitModelPointView
	}{version, model.BusinessNodeID, model.PointMap, mapped})
}

func currentProfileModel(model unitModelConfig, registry *profile.Registry, points []unitModelPointView) unitModelConfig {
	version, found := registry.Fingerprint(model.ProfileID)
	if !found || (model.ProfileVersion != "" && model.ProfileVersion != version) ||
		(model.MappingVersion != "" && model.MappingVersion != modelMappingVersion(model, registry, points)) {
		model.Verified = false
		model.VerificationRequired = true
	}
	return model
}

func verificationPending(model unitModelConfig) bool {
	return model.VerificationRequired && !model.Verified
}

type unitModelPointView struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	ServerID    int     `json:"server_id"`
	Address     int     `json:"address"`
	IOType      string  `json:"io_type"`
	DataFormat  string  `json:"data_format,omitempty"`
	ReadOnly    bool    `json:"read_only"`
	Unit        string  `json:"unit,omitempty"`
	Scale       float64 `json:"scale"`
	Offset      float64 `json:"offset"`
	Disabled    bool    `json:"disabled"`
	ConfigStamp string  `json:"config_stamp,omitempty"`
}

type unitModelView struct {
	ChannelID     string               `json:"channel_id"`
	ServerID      int                  `json:"server_id"`
	Model         *unitModelConfig     `json:"model,omitempty"`
	Profiles      []profile.Profile    `json:"profiles"`
	ProfileErrors []string             `json:"profile_errors,omitempty"`
	Points        []unitModelPointView `json:"points"`
	Commands      []unitModelCommand   `json:"commands"`
	Missing       []string             `json:"missing,omitempty"`
	ManualAllowed bool                 `json:"manual_allowed"`
}

type unitModelParameter struct {
	Name      string  `json:"name"`
	Label     string  `json:"label"`
	PointID   string  `json:"point_id,omitempty"`
	PointName string  `json:"point_name"`
	Unit      string  `json:"unit,omitempty"`
	Kind      string  `json:"kind,omitempty"`
	HasRange  bool    `json:"has_range,omitempty"`
	Min       float64 `json:"min"`
	Max       float64 `json:"max"`
	Default   float64 `json:"default"`
	Available bool    `json:"available"`
	Reason    string  `json:"reason,omitempty"`
}

type unitModelCommand struct {
	ID                string               `json:"id"`
	Name              string               `json:"name"`
	PointID           string               `json:"point_id,omitempty"`
	PointName         string               `json:"point_name"`
	Value             float64              `json:"value"`
	ReadbackPointID   string               `json:"readback_point_id,omitempty"`
	ReadbackPointName string               `json:"readback_point_name,omitempty"`
	ReadbackValue     float64              `json:"readback_value,omitempty"`
	TimeoutMS         int                  `json:"timeout_ms,omitempty"`
	Parameters        []unitModelParameter `json:"parameters,omitempty"`
	Available         bool                 `json:"available"`
	Reason            string               `json:"reason,omitempty"`
}

func (h *Handler) unitModel(w http.ResponseWriter, r *http.Request, identity Identity, channelID string, server int) {
	if r.Method == http.MethodPut {
		h.accessMu.Lock()
		defer h.accessMu.Unlock()
	}
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
	points := h.unitPoints(channel.ID)
	model, hasModel := unitModelFor(channel, server)
	switch r.Method {
	case http.MethodGet:
		if hasModel && !h.operationScope(identity, model) {
			writeError(w, http.StatusForbidden, "out_of_scope")
			return
		}
		view := unitModelResponse(channel.ID, server, model, hasModel, points, h.ems.Profiles)
		view.ManualAllowed = canAction(identity, ActionWriteValue)
		for i := range view.Commands {
			item := &view.Commands[i]
			if !canAction(identity, ActionCommand) {
				item.Available = false
				item.Reason = "当前账号无命令权限"
			}
			if h.ems.Service == nil || !h.ems.Service.HasDevice(fmt.Sprintf("%s/%d", channel.ID, server)) || h.ems.Service.State.Health(fmt.Sprintf("%s/%d", channel.ID, server)) == "offline" {
				item.Available = false
				item.Reason = "设备离线或运行时未就绪"
			}
			if item.Available {
				writes := []device.Write{}
				if item.PointID != "" {
					writes = append(writes, device.Write{Point: item.PointID, Value: signal.Num(item.Value)})
				}
				for _, parameter := range item.Parameters {
					writes = append(writes, device.Write{Point: parameter.PointID, Value: signal.Num(parameter.Default)})
				}
				for _, write := range writes {
					verdict := h.commandGuard(command.GuardRequest{DeviceID: fmt.Sprintf("%s/%d", channel.ID, server), Point: write.Point, Writes: []device.Write{write}}).Effective()
					if verdict.Decision == "refused" {
						item.Available = false
						item.Reason = verdict.Reason
						break
					}
				}
			}
		}
		writeJSON(w, view)
	case http.MethodPut:
		var input unitModelConfig
		if !decodeJSON(w, r, &input) {
			return
		}
		if hasModel && model.BusinessNodeID != "" && input.BusinessNodeID != model.BusinessNodeID {
			writeError(w, http.StatusConflict, "business_binding_conflict")
			return
		}
		if input.BusinessNodeID != "" {
			if !h.allows(identity, input.BusinessNodeID) {
				writeError(w, http.StatusForbidden, "out_of_scope")
				return
			}
			node, exists := h.routeNode(w, input.BusinessNodeID)
			if !exists {
				return
			}
			if node.Type != data.NodeTypeDevice || node.Parent == "root" {
				writeError(w, http.StatusBadRequest, "business_device_required")
				return
			}
			version, _ := h.ems.Profiles.Fingerprint(input.ProfileID)
			if !hasModel || model.ProfileID != input.ProfileID || model.ProfileVersion != version {
				writeError(w, http.StatusConflict, "profile_preview_required")
				return
			}
		}
		if hasModel && model.MappingVersion != "" && (model.BusinessNodeID != "" || input.MappingVersion != "") && input.MappingVersion != model.MappingVersion {
			writeError(w, http.StatusConflict, "mapping_version_conflict")
			return
		}
		if err := h.saveUnitModel(channel, server, input, points); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		stored, err := client.GetNodes(h.nc, "all", channel.ID, "", false)
		if err != nil || len(stored) == 0 {
			writeError(w, http.StatusServiceUnavailable, "model_reload_failed")
			return
		}
		model, _ = unitModelFor(stored[0], server)
		writeJSON(w, unitModelResponse(channel.ID, server, model, true, points, h.ems.Profiles))
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (h *Handler) unitPoints(channelID string) []unitModelPointView {
	buses, err := client.GetNodes(h.nc, "all", channelID, "", false)
	if err != nil || len(buses) == 0 {
		return nil
	}
	nodes, err := client.GetNodes(h.nc, channelID, "all", "", false)
	if err != nil {
		return nil
	}
	points := make([]unitModelPointView, 0)
	for _, node := range nodes {
		if node.Type != data.NodeTypeModbusIO || node.Parent != channelID {
			continue
		}
		server, _ := node.Points.ValueInt(data.PointTypeID, "")
		address, _ := node.Points.ValueInt(data.PointTypeAddress, "")
		points = append(points, unitModelPointView{
			ID: node.ID, Name: textPoint(node.Points, data.PointTypeDescription),
			ServerID: server, Address: address,
			IOType:      textPoint(node.Points, data.PointTypeModbusIOType),
			DataFormat:  textPoint(node.Points, data.PointTypeDataFormat),
			ReadOnly:    readOnlyNode(node),
			Unit:        textPoint(node.Points, data.PointTypeUnits),
			Scale:       numberPoint(node.Points, data.PointTypeScale),
			Offset:      numberPoint(node.Points, data.PointTypeOffset),
			Disabled:    numberPoint(node.Points, data.PointTypeDisabled) != 0,
			ConfigStamp: bridge.AcquisitionStamp(buses[0], node),
		})
	}
	return points
}

func unitModelFor(channel data.NodeEdge, server int) (unitModelConfig, bool) {
	point, ok := channel.Points.Find(unitModelPoint, strconv.Itoa(server))
	if !ok || point.Tombstone%2 != 0 {
		return unitModelConfig{}, false
	}
	var model unitModelConfig
	if json.Unmarshal([]byte(point.Txt()), &model) != nil {
		return unitModelConfig{}, false
	}
	if model.PointMap == nil {
		model.PointMap = map[string]string{}
	}
	return model, true
}

func unitModelResponse(channelID string, server int, model unitModelConfig, hasModel bool, points []unitModelPointView, registry *profile.Registry) unitModelView {
	if points == nil {
		points = []unitModelPointView{}
	}
	view := unitModelView{
		ChannelID: channelID, ServerID: server,
		Points: points, Profiles: registry.List(),
		ProfileErrors: registry.LoadErrors,
		Commands:      []unitModelCommand{},
	}
	if hasModel {
		model = currentProfileModel(model, registry, points)
		view.Model = &model
	}
	if hasModel {
		pending := verificationPending(model)
		if prof, ok := registry.Get(model.ProfileID); ok {
			seen := map[string]bool{}
			for _, spec := range prof.CommandSpecs {
				pointID := model.PointMap[spec.Point]
				item := unitModelCommand{ID: spec.ID, Name: spec.Name, PointID: pointID, PointName: spec.Point, Value: spec.Value,
					ReadbackPointName: spec.Readback, ReadbackValue: spec.ReadbackValue, TimeoutMS: spec.TimeoutMS}
				available := true
				reason := ""
				if spec.Point != "" {
					if pointID == "" {
						available = false
						reason = "未映射点位"
						if !seen[spec.Point] {
							view.Missing = append(view.Missing, spec.Point)
							seen[spec.Point] = true
						}
					} else if point, ok := findPoint(points, pointID); !ok {
						available = false
						reason = "映射点位不存在"
					} else if point.ReadOnly {
						available = false
						reason = "点位为只读"
					}
				}
				if spec.Readback != "" {
					item.ReadbackPointID = model.PointMap[spec.Readback]
					if item.ReadbackPointID == "" {
						available = false
						reason = "回读点未映射"
						if !seen[spec.Readback] {
							view.Missing = append(view.Missing, spec.Readback)
							seen[spec.Readback] = true
						}
					} else if _, ok := findPoint(points, item.ReadbackPointID); !ok {
						available = false
						reason = "回读点不存在"
					}
				}
				for _, parameter := range spec.Parameters {
					mappedID := model.PointMap[parameter.Point]
					param := unitModelParameter{
						Name: parameter.Name, Label: parameter.Label, PointID: mappedID,
						PointName: parameter.Point, Unit: parameter.Unit, Kind: parameter.Kind,
						HasRange: parameter.HasRange, Min: parameter.Min, Max: parameter.Max, Default: parameter.Default,
					}
					if mappedID == "" {
						param.Reason = "未映射点位"
						available = false
						reason = "参数点位未映射"
						if !seen[parameter.Point] {
							view.Missing = append(view.Missing, parameter.Point)
							seen[parameter.Point] = true
						}
					} else if point, ok := findPoint(points, mappedID); !ok {
						param.Reason = "映射点位不存在"
						available = false
						reason = "参数点位不存在"
					} else if point.ReadOnly {
						param.Reason = "点位为只读"
						available = false
						reason = "参数点位为只读"
					} else {
						param.Available = true
					}
					item.Parameters = append(item.Parameters, param)
				}
				if spec.Point == "" && len(spec.Parameters) == 0 {
					available = false
					reason = "命令未配置写点"
				}
				if pending {
					available = false
					reason = "换厂商后待重新验证控制保障"
				}
				item.Available = available
				item.Reason = reason
				view.Commands = append(view.Commands, item)
			}
		}
	}
	return view
}

func (h *Handler) saveUnitModel(channel data.NodeEdge, server int, input unitModelConfig, points []unitModelPointView) error {
	if server < 1 || server > 247 {
		return fmt.Errorf("invalid station %d", server)
	}
	if input.ProfileID == "" {
		return fmt.Errorf("profile is required")
	}
	if _, ok := h.ems.Profiles.Get(input.ProfileID); !ok {
		return fmt.Errorf("unknown profile %s", input.ProfileID)
	}
	byID := map[string]unitModelPointView{}
	for _, point := range points {
		byID[point.ID] = point
	}
	cleaned := map[string]string{}
	for name, id := range input.PointMap {
		point, ok := byID[id]
		if !ok || point.ServerID != server {
			return fmt.Errorf("point %s is not under station %d", id, server)
		}
		cleaned[name] = id
	}
	input.PointMap = cleaned
	version, _ := h.ems.Profiles.Fingerprint(input.ProfileID)
	mappingVersion := modelMappingVersion(input, h.ems.Profiles, points)
	if previous, exists := unitModelFor(channel, server); exists {
		changed := previous.ProfileID != input.ProfileID || !reflect.DeepEqual(previous.PointMap, input.PointMap) ||
			(previous.ProfileVersion != "" && previous.ProfileVersion != version) ||
			(previous.MappingVersion != "" && previous.MappingVersion != mappingVersion)
		if changed {
			input.Verified = false
			input.Verifier, input.VerifiedAt, input.Verification = "", "", ""
			input.VerificationRequired = true
		} else if previous.VerificationRequired && !input.Verified {
			input.VerificationRequired = true
		}
	}
	input.ProfileVersion = version
	input.MappingVersion = mappingVersion
	if input.Name == "" {
		input.Name = "站号 " + strconv.Itoa(server)
	}
	if input.Verified {
		// Re-verification is the only way out of a vendor replacement gate.
		input.VerificationRequired = false
	}
	if input.Verified && (strings.TrimSpace(input.Verifier) == "" || strings.TrimSpace(input.VerifiedAt) == "" || strings.TrimSpace(input.DeviceSerial) == "") {
		return fmt.Errorf("verified model requires verifier, date and device serial")
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return err
	}
	point := data.NewPointString(unitModelPoint, strconv.Itoa(server), string(encoded))
	point.Origin = "easyems"
	return client.SendNodePoint(h.nc, channel.ID, point, true)
}

// deviceVerificationPending reports whether the device's profile mapping was
// replaced and has not been re-verified yet.
func (h *Handler) deviceVerificationPending(deviceID string) bool {
	channelID, serverText, ok := strings.Cut(deviceID, "/")
	if !ok {
		return false
	}
	server, err := strconv.Atoi(serverText)
	if err != nil {
		return false
	}
	channelNodes, err := client.GetNodes(h.nc, "all", channelID, "", false)
	if err != nil || len(channelNodes) == 0 {
		return false
	}
	model, ok := unitModelFor(channelNodes[0], server)
	if ok && h.ems != nil && h.ems.Profiles != nil {
		model = currentProfileModel(model, h.ems.Profiles, h.unitPoints(channelID))
	}
	return ok && verificationPending(model)
}

func (h *Handler) commandProfile(deviceID string) (device.Profile, bool) {
	if h.ems == nil || h.ems.Profiles == nil {
		return device.Profile{}, false
	}
	channelID, serverText, ok := strings.Cut(deviceID, "/")
	if !ok {
		return device.Profile{}, false
	}
	server, err := strconv.Atoi(serverText)
	if err != nil {
		return device.Profile{}, false
	}
	channelNodes, err := client.GetNodes(h.nc, "all", channelID, "", false)
	if err != nil || len(channelNodes) == 0 {
		return device.Profile{}, false
	}
	channel := channelNodes[0]
	model, ok := unitModelFor(channel, server)
	points := h.unitPoints(channelID)
	model = currentProfileModel(model, h.ems.Profiles, points)
	if !ok || verificationPending(model) {
		// A replacement's commands stay unavailable until an operator
		// re-verifies the mapping, so no control path can use them silently.
		return device.Profile{}, false
	}
	prof, ok := h.ems.Profiles.Get(model.ProfileID)
	if !ok {
		return device.Profile{}, false
	}
	byID := map[string]unitModelPointView{}
	for _, point := range points {
		byID[point.ID] = point
	}
	profilePoints := make([]device.Point, 0, len(prof.PointSpecs))
	kinds := map[string]signal.Kind{}
	seenPoints := map[string]bool{}
	kindOf := func(point unitModelPointView) signal.Kind {
		if point.IOType == data.PointValueModbusCoil || point.IOType == data.PointValueModbusDiscreteInput {
			return signal.Bool
		}
		return signal.Number
	}
	for _, spec := range prof.PointSpecs {
		id := model.PointMap[spec.Name]
		point, ok := byID[id]
		if !ok || seenPoints[id] {
			continue
		}
		kind := kindOf(point)
		kinds[id] = kind
		seenPoints[id] = true
		profilePoints = append(profilePoints, device.Point{
			ID: id, Kind: kind, Unit: point.Unit, ReadOnly: point.ReadOnly || spec.ReadOnly,
		})
	}
	commands := make([]device.Command, 0, len(prof.CommandSpecs))
	for _, spec := range prof.CommandSpecs {
		var writes []device.Write
		var readback []device.Readback
		valid := true
		if spec.Point != "" {
			id := model.PointMap[spec.Point]
			if _, ok := byID[id]; id == "" || !ok {
				continue
			}
			kind := kinds[id]
			value := signal.Num(spec.Value)
			if kind == signal.Bool {
				value = signal.Flag(spec.Value != 0)
			}
			writes = []device.Write{{Point: id, Value: value}}
			readbackPoint, readbackKind, readbackValue := id, kind, spec.Value
			if spec.Readback != "" {
				readbackPoint = model.PointMap[spec.Readback]
				readbackView, found := byID[readbackPoint]
				if readbackPoint == "" || !found {
					continue
				}
				readbackKind = kindOf(readbackView)
				readbackValue = spec.ReadbackValue
			}
			confirmed := signal.Num(readbackValue)
			if readbackKind == signal.Bool {
				confirmed = signal.Flag(readbackValue != 0)
			}
			readback = []device.Readback{{Point: readbackPoint, Op: "eq", Value: confirmed}}
		}
		parameters := make([]device.Parameter, 0, len(spec.Parameters))
		for _, parameter := range spec.Parameters {
			id := model.PointMap[parameter.Point]
			point, ok := byID[id]
			if id == "" || !ok || point.ReadOnly {
				valid = false
				break
			}
			kind := kinds[id]
			parameters = append(parameters, device.Parameter{
				Name: parameter.Name, Label: parameter.Label, Point: id, Kind: kind, Binary: parameter.Kind == "bool",
				Unit: parameter.Unit, HasRange: parameter.HasRange,
				Min: parameter.Min, Max: parameter.Max, Default: parameter.Default,
			})
		}
		if !valid || (len(writes) == 0 && len(parameters) == 0) {
			continue
		}
		timeout := 5 * time.Second
		if spec.TimeoutMS > 0 {
			timeout = time.Duration(spec.TimeoutMS) * time.Millisecond
		}
		commands = append(commands, device.Command{
			ID: spec.ID, Name: spec.Name, Timeout: timeout, ReadbackTolerance: spec.ReadbackTolerance,
			Writes: writes, Readback: readback, Parameters: parameters,
		})
	}
	if len(commands) == 0 {
		return device.Profile{}, false
	}
	return device.Profile{ID: model.ProfileID, Vendor: prof.Vendor, Model: prof.Model, Points: profilePoints, Commands: commands}, true
}

func (h *Handler) operationScope(identity Identity, model unitModelConfig) bool {
	if model.BusinessNodeID != "" && !h.allows(identity, model.BusinessNodeID) {
		return false
	}
	for _, id := range model.PointMap {
		if !h.allows(identity, id) {
			return false
		}
	}
	return true
}

func writeCommandError(w http.ResponseWriter, err error) {
	status := http.StatusConflict
	if errors.Is(err, command.ErrJournal) {
		status = http.StatusServiceUnavailable
	}
	writeError(w, status, err.Error())
}

func (h *Handler) unitCommands(w http.ResponseWriter, r *http.Request, identity Identity, channelID string, server int, requestID string) {
	channel, ok := h.routeNode(w, channelID)
	if !ok {
		return
	}
	if channel.Type != data.NodeTypeModbus {
		writeError(w, http.StatusBadRequest, "invalid_modbus_bus")
		return
	}
	deviceID := fmt.Sprintf("%s/%d", channelID, server)
	if model, found := unitModelFor(channel, server); found && !h.operationScope(identity, model) {
		writeError(w, http.StatusForbidden, "out_of_scope")
		return
	}
	switch {
	case r.Method == http.MethodGet && requestID == "":
		outcomes := h.ems.Service.Commands().List()
		list := make([]contract.CommandOutcome, 0)
		for _, outcome := range outcomes {
			if outcome.DeviceID == deviceID {
				list = append(list, commandOutcome(outcome))
			}
		}
		writeJSON(w, map[string]any{"commands": list})
		return
	case r.Method == http.MethodGet && requestID != "":
		outcome, ok, err := h.ems.Service.Commands().Find(requestID)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "command_state_unavailable")
			return
		}
		if !ok || outcome.DeviceID != deviceID {
			writeError(w, http.StatusNotFound, "command_not_found")
			return
		}
		writeJSON(w, commandOutcome(outcome))
		return
	case r.Method == http.MethodPost && requestID == "":
		var input struct {
			RequestID  string             `json:"request_id"`
			CommandID  string             `json:"command_id"`
			Parameters map[string]float64 `json:"parameters"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		if input.RequestID == "" || input.CommandID == "" {
			writeError(w, http.StatusBadRequest, "request_id_and_command_required")
			return
		}
		if len(input.RequestID) > 128 {
			writeError(w, http.StatusBadRequest, "invalid_request_id")
			return
		}
		request := command.Request{ID: input.RequestID, DeviceID: deviceID, CommandID: input.CommandID, Actor: identity.UserID, Parameters: input.Parameters}
		if existing, found, err := h.ems.Service.Commands().Existing(request); err != nil {
			writeCommandError(w, err)
			return
		} else if found {
			writeJSON(w, commandOutcome(existing))
			return
		}
		model, ok := unitModelFor(channel, server)
		if !ok {
			writeError(w, http.StatusConflict, "unit_model_not_configured")
			return
		}
		if verificationPending(model) {
			writeError(w, http.StatusConflict, "verification_required")
			return
		}
		prof, ok := h.commandProfile(deviceID)
		if !ok {
			writeError(w, http.StatusConflict, "command_points_not_mapped")
			return
		}
		if _, ok := prof.Command(input.CommandID); !ok {
			writeError(w, http.StatusBadRequest, "command_not_available")
			return
		}
		if h.ems.Service.State.Health(deviceID) == "offline" {
			writeError(w, http.StatusConflict, "device_offline")
			return
		}
		outcome, err := h.ems.Service.Commands().Submit(time.Now(), request)
		if err != nil {
			writeCommandError(w, err)
			return
		}
		_ = model
		writeJSON(w, commandOutcome(outcome))
		return
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func commandOutcome(outcome command.Outcome) contract.CommandOutcome {
	observed := make([]contract.CommandObservation, 0, len(outcome.Observed))
	for _, sample := range outcome.Observed {
		observed = append(observed, contract.CommandObservation{Point: sample.Point, Value: sample.Value.String(), At: sample.At})
	}
	return contract.CommandOutcome{
		RequestID: outcome.RequestID, DeviceID: outcome.DeviceID, CommandID: outcome.CommandID,
		Actor: outcome.Actor, State: string(outcome.State), Reason: outcome.Reason,
		StartedAt: outcome.StartedAt, UpdatedAt: outcome.UpdatedAt, Deadline: outcome.Deadline, Observed: observed,
	}
}

func findPoint(points []unitModelPointView, id string) (unitModelPointView, bool) {
	for _, point := range points {
		if point.ID == id {
			return point, true
		}
	}
	return unitModelPointView{}, false
}

func hasPoint(points []unitModelPointView, id string) bool {
	_, ok := findPoint(points, id)
	return ok
}

func textPoint(points data.Points, typ string) string {
	point, ok := points.Find(typ, "")
	if !ok {
		return ""
	}
	return point.Txt()
}

func numberPoint(points data.Points, typ string) float64 {
	value, _ := points.Value(typ, "")
	return value
}

func readOnlyNode(node data.NodeEdge) bool {
	return numberPoint(node.Points, data.PointTypeReadOnly) != 0
}
