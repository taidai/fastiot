package calculation

import (
	"fmt"
	"sync"
)

type Batch struct {
	ID      string            `json:"id"`
	Results map[string]Result `json:"results"`
}
type Runtime struct {
	mu       sync.RWMutex
	epoch    string
	sequence uint64
	batch    Batch
}

func NewRuntime(epoch string) *Runtime {
	return &Runtime{epoch: epoch, batch: Batch{ID: epoch + "/0", Results: map[string]Result{}}}
}
func (r *Runtime) Publish(formulas map[string]Formula, snapshot Snapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := fmt.Sprintf("%s/%d", r.epoch, r.sequence+1)
	results := EvaluateBatch(formulas, snapshot, id, r.batch.Results)
	changed := len(results) != len(r.batch.Results)
	for _, result := range results {
		if result.BatchID == id {
			changed = true
			break
		}
	}
	if !changed {
		return
	}
	r.sequence++
	r.batch = Batch{ID: id, Results: results}
}
func cloneSample(s Sample) Sample {
	if s.Value != nil {
		v := *s.Value
		s.Value = &v
	}
	return s
}
func cloneResult(r Result) Result {
	r.Definition = cloneDefinition(r.Definition)
	r.Dependencies = append([]string(nil), r.Dependencies...)
	if r.Value != nil {
		v := *r.Value
		r.Value = &v
	}
	r.Inputs = append([]Sample(nil), r.Inputs...)
	for i := range r.Inputs {
		r.Inputs[i] = cloneSample(r.Inputs[i])
	}
	r.Leaves = append([]Sample(nil), r.Leaves...)
	for i := range r.Leaves {
		r.Leaves[i] = cloneSample(r.Leaves[i])
	}
	if r.Retained != nil {
		old := cloneResult(*r.Retained)
		r.Retained = &old
	}
	return r
}
func (r *Runtime) Snapshot() Batch {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := Batch{ID: r.batch.ID, Results: make(map[string]Result, len(r.batch.Results))}
	for id, value := range r.batch.Results {
		out.Results[id] = cloneResult(value)
	}
	return out
}
func (r *Runtime) Invalidate(ids []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	dependents := map[string][]string{}
	for id, result := range r.batch.Results {
		for _, input := range result.Definition.Inputs {
			dependents[input] = append(dependents[input], id)
		}
	}
	queue := append([]string(nil), ids...)
	seen := map[string]bool{}
	affected := []string{}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		queue = append(queue, dependents[id]...)
		if _, ok := r.batch.Results[id]; ok {
			affected = append(affected, id)
		}
	}
	if len(affected) == 0 {
		return
	}
	r.sequence++
	r.batch.ID = fmt.Sprintf("%s/%d", r.epoch, r.sequence)
	for _, id := range affected {
		if old, ok := r.batch.Results[id]; ok {
			next := old
			next.Quality = "invalid"
			next.Reason = "pending_calculation"
			next.Value = nil
			next.signature = ""
			next.BatchID = r.batch.ID
			r.batch.Results[id] = keepLast(next, old)
		}
	}
}
