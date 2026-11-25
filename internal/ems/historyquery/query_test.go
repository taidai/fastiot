package historyquery

import (
	"errors"
	"testing"
	"time"

	"fastiot/internal/ems/history"
	"fastiot/internal/ems/signal"
	"github.com/simpleiot/simpleiot/data"
)

var testAt = time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)

type nodeMap map[string]Node

func (m nodeMap) Resolve(id string) (Node, error) {
	node, ok := m[id]
	if !ok {
		return Node{}, ErrForbidden
	}
	return node, nil
}

type siotCall struct {
	nodeID, typ, key string
	start, end       time.Time
	limit            int
}

type fakeSIOT struct {
	points []data.Point
	err    error
	calls  []siotCall
}

func (f *fakeSIOT) PointHistory(nodeID, typ, key string, start, end time.Time, limit int) ([]data.Point, error) {
	f.calls = append(f.calls, siotCall{nodeID: nodeID, typ: typ, key: key, start: start, end: end, limit: limit})
	if f.err != nil {
		return nil, f.err
	}
	return f.points, nil
}

type fakeRuntime struct {
	points     map[string]DevicePoint
	locateErr  error
	samples    map[string][]history.Sample
	start, end time.Time
	calls      int
}

func (f *fakeRuntime) Locate(pointID string) (string, int, bool, error) {
	if f.locateErr != nil {
		return "", 0, false, f.locateErr
	}
	point, ok := f.points[pointID]
	if !ok {
		return "", 0, false, nil
	}
	return point.DeviceID, point.IntervalMS, true, nil
}

func (f *fakeRuntime) Timeline(deviceID, pointID string, start, end time.Time) []history.Sample {
	f.calls++
	f.start, f.end = start, end
	return f.samples[pointID]
}

func siotValue(at time.Time, value float64, origin string) data.Point {
	point := data.NewPointFloat(data.PointTypeValue, "", value)
	point.Time = at
	point.Origin = origin
	return point
}

func runtimeValue(at time.Time, value float64, quality signal.Quality) history.Sample {
	return history.Sample{
		DeviceID: "dev", PointID: "point", Value: signal.Num(value), Quality: quality,
		SourceTime: at, ReceivedTime: at.Add(time.Millisecond), Unit: "C", Kind: history.KindObservation,
	}
}

func TestAggregatePreservesInvalidAndStaleEvidence(t *testing.T) {
	store := history.New(20)
	late := runtimeValue(testAt, 0, signal.Good)
	late.Late = true
	store.Record([]history.Sample{
		late,
		runtimeValue(testAt.Add(time.Second), 99, signal.Stale),
		runtimeValue(testAt.Add(2*time.Second), 100, signal.Invalid),
		runtimeValue(testAt.Add(3*time.Second), 4, signal.Good),
		runtimeValue(testAt.Add(5*time.Second), 101, signal.Uninitialized),
		runtimeValue(testAt.Add(6*time.Second), 102, signal.Quality(255)),
	})
	store.RecordEvents([]history.Sample{{DeviceID: "dev", PointID: "point", SourceTime: testAt.Add(4 * time.Second), Kind: history.KindQualityEvent, Quality: signal.Invalid, Reason: "connection_lost"}})
	query := Query{Nodes: nodeMap{"point": {ID: "point", Type: data.NodeTypeModbusIO}}, Runtime: &StoreRuntime{Store: store, Points: func() ([]DevicePoint, error) {
		return []DevicePoint{{PointID: "point", DeviceID: "dev", IntervalMS: 1000}}, nil
	}}}
	results, matched, err := query.Execute(Request{NodeIDs: []string{"point"}, Aggregate: 5 * time.Second, Export: true})
	if err != nil {
		t.Fatal(err)
	}
	records := results[0].Records
	if matched != 6 || len(records) != 6 {
		t.Fatalf("aggregation erased quality evidence: %+v", records)
	}
	if !records[0].Late {
		t.Fatal("mean hid its late contributor")
	}
	if records[0].Value == nil || *records[0].Value != 2 || records[0].SampleCount != 2 {
		t.Fatalf("mean includes invalid inputs: %+v", records[0])
	}
	if records[1].Quality != "stale" || records[2].Quality != "invalid" || records[3].Kind != "quality_event" {
		t.Fatalf("quality not preserved: %+v", records)
	}
	if records[4].Quality != "uninitialized" || records[5].Quality != "quality(255)" || records[4].Aggregate != "" || records[5].Aggregate != "" {
		t.Fatalf("uninitialized/unknown evidence lost: %+v", records)
	}
	if records[1].Aggregate != "" || !records[1].ReceivedTime.Equal(testAt.Add(time.Second+time.Millisecond)) {
		t.Fatal("raw quality evidence lost its original time")
	}
}

