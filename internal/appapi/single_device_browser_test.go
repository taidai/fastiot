package appapi

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"
)

// Opt-in: a private TCP simulator and disposable store, never a physical device.
func TestSingleDeviceBrowserBackend(t *testing.T) {
	if os.Getenv("FASTIOT_ACCESS_BROWSER") != "1" {
		t.Skip("browser harness is opt-in")
	}
	f, bus, device, offline := singleDeviceFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	doneRuntime, doneBridge := make(chan struct{}), make(chan struct{})
	go func() { defer close(doneRuntime); _ = f.runtime.Run(ctx) }()
	f.bridge.Interval = 100 * time.Millisecond
	go func() { defer close(doneBridge); _ = f.bridge.Run(ctx) }()
	defer func() { cancel(); <-doneRuntime; <-doneBridge }()
	done := make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", f.handler))
	mux.Handle("/uniapp/", http.StripPrefix("/uniapp/", http.FileServer(http.Dir("../web/dist/uniapp"))))
	mux.HandleFunc("GET /__fixture", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"fixture": "ticket05-isolated-store", "token": f.token, "channel": bus, "device": device})
	})
	mux.HandleFunc("POST /__offline", func(w http.ResponseWriter, r *http.Request) { offline(); w.WriteHeader(204) })
	mux.HandleFunc("POST /__finish", func(w http.ResponseWriter, r *http.Request) {
		select {
		case done <- struct{}{}:
		default:
		}
		w.WriteHeader(204)
	})
	srv := &http.Server{Addr: ":18085", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
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
