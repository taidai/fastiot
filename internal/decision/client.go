package decision

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"sort"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	siotclient "github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/ems/rule"
	"fastiot/internal/ems/safety"
	"fastiot/internal/ems/signal"
)

// DecisionTable is stored as a Simple IoT node. Only Applied is executed;
// Draft is configuration in progress and never controls a device.
type DecisionTable struct {
	ID          string `node:"id"`
	Parent      string `node:"parent"`
	Description string `point:"description"`
	Draft       string `point:"draft"`
	Applied     string `point:"applied"`
	Enabled     bool   `point:"enabled"`
	LastMatch   string `point:"lastMatch"`
	Error       string `point:"error"`
}

type Document struct {
	Scope    string     `json:"scope,omitempty"`
	Revision string     `json:"revision,omitempty"`
	Table    Table      `json:"table"`
	Bindings BindingSet `json:"bindings"`
	// Scenarios are saved simulations; the running engine ignores them.
	Scenarios []Scenario `json:"scenarios,omitempty"`
	// Simulation is transient editor input and is ignored by the running engine.
	Simulation *SimulationOptions `json:"simulation,omitempty"`
}

type BindingSet struct {
	Points  map[string]Target `json:"points"`
	Actions map[string]Target `json:"actions"`
}

// Target connects a stable business ID to a Simple IoT point. Changing a
// device brand changes this binding, not the decision table or its IDs.
type Target struct {
	CommandID string `json:"command_id,omitempty"`
	Parameter string `json:"parameter,omitempty"`
	NodeID    string `json:"node_id"`
	PointType string `json:"point_type,omitempty"`
	Key       string `json:"key,omitempty"`
}

type inputUpdate struct {
	generation uint64
	pointID    string
	target     Target
	points     []data.Point
}

type manualUpdate struct {
	generation uint64
	actionID   string
	points     []data.Point
}

type activeTable struct {
	generation  uint64
	table       Table
	bindings    BindingSet
	values      map[string]signal.Sample
	loaded      map[string]bool
	manual      map[string]bool
	engine      *rule.Engine
	allocations map[string]*allocationRun
	sequence    *actionSequence
	pending     []string
	rampRates   map[string]float64
	rampTargets map[string]float64
	rampValues  map[string]float64
	rampAt      map[string]time.Time
	safety      map[string]safety.Output
	stops       []func()
}

type Client struct {
	nc            *nats.Conn
	config        DecisionTable
	stop          chan struct{}
	newPoints     chan siotclient.NewPoints
	newEdgePoints chan siotclient.NewPoints
	updates       chan inputUpdate
	manualUpdates chan manualUpdate
	generation    uint64
	lastCheck     string
	lastCheckRow  string
	lastAllocSig  string
	observations  Observations
	commands      *RuleCommands
}

func NewDecisionTableClient(nc *nats.Conn, config DecisionTable) siotclient.Client {
	return NewDecisionTableClientWithObservations(nc, config, nil)
}

// NewDecisionTableClientWithObservations adds the runtime's sample snapshot so
// allocator interlock inputs are judged by the quality recorded for them, not by
// a value mirrored onto the node tree. A nil provider keeps the mirror as the
// only source.
func NewDecisionTableClientWithObservations(nc *nats.Conn, config DecisionTable, observations Observations) siotclient.Client {
	return &Client{
		nc:            nc,
		config:        config,
		stop:          make(chan struct{}),
		newPoints:     make(chan siotclient.NewPoints),
		newEdgePoints: make(chan siotclient.NewPoints),
		updates:       make(chan inputUpdate, 256),
		manualUpdates: make(chan manualUpdate, 256),
		observations:  observations,
	}
}

