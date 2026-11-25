package appapi

import (
	"bytes"
	"encoding/json"
	"fmt"

	domainaccess "fastiot/internal/domain/access"

	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

const screenDocumentVersion = 1

type screenDocument struct {
	Layer    string              `json:"layer"`
	Version  int                 `json:"version"`
	Title    string              `json:"title"`
	Columns  int                 `json:"columns"`
	PointIDs []string            `json:"point_ids"`
	Slots    map[string][]string `json:"slots"`
	Blocks   []screenBlock       `json:"blocks"`
}

type screenBlock struct {
	Type    string         `json:"type"`
	Binding map[string]any `json:"binding"`
	Span    int            `json:"span"`
}

type screenMetric struct {
	PointID string `json:"point_id,omitempty"`
	Metric  string `json:"metric,omitempty"`
	Label   string `json:"label,omitempty"`
	Target  string `json:"target,omitempty"`
}

var screenBlockLayers = map[string]map[string]bool{
	"status": nil, "kpi": nil, "recent": nil, "alarm": nil, "today": nil, "trend": nil, "summary": nil,
	"flow": {"site": true, "area": true}, "children": {"site": true, "area": true},
	"members": {"unit": true}, "deviceData": {"device": true}, "oplog": {"device": true},
	"devices": {"site": true, "area": true, "unit": true},
}

var automaticScreenMetrics = map[string]bool{
	"online_devices": true, "active_alarms": true, "communication_success_rate": true,
	"data_freshness": true, "today_energy": true, "today_supply": true, "run_duration": true,
}

func screenLayerKind(node data.NodeEdge) string {
	if node.Parent == "root" || node.Parent == "" {
		return "site"
	}
	switch node.Type {
	case NodeTypeSite, NodeTypePlant:
		return "site"
	case data.NodeTypeGroup, "area":
		return "area"
	case NodeTypeEnergyUnit, NodeTypeResourceGroup, "energy_unit", "unit":
		return "unit"
	case data.NodeTypeDevice, "modbus_device":
		return "device"
	default:
		return ""
	}
}

func savedScreenVersion(node data.NodeEdge) int {
	point, found := node.Points.Find(screenPointType, "")
	if !found || point.Tombstone%2 != 0 {
		return 0
	}
	var document struct {
		Version int `json:"version"`
	}
	if json.Unmarshal([]byte(point.Txt()), &document) != nil {
		return 0
	}
	return document.Version
}

func (h *Handler) validateScreenDocument(identity Identity, node data.NodeEdge, document *screenDocument) error {
	if document.Layer != node.ID || document.Version < screenDocumentVersion || document.Columns < 1 || document.Columns > 2 {
		return fmt.Errorf("invalid_screen")
	}
	kind := screenLayerKind(node)
	if kind == "" || len(document.Slots) != 3 {
		return fmt.Errorf("invalid_screen")
	}
	for _, slot := range []string{"top", "main", "side"} {
		if _, ok := document.Slots[slot]; !ok {
			return fmt.Errorf("invalid_screen")
		}
	}
	blocks := make(map[string]screenBlock, len(document.Blocks))
	pointIDs := []string{}
	for _, block := range document.Blocks {
		layers, known := screenBlockLayers[block.Type]
		if !known || layers != nil && !layers[kind] || block.Span < 1 || block.Span > 2 || blocks[block.Type].Type != "" {
			return fmt.Errorf("invalid_block")
		}
		blocks[block.Type] = block
		ids, err := h.validateScreenBinding(identity, node, block)
		if err != nil {
			return err
		}
		if block.Type == "kpi" {
			pointIDs = append(pointIDs, ids...)
		}
	}
	slotted := map[string]bool{}
	for slot, ids := range document.Slots {
		for _, id := range ids {
			if slotted[id] || blocks[id].Type == "" || screenBlockSlot(id) != slot {
				return fmt.Errorf("invalid_slots")
			}
			slotted[id] = true
		}
	}
	if len(slotted) != len(blocks) {
		return fmt.Errorf("invalid_slots")
	}
	document.PointIDs = pointIDs
	return nil
}

func screenBlockSlot(kind string) string {
	switch kind {
	case "status", "kpi":
		return "top"
	case "flow", "children", "members", "deviceData", "devices", "recent":
		return "main"
	default:
		return "side"
	}
}

func (h *Handler) validateScreenBinding(identity Identity, node data.NodeEdge, block screenBlock) ([]string, error) {
	binding := block.Binding
	if binding == nil {
		binding = map[string]any{}
	}
	allowedKeys := map[string]map[string]bool{
		"kpi": {"metrics": true}, "recent": {"point_ids": true, "columns": true, "rows": true},
		"trend": {"point_id": true}, "devices": {"columns": true}, "deviceData": {"tabs": true}, "flow": {"detail": true},
	}
	for key := range binding {
		if !allowedKeys[block.Type][key] {
			return nil, fmt.Errorf("invalid_binding")
		}
	}
	switch block.Type {
	case "kpi":
		var metrics []screenMetric
		if err := remarshalScreenBinding(binding["metrics"], &metrics); err != nil || len(metrics) < 4 || len(metrics) > 6 {
			return nil, fmt.Errorf("invalid_binding")
		}
		ids := []string{}
		for _, metric := range metrics {
			if metric.PointID != "" {
				if metric.Metric != "" || !h.screenBindingReferenceAllowed(identity, node.ID, metric.PointID) {
					return nil, fmt.Errorf("invalid_binding")
				}
				ids = append(ids, metric.PointID)
			} else if !automaticScreenMetrics[metric.Metric] {
				return nil, fmt.Errorf("invalid_binding")
			}
		}
		return ids, nil
	case "recent":
		var input struct {
			PointIDs []string `json:"point_ids"`
			Columns  []string `json:"columns"`
			Rows     int      `json:"rows"`
		}
		if err := remarshalScreenBinding(binding, &input); err != nil || input.Rows != 3 && input.Rows != 5 && input.Rows != 10 || len(input.Columns) == 0 {
			return nil, fmt.Errorf("invalid_binding")
		}
		for _, column := range input.Columns {
			if column != "value" && column != "quality" && column != "time" {
				return nil, fmt.Errorf("invalid_binding")
			}
		}
		return input.PointIDs, h.validateScreenReferences(identity, node.ID, input.PointIDs)
	case "trend":
		var input struct {
			PointID string `json:"point_id"`
		}
		if err := remarshalScreenBinding(binding, &input); err != nil {
			return nil, fmt.Errorf("invalid_binding")
		}
		if input.PointID != "" && !h.screenBindingReferenceAllowed(identity, node.ID, input.PointID) {
			return nil, fmt.Errorf("invalid_binding")
		}
		return []string{input.PointID}, nil
	case "devices":
		return nil, validateStringEnum(binding["columns"], map[string]bool{"type": true, "state": true, "value": true, "communication": true, "updated": true})
	case "deviceData":
		return nil, validateStringEnum(binding["tabs"], map[string]bool{"realtime": true, "parameters": true})
	case "flow":
		if detail, ok := binding["detail"]; ok {
			if _, valid := detail.(bool); !valid {
				return nil, fmt.Errorf("invalid_binding")
			}
		}
		return nil, nil
	default:
		if len(binding) != 0 {
			return nil, fmt.Errorf("invalid_binding")
		}
		return nil, nil
	}
}

func remarshalScreenBinding(input any, target any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func validateStringEnum(input any, allowed map[string]bool) error {
	var values []string
	if err := remarshalScreenBinding(input, &values); err != nil || len(values) == 0 {
		return fmt.Errorf("invalid_binding")
	}
	for _, value := range values {
		if !allowed[value] {
			return fmt.Errorf("invalid_binding")
		}
	}
	return nil
}

func (h *Handler) validateScreenReferences(identity Identity, layer string, ids []string) error {
	for _, id := range ids {
		if id == "" || !h.screenBindingReferenceAllowed(identity, layer, id) {
			return fmt.Errorf("invalid_binding")
		}
	}
	return nil
}

// Daily engineering declarations also support directly scoped acquisition points.
// Customer screen bindings additionally require a bound business device.
func (h *Handler) screenReferenceAllowed(identity Identity, layer, id string) bool {
	if id != "" && h.allows(identity, id) && (id == layer || h.users.IsUnder(id, layer)) {
		nodes, err := client.GetNodes(h.nc, "all", id, "", false)
		return err == nil && len(nodes) > 0
	}
	return h.screenBindingReferenceAllowed(identity, layer, id)
}

func (h *Handler) screenBindingReferenceAllowed(identity Identity, layer, id string) bool {
	if !h.allows(identity, id) {
		return false
	}
	nodes, err := client.GetNodes(h.nc, "all", id, "", false)
	if err != nil || len(nodes) == 0 {
		return false
	}
	if nodes[0].Type != "variable" && nodes[0].Type != data.NodeTypeModbusIO && nodes[0].Type != calculationType {
		return false
	}
	point := nodes[0]
	if point.Type == data.NodeTypeModbusIO {
		channels, err := client.GetNodes(h.nc, "all", point.Parent, "", false)
		if err != nil || len(channels) == 0 {
			return false
		}
		server, _ := point.Points.ValueInt(data.PointTypeID, "")
		model, bound := unitModelFor(channels[0], server)
		return bound && model.BusinessNodeID != "" && h.allows(identity, model.BusinessNodeID) && h.screenReferenceInLayer(identity, layer, model.BusinessNodeID)
	}
	return h.screenReferenceInLayer(identity, layer, id)
}

func (h *Handler) screenReferenceInLayer(identity Identity, layer, id string) bool {
	if id == layer || h.users.IsUnder(id, layer) {
		return true
	}
	nodes, err := client.GetNodes(h.nc, "all", layer, "", false)
	if err != nil || len(nodes) == 0 || screenLayerKind(nodes[0]) != "unit" {
		return false
	}
	point, found := nodes[0].Points.Find(domainaccess.MembersPoint, "")
	if !found || point.Tombstone%2 != 0 {
		return false
	}
	var members []domainaccess.MemberRole
	if json.Unmarshal([]byte(point.Txt()), &members) != nil {
		return false
	}
	for _, member := range members {
		if member.NodeID != "" && h.allows(identity, member.NodeID) && (id == member.NodeID || h.users.IsUnder(id, member.NodeID)) {
			return true
		}
	}
	return false
}
