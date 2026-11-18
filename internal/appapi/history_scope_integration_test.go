package appapi

import (
	"encoding/csv"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"fastiot/internal/ems/bridge"
	"fastiot/internal/ems/channel"
	emshistory "fastiot/internal/ems/history"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/ems/signal"
	"fastiot/internal/emsapi"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

func TestHistoryCursorAndAggregateStayConsistent(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 2000)
	defer service.Close()
	source := bridge.NewNodeSource(h.nc)
	api := emsapi.New(service, time.Minute)
	api.Buses = source.Buses
	h.SetEMS(api)
	f := &pointConfigFixture{base: base, token: token}
	bus := f.createBus(t, "aggregate source")
	point := f.createPoint(t, bus, ModbusIOInput{Description: "A", ServerID: 1, Address: 0, IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1, ReadOnly: true, Units: "C"})
	configs, err := source.Buses()
	if err != nil {
		t.Fatal(err)
	}
	device := configs[0].Devices[0]
	at := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 600; i++ {
		now := at.Add(time.Duration(i) * time.Second)
		service.Observe(now.Add(time.Millisecond), []channel.Result{{DeviceID: device.ID, PointID: point, Value: signal.Num(float64(i)), Quality: signal.Good, SourceTime: now, Unit: "C", ConfigStamp: device.Points[0].ConfigStamp}})
	}
	if got := len(service.History.Query(device.ID, point, at, at.Add(599*time.Second), 1000)); got != 600 {
		t.Fatalf("history fixture lost samples: %d", got)
	}
	node, err := historyNode(h, point)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := h.readHistoryPoint(node, "value", "", at, at.Add(599*time.Second), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("raw first=%+v raw last=%+v", raw.records[0], raw.records[len(raw.records)-1])
	read, err := h.readHistoryPoint(node, "value", "", at, at.Add(599*time.Second), 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("aggregate records=%+v", read.records)
	query := url.Values{"node_id": []string{point}, "start": []string{at.Format(time.RFC3339)}, "end": []string{at.Add(599 * time.Second).Format(time.RFC3339)}, "aggregate": []string{"5m"}, "limit": []string{"1"}}
	var page struct {
		Results []struct {
			Matched    int       `json:"matched"`
			Returned   int       `json:"returned"`
			Truncated  bool      `json:"truncated"`
			NextBefore time.Time `json:"next_before"`
			Records    []struct {
				Value       float64   `json:"value"`
				SampleCount int       `json:"sample_count"`
				Aggregate   string    `json:"aggregate"`
				SourceTime  time.Time `json:"source_time"`
			} `json:"records"`
		} `json:"results"`
	}
	status, body := call(t, http.MethodGet, base+"/history?"+query.Encode(), token, nil, &page)
	if status != 200 || len(page.Results) != 1 || page.Results[0].Matched != 2 || page.Results[0].Returned != 1 || !page.Results[0].Truncated {
		t.Fatalf("aggregate pagination hidden: %d %s", status, body)
	}
	record := page.Results[0].Records[0]
	if record.Value != 449.5 || record.SampleCount != 300 || record.Aggregate != "5m0s" || !record.SourceTime.Equal(at.Add(599*time.Second).Truncate(5*time.Minute)) {
		t.Fatalf("aggregate mean/bucket wrong: %+v next=%v", record, page.Results[0].NextBefore)
	}
	status, body = call(t, http.MethodGet, base+"/history/export?"+query.Encode(), token, nil, nil)
	if status != 200 || !strings.Contains(body, "449.5") || !strings.Contains(body, "5m0s") {
		t.Fatalf("export did not use query aggregation: %d %s", status, body)
	}
	empty := url.Values{"node_id": []string{point}, "start": []string{at.Add(time.Hour).Format(time.RFC3339)}, "end": []string{at.Add(time.Hour).Format(time.RFC3339)}}
	status, body = call(t, http.MethodGet, base+"/history?"+empty.Encode(), token, nil, nil)
	if status != 200 || !strings.Contains(body, "\"records\":[]") {
		t.Fatalf("empty interval not distinct from failure: %d %s", status, body)
	}
}

func TestHistoryExportRejectsBeyondSupportedRangeWithoutPartialCSV(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 12000)
	defer service.Close()
	source := bridge.NewNodeSource(h.nc)
	api := emsapi.New(service, time.Minute)
	api.Buses = source.Buses
	h.SetEMS(api)
	f := &pointConfigFixture{base: base, token: token}
	bus := f.createBus(t, "bounded source")
	point := f.createPoint(t, bus, ModbusIOInput{Description: "A", ServerID: 1, Address: 0, IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1, ReadOnly: true, Units: "C"})
	configs, err := source.Buses()
	if err != nil {
		t.Fatal(err)
	}
	device := configs[0].Devices[0]
	at := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 10001; i++ {
		now := at.Add(time.Duration(i) * time.Second)
		service.Observe(now, []channel.Result{{DeviceID: device.ID, PointID: point, Value: signal.Num(float64(i)), Quality: signal.Good, SourceTime: now, ConfigStamp: device.Points[0].ConfigStamp}})
	}
	query := url.Values{"node_id": []string{point}, "start": []string{at.Format(time.RFC3339)}, "end": []string{at.Add(10000 * time.Second).Format(time.RFC3339)}}
	status, body := call(t, http.MethodGet, base+"/history/export?"+query.Encode(), token, nil, nil)
	if status != 400 || strings.Contains(body, "点位ID") || !strings.Contains(body, "history_export_limit_exceeded") {
		t.Fatalf("oversize export was truncated or disguised as complete: %d %s", status, body)
	}
	query.Set("limit", "500")
	var page struct {
		Results []struct {
			Matched, Returned int
			Truncated         bool
			NextBefore        time.Time `json:"next_before"`
		} `json:"results"`
	}
	status, body = call(t, http.MethodGet, base+"/history?"+query.Encode(), token, nil, &page)
	if status != 200 || len(page.Results) != 1 || page.Results[0].Matched != 10001 || page.Results[0].Returned != 500 || !page.Results[0].Truncated || page.Results[0].NextBefore.IsZero() {
		t.Fatalf("bounded query did not explain remaining data: %d %s", status, body)
	}
}

func TestMultiPointHistoryQueryAndExportShareScope(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 2000)
	defer service.Close()
	source := bridge.NewNodeSource(h.nc)
	api := emsapi.New(service, time.Minute)
	api.Buses = source.Buses
	h.SetEMS(api)
	root := snapshotRoot(t, base, token)
	a := createNodeAPI(t, base, token, "group", root, "A", 0)
	createNodeAPI(t, base, token, "group", root, "B-private", 0)
	f := &pointConfigFixture{base: base, token: token}
	busA := f.createBus(t, "A station")
	busB := f.createBus(t, "B station")
	input := ModbusIOInput{Description: "A", ServerID: 1, Address: 0, IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1, ReadOnly: true, Units: "C"}
	aPoint := f.createPoint(t, busA, input)
	input.Description, input.ServerID = "B", 2
	bPoint := f.createPoint(t, busB, input)
	configs, err := source.Buses()
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	for _, config := range configs {
		for _, device := range config.Devices {
			point := aPoint
			if device.ServerID == 2 {
				point = bPoint
			}
			for i := 0; i < 600; i++ {
				now := at.Add(time.Duration(i) * time.Second)
				service.Observe(now.Add(5*time.Millisecond), []channel.Result{{DeviceID: device.ID, PointID: point, Value: signal.Num(float64(i)), Quality: signal.Good, SourceTime: now, Unit: "C", ConfigStamp: device.Points[0].ConfigStamp}})
			}
		}
	}
	query := url.Values{"node_id": []string{aPoint, bPoint}, "start": []string{at.Format(time.RFC3339)}, "end": []string{at.Add(599 * time.Second).Format(time.RFC3339)}, "limit": []string{"1000"}}
	var result struct {
		Results []struct {
			NodeID    string `json:"node_id"`
			Matched   int    `json:"matched"`
			Returned  int    `json:"returned"`
			Truncated bool   `json:"truncated"`
		} `json:"results"`
	}
	status, body := call(t, http.MethodGet, base+"/history?"+query.Encode(), token, nil, &result)
	if status != 200 || len(result.Results) != 2 {
		t.Fatalf("multi-point query not supported: %d %s", status, body)
	}
	for _, item := range result.Results {
		if item.Matched != 600 || item.Returned != 600 || item.Truncated {
			t.Fatalf("query silently reduced scope: %+v", item)
		}
	}
	status, body = call(t, http.MethodGet, base+"/history/export?"+query.Encode(), token, nil, nil)
	if status != 200 {
		t.Fatalf("multi-point export: %d %s", status, body)
	}
	rows, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, row := range rows {
		counts[row[0]]++
	}
	if counts[aPoint] != 600 || counts[bPoint] != 600 {
		t.Fatalf("CSV did not preserve both point scopes: %v", counts)
	}
	if len(rows) != 1201 {
		t.Fatalf("CSV row count = %d", len(rows))
	}
	last := rows[len(rows)-1]
	if last[0] != bPoint || last[2] != at.Add(599*time.Second).Format(time.RFC3339Nano) || last[3] != at.Add(599*time.Second+5*time.Millisecond).Format(time.RFC3339Nano) || last[5] != "good" || last[6] != "599" {
		t.Fatalf("CSV evidence mismatch: %v", last)
	}
	limited := inputAccount(t, &pointConfigFixture{base: base, token: token}, a, "viewer", []string{ActionExport})
	status, body = call(t, http.MethodGet, base+"/history/export?"+query.Encode(), limited, nil, nil)
	if status == 200 || strings.Contains(body, bPoint) {
		t.Fatalf("unauthorized point appeared in exported data: %d %s", status, body)
	}
}

