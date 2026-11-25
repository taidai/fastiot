package decision

import (
	"fmt"
	"math"
	"time"

	"github.com/nats-io/nats.go"
	siotclient "github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/ems/command"
	"fastiot/internal/ems/signal"
)

type actionSequence struct {
	executionID string
	rowID       string
	actions     []Action
	checks      []CheckAction
	next        int
	notBefore   time.Time
	started     time.Time
	baseline    time.Time
	waiting     bool
	done        bool
	err         string
}

func readbackTarget(action Action, binding Target) Target {
	target := Target{}
	if action.Readback != nil {
		target = action.Readback.Target
	}
	if target.NodeID == "" {
		target.NodeID = binding.NodeID
		if target.Key == "" {
			target.Key = binding.Key
		}
	}
	if target.PointType == "" {
		target.PointType = data.PointTypeValue
	}
	return target
}

// readbackTime is the newest readback sample time before dispatch. Requiring a
// strictly newer sample still accepts a response recorded during dispatch.
func readbackTime(nc *nats.Conn, action Action, binding Target) time.Time {
	target := readbackTarget(action, binding)
	nodes, err := siotclient.GetNodes(nc, "all", target.NodeID, "", false)
	if err != nil || len(nodes) == 0 {
		return time.Time{}
	}
	point, ok := nodes[0].Points.Find(target.PointType, target.Key)
	if !ok || point.Tombstone%2 != 0 {
		return time.Time{}
	}
	return point.Time
}

func newActionSequence(row Row) *actionSequence {
	s := &actionSequence{rowID: row.ID, actions: row.Actions}
	for _, action := range row.Actions {
		s.checks = append(s.checks, CheckAction{Output: action.ActionID, Requested: action.Value, Applied: action.Value, Status: "pending"})
		if action.Notification != nil {
			s.checks[len(s.checks)-1].Kind = "notify"
		}
	}
	return s
}

func (s *actionSequence) fail(status, reason string) {
	if s.next < len(s.checks) {
		s.checks[s.next].Status = status
		s.checks[s.next].Reason = reason
	}
	for i := s.next + 1; i < len(s.checks); i++ {
		s.checks[i].Status = "skipped"
		s.checks[i].Reason = "previous action was not confirmed"
	}
	s.done = true
	s.err = reason
}

func (s *actionSequence) confirm() {
	s.checks[s.next].Status = "confirmed"
	s.checks[s.next].Reason = ""
	s.next++
	s.waiting = false
	s.started = time.Time{}
	s.notBefore = time.Time{}
	if s.next == len(s.actions) {
		s.done = true
	}
}

func (c *Client) checkRuleReadback(s *actionSequence, action Action, binding Target, now time.Time) bool {
	check := &s.checks[s.next]
	outcome, ok := c.commands.Get(check.CommandID)
	if !ok {
		s.fail("unknown", "canonical command unavailable")
		return true
	}
	if outcome.State != command.Accepted && outcome.State != command.Executing && outcome.State != command.Confirmed {
		s.fail(string(outcome.State), outcome.Reason)
		return true
	}
	readback := action.Readback
	if readback == nil {
		readback = &Readback{TimeoutMS: 5000}
	}
	deadline := s.started.Add(time.Duration(readback.TimeoutMS) * time.Millisecond)
	target := readbackTarget(action, binding)
	if outcome.External {
		if c.commands.SoftwareFeedback == nil {
			s.fail("unknown", "software feedback adapter unavailable")
			return true
		}
		outcome, ok = c.commands.SoftwareFeedback(check.CommandID, target, readback.TimeoutMS, readback.Tolerance)
		if !ok {
			s.fail("unknown", "software command unavailable")
			return true
		}
		if outcome.State == command.Confirmed {
			s.confirm()
			check.Confirmation = "software"
			return true
		}
		if outcome.State != command.Accepted {
			s.fail(string(outcome.State), outcome.Reason)
			return true
		}
		if !now.Before(deadline) {
			s.fail("unknown", "software acknowledgment result could not be persisted")
			return true
		}
		return false
	}
	if c.observations != nil {
		sample, present := c.observations(now)[target.NodeID]
		value := sample.Value.Num
		if sample.Value.Kind == signal.Bool {
			value = data.BoolToFloat(sample.Value.Bool)
		}
		if present && sample.Quality == signal.Good && (sample.Value.Kind == signal.Number || sample.Value.Kind == signal.Bool) && finite(value) && sample.SourceTime.After(s.started) && sample.ReceivedTime.After(s.started) && !sample.SourceTime.After(deadline) && !sample.ReceivedTime.After(deadline) && !sample.SourceTime.After(now) && !sample.ReceivedTime.After(now) {
			state, reason := command.Confirmed, ""
			if math.Abs(value-check.Applied) > readback.Tolerance {
				state, reason = command.Failed, "readback does not match requested value"
			}
			// A rule's extra readback constraint cannot confirm a device command.
			// Only the canonical manager may establish device confirmation.
			if state == command.Failed {
				s.fail(string(state), reason)
				return true
			}
			if outcome.State == command.Confirmed {
				s.confirm()
				check.Confirmation = "device"
				return true
			}
		}
	}
	if !now.Before(deadline) {
		reason := "readback timeout; result unknown"
		s.fail("unknown", reason)
		return true
	}
	return false
}

