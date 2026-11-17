package appapi

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"fastiot/internal/calculation"
	"fastiot/internal/ems/bridge"
	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/ems/signal"
	"fastiot/internal/emsapi"
	"github.com/simpleiot/simpleiot/data"
	"strings"
)

type calculationReadBus struct {
	*pointConfigBus
	valuesMu sync.Mutex
	values   map[byte]uint16
	fail     map[byte]bool
}

func (b *calculationReadBus) ReadHoldingRegisters(server byte, _ uint16, count uint16) ([]uint16, error) {
	b.valuesMu.Lock()
	defer b.valuesMu.Unlock()
	if b.fail[server] {
		return nil, errors.New("isolated read failure")
	}
	out := make([]uint16, count)
	for i := range out {
		out[i] = b.values[server]
	}
	return out, nil
}
func startCalculationLoop(t *testing.T, h *Handler) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = h.RunCalculations(ctx); close(done) }()
	stop := func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("calculation loop did not stop")
		}
	}
	t.Cleanup(stop)
	return stop
}

func TestCalculationCaptureSerializesPhysicalConfiguration(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	source := bridge.NewNodeSource(h.nc)
	api := emsapi.New(service, time.Minute)
	api.Buses = source.Buses
	h.SetEMS(api)
	f := &pointConfigFixture{base: base, token: token}
	bus := f.createBus(t, "capture-source")
	input := ModbusIOInput{Description: "P", ServerID: 1, Address: 10, IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1, ReadOnly: true, Units: "kW"}
	point := f.createPoint(t, bus, input)
	configs, err := source.Buses()
	if err != nil {
		t.Fatal(err)
	}
	ch, err := channel.New(configs[0].ChannelID, &pointConfigBus{}, configs[0].Devices)
	if err != nil {
		t.Fatal(err)
	}
	readings, _ := ch.RunOnce(context.Background(), time.Now)
	service.Observe(time.Now(), readings)
	var result calcResponse
	status, body := call(t, http.MethodPost, base+"/calculations", token, map[string]any{"parent": snapshotRoot(t, base, token), "name": "double", "operation_id": "create", "definition": calculation.Definition{Operator: "add", Inputs: []string{point, point}, Kind: "number", Unit: "kW"}}, &result)
	if status != 200 {
		t.Fatal(body)
	}
	status, body = call(t, http.MethodPost, base+"/calculations/"+result.ID+"/apply", token, map[string]any{"revision": result.Revision, "operation_id": "apply"}, &result)
	if status != 200 {
		t.Fatal(body)
	}
	stop := startCalculationLoop(t, h)
	waitFor(t, func() bool { return h.CalculationRuntime().Snapshot().Results[result.ID].Quality == "good" }, "initial observation")
	stop()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	api.Buses = func() ([]bridge.BusConfig, error) {
		cfg, err := source.Buses()
		once.Do(func() { close(entered); <-release })
		return cfg, err
	}
	stop = startCalculationLoop(t, h)
	<-entered
	completed := make(chan int, 1)
	input.Address = 11
	go func() {
		status, _ := call(t, http.MethodPut, base+"/modbus/io/"+point, token, input, nil)
		completed <- status
	}()
	early := false
	select {
	case status = <-completed:
		early = true
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if !early {
		select {
		case status = <-completed:
		case <-time.After(2 * time.Second):
			t.Fatal("configuration edit stuck")
		}
	}
	stop()
	if early {
		t.Fatal("physical edit completed inside an older configuration capture")
	}
	if status != 200 {
		t.Fatalf("edit status %d", status)
	}
	if r := h.CalculationRuntime().Snapshot().Results[result.ID]; r.Value != nil || r.Quality == "good" {
		t.Fatal("completed configuration edit left old current value usable")
	}
}

func TestCalculationRestartDoesNotUseStoredPhysicalValues(t *testing.T) {
	dir, password := t.TempDir(), data.NewID()
	base, token, h, stop := startInputServer(t, dir, password)
	attach := func(handler *Handler) (*runtime.Service, *bridge.NodeSource) {
		s := runtime.NewService(nil, time.Minute, time.Second, 100)
		source := bridge.NewNodeSource(handler.nc)
		api := emsapi.New(s, time.Minute)
		api.Buses = source.Buses
		handler.SetEMS(api)
		return s, source
	}
	service, source := attach(h)
	fake := &pointConfigBus{}
	b := bridge.New(service, source, func(string, string, time.Duration) (channel.Bus, error) { return fake, nil })
	f := &pointConfigFixture{base: base, token: token, bridge: b, runtime: service, bus: fake}
	busID := f.createBus(t, "restart-source")
	pointID := f.createPoint(t, busID, ModbusIOInput{Description: "P", ServerID: 1, Address: 10, IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1, ReadOnly: true, Units: "kW"})
	cfg, err := source.Buses()
	if err != nil {
		t.Fatal(err)
	}
	ch, err := channel.New(cfg[0].ChannelID, fake, cfg[0].Devices)
	if err != nil {
		t.Fatal(err)
	}
	values, _ := ch.RunOnce(context.Background(), time.Now)
	service.Observe(time.Now(), values)
	var result calcResponse
	status, body := call(t, http.MethodPost, base+"/calculations", token, map[string]any{"parent": snapshotRoot(t, base, token), "name": "double-P", "operation_id": "new", "definition": calculation.Definition{Operator: "add", Inputs: []string{pointID, pointID}, Kind: "number", Unit: "kW"}}, &result)
	if status != 200 {
		t.Fatalf("create: %d %s", status, body)
	}
	status, body = call(t, http.MethodPost, base+"/calculations/"+result.ID+"/apply", token, map[string]any{"revision": result.Revision, "operation_id": "apply"}, &result)
	if status != 200 {
		t.Fatalf("apply: %d %s", status, body)
	}
	id := result.ID
	stopLoop := startCalculationLoop(t, h)
	waitFor(t, func() bool {
		r := h.CalculationRuntime().Snapshot().Results[id]
		return r.Value != nil && *r.Value == 74
	}, "initial actual observation")
	stopLoop()
	service.Close()
	stop()
	base, token, h, _ = startInputServer(t, dir, password)
	service, _ = attach(h)
	defer service.Close()
	startCalculationLoop(t, h)
	waitFor(t, func() bool {
		r, ok := h.CalculationRuntime().Snapshot().Results[id]
		return ok && r.Value == nil && r.Quality != "good"
	}, "restart waiting for new observation")
	f.base, f.token = base, token
	stored := f.node(t, pointID)
	value, ok := stored.Points.Value(data.PointTypeValue, "")
	if !ok || value != 37 {
		t.Fatal("test lost the persisted observation that must not be replayed")
	}
	status, body = call(t, http.MethodGet, base+"/calculations/"+id, token, nil, &result)
	if status != 200 || result.State.Value != nil || result.State.Quality == "good" {
		t.Fatalf("restart replayed a stored physical value: %d %s", status, body)
	}
}

func TestCalculationLoopUpdatesWithoutResultReads(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, 500*time.Millisecond, time.Second, 100)
	api := emsapi.New(service, 500*time.Millisecond)
	h.SetEMS(api)
	bus := &calculationReadBus{pointConfigBus: &pointConfigBus{}, values: map[byte]uint16{1: 20, 2: 30}}
	source := bridge.NewNodeSource(h.nc)
	api.Buses = source.Buses
	b := bridge.New(service, source, func(string, string, time.Duration) (channel.Bus, error) { return bus, nil })
	b.Interval = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	bridgeDone, calcDone := make(chan error, 1), make(chan error, 1)
	go func() { bridgeDone <- b.Run(ctx) }()
	go func() { calcDone <- h.RunCalculations(ctx) }()
	t.Cleanup(func() {
		cancel()
		service.Close()
		select {
		case <-bridgeDone:
		case <-time.After(2 * time.Second):
			t.Error("bridge did not stop")
		}
		select {
		case <-calcDone:
		case <-time.After(2 * time.Second):
			t.Error("calculation loop did not stop")
		}
	})
	f := &pointConfigFixture{base: base, token: token, bridge: b, runtime: service, bus: bus.pointConfigBus}
	root := snapshotRoot(t, base, token)
	var connection struct {
		ID string `json:"id"`
	}
	status, body := call(t, http.MethodPost, base+"/modbus", token, ModbusConnectionInput{Parent: root, Description: "isolated", Protocol: data.PointValueTCP, URI: "127.0.0.1:1502", PollPeriod: 100, Timeout: 100}, &connection)
	if status != 200 {
		t.Fatalf("create connection: %d %s", status, body)
	}
	a := f.createPoint(t, connection.ID, ModbusIOInput{Description: "A", ServerID: 1, Address: 10, IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1, ReadOnly: true, Units: "kW"})
	pointB := f.createPoint(t, connection.ID, ModbusIOInput{Description: "B", ServerID: 2, Address: 10, IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1, ReadOnly: true, Units: "kW"})
	capacity := virtualCapacity("100")
	capacity.Unit = "kW"
	var input inputResponse
	status, body = call(t, http.MethodPost, base+"/virtual-inputs", token, map[string]any{"parent": root, "name": "capacity", "source": "configuration", "definition": capacity, "operation_id": "capacity"}, &input)
	if status != 200 {
		t.Fatalf("create capacity: %d %s", status, body)
	}
	status, body = call(t, http.MethodPost, base+"/virtual-inputs/"+input.ID+"/apply", token, map[string]any{"revision": input.Revision, "operation_id": "apply-capacity"}, &input)
	if status != 200 {
		t.Fatalf("apply capacity: %d %s", status, body)
	}
	create := func(name string, d calculation.Definition) string {
		t.Helper()
		var out calcResponse
		status, body := call(t, http.MethodPost, base+"/calculations", token, map[string]any{"parent": root, "name": name, "definition": d, "operation_id": "create-" + name}, &out)
		if status != 200 {
			t.Fatalf("create %s: %d %s", name, status, body)
		}
		status, body = call(t, http.MethodPost, base+"/calculations/"+out.ID+"/apply", token, map[string]any{"revision": out.Revision, "operation_id": "apply-" + name}, &out)
		if status != 200 {
			t.Fatalf("apply %s: %d %s", name, status, body)
		}
		return out.ID
	}
	total := create("total", calculation.Definition{Operator: "add", Inputs: []string{a, pointB}, Kind: "number", Unit: "kW"})
	ratio := create("ratio", calculation.Definition{Operator: "divide", Inputs: []string{total, input.ID}, Kind: "number", Unit: "1"})
	waitFor(t, func() bool {
		s := h.CalculationRuntime().Snapshot()
		r := s.Results[ratio]
		return r.Value != nil && *r.Value == 0.5
	}, "background initial calculation without GET")
	bus.valuesMu.Lock()
	bus.values[1] = 40
	bus.valuesMu.Unlock()
	waitFor(t, func() bool {
		s := h.CalculationRuntime().Snapshot()
		r := s.Results[ratio]
		return r.Value != nil && *r.Value == 0.7
	}, "background update without GET")
	published := h.CalculationRuntime().Snapshot()
	if *published.Results[total].Value != 70 {
		t.Fatal("batch contains mixed total and ratio")
	}
	var out calcResponse
	status, body = call(t, http.MethodGet, base+"/calculations/"+ratio, token, nil, &out)
	if status != 200 || out.State.Value == nil || *out.State.Value != 0.7 {
		t.Fatalf("read did not return published result: %d %s", status, body)
	}
	bus.valuesMu.Lock()
	bus.fail = map[byte]bool{1: true}
	bus.valuesMu.Unlock()
	waitFor(t, func() bool {
		r := h.CalculationRuntime().Snapshot().Results[ratio]
		return r.Value == nil && r.Quality != "good" && r.Retained != nil && r.Retained.Value != nil && *r.Retained.Value == 0.7
	}, "background invalidation without GET")
	bus.valuesMu.Lock()
	bus.fail[1] = false
	bus.valuesMu.Unlock()
	waitFor(t, func() bool {
		r := h.CalculationRuntime().Snapshot().Results[ratio]
		return r.Value != nil && *r.Value == 0.7 && r.Quality == "good"
	}, "background recovery without GET")
	service.RemoveChannel(connection.ID)
	waitFor(t, func() bool {
		r := h.CalculationRuntime().Snapshot().Results[ratio]
		return r.Value == nil && strings.Contains(r.Reason, "expired")
	}, "expiry must update the background batch without GET")
	accepted, ok := service.State.Get(time.Now(), connection.ID+"/1", a, time.Minute)
	if !ok {
		t.Fatal("missing acquisition witness")
	}
	at := time.Now()
	service.Observe(time.Now(), []channel.Result{{DeviceID: connection.ID + "/1", PointID: a, Value: signal.Flag(true), Quality: signal.Good, SourceTime: at, TimeSource: "platform_collection", Unit: accepted.Unit, ConfigStamp: accepted.ConfigStamp}})
	waitFor(t, func() bool {
		r := h.CalculationRuntime().Snapshot().Results[ratio]
		return r.Value == nil && strings.Contains(r.Reason, "input_type")
	}, "typed observation mismatch must not become numeric zero")
	if len(bus.written()) != 0 {
		t.Fatal("calculation produced a protocol write")
	}
}
