package runtime

import (
	"testing"
	"time"

	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/signal"
	"fastiot/internal/ems/state"
)

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}

func tempDevice() channel.Device {
	return channel.Device{
		ID: "d1", ServerID: 1,
		Points: []channel.Point{{
			ID: "temp", Function: channel.HoldingRegister, Address: 10,
			Format: "uint16", Scale: 0.1, Kind: signal.Number,
		}},
	}
}

func TestRejectedObservationDoesNotRepublishOrRefreshHistory(t *testing.T) {
	service := NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	now := time.Date(2026, 11, 15, 12, 0, 10, 0, time.UTC)
	firstTime := now.Add(-5 * time.Second)
	reading := func(at time.Time, value float64) channel.Result {
		return channel.Result{DeviceID: "d1", PointID: "temp", Value: signal.Num(value), Quality: signal.Good, SourceTime: at}
	}
	var published []channel.Result
	service.OnResults = func(_ string, results []channel.Result) { published = append(published, results...) }
	service.Observe(now, []channel.Result{reading(firstTime, 37)})
	service.Observe(now.Add(time.Second), []channel.Result{reading(now.Add(-8*time.Second), 12)})
	service.Observe(now.Add(2*time.Second), []channel.Result{reading(firstTime, 37)})
	service.Observe(now.Add(3*time.Second), []channel.Result{reading(now.Add(time.Hour), 99)})
	if len(published) != 1 || service.History.Len("d1", "temp") != 1 {
		t.Errorf("rejected samples escaped into publication/history: published=%d history=%d", len(published), service.History.Len("d1", "temp"))
	}
	service.Observe(now.Add(4*time.Second), []channel.Result{reading(now.Add(-4*time.Second), 38)})
	history := service.History.Query("d1", "temp", time.Time{}, time.Time{}, 10)
	if len(published) != 2 || len(history) != 2 || history[0].Value.Num != 37 || history[1].Value.Num != 38 {
		t.Fatalf("accepted observation sequence lost: published=%+v history=%+v", published, history)
	}
	if !published[0].SourceTime.Equal(firstTime) || !history[0].SourceTime.Equal(firstTime) || !history[0].ReceivedTime.Equal(now) {
		t.Fatal("publication or history replaced observation or receipt time")
	}
}

func TestHistoryRecordsFailedAttemptsWithReason(t *testing.T) {
	service := NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	service.Observe(now, []channel.Result{{DeviceID: "d1", PointID: "temp", Value: signal.Num(37), Quality: signal.Good, SourceTime: now.Add(-time.Second), Unit: "C"}})
	failure := func(reason string) channel.Result {
		return channel.Result{DeviceID: "d1", PointID: "temp", Quality: signal.Invalid, Error: reason}
	}
	service.Observe(now.Add(time.Second), []channel.Result{failure("timeout")})
	service.Observe(now.Add(2*time.Second), []channel.Result{failure("timeout")})
	events := service.History.QueryEvents("d1", "temp", time.Time{}, time.Time{}, 10)
	if len(events) != 1 {
		t.Fatalf("repeated failure reason was recorded %d times: %+v", len(events), events)
	}
	event := events[0]
	if event.Kind != "quality_event" || event.Reason != "timeout" || event.Quality != signal.Invalid || event.TimeSource != "attempt" || !event.SourceTime.Equal(now.Add(time.Second)) {
		t.Fatalf("failure event = %+v", event)
	}
	service.Observe(now.Add(3*time.Second), []channel.Result{failure("crc")})
	if events = service.History.QueryEvents("d1", "temp", time.Time{}, time.Time{}, 10); len(events) != 2 || events[1].Reason != "crc" {
		t.Fatalf("changed failure reason = %+v", events)
	}
	if got := service.History.Query("d1", "temp", time.Time{}, time.Time{}, 10); len(got) != 1 || got[0].Value.Num != 37 {
		t.Fatalf("failed attempts entered observations: %+v", got)
	}
}

