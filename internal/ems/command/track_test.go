package command

import (
	"testing"
	"time"

	"fastiot/internal/ems/signal"
)

// A caller-dispatched point write keeps one canonical outcome in the manager.
// The manager never writes it and Tick never advances it: only the caller's
// state report moves it.
func TestTrackedPointWriteIsOneCanonicalCallerAdvancedOutcome(t *testing.T) {
	writer := &fakeWriter{}
	samples := map[string]signal.Sample{}
	m := manager(writer, &samples)
	writeRequest := Request{ID: "manual-1", DeviceID: "io-1", Point: "io-1", Value: signal.Num(40), Actor: "admin", Source: "manual"}

	first, err := m.Track(t0, writeRequest)
	if err != nil {
		t.Fatal(err)
	}
	if first.State != Accepted || !first.External {
		t.Fatalf("tracked outcome = %+v, want accepted and external", first)
	}
	if writer.calls != 0 {
		t.Fatalf("Track wrote to the device %d times, want 0", writer.calls)
	}
	if len(first.Writes) != 1 || first.Writes[0].Value.Num != 40 {
		t.Fatalf("tracked writes = %+v, want one write of 40", first.Writes)
	}

	duplicate, err := m.Track(t0, writeRequest)
	if err != nil || duplicate.RequestID != first.RequestID || duplicate.StartedAt != first.StartedAt {
		t.Fatalf("duplicate track = %+v, %v; want the original outcome", duplicate, err)
	}
	if _, err := m.Track(t0, Request{ID: "manual-1", DeviceID: "io-1", Point: "io-1", Value: signal.Num(50)}); err == nil {
		t.Fatal("reusing a request id with another value must conflict")
	}
	if got := len(m.List()); got != 1 {
		t.Fatalf("outcome count = %d, want 1", got)
	}

	// Tick owns manager-dispatched requests only; an external request waiting on
	// its caller must not be timed out into Unknown.
	m.Tick(t0.Add(time.Hour))
	if got, _ := m.Get("manual-1"); got.State != Accepted {
		t.Fatalf("Tick moved a tracked request to %s", got.State)
	}

	updated, ok := m.SetState(t0.Add(time.Second), "manual-1", Failed, "read_only")
	if !ok || updated.State != Failed || updated.Reason != "read_only" {
		t.Fatalf("SetState = %+v, %v", updated, ok)
	}
	m.Tick(t0.Add(2 * time.Hour))
	if got, _ := m.Get("manual-1"); got.State != Failed {
		t.Fatalf("Tick overwrote a terminal tracked request: %+v", got)
	}

	if _, ok := m.SetState(t0, "missing", Failed, ""); ok {
		t.Fatal("SetState accepted an unknown request id")
	}

	// A request the manager dispatched itself is advanced by Tick, not by the
	// caller's state report.
	if _, err := m.Submit(t0, request("dispatched-1")); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.SetState(t0.Add(time.Second), "dispatched-1", Failed, "external edit"); ok {
		t.Fatal("SetState changed a manager-dispatched request")
	}
}
