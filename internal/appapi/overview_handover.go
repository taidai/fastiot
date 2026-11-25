package appapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	overviewHandoverFile      = "overview-handover.json"
	overviewHandoverNoteLimit = 4000
)

var overviewHandoverMu sync.Mutex

type overviewHandover struct {
	LayerID   string    `json:"layer_id"`
	Note      string    `json:"note"`
	Actor     string    `json:"actor,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type overviewHandoverResponse struct {
	Handover *overviewHandover `json:"handover"`
}

func readOverviewHandovers(dataDir string) (map[string]overviewHandover, error) {
	if dataDir == "" {
		return nil, errors.New("data_dir_unavailable")
	}
	raw, err := os.ReadFile(filepath.Join(dataDir, overviewHandoverFile))
	if os.IsNotExist(err) {
		return map[string]overviewHandover{}, nil
	}
	if err != nil {
		return nil, err
	}
	var records map[string]overviewHandover
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, err
	}
	if records == nil {
		records = map[string]overviewHandover{}
	}
	return records, nil
}

func writeOverviewHandovers(dataDir string, records map[string]overviewHandover) error {
	if dataDir == "" {
		return errors.New("data_dir_unavailable")
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return err
	}
	encoded, err := json.Marshal(records)
	if err != nil {
		return err
	}
	path := filepath.Join(dataDir, overviewHandoverFile)
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, encoded, 0600); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

// overviewHandover serves the latest shift note for one layer. Route
// registration supplies layer scope; this method owns validation and durable
// storage, and never trusts actor or timestamp fields supplied by the client.
func (h *Handler) overviewHandover(w http.ResponseWriter, r *http.Request, identity Identity) {
	layerID := strings.TrimSpace(r.URL.Query().Get("layer"))
	if layerID == "" {
		writeError(w, http.StatusBadRequest, "layer_required")
		return
	}
	overviewHandoverMu.Lock()
	defer overviewHandoverMu.Unlock()

	switch r.Method {
	case http.MethodGet:
		records, err := readOverviewHandovers(h.dataDir)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "handover_read_failed")
			return
		}
		response := overviewHandoverResponse{}
		if record, ok := records[layerID]; ok {
			response.Handover = &record
		}
		writeJSON(w, response)
	case http.MethodPut:
		var input struct {
			Note string `json:"note"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		note := strings.TrimSpace(input.Note)
		if note == "" || utf8.RuneCountInString(note) > overviewHandoverNoteLimit {
			writeError(w, http.StatusBadRequest, "invalid_handover_note")
			return
		}
		records, err := readOverviewHandovers(h.dataDir)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "handover_read_failed")
			return
		}
		actor := identity.Email
		if actor == "" {
			actor = identity.UserID
		}
		record := overviewHandover{LayerID: layerID, Note: note, Actor: actor, UpdatedAt: time.Now().UTC()}
		records[layerID] = record
		if err := writeOverviewHandovers(h.dataDir, records); err != nil {
			writeError(w, http.StatusInternalServerError, "handover_save_failed")
			return
		}
		writeJSON(w, overviewHandoverResponse{Handover: &record})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}
