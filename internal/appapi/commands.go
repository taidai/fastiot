package appapi

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/ems/command"
)

type CommandResult struct {
	RequestID string    `json:"request_id"`
	Status    string    `json:"status"`
	Target    float64   `json:"target"`
	Actor     string    `json:"actor"`
	CreatedAt time.Time `json:"created_at"`
}

// commandFinding is one command whose result is failed or unknown, read from
// the two places the runtime records results: the point-level lastCommand
// record and the named command manager.
type commandFinding struct {
	Object string
	State  string
}

// commandFindings collects failed or unknown command results for the nodes in
// scope. Named commands are keyed by "<bus id>/<server id>", so they are
// reported through the bus node that owns the device.
func (h *Handler) commandFindings(nodes []data.NodeEdge) []commandFinding {
	names := make(map[string]string, len(nodes))
	for _, node := range nodes {
		names[node.ID] = node.Desc()
	}
	describe := func(id string) string {
		if name, ok := names[id]; ok && name != "" {
			return name
		}
		return id
	}

	findings := make([]commandFinding, 0)
	for _, node := range nodes {
		if node.Type != data.NodeTypeVariable && node.Type != data.NodeTypeModbusIO {
			continue
		}
		text, _ := node.Points.Text("lastCommand", "")
		if text == "" {
			continue
		}
		var result CommandResult
		if json.Unmarshal([]byte(text), &result) != nil {
			continue
		}
		if result.Status == "unknown" || result.Status == "failed" {
			findings = append(findings, commandFinding{Object: describe(node.ID), State: result.Status})
		}
	}
	if h.ems == nil || h.ems.Service == nil {
		return findings
	}
	for _, outcome := range h.ems.Service.Commands().List() {
		if outcome.State != command.Failed && outcome.State != command.Unknown {
			continue
		}
		busID, server, named := strings.Cut(outcome.DeviceID, "/")
		if !named {
			busID = outcome.DeviceID
		}
		if _, ok := names[busID]; !ok {
			continue
		}
		object := describe(busID)
		if named {
			object += "/" + server
		}
		findings = append(findings, commandFinding{Object: object, State: string(outcome.State)})
	}
	return findings
}

func (h *Handler) existingCommand(id, requestID string, target float64) (CommandResult, bool, error) {
	nodes, err := client.GetNodes(h.nc, "all", id, "", false)
	if err != nil || len(nodes) == 0 {
		return CommandResult{}, false, err
	}
	point, ok := nodes[0].Points.Find("lastCommand", "")
	if !ok {
		return CommandResult{}, false, nil
	}
	var result CommandResult
	if json.Unmarshal([]byte(point.Txt()), &result) != nil || result.RequestID != requestID {
		return CommandResult{}, false, nil
	}
	if result.Target != target {
		return CommandResult{}, false, &commandConflictError{}
	}
	return result, true, nil
}

type commandConflictError struct{}

func (*commandConflictError) Error() string { return "request ID was already used for another target" }

func (h *Handler) recordCommand(id string, result CommandResult) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	point := data.NewPointString("lastCommand", "", string(encoded))
	point.Origin = result.Actor
	return client.SendNodePoint(h.nc, id, point, true)
}

func (h *Handler) waitForReadback(id string, result CommandResult) CommandResult {
	nodes, err := client.GetNodes(h.nc, "all", id, "", false)
	if err != nil || len(nodes) == 0 || nodes[0].Type != data.NodeTypeModbusIO {
		result.Status = "accepted"
		return result
	}
	buses, err := client.GetNodes(h.nc, "all", nodes[0].Parent, "", false)
	poll, timeout := 1000.0, 1000.0
	if err == nil && len(buses) > 0 {
		poll, _ = buses[0].Points.Value(data.PointTypePollPeriod, "")
		timeout, _ = buses[0].Points.Value(data.PointTypeTimeout, "")
	}
	wait := time.Duration(poll+timeout+500) * time.Millisecond
	if wait > 5*time.Second {
		wait = 5 * time.Second
	}
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		nodes, err := client.GetNodes(h.nc, "all", id, "", false)
		if err == nil && len(nodes) > 0 {
			point, ok := nodes[0].Points.Find(data.PointTypeValue, "")
			if ok && point.Time.After(result.CreatedAt) {
				value, err := point.ValueFloat()
				if err == nil && value == result.Target {
					result.Status = "confirmed"
					_ = h.recordCommand(id, result)
					return result
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	result.Status = "unknown"
	_ = h.recordCommand(id, result)
	return result
}
