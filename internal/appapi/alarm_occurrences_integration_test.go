package appapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fastiot/internal/ems/alarm"
	"fastiot/internal/ems/alarmtype"
	"fastiot/internal/ems/alarmwatch"
	"fastiot/internal/ems/bridge"
	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/ems/signal"
	"fastiot/internal/emsapi"
	"github.com/simpleiot/simpleiot/data"
)

func TestAlarmObservationsPreserveRecoveryBetweenScans(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, 15*time.Second, time.Second, 100)
	defer service.Close()
	source := bridge.NewNodeSource(h.nc)
	api := emsapi.New(service, 15*time.Second)
	api.Buses = source.Buses
	h.SetEMS(api)
	f := &pointConfigFixture{base: base, token: token}
	root := snapshotRoot(t, base, token)
	busID := f.createBus(t, "isolated A")
	point := f.createPoint(t, busID, ModbusIOInput{Description: "A temperature", ServerID: 1, Address: 10, IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1, ReadOnly: true, Units: "C"})
	config := alarmtype.Config{Code: "HIGH", Name: "A high", Severity: "warning", Trigger: "gt", PointID: point, Value: 40, Confirm: "manual", RecordRecovery: true, Enabled: true}
	status, body := call(t, http.MethodPut, base+"/alarm-types?root="+root, token, map[string]any{"root": root, "type": config}, nil)
	if status != 200 {
		t.Fatal(body)
	}
	watcher := alarmwatch.NewWatcher(h.nc, service.Alarms)
	watcher.State = service.State
	watcher.Buses = source.Buses
	watcher.MaxAge = service.MaxAge
	api.BeginAlarmConfiguration = watcher.BeginConfiguration
	service.OnObservations = watcher.Observe
	if err := watcher.Scan(time.Now()); err != nil {
		t.Fatal(err)
	}
	configs, err := source.Buses()
	if err != nil {
		t.Fatal(err)
	}
	bus := &calculationReadBus{pointConfigBus: &pointConfigBus{}, values: map[byte]uint16{1: 45}, fail: map[byte]bool{}}
	ch, err := channel.New(configs[0].ChannelID, bus, configs[0].Devices)
	if err != nil {
		t.Fatal(err)
	}
	cycle := func(value uint16, fail bool) {
		bus.values[1] = value
		bus.fail[1] = fail
		results, _ := ch.RunOnce(context.Background(), time.Now)
		service.Observe(time.Now(), results)
	}
	list := func() []alarm.Alarm {
		t.Helper()
		var out struct {
			Alarms []alarm.Alarm `json:"alarms"`
		}
		status, body := call(t, http.MethodGet, base+"/ems/alarms", token, nil, &out)
		if status != 200 {
			t.Fatal(body)
		}
		selected := []alarm.Alarm{}
		for _, a := range out.Alarms {
			if a.Title == "A high" {
				selected = append(selected, a)
			}
		}
		return selected
	}
	cycle(45, false)
	first := list()
	if len(first) != 1 || !first[0].Active || first[0].PointID != point {
		t.Fatalf("actual observation did not raise correctly: %+v", first)
	}
	status, body = call(t, http.MethodPost, base+"/ems/alarms/"+first[0].ID+"/ack", token, map[string]any{"note": "接手A"}, nil)
	if status != 200 {
		t.Fatal(body)
	}
	cycle(0, true)
	afterFailure := list()
	if len(afterFailure) != 1 || !afterFailure[0].Active {
		t.Fatal("failed read fabricated recovery")
	}
	var unknown struct {
		Alarms []struct {
			ID         string `json:"id"`
			InputState string `json:"input_state"`
		} `json:"alarms"`
	}
	status, body = call(t, http.MethodGet, base+"/ems/alarms", token, nil, &unknown)
	marked := false
	for _, record := range unknown.Alarms {
		if record.ID == first[0].ID && record.InputState == "unknown" {
			marked = true
		}
	}
	if status != 200 || !marked {
		t.Fatalf("unavailable input was not explained: %d %s", status, body)
	}
	cycle(20, false)
	var recoveredAt time.Time
	for _, value := range service.State.Snapshot(time.Now(), service.MaxAge) {
		if value.PointID == point {
			recoveredAt = value.SourceTime
		}
	}
	cycle(46, false)
	after := list()
	if len(after) != 2 {
		t.Fatalf("normal-to-abnormal changes between scans were lost: %+v", after)
	}
	active := 0
	for _, a := range after {
		if a.Active {
			active++
			if a.ID == first[0].ID || a.AckedBy != "" {
				t.Fatal("new occurrence reused old handling")
			}
		} else if a.ID != first[0].ID || a.State != alarm.Resolved || a.AckNote != "接手A" {
			t.Fatal("original recovered occurrence changed")
		}
	}
	if active != 1 || len(bus.written()) != 0 {
		t.Fatal("invalid active count or protocol write")
	}
	var evidence struct {
		Events []struct {
			Transition string `json:"transition"`
			Input      *struct {
				Value       float64   `json:"value"`
				SourceTime  time.Time `json:"source_time"`
				Quality     string    `json:"quality"`
				ConfigStamp string    `json:"config_stamp"`
			} `json:"input"`
		} `json:"events"`
	}
	status, body = call(t, http.MethodGet, base+"/alarm-events?occurrence_id="+first[0].ID, token, nil, &evidence)
	matched := false
	for _, event := range evidence.Events {
		if event.Transition == "resolved" && event.Input != nil && event.Input.Value == 20 && event.Input.SourceTime.Equal(recoveredAt) && event.Input.Quality == "good" && event.Input.ConfigStamp == configs[0].Devices[0].Points[0].ConfigStamp {
			matched = true
		}
	}
	if status != 200 || !matched {
		t.Fatalf("original recovery observation was not preserved: %d %s", status, body)
	}
	status, body = call(t, http.MethodPut, base+"/modbus/io/"+point, token, ModbusIOInput{Description: "A temperature", ServerID: 1, Address: 11, IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1, ReadOnly: true, Units: "C"}, nil)
	if status != 200 {
		t.Fatal(body)
	}
	cycle(20, false)
	stillActive := false
	for _, record := range list() {
		if record.ID != first[0].ID && record.Active {
			stillActive = true
		}
	}
	if !stillActive {
		t.Fatal("old channel configuration fabricated recovery after an address edit")
	}
	config.Enabled = false
	status, body = call(t, http.MethodPut, base+"/alarm-types?root="+root, token, map[string]any{"root": root, "type": config}, nil)
	if status != 200 {
		t.Fatal(body)
	}
	if err := watcher.Scan(time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, record := range list() {
		if record.ID != first[0].ID && (record.Active || !record.ResolvedAt.IsZero() || record.InputReason != "definition_disabled") {
			t.Fatalf("configuration retirement pretended to recover the device: %+v", record)
		}
	}
}

