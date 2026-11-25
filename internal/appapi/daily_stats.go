package appapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"fastiot/internal/dailystats"
	"fastiot/internal/ems/historyquery"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

const dailyStatsPoint = "dailyStats"

func readDailyConfig(node data.NodeEdge) (dailystats.Config, error) {
	var config dailystats.Config
	point, ok := node.Points.Find(dailyStatsPoint, "")
	if !ok || point.Tombstone%2 != 0 {
		return config, nil
	}
	err := json.Unmarshal([]byte(point.Txt()), &config)
	return config, err
}

// Reads recheck references: a moved or re-bound point never silently becomes an
// authorized statistic just because its declaration used to be valid.
func (h *Handler) dailyReferencesAllowed(identity Identity, layer string, c dailystats.Config) bool {
	for _, id := range c.PointIDs() {
		if id == "" || !h.screenReferenceAllowed(identity, layer, id) {
			return false
		}
	}
	return true
}

func (h *Handler) dailyStatsConfig(w http.ResponseWriter, r *http.Request, identity Identity, id string) {
	node, ok := h.routeNode(w, id)
	if !ok {
		return
	}
	if screenLayerKind(node) == "" {
		writeError(w, 400, "daily_stats_requires_layer")
		return
	}
	existing, err := readDailyConfig(node)
	if err != nil {
		writeError(w, 503, "invalid_daily_stats_config")
		return
	}
	if r.Method == http.MethodGet {
		if !h.dailyReferencesAllowed(identity, id, existing) {
			writeError(w, 403, "daily_stats_reference_forbidden")
			return
		}
		writeJSON(w, existing)
		return
	}
	// ServeHTTP's node-configuration lock serializes the version check and save.
	var input dailystats.Config
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Version != existing.Version {
		writeError(w, 409, "version_conflict")
		return
	}
	if err := input.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if !h.dailyReferencesAllowed(identity, id, input) {
		writeError(w, 403, "daily_stats_reference_forbidden")
		return
	}
	for _, point := range input.PointIDs() {
		resolved, err := h.historyQuery().Nodes.Resolve(point)
		if err != nil {
			writeHistoryError(w, err)
			return
		}
		if input.Energy != nil && point == input.Energy.PointID && resolved.Units != input.Energy.Unit {
			writeError(w, 400, "energy_unit_mismatch")
			return
		}
	}
	input.Version++
	raw, err := json.Marshal(input)
	if err != nil {
		writeError(w, 400, "invalid_daily_stats_config")
		return
	}
	point := data.NewPointString(dailyStatsPoint, "", string(raw))
	point.Origin = identity.UserID
	if err := client.SendNodePoint(h.nc, id, point, true); err != nil {
		writeError(w, 503, "daily_stats_save_failed")
		return
	}
	writeJSON(w, input)
}

type dailyStatsResponse struct {
	LayerID       string                         `json:"layer_id"`
	ConfigVersion int                            `json:"config_version"`
	AsOf          time.Time                      `json:"as_of"`
	Window        *dailystats.Window             `json:"window"`
	Energy        dailystats.Metric              `json:"energy"`
	Runtime       dailystats.Metric              `json:"runtime"`
	Freshness     dailystats.Metric              `json:"freshness"`
	Communication dailystats.CommunicationMetric `json:"communication"`
}

func dailyHistoryMetadata(metric *dailystats.Metric, rows historyquery.PointResult) {
	metric.Source = rows.Source
	metric.Retention = &rows.Retention
	if rows.Source == historyquery.SourceRuntime {
		metric.HistoryLifetime = "process_memory"
		if metric.Status == "partial" && rows.Retention.Limit > 0 && rows.Matched >= rows.Retention.Limit {
			metric.Reason = "retained_history_partial"
		}
	} else if rows.Source == historyquery.SourceSIOT {
		metric.HistoryLifetime = "persistent_quality_metadata_unavailable"
	}
}

func (h *Handler) dailyStats(w http.ResponseWriter, r *http.Request, identity Identity) {
	layer := strings.TrimSpace(r.URL.Query().Get("layer"))
	if layer == "" {
		writeError(w, 400, "layer_required")
		return
	}
	node, ok := h.routeNode(w, layer)
	if !ok {
		return
	}
	config, err := readDailyConfig(node)
	if err != nil {
		writeError(w, 503, "invalid_daily_stats_config")
		return
	}
	if !h.dailyReferencesAllowed(identity, layer, config) {
		writeError(w, 403, "daily_stats_reference_forbidden")
		return
	}
	now := time.Now()
	result := dailyStatsResponse{LayerID: layer, ConfigVersion: config.Version, AsOf: now, Energy: dailystats.Unavailable("kWh", "binding_missing"), Runtime: dailystats.Unavailable("h", "binding_missing"), Freshness: dailystats.Unavailable("s", "binding_missing"), Communication: dailystats.Communication(now, 0, 0)}
	result.Communication.Reason = "binding_missing"
	if config.Version == 0 {
		writeJSON(w, result)
		return
	}
	if err := config.Validate(); err != nil {
		writeError(w, 503, "invalid_daily_stats_config")
		return
	}
	window, err := dailystats.Day(r.URL.Query().Get("date"), config.TimeZone, now)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	result.Window = &window
	query := h.historyQuery()
	read := func(id string, start, end time.Time) (historyquery.PointResult, string) {
		rows, _, err := query.Execute(historyquery.Request{NodeIDs: []string{id}, Type: data.PointTypeValue, Start: start, End: end, Limit: historyquery.ExportLimit})
		if err != nil {
			return historyquery.PointResult{}, err.Error()
		}
		if len(rows) != 1 {
			return historyquery.PointResult{}, "history_unavailable"
		}
		if rows[0].Truncated {
			return rows[0], "history_limit_exceeded"
		}
		return rows[0], ""
	}
	if b := config.Energy; b != nil {
		rows, reason := read(b.PointID, window.Start, window.Through)
		result.Energy = dailystats.Energy(window, *b, rows.Records)
		if reason != "" {
			result.Energy = dailystats.Unavailable("kWh", reason)
		}
		dailyHistoryMetadata(&result.Energy, rows)
	}
	if b := config.Runtime; b != nil {
		rows, reason := read(b.PointID, window.Start, window.Through)
		result.Runtime = dailystats.Runtime(window, *b, rows.Records)
		if reason != "" {
			result.Runtime = dailystats.Unavailable("h", reason)
		}
		dailyHistoryMetadata(&result.Runtime, rows)
	}
	if b := config.Freshness; b != nil {
		rows, reason := read(b.PointID, time.Time{}, time.Time{})
		result.Freshness = dailystats.Freshness(now, *b, rows.Records)
		if reason != "" {
			result.Freshness = dailystats.Unavailable("s", reason)
		}
		dailyHistoryMetadata(&result.Freshness, rows)
	}
	if config.CommunicationPointID != "" {
		result.Communication.Reason = "communication_source_unavailable"
		if query.Runtime != nil && h.ems != nil && h.ems.Service != nil {
			device, _, found, err := query.Runtime.Locate(config.CommunicationPointID)
			if found && err == nil {
				for _, ch := range h.ems.Service.Channels() {
					for _, d := range ch.Devices {
						if d.ID == device {
							stats := ch.Window(device, now, 5)
							result.Communication = dailystats.Communication(now, stats.Polls, stats.Failures)
						}
					}
				}
			}
		}
	}
	writeJSON(w, result)
}
