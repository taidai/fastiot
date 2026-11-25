package appapi

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"

	"fastiot/internal/decision"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

// Each event has its own durable key. This is not a last-run projection and has
// no TTL: an old retry must never create a fresh set of physical commands.
const stateMachineRunPoint = "stateMachineRun"

type stateMachineRun struct {
	RequestID  string                          `json:"request_id"`
	Actor      string                          `json:"actor"`
	Revision   uint64                          `json:"revision"`
	Source     decision.StateMachineDefinition `json:"source"`
	Inputs     map[string]float64              `json:"inputs"`
	TextInputs map[string]string               `json:"text_inputs"`
	Execution  decision.StateMachineExecution  `json:"execution"`
	Finished   bool                            `json:"finished"`
}

func machineIdentity(parts ...string) string {
	encoded, _ := json.Marshal(parts)
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

func machineRuns(node data.NodeEdge) ([]stateMachineRun, error) {
	runs := []stateMachineRun{}
	for _, point := range node.Points {
		if point.Type != stateMachineRunPoint || point.Tombstone%2 != 0 {
			continue
		}
		var run stateMachineRun
		if json.Unmarshal([]byte(point.Txt()), &run) != nil || run.RequestID == "" || run.Execution.RequestID != run.RequestID || point.Key != machineIdentity(run.RequestID) {
			return nil, fmt.Errorf("invalid_state_machine_run")
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func (h *Handler) storeMachineRun(id string, run stateMachineRun) error {
	encoded, err := json.Marshal(run)
	if err != nil {
		return err
	}
	key := machineIdentity(run.RequestID)
	point := data.NewPointString(stateMachineRunPoint, key, string(encoded))
	point.Origin = run.Actor
	if err := client.SendNodePoint(h.nc, id, point, true); err != nil {
		return err
	}
	nodes, err := client.GetNodes(h.nc, "all", id, "", false)
	if err != nil || len(nodes) == 0 {
		return fmt.Errorf("state_machine_run_unconfirmed")
	}
	text, ok := nodes[0].Points.Text(stateMachineRunPoint, key)
	if !ok || text != string(encoded) {
		return fmt.Errorf("state_machine_run_unconfirmed")
	}
	return nil
}

// A claimed event is never dispatched a second time, even if the process died
// between durable intent and the first write. Missing outcome is unknown, not
// permission to replay. Only the command manager owns command confirmation.
func (h *Handler) replayMachineRun(w http.ResponseWriter, node data.NodeEdge, document decision.StateMachineDocument, identity Identity, input stateMachineRequest) bool {
	runs, err := machineRuns(node)
	if err != nil {
		writeError(w, 503, err.Error())
		return true
	}
	pending := false
	for _, run := range runs {
		if run.RequestID != input.RequestID {
			pending = pending || !run.Finished
			continue
		}
		if run.Actor != identity.UserID || run.Revision != input.Revision || run.Execution.Event != input.Event {
			writeError(w, 409, "request_id_conflict")
			return true
		}
		allowed := true
		run.Source.MapReferences(func(id string) string { allowed = allowed && h.allows(identity, id); return id })
		if !allowed {
			writeError(w, 403, "target_out_of_scope")
			return true
		}
		if !run.Finished {
			if document.LastExecution != nil && document.LastExecution.RequestID == run.RequestID {
				run.Execution = *document.LastExecution
			} else {
				run.Execution.Status = "unknown"
				run.Execution.To = run.Execution.From
				run.Execution.Reason = "interrupted execution; commands are not replayed"
				if manager := h.commandManager(); manager != nil {
					for i, ref := range run.Execution.Commands {
						outcome, found, err := manager.Find(ref.RequestID)
						if err != nil {
							writeError(w, 503, "command_state_unavailable")
							return true
						}
						if found {
							run.Execution.Commands[i].Status, run.Execution.Commands[i].Reason = string(outcome.State), outcome.Reason
						}
					}
				}
			}
			run.Finished = true
			if h.storeMachineRun(node.ID, run) != nil {
				writeError(w, 503, "state_machine_run_unconfirmed")
				return true
			}
		}
		response := h.stateMachineResult(node, document, identity)
		response.Execution = &run.Execution
		writeJSON(w, response)
		return true
	}
	if pending {
		writeError(w, 409, "previous_execution_unresolved")
		return true
	}
	return false
}