func TestAlarmOccurrencesSurviveRuntimeRestart(t *testing.T) {
	dir, password := t.TempDir(), data.NewID()
	base, token, h, stop := startInputServer(t, dir, password)
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	h.SetEMS(emsapi.New(service, time.Minute))
	root := snapshotRoot(t, base, token)
	now := time.Now()
	first, _ := service.Alarms.Raise(now, "persistent-condition", alarm.Warning, "Restart alarm", "high", root, "")
	service.Alarms.Raise(now.Add(time.Millisecond), first.Key, alarm.Warning, first.Title, "high", root, "")
	status, body := call(t, http.MethodPost, base+"/ems/alarms/"+first.ID+"/ack", token, map[string]any{"note": "still investigating"}, nil)
	if status != 200 {
		t.Fatal(body)
	}
	// A later identical abnormal observation must persist its count as well.
	service.Alarms.Raise(now.Add(2*time.Millisecond), first.Key, alarm.Warning, first.Title, "high", root, "")
	status, body = call(t, http.MethodPut, base+"/overview/handover?layer="+root, token, map[string]any{"note": "persist across store restart"}, nil)
	if status != 200 {
		t.Fatal(body)
	}
	service.Close()
	stop()
	base, token, h, _ = startInputServer(t, dir, password)
	reopened := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer reopened.Close()
	h.SetEMS(emsapi.New(reopened, time.Minute))
	var handover overviewHandoverResponse
	status, body = call(t, http.MethodGet, base+"/overview/handover?layer="+root, token, nil, &handover)
	if status != 200 || handover.Handover == nil || handover.Handover.Note != "persist across store restart" || handover.Handover.Actor == "" || handover.Handover.UpdatedAt.IsZero() {
		t.Fatalf("restart lost handover: %s", body)
	}
	var eventsBefore struct {
		Events []alarmEvent `json:"events"`
	}
	status, body = call(t, http.MethodGet, base+"/alarm-events?occurrence_id="+first.ID, token, nil, &eventsBefore)
	if status != 200 || len(eventsBefore.Events) != 2 {
		t.Fatalf("missing durable transitions: %s", body)
	}
	// Wiring the same runtime twice must not duplicate occurrences or replay ack.
	h.SetEMS(h.ems)
	var eventsAfter struct {
		Events []alarmEvent `json:"events"`
	}
	status, body = call(t, http.MethodGet, base+"/alarm-events?occurrence_id="+first.ID, token, nil, &eventsAfter)
	if status != 200 || len(eventsAfter.Events) != len(eventsBefore.Events) {
		t.Fatalf("replay emitted actions: %s", body)
	}
	var listed struct {
		Alarms []alarm.Alarm `json:"alarms"`
	}
	status, body = call(t, http.MethodGet, base+"/ems/alarms", token, nil, &listed)
	if status != 200 || len(listed.Alarms) != 1 || listed.Alarms[0].ID != first.ID || listed.Alarms[0].Count != 3 || listed.Alarms[0].State != alarm.Acknowledged || !listed.Alarms[0].Active {
		t.Fatalf("restart lost occurrence identity, count or handling: %d %s", status, body)
	}
	reopened.Alarms.Clear(now.Add(3*time.Millisecond), first.Key)
	second, _ := reopened.Alarms.Raise(now.Add(4*time.Millisecond), first.Key, alarm.Warning, first.Title, "high", root, "")
	if second.ID == first.ID || second.Key != first.Key || second.Count != 1 {
		t.Fatalf("retrigger identity: %+v", second)
	}
	third := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer third.Close()
	h.SetEMS(emsapi.New(third, time.Minute))
	status, body = call(t, http.MethodGet, base+"/ems/alarms", token, nil, &listed)
	if status != 200 || len(listed.Alarms) != 2 {
		t.Fatalf("restart lost recovery history: %d %s", status, body)
	}
	continued, _ := third.Alarms.Raise(now.Add(5*time.Millisecond), first.Key, alarm.Warning, first.Title, "high", root, "")
	if continued.ID != second.ID || continued.Count != 2 {
		t.Fatalf("restart created duplicate: %+v", continued)
	}
}