func TestHistoryExportRequiresExportAction(t *testing.T) {
	base, token, service, point, device := historyRuntimePoint(t, 10)
	at := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	service.History.Record([]emshistory.Sample{{DeviceID: device, PointID: point, Value: signal.Num(7), Quality: signal.Good, SourceTime: at, ReceivedTime: at.Add(5 * time.Millisecond), Unit: "C", TimeSource: "platform_acquisition"}})
	f := &pointConfigFixture{base: base, token: token}
	root := snapshotRoot(t, base, token)
	query := url.Values{"node_id": []string{point}, "start": []string{at.Format(time.RFC3339)}, "end": []string{at.Format(time.RFC3339)}}

	for _, role := range []string{"viewer", "operator"} {
		account := inputAccount(t, f, root, role, nil)
		status, body := call(t, http.MethodGet, base+"/history/export?"+query.Encode(), account, nil, nil)
		if status != http.StatusForbidden {
			t.Fatalf("%s exported history without the export action: %d %s", role, status, body)
		}
	}

	exporter := inputAccount(t, f, root, "viewer", []string{ActionExport})
	status, body := call(t, http.MethodGet, base+"/history/export?"+query.Encode(), exporter, nil, nil)
	if status != http.StatusOK || !strings.Contains(body, point) {
		t.Fatalf("explicit export grant did not export: %d %s", status, body)
	}
}

