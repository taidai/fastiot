package historyquery

import (
	"encoding/json"
	"time"

	"github.com/simpleiot/simpleiot/data"
)

const AggregateValuePoint = "easyemsAggregateValue"
const AggregateResultPoint = "easyemsAggregateResult"

// readAggregate adapts the persisted runtime evidence, not the numeric cache's
// evaluation timestamp. All existing cursor, display aggregation and export
// rules still run in Query. Text retains the full member/window evidence in CSV.
func (q *Query) readAggregate(node Node, start, end time.Time) (readResult, error) {
	out := readResult{source: "aggregate_runtime", records: []Record{}, aggregation: "raw", retention: Retention{Kind: "unknown"}}
	if q.SIOT == nil {
		return out, ErrUnavailable
	}
	// Evidence is stored at evaluation time so quality changes can replace older
	// node state. Its observation time is at most one supported window (24h)
	// earlier; instantaneous inputs have the existing 15s freshness bound.
	scanEnd := end
	if !scanEnd.IsZero() {
		scanEnd = scanEnd.Add(24 * time.Hour)
	}
	points, err := SIOTPoints(q.SIOT, node.ID, AggregateResultPoint, "", start, scanEnd)
	if err != nil {
		return out, err
	}
	for _, point := range points {
		if point.Tombstone%2 != 0 {
			continue
		}
		var result struct {
			Value       *float64   `json:"value"`
			Quality     string     `json:"quality"`
			Time        *time.Time `json:"time"`
			EvaluatedAt time.Time  `json:"evaluated_at"`
			TimeSource  string     `json:"time_source"`
			Config      struct {
				Units   string   `json:"units"`
				Members []string `json:"members"`
			} `json:"config"`
			Members []struct {
				NodeID string `json:"node_id"`
			} `json:"members"`
			Window *struct {
				End time.Time `json:"end"`
			} `json:"window"`
		}
		if json.Unmarshal([]byte(point.Txt()), &result) != nil || result.EvaluatedAt.IsZero() {
			return out, ErrUnavailable
		}
		record := Record{PointID: node.ID, Value: result.Value, Quality: result.Quality, SourceTime: result.EvaluatedAt, ReceivedTime: result.EvaluatedAt, TimeSource: result.TimeSource, Unit: result.Config.Units, Source: out.source, Text: point.Txt(), Kind: "observation"}
		if result.Time != nil {
			record.SourceTime = *result.Time
		}
		if result.Value == nil {
			record.Kind = "quality_event"
			if result.Time == nil {
				record.TimeSource = "aggregate_evaluation"
			}
		}
		if (!start.IsZero() && record.SourceTime.Before(start)) || (!end.IsZero() && record.SourceTime.After(end)) {
			continue
		}
		if q.AggregateMemberAllowed != nil {
			for _, id := range result.Config.Members {
				if !q.AggregateMemberAllowed(id) {
					return out, ErrForbidden
				}
			}
			for _, member := range result.Members {
				if !q.AggregateMemberAllowed(member.NodeID) {
					return out, ErrForbidden
				}
			}
		}
		if result.Window != nil {
			record.BucketEnd = result.Window.End
		}
		out.records = append(out.records, record)
		if record.Value != nil {
			scalar := data.NewPointFloat(AggregateValuePoint, "0", *record.Value)
			scalar.Time = record.SourceTime
			out.points = append(out.points, scalar)
		}
	}
	return out, nil
}
