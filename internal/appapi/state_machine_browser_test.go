//go:build ticket14browser

package appapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Explicit browser harness, not a production server or a skipped acceptance test.
func TestStateMachineBrowserBackend(t *testing.T) {
	f, site, business, sim := machineCommandFixture(t)
	var dropReply atomic.Bool
	done := make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/execute") && dropReply.CompareAndSwap(true, false) {
			result := httptest.NewRecorder()
			f.handler.ServeHTTP(result, r)
			if result.Code == 200 {
				writeError(w, 503, "fixture_response_lost")
				return
			}
			w.WriteHeader(result.Code)
			_, _ = w.Write(result.Body.Bytes())
			return
		}
		f.handler.ServeHTTP(w, r)
	})))
	mux.Handle("/uniapp/", http.StripPrefix("/uniapp/", http.FileServer(http.Dir("../web/dist/uniapp"))))
	mux.HandleFunc("GET /__fixture", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"fixture": "ticket14-private-simulator", "token": f.token, "site": site, "device": business, "writes": sim.written()})
	})
	mux.HandleFunc("POST /__drop_reply", func(w http.ResponseWriter, r *http.Request) { dropReply.Store(true); w.WriteHeader(204) })
	mux.HandleFunc("POST /__finish", func(w http.ResponseWriter, r *http.Request) {
		select {
		case done <- struct{}{}:
		default:
		}
		w.WriteHeader(204)
	})
	srv := &http.Server{Addr: ":18115", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
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
		t.Fatal("browser did not finish")
	}
}
