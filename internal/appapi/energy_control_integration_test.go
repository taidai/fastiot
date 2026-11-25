package appapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fastiot/internal/decision"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/simpleiot/simpleiot/api"
	siotclient "github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
	"github.com/simpleiot/simpleiot/server"
)

// A refusal before I/O is not evidence that the cabinet started transitioning.
func TestCabinetRejectedCommandDoesNotEnterTransition(t *testing.T) {
	for _, event := range []string{"start", "stop"} {
		t.Run(event, func(t *testing.T) {
			f, bus, sim := operationFixtureWithProfile(t, strings.Replace(operationProfile, `"id":"set"`, `"id":"`+event+`"`, 1))
			var model unitModelView
			call(t, http.MethodGet, f.base+"/modbus/"+bus+"/units/1/model", f.token, nil, &model)
			point := model.Model.PointMap["Target"]
			root := snapshotRoot(t, f.base, f.token)
			unit, err := CreateDomainNode(f.handler.nc, root, NodeTypeEnergyUnit, map[string]string{"description": "rejected cabinet"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := CreateDomainNode(f.handler.nc, unit, data.NodeTypeDevice, map[string]string{"description": "power device", PointTypePowerSetpoint: point}); err != nil {
				t.Fatal(err)
			}
			before := decision.CabinetStandby
			if event == "stop" {
				before = decision.CabinetRunning
			}
			if err := SetCabinetState(f.handler.nc, unit, before); err != nil {
				t.Fatal(err)
			}
			status, body := call(t, http.MethodPut, f.base+"/safety", f.token, map[string]any{"node": point, "safety": map[string]any{"conditions": []any{map[string]any{"point_id": point, "operator": "eq", "value": 99}}}}, nil)
			if status != 200 {
				t.Fatalf("safety: %d %s", status, body)
			}
			var payload map[string]any
			status, body = call(t, http.MethodPost, f.base+"/energy-units/"+unit+"/events", f.token, map[string]string{"event": event, "request_id": "cabinet-refused"}, &payload)
			if status != 200 || payload["command_state"] != "rejected" || payload["state"] != string(before) || payload["reason"] == nil {
				t.Errorf("refusal invented transition/lost reason: %d %s", status, body)
			}
			f.handler.ReconcileCabinetCommands()
			node := f.node(t, unit)
			if LoadCabinetState(node) != before {
				t.Errorf("cabinet moved on refusal: %s", LoadCabinetState(node))
			}
			if pending, found := loadPendingCommand(node); found {
				t.Errorf("rejected command left pending: %+v", pending)
			}
			var outcome pumpCommandOutcome
			status, body = call(t, http.MethodGet, f.base+"/modbus/"+bus+"/units/1/commands/cabinet-refused", f.token, nil, &outcome)
			if status != 200 || outcome.State != "rejected" || outcome.Reason == "" {
				t.Errorf("canonical refusal missing: %d %s", status, body)
			}
			if writes := sim.written(); len(writes) != 0 {
				t.Fatalf("refused operation reached simulator: %v", writes)
			}
			transition := decision.CabinetStarting
			if event == "stop" {
				transition = decision.CabinetStopping
			}
			for _, stored := range []decision.CabinetState{transition, decision.CabinetMaintenance, decision.CabinetRunning} {
				t.Run("legacy-"+string(stored), func(t *testing.T) {
					// Fixture for the persisted projection written by the pre-fix caller.
					if err := SetCabinetState(f.handler.nc, unit, stored); err != nil {
						t.Fatal(err)
					}
					encoded, _ := json.Marshal(pendingCommand{RequestID: "cabinet-refused", Event: event, Actor: "legacy-fixture", At: time.Now()})
					if err := siotclient.SendNodePoint(f.handler.nc, unit, data.NewPointString(pendingCommandPoint, "", string(encoded)), true); err != nil {
						t.Fatal(err)
					}
					f.handler.ReconcileCabinetCommands()
					node := f.node(t, unit)
					want := stored
					if stored == transition {
						want = decision.CabinetFault
					}
					if got := LoadCabinetState(node); got != want {
						t.Errorf("legacy rejection state=%s want %s", got, want)
					}
					if pending, found := loadPendingCommand(node); found {
						t.Errorf("legacy rejection remained pending: %+v", pending)
					}
					_, auditBody := call(t, http.MethodGet, f.base+"/audit", f.token, nil, nil)
					if !strings.Contains(auditBody, "rejected") || !strings.Contains(auditBody, "实际状态未核实") {
						t.Errorf("legacy refusal lacks explanatory terminal audit: %s", auditBody)
					}
					if len(sim.written()) != 0 {
						t.Fatal("reconciling rejected projection dispatched device")
					}
				})
			}
		})
	}
}

func TestCabinetUnconfirmedOutcomeEndsProcess(t *testing.T) {
	for _, event := range []string{"start", "stop"} {
		for _, mode := range []string{"reject", "disconnect"} {
			for _, maintenance := range []bool{false, true} {
				name := event + "-" + mode
				if maintenance {
					name += "-maintenance"
				}
				t.Run(name, func(t *testing.T) {
					definition := strings.Replace(operationProfile, `"id":"set"`, `"id":"`+event+`"`, 1)
					definition = strings.Replace(definition, `"default":10`, `"default":20`, 1)
					f, bus, sim := operationFixtureWithProfile(t, definition)
					var model unitModelView
					call(t, http.MethodGet, f.base+"/modbus/"+bus+"/units/1/model", f.token, nil, &model)
					unit, err := CreateDomainNode(f.handler.nc, snapshotRoot(t, f.base, f.token), NodeTypeEnergyUnit, map[string]string{"description": "unconfirmed cabinet"})
					if err != nil {
						t.Fatal(err)
					}
					if _, err := CreateDomainNode(f.handler.nc, unit, data.NodeTypeDevice, map[string]string{PointTypePowerSetpoint: model.Model.PointMap["Target"]}); err != nil {
						t.Fatal(err)
					}
					before := decision.CabinetStandby
					if event == "stop" {
						before = decision.CabinetRunning
					}
					if err := SetCabinetState(f.handler.nc, unit, before); err != nil {
						t.Fatal(err)
					}
					sim.mu.Lock()
					sim.writeMode = mode
					sim.mu.Unlock()
					eventURL := f.base + "/energy-units/" + unit + "/events"
					status, body := call(t, http.MethodPost, eventURL, f.token, map[string]string{"event": event, "request_id": "cabinet-unconfirmed"}, nil)
					if status != 200 {
						t.Fatalf("submit: %d %s", status, body)
					}
					want := "failed"
					if mode == "disconnect" {
						want = "unknown"
					}
					waitPumpCommand(t, f.base, f.token, bus, "cabinet-unconfirmed", want, 4*time.Second)
					wantProcess := decision.CabinetFault
					if maintenance {
						status, body = call(t, http.MethodPost, eventURL, f.token, map[string]string{"event": "enter_maintenance"}, nil)
						if status != 200 {
							t.Fatalf("maintenance: %d %s", status, body)
						}
						wantProcess = decision.CabinetMaintenance
					}
					f.handler.ReconcileCabinetCommands()
					f.handler.ReconcileCabinetCommands()
					node := f.node(t, unit)
					state := LoadCabinetState(node)
					_, pending := loadPendingCommand(node)
					writes := sim.written()
					var outcome pumpCommandOutcome
					status, body = call(t, http.MethodGet, f.base+"/modbus/"+bus+"/units/1/commands/cabinet-unconfirmed", f.token, nil, &outcome)
					t.Logf("%s/%s maintenance=%t: command=%s cabinet=%s pending=%t simulator_writes=%v", event, mode, maintenance, outcome.State, state, pending, writes)
					if status != 200 || outcome.State != want {
						t.Errorf("reconciliation changed canonical outcome: %d %s", status, body)
					}
					if pending || state != wantProcess {
						t.Errorf("terminal process state=%s pending=%t want %s", state, pending, wantProcess)
					}
					_, auditBody := call(t, http.MethodGet, f.base+"/audit", f.token, nil, nil)
					if !strings.Contains(auditBody, want) || !strings.Contains(auditBody, "实际状态未核实") || !strings.Contains(auditBody, "不代表已证实设备故障") {
						t.Error("unconfirmed result lacks explicit process-block explanation")
					}
					if len(writes) != 1 || writes[0] != [3]uint16{1, 10, 20} {
						t.Fatalf("unexpected physical attempts: %v", writes)
					}
				})
			}
		}
	}
}

// TestEnergyControlEvents 验证控制器人工操作的 HTTP 语义：人工模式立即停止
// 自动调度；检修/故障确认走状态机；没有命令运行时不假装启停成功。
func TestEnergyControlEvents(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	var users api.UserAuthority
	options := server.Options{
		ID: "easyems-energy-control", DataDir: dataDir, HTTPPort: "9820",
		NatsPort: 9821, NatsServer: "nats://127.0.0.1:9821", AuthToken: data.NewID(),
		DeviceAuth: server.DeviceAuthRequired, CustomUIFS: fstest.MapFS{"index.html": {Data: []byte("easyems")}},
		AppOnly: true, NatsLoopback: true, ProvisioningDisable: true,
		AppHandlerFactory: func(nc *nats.Conn, u api.UserAuthority) http.Handler {
			users = u
			return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
		},
	}
	siot, nc, err := server.NewServer(options)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- siot.Run() }()
	defer func() { siot.Stop(nil); <-done }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := siot.WaitStart(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	var root data.NodeEdge
	for {
		root, err = siotclient.GetRootNode(nc)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("store not ready")
		}
		time.Sleep(50 * time.Millisecond)
	}

	plantID, _ := CreateDomainNode(nc, root.ID, NodeTypePlant, map[string]string{PointTypeAssetCode: "P1"})
	unitID, _ := CreateDomainNode(nc, plantID, NodeTypeEnergyUnit, map[string]string{PointTypeAssetCode: "EU1"})
	handler := New(nc, users, nil)
	identity := Identity{UserID: "admin", Policy: AccessPolicy{Admin: true}, Anchors: []string{root.ID}}

	call := func(method, action string, body any) (int, map[string]any) {
		t.Helper()
		var reader *bytes.Reader
		if body != nil {
			encoded, _ := json.Marshal(body)
			reader = bytes.NewReader(encoded)
		} else {
			reader = bytes.NewReader(nil)
		}
		request := httptest.NewRequest(method, "/api/energy-units/"+unitID+"/"+action, reader)
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.energyControl(recorder, request, identity, unitID, action)
		var payload map[string]any
		_ = json.Unmarshal(recorder.Body.Bytes(), &payload)
		return recorder.Code, payload
	}
	state := func() string {
		nodes, _ := siotclient.GetNodes(nc, "all", unitID, "", false)
		return string(LoadCabinetState(nodes[0]))
	}

	if status, _ := call(http.MethodPut, "mode", map[string]string{"mode": "manual"}); status != http.StatusOK {
		t.Fatalf("manual mode = %d", status)
	}
	nodes, _ := siotclient.GetNodes(nc, "all", unitID, "", false)
	if AllowsEnergyUnitAutoDispatch(nodes[0]) {
		t.Fatal("manual mode must block auto dispatch")
	}
	if status, _ := call(http.MethodPut, "mode", map[string]string{"mode": "auto"}); status != http.StatusOK {
		t.Fatalf("auto mode = %d", status)
	}

	if status, payload := call(http.MethodPost, "events", map[string]string{"event": "enter_maintenance"}); status != http.StatusOK || payload["state"] != "maintenance" {
		t.Fatalf("enter maintenance = %d %v", status, payload)
	}
	if status, _ := call(http.MethodPost, "events", map[string]string{"event": "start", "request_id": "r-maintenance"}); status != http.StatusConflict {
		t.Fatalf("start during maintenance = %d, want conflict", status)
	}
	if status, _ := call(http.MethodPost, "events", map[string]string{"event": "exit_maintenance"}); status != http.StatusOK {
		t.Fatalf("exit maintenance = %d", status)
	}
	if state() != "standby" {
		t.Fatalf("state after maintenance = %s", state())
	}

	// 没有命令运行时，启停必须明确失败而不是假装成功。
	if status, payload := call(http.MethodPost, "events", map[string]string{"event": "start", "request_id": "r-start"}); status != http.StatusServiceUnavailable || payload["error"] != "command_runtime_unavailable" {
		t.Fatalf("start without runtime = %d %v", status, payload)
	}

	if err := SetCabinetState(nc, unitID, "fault"); err != nil {
		t.Fatal(err)
	}
	if status, _ := call(http.MethodPost, "events", map[string]string{"event": "fault_clear"}); status != http.StatusOK {
		t.Fatalf("fault clear = %d", status)
	}
	if state() != "standby" {
		t.Fatalf("state after fault clear = %s", state())
	}
}
