package appapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/ems/bridge"
	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/modbusrt"
	"fastiot/internal/emsapi"
	"fastiot/internal/web"
)

// Opt-in private browser fixture for deployment records. The only acquisition
// endpoint is the loopback Modbus simulator; nothing external is contacted.
func TestTicket22BrowserBackend(t *testing.T) {
	if os.Getenv("FASTIOT_TICKET22_BROWSER") != "1" {
		t.Skip("ticket22 isolated browser fixture is opt-in")
	}
	source, sourceBus, _ := l3Fixture(t)
	source.handler.SetDataDir(source.dir)
	_, pkg := ticket22PublishAndExport(t, source, sourceBus)

	target := newPointConfigFixture(t)
	target.handler.SetDataDir(target.dir)
	targetEMS := emsapi.New(target.runtime, time.Second)
	targetEMS.Buses = bridge.NewNodeSource(target.handler.nc).Buses
	target.handler.SetEMS(targetEMS)
	loadSingleDeviceProfile(t, target.handler, l3Profile)
	target.bridge = bridge.New(target.runtime, bridge.NewNodeSource(target.handler.nc), func(protocol, address string, timeout time.Duration) (channel.Bus, error) {
		if protocol != "tcp" || address != "127.0.0.1:18118" {
			return nil, fmt.Errorf("non-fixture address refused")
		}
		return modbusrt.OpenTCP(address, timeout)
	})
	var imported importProjectResult
	status, body := call(t, http.MethodPost, target.base+"/projects/import", target.token, map[string]any{"package": pkg}, &imported)
	if status != http.StatusOK || imported.VersionID == "" {
		t.Fatalf("target import: %d %s", status, body)
	}

	done := make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", target.handler))
	mux.HandleFunc("GET /__fixture", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"fixture": "ticket22-private", "token": target.token,
			"site": imported.SiteID, "version": imported.VersionID,
			"source_version": pkg.SourceVersion, "source_fingerprint": pkg.SourceFingerprint,
		})
	})
	// 测试专用：用真实 PUT /modbus 重绑并执行 L2 采集验证，不是产品入口。
	mux.HandleFunc("POST /__rebind", func(w http.ResponseWriter, r *http.Request) {
		targetBus := ""
		for _, node := range snapshotNodes(t, target.base, target.token) {
			if node.Type == data.NodeTypeModbus && node.Parent == imported.SiteID {
				targetBus = node.ID
			}
		}
		if targetBus == "" {
			http.Error(w, "channel missing", 500)
			return
		}
		status, body := call(t, http.MethodPut, target.base+"/modbus/"+targetBus, target.token, ModbusConnectionInput{
			Description: "Rebound private simulator", Protocol: "TCP", URI: "127.0.0.1:18118", PollPeriod: 50, Timeout: 100,
		}, nil)
		if status != http.StatusOK {
			http.Error(w, body, status)
			return
		}
		if err := target.bridge.Sync(time.Now()); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		go func() { _ = target.runtime.Run(ctx) }()
		t.Cleanup(cancel)
		waitFor(t, func() bool { return target.runtime.State.Health(targetBus+"/1") == "healthy" }, "target simulator acquisition")
		var verification l3Result
		status, body = call(t, http.MethodPost, target.base+"/modbus/"+targetBus+"/units/1/verify", target.token, map[string]any{"level": "L2"}, &verification)
		if status != http.StatusOK || !verification.Passed {
			http.Error(w, body, status)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /__finish", func(w http.ResponseWriter, r *http.Request) {
		select {
		case done <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("/", http.FileServer(http.FS(web.FS())))
	server := &http.Server{Addr: ":18240", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Error(err)
			done <- struct{}{}
		}
	}()
	defer server.Close()
	select {
	case <-done:
	case <-time.After(20 * time.Minute):
		t.Fatal("ticket22 browser did not finish")
	}
}
