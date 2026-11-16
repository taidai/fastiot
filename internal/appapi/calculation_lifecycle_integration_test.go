package appapi

import (
	"net/http"
	"strings"
	"testing"

	"fastiot/internal/calculation"
	"github.com/simpleiot/simpleiot/data"
)

type calculationTestSetup struct {
	f                            *pointConfigFixture
	h                            *Handler
	A, B, Capacity, Total, Ratio string
	stopLoop                     func()
}

func configCalculationSetup(t *testing.T) calculationTestSetup {
	t.Helper()
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	stopLoop := startCalculationLoop(t, h)
	f := &pointConfigFixture{base: base, token: token}
	root := snapshotRoot(t, base, token)
	source := func(name, value string) string {
		t.Helper()
		definition := virtualCapacity(value)
		definition.Unit = "kW"
		var out inputResponse
		status, body := call(t, http.MethodPost, base+"/virtual-inputs", token, map[string]any{"parent": root, "name": name, "source": "configuration", "definition": definition, "operation_id": "create-" + name}, &out)
		if status != 200 {
			t.Fatalf("create %s: %d %s", name, status, body)
		}
		status, body = call(t, http.MethodPost, base+"/virtual-inputs/"+out.ID+"/apply", token, map[string]any{"revision": out.Revision, "operation_id": "apply-" + name}, &out)
		if status != 200 {
			t.Fatalf("apply %s: %d %s", name, status, body)
		}
		return out.ID
	}
	a, b, capacity := source("A", "20"), source("B", "30"), source("capacity", "100")
	create := func(name string, d calculation.Definition) string {
		t.Helper()
		var out calcResponse
		status, body := call(t, http.MethodPost, base+"/calculations", token, map[string]any{"parent": root, "name": name, "definition": d, "operation_id": "create-" + name}, &out)
		if status != 200 {
			t.Fatalf("create: %d %s", status, body)
		}
		status, body = call(t, http.MethodPost, base+"/calculations/"+out.ID+"/apply", token, map[string]any{"revision": out.Revision, "operation_id": "apply-" + name}, &out)
		if status != 200 {
			t.Fatalf("apply: %d %s", status, body)
		}
		return out.ID
	}
	total := create("total", calculation.Definition{Operator: "add", Inputs: []string{a, b}, Kind: "number", Unit: "kW"})
	ratio := create("ratio", calculation.Definition{Operator: "divide", Inputs: []string{total, capacity}, Kind: "number", Unit: "1"})
	waitFor(t, func() bool {
		r := h.CalculationRuntime().Snapshot().Results[ratio]
		return r.Value != nil && *r.Value == 0.5
	}, "configuration graph")
	return calculationTestSetup{f, h, a, b, capacity, total, ratio, stopLoop}
}
func TestCalculationApplicationChecksOnlyAffectedGraph(t *testing.T) {
	s := configCalculationSetup(t)
	var input inputResponse
	call(t, http.MethodGet, s.f.base+"/virtual-inputs/"+s.A, s.f.token, nil, &input)
	changed := virtualCapacity("20")
	status, body := call(t, http.MethodPost, s.f.base+"/virtual-inputs/"+s.A+"/draft", s.f.token, map[string]any{"revision": input.Revision, "operation_id": "unit-draft", "definition": changed}, &input)
	if status != 200 {
		t.Fatal(body)
	}
	payload := map[string]any{"revision": input.Revision, "operation_id": "unit-apply", "action": "apply"}
	var impact inputImpact
	status, body = call(t, http.MethodPost, s.f.base+"/virtual-inputs/"+s.A+"/impact", s.f.token, payload, &impact)
	if status != 200 {
		t.Fatal(body)
	}
	delete(payload, "action")
	payload["confirmation"] = impact.Confirmation
	status, body = call(t, http.MethodPost, s.f.base+"/virtual-inputs/"+s.A+"/apply", s.f.token, payload, nil)
	if status != 200 {
		t.Fatal(body)
	}
	var ratio calcResponse
	call(t, http.MethodGet, s.f.base+"/calculations/"+s.Ratio, s.f.token, nil, &ratio)
	definition := calculation.Definition{Operator: "divide", Inputs: []string{s.A, s.A}, Kind: "number", Unit: "1"}
	status, body = call(t, http.MethodPost, s.f.base+"/calculations/"+s.Ratio+"/draft", s.f.token, map[string]any{"revision": ratio.Revision, "operation_id": "detach-draft", "definition": definition}, &ratio)
	if status != 200 {
		t.Fatal(body)
	}
	status, body = call(t, http.MethodPost, s.f.base+"/calculations/"+s.Ratio+"/apply", s.f.token, map[string]any{"revision": ratio.Revision, "operation_id": "detach-apply"}, &ratio)
	if status != 200 || ratio.Applied.Inputs[0] != s.A {
		t.Fatalf("unrelated invalid formula blocked a valid repair: %d %s", status, body)
	}
}

