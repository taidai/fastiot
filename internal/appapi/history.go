package appapi

import (
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"fastiot/internal/ems/history"
	"fastiot/internal/ems/signal"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

type historyReader interface {
	PointHistory(nodeID, typ, key string, start, end time.Time, limit int) ([]data.Point, error)
}

const historyPageLimit = 2000
const historyExportLimit = 10000

type historyRecord struct {
	PointID      string    `json:"point_id"`
	DeviceID     string    `json:"device_id,omitempty"`
	SourceTime   time.Time `json:"source_time,omitzero"`
	ReceivedTime time.Time `json:"received_time,omitzero"`
	BucketEnd    time.Time `json:"bucket_end,omitzero"`
	Quality      string    `json:"quality,omitempty"`
	Value        *float64  `json:"value,omitempty"`
	Text         string    `json:"text,omitempty"`
	Source       string    `json:"source"`
	Unit         string    `json:"unit,omitempty"`
	ConfigStamp  string    `json:"config_stamp,omitempty"`
	TimeSource   string    `json:"time_source,omitempty"`
	Kind         string    `json:"kind,omitempty"`
	Reason       string    `json:"reason,omitempty"`
	Late         bool      `json:"late,omitempty"`
	SampleCount  int       `json:"sample_count,omitempty"`
	Aggregate    string    `json:"aggregate,omitempty"`
}
type historyPointResult struct {
	NodeID           string          `json:"node_id"`
	DeviceID         string          `json:"device_id,omitempty"`
	Source           string          `json:"source"`
	SampleIntervalMS int             `json:"sample_interval_ms"`
	Points           data.Points     `json:"points,omitempty"`
	Records          []historyRecord `json:"records"`
	Matched          int             `json:"matched"`
	Returned         int             `json:"returned"`
	Truncated        bool            `json:"truncated"`
	NextBefore       time.Time       `json:"next_before,omitzero"`
	NextBeforeCount  int             `json:"next_before_count"`
}
type historyReadResult struct {
	records          []historyRecord
	points           data.Points
	deviceID, source string
	sampleIntervalMS int
}

func parseHistoryTime(text string) (time.Time, error) {
	if text == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, text)
}
func historyNode(identity Identity, h *Handler, id string) (data.NodeEdge, error) {
	nodes, err := client.GetNodes(h.nc, "all", id, "", false)
	if err != nil || len(nodes) == 0 || !h.allows(identity, id) {
		return data.NodeEdge{}, errors.New("history_forbidden")
	}
	node := nodes[0]
	if node.Type != data.NodeTypeVariable && node.Type != data.NodeTypeModbusIO && node.Type != NodeTypeOpcuaPoint && node.Type != "mqttSub" {
		return node, errors.New("history_not_supported")
	}
	return node, nil
}

type historyCursor struct {
	Before time.Time
	Count  int
}

