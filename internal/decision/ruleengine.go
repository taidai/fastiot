package decision

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/ems/alloc"
	"fastiot/internal/ems/rule"
	"fastiot/internal/ems/safety"
	"fastiot/internal/ems/signal"
)

// RuleTable converts the product document into the EMS rule engine table.
// The wire format stays compatible with the existing editor: numeric
// conditions and actions, plus optional hold and cooldown milliseconds.
func (d Document) RuleTable() (rule.Table, error) {
	if err := d.Validate(); err != nil {
		return rule.Table{}, err
	}
	rows := make([]rule.Row, 0, len(d.Table.Rows))
	for _, row := range d.Table.Rows {
		conditions := ruleConditions(row)
		actions := make([]rule.Action, 0, len(row.Actions))
		if !row.sequenced() {
			for _, action := range row.Actions {
				actions = append(actions, rule.Action{Output: action.ActionID, Value: signal.Num(action.Value)})
			}
		}
		rows = append(rows, rule.Row{
			ID: row.ID, Conditions: conditions, Actions: actions,
			Hold:     time.Duration(row.HoldMS) * time.Millisecond,
			Cooldown: time.Duration(row.CooldownMS) * time.Millisecond,
		})
		// Later authored rows are unreachable after an unconditional first match.
		// Keep them in the document/traces, not in the runtime's default-last table.
		if len(row.Conditions) == 0 {
			break
		}
	}
	return rule.Table{ID: "decision", Version: 1, Rows: rows}, nil
}

// SamplesFromValues builds a snapshot for simulation. Simulated inputs are
// good and current; they never touch a device.
func SamplesFromValues(values map[string]float64, texts map[string]string, now time.Time) map[string]signal.Sample {
	samples := make(map[string]signal.Sample, len(values)+len(texts))
	for input, value := range values {
		samples[input] = signal.Sample{
			Input: input, Value: signal.Num(value), Quality: signal.Good,
			SourceTime: now, ReceivedTime: now,
		}
	}
	for input, text := range texts {
		if text == "" {
			continue
		}
		samples[input] = signal.Sample{
			Input: input, Value: signal.Label(text), Quality: signal.Good,
			SourceTime: now, ReceivedTime: now,
		}
	}
	return samples
}

// pointSignal preserves string/enum points as labels and everything else as
// numbers. This keeps typed conditions usable without a second point store.
func pointSignal(point data.Point) (signal.Value, error) {
	if point.DataType == data.PointDataTypeString {
		text := point.Txt()
		if text == "" {
			return signal.Value{}, fmt.Errorf("empty text value")
		}
		return signal.Label(text), nil
	}
	value, err := point.ValueFloat()
	if err != nil {
		return signal.Value{}, err
	}
	return signal.Num(value), nil
}

// commandSafety is the shared verdict used by automatic actions before any
// protocol or SIOT write. Manual takeover blocks automatic output only.
func commandSafety(now time.Time, spec *safety.Output, value float64, samples map[string]signal.Sample) safety.Verdict {
	return safety.Evaluate(safety.Request{Requested: signal.Num(value), Spec: spec, Samples: samples, Automatic: true}, now)
}

// targetLimiter enforces product safety and manual takeover before an action
// leaves the engine.
type targetLimiter struct {
	nc       *nats.Conn
	parent   string
	actions  map[string]Target
	manual   func(actionID string) bool
	safety   map[string]safety.Output
	values   *map[string]signal.Sample
	validate func(Target, bool, *float64) error
}

func (l targetLimiter) Limit(now time.Time, output string, requested signal.Value) rule.LimitResult {
	target, ok := l.actions[output]
	if !ok {
		return rule.LimitResult{Blocked: true, Reason: "action is not bound"}
	}
	if l.manual != nil && l.manual(output) {
		return rule.LimitResult{Blocked: true, Reason: "manual takeover"}
	}
	if requested.Kind != signal.Number {
		return rule.LimitResult{Blocked: true, Reason: "only numeric setpoints are supported"}
	}
	value := requested.Num
	validate := l.validate
	if validate == nil {
		validate = func(t Target, output bool, v *float64) error { return ValidateTarget(l.nc, l.parent, t, output, v) }
	}
	if err := validate(target, true, &value); err != nil {
		return rule.LimitResult{Blocked: true, Reason: err.Error()}
	}
	if spec, ok := l.safety[output]; ok {
		samples := map[string]signal.Sample{}
		if l.values != nil {
			samples = *l.values
		}
		verdict := commandSafety(now, &spec, requested.Num, samples).Effective()
		if verdict.Decision == safety.DecisionRefused {
			return rule.LimitResult{Blocked: true, Reason: verdict.Reason}
		}
		if verdict.Decision == safety.DecisionClamped {
			return rule.LimitResult{Value: signal.Num(verdict.Value), Reason: verdict.Reason}
		}
		return rule.LimitResult{Value: requested}
	}
	return rule.LimitResult{Value: requested}
}

