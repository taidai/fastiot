package decision

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// RuleNotification is a literal in-app notice. Account references are checked by
// the application on authoring and execution; this type cannot dispatch anything.
type RuleNotification struct {
	RecipientIDs []string `json:"recipient_ids"`
	Title        string   `json:"title"`
	Body         string   `json:"body,omitempty"`
}

func (n RuleNotification) Validate() error {
	if strings.TrimSpace(n.Title) == "" || len(n.Title) > 256 || len(n.Body) > 8192 || len(n.RecipientIDs) < 1 || len(n.RecipientIDs) > 100 {
		return fmt.Errorf("invalid_rule_notification")
	}
	for _, id := range n.RecipientIDs {
		if strings.TrimSpace(id) == "" || len(id) > 256 {
			return fmt.Errorf("invalid_notification_recipient")
		}
	}
	return nil
}

func decodeRuleAction(raw []byte) (Action, error) {
	type plain Action
	var action plain
	wire := struct {
		*plain
		Kind         string            `json:"kind"`
		Notification *RuleNotification `json:"notification"`
	}{plain: &action}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return Action{}, err
	}
	var presence struct{ Kind, Value, Readback, Notification json.RawMessage }
	if err := json.Unmarshal(raw, &presence); err != nil {
		return Action{}, err
	}
	if err := ruleScalar(presence.Kind, false); err != nil {
		return Action{}, err
	}
	switch wire.Kind {
	case "notify":
		if wire.Notification == nil || len(presence.Value) > 0 || len(presence.Readback) > 0 {
			return Action{}, fmt.Errorf("notification has no numeric write or device readback")
		}
		action.Notification = wire.Notification
	case "", "command":
		if len(presence.Notification) > 0 {
			return Action{}, fmt.Errorf("notification payload requires notify kind")
		}
	default:
		return Action{}, fmt.Errorf("unsupported rule action kind")
	}
	return Action(action), nil
}

func marshalRuleAction(action Action) ([]byte, error) {
	if action.Notification == nil {
		return json.Marshal(action)
	}
	if action.Value != 0 || action.Readback != nil {
		return nil, fmt.Errorf("notification has no numeric write or device readback")
	}
	return json.Marshal(struct {
		ActionID     string            `json:"action_id"`
		Kind         string            `json:"kind"`
		DelayMS      int               `json:"delay_ms,omitempty"`
		Notification *RuleNotification `json:"notification"`
	}{action.ActionID, "notify", action.DelayMS, action.Notification})
}

func decodeRuleActions(raw []json.RawMessage) ([]Action, error) {
	if raw == nil {
		return nil, nil
	}
	actions := make([]Action, 0, len(raw))
	for _, item := range raw {
		action, err := decodeRuleAction(item)
		if err != nil {
			return nil, err
		}
		actions = append(actions, action)
	}
	return actions, nil
}
func marshalRuleActions(actions []Action) ([]json.RawMessage, error) {
	if actions == nil {
		return nil, nil
	}
	raw := make([]json.RawMessage, 0, len(actions))
	for _, action := range actions {
		encoded, err := marshalRuleAction(action)
		if err != nil {
			return nil, err
		}
		raw = append(raw, encoded)
	}
	return raw, nil
}
func (action SimulationAction) MarshalJSON() ([]byte, error) {
	type plain SimulationAction
	if action.Notification == nil {
		return json.Marshal(plain(action))
	}
	return marshalRuleAction(Action{ActionID: action.ActionID, DelayMS: action.DelayMS, Notification: action.Notification})
}

func (s *Scenario) UnmarshalJSON(raw []byte) error {
	type plain Scenario
	var decoded plain
	wire := struct {
		*plain
		Actions []json.RawMessage `json:"expect_actions"`
	}{plain: &decoded}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	actions, err := decodeRuleActions(wire.Actions)
	if err != nil {
		return err
	}
	decoded.ExpectActions = actions
	*s = Scenario(decoded)
	return nil
}
func (s Scenario) MarshalJSON() ([]byte, error) {
	type plain Scenario
	if !hasRuleNotification(s.ExpectActions) {
		return json.Marshal(plain(s))
	}
	actions, err := marshalRuleActions(s.ExpectActions)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		*plain
		Actions []json.RawMessage `json:"expect_actions,omitempty"`
	}{(*plain)(&s), actions})
}

func (action CheckAction) MarshalJSON() ([]byte, error) {
	type plain CheckAction
	if action.Kind != "notify" {
		return json.Marshal(plain(action))
	}
	return json.Marshal(struct {
		Kind           string `json:"kind"`
		Output         string `json:"output"`
		Status         string `json:"status"`
		NotificationID string `json:"notification_id,omitempty"`
		Confirmation   string `json:"confirmation,omitempty"`
		Reason         string `json:"reason,omitempty"`
	}{action.Kind, action.Output, action.Status, action.NotificationID, action.Confirmation, action.Reason})
}

// ClearNotificationRecipients preserves engineering text while forcing explicit
// account rebinding when a rule is copied into another project/instance.
func (d *Document) ClearNotificationRecipients() []string {
	var unresolved []string
	clearActions := func(actions []Action) {
		for _, action := range actions {
			if action.Notification != nil {
				unresolved = append(unresolved, action.Notification.RecipientIDs...)
				action.Notification.RecipientIDs = []string{}
			}
		}
	}
	for _, row := range d.Table.Rows {
		clearActions(row.Actions)
	}
	for _, scenario := range d.Scenarios {
		clearActions(scenario.ExpectActions)
	}
	return unresolved
}

func hasRuleNotification(actions []Action) bool {
	for _, action := range actions {
		if action.Notification != nil {
			return true
		}
	}
	return false
}
