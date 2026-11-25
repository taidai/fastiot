package appapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"fastiot/internal/calculation"
	"fastiot/internal/decision"
	"fastiot/internal/ems/historyquery"
	"fastiot/internal/virtualinput"
	"github.com/nats-io/nats.go"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

func TestDerivedAggregateStorageTimeDoesNotRenewObservation(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, token)
	group := createNodeAPI(t, base, token, "group", root, "stored observation", 0)
	member := createNodeAPI(t, base, token, "variable", group, "software", 7)
	status, body := call(t, "PUT", base+"/aggregate", token, AggregateConfig{Group: group, Op: "sum", Members: []string{member}}, nil)
	if status != 200 {
		t.Fatal(status, body)
	}
	stop := startAggregateLoop(t, h)
	var result AggregateResult
	waitFor(t, func() bool {
		call(t, "GET", base+"/aggregate?group="+group, token, nil, &result)
		return result.Value != nil
	}, "sample before restoring stored evidence")
	stop()
	old := time.Now().Add(-time.Minute)
	result.Time = &old
	result.EvaluatedAt = time.Now()
	encoded, _ := json.Marshal(result)
	// A restored/replayed latest-value write has a new LWW clock, not a new observation.
	if err := client.SendNodePoint(h.nc, group, data.NewPointString(aggregateResultPoint, "0", string(encoded)), true); err != nil {
		t.Fatal(err)
	}
	for _, node := range snapshotNodes(t, base, token) {
		if node.ID == group {
			text, _ := node.Points.Text("easyemsInput", "")
			var input decision.InputObservation
			_ = json.Unmarshal([]byte(text), &input)
			if input.Quality == "good" || input.Value != nil || input.Time == nil || !input.Time.Equal(old) {
				t.Fatalf("storage clock renewed observation: %s", text)
			}
			return
		}
	}
	t.Fatal("aggregate missing")
}

func TestDerivedSimulationRestartDoesNotRenewObservation(t *testing.T) {
	dir, password := t.TempDir(), data.NewID()
	base, token, h, stop := startInputServer(t, dir, password)
	root := snapshotRoot(t, base, token)
	min, max := 0.0, 100.0
	var input inputResponse
	status, body := call(t, "POST", base+"/virtual-inputs", token, map[string]any{"parent": root, "name": "simulated input", "source": "simulation", "definition": virtualinput.Definition{Kind: "number", Unit: "kW", Purpose: "test only", Min: &min, Max: &max, Validity: "timed", TTLSeconds: 60, RuntimeEditable: true}, "operation_id": "create"}, &input)
	if status != 200 {
		t.Fatal(status, body)
	}
	status, body = call(t, "POST", base+"/virtual-inputs/"+input.ID+"/apply", token, map[string]any{"revision": input.Revision, "operation_id": "apply"}, &input)
	if status != 200 {
		t.Fatal(status, body)
	}
	status, body = call(t, "POST", base+"/virtual-inputs/"+input.ID+"/value", token, map[string]any{"revision": input.Revision, "operation_id": "set", "value": 5}, &input)
	if status != 200 {
		t.Fatal(status, body)
	}
	stopLoop := startCalculationLoop(t, h)
	var before decision.InputObservation
	for _, node := range snapshotNodes(t, base, token) {
		if node.ID == input.ID {
			text, _ := node.Points.Text("easyemsInput", "")
			_ = json.Unmarshal([]byte(text), &before)
		}
	}
	if before.Value == nil || *before.Value != 5 || !before.Simulated || !before.Software || before.Time == nil {
		t.Fatalf("simulation provenance: %+v", before)
	}
	var history struct {
		Results []historyquery.PointResult `json:"results"`
	}
	waitFor(t, func() bool {
		call(t, "GET", base+"/history?node_id="+input.ID+"&type=value", token, nil, &history)
		return len(history.Results) == 1 && len(history.Results[0].Records) > 0
	}, "recorded simulated observation")
	stopLoop()
	stop()
	base, token, h, _ = startInputServer(t, dir, password)
	startCalculationLoop(t, h)
	for _, node := range snapshotNodes(t, base, token) {
		if node.ID == input.ID {
			text, _ := node.Points.Text("easyemsInput", "")
			var after decision.InputObservation
			_ = json.Unmarshal([]byte(text), &after)
			if after.Quality == "good" || after.Value != nil || !after.Simulated {
				t.Fatalf("restart renewed simulation: %s", text)
			}
		}
	}
	status, body = call(t, "GET", base+"/history?node_id="+input.ID+"&type=value", token, nil, &history)
	found := false
	if status == 200 && len(history.Results) == 1 {
		for _, record := range history.Results[0].Records {
			if record.Value != nil && *record.Value == 5 {
				found = true
				if !record.SourceTime.Equal(*before.Time) {
					t.Fatalf("history changed observation time: %+v", record)
				}
			}
		}
	}
	if !found {
		t.Fatalf("restart lost captured simulation history: %d %s", status, body)
	}
}