func TestExecuteSelectsSourcePerNode(t *testing.T) {
	runtimeNode := Node{ID: "point", Type: data.NodeTypeModbusIO, Units: "C"}
	siot := &fakeSIOT{points: []data.Point{siotValue(testAt, 1, "mqtt-adapter")}}
	runtime := &fakeRuntime{
		points:  map[string]DevicePoint{"point": {PointID: "point", DeviceID: "dev", IntervalMS: 5000}},
		samples: map[string][]history.Sample{"point": {runtimeValue(testAt, 2, signal.Good)}},
	}
	missing := &fakeRuntime{points: map[string]DevicePoint{}}

	tests := []struct {
		name         string
		node         Node
		runtime      Runtime
		wantSource   string
		wantDevice   string
		wantInterval int
		wantSIOTT    int
		wantOrigin   string
	}{
		{
			name: "runtime device answers modbus point", node: runtimeNode, runtime: runtime,
			wantSource: SourceRuntime, wantDevice: "dev", wantInterval: 5000, wantSIOTT: 0,
		},
		{
			name: "modbus point without runtime device falls back to SIOT", node: runtimeNode, runtime: missing,
			wantSource: SourceSIOT, wantSIOTT: 1, wantOrigin: "mqtt-adapter",
		},
		{
			name: "non modbus node reads SIOT", node: Node{ID: "point", Type: data.NodeTypeVariable}, runtime: runtime,
			wantSource: SourceSIOT, wantSIOTT: 1, wantOrigin: "mqtt-adapter",
		},
		{
			name: "no runtime adapter reads SIOT", node: runtimeNode, runtime: nil,
			wantSource: SourceSIOT, wantSIOTT: 1, wantOrigin: "mqtt-adapter",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := &fakeSIOT{points: siot.points}
			query := &Query{Nodes: nodeMap{"point": test.node}, SIOT: source, Runtime: test.runtime}
			results, matched, err := query.Execute(Request{NodeIDs: []string{"point"}})
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 || matched != 1 {
				t.Fatalf("results=%d matched=%d", len(results), matched)
			}
			result := results[0]
			if result.Source != test.wantSource || result.DeviceID != test.wantDevice || result.SampleIntervalMS != test.wantInterval {
				t.Fatalf("source=%q device=%q interval=%d", result.Source, result.DeviceID, result.SampleIntervalMS)
			}
			if len(source.calls) != test.wantSIOTT {
				t.Fatalf("SIOT calls=%d, want %d", len(source.calls), test.wantSIOTT)
			}
			if len(result.Records) != 1 {
				t.Fatalf("records=%d", len(result.Records))
			}
			if result.Records[0].Source != test.wantSource || result.Records[0].Origin != test.wantOrigin {
				t.Fatalf("record source=%q origin=%q", result.Records[0].Source, result.Records[0].Origin)
			}
		})
	}
}

func TestExecutePropagatesRuntimeLocateFailure(t *testing.T) {
	failure := errors.New("runtime lookup failed")
	runtime := &fakeRuntime{locateErr: failure}
	query := &Query{Nodes: nodeMap{"point": {ID: "point", Type: data.NodeTypeModbusIO}}, Runtime: runtime}
	_, _, err := query.Execute(Request{NodeIDs: []string{"point"}})
	if !errors.Is(err, failure) {
		t.Fatalf("err=%v, want runtime failure", err)
	}
}

func TestExecuteWithoutAnySourceReportsUnavailable(t *testing.T) {
	query := &Query{Nodes: nodeMap{"point": {ID: "point", Type: data.NodeTypeVariable}}}
	_, _, err := query.Execute(Request{NodeIDs: []string{"point"}})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err=%v, want ErrUnavailable", err)
	}
}

