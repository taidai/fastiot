package pumphouse

import (
	"errors"
	"testing"
	"time"

	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/modbusrt"
)

func TestTCPServerServesRealModbusFrames(t *testing.T) {
	c := newClock()
	cfg := DefaultConfig()
	cfg.StartDelay = time.Second
	cfg.FeedbackDelay = 300 * time.Millisecond
	cfg.Now = c.Now
	plant := New(cfg)

	server, err := plant.ServeTCP("127.0.0.1:0")
	if err != nil {
		t.Fatalf("serve tcp: %v", err)
	}
	defer server.Close()

	bus, err := modbusrt.OpenTCP(server.Addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("open tcp: %v", err)
	}
	defer bus.Close()

	words, err := bus.ReadHoldingRegisters(StationID, AddressLevel, 2)
	if err != nil {
		t.Fatalf("read level: %v", err)
	}
	if words[0] == 0 {
		t.Fatal("level register should be non-zero")
	}

	if err := bus.WriteHoldingRegister(StationID, AddressPump1Command, 1); err != nil {
		t.Fatalf("start command: %v", err)
	}
	bits, err := bus.ReadDiscreteInputs(StationID, BitPump1Run, 1)
	if err != nil {
		t.Fatal(err)
	}
	if bits[0] {
		t.Fatal("run feedback must not change before the start process")
	}
	c.Advance(1400 * time.Millisecond)
	bits, err = bus.ReadDiscreteInputs(StationID, BitPump1Run, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !bits[0] {
		t.Fatal("run feedback should be on after the start and feedback delays")
	}

	plant.SetFault(1, true)
	err = bus.WriteHoldingRegister(StationID, AddressPump1Command, 1)
	var refusal channel.Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("faulted start error = %v, want channel.Refusal from a Modbus exception", err)
	}

	if _, err := bus.ReadHoldingRegisters(StationID, 900, 1); err == nil {
		t.Fatal("unmapped register read must return a Modbus exception")
	}
	if _, err := bus.ReadHoldingRegisters(9, AddressLevel, 1); err == nil {
		t.Fatal("unknown station read must return a Modbus exception")
	}
}

func TestRefusalFromBusImplementsChannelBus(t *testing.T) {
	plant := New(DefaultConfig())
	var bus channel.Bus = plant.Bus()
	if _, err := bus.ReadInputRegisters(StationID, AddressLevel, 1); err == nil {
		t.Fatal("input registers are not part of the simulated map")
	}
}
