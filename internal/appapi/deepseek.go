package appapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/deepseek"
)

// 12 MiB covers a base64 screenshot plus the instruction. The image itself is
// inspected locally and never forwarded.
const maxDeepSeekBody = 12 << 20

func (h *Handler) SetDeepSeek(service *deepseek.Service) { h.deepseek = service }

type deepseekInput struct {
	Instruction  string                `json:"instruction"`
	Context      string                `json:"context,omitempty"`
	PointIDs     []string              `json:"point_ids,omitempty"`
	Screenshots  []deepseek.Screenshot `json:"screenshots,omitempty"`
	Acknowledged []string              `json:"acknowledged,omitempty"`
	Confirmed    bool                  `json:"confirmed,omitempty"`
}

func (h *Handler) deepseekStatus(w http.ResponseWriter) {
	status := deepseek.Status{Model: deepseek.DefaultModel}
	if h.deepseek != nil {
		status = h.deepseek.Status()
	}
	writeJSON(w, status)
}

func (h *Handler) deepseekPreview(w http.ResponseWriter, r *http.Request, identity Identity) {
	input, points, ok := h.readDeepSeekInput(w, r, identity)
	if !ok {
		return
	}
	configured := h.deepseek != nil && h.deepseek.Status().Configured
	writeJSON(w, map[string]any{
		"configured": configured,
		"manifest":   deepseek.BuildManifest(requestFromInput(input, points)),
	})
}

func (h *Handler) deepseekGenerate(w http.ResponseWriter, r *http.Request, identity Identity) {
	input, points, ok := h.readDeepSeekInput(w, r, identity)
	if !ok {
		return
	}
	if !input.Confirmed {
		writeError(w, http.StatusBadRequest, "confirmation_required")
		return
	}
	if h.deepseek == nil {
		writeError(w, http.StatusConflict, "deepseek_not_configured")
		return
	}
	result, err := h.deepseek.Generate(r.Context(), requestFromInput(input, points))
	switch {
	case errors.Is(err, deepseek.ErrNotConfigured):
		writeError(w, http.StatusConflict, "deepseek_not_configured")
	case errors.Is(err, deepseek.ErrBlocked):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":    "outbound_blocked",
			"manifest": result.Manifest,
		})
	case err != nil:
		writeError(w, http.StatusBadGateway, "model_unavailable")
	default:
		writeJSON(w, map[string]any{
			"content":  result.Content,
			"model":    result.Model,
			"manifest": result.Manifest,
		})
	}
}

func requestFromInput(input deepseekInput, points []deepseek.PointSummary) deepseek.Request {
	return deepseek.Request{
		Instruction:  input.Instruction,
		Context:      input.Context,
		Points:       points,
		Screenshots:  input.Screenshots,
		Acknowledged: input.Acknowledged,
	}
}

// readDeepSeekInput decodes the request and resolves the requested points into
// structure-only summaries. Point IDs are scope-checked before use.
func (h *Handler) readDeepSeekInput(w http.ResponseWriter, r *http.Request, identity Identity) (deepseekInput, []deepseek.PointSummary, bool) {
	var input deepseekInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDeepSeekBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return input, nil, false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return input, nil, false
	}
	points, ok := h.deepseekPoints(w, identity, input.PointIDs)
	if !ok {
		return input, nil, false
	}
	return input, points, true
}

func (h *Handler) deepseekPoints(w http.ResponseWriter, identity Identity, ids []string) ([]deepseek.PointSummary, bool) {
	summaries := []deepseek.PointSummary{}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if !h.allows(identity, id) {
			writeError(w, http.StatusForbidden, "forbidden")
			return nil, false
		}
		nodes, err := client.GetNodes(h.nc, "all", id, "", false)
		if err != nil || len(nodes) == 0 {
			writeError(w, http.StatusNotFound, "not_found")
			return nil, false
		}
		node := nodes[0]
		name, _ := node.Points.Text(data.PointTypeDescription, "")
		if strings.TrimSpace(name) == "" {
			name = "未命名点位"
		}
		unit, _ := node.Points.Text(data.PointTypeUnits, "")
		summaries = append(summaries, deepseek.PointSummary{
			Name: name,
			Type: deepseekPointType(node),
			Unit: unit,
		})
	}
	return summaries, true
}

// deepseekPointType reports the value kind, falling back to the node type. It
// deliberately ignores the value itself.
func deepseekPointType(node data.NodeEdge) string {
	for _, point := range node.Points {
		if point.Type != data.PointTypeValue && point.Type != data.PointTypeValueSet {
			continue
		}
		switch point.DataType {
		case data.PointDataTypeFloat, data.PointDataTypeInt:
			return "number"
		case data.PointDataTypeString, data.PointDataTypeJSON:
			return "string"
		}
	}
	return node.Type
}