func (c *Client) Run() error {
	var active *activeTable
	stopActive := func() {
		if active != nil {
			c.abandonSequence(active)
			for _, stop := range active.stops {
				stop()
			}
			active = nil
		}
	}
	defer stopActive()
	healthTicker := time.NewTicker(time.Second)
	defer healthTicker.Stop()

	restart := func() {
		stopActive()
		var err error
		active, err = c.start()
		c.setError(errorText(err))
	}

	if c.config.Enabled && c.config.Applied != "" {
		restart()
	}

	for {
		select {
		case <-c.stop:
			return nil
		case <-healthTicker.C:
			if active != nil {
				c.setError(errorText(c.evaluate(active)))
			}
		case pts := <-c.newPoints:
			updated := c.config
			if err := data.MergePoints(pts.ID, pts.Points, &updated); err != nil {
				log.Printf("decisionTable %s: %v", c.config.ID, err)
				continue
			}
			shouldRestart := false
			newVersion := false
			for _, point := range pts.Points {
				switch point.Type {
				case "applied":
					shouldRestart = true
					newVersion = true
				case "enabled":
					shouldRestart = true
				}
			}
			// Finish the old execution with its old revision/scope before replacing
			// configuration or clearing its last check.
			if shouldRestart {
				stopActive()
			}
			c.config = updated
			if newVersion {
				c.setLastMatch("")
				c.lastCheckRow = ""
				c.lastCheck = ""
				c.lastAllocSig = ""
			}
			if shouldRestart {
				restart()
			}
		case pts := <-c.newEdgePoints:
			updated := c.config
			if err := data.MergeEdgePoints(pts.ID, pts.Parent, pts.Points, &updated); err != nil {
				log.Printf("decisionTable %s: %v", c.config.ID, err)
				continue
			}
			stopActive()
			c.config = updated
			restart()
		case update := <-c.manualUpdates:
			if active == nil || update.generation != active.generation {
				continue
			}
			changed := false
			for _, point := range update.points {
				if point.Type != "easyemsManual" {
					continue
				}
				value, err := point.ValueFloat()
				if err == nil {
					active.manual[update.actionID] = value != 0
					changed = true
				}
			}
			if changed && !active.manual[update.actionID] {
				if err := c.setLastMatch(""); err != nil {
					c.setError(err.Error())
					continue
				}
				// Resuming automatic control must re-evaluate the current row
				// immediately, even if it was already fired once.
				active.engine.Reset()
				c.setError(errorText(c.evaluate(active)))
			}
		case update := <-c.updates:
			if active == nil || update.generation != active.generation {
				continue
			}
			changed := false
			for _, point := range update.points {
				if point.Type != update.target.PointType || point.Key != update.target.Key {
					continue
				}
				if point.Tombstone%2 != 0 {
					delete(active.values, update.pointID)
					changed = true
					continue
				}
				value, err := pointSignal(point)
				if err != nil {
					delete(active.values, update.pointID)
				} else {
					active.values[update.pointID] = signal.Sample{
						Input: update.pointID, Value: value, Quality: signal.Good,
						SourceTime: point.Time, ReceivedTime: point.Time,
					}
					active.loaded[update.pointID] = true
				}
				changed = true
			}
			if changed {
				c.setError(errorText(c.evaluate(active)))
			}
		}
	}
}

