package appapi

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"fastiot/internal/ems/command"
	"fastiot/internal/ems/device"
)

const (
	commandLogFile = "commands.jsonl"
	commandLogKeep = 500 // display preload only, never a deduplication retention limit
)

type commandLogRecord struct {
	RequestID   string             `json:"request_id"`
	DeviceID    string             `json:"device_id"`
	CommandID   string             `json:"command_id,omitempty"`
	Actor       string             `json:"actor,omitempty"`
	State       string             `json:"state"`
	Reason      string             `json:"reason,omitempty"`
	StartedAt   time.Time          `json:"started_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
	DispatchID  string             `json:"dispatch_id,omitempty"`
	Source      string             `json:"source,omitempty"`
	Stage       string             `json:"stage,omitempty"`
	FallbackOf  string             `json:"fallback_of,omitempty"`
	Fingerprint string             `json:"fingerprint,omitempty"`
	External    bool               `json:"external,omitempty"`
	Deadline    time.Time          `json:"deadline,omitempty"`
	Observed    []command.Observed `json:"observed,omitempty"`
	Writes      []device.Write     `json:"writes,omitempty"`
	Readback    []device.Readback  `json:"readback,omitempty"`
}

func (r commandLogRecord) outcome() command.Outcome {
	o := command.Outcome{RequestID: r.RequestID, DeviceID: r.DeviceID, CommandID: r.CommandID, Actor: r.Actor, State: command.State(r.State), Reason: r.Reason, StartedAt: r.StartedAt, UpdatedAt: r.UpdatedAt, DispatchID: r.DispatchID, Source: r.Source, Stage: r.Stage, FallbackOf: r.FallbackOf, Fingerprint: r.Fingerprint, External: r.External, Deadline: r.Deadline, Observed: r.Observed, Writes: r.Writes, Readback: r.Readback, Restored: true}
	if o.State == command.Accepted || o.State == command.Executing {
		o.State = command.Unknown
		o.Reason = "进程重启，结果未知"
	}
	return o
}

// WireCommandHistory uses the same append-only journal for history and durable
// idempotency. The manager serializes lookup, fsynced intent, and dispatch with
// its own lock. A missing/corrupt/unwritable journal fails closed, never retries I/O.
func WireCommandHistory(dataDir string, manager *command.Manager) {
	if dataDir == "" || manager == nil {
		return
	}
	path := filepath.Join(dataDir, commandLogFile)
	initErr := os.MkdirAll(dataDir, 0700)
	if initErr == nil {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		initErr = err
		if err == nil {
			initErr = file.Sync()
			file.Close()
		}
	}
	records := map[string]commandLogRecord{}
	order := []string{}
	if initErr == nil {
		initErr = scanCommandLog(path, func(record commandLogRecord) {
			if _, exists := records[record.RequestID]; !exists {
				order = append(order, record.RequestID)
				if len(order) > commandLogKeep {
					delete(records, order[0])
					order = order[1:]
				}
			}
			records[record.RequestID] = record
		})
	}
	outcomes := make([]command.Outcome, 0, len(order))
	for _, id := range order {
		outcomes = append(outcomes, records[id].outcome())
	}
	manager.Preload(outcomes)
	manager.Lookup = func(id string) (command.Outcome, bool, error) {
		if initErr != nil {
			return command.Outcome{}, false, initErr
		}
		var found commandLogRecord
		// ponytail: linear disk lookup for cold IDs; index this same journal if
		// profiling shows large histories need it. No TTL can reauthorize old IDs.
		err := scanCommandLog(path, func(record commandLogRecord) {
			if record.RequestID == id {
				found = record
			}
		})
		return found.outcome(), found.RequestID != "", err
	}
	manager.Persist = func(o command.Outcome) error {
		if initErr != nil {
			return initErr
		}
		record := commandLogRecord{RequestID: o.RequestID, DeviceID: o.DeviceID, CommandID: o.CommandID, Actor: o.Actor, State: string(o.State), Reason: o.Reason, StartedAt: o.StartedAt, UpdatedAt: o.UpdatedAt, DispatchID: o.DispatchID, Source: o.Source, Stage: o.Stage, FallbackOf: o.FallbackOf, Fingerprint: o.Fingerprint, External: o.External, Deadline: o.Deadline, Observed: o.Observed, Writes: o.Writes, Readback: o.Readback}
		encoded, err := json.Marshal(record)
		if err != nil {
			return err
		}
		// Do not recreate a lost journal after startup: that would forget identities.
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		defer file.Close()
		if _, err = file.Write(append(encoded, '\n')); err != nil {
			return err
		}
		return file.Sync()
	}
}

func scanCommandLog(path string, visit func(commandLogRecord)) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var record commandLogRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return fmt.Errorf("invalid command journal: %w", err)
		}
		if record.RequestID == "" {
			return fmt.Errorf("command journal record has no identity")
		}
		visit(record)
	}
	return scanner.Err()
}
