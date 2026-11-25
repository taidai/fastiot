package appapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/decision"
	domainaccess "fastiot/internal/domain/access"
	"fastiot/internal/ems/command"
	"fastiot/internal/ems/profile"
	"fastiot/internal/ems/signal"
)

func (h *Handler) updateNode(w http.ResponseWriter, r *http.Request, identity Identity, id string) {
	node, ok := h.routeNode(w, id)
	if !ok {
		return
	}
	// 根节点（parent 为虚拟 root）允许改项目名；其余节点走各自编辑器。
	if node.Parent != "root" && node.Type != data.NodeTypeGroup && node.Type != data.NodeTypeVariable &&
		node.Type != "decisionTable" && node.Type != "deviceTemplate" && !genericBusinessNodeType(node.Type) {
		writeError(w, http.StatusBadRequest, "use_specific_editor")
		return
	}
	var input struct {
		Description string                     `json:"description"`
		Disabled    *bool                      `json:"disabled"`
		Members     *[]domainaccess.MemberRole `json:"members"`
	}
	if !decodeJSON(w, r, &input) || strings.TrimSpace(input.Description) == "" {
		writeError(w, http.StatusBadRequest, "invalid_node")
		return
	}
	points := data.Points{data.NewPointString(data.PointTypeDescription, "", strings.TrimSpace(input.Description))}
	if input.Disabled != nil {
		if node.Type != data.NodeTypeVariable {
			writeError(w, http.StatusBadRequest, "unsupported_disable")
			return
		}
		points = append(points, data.NewPointFloat(data.PointTypeDisabled, "", data.BoolToFloat(*input.Disabled)))
	}
	missing := []string{}
	if input.Members != nil {
		if node.Type != domainaccess.NodeUnit && node.Type != NodeTypeEnergyUnit {
			writeError(w, http.StatusBadRequest, "members_require_unit")
			return
		}
		members := *input.Members
		if err := domainaccess.ValidateMembers(members); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		for i := range members {
			members[i].Role = strings.TrimSpace(members[i].Role)
			members[i].NodeID = strings.TrimSpace(members[i].NodeID)
			if members[i].Required && members[i].NodeID == "" {
				missing = append(missing, members[i].Role)
			}
			if members[i].NodeID == "" {
				continue
			}
			if members[i].NodeID == node.ID {
				writeError(w, http.StatusBadRequest, "member_cannot_bind_self")
				return
			}
			target, found := h.node(w, identity, members[i].NodeID)
			if !found {
				return
			}
			if target.Parent == "root" || target.Type == NodeTypeSiteRoot {
				writeError(w, http.StatusBadRequest, "member_cannot_bind_site_root")
				return
			}
			if target.Type != data.NodeTypeDevice && target.Type != data.NodeTypeVariable && target.Type != data.NodeTypeModbusIO {
				writeError(w, http.StatusBadRequest, "member_requires_device_or_point")
				return
			}
		}
		encoded, err := json.Marshal(members)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_members")
			return
		}
		points = append(points, data.NewPointString(domainaccess.MembersPoint, "", string(encoded)))
	}
	for i := range points {
		points[i].Origin = identity.UserID
	}
	if err := client.SendNodePoints(h.nc, id, points, true); err != nil {
		writeError(w, http.StatusBadRequest, "save_failed")
		return
	}
	writeJSON(w, map[string]any{"saved": true, "missing_required": missing})
}

