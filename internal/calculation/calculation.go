package calculation

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

type Definition struct {
	Operator  string   `json:"operator"`
	Inputs    []string `json:"inputs"`
	Kind      string   `json:"kind"`
	Unit      string   `json:"unit"`
	MaxSkewMS *int64   `json:"max_skew_ms,omitempty"`
}
type Metadata struct {
	Kind string `json:"kind"`
	Unit string `json:"unit"`
}
type Error struct {
	NodeID  string `json:"node_id"`
	Code    string `json:"code"`
	InputID string `json:"input_id,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s (%s)", e.NodeID, e.Code, e.InputID) }
func (d Definition) ValidateDraft() error {
	if (d.Operator != "add" && d.Operator != "divide") || len(d.Inputs) != 2 || d.Kind != "number" || strings.TrimSpace(d.Unit) == "" {
		return &Error{Code: "invalid_definition"}
	}
	if d.MaxSkewMS != nil && (*d.MaxSkewMS < 0 || *d.MaxSkewMS > math.MaxInt64/int64(time.Millisecond)) {
		return &Error{Code: "invalid_time_skew"}
	}
	return nil
}
func (d Definition) Validate() error {
	if err := d.ValidateDraft(); err != nil {
		return err
	}
	if d.Inputs[0] == "" || d.Inputs[1] == "" {
		return &Error{Code: "missing_reference"}
	}
	return nil
}
func dependencyOrder(definitions map[string]Definition) ([]string, []string) {
	pending := map[string]int{}
	dependents := map[string][]string{}
	for id, d := range definitions {
		pending[id] = 0
		for _, input := range d.Inputs {
			if _, ok := definitions[input]; ok {
				pending[id]++
				dependents[input] = append(dependents[input], id)
			}
		}
	}
	ready := []string{}
	for id, count := range pending {
		if count == 0 {
			ready = append(ready, id)
		}
	}
	order := make([]string, 0, len(definitions))
	for len(ready) > 0 {
		sort.Strings(ready)
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		for _, next := range dependents[id] {
			pending[next]--
			if pending[next] == 0 {
				ready = append(ready, next)
			}
		}
	}
	blocked := []string{}
	for id, count := range pending {
		if count > 0 {
			blocked = append(blocked, id)
		}
	}
	sort.Strings(blocked)
	return order, blocked
}
func checkInputs(id string, d Definition, metadata map[string]Metadata) error {
	for _, input := range d.Inputs {
		m, ok := metadata[input]
		if !ok {
			return &Error{NodeID: id, Code: "missing_reference", InputID: input}
		}
		if m.Kind != "number" {
			return &Error{NodeID: id, Code: "input_type", InputID: input}
		}
		if strings.TrimSpace(m.Unit) == "" {
			return &Error{NodeID: id, Code: "missing_unit", InputID: input}
		}
	}
	left, right := metadata[d.Inputs[0]], metadata[d.Inputs[1]]
	if left.Unit != right.Unit {
		return &Error{NodeID: id, Code: "unit_mismatch"}
	}
	unit := left.Unit
	if d.Operator == "divide" {
		unit = "1"
	}
	if d.Unit != unit {
		return &Error{NodeID: id, Code: "output_unit"}
	}
	return nil
}
func ValidateGraph(definitions map[string]Definition, sources map[string]Metadata) ([]string, error) {
	for id, d := range definitions {
		if err := d.Validate(); err != nil {
			return nil, &Error{NodeID: id, Code: err.(*Error).Code}
		}
	}
	order, blocked := dependencyOrder(definitions)
	if len(blocked) > 0 {
		return nil, &Error{NodeID: blocked[0], Code: "cycle"}
	}
	metadata := make(map[string]Metadata, len(sources)+len(definitions))
	for id, m := range sources {
		metadata[id] = m
	}
	for _, id := range order {
		d := definitions[id]
		if err := checkInputs(id, d, metadata); err != nil {
			return nil, err
		}
		metadata[id] = Metadata{Kind: d.Kind, Unit: d.Unit}
	}
	return order, nil
}
