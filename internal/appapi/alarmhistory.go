package appapi

import (
	"bufio"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"fastiot/internal/ems/alarm"
	"fastiot/internal/emsapi"
)

const alarmEventsFile = "alarm-events.jsonl"

// Limit redundant observation checkpoints, not real lifecycle transitions.
const alarmCheckpointCompactAfter = 1024

type alarmEvent struct {
	Time              time.Time            `json:"time"`
	Key               string               `json:"key"`
	OccurrenceID      string               `json:"occurrence_id,omitempty"`
	Revision          uint64               `json:"revision,omitempty"`
	Count             int                  `json:"count,omitempty"`
	GroupID           string               `json:"group_id,omitempty"`
	MaintenanceUntil  time.Time            `json:"maintenance_until,omitzero"`
	Transition        string               `json:"transition"`
	Severity          string               `json:"severity"`
	Title             string               `json:"title"`
	Detail            string               `json:"detail,omitempty"`
	DeviceID          string               `json:"device_id,omitempty"`
	PointID           string               `json:"point_id,omitempty"`
	RootCause         string               `json:"root_cause,omitempty"`
	State             string               `json:"state"`
	InputState        string               `json:"input_state,omitempty"`
	InputReason       string               `json:"input_reason,omitempty"`
	Input             *alarm.InputEvidence `json:"input,omitempty"`
	CreatedAt         time.Time            `json:"created_at,omitzero"`
	LastRaisedAt      time.Time            `json:"last_raised_at,omitzero"`
	AckedAt           time.Time            `json:"acked_at,omitzero"`
	ResolvedAt        time.Time            `json:"resolved_at,omitzero"`
	ClosedAt          time.Time            `json:"closed_at,omitzero"`
	AckedBy           string               `json:"acked_by,omitempty"`
	AckNote           string               `json:"ack_note,omitempty"`
	MaintainedBy      string               `json:"maintained_by,omitempty"`
	MaintenanceReason string               `json:"maintenance_reason,omitempty"`
}

type AlarmDayStats struct {
	Date      string `json:"date"`
	Raised    int    `json:"raised"`
	Unhandled int    `json:"unhandled"`
	Handled   int    `json:"handled"`
}

type AlarmRanking struct {
	DeviceID string `json:"device_id"`
	Name     string `json:"name"`
	Count    int    `json:"count"`
}

type AlarmStats struct {
	Today            AlarmDayStats   `json:"today"`
	Yesterday        AlarmDayStats   `json:"yesterday"`
	Trend            []AlarmDayStats `json:"trend"`
	Ranking          []AlarmRanking  `json:"ranking"`
	Since            string          `json:"since,omitempty"`
	LegacyIncomplete bool            `json:"legacy_incomplete,omitempty"`
}

func (h *Handler) restoreAlarmOccurrences(engine *alarm.Engine) {
	events, err := h.readAlarmEvents()
	if err != nil {
		h.alarmEventsMu.Lock()
		h.alarmEventsError = err
		h.alarmEventsMu.Unlock()
		return
	}
	latest := map[string]alarmEvent{}
	checkpointIDs := map[string]bool{}
	checkpointCount := 0
	for _, event := range events {
		if event.Transition == "checkpoint" {
			checkpointCount++
			checkpointIDs[event.OccurrenceID] = true
		}
		if event.OccurrenceID != "" && event.Revision > latest[event.OccurrenceID].Revision {
			latest[event.OccurrenceID] = event
		}
	}
	h.alarmCheckpointWrites = checkpointCount - len(checkpointIDs)
	records := make([]alarm.Alarm, 0, len(latest))
	for _, e := range latest {
		records = append(records, alarm.Alarm{
			ID: e.OccurrenceID, Key: e.Key, Revision: e.Revision, Count: e.Count, GroupID: e.GroupID,
			Severity: alarm.Severity(e.Severity), Title: e.Title, Detail: e.Detail,
			DeviceID: e.DeviceID, PointID: e.PointID, RootCause: e.RootCause,
			State: alarm.State(e.State), Active: e.State == string(alarm.Active) || e.State == string(alarm.Acknowledged),
			InputState: e.InputState, InputReason: e.InputReason, Input: e.Input,
			CreatedAt: e.CreatedAt, UpdatedAt: e.Time, LastRaisedAt: e.LastRaisedAt,
			AckedAt: e.AckedAt, AckedBy: e.AckedBy, AckNote: e.AckNote, ResolvedAt: e.ResolvedAt, ClosedAt: e.ClosedAt,
			MaintenanceUntil: e.MaintenanceUntil, MaintainedBy: e.MaintainedBy, MaintenanceReason: e.MaintenanceReason,
		})
	}
	sort.Slice(records, func(i, j int) bool { return records[i].CreatedAt.Before(records[j].CreatedAt) })
	engine.Restore(records)
}

