package appapi

import (
	"net/http"
	"os"
	"testing"
	"time"

	"fastiot/internal/ems/bridge"
	"fastiot/internal/ems/history"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/ems/signal"
	"fastiot/internal/emsapi"
	"github.com/simpleiot/simpleiot/data"
)

// Opt-in browser boundary: real authentication, authorization and history query,
// disposable JetStream storage and recorded observations. No device clients run.
func TestHistoryBrowserBackend(t *testing.T) {
	if os.Getenv("FASTIOT_HISTORY_BROWSER") != "1" {
		t.Skip("browser harness is opt-in")
	}
	base, token, h, _ := startInputServer(t, t.TempDir(), "history-browser-test-only")
	service := runtime.NewService(nil, time.Minute, time.Second, 12000)
	defer service.Close()
	source := bridge.NewNodeSource(h.nc)
	api := emsapi.New(service, time.Minute)
	api.Buses = source.Buses
	h.SetEMS(api)
	f := &pointConfigFixture{base: base, token: token}
	bus := f.createBusPeriod(t, "History scope", 1000)
	hiddenBus := f.createBus(t, "Private scope")
	input := ModbusIOInput{Description: "Pressure evidence", ServerID: 1, Address: 0, IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1, ReadOnly: true, Units: "MPa"}
	point := f.createPoint(t, bus, input)
	input.Description = "Private point"
	hidden := f.createPoint(t, hiddenBus, input)
	input.Description = "Bounded history"
	input.Address = 1
	many := f.createPoint(t, bus, input)
	status, body := call(t, http.MethodPost, base+"/users", token, map[string]any{"parent": bus, "email": "history-viewer@fixture.local", "password": "history-browser-test-only", "role": "viewer", "actions": []string{ActionExport}}, nil)
	if status != 200 {
		t.Fatalf("viewer: %d %s", status, body)
	}
	configs, err := source.Buses()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, config := range configs {
		for _, device := range config.Devices {
			for _, p := range device.Points {
				if p.ID == many {
					for i := 0; i < 10001; i++ {
						at := now.Add(-time.Duration(10100-i) * time.Second)
						service.History.Record([]history.Sample{{DeviceID: device.ID, PointID: many, SourceTime: at, ReceivedTime: at.Add(time.Millisecond), Quality: signal.Good, Value: signal.Num(float64(i)), TimeSource: "platform_acquisition"}})
					}
				}
				if p.ID != point {
					continue
				}
				at := now.Add(-time.Minute)
				for i, q := range []signal.Quality{signal.Good, signal.Good, signal.Stale, signal.Good, signal.Invalid, signal.Good} {
					when := at.Add(time.Duration(i) * time.Second)
					service.History.Record([]history.Sample{{DeviceID: device.ID, PointID: point, SourceTime: when, ReceivedTime: when.Add(2 * time.Second), Quality: q, Value: signal.Num(float64(i)), Unit: "MPa", TimeSource: "device", Late: i == 0}})
				}
				service.History.RecordEvents([]history.Sample{{DeviceID: device.ID, PointID: point, SourceTime: at.Add(6 * time.Second), ReceivedTime: at.Add(6 * time.Second), Kind: history.KindQualityEvent, Quality: signal.Invalid, Reason: "connection_lost", TimeSource: "quality_evaluation"}})
				service.History.Record([]history.Sample{{DeviceID: device.ID, PointID: point, SourceTime: at.Add(20 * time.Second), ReceivedTime: at.Add(21 * time.Second), Quality: signal.Good, Value: signal.Num(20), Unit: "MPa", TimeSource: "device"}})
			}
		}
	}
	done := make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", h))
	mux.HandleFunc("GET /__fixture", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"fixture": "ticket10-isolated-store", "point": point, "many": many, "hidden": hidden})
	})
	mux.HandleFunc("POST /__finish", func(w http.ResponseWriter, r *http.Request) {
		select {
		case done <- struct{}{}:
		default:
		}
		w.WriteHeader(204)
	})
	srv := &http.Server{Addr: ":18090", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Error(err)
			done <- struct{}{}
		}
	}()
	defer srv.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Minute):
		t.Fatal("history browser did not finish")
	}
}
