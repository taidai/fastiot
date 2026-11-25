package appapi

import (
	"context"
	"encoding/json"
	"fastiot/internal/ems/bridge"
	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/historyquery"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/emsapi"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

func startAggregateLoop(t *testing.T, h *Handler) func() {
	t.Helper()
	subscriptions := h.nc.NumSubscriptions()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.RunAggregates(ctx) }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("aggregate sampler did not stop")
		}
		if h.nc.NumSubscriptions() != subscriptions {
			t.Error("aggregate subscription leaked")
		}
	}
	t.Cleanup(stop)
	return stop
}

func TestAggregateExcludesUnusableAndUnprovenObservations(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, token)
	group := createNodeAPI(t, base, token, "group", root, "quality", 0)
	good := createNodeAPI(t, base, token, "variable", group, "good", 7)
	members := []string{good}
	stale := data.NewPointFloat("value", "", 90)
	stale.Time = time.Now().Add(-time.Minute)
	future := data.NewPointFloat("value", "", 90)
	future.Time = time.Now().Add(time.Minute)
	deleted := data.NewPointFloat("value", "", 90)
	deleted.Tombstone = 1
	for _, points := range []data.Points{{stale}, {future}, {deleted}, {data.NewPointString("value", "", "invalid")}, nil} {
		id := data.NewID()
		if err := client.SendNode(h.nc, data.NodeEdge{ID: id, Parent: group, Type: data.NodeTypeVariable, Points: points}, ""); err != nil {
			t.Fatal(err)
		}
		members = append(members, id)
	}
	physical := data.NewID()
	if err := client.SendNode(h.nc, data.NodeEdge{ID: physical, Parent: group, Type: data.NodeTypeModbusIO, Points: data.Points{data.NewPointFloat("value", "", 90)}}, ""); err != nil {
		t.Fatal(err)
	}
	members = append(members, physical)
	config := AggregateConfig{Group: group, Op: "sum", Members: members, Quality: "count"}
	status, body := call(t, http.MethodPut, base+"/aggregate", token, config, nil)
	if status != 200 {
		t.Fatal(status, body)
	}
	startAggregateLoop(t, h)
	var result AggregateResult
	waitFor(t, func() bool {
		call(t, http.MethodGet, base+"/aggregate?group="+group, token, nil, &result)
		return !result.EvaluatedAt.IsZero()
	}, "aggregate quality")
	if result.Value == nil || *result.Value != 7 || result.Valid != 1 || result.Total != 7 || result.Quality != "partial" {
		t.Fatalf("unusable observation contributed: %+v", result)
	}
	for _, member := range result.Members {
		if member.NodeID != good && member.Value != nil {
			t.Fatalf("unusable member exposes a usable scalar: %+v", member)
		}
	}
	if err := client.SendNodePoint(h.nc, good, data.NewPointFloat("disabled", "", 1), true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		call(t, http.MethodGet, base+"/aggregate?group="+group, token, nil, &result)
		return result.Quality == "offline"
	}, "all members unavailable")
	if result.Value != nil || result.Valid != 0 {
		t.Fatalf("missing aggregate became zero: %+v", result)
	}
	nodes, err := client.GetNodes(h.nc, "all", group, "", false)
	if err != nil {
		t.Fatal(err)
	}
	projection, ok := nodes[0].Points.Find(aggregateValuePoint, "")
	if !ok || projection.Tombstone%2 == 0 {
		t.Fatalf("old numeric result still live: %+v", projection)
	}
	var history struct {
		Results []historyquery.PointResult `json:"results"`
	}
	status, body = call(t, http.MethodGet, base+"/history?node_id="+group+"&type="+aggregateValuePoint, token, nil, &history)
	if status != 200 || len(history.Results) != 1 {
		t.Fatal(status, body)
	}
	found := false
	for _, record := range history.Results[0].Records {
		if record.Quality == "offline" {
			found = true
			if record.Value != nil || record.Kind != "quality_event" {
				t.Fatalf("offline history fabricated a number: %+v", record)
			}
		}
	}
	if !found {
		t.Fatal("missing quality event not in historical lookup")
	}
	if err := client.SendNodePoints(h.nc, good, data.Points{data.NewPointFloat("disabled", "", 0), data.NewPointFloat("value", "", 8)}, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		call(t, http.MethodGet, base+"/aggregate?group="+group, token, nil, &result)
		return result.Value != nil && *result.Value == 8
	}, "aggregate recovery")
	nodes, err = client.GetNodes(h.nc, "all", group, "", false)
	if err != nil {
		t.Fatal(err)
	}
	projection, _ = nodes[0].Points.Find(aggregateValuePoint, "")
	if projection.Tombstone%2 != 0 {
		t.Fatal("recovered numeric projection stayed tombstoned")
	}
}