type historyTestRecord struct {
	Value       *float64  `json:"value"`
	Text        string    `json:"text"`
	Unit        string    `json:"unit"`
	Quality     string    `json:"quality"`
	TimeSource  string    `json:"time_source"`
	SampleCount int       `json:"sample_count"`
	Kind        string    `json:"kind"`
	Reason      string    `json:"reason"`
	Late        bool      `json:"late"`
	SourceTime  time.Time `json:"source_time"`
}

func historyRuntimePoint(t *testing.T, limit int) (string, string, *runtime.Service, string, string) {
	t.Helper()
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, limit)
	t.Cleanup(service.Close)
	source := bridge.NewNodeSource(h.nc)
	api := emsapi.New(service, time.Minute)
	api.Buses = source.Buses
	h.SetEMS(api)
	f := &pointConfigFixture{base: base, token: token}
	bus := f.createBusPeriod(t, "history cursor source", 1000)
	point := f.createPoint(t, bus, ModbusIOInput{Description: "A", ServerID: 1, Address: 0, IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1, ReadOnly: true, Units: "C"})
	configs, err := source.Buses()
	if err != nil {
		t.Fatal(err)
	}
	return base, token, service, point, configs[0].Devices[0].ID
}

func TestHistoryCursorKeepsIdenticalSourceTimes(t *testing.T) {
	base, token, service, point, device := historyRuntimePoint(t, 10)
	at := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= 2; i++ {
		service.History.Record([]emshistory.Sample{{DeviceID: device, PointID: point, Value: signal.Num(float64(i)), Quality: signal.Good, SourceTime: at, ReceivedTime: at.Add(time.Duration(i) * time.Millisecond), Unit: "C", TimeSource: "platform_acquisition"}})
	}
	query := url.Values{"node_id": []string{point}, "start": []string{at.Format(time.RFC3339)}, "end": []string{at.Format(time.RFC3339)}, "limit": []string{"1"}}
	var page struct {
		TotalMatched int `json:"total_matched"`
		Results      []struct {
			Matched         int       `json:"matched"`
			Returned        int       `json:"returned"`
			Truncated       bool      `json:"truncated"`
			NextBefore      time.Time `json:"next_before"`
			NextBeforeCount int       `json:"next_before_count"`
			Records         []historyTestRecord
		} `json:"results"`
	}
	status, body := call(t, http.MethodGet, base+"/history?"+query.Encode(), token, nil, &page)
	if status != 200 || len(page.Results) != 1 || page.TotalMatched != 2 || page.Results[0].Matched != 2 || page.Results[0].Returned != 1 || !page.Results[0].Truncated || page.Results[0].NextBeforeCount != 1 {
		t.Fatalf("first identical-time page = %d %s", status, body)
	}
	if len(page.Results[0].Records) != 1 || page.Results[0].Records[0].Value == nil || *page.Results[0].Records[0].Value != 2 || page.Results[0].Records[0].TimeSource != "platform_acquisition" {
		t.Fatalf("latest record or time source missing: %+v", page.Results[0].Records)
	}
	query.Set("before", page.Results[0].NextBefore.Format(time.RFC3339Nano))
	query.Set("before_count", "1")
	page.Results = nil
	status, body = call(t, http.MethodGet, base+"/history?"+query.Encode(), token, nil, &page)
	if status != 200 || len(page.Results) != 1 || page.TotalMatched != 2 || page.Results[0].Matched != 2 || page.Results[0].Returned != 1 || page.Results[0].Truncated {
		t.Fatalf("remaining identical-time page = %d %s", status, body)
	}
	if len(page.Results[0].Records) != 1 || page.Results[0].Records[0].Value == nil || *page.Results[0].Records[0].Value != 1 {
		t.Fatalf("identical-time record was skipped or duplicated: %+v", page.Results[0].Records)
	}
}

