package appapi

import (
	"encoding/json"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
	"net/http"
	"os"
	"testing"
	"time"
)

// Disposable real-HTTP fixture. No physical device or user's launch configuration.
func TestScreenBrowserBackend(t *testing.T) {
	if os.Getenv("FASTIOT_SCREEN_BROWSER") != "1" {
		t.Skip("browser harness is opt-in")
	}
	base, token, h, ids := overviewLiveFixture(t)
	root := snapshotRoot(t, base, token)
	area := createNodeAPI(t, base, token, "group", root, "Screen area", 0)
	child := createNodeAPI(t, base, token, "group", area, "Screen child", 0)
	sibling := createNodeAPI(t, base, token, "group", root, "Screen sibling", 0)
	for i, layer := range []string{child, sibling} {
		device := createNodeAPI(t, base, token, "device", layer, "Screen device", 0)
		raw, _ := json.Marshal(unitModelConfig{Name: "Screen device", BusinessNodeID: device})
		if err := client.SendNodePoint(h.nc, ids["channel"], data.NewPointString(unitModelPoint, []string{"1", "2"}[i], string(raw)), true); err != nil {
			t.Fatal(err)
		}
	}
	outsidePoint := createNodeAPI(t, base, token, "variable", area, "Parent-only point", 0)
	done := make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", h))
	mux.HandleFunc("GET /__fixture", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"fixture": "ticket17-isolated-store", "root": root, "area": area, "child": child, "sibling": sibling, "outside_point": outsidePoint})
	})
	mux.HandleFunc("POST /__finish", func(w http.ResponseWriter, r *http.Request) {
		select {
		case done <- struct{}{}:
		default:
		}
		w.WriteHeader(204)
	})
	srv := &http.Server{Addr: ":18107", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Error(err)
			done <- struct{}{}
		}
	}()
	defer srv.Close()
	select {
	case <-done:
	case <-time.After(15 * time.Minute):
		t.Fatal("screen browser did not finish")
	}
}