func TestCalculationRetainedEvidenceRespectsCurrentScope(t *testing.T) {
	s := configCalculationSetup(t)
	root := snapshotRoot(t, s.f.base, s.f.token)
	public := createNodeAPI(t, s.f.base, s.f.token, "group", root, "public", 0)
	status, body := call(t, http.MethodPost, s.f.base+"/nodes/"+s.Ratio+"/move", s.f.token, map[string]any{"old_parent": root, "new_parent": public}, nil)
	if status != 200 {
		t.Fatal(body)
	}
	source := func(name, value string) string {
		t.Helper()
		d := virtualCapacity(value)
		d.Unit = "kW"
		var out inputResponse
		status, body := call(t, http.MethodPost, s.f.base+"/virtual-inputs", s.f.token, map[string]any{"parent": public, "name": name, "source": "configuration", "definition": d, "operation_id": name}, &out)
		if status != 200 {
			t.Fatal(body)
		}
		status, body = call(t, http.MethodPost, s.f.base+"/virtual-inputs/"+out.ID+"/apply", s.f.token, map[string]any{"revision": out.Revision, "operation_id": "apply-" + name}, &out)
		if status != 200 {
			t.Fatal(body)
		}
		return out.ID
	}
	left, right := source("local-left", "100"), source("local-zero", "0")
	var ratio calcResponse
	call(t, http.MethodGet, s.f.base+"/calculations/"+s.Ratio, s.f.token, nil, &ratio)
	d := calculation.Definition{Operator: "divide", Inputs: []string{left, right}, Kind: "number", Unit: "1"}
	status, body = call(t, http.MethodPost, s.f.base+"/calculations/"+s.Ratio+"/draft", s.f.token, map[string]any{"revision": ratio.Revision, "operation_id": "public-draft", "definition": d}, &ratio)
	if status != 200 {
		t.Fatal(body)
	}
	status, body = call(t, http.MethodPost, s.f.base+"/calculations/"+s.Ratio+"/apply", s.f.token, map[string]any{"revision": ratio.Revision, "operation_id": "public-apply"}, &ratio)
	if status != 200 {
		t.Fatal(body)
	}
	waitFor(t, func() bool {
		r := s.h.CalculationRuntime().Snapshot().Results[s.Ratio]
		return r.Version == 2 && r.Value == nil && r.Retained != nil
	}, "retained former private result")
	limited := inputAccount(t, s.f, public, "viewer", nil)
	status, body = call(t, http.MethodGet, s.f.base+"/calculations/"+s.Ratio, limited, nil, nil)
	if status != 200 || strings.Contains(body, s.Total) || strings.Contains(body, s.A) || strings.Contains(body, s.Capacity) {
		t.Fatalf("retained private inputs leaked: %d %s", status, body)
	}
}

func TestCalculationDependenciesBlockGenericDestruction(t *testing.T) {
	for _, operation := range []string{"delete", "move"} {
		t.Run(operation, func(t *testing.T) {
			s := configCalculationSetup(t)
			root := snapshotRoot(t, s.f.base, s.f.token)
			method, path, payload := http.MethodDelete, "/nodes/"+s.A, map[string]any{"parent": root}
			if operation == "move" {
				destination := createNodeAPI(t, s.f.base, s.f.token, "group", root, "destination", 0)
				method, path, payload = http.MethodPost, "/nodes/"+s.A+"/move", map[string]any{"old_parent": root, "new_parent": destination}
			}
			status, body := call(t, method, s.f.base+path, s.f.token, payload, nil)
			if status != 409 {
				t.Fatalf("%s destroyed a calculation dependency: %d %s", operation, status, body)
			}
		})
	}
}