func TestHistoryAggregateExportUsesAllRawSamples(t *testing.T) {
	base, token, service, point, device := historyRuntimePoint(t, 12000)
	at := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 10002; i++ {
		now := at.Add(time.Duration(i) * time.Second)
		service.Observe(now, []channel.Result{{DeviceID: device, PointID: point, Value: signal.Num(float64(i)), Quality: signal.Good, SourceTime: now, Unit: "C", TimeSource: "platform_acquisition"}})
	}
	query := url.Values{"node_id": []string{point}, "start": []string{at.Format(time.RFC3339)}, "end": []string{at.Add(10001 * time.Second).Format(time.RFC3339)}, "aggregate": []string{"5m"}}
	status, body := call(t, http.MethodGet, base+"/history/export?"+query.Encode(), token, nil, nil)
	if status != 200 {
		t.Fatalf("aggregate export failed: %d %s", status, body)
	}
	rows, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 35 || rows[1][6] != "149.5" || rows[1][9] != "C" || rows[1][11] != "300" {
		t.Fatalf("first aggregate bucket lost raw samples: rows=%d first=%v", len(rows), rows[1])
	}
}

func historyVariablePoint(t *testing.T) (string, string, *Handler, string) {
	t.Helper()
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	point := data.NodeEdge{ID: data.NewID(), Type: data.NodeTypeVariable, Parent: snapshotRoot(t, base, token), Points: data.Points{data.NewPointString(data.PointTypeDescription, "", "native history")}}
	if err := client.SendNode(h.nc, point, "history-fixture"); err != nil {
		t.Fatal(err)
	}
	return base, token, h, point.ID
}

