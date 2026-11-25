package appapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"fastiot/internal/decision"
	"fastiot/internal/ems/command"
	"fastiot/internal/emsapi"
	"github.com/nats-io/nats.go"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

func TestRuleSoftwareCommandsUseCanonicalFeedbackAndDoNotReplay(t *testing.T) {
	f := newPointConfigFixture(t)
	f.handler.SetDataDir(f.dir)
	f.handler.SetEMS(emsapi.New(f.runtime, time.Second))
	WireCommandHistory(f.dir, f.runtime.Commands())
	root := snapshotRoot(t, f.base, f.token)
	target := createNodeAPI(t, f.base, f.token, "variable", root, "Software configuration", 0)
	var writes atomic.Int32
	var acknowledge atomic.Bool
	acknowledge.Store(true)
	stop, err := client.SubscribePoints(f.handler.nc, target, func(points []data.Point) {
		for _, point := range points {
			if point.Type == data.PointTypeValueSet {
				writes.Add(1)
				if !acknowledge.Load() {
					continue
				}
				// Explicit software consumer acknowledgment, not a device observation.
				value, _ := point.ValueFloat()
				_ = client.SendNodePoint(f.handler.nc, target, data.NewPointFloat(data.PointTypeValue, "", value), true)
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	doc := decision.Document{Scope: "self", Table: decision.Table{Rows: []decision.Row{{ID: "software", Actions: []decision.Action{{ActionID: "setting", Value: 4, Readback: &decision.Readback{TimeoutMS: 5000}}}}, {ID: "unreachable", Actions: []decision.Action{{ActionID: "setting", Value: 99}}}}}, Bindings: decision.BindingSet{Actions: map[string]decision.Target{"setting": {NodeID: target}}}}
	var created struct {
		ID       string `json:"id"`
		Revision string `json:"revision"`
	}
	status, body := call(t, http.MethodPost, f.base+"/rules", f.token, map[string]any{"parent": root, "description": "Software canonical rule", "document": doc}, &created)
	if status != 200 {
		t.Fatalf("draft: %d %s", status, body)
	}
	doc.Revision = created.Revision
	var simulated decision.SimulationResult
	status, body = call(t, http.MethodPost, f.base+"/rules/"+created.ID+"/test", f.token, map[string]any{"document": doc}, &simulated)
	if status != 200 || simulated.RowID != "software" || len(simulated.Actions) != 1 || simulated.Actions[0].Value != 4 || writes.Load() != 0 {
		t.Fatalf("first-match simulation: %d %s writes=%d", status, body, writes.Load())
	}
	status, body = call(t, http.MethodPost, f.base+"/rules/"+created.ID+"/publish", f.token, doc, nil)
	if status != 200 {
		t.Fatalf("software publication: %d %s", status, body)
	}
	run := func() func() {
		manager := client.NewManager(f.handler.nc, func(nc *nats.Conn, config decision.DecisionTable) client.Client {
			return f.handler.NewRuleClient(nc, config)
		}, nil)
		done := make(chan error, 1)
		go func() { done <- manager.Run() }()
		return func() { manager.Stop(nil); <-done }
	}
	stopRuntime := run()
	stopped := false
	defer func() {
		if !stopped {
			stopRuntime()
		}
	}()
	status, body = call(t, http.MethodPost, f.base+"/rules/"+created.ID+"/enable", f.token, map[string]bool{"enabled": true}, nil)
	if status != 200 {
		t.Fatalf("software enable: %d %s", status, body)
	}
	waitFor(t, func() bool {
		out := f.runtime.Commands().List()
		return len(out) == 1 && out[0].State == command.Confirmed
	}, "software canonical acknowledgment")
	outcome := f.runtime.Commands().List()[0]
	if url.PathEscape(outcome.RequestID) != outcome.RequestID {
		t.Fatalf("command link identity is not one URL segment: %q", outcome.RequestID)
	}
	if !outcome.External || len(outcome.Observed) != 0 || writes.Load() != 1 {
		t.Fatalf("software claimed physical observation or replayed: %+v writes=%d", outcome, writes.Load())
	}
	nodes, _ := client.GetNodes(f.handler.nc, "all", created.ID, "", false)
	var report struct {
		Actions []struct {
			Confirmation string `json:"confirmation"`
			CommandID    string `json:"command_id"`
		} `json:"actions"`
	}
	waitFor(t, func() bool {
		nodes, _ = client.GetNodes(f.handler.nc, "all", created.ID, "", false)
		text, _ := nodes[0].Points.Text("lastCheck", "")
		_ = json.Unmarshal([]byte(text), &report)
		return len(report.Actions) == 1 && report.Actions[0].Confirmation == "software"
	}, "software-only completion label")
	if report.Actions[0].CommandID != outcome.RequestID {
		t.Fatal("software command link lost")
	}
	stopRuntime()
	stopped = true
	restartedAt := time.Now()
	stopRuntime = run()
	stopped = false
	waitFor(t, func() bool {
		nodes, _ = client.GetNodes(f.handler.nc, "all", created.ID, "", false)
		text, _ := nodes[0].Points.Text("lastCheck", "")
		var state struct {
			State string    `json:"state"`
			At    time.Time `json:"at"`
		}
		_ = json.Unmarshal([]byte(text), &state)
		return state.State == "already-fired" && state.At.After(restartedAt)
	}, "restarted rule retains consumed identity")
	if writes.Load() != 1 {
		t.Fatalf("runtime restart replayed software write: %d", writes.Load())
	}
	reserve := func() decision.RuleCommand {
		id := data.NewID()
		raw, _ := json.Marshal(decision.ExecutionReservation{ID: id, Row: "software", Revision: doc.Revision, At: time.Now()})
		if err := client.SendNodePoint(f.handler.nc, created.ID, data.NewPointString("lastExecution", "", string(raw)), true); err != nil {
			t.Fatal(err)
		}
		return decision.RuleCommand{RuleID: created.ID, Revision: doc.Revision, ExecutionID: id, RequestID: id + "-0", RowID: "software", Target: doc.Bindings.Actions["setting"], Action: doc.Table.Rows[0].Actions[0]}
	}
	acknowledge.Store(false)
	pending := reserve()
	submitted, err := f.handler.submitRuleCommand(pending)
	if err != nil || submitted.State != command.Accepted {
		t.Fatalf("software submit: %+v %v", submitted, err)
	}
	if _, err := f.handler.submitRuleCommand(pending); err != nil {
		t.Fatal(err)
	}
	readback := decision.Target{NodeID: target, PointType: "value"}
	waitFor(t, func() bool {
		out, ok := f.handler.observeRuleSoftware(pending.RequestID, readback, 5000, 0)
		return ok && out.State == command.Unknown
	}, "software unknown without fresh acknowledgment")
	if writes.Load() != 2 {
		t.Fatalf("accepted retry rewrote software value: %d", writes.Load())
	}
	_ = client.SendNodePoint(f.handler.nc, target, data.NewPointFloat("value", "", 4), true)
	late, _ := f.handler.observeRuleSoftware(pending.RequestID, readback, 5000, 0)
	retried, err := f.handler.submitRuleCommand(pending)
	if err != nil || late.State != command.Unknown || retried.State != command.Unknown || writes.Load() != 2 {
		t.Fatalf("late software feedback/retry changed terminal: %+v %+v %v writes=%d", late, retried, err, writes.Load())
	}
	failed := reserve()
	persist := f.runtime.Commands().Persist
	f.runtime.Commands().Persist = func(command.Outcome) error { return errors.New("fixture software journal failure") }
	_, err = f.handler.submitRuleCommand(failed)
	f.runtime.Commands().Persist = persist
	if err == nil || writes.Load() != 2 {
		t.Fatalf("software persistence failure wrote: %v writes=%d", err, writes.Load())
	}
	status, body = call(t, http.MethodPost, f.base+"/nodes/"+target+"/manual", f.token, map[string]bool{"manual": true}, nil)
	if status != 200 {
		t.Fatalf("software takeover: %d %s", status, body)
	}
	refused, err := f.handler.submitRuleCommand(reserve())
	if err != nil || refused.State != command.Rejected || writes.Load() != 2 {
		t.Fatalf("software automatic guard bypassed: %+v %v writes=%d", refused, err, writes.Load())
	}
}
