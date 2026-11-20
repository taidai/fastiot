package safety

import (
	"fmt"
	"math"
	"time"

	"fastiot/internal/ems/observation"
	"fastiot/internal/ems/signal"
)

type Request struct {
	Output    string
	Requested signal.Value
	Current   *float64
	Spec      *Output
	Manual    bool
	Automatic bool
	Disabled  bool
	ReadOnly  bool
	Samples   map[string]signal.Sample
	MaxAge    time.Duration
}

// Decision is the outcome of a safety evaluation. It is a named type so the
// three decisions live in one place and a misspelled comparison fails to
// compile instead of silently permitting a write.
type Decision string

const (
	DecisionAllowed Decision = "allowed"
	DecisionClamped Decision = "clamped"
	DecisionRefused Decision = "refused"
)

type Verdict struct {
	Decision Decision
	Value    float64
	Reason   string
}

// Effective returns the verdict a caller must obey. Only the three declared
// decisions are readable as permission; an empty or unknown decision becomes a
// refusal with a diagnostic reason, so a fabricated or corrupted verdict fails
// closed instead of being read as allowed.
func (v Verdict) Effective() Verdict {
	switch v.Decision {
	case DecisionAllowed, DecisionClamped, DecisionRefused:
		return v
	default:
		return Verdict{
			Decision: DecisionRefused, Value: v.Value,
			Reason: fmt.Sprintf("unknown safety decision %q", string(v.Decision)),
		}
	}
}

func Evaluate(request Request, now time.Time) Verdict {
	value := request.Requested.Num
	if request.Disabled {
		return Verdict{Decision: DecisionRefused, Value: value, Reason: "target_disabled"}
	}
	if request.ReadOnly {
		return Verdict{Decision: DecisionRefused, Value: value, Reason: "read_only"}
	}
	if request.Manual && request.Automatic {
		return Verdict{Decision: DecisionRefused, Value: value, Reason: "manual_takeover"}
	}
	if request.Spec == nil {
		return Verdict{Decision: DecisionAllowed, Value: value}
	}
	spec := request.Spec
	if spec.Kind != request.Requested.Kind {
		return Verdict{Decision: DecisionRefused, Value: value, Reason: fmt.Sprintf("output %s expects %s but got %s", request.Output, spec.Kind, request.Requested.Kind)}
	}
	maxAge := request.MaxAge
	if maxAge <= 0 {
		maxAge = observation.DefaultMaxAge
	}
	for _, input := range spec.Requires {
		sample, found := request.Samples[input]
		if !found {
			return Verdict{Decision: DecisionRefused, Value: value, Reason: "required input " + input + " is missing"}
		}
		if usable, why := sample.Usable(now, maxAge); !usable {
			return Verdict{Decision: DecisionRefused, Value: value, Reason: "required input " + input + " is not usable: " + why}
		}
	}
	for _, condition := range spec.Conditions {
		sample, found := request.Samples[condition.Input]
		if !found {
			return Verdict{Decision: DecisionRefused, Value: value, Reason: "safety input " + condition.Input + " is missing"}
		}
		if usable, why := sample.Usable(now, maxAge); !usable {
			return Verdict{Decision: DecisionRefused, Value: value, Reason: "safety input " + condition.Input + " is not usable: " + why}
		}
		coerced, err := signal.Coerce(sample.Value, condition.Value.Kind)
		if err != nil {
			return Verdict{Decision: DecisionRefused, Value: value, Reason: "safety input " + condition.Input + ": " + err.Error()}
		}
		matched, err := coerced.Compare(condition.Op, condition.Value)
		if err != nil {
			return Verdict{Decision: DecisionRefused, Value: value, Reason: "safety input " + condition.Input + ": " + err.Error()}
		}
		if !matched {
			return Verdict{Decision: DecisionRefused, Value: value, Reason: fmt.Sprintf("safety condition %s = %s does not satisfy %s %s", condition.Input, sample.Value, condition.Op, condition.Value)}
		}
	}
	// Envelopes only clamp numbers; booleans and labels pass through unchanged.
	if request.Requested.Kind != signal.Number {
		return Verdict{Decision: DecisionAllowed, Value: value}
	}
	decision, reason := DecisionAllowed, ""
	if spec.HasRange {
		if value < spec.Min {
			value, reason = spec.Min, fmt.Sprintf("clamped to minimum %g", spec.Min)
		} else if value > spec.Max {
			value, reason = spec.Max, fmt.Sprintf("clamped to maximum %g", spec.Max)
		}
	}
	capability, label := spec.CapPositive, "discharge"
	if value < 0 {
		capability, label = spec.CapNegative, "charge"
	}
	if capability > 0 && math.Abs(value) > capability {
		value = math.Copysign(capability, value)
		reason = fmt.Sprintf("clamped to available %s power %g", label, capability)
	}
	if reason != "" {
		decision = DecisionClamped
	}
	return Verdict{Decision: decision, Value: value, Reason: reason}
}
