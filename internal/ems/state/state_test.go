package state

import (
	"testing"
	"time"

	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/signal"
)

var t0 = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func result(deviceID, pointID string, value float64) channel.Result {
	return channel.Result{
		DeviceID: deviceID, PointID: pointID,
		Value: signal.Num(value), Quality: signal.Good, SourceTime: t0,
	}
}

func TestApplyAndGet(t *testing.T) {
	store := New()
	store.Apply(t0, []channel.Result{result("d1", "temp", 35.1)})
	value, ok := store.Get(t0, "d1", "temp", 10*time.Second)
	if !ok || value.Quality != signal.Good || value.Value.Num != 35.1 {
		t.Fatalf("unexpected value %+v ok=%v", value, ok)
	}
	if _, ok := store.Get(t0, "d1", "missing", time.Second); ok {
		t.Fatal("missing point must not be found")
	}
}

func TestOlderAndDuplicateObservationsDoNotRefreshCurrent(t *testing.T) {
	store := New()
	current := result("d1", "temp", 37)
	current.SourceTime = t0.Add(5 * time.Second)
	received := t0.Add(10 * time.Second)
	store.Apply(received, []channel.Result{current})

	older := result("d1", "temp", 12)
	older.SourceTime = t0.Add(2 * time.Second)
	store.Apply(received.Add(time.Second), []channel.Result{older})
	value, ok := store.Get(received.Add(time.Second), "d1", "temp", time.Minute)
	if !ok || value.Value.Num != 37 || !value.SourceTime.Equal(current.SourceTime) || !value.ReceivedTime.Equal(received) {
		t.Errorf("older sample replaced the current observation: %+v", value)
	}

	store.Apply(received.Add(2*time.Second), []channel.Result{current})
	value, _ = store.Get(received.Add(2*time.Second), "d1", "temp", time.Minute)
	if value.Value.Num != 37 || !value.SourceTime.Equal(current.SourceTime) || !value.ReceivedTime.Equal(received) {
		t.Errorf("duplicate refreshed the accepted observation: %+v", value)
	}

	current.SourceTime = t0.Add(6 * time.Second)
	store.Apply(received.Add(3*time.Second), []channel.Result{current})
	value, _ = store.Get(received.Add(3*time.Second), "d1", "temp", time.Minute)
	if value.Value.Num != 37 || !value.SourceTime.Equal(current.SourceTime) || !value.ReceivedTime.Equal(received.Add(3*time.Second)) {
		t.Errorf("new observation with the same numeric value was discarded: %+v", value)
	}
}

func TestUntrustedTimeDoesNotPoisonRecovery(t *testing.T) {
	for _, kind := range []string{"future", "missing", "same-time-conflict"} {
		t.Run(kind, func(t *testing.T) {
			store := New()
			trusted := result("d1", "temp", 37)
			trusted.SourceTime = t0.Add(5 * time.Second)
			received := t0.Add(10 * time.Second)
			store.Apply(received, []channel.Result{trusted})
			bad := result("d1", "temp", 99)
			switch kind {
			case "future":
				bad.SourceTime = received.Add(time.Hour)
			case "missing":
				bad.SourceTime = time.Time{}
			case "same-time-conflict":
				bad.SourceTime = trusted.SourceTime
			}
			if accepted := store.Apply(received.Add(time.Second), []channel.Result{bad}); len(accepted) != 0 {
				t.Error("untrusted time was accepted for publication")
			}
			value, ok := store.Get(received.Add(time.Second), "d1", "temp", time.Minute)
			if !ok || value.Quality != signal.Invalid || value.Reason == "" || !value.LastAttemptTime.Equal(received.Add(time.Second)) || value.Value.Num != 37 || !value.SourceTime.Equal(trusted.SourceTime) || !value.ReceivedTime.Equal(received) {
				t.Errorf("untrusted attempt did not preserve and invalidate the last trusted observation: %+v", value)
			}
			recovery := result("d1", "temp", 38)
			recovery.SourceTime = t0.Add(6 * time.Second)
			store.Apply(received.Add(2*time.Second), []channel.Result{recovery})
			value, _ = store.Get(received.Add(2*time.Second), "d1", "temp", time.Minute)
			if value.Quality != signal.Good || value.Value.Num != 38 || !value.SourceTime.Equal(recovery.SourceTime) {
				t.Errorf("invalid time poisoned recovery: %+v", value)
			}
		})
	}
	t.Run("no-previous-observation", func(t *testing.T) {
		store := New()
		bad := result("d1", "temp", 99)
		bad.SourceTime = t0.Add(time.Hour)
		store.Apply(t0, []channel.Result{bad})
		value, ok := store.Get(t0, "d1", "temp", time.Minute)
		if !ok || value.Quality != signal.Invalid || !value.SourceTime.IsZero() || !value.ReceivedTime.IsZero() {
			t.Errorf("invalid first sample invented a trusted observation: %+v", value)
		}
	})
}

func TestValueGoesStaleByAge(t *testing.T) {
	store := New()
	store.Apply(t0, []channel.Result{result("d1", "temp", 35.1)})
	value, _ := store.Get(t0.Add(11*time.Second), "d1", "temp", 10*time.Second)
	if value.Quality != signal.Stale {
		t.Fatalf("quality = %s, want stale", value.Quality)
	}
	if value.Value.Num != 35.1 {
		t.Fatal("stale values keep the last reading")
	}
}

func TestOfflineDeviceMarksPointsStale(t *testing.T) {
	store := New()
	store.Apply(t0, []channel.Result{result("d1", "temp", 35.1)})
	store.SetHealth("d1", channel.HealthOffline, t0)
	value, _ := store.Get(t0, "d1", "temp", time.Minute)
	if value.Quality != signal.Stale {
		t.Fatalf("offline device quality = %s, want stale", value.Quality)
	}
	if store.Health("d1") != channel.HealthOffline {
		t.Fatal("health not stored")
	}
}

func TestSamplesAndSnapshotAreDeterministic(t *testing.T) {
	store := New()
	store.Apply(t0, []channel.Result{
		result("d2", "b", 2), result("d1", "b", 2), result("d1", "a", 1),
	})
	samples := store.Samples("d1", t0, time.Minute)
	if len(samples) != 2 || samples["a"].Value.Num != 1 {
		t.Fatalf("unexpected samples %+v", samples)
	}
	snapshot := store.Snapshot(t0, time.Minute)
	if len(snapshot) != 3 || snapshot[0].DeviceID != "d1" || snapshot[0].PointID != "a" {
		t.Fatalf("snapshot not sorted: %+v", snapshot)
	}
}
