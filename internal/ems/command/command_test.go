package command

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"fastiot/internal/ems/device"
	"fastiot/internal/ems/safety"
	"fastiot/internal/ems/signal"
)

var t0 = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

type fakeWriter struct {
	err   error
	calls int
	last  []device.Write
}

func (f *fakeWriter) Write(_, _ string, writes []device.Write) error {
	f.calls++
	f.last = append([]device.Write(nil), writes...)
	return f.err
}

func pcsProfile() device.Profile {
	return device.Profile{
		ID: "enjo", Vendor: "恩玖", Model: "PCS",
		Points: []device.Point{
			{ID: "power", Kind: signal.Number, Unit: "kW", HasRange: true, Min: -500, Max: 500},
			{ID: "run", Kind: signal.Bool},
		},
		Commands: []device.Command{{
			ID: "start", Name: "启动",
			Writes:   []device.Write{{Point: "run", Value: signal.Flag(true)}},
			Readback: []device.Readback{{Point: "run", Op: "eq", Value: signal.Flag(true)}},
			Timeout:  5 * time.Second,
		}},
	}
}

func manager(writer Dispatcher, samples *map[string]signal.Sample) *Manager {
	return New(writer,
		func(deviceID string) (device.Profile, bool) {
			if deviceID != "pcs1" {
				return device.Profile{}, false
			}
			return pcsProfile(), true
		},
		func(string, time.Time) map[string]signal.Sample { return *samples },
		5*time.Second, 3*time.Second)
}

func request(id string) Request {
	return Request{ID: id, DeviceID: "pcs1", CommandID: "start", Actor: "operator"}
}

func TestSubmitWritesAndReadbackConfirms(t *testing.T) {
	writer := &fakeWriter{}
	samples := map[string]signal.Sample{}
	m := manager(writer, &samples)
	outcome, err := m.Submit(t0, request("r1"))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.State != Accepted || writer.calls != 1 {
		t.Fatalf("expected accepted after one write, got %+v calls=%d", outcome, writer.calls)
	}
	samples["run"] = signal.Sample{Input: "run", Value: signal.Flag(true), Quality: signal.Good, SourceTime: t0.Add(500 * time.Millisecond), ReceivedTime: t0.Add(500 * time.Millisecond)}
	m.Tick(t0.Add(time.Second))
	got, _ := m.Get("r1")
	if got.State != Confirmed {
		t.Fatalf("expected confirmed, got %+v", got)
	}
}

func TestSubmitIsIdempotent(t *testing.T) {
	writer := &fakeWriter{}
	samples := map[string]signal.Sample{}
	m := manager(writer, &samples)
	if _, err := m.Submit(t0, request("r1")); err != nil {
		t.Fatal(err)
	}
	outcome, err := m.Submit(t0.Add(time.Second), request("r1"))
	if err != nil {
		t.Fatal(err)
	}
	if writer.calls != 1 {
		t.Fatalf("duplicate submit wrote %d times", writer.calls)
	}
	if outcome.RequestID != "r1" {
		t.Fatalf("unexpected outcome %+v", outcome)
	}

	conflict := request("r1")
	conflict.CommandID = "stop"
	if _, err := m.Submit(t0, conflict); err == nil {
		t.Fatal("expected conflict for reused request id")
	}
}

func TestRequestIdentityBindsActorAndDevice(t *testing.T) {
	writer := &fakeWriter{}
	samples := map[string]signal.Sample{}
	m := manager(writer, &samples)
	if _, err := m.Submit(t0, request("scoped")); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"actor", "device"} {
		changed := request("scoped")
		if field == "actor" {
			changed.Actor = "other"
		} else {
			changed.DeviceID = "pcs2"
		}
		if _, err := m.Submit(t0, changed); err == nil {
			t.Errorf("reused request with different %s was accepted", field)
		}
	}
	if writer.calls != 1 {
		t.Fatalf("writes=%d", writer.calls)
	}
}

