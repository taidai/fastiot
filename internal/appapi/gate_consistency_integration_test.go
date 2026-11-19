package appapi

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/ems/bridge"
	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/command"
	"fastiot/internal/ems/device"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/ems/signal"
	"fastiot/internal/emsapi"
)

type gateDeliveryItem struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type gateDeliveryCheck struct {
	Status string             `json:"status"`
	Items  []gateDeliveryItem `json:"items"`
}

// startGateServer wires the real handler to a real runtime so delivery,
// preflight and acceptance read the same channel health and command results.
func startGateServer(t *testing.T) (string, string, *runtime.Service, *bridge.Bridge) {
	t.Helper()
	base, token, handler, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	t.Cleanup(service.Close)
	runner := bridge.New(service, bridge.NewNodeSource(handler.nc), func(string, string, time.Duration) (channel.Bus, error) {
		return &pointConfigBus{}, nil
	})
	handler.SetEMS(emsapi.New(service, time.Minute))
	return base, token, service, runner
}

func gateItem(t *testing.T, check gateDeliveryCheck, id string) gateDeliveryItem {
	t.Helper()
	for _, item := range check.Items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("delivery item %s missing from %#v", id, check.Items)
	return gateDeliveryItem{}
}

func gatePreflightGate(t *testing.T, report preflightReport, key string) preflightGate {
	t.Helper()
	for _, gate := range report.Gates {
		if gate.Key == key {
			return gate
		}
	}
	t.Fatalf("preflight gate %s missing from %#v", key, report.Gates)
	return preflightGate{}
}

func gateAcceptanceCheck(t *testing.T, report acceptanceReport, key string) acceptanceCheck {
	t.Helper()
	for _, check := range report.Checks {
		if check.Key == key {
			return check
		}
	}
	t.Fatalf("acceptance check %s missing from %#v", key, report.Checks)
	return acceptanceCheck{}
}

// A freshly configured channel has no observation yet: health is unknown.
// Delivery, preflight and acceptance must agree that it is not passable, and
// none of them may call it healthy.
// blockingBus holds the first cycle open so the device has no observation yet,
// which is the "awaiting first success" state of a brand new channel.
type blockingBus struct{ release chan struct{} }

func (b *blockingBus) ReadCoils(byte, uint16, uint16) ([]bool, error) {
	<-b.release
	return nil, errors.New("released")
}

func (b *blockingBus) ReadDiscreteInputs(byte, uint16, uint16) ([]bool, error) {
	<-b.release
	return nil, errors.New("released")
}

func (b *blockingBus) ReadHoldingRegisters(byte, uint16, uint16) ([]uint16, error) {
	<-b.release
	return nil, errors.New("released")
}

func (b *blockingBus) ReadInputRegisters(byte, uint16, uint16) ([]uint16, error) {
	<-b.release
	return nil, errors.New("released")
}

func (b *blockingBus) WriteCoil(byte, uint16, bool) error {
	<-b.release
	return errors.New("released")
}

func (b *blockingBus) WriteHoldingRegister(byte, uint16, uint16) error {
	<-b.release
	return errors.New("released")
}

