package health

import (
	"testing"
	"time"

	"github.com/simpleiot/simpleiot/data"
)

func TestEvaluate(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		input      Input
		wantState  string
		wantReason string
		wantAge    time.Duration
	}{
		{name: "creation grace", input: Input{Requests: 1, CreatedAt: now.Add(-2 * time.Second), MaxAge: 10 * time.Second}, wantState: Unknown, wantReason: "awaiting_first_success"},
		{name: "no observation", input: Input{}, wantState: Unknown, wantReason: "no_observation"},
		{name: "no success after grace", input: Input{Requests: 1, CreatedAt: now.Add(-20 * time.Second), MaxAge: 10 * time.Second}, wantState: Offline, wantReason: "no_success"},
		{name: "healthy", input: Input{Requests: 1, LastSuccess: now.Add(-time.Second)}, wantState: Healthy, wantAge: time.Second},
		{name: "degraded after failures", input: Input{Requests: 2, ConsecutiveFailures: 1, LastSuccess: now.Add(-time.Second)}, wantState: Degraded, wantReason: "consecutive_failures", wantAge: time.Second},
		{name: "expiry", input: Input{Requests: 2, LastSuccess: now.Add(-11 * time.Second), MaxAge: 10 * time.Second}, wantState: Offline, wantReason: "source_expired", wantAge: 11 * time.Second},
		{name: "future timestamp", input: Input{Requests: 1, LastSuccess: now.Add(6 * time.Second)}, wantState: Offline, wantReason: "invalid_time", wantAge: -6 * time.Second},
		{name: "consecutive failure limit", input: Input{Requests: 3, ConsecutiveFailures: 3, LastSuccess: now.Add(-time.Second)}, wantState: Offline, wantReason: "consecutive_failures", wantAge: time.Second},
		{name: "explicit max age", input: Input{Requests: 2, LastSuccess: now.Add(-6 * time.Second), PollPeriod: 100 * time.Millisecond, Timeout: 100 * time.Millisecond, MaxAge: 4 * time.Second}, wantState: Offline, wantReason: "source_expired", wantAge: 6 * time.Second},
		{name: "no success with failures", input: Input{Requests: 3, ConsecutiveFailures: 3, CreatedAt: now.Add(-20 * time.Second), MaxAge: 10 * time.Second}, wantState: Offline, wantReason: "no_success"},
		{name: "future with failures", input: Input{Requests: 3, ConsecutiveFailures: 3, LastSuccess: now.Add(6 * time.Second)}, wantState: Offline, wantReason: "invalid_time", wantAge: -6 * time.Second},
		{name: "future one second", input: Input{Requests: 1, LastSuccess: now.Add(time.Second)}, wantState: Offline, wantReason: "invalid_time", wantAge: -time.Second},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verdict := Evaluate(test.input, now)
			if verdict.State != test.wantState || verdict.Reason != test.wantReason || verdict.Age != test.wantAge {
				t.Fatalf("verdict = %+v, want state=%s reason=%s age=%s", verdict, test.wantState, test.wantReason, test.wantAge)
			}
		})
	}
}

func TestCreatedAtPrefersCreationPointOverDescriptionEdit(t *testing.T) {
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	points := data.Points{
		data.NewPointString(data.PointTypeDescription, "", "renamed"),
		data.NewPointString(data.PointTypeCreated, "", "created"),
	}
	points[0].Time = time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	points[1].Time = created
	if got := CreatedAt(points); !got.Equal(created) {
		t.Fatalf("createdAt = %s, want %s", got, created)
	}
}
