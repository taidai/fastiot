package historyquery

import (
	"encoding/json"
	"time"

	"github.com/simpleiot/simpleiot/data"
)

const DerivedObservationPoint = "easyemsDerivedObservation"

// Read captured evidence instead of re-evaluating old definitions. Source clocks
// can precede storage by an arbitrary duration (configuration inputs); scan the
// existing retained stream before applying the observation-time filter.
func (q *Query) readDerived(node Node, start, end time.Time) (readResult, error) {
	out := readResult{source: "derived_runtime", records: []Record{}, aggregation: "raw", retention: Retention{Kind: "unknown"}}
	if q.SIOT == nil {
		return out, ErrUnavailable
	}
	points, err := SIOTPoints(q.SIOT, node.ID, DerivedObservationPoint, "", time.Time{}, time.Time{})
	if err != nil {
		return out, err
	}
	for _, point := range points {
		if point.Tombstone%2 != 0 {
			continue
		}
		var input struct {
			Value        *float64   `json:"value"`
			Quality      string     `json:"quality"`
			Reason       string     `json:"reason"`
			Unit         string     `json:"unit"`
			Time         *time.Time `json:"time"`
			EvaluatedAt  time.Time  `json:"evaluated_at"`
			TimeSource   string     `json:"time_source"`
			Dependencies []string   `json:"dependencies"`
		}
		if json.Unmarshal([]byte(point.Txt()), &input) != nil || input.EvaluatedAt.IsZero() {
			return out, ErrUnavailable
		}
		record := Record{PointID: node.ID, Value: input.Value, Quality: input.Quality, Reason: input.Reason, Unit: input.Unit, Source: out.source, Text: point.Txt(), ReceivedTime: point.Time, SourceTime: input.EvaluatedAt, TimeSource: input.TimeSource, Kind: "quality_event"}
		if input.Quality == "good" && input.Time != nil {
			record.SourceTime = *input.Time
			record.Kind = "observation"
		} else {
			record.Value = nil
			record.TimeSource = "derived_evaluation"
		}
		if (!start.IsZero() && record.SourceTime.Before(start)) || (!end.IsZero() && record.SourceTime.After(end)) {
			continue
		}
		if q.AggregateMemberAllowed != nil {
			for _, id := range input.Dependencies {
				if !q.AggregateMemberAllowed(id) {
					return out, ErrForbidden
				}
			}
		}
		out.records = append(out.records, record)
		if record.Value != nil {
			scalar := data.NewPointFloat(data.PointTypeValue, "0", *record.Value)
			scalar.Time = record.SourceTime
			out.points = append(out.points, scalar)
		}
	}
	return out, nil
}
