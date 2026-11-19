package decision

import (
	"strings"
	"testing"
	"time"

	"fastiot/internal/ems/observation"
	"fastiot/internal/ems/rule"
	"fastiot/internal/ems/safety"
	"fastiot/internal/ems/signal"
)

// The ramp safety check must use the same freshness tolerance as every other
// safety input check, so changing the shared value moves both.
func TestRampUsesSharedFreshnessTolerance(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	spec := safety.Output{Kind: signal.Number, Requires: []string{"soc"}}
	for _, test := range []struct {
		name    string
		age     time.Duration
		blocked bool
	}{
		{"sample within tolerance", observation.DefaultMaxAge - time.Second, false},
		{"sample beyond tolerance", observation.DefaultMaxAge + time.Second, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &Client{}
			active := &activeTable{
				values: map[string]signal.Sample{
					"soc": {Input: "soc", Value: signal.Num(50), Quality: signal.Good, SourceTime: now.Add(-test.age)},
				},
				rampTargets: map[string]float64{"pcs": 80},
				rampRates:   map[string]float64{"pcs": 10},
				rampValues:  map[string]float64{"pcs": 80},
				rampAt:      map[string]time.Time{"pcs": now.Add(-time.Second)},
				safety:      map[string]safety.Output{"pcs": spec},
			}
			outcome := &rule.Outcome{}
			changed := false
			if err := client.stepRamps(active, now, outcome, &changed); err != nil {
				t.Fatalf("stepRamps: %v", err)
			}
			if test.blocked {
				if len(outcome.Actions) != 1 || !outcome.Actions[0].Blocked || !strings.Contains(outcome.Actions[0].Reason, "not usable") {
					t.Fatalf("stale sample did not stop the ramp: %+v", outcome.Actions)
				}
				return
			}
			if len(outcome.Actions) != 0 {
				t.Fatalf("fresh sample stopped the ramp: %+v", outcome.Actions)
			}
		})
	}
}