func TestCalculationImpactRejectsStaleConfirmation(t *testing.T) {
	s := configCalculationSetup(t)
	var total, ratio calcResponse
	call(t, http.MethodGet, s.f.base+"/calculations/"+s.Total, s.f.token, nil, &total)
	changed := calculation.Definition{Operator: "add", Inputs: []string{s.A, s.Capacity}, Kind: "number", Unit: "kW"}
	status, body := call(t, http.MethodPost, s.f.base+"/calculations/"+s.Total+"/draft", s.f.token, map[string]any{"revision": total.Revision, "operation_id": "draft-change", "definition": changed}, &total)
	if status != 200 {
		t.Fatal(body)
	}
	payload := map[string]any{"revision": total.Revision, "operation_id": "apply-change"}
	var impact inputImpact
	status, body = call(t, http.MethodPost, s.f.base+"/calculations/"+s.Total+"/impact", s.f.token, payload, &impact)
	if status != 200 || !impact.Required {
		t.Fatalf("missing calculation impact: %d %s", status, body)
	}
	call(t, http.MethodGet, s.f.base+"/calculations/"+s.Ratio, s.f.token, nil, &ratio)
	different := calculation.Definition{Operator: "divide", Inputs: []string{s.Total, s.A}, Kind: "number", Unit: "1"}
	status, body = call(t, http.MethodPost, s.f.base+"/calculations/"+s.Ratio+"/draft", s.f.token, map[string]any{"revision": ratio.Revision, "operation_id": "other-change", "definition": different}, nil)
	if status != 200 {
		t.Fatal(body)
	}
	payload["confirmation"] = impact.Confirmation
	status, _ = call(t, http.MethodPost, s.f.base+"/calculations/"+s.Total+"/apply", s.f.token, payload, nil)
	if status != 409 {
		t.Fatalf("stale dependency confirmation accepted: %d", status)
	}
}

func TestVirtualInputImpactIncludesCalculationDependencies(t *testing.T) {
	s := configCalculationSetup(t)
	var current inputResponse
	status, body := call(t, http.MethodGet, s.f.base+"/virtual-inputs/"+s.Capacity, s.f.token, nil, &current)
	if status != 200 {
		t.Fatal(body)
	}
	changed := virtualCapacity("80")
	changed.Unit = "kW"
	status, body = call(t, http.MethodPost, s.f.base+"/virtual-inputs/"+s.Capacity+"/draft", s.f.token, map[string]any{"revision": current.Revision, "operation_id": "capacity-draft", "definition": changed}, &current)
	if status != 200 {
		t.Fatal(body)
	}
	payload := map[string]any{"revision": current.Revision, "operation_id": "capacity-change", "action": "apply"}
	var impact inputImpact
	status, body = call(t, http.MethodPost, s.f.base+"/virtual-inputs/"+s.Capacity+"/impact", s.f.token, payload, &impact)
	found := false
	for _, ref := range impact.References {
		if ref.ID == s.Ratio {
			found = true
		}
	}
	if status != 200 || !impact.Required || !found {
		t.Fatalf("calculation dependency missing from input impact: %d %s", status, body)
	}
	delete(payload, "action")
	status, _ = call(t, http.MethodPost, s.f.base+"/virtual-inputs/"+s.Capacity+"/apply", s.f.token, payload, nil)
	if status != 409 {
		t.Fatalf("known dependency changed without confirmation: %d", status)
	}
	s.stopLoop()
	payload["confirmation"] = impact.Confirmation
	status, body = call(t, http.MethodPost, s.f.base+"/virtual-inputs/"+s.Capacity+"/apply", s.f.token, payload, nil)
	if status != 200 {
		t.Fatal(body)
	}
	var ratio calcResponse
	status, body = call(t, http.MethodGet, s.f.base+"/calculations/"+s.Ratio, s.f.token, nil, &ratio)
	if status != 200 || ratio.State.Value != nil || ratio.State.Quality == "good" {
		t.Fatalf("input change left derived current value usable: %d %s", status, body)
	}
}

