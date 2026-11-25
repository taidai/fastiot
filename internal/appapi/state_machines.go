package appapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"time"

	"fastiot/internal/decision"
	"fastiot/internal/ems/command"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

const stateMachineType = "stateMachine"
const stateMachineDocumentPoint = "stateMachineDocument"

type stateMachineRequest struct {
	Parent     string                          `json:"parent"`
	Name       string                          `json:"name"`
	Definition decision.StateMachineDefinition `json:"definition"`
	Revision   uint64                          `json:"revision"`
	Event      string                          `json:"event"`
	Confirm    bool                            `json:"confirm"`
	Inputs     map[string]float64              `json:"inputs"`
	TextInputs map[string]string               `json:"text_inputs"`
	At         string                          `json:"at"`
}

type stateMachineResponse struct {
	ID            string                           `json:"id"`
	Parent        string                           `json:"parent"`
	Name          string                           `json:"name"`
	Revision      uint64                           `json:"revision"`
	Version       uint64                           `json:"version"`
	Draft         *decision.StateMachineDefinition `json:"draft,omitempty"`
	Applied       *decision.StateMachineDefinition `json:"applied,omitempty"`
	CurrentState  string                           `json:"current_state"`
	LastExecution *decision.StateMachineExecution  `json:"last_execution,omitempty"`
	Permissions   map[string]bool                  `json:"permissions"`
	Simulation    *decision.StateMachineSimulation `json:"simulation,omitempty"`
	Execution     *decision.StateMachineExecution  `json:"execution,omitempty"`
}

func decodeStateMachine(node data.NodeEdge) (decision.StateMachineDocument, error) {
	var document decision.StateMachineDocument
	text, ok := node.Points.Text(stateMachineDocumentPoint, "")
	if node.Type != stateMachineType || !ok || json.Unmarshal([]byte(text), &document) != nil || document.Schema != 1 || document.Revision == 0 {
		return document, errors.New("invalid_state_machine_document")
	}
	return document, document.Draft.Validate()
}

func (h *Handler) stateMachineResult(node data.NodeEdge, document decision.StateMachineDocument, identity Identity) stateMachineResponse {
	return stateMachineResponse{
		ID: node.ID, Parent: node.Parent, Name: nodeName(node), Revision: document.Revision, Version: document.Version,
		Draft: &document.Draft, Applied: document.Applied, CurrentState: document.CurrentState, LastExecution: document.LastExecution,
		Permissions: map[string]bool{"edit": canAction(identity, ActionConfigure), "publish": canAction(identity, ActionRulePublish), "execute": canAction(identity, ActionCommand)},
	}
}

