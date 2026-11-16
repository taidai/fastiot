package calculation_test

import (
	"fastiot/internal/calculation"
	"testing"
	"time"
)

func graph() map[string]calculation.Definition {
	return map[string]calculation.Definition{
		"total": {Operator: "add", Inputs: []string{"A", "B"}, Kind: "number", Unit: "kW"},
		"ratio": {Operator: "divide", Inputs: []string{"total", "capacity"}, Kind: "number", Unit: "1"},
	}
}
func sources() map[string]calculation.Metadata {
	return map[string]calculation.Metadata{"A": {Kind: "number", Unit: "kW"}, "B": {Kind: "number", Unit: "kW"}, "capacity": {Kind: "number", Unit: "kW"}}
}
func TestGraphRejectsInvalidContracts(t *testing.T) {
	cases := []struct {
		name   string
		change func(map[string]calculation.Definition, map[string]calculation.Metadata)
	}{
		{"arity", func(g map[string]calculation.Definition, s map[string]calculation.Metadata) {
			d := g["total"]
			d.Inputs = []string{"A"}
			g["total"] = d
		}},
		{"operator", func(g map[string]calculation.Definition, s map[string]calculation.Metadata) {
			d := g["total"]
			d.Operator = "script"
			g["total"] = d
		}},
		{"output_type", func(g map[string]calculation.Definition, s map[string]calculation.Metadata) {
			d := g["total"]
			d.Kind = "bool"
			g["total"] = d
		}},
		{"input_type", func(g map[string]calculation.Definition, s map[string]calculation.Metadata) {
			s["B"] = calculation.Metadata{Kind: "bool", Unit: "kW"}
		}},
		{"unit", func(g map[string]calculation.Definition, s map[string]calculation.Metadata) {
			s["B"] = calculation.Metadata{Kind: "number", Unit: "kWh"}
		}},
		{"missing_unit", func(g map[string]calculation.Definition, s map[string]calculation.Metadata) {
			s["B"] = calculation.Metadata{Kind: "number"}
		}},
		{"missing_reference", func(g map[string]calculation.Definition, s map[string]calculation.Metadata) { delete(s, "B") }},
		{"self_cycle", func(g map[string]calculation.Definition, s map[string]calculation.Metadata) {
			d := g["total"]
			d.Inputs[0] = "total"
			g["total"] = d
		}},
		{"indirect_cycle", func(g map[string]calculation.Definition, s map[string]calculation.Metadata) {
			d := g["total"]
			d.Inputs[0] = "ratio"
			g["total"] = d
		}},
		{"declared_output_unit", func(g map[string]calculation.Definition, s map[string]calculation.Metadata) {
			d := g["ratio"]
			d.Unit = "kW"
			g["ratio"] = d
		}},
		{"downstream_incompatible", func(g map[string]calculation.Definition, s map[string]calculation.Metadata) {
			d := g["total"]
			d.Operator = "divide"
			d.Unit = "1"
			g["total"] = d
		}},
		{"negative_skew", func(g map[string]calculation.Definition, s map[string]calculation.Metadata) {
			d := g["total"]
			negative := int64(-1)
			d.MaxSkewMS = &negative
			g["total"] = d
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g, s := graph(), sources()
			c.change(g, s)
			if _, err := calculation.ValidateGraph(g, s); err == nil {
				t.Fatal("invalid graph accepted")
			}
		})
	}
}

func TestDraftAndApplyVersionsStaySeparate(t *testing.T) {
	now := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	op := calculation.Mutation{ID: "create", Actor: "engineer", Now: now}
	d, err := calculation.NewDocument(graph()["total"], op)
	if err != nil {
		t.Fatal(err)
	}
	if d.Applied != nil || d.Version != 0 {
		t.Fatal("created draft became active")
	}
	op.ID = "apply"
	op.ExpectedRevision = d.Revision
	d, err = d.Apply(op)
	if err != nil {
		t.Fatal(err)
	}
	if d.Applied == nil || d.Version != 1 {
		t.Fatal("application did not activate version1")
	}
	originalRevision := d.Revision
	retry := op
	retry.Now = now.Add(time.Hour)
	duplicate, err := d.Apply(retry)
	if err != nil || duplicate.Revision != originalRevision || duplicate.Version != 1 {
		t.Fatalf("apply retry changed formula version: %+v %v", duplicate, err)
	}
	draft := graph()["total"]
	draft.Inputs = []string{"A", "capacity"}
	op.ID = "draft"
	op.ExpectedRevision = d.Revision
	d, err = d.SaveDraft(draft, op)
	if err != nil {
		t.Fatal(err)
	}
	if d.Applied.Inputs[1] != "B" || d.Version != 1 {
		t.Fatal("draft changed active formula")
	}
	if _, err = d.Apply(calculation.Mutation{ID: "stale", Actor: "engineer", Now: now, ExpectedRevision: 1}); err == nil {
		t.Fatal("stale application accepted")
	}
	op.ID = "apply-next"
	op.ExpectedRevision = d.Revision
	d, err = d.Apply(op)
	if err != nil || d.Version != 2 || d.Applied.Inputs[1] != "capacity" {
		t.Fatalf("next formula not applied: %+v %v", d, err)
	}
	op.ID = "unchanged"
	op.ExpectedRevision = d.Revision
	unchanged, err := d.Apply(op)
	if err != nil || unchanged.Revision != d.Revision || unchanged.Version != 2 {
		t.Fatal("unchanged apply made a new formula version")
	}
}

func TestTwoLevelGraphOrdersDependencies(t *testing.T) {
	order, err := calculation.ValidateGraph(graph(), sources())
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "total" || order[1] != "ratio" {
		t.Fatalf("downstream precedes dependency: %v", order)
	}
}