func TestUnknownChannelHealthIsNotPassedByAnyGate(t *testing.T) {
	base, token, service, _ := startGateServer(t)
	fixture := &pointConfigFixture{base: base, token: token}
	busID := fixture.createBusPeriod(t, "新通道", 60000)
	pointID := fixture.createPoint(t, busID, ModbusIOInput{
		Description: "温度", ServerID: 1, Address: 0,
		IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1, ReadOnly: true,
	})
	bus := &blockingBus{release: make(chan struct{})}
	t.Cleanup(func() { close(bus.release) })
	if err := service.AddChannel(busID, bus, []channel.Device{{
		ID: busID + "/1", ServerID: 1,
		Points: []channel.Point{{
			ID: pointID, Function: channel.HoldingRegister, Address: 0, Format: "uint16", Kind: signal.Number,
		}},
	}}, time.Hour); err != nil {
		t.Fatal(err)
	}
	if health := service.Channels()[0].Health(busID+"/1", time.Now(), time.Minute); health != channel.HealthUnknown {
		t.Fatalf("fresh channel health = %s, want unknown", health)
	}

	var check gateDeliveryCheck
	if status, body := call(t, http.MethodGet, base+"/delivery/check", token, nil, &check); status != http.StatusOK {
		t.Fatalf("delivery check = %d %s", status, body)
	}
	modbus := gateItem(t, check, "modbus")
	if modbus.Status != "warning" || !strings.Contains(modbus.Detail, "未知") {
		t.Fatalf("delivery modbus item = %#v, want warning with unknown detail", modbus)
	}

	var gates preflightReport
	if status, body := call(t, http.MethodGet, base+"/delivery/gates", token, nil, &gates); status != http.StatusOK {
		t.Fatalf("delivery gates = %d %s", status, body)
	}
	channelGate := gatePreflightGate(t, gates, "channels")
	if channelGate.Status != "warn" || !strings.Contains(channelGate.Detail, "未知") {
		t.Fatalf("preflight channel gate = %#v, want warn with unknown detail", channelGate)
	}

	var report acceptanceReport
	if status, body := call(t, http.MethodPost, base+"/ems/acceptance/run", token, map[string]any{}, &report); status != http.StatusOK {
		t.Fatalf("acceptance run = %d %s", status, body)
	}
	accepted := gateAcceptanceCheck(t, report, "channels")
	if accepted.Status != "warning" || !strings.Contains(accepted.Detail, "状态未知 1") {
		t.Fatalf("acceptance channel check = %#v, want warning with unknown detail", accepted)
	}
}

// A failed named command must reach the same gates as a failed point command,
// not only the alarm monitor.
func TestFailedNamedCommandReachesDeliveryAndPreflight(t *testing.T) {
	base, token, service, runner := startGateServer(t)
	fixture := &pointConfigFixture{base: base, token: token}
	busID := fixture.createBusPeriod(t, "交付总线", 60000)
	fixture.createPoint(t, busID, ModbusIOInput{
		Description: "启停", ServerID: 1, Address: 0,
		IOType: data.PointValueModbusCoil, DataFormat: "uint16", Scale: 1,
	})
	if err := runner.Sync(time.Now()); err != nil {
		t.Fatal(err)
	}
	deviceID := busID + "/1"

	// The named command writes a point the channel does not map, so the real
	// dispatcher rejects the write and the manager records Failed.
	service.Commands().SetProfiles(func(id string) (device.Profile, bool) {
		if id != deviceID {
			return device.Profile{}, false
		}
		return device.Profile{
			ID:     "gate-test",
			Points: []device.Point{{ID: "unmapped", Kind: signal.Number}},
			Commands: []device.Command{{
				ID:     "start",
				Writes: []device.Write{{Point: "unmapped", Value: signal.Num(1)}},
			}},
		}, true
	})
	outcome, err := service.Commands().Submit(time.Now(), command.Request{
		ID: "gate-command-1", DeviceID: deviceID, CommandID: "start", Actor: "admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.State != command.Failed {
		t.Fatalf("command state = %s, want failed", outcome.State)
	}

	var check gateDeliveryCheck
	if status, body := call(t, http.MethodGet, base+"/delivery/check", token, nil, &check); status != http.StatusOK {
		t.Fatalf("delivery check = %d %s", status, body)
	}
	commands := gateItem(t, check, "commands")
	if commands.Status != "fail" || !strings.Contains(commands.Detail, "交付总线") {
		t.Fatalf("delivery command item = %#v, want fail naming the bus", commands)
	}

	var gates preflightReport
	if status, body := call(t, http.MethodGet, base+"/delivery/gates", token, nil, &gates); status != http.StatusOK {
		t.Fatalf("delivery gates = %d %s", status, body)
	}
	commandGate := gatePreflightGate(t, gates, "commands")
	if commandGate.Status != "fail" || !strings.Contains(commandGate.Detail, "1 条") {
		t.Fatalf("preflight command gate = %#v, want fail with one command", commandGate)
	}
}