// stateMachines is registered by the shared handler owner. Published machines
// execute only explicit API events; this handler does not claim a background
// event subscription that the application has not wired.
func (h *Handler) stateMachines(w http.ResponseWriter, r *http.Request, identity Identity, path []string) {
	if len(path) == 0 {
		switch r.Method {
		case http.MethodGet:
			nodes, err := h.visibleNodes(identity)
			if err != nil {
				writeError(w, 503, "state_machine_unavailable")
				return
			}
			items := []stateMachineResponse{}
			for _, node := range nodes {
				if node.Type != stateMachineType {
					continue
				}
				document, err := decodeStateMachine(node)
				if err != nil {
					writeError(w, 503, "invalid_state_machine_document")
					return
				}
				items = append(items, h.stateMachineResult(node, document, identity))
			}
			writeJSON(w, map[string]any{"state_machines": items, "can_create": canAction(identity, ActionConfigure)})
		case http.MethodPost:
			h.createStateMachine(w, r, identity)
		default:
			writeError(w, 405, "method_not_allowed")
		}
		return
	}
	node, ok := h.routeNode(w, path[0])
	if !ok {
		return
	}
	if node.Type != stateMachineType {
		writeError(w, 404, "not_found")
		return
	}
	document, err := decodeStateMachine(node)
	if err != nil {
		writeError(w, 503, "invalid_state_machine_document")
		return
	}
	if len(path) == 1 && r.Method == http.MethodGet {
		writeJSON(w, h.stateMachineResult(node, document, identity))
		return
	}
	if len(path) != 2 || r.Method != http.MethodPost {
		writeError(w, 405, "method_not_allowed")
		return
	}
	var input stateMachineRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	switch path[1] {
	case "draft":
		if !canAction(identity, ActionConfigure) {
			writeError(w, 403, "forbidden")
			return
		}
		if err := h.validateStateMachineScope(node.Parent, input.Definition); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		next, err := document.SaveDraft(input.Definition, input.Revision)
		if err != nil {
			inputError(w, err)
			return
		}
		h.persistStateMachine(w, node, next, identity)
	case "publish":
		if !canAction(identity, ActionRulePublish) {
			writeError(w, 403, "forbidden")
			return
		}
		next, err := document.Publish(input.Revision)
		if err != nil {
			inputError(w, err)
			return
		}
		if err := h.validateStateMachineScope(node.Parent, *next.Applied); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		h.ownershipMu.Lock()
		defer h.ownershipMu.Unlock()
		if err := decision.ValidateOutputOwnership(h.nc, node.ID, next.Applied.DecisionDocument()); err != nil {
			writeError(w, 409, "output_owned")
			return
		}
		h.persistStateMachine(w, node, next, identity)
	case "simulate":
		at, err := decision.ParseSimulationTime(input.At, time.Now())
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		current := document.CurrentState
		definition := document.Draft
		if input.Definition.InitialState != "" {
			definition = input.Definition
		}
		result, err := definition.Simulate(current, input.Event, input.Inputs, input.TextInputs, at)
		if err != nil {
			inputError(w, err)
			return
		}
		response := h.stateMachineResult(node, document, identity)
		response.Simulation = &result
		writeJSON(w, response)
	case "execute":
		if !canAction(identity, ActionCommand) {
			writeError(w, 403, "forbidden")
			return
		}
		if !input.Confirm {
			writeError(w, 400, "confirmation_required")
			return
		}
		h.ownershipMu.Lock()
		defer h.ownershipMu.Unlock()
		h.executeStateMachine(w, r, node, document, identity, input)
	default:
		writeError(w, 404, "not_found")
	}
}

func (h *Handler) createStateMachine(w http.ResponseWriter, r *http.Request, identity Identity) {
	if !canAction(identity, ActionConfigure) {
		writeError(w, 403, "forbidden")
		return
	}
	var input stateMachineRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Parent == "" || input.Name == "" {
		writeError(w, 400, "invalid_state_machine")
		return
	}
	parent, ok := h.routeNode(w, input.Parent)
	if !ok {
		return
	}
	if err := h.validateStateMachineScope(parent.ID, input.Definition); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	document, err := decision.NewStateMachineDocument(input.Definition)
	if err != nil {
		inputError(w, err)
		return
	}
	encoded, _ := json.Marshal(document)
	node := data.NodeEdge{ID: data.NewID(), Type: stateMachineType, Parent: parent.ID, Points: data.Points{
		data.NewPointString(data.PointTypeDescription, "", input.Name), data.NewPointString(stateMachineDocumentPoint, "", string(encoded)),
	}}
	if client.SendNode(h.nc, node, identity.UserID) != nil || h.verifyStateMachine(node.ID, string(encoded)) != nil {
		writeError(w, 503, "state_machine_persistence_unconfirmed")
		return
	}
	writeJSON(w, h.stateMachineResult(node, document, identity))
}

func (h *Handler) persistStateMachine(w http.ResponseWriter, node data.NodeEdge, document decision.StateMachineDocument, identity Identity) {
	encoded, _ := json.Marshal(document)
	point := data.NewPointString(stateMachineDocumentPoint, "", string(encoded))
	point.Origin = identity.UserID
	if client.SendNodePoint(h.nc, node.ID, point, true) != nil || h.verifyStateMachine(node.ID, string(encoded)) != nil {
		writeError(w, 503, "state_machine_persistence_unconfirmed")
		return
	}
	writeJSON(w, h.stateMachineResult(node, document, identity))
}