func TestSIOTHistoryValueAndAggregateSemantics(t *testing.T) {
	base, token, h, point := historyVariablePoint(t)
	at := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	if err := client.SendNodePoint(h.nc, point, data.NewPointString(data.PointTypeUnits, "", "C"), true); err != nil {
		t.Fatal(err)
	}
	points := data.Points{
		data.NewPointFloat(data.PointTypeValue, "", 2),
		data.NewPointFloat(data.PointTypeValue, "", 4),
		data.NewPointString(data.PointTypeValue, "", "not-a-number"),
	}
	points[0].Time = at
	points[1].Time = at.Add(time.Second)
	points[2].Time = at.Add(2 * time.Second)
	if err := client.SendNodePoints(h.nc, point, points, true); err != nil {
		t.Fatal(err)
	}
	query := url.Values{"node_id": []string{point}, "start": []string{at.Format(time.RFC3339)}, "end": []string{at.Add(2 * time.Second).Format(time.RFC3339)}}
	t.Run("raw text is not invented zero", func(t *testing.T) {
		var result struct {
			Results []struct{ Records []historyTestRecord }
		}
		status, body := call(t, http.MethodGet, base+"/history?"+query.Encode(), token, nil, &result)
		if status != 200 || len(result.Results) != 1 || len(result.Results[0].Records) != 3 {
			t.Fatalf("raw SIOT history = %d %s", status, body)
		}
		record := result.Results[0].Records[2]
		if record.Value != nil || record.Text != "not-a-number" {
			t.Fatalf("non-numeric point became a number: %+v", record)
		}
	})
	t.Run("aggregate uses numeric SIOT values and units", func(t *testing.T) {
		query.Set("aggregate", "5m")
		var result struct {
			Results []struct{ Records []historyTestRecord }
		}
		status, body := call(t, http.MethodGet, base+"/history?"+query.Encode(), token, nil, &result)
		if status != 200 || len(result.Results) != 1 || len(result.Results[0].Records) != 1 {
			t.Fatalf("aggregate SIOT history = %d %s", status, body)
		}
		record := result.Results[0].Records[0]
		if record.Value == nil || *record.Value != 3 || record.SampleCount != 2 || record.Unit != "C" || record.Quality != "" {
			t.Fatalf("aggregate SIOT evidence = %+v", record)
		}
	})
}

func TestSIOTHistoryPagingDoesNotDuplicateBoundary(t *testing.T) {
	base, token, h, point := historyVariablePoint(t)
	at := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	points := make(data.Points, 2001)
	for i := range points {
		points[i] = data.NewPointFloat(data.PointTypeValue, "", float64(i))
		points[i].Time = at.Add(time.Duration(i) * time.Second)
	}
	if err := client.SendNodePoints(h.nc, point, points, true); err != nil {
		t.Fatal(err)
	}
	query := url.Values{"node_id": []string{point}, "start": []string{at.Format(time.RFC3339)}, "end": []string{at.Add(2000 * time.Second).Format(time.RFC3339)}, "limit": []string{"2000"}}
	var page struct {
		TotalMatched int `json:"total_matched"`
		Results      []struct {
			Matched         int       `json:"matched"`
			Returned        int       `json:"returned"`
			Truncated       bool      `json:"truncated"`
			NextBefore      time.Time `json:"next_before"`
			NextBeforeCount int       `json:"next_before_count"`
		} `json:"results"`
	}
	status, body := call(t, http.MethodGet, base+"/history?"+query.Encode(), token, nil, &page)
	if status != 200 || page.TotalMatched != 2001 || len(page.Results) != 1 || page.Results[0].Matched != 2001 || page.Results[0].Returned != 2000 || !page.Results[0].Truncated {
		t.Fatalf("SIOT first page duplicated records: %d %s", status, body)
	}
	query.Set("before", page.Results[0].NextBefore.Format(time.RFC3339Nano))
	query.Set("before_count", "0")
	page.Results = nil
	status, body = call(t, http.MethodGet, base+"/history?"+query.Encode(), token, nil, &page)
	if status != 200 || page.TotalMatched != 2001 || len(page.Results) != 1 || page.Results[0].Matched != 2001 || page.Results[0].Returned != 1 || page.Results[0].Truncated {
		t.Fatalf("SIOT remaining page = %d %s", status, body)
	}
}

