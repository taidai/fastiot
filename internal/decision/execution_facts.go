package decision

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	siotclient "github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

// RuleExecutionFactPoint is runtime evidence, never a project asset or a raw
// public snapshot point. Existing latest-only history cannot be reconstructed.
const RuleExecutionFactPoint = "ruleExecutionFact"

type RuleExecutionFact struct {
	Schema      int         `json:"schema"`
	ScopeNodeID string      `json:"scope_node_id"`
	StartedAt   time.Time   `json:"started_at"`
	Sequence    uint64      `json:"sequence"`
	Source      Document    `json:"source"`
	Report      CheckReport `json:"report"`
}

// All producer reports, including abandonment, persist through this boundary.
// The latest summary is not acknowledged until its execution fact is retained.
func (c *Client) persistCheck(report CheckReport, capture bool) error {
	text, err := marshalCheck(report)
	if err != nil {
		return err
	}
	if text == c.lastCheck {
		return nil
	}
	if capture {
		if err := c.recordExecutionFact(report); err != nil {
			return err
		}
	} else if err := c.recoverExecutionFact(report); err != nil {
		return err
	}
	point := data.NewPointString("lastCheck", "", text)
	point.Origin = c.config.ID
	if err := siotclient.SendNodePoint(c.nc, c.config.ID, point, true); err != nil {
		return fmt.Errorf("record check: %w", err)
	}
	c.lastCheck = text
	return nil
}

// Capture at the existing producer, not by polling. Content-addressed reports
// preserve prior evidence; a reservation's logical sequence survives wall-clock
// movement. Identical observations/recovery never append or count a new execution.
func (c *Client) recordExecutionFact(report CheckReport) error {
	if c.commands == nil || report.ExecutionID == "" {
		return nil
	}
	// Points belong to the node identity, not the edge. MoveNode temporarily
	// exposes old and new edges together, both carrying the same node points.
	nodes, err := siotclient.GetNodes(c.nc, "all", c.config.ID, "", false)
	if err != nil || len(nodes) == 0 {
		return fmt.Errorf("execution capture source unavailable: %v", err)
	}
	var reserved ExecutionReservation
	raw, _ := nodes[0].Points.Text("lastExecution", "")
	if json.Unmarshal([]byte(raw), &reserved) != nil || reserved.ID != report.ExecutionID || reserved.Revision != report.SourceRevision || reserved.Row != report.Row {
		return nil
	}
	var source Document
	if err := json.Unmarshal([]byte(c.config.Applied), &source); err != nil {
		return err
	}
	fact := RuleExecutionFact{Schema: 1, ScopeNodeID: c.config.Parent, StartedAt: reserved.At, Source: source, Report: report}
	return c.appendExecutionFact(nodes[0], fact)
}

// A primed engine will not resume an old sequence. Resolve its captured waits
// as unknown without replacing already recorded outcomes or decision inputs.
func (c *Client) recoverExecutionFact(report CheckReport) error {
	if c.commands == nil {
		return nil
	}
	nodes, err := siotclient.GetNodes(c.nc, "all", c.config.ID, "", false)
	if err != nil || len(nodes) == 0 {
		return fmt.Errorf("execution recovery source unavailable: %v", err)
	}
	var reserved ExecutionReservation
	raw, _ := nodes[0].Points.Text("lastExecution", "")
	if json.Unmarshal([]byte(raw), &reserved) != nil || reserved.ID == "" || reserved.Revision != report.SourceRevision || reserved.Row != report.Row {
		return nil
	}
	var latest RuleExecutionFact
	for _, point := range nodes[0].Points {
		if point.Type != RuleExecutionFactPoint || point.Tombstone%2 != 0 {
			continue
		}
		var fact RuleExecutionFact
		if json.Unmarshal([]byte(point.Txt()), &fact) != nil {
			return fmt.Errorf("invalid execution capture")
		}
		if fact.Schema == 1 && fact.Report.RuleID == c.config.ID && fact.Report.ExecutionID == reserved.ID && fact.Report.Row == reserved.Row && fact.Report.SourceRevision == reserved.Revision && fact.Source.Revision == reserved.Revision && !fact.StartedAt.IsZero() && fact.Sequence > latest.Sequence {
			latest = fact
		}
	}
	interrupted := false
	for i := range latest.Report.Actions {
		switch latest.Report.Actions[i].Status {
		case "pending", "waiting-delay", "waiting-readback", "accepted", "executing":
			interrupted = true
			latest.Report.Actions[i].Status = "unknown"
			latest.Report.Actions[i].Reason = "execution interrupted; commands are not replayed"
		}
	}
	if !interrupted {
		return nil
	}
	latest.Report.State, latest.Report.At = "unknown", time.Now()
	return c.appendExecutionFact(nodes[0], latest)
}

func (c *Client) appendExecutionFact(node data.NodeEdge, fact RuleExecutionFact) error {
	fact.Sequence = 0
	normalized := fact
	normalized.Report.At = time.Time{}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("%x", sha256.Sum256(encoded))
	if _, exists := node.Points.Find(RuleExecutionFactPoint, key); exists {
		return nil
	}
	for _, point := range node.Points {
		if point.Type != RuleExecutionFactPoint || point.Tombstone%2 != 0 {
			continue
		}
		var previous RuleExecutionFact
		if json.Unmarshal([]byte(point.Txt()), &previous) != nil {
			return fmt.Errorf("invalid execution capture")
		}
		if previous.Report.ExecutionID == fact.Report.ExecutionID && previous.Sequence > fact.Sequence {
			fact.Sequence = previous.Sequence
		}
	}
	fact.Sequence++
	encoded, err = json.Marshal(fact)
	if err != nil {
		return err
	}
	point := data.NewPointString(RuleExecutionFactPoint, key, string(encoded))
	point.Origin = c.config.ID
	return siotclient.SendNodePoint(c.nc, c.config.ID, point, true)
}
