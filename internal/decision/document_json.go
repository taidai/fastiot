package decision

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Only the rule document wire contract is strict here. Action/Condition are
// shared with state machines; their separate wire contract is not changed.
func (d *Document) UnmarshalJSON(encoded []byte) error {
	type plain Document
	var decoded plain
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	type rawReadback struct {
		Timeout   json.RawMessage `json:"timeout_ms"`
		Tolerance json.RawMessage `json:"tolerance"`
	}
	type rawAction struct {
		Value    json.RawMessage `json:"value"`
		Delay    json.RawMessage `json:"delay_ms"`
		Readback *rawReadback    `json:"readback"`
	}
	type rawCondition struct {
		Kind     string          `json:"kind"`
		Value    json.RawMessage `json:"value"`
		Weekdays json.RawMessage `json:"weekday_mask"`
		Start    json.RawMessage `json:"start"`
		End      json.RawMessage `json:"end"`
	}
	type rawRow struct {
		Hold       json.RawMessage `json:"hold_ms"`
		Cooldown   json.RawMessage `json:"cooldown_ms"`
		Match      json.RawMessage `json:"match"`
		Conditions []rawCondition  `json:"conditions"`
		Actions    []rawAction     `json:"actions"`
	}
	var raw struct {
		Table struct {
			Rows []rawRow `json:"rows"`
		} `json:"table"`
		Scenarios []struct {
			Inputs  map[string]json.RawMessage `json:"inputs"`
			Actions []rawAction                `json:"expect_actions"`
		} `json:"scenarios"`
		Simulation *struct {
			Held json.RawMessage `json:"held_ms"`
		} `json:"simulation"`
	}
	if bytes.Equal(bytes.TrimSpace(encoded), []byte("null")) {
		return fmt.Errorf("rule_document_required")
	}
	if err := json.Unmarshal(encoded, &raw); err != nil {
		return err
	}
	checkAction := func(action rawAction) error {
		if err := ruleScalar(action.Value, true); err != nil {
			return fmt.Errorf("action value: %w", err)
		}
		if err := ruleScalar(action.Delay, false); err != nil {
			return fmt.Errorf("action delay: %w", err)
		}
		if action.Readback != nil {
			if err := ruleScalar(action.Readback.Timeout, true); err != nil {
				return fmt.Errorf("readback timeout: %w", err)
			}
			if err := ruleScalar(action.Readback.Tolerance, false); err != nil {
				return fmt.Errorf("readback tolerance: %w", err)
			}
		}
		return nil
	}
	for _, row := range raw.Table.Rows {
		for _, value := range []json.RawMessage{row.Hold, row.Cooldown, row.Match} {
			if err := ruleScalar(value, false); err != nil {
				return err
			}
		}
		for _, condition := range row.Conditions {
			required := condition.Kind == "" || condition.Kind == "number" || condition.Kind == "bool"
			if err := ruleScalar(condition.Value, required); err != nil {
				return fmt.Errorf("condition value: %w", err)
			}
			for _, value := range []json.RawMessage{condition.Weekdays, condition.Start, condition.End} {
				if err := ruleScalar(value, false); err != nil {
					return err
				}
			}
		}
		for _, action := range row.Actions {
			if err := checkAction(action); err != nil {
				return err
			}
		}
	}
	for _, scenario := range raw.Scenarios {
		for _, value := range scenario.Inputs {
			if err := ruleScalar(value, true); err != nil {
				return fmt.Errorf("scenario input: %w", err)
			}
		}
		for _, action := range scenario.Actions {
			if err := checkAction(action); err != nil {
				return err
			}
		}
	}
	if raw.Simulation != nil {
		if err := ruleScalar(raw.Simulation.Held, false); err != nil {
			return err
		}
	}
	*d = Document(decoded)
	return nil
}

func ruleScalar(raw json.RawMessage, required bool) error {
	if len(raw) == 0 {
		if required {
			return fmt.Errorf("value_required")
		}
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("null_value_not_allowed")
	}
	return nil
}

// SimulationInputs retains explicit zero but never converts null to a measured
// or hypothetical zero. Omission still means no supplied inputs.
type SimulationInputs map[string]float64

func (s *SimulationInputs) UnmarshalJSON(encoded []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &raw); err != nil {
		return err
	}
	if raw == nil {
		return fmt.Errorf("simulation_inputs_required")
	}
	for _, value := range raw {
		if err := ruleScalar(value, true); err != nil {
			return err
		}
	}
	type plain map[string]float64
	var decoded plain
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return err
	}
	for _, value := range decoded {
		if !finite(value) {
			return fmt.Errorf("invalid_simulation_value")
		}
	}
	*s = SimulationInputs(decoded)
	return nil
}