func TestDerivedCalculatedMachineAndScreen(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, token)
	own := createNodeAPI(t, base, token, "group", root, "visible calculations", 0)
	post := func(path string, payload, result any) {
		t.Helper()
		status, body := call(t, "POST", base+path, token, payload, result)
		if status != 200 {
			t.Fatalf("%s: %d %s", path, status, body)
		}
	}
	var input inputResponse
	post("/virtual-inputs", map[string]any{"parent": root, "name": "capacity", "source": "configuration", "definition": virtualCapacity("120"), "operation_id": "create"}, &input)
	post("/virtual-inputs/"+input.ID+"/apply", map[string]any{"revision": input.Revision, "operation_id": "apply"}, &input)
	var calculated calcResponse
	post("/calculations", map[string]any{"parent": own, "name": "double capacity", "definition": calculation.Definition{Operator: "add", Inputs: []string{input.ID, input.ID}, Kind: "number", Unit: "kWh"}, "operation_id": "create-calc"}, &calculated)
	post("/calculations/"+calculated.ID+"/apply", map[string]any{"revision": calculated.Revision, "operation_id": "apply-calc"}, &calculated)
	startCalculationLoop(t, h)
	waitFor(t, func() bool {
		var value calcResponse
		call(t, "GET", base+"/calculations/"+calculated.ID, token, nil, &value)
		return value.State.Quality == "good"
	}, "actual calculation runtime")
	definition := decision.StateMachineDefinition{InitialState: "idle", States: []string{"idle", "ready"}, Transitions: []decision.StateTransition{{ID: "ready", From: "idle", To: "ready", Event: "check", Conditions: []decision.Condition{{PointID: "capacity", Operator: "eq", Value: 240}}}}, Bindings: decision.BindingSet{Points: map[string]decision.Target{"capacity": {NodeID: calculated.ID, Use: "decision"}}}}
	var machine stateMachineResponse
	post("/state-machines", map[string]any{"parent": own, "name": "declared calculated guard", "definition": definition}, &machine)
	path := "/state-machines/" + machine.ID
	post(path+"/publish", map[string]any{"revision": machine.Revision}, &machine)
	post(path+"/execute", map[string]any{"revision": machine.Revision, "request_id": "calculated-event", "event": "check", "confirm": true}, &machine)
	if machine.CurrentState != "ready" {
		t.Fatalf("calculated guard did not run: %+v", machine)
	}
	limited := inputAccount(t, &pointConfigFixture{base: base, token: token}, own, "implementer", nil)
	var restricted stateMachineResponse
	status, body := call(t, "GET", base+path, limited, nil, &restricted)
	if status != 200 || restricted.LastExecution != nil || restricted.Applied != nil {
		t.Fatalf("indirect source exposed through machine: %d %s", status, body)
	}
	var history struct {
		Results []historyquery.PointResult `json:"results"`
	}
	status, body = call(t, "GET", base+"/history?node_id="+calculated.ID+"&type=value", token, nil, &history)
	if status != 200 || len(history.Results) != 1 || len(history.Results[0].Records) == 0 {
		t.Fatalf("calculated history unavailable: %d %s", status, body)
	}
	for _, record := range history.Results[0].Records {
		if record.Value != nil && *record.Value == 240 && record.Source != "derived_runtime" {
			t.Fatalf("calculated history masqueraded as physical: %+v", record)
		}
	}
	history.Results = nil // Do not let omitted JSON fields reuse raw-record evidence.
	status, body = call(t, "GET", base+"/history?node_id="+calculated.ID+"&type=value&aggregate=5s", token, nil, &history)
	if status != 200 || len(history.Results) != 1 {
		t.Fatalf("display aggregation: %d %s", status, body)
	}
	foundNumeric := false
	for _, record := range history.Results[0].Records {
		if record.Value != nil {
			foundNumeric = true
			if !strings.Contains(record.Text, `"software":true`) {
				t.Fatalf("display aggregation discarded captured sources: %+v", record)
			}
		}
	}
	if !foundNumeric {
		t.Fatalf("display aggregation has no numeric evidence: %s", body)
	}
	status, body = call(t, "GET", base+"/history?node_id="+calculated.ID+"&type=value&aggregate=5s", limited, nil, nil)
	if status != 403 {
		t.Fatalf("historical indirect source leaked: %d %s", status, body)
	}
	var snapshot struct {
		Nodes []data.NodeEdge `json:"nodes"`
	}
	call(t, "GET", base+"/snapshot", token, nil, &snapshot)
	for _, node := range snapshot.Nodes {
		if node.ID == calculated.ID {
			text, _ := node.Points.Text("easyemsInput", "")
			var evidence decision.InputObservation
			if json.Unmarshal([]byte(text), &evidence) != nil || evidence.Value == nil || *evidence.Value != 240 || evidence.Source != "calculation" || evidence.DefinitionVersion != calculated.Version || !evidence.Software || evidence.Time == nil {
				t.Fatalf("calculation screen evidence: %s", text)
			}
			return
		}
	}
	t.Fatal("calculation missing from snapshot")
}

