package appapi

import (
	"encoding/json"
	"fmt"

	"fastiot/internal/decision"
	"github.com/simpleiot/simpleiot/data"
)

func machineConfigurationPoint(point data.Point) (data.Point, error) {
	var document decision.StateMachineDocument
	if json.Unmarshal([]byte(point.Txt()), &document) != nil || document.Schema != 1 {
		return point, fmt.Errorf("invalid_state_machine_document")
	}
	document.CurrentState, document.LastExecution = "", nil
	encoded, _ := json.Marshal(document)
	return data.NewPointString(point.Type, point.Key, string(encoded)), nil
}

// Applying historical engineering must neither roll back process memory nor
// reuse a publication number. All machine changes are prepared before the
// generic project restore writes anything; incompatible state is an error.
func (h *Handler) prepareMachineVersionRestore(snapshot versionNode, current data.NodeEdge) (versionNode, error) {
	if current.ID == "" {
		return snapshot, fmt.Errorf("state_machine_restore_requires_import")
	}
	live, err := decodeStateMachine(current)
	if err != nil {
		return snapshot, err
	}
	runs, err := machineRuns(current)
	if err != nil {
		return snapshot, err
	}
	for _, run := range runs {
		if !run.Finished {
			return snapshot, fmt.Errorf("state_machine_execution_pending")
		}
	}
	points := data.Points{}
	found := false
	for _, point := range snapshot.Points {
		if point.Type == data.PointTypeDisabled || point.Type == stateMachineRunPoint {
			continue
		}
		if point.Type != stateMachineDocumentPoint {
			points = append(points, point)
			continue
		}
		var frozen decision.StateMachineDocument
		if json.Unmarshal([]byte(point.Txt()), &frozen) != nil || frozen.Schema != 1 {
			return snapshot, fmt.Errorf("invalid_state_machine_document")
		}
		if err := h.validateStateMachineScope(current.Parent, frozen.Draft); err != nil {
			return snapshot, err
		}
		definition := frozen.Draft
		if frozen.Applied != nil {
			if err := h.validateStateMachineScope(current.Parent, *frozen.Applied); err != nil {
				return snapshot, err
			}
			definition = *frozen.Applied
		}
		compatible := false
		for _, state := range definition.States {
			compatible = compatible || state == live.CurrentState
		}
		if !compatible {
			return snapshot, fmt.Errorf("state_machine_current_state_not_defined")
		}
		frozen.CurrentState, frozen.LastExecution = live.CurrentState, live.LastExecution
		frozen.Revision, frozen.Version, frozen.Versions = live.Revision, live.Version, live.Versions
		// Preserve the current published definition in history even for older stores
		// whose Versions map had not yet been materialized.
		if frozen.Versions == nil {
			frozen.Versions = map[uint64]decision.StateMachineDefinition{}
		}
		if live.Applied != nil {
			frozen.Versions[live.Version] = *live.Applied
		}
		if frozen.Applied != nil {
			draft := frozen.Draft
			frozen.Draft = *frozen.Applied
			// Publish records the *previous* Applied under Version, so use the actual
			// current definition here, not the historical one being applied.
			frozen.Applied = live.Applied
			frozen, err = frozen.Publish(frozen.Revision)
			if err != nil {
				return snapshot, err
			}
			frozen.Draft = draft
		} else {
			frozen.Revision++
		}
		encoded, _ := json.Marshal(frozen)
		points = append(points, data.NewPointString(point.Type, point.Key, string(encoded)))
		found = true
	}
	if !found {
		return snapshot, fmt.Errorf("invalid_state_machine_document")
	}
	snapshot.Points = points
	return snapshot, nil
}