func (c *Client) start() (*activeTable, error) {
	if !c.config.Enabled || c.config.Applied == "" {
		return nil, nil
	}
	var document Document
	if err := json.Unmarshal([]byte(c.config.Applied), &document); err != nil {
		return nil, fmt.Errorf("published table: %w", err)
	}
	if c.commands != nil {
		if err := c.commands.Validate(c.config.ID, document); err != nil {
			return nil, err
		}
	}
	table := document.Table
	if err := document.Validate(); err != nil {
		return nil, err
	}
	ruleTable, err := document.RuleTable()
	if err != nil {
		return nil, err
	}
	bindings := document.Bindings

	c.generation++
	active := &activeTable{
		generation:  c.generation,
		table:       table,
		bindings:    bindings,
		values:      make(map[string]signal.Sample),
		loaded:      make(map[string]bool),
		manual:      make(map[string]bool),
		allocations: make(map[string]*allocationRun),
		rampRates:   make(map[string]float64),
		rampTargets: make(map[string]float64),
		rampValues:  make(map[string]float64),
		rampAt:      make(map[string]time.Time),
		safety:      make(map[string]safety.Output),
	}
	fail := func(err error) (*activeTable, error) {
		for _, stop := range active.stops {
			stop()
		}
		return nil, err
	}
	requiredPoints := make(map[string]Target)
	requiredActions := make(map[string]Target)
	for _, row := range table.Rows {
		for _, condition := range row.Conditions {
			if condition.Kind == "time" {
				continue
			}
			target, ok := bindings.Points[condition.PointID]
			if !ok {
				return fail(fmt.Errorf("row %s: point %s is not bound", row.ID, condition.PointID))
			}
			requiredPoints[condition.PointID] = target
		}
		for _, action := range row.Actions {
			if action.Notification != nil {
				if c.commands == nil || c.commands.Notify == nil {
					return fail(fmt.Errorf("notification runtime unavailable"))
				}
				continue
			}
			target, ok := bindings.Actions[action.ActionID]
			if !ok || target.NodeID == "" {
				return fail(fmt.Errorf("row %s: action %s is not bound", row.ID, action.ActionID))
			}
			if err := c.validateTarget(target, true, &action.Value); err != nil {
				return fail(fmt.Errorf("action %s: %w", action.ActionID, err))
			}
			if action.Readback != nil && action.Readback.Target.NodeID != "" {
				if err := c.validateTarget(action.Readback.Target, false, nil); err != nil {
					return fail(fmt.Errorf("action %s readback: %w", action.ActionID, err))
				}
			}
			requiredActions[action.ActionID] = target
		}
	}

	safetySpecs := make(map[string]safety.Output)
	for actionID, target := range requiredActions {
		config, present, err := LoadOutputSafety(c.nc, target.NodeID)
		if err != nil {
			return fail(fmt.Errorf("action %s safety: malformed config: %w", actionID, err))
		}
		if !present {
			continue
		}
		spec, err := config.Output()
		if err != nil {
			return fail(fmt.Errorf("action %s safety: %w", actionID, err))
		}
		safetySpecs[actionID] = spec
		if spec.Ramp > 0 {
			active.rampRates[actionID] = spec.Ramp
		}
		for _, condition := range config.Conditions {
			pointTarget := Target{NodeID: condition.PointID, PointType: data.PointTypeValue}
			if err := c.validateTarget(pointTarget, false, nil); err != nil {
				return fail(fmt.Errorf("action %s safety point %s: %w", actionID, condition.PointID, err))
			}
			requiredPoints[condition.PointID] = pointTarget
		}
	}

	engine, err := rule.New(ruleTable, nil, targetLimiter{
		nc: c.nc, parent: c.config.Parent, actions: bindings.Actions, validate: c.validateTarget,
		manual: func(actionID string) bool { return active.manual[actionID] },
		safety: safetySpecs, values: &active.values,
	})
	if err != nil {
		return fail(err)
	}
	active.safety = safetySpecs
	active.engine = engine
	if c.config.LastMatch != "" {
		engine.Prime(c.config.LastMatch)
	}

	bindings.Points = requiredPoints
	bindings.Actions = requiredActions
	active.bindings.Points = requiredPoints
	active.bindings.Actions = requiredActions
	for actionID, target := range bindings.Actions {
		if config, err := LoadAllocator(c.nc, target.NodeID); err == nil {
			if err := ValidateAllocationOwnership(c.nc, c.config.ID, target.NodeID); err != nil {
				return fail(fmt.Errorf("action %s: %w", actionID, err))
			}
			run := newAllocationRun(target.NodeID, config)
			run.observations = c.observations
			active.allocations[actionID] = run
		}
	}
	for pointID, target := range bindings.Points {
		if target.NodeID == "" {
			return fail(fmt.Errorf("point %s: missing node_id", pointID))
		}
		if err := c.validateTarget(target, false, nil); err != nil {
			return fail(fmt.Errorf("point %s: %w", pointID, err))
		}
		if target.PointType == "" {
			target.PointType = data.PointTypeValue
		}
		generation := c.generation
		stop, err := siotclient.SubscribePoints(c.nc, target.NodeID, func(points []data.Point) {
			c.updates <- inputUpdate{generation, pointID, target, points}
		})
		if err != nil {
			return fail(err)
		}
		active.stops = append(active.stops, stop)

		nodes, err := siotclient.GetNodes(c.nc, "all", target.NodeID, "", false)
		if err != nil || len(nodes) == 0 {
			return fail(fmt.Errorf("point %s: source node unavailable", pointID))
		}
		point, ok := nodes[0].Points.Find(target.PointType, target.Key)
		if ok && point.Tombstone%2 == 0 {
			value, err := pointSignal(point)
			if err == nil {
				active.values[pointID] = signal.Sample{
					Input: pointID, Value: value, Quality: signal.Good,
					SourceTime: point.Time, ReceivedTime: point.Time,
				}
				active.loaded[pointID] = true
			}
		}
	}

	for actionID, target := range bindings.Actions {
		nodes, err := siotclient.GetNodes(c.nc, "all", target.NodeID, "", false)
		if err != nil || len(nodes) == 0 {
			return fail(fmt.Errorf("action %s: target unavailable", actionID))
		}
		manual, _ := nodes[0].Points.Value("easyemsManual", "")
		active.manual[actionID] = manual != 0
		if active.rampRates[actionID] > 0 {
			if current, ok := nodes[0].Points.Find("value", ""); ok {
				if value, err := current.ValueFloat(); err == nil && finite(value) {
					active.rampValues[actionID] = value
					active.rampAt[actionID] = time.Now()
				}
			}
		}
		generation := c.generation
		stop, err := siotclient.SubscribePoints(c.nc, target.NodeID, func(points []data.Point) {
			c.manualUpdates <- manualUpdate{generation: generation, actionID: actionID, points: points}
		})
		if err != nil {
			return fail(err)
		}
		active.stops = append(active.stops, stop)
	}

	// Evaluation starts once at least one input has produced a value. From
	// then on an invalid or missing input makes only its own condition
	// unsatisfied (ADR 0010), so a later fallback row stays reachable.
	var evaluationError error
	if len(active.bindings.Points) == 0 || len(active.loaded) > 0 {
		evaluationError = c.evaluate(active)
	}
	return active, evaluationError
}