func TestDerivedVirtualInputRuntimeEvidence(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, token)
	var input inputResponse
	status, body := call(t, http.MethodPost, base+"/virtual-inputs", token, map[string]any{"parent": root, "name": "configuration input", "source": "configuration", "definition": virtualCapacity("120"), "operation_id": "create"}, &input)
	if status != 200 {
		t.Fatal(status, body)
	}
	status, body = call(t, http.MethodPost, base+"/virtual-inputs/"+input.ID+"/apply", token, map[string]any{"revision": input.Revision, "operation_id": "apply"}, &input)
	if status != 200 {
		t.Fatal(status, body)
	}
	screen := automaticScreen(root, 0)
	screen["blocks"].([]any)[1].(map[string]any)["binding"].(map[string]any)["metrics"].([]any)[0] = map[string]any{"point_id": input.ID}
	status, body = call(t, "PUT", base+"/ems/screen/"+root, token, screen, nil)
	if status != 200 {
		t.Fatalf("virtual screen binding: %d %s", status, body)
	}
	doc := decision.Document{Scope: "self", Table: decision.Table{Rows: []decision.Row{{ID: "configured", Conditions: []decision.Condition{{PointID: "input", Operator: "eq", Value: 120}}}}}, Bindings: decision.BindingSet{Points: map[string]decision.Target{"input": {NodeID: input.ID, Use: "decision"}}}}
	var created struct {
		ID       string `json:"id"`
		Revision string `json:"revision"`
	}
	status, body = call(t, http.MethodPost, base+"/rules", token, map[string]any{"parent": root, "description": "Declared software input", "document": doc}, &created)
	if status != 200 {
		t.Fatalf("virtual decision binding: %d %s", status, body)
	}
	doc.Revision = created.Revision
	status, body = call(t, http.MethodPost, base+"/rules/"+created.ID+"/publish", token, doc, nil)
	if status != 200 {
		t.Fatal(status, body)
	}
	manager := client.NewManager(h.nc, func(nc *nats.Conn, config decision.DecisionTable) client.Client { return h.NewRuleClient(nc, config) }, RuleParentTypes())
	done := make(chan error, 1)
	go func() { done <- manager.Run() }()
	defer func() { manager.Stop(nil); <-done }()
	status, body = call(t, http.MethodPost, base+"/rules/"+created.ID+"/enable", token, map[string]bool{"enabled": true}, nil)
	if status != 200 {
		t.Fatal(status, body)
	}
	var report struct {
		Row    string `json:"row"`
		Inputs []struct {
			Quality     string                    `json:"quality"`
			Value       float64                   `json:"value"`
			Observation decision.InputObservation `json:"observation"`
		} `json:"inputs"`
	}
	waitFor(t, func() bool {
		var snapshot struct {
			Nodes []data.NodeEdge `json:"nodes"`
		}
		call(t, http.MethodGet, base+"/snapshot", token, nil, &snapshot)
		for _, node := range snapshot.Nodes {
			if node.ID == created.ID {
				text, _ := node.Points.Text("lastCheck", "")
				_ = json.Unmarshal([]byte(text), &report)
			}
		}
		return report.Row == "configured"
	}, "virtual input real rule runtime")
	if len(report.Inputs) != 1 || report.Inputs[0].Quality != "good" || report.Inputs[0].Value != 120 || report.Inputs[0].Observation.Source != "configuration" || !report.Inputs[0].Observation.Software || report.Inputs[0].Observation.TimeSource != "input_change" {
		t.Fatalf("runtime lost source evidence: %+v", report)
	}
	min, max := 0.0, 100.0
	var missing inputResponse
	status, body = call(t, "POST", base+"/virtual-inputs", token, map[string]any{"parent": root, "name": "missing simulation", "source": "simulation", "definition": virtualinput.Definition{Kind: "number", Unit: "kW", Purpose: "not measured", Min: &min, Max: &max, Validity: "timed", TTLSeconds: 60}, "operation_id": "missing"}, &missing)
	if status != 200 {
		t.Fatal(status, body)
	}
	status, body = call(t, "POST", base+"/virtual-inputs/"+missing.ID+"/apply", token, map[string]any{"revision": missing.Revision, "operation_id": "apply-missing"}, &missing)
	if status != 200 {
		t.Fatal(status, body)
	}
	doc.Revision = ""
	doc.Bindings.Points["input"] = decision.Target{NodeID: missing.ID, Use: "decision"}
	doc.Table.Rows = []decision.Row{{ID: "zero", Conditions: []decision.Condition{{PointID: "input", Operator: "eq", Value: 0}}}, {ID: "fallback"}}
	status, body = call(t, "POST", base+"/rules", token, map[string]any{"parent": root, "description": "missing is not zero", "document": doc}, &created)
	if status != 200 {
		t.Fatal(status, body)
	}
	doc.Revision = created.Revision
	status, body = call(t, "POST", base+"/rules/"+created.ID+"/publish", token, doc, nil)
	if status != 200 {
		t.Fatal(status, body)
	}
	status, body = call(t, "POST", base+"/rules/"+created.ID+"/enable", token, map[string]bool{"enabled": true}, nil)
	if status != 200 {
		t.Fatal(status, body)
	}
	var absent struct {
		Row    string `json:"row"`
		Inputs []struct {
			Value   *float64 `json:"value"`
			Quality string   `json:"quality"`
		} `json:"inputs"`
	}
	waitFor(t, func() bool {
		for _, node := range snapshotNodes(t, base, token) {
			if node.ID == created.ID {
				text, _ := node.Points.Text("lastCheck", "")
				_ = json.Unmarshal([]byte(text), &absent)
			}
		}
		return absent.Row == "fallback"
	}, "missing derived input chooses fallback")
	if len(absent.Inputs) != 1 || absent.Inputs[0].Quality == "good" || absent.Inputs[0].Value != nil {
		t.Fatalf("missing derived observation became zero: %+v", absent)
	}
}

