package appapi

import (
	"context"
	"encoding/binary"
	"fastiot/internal/audit"
	"fastiot/internal/ems/command"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/ems/signal"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"fastiot/internal/ems/bridge"
	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/modbusrt"
	"fastiot/internal/emsapi"
)

// This private Modbus station records requests at the wire boundary, not the
// dispatcher. Only station 1/2, holding register 10 exist. No physical addresses.
type operationSimulator struct {
	mu       sync.Mutex
	values   map[byte]uint16
	writes   [][3]uint16
	listener net.Listener
	conns    []net.Conn
	writeMode string
	readFailure bool
}

func newOperationSimulator(t *testing.T) *operationSimulator {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:18108")
	if err != nil {
		t.Fatal(err)
	}
	s := &operationSimulator{values: map[byte]uint16{1: 10, 2: 10}, listener: listener}
	t.Cleanup(func() {
		listener.Close()
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, c := range s.conns {
			c.Close()
		}
	})
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.conns = append(s.conns, c)
			s.mu.Unlock()
			go s.serve(c)
		}
	}()
	return s
}
func (s *operationSimulator) serve(c net.Conn) {
	defer c.Close()
	for {
		h := make([]byte, 7)
		if _, err := io.ReadFull(c, h); err != nil {
			return
		}
		n := int(binary.BigEndian.Uint16(h[4:6]))
		if n < 2 || n > 254 {
			return
		}
		p := make([]byte, n-1)
		if _, err := io.ReadFull(c, p); err != nil {
			return
		}
		response := []byte{p[0] | 0x80, 2}
		s.mu.Lock()
		if len(p) == 5 && (h[6] == 1 || h[6] == 2) && binary.BigEndian.Uint16(p[1:3]) == 10 {
			switch p[0] {
			case 3:
				if binary.BigEndian.Uint16(p[3:5]) == 1 && !s.readFailure {
					response = []byte{3, 2, byte(s.values[h[6]] >> 8), byte(s.values[h[6]])}
				}
			case 6:
				v := binary.BigEndian.Uint16(p[3:5])
				s.writes = append(s.writes, [3]uint16{uint16(h[6]), 10, v})
				if s.writeMode != "wrong" && s.writeMode != "reject" { s.values[h[6]] = v }
				response = p
				if s.writeMode == "missing" || s.writeMode == "disconnect" { s.readFailure = true }
				if s.writeMode == "reject" { response = []byte{0x86,4} }
				if s.writeMode == "disconnect" { s.mu.Unlock(); return }
			}
		}
		s.mu.Unlock()
		binary.BigEndian.PutUint16(h[4:6], uint16(len(response)+1))
		if _, err := c.Write(append(h, response...)); err != nil {
			return
		}
	}
}
func (s *operationSimulator) written() [][3]uint16 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][3]uint16(nil), s.writes...)
}

const operationProfile = `{"id":"operation-sim","vendor":"Isolated simulator","model":"Declared operation","protocol":"modbus","point_specs":[{"name":"Target","function":"holdingRegister","address":10,"format":"uint16","scale":1,"read_only":false}],"command_specs":[{"id":"set","name":"设置目标","timeout_ms":1000,"parameters":[{"name":"target","label":"目标","point":"Target","kind":"number","has_range":true,"min":0,"max":100,"default":10}]}]}`

