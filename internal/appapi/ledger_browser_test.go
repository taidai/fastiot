package appapi

import (
	"net/http"
	"os"
	"testing"
	"time"
)

// Opt-in disposable HTTP boundary; no device clients or user's launch settings.
func TestLedgerBrowserBackend(t *testing.T) {
	if os.Getenv("FASTIOT_LEDGER_BROWSER") != "1" {
		t.Skip("browser harness is opt-in")
	}
	_, _, h, _ := startInputServer(t, t.TempDir(), "ledger-browser-test-only")
	done := make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", h))
	mux.HandleFunc("GET /__fixture", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ticket03-isolated-store")) })
	mux.HandleFunc("POST /__finish", func(w http.ResponseWriter, r *http.Request) {
		select {
		case done <- struct{}{}:
		default:
		}
		w.WriteHeader(204)
	})
	srv := &http.Server{Addr: ":18083", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
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
