// Package historyquery owns the history read path: choosing between runtime
// history and the SIOT fallback, merging records, quality and late-sample
// semantics, pagination cursors, aggregation and the export cap. HTTP callers
// only map request parameters and render the result.
package historyquery

import (
	"encoding/json"
	"errors"
	"sort"
	"time"

	"fastiot/internal/ems/history"
	"fastiot/internal/ems/signal"
	"github.com/simpleiot/simpleiot/data"
)

// PageLimit caps one query page; ExportLimit caps one export.
const (
	PageLimit   = 2000
	ExportLimit = 10000
)

// Source values on PointResult.Source and Record.Source name the history
// channel that answered the request.
const (
	SourceSIOT    = "siot"
	SourceRuntime = "modbus_runtime"
)

// Record is one history row. Kind is always observation or quality_event.
type Record struct {
	PointID      string    `json:"point_id"`
	DeviceID     string    `json:"device_id,omitempty"`
	SourceTime   time.Time `json:"source_time,omitzero"`
	ReceivedTime time.Time `json:"received_time,omitzero"`
	BucketEnd    time.Time `json:"bucket_end,omitzero"`
	Quality      string    `json:"quality,omitempty"`
	Value        *float64  `json:"value,omitempty"`
	Text         string    `json:"text,omitempty"`
	// Source is the history channel that produced this record: SourceSIOT or
	// SourceRuntime.
	Source string `json:"source"`
	// Origin is the upstream origin the SIOT adapter recorded for the point;
	// empty for runtime history.
	Origin      string `json:"origin,omitempty"`
	Unit        string `json:"unit,omitempty"`
	ConfigStamp string `json:"config_stamp,omitempty"`
	TimeSource  string `json:"time_source,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Late        bool   `json:"late,omitempty"`
	SampleCount int    `json:"sample_count,omitempty"`
	Aggregate   string `json:"aggregate,omitempty"`
}

// Retention describes a storage policy, not a fabricated duration or a promise
// that every requested observation still exists. Unknown is explicit for adapters
// that do not expose their retention configuration.
type Retention struct {
	Kind  string `json:"kind"`
	Limit int    `json:"limit,omitempty"`
}

// PointResult is the paginated history of one point.
type PointResult struct {
	NodeID           string      `json:"node_id"`
	DeviceID         string      `json:"device_id,omitempty"`
	Source           string      `json:"source"`
	SampleIntervalMS int         `json:"sample_interval_ms"`
	ActualIntervalMS int         `json:"actual_interval_ms"`
	Aggregation      string      `json:"aggregation"`
	Retention        Retention   `json:"retention"`
	Points           data.Points `json:"points,omitempty"`
	Records          []Record    `json:"records"`
	// Matched is the number of records matching the time window and filter
	// before the pagination cursor is applied; it is the same for a query page
	// and for an export.
	Matched         int       `json:"matched"`
	Returned        int       `json:"returned"`
	Truncated       bool      `json:"truncated"`
	NextBefore      time.Time `json:"next_before,omitzero"`
	NextBeforeCount int       `json:"next_before_count"`
}

// Cursor is the pagination position: only rows older than Before are returned,
// with Count extra rows sharing Before kept for the boundary time.
type Cursor struct {
	Before time.Time
	Count  int
}

// Request is one history query or export. Export and query share the same
// object, time window and filter; Export only changes the cap and the page
// size used to detect it.
type Request struct {
	NodeIDs   []string
	Type      string
	Key       string
	Start     time.Time
	End       time.Time
	Cursor    Cursor
	Aggregate time.Duration
	Limit     int
	Export    bool
}

// Node is the resolved product node a history request reads.
type Node struct {
	Derived   bool
	Aggregate bool
	ID        string
	Type      string
	Units     string
}

// Resolver resolves product nodes, returning ErrForbidden or ErrNotSupported.
type Resolver interface {
	Resolve(nodeID string) (Node, error)
}

// SIOT is the product history adapter.
type SIOT interface {
	PointHistory(nodeID, typ, key string, start, end time.Time, limit int) ([]data.Point, error)
}

// Runtime is the EMS runtime history adapter. Locate reports whether a point
// belongs to a runtime device; Timeline returns observations and quality
// events as one typed sequence.
type Runtime interface {
	Locate(pointID string) (deviceID string, intervalMS int, ok bool, err error)
	Timeline(deviceID, pointID string, start, end time.Time) []history.Sample
}

var (
	ErrForbidden           = errors.New("history_forbidden")
	ErrNotSupported        = errors.New("history_not_supported")
	ErrUnavailable         = errors.New("history_unavailable")
	ErrTimeCursorSaturated = errors.New("history_time_cursor_saturated")
	ErrInvalidTimeRange    = errors.New("invalid_time_range")
	ErrNodeIDRequired      = errors.New("node_id_required")
	ErrTooManyPoints       = errors.New("too_many_history_points")
	ErrExportLimitExceeded = errors.New("history_export_limit_exceeded")
)

// Query owns source selection and the record pipeline.
type Query struct {
	// AggregateMemberAllowed authorizes nested member evidence before projection
	// or display aggregation. HTTP adapters supply the current reader's scope.
	AggregateMemberAllowed func(string) bool
	Nodes                  Resolver
	SIOT                   SIOT
	Runtime                Runtime
}

type readResult struct {
	records          []Record
	points           data.Points
	deviceID, source string
	sampleIntervalMS int
	actualIntervalMS int
	aggregation      string
	retention        Retention
}

// Execute resolves each node, selects its source, merges records, aggregates,
// applies the cursor and paginates. The returned total is the number of
// records matching the time window and filter before the pagination cursor,
// identical for a query page and for an export.
func (q *Query) Execute(req Request) ([]PointResult, int, error) {
	if !req.Start.IsZero() && !req.End.IsZero() && req.End.Before(req.Start) {
		return nil, 0, ErrInvalidTimeRange
	}
	if len(req.NodeIDs) == 0 {
		return nil, 0, ErrNodeIDRequired
	}
	if len(req.NodeIDs) > ExportLimit/PageLimit {
		return nil, 0, ErrTooManyPoints
	}
	limit := req.Limit
	if limit <= 0 {
		limit = PageLimit
	}
	results := make([]PointResult, 0, len(req.NodeIDs))
	total, matched := 0, 0
	for _, id := range req.NodeIDs {
		node, err := q.Nodes.Resolve(id)
		if err != nil {
			return nil, 0, err
		}
		read, err := q.read(node, req.Type, req.Key, req.Start, req.End, req.Aggregate)
		if err != nil {
			return nil, 0, err
		}
		filtered := filterRecords(read.records, req.Cursor)
		matchedCount := len(read.records)
		matched += matchedCount
		total += len(filtered)
		pageLimit := limit
		if req.Export {
			pageLimit = ExportLimit + 1
		}
		records := filtered
		next := Cursor{}
		truncated := len(records) > pageLimit
		if truncated {
			records = records[len(records)-pageLimit:]
			next.Before = records[0].SourceTime
			for _, candidate := range filtered {
				if candidate.SourceTime.Equal(next.Before) {
					next.Count++
				}
			}
			for _, candidate := range records {
				if candidate.SourceTime.Equal(next.Before) {
					next.Count--
				}
			}
		}
		results = append(results, PointResult{
			NodeID: id, DeviceID: read.deviceID, Source: read.source, SampleIntervalMS: read.sampleIntervalMS,
			ActualIntervalMS: read.actualIntervalMS, Aggregation: read.aggregation, Retention: read.retention,
			Points: read.points, Records: records, Matched: matchedCount, Returned: len(records),
			Truncated: truncated, NextBefore: next.Before, NextBeforeCount: next.Count,
		})
	}
	if req.Export && total > ExportLimit {
		return nil, 0, ErrExportLimitExceeded
	}
	return results, matched, nil
}

// read selects the source for one node and applies aggregation to whichever
// source answered, so a SIOT fallback is never silently left out of an
// aggregated result.
func (q *Query) read(node Node, typ, key string, start, end time.Time, aggregate time.Duration) (readResult, error) {
	out := readResult{source: SourceSIOT, records: []Record{}, aggregation: "raw", retention: Retention{Kind: "unknown"}}
	if node.Derived {
		if typ != data.PointTypeValue || (key != "" && key != "0") {
			return out, ErrNotSupported
		}
		var err error
		out, err = q.readDerived(node, start, end)
		if err != nil {
			return out, err
		}
	} else if node.Aggregate {
		if typ != AggregateValuePoint || (key != "" && key != "0") {
			return out, ErrNotSupported
		}
		var err error
		out, err = q.readAggregate(node, start, end)
		if err != nil {
			return out, err
		}
	} else if typ == AggregateValuePoint {
		return out, ErrNotSupported
	}
	runtimePoint := node.Type == data.NodeTypeModbusIO && q.Runtime != nil
	if runtimePoint {
		deviceID, intervalMS, ok, err := q.Runtime.Locate(node.ID)
		if err != nil {
			return out, err
		}
		if ok {
			out.deviceID, out.source, out.sampleIntervalMS = deviceID, SourceRuntime, intervalMS
			out.actualIntervalMS = intervalMS
			if policy, ok := q.Runtime.(interface{ Retention() Retention }); ok {
				out.retention = policy.Retention()
			}
			for _, sample := range q.Runtime.Timeline(deviceID, node.ID, start, end) {
				if sample.SourceTime.IsZero() {
					continue
				}
				out.records = append(out.records, sampleRecord(sample, node.ID, deviceID, out.source))
			}
		} else {
			runtimePoint = false
		}
	}
	if !runtimePoint && !node.Aggregate && !node.Derived {
		if q.SIOT == nil {
			return out, ErrUnavailable
		}
		points, err := SIOTPoints(q.SIOT, node.ID, typ, key, start, end)
		if err != nil {
			return out, err
		}
		out.points = points
		out.records = siotRecords(node, points)
	}
	sortRecords(out.records)
	if aggregate == 0 {
		return out, nil
	}
	// A requested display window cannot create observations faster than the
	// declared sampling cadence. Report the actual window independently.
	if cadence := time.Duration(out.sampleIntervalMS) * time.Millisecond; aggregate < cadence {
		aggregate = cadence
	}
	out.actualIntervalMS, out.aggregation = int(aggregate/time.Millisecond), "mean"
	out.records = aggregateRecords(out.records, aggregate, node.ID, out.deviceID, out.source)
	return out, nil
}

// SIOTPoints pages the SIOT adapter until the requested window is exhausted.
func SIOTPoints(reader SIOT, nodeID, typ, key string, start, end time.Time) ([]data.Point, error) {
	result := []data.Point{}
	for {
		points, err := reader.PointHistory(nodeID, typ, key, start, end, PageLimit)
		if err != nil {
			return nil, err
		}
		if len(points) == 0 {
			break
		}
		result = append(result, points...)
		if len(points) < PageLimit {
			break
		}
		oldest := points[0].Time
		if oldest.Equal(points[len(points)-1].Time) {
			return nil, ErrTimeCursorSaturated
		}
		if !start.IsZero() && !oldest.After(start) {
			break
		}
		end = oldest.Add(-time.Nanosecond)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Time.Equal(result[j].Time) {
			return result[i].Origin < result[j].Origin
		}
		return result[i].Time.Before(result[j].Time)
	})
	return result, nil
}

func siotRecords(node Node, points data.Points) []Record {
	records := make([]Record, 0, len(points))
	for _, point := range points {
		record := Record{PointID: node.ID, SourceTime: point.Time, Text: point.Txt(), Source: SourceSIOT, Origin: point.Origin, Unit: node.Units, Kind: history.KindObservation}
		if value, err := point.ValueFloat(); err == nil {
			v := value
			record.Value = &v
		}
		records = append(records, record)
	}
	return records
}

func sampleRecord(sample history.Sample, nodeID, deviceID, source string) Record {
	record := Record{
		PointID: nodeID, DeviceID: deviceID, SourceTime: sample.SourceTime, ReceivedTime: sample.ReceivedTime,
		Quality: sample.Quality.String(), Source: source, Unit: sample.Unit, ConfigStamp: sample.ConfigStamp,
		TimeSource: sample.TimeSource, Kind: history.KindObservation, Reason: sample.Reason, Late: sample.Late,
	}
	if sample.Kind == history.KindQualityEvent {
		record.Kind = history.KindQualityEvent
		return record
	}
	switch sample.Value.Kind {
	case signal.Number:
		value := sample.Value.Num
		record.Value = &value
	case signal.Bool:
		value := data.BoolToFloat(sample.Value.Bool)
		record.Value = &value
	}
	return record
}

// aggregateRecords means good numeric observations per bucket and carries
// non-good observations and quality events unchanged. A record with no declared
// quality is not treated as unavailable: SIOT fallback rows have no quality but
// are still real history.
func aggregateRecords(records []Record, aggregate time.Duration, nodeID, deviceID, source string) []Record {
	buckets := []Record{}
	events := make([]Record, 0)
	var bucketStart time.Time
	var sum float64
	var late bool
	count := 0
	quality, unit, timeSource := "", "", ""
	var evidence []json.RawMessage
	flush := func() {
		if count == 0 {
			return
		}
		value := sum / float64(count)
		text := ""
		if len(evidence) > 0 {
			encoded, _ := json.Marshal(map[string]any{"observations": evidence})
			text = string(encoded)
		}
		buckets = append(buckets, Record{
			PointID: nodeID, DeviceID: deviceID, SourceTime: bucketStart, BucketEnd: bucketStart.Add(aggregate),
			Quality: quality, Value: &value, Text: text, Source: source, Unit: unit, TimeSource: timeSource,
			SampleCount: count, Aggregate: aggregate.String(), Kind: history.KindObservation, Late: late,
		})
	}
	for _, record := range records {
		if record.Kind == history.KindQualityEvent {
			events = append(events, record)
			continue
		}
		if record.Quality != "" && record.Quality != signal.Good.String() {
			// Invalid/stale observations are evidence, not missing inputs to
			// discard. Keep original times and kind beside the numeric mean.
			events = append(events, record)
			continue
		}
		if record.Value == nil {
			continue
		}
		at := record.SourceTime.Truncate(aggregate)
		if count > 0 && !at.Equal(bucketStart) {
			flush()
			count = 0
			sum = 0
		}
		if count == 0 {
			evidence = nil
			late = false
			bucketStart = at
			quality = record.Quality
			unit = record.Unit
			timeSource = record.TimeSource
		} else {
			if unit != record.Unit {
				unit = ""
			}
			if timeSource != record.TimeSource {
				timeSource = ""
			}
		}
		if (source == "derived_runtime" || source == "aggregate_runtime") && json.Valid([]byte(record.Text)) {
			evidence = append(evidence, json.RawMessage(record.Text))
		}
		late = late || record.Late
		sum += *record.Value
		count++
	}
	flush()
	merged := append(buckets, events...)
	sortRecords(merged)
	return merged
}

func sortRecords(records []Record) {
	sort.SliceStable(records, func(i, j int) bool { return records[i].SourceTime.Before(records[j].SourceTime) })
}

func filterRecords(records []Record, cursor Cursor) []Record {
	filtered := make([]Record, 0, len(records))
	boundarySeen := 0
	for _, record := range records {
		if cursor.Before.IsZero() || record.SourceTime.Before(cursor.Before) {
			filtered = append(filtered, record)
			continue
		}
		if cursor.Count >= 0 && record.SourceTime.Equal(cursor.Before) && boundarySeen < cursor.Count {
			filtered = append(filtered, record)
			boundarySeen++
		}
	}
	return filtered
}
