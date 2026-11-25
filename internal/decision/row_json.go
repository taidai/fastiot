package decision

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Per-condition joins are a rule-row wire feature, not a shared guard feature.
func (r *Row) UnmarshalJSON(encoded []byte) error {
	type plain Row
	var decoded plain
	wire := struct {
		*plain
		Conditions []json.RawMessage `json:"conditions"`
		Actions    []json.RawMessage `json:"actions"`
	}{plain: &decoded}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	if wire.Conditions != nil {
		decoded.Conditions = make([]Condition, 0, len(wire.Conditions))
	}
	for _, raw := range wire.Conditions {
		var condition Condition
		entry := struct {
			*Condition
			Join json.RawMessage `json:"join"`
		}{Condition: &condition}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&entry); err != nil {
			return err
		}
		join := ""
		if len(entry.Join) > 0 {
			if bytes.Equal(bytes.TrimSpace(entry.Join), []byte("null")) {
				return fmt.Errorf("condition join must not be null")
			}
			if err := json.Unmarshal(entry.Join, &join); err != nil {
				return err
			}
			join = strings.ToUpper(join)
		}
		decoded.Conditions = append(decoded.Conditions, condition)
		decoded.Joins = append(decoded.Joins, join)
	}
	actions, err := decodeRuleActions(wire.Actions)
	if err != nil {
		return err
	}
	decoded.Actions = actions
	*r = Row(decoded)
	return nil
}

func (r Row) MarshalJSON() ([]byte, error) {
	type plain Row
	if !r.hasJoins() && !hasRuleNotification(r.Actions) {
		return json.Marshal(plain(r))
	}
	type joined struct {
		Condition
		Join string `json:"join,omitempty"`
	}
	var conditions []joined
	if r.Conditions != nil {
		conditions = make([]joined, len(r.Conditions))
	}
	for i, condition := range r.Conditions {
		conditions[i].Condition = condition
		if i < len(r.Joins) {
			conditions[i].Join = r.Joins[i]
		}
	}
	actions, err := marshalRuleActions(r.Actions)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		*plain
		Conditions []joined          `json:"conditions"`
		Actions    []json.RawMessage `json:"actions"`
	}{(*plain)(&r), conditions, actions})
}

func (r Row) hasJoins() bool {
	for _, join := range r.Joins {
		if join != "" {
			return true
		}
	}
	return false
}
func (r Row) joinAt(index int) string {
	if index < len(r.Joins) && r.Joins[index] != "" {
		return r.Joins[index]
	}
	if r.Match == "any" {
		return "OR"
	}
	return "AND"
}