type saturatedHistoryReader struct{ points data.Points }

func (r saturatedHistoryReader) PointHistory(string, string, string, time.Time, time.Time, int) ([]data.Point, error) {
	return r.points, nil
}

func TestSIOTHistoryRejectsSaturatedTimeCursor(t *testing.T) {
	at := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	points := make(data.Points, historyPageLimit)
	for i := range points {
		points[i] = data.NewPointFloat(data.PointTypeValue, "", float64(i))
		points[i].Time = at
	}
	_, err := historySIOT(saturatedHistoryReader{points: points}, "point", data.PointTypeValue, "", time.Time{}, time.Time{})
	if err == nil || err.Error() != "history_time_cursor_saturated" {
		t.Fatalf("saturated SIOT cursor error = %v", err)
	}
}

func TestMultiPointHistoryKeepsGapsWithTheirPoint(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 200)
	t.Cleanup(service.Close)
	source := bridge.NewNodeSource(h.nc)
	api := emsapi.New(service, time.Minute)
	api.Buses = source.Buses
	h.SetEMS(api)
	f := &pointConfigFixture{base: base, token: token}
	busA := f.createBusPeriod(t, "A gap source", 1000)
	busB := f.createBusPeriod(t, "B normal source", 1000)
	input := ModbusIOInput{Description: "A", ServerID: 1, Address: 0, IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1, ReadOnly: true, Units: "C"}
	aPoint := f.createPoint(t, busA, input)
	input.Description, input.ServerID = "B", 2
	bPoint := f.createPoint(t, busB, input)
	configs, err := source.Buses()
	if err != nil {
		t.Fatal(err)
	}
	devices := map[int]string{}
	for _, config := range configs {
		for _, device := range config.Devices {
			devices[int(device.ServerID)] = device.ID
		}
	}
	at := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	observe := func(point, device string, seconds int, value float64) {
		when := at.Add(time.Duration(seconds) * time.Second)
		service.Observe(when, []channel.Result{{DeviceID: device, PointID: point, Value: signal.Num(value), Quality: signal.Good, SourceTime: when, Unit: "C", TimeSource: "platform_acquisition"}})
	}
	for i := 0; i < 5; i++ {
		observe(aPoint, devices[1], i, float64(10+i))
		observe(bPoint, devices[2], i, float64(20+i))
	}
	service.Observe(at.Add(5*time.Second), []channel.Result{{DeviceID: devices[1], PointID: aPoint, Quality: signal.Invalid, Error: "timeout"}})
	observe(aPoint, devices[1], 10, 15)
	service.Observe(at.Add(11*time.Second), []channel.Result{{DeviceID: devices[1], PointID: aPoint, Value: signal.Num(99), Quality: signal.Good, SourceTime: at.Add(6 * time.Second), Unit: "C", TimeSource: "platform_acquisition"}})
	for i := 5; i < 10; i++ {
		observe(bPoint, devices[2], i, float64(20+i))
	}
	query := url.Values{"node_id": []string{aPoint, bPoint}, "start": []string{at.Format(time.RFC3339)}, "end": []string{at.Add(10 * time.Second).Format(time.RFC3339)}, "limit": []string{"200"}}
	var result struct {
		Results []struct {
			NodeID  string              `json:"node_id"`
			Records []historyTestRecord `json:"records"`
		} `json:"results"`
	}
	status, body := call(t, http.MethodGet, base+"/history?"+query.Encode(), token, nil, &result)
	if status != 200 || len(result.Results) != 2 {
		t.Fatalf("multi-point gap query = %d %s", status, body)
	}
	for _, point := range result.Results {
		events, late := 0, 0
		for _, record := range point.Records {
			if record.Kind == "quality_event" {
				events++
			}
			if record.Late {
				late++
			}
		}
		if point.NodeID == aPoint && (events != 1 || late != 1) {
			t.Fatalf("A lost its gap evidence: events=%d late=%d records=%+v", events, late, point.Records)
		}
		if point.NodeID == bPoint && (events != 0 || late != 0) {
			t.Fatalf("B inherited A quality evidence: events=%d late=%d", events, late)
		}
	}
}

