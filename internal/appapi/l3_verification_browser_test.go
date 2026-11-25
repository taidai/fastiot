package appapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestL3VerificationBrowserBackend(t *testing.T) {
	if os.Getenv("FASTIOT_L3_BROWSER") != "1" {
		t.Skip("private L3 browser fixture only")
	}
	f, bus, sim := l3Fixture(t)
	var dropReply atomic.Bool
	done := make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/verify") && dropReply.CompareAndSwap(true, false) {
			result := httptest.NewRecorder()
			f.handler.ServeHTTP(result, r)
			if result.Code == 200 {
				writeError(w, http.StatusServiceUnavailable, "isolated_response_lost_after_acceptance")
				return
			}
			for key, values := range result.Header() {
				w.Header()[key] = values
			}
			w.WriteHeader(result.Code)
			_, _ = w.Write(result.Body.Bytes())
			return
		}
		f.handler.ServeHTTP(w, r)
	})))
	mux.Handle("/uniapp/", http.StripPrefix("/uniapp/", http.FileServer(http.Dir("../web/dist/uniapp"))))
	mux.HandleFunc("GET /__fixture", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"fixture": "ticket08-private-simulator", "token": f.token, "channel": bus, "writes": sim.written()})
	})
	mux.HandleFunc("POST /__uncertain", func(w http.ResponseWriter, r *http.Request) {
		sim.mu.Lock()
		sim.writeMode = "missing"
		sim.mu.Unlock()
		dropReply.Store(true)
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /__finish", func(w http.ResponseWriter, r *http.Request) {
		select {
		case done <- struct{}{}:
		default:
		}
		w.WriteHeader(204)
	})
	srv := &http.Server{Addr: ":18088", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
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
		t.Fatal("L3 browser did not finish")
	}
}
