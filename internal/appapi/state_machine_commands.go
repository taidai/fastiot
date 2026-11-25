package appapi

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"fastiot/internal/decision"
	"fastiot/internal/ems/command"
	"fastiot/internal/ems/device"
	"fastiot/internal/ems/safety"
	"fastiot/internal/ems/signal"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

// Resolve the stable business device through the same Profile/model registry
// used by the 07 device command endpoint. Transport identities are not authored.
func (h *Handler) resolveMachineCommand(parent, scope string, binding decision.StateMachineCommandBinding) (string, unitModelConfig, device.Command, error) {
	nodes, err := client.GetNodes(h.nc, "all", binding.DeviceID, "", false)
	if err != nil || len(nodes) != 1 {
		return "", unitModelConfig{}, device.Command{}, fmt.Errorf("command_device_unavailable")
	}
	if binding.DeviceID != parent && nodes[0].Parent != parent && (scope != "subtree" || !h.users.IsUnder(binding.DeviceID, parent)) {
		return "", unitModelConfig{}, device.Command{}, fmt.Errorf("target_out_of_scope")
	}
	buses, err := client.GetNodes(h.nc, "all", "all", data.NodeTypeModbus, false)
	if err != nil {
		return "", unitModelConfig{}, device.Command{}, err
	}
	var runtimeID string
	var model unitModelConfig
	for _, bus := range buses {
		for _, point := range bus.Points {
			if point.Type != unitModelPoint || point.Tombstone%2 != 0 {
				continue
			}
			server, err := strconv.Atoi(point.Key)
			if err != nil {
				continue
			}
			candidate, ok := unitModelFor(bus, server)
			if !ok || candidate.BusinessNodeID != binding.DeviceID {
				continue
			}
			if runtimeID != "" {
				return "", model, device.Command{}, fmt.Errorf("ambiguous_command_device")
			}
			runtimeID, model = fmt.Sprintf("%s/%d", bus.ID, server), candidate
		}
	}
	if runtimeID == "" {
		return "", model, device.Command{}, fmt.Errorf("unit_model_not_configured")
	}
	profile, ok := h.commandProfile(runtimeID)
	if !ok {
		return "", model, device.Command{}, fmt.Errorf("command_points_not_mapped")
	}
	declared, ok := profile.Command(binding.CommandID)
	if !ok {
		return "", model, declared, fmt.Errorf("command_not_available")
	}
	allowed := map[string]bool{}
	for _, parameter := range declared.Parameters {
		allowed[parameter.Name] = true
		value, supplied := binding.Parameters[parameter.Name]
		if !supplied {
			value = parameter.Default
		}
		if !finite(value) || (parameter.HasRange && (value < parameter.Min || value > parameter.Max)) || ((parameter.Binary || parameter.Kind == signal.Bool) && value != 0 && value != 1) {
			return "", model, declared, fmt.Errorf("invalid_command_parameter:%s", parameter.Name)
		}
	}
	for key := range binding.Parameters {
		if !allowed[key] {
			return "", model, declared, fmt.Errorf("unknown_command_parameter:%s", key)
		}
	}
	return runtimeID, model, declared, nil
}

func (h *Handler) submitMachineCommand(r *http.Request, identity Identity, parent, scope string, binding decision.StateMachineCommandBinding, requestID, eventID string) CommandResult {
	fail := func(reason string) CommandResult {
		return CommandResult{RequestID: requestID, Status: "rejected", Reason: reason}
	}
	runtimeID, model, _, err := h.resolveMachineCommand(parent, scope, binding)
	if err != nil {
		return fail(err.Error())
	}
	if !canAction(identity, ActionCommand) || !h.operationScope(identity, model) {
		return fail("out_of_scope")
	}
	if verificationPending(model) {
		return fail("verification_required")
	}
	if h.commands() == nil {
		return fail("command_runtime_unavailable")
	}
	if h.ems.Service.State.Health(runtimeID) == "offline" {
		return fail("device_offline")
	}
	outcome, err := h.commands().Submit(time.Now(), command.Request{ID: requestID, DeviceID: runtimeID, CommandID: binding.CommandID, Parameters: binding.Parameters, Actor: identity.UserID, Source: "state_machine", DispatchID: eventID})
	if err != nil {
		return CommandResult{RequestID: requestID, Status: "unknown", Reason: err.Error()}
	}
	return h.waitMachineCommand(r, outcome)
}

