package appapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"fastiot/internal/calculation"
	"fastiot/internal/virtualinput"
	"github.com/nats-io/nats.go"
	"github.com/simpleiot/simpleiot/api"
	"github.com/simpleiot/simpleiot/data"
	"github.com/simpleiot/simpleiot/server"
)

func startInputServer(t *testing.T, dir, password string) (string, string, *Handler, func()) {
	t.Helper()
	t.Setenv("FASTEMS_ADMIN_PASSWORD", password)
	t.Setenv("FASTEMS_SEED_DEMO", "0")
	ports := make([]int, 2)
	for i := range ports {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		ports[i] = listener.Addr().(*net.TCPAddr).Port
		listener.Close()
	}
	ready := new(atomic.Bool)
	var handler *Handler
	instance, nc, err := server.NewServer(server.Options{
		ID: "virtual-input-test", DataDir: dir, HTTPPort: strconv.Itoa(ports[0]),
		NatsPort: ports[1], NatsServer: "nats://127.0.0.1:" + strconv.Itoa(ports[1]), AuthToken: data.NewID(),
		DeviceAuth: server.DeviceAuthRequired, CustomUIFS: fstest.MapFS{"index.html": {Data: []byte("test")}},
		AppOnly: true, NatsLoopback: true, ProvisioningDisable: true,
		AppHandlerFactory: func(nc *nats.Conn, users api.UserAuthority) http.Handler {
			handler = New(nc, users, ready)
			handler.SetDataDir(dir)
			return handler
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- instance.Run() }()
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			instance.Stop(nil)
			<-done
		}
	}
	t.Cleanup(stop)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := instance.WaitStart(ctx); err != nil {
		t.Fatal(err)
	}
	if err := Bootstrap(nc, dir); err != nil {
		t.Fatal(err)
	}
	ready.Store(true)
	base := "http://127.0.0.1:" + strconv.Itoa(ports[0]) + "/api"
	waitFor(t, func() bool {
		r, err := http.Get(base + "/health")
		if err != nil {
			return false
		}
		r.Body.Close()
		return r.StatusCode == 200
	}, "input server")
	return base, loginAPI(t, base, "admin", password), handler, stop
}