func TestHistoryExposesQualityEventsLateSamplesAndInterval(t *testing.T) {
	base, token, service, point, device := historyRuntimePoint(t, 50)
	now := time.Now().UTC().Truncate(time.Second)
	observe := func(received time.Time, value float64) {
		service.Observe(received, []channel.Result{{DeviceID: device, PointID: point, Value: signal.Num(value), Quality: signal.Good, SourceTime: received, Unit: "C", TimeSource: "platform_acquisition"}})
	}
	observe(now.Add(-10*time.Second), 10)
	service.Observe(now.Add(-8*time.Second), []channel.Result{{DeviceID: device, PointID: point, Quality: signal.Invalid, Error: "timeout"}})
	service.Observe(now.Add(-7*time.Second), []channel.Result{{DeviceID: device, PointID: point, Quality: signal.Invalid, Error: "timeout"}})
	observe(now.Add(-5*time.Second), 50)
	service.Observe(now.Add(-4*time.Second), []channel.Result{{DeviceID: device, PointID: point, Value: signal.Num(20), Quality: signal.Good, SourceTime: now.Add(-9 * time.Second), Unit: "C", TimeSource: "platform_acquisition"}})

	query := url.Values{"node_id": []string{point}, "start": []string{now.Add(-20 * time.Second).Format(time.RFC3339)}, "end": []string{now.Format(time.RFC3339)}}
	var result struct {
		TotalMatched int `json:"total_matched"`
		Results      []struct {
			SampleIntervalMS int                 `json:"sample_interval_ms"`
			Records          []historyTestRecord `json:"records"`
		} `json:"results"`
	}
	status, body := call(t, http.MethodGet, base+"/history?"+query.Encode(), token, nil, &result)
	if status != 200 || len(result.Results) != 1 {
		t.Fatalf("history with events = %d %s", status, body)
	}
	records := result.Results[0].Records
	if result.Results[0].SampleIntervalMS != 1000 {
		t.Fatalf("declared sample interval = %d", result.Results[0].SampleIntervalMS)
	}
	var events, late []historyTestRecord
	for _, record := range records {
		if record.Kind == "quality_event" {
			events = append(events, record)
		}
		if record.Late {
			late = append(late, record)
		}
	}
	if len(events) != 1 || events[0].Reason != "timeout" || events[0].Quality != "invalid" || events[0].Value != nil || events[0].TimeSource != "attempt" || !events[0].SourceTime.Equal(now.Add(-8*time.Second)) {
		t.Fatalf("quality events = %+v", events)
	}
	if len(late) != 1 || late[0].Value == nil || *late[0].Value != 20 || !late[0].SourceTime.Equal(now.Add(-9*time.Second)) || late[0].Reason != "late_observation" {
		t.Fatalf("late samples = %+v", late)
	}

	query.Set("aggregate", "5m")
	result.Results = nil
	status, body = call(t, http.MethodGet, base+"/history?"+query.Encode(), token, nil, &result)
	if status != 200 || len(result.Results) != 1 {
		t.Fatalf("aggregate history with events = %d %s", status, body)
	}
	sawEvent := false
	for _, record := range result.Results[0].Records {
		if record.Kind == "quality_event" {
			sawEvent = true
			if record.Value != nil || record.Reason != "timeout" {
				t.Fatalf("aggregate changed a quality event: %+v", record)
			}
		}
	}
	if !sawEvent {
		t.Fatalf("aggregate dropped the invalid interval: %+v", result.Results[0].Records)
	}
}
