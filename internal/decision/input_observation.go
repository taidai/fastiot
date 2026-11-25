package decision

import (
	"encoding/json"
	"time"

	"fastiot/internal/ems/signal"
)

// InputObservation is captured input evidence, not a physical command receipt.
// Time describes its source observation/change; EvaluatedAt never refreshes it.
type InputObservation struct {
	DefinitionVersion uint64     `json:"definition_version,omitempty"`
	Value             *float64   `json:"value"`
	Text              string     `json:"text,omitempty"`
	Kind              string     `json:"kind"`
	Unit              string     `json:"unit,omitempty"`
	Quality           string     `json:"quality"`
	Reason            string     `json:"reason,omitempty"`
	Source            string     `json:"source"`
	Software          bool       `json:"software"`
	Simulated         bool       `json:"simulated"`
	Time              *time.Time `json:"time,omitempty"`
	EvaluatedAt       time.Time  `json:"evaluated_at"`
	TimeSource        string     `json:"time_source,omitempty"`
	Window            string     `json:"window,omitempty"`
	LagSeconds        float64    `json:"lag_seconds"`
	Dependencies      []string   `json:"dependencies,omitempty"`
}

func (input CheckInput) MarshalJSON() ([]byte, error) {
	type plain CheckInput
	if input.Observation == nil {
		return json.Marshal(plain(input))
	}
	var value *float64
	if input.Quality == "good" {
		value = input.Observation.Value
	}
	return json.Marshal(struct {
		plain
		Value *float64 `json:"value"`
	}{plain(input), value})
}

func (input InputObservation) Sample(id string) signal.Sample {
	sample := signal.Sample{Input: id, Quality: signal.Invalid, ReceivedTime: input.EvaluatedAt}
	if input.Time != nil {
		sample.SourceTime = *input.Time
	}
	switch input.Quality {
	case "good":
		sample.Quality = signal.Good
	case "stale":
		sample.Quality = signal.Stale
	case "pending", "uninitialized":
		sample.Quality = signal.Uninitialized
	}
	switch input.Kind {
	case "enum":
		sample.Value = signal.Label(input.Text)
	case "bool":
		if input.Value != nil {
			sample.Value = signal.Flag(*input.Value != 0)
		} else {
			sample.Quality = signal.Invalid
		}
	default:
		if input.Value != nil {
			sample.Value = signal.Num(*input.Value)
		} else if sample.Quality == signal.Good {
			sample.Quality = signal.Invalid
		}
	}
	return sample
}
