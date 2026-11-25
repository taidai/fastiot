package appapi

import (
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"fastiot/internal/ems/historyquery"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

const (
	historyPageLimit   = historyquery.PageLimit
	historyExportLimit = historyquery.ExportLimit
)

// historyReader is the product store's point history, used by the history
// query adapter and by aggregate and forecast readers.
type historyReader interface {
	PointHistory(nodeID, typ, key string, start, end time.Time, limit int) ([]data.Point, error)
}

func parseHistoryTime(text string) (time.Time, error) {
	if text == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, text)
}

// historyResolver resolves the product node a history request names. Node
// lookup stays in the HTTP layer because it is a product-store concern.
type historyResolver struct {
	nodes func(nodeID string) ([]data.NodeEdge, error)
}

func (r historyResolver) Resolve(id string) (historyquery.Node, error) {
	nodes, err := r.nodes(id)
	if err != nil {
		// A backend failure is not a permission decision: report it as
		// unavailable so the HTTP layer answers 503 and the caller retries.
		return historyquery.Node{}, historyquery.ErrUnavailable
	}
	if len(nodes) == 0 {
		return historyquery.Node{}, historyquery.ErrForbidden
	}
	node := nodes[0]
	if node.Type == virtualInputType || node.Type == calculationType {
		return historyquery.Node{ID: node.ID, Type: node.Type, Derived: true}, nil
	}
	if node.Type == data.NodeTypeGroup || node.Parent == "root" {
		if definition, ok := node.Points.Find(aggregatePoint, ""); ok && definition.Tombstone%2 == 0 {
			return historyquery.Node{ID: node.ID, Type: node.Type, Aggregate: true}, nil
		}
	}
	switch node.Type {
	case data.NodeTypeVariable, data.NodeTypeModbusIO, NodeTypeOpcuaPoint, "mqttSub":
	default:
		return historyquery.Node{}, historyquery.ErrNotSupported
	}
	return historyquery.Node{ID: node.ID, Type: node.Type, Units: textPoint(node.Points, data.PointTypeUnits)}, nil
}

// historyQuery wires the shared query to the product history and runtime
// adapters available to this handler.
func (h *Handler) historyQuery() *historyquery.Query {
	query := &historyquery.Query{Nodes: historyResolver{nodes: func(id string) ([]data.NodeEdge, error) {
		return client.GetNodes(h.nc, "all", id, "", false)
	}}}
	if reader, ok := h.users.(historyquery.SIOT); ok {
		query.SIOT = reader
	}
	if h.ems != nil && h.ems.Service != nil && h.ems.Buses != nil {
		buses := h.ems.Buses
		query.Runtime = &historyquery.StoreRuntime{
			Store: h.ems.Service.History,
			Points: func() ([]historyquery.DevicePoint, error) {
				configs, err := buses()
				if err != nil {
					return nil, err
				}
				points := []historyquery.DevicePoint{}
				for _, config := range configs {
					for _, device := range config.Devices {
						for _, point := range device.Points {
							points = append(points, historyquery.DevicePoint{
								PointID: point.ID, DeviceID: device.ID, IntervalMS: int(config.Period / time.Millisecond),
							})
						}
					}
				}
				return points, nil
			},
		}
	}
	return query
}

func historyWindow(r *http.Request) (time.Time, time.Time, historyquery.Cursor, error) {
	cursor := historyquery.Cursor{Count: -1}
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
	return start, end, cursor, nil
}

func historyAggregation(value string) (time.Duration, error) {
	switch value {
	case "", "raw":
		return 0, nil
	case "1s":
		return time.Second, nil
	case "5s":
		return 5 * time.Second, nil
	case "30s":
		return 30 * time.Second, nil
	case "1m":
		return time.Minute, nil
	case "5m":
		return 5 * time.Minute, nil
	case "1h":
		return time.Hour, nil
	}
	return 0, fmt.Errorf("invalid_aggregation")
}

func writeHistoryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, historyquery.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, historyquery.ErrForbidden),
		errors.Is(err, historyquery.ErrNotSupported),
		errors.Is(err, historyquery.ErrTimeCursorSaturated),
		errors.Is(err, historyquery.ErrInvalidTimeRange),
		errors.Is(err, historyquery.ErrNodeIDRequired),
		errors.Is(err, historyquery.ErrTooManyPoints),
		errors.Is(err, historyquery.ErrExportLimitExceeded):
		status := http.StatusBadRequest
		if errors.Is(err, historyquery.ErrForbidden) {
			status = http.StatusForbidden
		}
		writeError(w, status, err.Error())
	default:
		writeError(w, http.StatusServiceUnavailable, "history_unavailable")
	}
}

func (h *Handler) history(w http.ResponseWriter, r *http.Request, identity Identity, export bool) {
	start, end, cursor, err := historyWindow(r)
	if err != nil || (!start.IsZero() && !end.IsZero() && end.Before(start)) {
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
	if typ != data.PointTypeValue && typ != data.PointTypeValueSet && typ != historyquery.AggregateValuePoint {
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
	query := h.historyQuery()
	memberAccess := map[string]bool{}
	query.AggregateMemberAllowed = func(id string) bool {
		allowed, checked := memberAccess[id]
		if !checked {
			allowed = h.allows(identity, id)
			memberAccess[id] = allowed
		}
		return allowed
	}
	results, matched, err := query.Execute(historyquery.Request{
		NodeIDs: ids, Type: typ, Key: r.URL.Query().Get("key"),
		Start: start, End: end, Cursor: cursor, Aggregate: aggregate, Limit: limit, Export: export,
	})
	if err != nil {
		writeHistoryError(w, err)
		return
	}
	if export {
		total := 0
		for _, result := range results {
			total += len(result.Records)
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("X-History-Returned", strconv.Itoa(total))
		w.Header().Set("X-History-Limit", strconv.Itoa(historyExportLimit))
		w.Header().Set("Content-Disposition", `attachment; filename="history.csv"`)
		writer := csv.NewWriter(w)
		if err := writer.Write([]string{"点位ID", "设备ID", "观测时间", "接收时间", "桶结束", "质量", "数值", "文本", "来源", "来源标识", "单位", "聚合", "样本数", "种类", "原因", "时间依据", "迟到观测", "实际间隔毫秒", "保留策略", "保留条数"}); err != nil {
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
				_ = writer.Write([]string{record.PointID, record.DeviceID, at, received, bucketEnd, record.Quality, value, text, record.Source, record.Origin, record.Unit, record.Aggregate, strconv.Itoa(record.SampleCount), record.Kind, record.Reason, record.TimeSource, strconv.FormatBool(record.Late), strconv.Itoa(result.ActualIntervalMS), result.Retention.Kind, strconv.Itoa(result.Retention.Limit)})
			}
		}
		writer.Flush()
		return
	}
	legacy := data.Points{}
	for _, result := range results {
		legacy = append(legacy, result.Points...)
	}
	// total_matched is the number of records matching the request before the
	// pagination cursor; /history and /history/export report the same total.
	response := map[string]any{"results": results, "start": start, "end": end, "aggregate": r.URL.Query().Get("aggregate"), "total_matched": matched}
	if len(ids) == 1 {
		response["node_id"] = ids[0]
		response["type"] = typ
		response["points"] = legacy
	}
	writeJSON(w, response)
}
