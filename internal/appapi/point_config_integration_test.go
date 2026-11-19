package appapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/simpleiot/simpleiot/api"
	"github.com/simpleiot/simpleiot/data"
	"github.com/simpleiot/simpleiot/server"

	"fastiot/internal/ems/bridge"
	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/runtime"
)

func TestPointConfigurationSaveDoesNotWriteDevice(t *testing.T) {
	f := newPointConfigFixture(t)
	busID := f.createBus(t, "Source")
	input := ModbusIOInput{
		Description: "Pressure", ServerID: 1, Address: 10,
		IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1,
	}
	pointID := f.createPoint(t, busID, input)
	f.cycle(t)
	waitFor(t, func() bool {
		p, ok := f.node(t, pointID).Points.Find(data.PointTypeValue, "")
		value, err := p.ValueFloat()
		return ok && err == nil && value == 37
	}, "first real fake-bus observation")
	// Subsequent configuration saves must not masquerade as fresh observations.
	f.bus.setOffline(true)
	f.writeValue(t, pointID, 73)
	f.cycle(t)
	if writes := f.bus.written(); len(writes) != 1 || writes[0] != 73 {
		t.Fatalf("explicit operation did not reach the bus: %v", writes)
	}
	before := f.node(t, pointID)

	input.Description = "Pressure renamed"
	input.Scale = 2
	status, body := call(t, http.MethodPut, f.base+"/modbus/io/"+pointID, f.token, input, nil)
	if status != http.StatusOK {
		t.Fatalf("save point configuration: %d %s", status, body)
	}
	for i := 0; i < 3; i++ {
		f.cycle(t)
	}
	if writes := f.bus.written(); len(writes) != 1 {
		t.Errorf("configuration save produced device writes: got %v, want only the explicit 73", writes)
	}
	after := f.node(t, pointID)
	for _, typ := range []string{data.PointTypeValue, data.PointTypeValueSet} {
		previous, had := before.Points.Find(typ, "")
		current, has := after.Points.Find(typ, "")
		previousValue, previousErr := previous.ValueFloat()
		currentValue, currentErr := current.ValueFloat()
		if !had || !has || previousErr != nil || currentErr != nil || currentValue != previousValue || !current.Time.Equal(previous.Time) {
			t.Errorf("configuration changed %s: before=%+v after=%+v", typ, previous, current)
		}
	}
	if name, _ := after.Points.Text(data.PointTypeDescription, ""); name != input.Description {
		t.Errorf("configuration itself was not saved: %q", name)
	}
	status, _ = call(t, http.MethodPut, f.base+"/modbus/io/"+pointID, f.token, struct {
		ModbusIOInput
		Value float64 `json:"value"`
	}{ModbusIOInput: input, Value: 999}, nil)
	if status != http.StatusBadRequest {
		t.Errorf("configuration API accepted an observation field: %d", status)
	}
}

func TestPointCreationDoesNotInventObservation(t *testing.T) {
	f := newPointConfigFixture(t)
	f.bus.setOffline(true)
	busID := f.createBus(t, "No observations yet")
	pointID := f.createPoint(t, busID, ModbusIOInput{
		Description: "Unobserved", ServerID: 1, Address: 10,
		IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1,
	})
	for i := 0; i < 3; i++ {
		f.cycle(t)
	}
	node := f.node(t, pointID)
	for _, typ := range []string{data.PointTypeValue, data.PointTypeValueSet} {
		if p, found := node.Points.Find(typ, ""); found {
			t.Errorf("unobserved point acquired %s: %+v", typ, p)
		}
	}
	var history struct {
		Points []data.Point `json:"points"`
	}
	status, body := call(t, http.MethodGet, f.base+"/history?node_id="+pointID+"&type=value&limit=10", f.token, nil, &history)
	if status != http.StatusOK || len(history.Points) != 0 {
		t.Errorf("new point has fabricated observation history: status=%d count=%d body=%s", status, len(history.Points), body)
	}
	if writes := f.bus.written(); len(writes) != 0 {
		t.Errorf("point creation wrote the device: %v", writes)
	}
}

