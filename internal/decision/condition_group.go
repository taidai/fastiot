package decision

import (
	"fmt"
	"time"

	"fastiot/internal/ems/rule"
	"fastiot/internal/ems/signal"
)

func conditionGroupInput(rowID string) string { return "__decision_condition_group__" + rowID }
func timeWindowInput(rowID string) string     { return "__decision_time_window__" + rowID }

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
	if (row.Match == "any" || row.hasJoins()) && len(row.Conditions) > 0 {
		points, windows := false, false
		for _, condition := range row.Conditions {
			if condition.Kind == "time" {
				windows = true
			} else {
				points = true
			}
		}
		conditions := []rule.Condition{}
		if points {
			conditions = append(conditions, rule.Condition{Input: conditionGroupInput(row.ID), Op: "eq", Value: signal.Flag(true)})
		}
		if windows {
			conditions = append(conditions, rule.Condition{Input: timeWindowInput(row.ID), Op: "eq", Value: signal.Flag(true)})
		}
		return conditions
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

func conditionGroupValues(row Row, samples map[string]signal.Sample, now time.Time) map[string]bool {
	groups := map[string]bool{}
	for index, condition := range row.Conditions {
		input, join := conditionGroupInput(row.ID), row.joinAt(index)
		if condition.Kind == "time" {
			input = timeWindowInput(row.ID)
			join = "AND"
			if row.Match == "any" {
				join = "OR"
			}
		}
		matched, _ := conditionMatches(condition, samples, now)
		previous, found := groups[input]
		if !found {
			groups[input] = matched
		} else if join == "OR" {
			groups[input] = previous || matched
		} else {
			groups[input] = previous && matched
		}
	}
	return groups
}

// conditionGroupSamples reduces each non-nested point group to one synthetic boolean. The
// existing rule engine then owns hold/cooldown for the original row ID, rather
// than splitting an OR row into branches with independent timers.
func conditionGroupSamples(table Table, now time.Time, samples map[string]signal.Sample) map[string]signal.Sample {
	result := samples
	for _, row := range table.Rows {
		if (row.Match != "any" && !row.hasJoins()) || len(row.Conditions) == 0 {
			continue
		}
		if len(result) == len(samples) {
			result = make(map[string]signal.Sample, len(samples)+1)
			for id, sample := range samples {
				result[id] = sample
			}
		}
		groups := conditionGroupValues(row, samples, now)
		// The point OR group and the time-window clause are separate constraints.
		// Multiple OR windows remain a union, but cannot replace point conditions.
		for input, matched := range groups {
			result[input] = signal.Sample{Input: input, Value: signal.Flag(matched), Quality: signal.Good, SourceTime: now, ReceivedTime: now}
		}
	}
	return result
}
