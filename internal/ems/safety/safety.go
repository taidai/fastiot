// Package safety applies range, capability and freshness limits to actions
// requested by a decision table before they become device commands.
package safety

import (
	"time"

	"fastiot/internal/ems/rule"
	"fastiot/internal/ems/signal"
)

// Condition must hold before an output is written. Conditions reuse the typed
// signal model so SOC/temperature/status checks are explicit and explainable.
type Condition struct {
	Input string
	Op    string
	Value signal.Value
}

// Output is the safe envelope of one writable output. Capabilities are
// magnitudes: CapPositive applies to discharge, CapNegative to charge. Zero
// means no capability limit.
type Output struct {
	Kind        signal.Kind
	HasRange    bool
	Min         float64
	Max         float64
	CapPositive float64
	CapNegative float64
	// Requires lists inputs that must be usable before this output is written.
	Requires []string
	// Conditions must all compare true; unusable inputs block the write.
	Conditions []Condition
	// Ramp is the maximum change per second. The command layer applies it.
	Ramp float64
}

// Limiter checks requests against outputs and the current samples. Create one
// per evaluation with a snapshot of samples; do not share it across goroutines.
type Limiter struct {
	MaxAge  time.Duration
	Outputs map[string]Output
	Samples map[string]signal.Sample
}

// Limit implements rule.Limiter. It is a thin adapter over Evaluate: the
// envelope has exactly one implementation, so automatic rows, ramps and
// manual writes cannot disagree. A limiter without MaxAge inherits Evaluate's
// default freshness window.
func (l Limiter) Limit(now time.Time, output string, requested signal.Value) rule.LimitResult {
	spec, ok := l.Outputs[output]
	if !ok {
		return rule.LimitResult{Value: requested, Blocked: true, Reason: "output " + output + " has no safety envelope"}
	}
	verdict := Evaluate(Request{
		Output: output, Requested: requested, Spec: &spec,
		Samples: l.Samples, MaxAge: l.MaxAge,
	}, now).Effective()
	if verdict.Decision == DecisionRefused {
		return rule.LimitResult{Value: requested, Blocked: true, Reason: verdict.Reason}
	}
	applied := requested
	if verdict.Decision == DecisionClamped {
		applied = signal.Num(verdict.Value)
	}
	return rule.LimitResult{Value: applied, Reason: verdict.Reason}
}

// RampStep moves current toward target by at most rate per second. It is
// deliberately a pure function so the command layer can persist its own last
// applied value and the behavior stays testable.
func RampStep(current, target, rate float64, elapsed time.Duration) float64 {
	if rate <= 0 || elapsed <= 0 || current == target {
		return current
	}
	delta := rate * elapsed.Seconds()
	if target > current {
		if current+delta >= target {
			return target
		}
		return current + delta
	}
	if current-delta <= target {
		return target
	}
	return current - delta
}
