package calculation_test

import (
	"fastiot/internal/calculation"
	"testing"
	"time"
)

func TestRuntimePublishesAndInvalidatesWholeBatches(t *testing.T) {
	at := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	runtime := calculation.NewRuntime("process")
	runtime.Publish(formulas(), samples(at))
	first := runtime.Snapshot()
	if first.Results["total"].Value == nil || *first.Results["total"].Value != 50 || *first.Results["ratio"].Value != 0.5 {
		t.Fatal("coherent batch was not published")
	}
	*first.Results["total"].Value = 999
	if *runtime.Snapshot().Results["total"].Value != 50 {
		t.Fatal("reader mutated published state")
	}
	runtime.Invalidate([]string{"total", "ratio"})
	next := runtime.Snapshot()
	if next.ID == first.ID {
		t.Fatal("invalidation did not advance batch identity")
	}
	for _, id := range []string{"total", "ratio"} {
		r := next.Results[id]
		if r.Value != nil || r.Quality == "good" || r.Retained == nil {
			t.Fatalf("old successful result remained current: %+v", r)
		}
	}
	runtime.Publish(formulas(), samples(at))
	if *runtime.Snapshot().Results["total"].Value != 50 {
		t.Fatal("invalidated result did not recalculate")
	}
}
