package pumphouse

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/profile"
)

func TestDefinitionLoadsAndMatchesSimulator(t *testing.T) {
	dir := t.TempDir()
	encoded, err := SampleProfileJSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pumphouse.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	registry := profile.Load(dir)
	if len(registry.LoadErrors) != 0 {
		t.Fatalf("profile load errors: %v", registry.LoadErrors)
	}
	loaded, ok := registry.Get(ProfileID)
	if !ok {
		t.Fatalf("profile %s not loaded", ProfileID)
	}
	if loaded.Points != len(SamplePoints) || loaded.Commands != len(SampleCommands) {
		t.Fatalf("profile counts = %d/%d, want %d/%d", loaded.Points, loaded.Commands, len(SamplePoints), len(SampleCommands))
	}

	plant := New(DefaultConfig())
	bus := plant.Bus()
	for _, spec := range loaded.PointSpecs {
		var err error
		switch spec.Function {
		case channel.HoldingRegister:
			_, err = bus.ReadHoldingRegisters(StationID, spec.Address, 1)
		case channel.DiscreteInput:
			_, err = bus.ReadDiscreteInputs(StationID, spec.Address, 1)
		default:
			t.Fatalf("point %s uses unsupported function %s", spec.Name, spec.Function)
		}
		if err != nil {
			t.Fatalf("point %s at %d is not in the simulator map: %v", spec.Name, spec.Address, err)
		}
	}

	byName := map[string]profile.PointSpec{}
	for _, spec := range loaded.PointSpecs {
		byName[spec.Name] = spec
	}
	for _, command := range loaded.CommandSpecs {
		write, ok := byName[command.Point]
		if !ok || write.ReadOnly {
			t.Fatalf("command %s does not write a writable declared point", command.ID)
		}
		readback, ok := byName[command.Readback]
		if !ok || command.Readback == command.Point {
			t.Fatalf("command %s must confirm on a separate declared readback point", command.ID)
		}
		if readback.Function != channel.DiscreteInput {
			t.Fatalf("command %s readback %s is not a status input", command.ID, readback.Name)
		}
	}
}

func TestSampleStartsAndConfirmsThroughTheBus(t *testing.T) {
	now := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	cfg.StartDelay = time.Second
	cfg.FeedbackDelay = 200 * time.Millisecond
	cfg.Now = func() time.Time { return now }
	plant := New(cfg)
	bus := plant.Bus()

	command := SampleCommands[0]
	if err := bus.WriteHoldingRegister(StationID, AddressPump1Command, 1); err != nil {
		t.Fatalf("start: %v", err)
	}
	now = now.Add(1300 * time.Millisecond)
	bits, err := bus.ReadDiscreteInputs(StationID, BitPump1Run, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !bits[0] {
		t.Fatalf("run feedback for %s did not follow the start process", command.Name)
	}
}
