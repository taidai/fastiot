package appapi

import (
	"encoding/json"
	"testing"
	"time"

	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/command"
	"fastiot/internal/ems/device"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/ems/safety"
	"fastiot/internal/ems/signal"
	"fastiot/internal/emsapi"
	siotclient "github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

type guardDispatcher struct {
	calls int
	last  []device.Write
}

func (d *guardDispatcher) Write(_, _ string, writes []device.Write) error {
	d.calls++
	d.last = append([]device.Write(nil), writes...)
	return nil
}

func TestProductionGuardClampsAndConfirms(t *testing.T) {
	min, max := 0.0, 60.0
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, 15*time.Second, 5*time.Second, 100)
	defer service.Close()
	h.SetEMS(emsapi.New(service, 15*time.Second))
	fixture := &pointConfigFixture{base: base, token: token}
	busID := fixture.createBus(t, "guard bus")
	pointID := fixture.createPoint(t, busID, ModbusIOInput{Description: "guarded power", ServerID: 1, Address: 10, IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1})
	encoded, _ := json.Marshal(safety.Config{Min: &min, Max: &max})
	if err := siotclient.SendNodePoint(h.nc, pointID, data.NewPointString("easyemsSafety", "", string(encoded)), true); err != nil {
		t.Fatal(err)
	}
	writer := &guardDispatcher{}
	deviceID := busID + "/1"
	service.RegisterDispatcher(deviceID, writer)
	outcome, err := service.Commands().Submit(time.Now(), command.Request{ID: "clamp", DeviceID: deviceID, Point: pointID, Value: signal.Num(90), Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.State != command.Accepted || len(writer.last) != 1 || writer.last[0].Value.Num != 60 {
		t.Fatalf("guarded write = %+v writes=%+v", outcome, writer.last)
	}
	at := outcome.StartedAt.Add(time.Second)
	service.Observe(at, []channel.Result{{DeviceID: deviceID, PointID: pointID, Value: signal.Num(60), Quality: signal.Good, SourceTime: at}})
	service.Commands().Tick(at)
	confirmed, _ := service.Commands().Get("clamp")
	if confirmed.State != command.Confirmed {
		t.Fatalf("clamped command did not confirm: %+v", confirmed)
	}
}

func TestProductionGuardRefusesReadOnlyAndAutomaticTakeover(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, 15*time.Second, 5*time.Second, 100)
	defer service.Close()
	h.SetEMS(emsapi.New(service, 15*time.Second))
	fixture := &pointConfigFixture{base: base, token: token}
	busID := fixture.createBus(t, "guard bus")
	pointID := fixture.createPoint(t, busID, ModbusIOInput{Description: "guarded power", ServerID: 1, Address: 10, IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1, ReadOnly: true})
	writer := &guardDispatcher{}
	deviceID := busID + "/1"
	service.RegisterDispatcher(deviceID, writer)
	outcome, err := service.Commands().Submit(time.Now(), command.Request{ID: "readonly", DeviceID: deviceID, Point: pointID, Value: signal.Num(10), Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.State != command.Failed || outcome.Reason != "read_only" || writer.calls != 0 {
		t.Fatalf("read-only write = %+v calls=%d", outcome, writer.calls)
	}
	if err := siotclient.SendNodePoint(h.nc, pointID, data.NewPointFloat(data.PointTypeReadOnly, "", 0), true); err != nil {
		t.Fatal(err)
	}
	if err := siotclient.SendNodePoint(h.nc, pointID, data.NewPointFloat("easyemsManual", "", 1), true); err != nil {
		t.Fatal(err)
	}
	outcome, err = service.Commands().Submit(time.Now(), command.Request{ID: "takeover", DeviceID: deviceID, Point: pointID, Value: signal.Num(10), Source: "schedule"})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.State != command.Failed || outcome.Reason != "manual_takeover" || writer.calls != 0 {
		t.Fatalf("automatic takeover = %+v calls=%d", outcome, writer.calls)
	}
	outcome, err = service.Commands().Submit(time.Now(), command.Request{ID: "human", DeviceID: deviceID, Point: pointID, Value: signal.Num(10), Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.State != command.Accepted || writer.calls != 1 {
		t.Fatalf("authorized human command = %+v calls=%d", outcome, writer.calls)
	}
}