func operationFixture(t *testing.T) (*pointConfigFixture, string, *operationSimulator) {
	t.Helper()
	f := newPointConfigFixture(t)
	f.handler.SetAudit(audit.New(filepath.Join(f.dir, "operations-audit.jsonl")))
	f.handler.SetEMS(emsapi.New(f.runtime, time.Second))
	loadSingleDeviceProfile(t, f.handler, operationProfile)
	sim := newOperationSimulator(t)
	f.bridge = bridge.New(f.runtime, bridge.NewNodeSource(f.handler.nc), func(protocol, address string, timeout time.Duration) (channel.Bus, error) {
		if protocol != "tcp" || address != "127.0.0.1:18108" {
			return nil, fmt.Errorf("non-fixture address refused")
		}
		return modbusrt.OpenTCP(address, timeout)
	})
	root := snapshotRoot(t, f.base, f.token)
	site := createNodeAPI(t, f.base, f.token, "site", root, "Operation site", 0)
	var bus struct {
		ID string `json:"id"`
	}
	status, body := call(t, http.MethodPost, f.base+"/modbus", f.token, ModbusConnectionInput{Parent: site, Description: "Private command simulator", Protocol: "TCP", URI: "127.0.0.1:18108", PollPeriod: 50, Timeout: 100}, &bus)
	if status != 200 {
		t.Fatalf("bus: %d %s", status, body)
	}
	for server := 1; server <= 2; server++ {
		if server == 1 {
			business := createNodeAPI(t, f.base, f.token, "device", site, "操作模拟设备", 0)
			input := map[string]any{"profile_id": "operation-sim", "servers": []int{1}, "business_node_id": business, "create_points": true, "dry_run": true}
			var preview struct {
				Version string `json:"preview_version"`
			}
			status, body = call(t, http.MethodPost, f.base+"/modbus/"+bus.ID+"/units/apply", f.token, input, &preview)
			if status != 200 {
				t.Fatalf("preview: %d %s", status, body)
			}
			input["dry_run"] = false
			input["preview_version"] = preview.Version
			status, body = call(t, http.MethodPost, f.base+"/modbus/"+bus.ID+"/units/apply", f.token, input, nil)
			if status != 200 {
				t.Fatalf("bind: %d %s", status, body)
			}
			continue
		}
		point := f.createPoint(t, bus.ID, ModbusIOInput{Description: "Target", ServerID: server, Address: 10, IOType: "modbusHoldingRegister", DataFormat: "uint16", Scale: 1})
		status, body = call(t, http.MethodPut, fmt.Sprintf("%s/modbus/%s/units/%d/model", f.base, bus.ID, server), f.token, unitModelConfig{Name: "Command simulator", ProfileID: "operation-sim", PointMap: map[string]string{"Target": point}}, nil)
		if status != 200 {
			t.Fatalf("model: %d %s", status, body)
		}
	}
	if err := f.bridge.Sync(time.Now()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = f.runtime.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, func() bool { return f.runtime.State.Health(bus.ID+"/1") == "healthy" }, "private simulator first sample")
	WireCommandHistory(f.dir, f.runtime.Commands())
	return f, bus.ID, sim
}

func TestDeviceOperationRejectsBypasses(t *testing.T) {
	f, bus, sim := operationFixture(t)
	url := f.base + "/modbus/" + bus + "/units/1"
	var model unitModelView
	call(t, http.MethodGet, url+"/model", f.token, nil, &model)
	point := model.Model.PointMap["Target"]
	viewer := inputAccount(t, f, snapshotRoot(t, f.base, f.token), "viewer", nil)
	for _, tc := range []struct {
		name, path, token string
		body              any
		want              int
	}{
		{"viewer", url + "/commands", viewer, map[string]any{"request_id": "viewer", "command_id": "set", "parameters": map[string]float64{"target": 20}}, 403},
		{"undeclared", url + "/commands", f.token, map[string]any{"request_id": "undeclared", "command_id": "start"}, 400},
		{"range", url + "/commands", f.token, map[string]any{"request_id": "range", "command_id": "set", "parameters": map[string]float64{"target": 101}}, 409},
		{"type", url + "/commands", f.token, map[string]any{"request_id": "type", "command_id": "set", "parameters": map[string]string{"target": "20"}}, 400},
		{"unknown parameter", url + "/commands", f.token, map[string]any{"request_id": "extra", "command_id": "set", "parameters": map[string]float64{"other": 20}}, 409},
		{"generic bypass", f.base + "/nodes/" + point + "/value", f.token, map[string]any{"request_id": "bypass", "value": 101}, 409},
	} {
		status, body := call(t, http.MethodPost, tc.path, tc.token, tc.body, nil)
		if status != tc.want {
			t.Errorf("%s: %d %s want %d", tc.name, status, body, tc.want)
		}
	}
	if writes := sim.written(); len(writes) != 0 {
		t.Fatalf("unauthorized/undeclared writes reached simulator: %v", writes)
	}
}

func TestDeviceOperationBusinessScope(t *testing.T) {
	f, bus, _ := operationFixture(t)
	limited := inputAccount(t, f, bus, "operator", nil)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		status, body := call(t, method, f.base+"/modbus/"+bus+"/units/1/commands", limited, map[string]any{"request_id": "foreign", "command_id": "set"}, nil)
		if status != 403 {
			t.Errorf("business scope %s: %d %s", method, status, body)
		}
	}
}

