package appapi

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"fastiot/internal/ems/bridge"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/emsapi"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

// A Modbus IO point that exists only as configuration (no runtime device
// channel) reads its records back from SIOT. Those records carry no quality,
// which means "not declared unavailable", so aggregation must keep them
// instead of silently emptying the bucket.
func TestModbusPointWithoutRuntimeDeviceAggregatesSIOTFallback(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 2000)
	t.Cleanup(service.Close)
	source := bridge.NewNodeSource(h.nc)
	api := emsapi.New(service, time.Minute)
	api.Buses = source.Buses
	h.SetEMS(api)
	point := data.NodeEdge{
		ID: data.NewID(), Type: data.NodeTypeModbusIO, Parent: snapshotRoot(t, base, token),
		Points: data.Points{data.NewPointString(data.PointTypeDescription, "", "config only")},
	}
	if err := client.SendNode(h.nc, point, "stage-d-fallback"); err != nil {
		t.Fatal(err)
	}
	if configs, err := source.Buses(); err != nil {
		t.Fatal(err)
	} else if len(configs) != 0 {
		t.Fatalf("fixture unexpectedly reached the runtime: %+v", configs)
	}
	if err := client.SendNodePoint(h.nc, point.ID, data.NewPointString(data.PointTypeUnits, "", "C"), true); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	points := data.Points{
		data.NewPointFloat(data.PointTypeValue, "", 2),
		data.NewPointFloat(data.PointTypeValue, "", 4),
		data.NewPointString(data.PointTypeValue, "", "not-a-number"),
	}
	points[0].Time = at
	points[1].Time = at.Add(time.Second)
	points[2].Time = at.Add(2 * time.Second)
	if err := client.SendNodePoints(h.nc, point.ID, points, true); err != nil {
		t.Fatal(err)
	}
	query := url.Values{
		"node_id":   []string{point.ID},
		"start":     []string{at.Format(time.RFC3339)},
		"end":       []string{at.Add(2 * time.Second).Format(time.RFC3339)},
		"aggregate": []string{"5m"},
	}
	var result struct {
		Results []struct {
			Source  string `json:"source"`
			Records []struct {
				Value       *float64 `json:"value"`
				Unit        string   `json:"unit"`
				Quality     string   `json:"quality"`
				SampleCount int      `json:"sample_count"`
				Aggregate   string   `json:"aggregate"`
			} `json:"records"`
		} `json:"results"`
	}
	status, body := call(t, http.MethodGet, base+"/history?"+query.Encode(), token, nil, &result)
	if status != 200 || len(result.Results) != 1 {
		t.Fatalf("fallback aggregate query = %d %s", status, body)
	}
	if result.Results[0].Source != "siot" {
		t.Fatalf("source = %q, want siot", result.Results[0].Source)
	}
	records := result.Results[0].Records
	if len(records) != 1 {
		t.Fatalf("SIOT fallback records vanished from aggregation: %+v", records)
	}
	record := records[0]
	if record.Value == nil || *record.Value != 3 || record.SampleCount != 2 || record.Unit != "C" || record.Aggregate != "5m0s" {
		t.Fatalf("fallback aggregate evidence = %+v", record)
	}
	if record.Quality != "" {
		t.Fatalf("fallback record invented a quality: %q", record.Quality)
	}
}