// evaluate marks untrusted inputs stale and runs one first-hit evaluation.
// Following ADR 0010, an invalid input only makes its own condition
// unsatisfied; the table keeps evaluating later rows, and the reason is
// reported through the table error and check report.
func (c *Client) evaluate(active *activeTable) error {
	if len(active.bindings.Points) > 0 && len(active.loaded) == 0 {
		return nil
	}
	stale := c.markStale(active)
	err := c.evaluateNow(active)
	if len(stale) > 0 {
		note := strings.Join(stale, "; ")
		if err != nil {
			return fmt.Errorf("%v; %s", err, note)
		}
		return errors.New(note)
	}
	return err
}

func (c *Client) evaluateNow(active *activeTable) error {
	if c.commands != nil {
		var document Document
		if err := json.Unmarshal([]byte(c.config.Applied), &document); err != nil {
			return err
		}
		if err := c.commands.Validate(c.config.ID, document); err != nil {
			c.abandonSequence(active)
			return err
		}
	}
	if active.engine == nil {
		return nil
	}
	now := time.Now()
	samples := active.values
	if timeSamples := TimeSamples(active.table, now); len(timeSamples) > 0 {
		samples = make(map[string]signal.Sample, len(active.values)+len(timeSamples))
		maps.Copy(samples, active.values)
		maps.Copy(samples, timeSamples)
	}
	samples = conditionGroupSamples(active.table, now, samples)
	outcome, err := active.engine.Evaluate(now, samples)
	if err != nil {
		return err
	}

	accepted := 0
	rampChanged := false
	sequenceChanged := false
	if active.sequence != nil && outcome.MatchedRow != active.sequence.rowID {
		c.abandonSequence(active)
		active.sequence = nil
		sequenceChanged = true
	}
	if outcome.State == rule.StateNoMatch {
		clear(active.rampTargets)
	}
	if outcome.Fired {
		sequencedRow := false
		for _, row := range active.table.Rows {
			if row.ID == outcome.MatchedRow && (row.sequenced() || c.commands != nil) {
				active.sequence = newActionSequence(row)
				if c.commands != nil {
					id, err := c.reserveExecution(row.ID, now)
					if err != nil {
						active.sequence = nil
						return err
					}
					active.sequence.executionID = id
				}
				sequencedRow = true
				sequenceChanged = true
				clear(active.rampTargets)
				break
			}
		}
		if !sequencedRow {
			for i := range outcome.Actions {
				action := &outcome.Actions[i]
				if action.Blocked {
					delete(active.rampTargets, action.Output)
					continue
				}
				target := active.bindings.Actions[action.Output]
				if run, ok := active.allocations[action.Output]; ok {
					if err := run.start(c.nc, action.Applied.Num, now); err != nil {
						return fmt.Errorf("action %s: %w", action.Output, err)
					}
					active.pending = append(active.pending, action.Output)
					accepted++
					continue
				}
				if target.PointType == "" {
					target.PointType = data.PointTypeValueSet
				}
				value := 0.0
				if action.Applied.Kind == signal.Number {
					value = action.Applied.Num
				}
				if rate := active.rampRates[action.Output]; rate > 0 && action.Applied.Kind == signal.Number {
					current, ok := active.rampValues[action.Output]
					if !ok {
						// No readback: do not invent a starting point; write the target.
						current = value
						active.rampValues[action.Output] = current
						active.rampAt[action.Output] = now
					}
					next := safety.RampStep(current, value, rate, now.Sub(active.rampAt[action.Output]))
					if next != current {
						active.rampValues[action.Output] = next
						active.rampAt[action.Output] = now
					}
					value = next
					action.Applied = signal.Num(next)
					rampChanged = true
					if next == action.Requested.Num {
						action.Reason = ""
						delete(active.rampTargets, action.Output)
					} else {
						action.Reason = rampReason(next, action.Requested.Num)
						active.rampTargets[action.Output] = action.Requested.Num
					}
				} else {
					delete(active.rampTargets, action.Output)
				}
				point := data.NewPointFloat(target.PointType, target.Key, value)
				point.Origin = c.config.ID
				if err := siotclient.SendNodePoint(c.nc, target.NodeID, point, true); err != nil {
					return fmt.Errorf("action %s: %w", action.Output, err)
				}
				accepted++
			}
		}
	}
	if outcome.State == rule.StateAlreadyFired {
		if err := c.stepRamps(active, now, &outcome, &rampChanged); err != nil {
			return err
		}
	}

	c.stepAllocations(active, now)
	if active.sequence != nil && c.stepSequence(active, now) {
		sequenceChanged = true
	}
	signature := allocationSignature(active)
	if outcome.MatchedRow != c.lastCheckRow || outcome.Fired || rampChanged || sequenceChanged || signature != c.lastAllocSig {
		if err := c.recordCheck(outcome, now, active); err != nil {
			return err
		}
		c.lastCheckRow = outcome.MatchedRow
		c.lastAllocSig = signature
	}

	if outcome.Fired {
		log.Printf("decisionTable %s: matched %s, accepted %d actions, state %s", c.config.ID, outcome.MatchedRow, accepted, outcome.State)
		if err := c.setLastMatch(outcome.MatchedRow); err != nil {
			return err
		}
	}
	if outcome.MatchedRow == "" && c.config.LastMatch != "" {
		return c.setLastMatch("")
	}
	if active.sequence != nil && active.sequence.err != "" {
		return fmt.Errorf("action sequence: %s", active.sequence.err)
	}
	return nil
}