func (h *Handler) recordAlarmChange(value alarm.Alarm, transition string) {
	if h.dataDir == "" {
		return
	}
	event := alarmEvent{
		Time: value.UpdatedAt, Key: value.Key, OccurrenceID: value.ID, Revision: value.Revision, Transition: transition,
		Count: value.Count, GroupID: value.GroupID, MaintenanceUntil: value.MaintenanceUntil,
		Severity: string(value.Severity), Title: value.Title, Detail: value.Detail,
		DeviceID: value.DeviceID, PointID: value.PointID, RootCause: value.RootCause, State: string(value.State), InputState: value.InputState, InputReason: value.InputReason, Input: value.Input,
		CreatedAt: value.CreatedAt, LastRaisedAt: value.LastRaisedAt, AckedAt: value.AckedAt, ResolvedAt: value.ResolvedAt, ClosedAt: value.ClosedAt,
		AckedBy: value.AckedBy, AckNote: value.AckNote, MaintainedBy: value.MaintainedBy, MaintenanceReason: value.MaintenanceReason,
	}
	if event.Time.IsZero() {
		event.Time = time.Now()
	}
	line, err := json.Marshal(event)
	h.alarmEventsMu.Lock()
	defer h.alarmEventsMu.Unlock()
	if err != nil {
		h.alarmEventsError = err
		return
	}
	if err := os.MkdirAll(h.dataDir, 0700); err != nil {
		h.alarmEventsError = err
		return
	}
	file, err := os.OpenFile(filepath.Join(h.dataDir, alarmEventsFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		h.alarmEventsError = err
		return
	}
	_, writeErr := file.Write(append(line, '\n'))
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		h.alarmEventsError = writeErr
	} else if closeErr != nil {
		h.alarmEventsError = closeErr
	}
	if transition == "checkpoint" && h.alarmEventsError == nil {
		h.alarmCheckpointWrites++
		if h.alarmCheckpointWrites >= alarmCheckpointCompactAfter {
			h.alarmEventsError = h.compactAlarmCheckpoints()
			if h.alarmEventsError == nil {
				h.alarmCheckpointWrites = 0
			}
		}
	}
}

// Called under alarmEventsMu. Replacement is prepared and synced separately;
// a failed write/close/rename leaves the old, already-synced journal intact.
func (h *Handler) compactAlarmCheckpoints() error {
	events, err := h.readAlarmEventsUnlocked()
	if err != nil {
		return err
	}
	latest := map[string]int{}
	for i, event := range events {
		if event.Transition != "checkpoint" {
			continue
		}
		previous, exists := latest[event.OccurrenceID]
		if !exists || event.Revision > events[previous].Revision {
			latest[event.OccurrenceID] = i
		}
	}
	path := filepath.Join(h.dataDir, alarmEventsFile)
	temporary := path + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	encoder := json.NewEncoder(file)
	for i, event := range events {
		if event.Transition == "checkpoint" && latest[event.OccurrenceID] != i {
			continue
		}
		if err = encoder.Encode(event); err != nil {
			file.Close()
			return err
		}
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func (h *Handler) alarmEvidenceError() error {
	h.alarmEventsMu.Lock()
	defer h.alarmEventsMu.Unlock()
	return h.alarmEventsError
}

func (h *Handler) alarmEventVisibility(identity Identity) (func(alarmEvent) bool, error) {
	if h.ems == nil {
		return nil, errors.New("ems_unavailable")
	}
	visible, err := h.ems.AlarmVisibility(emsapi.Access{AllowNode: func(id string) bool { return h.allows(identity, id) }})
	if err != nil {
		return nil, err
	}
	return func(event alarmEvent) bool {
		return visible(alarm.Alarm{DeviceID: event.DeviceID, PointID: event.PointID})
	}, nil
}

func (h *Handler) alarmOccurrenceEvents(w http.ResponseWriter, r *http.Request, identity Identity) {
	id := r.URL.Query().Get("occurrence_id")
	if id == "" {
		writeError(w, 400, "occurrence_id_required")
		return
	}
	visible, err := h.alarmEventVisibility(identity)
	if err != nil {
		writeError(w, 503, "alarm_objects_unavailable")
		return
	}
	all, err := h.readAlarmEvents()
	if err != nil {
		writeError(w, 503, "alarm_history_unavailable")
		return
	}
	events := []alarmEvent{}
	for _, event := range all {
		if event.Transition == "checkpoint" || event.OccurrenceID != id {
			continue
		}
		if !visible(event) {
			writeError(w, 404, "alarm_not_found")
			return
		}
		events = append(events, event)
	}
	if len(events) == 0 {
		writeError(w, 404, "alarm_not_found")
		return
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].Revision < events[j].Revision })
	writeJSON(w, map[string]any{"occurrence_id": id, "events": events})
}

