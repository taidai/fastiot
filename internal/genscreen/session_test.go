package genscreen

import (
	"testing"
	"time"
)

var simNow = time.Unix(1700000000, 0)

func simulatorPoints() []Point {
	return []Point{
		{ID: "level", Name: "水箱液位", Kind: "number", Unit: "%"},
		{ID: "pump", Name: "1#泵启停", Kind: "number", Writable: true},
		{ID: "note", Name: "备注", Kind: "text", Writable: true},
	}
}

func TestSimulatorIsDeterministicForOneClock(t *testing.T) {
	first := NewSimulator(simulatorPoints())
	second := NewSimulator(simulatorPoints())
	first.Now = func() time.Time { return simNow }
	second.Now = func() time.Time { return simNow }

	a := first.Read([]string{"level", "pump"})
	b := second.Read([]string{"level", "pump"})
	if a["level"].Value != b["level"].Value || a["pump"].Value != b["pump"].Value {
		t.Fatalf("two previews disagreed: %+v vs %+v", a, b)
	}
	if a["level"].Value == nil || a["note"].Value != nil {
		t.Fatalf("read returned %+v", a)
	}
	if a["level"].Quality != QualityGood || !a["level"].Simulated {
		t.Fatalf("value must be marked simulated and good: %+v", a["level"])
	}
}

func TestSimulatorReadOnlyReturnsCatalogPoints(t *testing.T) {
	sim := NewSimulator(simulatorPoints())
	values := sim.Read([]string{"level", "unknown"})
	if _, ok := values["unknown"]; ok {
		t.Fatalf("unknown point returned: %+v", values)
	}
	if _, ok := values["level"]; !ok {
		t.Fatalf("catalog point missing: %+v", values)
	}
}

func TestSimulatorWritesStayInsideThePreview(t *testing.T) {
	sim := NewSimulator(simulatorPoints())
	sim.Now = func() time.Time { return simNow }

	outcome := sim.Write("pump", 1)
	if outcome.Status != WriteSimulated {
		t.Fatalf("write = %+v", outcome)
	}
	if got, _ := sim.Read([]string{"pump"})["pump"].Value.(float64); got != 1 {
		t.Fatalf("simulated value = %v, want 1", got)
	}
	if log := sim.Log(); len(log) != 1 || log[0].Point != "pump" || log[0].Status != WriteSimulated {
		t.Fatalf("write log = %+v", log)
	}

	if outcome := sim.Write("level", 5); outcome.Status != WriteRejected || outcome.Reason != ReasonReadOnly {
		t.Fatalf("read-only write = %+v", outcome)
	}
	if outcome := sim.Write("missing", 5); outcome.Status != WriteRejected || outcome.Reason != ReasonNotInPreview {
		t.Fatalf("unknown write = %+v", outcome)
	}
	if outcome := sim.Write("note", 5); outcome.Status != WriteRejected || outcome.Reason != ReasonNotNumeric {
		t.Fatalf("text write = %+v", outcome)
	}
	if len(sim.Log()) != 1 {
		t.Fatalf("rejected writes must not be logged as simulated: %+v", sim.Log())
	}
}

func TestSimulatorWriteLogIsBounded(t *testing.T) {
	sim := NewSimulator(simulatorPoints())
	for i := 0; i < 80; i++ {
		sim.Write("pump", float64(i%2))
	}
	if len(sim.Log()) != 50 {
		t.Fatalf("log length = %d", len(sim.Log()))
	}
}

func TestStoreScopesSessionsToTheirOwner(t *testing.T) {
	store := NewStore(4, time.Hour)
	session := store.Put(&Session{Owner: "alice", NodeID: "n1", Source: "<div>x</div>", Points: simulatorPoints()})
	if session.ID == "" || len(session.Capability) < 32 {
		t.Fatalf("session identity missing: %+v", session)
	}
	if _, ok := store.Get(session.ID, "bob"); ok {
		t.Fatal("another user read the session")
	}
	if _, ok := store.Get(session.ID, "alice"); !ok {
		t.Fatal("owner could not read the session")
	}
	if _, ok := store.ByCapability(session.Capability); !ok {
		t.Fatal("capability lookup failed")
	}
	if _, ok := store.ByCapability("nope"); ok {
		t.Fatal("invalid capability accepted")
	}
}

func TestStoreExpiresAndBoundsSessions(t *testing.T) {
	store := NewStore(2, time.Hour)
	now := simNow
	store.Now = func() time.Time { return now }

	first := store.Put(&Session{Owner: "alice", NodeID: "n1"})
	store.Put(&Session{Owner: "alice", NodeID: "n2"})
	third := store.Put(&Session{Owner: "alice", NodeID: "n3"})
	if _, ok := store.Get(first.ID, "alice"); ok {
		t.Fatal("oldest session was not evicted at the capacity bound")
	}
	if _, ok := store.Get(third.ID, "alice"); !ok {
		t.Fatal("newest session missing")
	}

	now = now.Add(2 * time.Hour)
	if _, ok := store.Get(third.ID, "alice"); ok {
		t.Fatal("expired session was served")
	}
}

func TestMergeReportsBlocksOnAnyBlockingFinding(t *testing.T) {
	static := []Finding{{Code: "binding_unknown", Severity: SeverityBlocking}}
	report := MergeReports(static, nil)
	if report.Ready || report.Blocking != 1 {
		t.Fatalf("report = %+v", report)
	}
	report = MergeReports([]Finding{{Code: "write_element_not_button", Severity: SeverityWarning}}, nil)
	if !report.Ready {
		t.Fatalf("warnings must not block: %+v", report)
	}
	report = MergeReports(nil, []Finding{{Code: "runtime_error", Severity: SeverityBlocking}})
	if report.Ready || len(report.Findings) != 1 {
		t.Fatalf("client finding not merged: %+v", report)
	}
}

func TestSanitizeClientFindingsRejectsUnknownShapes(t *testing.T) {
	clean, ok := SanitizeClientFindings([]Finding{
		{Code: "runtime_error", Origin: OriginRuntime, Severity: SeverityBlocking, Message: "x is not defined", Line: 12, Column: 3},
		{Code: "layout_clipped", Origin: OriginLayout, Severity: SeverityWarning, Message: "内容被裁切"},
	})
	if !ok || len(clean) != 2 {
		t.Fatalf("sanitize = %+v ok=%v", clean, ok)
	}
	if clean[0].Origin != OriginRuntime || clean[0].Line != 12 {
		t.Fatalf("clean finding = %+v", clean[0])
	}
	if _, ok := SanitizeClientFindings([]Finding{{Code: "made_up_code", Origin: OriginRuntime, Severity: SeverityBlocking, Message: "boom"}}); ok {
		t.Fatal("unknown code accepted")
	}
	if _, ok := SanitizeClientFindings([]Finding{{Code: "runtime_error", Severity: "critical"}}); ok {
		t.Fatal("unknown severity accepted")
	}
	long := make([]Finding, 201)
	for i := range long {
		long[i] = Finding{Code: "runtime_error", Origin: OriginRuntime, Severity: SeverityBlocking, Message: "x"}
	}
	if _, ok := SanitizeClientFindings(long); ok {
		t.Fatal("oversized finding list accepted")
	}
}
