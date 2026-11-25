package appapi

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"time"

	"fastiot/internal/decision"
	"fastiot/internal/ems/command"
	"fastiot/internal/ems/device"
	"fastiot/internal/ems/safety"
	"fastiot/internal/ems/signal"
	"github.com/nats-io/nats.go"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

const ruleAuthorizationPoint = "ruleAuthorization"

type ruleAuthorization struct {
	Revision  string    `json:"revision"`
	Parent    string    `json:"parent"`
	Anchors   []string  `json:"anchors"`
	GrantedBy string    `json:"granted_by"`
	GrantedAt time.Time `json:"granted_at"`
}

// ruleWriteTargets resolves every fixed write and parameter-default write of a
// declared command, not merely the representative point selected by the editor.
func (h *Handler) ruleWriteTargets(document decision.Document) ([]decision.Target, error) {
	targets := []decision.Target{}
	for _, row := range document.Table.Rows {
		for _, action := range row.Actions {
			target := document.Bindings.Actions[action.ActionID]
			if target.CommandID == "" {
				targets = append(targets, target)
				continue
			}
			nodes, err := client.GetNodes(h.nc, "all", target.NodeID, "", false)
			if err != nil || len(nodes) == 0 {
				return nil, fmt.Errorf("command target unavailable")
			}
			server, _ := nodes[0].Points.ValueInt(data.PointTypeID, "")
			profile, ok := h.commandProfile(nodes[0].Parent + "/" + strconv.Itoa(server))
			if !ok {
				return nil, fmt.Errorf("command profile unavailable")
			}
			declared, ok := profile.Command(target.CommandID)
			if !ok {
				return nil, fmt.Errorf("declared command unavailable")
			}
			for _, write := range declared.Writes {
				targets = append(targets, decision.Target{NodeID: write.Point, PointType: data.PointTypeValueSet})
			}
			for _, parameter := range declared.Parameters {
				targets = append(targets, decision.Target{NodeID: parameter.Point, PointType: data.PointTypeValueSet})
			}
		}
	}
	return targets, nil
}

// ruleExecutionSupport fails before publication/enable rather than accepting a
// rule whose legacy variable/allocator/ramp transport would bypass commands.
func (h *Handler) ruleExecutionSupport(identity Identity, parent string, document decision.Document) error {
	for _, row := range document.Table.Rows {
		for _, action := range row.Actions {
			target := document.Bindings.Actions[action.ActionID]
			nodes, err := client.GetNodes(h.nc, "all", target.NodeID, "", false)
			if err != nil || len(nodes) == 0 {
				return fmt.Errorf("rule_target_unavailable:%s", target.NodeID)
			}
			node := nodes[0]
			if node.Type != data.NodeTypeModbusIO && node.Type != data.NodeTypeVariable {
				return fmt.Errorf("unsupported_rule_adapter:%s:%s", target.NodeID, node.Type)
			}
			safety, present, err := decision.LoadOutputSafety(h.nc, target.NodeID)
			if err != nil {
				return fmt.Errorf("invalid_safety_config:%s", target.NodeID)
			}
			if present && safety.RampPerSecond != nil && *safety.RampPerSecond > 0 {
				return fmt.Errorf("unsupported_rule_ramp:%s", target.NodeID)
			}
			if node.Type == data.NodeTypeVariable {
				if target.CommandID != "" || target.Parameter != "" {
					return fmt.Errorf("software_variable_has_no_device_command:%s", target.NodeID)
				}
				if action.Readback != nil && action.Readback.Target.NodeID != "" {
					feedback, err := client.GetNodes(h.nc, "all", action.Readback.Target.NodeID, "", false)
					if err != nil || len(feedback) == 0 || feedback[0].Type != data.NodeTypeVariable {
						return fmt.Errorf("software_readback_requires_variable:%s", target.NodeID)
					}
				}
				continue
			}
			channels, err := client.GetNodes(h.nc, "all", node.Parent, "", false)
			if err != nil || len(channels) == 0 {
				return fmt.Errorf("rule_channel_unavailable:%s", target.NodeID)
			}
			server, _ := node.Points.ValueInt(data.PointTypeID, "")
			if model, bound := unitModelFor(channels[0], server); bound && model.ProfileID != "" {
				if !h.operationScope(identity, model) {
					return fmt.Errorf("command references outside authorized scope")
				}
				for _, id := range model.PointMap {
					if !h.ruleTargetAllowed(identity, parent, document.Scope, decision.Target{NodeID: id}, false) {
						return fmt.Errorf("command mapping outside rule scope")
					}
				}
				if target.CommandID == "" {
					return fmt.Errorf("declared_profile_command_required:%s", target.NodeID)
				}
				profile, ok := h.commandProfile(node.Parent + "/" + strconv.Itoa(server))
				if !ok {
					return fmt.Errorf("command_points_not_mapped:%s", target.NodeID)
				}
				if err := profile.Validate(); err != nil {
					return err
				}
				if _, ok := profile.Command(target.CommandID); !ok {
					return fmt.Errorf("command_not_available:%s", target.CommandID)
				}
			} else if target.CommandID != "" {
				return fmt.Errorf("unit_model_not_configured:%s", target.NodeID)
			}
		}
	}
	return nil
}

