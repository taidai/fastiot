package appapi

import (
	"encoding/json"
	"fmt"
	"time"

	"fastiot/internal/decision"
	"fastiot/internal/ems/observation"
	"fastiot/internal/ems/signal"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

// validateInputBinding is opt-in for decision inputs only. ValidateTarget stays
// unchanged for safety envelopes, writes, and physical/process readbacks.
func (h *Handler) validateInputBinding(node data.NodeEdge, target decision.Target) error {
	switch target.Use {
	case "", "decision", "protection", "interlock", "readback":
	default:
		return fmt.Errorf("unknown_input_use")
	}
	if node.Type == calculationType {
		if target.Use == "" || target.Use == "readback" || (target.PointType != "" && target.PointType != "value") || (target.Key != "" && target.Key != "0") {
			return fmt.Errorf("invalid_calculation_binding")
		}
		document, err := decodeCalculation(node)
		if err != nil || document.Applied == nil {
			return fmt.Errorf("calculation_not_applied")
		}
		return nil
	}
	if node.Type == virtualInputType {
		if target.Use == "" || target.Use == "readback" || (target.PointType != "" && target.PointType != "value") || (target.Key != "" && target.Key != "0") {
			return fmt.Errorf("invalid_virtual_input_binding")
		}
		document, err := decodeVirtualInput(node)
		if err != nil || document.Applied == nil {
			return fmt.Errorf("virtual_input_not_applied")
		}
		return nil
	}
	if target.PointType != aggregateValuePoint {
		return decision.ValidateTarget(h.nc, node.Parent, target, false, nil)
	}
	if target.Use == "" || target.Use == "readback" || (target.Key != "" && target.Key != "0") {
		return fmt.Errorf("derived_input_use_required")
	}
	var config AggregateConfig
	point, ok := node.Points.Find(aggregatePoint, "")
	if !ok || point.Tombstone%2 != 0 || json.Unmarshal([]byte(point.Txt()), &config) != nil {
		return fmt.Errorf("invalid_aggregate_source")
	}
	if _, err := parseAggregateWindow(config.Window); err != nil || !aggregateOps[config.Op] || len(config.Members) == 0 {
		return fmt.Errorf("invalid_aggregate_source")
	}
	if config.Window != "" && target.Use != "decision" {
		return fmt.Errorf("window_input_not_allowed_for_protection")
	}
	return nil
}

func (h *Handler) readBoundInput(target decision.Target, now time.Time) *decision.InputObservation {
	nodes, err := client.GetNodes(h.nc, "all", target.NodeID, "", false)
	if err != nil || len(nodes) == 0 {
		return nil
	}
	node := nodes[0]
	if target.PointType == aggregateValuePoint {
		config, ok := h.loadAggregate(node.ID)
		if !ok {
			return &decision.InputObservation{Source: "aggregate", Quality: "invalid", Reason: "missing_definition"}
		}
		input := aggregateInput(h.aggregateSnapshot(config))
		return &input
	}
	if node.Type == calculationType {
		input := &decision.InputObservation{Source: "calculation", Software: true, Kind: "number", Quality: "uninitialized", Reason: "pending_calculation"}
		document, err := decodeCalculation(node)
		if err != nil || document.Applied == nil {
			input.Quality, input.Reason = "invalid", "not_applied"
			return input
		}
		result, ok := h.CalculationRuntime().Snapshot().Results[node.ID]
		if !ok || result.Version != document.Version {
			return input
		}
		input.DefinitionVersion = result.Version
		input.Value, input.Unit, input.Quality, input.Reason = result.Value, result.Unit, result.Quality, result.Reason
		input.EvaluatedAt, input.Simulated, input.TimeSource = result.CalculatedAt, result.Simulated, "oldest_input_observation_or_change"
		input.Dependencies = append([]string(nil), result.Dependencies...)
		for _, leaf := range result.Leaves {
			input.Dependencies = append(input.Dependencies, leaf.ID)
			at := leaf.SourceTime
			if at.IsZero() {
				at = leaf.ChangedAt
			}
			if !at.IsZero() && (input.Time == nil || at.Before(*input.Time)) {
				input.Time = &at
			}
		}
		freshness := result.Freshness(now)
		if freshness.Quality.String() != "good" {
			input.Quality, input.Reason = freshness.Quality.String(), freshness.Reason
		}
		if input.Quality != "good" {
			input.Value = nil
		}
		return input
	}
	if node.Type != virtualInputType {
		return nil
	}
	input := &decision.InputObservation{Source: "virtual", Software: true, Quality: "invalid", Reason: "invalid_input_document", EvaluatedAt: now, TimeSource: "input_change"}
	document, err := decodeVirtualInput(node)
	if err != nil {
		return input
	}
	h.virtualInputMu.Lock()
	view := document.Evaluate(h.inputClock(time.Now()))
	h.virtualInputMu.Unlock()
	input.Source, input.Quality, input.Reason, input.Simulated = document.Source, view.Quality, view.Reason, document.Source == "simulation"
	if document.Applied != nil {
		input.Kind, input.Unit = document.Applied.Kind, document.Applied.Unit
	}
	if document.Current != nil {
		at := document.Current.EnteredAt
		input.Time = &at
	}
	if view.Quality != "good" {
		return input
	}
	switch input.Kind {
	case "number":
		if json.Unmarshal(view.Value, &input.Value) != nil {
			input.Quality, input.Reason = "invalid", "invalid_value"
		}
	case "bool":
		var value bool
		if json.Unmarshal(view.Value, &value) != nil {
			input.Quality = "invalid"
		} else {
			number := data.BoolToFloat(value)
			input.Value = &number
		}
	case "enum":
		if json.Unmarshal(view.Value, &input.Text) != nil {
			input.Quality = "invalid"
		}
	}
	return input
}

func (h *Handler) inputVisible(identity Identity, target decision.Target) bool {
	if !h.allows(identity, target.NodeID) {
		return false
	}
	nodes, err := client.GetNodes(h.nc, "all", target.NodeID, "", false)
	if err != nil || len(nodes) == 0 {
		return false
	}
	if nodes[0].Type == calculationType {
		catalog, err := h.loadCalculationCatalog()
		document, ok := catalog.documents[target.NodeID]
		if err != nil || !ok || document.Applied == nil || !h.readableCalculation(identity, *document.Applied, catalog) {
			return false
		}
	}
	if config, ok := h.loadAggregate(target.NodeID); ok && !h.aggregateVisible(identity, h.aggregateSnapshot(config)) {
		return false
	}
	return true
}

func (h *Handler) inputEvidenceVisible(identity Identity, input decision.InputObservation) bool {
	for _, id := range input.Dependencies {
		if !h.allows(identity, id) {
			return false
		}
	}
	return true
}

func (h *Handler) validateMachineInput(parent, scope string, target decision.Target) error {
	nodes, err := client.GetNodes(h.nc, "all", target.NodeID, "", false)
	if err != nil || len(nodes) == 0 {
		return fmt.Errorf("input_unavailable")
	}
	node := nodes[0]
	if node.ID != parent && (scope != "subtree" && node.Parent != parent || scope == "subtree" && !h.users.IsUnder(node.ID, parent)) {
		return fmt.Errorf("target_out_of_scope")
	}
	return h.validateInputBinding(node, target)
}

func (h *Handler) readMachineInputs(parent string, bindings map[string]decision.Target) (map[string]float64, map[string]string, map[string]decision.InputObservation, error) {
	values, texts := map[string]float64{}, map[string]string{}
	observations := map[string]decision.InputObservation{}
	for id, target := range bindings {
		if input := h.readBoundInput(target, time.Now()); input != nil {
			if ok, reason := input.Sample(id).Usable(time.Now(), 0); !ok {
				return nil, nil, nil, fmt.Errorf("input %s: %s", id, reason)
			}
			observations[id] = *input
			if input.Kind == "enum" {
				texts[id] = input.Text
			} else if input.Value != nil {
				values[id] = *input.Value
			}
			continue
		}
		numbers, labels, err := decision.ReadLiveInputs(h.nc, parent, map[string]decision.Target{id: target})
		if err != nil {
			return nil, nil, nil, err
		}
		for id, value := range numbers {
			values[id] = value
		}
		for id, value := range labels {
			texts[id] = value
		}
	}
	return values, texts, observations, nil
}

func (h *Handler) publicInputNodes(identity Identity, nodes []data.NodeEdge) []data.NodeEdge {
	out := append([]data.NodeEdge(nil), nodes...)
	for i, node := range out {
		if node.Type != virtualInputType && node.Type != calculationType {
			continue
		}
		if !h.inputVisible(identity, decision.Target{NodeID: node.ID}) {
			continue
		}
		out[i].Points = append(data.Points(nil), node.Points...)
		input := h.readBoundInput(decision.Target{NodeID: node.ID, Use: "decision"}, time.Now())
		if input == nil {
			continue
		}
		visible := true
		for _, id := range input.Dependencies {
			visible = visible && h.allows(identity, id)
		}
		if !visible {
			continue
		}
		encoded, _ := json.Marshal(input)
		out[i].Points = append(out[i].Points, data.NewPointString("easyemsInput", "0", string(encoded)))
	}
	return out
}

func aggregateInput(result AggregateResult) decision.InputObservation {
	input := decision.InputObservation{Value: result.Value, Kind: "number", Unit: result.Config.Units, Quality: result.Quality, Source: "aggregate", Time: result.Time, EvaluatedAt: result.EvaluatedAt, TimeSource: result.TimeSource, Window: result.Config.Window, LagSeconds: result.LagSeconds, Dependencies: append([]string(nil), result.Config.Members...)}
	for _, member := range result.Members {
		input.Software = input.Software || member.TimeSource == "node_value"
		input.Dependencies = append(input.Dependencies, member.NodeID)
	}
	if input.Quality == "good" && (input.Value == nil || input.Time == nil) {
		input.Quality, input.Reason = "invalid", "missing_observation"
	}
	if input.Quality == "good" && input.Value != nil && input.Time != nil {
		maxAge := observation.DefaultMaxAge
		if result.Config.Window != "" {
			window, err := parseAggregateWindow(result.Config.Window)
			if err != nil {
				input.Quality, input.Reason = "invalid", "invalid_window"
			} else {
				maxAge = window
			}
		}
		if input.Quality == "good" {
			verdict := observation.Evaluate(observation.Input{Value: signal.Num(*input.Value), Quality: signal.Good, SourceTime: *input.Time, MaxAge: maxAge}, time.Now())
			input.Quality, input.Reason = verdict.Quality.String(), verdict.Reason
		}
	}
	if input.Quality != "good" || input.Time == nil {
		input.Value = nil
	}
	return input
}
