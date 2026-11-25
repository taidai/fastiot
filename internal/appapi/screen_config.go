package appapi

import (
	"encoding/json"
	"fmt"

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
	for _, ids := range document.Slots {
		for _, id := range ids {
			if slotted[id] || blocks[id].Type == "" {
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

func (h *Handler) validateScreenBinding(identity Identity, node data.NodeEdge, block screenBlock) ([]string, error) {
	binding := block.Binding
	if binding == nil {
		binding = map[string]any{}
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
				if metric.Metric != "" || !h.screenReferenceAllowed(identity, node.ID, metric.PointID) {
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
		if input.PointID != "" && !h.screenReferenceAllowed(identity, node.ID, input.PointID) {
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
	return json.Unmarshal(raw, target)
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
		if id == "" || !h.screenReferenceAllowed(identity, layer, id) {
			return fmt.Errorf("invalid_binding")
		}
	}
	return nil
}

func (h *Handler) screenReferenceAllowed(identity Identity, layer, id string) bool {
	if !h.allows(identity, id) {
		return false
	}
	nodes, err := client.GetNodes(h.nc, "all", id, "", false)
	if err != nil || len(nodes) == 0 {
		return false
	}
	return id == layer || h.users.IsUnder(id, layer)
}
