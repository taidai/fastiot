package channel

import (
	"context"
	"errors"
	"testing"
	"time"

	"fastiot/internal/ems/signal"
)

func TestWindowCountsActualAttemptsOnceAndUsesObservationClock(t *testing.T) {
	bus := newFakeBus()
	ch, err := New("window", bus, []Device{device("d", 1, tempPoint(0))})
	if err != nil {
		t.Fatal(err)
	}
	if got := ch.Window("d", t0, 5); got.Polls != 0 {
		t.Fatalf("no I/O: %+v", got)
	}
	ch.RunOnce(context.Background(), func() time.Time { return t0 })
	bus.fail[1] = true
	ch.RunOnce(context.Background(), func() time.Time { return t0.Add(time.Minute) })
	got := ch.Window("d", t0.Add(time.Minute), 5)
	if got.Polls != 2 || got.Failures != 1 || got.SuccessRate != 50 {
		t.Fatalf("one success and one failed connection: %+v", got)
	}
	if stats := ch.Stats("d"); stats.Requests != 2 || stats.Failures != 1 {
		t.Fatalf("legacy health counters changed: %+v", stats)
	}
	if got = ch.Window("d", t0, 5); got.Polls != 1 || got.Failures != 0 {
		t.Fatalf("future bucket leaked: %+v", got)
	}
	if got = ch.Window("d", t0.Add(6*time.Minute), 5); got.Polls != 1 || got.Failures != 1 {
		t.Fatalf("inclusive minute boundary: %+v", got)
	}
	if got = ch.Window("d", t0.Add(7*time.Minute), 5); got.Polls != 0 {
		t.Fatalf("expired window: %+v", got)
	}
}
func TestWindowFailedWriteAndUnexecutedIO(t *testing.T) {
	bus := newFakeBus()
	ch, err := New("window", bus, []Device{device("d", 1, tempPoint(0))})
	if err != nil {
		t.Fatal(err)
	}
	if err = ch.QueueWrite("d", "temp", signal.Label("bad kind"), "bad"); err == nil {
		t.Fatal("invalid write accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ch.RunOnce(ctx, func() time.Time { return t0 })
	if got := ch.Window("d", t0, 5); got.Polls != 0 {
		t.Fatalf("unexecuted I/O counted: %+v", got)
	}
	bus.writeErr = errors.New("write connection refused")
	if err = ch.QueueWrite("d", "temp", signal.Num(10), "write"); err != nil {
		t.Fatal(err)
	}
	ch.RunOnce(context.Background(), func() time.Time { return t0 })
	if got := ch.Window("d", t0, 5); got.Polls != 2 || got.Failures != 1 || got.SuccessRate != 50 {
		t.Fatalf("failed write plus successful read: %+v", got)
	}
}

type shortWindowBus struct{ *fakeBus }

func (s shortWindowBus) ReadHoldingRegisters(byte, uint16, uint16) ([]uint16, error) { return nil, nil }
func TestWindowSeparatesDecodeQualityFromTransportSuccess(t *testing.T) {
	ch, err := New("window", shortWindowBus{newFakeBus()}, []Device{device("d", 1, tempPoint(0))})
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := ch.RunOnce(context.Background(), func() time.Time { return t0 })
	if len(rows) != 1 || rows[0].Quality != signal.Invalid || ch.Stats("d").Failures != 1 {
		t.Fatal("decode health failure lost")
	}
	if got := ch.Window("d", t0, 5); got.Polls != 1 || got.Failures != 0 || got.SuccessRate != 100 {
		t.Fatalf("decode failure invented another transport attempt: %+v", got)
	}
}
func TestWindowCountsEachMultiRegisterWrite(t *testing.T) {
	p := tempPoint(0)
	p.Format = "uint32"
	ch, err := New("window", newFakeBus(), []Device{device("d", 1, p)})
	if err != nil {
		t.Fatal(err)
	}
	if err = ch.QueueWrite("d", "temp", signal.Num(10), "wide"); err != nil {
		t.Fatal(err)
	}
	ch.RunOnce(context.Background(), func() time.Time { return t0 })
	if got := ch.Window("d", t0, 5); got.Polls != 3 || got.Failures != 0 {
		t.Fatalf("two writes and one read: %+v", got)
	}
}