func TestCalculationApplyInvalidatesDownstreamBeforeRecalculation(t *testing.T) {
	s := configCalculationSetup(t)
	s.stopLoop()
	var total calcResponse
	status, body := call(t, http.MethodGet, s.f.base+"/calculations/"+s.Total, s.f.token, nil, &total)
	if status != 200 {
		t.Fatal(body)
	}
	definition := calculation.Definition{Operator: "add", Inputs: []string{s.A, s.Capacity}, Kind: "number", Unit: "kW"}
	status, body = call(t, http.MethodPost, s.f.base+"/calculations/"+s.Total+"/draft", s.f.token, map[string]any{"revision": total.Revision, "operation_id": "change-total", "definition": definition}, &total)
	if status != 200 {
		t.Fatal(body)
	}
	payload := map[string]any{"revision": total.Revision, "operation_id": "apply-change"}
	var impact inputImpact
	status, body = call(t, http.MethodPost, s.f.base+"/calculations/"+s.Total+"/impact", s.f.token, payload, &impact)
	if status != 200 {
		t.Fatal(body)
	}
	payload["confirmation"] = impact.Confirmation
	status, body = call(t, http.MethodPost, s.f.base+"/calculations/"+s.Total+"/apply", s.f.token, payload, &total)
	if status != 200 {
		t.Fatal(body)
	}
	var ratio calcResponse
	status, body = call(t, http.MethodGet, s.f.base+"/calculations/"+s.Ratio, s.f.token, nil, &ratio)
	if status != 200 || ratio.State.Value != nil || ratio.State.Quality == "good" {
		t.Fatalf("dependent retained an obsolete normal value: %d %s", status, body)
	}
	startCalculationLoop(t, s.h)
	waitFor(t, func() bool {
		r := s.h.CalculationRuntime().Snapshot().Results[s.Ratio]
		return r.Value != nil && *r.Value == 1.2
	}, "recalculated descendant")
}

func TestCalculationTrialUsesOnlyFrozenInputs(t *testing.T) {
	s := configCalculationSetup(t)
	var object calcResponse
	status, body := call(t, http.MethodGet, s.f.base+"/calculations/"+s.Ratio, s.f.token, nil, &object)
	if status != 200 {
		t.Fatal(body)
	}
	before := s.h.CalculationRuntime().Snapshot()
	values := map[string]float64{s.A: 40, s.B: 30, s.Capacity: 100}
	var trial struct {
		Simulated bool `json:"simulated"`
		Results   map[string]struct {
			Value     *float64 `json:"value"`
			Reason    string   `json:"reason"`
			Simulated bool     `json:"simulated"`
		} `json:"results"`
	}
	status, body = call(t, http.MethodPost, s.f.base+"/calculations/"+s.Ratio+"/trial", s.f.token, map[string]any{"revision": object.Revision, "values": values}, &trial)
	if status != 200 {
		t.Fatalf("trial: %d %s", status, body)
	}
	if !trial.Simulated || !trial.Results[s.Ratio].Simulated || trial.Results[s.Ratio].Value == nil || *trial.Results[s.Ratio].Value != 0.7 || *trial.Results[s.Total].Value != 70 {
		t.Fatalf("trial omitted intermediate/simulation evidence: %s", body)
	}
	after := s.h.CalculationRuntime().Snapshot()
	if after.ID != before.ID || *after.Results[s.Ratio].Value != 0.5 {
		t.Fatal("trial changed live result")
	}
	delete(values, s.B)
	status, _ = call(t, http.MethodPost, s.f.base+"/calculations/"+s.Ratio+"/trial", s.f.token, map[string]any{"revision": object.Revision, "values": values}, nil)
	if status != 400 {
		t.Fatalf("missing fixed input fell back to live data: %d", status)
	}
	values[s.B] = 30
	values[s.Capacity] = 0
	status, body = call(t, http.MethodPost, s.f.base+"/calculations/"+s.Ratio+"/trial", s.f.token, map[string]any{"revision": object.Revision, "values": values}, &trial)
	if status != 200 || trial.Results[s.Ratio].Value != nil || trial.Results[s.Ratio].Reason == "" {
		t.Fatalf("division by zero was not diagnosed: %d %s", status, body)
	}
}
