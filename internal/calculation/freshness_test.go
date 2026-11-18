package calculation

import (
	"testing"
	"time"

	"fastiot/internal/ems/observation"
	"fastiot/internal/ems/signal"
)

func TestFreshnessAtAndAfterValidUntil(t *testing.T) {
	at := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	value := 42.0
	sample := Sample{ID: "a", Kind: "number", Value: &value, Quality: "good", Source: "observation", SourceTime: at.Add(-time.Second), ReceivedTime: at, ValidUntil: at}
	if verdict := Freshness(sample, at); verdict.State != observation.Usable || verdict.Quality != signal.Good {
		t.Fatalf("exact boundary = %+v, want usable", verdict)
	}
	verdict := Freshness(sample, at.Add(time.Millisecond))
	if verdict.State != observation.Stale || verdict.Quality != signal.Stale || verdict.Reason != "expired" {
		t.Fatalf("after boundary = %+v, want stale/expired", verdict)
	}
	result := Result{ID: "a", Quality: "good", Value: &value, ValidUntil: at}
	if verdict := result.Freshness(at.Add(time.Millisecond)); verdict.State != observation.Stale {
		t.Fatalf("result freshness = %+v, want stale", verdict)
	}
}

func TestFreshnessAllowsConfigurationWithoutExpiry(t *testing.T) {
	value := 100.0
	sample := Sample{ID: "capacity", Kind: "number", Value: &value, Quality: "good", Source: "configuration"}
	verdict := Freshness(sample, time.Now())
	if verdict.State != observation.Usable || verdict.Quality != signal.Good || !verdict.ExpiresAt.IsZero() {
		t.Fatalf("configuration freshness = %+v", verdict)
	}
}

func TestOrdinaryObservationWithoutExpiryIsUnknown(t *testing.T) {
	at := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	value := 42.0
	sample := Sample{ID: "meter", Kind: "number", Value: &value, Quality: "good", Source: "observation", SourceTime: at}
	verdict := Freshness(sample, at)
	if verdict.State != observation.Unknown || verdict.Quality != signal.Uninitialized || verdict.Reason != "expiry_unconfigured" {
		t.Fatalf("undeclared observation expiry = %+v, want unknown/expiry_unconfigured", verdict)
	}
}