func TestDeviceOperationManualModeAndSafety(t *testing.T) {
	f, bus, sim := operationFixture(t)
	url := f.base + "/modbus/" + bus + "/units/1"
	var model unitModelView
	call(t, http.MethodGet, url+"/model", f.token, nil, &model)
	point := model.Model.PointMap["Target"]
	commandOnly := inputAccount(t, f, snapshotRoot(t, f.base, f.token), "operator", []string{ActionCommand})
	status, _ := call(t, http.MethodPost, f.base+"/nodes/"+point+"/manual", commandOnly, map[string]bool{"manual": true}, nil)
	if status != 403 {
		t.Fatalf("command permission granted mode change: %d", status)
	}
	for _, manual := range []bool{true, false} {
		status, body := call(t, http.MethodPost, f.base+"/nodes/"+point+"/manual", f.token, map[string]bool{"manual": manual}, nil)
		if status != 200 {
			t.Fatalf("mode: %d %s", status, body)
		}
		got, err := f.runtime.Commands().Submit(time.Now(), command.Request{ID: fmt.Sprintf("auto-%t", manual), DeviceID: bus + "/1", CommandID: "set", Actor: "project-authority", Source: "rule", Parameters: map[string]float64{"target": 21}})
		if err != nil {
			t.Fatal(err)
		}
		if manual && (got.State != command.Rejected || len(sim.written()) != 0) {
			t.Fatalf("takeover did not block automatic output: %+v", got)
		}
		if !manual {
			waitPumpCommand(t, f.base, f.token, bus, "auto-false", "confirmed", 3*time.Second)
		}
	}
	_, body := call(t, http.MethodGet, f.base+"/audit", f.token, nil, nil)
	if !strings.Contains(body, "人工接管") || !strings.Contains(body, "恢复自动") {
		t.Errorf("mode audit not explicit: %s", body)
	}
	status, body = call(t, http.MethodPut, f.base+"/safety", f.token, map[string]any{"node": point, "safety": map[string]any{"conditions": []any{map[string]any{"point_id": point, "operator": "eq", "value": 99}}}}, nil)
	if status != 200 {
		t.Fatalf("safety: %d %s", status, body)
	}
	var rejected pumpCommandOutcome
	status, body = call(t, http.MethodPost, url+"/commands", f.token, map[string]any{"request_id": "unsafe", "command_id": "set"}, &rejected)
	if status != 200 || rejected.State != "rejected" {
		t.Fatalf("interlock: %d %s", status, body)
	}
	call(t, http.MethodGet, url+"/model", f.token, nil, &model)
	if model.Commands[0].Available {
		t.Error("interlocked command still offered as available")
	}
	if writes := sim.written(); len(writes) != 1 || writes[0] != [3]uint16{1, 10, 21} {
		t.Fatalf("unexpected writes: %v", writes)
	}
}

func TestDeviceOperationUncertainReadbackNeverRetries(t *testing.T) {
 for _,mode:=range []string{"missing","wrong","disconnect","reject"} { t.Run(mode,func(t *testing.T){
  f,bus,sim:=operationFixture(t)
  sim.mu.Lock();sim.writeMode=mode;sim.mu.Unlock()
  url:=f.base+"/modbus/"+bus+"/units/1/commands"
  target:=25.0; if mode=="missing" {target=10} // old matching feedback is not new evidence
  input:=map[string]any{"request_id":"fault","command_id":"set","parameters":map[string]float64{"target":target}}
  status,body:=call(t,http.MethodPost,url,f.token,input,nil); if status!=200 {t.Fatalf("submit: %d %s",status,body)}
  want:="unknown";if mode=="reject"{want="failed"}
  waitPumpCommand(t,f.base,f.token,bus,"fault",want,4*time.Second)
  for i:=0;i<3;i++ {status,body=call(t,http.MethodPost,url,f.token,input,nil);if status!=200 || !strings.Contains(body,want){t.Fatalf("retry lost canonical result: %d %s",status,body)}}
  if writes:=sim.written();len(writes)!=1 || writes[0]!=[3]uint16{1,10,uint16(target)} {t.Fatalf("fault retried physical action: %v",writes)}
  // Restarting just the command owner restores the same unknown/failure and
  // cannot replay an accepted journal intent, even with the device unavailable.
  restored:=runtime.NewService(nil,time.Minute,time.Second,100);t.Cleanup(restored.Close)
  ems:=emsapi.New(restored,time.Minute);ems.Profiles=f.handler.ems.Profiles;f.handler.SetEMS(ems);WireCommandHistory(f.dir,restored.Commands())
  status,body=call(t,http.MethodPost,url,f.token,input,nil);if status!=200 || !strings.Contains(body,want){t.Fatalf("restored fault changed outcome: %d %s",status,body)}
  if len(sim.written())!=1 {t.Fatal("restart replayed physical action")}
 }) }
}