func (h *Handler) alarmStats(w http.ResponseWriter, r *http.Request, identity Identity) {
	days := 7
	if value, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && value >= 1 && value <= 30 {
		days = value
	}
	stats, err := h.alarmStatsData(identity, days)
	if err != nil {
		writeError(w, 503, "alarm_history_unavailable")
		return
	}
	writeJSON(w, stats)
}

func (h *Handler) alarmStatsData(identity Identity, days int) (AlarmStats, error) {
	events, err := h.readAlarmEvents()
	if err != nil {
		return AlarmStats{}, err
	}
	now := time.Now()
	location := now.Location()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location).AddDate(0, 0, -29)
	byDate := map[string][]alarmEvent{}
	ranking := map[string]int{}
	since := ""
	legacyIncomplete := false
	visible, err := h.alarmEventVisibility(identity)
	if err != nil {
		return AlarmStats{}, err
	}
	for _, event := range events {
		if event.Transition == "checkpoint" || !visible(event) {
			continue
		}
		at := event.Time.In(location)
		if at.Before(start) {
			continue
		}
		if event.OccurrenceID == "" {
			legacyIncomplete = true
		}
		date := at.Format("2006-01-02")
		byDate[date] = append(byDate[date], event)
		if since == "" || at.Format("2006-01-02") < since {
			since = date
		}
		if event.Transition == "raised" && event.DeviceID != "" {
			ranking[event.DeviceID]++
		}
	}
	day := func(date time.Time) AlarmDayStats {
		key := date.Format("2006-01-02")
		agg := AlarmDayStats{Date: key}
		raised := map[string]bool{}
		state := map[string]alarmEvent{}
		for _, event := range byDate[key] {
			id := event.OccurrenceID
			if id == "" {
				id = "legacy:" + event.Key
			}
			if event.Transition == "raised" {
				raised[id] = true
			}
			previous, exists := state[id]
			if !exists || event.Revision > previous.Revision || (event.Revision == previous.Revision && event.Time.After(previous.Time)) {
				state[id] = event
			}
		}
		agg.Raised = len(raised)
		for key := range raised {
			if state[key].State == string(alarm.Active) {
				agg.Unhandled++
			} else {
				agg.Handled++
			}
		}
		return agg
	}
	trend := make([]AlarmDayStats, 0, days)
	for i := days - 1; i >= 0; i-- {
		trend = append(trend, day(now.AddDate(0, 0, -i)))
	}
	names := map[string]string{}
	if nodes, err := h.visibleNodes(identity); err == nil {
		for _, node := range nodes {
			names[node.ID] = node.Desc()
		}
	}
	ranked := make([]AlarmRanking, 0, len(ranking))
	for id, count := range ranking {
		name := names[id]
		if name == "" {
			name = id
		}
		ranked = append(ranked, AlarmRanking{DeviceID: id, Name: name, Count: count})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Count != ranked[j].Count {
			return ranked[i].Count > ranked[j].Count
		}
		return ranked[i].Name < ranked[j].Name
	})
	if len(ranked) > 10 {
		ranked = ranked[:10]
	}
	return AlarmStats{
		Today: day(now), Yesterday: day(now.AddDate(0, 0, -1)),
		Trend: trend, Ranking: ranked, Since: since, LegacyIncomplete: legacyIncomplete,
	}, nil
}

func (h *Handler) readAlarmEvents() ([]alarmEvent, error) {
	h.alarmEventsMu.Lock()
	defer h.alarmEventsMu.Unlock()
	if h.alarmEventsError != nil {
		return nil, h.alarmEventsError
	}
	return h.readAlarmEventsUnlocked()
}

func (h *Handler) readAlarmEventsUnlocked() ([]alarmEvent, error) {
	if h.dataDir == "" {
		return nil, nil
	}
	file, err := os.Open(filepath.Join(h.dataDir, alarmEventsFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	events := []alarmEvent{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var event alarmEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, scanner.Err()
}
