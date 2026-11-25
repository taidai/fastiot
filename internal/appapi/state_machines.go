package appapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
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
	RequestID  string                          `json:"request_id"`
	Event      string                          `json:"event"`
	Confirm    bool                            `json:"confirm"`
	Inputs     map[string]float64              `json:"inputs"`
	TextInputs map[string]string               `json:"text_inputs"`
	At         string                          `json:"at"`
}

type stateMachineResponse struct {
	Restricted    map[string]string                          `json:"restricted,omitempty"`
	Pending       *stateMachineRun                           `json:"pending,omitempty"`
	Versions      map[uint64]decision.StateMachineDefinition `json:"versions,omitempty"`
	ID            string                                     `json:"id"`
	Parent        string                                     `json:"parent"`
	Name          string                                     `json:"name"`
	Revision      uint64                                     `json:"revision"`
	Version       uint64                                     `json:"version"`
	Draft         *decision.StateMachineDefinition           `json:"draft,omitempty"`
	Applied       *decision.StateMachineDefinition           `json:"applied,omitempty"`
	CurrentState  string                                     `json:"current_state"`
	LastExecution *decision.StateMachineExecution            `json:"last_execution,omitempty"`
	Permissions   map[string]bool                            `json:"permissions"`
	Simulation    *decision.StateMachineSimulation           `json:"simulation,omitempty"`
	Execution     *decision.StateMachineExecution            `json:"execution,omitempty"`
}

func decodeStateMachine(node data.NodeEdge) (decision.StateMachineDocument, error) {
	var document decision.StateMachineDocument
	text, ok := node.Points.Text(stateMachineDocumentPoint, "")
	if node.Type != stateMachineType || !ok || json.Unmarshal([]byte(text), &document) != nil || document.Schema != 1 || document.Revision == 0 {
		return document, errors.New("invalid_state_machine_document")
	}
	return document, document.Draft.Validate()
}

func (h *Handler) stateMachineResult(node data.NodeEdge, document decision.StateMachineDocument, identity Identity, execution ...*decision.StateMachineExecution) stateMachineResponse {
	last := document.LastExecution
	var pending *stateMachineRun
	runs, _ := machineRuns(node)
	for _, run := range runs {
		if !run.Finished {
			pending = &run
			continue
		}
		if last == nil || run.Execution.At.After(last.At) {
			execution := run.Execution
			last = &execution
		}
	}
	response := stateMachineResponse{
		Pending: pending, Versions: document.Versions,
		ID: node.ID, Parent: node.Parent, Name: nodeName(node), Revision: document.Revision, Version: document.Version,
		Draft: &document.Draft, Applied: document.Applied, CurrentState: document.CurrentState, LastExecution: last,
		Permissions: map[string]bool{"edit": canAction(identity, ActionConfigure), "publish": canAction(identity, ActionRulePublish), "execute": canAction(identity, ActionCommand)},
	}
	if len(execution) != 0 {
		response.Execution = execution[0]
	}
	return h.scopeMachineResponse(identity, document, runs, response)
}