func (h *Handler) verifyStateMachine(id, expected string) error {
	nodes, err := client.GetNodes(h.nc, "all", id, "", false)
	if err != nil || len(nodes) == 0 {
		return errors.New("state_machine_persistence_unconfirmed")
	}
	text, ok := nodes[0].Points.Text(stateMachineDocumentPoint, "")
	if !ok || text != expected {
		return errors.New("state_machine_persistence_unconfirmed")
	}
	return nil
}

func (h *Handler) validateStateMachineScope(parent string, definition decision.StateMachineDefinition) error {
	if err := definition.Validate(); err != nil {
		return err
	}
	for pointID, target := range definition.Bindings.Points {
		if err := validateStateMachineTarget(h, parent, definition.Scope, target, false, nil); err != nil {
			return fmt.Errorf("point %s: %w", pointID, err)
		}
	}
	for _, transition := range definition.Transitions {
		for _, action := range transition.Actions {
			target := definition.Bindings.Actions[action.ActionID]
			if err := validateStateMachineTarget(h, parent, definition.Scope, target, true, &action.Value); err != nil {
				return fmt.Errorf("action %s: %w", action.ActionID, err)
			}
			if action.Readback.Target.NodeID != "" {
				if err := validateStateMachineTarget(h, parent, definition.Scope, action.Readback.Target, false, nil); err != nil {
					return fmt.Errorf("action %s readback: %w", action.ActionID, err)
				}
			}
		}
	}
	return nil
}

func validateStateMachineTarget(h *Handler, parent, scope string, target decision.Target, output bool, value *float64) error {
	if scope == "subtree" {
		return decision.ValidateTarget(h.nc, parent, target, output, value)
	}
	nodes, err := client.GetNodes(h.nc, "all", target.NodeID, "", false)
	if err != nil || len(nodes) == 0 || nodes[0].Parent != parent {
		return errors.New("target_out_of_scope")
	}
	return decision.ValidateTarget(h.nc, parent, target, output, value)
}

func (h *Handler) executeStateMachine(w http.ResponseWriter, r *http.Request, node data.NodeEdge, document decision.StateMachineDocument, identity Identity, input stateMachineRequest) {
	if document.Applied == nil || document.Revision != input.Revision {
		writeError(w, 409, "revision_conflict")
		return
	}
	if err := h.validateStateMachineScope(node.Parent, *document.Applied); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	values, texts, err := decision.ReadLiveInputs(h.nc, node.Parent, document.Applied.Bindings.Points)
	if err != nil {
		writeError(w, 409, "input_unavailable")
		return
	}
	plan, err := document.Plan(input.Event, values, texts, time.Now())
	if err != nil {
		inputError(w, err)
		return
	}
	execution := &decision.StateMachineExecution{At: time.Now().UTC(), Event: input.Event, Status: plan.Status, TransitionID: plan.TransitionID, From: plan.From, To: plan.To, Reason: plan.Reason}
	if plan.Status == "matched" {
		execution.Status = "confirmed"
		for _, transition := range document.Applied.Transitions {
			if transition.ID == plan.TransitionID {
				execution.Actions = h.executeStateMachineActions(r, identity, transition.Actions, document.Applied.Bindings.Actions)
				for _, action := range execution.Actions {
					if action.Status != "confirmed" {
						execution.Status = action.Status
						execution.Reason = action.Reason
						break
					}
				}
				break
			}
		}
	}
	previousState := document.CurrentState
	if execution.Status == "confirmed" {
		document.CurrentState = plan.To
	} else {
		execution.To = document.CurrentState
	}
	document.LastExecution = execution
	document.Revision++
	encoded, _ := json.Marshal(document)
	point := data.NewPointString(stateMachineDocumentPoint, "", string(encoded))
	point.Origin = identity.UserID
	if client.SendNodePoint(h.nc, node.ID, point, true) != nil || h.verifyStateMachine(node.ID, string(encoded)) != nil {
		// The device actions may already be confirmed; returning only an error
		// would hide that fact. Report the execution and the state that is
		// actually persisted, and require a refresh before any retry.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": "state_persistence_unconfirmed", "id": node.ID, "revision": document.Revision - 1,
			"current_state": previousState, "execution": execution, "persisted": false,
		})
		return
	}
	response := h.stateMachineResult(node, document, identity)
	response.Execution = execution
	writeJSON(w, response)
}