func TestReadbackDeadlineUsesObservationAndArrivalNotTick(t *testing.T) {
	for _, tc := range []struct {
		name               string
		observed, received time.Duration
		quality            signal.Quality
		want               State
	}{
		{"on_time_processed_later", 4 * time.Second, 4 * time.Second, signal.Good, Confirmed},
		{"late_observation", 6 * time.Second, 6 * time.Second, signal.Good, Unknown},
		{"late_arrival", 4 * time.Second, 6 * time.Second, signal.Good, Unknown},
		{"old_cache", -time.Second, 4 * time.Second, signal.Good, Unknown},
		{"clock_backwards_on_arrival", 4 * time.Second, 3 * time.Second, signal.Good, Unknown},
		{"missing_arrival", 4 * time.Second, 0, signal.Good, Unknown},
		{"invalid", 4 * time.Second, 4 * time.Second, signal.Invalid, Unknown},
		{"stale", 4 * time.Second, 4 * time.Second, signal.Stale, Unknown},
		{"future", 8 * time.Second, 8 * time.Second, signal.Good, Unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writer := &fakeWriter{}
			samples := map[string]signal.Sample{}
			m := manager(writer, &samples)
			if _, err := m.Submit(t0, request("deadline")); err != nil {
				t.Fatal(err)
			}
			samples["run"] = signal.Sample{Input: "run", Value: signal.Flag(true), Quality: tc.quality, SourceTime: t0.Add(tc.observed), ReceivedTime: t0.Add(tc.received)}
			m.Tick(t0.Add(6 * time.Second))
			got, _ := m.Get("deadline")
			if got.State != tc.want {
				t.Fatalf("state=%s want %s", got.State, tc.want)
			}
			m.Tick(t0.Add(10 * time.Second))
			if writer.calls != 1 {
				t.Fatalf("timeout retried physical action: %d", writer.calls)
			}
		})
	}
}

func TestDeclaredReadbackTolerance(t *testing.T) {
	for _, value := range []float64{20.2, 20.3} {
		writer := &fakeWriter{}
		samples := map[string]signal.Sample{}
		m := manager(writer, &samples)
		m.SetProfiles(func(string) (device.Profile, bool) {
			return device.Profile{ID: "tolerance", Points: []device.Point{{ID: "target", Kind: signal.Number}}, Commands: []device.Command{{ID: "set", ReadbackTolerance: 0.25, Parameters: []device.Parameter{{Name: "target", Point: "target", Kind: signal.Number, Default: 20}}}}}, true
		})
		_, err := m.Submit(t0, Request{ID: "tolerance", DeviceID: "pcs1", CommandID: "set"})
		if err != nil {
			t.Fatal(err)
		}
		samples["target"] = signal.Sample{Input: "target", Value: signal.Num(value), Quality: signal.Good, SourceTime: t0.Add(time.Second), ReceivedTime: t0.Add(time.Second)}
		m.Tick(t0.Add(time.Second))
		got, _ := m.Get("tolerance")
		if (got.State == Confirmed) != (value == 20.2) {
			t.Fatalf("readback %g: %s", value, got.State)
		}
	}
}

func TestDeclaredCommandSafetyRejectionIsNotADeviceFailure(t *testing.T) {
	writer := &fakeWriter{}
	samples := map[string]signal.Sample{}
	m := manager(writer, &samples)
	m.Guard = func(GuardRequest) safety.Verdict {
		return safety.Verdict{Decision: safety.DecisionRefused, Reason: "interlock"}
	}
	got, err := m.Submit(t0, request("rejected"))
	if err != nil || got.State != State("rejected") || writer.calls != 0 {
		t.Fatalf("rejection=%+v err=%v writes=%d", got, err, writer.calls)
	}
}

func TestDeclaredBooleanRegisterParameterRejectsNonBinaryValue(t *testing.T) {
	writer := &fakeWriter{}
	samples := map[string]signal.Sample{}
	m := manager(writer, &samples)
	m.SetProfiles(func(string) (device.Profile, bool) {
		return device.Profile{ID: "binary", Points: []device.Point{{ID: "target", Kind: signal.Number}}, Commands: []device.Command{{ID: "set", Parameters: []device.Parameter{{Name: "value", Point: "target", Kind: signal.Number, Binary: true}}}}}, true
	})
	if _, err := m.Submit(t0, Request{ID: "invalid-bool", DeviceID: "pcs1", CommandID: "set", Parameters: map[string]float64{"value": 2}}); err == nil || writer.calls != 0 {
		t.Fatalf("boolean declaration ignored: err=%v writes=%d", err, writer.calls)
	}
}

