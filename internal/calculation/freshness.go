package calculation

import (
	"time"

	"fastiot/internal/ems/observation"
	"fastiot/internal/ems/signal"
)

// Freshness adapts a calculation sample to the shared observation verdict.
func Freshness(sample Sample, now time.Time) observation.Verdict {
	quality := signal.Uninitialized
	switch sample.Quality {
	case "good":
		quality = signal.Good
	case "stale":
		quality = signal.Stale
	case "invalid":
		quality = signal.Invalid
	}
	value := signal.Value{Kind: signal.None}
	if sample.Value != nil {
		value = signal.Num(*sample.Value)
	}
	maxAge := time.Duration(0)
	if !sample.SourceTime.IsZero() && !sample.ValidUntil.IsZero() {
		maxAge = sample.ValidUntil.Sub(sample.SourceTime)
	}
	verdict := observation.Evaluate(observation.Input{
		Value: value, Quality: quality, Reason: sample.Reason,
		SourceTime: sample.SourceTime, ReceivedTime: sample.ReceivedTime,
		MaxAge: maxAge, ExpiresAt: sample.ValidUntil, AllowNoExpiry: sample.Source != signal.SourceObservation || sample.Simulated,
	}, now)
	verdict.ExpiresAt = sample.ValidUntil
	if verdict.State == observation.Stale && verdict.Reason == "source_expired" {
		verdict.Reason = "expired"
	}
	return verdict
}

func (r Result) Freshness(now time.Time) observation.Verdict {
	return Freshness(r.sample(), now)
}