// Software variables use the same caller-advanced canonical seam as ticket14.
// Authorization and the durable execution reservation are checked by the caller.
// An existing identity, including an unfinished/unknown one, is never written again.
func (h *Handler) submitRuleSoftware(input decision.RuleCommand) (command.Outcome, error) {
	h.commandMu.Lock()
	defer h.commandMu.Unlock()
	manager := h.commands()
	request := command.Request{ID: input.RequestID, DeviceID: input.Target.NodeID, Point: input.Target.NodeID, Value: signal.Num(input.Action.Value), Actor: "rule:" + input.RuleID, Source: "rule:" + input.RuleID + ":" + input.Revision, DispatchID: input.ExecutionID, Stage: "software:" + input.RowID}
	verdict := h.commandGuard(command.GuardRequest{Point: input.Target.NodeID, Automatic: true, Writes: []device.Write{{Point: input.Target.NodeID, Value: request.Value}}}).Effective()
	if verdict.Decision == safety.DecisionClamped {
		request.Value = signal.Num(verdict.Value)
	}
	if old, found, err := manager.Existing(request); err != nil || found {
		return old, err
	}
	outcome, err := manager.Track(time.Now(), request)
	if err != nil {
		return outcome, err
	}
	if verdict.Decision == safety.DecisionRefused {
		updated, ok := manager.SetState(time.Now(), request.ID, command.Rejected, verdict.Reason)
		if !ok || updated.State != command.Rejected {
			return updated, fmt.Errorf("command_state_unavailable")
		}
		return updated, nil
	}
	point := data.NewPointFloat(data.PointTypeValueSet, input.Target.Key, request.Value.Num)
	point.Origin = request.Actor
	if err := client.SendNodePoint(h.nc, input.Target.NodeID, point, true); err != nil {
		updated, _ := manager.SetState(time.Now(), request.ID, command.Unknown, "software write result unknown; not retried")
		return updated, err
	}
	return outcome, nil
}

// Only a fresh software-value acknowledgment advances an External outcome.
// This is not physical feedback: Observed remains empty and the sequence labels
// the confirmation as software. Terminal outcomes are immutable at this seam.
func (h *Handler) observeRuleSoftware(id string, target decision.Target, timeoutMS int, tolerance float64) (command.Outcome, bool) {
	h.commandMu.Lock()
	defer h.commandMu.Unlock()
	manager := h.commands()
	if manager == nil {
		return command.Outcome{}, false
	}
	outcome, found, err := manager.Find(id)
	if err != nil || !found {
		return outcome, false
	}
	if !outcome.External || outcome.State != command.Accepted {
		return outcome, true
	}
	now := time.Now()
	deadline := outcome.StartedAt.Add(time.Duration(timeoutMS) * time.Millisecond)
	state, reason := command.Accepted, ""
	nodes, err := client.GetNodes(h.nc, "all", target.NodeID, "", false)
	if err == nil && len(nodes) > 0 && nodes[0].Type == data.NodeTypeVariable && len(outcome.Writes) == 1 {
		quality, hasQuality := nodes[0].Points.Text("quality", "")
		point, ok := nodes[0].Points.Find(target.PointType, target.Key)
		if ok && point.Tombstone%2 == 0 && (!hasQuality || quality == "good") && point.Time.After(outcome.StartedAt) && !point.Time.After(now) && !point.Time.After(deadline) {
			value, valueErr := point.ValueFloat()
			if valueErr == nil && finite(value) {
				state, reason = command.Confirmed, "software value acknowledged; not device feedback"
				if math.Abs(value-outcome.Writes[0].Value.Num) > tolerance {
					state, reason = command.Failed, "software value mismatch"
				}
			}
		}
	}
	if state == command.Accepted && !now.Before(deadline) {
		state, reason = command.Unknown, "software acknowledgment timeout; result unknown"
	}
	if state != command.Accepted {
		if updated, ok := manager.SetState(now, id, state, reason); ok {
			return updated, true
		}
	}
	return outcome, true
}

