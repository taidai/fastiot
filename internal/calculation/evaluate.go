package calculation

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"
)

type Formula struct {
	Definition Definition
	Version    uint64
}
type Sample struct {
	ID           string    `json:"id"`
	Kind         string    `json:"kind"`
	Unit         string    `json:"unit"`
	Value        *float64  `json:"value,omitempty"`
	Quality      string    `json:"quality"`
	Reason       string    `json:"reason,omitempty"`
	Source       string    `json:"source"`
	Simulated    bool      `json:"simulated"`
	Version      string    `json:"version,omitempty"`
	TimeSource   string    `json:"time_source,omitempty"`
	SourceTime   time.Time `json:"source_time,omitzero"`
	ReceivedTime time.Time `json:"received_time,omitzero"`
	ChangedAt    time.Time `json:"changed_at,omitzero"`
	ValidUntil   time.Time `json:"valid_until,omitzero"`
}
type Snapshot struct {
	At     time.Time
	Inputs map[string]Sample
}
type Result struct {
	ID           string     `json:"id"`
	BatchID      string     `json:"batch_id"`
	Version      uint64     `json:"version"`
	Definition   Definition `json:"definition"`
	Quality      string     `json:"quality"`
	Reason       string     `json:"reason,omitempty"`
	Value        *float64   `json:"value,omitempty"`
	Unit         string     `json:"unit"`
	CalculatedAt time.Time  `json:"calculated_at"`
	ValidUntil   time.Time  `json:"valid_until,omitzero"`
	Inputs       []Sample   `json:"inputs"`
	Leaves       []Sample   `json:"leaves"`
	Simulated    bool       `json:"simulated"`
	Retained     *Result    `json:"retained,omitempty"`
	Dependencies []string   `json:"-"`
	signature    string
}

func keepLast(result Result, previous Result) Result {
	if result.Quality != "good" {
		if previous.Quality == "good" && previous.Value != nil {
			previous.Retained = nil
			result.Retained = &previous
		} else {
			result.Retained = previous.Retained
		}
	}
	return result
}
func (r Result) sample() Sample {
	return Sample{ID: r.ID, Kind: "number", Unit: r.Unit, Value: r.Value, Quality: r.Quality, Reason: r.Reason, Source: "calculation", Simulated: r.Simulated, Version: r.BatchID + "/" + strconv.FormatUint(r.Version, 10), ValidUntil: r.ValidUntil}
}

func EvaluateBatch(formulas map[string]Formula, snapshot Snapshot, batchID string, previous map[string]Result) map[string]Result {
	definitions := map[string]Definition{}
	metadata := map[string]Metadata{}
	values := map[string]Sample{}
	for id, f := range formulas {
		definitions[id] = f.Definition
	}
	for id, s := range snapshot.Inputs {
		s.ID = id
		if s.Value != nil {
			value := *s.Value
			s.Value = &value
			if math.IsNaN(value) || math.IsInf(value, 0) {
				s.Value = nil
				s.Quality = "invalid"
				s.Reason = "nonfinite_input"
			}
		}
		freshness := Freshness(s, snapshot.At)
		s.Quality, s.Reason = freshness.Quality.String(), freshness.Reason
		metadata[id] = Metadata{Kind: s.Kind, Unit: s.Unit}
		values[id] = s
	}
	order, blocked := dependencyOrder(definitions)
	results := map[string]Result{}
	for _, id := range blocked {
		f := formulas[id]
		results[id] = keepLast(Result{ID: id, BatchID: batchID, Version: f.Version, Definition: cloneDefinition(f.Definition), Quality: "invalid", Reason: "cycle", Unit: f.Definition.Unit, CalculatedAt: snapshot.At}, previous[id])
	}
	for _, id := range order {
		f := formulas[id]
		d := f.Definition
		result := Result{ID: id, BatchID: batchID, Version: f.Version, Definition: cloneDefinition(d), Quality: "invalid", Unit: d.Unit, CalculatedAt: snapshot.At}
		leaves := map[string]Sample{}
		dependencies := map[string]bool{}
		for _, ref := range d.Inputs {
			dependencies[ref] = true
			value, ok := values[ref]
			if !ok {
				value = Sample{ID: ref, Quality: "invalid", Reason: "missing_reference"}
			}
			result.Inputs = append(result.Inputs, value)
			if upstream, ok := results[ref]; ok {
				for _, dependency := range upstream.Dependencies {
					dependencies[dependency] = true
				}
				for _, leaf := range upstream.Leaves {
					leaves[leaf.ID] = leaf
				}
			} else {
				leaves[ref] = value
			}
		}
		for dependency := range dependencies {
			result.Dependencies = append(result.Dependencies, dependency)
		}
		sort.Strings(result.Dependencies)
		ids := []string{}
		for ref := range leaves {
			ids = append(ids, ref)
		}
		sort.Strings(ids)
		for _, ref := range ids {
			leaf := leaves[ref]
			result.Leaves = append(result.Leaves, leaf)
			if leaf.Simulated || leaf.Source == "simulation" {
				result.Simulated = true
			}
			if !leaf.ValidUntil.IsZero() && (result.ValidUntil.IsZero() || leaf.ValidUntil.Before(result.ValidUntil)) {
				result.ValidUntil = leaf.ValidUntil
			}
		}
		encoded, _ := json.Marshal(struct {
			Formula        Formula
			Inputs, Leaves []Sample
		}{f, result.Inputs, result.Leaves})
		result.signature = fmt.Sprintf("%x", sha256.Sum256(encoded))
		if old, ok := previous[id]; ok && old.signature == result.signature {
			results[id] = old
			values[id] = old.sample()
			metadata[id] = Metadata{Kind: "number", Unit: old.Unit}
			continue
		}
		err := d.Validate()
		if err == nil {
			err = checkInputs(id, d, metadata)
		}
		if err != nil {
			result.Reason = err.Error()
		} else {
			for _, input := range result.Inputs {
				if input.Quality != "good" || input.Value == nil {
					result.Reason = fmt.Sprintf("input_unavailable:%s:%s", input.ID, input.Reason)
					break
				}
			}
			if result.Reason == "" && d.MaxSkewMS != nil {
				var earliest, latest time.Time
				for _, leaf := range result.Leaves {
					if leaf.Source != "observation" {
						continue
					}
					if leaf.SourceTime.IsZero() || leaf.SourceTime.After(snapshot.At) {
						result.Reason = "untrusted_observation_time"
						break
					}
					if earliest.IsZero() || leaf.SourceTime.Before(earliest) {
						earliest = leaf.SourceTime
					}
					if latest.IsZero() || leaf.SourceTime.After(latest) {
						latest = leaf.SourceTime
					}
				}
				if result.Reason == "" && !earliest.IsZero() && latest.Sub(earliest) > time.Duration(*d.MaxSkewMS)*time.Millisecond {
					result.Reason = "time_skew"
				}
			}
			if result.Reason == "" {
				left, right := *result.Inputs[0].Value, *result.Inputs[1].Value
				if d.Operator == "divide" && right == 0 {
					result.Reason = "division_by_zero:" + d.Inputs[1]
				} else {
					value := left + right
					if d.Operator == "divide" {
						value = left / right
					}
					if math.IsNaN(value) || math.IsInf(value, 0) {
						result.Reason = "nonfinite_result"
					} else {
						result.Value = &value
						result.Quality = "good"
					}
				}
			}
		}
		result = keepLast(result, previous[id])
		results[id] = result
		values[id] = result.sample()
		metadata[id] = Metadata{Kind: "number", Unit: result.Unit}
	}
	return results
}
