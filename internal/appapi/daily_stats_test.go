package appapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"fastiot/internal/ems/bridge"
	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/history"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/ems/signal"
	"fastiot/internal/emsapi"
	"github.com/simpleiot/simpleiot/data"
)

func TestDailyStatsConfigurationAndRealHistory(t *testing.T) {
	dir, password := t.TempDir(), data.NewID()
	base, token, h, stop := startInputServer(t, dir, password)
	root := snapshotRoot(t, base, token)
	f := &pointConfigFixture{base: base, token: token}
	bus := f.createBus(t, "Daily evidence")
	point := f.createPoint(t, bus, ModbusIOInput{Description: "Not inferred from name", ServerID: 1, Address: 0, IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1, ReadOnly: true, Units: "kWh"})
	var missing map[string]any
	status, body := call(t, http.MethodGet, base+"/overview/daily-stats?layer="+root, token, nil, &missing)
	if status != 200 || !strings.Contains(body, "binding_missing") || !strings.Contains(body, `"value":null`) {
		t.Fatalf("missing declaration: %d %s", status, body)
	}
	config := map[string]any{"version": 0, "time_zone": "UTC", "energy": map[string]any{"point_id": point, "unit": "kWh", "max_gap_seconds": 600, "reset_policy": "discard_interval"}, "freshness": map[string]any{"point_id": point, "max_age_seconds": 60}, "communication_point_id": point}
	path := base + "/nodes/" + root + "/daily-stats-config"
	status, body = call(t, http.MethodPut, path, token, config, nil)
	if status != 200 {
		t.Fatalf("save declaration: %d %s", status, body)
	}
	status, body = call(t, http.MethodPut, path, token, config, nil)
	if status != 409 {
		t.Fatalf("stale version: %d %s", status, body)
	}
	config["version"] = 1
	config["time_zone"] = "not-a-zone"
	status, body = call(t, http.MethodPut, path, token, config, nil)
	if status != 400 {
		t.Fatalf("invalid timezone: %d %s", status, body)
	}
	var stored map[string]any
	status, body = call(t, http.MethodGet, path, token, nil, &stored)
	if status != 200 || stored["version"] != float64(1) || stored["time_zone"] != "UTC" {
		t.Fatalf("rejection mutated declaration: %d %s", status, body)
	}
	config["time_zone"] = "UTC"
	for _, bad := range []map[string]any{
		{"point_id": point, "unit": "kW", "max_gap_seconds": 600, "reset_policy": "discard_interval"},
		{"point_id": point, "unit": "kWh", "max_gap_seconds": 0, "reset_policy": "discard_interval"},
		{"point_id": point, "unit": "kWh", "max_gap_seconds": 600, "reset_policy": "assume_zero"},
	} {
		candidate := map[string]any{"version": 1, "time_zone": "UTC", "energy": bad}
		status, body = call(t, http.MethodPut, path, token, candidate, nil)
		if status != 400 {
			t.Fatalf("invalid semantics: %d %s", status, body)
		}
	}
	area := createNodeAPI(t, base, token, "group", root, "Other statistics layer", 0)
	outside := map[string]any{"version": 0, "time_zone": "UTC", "freshness": map[string]any{"point_id": point, "max_age_seconds": 60}}
	status, body = call(t, http.MethodPut, base+"/nodes/"+area+"/daily-stats-config", token, outside, nil)
	if status != 403 {
		t.Fatalf("cross-layer reference: %d %s", status, body)
	}
	status, body = call(t, http.MethodGet, base+"/nodes/"+area+"/daily-stats-config", token, nil, nil)
	if status != 200 || !strings.Contains(body, `"version":0`) {
		t.Fatalf("rejected reference left partial config: %d %s", status, body)
	}
	viewer := inputAccount(t, f, root, "viewer", nil)
	status, body = call(t, http.MethodPut, path, viewer, config, nil)
	if status != 403 {
		t.Fatalf("viewer configured: %d %s", status, body)
	}
	restricted := inputAccount(t, f, bus, "viewer", nil)
	status, body = call(t, http.MethodGet, base+"/overview/daily-stats?layer="+root, restricted, nil, nil)
	if status != 403 {
		t.Fatalf("out-of-scope layer: %d %s", status, body)
	}

	service := runtime.NewService(nil, time.Minute, time.Second, 1000)
	defer service.Close()
	source := bridge.NewNodeSource(h.nc)
	api := emsapi.New(service, time.Minute)
	api.Buses = source.Buses
	h.SetEMS(api)
	configs, err := source.Buses()
	if err != nil {
		t.Fatal(err)
	}
	device := configs[0].Devices[0]
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, v := range []float64{100, 110, 2, 5} {
		at := start.Add(time.Duration(i) * 10 * time.Minute)
		service.History.Record([]history.Sample{{DeviceID: device.ID, PointID: point, SourceTime: at, ReceivedTime: at, TimeSource: "device", Value: signal.Num(v), Quality: signal.Good, Unit: "kWh"}})
	}
	var result struct {
		Energy struct {
			Value   *float64 `json:"value"`
			Covered float64  `json:"covered_seconds"`
			Resets  int      `json:"resets"`
			Source  string   `json:"source"`
		} `json:"energy"`
	}
	status, body = call(t, http.MethodGet, base+"/overview/daily-stats?layer="+root+"&date=2026-01-01", viewer, nil, &result)
	if status != 200 || result.Energy.Value == nil || *result.Energy.Value != 13 || result.Energy.Covered != 1200 || result.Energy.Resets != 1 || result.Energy.Source != "modbus_runtime" {
		t.Fatalf("recorded evidence: %d %s", status, body)
	}
	sim := &pointConfigBus{}
	sim.setOffline(true)
	if err := service.AddChannel(configs[0].ChannelID, sim, []channel.Device{device}, time.Hour); err != nil {
		t.Fatal(err)
	}
	ch := service.Channels()[0]
	waitFor(t, func() bool { return ch.Stats(device.ID).Requests == 1 }, "initial read-only failed poll")
	sim.setOffline(false)
	ch.RunOnce(context.Background(), func() time.Time { return time.Now().Add(-10 * time.Minute) })
	sim.setOffline(true)
	var health struct {
		Communication struct {
			Value     *float64 `json:"value"`
			Attempts  int      `json:"attempts"`
			Successes int      `json:"successes"`
		} `json:"communication"`
	}
	status, body = call(t, http.MethodGet, base+"/overview/daily-stats?layer="+root, viewer, nil, &health)
	if status != 200 || health.Communication.Value == nil || *health.Communication.Value != 0 || health.Communication.Attempts != 1 || health.Communication.Successes != 0 {
		t.Fatalf("window not lifetime: %d %s", status, body)
	}
	if len(sim.written()) != 0 {
		t.Fatal("statistics caused physical write")
	}
	// An empty date is unavailable, not a synthetic zero day.
	status, body = call(t, http.MethodGet, base+"/overview/daily-stats?layer="+root+"&date=2026-01-02", viewer, nil, nil)
	if status != 200 || !strings.Contains(body, "no_valid_intervals") {
		t.Fatalf("empty day: %d %s", status, body)
	}
	// A real server/store restart preserves the definition, not the volatile
	// runtime history. The API must show that loss rather than invent a zero day.
	stop()
	base, token, h, _ = startInputServer(t, dir, password)
	status, body = call(t, http.MethodGet, base+"/nodes/"+root+"/daily-stats-config", token, nil, &stored)
	if status != 200 || stored["version"] != float64(1) {
		t.Fatalf("configuration restart: %d %s", status, body)
	}
	second := runtime.NewService(nil, time.Minute, time.Second, 12000)
	defer second.Close()
	source = bridge.NewNodeSource(h.nc)
	api = emsapi.New(second, time.Minute)
	api.Buses = source.Buses
	h.SetEMS(api)
	query := base + "/overview/daily-stats?layer=" + root + "&date=2026-01-01"
	status, body = call(t, http.MethodGet, query, token, nil, nil)
	if status != 200 || !strings.Contains(body, "no_valid_intervals") || !strings.Contains(body, "process_memory") {
		t.Fatalf("restart loss hidden: %d %s", status, body)
	}
	for i := 0; i < 3; i++ {
		at := start.Add(12*time.Hour + time.Duration(i)*time.Minute)
		second.History.Record([]history.Sample{{DeviceID: device.ID, PointID: point, SourceTime: at, ReceivedTime: at, TimeSource: "device", Value: signal.Num(float64(i)), Quality: signal.Good, Unit: "kWh"}})
	}
	second.History.SetLimit(2)
	status, body = call(t, http.MethodGet, query, token, nil, nil)
	if status != 200 || !strings.Contains(body, `"covered_seconds":60`) || !strings.Contains(body, "retained_history_partial") || !strings.Contains(body, `"limit":2`) {
		t.Fatalf("retained partial coverage: %d %s", status, body)
	}
	second.History.SetLimit(12000)
	for i := 0; i < 10001; i++ {
		at := start.Add(time.Duration(i) * time.Second)
		second.History.Record([]history.Sample{{DeviceID: device.ID, PointID: point, SourceTime: at, ReceivedTime: at, TimeSource: "device", Value: signal.Num(float64(i)), Quality: signal.Good, Unit: "kWh"}})
	}
	status, body = call(t, http.MethodGet, query, token, nil, nil)
	if status != 200 || !strings.Contains(body, "history_limit_exceeded") || !strings.Contains(body, `"value":null`) {
		t.Fatalf("bounded query must not sum a truncated day: %d %s", status, body)
	}
}
