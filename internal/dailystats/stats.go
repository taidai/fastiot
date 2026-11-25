// Package dailystats calculates read-only statistics from recorded evidence.
// It never creates observations, fills missing intervals or writes devices.
package dailystats

import (
	"errors"
	"math"
	"sort"
	"time"
	_ "time/tzdata"

	"fastiot/internal/ems/historyquery"
)

type Window struct {
	Date     string    `json:"date"`
	TimeZone string    `json:"time_zone"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Through  time.Time `json:"through"`
}

func Day(date, zone string, now time.Time) (Window, error) {
	w := Window{TimeZone: zone}
	if zone == "" || zone == "Local" {
		return w, errors.New("time_zone_required")
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return w, errors.New("invalid_time_zone")
	}
	if date == "" {
		date = now.In(loc).Format(time.DateOnly)
	}
	start, err := time.ParseInLocation(time.DateOnly, date, loc)
	if err != nil || start.Format(time.DateOnly) != date {
		return w, errors.New("invalid_date")
	}
	if start.After(now) {
		return w, errors.New("future_date")
	}
	w.Date, w.Start, w.End = date, start, start.AddDate(0, 0, 1)
	w.Through = w.End
	if now.Before(w.End) {
		w.Through = now
	}
	return w, nil
}

type EnergyBinding struct {
	PointID       string  `json:"point_id"`
	Unit          string  `json:"unit"`
	MaxGapSeconds float64 `json:"max_gap_seconds"`
	ResetPolicy   string  `json:"reset_policy"`
}

type RuntimeBinding struct {
	PointID       string   `json:"point_id"`
	RunningValue  *float64 `json:"running_value"`
	StoppedValue  *float64 `json:"stopped_value"`
	MaxGapSeconds float64  `json:"max_gap_seconds"`
}
type FreshnessBinding struct {
	PointID       string  `json:"point_id"`
	MaxAgeSeconds float64 `json:"max_age_seconds"`
}

// Runtime uses a declared sampled-state contract: the left state describes a
// bounded interval closed by the next valid observation. Nothing is extended
// beyond that observation, across an invalid state, or across a sampling gap.
func Runtime(w Window, b RuntimeBinding, rows []historyquery.Record) Metric {
	if b.PointID == "" {
		return Unavailable("h", "binding_missing")
	}
	if b.MaxGapSeconds <= 0 || !finite(b.MaxGapSeconds) || b.RunningValue == nil || b.StoppedValue == nil || !finite(*b.RunningValue) || !finite(*b.StoppedValue) || *b.RunningValue == *b.StoppedValue {
		return Unavailable("h", "unsupported_runtime_semantics")
	}
	out := intervals(w, rows, b.MaxGapSeconds, func(a, c historyquery.Record, seconds float64) (float64, bool) {
		valid := func(v float64) bool { return v == *b.RunningValue || v == *b.StoppedValue }
		if !valid(*a.Value) || !valid(*c.Value) {
			return 0, false
		}
		if *a.Value == *b.RunningValue {
			return seconds / 3600, true
		}
		return 0, true
	})
	out.Unit, out.PointID = "h", b.PointID
	return out
}
func Freshness(now time.Time, b FreshnessBinding, rows []historyquery.Record) Metric {
	if b.PointID == "" {
		return Unavailable("s", "binding_missing")
	}
	if b.MaxAgeSeconds <= 0 || !finite(b.MaxAgeSeconds) {
		return Unavailable("s", "unsupported_freshness_semantics")
	}
	rows = timeline(rows)
	if len(rows) == 0 {
		return Unavailable("s", "no_observations")
	}
	latest := rows[len(rows)-1]
	if latest.SourceTime.After(now) {
		return Unavailable("s", "future_observation")
	}
	if !trusted(latest) {
		return Unavailable("s", "invalid_or_untrusted_observation")
	}
	age := now.Sub(latest.SourceTime).Seconds()
	out := Metric{Status: "available", Value: &age, Unit: "s", PointID: b.PointID, ObservedAt: &latest.SourceTime, TimeBasis: latest.TimeSource}
	if age > b.MaxAgeSeconds {
		out.Status, out.Reason = "stale", "max_age_exceeded"
	}
	return out
}

type CommunicationMetric struct {
	Status            string    `json:"status"`
	Reason            string    `json:"reason,omitempty"`
	Value             *float64  `json:"value"`
	Unit              string    `json:"unit"`
	Start             time.Time `json:"start"`
	End               time.Time `json:"end"`
	Attempts          int       `json:"attempts"`
	Successes         int       `json:"successes"`
	ResolutionSeconds int       `json:"resolution_seconds"`
	TimeBasis         string    `json:"time_basis"`
	Retention         string    `json:"retention"`
}

// The existing quality source retains process-local minute buckets, not exact
// five-minute event history. Publish that boundary and never relabel lifetime counters.
func Communication(now time.Time, attempts, failures int) CommunicationMetric {
	out := CommunicationMetric{Status: "unavailable", Reason: "no_attempts", Unit: "%", Start: now.Add(-5 * time.Minute).Truncate(time.Minute), End: now, Attempts: attempts, Successes: attempts - failures, ResolutionSeconds: 60, TimeBasis: "platform_poll_attempt", Retention: "process_memory_partial_after_restart"}
	if attempts > 0 && failures >= 0 && failures <= attempts {
		value := 100 * float64(attempts-failures) / float64(attempts)
		out.Value = &value
		out.Status = "available"
		out.Reason = ""
	}
	return out
}

type Metric struct {
	Status          string                  `json:"status"`
	Reason          string                  `json:"reason,omitempty"`
	Value           *float64                `json:"value"`
	Unit            string                  `json:"unit"`
	CoveredSeconds  float64                 `json:"covered_seconds"`
	Coverage        float64                 `json:"coverage"`
	Resets          int                     `json:"resets,omitempty"`
	Source          string                  `json:"source,omitempty"`
	HistoryLifetime string                  `json:"history_lifetime,omitempty"`
	Retention       *historyquery.Retention `json:"retention,omitempty"`
	PointID         string                  `json:"point_id,omitempty"`
	ObservedAt      *time.Time              `json:"observed_at,omitempty"`
	TimeBasis       string                  `json:"time_basis,omitempty"`
}

func Unavailable(unit, reason string) Metric {
	return Metric{Status: "unavailable", Reason: reason, Unit: unit}
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func trusted(r historyquery.Record) bool {
	return r.Kind == "observation" && r.Quality == "good" && r.Value != nil && finite(*r.Value) && !r.SourceTime.IsZero() && (r.TimeSource == "device" || r.TimeSource == "platform_acquisition" || r.TimeSource == "platform_collection") && r.Aggregate == ""
}

// Canonical timestamps are conservative: identical duplicates count once;
// conflicting values or a quality event invalidate the timestamp, not just a row.
func timeline(rows []historyquery.Record) []historyquery.Record {
	rows = append([]historyquery.Record(nil), rows...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].SourceTime.Before(rows[j].SourceTime) })
	out := []historyquery.Record{}
	for _, r := range rows {
		if len(out) > 0 && r.SourceTime.Equal(out[len(out)-1].SourceTime) {
			prev := &out[len(out)-1]
			if !trusted(*prev) || !trusted(r) || *prev.Value != *r.Value || prev.Unit != r.Unit || prev.ConfigStamp != r.ConfigStamp || prev.TimeSource != r.TimeSource {
				prev.Quality = "invalid"
				prev.Reason = "conflicting_timestamp"
			}
			continue
		}
		out = append(out, r)
	}
	return out
}
func intervals(w Window, rows []historyquery.Record, maxGap float64, accept func(a, b historyquery.Record, seconds float64) (float64, bool)) Metric {
	out := Unavailable("", "no_valid_intervals")
	total := 0.0
	rows = timeline(rows)
	for _, r := range rows {
		if r.Kind == "observation" && (r.Quality == "" || r.TimeSource == "") {
			out.Reason = "quality_or_time_metadata_missing"
		}
	}
	for i := 1; i < len(rows); i++ {
		a, b := rows[i-1], rows[i]
		seconds := b.SourceTime.Sub(a.SourceTime).Seconds()
		if a.SourceTime.Before(w.Start) || b.SourceTime.After(w.Through) || seconds <= 0 || seconds > maxGap || !trusted(a) || !trusted(b) || a.ConfigStamp != b.ConfigStamp {
			continue
		}
		delta, ok := accept(a, b, seconds)
		if !ok {
			continue
		}
		total += delta
		if !finite(total) {
			return Unavailable("", "numeric_overflow")
		}
		out.CoveredSeconds += seconds
		at := b.SourceTime
		out.ObservedAt = &at
		out.TimeBasis = b.TimeSource
	}
	if out.CoveredSeconds > 0 {
		out.Value = &total
		out.Coverage = out.CoveredSeconds / w.Through.Sub(w.Start).Seconds()
		out.Status, out.Reason = "partial", "incomplete_coverage"
		if out.Coverage == 1 {
			out.Status, out.Reason = "available", ""
		}
	}
	return out
}
func Energy(w Window, b EnergyBinding, rows []historyquery.Record) Metric {
	if b.PointID == "" {
		return Unavailable("kWh", "binding_missing")
	}
	if b.Unit != "kWh" || b.MaxGapSeconds <= 0 || !finite(b.MaxGapSeconds) || b.ResetPolicy != "discard_interval" {
		return Unavailable("kWh", "unsupported_energy_semantics")
	}
	resets := 0
	out := intervals(w, rows, b.MaxGapSeconds, func(a, c historyquery.Record, _ float64) (float64, bool) {
		if a.Unit != b.Unit || c.Unit != b.Unit || *a.Value < 0 || *c.Value < 0 {
			return 0, false
		}
		if *c.Value < *a.Value {
			resets++
			return 0, false
		}
		return *c.Value - *a.Value, true
	})
	out.Unit, out.PointID, out.Resets = "kWh", b.PointID, resets
	return out
}
