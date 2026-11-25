package dailystats

import (
	"testing"
	"time"

	"fastiot/internal/ems/historyquery"
)

func num(v float64) *float64 { return &v }
func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}
func observation(t time.Time, value float64) historyquery.Record {
	return historyquery.Record{SourceTime: t, ReceivedTime: t, TimeSource: "device", Kind: "observation", Quality: "good", Value: &value, Unit: "kWh"}
}

func TestRuntimeRejectsGapsDuplicatesAndUnknownStates(t *testing.T) {
	start := at("2026-11-25T00:00:00Z")
	w, _ := Day("2026-11-25", "UTC", start.Add(time.Hour))
	rows := []historyquery.Record{observation(start, 1), observation(start.Add(10*time.Minute), 1), observation(start.Add(10*time.Minute), 1), observation(start.Add(20*time.Minute), 0), observation(start.Add(30*time.Minute), 7), observation(start.Add(40*time.Minute), 1), observation(start.Add(time.Hour), 1)}
	got := Runtime(w, RuntimeBinding{PointID: "run", RunningValue: num(1), StoppedValue: num(0), MaxGapSeconds: 600}, rows)
	if got.Value == nil || *got.Value != 1.0/3 || got.CoveredSeconds != 1200 {
		t.Fatalf("%+v", got)
	}
	// Conflict and an invalidation split evidence, without filling to next good row.
	rows[2] = observation(start.Add(10*time.Minute), 0)
	got = Runtime(w, RuntimeBinding{PointID: "run", RunningValue: num(1), StoppedValue: num(0), MaxGapSeconds: 600}, rows)
	if got.Value != nil {
		t.Fatalf("conflicting duplicate created runtime: %+v", got)
	}
}
func TestFreshnessUsesObservationNotReceptionAndRejectsFuture(t *testing.T) {
	now := at("2026-11-25T12:00:00Z")
	row := observation(now.Add(-10*time.Minute), 1)
	row.ReceivedTime = now
	b := FreshnessBinding{PointID: "run", MaxAgeSeconds: 60}
	got := Freshness(now, b, []historyquery.Record{row})
	if got.Value == nil || *got.Value != 600 || got.Status != "stale" {
		t.Fatalf("%+v", got)
	}
	row.SourceTime = now.Add(time.Second)
	if got = Freshness(now, b, []historyquery.Record{row}); got.Value != nil || got.Reason != "future_observation" {
		t.Fatalf("%+v", got)
	}
	row.SourceTime = now
	row.TimeSource = "platform_collection"
	if got = Freshness(now, b, []historyquery.Record{row}); got.Value == nil || *got.Value != 0 {
		t.Fatalf("successful runtime acquisition: %+v", got)
	}
	row.SourceTime = now
	row.TimeSource = ""
	if got = Freshness(now, b, []historyquery.Record{row}); got.Value != nil {
		t.Fatalf("untrusted time: %+v", got)
	}
}

func TestCommunicationWindowIsMinuteAlignedAndZeroAttemptsUnavailable(t *testing.T) {
	now := at("2026-11-25T12:05:42Z")
	got := Communication(now, 10, 2)
	if got.Value == nil || *got.Value != 80 || got.Successes != 8 || got.Attempts != 10 || !got.Start.Equal(at("2026-11-25T12:00:00Z")) || !got.End.Equal(now) {
		t.Fatalf("%+v", got)
	}
	if got = Communication(now, 0, 0); got.Value != nil || got.Reason != "no_attempts" {
		t.Fatalf("%+v", got)
	}
}

func TestDailyEvidenceQualityZeroAndBoundaryRules(t *testing.T) {
	start := at("2026-11-25T00:00:00Z")
	w, _ := Day("2026-11-25", "UTC", start.Add(time.Hour))
	binding := EnergyBinding{PointID: "meter", Unit: "kWh", MaxGapSeconds: 3600, ResetPolicy: "discard_interval"}
	zero := []historyquery.Record{observation(start, 7), observation(start.Add(time.Hour), 7)}
	got := Energy(w, binding, zero)
	if got.Value == nil || *got.Value != 0 || got.Status != "available" || got.Coverage != 1 {
		t.Fatalf("real zero: %+v", got)
	}
	invalid := historyquery.Record{SourceTime: start.Add(30 * time.Minute), Kind: "quality_event", Quality: "invalid"}
	got = Energy(w, binding, append(zero, invalid))
	if got.Value != nil || got.CoveredSeconds != 0 {
		t.Fatalf("quality event bridged: %+v", got)
	}
	zero[0].Quality = ""
	zero[1].TimeSource = ""
	got = Energy(w, binding, zero)
	if got.Value != nil || got.Reason != "quality_or_time_metadata_missing" {
		t.Fatalf("missing metadata: %+v", got)
	}
	// A cross-midnight pair is not proportionally interpolated into this day.
	got = Energy(w, binding, []historyquery.Record{observation(start.Add(-time.Minute), 10), observation(start.Add(time.Minute), 12)})
	if got.Value != nil {
		t.Fatalf("midnight interpolation: %+v", got)
	}
	for _, zone := range []string{"", "Local", "made/up"} {
		if _, err := Day("", zone, start); err == nil {
			t.Fatalf("accepted zone %q", zone)
		}
	}
	if _, err := Day("2026-02-30", "UTC", start); err == nil {
		t.Fatal("accepted impossible date")
	}
	if _, err := Day("2026-11-26", "UTC", start); err == nil {
		t.Fatal("accepted future day")
	}
}

func TestEnergyOverflowIsUnavailable(t *testing.T) {
	start := at("2026-11-25T00:00:00Z")
	w, _ := Day("2026-11-25", "UTC", start.Add(time.Hour))
	rows := []historyquery.Record{observation(start, 0), observation(start.Add(time.Minute), 1e308), observation(start.Add(2*time.Minute), 0), observation(start.Add(3*time.Minute), 1e308)}
	got := Energy(w, EnergyBinding{PointID: "meter", Unit: "kWh", MaxGapSeconds: 60, ResetPolicy: "discard_interval"}, rows)
	if got.Value != nil || got.Reason != "numeric_overflow" {
		t.Fatalf("%+v", got)
	}
}

func TestDayUsesCalendarBoundaries(t *testing.T) {
	for _, tc := range []struct {
		date  string
		hours float64
	}{{"2026-03-08", 23}, {"2026-11-01", 25}} {
		w, err := Day(tc.date, "America/New_York", at("2026-11-25T12:00:00Z"))
		if err != nil || w.End.Sub(w.Start).Hours() != tc.hours {
			t.Fatalf("%s: %+v %v", tc.date, w, err)
		}
	}
}
func TestCumulativeEnergyDoesNotBridgeResetOrMissingSegments(t *testing.T) {
	start := at("2026-11-25T00:00:00Z")
	w, _ := Day("2026-11-25", "UTC", start.Add(time.Hour))
	rows := []historyquery.Record{observation(start, 100), observation(start.Add(10*time.Minute), 110), observation(start.Add(20*time.Minute), 2), observation(start.Add(30*time.Minute), 5), observation(start.Add(time.Hour), 20)}
	got := Energy(w, EnergyBinding{PointID: "meter", Unit: "kWh", MaxGapSeconds: 600, ResetPolicy: "discard_interval"}, rows)
	if got.Value == nil || *got.Value != 13 || got.CoveredSeconds != 1200 || got.Coverage != 1.0/3 || got.Status != "partial" || got.Resets != 1 {
		t.Fatalf("%+v", got)
	}
}
