package safety

import (
	"strings"
	"testing"
	"time"

	"fastiot/internal/ems/signal"
)

var now = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func sample(input string, value signal.Value, quality signal.Quality) signal.Sample {
	return signal.Sample{Input: input, Value: value, Quality: quality, SourceTime: now, ReceivedTime: now}
}

func limiter(outputs map[string]Output, samples map[string]signal.Sample) Limiter {
	return Limiter{MaxAge: 5 * time.Second, Outputs: outputs, Samples: samples}
}

func TestClampsToRange(t *testing.T) {
	l := limiter(map[string]Output{"pcs": {Kind: signal.Number, HasRange: true, Min: -100, Max: 100}}, nil)
	result := l.Limit(now, "pcs", signal.Num(500))
	if result.Blocked || result.Value.Num != 100 || !strings.Contains(result.Reason, "maximum") {
		t.Fatalf("unexpected result %+v", result)
	}
}

func TestClampsToAvailablePowerBothDirections(t *testing.T) {
	output := Output{Kind: signal.Number, CapPositive: 300, CapNegative: 200}
	l := limiter(map[string]Output{"pcs": output}, nil)

	discharge := l.Limit(now, "pcs", signal.Num(500))
	if discharge.Value.Num != 300 || !strings.Contains(discharge.Reason, "discharge") {
		t.Fatalf("discharge clamp failed: %+v", discharge)
	}
	charge := l.Limit(now, "pcs", signal.Num(-500))
	if charge.Value.Num != -200 || !strings.Contains(charge.Reason, "charge") {
		t.Fatalf("charge clamp failed: %+v", charge)
	}
}

func TestBlocksWhenRequiredInputIsNotUsable(t *testing.T) {
	output := Output{Kind: signal.Number, HasRange: true, Min: -500, Max: 500, Requires: []string{"soc"}}
	l := limiter(map[string]Output{"pcs": output}, map[string]signal.Sample{
		"soc": sample("soc", signal.Num(50), signal.Stale),
	})
	result := l.Limit(now, "pcs", signal.Num(100))
	if !result.Blocked || !strings.Contains(result.Reason, "soc") {
		t.Fatalf("expected block on stale soc, got %+v", result)
	}
}

func TestBlocksUnknownOutputAndKindMismatch(t *testing.T) {
	l := limiter(map[string]Output{"pcs": {Kind: signal.Number}}, nil)
	if result := l.Limit(now, "missing", signal.Num(1)); !result.Blocked {
		t.Fatalf("unknown output must block: %+v", result)
	}
	if result := l.Limit(now, "pcs", signal.Flag(true)); !result.Blocked || !strings.Contains(result.Reason, "expects") {
		t.Fatalf("kind mismatch must block: %+v", result)
	}
}

func TestBoolPassesThrough(t *testing.T) {
	l := limiter(map[string]Output{"run": {Kind: signal.Bool}}, nil)
	result := l.Limit(now, "run", signal.Flag(true))
	if result.Blocked || !result.Value.Bool {
		t.Fatalf("bool output should pass: %+v", result)
	}
}

// The limiter must not own a second copy of the envelope: it has to return
// exactly the verdict Evaluate gives for the same inputs.
func TestLimiterReturnsEvaluateVerdict(t *testing.T) {
	output := Output{Kind: signal.Number, HasRange: true, Min: 0, Max: 100,
		Conditions: []Condition{{Input: "soc", Op: "ge", Value: signal.Num(10)}}}
	samples := map[string]signal.Sample{"soc": sample("soc", signal.Num(5), signal.Good)}
	l := limiter(map[string]Output{"pcs": output}, samples)
	result := l.Limit(now, "pcs", signal.Num(500))
	verdict := Evaluate(Request{Output: "pcs", Requested: signal.Num(500), Spec: &output, Samples: samples, MaxAge: 5 * time.Second}, now)
	if !result.Blocked || verdict.Decision != "refused" || result.Reason != verdict.Reason {
		t.Fatalf("limiter drifted from Evaluate: %+v vs %+v", result, verdict)
	}

	output.Conditions = nil
	l = limiter(map[string]Output{"pcs": output}, samples)
	result = l.Limit(now, "pcs", signal.Num(500))
	verdict = Evaluate(Request{Output: "pcs", Requested: signal.Num(500), Spec: &output, Samples: samples, MaxAge: 5 * time.Second}, now)
	if result.Blocked || result.Value.Num != verdict.Value || result.Reason != verdict.Reason {
		t.Fatalf("limiter drifted from Evaluate: %+v vs %+v", result, verdict)
	}
}

// A limiter with no MaxAge must inherit Evaluate's freshness default rather
// than silently skipping the staleness check.
func TestLimiterUsesEvaluateFreshnessDefault(t *testing.T) {
	spec := Output{Kind: signal.Number, Requires: []string{"soc"}}
	stale := sample("soc", signal.Num(50), signal.Good)
	stale.SourceTime = now.Add(-20 * time.Second)
	stale.ReceivedTime = stale.SourceTime
	l := Limiter{Outputs: map[string]Output{"pcs": spec}, Samples: map[string]signal.Sample{"soc": stale}}
	result := l.Limit(now, "pcs", signal.Num(10))
	if !result.Blocked || !strings.Contains(result.Reason, "not usable") {
		t.Fatalf("stale input must block: %+v", result)
	}
}
