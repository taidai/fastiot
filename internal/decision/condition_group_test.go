package decision

import (
	"testing"
	"time"

	"fastiot/internal/ems/rule"
	"fastiot/internal/ems/signal"
)

func conditionGroupDocument(match string, holdMS int) Document {
	return Document{
		Table: Table{Rows: []Row{{
			ID: "or-row", Match: match, HoldMS: holdMS,
			Conditions: []Condition{
				{PointID: "pressure", Operator: "lt", Value: 0.35},
				{PointID: "level", Operator: "gt", Value: 50},
			},
			Actions: []Action{{ActionID: "pump", Value: 1}},
		}}},
		Bindings: BindingSet{
			Points:  map[string]Target{"pressure": {NodeID: "pressure"}, "level": {NodeID: "level"}},
			Actions: map[string]Target{"pump": {NodeID: "pump"}},
		},
	}
}

func TestFourPartTimeWindowCannotBeBypassedByOR(t *testing.T) {
	document := conditionGroupDocument("any", 0)
	document.Table.Rows[0].Conditions = append(document.Table.Rows[0].Conditions, Condition{Kind: "time", Start: "08:00", End: "09:00"})
	for _, tc := range []struct {
		name       string
		hour       int
		level      float64
		matched    bool
		hold, held int
		missing    bool
	}{
		{name: "true point outside window", hour: 12, level: 60, matched: false},
		{name: "window does not replace point group", hour: 8, level: 40, matched: false},
		{name: "both point group and window", hour: 8, level: 60, matched: true},
		{name: "hold does not bypass window", hour: 12, level: 60, hold: 2000, held: 3000},
		{name: "point and window wait for hold", hour: 8, level: 60, matched: true, hold: 2000},
		{name: "unknown inputs stay unsatisfied", hour: 8, missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			document.Table.Rows[0].HoldMS = tc.hold
			document.Simulation = &SimulationOptions{HeldMS: tc.held}
			inputs := map[string]float64{"pressure": 0.5, "level": tc.level}
			if tc.missing {
				inputs = map[string]float64{}
			}
			result, err := document.Simulate(inputs, nil, time.Date(2026, 11, 25, tc.hour, 30, 0, 0, time.UTC))
			if err != nil {
				t.Fatal(err)
			}
			if (result.RowID == "or-row") != tc.matched {
				t.Fatalf("four-part window mismatch: %+v", result)
			}
			if tc.matched && tc.hold > tc.held && result.State != "waiting-hold" {
				t.Fatalf("hold bypassed: %+v", result)
			}
		})
	}
}

