package appapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"fastiot/internal/deepseek"
	"fastiot/internal/genscreen"
)

// generatedScreenRevisionInput is a follow-up instruction in the same
// generation session. The screen source and device model always come from the
// session, never from the client, so a revision can only change what was
// already previewed.
type generatedScreenRevisionInput struct {
	Instruction  string                `json:"instruction"`
	Screenshots  []deepseek.Screenshot `json:"screenshots,omitempty"`
	Acknowledged []string              `json:"acknowledged,omitempty"`
	Confirmed    bool                  `json:"confirmed,omitempty"`
}

func (h *Handler) readGeneratedScreenRevisionInput(w http.ResponseWriter, r *http.Request) (generatedScreenRevisionInput, bool) {
	var input generatedScreenRevisionInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDeepSeekBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return input, false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return input, false
	}
	for _, screenshot := range input.Screenshots {
		annotated := false
		for _, region := range screenshot.Regions {
			if strings.TrimSpace(region.Note) != "" {
				annotated = true
				break
			}
		}
		if !annotated {
			writeError(w, http.StatusBadRequest, "annotation_required")
			return input, false
		}
	}
	return input, true
}

// generatedScreenRevisionSession resolves the preview session and checks that
// its node is still inside the caller's scope. Revisions mutate this session,
// so the modified screen re-enters the same isolated frame.
func (h *Handler) generatedScreenRevisionSession(w http.ResponseWriter, identity Identity, id string) (*genscreen.Session, bool) {
	session, ok := h.generatedScreens.Get(id, identity.UserID)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return nil, false
	}
	if !h.allows(identity, session.NodeID) {
		writeError(w, http.StatusForbidden, "forbidden")
		return nil, false
	}
	return session, true
}

func (h *Handler) revisionRequest(input generatedScreenRevisionInput, snapshot genscreen.Snapshot) deepseek.Request {
	points := make([]deepseek.PointSummary, 0, len(snapshot.Points))
	for _, point := range snapshot.Points {
		points = append(points, deepseek.PointSummary{ID: point.ID, Name: point.Name, Type: point.Kind, Unit: point.Unit})
	}
	return deepseek.Request{
		Mode: deepseek.ModeRevise, Instruction: input.Instruction, Context: snapshot.Source,
		Points: points, Screenshots: input.Screenshots, Acknowledged: input.Acknowledged,
	}
}

// generatedScreenRevisionPreview shows the exact outbound content for a
// follow-up instruction: the existing screen source, the annotation notes and
// the same sensitive-content findings as first generation.
func (h *Handler) generatedScreenRevisionPreview(w http.ResponseWriter, r *http.Request, identity Identity, id string) {
	input, ok := h.readGeneratedScreenRevisionInput(w, r)
	if !ok {
		return
	}
	session, ok := h.generatedScreenRevisionSession(w, identity, id)
	if !ok {
		return
	}
	snapshot := session.Snapshot()
	writeJSON(w, map[string]any{
		"manifest": deepseek.BuildManifest(h.revisionRequest(input, snapshot)),
		"revision": snapshot.Revision,
	})
}

// generatedScreenRevision asks the model to modify the existing screen. The
// reply is validated exactly like a first generation, replaces the preview
// source in place, and clears the browser checks so the publish gate must run
// again.
func (h *Handler) generatedScreenRevision(w http.ResponseWriter, r *http.Request, identity Identity, id string) {
	input, ok := h.readGeneratedScreenRevisionInput(w, r)
	if !ok {
		return
	}
	if !input.Confirmed {
		writeError(w, http.StatusBadRequest, "confirmation_required")
		return
	}
	session, ok := h.generatedScreenRevisionSession(w, identity, id)
	if !ok {
		return
	}
	if h.deepseek == nil {
		writeError(w, http.StatusConflict, "deepseek_not_configured")
		return
	}
	snapshot := session.Snapshot()
	result, err := h.deepseek.Generate(r.Context(), h.revisionRequest(input, snapshot))
	switch {
	case errors.Is(err, deepseek.ErrNotConfigured):
		writeError(w, http.StatusConflict, "deepseek_not_configured")
		return
	case errors.Is(err, deepseek.ErrBlocked):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":    "outbound_blocked",
			"manifest": result.Manifest,
		})
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, "model_unavailable")
		return
	}

	document := genscreen.Parse(genscreen.Extract(result.Content))
	if strings.TrimSpace(document.Source) == "" {
		writeError(w, http.StatusBadGateway, "model_source_invalid")
		return
	}
	selected := make(map[string]genscreen.Point, len(snapshot.Points))
	for _, point := range snapshot.Points {
		selected[point.ID] = point
	}
	report := document.Validate(h.generatedScreenResolver(identity, selected))
	changes := genscreen.Diff(genscreen.Parse(snapshot.Source), document)
	revision := session.ApplyRevision(document.Source, report.Findings)

	writeJSON(w, map[string]any{
		"content":   result.Content,
		"model":     result.Model,
		"manifest":  result.Manifest,
		"report":    report,
		"revision":  revision,
		"frame_url": generatedScreenFrameURL(session, revision),
		"changes":   changes,
	})
}

func generatedScreenFrameURL(session *genscreen.Session, revision int) string {
	url := "/api/generated-screens/" + session.ID + "/frame?cap=" + session.Capability
	if revision > 0 {
		url += "&rev=" + strconv.Itoa(revision)
	}
	return url
}