func TestExecuteForwardsFiltersAndWindowToSIOT(t *testing.T) {
	source := &fakeSIOT{}
	query := &Query{Nodes: nodeMap{"point": {ID: "point", Type: data.NodeTypeVariable}}, SIOT: source}
	start, end := testAt, testAt.Add(time.Hour)
	if _, _, err := query.Execute(Request{NodeIDs: []string{"point"}, Type: data.PointTypeValueSet, Key: "kw", Start: start, End: end}); err != nil {
		t.Fatal(err)
	}
	if len(source.calls) != 1 {
		t.Fatalf("SIOT calls=%d", len(source.calls))
	}
	call := source.calls[0]
	if call.nodeID != "point" || call.typ != data.PointTypeValueSet || call.key != "kw" || !call.start.Equal(start) || !call.end.Equal(end) || call.limit != PageLimit {
		t.Fatalf("SIOT read = %+v", call)
	}
}

func TestExecuteForwardsWindowToRuntime(t *testing.T) {
	runtime := &fakeRuntime{points: map[string]DevicePoint{"point": {PointID: "point", DeviceID: "dev"}}}
	query := &Query{Nodes: nodeMap{"point": {ID: "point", Type: data.NodeTypeModbusIO}}, Runtime: runtime}
	start, end := testAt, testAt.Add(time.Hour)
	if _, _, err := query.Execute(Request{NodeIDs: []string{"point"}, Start: start, End: end}); err != nil {
		t.Fatal(err)
	}
	if runtime.calls != 1 || !runtime.start.Equal(start) || !runtime.end.Equal(end) {
		t.Fatalf("runtime window = %v..%v calls=%d", runtime.start, runtime.end, runtime.calls)
	}
}

