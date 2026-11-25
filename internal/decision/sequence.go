package decision

import (
	"fmt"
	"math"
	"time"

	"github.com/nats-io/nats.go"
	siotclient "github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/ems/signal"
)

type actionSequence struct {
	rowID     string
	actions   []Action
	checks    []CheckAction
	next      int
	notBefore time.Time
	started   time.Time
	baseline  time.Time
	waiting   bool
	done      bool
	err       string
}

func readbackTarget(action Action, binding Target) Target {
	target := Target{}
	if action.Readback != nil {
		target = action.Readback.Target
	}
	if target.NodeID == "" {
		target.NodeID = binding.NodeID
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
		limiter := targetLimiter{
			nc: c.nc, parent: c.config.Parent, actions: active.bindings.Actions,
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
		if run, ok := active.allocations[action.ActionID]; ok {
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
