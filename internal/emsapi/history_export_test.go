package emsapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"fastiot/internal/authz"
	"fastiot/internal/ems/history"
	"fastiot/internal/ems/historyquery"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/ems/signal"
)

func recordSamples(service *runtime.Service, count int) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	samples := make([]history.Sample, 0, count)
	for i := 0; i < count; i++ {
		samples = append(samples, history.Sample{
			DeviceID: "d1", PointID: "temp", Value: signal.Num(float64(i)),
			Quality: signal.Good, SourceTime: base.Add(time.Duration(i) * time.Second),
		})
	}
	service.History.Record(samples)
}

func TestHistoryExportRejectsWindowOverLimitBeforeWritingCSV(t *testing.T) {
	service := runtime.NewService(nil, time.Minute, time.Second, historyquery.ExportLimit+1)
	defer service.Close()
	recordSamples(service, historyquery.ExportLimit+1)

	response := call(t, New(service, time.Minute),
		Access{UserID: "exporter", Actions: map[string]bool{authz.Export: true}},
		http.MethodGet, "/api/ems/history/export?device_id=d1&point_id=temp", "")
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), historyquery.ErrExportLimitExceeded.Error()) {
		t.Fatalf("over-limit export = %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Header().Get("Content-Type"), "text/csv") || strings.Contains(response.Body.String(), "时间,设备,点位,数值,质量") {
		t.Fatalf("over-limit export wrote a CSV file: headers=%v body=%s", response.Header(), response.Body.String())
	}
}

func TestHistoryQueryReportsTruncationInsteadOfSilentCut(t *testing.T) {
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	recordSamples(service, 3)
	handler := New(service, time.Minute)

	type payload struct {
		Samples []struct {
			Value struct {
				Number float64 `json:"number"`
			} `json:"value"`
		} `json:"samples"`
		Truncated bool `json:"truncated"`
	}
	read := func(limit string) payload {
		t.Helper()
		response := call(t, handler, Access{UserID: "viewer"}, http.MethodGet,
			"/api/ems/history?device_id=d1&point_id=temp&limit="+limit, "")
		if response.Code != http.StatusOK {
			t.Fatalf("history limit=%s = %d %s", limit, response.Code, response.Body.String())
		}
		var result payload
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}

	if full := read("3"); full.Truncated || len(full.Samples) != 3 {
		t.Fatalf("complete page flagged truncated: %+v", full)
	}
	cut := read("2")
	if !cut.Truncated || len(cut.Samples) != 2 {
		t.Fatalf("over-limit page not flagged truncated: %+v", cut)
	}
	if cut.Samples[0].Value.Number != 1 || cut.Samples[1].Value.Number != 2 {
		t.Fatalf("truncated page kept the wrong rows: %+v", cut.Samples)
	}
}
