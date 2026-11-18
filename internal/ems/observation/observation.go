// Package observation resolves one observation's usability, reason and time
// basis for runtime consumers and presentation adapters.
package observation

import (
	"time"

	"fastiot/internal/ems/signal"
)

const (
	Usable  = "usable"
	Stale   = "stale"
	Invalid = "invalid"
	Unknown = "unknown"
)

type Input struct {
	Value         signal.Value
	Quality       signal.Quality
	Reason        string
	SourceTime    time.Time
	ReceivedTime  time.Time
	MaxAge        time.Duration
	ExpiresAt     time.Time
	AllowNoExpiry bool
}

type Verdict struct {
	State        string
	Quality      signal.Quality
	Reason       string
	SourceTime   time.Time
	ReceivedTime time.Time
	ExpiresAt    time.Time
}

func Evaluate(input Input, now time.Time) Verdict {
	verdict := Verdict{Quality: input.Quality, Reason: input.Reason, SourceTime: input.SourceTime, ReceivedTime: input.ReceivedTime, ExpiresAt: input.ExpiresAt}
	if verdict.ExpiresAt.IsZero() && input.MaxAge > 0 && !input.SourceTime.IsZero() {
		verdict.ExpiresAt = input.SourceTime.Add(input.MaxAge)
	}
	switch input.Quality {
	case signal.Uninitialized:
		verdict.State = Unknown
		return verdict
	case signal.Stale:
		verdict.State = Stale
		return verdict
	case signal.Invalid:
		verdict.State = Invalid
		return verdict
	}
	if !input.Value.Valid() {
		verdict.State, verdict.Quality, verdict.Reason = Invalid, signal.Invalid, "invalid_value"
		return verdict
	}
	if input.SourceTime.IsZero() && !input.AllowNoExpiry {
		verdict.State, verdict.Quality, verdict.Reason = Unknown, signal.Uninitialized, "missing_source_time"
		return verdict
	}
	if input.SourceTime.UTC().After(now.UTC()) {
		verdict.State, verdict.Quality, verdict.Reason = Invalid, signal.Invalid, "future_source_time"
		return verdict
	}
	if input.MaxAge <= 0 && verdict.ExpiresAt.IsZero() && !input.AllowNoExpiry {
		verdict.State, verdict.Quality, verdict.Reason = Unknown, signal.Uninitialized, "expiry_unconfigured"
		return verdict
	}
	if !verdict.ExpiresAt.IsZero() && now.After(verdict.ExpiresAt) {
		verdict.State, verdict.Quality, verdict.Reason = Stale, signal.Stale, "source_expired"
		return verdict
	}
	verdict.State, verdict.Quality, verdict.Reason = Usable, signal.Good, ""
	return verdict
}
