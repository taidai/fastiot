//go:build ticket18browser

package appapi

import (
	"net/http"
	"testing"
	"time"

	"fastiot/internal/decision"
	"github.com/nats-io/nats.go"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

// Only the named browser-test build exposes this isolated simulator fixture.
func TestDerivedBrowserBackend(t *testing.T) {
	f, site, business, sim := machineCommandFixture(t)
	f.handler.SetDataDir(f.dir)
	group := createNodeAPI(t, f.base, f.token, "group", site, "Ticket18 derived inputs", 0)
	child := createNodeAPI(t, f.base, f.token, "group", group, "Inherited screen", 0)
	member := createNodeAPI(t, f.base, f.token, "variable", group, "Private software sample (not measured)", 7)
	status, body := call(t, "PUT", f.base+"/aggregate", f.token, AggregateConfig{Group: group, Op: "sum", Members: []string{member}, Window: "2s", Units: "kW"}, nil)
	if status != 200 {
		t.Fatalf("window fixture: %d %s", status, body)
	}
	samplesDone := make(chan struct{})
	defer close(samplesDone)
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-samplesDone:
				return
			case <-ticker.C:
				_ = client.SendNodePoint(f.handler.nc, member, data.NewPointFloat("value", "", 7), true)
			}
		}
	}()
	startDerivedLoops(t, f.handler)
	manager := client.NewManager(f.handler.nc, func(nc *nats.Conn, config decision.DecisionTable) client.Client {
		return f.handler.NewRuleClient(nc, config)
	}, RuleParentTypes())
	stopped := make(chan error, 1)
	go func() { stopped <- manager.Run() }()
	defer func() { manager.Stop(nil); <-stopped }()
	done := make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", f.handler))
	mux.Handle("/uniapp/", http.StripPrefix("/uniapp/", http.FileServer(http.Dir("../web/dist/uniapp"))))
	mux.Handle("/", http.FileServer(http.Dir("../web/dist")))
	mux.HandleFunc("GET /__fixture", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"fixture": "ticket18-private-simulator", "token": f.token, "site": site, "group": group, "child": child, "member": member, "device": business, "writes": sim.written()})
	})
	mux.HandleFunc("POST /__finish", func(w http.ResponseWriter, r *http.Request) {
		select {
		case done <- struct{}{}:
		default:
		}
		w.WriteHeader(204)
	})
	srv := &http.Server{Addr: ":18180", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Error(err)
			done <- struct{}{}
		}
	}()
	defer srv.Close()
	t.Log("ticket18 safe browser ready on 18180; only private simulator enabled")
	select {
	case <-done:
	case <-time.After(30 * time.Minute):
		t.Fatal("browser did not finish")
	}
}