func TestDeviceOperationConcurrentRetryAndDurableFailure(t *testing.T) {
 f,bus,sim:=operationFixture(t)
 url:=f.base+"/modbus/"+bus+"/units/1/commands"
 input:=map[string]any{"request_id":"concurrent","command_id":"set","parameters":map[string]float64{"target":23}}
 var wg sync.WaitGroup
 for i:=0;i<8;i++ {wg.Add(1);go func(){defer wg.Done(); status,body:=call(t,http.MethodPost,url,f.token,input,nil);if status!=200 {t.Errorf("concurrent submit: %d %s",status,body)}}()}
 wg.Wait();waitPumpCommand(t,f.base,f.token,bus,"concurrent","confirmed",3*time.Second)
 for i:=0;i<501;i++ {id:=fmt.Sprintf("fixture-history-%d",i); _,err:=f.runtime.Commands().Track(time.Now(),command.Request{ID:id,DeviceID:"fixture",Point:"no-physical-point",Value:signal.Num(0),Actor:"fixture"});if err!=nil {t.Fatal(err)}; f.runtime.Commands().SetState(time.Now(),id,command.Confirmed,"")}
 restarted:=runtime.NewService(nil,time.Minute,time.Second,100);t.Cleanup(restarted.Close)
 restoredHandler:=emsapi.New(restarted,time.Minute);restoredHandler.Profiles=f.handler.ems.Profiles
 f.handler.SetEMS(restoredHandler);WireCommandHistory(f.dir,restarted.Commands())
 status,body:=call(t,http.MethodPost,url,f.token,input,nil)
 if status!=200 || !strings.Contains(body,"confirmed") {t.Fatalf("cold persisted retry: %d %s",status,body)}
 call(t,http.MethodGet,url+"/concurrent",f.token,nil,nil)
 log:=filepath.Join(f.dir,commandLogFile)
 if err:=os.Rename(log,log+".saved");err!=nil {t.Fatal(err)}
 if err:=os.Mkdir(log,0700);err!=nil {t.Fatal(err)}
 input["request_id"]="storage-unavailable"
 status,body=call(t,http.MethodPost,url,f.token,input,nil)
 if status<400 {t.Fatalf("unpersisted command accepted: %d %s",status,body)}
 if writes:=sim.written();len(writes)!=1 || writes[0]!=[3]uint16{1,10,23} {t.Fatalf("concurrency/restart/storage failure duplicated physical write: %v",writes)}
}

func TestDeviceOperationRequestScope(t *testing.T) {
	f, bus, sim := operationFixture(t)
	other := inputAccount(t, f, snapshotRoot(t, f.base, f.token), "operator", nil)
	url := f.base + "/modbus/" + bus + "/units/1/commands"
	input := map[string]any{"request_id": "scope", "command_id": "set", "parameters": map[string]float64{"target": 20}}
	for i := 0; i < 2; i++ {
		status, body := call(t, http.MethodPost, url, f.token, input, nil)
		if status != 200 {
			t.Fatalf("submit/retry: %d %s", status, body)
		}
	}
	waitPumpCommand(t, f.base, f.token, bus, "scope", "confirmed", 3*time.Second)
	var evidence struct {
		Deadline time.Time `json:"deadline"`
		Observed []struct {
			Point string    `json:"point"`
			Value string    `json:"value"`
			At    time.Time `json:"at"`
		} `json:"observed"`
	}
	call(t, http.MethodGet, url+"/scope", f.token, nil, &evidence)
	if evidence.Deadline.IsZero() || len(evidence.Observed) != 1 || evidence.Observed[0].Value != "20" || evidence.Observed[0].At.After(evidence.Deadline) {
		original, _ := f.runtime.Commands().Get("scope")
		t.Logf("[DEBUG-ticket07-time] deadline=%v observed=%+v started=%v", original.Deadline, original.Observed, original.StartedAt)
		t.Errorf("canonical readback evidence missing: %+v", evidence)
	}
	for _, tc := range []struct {
		name, url, token string
		params           map[string]any
	}{
		{"actor", url, other, input},
		{"target", f.base + "/modbus/" + bus + "/units/2/commands", f.token, input},
		{"params", url, f.token, map[string]any{"request_id": "scope", "command_id": "set", "parameters": map[string]float64{"target": 30}}},
	} {
		status, body := call(t, http.MethodPost, tc.url, tc.token, tc.params, nil)
		if status != 409 {
			t.Errorf("%s reuse: %d %s; want 409", tc.name, status, body)
		}
	}
	if writes := sim.written(); len(writes) != 1 || writes[0] != [3]uint16{1, 10, 20} {
		t.Fatalf("simulator received %v; want only declared authorized write", writes)
	}
}