func (h *Handler) waitMachineCommand(r *http.Request, outcome command.Outcome) CommandResult {
	manager := h.commandManager()
	for outcome.State == command.Accepted || outcome.State == command.Executing {
		manager.Tick(time.Now())
		current, ok := manager.Get(outcome.RequestID)
		if !ok {
			return CommandResult{RequestID: outcome.RequestID, Status: "unknown", Reason: "command_state_unavailable"}
		}
		outcome = current
		if outcome.State != command.Accepted && outcome.State != command.Executing {
			break
		}
		select {
		case <-r.Context().Done():
			result := projectCommandResult(outcome)
			result.Status, result.Reason = "unknown", "execution cancelled; canonical command may still be pending"
			return result
		case <-time.After(20 * time.Millisecond):
		}
	}
	return projectCommandResult(outcome)
}

func (h *Handler) submitMachinePoint(r *http.Request, identity Identity, target decision.Target, value float64, requestID string) (CommandResult, string) {
	fail := func(reason string) (CommandResult, string) {
		return CommandResult{RequestID: requestID, Status: "rejected", Reason: reason}, reason
	}
	if !canAction(identity, ActionCommand) || !h.allows(identity, target.NodeID) {
		return fail("out_of_scope")
	}
	if r.Context().Err() != nil {
		return fail("execution_cancelled")
	}
	nodes, err := client.GetNodes(h.nc, "all", target.NodeID, "", false)
	if err != nil || len(nodes) != 1 {
		return fail("target_unavailable")
	}
	node := nodes[0]
	if node.Type != data.NodeTypeVariable && node.Type != data.NodeTypeModbusIO {
		return fail("operation_not_allowed")
	}
	manager := h.commandManager()
	if manager == nil {
		return fail("command_runtime_unavailable")
	}
	request := command.Request{ID: requestID, DeviceID: node.ID, Point: node.ID, Value: signal.Num(value), Actor: identity.UserID, Source: "state_machine"}
	if node.Type == data.NodeTypeModbusIO {
		buses, err := client.GetNodes(h.nc, "all", node.Parent, "", false)
		if err != nil || len(buses) != 1 {
			return fail("command_mapping_unavailable")
		}
		server, _ := node.Points.ValueInt(data.PointTypeID, "")
		if model, found := unitModelFor(buses[0], server); found && model.ProfileID != "" {
			for _, mapped := range model.PointMap {
				if mapped == node.ID {
					return fail("declared_profile_command_required")
				}
			}
		}
		if h.commands() == nil || server < 1 || server > 247 {
			return fail("command_runtime_unavailable")
		}
		request.DeviceID = fmt.Sprintf("%s/%d", node.Parent, server)
		outcome, err := manager.Submit(time.Now(), request)
		if err != nil {
			return fail(err.Error())
		}
		return h.waitMachineCommand(r, outcome), ""
	}
	// Software point actions use the existing caller-advanced canonical tracker;
	// they never masquerade as physical-device feedback or bypass manual takeover.
	verdict := h.commandGuard(command.GuardRequest{Point: node.ID, Automatic: true, Writes: []device.Write{{Point: node.ID, Value: signal.Num(value)}}}).Effective()
	if verdict.Decision == safety.DecisionClamped {
		request.Value = signal.Num(verdict.Value)
	}
	if old, found, err := manager.Existing(request); err != nil {
		return fail(err.Error())
	} else if found {
		return projectCommandResult(old), ""
	}
	outcome, err := manager.Track(time.Now(), request)
	if err != nil {
		return fail("command_state_unavailable")
	}
	if verdict.Decision == safety.DecisionRefused {
		return h.advanceCommand(node.ID, projectCommandResult(outcome), command.Rejected, verdict.Reason), verdict.Reason
	}
	point := data.NewPointFloat(data.PointTypeValueSet, target.Key, request.Value.Num)
	point.Origin = identity.UserID
	if err := client.SendNodePoint(h.nc, node.ID, point, true); err != nil {
		return h.advanceCommand(node.ID, projectCommandResult(outcome), command.Unknown, "write_result_unknown"), "write_result_unknown"
	}
	_ = h.projectCommand(node.ID, outcome)
	return projectCommandResult(outcome), ""
}