func TestAggregateUsesRuntimeAcquisitionQualityWithoutDeviceWrites(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	source := bridge.NewNodeSource(h.nc)
	api := emsapi.New(service, time.Minute)
	api.Buses = source.Buses
	h.SetEMS(api)
	f := &pointConfigFixture{base: base, token: token}
	busID := f.createBus(t, "aggregate source")
	pointID := f.createPoint(t, busID, ModbusIOInput{Description: "power", ServerID: 1, Address: 10, IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1, ReadOnly: true, Units: "kW"})
	configs, err := source.Buses()
	if err != nil {
		t.Fatal(err)
	}
	bus := &pointConfigBus{}
	ch, err := channel.New(configs[0].ChannelID, bus, configs[0].Devices)
	if err != nil {
		t.Fatal(err)
	}
	readings, _ := ch.RunOnce(context.Background(), time.Now)
	service.Observe(time.Now(), readings)
	root := snapshotRoot(t, base, token)
	config := AggregateConfig{Group: root, Op: "sum", Members: []string{pointID}, Units: "kW"}
	status, body := call(t, http.MethodPut, base+"/aggregate", token, config, nil)
	if status != 200 {
		t.Fatal(status, body)
	}
	startAggregateLoop(t, h)
	var result AggregateResult
	waitFor(t, func() bool {
		call(t, http.MethodGet, base+"/aggregate?group="+root, token, nil, &result)
		return result.Value != nil
	}, "runtime aggregate")
	if *result.Value != 37 || result.Time == nil || !result.Time.Equal(readings[0].SourceTime) || result.Members[0].TimeSource != "platform_collection" {
		t.Fatalf("runtime source evidence lost: %+v", result)
	}
	bus.mu.Lock()
	bus.offline = true
	bus.mu.Unlock()
	readings, _ = ch.RunOnce(context.Background(), time.Now)
	service.Observe(time.Now(), readings)
	waitFor(t, func() bool {
		call(t, http.MethodGet, base+"/aggregate?group="+root, token, nil, &result)
		return result.Value == nil && result.Quality == "offline"
	}, "runtime failed acquisition")
	config.Window = "2s"
	status, body = call(t, http.MethodPut, base+"/aggregate", token, config, nil)
	if status != 200 {
		t.Fatal(status, body)
	}
	start := time.Now().UTC().Truncate(2 * time.Second).Add(2 * time.Second)
	end := start.Add(2 * time.Second)
	time.Sleep(time.Until(start.Add(100 * time.Millisecond)))
	bus.mu.Lock()
	bus.offline = false
	bus.mu.Unlock()
	readings, _ = ch.RunOnce(context.Background(), time.Now)
	service.Observe(time.Now(), readings)
	bus.mu.Lock()
	bus.offline = true
	bus.mu.Unlock()
	readings, _ = ch.RunOnce(context.Background(), time.Now)
	service.Observe(time.Now(), readings)
	waitFor(t, func() bool {
		call(t, http.MethodGet, base+"/aggregate?group="+root, token, nil, &result)
		return result.Window != nil && result.Window.End.Equal(end)
	}, "physical closed window")
	if result.Value == nil || *result.Value != 37 || result.Quality != "partial" || result.Members[0].RejectedSamples == 0 {
		t.Fatalf("failed samples lost or counted as zero: %+v", result)
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.writes) != 0 {
		t.Fatalf("aggregate wrote device: %v", bus.writes)
	}
}