func TestStoreRuntimeHistoryHonoursTimeWindow(t *testing.T) {
	store := history.New(100)
	store.Record([]history.Sample{
		runtimeValue(testAt.Add(-time.Minute), 5, signal.Good),
		runtimeValue(testAt, 10, signal.Good),
		runtimeValue(testAt.Add(time.Minute), 20, signal.Good),
	})
	runtime := &StoreRuntime{Store: store, Points: func() ([]DevicePoint, error) {
		return []DevicePoint{{PointID: "point", DeviceID: "dev", IntervalMS: 1000}}, nil
	}}
	query := &Query{Nodes: nodeMap{"point": {ID: "point", Type: data.NodeTypeModbusIO}}, Runtime: runtime}
	results, matched, err := query.Execute(Request{NodeIDs: []string{"point"}, Start: testAt, End: testAt.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if matched != 2 || len(results[0].Records) != 2 {
		t.Fatalf("matched=%d records=%d", matched, len(results[0].Records))
	}
	if *results[0].Records[0].Value != 10 || *results[0].Records[1].Value != 20 {
		t.Fatalf("window kept %v..%v", *results[0].Records[0].Value, *results[0].Records[1].Value)
	}
}

func TestExecuteCursorPagesNewestPageFirst(t *testing.T) {
	points := make([]data.Point, 0, 5)
	for i := 0; i < 5; i++ {
		points = append(points, siotValue(testAt.Add(time.Duration(i)*time.Second), float64(i), "adapter"))
	}
	query := &Query{Nodes: nodeMap{"point": {ID: "point", Type: data.NodeTypeVariable}}, SIOT: &fakeSIOT{points: points}}

	first, matched, err := query.Execute(Request{NodeIDs: []string{"point"}, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	page := first[0]
	if matched != 5 || page.Matched != 5 || page.Returned != 2 || !page.Truncated || page.NextBeforeCount != 0 {
		t.Fatalf("first page = %+v total=%d", page, matched)
	}
	if !page.NextBefore.Equal(testAt.Add(3*time.Second)) || *page.Records[0].Value != 3 || *page.Records[1].Value != 4 {
		t.Fatalf("first page records = %+v next=%v", page.Records, page.NextBefore)
	}

	second, _, err := query.Execute(Request{NodeIDs: []string{"point"}, Limit: 2, Cursor: Cursor{Before: page.NextBefore, Count: page.NextBeforeCount}})
	if err != nil {
		t.Fatal(err)
	}
	page = second[0]
	if page.Returned != 2 || !page.Truncated || *page.Records[0].Value != 1 || *page.Records[1].Value != 2 {
		t.Fatalf("second page = %+v", page)
	}

	third, _, err := query.Execute(Request{NodeIDs: []string{"point"}, Limit: 2, Cursor: Cursor{Before: page.NextBefore, Count: page.NextBeforeCount}})
	if err != nil {
		t.Fatal(err)
	}
	page = third[0]
	if page.Returned != 1 || page.Truncated || page.NextBeforeCount != 0 || *page.Records[0].Value != 0 {
		t.Fatalf("third page = %+v", page)
	}
}

func TestExecuteCursorKeepsIdenticalTimesAtBoundary(t *testing.T) {
	points := data.Points{
		siotValue(testAt, 1, "a"),
		siotValue(testAt, 2, "b"),
		siotValue(testAt, 3, "c"),
	}
	query := &Query{Nodes: nodeMap{"point": {ID: "point", Type: data.NodeTypeVariable}}, SIOT: &fakeSIOT{points: points}}
	first, _, err := query.Execute(Request{NodeIDs: []string{"point"}, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	page := first[0]
	if page.Returned != 2 || !page.Truncated || page.NextBeforeCount != 1 || *page.Records[0].Value != 2 || *page.Records[1].Value != 3 {
		t.Fatalf("first identical page = %+v", page)
	}
	second, _, err := query.Execute(Request{NodeIDs: []string{"point"}, Limit: 2, Cursor: Cursor{Before: page.NextBefore, Count: page.NextBeforeCount}})
	if err != nil {
		t.Fatal(err)
	}
	if second[0].Returned != 1 || second[0].Truncated || *second[0].Records[0].Value != 1 {
		t.Fatalf("second identical page = %+v", second[0])
	}
}

func TestExecuteAggregatesRuntimeRecordsPerBucket(t *testing.T) {
	event := history.Sample{
		DeviceID: "dev", PointID: "point", Quality: signal.Stale,
		SourceTime: testAt.Add(7 * time.Minute), Kind: history.KindQualityEvent,
	}
	runtime := &fakeRuntime{
		points: map[string]DevicePoint{"point": {PointID: "point", DeviceID: "dev"}},
		samples: map[string][]history.Sample{"point": {
			runtimeValue(testAt, 2, signal.Good),
			runtimeValue(testAt.Add(4*time.Minute), 4, signal.Good),
			runtimeValue(testAt.Add(5*time.Minute), 8, signal.Good),
			runtimeValue(testAt.Add(6*time.Minute), 9, signal.Stale),
			event,
		}},
	}
	query := &Query{Nodes: nodeMap{"point": {ID: "point", Type: data.NodeTypeModbusIO, Units: "C"}}, Runtime: runtime}
	results, _, err := query.Execute(Request{NodeIDs: []string{"point"}, Aggregate: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	records := results[0].Records
	if len(records) != 4 {
		t.Fatalf("aggregate records = %+v", records)
	}
	first := records[0]
	if first.Value == nil || *first.Value != 3 || first.SampleCount != 2 || first.Aggregate != "5m0s" ||
		!first.SourceTime.Equal(testAt) || !first.BucketEnd.Equal(testAt.Add(5*time.Minute)) ||
		first.Quality != "good" || first.Unit != "C" || first.Source != SourceRuntime {
		t.Fatalf("first bucket = %+v", first)
	}
	if records[1].Value == nil || *records[1].Value != 8 || records[1].SampleCount != 1 {
		t.Fatalf("second bucket = %+v", records[1])
	}
	if records[2].Kind != history.KindObservation || records[2].Quality != "stale" {
		t.Fatalf("stale observation discarded: %+v", records[2])
	}
	if records[3].Kind != history.KindQualityEvent || records[3].Quality != "stale" || !records[3].SourceTime.Equal(event.SourceTime) {
		t.Fatalf("quality event not carried through = %+v", records[3])
	}
}

func TestExecuteAggregatesFallbackRecordsWithoutQuality(t *testing.T) {
	points := data.Points{
		siotValue(testAt, 2, "adapter"),
		siotValue(testAt.Add(4*time.Minute), 4, "adapter"),
	}
	query := &Query{Nodes: nodeMap{"point": {ID: "point", Type: data.NodeTypeModbusIO, Units: "C"}}, SIOT: &fakeSIOT{points: points}}
	results, _, err := query.Execute(Request{NodeIDs: []string{"point"}, Aggregate: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	records := results[0].Records
	if len(records) != 1 || records[0].Value == nil || *records[0].Value != 3 || records[0].SampleCount != 2 ||
		records[0].Source != SourceSIOT || records[0].Unit != "C" {
		t.Fatalf("fallback bucket = %+v", records)
	}
}

func TestExecuteReportsMatchedBeforePaginationForQueryAndExport(t *testing.T) {
	points := make([]data.Point, 0, 5)
	for i := 0; i < 5; i++ {
		points = append(points, siotValue(testAt.Add(time.Duration(i)*time.Second), float64(i), "adapter"))
	}
	newQuery := func() *Query {
		return &Query{Nodes: nodeMap{"point": {ID: "point", Type: data.NodeTypeVariable}}, SIOT: &fakeSIOT{points: points}}
	}
	request := Request{NodeIDs: []string{"point"}, Limit: 2, Cursor: Cursor{Before: testAt.Add(3 * time.Second), Count: 0}}

	page, pageTotal, err := newQuery().Execute(request)
	if err != nil {
		t.Fatal(err)
	}
	request.Export = true
	export, exportTotal, err := newQuery().Execute(request)
	if err != nil {
		t.Fatal(err)
	}
	if pageTotal != 5 || page[0].Matched != 5 {
		t.Fatalf("query reported matched=%d result=%d, want 5", pageTotal, page[0].Matched)
	}
	if exportTotal != 5 || export[0].Matched != 5 {
		t.Fatalf("export reported matched=%d result=%d, want the same pre-cursor total 5", exportTotal, export[0].Matched)
	}
	if export[0].Returned != 3 || export[0].Truncated {
		t.Fatalf("export page = %+v", export[0])
	}
	if page[0].Returned != 2 || !page[0].Truncated {
		t.Fatalf("query page = %+v", page[0])
	}
}

func TestExecuteExportRejectsMoreThanExportLimit(t *testing.T) {
	points := make([]data.Point, 0, ExportLimit+1)
	for i := 0; i < ExportLimit+1; i++ {
		points = append(points, siotValue(testAt.Add(time.Duration(i)*time.Second), float64(i), "adapter"))
	}
	query := &Query{Nodes: nodeMap{"point": {ID: "point", Type: data.NodeTypeVariable}}, SIOT: &fakeSIOT{points: points}}
	_, _, err := query.Execute(Request{NodeIDs: []string{"point"}, Start: testAt, Export: true})
	if !errors.Is(err, ErrExportLimitExceeded) {
		t.Fatalf("err=%v, want ErrExportLimitExceeded", err)
	}
}

func TestExecuteExportAllowsExactlyExportLimit(t *testing.T) {
	points := make([]data.Point, 0, ExportLimit)
	for i := 0; i < ExportLimit; i++ {
		points = append(points, siotValue(testAt.Add(time.Duration(i)*time.Second), float64(i), "adapter"))
	}
	query := &Query{Nodes: nodeMap{"point": {ID: "point", Type: data.NodeTypeVariable}}, SIOT: &fakeSIOT{points: points}}
	results, matched, err := query.Execute(Request{NodeIDs: []string{"point"}, Start: testAt, Export: true})
	if err != nil {
		t.Fatal(err)
	}
	if matched != ExportLimit || results[0].Returned != ExportLimit || results[0].Truncated {
		t.Fatalf("matched=%d returned=%d truncated=%v", matched, results[0].Returned, results[0].Truncated)
	}
}

func TestExecuteRejectsInvalidRequests(t *testing.T) {
	query := &Query{Nodes: nodeMap{"point": {ID: "point", Type: data.NodeTypeVariable}}}
	if _, _, err := query.Execute(Request{}); !errors.Is(err, ErrNodeIDRequired) {
		t.Fatalf("empty node list err=%v", err)
	}
	if _, _, err := query.Execute(Request{NodeIDs: []string{"point"}, Start: testAt.Add(time.Hour), End: testAt}); !errors.Is(err, ErrInvalidTimeRange) {
		t.Fatalf("reversed window err=%v", err)
	}
	ids := make([]string, ExportLimit/PageLimit+1)
	for i := range ids {
		ids[i] = "point"
	}
	if _, _, err := query.Execute(Request{NodeIDs: ids}); !errors.Is(err, ErrTooManyPoints) {
		t.Fatalf("too many nodes err=%v", err)
	}
}