// CheckInput and CheckAction describe one evaluation for the lastCheck point.
type CheckInput struct {
	Target       Target    `json:"target,omitempty"`
	ReceivedTime time.Time `json:"received_time"`
	Input        string    `json:"input"`
	Value        float64   `json:"value"`
	Text         string    `json:"text,omitempty"`
	Kind         string    `json:"kind,omitempty"`
	Quality      string    `json:"quality"`
	SourceTime   time.Time `json:"source_time"`
}

type CheckAction struct {
	CommandID    string        `json:"command_id,omitempty"`
	Confirmation string        `json:"confirmation,omitempty"`
	Target       Target        `json:"target,omitempty"`
	Output       string        `json:"output"`
	Requested    float64       `json:"requested"`
	Applied      float64       `json:"applied"`
	Blocked      bool          `json:"blocked"`
	Reason       string        `json:"reason"`
	Status       string        `json:"status,omitempty"`
	Shares       []alloc.Share `json:"shares,omitempty"`
}

type CheckReport struct {
	RuleID         string        `json:"rule_id,omitempty"`
	SourceRevision string        `json:"source_revision,omitempty"`
	ExecutionID    string        `json:"execution_id,omitempty"`
	At             time.Time     `json:"at"`
	Row            string        `json:"row"`
	State          string        `json:"state"`
	Inputs         []CheckInput  `json:"inputs"`
	Actions        []CheckAction `json:"actions"`
}

func buildCheck(outcome rule.Outcome, now time.Time) CheckReport {
	report := CheckReport{At: now, Row: outcome.MatchedRow, State: string(outcome.State)}
	for _, sample := range outcome.Snapshot {
		value := 0.0
		text := ""
		switch sample.Value.Kind {
		case signal.Number:
			value = sample.Value.Num
		case signal.Bool:
			if sample.Value.Bool {
				value = 1
			}
		case signal.Enum:
			text = sample.Value.Enum
		}
		report.Inputs = append(report.Inputs, CheckInput{
			Input: sample.Input, Value: value, Text: text, Kind: sample.Value.Kind.String(),
			Quality: sample.Quality.String(), SourceTime: sample.SourceTime, ReceivedTime: sample.ReceivedTime,
		})
	}
	for _, action := range outcome.Actions {
		requested, applied := 0.0, 0.0
		if action.Requested.Kind == signal.Number {
			requested = action.Requested.Num
		}
		if action.Applied.Kind == signal.Number {
			applied = action.Applied.Num
		}
		report.Actions = append(report.Actions, CheckAction{
			Output: action.Output, Requested: requested, Applied: applied,
			Blocked: action.Blocked, Reason: action.Reason,
		})
	}
	return report
}

func marshalCheck(report CheckReport) (string, error) {
	encoded, err := json.Marshal(report)
	if err != nil {
		return "", fmt.Errorf("encode check: %w", err)
	}
	return string(encoded), nil
}

// SimulationAction is the API shape the editor already consumes.
type SimulationAction struct {
	ActionID string  `json:"action_id"`
	Value    float64 `json:"value"`
	DelayMS  int     `json:"delay_ms,omitempty"`
	Blocked  bool    `json:"blocked,omitempty"`
	Reason   string  `json:"reason,omitempty"`
}

// TimeCheck reports one time condition's result at the simulated moment.
type TimeCheck struct {
	Start     string `json:"start,omitempty"`
	End       string `json:"end,omitempty"`
	Weekdays  int    `json:"weekday_mask,omitempty"`
	Satisfied bool   `json:"satisfied"`
}

// SimulationOptions supplies hypothetical elapsed time. It is not runtime
// state: the live engine measures hold from its own consecutive evaluations.
type SimulationOptions struct {
	HeldMS int `json:"held_ms"`
}

type SimulationRow struct {
	RowID      string   `json:"row_id"`
	State      string   `json:"state"`
	Reasons    []string `json:"reasons,omitempty"`
	HeldMS     int      `json:"held_ms"`
	RequiredMS int      `json:"required_ms"`
}