func (h *Handler) stopRuleSoftware(id string) {
	h.commandMu.Lock()
	defer h.commandMu.Unlock()
	manager := h.commands()
	if manager == nil {
		return
	}
	outcome, found, err := manager.Find(id)
	if err == nil && found && outcome.External && outcome.State == command.Accepted {
		manager.SetState(time.Now(), id, command.Unknown, "rule stopped before software acknowledgment")
	}
}

// NewRuleClient is the sole production decision runtime adapter. Saving,
// publishing and simulating never call it or submit a device command.
func (h *Handler) NewRuleClient(nc *nats.Conn, config decision.DecisionTable) client.Client {
	observations := func(now time.Time) map[string]signal.Sample {
		samples := map[string]signal.Sample{}
		if h.ems == nil || h.ems.Service == nil {
			return samples
		}
		for _, value := range h.ems.Service.State.Snapshot(now, h.ems.Service.MaxAge) {
			samples[value.PointID] = signal.Sample{Input: value.PointID, Value: value.Value, Quality: value.Quality, SourceTime: value.SourceTime, ReceivedTime: value.ReceivedTime}
		}
		return samples
	}
	return decision.NewDecisionTableClientWithCommands(nc, config, observations, decision.RuleCommands{
		Validate:         func(id string, doc decision.Document) error { _, err := h.ruleRuntimeAuthority(id, doc); return err },
		Submit:           h.submitRuleCommand,
		SoftwareFeedback: h.observeRuleSoftware,
		StopSoftware:     h.stopRuleSoftware,
		Get: func(id string) (command.Outcome, bool) {
			manager := h.commands()
			if manager == nil {
				return command.Outcome{}, false
			}
			outcome, found, err := manager.Find(id)
			return outcome, found && err == nil
		},
	})
}

func (h *Handler) ruleRuntimeAuthority(id string, document decision.Document) (Identity, error) {
	var identity Identity
	nodes, err := client.GetNodes(h.nc, "all", id, "", false)
	if err != nil || len(nodes) == 0 || nodes[0].Type != "decisionTable" {
		return identity, fmt.Errorf("rule unavailable")
	}
	node := nodes[0]
	enabled, _ := node.Points.Value("enabled", "")
	applied, err := documentSource(node, "applied")
	if enabled == 0 || err != nil || document.Revision == "" || !reflect.DeepEqual(applied, document) {
		return identity, fmt.Errorf("rule version is not enabled")
	}
	var grant ruleAuthorization
	text, _ := node.Points.Text(ruleAuthorizationPoint, "")
	if json.Unmarshal([]byte(text), &grant) != nil || grant.Revision != document.Revision || grant.Parent != node.Parent || grant.GrantedBy == "" {
		return identity, fmt.Errorf("rule authorization required")
	}
	// Project authorization is independent of a login session. Its persisted
	// anchors remain an upper bound, never expanded by business membership.
	identity.Anchors = grant.Anchors
	if !h.ruleReferencesAllowed(identity, node.Parent, document) {
		return identity, fmt.Errorf("rule references outside authorized scope")
	}
	return identity, nil
}

