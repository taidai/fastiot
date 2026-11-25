package appapi

import (
	"context"
	"testing"
	"time"

	"fastiot/internal/calculation"
	"fastiot/internal/ems/bridge"
	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/historyquery"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/emsapi"

	"github.com/simpleiot/simpleiot/data"
)

// Register each service before capturing the next service's subscription
// baseline. Neither service may borrow the other's in-flight startup/cleanup.
func startDerivedLoops(t *testing.T, h *Handler) func() {
	t.Helper()
	baseline := h.nc.NumSubscriptions()
	stopCalculation := startCalculationLoop(t, h)
	waitFor(t, func() bool { return h.nc.NumSubscriptions() == baseline+1 }, "calculation subscription registered")
	stopAggregate := startAggregateLoop(t, h)
	waitFor(t, func() bool { return h.nc.NumSubscriptions() == baseline+2 }, "aggregate subscription registered")
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		stopAggregate()
		stopCalculation()
		if got := h.nc.NumSubscriptions(); got != baseline {
			t.Errorf("derived runtime leaked subscriptions: got %d want %d", got, baseline)
		}
	}
	t.Cleanup(stop)
	return stop
}

func TestDerivedHistoryDoesNotDuplicateAnUnchangedPhysicalObservation(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	source := bridge.NewNodeSource(h.nc)
	api := emsapi.New(service, time.Minute)
	api.Buses = source.Buses
	h.SetEMS(api)
	f := &pointConfigFixture{base: base, token: token}
	bus := f.createBus(t, "single private sample")
	point := f.createPoint(t, bus, ModbusIOInput{Description: "P", ServerID: 1, Address: 10, IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1, ReadOnly: true, Units: "kW"})
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
	var calculated calcResponse
	status, body := call(t, "POST", base+"/calculations", token, map[string]any{"parent": snapshotRoot(t, base, token), "name": "single observation", "operation_id": "create", "definition": calculation.Definition{Operator: "add", Inputs: []string{point, point}, Kind: "number", Unit: "kW"}}, &calculated)
	if status != 200 {
		t.Fatal(status, body)
	}
	status, body = call(t, "POST", base+"/calculations/"+calculated.ID+"/apply", token, map[string]any{"revision": calculated.Revision, "operation_id": "apply"}, &calculated)
	if status != 200 {
		t.Fatal(status, body)
	}
	stop := startCalculationLoop(t, h)
	count := func() int {
		var history struct {
			Results []historyquery.PointResult `json:"results"`
		}
		status, body := call(t, "GET", base+"/history?node_id="+calculated.ID+"&type=value", token, nil, &history)
		if status != 200 {
			t.Fatalf("history: %d %s", status, body)
		}
		n := 0
		for _, result := range history.Results {
			for _, record := range result.Records {
				if record.Quality == "good" && record.Value != nil {
					n++
				}
			}
		}
		return n
	}
	waitFor(t, func() bool { return count() > 0 }, "first derived observation recorded")
	time.Sleep(1200 * time.Millisecond) // Cross the existing calculation tick, without another acquisition.
	stop()
	if got := count(); got != 1 {
		t.Fatalf("unchanged source observation recorded %d times, want 1", got)
	}
}

func TestDerivedRuntimeSubscriptionLifecycle(t *testing.T) {
	_, _, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	baseline := h.nc.NumSubscriptions()
	stop := startDerivedLoops(t, h)
	if got := h.nc.NumSubscriptions(); got != baseline+2 {
		t.Fatalf("derived runtime subscriptions: got %d want %d", got, baseline+2)
	}
	stop()
	if got := h.nc.NumSubscriptions(); got != baseline {
		t.Fatalf("derived runtime leaked subscriptions: got %d want %d", got, baseline)
	}
}