func TestJournalFailureAfterDispatchKeepsUnknownWithoutRetry(t *testing.T) {
	writer := &fakeWriter{}
	samples := map[string]signal.Sample{}
	m := manager(writer, &samples)
	saves := 0
	m.Persist = func(Outcome) error {
		saves++
		if saves > 1 {
			return errors.New("disk unavailable")
		}
		return nil
	}
	_, err := m.Submit(t0, request("journal-failed"))
	if err == nil {
		t.Fatal("post-dispatch persistence failure was hidden")
	}
	got, found := m.Get("journal-failed")
	if !found || got.State != Unknown {
		t.Fatalf("unpersisted outcome: %+v", got)
	}
	again, err := m.Submit(t0.Add(time.Second), request("journal-failed"))
	if err != nil || again.State != Unknown || writer.calls != 1 {
		t.Fatalf("ambiguous write retried: %+v %v writes=%d", again, err, writer.calls)
	}
}

func TestRetryParameterNamesCannotCollideWithSerializedValues(t *testing.T) {
	writer := &fakeWriter{}
	samples := map[string]signal.Sample{}
	m := manager(writer, &samples)
	m.SetProfiles(func(string) (device.Profile, bool) {
		return device.Profile{ID: "two-parameters", Points: []device.Point{{ID: "a", Kind: signal.Number}, {ID: "b", Kind: signal.Number}}, Commands: []device.Command{{ID: "set", Parameters: []device.Parameter{{Name: "a", Point: "a", Kind: signal.Number}, {Name: "b", Point: "b", Kind: signal.Number}}}}}, true
	})
	req := Request{ID: "encoded", DeviceID: "pcs1", CommandID: "set", Parameters: map[string]float64{"a": 1, "b": 2}}
	if _, err := m.Submit(t0, req); err != nil {
		t.Fatal(err)
	}
	req.Parameters = map[string]float64{"a=1;b": 2}
	if _, err := m.Submit(t0, req); err == nil {
		t.Fatal("different parameters collided with existing request fingerprint")
	}
	if writer.calls != 1 {
		t.Fatalf("writes=%d", writer.calls)
	}
}

func TestLegacyRequestIdentityRemainsReadableButRejectsAmbiguousKeys(t *testing.T) {
	writer := &fakeWriter{}
	samples := map[string]signal.Sample{}
	m := manager(writer, &samples)
	m.Preload([]Outcome{{RequestID: "legacy", DeviceID: "pcs1", CommandID: "set", Actor: "operator", State: Confirmed, Fingerprint: "command:set;a=1;b=2"}})
	req := Request{ID: "legacy", DeviceID: "pcs1", CommandID: "set", Actor: "operator", Parameters: map[string]float64{"a": 1, "b": 2}}
	if got, err := m.Submit(t0, req); err != nil || got.State != Confirmed {
		t.Fatalf("legacy retry lost result: %+v %v", got, err)
	}
	req.Parameters = map[string]float64{"a=1;b": 2}
	if _, err := m.Submit(t0, req); err == nil {
		t.Fatal("ambiguous legacy keys accepted")
	}
	if writer.calls != 0 {
		t.Fatal("legacy retry executed physical action")
	}
}

func TestRequestIdentityEncodingFailureDoesNotDispatch(t *testing.T) {
	writer := &fakeWriter{}
	samples := map[string]signal.Sample{}
	m := manager(writer, &samples)
	req := request("invalid-identity")
	req.Parameters = map[string]float64{"invalid": math.NaN()}
	if _, err := m.Submit(t0, req); err == nil {
		t.Fatal("encoding failure accepted")
	}
	if writer.calls != 0 {
		t.Fatal("invalid identity dispatched")
	}
}

