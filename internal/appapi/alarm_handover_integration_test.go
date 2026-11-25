package appapi

import (
	"encoding/json"
	"net/http"
	"os"
	"sync"
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
)

// The existing observation fixture owns NATS/SQLite, authentication, the ledger
// and read-only acquisition declarations. These conditions enter its actual watcher.
func alarmHandoverFixture(t *testing.T) (string, string, *Handler, map[string]string, func(string, float64), func()) {
	t.Helper()
	base, token, h, ids := overviewLiveFixture(t)
	service := h.ems.Service
	service.RemoveChannel(ids["channel"])
	var watcher *alarmwatch.Watcher
	wire := func() {
		watcher = alarmwatch.NewWatcher(h.nc, service.Alarms)
		watcher.State, watcher.Buses, watcher.MaxAge = service.State, bridge.NewNodeSource(h.nc).Buses, time.Hour
		service.OnObservations = watcher.Observe
		h.ems.BeginAlarmConfiguration = watcher.BeginConfiguration
		if err := watcher.Scan(time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	for _, site := range []string{"North", "South"} {
		point := ids["Zero pressure"]
		if site == "South" {
			point = ids["South pressure"]
		}
		config := alarmtype.Config{Code: site + "-HIGH", Name: site + " high pressure", Severity: "warning", Trigger: "gt", PointID: point, Value: 40, Confirm: "manual", RecordRecovery: true, Enabled: true}
		status, body := call(t, http.MethodPut, base+"/alarm-types?root="+ids["root"], token, map[string]any{"root": ids["root"], "type": config}, nil)
		if status != 200 {
			t.Fatal(body)
		}
	}
	wire()
	observe := func(site string, value float64) {
		configs, err := bridge.NewNodeSource(h.nc).Buses()
		if err != nil {
			t.Fatal(err)
		}
		point := ids["Zero pressure"]
		if site == "South" {
			point = ids["South pressure"]
		}
		for _, config := range configs {
			for _, device := range config.Devices {
				for _, p := range device.Points {
					if p.ID == point {
						now := time.Now()
						service.Observe(now, []channel.Result{{DeviceID: device.ID, PointID: p.ID, Value: signal.Num(value), Quality: signal.Good, SourceTime: now, ConfigStamp: p.ConfigStamp}})
						return
					}
				}
			}
		}
		t.Fatal("fixture point missing")
	}
	restart := func() {
		service.Close()
		service = runtime.NewService(nil, time.Hour, time.Second, 100)
		t.Cleanup(service.Close)
		api := emsapi.New(service, time.Hour)
		api.Buses = bridge.NewNodeSource(h.nc).Buses
		h.SetEMS(api)
		wire()
	}
	observe("North", 45)
	observe("South", 46)
	return base, token, h, ids, observe, restart
}

func TestAlarmHandoverScopedHTTP(t *testing.T) {
	base, token, h, ids, observe, restart := alarmHandoverFixture(t)
	operator := inputAccount(t, &pointConfigFixture{base: base, token: token}, ids["North"], "operator", nil)
	viewer := inputAccount(t, &pointConfigFixture{base: base, token: token}, ids["North"], "viewer", nil)
	// Scope follows the existing node ACL; channel points are deliberately not
	// granted just because their business device lies in North.
	for _, c := range []struct {
		method, route, auth, project, body string
		want                               int
	}{
		{"PUT", "/overview/handover?layer=" + ids["North"], operator, ids["root"], `{"note":"North shift"}`, 200},
		{"GET", "/overview/handover?layer=" + ids["North"], viewer, ids["root"], "", 200},
		{"PUT", "/overview/handover?layer=" + ids["North"], viewer, ids["root"], `{"note":"denied"}`, 403},
		{"GET", "/overview/handover?layer=" + ids["South"], operator, ids["root"], "", 403},
		{"PUT", "/overview/handover?layer=" + ids["South"], operator, ids["root"], `{"note":"denied"}`, 403},
		{"GET", "/ems/alarms", "", ids["root"], "", 401},
		{"GET", "/overview/handover?layer=" + ids["North"], token, "foreign", "", 409},
	} {
		var input any
		if c.body != "" {
			if err := json.Unmarshal([]byte(c.body), &input); err != nil {
				t.Fatal(err)
			}
		}
		status, body := projectCall(t, c.method, base+c.route, c.auth, c.project, input, nil)
		if status != c.want {
			t.Fatalf("%s %s got %d want %d: %s", c.method, c.route, status, c.want, body)
		}
	}
	var list struct {
		Alarms []alarm.Alarm `json:"alarms"`
	}
	status, body := call(t, "GET", base+"/ems/alarms", token, nil, &list)
	if status != 200 || len(list.Alarms) != 2 {
		t.Fatalf("global conditions: %d %s", status, body)
	}
	var north alarm.Alarm
	for _, a := range list.Alarms {
		if a.PointID == ids["Zero pressure"] {
			north = a
		}
	}
	call(t, "POST", base+"/ems/alarms/"+north.ID+"/ack", token, map[string]any{"note": "inspect"}, nil)
	observe("North", 47)
	restart()
	status, body = call(t, "GET", base+"/ems/alarms", token, nil, &list)
	if status != 200 {
		t.Fatal(body)
	}
	found := false
	for _, a := range list.Alarms {
		if a.ID == north.ID {
			found = a.Active && a.State == alarm.Acknowledged && a.Count == 2
		}
	}
	if !found {
		t.Fatalf("restart lost live condition: %s", body)
	}
	observe("North", 20)
	observe("North", 48)
	status, body = call(t, "GET", base+"/ems/alarms", token, nil, &list)
	if status != 200 || len(list.Alarms) != 3 {
		t.Fatalf("retrigger history lost: %s", body)
	}
	var note overviewHandoverResponse
	status, body = call(t, "GET", base+"/overview/handover?layer="+ids["North"], operator, nil, &note)
	if status != 200 || note.Handover == nil || note.Handover.Note != "North shift" || note.Handover.Actor == "" || note.Handover.UpdatedAt.IsZero() {
		t.Fatalf("handover reload: %s", body)
	}
	_ = h
}

func TestAlarmHandoverBrowserBackend(t *testing.T) {
	if os.Getenv("FASTIOT_ALARM_BROWSER") != "1" {
		return
	}
	_, _, h, ids, observe, restart := alarmHandoverFixture(t)
	var mu sync.Mutex
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", h))
	mux.HandleFunc("GET /__fixture", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, ids) })
	mux.HandleFunc("POST /__observe", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Site  string  `json:"site"`
			Value float64 `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "invalid", 400)
			return
		}
		observe(in.Site, in.Value)
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /__restart", func(w http.ResponseWriter, r *http.Request) { restart(); w.WriteHeader(204) })
	done := make(chan struct{}, 1)
	mux.HandleFunc("POST /__finish", func(w http.ResponseWriter, r *http.Request) {
		select {
		case done <- struct{}{}:
		default:
		}
		w.WriteHeader(204)
	})
	srv := &http.Server{Addr: ":18101", ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { mu.Lock(); defer mu.Unlock(); mux.ServeHTTP(w, r) })}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Error(err)
			done <- struct{}{}
		}
	}()
	defer srv.Close()
	select {
	case <-done:
	case <-time.After(20 * time.Minute):
		t.Fatal("browser deadline")
	}
}