func TestLateObservationIsRecordedWithoutRollingBackState(t *testing.T) {
	service := NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	var captures []state.Capture
	service.OnObservations = func(snapshot state.Capture) { captures = append(captures, snapshot) }
	published := 0
	service.OnResults = func(_ string, results []channel.Result) { published += len(results) }
	service.Observe(now, []channel.Result{{DeviceID: "d1", PointID: "temp", Value: signal.Num(38), Quality: signal.Good, SourceTime: now, Unit: "C"}})
	service.Observe(now.Add(time.Second), []channel.Result{{DeviceID: "d1", PointID: "temp", Value: signal.Num(12), Quality: signal.Good, SourceTime: now.Add(-30 * time.Second), Unit: "C"}})

	if published != 1 {
		t.Fatalf("late observation was republished to rules or callers: %d", published)
	}

	if got := service.History.Query("d1", "temp", time.Time{}, time.Time{}, 10); len(got) != 1 || got[0].Value.Num != 38 {
		t.Fatalf("late observation replaced the current series: %+v", got)
	}
	events := service.History.QueryEvents("d1", "temp", time.Time{}, time.Time{}, 10)
	if len(events) != 1 || events[0].Kind != "observation" || !events[0].Late || events[0].Value.Num != 12 || !events[0].SourceTime.Equal(now.Add(-30*time.Second)) || !events[0].ReceivedTime.Equal(now.Add(time.Second)) {
		t.Fatalf("late event = %+v", events)
	}
	value, _ := service.State.Get(now.Add(time.Second), "d1", "temp", time.Minute)
	if value.Value.Num != 38 || !value.SourceTime.Equal(now) {
		t.Fatalf("late observation changed current state: %+v", value)
	}
	if len(captures) != 2 || captures[1].Values[0].Value.Num != 38 {
		t.Fatalf("late observation changed the evaluation capture: %+v", captures)
	}
}

func TestStalenessTransitionRecordsExpiryEventOnce(t *testing.T) {
	service := NewService(nil, 5*time.Second, time.Second, 100)
	defer service.Close()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	service.Observe(now, []channel.Result{{DeviceID: "d1", PointID: "temp", Value: signal.Num(37), Quality: signal.Good, SourceTime: now, Unit: "C"}})
	service.SweepStaleness(now.Add(6 * time.Second))
	service.SweepStaleness(now.Add(7 * time.Second))
	events := service.History.QueryEvents("d1", "temp", time.Time{}, time.Time{}, 10)
	if len(events) != 1 || events[0].Quality != signal.Stale || events[0].Reason != "source_expired" || events[0].TimeSource != "expiry" {
		t.Fatalf("expiry event = %+v", events)
	}
	service.Observe(now.Add(8*time.Second), []channel.Result{{DeviceID: "d1", PointID: "temp", Value: signal.Num(38), Quality: signal.Good, SourceTime: now.Add(8 * time.Second), Unit: "C"}})
	service.SweepStaleness(now.Add(14 * time.Second))
	events = service.History.QueryEvents("d1", "temp", time.Time{}, time.Time{}, 10)
	if len(events) != 2 || !events[1].SourceTime.Equal(now.Add(14*time.Second)) {
		t.Fatalf("second expiry transition = %+v", events)
	}
}

func TestServicePollsAndRecordsHistory(t *testing.T) {
	bus := &fakeBus{coils: map[uint16]bool{}, holding: map[uint16]uint16{10: 351}, fail: map[byte]bool{}}
	service := NewService(nil, 5*time.Second, 3*time.Second, 100)
	defer service.Close()
	if err := service.AddChannel("c1", bus, []channel.Device{tempDevice()}, 5*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		value, ok := service.State.Get(time.Now(), "d1", "temp", time.Minute)
		return ok && value.Value.Num == 35.1
	})
	waitFor(t, func() bool { return service.History.Len("d1", "temp") >= 2 })
	history := service.History.Query("d1", "temp", time.Time{}, time.Time{}, 10)
	if len(history) < 2 || history[len(history)-1].Value.Num != 35.1 {
		t.Fatalf("history = %+v", history)
	}
}

