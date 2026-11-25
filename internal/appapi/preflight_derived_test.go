package appapi

import (
	"net/http"
	"testing"
	"time"

	"fastiot/internal/decision"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/emsapi"
	"fastiot/internal/virtualinput"
	"github.com/simpleiot/simpleiot/data"
)

// Combined ticket18/20 boundary: preflight must judge derived inputs by their
// captured quality and observation evidence, not by physical telemetry presence.
func TestPreflightUsesDerivedInputEvidence(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 1000)
	defer service.Close()
	h.SetEMS(emsapi.New(service, time.Minute))
	root := snapshotRoot(t, base, token)
	min, max := 0.0, 100.0
	var input inputResponse
	status, body := call(t, http.MethodPost, base+"/virtual-inputs", token, map[string]any{
		"parent": root, "name": "configured derived input", "source": "simulation", "operation_id": "create",
		"definition": virtualinput.Definition{Kind: "number", Unit: "kW", Purpose: "preflight", Min: &min, Max: &max, Validity: "timed", TTLSeconds: 60, RuntimeEditable: true},
	}, &input)
	if status != 200 {
		t.Fatal(status, body)
	}
	status, body = call(t, http.MethodPost, base+"/virtual-inputs/"+input.ID+"/apply", token, map[string]any{"revision": input.Revision, "operation_id": "apply"}, &input)
	if status != 200 {
		t.Fatal(status, body)
	}
	status, body = call(t, http.MethodPost, base+"/virtual-inputs/"+input.ID+"/value", token, map[string]any{"revision": input.Revision, "operation_id": "set", "value": 5}, &input)
	if status != 200 {
		t.Fatal(status, body)
	}
	pointsStatus := func() (string, string) {
		var report preflightReport
		status, body := call(t, http.MethodGet, base+"/delivery/gates", token, nil, &report)
		if status != 200 {
			t.Fatalf("gates: %d %s", status, body)
		}
		for _, gate := range report.Gates {
			if gate.Key == "points" {
				return gate.Status, gate.Detail
			}
		}
		t.Fatal("points gate missing")
		return "", ""
	}
	if status, detail := pointsStatus(); status != "pass" {
		t.Fatalf("valid derived input judged by missing physical sample: %s %s", status, detail)
	}
	recipient := inboxAccount(t, base, token, root, "preflight-derived@fixture.local", "preflight-derived-only")
	document := decision.Document{Scope: "self", Table: decision.Table{Rows: []decision.Row{{
		ID: "derived", Conditions: []decision.Condition{{PointID: "in", Operator: "gt", Value: 0}},
		Actions: []decision.Action{{ActionID: "notify", Notification: &decision.RuleNotification{RecipientIDs: []string{recipient}, Title: "preflight"}}},
	}}}, Bindings: decision.BindingSet{Points: map[string]decision.Target{"in": {NodeID: input.ID, Use: "decision"}}}}
	var rule struct {
		ID       string `json:"id"`
		Revision string `json:"revision"`
	}
	status, body = call(t, http.MethodPost, base+"/rules", token, map[string]any{"parent": root, "description": "derived critical input", "document": document}, &rule)
	if status != 200 {
		t.Fatal(status, body)
	}
	document.Revision = rule.Revision
	status, body = call(t, http.MethodPost, base+"/rules/"+rule.ID+"/publish", token, document, nil)
	if status != 200 {
		t.Fatal(status, body)
	}
	status, body = call(t, http.MethodPost, base+"/rules/"+rule.ID+"/enable", token, map[string]bool{"enabled": true}, nil)
	if status != 200 {
		t.Fatal(status, body)
	}
	if status, detail := pointsStatus(); status == "fail" {
		t.Fatalf("valid critical derived input blocked preflight: %s %s", status, detail)
	}
}