func TestAggregateReadsAndConfigurationAreScoped(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, token)
	group := createNodeAPI(t, base, token, "group", root, "A", 0)
	outside := createNodeAPI(t, base, token, "group", root, "B", 0)
	member := createNodeAPI(t, base, token, "variable", group, "source", 7)
	foreign := createNodeAPI(t, base, token, "variable", outside, "private", 99)
	config := AggregateConfig{Group: group, Op: "sum", Members: []string{member}}
	status, body := call(t, http.MethodPut, base+"/aggregate", token, config, nil)
	if status != 200 {
		t.Fatal(status, body)
	}
	startAggregateLoop(t, h)
	viewer := inputAccount(t, &pointConfigFixture{base: base, token: token}, group, "viewer", []string{ActionExport})
	status, body = call(t, http.MethodGet, base+"/aggregate?group="+group, viewer, nil, nil)
	if status != 200 {
		t.Fatal(status, body)
	}
	for _, path := range []string{"/aggregate?group=" + outside, "/history?node_id=" + outside + "&type=" + aggregateValuePoint, "/history/export?node_id=" + outside + "&type=" + aggregateValuePoint} {
		status, body = call(t, http.MethodGet, base+path, viewer, nil, nil)
		if status != http.StatusForbidden {
			t.Fatalf("outside read accepted: %s %d %s", path, status, body)
		}
	}
	status, body = call(t, http.MethodPut, base+"/aggregate", viewer, config, nil)
	if status != http.StatusForbidden {
		t.Fatalf("viewer configured sampler: %d %s", status, body)
	}
	config.Members = []string{foreign}
	status, body = call(t, http.MethodPut, base+"/aggregate", token, config, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("outside member accepted: %d %s", status, body)
	}
	status, body = call(t, http.MethodPost, base+"/nodes/"+group+"/value", token, map[string]any{"value": 99, "point_type": aggregateValuePoint}, nil)
	if status != http.StatusForbidden {
		t.Fatalf("aggregate scalar write: %d %s", status, body)
	}
	var result AggregateResult
	waitFor(t, func() bool {
		call(t, http.MethodGet, base+"/aggregate?group="+group, viewer, nil, &result)
		return result.Value != nil
	}, "authorized member evidence")
	if err := client.MoveNode(h.nc, member, group, outside, ""); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/aggregate?group=" + group, "/history?node_id=" + group + "&type=" + aggregateValuePoint, "/history?node_id=" + group + "&type=" + aggregateValuePoint + "&aggregate=5m", "/history/export?node_id=" + group + "&type=" + aggregateValuePoint} {
		status, body = call(t, http.MethodGet, base+path, viewer, nil, nil)
		if status != http.StatusForbidden || strings.Contains(body, member) {
			t.Fatalf("member evidence leaked after leaving scope: %s %d %s", path, status, body)
		}
	}
	status, body = call(t, http.MethodGet, base+"/snapshot", viewer, nil, nil)
	if status != 200 || strings.Contains(body, member) {
		t.Fatalf("snapshot leaked restricted aggregate metadata: %d %s", status, body)
	}
	status, body = call(t, http.MethodGet, base+"/aggregate?group="+group, token, nil, &result)
	if status != 200 || !result.Configured || len(result.Config.Members) != 1 || result.Config.Members[0] != member {
		t.Fatalf("restricted read polluted admin evidence: %d %s", status, body)
	}
	for _, node := range snapshotNodes(t, base, token) {
		if node.ID == group {
			if _, ok := node.Points.Find(aggregatePoint, ""); !ok {
				t.Fatal("restricted snapshot removed shared definition")
			}
			if _, ok := node.Points.Find(aggregateResultPoint, ""); !ok {
				t.Fatal("restricted snapshot removed shared evidence")
			}
		}
	}
}