func TestTimeoutBecomesUnknown(t *testing.T) {
	writer := &fakeWriter{}
	samples := map[string]signal.Sample{}
	m := manager(writer, &samples)
	if _, err := m.Submit(t0, request("r1")); err != nil {
		t.Fatal(err)
	}
	m.Tick(t0.Add(4 * time.Second))
	if got, _ := m.Get("r1"); got.State != Accepted {
		t.Fatalf("should still be executing before timeout, got %+v", got)
	}
	m.Tick(t0.Add(6 * time.Second))
	got, _ := m.Get("r1")
	if got.State != Unknown || !strings.Contains(got.Reason, "timeout") {
		t.Fatalf("expected unknown after timeout, got %+v", got)
	}
}

func TestWriteFailureIsFailed(t *testing.T) {
	writer := &fakeWriter{err: errors.New("port closed")}
	samples := map[string]signal.Sample{}
	m := manager(writer, &samples)
	outcome, err := m.Submit(t0, request("r1"))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.State != Failed || !strings.Contains(outcome.Reason, "port closed") {
		t.Fatalf("expected failed outcome, got %+v", outcome)
	}
}

func TestUnknownDeviceOrCommandRejected(t *testing.T) {
	writer := &fakeWriter{}
	samples := map[string]signal.Sample{}
	m := manager(writer, &samples)
	bad := Request{ID: "r1", DeviceID: "missing", CommandID: "start"}
	if _, err := m.Submit(t0, bad); err == nil {
		t.Fatal("expected unknown device error")
	}
	bad = Request{ID: "r2", DeviceID: "pcs1", CommandID: "missing"}
	if _, err := m.Submit(t0, bad); err == nil {
		t.Fatal("expected unknown command error")
	}
	if writer.calls != 0 {
		t.Fatalf("rejected commands must not write, calls=%d", writer.calls)
	}
}

func parameterProfile() device.Profile {
	return device.Profile{
		ID: "pcs", Vendor: "test", Model: "parameter",
		Points: []device.Point{{ID: "percent", Kind: signal.Number}},
		Commands: []device.Command{{
			ID: "set-percent", Name: "设置百分比", Timeout: 3 * time.Second,
			Parameters: []device.Parameter{{
				Name: "percent", Label: "百分比", Point: "percent", Kind: signal.Number,
				HasRange: true, Min: 0, Max: 100, Default: 10,
			}},
		}},
	}
}

func parameterManager(writer Dispatcher, samples *map[string]signal.Sample) *Manager {
	return New(writer,
		func(deviceID string) (device.Profile, bool) {
			if deviceID != "pcs1" {
				return device.Profile{}, false
			}
			return parameterProfile(), true
		},
		func(string, time.Time) map[string]signal.Sample { return *samples },
		5*time.Second, 3*time.Second)
}

