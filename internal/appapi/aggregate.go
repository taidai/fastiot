package appapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"time"

	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/decision"
	"fastiot/internal/ems/alarm"
	"fastiot/internal/ems/historyquery"
	"fastiot/internal/ems/observation"
	"fastiot/internal/ems/signal"
)

const aggregatePoint = "easyemsAggregate"
const aggregateResultPoint = historyquery.AggregateResultPoint
const aggregateValuePoint = historyquery.AggregateValuePoint

type AggregateConfig struct {
	Group   string          `json:"group"`
	Op      string          `json:"op"`
	Members []string        `json:"members"`
	Units   string          `json:"units,omitempty"`
	Window  string          `json:"window,omitempty"`  // "" = 瞬时；"5m"/"15m"/"1h" = 闭合窗口
	Quality string          `json:"quality,omitempty"` // ignore | count | degrade（默认 count）
	Alarm   *AggregateAlarm `json:"alarm,omitempty"`
}

// AggregateAlarm configures a threshold alarm on the computed aggregate value.
type AggregateAlarm struct {
	Enabled  bool     `json:"enabled"`
	Min      *float64 `json:"min,omitempty"` // 低于下限告警
	Max      *float64 `json:"max,omitempty"` // 超过上限告警
	Severity string   `json:"severity,omitempty"`
}

// AggregateWindow explains a closed, UTC-aligned [Start, End) window and
// how many members contributed at least one valid observation.
type AggregateWindow struct {
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Members int       `json:"members"`
}

// AggregateAlarmState reflects the alarm engine's current view of the
// aggregate threshold, read back for the result panel.
type AggregateAlarmState struct {
	Active   bool   `json:"active"`
	Severity string `json:"severity"`
	Detail   string `json:"detail,omitempty"`
}

type AggregateMember struct {
	Reason          string     `json:"reason,omitempty"`
	TimeSource      string     `json:"time_source,omitempty"`
	Samples         int        `json:"samples"`
	RejectedSamples int        `json:"rejected_samples"`
	NodeID          string     `json:"node_id"`
	Name            string     `json:"name"`
	Value           *float64   `json:"value"`
	Quality         string     `json:"quality"`
	Time            *time.Time `json:"time,omitempty"`
}

type AggregateResult struct {
	DefinitionTime time.Time            `json:"definition_time,omitzero"`
	EvaluatedAt    time.Time            `json:"evaluated_at,omitzero"`
	TimeSource     string               `json:"time_source,omitempty"`
	LagSeconds     float64              `json:"lag_seconds"`
	Configured     bool                 `json:"configured"`
	Config         AggregateConfig      `json:"config"`
	Value          *float64             `json:"value"`
	Quality        string               `json:"quality"`
	Partial        bool                 `json:"partial"`
	Valid          int                  `json:"valid"`
	Total          int                  `json:"total"`
	Time           *time.Time           `json:"time,omitempty"`
	Members        []AggregateMember    `json:"members"`
	Window         *AggregateWindow     `json:"window,omitempty"`
	Alarm          *AggregateAlarmState `json:"alarm,omitempty"`
}

var aggregateOps = map[string]bool{"sum": true, "avg": true, "min": true, "max": true, "count": true}

func (h *Handler) aggregate(w http.ResponseWriter, r *http.Request, identity Identity) {
	if r.Method == http.MethodPut {
		var config AggregateConfig
		if !decodeJSON(w, r, &config) {
			return
		}
		if err := h.validateAggregate(config, identity); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		encoded, _ := json.Marshal(config)
		point := data.NewPointString(aggregatePoint, "", string(encoded))
		point.Origin = identity.UserID
		h.aggregateMu.Lock()
		err := client.SendNodePoint(h.nc, config.Group, point, true)
		h.aggregateMu.Unlock()
		if err != nil {
			writeError(w, http.StatusBadRequest, "save_failed")
			return
		}
		result := h.aggregateSnapshot(config)
		if !h.aggregateVisible(identity, result) {
			writeError(w, http.StatusForbidden, "no_access")
			return
		}
		writeJSON(w, result)
		return
	}
	group := r.URL.Query().Get("group")
	if group == "" {
		writeError(w, http.StatusBadRequest, "missing_group")
		return
	}
	config, ok := h.loadAggregate(group)
	if !ok {
		writeJSON(w, AggregateResult{Configured: false, Config: AggregateConfig{Group: group}, Quality: "unconfigured", Members: []AggregateMember{}})
		return
	}
	result := h.aggregateSnapshot(config)
	if !h.aggregateVisible(identity, result) {
		writeError(w, http.StatusForbidden, "no_access")
		return
	}
	writeJSON(w, result)
}

