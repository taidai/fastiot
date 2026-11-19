package appapi

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/ems/command"
	"fastiot/internal/ems/signal"
)

// CommandResult is the read-only lastCommand projection of a canonical
// command.Outcome. It is derived by projectCommandResult and written only by
// projectCommand; nothing may store or mutate it independently of the outcome
// it came from. Carrying reason, target, request id and time here preserves
// what the alarm and point views read while keeping one command fact.
type CommandResult struct {
	RequestID string    `json:"request_id"`
	Status    string    `json:"status"`
	Target    float64   `json:"target"`
	Actor     string    `json:"actor"`
	CreatedAt time.Time `json:"created_at"`
	Reason    string    `json:"reason,omitempty"`
}

// projectCommandResult derives the lastCommand view from the canonical outcome.
func projectCommandResult(outcome command.Outcome) CommandResult {
	result := CommandResult{
		RequestID: outcome.RequestID,
		Status:    string(outcome.State),
		Actor:     outcome.Actor,
		CreatedAt: outcome.StartedAt,
		Reason:    outcome.Reason,
	}
	if len(outcome.Writes) > 0 {
		result.Target = outcome.Writes[0].Value.Num
	}
	return result
}

// commandFinding is one command whose result is failed or unknown.
type commandFinding struct {
	Object string
	State  string
}

// commandManager returns the one manager that owns command outcomes: the
// runtime's when mounted, otherwise a handler-local manager so manual point
// writes in a runtime-less deployment still record one canonical outcome.
func (h *Handler) commandManager() *command.Manager {
	if h.ems != nil && h.ems.Service != nil {
		return h.ems.Service.Commands()
	}
	return h.localCommands
}

// commandFindings collects failed or unknown command outcomes for the nodes in
// scope. It reads the canonical outcomes only: a lastCommand point is a
// projection and carries no authority of its own.
func (h *Handler) commandFindings(nodes []data.NodeEdge) []commandFinding {
	findings := make([]commandFinding, 0)
	manager := h.commandManager()
	if manager == nil {
		return findings
	}
	names := make(map[string]string, len(nodes))
	for _, node := range nodes {
		names[node.ID] = node.Desc()
	}
	for _, outcome := range manager.List() {
		if outcome.State != command.Failed && outcome.State != command.Unknown {
			continue
		}
		object, ok := commandObject(outcome.DeviceID, names)
		if !ok {
			continue
		}
		findings = append(findings, commandFinding{Object: object, State: string(outcome.State)})
	}
	return findings
}

// commandObject names the node a command belongs to. Point writes use the node
// id; named and power writes use "<bus id>/<server id>" and are reported
// through the bus that owns the device.
func commandObject(deviceID string, names map[string]string) (string, bool) {
	describe := func(id string) string {
		if name, ok := names[id]; ok && name != "" {
			return name
		}
		return id
	}
	busID, server, named := strings.Cut(deviceID, "/")
	if !named {
		busID = deviceID
	}
	if _, ok := names[busID]; !ok {
		return "", false
	}
	object := describe(busID)
	if named {
		object += "/" + server
	}
	return object, true
}

// existingCommand reports the canonical outcome already recorded for a request
// id, including the conflict when the id was used for another target.
func (h *Handler) existingCommand(id, requestID string, target float64) (CommandResult, bool, error) {
	manager := h.commandManager()
	if manager == nil {
		return CommandResult{}, false, nil
	}
	outcome, ok := manager.Get(requestID)
	if !ok {
		return CommandResult{}, false, nil
	}
	if outcome.DeviceID != id || len(outcome.Writes) != 1 || outcome.Writes[0].Value.Num != target {
		return CommandResult{}, false, &commandConflictError{}
	}
	return projectCommandResult(outcome), true, nil
}

type commandConflictError struct{}

func (*commandConflictError) Error() string { return "request ID was already used for another target" }

// recordCommand records one manual point write as a canonical command outcome
// and writes its lastCommand projection. This is the only way a manual write
// enters command state.
func (h *Handler) recordCommand(id string, result CommandResult, reason string) error {
	manager := h.commandManager()
	if manager == nil {
		return fmt.Errorf("command runtime unavailable")
	}
	now := time.Now()
	outcome, err := manager.Track(now, command.Request{
		ID: result.RequestID, DeviceID: id, Point: id,
		Value: signal.Num(result.Target), Actor: result.Actor, Source: "manual",
	})
	if err != nil {
		return err
	}
	if outcome.State != command.State(result.Status) || outcome.Reason != reason {
		if updated, ok := manager.SetState(now, result.RequestID, command.State(result.Status), reason); ok {
			outcome = updated
		}
	}
	return h.projectCommand(id, outcome)
}

// projectCommand writes the read-only lastCommand view derived from outcome.
func (h *Handler) projectCommand(id string, outcome command.Outcome) error {
	if !outcome.External {
		return nil
	}
	encoded, err := json.Marshal(projectCommandResult(outcome))
	if err != nil {
		return err
	}
	point := data.NewPointString("lastCommand", "", string(encoded))
	point.Origin = outcome.Actor
	return client.SendNodePoint(h.nc, id, point, true)
}

func (h *Handler) waitForReadback(id string, result CommandResult) CommandResult {
	manager := h.commandManager()
	current := result
	if manager != nil {
		if outcome, ok := manager.Get(result.RequestID); ok {
			current = projectCommandResult(outcome)
		}
	}
	nodes, err := client.GetNodes(h.nc, "all", id, "", false)
	if err != nil || len(nodes) == 0 || nodes[0].Type != data.NodeTypeModbusIO {
		return current
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
					return h.advanceCommand(id, current, command.Confirmed, "")
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return h.advanceCommand(id, current, command.Unknown, "no readback before timeout")
}

// advanceCommand moves a tracked manual write to its terminal state and
// projects the result. The canonical outcome is the only state it changes.
func (h *Handler) advanceCommand(id string, current CommandResult, state command.State, reason string) CommandResult {
	manager := h.commandManager()
	if manager == nil {
		return current
	}
	outcome, ok := manager.SetState(time.Now(), current.RequestID, state, reason)
	if !ok {
		return current
	}
	_ = h.projectCommand(id, outcome)
	return projectCommandResult(outcome)
}