func (h *Handler) executeStateMachineActions(r *http.Request, identity Identity, actions []decision.Action, bindings map[string]decision.Target) []decision.CheckAction {
	checks := make([]decision.CheckAction, len(actions))
	for i, action := range actions {
		checks[i] = decision.CheckAction{Output: action.ActionID, Requested: action.Value, Applied: action.Value, Status: "pending"}
	}
	for i, action := range actions {
		if action.DelayMS > 0 {
			timer := time.NewTimer(time.Duration(action.DelayMS) * time.Millisecond)
			select {
			case <-r.Context().Done():
				timer.Stop()
				checks[i].Status = "unknown"
				checks[i].Reason = "execution cancelled before action"
				skipStateMachineActions(checks[i+1:])
				return checks
			case <-timer.C:
			}
		}
		result, reason := h.writeStateMachineValue(r, identity, bindings[action.ActionID], action.Value)
		checks[i].Applied = result.Target
		if result.Status == "accepted" {
			result = h.waitStateMachineReadback(r, bindings[action.ActionID], action, result)
		}
		checks[i].Status = result.Status
		checks[i].Reason = result.Reason
		if checks[i].Reason == "" && result.Status != "confirmed" && reason != "" {
			checks[i].Reason = reason
		}
		if result.Status != "confirmed" {
			skipStateMachineActions(checks[i+1:])
			return checks
		}
	}
	return checks
}

func skipStateMachineActions(checks []decision.CheckAction) {
	for i := range checks {
		checks[i].Status = "skipped"
		checks[i].Reason = "previous action was not confirmed"
	}
}

func (h *Handler) writeStateMachineValue(r *http.Request, identity Identity, target decision.Target, value float64) (CommandResult, string) {
	body, _ := json.Marshal(map[string]any{"value": value, "point_type": data.PointTypeValueSet, "request_id": data.NewID()})
	request := httptest.NewRequest(http.MethodPost, "/api/nodes/"+target.NodeID+"/value", bytes.NewReader(body)).WithContext(r.Context())
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	h.writeValue(recorder, request, identity, target.NodeID)
	var result CommandResult
	_ = json.Unmarshal(recorder.Body.Bytes(), &result)
	if recorder.Code != 200 {
		var apiError map[string]string
		_ = json.Unmarshal(recorder.Body.Bytes(), &apiError)
		if apiError["error"] != "" {
			reason := apiError["error"]
			if result.Status == "" {
				result.Status = "failed"
			}
			return result, reason
		}
	}
	if result.Status == "" {
		result.Status = "failed"
	}
	return result, recorder.Body.String()
}

func (h *Handler) waitStateMachineReadback(r *http.Request, target decision.Target, action decision.Action, result CommandResult) CommandResult {
	readTarget := action.Readback.Target
	if readTarget.NodeID == "" {
		readTarget.NodeID = target.NodeID
	}
	if readTarget.PointType == "" {
		readTarget.PointType = data.PointTypeValue
	}
	deadline := time.Now().Add(time.Duration(action.Readback.TimeoutMS) * time.Millisecond)
	for time.Now().Before(deadline) {
		nodes, err := client.GetNodes(h.nc, "all", readTarget.NodeID, "", false)
		if err == nil && len(nodes) > 0 {
			if point, ok := nodes[0].Points.Find(readTarget.PointType, readTarget.Key); ok && point.Tombstone%2 == 0 && point.Time.After(result.CreatedAt) {
				if value, err := point.ValueFloat(); err == nil && finite(value) {
					if math.Abs(value-result.Target) <= action.Readback.Tolerance {
						return h.advanceCommand(target.NodeID, result, command.Confirmed, "")
					}
					return h.advanceCommand(target.NodeID, result, command.Failed, "readback value mismatch")
				}
			}
		}
		select {
		case <-r.Context().Done():
			return h.advanceCommand(target.NodeID, result, command.Unknown, "execution cancelled; result unknown")
		case <-time.After(20 * time.Millisecond):
		}
	}
	return h.advanceCommand(target.NodeID, result, command.Unknown, "no readback before timeout")
}
