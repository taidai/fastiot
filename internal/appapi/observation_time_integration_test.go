package appapi

import (
	"testing"
	"time"

	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/signal"
)

func TestAcceptedObservationKeepsSourceTimeInSnapshot(t *testing.T) {
	f := newPointConfigFixture(t)
	f.bus.setOffline(true)
	busID := f.createBus(t, "Observation time")
	pointID := f.createPoint(t, busID, ModbusIOInput{
		Description: "Pressure", ServerID: 1, Address: 10,
		IOType: data.PointValueModbusInputRegister, DataFormat: "uint16", ReadOnly: true, Scale: 1,
	})
	f.cycle(t)
	received := time.Now().UTC()
	observed := received.Add(-3 * time.Second)
	f.runtime.Observe(received, []channel.Result{{
		DeviceID: busID + "/1", PointID: pointID,
		Value: signal.Num(37), Quality: signal.Good, SourceTime: observed,
	}})
	value, ok := f.node(t, pointID).Points.Find(data.PointTypeValue, "")
	actual, err := value.ValueFloat()
	if !ok || err != nil || actual != 37 || !value.Time.Equal(observed) {
		t.Fatalf("node snapshot re-stamped accepted observation: value=%+v wantTime=%s", value, observed)
	}
	if writes := f.bus.written(); len(writes) != 0 {
		t.Fatalf("read-only observation wrote the protocol: %v", writes)
	}
}