func TestAlarmCheckpointsBoundReplayAndKeepFailedCompactionRecoverable(t *testing.T) {
	dir, password := t.TempDir(), data.NewID()
	base, token, h, stop := startInputServer(t, dir, password)
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	h.SetEMS(emsapi.New(service, time.Minute))
	root := snapshotRoot(t, base, token)
	now := time.Now()
	first, _ := service.Alarms.Raise(now, "bounded", alarm.Warning, "bounded", "same", root, "")
	// Force atomic replacement preparation to fail. The original journal must
	// remain readable on the next complete storage/runtime restart.
	if err := os.Mkdir(filepath.Join(dir, alarmEventsFile+".tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 1024; i++ {
		service.Alarms.Raise(now.Add(time.Duration(i)*time.Millisecond), first.Key, alarm.Warning, first.Title, "same", root, "")
	}
	status, body := call(t, http.MethodGet, base+"/ems/alarms", token, nil, nil)
	if status != 503 {
		t.Fatalf("compaction failure not surfaced: %d %s", status, body)
	}
	service.Close()
	stop()
	if err := os.Remove(filepath.Join(dir, alarmEventsFile+".tmp")); err != nil {
		t.Fatal(err)
	}
	// An interrupted replacement must never become the authoritative journal.
	if err := os.WriteFile(filepath.Join(dir, alarmEventsFile+".tmp"), []byte("interrupted replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	base, token, h, _ = startInputServer(t, dir, password)
	reopened := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer reopened.Close()
	h.SetEMS(emsapi.New(reopened, time.Minute))
	var list struct {
		Alarms []alarm.Alarm `json:"alarms"`
	}
	status, body = call(t, http.MethodGet, base+"/ems/alarms", token, nil, &list)
	if status != 200 || len(list.Alarms) != 1 || list.Alarms[0].ID != first.ID || list.Alarms[0].Count != 1025 {
		t.Fatalf("failed compaction destroyed count: %s", body)
	}
	// First fresh observation also records restart uncertainty becoming known.
	for i := 1025; i <= 2050; i++ {
		reopened.Alarms.Raise(now.Add(time.Duration(i)*time.Millisecond), first.Key, alarm.Warning, first.Title, "same", root, "")
	}
	info, err := os.Stat(filepath.Join(dir, alarmEventsFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 20000 {
		t.Fatalf("continuous identical samples grew unbounded replay: %d bytes", info.Size())
	}
	var history struct {
		Events []alarmEvent `json:"events"`
	}
	status, body = call(t, http.MethodGet, base+"/alarm-events?occurrence_id="+first.ID, token, nil, &history)
	if status != 200 || len(history.Events) != 2 || history.Events[0].Transition != "raised" || history.Events[1].Transition != "changed" {
		t.Fatalf("checkpoints leaked as lifecycle transitions: %s", body)
	}
	var stats AlarmStats
	status, body = call(t, http.MethodGet, base+"/alarm-stats", token, nil, &stats)
	if status != 200 || stats.Today.Raised != 1 {
		t.Fatalf("checkpoints counted as retriggers: %s", body)
	}
}

func TestAlarmRestartWithUnreadableEvidenceIsUnavailable(t *testing.T) {
	dir := t.TempDir()
	base, token, h, _ := startInputServer(t, dir, data.NewID())
	if err := os.WriteFile(filepath.Join(dir, alarmEventsFile), []byte("broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	h.SetEMS(emsapi.New(service, time.Minute))
	status, body := call(t, http.MethodGet, base+"/ems/alarms", token, nil, nil)
	if status != 503 {
		t.Fatalf("unreadable evidence became an empty successful alarm list: %d %s", status, body)
	}
}

func TestAlarmWatcherUnknownDoesNotResolveOccurrence(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	h.SetEMS(emsapi.New(service, time.Minute))
	root := snapshotRoot(t, base, token)
	point := createNodeAPI(t, base, token, "variable", root, "isolated legacy input", 45)
	config := alarmtype.Config{Code: "HIGH", Name: "温度异常", Severity: "warning", Trigger: "gt", PointID: point, Value: 40, Confirm: "manual", RecordRecovery: true, Enabled: true}
	status, body := call(t, http.MethodPut, base+"/alarm-types?root="+root, token, map[string]any{"root": root, "type": config}, nil)
	if status != 200 {
		t.Fatal(body)
	}
	watcher := alarmwatch.NewWatcher(h.nc, service.Alarms)
	now := time.Now()
	if err := watcher.Scan(now); err != nil {
		t.Fatal(err)
	}
	var first alarm.Alarm
	for _, value := range service.Alarms.List() {
		if value.Title == "温度异常" {
			first = value
		}
	}
	if first.ID == "" || !first.Active {
		t.Fatal("valid abnormal input did not raise")
	}
	finish := watcher.BeginConfiguration()
	finish()
	if err := watcher.Scan(now); err != nil {
		t.Fatal(err)
	}
	refreshed, _ := service.Alarms.Get(first.ID)
	if refreshed.InputState != "abnormal" {
		t.Fatal("verified unchanged configuration remained unknown")
	}
	if err := watcher.Scan(now.Add(20 * time.Second)); err != nil {
		t.Fatal(err)
	}
	current, _ := service.Alarms.Get(first.ID)
	if !current.Active || current.State == alarm.Resolved {
		t.Fatalf("stale input fabricated recovery: %+v", current)
	}
}

func TestAlarmEvidenceWriteFailureIsReported(t *testing.T) {
	dir := t.TempDir()
	base, token, h, _ := startInputServer(t, dir, data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	h.SetEMS(emsapi.New(service, time.Minute))
	root := snapshotRoot(t, base, token)
	record, _ := service.Alarms.Raise(time.Now(), "source", alarm.Warning, "A alarm", "active", root, "")
	path := filepath.Join(dir, "alarm-events.jsonl")
	if err := os.Rename(path, path+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	status, body := call(t, http.MethodPost, base+"/ems/alarms/"+record.ID+"/ack", token, map[string]any{"note": "handling"}, nil)
	if status != 503 {
		t.Fatalf("handling claimed durable evidence after write failure: %d %s", status, body)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	status, body = call(t, http.MethodGet, base+"/alarm-stats", token, nil, nil)
	if status != 503 {
		t.Fatalf("lost evidence became successful empty history: %d %s", status, body)
	}
}

func TestActiveAlarmSeverityDoesNotRestartActivationDelay(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	h.SetEMS(emsapi.New(service, time.Minute))
	root := snapshotRoot(t, base, token)
	point := createNodeAPI(t, base, token, "variable", root, "delayed input", 45)
	config := alarmtype.Config{Code: "DELAY", Name: "delayed alarm", Severity: "warning", Trigger: "gt", PointID: point, Value: 40, DelaySeconds: 10, Confirm: "manual", RecordRecovery: true, Enabled: true}
	save := func() {
		t.Helper()
		status, body := call(t, http.MethodPut, base+"/alarm-types?root="+root, token, map[string]any{"root": root, "type": config}, nil)
		if status != 200 {
			t.Fatal(body)
		}
	}
	save()
	watcher := alarmwatch.NewWatcher(h.nc, service.Alarms)
	now := time.Now()
	if err := watcher.Scan(now); err != nil {
		t.Fatal(err)
	}
	if err := watcher.Scan(now.Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var first alarm.Alarm
	for _, record := range service.Alarms.List() {
		if record.Title == config.Name {
			first = record
		}
	}
	if !first.Active {
		t.Fatal("initial delay did not qualify")
	}
	config.Severity = "critical"
	save()
	if err := watcher.Scan(now.Add(11 * time.Second)); err != nil {
		t.Fatal(err)
	}
	updated, _ := service.Alarms.Get(first.ID)
	if updated.Severity != alarm.Critical {
		t.Fatalf("active severity update restarted delay: %+v", updated)
	}
}

func TestTrustedDigitalAlarmPreservesBooleanConditions(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	source := bridge.NewNodeSource(h.nc)
	api := emsapi.New(service, time.Minute)
	api.Buses = source.Buses
	h.SetEMS(api)
	f := &pointConfigFixture{base: base, token: token}
	root := snapshotRoot(t, base, token)
	bus := f.createBus(t, "digital")
	point := f.createPoint(t, bus, ModbusIOInput{Description: "fault bit", ServerID: 1, Address: 0, IOType: data.PointValueModbusCoil, DataFormat: "uint16", Scale: 1, ReadOnly: true})
	config := alarmtype.Config{Code: "FAULT", Name: "fault", Severity: "warning", Trigger: "eq", PointID: point, Value: 1, Confirm: "manual", RecordRecovery: true, Enabled: true}
	status, body := call(t, http.MethodPut, base+"/alarm-types?root="+root, token, map[string]any{"root": root, "type": config}, nil)
	if status != 200 {
		t.Fatal(body)
	}
	watcher := alarmwatch.NewWatcher(h.nc, service.Alarms)
	watcher.State = service.State
	watcher.Buses = source.Buses
	service.OnObservations = watcher.Observe
	if err := watcher.Scan(time.Now()); err != nil {
		t.Fatal(err)
	}
	configs, err := source.Buses()
	if err != nil {
		t.Fatal(err)
	}
	device := configs[0].Devices[0]
	observe := func(value bool) {
		now := time.Now()
		service.Observe(now, []channel.Result{{DeviceID: device.ID, PointID: point, Value: signal.Value{Kind: signal.Bool, Bool: value}, Quality: signal.Good, SourceTime: now, ConfigStamp: device.Points[0].ConfigStamp}})
	}
	observe(true)
	var raised alarm.Alarm
	for _, record := range service.Alarms.List() {
		if record.Title == "fault" {
			raised = record
		}
	}
	if !raised.Active {
		t.Fatal("trusted boolean true did not trigger eq 1")
	}
	observe(false)
	resolved, _ := service.Alarms.Get(raised.ID)
	if resolved.Active || resolved.State != alarm.Resolved {
		t.Fatal("trusted boolean false did not recover eq 1")
	}
}

func TestAlarmConfigurationReadFailureIsNotEmptySuccess(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	api := emsapi.New(service, time.Minute)
	h.SetEMS(api)
	root := snapshotRoot(t, base, token)
	record, _ := service.Alarms.Raise(time.Now(), "source", alarm.Warning, "A alarm", "active", root, "")
	api.Buses = func() ([]bridge.BusConfig, error) { return nil, errors.New("configuration read failed") }
	for _, path := range []string{"/ems/alarms", "/alarm-stats", "/alarm-events?occurrence_id=" + record.ID} {
		status, body := call(t, http.MethodGet, base+path, token, nil, nil)
		if status != 503 {
			t.Fatalf("configuration failure became empty/hidden data: %s %d %s", path, status, body)
		}
	}
}

func TestLegacyAlarmStatisticsAreExplicitlyIncomplete(t *testing.T) {
	dir := t.TempDir()
	base, token, h, _ := startInputServer(t, dir, data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	h.SetEMS(emsapi.New(service, time.Minute))
	root := snapshotRoot(t, base, token)
	line, _ := json.Marshal(map[string]any{"time": time.Now(), "key": "legacy-A", "transition": "raised", "severity": "warning", "title": "old record", "device_id": root, "state": "active"})
	if err := os.WriteFile(filepath.Join(dir, "alarm-events.jsonl"), append(line, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Legacy bool `json:"legacy_incomplete"`
	}
	status, body := call(t, http.MethodGet, base+"/alarm-stats", token, nil, &result)
	if status != 200 || !result.Legacy {
		t.Fatalf("legacy counts presented as exact occurrence history: %d %s", status, body)
	}
}

func TestAlarmHistoryReadFailureIsNotEmptySuccess(t *testing.T) {
	dir := t.TempDir()
	base, token, h, _ := startInputServer(t, dir, data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	h.SetEMS(emsapi.New(service, time.Minute))
	if err := os.Mkdir(filepath.Join(dir, "alarm-events.jsonl"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/alarm-stats", "/alarm-events?occurrence_id=unknown"} {
		status, body := call(t, http.MethodGet, base+path, token, nil, nil)
		if status != 503 {
			t.Fatalf("failed history read reported as empty: %s %d %s", path, status, body)
		}
	}
}

func TestAlarmDeviceScopeResolvesSameChannelStations(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	api := emsapi.New(service, time.Minute)
	api.Buses = bridge.NewNodeSource(h.nc).Buses
	h.SetEMS(api)
	f := &pointConfigFixture{base: base, token: token}
	bus := f.createBus(t, "shared-channel")
	input := ModbusIOInput{Description: "A", ServerID: 1, Address: 10, IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1, ReadOnly: true, Units: "kW"}
	a := f.createPoint(t, bus, input)
	input.ServerID, input.Description = 2, "B"
	f.createPoint(t, bus, input)
	configs, err := api.Buses()
	if err != nil {
		t.Fatal(err)
	}
	var aID, bID string
	for _, device := range configs[0].Devices {
		if device.ServerID == 1 {
			aID = device.ID
		} else {
			bID = device.ID
		}
	}
	first, _ := service.Alarms.Raise(time.Now(), "offline:"+aID, alarm.Critical, "通信故障", "A", aID, "")
	second, _ := service.Alarms.Raise(time.Now(), "offline:"+bID, alarm.Critical, "通信故障", "B", bID, "")
	operator := inputAccount(t, f, a, "operator", nil)
	var listed struct {
		Alarms []alarm.Alarm `json:"alarms"`
	}
	status, body := call(t, http.MethodGet, base+"/ems/alarms", operator, nil, &listed)
	if status != 200 || len(listed.Alarms) != 1 || listed.Alarms[0].ID != first.ID {
		t.Fatalf("runtime station identity not scoped correctly: %d %s", status, body)
	}
	var projected struct {
		Alarms []struct {
			ID     string `json:"id"`
			Target struct {
				Kind      string   `json:"kind"`
				ChannelID string   `json:"channel_id"`
				ServerID  int      `json:"server_id"`
				Points    []string `json:"point_ids"`
			} `json:"target"`
			Permissions map[string]bool `json:"permissions"`
		} `json:"alarms"`
	}
	status, body = call(t, http.MethodGet, base+"/ems/alarms", operator, nil, &projected)
	if status != 200 || len(projected.Alarms) != 1 || projected.Alarms[0].Target.Kind != "device" || projected.Alarms[0].Target.ChannelID != bus || projected.Alarms[0].Target.ServerID != 1 || len(projected.Alarms[0].Target.Points) != 1 || projected.Alarms[0].Target.Points[0] != a || !projected.Alarms[0].Permissions["ack"] {
		t.Fatalf("alarm navigation or action metadata missing: %d %s", status, body)
	}
	status, _ = call(t, http.MethodPost, base+"/ems/alarms/"+second.ID+"/ack", operator, map[string]any{"note": "forbidden"}, nil)
	if status != 404 {
		t.Fatalf("shared channel widened alarm scope: %d", status)
	}
}

func TestAlarmReadsAndActionsRespectObjectScope(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	h.SetEMS(emsapi.New(service, time.Minute))
	root := snapshotRoot(t, base, token)
	a := createNodeAPI(t, base, token, "group", root, "A", 0)
	b := createNodeAPI(t, base, token, "group", root, "B-private", 0)
	pa := createNodeAPI(t, base, token, "variable", a, "A-temperature", 0)
	pb := createNodeAPI(t, base, token, "variable", b, "B-temperature", 0)
	first, _ := service.Alarms.Raise(time.Now(), "a:temperature", alarm.Warning, "A alarm", "46", a, pa)
	second, _ := service.Alarms.Raise(time.Now(), "b:temperature", alarm.Warning, "B-private alarm", "46", b, pb)
	operator := inputAccount(t, &pointConfigFixture{base: base, token: token}, a, "operator", nil)
	var listed struct {
		Alarms []alarm.Alarm `json:"alarms"`
	}
	status, body := call(t, http.MethodGet, base+"/ems/alarms", operator, nil, &listed)
	if status != 200 || len(listed.Alarms) != 1 || listed.Alarms[0].ID != first.ID {
		t.Fatalf("scoped alarm list disclosed another object: %d %s", status, body)
	}
	var scopedStats AlarmStats
	status, body = call(t, http.MethodGet, base+"/alarm-stats?days=1", operator, nil, &scopedStats)
	if status != 200 || scopedStats.Today.Raised != 1 || len(scopedStats.Ranking) != 1 || scopedStats.Ranking[0].DeviceID != a {
		t.Fatalf("alarm statistics disclosed B: %d %s", status, body)
	}
	for _, action := range []string{"ack", "close", "maintenance"} {
		payload := map[string]any{"note": "no permission", "reason": "no permission", "until": time.Now().Add(time.Hour).Format(time.RFC3339)}
		status, body = call(t, http.MethodPost, base+"/ems/alarms/"+second.ID+"/"+action, operator, payload, nil)
		if status != 403 && status != 404 {
			t.Fatalf("out-of-scope %s accepted or disclosed state: %d %s", action, status, body)
		}
	}
	status, body = call(t, http.MethodDelete, base+"/ems/alarms/"+second.ID+"/maintenance", operator, nil, nil)
	if status != 403 && status != 404 {
		t.Fatalf("out-of-scope maintenance release: %d %s", status, body)
	}
	status, body = call(t, http.MethodPost, base+"/ems/alarms/"+first.ID+"/ack", operator, map[string]any{"note": "已接手A"}, nil)
	if status != 200 {
		t.Fatalf("authorized acknowledgement rejected: %d %s", status, body)
	}
	var history struct {
		Events []struct {
			OccurrenceID string    `json:"occurrence_id"`
			PointID      string    `json:"point_id"`
			Transition   string    `json:"transition"`
			AckedAt      time.Time `json:"acked_at"`
			AckedBy      string    `json:"acked_by"`
			AckNote      string    `json:"ack_note"`
			Revision     uint64    `json:"revision"`
		} `json:"events"`
	}
	status, body = call(t, http.MethodGet, base+"/alarm-events?occurrence_id="+first.ID, operator, nil, &history)
	if status != 200 || len(history.Events) != 2 {
		t.Fatalf("missing authorized process history: %d %s", status, body)
	}
	last := history.Events[1]
	if last.OccurrenceID != first.ID || last.PointID != pa || last.AckedAt.IsZero() || last.AckedBy == "" || last.AckNote != "已接手A" || last.Revision != 2 {
		t.Fatalf("handling evidence incomplete: %s", body)
	}
	status, body = call(t, http.MethodGet, base+"/alarm-events?occurrence_id="+second.ID, operator, nil, nil)
	if status != 404 || strings.Contains(body, "B-private") {
		t.Fatalf("B process history disclosed: %d %s", status, body)
	}
	untouched, _ := service.Alarms.Get(second.ID)
	if untouched.State != alarm.Active || untouched.AckedBy != "" || !untouched.MaintenanceUntil.IsZero() {
		t.Fatal("forbidden actions changed B")
	}
}

func TestAlarmStatisticsRespectOccurrenceTransitionOrder(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	h.SetEMS(emsapi.New(service, time.Minute))
	root := snapshotRoot(t, base, token)
	device := createNodeAPI(t, base, token, "group", root, "A", 0)
	point := createNodeAPI(t, base, token, "variable", device, "temperature", 0)
	entered, release, done := make(chan string, 1), make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	record := service.Alarms.OnChange
	service.Alarms.OnChange = func(value alarm.Alarm, transition string) {
		if transition == "raised" {
			entered <- value.ID
			<-release
		}
		record(value, transition)
	}
	go func() {
		defer close(done)
		_, _ = service.Alarms.Raise(time.Now(), "temperature:A", alarm.Warning, "温度异常", "46", device, point)
	}()
	var id string
	select {
	case id = <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("raise callback did not start")
	}
	status, body := call(t, http.MethodPost, base+"/ems/alarms/"+id+"/ack", token, map[string]any{"note": "已接手"}, nil)
	if status != 200 {
		t.Fatal(body)
	}
	close(release)
	<-done
	var stats AlarmStats
	status, body = call(t, http.MethodGet, base+"/alarm-stats?days=1", token, nil, &stats)
	if status != 200 || stats.Today.Raised != 1 || stats.Today.Unhandled != 0 || stats.Today.Handled != 1 {
		t.Fatalf("late delivery overwrote acknowledged state: %d %s", status, body)
	}
}

func TestAlarmStatisticsCountIndependentOccurrences(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	h.SetEMS(emsapi.New(service, time.Minute))
	root := snapshotRoot(t, base, token)
	device := createNodeAPI(t, base, token, "group", root, "A", 0)
	point := createNodeAPI(t, base, token, "variable", device, "temperature", 0)
	now := time.Now()
	first, err := service.Alarms.Raise(now, "temperature:A", alarm.Warning, "温度异常", "46", device, point)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Alarms.Clear(now.Add(time.Millisecond), first.Key); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Alarms.Raise(now.Add(2*time.Millisecond), first.Key, alarm.Warning, "温度异常", "48", device, point); err != nil {
		t.Fatal(err)
	}
	var stats AlarmStats
	status, body := call(t, http.MethodGet, base+"/alarm-stats?days=1", token, nil, &stats)
	if status != 200 || stats.Today.Raised != 2 || stats.Today.Unhandled != 1 {
		t.Fatalf("independent occurrences collapsed in statistics: %d %s", status, body)
	}
}