func TestPointTemplateAndCSVOnlyCopyConfiguration(t *testing.T) {
	f := newPointConfigFixture(t)
	sourceBus := f.createBus(t, "Source template")
	sourcePoint := f.createPoint(t, sourceBus, ModbusIOInput{
		Description: "Pressure", ServerID: 1, Address: 10,
		IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1, Units: "bar",
	})
	f.cycle(t)
	waitFor(t, func() bool {
		p, ok := f.node(t, sourcePoint).Points.Find(data.PointTypeValue, "")
		value, err := p.ValueFloat()
		return ok && err == nil && value == 37
	}, "source template observation")
	f.bus.setOffline(true)
	f.writeValue(t, sourcePoint, 73)
	f.cycle(t)
	before := f.node(t, sourcePoint)
	var template struct {
		ID string `json:"id"`
	}
	status, body := call(t, http.MethodPost, f.base+"/templates", f.token, map[string]string{
		"parent": snapshotRoot(t, f.base, f.token), "description": "Pressure template", "source_bus_id": sourceBus,
	}, &template)
	if status != http.StatusOK {
		t.Fatalf("save template: %d %s", status, body)
	}
	const csv = "名称,站号,地址,类型,数据格式,只读,缩放,偏移,单位\nPressure,1,10,保持寄存器,uint16,否,2,0,bar\n"
	for _, method := range []string{"template", "csv"} {
		t.Run(method, func(t *testing.T) {
			busID := f.createBus(t, method+" destination")
			var status int
			var body string
			if method == "template" {
				status, body = call(t, http.MethodPost, f.base+"/templates/"+template.ID+"/apply", f.token,
					map[string]string{"bus_id": busID}, nil)
			} else {
				status, body = callRaw(t, f.base+"/modbus/"+busID+"/io/import", f.token, csv)
			}
			if status != http.StatusOK {
				t.Fatalf("copy configuration: %d %s", status, body)
			}
			f.cycle(t)
			count := 0
			for _, node := range snapshotNodes(t, f.base, f.token) {
				if node.Parent != busID || node.Type != data.NodeTypeModbusIO {
					continue
				}
				count++
				if node.ID == sourcePoint {
					t.Error("copy reused source identity")
				}
				for _, typ := range []string{data.PointTypeValue, data.PointTypeValueSet} {
					if _, found := node.Points.Find(typ, ""); found {
						t.Errorf("copy inherited or fabricated %s", typ)
					}
				}
				var history struct {
					Points []data.Point `json:"points"`
				}
				status, _ := call(t, http.MethodGet, f.base+"/history?node_id="+node.ID+"&type=value", f.token, nil, &history)
				if status != http.StatusOK || len(history.Points) != 0 {
					t.Errorf("copy has observation history: status=%d count=%d", status, len(history.Points))
				}
			}
			if count != 1 {
				t.Fatalf("copied point count = %d, want 1", count)
			}
		})
	}
	t.Run("csv-update", func(t *testing.T) {
		status, body := callRaw(t, f.base+"/modbus/"+sourceBus+"/io/import?apply=diff", f.token, csv)
		if status != http.StatusOK {
			t.Fatalf("apply CSV configuration: %d %s", status, body)
		}
		f.cycle(t)
		after := f.node(t, sourcePoint)
		for _, typ := range []string{data.PointTypeValue, data.PointTypeValueSet} {
			previous, had := before.Points.Find(typ, "")
			current, has := after.Points.Find(typ, "")
			prevValue, prevErr := previous.ValueFloat()
			curValue, curErr := current.ValueFloat()
			if !had || !has || prevErr != nil || curErr != nil || curValue != prevValue || !current.Time.Equal(previous.Time) {
				t.Errorf("CSV edit changed %s", typ)
			}
		}
	})
	if writes := f.bus.written(); len(writes) != 1 || writes[0] != 73 {
		t.Errorf("template/CSV operations wrote the bus: %v", writes)
	}
}

// The only fake is the physical protocol adapter. HTTP, identity, persistence,
// NodeSource and the product Bridge are real and isolated to this test.
type pointConfigBus struct {
	mu      sync.Mutex
	offline bool
	writes  []uint16
}

func (b *pointConfigBus) ReadCoils(byte, uint16, uint16) ([]bool, error) {
	return nil, errors.New("unused bit area")
}
func (b *pointConfigBus) ReadDiscreteInputs(byte, uint16, uint16) ([]bool, error) {
	return nil, errors.New("unused bit area")
}
func (b *pointConfigBus) ReadInputRegisters(id byte, address, count uint16) ([]uint16, error) {
	return b.ReadHoldingRegisters(id, address, count)
}
func (b *pointConfigBus) ReadHoldingRegisters(_ byte, _ uint16, count uint16) ([]uint16, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.offline {
		return nil, errors.New("simulated unavailable observation")
	}
	values := make([]uint16, count)
	for i := range values {
		values[i] = 37
	}
	return values, nil
}
func (b *pointConfigBus) WriteCoil(id byte, address uint16, value bool) error {
	var number uint16
	if value {
		number = 1
	}
	return b.WriteHoldingRegister(id, address, number)
}
func (b *pointConfigBus) WriteHoldingRegister(_ byte, _ uint16, value uint16) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.writes = append(b.writes, value)
	return nil
}
func (b *pointConfigBus) setOffline(value bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.offline = value
}
func (b *pointConfigBus) written() []uint16 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]uint16(nil), b.writes...)
}

