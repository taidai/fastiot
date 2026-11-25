package appapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fastiot/internal/ems/command"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/emsapi"
)

func TestDeviceOperationBrowserBackend(t *testing.T) {
	if os.Getenv("FASTIOT_OPERATION_BROWSER") != "1" {
		t.Skip("private browser fixture only")
	}
	f, bus, sim := operationFixture(t)
	var failTerminal, dropReply atomic.Bool
	persist := f.runtime.Commands().Persist
	f.runtime.Commands().Persist = func(outcome command.Outcome) error {
		if failTerminal.Load() && outcome.State == command.Confirmed {
			return errors.New("isolated fixture terminal journal failure")
		}
		return persist(outcome)
	}
	done := make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Exercise a lost submission response, not a fabricated command outcome.
		// The real handler still authorizes, journals and dispatches the same request.
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/commands") && dropReply.CompareAndSwap(true, false) {
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
		writeJSON(w, map[string]any{"fixture": "ticket07-private-simulator", "token": f.token, "channel": bus, "writes": sim.written()})
	})
	mux.HandleFunc("POST /__uncertain", func(w http.ResponseWriter, r *http.Request) {
		failTerminal.Store(true)
		dropReply.Store(true)
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /__restart_commands", func(w http.ResponseWriter, r *http.Request) {
		service := runtime.NewService(nil, time.Minute, time.Second, 100)
		t.Cleanup(service.Close)
		handler := emsapi.New(service, time.Minute)
		handler.Profiles = f.handler.ems.Profiles
		WireCommandHistory(f.dir, service.Commands())
		f.handler.SetEMS(handler)
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /__finish", func(w http.ResponseWriter, r *http.Request) {
		select {
		case done <- struct{}{}:
		default:
		}
		w.WriteHeader(204)
	})
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
