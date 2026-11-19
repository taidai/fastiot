package safety

import (
	"testing"
	"time"

	"fastiot/internal/ems/observation"
	"fastiot/internal/ems/signal"
)

var verdictNow = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

func goodSample() signal.Sample {
	return signal.Sample{Input: "soc", Value: signal.Num(50), Quality: signal.Good, SourceTime: verdictNow}
}

func TestRangeClamp(t *testing.T) {
	spec := &Output{HasRange: true, Min: 10, Max: 100}
	for _, test := range []struct {
		value, want float64
		reason      string
	}{
		{120, 100, "clamped to maximum 100"},
		{-5, 10, "clamped to minimum 10"},
	} {
		verdict := Evaluate(Request{Requested: signal.Num(test.value), Spec: spec}, verdictNow)
		if verdict.Decision != "clamped" || verdict.Value != test.want || verdict.Reason != test.reason {
			t.Fatalf("verdict = %+v", verdict)
		}
	}
	verdict := Evaluate(Request{Requested: signal.Num(120)}, verdictNow)
	if verdict.Decision != "allowed" || verdict.Value != 120 {
		t.Fatalf("undeclared range = %+v", verdict)
	}
}

func TestTargetRefusals(t *testing.T) {
	for _, test := range []struct {
		request Request
		reason  string
	}{
		{Request{Requested: signal.Num(50), Manual: true, Automatic: true}, "manual_takeover"},
		{Request{Requested: signal.Num(50), Disabled: true}, "target_disabled"},
		{Request{Requested: signal.Num(50), ReadOnly: true}, "read_only"},
	} {
		verdict := Evaluate(test.request, verdictNow)
		if verdict.Decision != "refused" || verdict.Reason != test.reason {
			t.Fatalf("verdict = %+v", verdict)
		}
	}
	verdict := Evaluate(Request{Requested: signal.Num(50), Manual: true}, verdictNow)
	if verdict.Decision != "allowed" {
		t.Fatalf("authorized human command = %+v", verdict)
	}
}

func TestRequiredInputAndConditions(t *testing.T) {
	spec := &Output{Requires: []string{"soc"}}
	verdict := Evaluate(Request{Requested: signal.Num(50), Spec: spec}, verdictNow)
	if verdict.Decision != "refused" || verdict.Reason != "required input soc is missing" {
		t.Fatalf("missing required input = %+v", verdict)
	}
	stale := goodSample()
	stale.Quality = signal.Stale
	verdict = Evaluate(Request{Requested: signal.Num(50), Spec: spec, Samples: map[string]signal.Sample{"soc": stale}}, verdictNow)
	if verdict.Decision != "refused" || verdict.Reason != "required input soc is not usable: quality is stale" {
		t.Fatalf("stale required input = %+v", verdict)
	}
}

func TestDefaultFreshnessToleranceTracksSharedValue(t *testing.T) {
	spec := &Output{Requires: []string{"soc"}}
	for _, test := range []struct {
		name     string
		age      time.Duration
		expected string
	}{
		{"within tolerance", observation.DefaultMaxAge - time.Second, "allowed"},
		{"beyond tolerance", observation.DefaultMaxAge + time.Second, "refused"},
	} {
		t.Run(test.name, func(t *testing.T) {
			sample := goodSample()
			sample.SourceTime = verdictNow.Add(-test.age)
			verdict := Evaluate(Request{Requested: signal.Num(50), Spec: spec, Samples: map[string]signal.Sample{"soc": sample}}, verdictNow)
			if verdict.Decision != test.expected {
				t.Fatalf("verdict = %+v, want %s", verdict, test.expected)
			}
		})
	}
}

func TestFailedCondition(t *testing.T) {
	spec := &Output{Conditions: []Condition{{Input: "soc", Op: "gt", Value: signal.Num(80)}}}
	verdict := Evaluate(Request{Requested: signal.Num(50), Spec: spec, Samples: map[string]signal.Sample{"soc": goodSample()}}, verdictNow)
	if verdict.Decision != "refused" || verdict.Reason != "safety condition soc = 50 does not satisfy gt 80" {
		t.Fatalf("failed condition = %+v", verdict)
	}
}

func TestCapabilityClamp(t *testing.T) {
	verdict := Evaluate(Request{Requested: signal.Num(80), Spec: &Output{CapPositive: 60}}, verdictNow)
	if verdict.Decision != "clamped" || verdict.Value != 60 || verdict.Reason != "clamped to available discharge power 60" {
		t.Fatalf("capability = %+v", verdict)
	}
}

func TestUnspecifiedOutputAllowed(t *testing.T) {
	verdict := Evaluate(Request{Output: "other", Requested: signal.Num(42)}, verdictNow)
	if verdict.Decision != "allowed" || verdict.Value != 42 {
		t.Fatalf("unspecified = %+v", verdict)
	}
}