func TestAggregateQualityChangeKeepsObservationTime(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, token)
	group := createNodeAPI(t, base, token, "group", root, "quality transition", 0)
	older := createNodeAPI(t, base, token, "variable", group, "older", 5)
	latest := createNodeAPI(t, base, token, "variable", group, "latest", 7)
	config := AggregateConfig{Group: group, Op: "sum", Members: []string{older, latest}}
	status, body := call(t, http.MethodPut, base+"/aggregate", token, config, nil)
	if status != 200 {
		t.Fatal(status, body)
	}
	startAggregateLoop(t, h)
	var result AggregateResult
	waitFor(t, func() bool {
		call(t, http.MethodGet, base+"/aggregate?group="+group, token, nil, &result)
		return result.Value != nil && *result.Value == 12
	}, "complete aggregate")
	observed, evaluated := *result.Time, result.EvaluatedAt
	if err := client.SendNodePoint(h.nc, older, data.NewPointFloat("disabled", "", 1), true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		call(t, http.MethodGet, base+"/aggregate?group="+group, token, nil, &result)
		return result.Value != nil && *result.Value == 7 && result.Quality == "partial"
	}, "quality transition")
	if result.Time == nil || !result.Time.Equal(observed) || !result.EvaluatedAt.After(evaluated) {
		t.Fatalf("quality transition refreshed observation time: %+v", result)
	}
	nodes, err := client.GetNodes(h.nc, "all", group, "", false)
	if err != nil {
		t.Fatal(err)
	}
	scalar, _ := nodes[0].Points.Find(aggregateValuePoint, "")
	value, err := scalar.ValueFloat()
	if err != nil || value != 7 {
		t.Fatalf("same-source-time projection kept old scalar: %v %v", value, err)
	}
	var history struct {
		Results []historyquery.PointResult `json:"results"`
	}
	status, body = call(t, http.MethodGet, base+"/history?node_id="+group+"&type="+aggregateValuePoint, token, nil, &history)
	if status != 200 {
		t.Fatal(status, body)
	}
	good, partial := false, false
	for _, record := range history.Results[0].Records {
		if record.SourceTime.Equal(observed) && record.Value != nil {
			good = good || record.Quality == "good" && *record.Value == 12
			partial = partial || record.Quality == "partial" && *record.Value == 7
		}
	}
	if !good || !partial {
		t.Fatalf("same-time quality history was overwritten: %+v", history)
	}
	for _, node := range snapshotNodes(t, base, token) {
		if node.ID == group {
			projection, ok := node.Points.Find(aggregateValuePoint, "")
			if !ok || !projection.Time.Equal(observed) {
				t.Fatalf("public snapshot exposed storage clock as observation: %+v", projection)
			}
		}
	}
}

func TestAggregateRuntimeEvidenceStaysOutOfProjectExports(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, token)
	site, err := CreateDomainNode(h.nc, root, NodeTypeSite, map[string]string{"description": "aggregate fixture"})
	if err != nil {
		t.Fatal(err)
	}
	group := createNodeAPI(t, base, token, "group", site, "aggregate", 0)
	member := createNodeAPI(t, base, token, "variable", group, "source", 7)
	status, body := call(t, http.MethodPut, base+"/aggregate", token, AggregateConfig{Group: group, Op: "sum", Members: []string{member}}, nil)
	if status != 200 {
		t.Fatal(status, body)
	}
	stopSampling := startAggregateLoop(t, h)
	var result AggregateResult
	waitFor(t, func() bool {
		call(t, http.MethodGet, base+"/aggregate?group="+group, token, nil, &result)
		return result.Value != nil
	}, "aggregate evidence")
	var exported struct {
		Nodes []struct {
			Points data.Points `json:"points"`
		} `json:"nodes"`
	}
	status, body = call(t, http.MethodGet, base+"/projects/export?site="+site, token, nil, &exported)
	if status != 200 {
		t.Fatal(status, body)
	}
	definition := false
	for _, node := range exported.Nodes {
		for _, point := range node.Points {
			if point.Type == aggregateValuePoint || point.Type == aggregateResultPoint {
				t.Fatalf("runtime evidence escaped as project configuration: %s", point.Type)
			}
			definition = definition || point.Type == aggregatePoint
		}
	}
	if !definition {
		t.Fatal("export lost aggregate definition")
	}
	stopSampling()
	var copied struct {
		ID string `json:"id"`
	}
	status, body = call(t, http.MethodPost, base+"/nodes/"+group+"/copy", token, map[string]string{"parent": site, "description": "aggregate copy"}, &copied)
	if status != 200 || copied.ID == "" {
		t.Fatal(status, body)
	}
	nodes, err := client.GetNodes(h.nc, "all", copied.ID, "", false)
	if err != nil || len(nodes) != 1 {
		t.Fatal(err)
	}
	for _, point := range nodes[0].Points {
		if point.Type == aggregateResultPoint || point.Type == aggregateValuePoint {
			t.Fatalf("copy inherited runtime evidence: %s", point.Type)
		}
	}
	if _, ok := nodes[0].Points.Find(aggregatePoint, ""); !ok {
		t.Fatal("copy lost aggregate definition")
	}
	status, body = call(t, http.MethodGet, base+"/aggregate?group="+copied.ID, token, nil, &result)
	if status != 200 || result.Value != nil || result.Quality != "pending" {
		t.Fatalf("copy presented old result: %d %s", status, body)
	}
}