func TestDerivedAggregateSavedScreenObservation(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, token)
	area := createNodeAPI(t, base, token, "group", root, "area", 0)
	child := createNodeAPI(t, base, token, "group", area, "child", 0)
	member := createNodeAPI(t, base, token, "variable", area, "software observation", 7)
	status, body := call(t, http.MethodPut, base+"/aggregate", token, AggregateConfig{Group: area, Op: "sum", Members: []string{member}}, nil)
	if status != 200 {
		t.Fatal(status, body)
	}
	startAggregateLoop(t, h)
	var result AggregateResult
	waitFor(t, func() bool {
		call(t, http.MethodGet, base+"/aggregate?group="+area, token, nil, &result)
		return result.Value != nil
	}, "runtime aggregate")
	document := automaticScreen(area, 0)
	document["blocks"].([]any)[1].(map[string]any)["binding"].(map[string]any)["metrics"].([]any)[0] = map[string]any{"point_id": area}
	status, body = call(t, http.MethodPut, base+"/ems/screen/"+area, token, document, nil)
	if status != 200 {
		t.Fatalf("bind aggregate to screen: %d %s", status, body)
	}
	var inherited map[string]any
	status, body = call(t, http.MethodGet, base+"/ems/screen/"+child, token, nil, &inherited)
	if status != 200 || inherited["inherited_from"] != area {
		t.Fatalf("inherit: %d %s", status, body)
	}
	var snapshot struct {
		Nodes []data.NodeEdge `json:"nodes"`
	}
	status, body = call(t, http.MethodGet, base+"/snapshot", token, nil, &snapshot)
	if status != 200 {
		t.Fatal(status, body)
	}
	for _, node := range snapshot.Nodes {
		if node.ID != area {
			continue
		}
		text, _ := node.Points.Text("easyemsInput", "")
		var input struct {
			Value       *float64 `json:"value"`
			Source      string   `json:"source"`
			Quality     string   `json:"quality"`
			Time        string   `json:"time"`
			EvaluatedAt string   `json:"evaluated_at"`
			Software    bool     `json:"software"`
		}
		if json.Unmarshal([]byte(text), &input) != nil || input.Value == nil || *input.Value != 7 || input.Source != "aggregate" || input.Quality != "good" || input.Time == "" || input.EvaluatedAt == "" || !input.Software {
			t.Fatalf("missing honest derived metadata: %s", text)
		}
		return
	}
	t.Fatal("aggregate node missing")
}