func (h *Handler) moveNode(w http.ResponseWriter, r *http.Request, identity Identity, id string) {
	node, ok := h.routeNode(w, id)
	if !ok {
		return
	}
	var input struct {
		OldParent string `json:"old_parent"`
		NewParent string `json:"new_parent"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if node.Parent != input.OldParent {
		writeError(w, http.StatusConflict, "source_parent_mismatch")
		return
	}
	if input.NewParent == "" {
		writeError(w, http.StatusForbidden, "destination_forbidden")
		return
	}
	if input.NewParent == id || h.users.IsUnder(input.NewParent, id) {
		writeError(w, http.StatusForbidden, "move_cycle")
		return
	}
	destination, ok := h.routeNode(w, input.NewParent)
	if !ok {
		return
	}
	if genericBusinessNodeType(node.Type) && node.Type != data.NodeTypeDevice {
		if !domainaccess.CanPlace(node.Type, destination.Type, destination.Parent == "root" || destination.Type == NodeTypeSiteRoot) {
			writeError(w, http.StatusBadRequest, "invalid_destination")
			return
		}
	} else if destination.Type != data.NodeTypeGroup && destination.Type != data.NodeTypeDevice {
		writeError(w, http.StatusBadRequest, "invalid_destination")
		return
	}
	if node.Type == data.NodeTypeModbusIO || node.Type == data.NodeTypeUser || node.Type == data.NodeTypeDevice {
		writeError(w, http.StatusForbidden, "invalid_move")
		return
	}
	if input.OldParent == input.NewParent {
		writeJSON(w, map[string]bool{"moved": true})
		return
	}
	if h.rejectCalculationDependents(w, id) {
		return
	}
	if node.Type == "decisionTable" {
		applied, _ := node.Points.Text("applied", "")
		if applied != "" {
			var document decision.Document
			check := node
			check.Parent = input.NewParent
			if json.Unmarshal([]byte(applied), &document) != nil || !h.documentInScope(check, document) {
				writeError(w, http.StatusConflict, "rule_targets_outside_destination")
				return
			}
		}
	} else {
		rules, err := h.allRules()
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "dependency_check_failed")
			return
		}
		for _, rule := range rules {
			if rule.ID == id || h.users.IsUnder(rule.ID, id) {
				continue
			}
			if h.referencesSubtree(rule, id) && input.NewParent != rule.Parent && !h.users.IsUnder(input.NewParent, rule.Parent) {
				if (node.Type == virtualInputType || node.Type == calculationType) && !h.allows(identity, rule.ID) {
					writeError(w, http.StatusConflict, "restricted_dependency")
					return
				}
				writeError(w, http.StatusConflict, "move_would_break_rule: "+rule.Desc())
				return
			}
		}
	}
	if err := client.MoveNode(h.nc, id, input.OldParent, input.NewParent, identity.UserID); err != nil {
		writeError(w, http.StatusBadRequest, "move_failed")
		return
	}
	writeJSON(w, map[string]bool{"moved": true})
}

func (h *Handler) deleteNode(w http.ResponseWriter, r *http.Request, identity Identity, id string) {
	node, ok := h.routeNode(w, id)
	if !ok {
		return
	}
	var input struct {
		Parent string `json:"parent"`
	}
	if !decodeJSON(w, r, &input) || input.Parent != node.Parent {
		writeError(w, http.StatusBadRequest, "invalid_parent")
		return
	}
	for _, root := range identity.Anchors {
		if id == root {
			writeError(w, http.StatusForbidden, "cannot_delete_root")
			return
		}
	}
	children, err := client.GetNodes(h.nc, id, "all", "", false)
	if err == nil && len(children) > 0 {
		writeError(w, http.StatusConflict, "has_children")
		return
	}
	if h.rejectCalculationDependents(w, id) {
		return
	}
	if node.Type != "decisionTable" {
		rules, err := h.allRules()
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "dependency_check_failed")
			return
		}
		for _, rule := range rules {
			if h.referencesSubtree(rule, id) {
				if (node.Type == virtualInputType || node.Type == calculationType) && !h.allows(identity, rule.ID) {
					writeError(w, http.StatusConflict, "restricted_dependency")
					return
				}
				writeError(w, http.StatusConflict, "used_by_rule: "+rule.Desc())
				return
			}
		}
	}
	if err := client.DeleteNode(h.nc, id, input.Parent, identity.UserID); err != nil {
		writeError(w, http.StatusBadRequest, "delete_failed")
		return
	}
	writeJSON(w, map[string]bool{"deleted": true})
}

func (h *Handler) allRules() ([]data.NodeEdge, error) {
	return client.GetNodes(h.nc, "all", "all", "decisionTable", false)
}

func (h *Handler) referencesSubtree(rule data.NodeEdge, id string) bool {
	for _, pointType := range []string{"draft", "applied"} {
		text, _ := rule.Points.Text(pointType, "")
		if text == "" {
			continue
		}
		var document decision.Document
		if json.Unmarshal([]byte(text), &document) != nil {
			continue
		}
		for _, bindings := range []map[string]decision.Target{document.Bindings.Points, document.Bindings.Actions} {
			for _, target := range bindings {
				if target.NodeID == id || h.users.IsUnder(target.NodeID, id) {
					return true
				}
			}
		}
	}
	return false
}

func genericBusinessNodeType(nodeType string) bool {
	switch nodeType {
	case domainaccess.NodeSite, domainaccess.NodePlant, domainaccess.NodeArea,
		domainaccess.NodeUnit, domainaccess.NodeEnergyUnit, data.NodeTypeDevice:
		return true
	default:
		return false
	}
}

func (h *Handler) createSiteFromTemplate(w http.ResponseWriter, identity Identity, parentID, name, templateID string) {
	template, ok := domainaccess.SiteTemplateByID(templateID)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown_site_template")
		return
	}
	if err := domainaccess.ValidateSiteTemplate(template); err != nil {
		writeError(w, http.StatusConflict, "invalid_site_template")
		return
	}
	parent, ok := h.routeNode(w, parentID)
	if !ok {
		return
	}
	if !domainaccess.CanPlace(domainaccess.NodeSite, parent.Type, parent.Parent == "root" || parent.Type == NodeTypeSiteRoot) {
		writeError(w, http.StatusBadRequest, "invalid_parent_type")
		return
	}
	created := []data.NodeEdge{}
	root := data.NodeEdge{ID: data.NewID(), Type: domainaccess.NodeSite, Parent: parentID,
		Points: data.Points{data.NewPointString(data.PointTypeDescription, "", name)}}
	if err := client.SendNode(h.nc, root, identity.UserID); err != nil {
		writeError(w, http.StatusBadRequest, "create_failed")
		return
	}
	created = append(created, root)
	for _, child := range template.Nodes {
		if err := h.createTemplateNode(child, root.ID, identity.UserID, &created); err != nil {
			rollbackCreatedNodes(h.nc, created, identity.UserID)
			writeError(w, http.StatusBadRequest, "create_failed")
			return
		}
	}
	writeJSON(w, map[string]any{"id": root.ID, "created": len(created)})
}

func (h *Handler) createTemplateNode(template domainaccess.TemplateNode, parentID, origin string, created *[]data.NodeEdge) error {
	points := data.Points{data.NewPointString(data.PointTypeDescription, "", template.Name)}
	if len(template.Members) > 0 {
		encoded, err := json.Marshal(template.Members)
		if err != nil {
			return err
		}
		points = append(points, data.NewPointString(domainaccess.MembersPoint, "", string(encoded)))
	}
	node := data.NodeEdge{ID: data.NewID(), Type: template.Type, Parent: parentID, Points: points}
	if err := client.SendNode(h.nc, node, origin); err != nil {
		return err
	}
	*created = append(*created, node)
	for _, child := range template.Children {
		if err := h.createTemplateNode(child, node.ID, origin, created); err != nil {
			return err
		}
	}
	return nil
}

func rollbackCreatedNodes(nc *nats.Conn, created []data.NodeEdge, origin string) {
	for i := len(created) - 1; i >= 0; i-- {
		_ = client.DeleteNode(nc, created[i].ID, created[i].Parent, origin)
	}
}

func (h *Handler) copyNodeSubtree(w http.ResponseWriter, r *http.Request, identity Identity, id string) {
	source, ok := h.node(w, identity, id)
	if !ok {
		return
	}
	if source.Parent == "root" || !domainaccess.CopyableNodeType(source.Type) {
		writeError(w, http.StatusForbidden, "subtree_not_copyable")
		return
	}
	var input struct {
		Parent      string `json:"parent"`
		Description string `json:"description"`
	}
	if !decodeJSON(w, r, &input) || strings.TrimSpace(input.Description) == "" || input.Parent == "" {
		writeError(w, http.StatusBadRequest, "invalid_copy")
		return
	}
	destination, found := h.node(w, identity, input.Parent)
	if !found {
		return
	}
	if !domainaccess.CanPlace(source.Type, destination.Type, destination.Parent == "root" || destination.Type == NodeTypeSiteRoot) {
		writeError(w, http.StatusBadRequest, "invalid_parent_type")
		return
	}
	nodes, err := h.loadSubtree(id)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "source_unavailable")
		return
	}
	created := []data.NodeEdge{}
	idMap := map[string]string{}
	skipped := 0
	for _, node := range orderByDepth(nodes) {
		if !domainaccess.CopyableNodeType(node.Type) {
			skipped++
			continue
		}
		newParent := destination.ID
		if node.ID != source.ID {
			var mapped bool
			newParent, mapped = idMap[node.Parent]
			if !mapped {
				skipped++
				continue
			}
		}
		newID := data.NewID()
		points := structuralCopyPoints(node)
		if node.ID == source.ID {
			points = setDescription(points, strings.TrimSpace(input.Description))
		}
		clone := data.NodeEdge{ID: newID, Type: node.Type, Parent: newParent, Points: points}
		if err := client.SendNode(h.nc, clone, identity.UserID); err != nil {
			rollbackCreatedNodes(h.nc, created, identity.UserID)
			writeError(w, http.StatusBadRequest, "copy_failed")
			return
		}
		created = append(created, clone)
		idMap[node.ID] = newID
	}
	confirm := []string{}
	for _, node := range nodes {
		newID, mapped := idMap[node.ID]
		if !mapped || (node.Type != domainaccess.NodeUnit && node.Type != NodeTypeEnergyUnit) {
			continue
		}
		point, present := node.Points.Find(domainaccess.MembersPoint, "")
		if !present || point.Txt() == "" {
			continue
		}
		var members []domainaccess.MemberRole
		if json.Unmarshal([]byte(point.Txt()), &members) != nil {
			continue
		}
		rewritten, unresolved := domainaccess.RewriteMembers(members, idMap)
		confirm = append(confirm, unresolved...)
		encoded, _ := json.Marshal(rewritten)
		binding := data.NewPointString(domainaccess.MembersPoint, "", string(encoded))
		binding.Origin = identity.UserID
		if err := client.SendNodePoint(h.nc, newID, binding, true); err != nil {
			rollbackCreatedNodes(h.nc, created, identity.UserID)
			writeError(w, http.StatusBadRequest, "copy_binding_failed")
			return
		}
	}
	writeJSON(w, map[string]any{"id": idMap[source.ID], "created": len(created), "skipped": skipped, "confirm": confirm})
}

func structuralCopyPoints(node data.NodeEdge) data.Points {
	points := data.Points{}
	for _, point := range copyConfigPoints(node) {
		switch point.Type {
		case domainaccess.MembersPoint, PointTypePowerSetpoint, PointTypeProfileID, PointTypePointMap:
			continue
		case PointTypeAssetCode, "businessId":
			point = data.NewPointString(point.Type, point.Key, data.NewID())
		}
		points = append(points, point)
	}
	return points
}

func (h *Handler) batchAccess(w http.ResponseWriter, r *http.Request, identity Identity) {
	var input struct {
		CSV    string `json:"csv"`
		DryRun bool   `json:"dry_run"`
	}
	if !decodeJSON(w, r, &input) || strings.TrimSpace(input.CSV) == "" {
		writeError(w, http.StatusBadRequest, "empty_or_invalid_csv")
		return
	}
	inventory, err := h.batchInventory(identity)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "batch_inventory_unavailable")
		return
	}
	rows, err := domainaccess.PreflightCSV(input.CSV, inventory)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	applied := 0
	if !input.DryRun {
		h.accessMu.Lock()
		defer h.accessMu.Unlock()
		for i := range rows {
			if !rows[i].Valid {
				continue
			}
			if h.applyBatchBinding(&rows[i], identity.UserID, inventory.Profiles) {
				applied++
			}
		}
	}
	writeJSON(w, map[string]any{"dry_run": input.DryRun, "rows": rows, "applied": applied})
}

func (h *Handler) batchInventory(identity Identity) (domainaccess.BatchInventory, error) {
	if h.ems == nil || h.ems.Profiles == nil {
		return domainaccess.BatchInventory{}, fmt.Errorf("ems_unavailable")
	}
	nodes, err := h.visibleNodes(identity)
	if err != nil {
		return domainaccess.BatchInventory{}, err
	}
	inventory := domainaccess.BatchInventory{Profiles: h.ems.Profiles.List()}
	bound := map[string]bool{}
	for _, node := range nodes {
		switch node.Type {
		case data.NodeTypeDevice:
			if node.Parent != "root" {
				inventory.Devices = append(inventory.Devices, domainaccess.BatchDevice{ID: node.ID, Name: node.Desc()})
			}
		case data.NodeTypeModbus:
			channel := domainaccess.BatchChannel{ID: node.ID, Name: node.Desc(), Occupied: map[int]bool{}}
			for _, point := range h.unitPoints(node.ID) {
				channel.Occupied[point.ServerID] = true
			}
			for _, point := range node.Points {
				if point.Type != unitModelPoint || point.Tombstone%2 != 0 {
					continue
				}
				server, err := strconv.Atoi(point.Key)
				if err != nil {
					continue
				}
				channel.Occupied[server] = true
				if model, ok := unitModelFor(node, server); ok && model.BusinessNodeID != "" {
					bound[model.BusinessNodeID] = true
				}
			}
			inventory.Channels = append(inventory.Channels, channel)
		}
	}
	for i := range inventory.Devices {
		inventory.Devices[i].Bound = bound[inventory.Devices[i].ID]
	}
	return inventory, nil
}

func (h *Handler) applyBatchBinding(row *domainaccess.BindingRow, origin string, profiles []profile.Profile) bool {
	channel, err := client.GetNodes(h.nc, "all", row.ChannelID, "", false)
	if err != nil || len(channel) == 0 || channel[0].Type != data.NodeTypeModbus {
		row.Status = "failed"
		row.Valid = false
		row.Errors = append(row.Errors, "通道不存在")
		return false
	}
	if h.stationOccupied(channel[0], row.Station) {
		row.Status = "blocked"
		row.Valid = false
		row.Errors = append(row.Errors, "站号冲突")
		return false
	}
	var prof profile.Profile
	found := false
	for _, candidate := range profiles {
		if candidate.ID == row.ProfileID {
			prof, found = candidate, true
			break
		}
	}
	if !found {
		row.Status = "failed"
		row.Valid = false
		row.Errors = append(row.Errors, "型号不存在")
		return false
	}
	before := map[string]bool{}
	for _, point := range h.unitPoints(channel[0].ID) {
		before[point.ID] = true
	}
	inputs := profilePointInputs(channel[0].ID, row.Station, prof)
	if err := h.createModbusBatch(channel[0].ID, inputs, origin); err != nil {
		row.Status = "failed"
		row.Valid = false
		row.Errors = append(row.Errors, "物理点创建失败")
		return false
	}
	points := h.unitPoints(channel[0].ID)
	existing, _ := unitModelFor(channel[0], row.Station)
	result := applyProfile(row.Station, prof, points, existing)
	result.Model.Name = row.Device
	result.Model.BusinessNodeID = row.DeviceID
	if err := h.saveUnitModel(channel[0], row.Station, result.Model, points); err != nil {
		h.rollbackNewBatchPoints(channel[0].ID, before, origin)
		row.Status = "failed"
		row.Valid = false
		row.Errors = append(row.Errors, err.Error())
		return false
	}
	row.Status = "mapped"
	row.CreatedPoints = len(inputs)
	row.Mapped = result.Mapped
	row.Missing = result.Missing
	return true
}

func (h *Handler) stationOccupied(channel data.NodeEdge, server int) bool {
	if _, ok := unitModelFor(channel, server); ok {
		return true
	}
	for _, point := range h.unitPoints(channel.ID) {
		if point.ServerID == server {
			return true
		}
	}
	return false
}

func (h *Handler) rollbackNewBatchPoints(channelID string, before map[string]bool, origin string) {
	for _, point := range h.unitPoints(channelID) {
		if !before[point.ID] {
			_ = client.DeleteNode(h.nc, point.ID, channelID, origin)
		}
	}
}

func profilePointInputs(channelID string, server int, prof profile.Profile) []ModbusIOInput {
	inputs := make([]ModbusIOInput, 0, len(prof.PointSpecs))
	for _, spec := range prof.PointSpecs {
		format := spec.Format
		if format == "" {
			format = data.PointValueUINT16
		}
		scale := spec.Scale
		if scale == 0 {
			scale = 1
		}
		inputs = append(inputs, ModbusIOInput{
			Parent: channelID, Description: spec.Name, ServerID: server, Address: int(spec.Address),
			IOType: profile.IOTypeFor(spec.Function), DataFormat: format, ReadOnly: spec.ReadOnly,
			Scale: scale, Offset: spec.Offset, Units: spec.Unit,
		})
	}
	return inputs
}

func (h *Handler) verifyUnit(w http.ResponseWriter, r *http.Request, identity Identity, channelID string, server int) {
	channel, ok := h.routeNode(w, channelID)
	if !ok {
		return
	}
	if channel.Type != data.NodeTypeModbus || server < 1 || server > 247 {
		writeError(w, http.StatusBadRequest, "invalid_modbus_unit")
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, map[string]any{"records": accessVerificationHistory(channel, server)})
		return
	}
	var input struct {
		Level     string `json:"level"`
		CommandID string `json:"command_id"`
		Confirm   bool   `json:"confirm"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if (input.Level == "L1" || input.Level == "L2") && !canAction(identity, ActionConfigure) {
		writeError(w, http.StatusForbidden, "admin_required")
		return
	}
	switch input.Level {
	case "L1":
		connection, valid := modbusConnectionFromNode(channel)
		if !valid {
			writeError(w, http.StatusBadRequest, "invalid_modbus_connection")
			return
		}
		line := probeLine(connection, server, server)
		passed := line.Connected && len(line.Stations) == 1 && line.Stations[0].Responded
		reasons := []string{}
		if !passed {
			reasons = append(reasons, "通道或站号无响应")
			if len(line.Stations) == 1 && line.Stations[0].Error != "" {
				reasons = append(reasons, line.Stations[0].Error)
			}
		}
		h.finishAccessVerification(w, channel, server, identity, input.Level, passed, reasons, map[string]any{"line": line})
	case "L2":
		model, configured := unitModelFor(channel, server)
		if !configured || h.ems == nil || h.ems.Profiles == nil {
			writeError(w, http.StatusConflict, "unit_model_not_configured")
			return
		}
		prof, found := h.ems.Profiles.Get(model.ProfileID)
		if !found {
			writeError(w, http.StatusConflict, "unknown_profile")
			return
		}
		required := domainaccess.RequiredPointSpecs(prof)
		pointResults := []domainaccess.PointVerification{}
		reasons := []string{}
		passed := len(required) > 0
		if len(required) == 0 {
			reasons = append(reasons, "Profile 没有必需点位")
		}
		deviceID := fmt.Sprintf("%s/%d", channel.ID, server)
		points := h.unitPoints(channel.ID)
		for _, spec := range required {
			result := domainaccess.PointVerification{Name: spec.Name, PointID: model.PointMap[spec.Name]}
			point, mapped := findPoint(points, result.PointID)
			if result.PointID == "" || !mapped || point.ServerID != server {
				result.Reason = "必需点未映射"
			} else if h.ems.Service == nil {
				result.Reason = "运行时不可用"
			} else if sample, exists := h.ems.Service.State.Get(time.Now(), deviceID, result.PointID, h.ems.Service.MaxAge); !exists {
				result.Quality = "uninitialized"
				result.Reason = "无实时值"
			} else {
				result.Quality = sample.Quality.String()
				result.SourceTime = sample.SourceTime.UTC().Format(time.RFC3339)
				if sample.Quality != signal.Good {
					result.Reason = sample.Reason
					if result.Reason == "" {
						result.Reason = "质量非正常"
					}
				} else if !sample.Value.Valid() {
					result.Reason = "无效值"
				}
			}
			result.Passed = result.Reason == ""
			if !result.Passed {
				passed = false
				reasons = append(reasons, result.Name+"："+result.Reason)
			}
			pointResults = append(pointResults, result)
		}
		h.finishAccessVerification(w, channel, server, identity, input.Level, passed, reasons, map[string]any{"points": pointResults})
	case "L3":
		if !input.Confirm {
			writeError(w, http.StatusBadRequest, "l3_confirmation_required")
			return
		}
		if !canAction(identity, ActionCommand) {
			writeError(w, http.StatusForbidden, "command_required")
			return
		}
		model, configured := unitModelFor(channel, server)
		if !configured || verificationPending(model) || h.ems == nil || h.ems.Profiles == nil || h.ems.Service == nil {
			writeError(w, http.StatusConflict, "verification_required")
			return
		}
		prof, found := h.ems.Profiles.Get(model.ProfileID)
		if !found {
			writeError(w, http.StatusConflict, "unknown_profile")
			return
		}
		safeCommand, err := domainaccess.SafeCommand(prof, input.CommandID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		deviceID := fmt.Sprintf("%s/%d", channel.ID, server)
		if _, available := h.commandProfile(deviceID); !available {
			writeError(w, http.StatusConflict, "command_points_not_mapped")
			return
		}
		if h.ems.Service.State.Health(deviceID) == "offline" {
			writeError(w, http.StatusConflict, "device_offline")
			return
		}
		requestID := data.NewID()
		outcome, err := h.ems.Service.Commands().Submit(time.Now(), command.Request{
			ID: requestID, DeviceID: deviceID, CommandID: input.CommandID, Actor: identity.UserID,
		})
		if err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		timeout := 5 * time.Second
		if safeCommand.TimeoutMS > 0 {
			timeout = time.Duration(safeCommand.TimeoutMS) * time.Millisecond
		}
		deadline := time.Now().Add(timeout)
		for !domainaccess.TerminalCommandState(string(outcome.State)) && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
			if current, found := h.ems.Service.Commands().Get(requestID); found {
				outcome = current
			}
		}
		if !domainaccess.TerminalCommandState(string(outcome.State)) {
			h.ems.Service.Commands().Tick(time.Now())
			if current, found := h.ems.Service.Commands().Get(requestID); found {
				outcome = current
			}
		}
		passed := domainaccess.CommandOutcomePassed(string(outcome.State))
		reasons := []string{}
		if outcome.Reason != "" {
			reasons = append(reasons, outcome.Reason)
		} else if !passed {
			reasons = append(reasons, "命令尚未确认")
		}
		h.finishAccessVerification(w, channel, server, identity, input.Level, passed, reasons, map[string]any{"command": commandOutcome(outcome)})
	default:
		writeError(w, http.StatusBadRequest, "invalid_verification_level")
	}
}

func (h *Handler) finishAccessVerification(w http.ResponseWriter, channel data.NodeEdge, server int, identity Identity, level string, passed bool, reasons []string, response map[string]any) {
	record := domainaccess.VerificationRecord{At: time.Now().UTC().Format(time.RFC3339), Actor: identity.UserID, Level: level, Passed: passed, Reasons: reasons}
	err := h.appendAccessVerification(channel, server, record, identity.UserID)
	response["level"] = level
	response["passed"] = passed
	response["reasons"] = reasons
	response["recorded"] = err == nil
	if err != nil {
		response["record_error"] = "verification_history_save_failed"
	}
	writeJSON(w, response)
}

func accessVerificationHistory(channel data.NodeEdge, server int) []domainaccess.VerificationRecord {
	point, ok := channel.Points.Find(domainaccess.VerificationPoint, strconv.Itoa(server))
	if !ok || point.Tombstone%2 != 0 {
		return []domainaccess.VerificationRecord{}
	}
	var records []domainaccess.VerificationRecord
	if json.Unmarshal([]byte(point.Txt()), &records) != nil {
		return []domainaccess.VerificationRecord{}
	}
	return records
}

func (h *Handler) appendAccessVerification(channel data.NodeEdge, server int, record domainaccess.VerificationRecord, origin string) error {
	records := append(accessVerificationHistory(channel, server), record)
	if len(records) > 20 {
		records = records[len(records)-20:]
	}
	encoded, err := json.Marshal(records)
	if err != nil {
		return err
	}
	point := data.NewPointString(domainaccess.VerificationPoint, strconv.Itoa(server), string(encoded))
	point.Origin = origin
	return client.SendNodePoint(h.nc, channel.ID, point, true)
}