func historyWindow(r *http.Request) (time.Time, time.Time, historyCursor, error) {
	cursor := historyCursor{Count: -1}
	start, err := parseHistoryTime(r.URL.Query().Get("start"))
	if err != nil {
		return start, time.Time{}, cursor, err
	}
	end, err := parseHistoryTime(r.URL.Query().Get("end"))
	if err != nil {
		return start, end, cursor, err
	}
	cursor.Before, err = parseHistoryTime(r.URL.Query().Get("before"))
	if err != nil {
		return start, end, cursor, err
	}
	if text := r.URL.Query().Get("before_count"); text != "" {
		count, parseErr := strconv.Atoi(text)
		if parseErr != nil || count < 0 || cursor.Before.IsZero() {
			return start, end, cursor, fmt.Errorf("invalid_before_count")
		}
		cursor.Count = count
	}
	if !start.IsZero() && !end.IsZero() && end.Before(start) {
		return start, end, cursor, fmt.Errorf("invalid_time_range")
	}
	return start, end, cursor, nil
}
func historyAggregation(value string) (time.Duration, error) {
	switch value {
	case "", "raw":
		return 0, nil
	case "5m":
		return 5 * time.Minute, nil
	case "1h":
		return time.Hour, nil
	}
	return 0, fmt.Errorf("invalid_aggregation")
}
func historySIOT(reader historyReader, id, typ, key string, start, end time.Time) ([]data.Point, error) {
	result := []data.Point{}
	for {
		points, err := reader.PointHistory(id, typ, key, start, end, historyPageLimit)
		if err != nil {
			return nil, err
		}
		if len(points) == 0 {
			break
		}
		result = append(result, points...)
		if len(points) < historyPageLimit {
			break
		}
		oldest := points[0].Time
		if oldest.Equal(points[len(points)-1].Time) {
			return nil, errors.New("history_time_cursor_saturated")
		}
		if !start.IsZero() && !oldest.After(start) {
			break
		}
		end = oldest.Add(-time.Nanosecond)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Time.Equal(result[j].Time) {
			return result[i].Origin < result[j].Origin
		}
		return result[i].Time.Before(result[j].Time)
	})
	return result, nil
}
func siotHistoryRecords(node data.NodeEdge, points data.Points) []historyRecord {
	records := make([]historyRecord, 0, len(points))
	for _, point := range points {
		record := historyRecord{PointID: node.ID, SourceTime: point.Time, Text: point.Txt(), Source: point.Origin, Unit: textPoint(node.Points, data.PointTypeUnits), Kind: history.KindObservation}
		if value, err := point.ValueFloat(); err == nil {
			v := value
			record.Value = &v
		}
		records = append(records, record)
	}
	return records
}

func (h *Handler) readSIOTHistoryPoint(node data.NodeEdge, typ, key string, start, end time.Time) (historyReadResult, error) {
	out := historyReadResult{source: "siot", records: []historyRecord{}}
	reader, ok := h.users.(historyReader)
	if !ok {
		return out, errors.New("history_unavailable")
	}
	points, err := historySIOT(reader, node.ID, typ, key, start, end)
	if err != nil {
		return out, err
	}
	out.points = points
	out.records = siotHistoryRecords(node, points)
	return out, nil
}

func historySampleRecord(sample history.Sample, nodeID, deviceID, source string) historyRecord {
	record := historyRecord{PointID: nodeID, DeviceID: deviceID, SourceTime: sample.SourceTime, ReceivedTime: sample.ReceivedTime, Quality: sample.Quality.String(), Source: source, Unit: sample.Unit, ConfigStamp: sample.ConfigStamp, TimeSource: sample.TimeSource, Kind: history.KindObservation, Reason: sample.Reason, Late: sample.Late}
	if sample.Kind == history.KindQualityEvent {
		record.Kind = history.KindQualityEvent
		return record
	}
	if sample.Value.Kind == signal.Number {
		value := sample.Value.Num
		record.Value = &value
	}
	if sample.Value.Kind == signal.Bool {
		value := data.BoolToFloat(sample.Value.Bool)
		record.Value = &value
	}
	return record
}

