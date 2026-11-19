package appapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/ems/alarmwatch"
	"fastiot/internal/ems/bridge"
	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/profile"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/emsapi"
	"fastiot/internal/sim/pumphouse"
)

type pumpValue struct {
	Kind   string   `json:"kind"`
	Number *float64 `json:"number"`
	Bool   *bool    `json:"bool"`
}

func pumpStateValues(t *testing.T, base, token string) map[string]pumpValue {
	t.Helper()
	var result struct {
		Values []struct {
			PointID string    `json:"point_id"`
			Value   pumpValue `json:"value"`
		} `json:"values"`
	}
	status, _ := call(t, http.MethodGet, base+"/ems/state", token, nil, &result)
	if status != http.StatusOK {
		t.Fatalf("ems state: %d", status)
	}
	values := map[string]pumpValue{}
	for _, entry := range result.Values {
		values[entry.PointID] = entry.Value
	}
	return values
}

func pumpBool(t *testing.T, base, token, pointID string) (bool, bool) {
	t.Helper()
	value, ok := pumpStateValues(t, base, token)[pointID]
	if !ok || value.Bool == nil {
		return false, false
	}
	return *value.Bool, true
}

func pumpNumber(t *testing.T, base, token, pointID string) (float64, bool) {
	t.Helper()
	value, ok := pumpStateValues(t, base, token)[pointID]
	if !ok || value.Number == nil {
		return 0, false
	}
	return *value.Number, true
}

type pumpCommandOutcome struct {
	RequestID string `json:"request_id"`
	State     string `json:"state"`
	Reason    string `json:"reason"`
}

func submitPumpCommand(t *testing.T, base, token, channelID, commandID string) pumpCommandOutcome {
	t.Helper()
	var outcome pumpCommandOutcome
	status, body := call(t, http.MethodPost, base+"/modbus/"+channelID+"/units/1/commands", token, map[string]any{
		"request_id": data.NewID(), "command_id": commandID,
	}, &outcome)
	if status != http.StatusOK {
		t.Fatalf("submit %s: %d %s", commandID, status, body)
	}
	if outcome.State != "accepted" {
		t.Fatalf("submit %s state = %s, want accepted", commandID, outcome.State)
	}
	return outcome
}

