package appapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/ems/bridge"
	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/modbusrt"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/emsapi"
	"fastiot/internal/web"
)

// TestTicket21BrowserBackend serves a fresh target instance and one external
// published package. The only reachable acquisition endpoint is the private
// Modbus simulator; no inherited account, device or launch configuration is used.
func TestTicket21BrowserBackend(t *testing.T) {
	if os.Getenv("FASTIOT_TICKET21_BROWSER") != "1" {
		t.Skip("ticket21 isolated browser fixture is opt-in")
	}
	source, sourceBus, sim := l3Fixture(t)
	source.handler.SetDataDir(source.dir)
	var sourceVerification l3Result
	status, body := call(t, http.MethodPost, source.base+"/modbus/"+sourceBus+"/units/1/verify", source.token, map[string]any{"level": "L2"}, &sourceVerification)
	if status != http.StatusOK || !sourceVerification.Passed {
		t.Fatalf("source acquisition verification: %d %s", status, body)
	}
	site := ""
	for _, node := range snapshotNodes(t, source.base, source.token) {
		if node.ID == sourceBus {
			site = node.Parent
		}
	}
	var gates preflightReport
	status, body = call(t, http.MethodGet, source.base+"/delivery/gates?site="+site, source.token, nil, &gates)
	if status != http.StatusOK || gates.Status == "fail" {
		t.Fatalf("source gates: %d %s", status, body)
	}
	var published versionSummary
	status, body = call(t, http.MethodPost, source.base+"/projects/publish", source.token, map[string]any{"site": site, "expected_fingerprint": gates.Fingerprint}, &published)
	if status != http.StatusOK {
		t.Fatalf("source publish: %d %s", status, body)
	}
	var pkg projectPackage
	status, body = call(t, http.MethodGet, source.base+"/projects/export?site="+site+"&version="+published.ID, source.token, nil, &pkg)
	if status != http.StatusOK {
		t.Fatalf("source export: %d %s", status, body)
	}
	missing := projectPackage{}
	raw, _ := json.Marshal(pkg)
	if err := json.Unmarshal(raw, &missing); err != nil {
		t.Fatal(err)
	}
	missing.Dependencies = append(missing.Dependencies, packageDependency{Kind: dependencyKindProfile, ID: "browser-missing-profile", Version: "private-test-version"})
	missing.seal()
	tampered := projectPackage{}
	if err := json.Unmarshal(raw, &tampered); err != nil {
		t.Fatal(err)
	}
	tampered.Name = "tampered after sealing"

	targetDir, targetPassword := t.TempDir(), data.NewID()
	_, _, targetHandler, _ := startInputServer(t, targetDir, targetPassword)
	targetService := runtime.NewService(nil, time.Minute, time.Second, 100)
	t.Cleanup(targetService.Close)
	targetHandler.SetEMS(emsapi.New(targetService, time.Second))
	loadSingleDeviceProfile(t, targetHandler, l3Profile)
	targetBridge := bridge.New(targetService, bridge.NewNodeSource(targetHandler.nc), func(protocol, address string, timeout time.Duration) (channel.Bus, error) {
		if protocol != "tcp" || address != "127.0.0.1:18118" {
			return nil, fmt.Errorf("non-fixture address refused")
		}
		return modbusrt.OpenTCP(address, timeout)
	})
	ctx, cancel := context.WithCancel(context.Background())
	runtimeDone := make(chan struct{})
	go func() { defer close(runtimeDone); _ = targetService.Run(ctx) }()
	syncDone := make(chan struct{})
	go func() {
		defer close(syncDone)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = targetBridge.Sync(time.Now())
			case <-ctx.Done():
				return
			}
		}
	}()
	t.Cleanup(func() { cancel(); <-runtimeDone; <-syncDone })

	var physicalWrites atomic.Int64
	sub, err := targetHandler.nc.Subscribe("p.*.valueSet.>", func(_ *nats.Msg) { physicalWrites.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()
	if err := targetHandler.nc.Flush(); err != nil {
		t.Fatal(err)
	}
	rootNode, err := client.GetRootNode(targetHandler.nc)
	if err != nil {
		t.Fatal(err)
	}
	initialNodes, err := targetHandler.loadSubtree(rootNode.ID)
	if err != nil {
		t.Fatal(err)
	}
	finish := make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", targetHandler))
	mux.HandleFunc("GET /__source-package", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, pkg) })
	mux.HandleFunc("GET /__missing-package", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, missing) })
	mux.HandleFunc("GET /__tampered-package", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, tampered) })
	mux.HandleFunc("GET /__fixture", func(w http.ResponseWriter, r *http.Request) {
		nodes, _ := targetHandler.loadSubtree(rootNode.ID)
		writeJSON(w, map[string]any{
			"fixture": "ticket21-isolated", "username": "admin", "password": targetPassword,
			"initial_node_count": len(initialNodes), "node_count": len(nodes),
			"source_version": pkg.SourceVersion, "source_fingerprint": pkg.SourceFingerprint,
			"physical_writes": physicalWrites.Load(), "simulator_writes": len(sim.written()),
		})
	})
	mux.HandleFunc("POST /__finish", func(w http.ResponseWriter, r *http.Request) {
		select {
		case finish <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("/", http.FileServer(http.FS(web.FS())))
	listener, err := net.Listen("tcp", ":18220")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	serverError := make(chan error, 1)
	go func() { serverError <- server.Serve(listener) }()
	defer server.Close()
	select {
	case <-finish:
	case err := <-serverError:
		if err != nil && err != http.ErrServerClosed {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Minute):
		t.Fatal("ticket21 browser did not finish")
	}
	if physicalWrites.Load() != 0 || len(sim.written()) != 0 {
		t.Fatalf("browser workflow issued physical writes: NATS=%d simulator=%d", physicalWrites.Load(), len(sim.written()))
	}
}
