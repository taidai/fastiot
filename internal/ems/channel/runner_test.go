package channel

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type heldReadBus struct {
	*fakeBus
	entered chan struct{}
	release chan struct{}
}

func (b *heldReadBus) ReadHoldingRegisters(byte, uint16, uint16) ([]uint16, error) {
	close(b.entered)
	<-b.release
	return nil, context.DeadlineExceeded
}

func TestCancelledRunnerDoesNotPublishCompletedRead(t *testing.T) {
	bus := &heldReadBus{fakeBus: newFakeBus(), entered: make(chan struct{}), release: make(chan struct{})}
	ch, err := New("c1", bus, []Device{device("d1", 1, tempPoint(10))})
	if err != nil {
		t.Fatal(err)
	}
	cycles := 0
	runner := &Runner{Channel: ch, OnCycle: func(time.Time, []Result, []WriteResult) { cycles++ }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	select {
	case <-bus.entered:
	case <-time.After(time.Second):
		t.Fatal("read did not start")
	}
	cancel()
	close(bus.release)
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("runner did not stop")
	}
	if cycles != 0 {
		t.Fatalf("cancelled source published %d obsolete cycles", cycles)
	}
}

func TestRunnerCyclesUntilCancelled(t *testing.T) {
	bus := newFakeBus()
	bus.holding[10] = 351
	ch, err := New("c1", bus, []Device{device("d1", 1, tempPoint(10))})
	if err != nil {
		t.Fatal(err)
	}
	var cycles int32
	runner := &Runner{
		Channel: ch,
		Period:  5 * time.Millisecond,
		OnCycle: func(time.Time, []Result, []WriteResult) {
			atomic.AddInt32(&cycles, 1)
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if err := runner.Run(ctx); err != context.DeadlineExceeded {
		t.Fatalf("runner returned %v, want deadline exceeded", err)
	}
	if atomic.LoadInt32(&cycles) < 2 {
		t.Fatalf("cycles = %d, want at least 2", cycles)
	}
	if stats := ch.Stats("d1"); stats.Requests == 0 {
		t.Fatal("runner did not poll")
	}
}