func TestParameterizedCommandResolvesWritesAndReadback(t *testing.T) {
	writer := &fakeWriter{}
	samples := map[string]signal.Sample{}
	m := parameterManager(writer, &samples)
	req := Request{ID: "r1", DeviceID: "pcs1", CommandID: "set-percent", Actor: "operator", Parameters: map[string]float64{"percent": 42}}
	outcome, err := m.Submit(t0, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(writer.last) != 1 || writer.last[0].Point != "percent" || writer.last[0].Value.Num != 42 {
		t.Fatalf("writes = %+v", writer.last)
	}
	if len(outcome.Readback) != 1 || outcome.Readback[0].Value.Num != 42 {
		t.Fatalf("readback = %+v", outcome.Readback)
	}
	samples["percent"] = signal.Sample{Input: "percent", Value: signal.Num(42), Quality: signal.Good, SourceTime: t0.Add(500 * time.Millisecond), ReceivedTime: t0.Add(500 * time.Millisecond)}
	m.Tick(t0.Add(time.Second))
	if got, _ := m.Get("r1"); got.State != Confirmed {
		t.Fatalf("expected confirmed, got %+v", got)
	}
}

func TestParameterizedCommandValidatesRangeAndUnknownNames(t *testing.T) {
	writer := &fakeWriter{}
	samples := map[string]signal.Sample{}
	m := parameterManager(writer, &samples)
	req := Request{ID: "r1", DeviceID: "pcs1", CommandID: "set-percent", Parameters: map[string]float64{"percent": 101}}
	if _, err := m.Submit(t0, req); err == nil {
		t.Fatal("expected range error")
	}
	req.Parameters = map[string]float64{"unknown": 1}
	if _, err := m.Submit(t0, req); err == nil {
		t.Fatal("expected unknown parameter error")
	}
	// Missing parameter uses the default and confirms against that value.
	req.Parameters = nil
	if _, err := m.Submit(t0, req); err != nil {
		t.Fatal(err)
	}
	if len(writer.last) != 1 || writer.last[0].Value.Num != 10 {
		t.Fatalf("default writes = %+v", writer.last)
	}
}

func pointRequest(id string, value float64) Request {
	return Request{ID: id, DeviceID: "pcs1", Point: "power", Value: signal.Num(value), Actor: "operator", SafeFallback: true}
}

func TestSubmitGuardRefusesWithoutWriting(t *testing.T) {
	writer := &fakeWriter{}
	samples := map[string]signal.Sample{}
	m := manager(writer, &samples)
	m.Guard = func(request GuardRequest) safety.Verdict {
		return safety.Verdict{Decision: "refused", Reason: "manual_takeover"}
	}
	outcome, err := m.Submit(t0, pointRequest("blocked", 40))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.State != Failed || outcome.Reason != "manual_takeover" || writer.calls != 0 {
		t.Fatalf("refused command = %+v calls=%d", outcome, writer.calls)
	}
}

func TestSubmitGuardRefusesUnknownDecisionWithoutWriting(t *testing.T) {
	writer := &fakeWriter{}
	samples := map[string]signal.Sample{}
	m := manager(writer, &samples)
	m.Guard = func(request GuardRequest) safety.Verdict {
		return safety.Verdict{Decision: "permit", Value: 90}
	}
	outcome, err := m.Submit(t0, pointRequest("unknown-decision", 90))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.State != Failed || writer.calls != 0 {
		t.Fatalf("unknown decision wrote a command: %+v calls=%d", outcome, writer.calls)
	}
	if !strings.Contains(outcome.Reason, "unknown safety decision") {
		t.Fatalf("refusal reason = %q, want a diagnostic", outcome.Reason)
	}
}

func TestSubmitGuardClampsBeforeWriting(t *testing.T) {
	writer := &fakeWriter{}
	samples := map[string]signal.Sample{}
	m := manager(writer, &samples)
	m.Guard = func(request GuardRequest) safety.Verdict {
		return safety.Verdict{Decision: "clamped", Value: 60, Reason: "clamped to available discharge power 60"}
	}
	outcome, err := m.Submit(t0, pointRequest("clamped", 90))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.State != Accepted || len(writer.last) != 1 || writer.last[0].Value.Num != 60 {
		t.Fatalf("clamped command = %+v writes=%+v", outcome, writer.last)
	}
	samples["power"] = signal.Sample{Input: "power", Value: signal.Num(60), Quality: signal.Good, SourceTime: t0.Add(time.Second), ReceivedTime: t0.Add(time.Second)}
	m.Tick(t0.Add(time.Second))
	confirmed, _ := m.Get("clamped")
	if confirmed.State != Confirmed {
		t.Fatalf("clamped readback did not confirm: %+v", confirmed)
	}
	if _, err := m.Submit(t0.Add(time.Second), pointRequest("clamped", 90)); err != nil {
		t.Fatal(err)
	}
	if writer.calls != 1 {
		t.Fatalf("duplicate submit wrote %d times", writer.calls)
	}
}

func TestSubmitGuardEvaluatesEveryNamedWrite(t *testing.T) {
	writer := &fakeWriter{}
	samples := map[string]signal.Sample{}
	m := manager(writer, &samples)
	profile := pcsProfile()
	profile.Commands[0].Writes = []device.Write{
		{Point: "run", Value: signal.Flag(true)},
		{Point: "power", Value: signal.Num(90)},
	}
	m.Profiles = func(string) (device.Profile, bool) { return profile, true }
	m.Guard = func(request GuardRequest) safety.Verdict {
		if request.Point == "power" {
			return safety.Verdict{Decision: "clamped", Value: 60, Reason: "power limit"}
		}
		return safety.Verdict{Decision: "allowed"}
	}
	if _, err := m.Submit(t0, request("multi")); err != nil {
		t.Fatal(err)
	}
	if len(writer.last) != 2 || writer.last[1].Value.Num != 60 {
		t.Fatalf("named writes bypassed guard: %+v", writer.last)
	}
}

func TestGuardedNoReadbackRemainsSubmitted(t *testing.T) {
	writer := &fakeWriter{}
	samples := map[string]signal.Sample{}
	m := manager(writer, &samples)
	request := pointRequest("submitted", 50)
	request.NoReadback = true
	outcome, err := m.Submit(t0, request)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.State != Submitted {
		t.Fatalf("no-readback outcome = %+v", outcome)
	}
}

func TestPointWriteRequiresFreshReadback(t *testing.T) {
	writer := &fakeWriter{}
	samples := map[string]signal.Sample{
		"power": {Input: "power", Value: signal.Num(40), Quality: signal.Good, SourceTime: t0, ReceivedTime: t0},
	}
	m := manager(writer, &samples)
	if _, err := m.Submit(t0, pointRequest("pw1", 40)); err != nil {
		t.Fatal(err)
	}
	m.Tick(t0.Add(time.Second))
	if got, _ := m.Get("pw1"); got.State != Accepted {
		t.Fatalf("old matching sample must not confirm, got %+v", got)
	}
	samples["power"] = signal.Sample{Input: "power", Value: signal.Num(40), Quality: signal.Good, SourceTime: t0.Add(2 * time.Second), ReceivedTime: t0.Add(2 * time.Second)}
	m.Tick(t0.Add(2 * time.Second))
	if got, _ := m.Get("pw1"); got.State != Confirmed || len(got.Observed) != 1 {
		t.Fatalf("fresh sample must confirm with observation, got %+v", got)
	}
}

func TestPointWriteDuplicateConflictAndIdempotency(t *testing.T) {
	writer := &fakeWriter{}
	samples := map[string]signal.Sample{}
	m := manager(writer, &samples)
	if _, err := m.Submit(t0, pointRequest("pw1", 30)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Submit(t0, pointRequest("pw1", 30)); err != nil {
		t.Fatal(err)
	}
	if writer.calls != 1 {
		t.Fatalf("duplicate same-value submit wrote %d times", writer.calls)
	}
	if _, err := m.Submit(t0, pointRequest("pw1", 90)); err == nil {
		t.Fatal("same request id with a different value must conflict")
	}
	if writer.calls != 1 {
		t.Fatalf("conflicting submit must not write, calls=%d", writer.calls)
	}
}

// 超时只回退一次：没有已确认值时回 0，回退请求自身超时不再回退。
func TestPointWriteTimeoutFallsBackOnce(t *testing.T) {
	writer := &fakeWriter{}
	samples := map[string]signal.Sample{}
	m := manager(writer, &samples)
	if _, err := m.Submit(t0, pointRequest("pw1", 60)); err != nil {
		t.Fatal(err)
	}
	m.Tick(t0.Add(4 * time.Second))
	original, _ := m.Get("pw1")
	if original.State != Unknown {
		t.Fatalf("expected unknown after timeout, got %+v", original)
	}
	fallback, ok := m.Get("pw1-fallback")
	if !ok || fallback.FallbackOf != "pw1" || fallback.State != Accepted {
		t.Fatalf("expected one fallback request, got %+v ok=%v", fallback, ok)
	}
	if writer.calls != 2 || writer.last[0].Value.Num != 0 {
		t.Fatalf("fallback should write 0 once, calls=%d writes=%+v", writer.calls, writer.last)
	}
	m.Tick(t0.Add(10 * time.Second))
	if _, ok := m.Get("pw1-fallback-fallback"); ok {
		t.Fatal("fallback must never fall back again")
	}
	if writer.calls != 2 {
		t.Fatalf("fallback failure must not write again, calls=%d", writer.calls)
	}
}

// 已确认过的值作为回退目标；被取代的请求不得再触发回退。
func TestFallbackUsesLastConfirmedAndSupersede(t *testing.T) {
	writer := &fakeWriter{}
	samples := map[string]signal.Sample{}
	m := manager(writer, &samples)
	if _, err := m.Submit(t0, pointRequest("pw1", 50)); err != nil {
		t.Fatal(err)
	}
	samples["power"] = signal.Sample{Input: "power", Value: signal.Num(50), Quality: signal.Good, SourceTime: t0.Add(500 * time.Millisecond), ReceivedTime: t0.Add(500 * time.Millisecond)}
	m.Tick(t0.Add(time.Second))
	if value, ok := m.LastConfirmed("pcs1", "power"); !ok || value != 50 {
		t.Fatalf("last confirmed = %v ok=%v", value, ok)
	}
	if _, err := m.Submit(t0.Add(2*time.Second), pointRequest("pw2", 90)); err != nil {
		t.Fatal(err)
	}
	m.Tick(t0.Add(6 * time.Second))
	fallback, ok := m.Get("pw2-fallback")
	if !ok {
		t.Fatal("expected fallback to last confirmed value")
	}
	if writer.last[0].Value.Num != 50 {
		t.Fatalf("fallback value = %v, want 50", writer.last[0].Value.Num)
	}
	_ = fallback

	if _, err := m.Submit(t0.Add(7*time.Second), pointRequest("pw3", 70)); err != nil {
		t.Fatal(err)
	}
	if count := m.Supersede("pcs1"); count == 0 {
		t.Fatal("expected a nonterminal request to supersede")
	}
	before := writer.calls
	m.Tick(t0.Add(20 * time.Second))
	if got, _ := m.Get("pw3"); got.State != Superseded {
		t.Fatalf("superseded request changed state: %+v", got)
	}
	if _, ok := m.Get("pw3-fallback"); ok {
		t.Fatal("superseded request must not fall back")
	}
	if writer.calls != before {
		t.Fatalf("supersede must not write, calls %d -> %d", before, writer.calls)
	}
}

func TestFailWriteMarksAcceptedRequestFailed(t *testing.T) {
	manager := New(&fakeWriter{}, nil, func(string, time.Time) map[string]signal.Sample {
		return nil
	}, time.Minute, 5*time.Second)
	outcome, err := manager.Submit(t0, Request{ID: "r1", DeviceID: "d", Point: "cmd", Value: signal.Num(1)})
	if err != nil || outcome.State != Accepted {
		t.Fatalf("submit = %+v, %v", outcome, err)
	}
	manager.FailWrite(t0.Add(time.Second), "r1", "device refused write: SERVER DEVICE FAILURE")
	got, _ := manager.Get("r1")
	if got.State != Failed {
		t.Fatalf("state = %s, want failed", got.State)
	}
	if !strings.Contains(got.Reason, "refused") {
		t.Fatalf("reason = %q", got.Reason)
	}
	if !got.UpdatedAt.Equal(t0.Add(time.Second)) {
		t.Fatalf("updated = %v", got.UpdatedAt)
	}
	// A terminal outcome is never rewritten by a late refusal.
	manager.FailWrite(t0.Add(2*time.Second), "r1", "second")
	again, _ := manager.Get("r1")
	if again.Reason != got.Reason {
		t.Fatalf("terminal outcome changed: %q", again.Reason)
	}
}

func TestFailWriteIgnoresCallerOwnedRequests(t *testing.T) {
	manager := New(&fakeWriter{}, nil, func(string, time.Time) map[string]signal.Sample {
		return nil
	}, time.Minute, 5*time.Second)
	if _, err := manager.Track(t0, Request{ID: "manual", DeviceID: "d", Point: "cmd", Value: signal.Num(1)}); err != nil {
		t.Fatal(err)
	}
	manager.FailWrite(t0.Add(time.Second), "manual", "device refused write")
	got, _ := manager.Get("manual")
	if got.State != Accepted || got.Reason != "" {
		t.Fatalf("caller-owned outcome changed: %+v", got)
	}
}
