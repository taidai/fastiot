package observation

import (
	"testing"
	"time"

	"fastiot/internal/ems/signal"
)

func TestEvaluate(t *testing.T) {
	source := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	received := source.Add(20 * time.Millisecond)
	base := Input{Value: signal.Num(42), Quality: signal.Good, SourceTime: source, ReceivedTime: received, MaxAge: 10 * time.Second}
	tests := []struct {
		name        string
		now         time.Time
		mutate      func(*Input)
		wantState   string
		wantQuality signal.Quality
		wantReason  string
		wantExpiry  time.Time
	}{
		{name: "before expiry", now: source.Add(9 * time.Second), wantState: Usable, wantQuality: signal.Good, wantExpiry: source.Add(10 * time.Second)},
		{name: "exact expiry boundary", now: source.Add(10 * time.Second), wantState: Usable, wantQuality: signal.Good, wantExpiry: source.Add(10 * time.Second)},
		{name: "after expiry", now: source.Add(10*time.Second + time.Millisecond), wantState: Stale, wantQuality: signal.Stale, wantReason: "source_expired", wantExpiry: source.Add(10 * time.Second)},
		{name: "future source", now: source, mutate: func(input *Input) { input.SourceTime = source.Add(time.Second) }, wantState: Invalid, wantQuality: signal.Invalid, wantReason: "future_source_time", wantExpiry: source.Add(11 * time.Second)},
		{name: "explicit invalid", now: source, mutate: func(input *Input) { input.Quality = signal.Invalid; input.Reason = "read_failed" }, wantState: Invalid, wantQuality: signal.Invalid, wantReason: "read_failed", wantExpiry: source.Add(10 * time.Second)},
		{name: "invalid value", now: source, mutate: func(input *Input) { input.Value = signal.Value{Kind: signal.Kind(99)} }, wantState: Invalid, wantQuality: signal.Invalid, wantReason: "invalid_value", wantExpiry: source.Add(10 * time.Second)},
		{name: "missing observation", now: source, mutate: func(input *Input) {
			input.Quality = signal.Uninitialized
			input.Reason = "not_entered"
			input.SourceTime = time.Time{}
			input.ReceivedTime = time.Time{}
		}, wantState: Unknown, wantQuality: signal.Uninitialized, wantReason: "not_entered"},
		{name: "no declared expiry", now: source, mutate: func(input *Input) { input.MaxAge = 0 }, wantState: Unknown, wantQuality: signal.Uninitialized, wantReason: "expiry_unconfigured"},
		{name: "explicit non-expiring input", now: source, mutate: func(input *Input) { input.MaxAge = 0; input.AllowNoExpiry = true }, wantState: Usable, wantQuality: signal.Good},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := base
			if test.mutate != nil {
				test.mutate(&input)
			}
			verdict := Evaluate(input, test.now)
			if verdict.State != test.wantState || verdict.Quality != test.wantQuality || verdict.Reason != test.wantReason {
				t.Fatalf("verdict = %+v, want state=%s quality=%s reason=%s", verdict, test.wantState, test.wantQuality, test.wantReason)
			}
			if !verdict.ExpiresAt.Equal(test.wantExpiry) {
				t.Errorf("expires_at = %s, want %s", verdict.ExpiresAt, test.wantExpiry)
			}
			if !verdict.SourceTime.Equal(input.SourceTime) || !verdict.ReceivedTime.Equal(input.ReceivedTime) {
				t.Errorf("time basis changed: %+v", verdict)
			}
		})
	}
}
