package appapi

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"fastiot/internal/decision"
	"fastiot/internal/ems/command"
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
			if node.Type != data.NodeTypeModbusIO {
				return fmt.Errorf("unsupported_rule_adapter:%s:%s", target.NodeID, node.Type)
			}
			safety, present, err := decision.LoadOutputSafety(h.nc, target.NodeID)
			if err != nil {
				return fmt.Errorf("invalid_safety_config:%s", target.NodeID)
			}
			if present && safety.RampPerSecond != nil && *safety.RampPerSecond > 0 {
				return fmt.Errorf("unsupported_rule_ramp:%s", target.NodeID)
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
		Validate: func(id string, doc decision.Document) error { _, err := h.ruleRuntimeAuthority(id, doc); return err },
		Submit:   h.submitRuleCommand,
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
	if !valid || input.ExecutionID == "" || input.RequestID != fmt.Sprintf("%s/%d", input.ExecutionID, input.Step) {
		return command.Outcome{}, fmt.Errorf("invalid rule action identity")
	}
	points, err := client.GetNodes(h.nc, "all", input.Target.NodeID, "", false)
	if err != nil || len(points) == 0 {
		return command.Outcome{}, fmt.Errorf("target unavailable")
	}
	point := points[0]
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
