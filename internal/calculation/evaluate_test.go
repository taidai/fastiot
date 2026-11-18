package calculation_test

import (
	"fastiot/internal/calculation"
	"math"
	"testing"
	"time"
)

func scalar(v float64) *float64 { return &v }
func formulas() map[string]calculation.Formula {
	out := map[string]calculation.Formula{}
	for id, d := range graph() {
		out[id] = calculation.Formula{Definition: d, Version: 1}
	}
	return out
}
func samples(at time.Time) calculation.Snapshot {
	return calculation.Snapshot{At: at, Inputs: map[string]calculation.Sample{
		"A":        {ID: "A", Kind: "number", Unit: "kW", Value: scalar(20), Quality: "good", Source: "observation", SourceTime: at, ReceivedTime: at, ValidUntil: at.Add(10 * time.Second)},
		"B":        {ID: "B", Kind: "number", Unit: "kW", Value: scalar(30), Quality: "good", Source: "observation", SourceTime: at, ReceivedTime: at, ValidUntil: at.Add(10 * time.Second)},
		"capacity": {ID: "capacity", Kind: "number", Unit: "kW", Value: scalar(100), Quality: "good", Source: "configuration", ChangedAt: at.Add(-24 * time.Hour)},
	}}
}
func TestInvalidDependencyClearsCurrentWithoutPoisoningIndependentResults(t *testing.T) {
	at := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	for _, name := range []string{"quality", "missing", "type", "unit", "zero", "overflow", "expiry"} {
		t.Run(name, func(t *testing.T) {
			f := formulas()
			f["independent"] = calculation.Formula{Version: 1, Definition: calculation.Definition{Operator: "add", Inputs: []string{"capacity", "capacity"}, Kind: "number", Unit: "kW"}}
			initial := calculation.EvaluateBatch(f, samples(at), "old", nil)
			input := samples(at)
			input.At = at.Add(time.Second)
			a := input.Inputs["A"]
			capacity := input.Inputs["capacity"]
			switch name {
			case "quality":
				a.Quality = "invalid"
				a.Reason = "read_failed"
			case "missing":
				a.Value = nil
			case "type":
				a.Kind = "bool"
			case "unit":
				a.Unit = "kWh"
			case "zero":
				capacity.Value = scalar(0)
				input.Inputs["capacity"] = capacity
			case "overflow":
				a.Value = scalar(math.MaxFloat64)
				b := input.Inputs["B"]
				b.Value = scalar(math.MaxFloat64)
				input.Inputs["B"] = b
			case "expiry":
				input.At = at.Add(10*time.Second + time.Millisecond)
			}
			input.Inputs["A"] = a
			current := calculation.EvaluateBatch(f, input, "new", initial)
			ratio := current["ratio"]
			if ratio.Value != nil || ratio.Quality == "good" || ratio.Reason == "" {
				t.Fatalf("invalid dependency produced current result: %+v", ratio)
			}
			if ratio.Retained == nil || ratio.Retained.Value == nil || *ratio.Retained.Value != 0.5 || !ratio.Retained.CalculatedAt.Equal(at) {
				t.Fatal("last valid result lost its original evidence")
			}
			independent := current["independent"]
			want := 200.0
			if name == "zero" {
				want = 0
			}
			if independent.Value == nil || *independent.Value != want || independent.Quality != "good" {
				t.Fatalf("unrelated branch invalidated: %+v", independent)
			}
		})
	}
}

func TestBatchReusesUnchangedInputsButPropagatesNewObservation(t *testing.T) {
	at := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	input := samples(at)
	first := calculation.EvaluateBatch(formulas(), input, "first", nil)
	input.At = at.Add(time.Second)
	same := calculation.EvaluateBatch(formulas(), input, "poll", first)
	if !same["ratio"].CalculatedAt.Equal(at) || same["ratio"].BatchID != "first" {
		t.Fatal("clock-only polling renewed calculation evidence")
	}
	a := input.Inputs["A"]
	a.SourceTime = at.Add(time.Second)
	a.ReceivedTime = a.SourceTime
	a.ValidUntil = at.Add(11 * time.Second)
	input.Inputs["A"] = a
	fresh := calculation.EvaluateBatch(formulas(), input, "fresh", same)
	if fresh["ratio"].BatchID != "fresh" || !fresh["ratio"].CalculatedAt.Equal(input.At) || *fresh["ratio"].Value != 0.5 {
		t.Fatal("same-valued new observation did not propagate")
	}
	a.Source = "simulation"
	a.SourceTime = time.Time{}
	a.ChangedAt = input.At
	input.Inputs["A"] = a
	simulated := calculation.EvaluateBatch(formulas(), input, "simulation", fresh)
	found := false
	for _, leaf := range simulated["ratio"].Leaves {
		if leaf.ID == "A" && leaf.Source == "simulation" {
			found = true
		}
	}
	if !found {
		t.Fatal("two-level calculation washed simulation provenance")
	}
}

func TestSkewUsesOriginalObservationLeavesNotCalculationTimes(t *testing.T) {
	at := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	f := formulas()
	ratio := f["ratio"]
	skew := int64(500)
	ratio.Definition.MaxSkewMS = &skew
	f["ratio"] = ratio
	input := samples(at)
	good := calculation.EvaluateBatch(f, input, "initial", nil)
	if good["ratio"].Quality != "good" {
		t.Fatal("configuration edit time was treated as a sampling time")
	}
	b := input.Inputs["B"]
	b.SourceTime = at.Add(2 * time.Second)
	b.ReceivedTime = b.SourceTime
	b.ValidUntil = at.Add(12 * time.Second)
	input.Inputs["B"] = b
	input.At = b.SourceTime
	bad := calculation.EvaluateBatch(f, input, "skewed", good)
	if bad["total"].Quality != "good" || bad["ratio"].Value != nil || bad["ratio"].Reason != "time_skew" {
		t.Fatalf("transitive skew hidden by calculation time: %+v", bad["ratio"])
	}
}

func TestBatchComputesCoherentTwoLevelResults(t *testing.T) {
	at := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	input := samples(at)
	first := calculation.EvaluateBatch(formulas(), input, "batch1", nil)
	if first["total"].Value == nil || *first["total"].Value != 50 || first["ratio"].Value == nil || *first["ratio"].Value != 0.5 {
		t.Fatalf("wrong initial batch: %+v", first)
	}
	if first["ratio"].Inputs[0].Value == nil || *first["ratio"].Inputs[0].Value != 50 || first["ratio"].Inputs[0].Version != "batch1/1" {
		t.Fatal("downstream evidence did not use same batch upstream")
	}
	next := input.Inputs["A"]
	next.Value = scalar(40)
	next.SourceTime = at.Add(time.Second)
	next.ReceivedTime = next.SourceTime
	next.ValidUntil = at.Add(11 * time.Second)
	input.Inputs["A"] = next
	input.At = at.Add(time.Second)
	second := calculation.EvaluateBatch(formulas(), input, "batch2", first)
	if *second["total"].Value != 70 || *second["ratio"].Value != 0.7 || *second["ratio"].Inputs[0].Value != 70 {
		t.Fatalf("mixed generation or stale value: %+v", second)
	}
	if second["total"].BatchID != "batch2" || second["ratio"].BatchID != "batch2" {
		t.Fatal("dependent result not in the new batch")
	}
	if !second["ratio"].ValidUntil.Equal(at.Add(10 * time.Second)) {
		t.Fatal("calculation renewed the oldest dependency expiry")
	}
	if len(second["ratio"].Leaves) != 3 {
		t.Fatal("transitive source evidence lost")
	}
}
