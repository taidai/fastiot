package appapi

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fastiot/internal/ems/command"
	"fastiot/internal/ems/device"
	"fastiot/internal/ems/signal"
)

type nullDispatcher struct{}

func (nullDispatcher) Write(string, string, []device.Write) error { return nil }

func testManager() *command.Manager {
	return command.New(
		nullDispatcher{},
		func(string) (device.Profile, bool) { return device.Profile{}, false },
		func(string, time.Time) map[string]signal.Sample { return nil },
		15*time.Second, 5*time.Second,
	)
}

func TestDurableCommandIdentityOutlivesDisplayCache(t *testing.T) {
	dir := t.TempDir()
	first := testManager()
	writer := &guardDispatcher{}
	first.Writer = writer
	WireCommandHistory(dir, first)
	req := command.Request{ID: "old", DeviceID: "station1", Point: "target", Value: signal.Num(20), Actor: "operator", Source: "manual", NoReadback: true}
	if _, err := first.Submit(time.Now(), req); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2001; i++ {
		id := fmt.Sprintf("filler-%d", i)
		if _, err := first.Track(time.Now(), command.Request{ID: id, DeviceID: "fixture", Point: "cache", Value: signal.Num(0), Actor: "operator"}); err != nil {
			t.Fatal(err)
		}
		first.SetState(time.Now(), id, command.Confirmed, "")
	}
	if _, err := first.Submit(time.Now(), req); err != nil {
		t.Fatal(err)
	}
	if writer.calls != 1 {
		t.Errorf("cache eviction reexecuted action: %d", writer.calls)
	}
	second := testManager()
	second.Writer = writer
	WireCommandHistory(dir, second)
	if _, err := second.Submit(time.Now(), req); err != nil {
		t.Fatal(err)
	}
	if writer.calls != 1 {
		t.Errorf("restart preload boundary reexecuted action: %d", writer.calls)
	}
	req.Actor = "other"
	if _, err := second.Submit(time.Now(), req); err == nil {
		t.Error("old identity reused by different actor")
	}
	// A broken journal must not result in an unrecorded physical write.
	if err := os.Rename(filepath.Join(dir, commandLogFile), filepath.Join(dir, "saved-log")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, commandLogFile), 0700); err != nil {
		t.Fatal(err)
	}
	req.ID = "unpersisted"
	if _, err := second.Submit(time.Now(), req); err == nil {
		t.Error("storage failure accepted command")
	}
	if writer.calls != 1 {
		t.Errorf("storage failure wrote device: %d", writer.calls)
	}
}

func TestCommandHistorySurvivesRestart(t *testing.T) {
	dataDir := t.TempDir()
	first := testManager()
	WireCommandHistory(dataDir, first)
	started := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	first.Persist(command.Outcome{
		RequestID: "req-1", DeviceID: "ch1/1", CommandID: "on", Actor: "operator",
		State: command.Confirmed, StartedAt: started, UpdatedAt: started.Add(2 * time.Second),
	})
	first.Persist(command.Outcome{
		RequestID: "req-2", DeviceID: "ch1/1", CommandID: "off", Actor: "operator",
		State: command.Executing, StartedAt: started.Add(time.Minute), UpdatedAt: started.Add(time.Minute),
	})

	second := testManager()
	WireCommandHistory(dataDir, second)
	outcomes := second.List()
	if len(outcomes) != 2 {
		t.Fatalf("expected 2 restored outcomes, got %d", len(outcomes))
	}
	if outcomes[0].RequestID != "req-1" || outcomes[0].State != command.Confirmed || !outcomes[0].Restored {
		t.Fatalf("confirmed outcome not restored: %+v", outcomes[0])
	}
	if outcomes[1].State != command.Unknown || outcomes[1].Reason == "" {
		t.Fatalf("in-flight outcome must restore as unknown with a reason: %+v", outcomes[1])
	}
}

func TestCommandHistoryKeepsLastStatePerRequest(t *testing.T) {
	dataDir := t.TempDir()
	first := testManager()
	WireCommandHistory(dataDir, first)
	started := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	first.Persist(command.Outcome{RequestID: "req-1", DeviceID: "d", CommandID: "c", State: command.Executing, StartedAt: started, UpdatedAt: started})
	first.Persist(command.Outcome{RequestID: "req-1", DeviceID: "d", CommandID: "c", State: command.Failed, Reason: "write failed", StartedAt: started, UpdatedAt: started.Add(time.Second)})

	second := testManager()
	WireCommandHistory(dataDir, second)
	outcomes := second.List()
	if len(outcomes) != 1 || outcomes[0].State != command.Failed || outcomes[0].Reason != "write failed" {
		t.Fatalf("last state must win: %+v", outcomes)
	}
}