func (c *Client) stepSequence(active *activeTable, now time.Time) bool {
	s := active.sequence
	if s == nil || s.done {
		return false
	}
	if s.next >= len(s.actions) {
		s.done = true
		return true
	}
	action := s.actions[s.next]
	check := &s.checks[s.next]
	if !s.waiting {
		if s.notBefore.IsZero() {
			s.notBefore = now.Add(time.Duration(action.DelayMS) * time.Millisecond)
			check.Status = "waiting-delay"
			if now.Before(s.notBefore) {
				return true
			}
		}
		if now.Before(s.notBefore) {
			return false
		}
		if action.Notification != nil {
			if c.commands == nil || c.commands.Notify == nil {
				s.fail("failed", "notification runtime unavailable")
				return true
			}
			id := fmt.Sprintf("%s-%d", s.executionID, s.next)
			created, err := c.commands.Notify(RuleCommand{RuleID: c.config.ID, Revision: c.documentRevision(), ExecutionID: s.executionID, RequestID: id, RowID: s.rowID, Step: s.next, Action: action})
			if err != nil {
				s.fail("failed", err.Error())
				return true
			}
			if created.Status != "created" || created.ID == "" {
				s.fail("unknown", "notification creation not confirmed")
				return true
			}
			s.confirm()
			check.Status = "created"
			check.NotificationID = created.ID
			check.Confirmation = "durable_record"
			return true
		}
		limiter := targetLimiter{
			nc: c.nc, parent: c.config.Parent, actions: active.bindings.Actions, validate: c.validateTarget,
			manual: func(actionID string) bool { return active.manual[actionID] },
			safety: active.safety, values: &active.values,
		}
		limited := limiter.Limit(now, action.ActionID, signal.Num(action.Value))
		if limited.Blocked {
			s.fail("failed", limited.Reason)
			return true
		}
		check.Applied = limited.Value.Num
		target := active.bindings.Actions[action.ActionID]
		if active.rampRates[action.ActionID] > 0 {
			s.fail("failed", "ramped outputs are not supported in an action sequence")
			return true
		}
		if c.commands != nil {
			if err := c.submitRuleStep(active, action, target); err != nil {
				s.fail("failed", err.Error())
				return true
			}
		} else if run, ok := active.allocations[action.ActionID]; ok {
			s.started = now
			if err := run.start(c.nc, limited.Value.Num, now); err != nil {
				s.fail("failed", err.Error())
				return true
			}
		} else {
			if target.PointType == "" {
				target.PointType = data.PointTypeValueSet
			}
			// Remember the pre-dispatch readback time so a sample recorded
			// during dispatch still counts as the response to this action.
			s.baseline = readbackTime(c.nc, action, active.bindings.Actions[action.ActionID])
			s.started = time.Now()
			point := data.NewPointFloat(target.PointType, target.Key, limited.Value.Num)
			point.Origin = c.config.ID
			if err := siotclient.SendNodePoint(c.nc, target.NodeID, point, true); err != nil {
				s.fail("failed", err.Error())
				return true
			}
		}
		s.waiting = true
		check.Status = "waiting-readback"
		return true
	}

	if c.commands != nil {
		return c.checkRuleReadback(s, action, active.bindings.Actions[action.ActionID], now)
	}
	if run, ok := active.allocations[action.ActionID]; ok {
		run.step(c.nc, now)
		check.Shares = run.result.Shares
		check.Applied = run.result.Delivered
		switch run.status {
		case "confirmed":
			c.recordFinished(run, now)
			s.confirm()
		case "failed", "unknown":
			c.recordFinished(run, now)
			s.fail(run.status, run.reason)
		}
		return true
	}

	readback := action.Readback
	if readback == nil {
		readback = &Readback{TimeoutMS: 5000}
	}
	target := readbackTarget(action, active.bindings.Actions[action.ActionID])
	nodes, err := siotclient.GetNodes(c.nc, "all", target.NodeID, "", false)
	if err != nil || len(nodes) == 0 {
		s.fail("unknown", "readback target unavailable after dispatch")
		return true
	}
	point, ok := nodes[0].Points.Find(target.PointType, target.Key)
	if ok && point.Tombstone%2 == 0 && point.Time.After(s.baseline) {
		value, valueErr := point.ValueFloat()
		if valueErr != nil || !finite(value) {
			s.fail("unknown", "readback value is invalid after dispatch")
			return true
		}
		if math.Abs(value-check.Applied) <= readback.Tolerance {
			s.confirm()
			return true
		}
		s.fail("failed", fmt.Sprintf("readback %g does not match %g", value, check.Applied))
		return true
	}
	if now.Sub(s.started) >= time.Duration(readback.TimeoutMS)*time.Millisecond {
		s.fail("unknown", "readback timeout; result unknown")
		return true
	}
	return false
}
