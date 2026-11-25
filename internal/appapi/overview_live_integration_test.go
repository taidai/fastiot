package appapi

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"fastiot/internal/ems/bridge"
	"fastiot/internal/ems/channel"
	emshistory "fastiot/internal/ems/history"
	"fastiot/internal/ems/profile"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/ems/signal"
	"fastiot/internal/emsapi"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

// Real HTTP, authorization and temporary NATS/SQLite storage. Observations are
// injected at the acquisition boundary (not a physical device or a UI mock).
func overviewLiveFixture(t *testing.T) (string, string, *Handler, map[string]string) {
	t.Helper()
	base, token, h, _ := startInputServer(t, t.TempDir(), "overview-browser-test-only")
	service := runtime.NewService(nil, time.Hour, time.Second, 100)
	t.Cleanup(service.Close)
	api := emsapi.New(service, time.Hour)
	api.Profiles = profile.Load(t.TempDir())
	api.Buses = bridge.NewNodeSource(h.nc).Buses
	h.SetEMS(api)
	root := snapshotRoot(t, base, token)
	ids := map[string]string{"root": root}
	for _, site := range []string{"North", "South"} {
		ids[site] = createNodeAPI(t, base, token, "site", root, site, 0)
		ids[site+" pump"] = createNodeAPI(t, base, token, "device", ids[site], site+" pump", 0)
	}
	createNodeAPI(t, base, token, "device", ids["North"], "Unbound spare", 0)
	unit := createNodeAPI(t, base, token, "unit", ids["North"], "North unit", 0)
	roles, _ := json.Marshal([]map[string]any{{"role": "驱动", "node_id": ids["North pump"], "required": true}, {"role": "备用", "required": false}})
	if err := client.SendNodePoint(h.nc, unit, data.NewPointString("easyemsUnitMembers", "", string(roles)), true); err != nil {
		t.Fatal(err)
	}
	// Persist declarations; only the isolated failing read simulator is started.
	ch := data.NodeEdge{ID: data.NewID(), Parent: root, Type: data.NodeTypeModbus, Points: data.Points{data.NewPointString(data.PointTypeDescription, "", "Isolated observation fixture")}}
	ch.Points = append(ch.Points, data.NewPointString(data.PointTypeProtocol, "", data.PointValueTCP), data.NewPointString(data.PointTypeURI, "", "127.0.0.1:1")) // Declaration only; no network transport is created.
	for i, site := range []string{"North", "South"} {
		encoded, _ := json.Marshal(unitModelConfig{Name: site + " pump", BusinessNodeID: ids[site+" pump"]})
		ch.Points = append(ch.Points, data.NewPointString(unitModelPoint, []string{"1", "2"}[i], string(encoded)))
	}
	if err := client.SendNode(h.nc, ch, "test"); err != nil {
		t.Fatal(err)
	}
	ids["channel"] = ch.ID
	for i, name := range []string{"Zero pressure", "Missing pressure", "Stale pressure", "South pressure", "Unbound telemetry"} {
		server := 1
		if i == 3 {
			server = 2
		}
		if i == 4 {
			server = 3
		}
		p := data.NodeEdge{ID: data.NewID(), Parent: ch.ID, Type: data.NodeTypeModbusIO, Points: data.Points{data.NewPointString(data.PointTypeDescription, "", name), data.NewPointFloat(data.PointTypeID, "", float64(server)), data.NewPointString(data.PointTypeUnits, "", "MPa")}}
		p.Points = append(p.Points, data.NewPointString(data.PointTypeModbusIOType, "", data.PointValueModbusHoldingRegister), data.NewPointString(data.PointTypeDataFormat, "", "uint16"), data.NewPointFloat(data.PointTypeScale, "", 1), data.NewPointFloat(data.PointTypeReadOnly, "", 1))
		if err := client.SendNode(h.nc, p, "test"); err != nil {
			t.Fatal(err)
		}
		ids[name] = p.ID
	}
	now := time.Now()
	service.State.Apply(now, []channel.Result{
		{DeviceID: ch.ID + "/3", PointID: ids["Unbound telemetry"], Value: signal.Num(777), Quality: signal.Good, SourceTime: now},
		{DeviceID: ch.ID + "/1", PointID: ids["Zero pressure"], Value: signal.Num(0), Quality: signal.Good, SourceTime: now},
		{DeviceID: ch.ID + "/1", PointID: ids["Stale pressure"], Value: signal.Num(8), Quality: signal.Good, SourceTime: now.Add(-2 * time.Hour)},
		{DeviceID: ch.ID + "/2", PointID: ids["South pressure"], Value: signal.Num(23), Quality: signal.Good, SourceTime: now},
	})
	bus := &pointConfigBus{}
	bus.setOffline(true)
	if err := service.AddChannel(ch.ID, bus, []channel.Device{{ID: ch.ID + "/2", ServerID: 2, Points: []channel.Point{{ID: ids["South pressure"], Function: channel.HoldingRegister, Format: "uint16", Scale: 1, Kind: signal.Number, ReadOnly: true}}}}, 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if len(bus.written()) != 0 {
			t.Fatal("overview performed device writes")
		}
	})
	service.History.Record([]emshistory.Sample{{DeviceID: ch.ID + "/1", PointID: ids["Zero pressure"], Value: signal.Num(0), Quality: signal.Good, SourceTime: now, ReceivedTime: now, TimeSource: "platform_acquisition", Unit: "MPa"}})
	return base, token, h, ids
}

