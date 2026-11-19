package safety

import (
	"fmt"
	"math"
	"time"

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

type Verdict struct {
	Decision string
	Value    float64
	Reason   string
}

func Evaluate(request Request, now time.Time) Verdict {
	value := request.Requested.Num
	if request.Disabled {
		return Verdict{Decision: "refused", Value: value, Reason: "target_disabled"}
	}
	if request.ReadOnly {
		return Verdict{Decision: "refused", Value: value, Reason: "read_only"}
	}
	if request.Manual && request.Automatic {
		return Verdict{Decision: "refused", Value: value, Reason: "manual_takeover"}
	}
	if request.Spec == nil {
		return Verdict{Decision: "allowed", Value: value}
	}
	spec := request.Spec
	if spec.Kind != request.Requested.Kind {
		return Verdict{Decision: "refused", Value: value, Reason: fmt.Sprintf("output %s expects %s but got %s", request.Output, spec.Kind, request.Requested.Kind)}
	}
	maxAge := request.MaxAge
	if maxAge <= 0 {
		maxAge = 15 * time.Second
	}
	for _, input := range spec.Requires {
		sample, found := request.Samples[input]
		if !found {
			return Verdict{Decision: "refused", Value: value, Reason: "required input " + input + " is missing"}
		}
		if usable, why := sample.Usable(now, maxAge); !usable {
			return Verdict{Decision: "refused", Value: value, Reason: "required input " + input + " is not usable: " + why}
		}
	}
	for _, condition := range spec.Conditions {
		sample, found := request.Samples[condition.Input]
		if !found {
			return Verdict{Decision: "refused", Value: value, Reason: "safety input " + condition.Input + " is missing"}
		}
		if usable, why := sample.Usable(now, maxAge); !usable {
			return Verdict{Decision: "refused", Value: value, Reason: "safety input " + condition.Input + " is not usable: " + why}
		}
		coerced, err := signal.Coerce(sample.Value, condition.Value.Kind)
		if err != nil {
			return Verdict{Decision: "refused", Value: value, Reason: "safety input " + condition.Input + ": " + err.Error()}
		}
		matched, err := coerced.Compare(condition.Op, condition.Value)
		if err != nil {
			return Verdict{Decision: "refused", Value: value, Reason: "safety input " + condition.Input + ": " + err.Error()}
		}
		if !matched {
			return Verdict{Decision: "refused", Value: value, Reason: fmt.Sprintf("safety condition %s = %s does not satisfy %s %s", condition.Input, sample.Value, condition.Op, condition.Value)}
		}
	}
	// Envelopes only clamp numbers; booleans and labels pass through unchanged.
	if request.Requested.Kind != signal.Number {
		return Verdict{Decision: "allowed", Value: value}
	}
	decision, reason := "allowed", ""
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
		decision = "clamped"
	}
	return Verdict{Decision: decision, Value: value, Reason: reason}
}