func TestVirtualInputPersistenceRestartCopyAndRollback(t *testing.T) {
	dir, password := t.TempDir(), data.NewID()
	base, token, handler, stop := startInputServer(t, dir, password)
	root := snapshotRoot(t, base, token)
	site, err := CreateDomainNode(handler.nc, root, NodeTypeSite, map[string]string{"description": "isolated input project"})
	if err != nil {
		t.Fatal(err)
	}
	group := createNodeAPI(t, base, token, "group", site, "inputs", 0)
	min, max := -10.0, 10.0
	timed := virtualinput.Definition{Kind: "number", Unit: "kW", Purpose: "test demand", Min: &min, Max: &max, Validity: "timed", TTLSeconds: 60, RuntimeEditable: true}
	inputs := map[string]inputResponse{}
	for _, source := range []string{"configuration", "simulation"} {
		definition := timed
		if source == "configuration" {
			definition = virtualCapacity("120")
		}
		var input inputResponse
		status, body := call(t, http.MethodPost, base+"/virtual-inputs", token, map[string]any{"parent": group, "name": source, "source": source, "definition": definition, "operation_id": source}, &input)
		if status != 200 {
			t.Fatalf("create %s: %d %s", source, status, body)
		}
		url := base + "/virtual-inputs/" + input.ID
		status, body = call(t, http.MethodPost, url+"/apply", token, map[string]any{"revision": input.Revision, "operation_id": "apply"}, &input)
		if status != 200 {
			t.Fatalf("apply: %d %s", status, body)
		}
		if source != "configuration" {
			status, body = call(t, http.MethodPost, url+"/value", token, map[string]any{"revision": input.Revision, "operation_id": "value", "value": 5}, &input)
			if status != 200 {
				t.Fatalf("value: %d %s", status, body)
			}
		}
		inputs[source] = input
	}
	original := inputs["simulation"].State.Retained
	if original == nil {
		t.Fatal("missing original validity evidence")
	}
	var calculated calcResponse
	definition := calculation.Definition{Operator: "add", Inputs: []string{inputs["simulation"].ID, inputs["simulation"].ID}, Kind: "number", Unit: "kW"}
	status, body := call(t, http.MethodPost, base+"/calculations", token, map[string]any{"parent": group, "name": "restart calculation", "definition": definition, "operation_id": "restart-calculation"}, &calculated)
	if status != 200 {
		t.Fatalf("create restart calculation: %d %s", status, body)
	}
	status, body = call(t, http.MethodPost, base+"/calculations/"+calculated.ID+"/apply", token, map[string]any{"revision": calculated.Revision, "operation_id": "apply-restart-calculation"}, &calculated)
	if status != 200 {
		t.Fatalf("apply restart calculation: %d %s", status, body)
	}
	admin := Identity{UserID: "admin", Policy: AccessPolicy{Admin: true}, Anchors: []string{root}}
	version, err := handler.snapshotSite(admin, site, "input baseline")
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range version.Nodes {
		if node.Type == virtualInputType {
			for _, p := range node.Points {
				if p.Type == virtualInputDocument {
					var doc virtualinput.Document
					if json.Unmarshal([]byte(p.Txt()), &doc) != nil || doc.Current != nil || doc.Retained != nil || doc.LastChange != nil {
						t.Error("version contains runtime value or evidence")
					}
				}
			}
		}
	}
	if err := handler.saveVersion(version); err != nil {
		t.Fatal(err)
	}
	var cloned reuseResult
	status, body = call(t, http.MethodPost, base+"/projects/reuse", token, reuseRequest{Source: site, Name: "copied input project"}, &cloned)
	if status != 200 {
		t.Fatalf("copy: %d %s", status, body)
	}
	var listed struct {
		Inputs []virtualInputResponse `json:"inputs"`
	}
	status, body = call(t, http.MethodGet, base+"/virtual-inputs", token, nil, &listed)
	if status != 200 {
		t.Fatalf("list copied: %d %s", status, body)
	}
	copied := 0
	for _, input := range listed.Inputs {
		if handler.users.IsUnder(input.ID, cloned.SiteID) {
			copied++
			if input.Applied != nil || input.State.Value != nil || input.State.Retained != nil || input.Draft == nil || input.Draft.RuntimeEditable {
				t.Errorf("copy retained active value or grant: %+v", input)
			}
			if input.Name == "simulation" && input.Source != "simulation" {
				t.Error("copy lost simulation provenance")
			}
		}
	}
	if copied != 2 {
		t.Errorf("copied %d inputs, want 2", copied)
	}
	status, _ = call(t, http.MethodPost, base+"/projects/rollback", token, rollbackRequest{Site: site, Version: version.ID}, nil)
	if status != 409 {
		t.Errorf("unsupported rollback not rejected: %d", status)
	}
	stop()
	base, token, restarted, _ := startInputServer(t, dir, password)
	for source, before := range inputs {
		var after inputResponse
		status, body := call(t, http.MethodGet, base+"/virtual-inputs/"+before.ID, token, nil, &after)
		if status != 200 {
			t.Fatalf("recovery: %d %s", status, body)
		}
		if source == "configuration" {
			if string(after.State.Value) != "120" || after.State.Quality != "good" {
				t.Fatalf("configuration lost: %+v", after)
			}
		} else {
			if after.State.Value != nil || after.State.Quality == "good" || after.State.Reason != "pending_verification" || after.State.Retained == nil || !after.State.Retained.EnteredAt.Equal(original.EnteredAt) || !after.State.Retained.ExpiresAt.Equal(original.ExpiresAt) {
				t.Fatalf("restart renewed runtime input: %+v", after)
			}
		}
	}
	if _, err := restarted.runCalculationBatch(); err != nil {
		t.Fatal(err)
	}
	result := restarted.CalculationRuntime().Snapshot().Results[calculated.ID]
	if result.Quality == "good" || !strings.Contains(result.Reason, "pending_verification") {
		t.Fatalf("restart made calculation usable without re-verification: %+v", result)
	}
}