func TestOverviewLiveScopedHTTP(t *testing.T) {
	base, token, _, ids := overviewLiveFixture(t)
	for _, route := range []string{"/snapshot", "/device-ledger", "/ems/state", "/ems/quality"} {
		status, body := projectCall(t, http.MethodGet, base+route, token, ids["root"], nil, nil)
		if status != 200 {
			t.Fatalf("%s: %d %s", route, status, body)
		}
		status, _ = projectCall(t, http.MethodGet, base+route, token, "foreign-project", nil, nil)
		if status != 409 {
			t.Fatalf("foreign project %s: %d", route, status)
		}
		status, _ = projectCall(t, http.MethodGet, base+route, "", ids["root"], nil, nil)
		if status != 401 {
			t.Fatalf("anonymous %s: %d", route, status)
		}
	}
	var state struct {
		Values []struct {
			PointID string `json:"point_id"`
			Quality string `json:"quality"`
			Value   struct {
				Number *float64 `json:"number"`
			} `json:"value"`
		} `json:"values"`
	}
	status, body := projectCall(t, http.MethodGet, base+"/ems/state", token, ids["root"], nil, &state)
	if status != 200 {
		t.Fatal(body)
	}
	foundZero, foundStale := false, false
	for _, value := range state.Values {
		if value.PointID == ids["Missing pressure"] {
			t.Fatal("unobserved point fabricated a value")
		}
		if value.PointID == ids["Zero pressure"] {
			foundZero = value.Quality == "good" && value.Value.Number != nil && *value.Value.Number == 0
		}
		if value.PointID == ids["Stale pressure"] {
			foundStale = value.Quality == "stale"
		}
	}
	if !foundZero || !foundStale {
		t.Fatalf("zero/stale lost: %s", body)
	}
	var recent struct {
		Results []struct {
			Records []struct {
				Value *float64 `json:"value"`
			} `json:"records"`
		} `json:"results"`
	}
	status, body = projectCall(t, http.MethodGet, base+"/history?node_id="+ids["Zero pressure"]+"&type=value&start="+time.Now().Add(-15*time.Minute).Format(time.RFC3339Nano)+"&end="+time.Now().Format(time.RFC3339Nano), token, ids["root"], nil, &recent)
	if status != 200 || len(recent.Results) != 1 || len(recent.Results[0].Records) != 1 || recent.Results[0].Records[0].Value == nil || *recent.Results[0].Records[0].Value != 0 {
		t.Fatalf("recent runtime zero: %d %s", status, body)
	}
	viewer := inputAccount(t, &pointConfigFixture{base: base, token: token}, ids["North"], "viewer", nil)
	status, body = projectCall(t, http.MethodGet, base+"/ems/state", viewer, ids["root"], nil, &state)
	if status != 200 || len(state.Values) != 0 {
		t.Fatalf("restricted state disclosed out-of-scope channel: %d %s", status, body)
	}
	status, body = projectCall(t, http.MethodGet, base+"/ems/screen/"+ids["South"], viewer, ids["root"], nil, nil)
	if status != 403 {
		t.Fatalf("foreign layer: %d %s", status, body)
	}
}

func TestOverviewLiveBrowserBackend(t *testing.T) {
	if os.Getenv("FASTIOT_OVERVIEW_BROWSER") != "1" {
		t.Skip("opt-in isolated browser harness")
	}
	_, _, h, _ := overviewLiveFixture(t)
	done := make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", h))
	mux.HandleFunc("GET /__fixture", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ticket09-isolated-observations")) })
	mux.HandleFunc("POST /__finish", func(w http.ResponseWriter, r *http.Request) {
		select {
		case done <- struct{}{}:
		default:
		}
		w.WriteHeader(204)
	})
	srv := &http.Server{Addr: ":18089", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Error(err)
			done <- struct{}{}
		}
	}()
	defer srv.Close()
	select {
	case <-done:
	case <-time.After(8 * time.Minute):
		t.Fatal("browser did not finish")
	}
}