func (h *Handler) aggregateVisible(identity Identity, result AggregateResult) bool {
	for _, id := range result.Config.Members {
		if !h.allows(identity, id) {
			return false
		}
	}
	for _, member := range result.Members {
		if !h.allows(identity, member.NodeID) {
			return false
		}
	}
	return true
}

func (h *Handler) aggregateSnapshot(config AggregateConfig) AggregateResult {
	nodes, err := client.GetNodes(h.nc, "all", config.Group, "", false)
	if err == nil && len(nodes) > 0 {
		return aggregateNodeSnapshot(nodes[0], config)
	}
	return aggregateNodeSnapshot(data.NodeEdge{}, config)
}

func aggregateNodeSnapshot(node data.NodeEdge, config AggregateConfig) AggregateResult {
	definition, _ := node.Points.Find(aggregatePoint, "")
	if point, ok := node.Points.Find(aggregateResultPoint, ""); ok && point.Tombstone%2 == 0 {
		var result AggregateResult
		if json.Unmarshal([]byte(point.Txt()), &result) == nil && result.Config.Group == config.Group && result.DefinitionTime.Equal(definition.Time) && !result.EvaluatedAt.IsZero() && (result.Value == nil || result.Time != nil) {
			if result.Window != nil {
				result.LagSeconds = max(0, time.Since(result.Window.End).Seconds())
			}
			return result
		}
	}
	return AggregateResult{Configured: true, Config: config, Quality: "pending", Total: len(config.Members), Members: []AggregateMember{}}
}