func TestAggregateWindowPagesMemberHistory(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, token)
	group := createNodeAPI(t, base, token, "group", root, "restored hourly aggregate", 0)
	member := data.NewID()
	if err := client.SendNode(h.nc, data.NodeEdge{ID: member, Parent: group, Type: data.NodeTypeVariable, Points: data.Points{data.NewPointString("description", "", "hourly observations")}}, ""); err != nil {
		t.Fatal(err)
	}
	end := time.Now().UTC().Truncate(time.Hour)
	start := end.Add(-time.Hour)
	samples := make(data.Points, 2002)
	for i := range samples {
		value := 4.0
		if i%2 == 1 {
			value = 6
		}
		samples[i] = data.NewPointFloat("value", "", value)
		samples[i].Time = start.Add(time.Duration(i+1) * time.Second)
	}
	if err := client.SendNodePoints(h.nc, member, samples, true); err != nil {
		t.Fatal(err)
	}
	config := AggregateConfig{Group: group, Op: "sum", Members: []string{member}, Window: "1h"}
	encoded, _ := json.Marshal(config)
	definition := data.NewPointString(aggregatePoint, "", string(encoded))
	definition.Time = start.Add(-time.Second)
	if err := client.SendNodePoint(h.nc, group, definition, true); err != nil {
		t.Fatal(err)
	}
	startAggregateLoop(t, h)
	var result AggregateResult
	waitFor(t, func() bool {
		call(t, http.MethodGet, base+"/aggregate?group="+group, token, nil, &result)
		return result.Window != nil
	}, "hourly window")
	if !result.Window.End.Equal(end) || result.Value == nil || *result.Value != 5 || result.Quality != "good" || result.Members[0].Samples != 2002 {
		t.Fatalf("window truncated member history: %+v", result)
	}
}

func TestAggregateClosesWindowsOnce(t *testing.T) {
	dir, password := t.TempDir(), data.NewID()
	base, token, h, stopServer := startInputServer(t, dir, password)
	root := snapshotRoot(t, base, token)
	group := createNodeAPI(t, base, token, "group", root, "window aggregate", 0)
	member := createNodeAPI(t, base, token, "variable", group, "sampled", 99)
	missing := createNodeAPI(t, base, token, "variable", group, "no window observations", 99)
	stopSampling := startAggregateLoop(t, h)
	config := AggregateConfig{Group: group, Op: "sum", Members: []string{member, missing}, Window: "2s", Quality: "count"}
	status, body := call(t, http.MethodPut, base+"/aggregate", token, config, nil)
	if status != 200 {
		t.Fatal(status, body)
	}
	start := time.Now().UTC().Truncate(2 * time.Second).Add(2 * time.Second)
	end := start.Add(2 * time.Second)
	time.Sleep(time.Until(start.Add(100 * time.Millisecond)))
	for _, value := range []float64{4, 6} {
		if err := client.SendNodePoint(h.nc, member, data.NewPointFloat("value", "", value), true); err != nil {
			t.Fatal(err)
		}
	}
	var result AggregateResult
	waitFor(t, func() bool {
		call(t, http.MethodGet, base+"/aggregate?group="+group, token, nil, &result)
		return result.Window != nil && result.Window.End.Equal(end)
	}, "deterministically closed aggregate window")
	if !result.Window.Start.Equal(start) || result.Time == nil || !result.Time.Equal(end) || result.Value == nil || *result.Value != 5 || result.Quality != "partial" || result.Valid != 1 || result.Total != 2 {
		t.Fatalf("closed window evidence: %+v", result)
	}
	evaluated := result.EvaluatedAt
	late := data.NewPointFloat("value", "", 100)
	late.Time = start.Add(300 * time.Millisecond)
	if err := client.SendNodePoint(h.nc, member, late, true); err != nil {
		t.Fatal(err)
	}
	call(t, http.MethodGet, base+"/aggregate?group="+group, token, nil, &result)
	if !result.EvaluatedAt.Equal(evaluated) || result.Value == nil || *result.Value != 5 {
		t.Fatalf("closed evidence rewritten: %+v", result)
	}
	query := url.Values{"node_id": {group}, "type": {aggregateValuePoint}, "start": {end.Format(time.RFC3339Nano)}, "end": {end.Format(time.RFC3339Nano)}}
	var history struct {
		Results []historyquery.PointResult `json:"results"`
	}
	status, body = call(t, http.MethodGet, base+"/history?"+query.Encode(), token, nil, &history)
	if status != 200 || len(history.Results) != 1 || len(history.Results[0].Records) != 1 {
		t.Fatalf("closed-window lookup: %d %s", status, body)
	}
	record := history.Results[0].Records[0]
	var evidence AggregateResult
	if err := json.Unmarshal([]byte(record.Text), &evidence); err != nil || evidence.Window == nil || !evidence.Window.Start.Equal(start) || !evidence.Window.End.Equal(end) || evidence.Valid != 1 || record.Quality != "partial" || !record.SourceTime.Equal(end) || record.Value == nil || *record.Value != 5 {
		t.Fatalf("window history lost evidence: %+v %v", record, err)
	}
	status, body = call(t, http.MethodGet, base+"/history/export?"+query.Encode(), token, nil, nil)
	if status != 200 || !strings.Contains(body, "partial") || !strings.Contains(body, "window_end") {
		t.Fatalf("aggregate export: %d %s", status, body)
	}
	stopSampling()
	stopServer()
	base, token, h, _ = startInputServer(t, dir, password)
	startAggregateLoop(t, h)
	time.Sleep(1100 * time.Millisecond)
	status, body = call(t, http.MethodGet, base+"/history?"+query.Encode(), token, nil, &history)
	if status != 200 || len(history.Results) != 1 || len(history.Results[0].Records) != 1 {
		t.Fatalf("restart duplicated or lost closed window: %d %s", status, body)
	}
	if err := json.Unmarshal([]byte(history.Results[0].Records[0].Text), &evidence); err != nil || !evidence.EvaluatedAt.Equal(evaluated) || evidence.Value == nil || *evidence.Value != 5 {
		t.Fatalf("restart rewrote closed evidence with late observations: %+v %v", evidence, err)
	}
	call(t, http.MethodGet, base+"/aggregate?group="+group, token, nil, &result)
	if !result.Configured || result.Config.Window != "2s" {
		t.Fatalf("window definition not recovered: %+v", result)
	}
}

