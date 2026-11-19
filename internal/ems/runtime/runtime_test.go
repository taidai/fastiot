package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/command"
	"fastiot/internal/ems/device"
	"fastiot/internal/ems/signal"
)

var t0 = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

type testDispatcher struct{ channel *channel.Channel }

func (d testDispatcher) Write(deviceID, requestID string, writes []device.Write) error {
	for _, write := range writes {
		if err := d.channel.QueueWrite(deviceID, write.Point, write.Value, requestID); err != nil {
			return err
		}
	}
	return nil
}

type fakeBus struct {
	coils   map[uint16]bool
	holding map[uint16]uint16
	fail    map[byte]bool
}

func (f *fakeBus) ReadCoils(server byte, address, count uint16) ([]bool, error) {
	if f.fail[server] {
		return nil, errors.New("timeout")
	}
	out := make([]bool, count)
	for i := uint16(0); i < count; i++ {
		out[i] = f.coils[address+i]
	}
	return out, nil
}

func (f *fakeBus) ReadDiscreteInputs(_ byte, address, count uint16) ([]bool, error) {
	return make([]bool, count), nil
}

func (f *fakeBus) ReadHoldingRegisters(server byte, address, count uint16) ([]uint16, error) {
	if f.fail[server] {
		return nil, errors.New("timeout")
	}
	out := make([]uint16, count)
	for i := uint16(0); i < count; i++ {
		out[i] = f.holding[address+i]
	}
	return out, nil
}

func (f *fakeBus) ReadInputRegisters(_ byte, address, count uint16) ([]uint16, error) {
	return make([]uint16, count), nil
}

func (f *fakeBus) WriteCoil(_ byte, address uint16, value bool) error {
	f.coils[address] = value
	return nil
}

func (f *fakeBus) WriteHoldingRegister(_ byte, address, value uint16) error {
	f.holding[address] = value
	return nil
}

func profile() device.Profile {
	return device.Profile{
		ID: "pcs", Vendor: "恩玖", Model: "PCS",
		Points: []device.Point{{ID: "run", Kind: signal.Bool}},
		Commands: []device.Command{{
			ID: "start", Name: "启动",
			Writes:   []device.Write{{Point: "run", Value: signal.Flag(true)}},
			Readback: []device.Readback{{Point: "run", Op: "eq", Value: signal.Flag(true)}},
			Timeout:  5 * time.Second,
		}},
	}
}

func TestCommandWriteIsQueuedThenConfirmedByReadback(t *testing.T) {
	bus := &fakeBus{coils: map[uint16]bool{}, holding: map[uint16]uint16{}}
	ch, err := channel.New("c1", bus, []channel.Device{{
		ID: "pcs1", ServerID: 1,
		Points: []channel.Point{{ID: "run", Function: channel.Coil, Address: 1, Kind: signal.Bool}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	samples := map[string]signal.Sample{}
	manager := command.New(
		testDispatcher{channel: ch},
		func(deviceID string) (device.Profile, bool) {
			return profile(), deviceID == "pcs1"
		},
		func(string, time.Time) map[string]signal.Sample { return samples },
		5*time.Second, 3*time.Second,
	)

	outcome, err := manager.Submit(t0, command.Request{ID: "req-1", DeviceID: "pcs1", CommandID: "start", Actor: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.State != command.Accepted {
		t.Fatalf("state = %s, want accepted", outcome.State)
	}
	if bus.coils[1] {
		t.Fatal("write must wait for the channel run, not happen inside Submit")
	}

	results, writes := ch.RunOnce(context.Background(), func() time.Time { return t0 })
	if len(writes) != 1 || writes[0].Error != "" {
		t.Fatalf("queued write not applied: %+v", writes)
	}
	if !bus.coils[1] {
		t.Fatal("coil was not written")
	}
	for _, result := range results {
		samples[result.PointID] = signal.Sample{
			Input: result.PointID, Value: result.Value, Quality: result.Quality,
			// 回读必须来自提交之后的新读取，同一时刻的样本不算确认。
			SourceTime: result.SourceTime.Add(100 * time.Millisecond), ReceivedTime: result.SourceTime.Add(100 * time.Millisecond),
		}
	}

	manager.Tick(t0.Add(time.Second))
	final, _ := manager.Get("req-1")
	if final.State != command.Confirmed {
		t.Fatalf("state = %s, want confirmed", final.State)
	}
}
