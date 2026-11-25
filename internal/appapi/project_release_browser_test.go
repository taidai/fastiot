package appapi

import (
	"encoding/json"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"fastiot/internal/ems/runtime"
	"fastiot/internal/emsapi"
	"fastiot/internal/web"
	"github.com/nats-io/nats.go"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

// Opt-in, disposable browser fixture. It never connects a device, starts rules,
// sends notifications, or reads another session's launch configuration/secrets.
func TestTicket20BrowserBackend(t *testing.T) {
	if os.Getenv("FASTIOT_TICKET20_BROWSER") != "1" {
		t.Skip("ticket20 isolated browser fixture is opt-in")
	}
	password := data.NewID()
	base, token, h, _ := startInputServer(t, t.TempDir(), password)
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	h.SetEMS(emsapi.New(service, time.Minute))
	root := snapshotRoot(t, base, token)
	site := createNodeAPI(t, base, token, NodeTypeSite, root, "交付验证工程", 0)
	unit := createNodeAPI(t, base, token, "unit", site, "统计单元", 0)
	point := createNodeAPI(t, base, token, "variable", unit, "软件观测输入", 0)
	members, _ := json.Marshal([]map[string]any{{"role": "统计输入", "required": true}})
	if err := client.SendNodePoint(h.nc, unit, data.NewPointString("easyemsUnitMembers", "0", string(members)), true); err != nil {
		t.Fatal(err)
	}
	readonlyPassword := data.NewID()
	code, body := call(t, http.MethodPost, base+"/users", token, map[string]any{"parent": site, "email": "readonly@fixture.local", "password": readonlyPassword, "role": "viewer"}, nil)
	if code != 200 {
		t.Fatalf("fixture readonly account: %d %s", code, body)
	}
	var physicalWrites atomic.Int64
	sub, err := h.nc.Subscribe("p.*.valueSet.>", func(_ *nats.Msg) { physicalWrites.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()
	if err := h.nc.Flush(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", h))
	mux.HandleFunc("GET /__fixture", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"fixture": "ticket20-isolated", "username": "admin", "password": password, "readonly_username": "readonly@fixture.local", "readonly_password": readonlyPassword, "site": site, "unit": unit, "point": point, "physical_writes": physicalWrites.Load()})
	})
	mux.HandleFunc("POST /__finish", func(w http.ResponseWriter, r *http.Request) {
		select {
		case done <- struct{}{}:
		default:
		}
		w.WriteHeader(204)
	})
	mux.Handle("/", http.FileServer(http.FS(web.FS())))
	server := &http.Server{Addr: ":18200", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Error(err)
			done <- struct{}{}
		}
	}()
	defer server.Close()
	select {
	case <-done:
	case <-time.After(20 * time.Minute):
		t.Fatal("ticket20 browser did not finish")
	}
	if physicalWrites.Load() != 0 {
		t.Fatalf("browser workflow issued %d physical writes", physicalWrites.Load())
	}
}