func (h *Handler) submitRuleCommand(input decision.RuleCommand) (command.Outcome, error) {
	h.virtualInputMu.Lock()
	defer h.virtualInputMu.Unlock()
	h.ownershipMu.Lock()
	defer h.ownershipMu.Unlock()
	manager := h.commands()
	if manager == nil || manager.Persist == nil || manager.Lookup == nil {
		return command.Outcome{}, fmt.Errorf("durable command runtime unavailable")
	}
	nodes, err := client.GetNodes(h.nc, "all", input.RuleID, "", false)
	if err != nil || len(nodes) == 0 {
		return command.Outcome{}, fmt.Errorf("rule unavailable")
	}
	document, err := documentSource(nodes[0], "applied")
	if err != nil || document.Revision != input.Revision {
		return command.Outcome{}, fmt.Errorf("rule revision changed")
	}
	identity, err := h.ruleRuntimeAuthority(input.RuleID, document)
	if err != nil {
		return command.Outcome{}, err
	}
	if err := decision.ValidateResolvedOutputOwnership(h.nc, input.RuleID, document, h.ruleWriteTargets); err != nil {
		return command.Outcome{}, err
	}
	var reservation decision.ExecutionReservation
	reserved, _ := nodes[0].Points.Text("lastExecution", "")
	if json.Unmarshal([]byte(reserved), &reservation) != nil || reservation.ID != input.ExecutionID || reservation.Row != input.RowID || reservation.Revision != input.Revision {
		return command.Outcome{}, fmt.Errorf("rule execution was not reserved")
	}
	valid := false
	for _, row := range document.Table.Rows {
		if row.ID == input.RowID && input.Step >= 0 && input.Step < len(row.Actions) {
			action := row.Actions[input.Step]
			valid = reflect.DeepEqual(action, input.Action) && document.Bindings.Actions[action.ActionID] == input.Target
		}
	}
	if !valid || input.ExecutionID == "" || input.RequestID != fmt.Sprintf("%s-%d", input.ExecutionID, input.Step) {
		return command.Outcome{}, fmt.Errorf("invalid rule action identity")
	}
	points, err := client.GetNodes(h.nc, "all", input.Target.NodeID, "", false)
	if err != nil || len(points) == 0 {
		return command.Outcome{}, fmt.Errorf("target unavailable")
	}
	point := points[0]
	if point.Type == data.NodeTypeVariable {
		return h.submitRuleSoftware(input)
	}
	if point.Type != data.NodeTypeModbusIO {
		return command.Outcome{}, fmt.Errorf("rule action requires a canonical device adapter")
	}
	server, _ := point.Points.ValueInt(data.PointTypeID, "")
	deviceID := point.Parent + "/" + strconv.Itoa(server)
	channels, err := client.GetNodes(h.nc, "all", point.Parent, "", false)
	if err != nil || len(channels) == 0 || !h.ems.Service.HasDevice(deviceID) {
		return command.Outcome{}, fmt.Errorf("device unavailable")
	}
	request := command.Request{ID: input.RequestID, DeviceID: deviceID, Actor: "rule:" + input.RuleID, Source: "rule:" + input.RuleID + ":" + input.Revision, DispatchID: input.ExecutionID, Stage: input.RowID}
	model, bound := unitModelFor(channels[0], server)
	if bound && model.ProfileID != "" {
		if !h.operationScope(identity, model) {
			return command.Outcome{}, fmt.Errorf("command references outside authorized scope")
		}
		if input.Target.CommandID == "" {
			return command.Outcome{}, fmt.Errorf("declared_profile_command_required")
		}
		if verificationPending(model) {
			return command.Outcome{}, fmt.Errorf("verification_required")
		}
		profile, ok := h.commandProfile(deviceID)
		if !ok {
			return command.Outcome{}, fmt.Errorf("command_points_not_mapped")
		}
		declared, ok := profile.Command(input.Target.CommandID)
		if !ok {
			return command.Outcome{}, fmt.Errorf("command_not_available")
		}
		boundValue := false
		if input.Target.Parameter != "" {
			for _, parameter := range declared.Parameters {
				if parameter.Name == input.Target.Parameter && parameter.Point == point.ID {
					boundValue = true
				}
			}
		} else {
			for _, write := range declared.Writes {
				value := write.Value.Num
				if write.Value.Kind == signal.Bool {
					value = data.BoolToFloat(write.Value.Bool)
				}
				if write.Point == point.ID && (write.Value.Kind == signal.Number || write.Value.Kind == signal.Bool) && value == input.Action.Value {
					boundValue = true
				}
			}
		}
		if !boundValue {
			return command.Outcome{}, fmt.Errorf("command_value_binding_mismatch")
		}
		for _, id := range model.PointMap {
			if !h.ruleTargetAllowed(identity, nodes[0].Parent, document.Scope, decision.Target{NodeID: id}, false) {
				return command.Outcome{}, fmt.Errorf("command mapping outside rule scope")
			}
		}
		request.CommandID = input.Target.CommandID
		if input.Target.Parameter != "" {
			request.Parameters = map[string]float64{input.Target.Parameter: input.Action.Value}
		}
	} else {
		if input.Target.CommandID != "" {
			return command.Outcome{}, fmt.Errorf("unit_model_not_configured")
		}
		request.Point = point.ID
		request.Value = signal.Num(input.Action.Value)
	}
	// Submit owns guard evaluation, durable reservation, deduplication, transport,
	// and time/quality/deadline-qualified readback. Never fall back to valueSet.
	return manager.Submit(time.Now(), request)
}