// A window is a decision input, but never a protection/interlock or
// readback. The declaration, not the human-readable rule name, determines use.
func TestDerivedWindowBindingDeclaredUse(t *testing.T) {
	base, token, _, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, token)
	group := createNodeAPI(t, base, token, "group", root, "window", 0)
	member := createNodeAPI(t, base, token, "variable", group, "software sample", 7)
	status, body := call(t, http.MethodPut, base+"/aggregate", token, AggregateConfig{Group: group, Op: "sum", Members: []string{member}, Window: "5m"}, nil)
	if status != 200 {
		t.Fatalf("definition: %d %s", status, body)
	}
	for _, use := range []string{"", "decision"} {
		condition := map[string]any{"point_id": group, "operator": "gt", "value": 0}
		if use != "" {
			condition["use"] = use
		}
		status, body := call(t, "PUT", base+"/safety", token, map[string]any{"node": member, "safety": map[string]any{"conditions": []any{condition}}}, nil)
		if status == 200 {
			t.Fatalf("actual safety consumer accepted window with use=%q: %s", use, body)
		}
	}
	for _, use := range []string{"", "decision", "protection", "interlock", "readback", "unknown"} {
		t.Run(use, func(t *testing.T) {
			document := map[string]any{
				"scope":    "descendants",
				"table":    map[string]any{"rows": []any{map[string]any{"row_id": "first", "conditions": []any{map[string]any{"point_id": "window", "operator": "gt", "value": 0}}, "actions": []any{}}}},
				"bindings": map[string]any{"points": map[string]any{"window": map[string]any{"node_id": group, "point_type": "easyemsAggregateValue", "use": use}}, "actions": map[string]any{}},
			}
			if use == "" {
				delete(document["bindings"].(map[string]any)["points"].(map[string]any)["window"].(map[string]any), "use")
			}
			status, body := call(t, http.MethodPost, base+"/rules", token, map[string]any{"parent": root, "description": "ordinary name", "document": document}, nil)
			if use == "decision" && status != 200 {
				t.Fatalf("permitted window decision binding: %d %s", status, body)
			}
			if use != "decision" && status == 200 {
				t.Fatalf("forbidden %s window binding saved: %s", use, body)
			}
		})
	}
}
