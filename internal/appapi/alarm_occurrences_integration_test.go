package appapi

import (
	"context"
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
	if err := watcher.Scan(now.Add(20 * time.Second)); err != nil {
		t.Fatal(err)
	}
	current, _ := service.Alarms.Get(first.ID)
	if !current.Active || current.State == alarm.Resolved {
		t.Fatalf("stale input fabricated recovery: %+v", current)
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
