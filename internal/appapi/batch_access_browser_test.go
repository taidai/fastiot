package appapi

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestBatchAccessBrowserBackend(t *testing.T) {
	if os.Getenv("FASTIOT_BATCH_BROWSER") != "1" {
		t.Skip("private browser fixture is opt-in")
	}
	f, bus, device, offline := singleDeviceFixture(t)
	var site string
	for _, node := range snapshotNodes(t, f.base, f.token) {
		if node.ID == device {
			site = node.Parent
		}
	}
	second := createNodeAPI(t, f.base, f.token, "device", site, "缺席从站", 0)
	third := createNodeAPI(t, f.base, f.token, "device", site, "冲突设备", 0)
	ctx, cancel := context.WithCancel(context.Background())
	runtimeDone, bridgeDone := make(chan struct{}), make(chan struct{})
	go func() { defer close(runtimeDone); _ = f.runtime.Run(ctx) }()
	f.bridge.Interval = 100 * time.Millisecond
	go func() { defer close(bridgeDone); _ = f.bridge.Run(ctx) }()
	defer func() { cancel(); <-runtimeDone; <-bridgeDone }()
	done := make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", f.handler))
	mux.Handle("/uniapp/", http.StripPrefix("/uniapp/", http.FileServer(http.Dir("../web/dist/uniapp"))))
	mux.HandleFunc("GET /__fixture", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"fixture": "ticket06-isolated-store", "token": f.token, "channel": bus, "device": device, "second": second, "third": third, "site": site})
	})
	mux.HandleFunc("POST /__offline", func(w http.ResponseWriter, r *http.Request) { offline(); w.WriteHeader(204) })
	mux.HandleFunc("POST /__finish", func(w http.ResponseWriter, r *http.Request) {
		select {
		case done <- struct{}{}:
		default:
		}
		w.WriteHeader(204)
	})
	server := &http.Server{Addr: ":18106", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Error(err)
			done <- struct{}{}
		}
	}()
	defer server.Close()
	select {
	case <-done:
	case <-time.After(8 * time.Minute):
		t.Fatal("browser did not finish")
	}
}
