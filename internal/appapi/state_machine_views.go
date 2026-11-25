package appapi

import (
	"fmt"

	"fastiot/internal/decision"
	"github.com/simpleiot/simpleiot/data"
)

// Read-only authorization: never rewrite references in the stored definition or
// delete entries from its shared version map while constructing a response.
func (h *Handler) machineDefinitionVisible(identity Identity, definition decision.StateMachineDefinition) bool {
	if definition.Validate() != nil {
		return false
	}
	visible := func(id string) bool { return id == "" || h.allows(identity, id) }
	for _, target := range definition.Bindings.Points {
		if !visible(target.NodeID) {
			return false
		}
	}
	for _, target := range definition.Bindings.Actions {
		if !visible(target.NodeID) {
			return false
		}
	}
	for _, binding := range definition.CommandBindings {
		if !visible(binding.DeviceID) {
			return false
		}
	}
	for _, row := range definition.DecisionDocument().Table.Rows {
		for _, action := range row.Actions {
			if action.Readback != nil && !visible(action.Readback.Target.NodeID) {
				return false
			}
		}
	}
	return true
}

func (h *Handler) machineExecutionVisible(identity Identity, execution *decision.StateMachineExecution, document decision.StateMachineDocument, runs []stateMachineRun) bool {
	if execution == nil {
		return true
	}
	sourceKnown := false
	for _, run := range runs {
		if execution.RequestID != "" && run.RequestID == execution.RequestID {
			sourceKnown = true
			if !h.machineDefinitionVisible(identity, run.Source) {
				return false
			}
		}
	}
	if source, ok := document.Versions[execution.Version]; ok {
		sourceKnown = true
		if !h.machineDefinitionVisible(identity, source) {
			return false
		}
	} else if execution.Version == document.Version && document.Applied != nil {
		sourceKnown = true
		if !h.machineDefinitionVisible(identity, *document.Applied) {
			return false
		}
	}
	if !sourceKnown {
		return false
	}
	for _, reference := range execution.Commands {
		if reference.NodeID == "" || !h.allows(identity, reference.NodeID) {
			return false
		}
	}
	return true
}

func (h *Handler) scopeMachineResponse(identity Identity, document decision.StateMachineDocument, runs []stateMachineRun, response stateMachineResponse) stateMachineResponse {
	restricted := map[string]string{}
	restrict := func(field string) { restricted[field] = "source_scope_restricted" }
	if response.Draft != nil && !h.machineDefinitionVisible(identity, *response.Draft) {
		response.Draft = nil
		restrict("draft")
	}
	if response.Applied != nil && !h.machineDefinitionVisible(identity, *response.Applied) {
		response.Applied = nil
		restrict("applied")
	}
	response.Versions = map[uint64]decision.StateMachineDefinition{}
	for version, definition := range document.Versions {
		if h.machineDefinitionVisible(identity, definition) {
			response.Versions[version] = definition
		} else {
			restrict(fmt.Sprintf("versions/%d", version))
		}
	}
	if !h.machineExecutionVisible(identity, response.LastExecution, document, runs) {
		response.LastExecution = nil
		restrict("last_execution")
	}
	if !h.machineExecutionVisible(identity, response.Execution, document, runs) {
		response.Execution = nil
		restrict("execution")
	}
	if response.Pending != nil && (!h.machineDefinitionVisible(identity, response.Pending.Source) || !h.machineExecutionVisible(identity, &response.Pending.Execution, document, runs)) {
		response.Pending = nil
		restrict("pending")
	}
	response.Restricted = restricted
	return response
}

// Generic snapshots/WebSockets must not bypass the scoped machine API by
// exposing raw definition histories or unbounded event ledgers. Copy the point
// slices so this public projection cannot damage execution or another identity.
func publicStateMachineNodes(nodes []data.NodeEdge) []data.NodeEdge {
	out := append([]data.NodeEdge(nil), nodes...)
	strip := func(points data.Points) data.Points {
		safe := make(data.Points, 0, len(points))
		for _, point := range points {
			if point.Type != stateMachineDocumentPoint && point.Type != stateMachineRunPoint {
				safe = append(safe, point)
			}
		}
		return safe
	}
	for i, node := range nodes {
		if node.Type != stateMachineType {
			continue
		}
		out[i].Points = append(strip(node.Points), data.NewPointString("stateMachineDetails", "0", "restricted_to_scoped_api"))
		out[i].EdgePoints = strip(node.EdgePoints)
	}
	return out
}
