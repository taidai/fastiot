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

// Capture at the existing producer, not by polling. Content-addressed reports
// preserve prior evidence; a reservation's logical sequence survives wall-clock
// movement. Identical observations/recovery never append or count a new execution.
func (c *Client) recordExecutionFact(report CheckReport) error {
	if c.commands == nil || report.ExecutionID == "" {
		return nil
	}
	nodes, err := siotclient.GetNodes(c.nc, "all", c.config.ID, "", false)
	if err != nil || len(nodes) != 1 {
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
	normalized := fact
	normalized.Report.At = time.Time{}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("%x", sha256.Sum256(encoded))
	if _, exists := nodes[0].Points.Find(RuleExecutionFactPoint, key); exists {
		return nil
	}
	for _, point := range nodes[0].Points {
		if point.Type != RuleExecutionFactPoint || point.Tombstone%2 != 0 {
			continue
		}
		var previous RuleExecutionFact
		if json.Unmarshal([]byte(point.Txt()), &previous) != nil {
			return fmt.Errorf("invalid execution capture")
		}
		if previous.Report.ExecutionID == report.ExecutionID && previous.Sequence > fact.Sequence {
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
