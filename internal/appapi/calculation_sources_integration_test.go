package appapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"fastiot/internal/ems/channel"
	"github.com/simpleiot/simpleiot/data"
)

func TestCalculationSourceKeepsAcquisitionConfiguration(t *testing.T) {
	f := newPointConfigFixture(t)
	bus := f.createBus(t, "source-config")
	input := ModbusIOInput{Description: "P", ServerID: 1, Address: 10, IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1, ReadOnly: true, Units: "kW"}
	id := f.createPoint(t, bus, input)
	configs, err := f.bridge.Source.Buses()
	if err != nil || len(configs) != 1 {
		t.Fatalf("configs: %v", err)
	}
	cfg := configs[0]
	p := cfg.Devices[0].Points[0]
	ch, err := channel.New(cfg.ChannelID, f.bus, cfg.Devices)
	if err != nil {
		t.Fatal(err)
	}
	readings, _ := ch.RunOnce(context.Background(), time.Now)
	f.runtime.Observe(time.Now(), readings)
	current, ok := f.runtime.State.Get(time.Now(), cfg.Devices[0].ID, id, time.Minute)
	if !ok || current.Unit != "kW" || current.ConfigStamp == "" || current.ConfigStamp != p.ConfigStamp {
		t.Fatalf("acquisition metadata lost: %+v", current)
	}
	original := current.ConfigStamp
	input.Units = "W"
	status, body := call(t, http.MethodPut, f.base+"/modbus/io/"+id, f.token, input, nil)
	if status != 200 {
		t.Fatalf("edit units: %d %s", status, body)
	}
	changed, err := f.bridge.Source.Buses()
	if err != nil {
		t.Fatal(err)
	}
	changedStamp := changed[0].Devices[0].Points[0].ConfigStamp
	if changedStamp == "" || changedStamp == original {
		t.Fatal("unit change did not change acquisition identity")
	}
	current, _ = f.runtime.State.Get(time.Now(), cfg.Devices[0].ID, id, time.Minute)
	if current.Unit != "kW" || current.ConfigStamp != original {
		t.Fatal("old observation was decorated with new configuration")
	}
	input.Units = "kW"
	status, body = call(t, http.MethodPut, f.base+"/modbus/io/"+id, f.token, input, nil)
	if status != 200 {
		t.Fatalf("revert units: %d %s", status, body)
	}
	reverted, err := f.bridge.Source.Buses()
	if err != nil {
		t.Fatal(err)
	}
	stamp := reverted[0].Devices[0].Points[0].ConfigStamp
	if stamp == original || stamp == changedStamp {
		t.Fatal("editing back revived an old acquisition identity")
	}
}