// stepRamps continues an in-progress setpoint ramp once per evaluation, even
// though the rule itself is already-fired. It stops on manual takeover or when
// the row no longer matches.
func (c *Client) stepRamps(active *activeTable, now time.Time, outcome *rule.Outcome, changed *bool) error {
	for output, target := range active.rampTargets {
		if active.manual[output] {
			delete(active.rampTargets, output)
			continue
		}
		rate := active.rampRates[output]
		if rate <= 0 {
			delete(active.rampTargets, output)
			continue
		}
		if spec, ok := active.safety[output]; ok {
			limiter := safety.Limiter{
				Outputs: map[string]safety.Output{output: spec},
				Samples: active.values,
			}
			limited := limiter.Limit(now, output, signal.Num(target))
			if limited.Blocked {
				outcome.Actions = append(outcome.Actions, rule.AppliedAction{
					Output: output, Requested: signal.Num(target), Applied: signal.Num(target),
					Blocked: true, Reason: "ramp stopped: " + limited.Reason,
				})
				*changed = true
				delete(active.rampTargets, output)
				continue
			}
			target = limited.Value.Num
			active.rampTargets[output] = target
		}
		current := active.rampValues[output]
		next := safety.RampStep(current, target, rate, now.Sub(active.rampAt[output]))
		if next == current {
			if current == target {
				delete(active.rampTargets, output)
			}
			continue
		}
		binding := active.bindings.Actions[output]
		if binding.PointType == "" {
			binding.PointType = data.PointTypeValueSet
		}
		point := data.NewPointFloat(binding.PointType, binding.Key, next)
		point.Origin = c.config.ID
		if err := siotclient.SendNodePoint(c.nc, binding.NodeID, point, true); err != nil {
			return fmt.Errorf("action %s ramp: %w", output, err)
		}
		active.rampValues[output] = next
		active.rampAt[output] = now
		reason := ""
		if next != target {
			reason = rampReason(next, target)
		}
		outcome.Actions = append(outcome.Actions, rule.AppliedAction{
			Output: output, Requested: signal.Num(target), Applied: signal.Num(next), Reason: reason,
		})
		*changed = true
		if next == target {
			delete(active.rampTargets, output)
		}
	}
	return nil
}

