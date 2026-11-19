package appapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	siotclient "github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/ems/runtime"
	"fastiot/internal/ems/safety"
	"fastiot/internal/emsapi"
)

type storedCommandResult struct {
	RequestID string    `json:"request_id"`
	Status    string    `json:"status"`
	Target    float64   `json:"target"`
	Actor     string    `json:"actor"`
	CreatedAt time.Time `json:"created_at"`
	Reason    string    `json:"reason"`
}

func storedLastCommand(t *testing.T, base, token, id string) storedCommandResult {
	t.Helper()
	for _, node := range snapshotNodes(t, base, token) {
		if node.ID != id {
			continue
		}
		text, _ := node.Points.Text("lastCommand", "")
		if text == "" {
			t.Fatalf("node %s has no lastCommand point", id)
		}
		var result storedCommandResult
		if json.Unmarshal([]byte(text), &result) != nil {
			t.Fatalf("node %s lastCommand is not JSON: %s", id, text)
		}
		return result
	}
	t.Fatalf("node %s missing from snapshot", id)
	return storedCommandResult{}
}

func commandDeliveryItem(t *testing.T, base, token string) gateDeliveryItem {
	t.Helper()
	var check gateDeliveryCheck
	if status, body := call(t, http.MethodGet, base+"/delivery/check", token, nil, &check); status != http.StatusOK {
		t.Fatalf("delivery check = %d %s", status, body)
	}
	return gateItem(t, check, "commands")
}

func commandPreflightGate(t *testing.T, base, token string) preflightGate {
	t.Helper()
	var report preflightReport
	if status, body := call(t, http.MethodGet, base+"/delivery/gates", token, nil, &report); status != http.StatusOK {
		t.Fatalf("delivery gates = %d %s", status, body)
	}
	return gatePreflightGate(t, report, "commands")
}

// A manual point write must leave exactly one command fact: the canonical
// outcome in the command manager. The lastCommand point is its projection,
// carrying the same request id, target, actor, time and reason.
func TestManualCommandResultIsProjectionOfCanonicalOutcome(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	t.Cleanup(service.Close)
	h.SetEMS(emsapi.New(service, time.Minute))
	root := snapshotRoot(t, base, token)
	nodeID := createNodeAPI(t, base, token, "variable", root, "投影点位", 0)

	var response storedCommandResult
	status, body := call(t, http.MethodPost, base+"/nodes/"+nodeID+"/value", token,
		map[string]any{"value": 12.5, "request_id": "projection-1"}, &response)
	if status != http.StatusOK {
		t.Fatalf("manual write = %d %s", status, body)
	}

	outcome, ok := service.Commands().Get("projection-1")
	if !ok {
		t.Fatalf("manual write left no canonical outcome: %#v", service.Commands().List())
	}
	if outcome.DeviceID != nodeID || string(outcome.State) != "accepted" || len(outcome.Writes) != 1 || outcome.Writes[0].Value.Num != 12.5 {
		t.Fatalf("canonical outcome = %+v", outcome)
	}

	stored := storedLastCommand(t, base, token, nodeID)
	if stored.RequestID != outcome.RequestID || stored.Status != string(outcome.State) ||
		stored.Target != outcome.Writes[0].Value.Num || stored.Actor != outcome.Actor ||
		!stored.CreatedAt.Equal(outcome.StartedAt) {
		t.Fatalf("lastCommand projection = %+v, canonical outcome = %+v", stored, outcome)
	}
	if response.RequestID != stored.RequestID || response.Status != stored.Status ||
		response.Target != stored.Target || !response.CreatedAt.Equal(stored.CreatedAt) {
		t.Fatalf("response = %+v, projection = %+v", response, stored)
	}
}

// Delivery and preflight must read command results from the canonical outcomes
// only. A lastCommand point written by anything else is not a command fact and
// must not reach either gate. A real refused write reaches both, keeps its
// reason in the projection, and is counted exactly once.
func TestGatesReadCanonicalCommandOutcomesOnly(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	t.Cleanup(service.Close)
	h.SetEMS(emsapi.New(service, time.Minute))
	root := snapshotRoot(t, base, token)
	ghostID := createNodeAPI(t, base, token, "variable", root, "幽灵命令", 0)
	writeID := createNodeAPI(t, base, token, "variable", root, "单源点位", 0)

	// This point looks like a failed command but no command manager recorded it.
	ghost := `{"request_id":"ghost-1","status":"unknown","target":5,"actor":"someone","created_at":"2026-01-01T00:00:00Z"}`
	if err := siotclient.SendNodePoint(h.nc, ghostID, data.NewPointString("lastCommand", "", ghost), true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		for _, node := range snapshotNodes(t, base, token) {
			if node.ID == ghostID {
				text, _ := node.Points.Text("lastCommand", "")
				return text != ""
			}
		}
		return false
	}, "ghost lastCommand point")

	if item := commandDeliveryItem(t, base, token); item.Status != "pass" {
		t.Fatalf("delivery trusted a projection with no canonical outcome: %#v", item)
	}
	if gate := commandPreflightGate(t, base, token); gate.Status != "pass" {
		t.Fatalf("preflight trusted a projection with no canonical outcome: %#v", gate)
	}

	encoded, _ := json.Marshal(safety.Config{
		Conditions: []safety.ConditionConfig{{PointID: "unobserved-soc", Operator: "ge", Value: 10}},
	})
	if err := siotclient.SendNodePoint(h.nc, writeID, data.NewPointString("easyemsSafety", "", string(encoded)), true); err != nil {
		t.Fatal(err)
	}
	status, body := call(t, http.MethodPost, base+"/nodes/"+writeID+"/value", token,
		map[string]any{"value": 30, "request_id": "refused-1"}, nil)
	if status != http.StatusConflict || !strings.Contains(body, "unobserved-soc") {
		t.Fatalf("refused manual write = %d %s", status, body)
	}

	stored := storedLastCommand(t, base, token, writeID)
	if stored.RequestID != "refused-1" || stored.Status != "failed" || stored.Target != 30 {
		t.Fatalf("refusal projection = %+v", stored)
	}
	if !strings.Contains(stored.Reason, "unobserved-soc") {
		t.Fatalf("refusal lost its reason in the projection: %+v", stored)
	}
	if stored.Actor == "" || stored.CreatedAt.IsZero() {
		t.Fatalf("refusal projection lost actor or time: %+v", stored)
	}

	item := commandDeliveryItem(t, base, token)
	if item.Status != "fail" || !strings.Contains(item.Detail, "单源点位") || strings.Contains(item.Detail, "幽灵命令") {
		t.Fatalf("delivery command item = %#v, want only the canonical failure", item)
	}
	if gate := commandPreflightGate(t, base, token); gate.Status != "fail" || !strings.Contains(gate.Detail, "1 条") {
		t.Fatalf("preflight command gate = %#v, want exactly one canonical failure", gate)
	}
}