// publicAggregateNodes is the shared tree projection for snapshots, WebSockets
// and other scoped readers. Raw storage clocks never masquerade as observation
// time, and nested configuration/member evidence cannot bypass member scope.
func (h *Handler) publicAggregateNodes(identity Identity, nodes []data.NodeEdge) []data.NodeEdge {
	out := append([]data.NodeEdge(nil), nodes...)
	for i, node := range out {
		found := false
		for _, point := range node.Points {
			if point.Type == aggregatePoint || point.Type == aggregateValuePoint || point.Type == aggregateResultPoint {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		out[i].Points = nil
		for _, point := range node.Points {
			if point.Type != aggregatePoint && point.Type != aggregateValuePoint && point.Type != aggregateResultPoint {
				out[i].Points = append(out[i].Points, point)
			}
		}
		definition, ok := node.Points.Find(aggregatePoint, "")
		var config AggregateConfig
		if !ok || definition.Tombstone%2 != 0 || json.Unmarshal([]byte(definition.Txt()), &config) != nil {
			continue
		}
		config.Group = node.ID
		result := aggregateNodeSnapshot(node, config)
		if !h.aggregateVisible(identity, result) {
			continue
		}
		encoded, _ := json.Marshal(config)
		definition.PutString(string(encoded))
		encoded, _ = json.Marshal(result)
		evidence := data.NewPointString(aggregateResultPoint, "0", string(encoded))
		evidence.Time = result.EvaluatedAt
		out[i].Points = append(out[i].Points, definition, evidence)
		if result.Value != nil && result.Time != nil {
			scalar := data.NewPointFloat(aggregateValuePoint, "0", *result.Value)
			scalar.Time = *result.Time
			out[i].Points = append(out[i].Points, scalar)
		}
	}
	return out
}

// RunAggregates owns sampling, independently of HTTP readers. Like calculations,
// it recovers definitions from nodes and releases its subscription on shutdown.
func (h *Handler) RunAggregates(ctx context.Context) error {
	wake := make(chan struct{}, 1)
	stop, err := client.SubscribePoints(h.nc, "*", func(points []data.Point) {
		for _, point := range points {
			if point.Type == aggregatePoint {
				select {
				case wake <- struct{}{}:
				default:
				}
				return
			}
		}
	})
	if err != nil {
		return err
	}
	defer stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if h.ready.Load() {
			if err := h.sampleAggregates(ctx); err != nil && ctx.Err() == nil {
				log.Printf("aggregate sampling: %v", err)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		case <-wake:
		}
	}
}

func (h *Handler) sampleAggregates(ctx context.Context) error {
	h.aggregateMu.Lock()
	defer h.aggregateMu.Unlock()
	nodes, err := client.GetNodes(h.nc, "all", "all", data.NodeTypeGroup, false)
	if err != nil {
		return err
	}
	roots, err := client.GetNodes(h.nc, "root", "all", "", false)
	if err != nil {
		return err
	}
	nodes = append(nodes, roots...)
	seen := map[string]bool{}
	for _, node := range nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if seen[node.ID] || (node.Type != data.NodeTypeGroup && node.Parent != "root") {
			continue
		}
		seen[node.ID] = true
		definition, ok := node.Points.Find(aggregatePoint, "")
		if !ok || definition.Tombstone%2 != 0 {
			continue
		}
		var config AggregateConfig
		if json.Unmarshal([]byte(definition.Txt()), &config) != nil || !aggregateOps[config.Op] {
			continue
		}
		config.Group = node.ID
		at := time.Now()
		window, err := parseAggregateWindow(config.Window)
		if err != nil {
			continue
		}
		var end time.Time
		previous := h.aggregateSnapshot(config)
		if window > 0 {
			end = at.UTC().Truncate(window)
			if previous.Window != nil && !end.After(previous.Window.End) {
				continue
			}
		}
		var result AggregateResult
		if window > 0 && end.Add(-window).Before(definition.Time) {
			// Never label a partially configured interval as a complete window.
			if previous.DefinitionTime.Equal(definition.Time) {
				continue
			}
			result = AggregateResult{Configured: true, Config: config, Quality: "pending", Total: len(config.Members), Members: []AggregateMember{}}
		} else {
			result, err = h.computeAggregate(config, at, end)
			if err != nil {
				return err
			}
		}
		result.DefinitionTime = definition.Time
		result.EvaluatedAt = at
		result.Alarm = h.applyAggregateAlarm(result)
		if err := h.persistAggregateValue(result); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) validateAggregate(config AggregateConfig, identity Identity) error {
	if config.Group == "" {
		return errText("invalid_group")
	}
	if !aggregateOps[config.Op] {
		return errText("invalid_aggregate_op")
	}
	if _, err := parseAggregateWindow(config.Window); err != nil {
		return errText("invalid_aggregate_window")
	}
	if config.Quality != "" && config.Quality != qualityPolicyIgnore && config.Quality != qualityPolicyCount && config.Quality != qualityPolicyDegrade {
		return errText("invalid_aggregate_quality")
	}
	if err := validateAggregateAlarm(config.Alarm); err != nil {
		return errText("invalid_aggregate_alarm")
	}
	if len(config.Members) == 0 || len(config.Members) > 64 {
		return errText("invalid_members")
	}
	nodes, err := client.GetNodes(h.nc, "all", config.Group, "", false)
	if err != nil || len(nodes) == 0 {
		return errText("invalid_group")
	}
	isRoot := false
	for _, anchor := range identity.Anchors {
		if anchor == config.Group {
			isRoot = true
			break
		}
	}
	if nodes[0].Type != data.NodeTypeGroup && !isRoot {
		return errText("invalid_group")
	}
	seen := map[string]bool{}
	for _, member := range config.Members {
		if member == "" || seen[member] {
			return errText("invalid_members")
		}
		seen[member] = true
		if err := decision.ValidateTarget(h.nc, config.Group, decision.Target{NodeID: member, PointType: data.PointTypeValue}, false, nil); err != nil {
			return errText("invalid_members")
		}
	}
	return nil
}

func (h *Handler) loadAggregate(group string) (AggregateConfig, bool) {
	nodes, err := client.GetNodes(h.nc, "all", group, "", false)
	if err != nil || len(nodes) == 0 {
		return AggregateConfig{}, false
	}
	point, ok := nodes[0].Points.Find(aggregatePoint, "")
	if !ok || point.Tombstone%2 != 0 {
		return AggregateConfig{}, false
	}
	var config AggregateConfig
	if json.Unmarshal([]byte(point.Txt()), &config) != nil {
		return AggregateConfig{}, false
	}
	config.Group = group
	return config, true
}

func (h *Handler) computeAggregate(config AggregateConfig, now, windowEnd time.Time) (AggregateResult, error) {
	window, err := parseAggregateWindow(config.Window)
	if err != nil {
		return AggregateResult{}, err
	}
	policy := normalizeAggregateQuality(config.Quality)
	result := AggregateResult{
		Configured: true,
		Config:     config,
		Total:      len(config.Members),
		Members:    make([]AggregateMember, 0, len(config.Members)),
		TimeSource: "member_latest",
	}
	var windowStart time.Time
	if window > 0 {
		windowStart = windowEnd.Add(-window)
		result.Time = &windowEnd
		result.TimeSource = "window_end"
		result.LagSeconds = now.Sub(windowEnd).Seconds()
		result.Window = &AggregateWindow{Start: windowStart, End: windowEnd}
	}
	values := make([]float64, 0, len(config.Members))
	var latest time.Time
	bad := 0
	for _, id := range config.Members {
		member := h.readAggregateMember(config.Group, id, now, windowStart, windowEnd)
		if member.Value != nil {
			values = append(values, *member.Value)
			if member.Time != nil && member.Time.After(latest) {
				latest = *member.Time
			}
		}
		if member.Quality != "good" {
			bad++
		}
		result.Members = append(result.Members, member)
	}
	sort.Slice(result.Members, func(i, j int) bool { return result.Members[i].Name < result.Members[j].Name })

	good := len(values)
	result.Valid = good
	result.Quality, result.Partial = applyAggregateQuality(policy, good, bad)

	if result.Window != nil {
		result.Window.Members = good
	}

	if good == 0 {
		return result, nil
	}
	if window == 0 && !latest.IsZero() {
		result.Time = &latest
	}
	value := aggregateValues(config.Op, values)
	if !finite(value) {
		result.Quality = "invalid"
		return result, nil
	}
	result.Value = &value
	return result, nil
}

func (h *Handler) readAggregateMember(group, id string, now, start, end time.Time) AggregateMember {
	member := AggregateMember{NodeID: id, Name: id, Quality: "missing"}
	if !h.users.IsUnder(id, group) {
		member.Reason = "outside_scope"
		return member
	}
	nodes, err := client.GetNodes(h.nc, "all", id, "", false)
	if err != nil || len(nodes) == 0 {
		return member
	}
	node := nodes[0]
	if name, ok := node.Points.Text("description", ""); ok && name != "" {
		member.Name = name
	}
	if disabled, _ := node.Points.Value("disabled", ""); disabled != 0 {
		member.Quality, member.Reason = "invalid", "disabled"
		return member
	}
	var samples []data.Point
	if node.Type == data.NodeTypeVariable {
		member.TimeSource = "node_value"
		if end.IsZero() {
			point, ok := node.Points.Find("value", "")
			if !ok || point.Tombstone%2 != 0 {
				return member
			}
			value, err := point.ValueFloat()
			if err != nil {
				member.Quality = "invalid"
				return member
			}
			verdict := observation.Evaluate(observation.Input{Value: signal.Num(value), Quality: signal.Good, SourceTime: point.Time, MaxAge: observation.DefaultMaxAge}, now)
			member.Quality, member.Reason = verdict.Quality.String(), verdict.Reason
			if !point.Time.IsZero() {
				at := point.Time
				member.Time = &at
			}
			if verdict.State == observation.Usable {
				member.Value = &value
				member.Samples = 1
			}
			return member
		}
		results, _, err := h.historyQuery().Execute(historyquery.Request{NodeIDs: []string{id}, Type: data.PointTypeValue, Start: start, End: end.Add(-time.Nanosecond), Export: true})
		if err != nil || len(results) != 1 {
			member.Quality, member.Reason = "invalid", "history_unavailable_or_limit_exceeded"
			return member
		}
		samples = results[0].Points
	} else {
		// A cached physical node value has no proof of a successful acquisition.
		// Use the existing runtime's quality and configuration-stamped observations.
		member.Quality, member.Reason = "uninitialized", "unproven_observation"
		if node.Type != data.NodeTypeModbusIO || h.ems == nil || h.ems.Service == nil || h.ems.Buses == nil {
			return member
		}
		buses, err := h.ems.Buses()
		if err != nil {
			return member
		}
		deviceID, stamp := "", ""
		for _, bus := range buses {
			for _, device := range bus.Devices {
				for _, point := range device.Points {
					if point.ID == id {
						if deviceID != "" {
							member.Quality, member.Reason = "invalid", "ambiguous_source"
							return member
						}
						deviceID, stamp = device.ID, point.ConfigStamp
					}
				}
			}
		}
		if deviceID == "" || stamp == "" {
			return member
		}
		if end.IsZero() {
			value, ok := h.ems.Service.State.Get(now, deviceID, id, h.ems.Service.MaxAge)
			if !ok || value.ConfigStamp != stamp {
				return member
			}
			verdict := observation.Evaluate(observation.Input{Value: value.Value, Quality: value.Quality, Reason: value.Reason, SourceTime: value.SourceTime, ReceivedTime: value.ReceivedTime, MaxAge: observation.DefaultMaxAge}, now)
			member.Quality, member.Reason, member.TimeSource = verdict.Quality.String(), verdict.Reason, value.TimeSource
			if !value.SourceTime.IsZero() {
				at := value.SourceTime
				member.Time = &at
			}
			if verdict.State == observation.Usable && value.Value.Kind == signal.Number {
				number := value.Value.Num
				member.Value = &number
				member.Samples = 1
			} else if verdict.State == observation.Usable {
				member.Quality, member.Reason = "invalid", "numeric_observation_required"
			}
			return member
		}
		results, _, err := h.historyQuery().Execute(historyquery.Request{NodeIDs: []string{id}, Type: data.PointTypeValue, Start: start, End: end.Add(-time.Nanosecond), Export: true})
		if err != nil || len(results) != 1 || results[0].Source != historyquery.SourceRuntime {
			member.Reason = "history_unavailable"
			return member
		}
		for _, record := range results[0].Records {
			if record.Quality != "good" || record.Value == nil || record.ConfigStamp != stamp || record.ReceivedTime.IsZero() || record.ReceivedTime.After(now) {
				member.RejectedSamples++
				continue
			}
			point := data.NewPointFloat("value", "", *record.Value)
			point.Time = record.SourceTime
			samples = append(samples, point)
			member.TimeSource = record.TimeSource
		}
	}
	valid := make([]data.Point, 0, len(samples))
	for _, point := range samples {
		value, err := point.ValueFloat()
		if point.Tombstone%2 != 0 || point.Time.IsZero() || point.Time.Before(start) || !point.Time.Before(end) || err != nil || !finite(value) {
			member.RejectedSamples++
			continue
		}
		valid = append(valid, point)
	}
	member.Samples = len(valid)
	member.Quality, member.Reason = "stale", "no_valid_window_observations"
	if value, at, ok := windowMemberMean(valid); ok {
		if !finite(value) {
			member.Quality, member.Reason = "invalid", "aggregate_overflow"
			return member
		}
		member.Value, member.Time = &value, &at
		member.Quality, member.Reason = "good", ""
		if member.RejectedSamples > 0 {
			member.Quality, member.Reason = "partial", "rejected_window_observations"
		}
	}
	return member
}

// persistAggregateValue projects a numeric point and the complete evidence onto
// the same scope node. The point timestamp is evaluation time, never fabricated
// source freshness; consumers read the original observation time in the result.
func (h *Handler) persistAggregateValue(result AggregateResult) error {
	nodes, err := client.GetNodes(h.nc, "all", result.Config.Group, "", false)
	if err != nil {
		return err
	}
	if len(nodes) == 0 {
		return errText("aggregate_target_unavailable")
	}
	previous, _ := nodes[0].Points.Find(aggregateValuePoint, "")
	point := data.NewPointString(aggregateValuePoint, "", "")
	point.Tombstone = previous.Tombstone
	if result.Value != nil {
		point.PutFloat(*result.Value)
		if point.Tombstone%2 != 0 {
			point.Tombstone++
		}
	} else if point.Tombstone%2 == 0 {
		point.Tombstone++
	}
	point.Time = result.EvaluatedAt
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	evidence := data.NewPointString(aggregateResultPoint, "0", string(encoded))
	evidence.Time = result.EvaluatedAt
	return client.SendNodePoints(h.nc, result.Config.Group, data.Points{point, evidence}, true)
}

// applyAggregateAlarm raises or clears the aggregate threshold alarm using the
// existing alarm engine (read-only calls; the engine implementation is not
// modified here).
func (h *Handler) applyAggregateAlarm(result AggregateResult) *AggregateAlarmState {
	group := result.Config.Group
	cfg := result.Config.Alarm
	if h.ems == nil || h.ems.Service == nil || h.ems.Service.Alarms == nil {
		return nil
	}
	engine := h.ems.Service.Alarms
	key := "aggregate:" + group
	now := time.Now()
	severity := alarm.Severity("warning")
	if cfg != nil && cfg.Severity != "" {
		severity = alarm.Severity(cfg.Severity)
	}
	if cfg == nil || !cfg.Enabled || result.Value == nil || result.Quality == "offline" {
		clearAggregateAlarm(engine, key, now)
		return nil
	}
	breach, reason := evaluateAggregateAlarm(*result.Value, cfg)
	if !breach {
		clearAggregateAlarm(engine, key, now)
		return nil
	}
	detail := fmt.Sprintf("聚合值 %g · %s · 有效 %d/%d", *result.Value, reason, result.Valid, result.Total)
	raised, _ := engine.Raise(now, key, severity, "虚拟聚合超限", detail, group, "")
	return &AggregateAlarmState{Active: raised.Active, Severity: string(severity), Detail: detail}
}

func clearAggregateAlarm(engine *alarm.Engine, key string, now time.Time) {
	if a, ok := engine.Get(key); ok && a.Active {
		_, _ = engine.Clear(now, key)
	}
}

type errText string

func (e errText) Error() string { return string(e) }
