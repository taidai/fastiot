package appapi

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	"fastiot/internal/calculation"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

func copyCalculation(node data.NodeEdge, idMap map[string]string) (string, []string, error) {
	d, err := decodeCalculation(node)
	if err != nil {
		return "", nil, err
	}
	d.Applied = nil
	d.Version = 0
	d.Revision = 1
	d.LastChange = nil
	unresolved := []string{}
	for i, ref := range d.Draft.Inputs {
		if mapped, ok := idMap[ref]; ok {
			d.Draft.Inputs[i] = mapped
		} else {
			d.Draft.Inputs[i] = ""
			unresolved = append(unresolved, fmt.Sprintf("calculation input %d", i+1))
		}
	}
	encoded, err := json.Marshal(d)
	return string(encoded), unresolved, err
}

func (h *Handler) rejectCalculationDependents(w http.ResponseWriter, id string) bool {
	nodes, err := client.GetNodes(h.nc, "all", "all", calculationType, false)
	if err != nil {
		writeError(w, 503, "dependency_check_failed")
		return true
	}
	for _, n := range nodes {
		if n.ID == id || h.users.IsUnder(n.ID, id) {
			continue
		}
		d, err := decodeCalculation(n)
		if err != nil {
			writeError(w, 503, "dependency_check_failed")
			return true
		}
		refs := append([]string(nil), d.Draft.Inputs...)
		if d.Applied != nil {
			refs = append(refs, d.Applied.Inputs...)
		}
		for _, ref := range refs {
			if ref != "" && (ref == id || h.users.IsUnder(ref, id)) {
				writeError(w, 409, "used_by_calculation")
				return true
			}
		}
	}
	return false
}

func calculationAffectedGraph(root string, definitions map[string]calculation.Definition) map[string]calculation.Definition {
	dependents := map[string][]string{}
	for id, d := range definitions {
		for _, input := range d.Inputs {
			dependents[input] = append(dependents[input], id)
		}
	}
	queue := []string{root}
	seen := map[string]bool{}
	selected := map[string]calculation.Definition{}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		queue = append(queue, dependents[id]...)
		for dependency, d := range calculationClosure(id, definitions) {
			selected[dependency] = d
		}
	}
	return selected
}

func calculationClosure(root string, definitions map[string]calculation.Definition) map[string]calculation.Definition {
	selected := map[string]calculation.Definition{}
	queue := []string{root}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if _, ok := selected[id]; ok {
			continue
		}
		if d, ok := definitions[id]; ok {
			selected[id] = d
			queue = append(queue, d.Inputs...)
		}
	}
	return selected
}
func (h *Handler) trialCalculation(w http.ResponseWriter, r *http.Request, identity Identity, node data.NodeEdge, d calculation.Document, catalog calculationCatalog) {
	var input calculationRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Revision != d.Revision {
		writeError(w, 409, "revision_conflict")
		return
	}
	if !h.readableCalculation(identity, d.Draft, catalog) {
		writeError(w, 403, "restricted_dependency")
		return
	}
	all := map[string]calculation.Definition{}
	for id, doc := range catalog.documents {
		if doc.Applied != nil {
			all[id] = *doc.Applied
		}
	}
	all[node.ID] = d.Draft
	definitions := calculationClosure(node.ID, all)
	if _, err := calculation.ValidateGraph(definitions, catalog.sources); err != nil {
		inputError(w, err)
		return
	}
	formulas := map[string]calculation.Formula{}
	leaves := map[string]bool{}
	timed := false
	for id, definition := range definitions {
		formulas[id] = calculation.Formula{Definition: definition, Version: catalog.documents[id].Version}
		if definition.MaxSkewMS != nil {
			timed = true
		}
		for _, ref := range definition.Inputs {
			if _, ok := definitions[ref]; !ok {
				leaves[ref] = true
			}
		}
	}
	now := time.Now()
	snapshot := calculation.Snapshot{At: now, Inputs: map[string]calculation.Sample{}}
	for id := range leaves {
		value, ok := input.Values[id]
		if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
			writeError(w, 400, "missing_or_invalid_trial_input")
			return
		}
		metadata := catalog.sources[id]
		source := "observation"
		n := catalog.nodes[id]
		if n.Type == virtualInputType {
			document, err := decodeVirtualInput(n)
			if err != nil {
				inputError(w, err)
				return
			}
			source = document.Source
		}
		sample := calculation.Sample{ID: id, Kind: metadata.Kind, Unit: metadata.Unit, Value: &value, Quality: "good", Source: source, Simulated: true, Version: "fixed-trial"}
		if source == "observation" {
			at, provided := input.Times[id]
			if !provided {
				if timed {
					writeError(w, 400, "missing_trial_time")
					return
				}
				at = now
			}
			if at.After(now) || at.IsZero() {
				writeError(w, 400, "invalid_trial_time")
				return
			}
			sample.SourceTime = at
			sample.ReceivedTime = now
			sample.TimeSource = "trial_fixed"
		} else {
			sample.ChangedAt = now
		}
		snapshot.Inputs[id] = sample
	}
	if len(input.Values) != len(leaves) {
		writeError(w, 400, "unexpected_trial_input")
		return
	}
	results := calculation.EvaluateBatch(formulas, snapshot, "trial-"+data.NewID(), nil)
	writeJSON(w, map[string]any{"simulated": true, "draft_revision": d.Revision, "results": results})
}