func (h *Handler) readHistoryPoint(node data.NodeEdge, typ, key string, start, end time.Time, aggregate time.Duration) (historyReadResult, error) {
	out := historyReadResult{source: "siot", records: []historyRecord{}}
	if node.Type == data.NodeTypeModbusIO {
		if h.ems == nil || h.ems.Service == nil || h.ems.Buses == nil {
			return h.readSIOTHistoryPoint(node, typ, key, start, end)
		}
		configs, err := h.ems.Buses()
		if err != nil {
			return out, err
		}
		deviceID := ""
		intervalMS := 0
		for _, config := range configs {
			for _, device := range config.Devices {
				for _, point := range device.Points {
					if point.ID == node.ID {
						if deviceID != "" && deviceID != device.ID {
							return out, errors.New("ambiguous_history_source")
						}
						deviceID = device.ID
						intervalMS = int(config.Period / time.Millisecond)
					}
				}
			}
		}
		if deviceID == "" {
			return h.readSIOTHistoryPoint(node, typ, key, start, end)
		}
		out.deviceID, out.source, out.sampleIntervalMS = deviceID, "modbus_runtime", intervalMS
		for _, sample := range h.ems.Service.History.Query(deviceID, node.ID, start, end, 0) {
			if sample.SourceTime.IsZero() {
				continue
			}
			out.records = append(out.records, historySampleRecord(sample, node.ID, deviceID, out.source))
		}
		for _, sample := range h.ems.Service.History.QueryEvents(deviceID, node.ID, start, end, 0) {
			if sample.SourceTime.IsZero() {
				continue
			}
			out.records = append(out.records, historySampleRecord(sample, node.ID, deviceID, out.source))
		}
	} else {
		var err error
		out, err = h.readSIOTHistoryPoint(node, typ, key, start, end)
		if err != nil {
			return out, err
		}
	}
	sortHistoryRecords(out.records)
	if aggregate == 0 {
		return out, nil
	}
	records := []historyRecord{}
	events := make([]historyRecord, 0)
	var bucketStart time.Time
	var sum float64
	count := 0
	quality, unit, timeSource := "", "", ""
	flush := func() {
		if count == 0 {
			return
		}
		value := sum / float64(count)
		records = append(records, historyRecord{PointID: node.ID, DeviceID: out.deviceID, SourceTime: bucketStart, BucketEnd: bucketStart.Add(aggregate), Quality: quality, Value: &value, Source: out.source, Unit: unit, TimeSource: timeSource, SampleCount: count, Aggregate: aggregate.String(), Kind: history.KindObservation})
	}
	for _, record := range out.records {
		if record.Kind == history.KindQualityEvent {
			events = append(events, record)
			continue
		}
		if record.Value == nil || (node.Type == data.NodeTypeModbusIO && record.Quality != signal.Good.String()) {
			continue
		}
		at := record.SourceTime.Truncate(aggregate)
		if count > 0 && !at.Equal(bucketStart) {
			flush()
			count = 0
			sum = 0
		}
		if count == 0 {
			bucketStart = at
			quality = record.Quality
			unit = record.Unit
			timeSource = record.TimeSource
		} else {
			if unit != record.Unit {
				unit = ""
			}
			if timeSource != record.TimeSource {
				timeSource = ""
			}
		}
		sum += *record.Value
		count++
	}
	flush()
	out.records = append(records, events...)
	sortHistoryRecords(out.records)
	return out, nil
}
func sortHistoryRecords(records []historyRecord) {
	sort.SliceStable(records, func(i, j int) bool { return records[i].SourceTime.Before(records[j].SourceTime) })
}
func filterHistoryRecords(records []historyRecord, cursor historyCursor) []historyRecord {
	filtered := make([]historyRecord, 0, len(records))
	boundarySeen := 0
	for _, r := range records {
		if cursor.Before.IsZero() || r.SourceTime.Before(cursor.Before) {
			filtered = append(filtered, r)
			continue
		}
		if cursor.Count >= 0 && r.SourceTime.Equal(cursor.Before) && boundarySeen < cursor.Count {
			filtered = append(filtered, r)
			boundarySeen++
		}
	}
	return filtered
}