func TestRemoveChannelStopsPolling(t *testing.T) {
	bus := &fakeBus{coils: map[uint16]bool{}, holding: map[uint16]uint16{10: 100}, fail: map[byte]bool{}}
	service := NewService(nil, 5*time.Second, 3*time.Second, 100)
	defer service.Close()
	if err := service.AddChannel("c1", bus, []channel.Device{tempDevice()}, 5*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(service.Channels()) == 1 })
	ch := service.Channels()[0]
	waitFor(t, func() bool { return ch.Stats("d1").Requests >= 2 })
	if !service.RemoveChannel("c1") {
		t.Fatal("channel was not removed")
	}
	requests := ch.Stats("d1").Requests
	time.Sleep(30 * time.Millisecond)
	if later := ch.Stats("d1").Requests; later > requests+1 {
		t.Fatalf("polling continued after removal: %d -> %d", requests, later)
	}
	if len(service.Channels()) != 0 {
		t.Fatal("channel list should be empty")
	}
}

func TestObservationCallbackPreservesRapidChangesAndFailures(t *testing.T) {
	service := NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	var captured []state.Capture
	service.OnObservations = func(snapshot state.Capture) { captured = append(captured, snapshot) }
	published := 0
	service.OnResults = func(_ string, values []channel.Result) { published += len(values) }
	at := time.Now()
	for i, value := range []float64{45, 20, 46} {
		now := at.Add(time.Duration(i) * time.Millisecond)
		service.Observe(now, []channel.Result{{DeviceID: "d1", PointID: "p1", Value: signal.Value{Kind: signal.Number, Num: value}, Quality: signal.Good, SourceTime: now}})
	}
	service.Observe(at.Add(3*time.Millisecond), []channel.Result{{DeviceID: "d1", PointID: "p1", Quality: signal.Invalid, Error: "read_failed"}})
	if len(captured) != 4 || published != 3 {
		t.Fatalf("observation sequence lost: captures=%d published=%d", len(captured), published)
	}
	for i, want := range []float64{45, 20, 46} {
		if len(captured[i].Values) != 1 || captured[i].Values[0].Value.Num != want {
			t.Fatalf("capture %d lost its original value", i)
		}
	}
	if captured[3].Values[0].Quality != signal.Invalid {
		t.Fatal("failed observation did not notify alarm evaluation")
	}
}

func TestServiceRaisesOfflineAlarm(t *testing.T) {
	bus := &fakeBus{coils: map[uint16]bool{}, holding: map[uint16]uint16{}, fail: map[byte]bool{1: true}}
	service := NewService(nil, 5*time.Second, 3*time.Second, 100)
	defer service.Close()
	if err := service.AddChannel("c1", bus, []channel.Device{tempDevice()}, 5*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		for _, value := range service.Alarms.List() {
			if value.Key == "device-offline:d1" && value.Active {
				return true
			}
		}
		return false
	})
}

func TestIngestRecordsObservationsAndEventsThroughOnePath(t *testing.T) {
	service := NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	accepted := service.ingest(now, []channel.Result{
		{DeviceID: "d1", PointID: "temp", Value: signal.Num(37), Quality: signal.Good, SourceTime: now.Add(-time.Second), Unit: "C"},
		{DeviceID: "d1", PointID: "temp", Quality: signal.Invalid, Error: "timeout"},
		{DeviceID: "d1", PointID: "temp", Value: signal.Num(12), Quality: signal.Good, SourceTime: now.Add(-time.Minute)},
	})
	if len(accepted) != 1 || accepted[0].Value.Num != 37 {
		t.Fatalf("accepted = %+v", accepted)
	}
	observations := service.History.Query("d1", "temp", time.Time{}, time.Time{}, 10)
	if len(observations) != 1 || observations[0].Value.Num != 37 || observations[0].ReceivedTime != now {
		t.Fatalf("observations = %+v", observations)
	}
	events := service.History.Timeline("d1", "temp", time.Time{}, time.Time{}, 0)
	if len(events) != 3 {
		t.Fatalf("timeline = %+v", events)
	}
	if events[0].Kind != "observation" || !events[0].Late || events[0].Value.Num != 12 {
		t.Fatalf("late observation = %+v", events[0])
	}
	if events[1].Kind != "observation" || events[1].Value.Num != 37 {
		t.Fatalf("observation kind missing: %+v", events[1])
	}
	if events[2].Kind != "quality_event" || events[2].Reason != "timeout" {
		t.Fatalf("quality event = %+v", events[2])
	}
}