func TestTimeOnlyRowIsNotAnUnconditionalDefault(t *testing.T) {
	document := Document{Table: Table{Rows: []Row{{ID: "window", Conditions: []Condition{{Kind: "time", Start: "08:00", End: "09:00"}}}, {ID: "fallback"}}}}
	converted, err := document.RuleTable()
	if err != nil {
		t.Fatal(err)
	}
	for _, hour := range []int{8, 12} {
		now := time.Date(2026, 11, 25, hour, 30, 0, 0, time.UTC)
		engine, err := rule.New(converted, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		actual, err := engine.Evaluate(now, TimeSamples(document.Table, now))
		if err != nil {
			t.Fatal(err)
		}
		simulated, err := document.Simulate(nil, nil, now)
		if err != nil {
			t.Fatal(err)
		}
		want := "window"
		if hour == 12 {
			want = "fallback"
		}
		if actual.MatchedRow != want || simulated.RowID != want {
			t.Fatalf("time-only row lost parity: actual=%+v simulation=%+v", actual, simulated)
		}
	}
	// Existing time-only OR windows remain a union within the separate window clause.
	document.Table.Rows[0].Match = "any"
	document.Table.Rows[0].Conditions = append(document.Table.Rows[0].Conditions, Condition{Kind: "time", Start: "16:00", End: "17:00"})
	simulated, err := document.Simulate(nil, nil, time.Date(2026, 11, 25, 8, 30, 0, 0, time.UTC))
	if err != nil || simulated.RowID != "window" {
		t.Fatalf("time-only OR windows changed: %+v %v", simulated, err)
	}
}

func TestConditionGroupORUsesAnyUsableCondition(t *testing.T) {
	document := conditionGroupDocument("any", 0)
	result, err := document.Simulate(map[string]float64{"level": 60}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.RowID != "or-row" || len(result.Actions) != 1 {
		t.Fatalf("one true OR branch must match despite a missing sibling: %+v", result)
	}

	result, err = document.Simulate(map[string]float64{"pressure": 0.5, "level": 40}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.RowID != "" || len(result.Rows) != 1 || result.Rows[0].State != "no-match" {
		t.Fatalf("false OR group must explain its skip: %+v", result)
	}
}

func TestConditionGroupUnknownInputIsUnsatisfied(t *testing.T) {
	// ADR 0010: invalid data means that condition is not satisfied, with a
	// reason; it is never zero-filled. The row simply does not match.
	document := conditionGroupDocument("all", 0)
	now := time.Now()
	samples := SamplesFromValues(map[string]float64{}, nil, now)
	outcome, err := rule.Simulate(rule.Table{ID: "decision", Version: 1, Rows: []rule.Row{{
		ID: "needs-input", Conditions: []rule.Condition{{Input: "pressure", Op: "lt", Value: signal.Num(0.35)}},
	}}}, now, samples, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.State != rule.StateNoMatch || len(outcome.Blocked) != 1 || outcome.Blocked[0].Reason == "" {
		t.Fatalf("missing input must be unsatisfied with a reason: %+v", outcome)
	}
	// A following row is still evaluated by the same first-hit engine.
	result, err := document.Simulate(map[string]float64{"level": 60}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	row := document.Table.Rows[0]
	row.Match = "all"
	document.Table.Rows = []Row{row, {ID: "default"}}
	result, err = document.Simulate(map[string]float64{"level": 60}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.RowID != "default" {
		t.Fatalf("a later row must be reachable when an earlier condition is unsatisfied: %+v", result)
	}
}

func TestConditionGroupORIsDecisiveWhenABranchMatches(t *testing.T) {
	now := time.Now()
	document := conditionGroupDocument("any", 0)
	result, err := document.Simulate(map[string]float64{"level": 60}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.RowID != "or-row" {
		t.Fatalf("one true OR branch must match despite a missing sibling: %+v", result)
	}
	result, err = document.Simulate(map[string]float64{"level": 40}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.RowID != "" || len(result.Rows) != 1 || result.Rows[0].State != "no-match" {
		t.Fatalf("false OR group must explain its skip: %+v", result)
	}
}

func TestConditionGroupValidationRejectsMixedLogic(t *testing.T) {
	document := conditionGroupDocument("xor", 0)
	if err := document.Validate(); err == nil {
		t.Fatal("unsupported condition group operator was accepted")
	}
}

func TestSimulationReportsHoldAndPerRowReasons(t *testing.T) {
	document := conditionGroupDocument("any", 30_000)
	document.Simulation = &SimulationOptions{HeldMS: 10_000}
	result, err := document.Simulate(map[string]float64{"level": 60}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "waiting-hold" || result.RowID != "or-row" || len(result.Actions) != 0 {
		t.Fatalf("hold must not fabricate actions: %+v", result)
	}
	if len(result.Rows) != 1 || result.Rows[0].HeldMS != 10_000 || result.Rows[0].RequiredMS != 30_000 {
		t.Fatalf("hold evidence missing: %+v", result.Rows)
	}

	document.Simulation.HeldMS = 30_000
	result, err = document.Simulate(map[string]float64{"level": 60}, nil, time.Now())
	if err != nil || result.State != "fired" || len(result.Actions) != 1 {
		t.Fatalf("satisfied hold must simulate the action plan: %+v, %v", result, err)
	}
}
