package alarm

import (
	"encoding/json"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func raise(t *testing.T, engine *Engine, at time.Time, key, detail string) Alarm {
	t.Helper()
	alarm, err := engine.Raise(at, key, Critical, "设备通信中断", detail, "d1", "temp")
	if err != nil {
		t.Fatalf("raise: %v", err)
	}
	return alarm
}

func TestRapidRecurrenceCreatesIndependentOccurrence(t *testing.T) {
	engine := New()
	raised := 0
	engine.OnChange = func(_ Alarm, transition string) {
		if transition == "raised" {
			raised++
		}
	}
	first := raise(t, engine, t0, "d1:offline", "timeout")
	if _, err := engine.AcknowledgeWithNote(t0.Add(time.Second), first.ID, "operator", "已接手"); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Clear(t0.Add(2*time.Second), first.Key); err != nil {
		t.Fatal(err)
	}
	again := raise(t, engine, t0.Add(3*time.Second), first.Key, "timeout")
	if again.ID == first.ID || again.AckedBy != "" || again.AckNote != "" || again.State != Active {
		t.Fatalf("recurrence reused the old occurrence or handling: %+v", again)
	}
	old, ok := engine.Get(first.ID)
	if !ok || old.Active || old.State != Resolved || old.AckedBy != "operator" || old.AckNote != "已接手" || !old.CreatedAt.Equal(t0) {
		t.Fatalf("old occurrence lost: %+v", old)
	}
	if len(engine.List()) != 2 || engine.Active() != 1 || raised != 2 {
		t.Fatal("recurrence or required raised event was suppressed")
	}
	if _, err := engine.Close(t0.Add(4*time.Second), first.ID); err != nil {
		t.Fatal(err)
	}
	current, _ := engine.Get(again.ID)
	if !current.Active || current.State != Active {
		t.Fatal("old occurrence operation changed the new occurrence")
	}
}

func TestClosedOccurrenceCannotChangeConditionMaintenance(t *testing.T) {
	engine := New()
	first := raise(t, engine, t0, "k", "timeout")
	if _, err := engine.Clear(t0.Add(time.Second), first.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Close(t0.Add(2*time.Second), first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Shelve(t0.Add(3*time.Second), first.ID, "old", "operator", t0.Add(time.Hour)); err == nil {
		t.Fatal("closed occurrence installed maintenance for a future occurrence")
	}
	if _, err := engine.Release(t0.Add(3*time.Second), first.ID); err == nil {
		t.Fatal("closed occurrence released condition maintenance")
	}
	next := raise(t, engine, t0.Add(4*time.Second), first.Key, "timeout")
	if !next.Active || next.ID == first.ID {
		t.Fatal("old handling suppressed a new occurrence")
	}
}

func TestOccurrenceKeepsLifecycleTimesAndRevision(t *testing.T) {
	engine := New()
	first := raise(t, engine, t0, "k", "timeout")
	if _, err := engine.AcknowledgeWithNote(t0.Add(time.Second), first.ID, "operator", "已接手"); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Clear(t0.Add(2*time.Second), first.Key); err != nil {
		t.Fatal(err)
	}
	closed, err := engine.Close(t0.Add(3*time.Second), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(closed)
	var wire struct {
		AckedAt    time.Time `json:"acked_at"`
		ResolvedAt time.Time `json:"resolved_at"`
		ClosedAt   time.Time `json:"closed_at"`
		Revision   uint64    `json:"revision"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if !wire.AckedAt.Equal(t0.Add(time.Second)) || !wire.ResolvedAt.Equal(t0.Add(2*time.Second)) || !wire.ClosedAt.Equal(t0.Add(3*time.Second)) || wire.Revision != 4 {
		t.Fatalf("lifecycle evidence overwritten: %s", encoded)
	}
}

func TestContinuousOccurrenceTracksTriggerAndSeverity(t *testing.T) {
	engine := New()
	var events []Alarm
	var checkpoints []Alarm
	engine.OnChange = func(value Alarm, _ string) { events = append(events, value) }
	engine.OnCheckpoint = func(value Alarm) { checkpoints = append(checkpoints, value) }
	first, _ := engine.Raise(t0, "temperature:d1", Warning, "温度高", "46", "d1", "p1")
	repeated, _ := engine.Raise(t0.Add(time.Second), first.Key, Warning, "温度高", "46", "d1", "p1")
	if repeated.ID != first.ID || !repeated.CreatedAt.Equal(t0) || !repeated.LastRaisedAt.Equal(t0.Add(time.Second)) || repeated.Count != 2 {
		t.Fatalf("continuous trigger evidence lost: %+v", repeated)
	}
	if len(events) != 1 {
		t.Fatal("unchanged condition repeated an occurrence event")
	}
	if len(checkpoints) != 1 || checkpoints[0].ID != first.ID || checkpoints[0].Count != 2 {
		t.Fatal("continuous count checkpoint missing")
	}
	duplicate, _ := engine.Raise(t0.Add(time.Second), first.Key, Warning, "温度高", "46", "d1", "p1")
	if duplicate.Count != 2 || len(events) != 1 || len(checkpoints) != 1 {
		t.Fatal("duplicate observation incremented or notified")
	}
	changed, _ := engine.Raise(t0.Add(2*time.Second), first.Key, Critical, "温度高", "60", "d1", "p1")
	if changed.ID != first.ID || !changed.CreatedAt.Equal(t0) || len(events) != 2 || events[1].Severity != Critical {
		t.Fatalf("severity change was not traceable: %+v events=%+v", changed, events)
	}
}

func TestRaiseDeduplicatesLiveCondition(t *testing.T) {
	engine := New()
	first := raise(t, engine, t0, "d1:offline", "timeout")
	second := raise(t, engine, t0.Add(time.Second), "d1:offline", "timeout")
	if engine.Active() != 1 || len(engine.List()) != 1 {
		t.Fatalf("expected one alarm, got %d", len(engine.List()))
	}
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Fatal("repeated condition must not reset creation time")
	}
	changed := raise(t, engine, t0.Add(2*time.Second), "d1:offline", "no response for 30s")
	if changed.Detail != "no response for 30s" {
		t.Fatalf("detail not updated: %+v", changed)
	}
}

func TestClearResolvesAndRecurrenceReactivates(t *testing.T) {
	engine := New()
	raise(t, engine, t0, "d1:offline", "timeout")
	cleared, err := engine.Clear(t0.Add(time.Minute), "d1:offline")
	if err != nil || cleared.Active || cleared.State != Resolved {
		t.Fatalf("unexpected clear result %+v err=%v", cleared, err)
	}
	if engine.Active() != 0 {
		t.Fatal("resolved alarm must not count as active")
	}
	again := raise(t, engine, t0.Add(2*time.Minute), "d1:offline", "timeout")
	if !again.Active || again.State != Active || !again.CreatedAt.Equal(t0.Add(2*time.Minute)) {
		t.Fatalf("recurrence not reactivated: %+v", again)
	}
}

func TestAcknowledgeAndCloseRules(t *testing.T) {
	engine := New()
	first := raise(t, engine, t0, "d1:offline", "timeout")
	if _, err := engine.Close(t0, first.ID); err == nil {
		t.Fatal("active alarms must not be closed directly")
	}
	acked, err := engine.Acknowledge(t0.Add(time.Second), first.ID, "operator")
	if err != nil || acked.State != Acknowledged || acked.AckedBy != "operator" || !acked.Active {
		t.Fatalf("unexpected ack %+v err=%v", acked, err)
	}
	if _, err := engine.Acknowledge(t0.Add(2*time.Second), first.ID, "operator"); err == nil {
		t.Fatal("acknowledging twice must fail")
	}
	if _, err := engine.Clear(t0.Add(3*time.Second), first.Key); err != nil {
		t.Fatal(err)
	}
	closed, err := engine.Close(t0.Add(4*time.Second), first.ID)
	if err != nil || closed.State != Closed || closed.Active {
		t.Fatalf("unexpected close %+v err=%v", closed, err)
	}
	if _, err := engine.Acknowledge(t0.Add(5*time.Second), first.ID, "operator"); err == nil {
		t.Fatal("closed alarm must not be acknowledged")
	}
}

func TestListPutsActiveFirst(t *testing.T) {
	engine := New()
	raise(t, engine, t0, "a", "first")
	if _, err := engine.Clear(t0.Add(time.Second), "a"); err != nil {
		t.Fatal(err)
	}
	raise(t, engine, t0.Add(2*time.Second), "b", "second")
	list := engine.List()
	if len(list) != 2 || list[0].Key != "b" || list[1].Key != "a" {
		t.Fatalf("order = %+v", list)
	}
}

func TestInvalidInputsAreRejected(t *testing.T) {
	engine := New()
	if _, err := engine.Raise(t0, "", Critical, "t", "d", "", ""); err == nil {
		t.Fatal("empty key must be rejected")
	}
	if _, err := engine.Raise(t0, "k", "urgent", "t", "d", "", ""); err == nil {
		t.Fatal("invalid severity must be rejected")
	}
	if _, err := engine.Acknowledge(t0, "missing", "user"); err == nil {
		t.Fatal("unknown alarm must be rejected")
	}
}

func TestMaintenanceSuppressesRaiseUntilReleased(t *testing.T) {
	engine := New()
	now := time.Now()
	first, err := engine.Raise(now, "k", Warning, "温度高", "45", "d", "p")
	if err != nil {
		t.Fatal(err)
	}
	until := now.Add(time.Hour)
	if _, err := engine.Shelve(now, first.ID, "检修", "admin", until); err != nil {
		t.Fatal(err)
	}
	current, _ := engine.Get(first.ID)
	if current.MaintenanceUntil.IsZero() || current.MaintenanceReason != "检修" || current.MaintainedBy != "admin" {
		t.Fatalf("alarm = %+v", current)
	}
	if _, err := engine.Clear(now.Add(time.Minute), first.Key); err != nil {
		t.Fatal(err)
	}
	suppressed, err := engine.Raise(now.Add(2*time.Minute), "k", Warning, "温度高", "46", "d", "p")
	if err != nil || suppressed.Active {
		t.Fatalf("maintenance did not follow condition identity: %+v %v", suppressed, err)
	}
	again, err := engine.Raise(until.Add(time.Minute), "k", Warning, "温度高", "47", "d", "p")
	if err != nil || !again.Active || again.ID == first.ID {
		t.Fatalf("maintenance expiry did not permit a new occurrence: %+v %v", again, err)
	}
	if _, err := engine.Release(until.Add(2*time.Minute), first.ID); err == nil {
		t.Fatal("superseded occurrence altered maintenance")
	}
	if _, err := engine.Release(until.Add(2*time.Minute), again.ID); err != nil {
		t.Fatal(err)
	}
	current, _ = engine.Get(again.ID)
	if !current.MaintenanceUntil.IsZero() || current.MaintenanceReason != "" {
		t.Fatalf("maintenance not released: %+v", current)
	}
}

func TestMaintenanceExportImport(t *testing.T) {
	engine := New()
	now := time.Now()
	first, err := engine.Raise(now, "future", Warning, "测试", "detail", "d", "p")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Shelve(now, first.ID, "检修", "admin", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	exported := engine.ExportMaintenance()
	restored := New()
	restored.ImportMaintenance(exported)
	suppressed, err := restored.Raise(now.Add(time.Minute), "future", Warning, "测试", "detail", "d", "p")
	if err != nil {
		t.Fatal(err)
	}
	if suppressed.Active || len(restored.List()) != 0 {
		t.Fatalf("imported maintenance did not suppress raise: %+v", suppressed)
	}
}

func TestAcknowledgeNote(t *testing.T) {
	engine := New()
	at := time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC)
	first, err := engine.Raise(at, "k", Warning, "温度高", "46", "d", "p")
	if err != nil {
		t.Fatal(err)
	}
	acked, err := engine.AcknowledgeWithNote(at.Add(time.Second), first.ID, "operator", "已联系现场")
	if err != nil {
		t.Fatal(err)
	}
	if acked.AckNote != "已联系现场" {
		t.Fatalf("ack note = %q", acked.AckNote)
	}
}
