package appapi

import (
	"context"
	"net/http"
	"os"
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

// Opt-in read-only browser fixture. All recorded evidence is simulator evidence;
// only an in-memory read simulator runs, with no network bus or physical device.
func TestDailyStatsBrowserBackend(t *testing.T) {
	if os.Getenv("FASTIOT_DAILY_BROWSER") != "1" {
		t.Skip("browser harness is opt-in")
	}
	base, token, h, _ := startInputServer(t, t.TempDir(), "daily-browser-test-only")
	root := snapshotRoot(t, base, token)
	f := &pointConfigFixture{base: base, token: token}
	bus := f.createBusPeriod(t, "Daily recorded evidence", 1000)
	energy := f.createPoint(t, bus, ModbusIOInput{Description: "Cumulative evidence", ServerID: 1, Address: 0, IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1, ReadOnly: true, Units: "kWh"})
	run := f.createPoint(t, bus, ModbusIOInput{Description: "Run evidence", ServerID: 1, Address: 1, IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1, ReadOnly: true})
	empty := createNodeAPI(t, base, token, "group", root, "No daily binding", 0)
	config := map[string]any{"version": 0, "time_zone": "UTC", "energy": map[string]any{"point_id": energy, "unit": "kWh", "max_gap_seconds": 600, "reset_policy": "discard_interval"}, "runtime": map[string]any{"point_id": run, "running_value": 1, "stopped_value": 0, "max_gap_seconds": 600}, "freshness": map[string]any{"point_id": energy, "max_age_seconds": 60}, "communication_point_id": energy}
	status, body := call(t, http.MethodPut, base+"/nodes/"+root+"/daily-stats-config", token, config, nil)
	if status != 200 {
		t.Fatalf("configure: %d %s", status, body)
	}
	status, body = call(t, http.MethodPost, base+"/users", token, map[string]any{"parent": root, "email": "daily-viewer@fixture.local", "password": "daily-browser-test-only", "role": "viewer"}, nil)
	if status != 200 {
		t.Fatalf("viewer: %d %s", status, body)
	}
	service := runtime.NewService(nil, time.Minute, time.Second, 12000)
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
	// Pick the current UTC day's start so every browser run has the same 13 kWh
	// and 1/3 h result, independent of browser connection time.
	start := time.Now().UTC().Truncate(24 * time.Hour)
	if time.Since(start) < 30*time.Minute {
		t.Fatal("browser fixture requires 30 elapsed UTC minutes")
	}
	for i, v := range []float64{100, 110, 2, 5} {
		at := start.Add(time.Duration(i) * 10 * time.Minute)
		service.History.Record([]history.Sample{
			{DeviceID: device.ID, PointID: energy, SourceTime: at, ReceivedTime: at, TimeSource: "device", Value: signal.Num(v), Quality: signal.Good, Unit: "kWh"},
			{DeviceID: device.ID, PointID: run, SourceTime: at, ReceivedTime: at, TimeSource: "device", Value: signal.Num([]float64{1, 1, 0, 0}[i]), Quality: signal.Good},
		})
	}
	sim := &pointConfigBus{}
	if err := service.AddChannel(configs[0].ChannelID, sim, []channel.Device{device}, time.Hour); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return service.Channels()[0].Stats(device.ID).Requests == 1 }, "initial read-only successful poll")
	sim.setOffline(true)
	service.Channels()[0].RunOnce(context.Background(), time.Now)
	if len(sim.written()) != 0 {
		t.Fatal("read-only fixture wrote a device")
	}
	done := make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", h))
	mux.Handle("/uniapp/", http.StripPrefix("/uniapp/", http.FileServer(http.Dir("../web/dist/uniapp"))))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/uniapp/", http.StatusFound) })
	mux.HandleFunc("GET /__fixture", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"fixture": "ticket12-named-volume", "root": root, "empty": empty})
	})
	mux.HandleFunc("POST /__finish", func(w http.ResponseWriter, r *http.Request) {
		select {
		case done <- struct{}{}:
		default:
		}
		w.WriteHeader(204)
	})
	srv := &http.Server{Addr: ":18102", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Error(err)
			done <- struct{}{}
		}
	}()
	defer srv.Close()
	select {
	case <-done:
	case <-time.After(15 * time.Minute):
		t.Fatal("daily browser did not finish")
	}
}