func (h *Handler) history(w http.ResponseWriter, r *http.Request, identity Identity, export bool) {
	if export && !canAction(identity, ActionExport) {
		writeError(w, http.StatusForbidden, "admin_required")
		return
	}
	start, end, cursor, err := historyWindow(r)
	if err != nil {
		writeError(w, 400, "invalid_time_range")
		return
	}
	aggregate, err := historyAggregation(r.URL.Query().Get("aggregate"))
	if err != nil {
		writeError(w, 400, "invalid_aggregation")
		return
	}
	typ := r.URL.Query().Get("type")
	if typ == "" {
		typ = data.PointTypeValue
	}
	if typ != data.PointTypeValue && typ != data.PointTypeValueSet {
		writeError(w, 403, "history_type_not_allowed")
		return
	}
	limit := historyPageLimit
	if text := r.URL.Query().Get("limit"); text != "" {
		limit, err = strconv.Atoi(text)
		if err != nil || limit < 1 || limit > historyPageLimit {
			writeError(w, 400, "invalid_limit")
			return
		}
	}
	ids := r.URL.Query()["node_id"]
	if len(ids) == 0 {
		writeError(w, 400, "node_id_required")
		return
	}
	if len(ids) > historyExportLimit/historyPageLimit {
		writeError(w, 400, "too_many_history_points")
		return
	}
	results := []historyPointResult{}
	total := 0
	matched := 0
	legacy := data.Points{}
	for _, id := range ids {
		node, err := historyNode(identity, h, id)
		if err != nil {
			if err.Error() == "history_not_supported" {
				writeError(w, 400, err.Error())
			} else {
				writeError(w, 403, "history_forbidden")
			}
			return
		}
		read, err := h.readHistoryPoint(node, typ, r.URL.Query().Get("key"), start, end, aggregate)
		if err != nil {
			if err.Error() == "history_time_cursor_saturated" {
				writeError(w, 400, err.Error())
			} else {
				writeError(w, 503, "history_unavailable")
			}
			return
		}
		pageLimit := limit
		if export {
			pageLimit = historyExportLimit + 1
		}
		filtered := filterHistoryRecords(read.records, cursor)
		matchedCount := len(read.records)
		if export {
			matchedCount = len(filtered)
		}
		matched += matchedCount
		total += len(filtered)
		records := filtered
		next := historyCursor{}
		truncated := len(records) > pageLimit
		if truncated {
			records = records[len(records)-pageLimit:]
			next.Before = records[0].SourceTime
			for _, candidate := range filtered {
				if candidate.SourceTime.Equal(next.Before) {
					next.Count++
				}
			}
			for _, candidate := range records {
				if candidate.SourceTime.Equal(next.Before) {
					next.Count--
				}
			}
		}
		results = append(results, historyPointResult{NodeID: id, DeviceID: read.deviceID, Source: read.source, SampleIntervalMS: read.sampleIntervalMS, Points: read.points, Records: records, Matched: matchedCount, Returned: len(records), Truncated: truncated, NextBefore: next.Before, NextBeforeCount: next.Count})
		legacy = append(legacy, read.points...)
	}
	if export && total > historyExportLimit {
		writeError(w, 400, "history_export_limit_exceeded")
		return
	}
	if export {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("X-History-Returned", strconv.Itoa(total))
		w.Header().Set("X-History-Limit", strconv.Itoa(historyExportLimit))
		w.Header().Set("Content-Disposition", `attachment; filename="history.csv"`)
		writer := csv.NewWriter(w)
		if err := writer.Write([]string{"点位ID", "设备ID", "观测时间", "接收时间", "桶结束", "质量", "数值", "文本", "来源", "单位", "聚合", "样本数", "种类", "原因"}); err != nil {
			return
		}
		for _, result := range results {
			for _, record := range result.Records {
				value, text := "", record.Text
				if record.Value != nil {
					value = strconv.FormatFloat(*record.Value, 'g', -1, 64)
				}
				at, received, bucketEnd := "", "", ""
				if !record.SourceTime.IsZero() {
					at = record.SourceTime.Format(time.RFC3339Nano)
				}
				if !record.ReceivedTime.IsZero() {
					received = record.ReceivedTime.Format(time.RFC3339Nano)
				}
				if !record.BucketEnd.IsZero() {
					bucketEnd = record.BucketEnd.Format(time.RFC3339Nano)
				}
				_ = writer.Write([]string{record.PointID, record.DeviceID, at, received, bucketEnd, record.Quality, value, text, record.Source, record.Unit, record.Aggregate, strconv.Itoa(record.SampleCount), record.Kind, record.Reason})
			}
		}
		writer.Flush()
		return
	}
	response := map[string]any{"results": results, "start": start, "end": end, "aggregate": r.URL.Query().Get("aggregate"), "total_matched": matched}
	if len(ids) == 1 {
		response["node_id"] = ids[0]
		response["type"] = typ
		response["points"] = legacy
	}
	writeJSON(w, response)
}
