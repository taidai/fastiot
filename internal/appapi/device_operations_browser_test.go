package appapi

import (
	"net/http"
	"os"
	"testing"
	"time"
)

func TestDeviceOperationBrowserBackend(t *testing.T) {
	if os.Getenv("FASTIOT_OPERATION_BROWSER") != "1" {
		t.Skip("private browser fixture only")
	}
	f, bus, sim := operationFixture(t)
	done := make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", f.handler))
	mux.Handle("/uniapp/", http.StripPrefix("/uniapp/", http.FileServer(http.Dir("../web/dist/uniapp"))))
	mux.HandleFunc("GET /__fixture", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"fixture": "ticket07-private-simulator", "token": f.token, "channel": bus, "writes": sim.written()})
	})
	mux.HandleFunc("POST /__finish", func(w http.ResponseWriter, r *http.Request) { done <- struct{}{}; w.WriteHeader(204) })
	srv := &http.Server{Addr: ":18087", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Error(err)
			done <- struct{}{}
		}
	}()
	defer srv.Close()
	select {
	case <-done:
	case <-time.After(6 * time.Minute):
		t.Fatal("browser did not finish")
	}
}
