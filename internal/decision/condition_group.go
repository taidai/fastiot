package decision

import (
	"fmt"
	"time"

	"fastiot/internal/ems/rule"
	"fastiot/internal/ems/signal"
)

func conditionGroupInput(rowID string) string { return "__decision_condition_group__" + rowID }

func conditionValue(condition Condition) signal.Value {
	switch condition.Kind {
	case "bool":
		return signal.Flag(condition.Value != 0)
	case "enum":
		return signal.Label(condition.Text)
	default:
		return signal.Num(condition.Value)
	}
}

func ruleConditions(row Row) []rule.Condition {
	if row.Match == "any" && len(row.Conditions) > 0 {
		return []rule.Condition{{Input: conditionGroupInput(row.ID), Op: "eq", Value: signal.Flag(true)}}
	}
	conditions := make([]rule.Condition, 0, len(row.Conditions))
	for _, condition := range row.Conditions {
		if condition.Kind == "time" {
			conditions = append(conditions, rule.Condition{Input: timeConditionID(condition), Op: "eq", Value: signal.Flag(true)})
			continue
		}
		conditions = append(conditions, rule.Condition{Input: condition.PointID, Op: condition.Operator, Value: conditionValue(condition)})
	}
	return conditions
}

func conditionMatches(condition Condition, samples map[string]signal.Sample, now time.Time) (bool, string) {
	if condition.Kind == "time" {
		if timeMatches(condition, now) {
			return true, ""
		}
		return false, "time window does not match"
	}
	sample, ok := samples[condition.PointID]
	if !ok {
		return false, fmt.Sprintf("input %s is missing or invalid", condition.PointID)
	}
	usable, reason := sample.Usable(now, 0)
	if !usable {
		return false, fmt.Sprintf("input %s is not usable: %s", condition.PointID, reason)
	}
	value, err := signal.Coerce(sample.Value, conditionValue(condition).Kind)
	if err != nil {
		return false, fmt.Sprintf("input %s: %v", condition.PointID, err)
	}
	matched, err := value.Compare(condition.Operator, conditionValue(condition))
	if err != nil {
		return false, fmt.Sprintf("input %s: %v", condition.PointID, err)
	}
	if !matched {
		return false, fmt.Sprintf("input %s does not satisfy %s", condition.PointID, condition.Operator)
	}
	return true, ""
}

// conditionGroupSamples reduces each OR group to one synthetic boolean. The
// existing rule engine then owns hold/cooldown for the original row ID, rather
// than splitting an OR row into branches with independent timers.
func conditionGroupSamples(table Table, now time.Time, samples map[string]signal.Sample) map[string]signal.Sample {
	result := samples
	for _, row := range table.Rows {
		if row.Match != "any" || len(row.Conditions) == 0 {
			continue
		}
		if len(result) == len(samples) {
			result = make(map[string]signal.Sample, len(samples)+1)
			for id, sample := range samples {
				result[id] = sample
			}
		}
		matched := false
		for _, condition := range row.Conditions {
			ok, _ := conditionMatches(condition, samples, now)
			matched = matched || ok
		}
		result[conditionGroupInput(row.ID)] = signal.Sample{
			Input: conditionGroupInput(row.ID), Value: signal.Flag(matched), Quality: signal.Good,
			SourceTime: now, ReceivedTime: now,
		}
	}
	return result
}