func rampReason(current, target float64) string {
	return fmt.Sprintf("ramping to %g (current %g)", target, current)
}

// stepAllocations advances one output at a time. A failed or unknown member
// stops the remaining outputs in the same matched row.
func (c *Client) stepAllocations(active *activeTable, now time.Time) {
	for len(active.pending) > 0 {
		output := active.pending[0]
		run := active.allocations[output]
		run.step(c.nc, now)
		switch run.status {
		case "confirmed":
			c.recordFinished(run, now)
			active.pending = active.pending[1:]
			continue
		case "failed", "unknown":
			c.recordFinished(run, now)
			active.pending = nil
			return
		default:
			return
		}
	}
}

func (c *Client) recordFinished(run *allocationRun, now time.Time) {
	if run.recorded {
		return
	}
	c.recordAllocation(run.parent, run, now)
	run.recorded = true
}

func allocationSignature(active *activeTable) string {
	if len(active.allocations) == 0 {
		return ""
	}
	keys := make([]string, 0, len(active.allocations))
	for key := range active.allocations {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	for _, key := range keys {
		builder.WriteString(key)
		builder.WriteString(active.allocations[key].signature())
	}
	return builder.String()
}

// recordCheck publishes the evaluation snapshot and the reason each action was
// applied or blocked, so the interface can explain the outcome.
func (c *Client) recordCheck(outcome rule.Outcome, now time.Time, active *activeTable) error {
	text, err := marshalCheck(buildCheck(outcome, now))
	if err != nil {
		return err
	}
	var report CheckReport
	if err := json.Unmarshal([]byte(text), &report); err != nil {
		return err
	}
	if len(report.Actions) == 0 && outcome.MatchedRow != "" {
		for _, row := range active.table.Rows {
			if row.ID != outcome.MatchedRow {
				continue
			}
			for _, action := range row.Actions {
				check := CheckAction{Output: action.ActionID, Requested: action.Value, Applied: action.Value}
				if action.Notification != nil {
					check.Kind, check.Status = "notify", string(outcome.State)
					if outcome.State == rule.StateAlreadyFired {
						check.Status, check.Reason = "unknown", "no recorded notification creation receipt"
					}
				}
				report.Actions = append(report.Actions, check)
			}
		}
	}
	if active.sequence != nil && active.sequence.rowID == outcome.MatchedRow {
		report.Actions = append([]CheckAction(nil), active.sequence.checks...)
	}
	report.RuleID = c.config.ID
	for i := range report.Inputs {
		report.Inputs[i].Target = active.bindings.Points[report.Inputs[i].Input]
	}
	report.SourceRevision = c.documentRevision()
	if active.sequence != nil {
		report.ExecutionID = active.sequence.executionID
	} else if outcome.State == rule.StateAlreadyFired {
		if err := c.restoreNotificationReceipts(&report); err != nil {
			return err
		}
	}
	for i := range report.Actions {
		if report.Actions[i].Kind == "notify" {
			continue
		}
		report.Actions[i].Target = active.bindings.Actions[report.Actions[i].Output]
		if run, ok := active.allocations[report.Actions[i].Output]; ok {
			report.Actions[i].Shares = run.result.Shares
			report.Actions[i].Status = run.status
			report.Actions[i].Applied = run.result.Delivered
			if run.reason != "" {
				report.Actions[i].Reason = run.reason
			}
		}
	}
	// Recovery is a projection of the prior execution, not new execution evidence.
	return c.persistCheck(report, outcome.State != rule.StateAlreadyFired || active.sequence != nil)
}

// A primed engine does not re-run an interrupted sequence. Keep only its
// previously recorded notification facts, tied to the same reserved execution.
// A reservation without a creation receipt remains unknown, never created.
func (c *Client) restoreNotificationReceipts(report *CheckReport) error {
	hasNotification := false
	for _, action := range report.Actions {
		hasNotification = hasNotification || action.Kind == "notify"
	}
	if !hasNotification {
		return nil
	}
	nodes, err := siotclient.GetNodes(c.nc, c.config.Parent, c.config.ID, "", false)
	if err != nil {
		return fmt.Errorf("read previous notification facts: %w", err)
	}
	if len(nodes) != 1 {
		return nil
	}
	var reserved ExecutionReservation
	reservation, _ := nodes[0].Points.Text("lastExecution", "")
	if json.Unmarshal([]byte(reservation), &reserved) != nil || reserved.ID == "" || reserved.Row != report.Row || reserved.Revision != report.SourceRevision {
		return nil
	}
	report.ExecutionID = reserved.ID
	var previous CheckReport
	text, _ := nodes[0].Points.Text("lastCheck", "")
	if json.Unmarshal([]byte(text), &previous) != nil || previous.RuleID != report.RuleID || previous.Row != report.Row || previous.SourceRevision != report.SourceRevision || previous.ExecutionID != reserved.ID || len(previous.Actions) != len(report.Actions) {
		return nil
	}
	for i, action := range previous.Actions {
		if report.Actions[i].Kind != "notify" || action.Kind != "notify" || action.Output != report.Actions[i].Output || action.CommandID != "" || action.Target.NodeID != "" {
			continue
		}
		switch action.Status {
		case "created":
			if action.NotificationID == "" || action.Confirmation != "durable_record" {
				continue
			}
		case "failed", "unknown", "skipped":
			if action.NotificationID != "" {
				continue
			}
		default:
			continue
		}
		report.Actions[i] = action
	}
	return nil
}

// markStale downgrades inputs whose source cannot currently be trusted, so
// their conditions evaluate as unsatisfied instead of re-using an old value.
// A recovered source restores trust for the last known value; a deleted or
// invalid point simply stays absent.
func (c *Client) markStale(active *activeTable) []string {
	var reasons []string
	var observed map[string]signal.Sample
	if c.commands != nil && c.observations != nil {
		observed = c.observations(time.Now())
	}
	for pointID, target := range active.bindings.Points {
		if observed != nil {
			nodes, err := siotclient.GetNodes(c.nc, "all", target.NodeID, "", false)
			physical := err == nil && len(nodes) > 0 && (nodes[0].Type == data.NodeTypeModbusIO || nodes[0].Type == "opcuaPoint" || nodes[0].Type == "mqttSub")
			if physical {
				sample, present := observed[target.NodeID]
				if !present {
					sample.Quality = signal.Uninitialized
				}
				sample.Input = pointID
				active.values[pointID] = sample
				if present {
					active.loaded[pointID] = true
				}
				if sample.Quality != signal.Good {
					reasons = append(reasons, "point "+pointID+": "+sample.Quality.String())
					continue
				}
			}
		}
		err := validateFreshInput(c.nc, pointID, target)
		sample, present := active.values[pointID]
		if err != nil {
			if present {
				sample.Quality = signal.Stale
				active.values[pointID] = sample
			}
			reasons = append(reasons, err.Error())
			continue
		}
		if present && sample.Quality != signal.Good {
			sample.Quality = signal.Good
			active.values[pointID] = sample
		}
	}
	sort.Strings(reasons)
	return reasons
}

func validateFreshInput(nc *nats.Conn, pointID string, target Target) error {
	nodes, err := siotclient.GetNodes(nc, "all", target.NodeID, "", false)
	if err != nil || len(nodes) == 0 {
		return fmt.Errorf("point %s: input unavailable", pointID)
	}
	disabled, _ := nodes[0].Points.Value(data.PointTypeDisabled, "")
	if disabled != 0 {
		return fmt.Errorf("point %s: input disabled", pointID)
	}
	if nodes[0].Type != data.NodeTypeModbusIO {
		// 其他协议（OPC-UA/MQTT）没有 Modbus 心跳，按最新值时间判断时效。
		if nodes[0].Type == "opcuaPoint" || nodes[0].Type == "mqttSub" {
			value, found := nodes[0].Points.Find(data.PointTypeValue, "")
			if !found || value.Time.IsZero() {
				return fmt.Errorf("point %s: no %s sample", pointID, nodes[0].Type)
			}
			age := time.Since(value.Time)
			if age > 15*time.Second || age < -5*time.Second {
				return fmt.Errorf("point %s: stale %s input", pointID, nodes[0].Type)
			}
		}
		return nil
	}
	busID := nodes[0].Parent
	buses, err := siotclient.GetNodes(nc, "all", busID, "", false)
	if err != nil || len(buses) == 0 {
		return fmt.Errorf("point %s: bus unavailable", pointID)
	}
	busDisabled, _ := buses[0].Points.Value(data.PointTypeDisabled, "")
	if busDisabled != 0 {
		return fmt.Errorf("point %s: bus disabled", pointID)
	}
	heartbeat, ok := buses[0].Points.Find("lastGoodPoll", "")
	if !ok || heartbeat.Time.IsZero() {
		return fmt.Errorf("point %s: no successful Modbus poll", pointID)
	}
	poll, _ := buses[0].Points.Value(data.PointTypePollPeriod, "")
	timeout, _ := buses[0].Points.Value(data.PointTypeTimeout, "")
	maxAge := 5 * time.Second
	if duration := 5 * time.Duration(poll) * time.Millisecond; duration > maxAge {
		maxAge = duration
	}
	if duration := 3 * time.Duration(timeout) * time.Millisecond; duration > maxAge {
		maxAge = duration
	}
	age := time.Since(heartbeat.Time)
	if age > maxAge || age < -5*time.Second {
		return fmt.Errorf("point %s: stale Modbus input", pointID)
	}
	return nil
}

func (c *Client) setLastMatch(rowID string) error {
	if c.config.LastMatch == rowID {
		return nil
	}
	point := data.NewPointString("lastMatch", "", rowID)
	if err := siotclient.SendNodePoint(c.nc, c.config.ID, point, true); err != nil {
		return err
	}
	c.config.LastMatch = rowID
	return nil
}

func (c *Client) setError(message string) {
	if c.config.Error == message {
		return
	}
	if err := siotclient.SendNodePoint(c.nc, c.config.ID, data.NewPointString("error", "", message), true); err != nil {
		log.Printf("decisionTable %s: cannot record error: %v", c.config.ID, err)
		return
	}
	c.config.Error = message
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (c *Client) Stop(_ error) {
	close(c.stop)
}

func (c *Client) Points(nodeID string, points []data.Point) {
	c.newPoints <- siotclient.NewPoints{ID: nodeID, Points: points}
}

func (c *Client) EdgePoints(nodeID, parentID string, points []data.Point) {
	c.newEdgePoints <- siotclient.NewPoints{ID: nodeID, Parent: parentID, Points: points}
}