func waitPumpCommand(t *testing.T, base, token, channelID, requestID, want string, timeout time.Duration) pumpCommandOutcome {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last pumpCommandOutcome
	for time.Now().Before(deadline) {
		var outcome pumpCommandOutcome
		status, _ := call(t, http.MethodGet, base+"/modbus/"+channelID+"/units/1/commands/"+requestID, token, nil, &outcome)
		if status == http.StatusOK {
			last = outcome
			if outcome.State == want {
				return outcome
			}
			switch outcome.State {
			case "failed", "unknown", "confirmed", "submitted", "superseded":
				if outcome.State != want {
					t.Fatalf("command %s reached %s (%s), want %s", requestID, outcome.State, outcome.Reason, want)
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("command %s did not reach %s within %v (last %+v)", requestID, want, timeout, last)
	return last
}

func importPumpPointTable(t *testing.T, base, token, channelID string, content []byte) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, base+"/modbus/"+channelID+"/io/import", bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "text/csv; charset=utf-8")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("import point table: %d %s", response.StatusCode, body)
	}
}

// TestPumpHouseSampleCommandLifecycle builds the two-pump sample through the
// product API and runs it against the simulated station: start and stop take
// time, a faulted pump rejects the command, and a pump in local mode never
// moves. It is the acceptance path behind issue #13.
func TestPumpHouseSampleCommandLifecycle(t *testing.T) {
	dir, password := t.TempDir(), data.NewID()
	base, token, handler, _ := startInputServer(t, dir, password)

	profileDir := filepath.Join(dir, "profiles")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		t.Fatal(err)
	}
	sampleProfile := pumphouse.SampleProfile()
	for i := range sampleProfile.CommandSpecs {
		sampleProfile.CommandSpecs[i].TimeoutMS = 3000
	}
	encoded, err := json.Marshal(sampleProfile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "pumphouse.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	registry := profile.Load(profileDir)
	if len(registry.LoadErrors) != 0 {
		t.Fatalf("profile load errors: %v", registry.LoadErrors)
	}

	cfg := pumphouse.DefaultConfig()
	cfg.StartDelay = 1200 * time.Millisecond
	cfg.FeedbackDelay = 300 * time.Millisecond
	cfg.StopDelay = 600 * time.Millisecond
	plant := pumphouse.New(cfg)

	service := runtime.NewService(nil, 5*time.Second, time.Second, 500)
	t.Cleanup(service.Close)
	source := bridge.NewNodeSource(handler.nc)
	runner := bridge.New(service, source, func(string, string, time.Duration) (channel.Bus, error) {
		return plant.Bus(), nil
	})
	emsHandler := emsapi.New(service, 5*time.Second)
	emsHandler.Profiles = registry
	emsHandler.Buses = source.Buses
	handler.SetEMS(emsHandler)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = service.Run(ctx) }()

	root := snapshotRoot(t, base, token)
	group := createNodeAPI(t, base, token, "group", root, pumphouse.SampleName, 0)
	var channelNode struct {
		ID string `json:"id"`
	}
	status, body := call(t, http.MethodPost, base+"/modbus", token, map[string]any{
		"parent": group, "description": "泵站模拟器（Modbus TCP，非真实设备）",
		"protocol": "TCP", "uri": "127.0.0.1:1502", "poll_period": 300, "timeout": 500,
	}, &channelNode)
	if status != http.StatusOK || channelNode.ID == "" {
		t.Fatalf("create channel: %d %s", status, body)
	}
	busID := channelNode.ID
	importPumpPointTable(t, base, token, busID, pumphouse.PointTableCSV(1))

	var model struct {
		Points []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"points"`
	}
	status, body = call(t, http.MethodGet, base+"/modbus/"+busID+"/units/1/model", token, nil, &model)
	if status != http.StatusOK || len(model.Points) != len(pumphouse.SamplePoints) {
		t.Fatalf("unit model: %d %s (%d points)", status, body, len(model.Points))
	}
	pointIDs := map[string]string{}
	for _, point := range model.Points {
		pointIDs[point.Name] = point.ID
	}
	status, body = call(t, http.MethodPut, base+"/modbus/"+busID+"/units/1/model", token, map[string]any{
		"name": "泵站模拟控制器（模拟）", "profile_id": pumphouse.ProfileID, "point_map": pointIDs,
	}, nil)
	if status != http.StatusOK {
		t.Fatalf("save unit model: %d %s", status, body)
	}

	if err := runner.Sync(time.Now()); err != nil {
		t.Fatalf("bridge sync: %v", err)
	}
	waitFor(t, func() bool {
		_, ok := pumpNumber(t, base, token, pointIDs[pumphouse.PointLevel])
		return ok
	}, "first simulated poll")

	tiles := make([]string, 0, len(pumphouse.ScreenTiles))
	for _, name := range pumphouse.ScreenTiles {
		tiles = append(tiles, pointIDs[name])
	}
	status, body = call(t, http.MethodPut, base+"/ems/screen/"+group, token, map[string]any{
		"title": pumphouse.SampleName, "columns": 4, "point_ids": tiles,
	}, nil)
	if status != http.StatusOK {
		t.Fatalf("save screen: %d %s", status, body)
	}
	var screen struct {
		Title    string   `json:"title"`
		PointIDs []string `json:"point_ids"`
	}
	status, body = call(t, http.MethodGet, base+"/ems/screen/"+group, token, nil, &screen)
	if status != http.StatusOK || screen.Title != pumphouse.SampleName || len(screen.PointIDs) != len(tiles) {
		t.Fatalf("read screen: %d %s %+v", status, body, screen)
	}

	for _, config := range pumphouse.AlarmConfigs(pointIDs) {
		status, body = call(t, http.MethodPut, base+"/alarm-types?root="+group, token, map[string]any{
			"root": group, "type": config,
		}, nil)
		if status != http.StatusOK {
			t.Fatalf("save alarm type %s: %d %s", config.Code, status, body)
		}
	}
	for _, name := range []string{pumphouse.PointPump1Command, pumphouse.PointPump2Command} {
		status, body = call(t, http.MethodPut, base+"/safety", token, map[string]any{
			"node": pointIDs[name], "safety": pumphouse.StartSafety(pointIDs[pumphouse.PointLevel]),
		}, nil)
		if status != http.StatusOK {
			t.Fatalf("save safety on %s: %d %s", name, status, body)
		}
	}

	watcher := alarmwatch.NewWatcher(handler.nc, service.Alarms)
	watcher.State = service.State
	watcher.MaxAge = service.MaxAge
	watcher.Buses = source.Buses
	if err := watcher.Scan(time.Now()); err != nil {
		t.Fatalf("alarm scan: %v", err)
	}

	run1 := pointIDs[pumphouse.PointPump1Run]
	started := time.Now()
	startOutcome := submitPumpCommand(t, base, token, busID, "start-1")
	time.Sleep(400 * time.Millisecond)
	if feedback, ok := pumpBool(t, base, token, run1); !ok || feedback {
		t.Fatalf("run feedback = %v (%v) 400ms after start, want off while the pump starts", feedback, ok)
	}
	current := pumpCommandOutcome{}
	if status, _ := call(t, http.MethodGet, base+"/modbus/"+busID+"/units/1/commands/"+startOutcome.RequestID, token, nil, &current); status != http.StatusOK {
		t.Fatalf("read start outcome: %d", status)
	}
	if current.State == "confirmed" {
		t.Fatal("command confirmed before the run feedback appeared")
	}
	var confirmed pumpCommandOutcome
	deadline := time.Now().Add(15 * time.Second)
	for {
		var outcome pumpCommandOutcome
		if status, _ := call(t, http.MethodGet, base+"/modbus/"+busID+"/units/1/commands/"+startOutcome.RequestID, token, nil, &outcome); status == http.StatusOK {
			confirmed = outcome
			feedback, _ := pumpBool(t, base, token, run1)
			switch outcome.State {
			case "confirmed":
				if !feedback {
					t.Fatal("command confirmed while the run feedback was still off")
				}
			case "failed", "unknown", "submitted", "superseded":
				t.Fatalf("start reached %s (%s), want confirmed", outcome.State, outcome.Reason)
			}
			if outcome.State == "confirmed" {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("start did not confirm within the deadline: %+v", confirmed)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if elapsed := time.Since(started); elapsed < cfg.StartDelay {
		t.Fatalf("start confirmed after %v, want at least the %v start process", elapsed, cfg.StartDelay)
	}
	if level, ok := pumpNumber(t, base, token, pointIDs[pumphouse.PointLevel]); !ok || level <= 0 {
		t.Fatalf("level = %v (%v), want a live simulation value", level, ok)
	}

	query := url.Values{}
	query.Set("device_id", busID+"/1")
	query.Set("point_id", pointIDs[pumphouse.PointLevel])
	query.Set("start", time.Now().Add(-time.Minute).Format(time.RFC3339))
	query.Set("end", time.Now().Add(time.Minute).Format(time.RFC3339))
	var history struct {
		Samples []json.RawMessage `json:"samples"`
	}
	status, body = call(t, http.MethodGet, base+"/ems/history?"+query.Encode(), token, nil, &history)
	if status != http.StatusOK || len(history.Samples) == 0 {
		t.Fatalf("history: %d %s", status, body)
	}

	stopOutcome := submitPumpCommand(t, base, token, busID, "stop-1")
	time.Sleep(200 * time.Millisecond)
	if feedback, _ := pumpBool(t, base, token, run1); !feedback {
		t.Fatal("run feedback dropped before the stop process finished")
	}
	waitPumpCommand(t, base, token, busID, stopOutcome.RequestID, "confirmed", 10*time.Second)
	if feedback, _ := pumpBool(t, base, token, run1); feedback {
		t.Fatal("stop confirmed but the run feedback is still on")
	}

	// A fault refuses the next start at the device and never turns the run
	// feedback on. This is the "拒动" case, not a silent success.
	plant.SetFault(1, true)
	waitFor(t, func() bool {
		fault, ok := pumpBool(t, base, token, pointIDs[pumphouse.PointPump1Fault])
		return ok && fault
	}, "fault status")
	faultOutcome := submitPumpCommand(t, base, token, busID, "start-1")
	failed := waitPumpCommand(t, base, token, busID, faultOutcome.RequestID, "failed", 8*time.Second)
	if !strings.Contains(failed.Reason, "refused") {
		t.Fatalf("fault reason = %q, want a device refusal", failed.Reason)
	}
	if feedback, _ := pumpBool(t, base, token, run1); feedback {
		t.Fatal("a refused start must not turn the run feedback on")
	}
	if err := watcher.Scan(time.Now()); err != nil {
		t.Fatalf("alarm scan after fault: %v", err)
	}
	faultAlarm := false
	for _, alarm := range service.Alarms.List() {
		if alarm.Active && alarm.Title == "1#泵故障" {
			faultAlarm = true
		}
	}
	if !faultAlarm {
		t.Fatalf("fault alarm missing: %+v", service.Alarms.List())
	}

	// Local mode accepts the write but the field switch ignores it: the
	// command stays unconfirmed until it times out as unknown.
	plant.SetFault(1, false)
	plant.SetLocal(1, true)
	waitFor(t, func() bool {
		remote, ok := pumpBool(t, base, token, pointIDs[pumphouse.PointPump1Remote])
		return ok && !remote
	}, "local/remote status")
	localOutcome := submitPumpCommand(t, base, token, busID, "start-1")
	unknown := waitPumpCommand(t, base, token, busID, localOutcome.RequestID, "unknown", 10*time.Second)
	if unknown.Reason == "" {
		t.Fatal("an unconfirmed command must explain itself")
	}
	if feedback, _ := pumpBool(t, base, token, run1); feedback {
		t.Fatal("a pump in local mode must not start from a remote command")
	}
}
