package appapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func callOverviewHandover(t *testing.T, handler *Handler, method, target, body string) (int, overviewHandoverResponse) {
	t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	recorder := httptest.NewRecorder()
	handler.overviewHandover(recorder, request, Identity{UserID: "user-1", Email: "operator@example.com"})
	var response overviewHandoverResponse
	if recorder.Code == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode response: %v", err)
		}
	}
	return recorder.Code, response
}

func TestOverviewHandoverPersistsAcrossHandlersAndIsolatesLayers(t *testing.T) {
	dir := t.TempDir()
	first := &Handler{dataDir: dir}
	status, saved := callOverviewHandover(t, first, http.MethodPut, "/overview/handover?layer=room", `{"note":"  夜间注意液位  "}`)
	if status != http.StatusOK {
		t.Fatalf("save status = %d", status)
	}
	if saved.Handover == nil || saved.Handover.Note != "夜间注意液位" || saved.Handover.LayerID != "room" {
		t.Fatalf("unexpected saved handover: %#v", saved.Handover)
	}
	if saved.Handover.Actor != "operator@example.com" || saved.Handover.UpdatedAt.IsZero() {
		t.Fatalf("server metadata missing: %#v", saved.Handover)
	}

	reopened := &Handler{dataDir: dir}
	status, loaded := callOverviewHandover(t, reopened, http.MethodGet, "/overview/handover?layer=room", "")
	if status != http.StatusOK || loaded.Handover == nil || loaded.Handover.Note != "夜间注意液位" {
		t.Fatalf("reopened handover = %#v, status %d", loaded.Handover, status)
	}
	status, other := callOverviewHandover(t, reopened, http.MethodGet, "/overview/handover?layer=other", "")
	if status != http.StatusOK || other.Handover != nil {
		t.Fatalf("other layer must be empty: %#v, status %d", other.Handover, status)
	}
}

func TestOverviewHandoverRejectsInvalidInput(t *testing.T) {
	handler := &Handler{dataDir: t.TempDir()}
	cases := []struct {
		name   string
		method string
		target string
		body   string
		status int
	}{
		{name: "missing layer", method: http.MethodGet, target: "/overview/handover", status: http.StatusBadRequest},
		{name: "invalid json", method: http.MethodPut, target: "/overview/handover?layer=room", body: `{`, status: http.StatusBadRequest},
		{name: "unknown field", method: http.MethodPut, target: "/overview/handover?layer=room", body: `{"note":"ok","actor":"spoof"}`, status: http.StatusBadRequest},
		{name: "blank note", method: http.MethodPut, target: "/overview/handover?layer=room", body: `{"note":"  "}`, status: http.StatusBadRequest},
		{name: "too long", method: http.MethodPut, target: "/overview/handover?layer=room", body: `{"note":"` + strings.Repeat("界", 4001) + `"}`, status: http.StatusBadRequest},
		{name: "wrong method", method: http.MethodDelete, target: "/overview/handover?layer=room", status: http.StatusMethodNotAllowed},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			status, _ := callOverviewHandover(t, handler, test.method, test.target, test.body)
			if status != test.status {
				t.Fatalf("status = %d, want %d", status, test.status)
			}
		})
	}
}

func TestOverviewHandoverReportsPersistenceFailures(t *testing.T) {
	handler := &Handler{}
	status, _ := callOverviewHandover(t, handler, http.MethodPut, "/overview/handover?layer=room", `{"note":"test"}`)
	if status != http.StatusInternalServerError {
		t.Fatalf("missing data dir status = %d", status)
	}

	corrupt := &Handler{dataDir: t.TempDir()}
	status, _ = callOverviewHandover(t, corrupt, http.MethodGet, "/overview/handover?layer=room", "")
	if status != http.StatusOK {
		t.Fatalf("empty store status = %d", status)
	}
	if err := os.WriteFile(filepath.Join(corrupt.dataDir, overviewHandoverFile), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	status, _ = callOverviewHandover(t, corrupt, http.MethodGet, "/overview/handover?layer=room", "")
	if status != http.StatusInternalServerError {
		t.Fatalf("corrupt store status = %d", status)
	}
}
