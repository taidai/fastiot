package decision

import (
	"encoding/json"
	"fmt"
	"time"

	"fastiot/internal/ems/command"
	"fastiot/internal/ems/signal"
	"github.com/nats-io/nats.go"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

// RuleCommand carries immutable source identity into the canonical command owner.
type ExecutionReservation struct {
	ID       string    `json:"execution_id"`
	Row      string    `json:"row_id"`
	Revision string    `json:"source_revision"`
	At       time.Time `json:"at"`
}

type RuleCommand struct {
	RuleID, Revision, ExecutionID, RequestID, RowID string
	Step                                            int
	Target                                          Target
	Action                                          Action
}

// RuleCommands is the production command boundary. Simulation never receives it.
// Legacy standalone clients keep their old transport; the application always
// constructs its clients with this boundary and never falls back from it.
type RuleCommands struct {
	Validate         func(string, Document) error
	Submit           func(RuleCommand) (command.Outcome, error)
	Get              func(string) (command.Outcome, bool)
	SoftwareFeedback func(string, Target, int, float64) (command.Outcome, bool)
	StopSoftware     func(string)
}

func NewDecisionTableClientWithCommands(nc *nats.Conn, config DecisionTable, observations Observations, commands RuleCommands) client.Client {
	c := NewDecisionTableClientWithObservations(nc, config, observations).(*Client)
	c.commands = &commands
	return c
}

func (c *Client) validateTarget(target Target, output bool, value *float64) error {
	if c.commands == nil {
		return ValidateTarget(c.nc, c.config.Parent, target, output, value)
	}
	// Business scope and authorization are checked by Validate and again on Submit.
	// Retain the existing point type/write/read-only checks independently of the
	// acquisition tree location of a mapped business point.
	nodes, err := client.GetNodes(c.nc, "all", target.NodeID, "", false)
	if err != nil || len(nodes) == 0 {
		return fmt.Errorf("target unavailable")
	}
	return ValidateTarget(c.nc, nodes[0].Parent, target, output, value)
}

func (c *Client) reserveExecution(row string, now time.Time) (string, error) {
	id := data.NewID()
	encoded, err := json.Marshal(ExecutionReservation{id, row, c.documentRevision(), now})
	if err != nil {
		return "", err
	}
	// Persist the first-match reservation before any command callback. A restart
	// primes LastMatch and must not replay an interrupted sequence.
	if err = c.setLastMatch(row); err != nil {
		return "", err
	}
	if err = client.SendNodePoint(c.nc, c.config.ID, data.NewPointString("lastExecution", "", string(encoded)), true); err != nil {
		return "", err
	}
	return id, nil
}

func (c *Client) documentRevision() string {
	var doc Document
	_ = json.Unmarshal([]byte(c.config.Applied), &doc)
	return doc.Revision
}

func (c *Client) abandonSequence(active *activeTable) {
	if c.commands == nil || active == nil || active.sequence == nil || active.sequence.done {
		return
	}
	if c.commands.StopSoftware != nil {
		for _, check := range active.sequence.checks {
			if check.CommandID != "" {
				c.commands.StopSoftware(check.CommandID)
			}
		}
	}
	active.sequence.fail("unknown", "rule stopped before sequence completion; device commands retain canonical outcomes")
	var report CheckReport
	if json.Unmarshal([]byte(c.lastCheck), &report) == nil {
		report.At, report.State = time.Now(), "unknown"
		report.Actions = append([]CheckAction(nil), active.sequence.checks...)
		for i := range report.Actions {
			report.Actions[i].Target = active.bindings.Actions[report.Actions[i].Output]
		}
		if text, err := marshalCheck(report); err == nil {
			point := data.NewPointString("lastCheck", "", text)
			point.Origin = c.config.ID
			_ = client.SendNodePoint(c.nc, c.config.ID, point, true)
		}
	}
}

func (c *Client) submitRuleStep(active *activeTable, action Action, target Target) error {
	s := active.sequence
	id := fmt.Sprintf("%s-%d", s.executionID, s.next)
	s.checks[s.next].CommandID = id
	outcome, err := c.commands.Submit(RuleCommand{RuleID: c.config.ID, Revision: c.documentRevision(), ExecutionID: s.executionID, RequestID: id, RowID: s.rowID, Step: s.next, Target: target, Action: action})
	if err != nil {
		return err
	}
	s.started = outcome.StartedAt
	for _, write := range outcome.Writes {
		if write.Point != target.NodeID {
			continue
		}
		s.checks[s.next].Applied = write.Value.Num
		if write.Value.Kind == signal.Bool {
			s.checks[s.next].Applied = data.BoolToFloat(write.Value.Bool)
		}
	}
	switch outcome.State {
	case command.Accepted, command.Executing, command.Confirmed:
		return nil
	default:
		return fmt.Errorf("command %s: %s", outcome.State, outcome.Reason)
	}
}