// Reading a definition/result must not manufacture observations or history.
func TestAggregateReadsDoNotProduceSamples(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, token)
	group := createNodeAPI(t, base, token, "group", root, "aggregate", 0)
	member := createNodeAPI(t, base, token, "variable", group, "power", 7)
	config := AggregateConfig{Group: group, Op: "sum", Members: []string{member}, Units: "kW"}
	status, body := call(t, http.MethodPut, base+"/aggregate", token, config, nil)
	if status != http.StatusOK {
		t.Fatal(status, body)
	}
	for i := 0; i < 3; i++ {
		status, body = call(t, http.MethodGet, base+"/aggregate?group="+group, token, nil, nil)
		if status != http.StatusOK {
			t.Fatal(status, body)
		}
	}
	if err := h.nc.Flush(); err != nil {
		t.Fatal(err)
	}
	nodes, err := client.GetNodes(h.nc, "all", group, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := nodes[0].Points.Find("easyemsAggregateValue", ""); ok {
		t.Fatal("HTTP save/read produced an aggregate sample without a running sampler")
	}
	points, err := h.users.(historyReader).PointHistory(group, "easyemsAggregateValue", "", time.Time{}, time.Now(), 100)
	if err != nil || len(points) != 0 {
		t.Fatalf("HTTP manufactured history: %v %v", points, err)
	}
	if err := client.SendNodePoint(h.nc, group, data.NewPointFloat(aggregateValuePoint, "", 999), true); err != nil {
		t.Fatal(err)
	}
	var result AggregateResult
	status, body = call(t, http.MethodGet, base+"/aggregate?group="+group, token, nil, &result)
	if status != 200 || result.Value != nil || result.Quality != "pending" {
		t.Fatalf("bare scalar fabricated evidence: %d %s", status, body)
	}
	for _, node := range snapshotNodes(t, base, token) {
		if node.ID == group {
			if _, ok := node.Points.Find(aggregateValuePoint, ""); ok {
				t.Fatal("snapshot exposed bare scalar without evidence")
			}
		}
	}
}