// stateMachines is registered by the shared handler owner. Published machines
// execute only explicit API events; this handler does not claim a background
// event subscription that the application has not wired.
func (h *Handler) stateMachines(w http.ResponseWriter, r *http.Request, identity Identity, path []string) {
	// ponytail: one mutation lock per runtime; use per-machine locks if event
	// throughput needs it. Reads and pure simulation can observe pending intent.
	if r.Method != http.MethodGet && !(len(path) == 2 && path[1] == "simulate") {
		h.stateMachineMu.Lock()
		defer h.stateMachineMu.Unlock()
	}
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
		if !h.machineDefinitionVisible(identity, document.Draft) {
			writeError(w, 403, "source_scope_restricted")
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
		if err := definition.Validate(); err != nil {
			inputError(w, err)
			return
		}
		if !h.machineDefinitionVisible(identity, definition) {
			writeError(w, 403, "source_scope_restricted")
			return
		}
		result, err := definition.Simulate(current, input.Event, input.Inputs, input.TextInputs, at)
		if err != nil {
			inputError(w, err)
			return
		}
		response := h.stateMachineResult(node, document, identity)
		if h.machineDefinitionVisible(identity, definition) {
			response.Simulation = &result
		} else {
			response.Restricted["simulation"] = "source_scope_restricted"
		}
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
		if err := h.validateMachineInput(parent, definition.Scope, target); err != nil {
			return fmt.Errorf("point %s: %w", pointID, err)
		}
	}
	for _, row := range definition.DecisionDocument().Table.Rows {
		for _, action := range row.Actions {
			if binding, named := definition.CommandBindings[action.ActionID]; named {
				if _, _, _, err := h.resolveMachineCommand(parent, definition.Scope, binding); err != nil {
					return fmt.Errorf("action %s: %w", action.ActionID, err)
				}
				if action.Readback != nil {
					if err := validateStateMachineTarget(h, parent, definition.Scope, action.Readback.Target, false, nil); err != nil {
						return fmt.Errorf("action %s process readback: %w", action.ActionID, err)
					}
				}
				continue
			}
			target := definition.Bindings.Actions[action.ActionID]
			if target.Key != "" && target.Key != "0" {
				return fmt.Errorf("action %s: unsupported output key", action.ActionID)
			}
			if err := validateStateMachineTarget(h, parent, definition.Scope, target, true, &action.Value); err != nil {
				return fmt.Errorf("action %s: %w", action.ActionID, err)
			}
			readback := action.Readback.Target
			if readback.NodeID == "" {
				readback.NodeID = target.NodeID
			}
			if err := validateStateMachineTarget(h, parent, definition.Scope, readback, false, nil); err != nil {
				return fmt.Errorf("action %s readback: %w", action.ActionID, err)
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
	if input.RequestID == "" || len(input.RequestID) > 128 || input.Event == "" {
		writeError(w, 400, "event_request_id_required")
		return
	}
	// Live execution never accepts caller-supplied observations or definitions.
	if input.Definition.InitialState != "" || len(input.Inputs) != 0 || len(input.TextInputs) != 0 || input.At != "" {
		writeError(w, 400, "simulation_inputs_not_executable")
		return
	}
	if h.replayMachineRun(w, node, document, identity, input) {
		return
	}
	if disabled, _ := node.Points.Value(data.PointTypeDisabled, ""); disabled != 0 {
		writeError(w, 409, "state_machine_disabled")
		return
	}
	if document.Applied == nil || document.Revision != input.Revision {
		writeError(w, 409, "revision_conflict")
		return
	}
	if !h.machineDefinitionVisible(identity, *document.Applied) {
		writeError(w, 403, "source_scope_restricted")
		return
	}
	if err := h.validateStateMachineScope(node.Parent, *document.Applied); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	allowed := true
	document.Applied.MapReferences(func(id string) string { allowed = allowed && h.allows(identity, id); return id })
	if !allowed {
		writeError(w, 403, "target_out_of_scope")
		return
	}
	values, texts, observations, err := h.readMachineInputs(node.Parent, document.Applied.Bindings.Points)
	if err != nil {
		writeError(w, 409, "input_unavailable")
		return
	}
	plan, err := document.Plan(input.Event, values, texts, time.Now())
	if err != nil {
		inputError(w, err)
		return
	}
	execution := &decision.StateMachineExecution{InputObservations: observations, RequestID: input.RequestID, Version: document.Version, Trace: plan.Trace, At: time.Now().UTC(), Event: input.Event, Status: plan.Status, TransitionID: plan.TransitionID, From: plan.From, To: plan.To, Reason: plan.Reason}
	var actions []decision.Action
	if plan.Status == "matched" {
		for _, transition := range document.Applied.Transitions {
			if transition.ID == plan.TransitionID {
				actions = document.Applied.TransitionActions(plan.From, transition)
				break
			}
		}
		for i, action := range actions {
			target := document.Applied.Bindings.Actions[action.ActionID].NodeID
			if binding, named := document.Applied.CommandBindings[action.ActionID]; named {
				target = binding.DeviceID
			}
			execution.Commands = append(execution.Commands, decision.StateMachineCommand{RequestID: machineIdentity(node.ID, input.RequestID, fmt.Sprint(i)), NodeID: target})
		}
	}
	run := stateMachineRun{RequestID: input.RequestID, Actor: identity.UserID, Revision: input.Revision, Source: *document.Applied, Inputs: values, TextInputs: texts, Execution: *execution}
	run.Execution.Status, run.Execution.To = "pending", execution.From
	if err := h.storeMachineRun(node.ID, run); err != nil {
		writeError(w, 503, "state_machine_run_unconfirmed")
		return
	}
	if plan.Status == "matched" {
		execution.Status = "confirmed"
		execution.Actions = h.executeStateMachineActions(r, identity, node.Parent, *document.Applied, actions, execution.Commands, input.RequestID)
		for _, action := range execution.Actions {
			if action.Status != "confirmed" {
				execution.Status, execution.Reason = action.Status, action.Reason
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
		uncertain := *execution
		uncertain.Status, uncertain.To, uncertain.Reason = "unknown", previousState, "state_persistence_unconfirmed"
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		view := document
		view.Revision--
		view.CurrentState, view.LastExecution = previousState, &uncertain
		response := h.stateMachineResult(node, view, identity, &uncertain)
		_ = json.NewEncoder(w).Encode(struct {
			stateMachineResponse
			Error     string `json:"error"`
			Persisted string `json:"persisted"`
		}{response, "state_persistence_unconfirmed", "unknown"})
		return
	}
	run.Execution, run.Finished = *execution, true
	if err := h.storeMachineRun(node.ID, run); err != nil {
		writeError(w, 503, "state_machine_run_unconfirmed")
		return
	}
	response := h.stateMachineResult(node, document, identity, execution)
	writeJSON(w, response)
}

func (h *Handler) executeStateMachineActions(r *http.Request, identity Identity, parent string, definition decision.StateMachineDefinition, actions []decision.Action, commands []decision.StateMachineCommand, eventID string) []decision.CheckAction {
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
		if r.Context().Err() != nil {
			checks[i].Status, checks[i].Reason = "unknown", "execution cancelled before action"
			skipStateMachineActions(checks[i+1:])
			return checks
		}
		if err := h.validateStateMachineScope(parent, definition); err != nil {
			checks[i].Status, checks[i].Reason = "rejected", err.Error()
			skipStateMachineActions(checks[i+1:])
			return checks
		}
		var result CommandResult
		var reason string
		if binding, named := definition.CommandBindings[action.ActionID]; named {
			result = h.submitMachineCommand(r, identity, parent, definition.Scope, binding, commands[i].RequestID, eventID)
		} else {
			result, reason = h.submitMachinePoint(r, identity, definition.Bindings.Actions[action.ActionID], action.Value, commands[i].RequestID)
		}
		checks[i].Applied = result.Target
		if result.Status == "accepted" {
			// Only a software point uses the caller-advanced confirmation path.
			result = h.waitStateMachineReadback(r, definition.Bindings.Actions[action.ActionID], action, result, true)
		}
		if manager := h.commandManager(); manager != nil {
			if outcome, found, err := manager.Find(commands[i].RequestID); err == nil && found {
				commands[i].Status, commands[i].Reason = string(outcome.State), outcome.Reason
			}
		}
		if result.Status == "confirmed" && action.Readback != nil {
			// Process completion is separate from the immutable command outcome.
			result = h.waitStateMachineReadback(r, definition.Bindings.Actions[action.ActionID], action, result, false)
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

func (h *Handler) waitStateMachineReadback(r *http.Request, target decision.Target, action decision.Action, result CommandResult, softwareCommand bool) CommandResult {
	finish := func(state command.State, reason string) CommandResult {
		if softwareCommand {
			return h.advanceCommand(target.NodeID, result, state, reason)
		}
		result.Status, result.Reason = string(state), reason
		return result
	}
	readTarget := action.Readback.Target
	if readTarget.NodeID == "" {
		readTarget.NodeID = target.NodeID
	}
	if readTarget.PointType == "" {
		readTarget.PointType = data.PointTypeValue
	}
	deadline := time.Now().Add(time.Duration(action.Readback.TimeoutMS) * time.Millisecond)
	expected := result.Target
	if target.NodeID == "" {
		expected = action.Value
	}
	for time.Now().Before(deadline) {
		nodes, err := client.GetNodes(h.nc, "all", readTarget.NodeID, "", false)
		if err == nil && len(nodes) > 0 {
			if point, ok := nodes[0].Points.Find(readTarget.PointType, readTarget.Key); ok && point.Tombstone%2 == 0 && point.Time.After(result.CreatedAt) && !point.Time.After(time.Now()) {
				quality, _ := nodes[0].Points.Text("quality", "")
				_, _, freshErr := decision.ReadLiveInputs(h.nc, nodes[0].Parent, map[string]decision.Target{"readback": readTarget})
				if value, err := point.ValueFloat(); err == nil && finite(value) && (quality == "" || quality == "good") && freshErr == nil {
					if math.Abs(value-expected) <= action.Readback.Tolerance {
						return finish(command.Confirmed, "")
					}
					return finish(command.Failed, "process readback value mismatch")
				}
			}
		}
		select {
		case <-r.Context().Done():
			return finish(command.Unknown, "execution cancelled; result unknown")
		case <-time.After(20 * time.Millisecond):
		}
	}
	return finish(command.Unknown, "process readback timeout")
}