type pointConfigFixture struct {
	base, token string
	bridge      *bridge.Bridge
	runtime     *runtime.Service
	bus         *pointConfigBus
	handler     *Handler
	dir         string
}

func newPointConfigFixture(t *testing.T) *pointConfigFixture {
	return newPointConfigFixtureAt(t, filepath.Join(t.TempDir(), "data"))
}

func newPointConfigFixtureAt(t *testing.T, dataDir string) *pointConfigFixture {
	t.Helper()
	password := data.NewID()
	t.Setenv("FASTEMS_ADMIN_PASSWORD", password)
	t.Setenv("FASTEMS_SEED_DEMO", "0")
	ports := make([]string, 2)
	listeners := make([]net.Listener, 0, 2)
	for i := range ports {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		listeners = append(listeners, listener)
		ports[i] = strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	}
	for _, listener := range listeners {
		_ = listener.Close()
	}
	natsPort, _ := strconv.Atoi(ports[1])
	ready := new(atomic.Bool)
	var handler *Handler
	siot, nc, err := server.NewServer(server.Options{
		ID: "point-configuration-test", DataDir: dataDir, HTTPPort: ports[0],
		NatsPort: natsPort, NatsServer: "nats://127.0.0.1:" + ports[1], AuthToken: data.NewID(),
		DeviceAuth: server.DeviceAuthRequired, CustomUIFS: fstest.MapFS{"index.html": {Data: []byte("test")}},
		AppOnly: true, NatsLoopback: true, ProvisioningDisable: true,
		AppHandlerFactory: func(nc *nats.Conn, users api.UserAuthority) http.Handler {
			handler = New(nc, users, ready)
			return handler
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- siot.Run() }()
	t.Cleanup(func() { siot.Stop(nil); <-done })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := siot.WaitStart(ctx); err != nil {
		t.Fatal(err)
	}
	if err := Bootstrap(nc, dataDir); err != nil {
		t.Fatal(err)
	}
	ready.Store(true)
	base := "http://127.0.0.1:" + ports[0] + "/api"
	waitFor(t, func() bool {
		response, err := http.Get(base + "/health")
		if err != nil {
			return false
		}
		response.Body.Close()
		return response.StatusCode == http.StatusOK
	}, "point configuration HTTP readiness")
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	t.Cleanup(service.Close)
	bus := &pointConfigBus{}
	f := &pointConfigFixture{base: base, token: loginAPI(t, base, "admin", password), runtime: service, bus: bus, handler: handler, dir: dataDir}
	f.bridge = bridge.New(service, bridge.NewNodeSource(nc), func(string, string, time.Duration) (channel.Bus, error) { return bus, nil })
	return f
}

func (f *pointConfigFixture) createBus(t *testing.T, name string) string {
	t.Helper()
	return f.createBusPeriod(t, name, 60000)
}

func (f *pointConfigFixture) createBusPeriod(t *testing.T, name string, period int) string {
	t.Helper()
	var result struct {
		ID string `json:"id"`
	}
	status, body := call(t, http.MethodPost, f.base+"/modbus", f.token, ModbusConnectionInput{
		Parent: snapshotRoot(t, f.base, f.token), Description: name,
		Protocol: data.PointValueTCP, URI: "127.0.0.1:1502", PollPeriod: period, Timeout: 1000,
	}, &result)
	if status != http.StatusOK {
		t.Fatalf("create bus: %d %s", status, body)
	}
	return result.ID
}
func (f *pointConfigFixture) createPoint(t *testing.T, busID string, input ModbusIOInput) string {
	t.Helper()
	var result struct {
		ID string `json:"id"`
	}
	status, body := call(t, http.MethodPost, f.base+"/modbus/"+busID+"/io", f.token, input, &result)
	if status != http.StatusOK {
		t.Fatalf("create point: %d %s", status, body)
	}
	return result.ID
}
func (f *pointConfigFixture) node(t *testing.T, id string) data.NodeEdge {
	t.Helper()
	for _, node := range snapshotNodes(t, f.base, f.token) {
		if node.ID == id {
			return node
		}
	}
	t.Fatal("point missing from snapshot")
	return data.NodeEdge{}
}
func (f *pointConfigFixture) cycle(t *testing.T) {
	t.Helper()
	if err := f.bridge.Sync(time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, ch := range f.runtime.Channels() {
		ch.RunOnce(context.Background(), time.Now)
	}
}
func (f *pointConfigFixture) writeValue(t *testing.T, id string, value float64) {
	t.Helper()
	status, body := call(t, http.MethodPost, f.base+"/nodes/"+id+"/value", f.token,
		map[string]any{"value": value, "request_id": data.NewID()}, nil)
	if status != http.StatusOK {
		t.Fatal(fmt.Sprintf("explicit operation: %d %s", status, body))
	}
}