// SimulationResult reports the matching row and what would be requested. It
// never creates a command.
type SimulationResult struct {
	RowID   string             `json:"row_id,omitempty"`
	Actions []SimulationAction `json:"actions,omitempty"`
	State   string             `json:"state"`
	Blocked []string           `json:"blocked,omitempty"`
	Times   []TimeCheck        `json:"time_checks,omitempty"`
	Rows    []SimulationRow    `json:"rows,omitempty"`
}

// Simulate evaluates rows in first-hit order at now. Hold uses the explicitly
// supplied hypothetical elapsed time; no command or runtime timer is created.
func (d Document) Simulate(inputs map[string]float64, texts map[string]string, now time.Time) (SimulationResult, error) {
	table, err := d.RuleTable()
	if err != nil {
		return SimulationResult{}, err
	}
	samples := SamplesFromValues(inputs, texts, now)
	for id, sample := range TimeSamples(d.Table, now) {
		samples[id] = sample
	}
	samples = conditionGroupSamples(d.Table, now, samples)
	result := SimulationResult{State: string(rule.StateNoMatch)}
	seenTimes := make(map[string]bool)
	for _, row := range d.Table.Rows {
		for _, condition := range row.Conditions {
			if condition.Kind != "time" || seenTimes[timeConditionID(condition)] {
				continue
			}
			seenTimes[timeConditionID(condition)] = true
			result.Times = append(result.Times, TimeCheck{
				Start: condition.Start, End: condition.End, Weekdays: condition.Weekdays,
				Satisfied: timeMatches(condition, now),
			})
		}
	}
	heldMS := 0
	if d.Simulation != nil {
		heldMS = d.Simulation.HeldMS
	}
	for i, row := range d.Table.Rows {
		outcome, simulateErr := rule.Simulate(rule.Table{ID: "decision", Version: 1, Rows: []rule.Row{table.Rows[i]}}, now, samples, nil)
		if simulateErr != nil {
			return SimulationResult{}, simulateErr
		}
		trace := SimulationRow{RowID: row.ID, State: string(outcome.State), HeldMS: heldMS, RequiredMS: row.HoldMS}
		if outcome.State == rule.StateNoMatch {
			trace.State = "no-match"
			if row.Match == "any" {
				for _, condition := range row.Conditions {
					group := conditionGroupInput(row.ID)
					if condition.Kind == "time" {
						group = timeWindowInput(row.ID)
					}
					if samples[group].Value.Bool {
						continue
					}
					if _, reason := conditionMatches(condition, samples, now); reason != "" {
						trace.Reasons = append(trace.Reasons, reason)
					}
				}
			} else {
				for _, blocked := range outcome.Blocked {
					trace.Reasons = append(trace.Reasons, blocked.Reason)
				}
			}
			result.Blocked = append(result.Blocked, row.ID+": "+joinReasons(trace.Reasons))
			result.Rows = append(result.Rows, trace)
			continue
		}
		result.RowID = row.ID
		if row.HoldMS > heldMS {
			result.State = string(rule.StateWaitingHold)
			trace.State = result.State
			trace.Reasons = []string{fmt.Sprintf("held %dms is less than required %dms", heldMS, row.HoldMS)}
			result.Rows = append(result.Rows, trace)
			return explainUnvisitedRows(result, d.Table.Rows), nil
		}
		result.State = string(rule.StateFired)
		if row.sequenced() {
			for _, action := range row.Actions {
				result.Actions = append(result.Actions, SimulationAction{ActionID: action.ActionID, Value: action.Value, DelayMS: action.DelayMS})
			}
		} else {
			for _, action := range outcome.Actions {
				value := 0.0
				if action.Applied.Kind == signal.Number {
					value = action.Applied.Num
				}
				result.Actions = append(result.Actions, SimulationAction{
					ActionID: action.Output, Value: value,
					Blocked: action.Blocked, Reason: action.Reason,
				})
			}
		}
		result.Rows = append(result.Rows, trace)
		return explainUnvisitedRows(result, d.Table.Rows), nil
	}
	return result, nil
}

func explainUnvisitedRows(result SimulationResult, rows []Row) SimulationResult {
	for _, row := range rows[len(result.Rows):] {
		result.Rows = append(result.Rows, SimulationRow{RowID: row.ID, State: "skipped", RequiredMS: row.HoldMS, Reasons: []string{"earlier row " + result.RowID + " matched; ordered first-match stops evaluation"}})
	}
	return result
}

func joinReasons(reasons []string) string {
	if len(reasons) == 0 {
		return "condition group does not match"
	}
	joined := reasons[0]
	for _, reason := range reasons[1:] {
		joined += "; " + reason
	}
	return joined
}
