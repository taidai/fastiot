package appapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/genscreen"
)

const (
	// generatedScreenDraftType is where a confirmed screen is kept. It is a
	// draft on the node, not the running screen: publishing it is a later step.
	generatedScreenDraftType = "easyemsGeneratedScreenDraft"
	maxGeneratedScreenSource = 2 << 20
	maxGeneratedScreenIDs    = 200
)

// generatedScreenPreview validates the model's reply and starts an isolated
// preview. The source is never applied to a screen and never runs in the
// platform page.
func (h *Handler) generatedScreenPreview(w http.ResponseWriter, r *http.Request, identity Identity) {
	var input struct {
		NodeID   string   `json:"node_id"`
		Source   string   `json:"source"`
		PointIDs []string `json:"point_ids"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if len(input.Source) > maxGeneratedScreenSource {
		writeError(w, http.StatusRequestEntityTooLarge, "source_too_large")
		return
	}
	if len(input.PointIDs) > maxGeneratedScreenIDs {
		writeError(w, http.StatusBadRequest, "invalid_points")
		return
	}
	node, ok := h.node(w, identity, input.NodeID)
	if !ok {
		return
	}
	if node.Type != data.NodeTypeGroup && node.Type != data.NodeTypeDevice {
		writeError(w, http.StatusBadRequest, "screen_requires_group")
		return
	}

	selected := make([]genscreen.Point, 0, len(input.PointIDs))
	byID := map[string]genscreen.Point{}
	for _, id := range input.PointIDs {
		if id == "" || byID[id].ID != "" {
			continue
		}
		if !h.allows(identity, id) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		nodes, err := client.GetNodes(h.nc, "all", id, "", false)
		if err != nil || len(nodes) == 0 {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		point := generatedScreenPoint(nodes[0])
		byID[id] = point
		selected = append(selected, point)
	}

	document := genscreen.Parse(genscreen.Extract(input.Source))
	if strings.TrimSpace(document.Source) == "" {
		writeError(w, http.StatusBadRequest, "invalid_source")
		return
	}
	report := document.Validate(h.generatedScreenResolver(identity, byID))

	session := h.generatedScreens.Put(&genscreen.Session{
		Owner: identity.UserID, NodeID: input.NodeID,
		Source: document.Source, Points: selected, Static: report.Findings,
	})
	writeJSON(w, map[string]any{
		"id":         session.ID,
		"capability": session.Capability,
		"frame_url":  "/api/generated-screens/" + session.ID + "/frame?cap=" + session.Capability,
		"points":     selected,
		"report":     report,
		"simulated":  true,
	})
}

// generatedScreenResolver answers binding checks against the project. Points
// outside the selected model are reported separately from points that do not
// exist, so the deliverer knows whether to re-select or fix the code.
func (h *Handler) generatedScreenResolver(identity Identity, selected map[string]genscreen.Point) genscreen.Resolver {
	return func(id string) (genscreen.Point, genscreen.Status) {
		if point, ok := selected[id]; ok {
			return point, genscreen.PointOK
		}
		if id == "" || len(id) > 128 {
			return genscreen.Point{}, genscreen.PointMissing
		}
		nodes, err := client.GetNodes(h.nc, "all", id, "", false)
		if err != nil || len(nodes) == 0 {
			return genscreen.Point{}, genscreen.PointMissing
		}
		if !h.allows(identity, id) {
			return genscreen.Point{}, genscreen.PointOutOfScope
		}
		return genscreen.Point{}, genscreen.PointNotSelected
	}
}

func generatedScreenPoint(node data.NodeEdge) genscreen.Point {
	point := genscreen.Point{ID: node.ID, Name: "未命名点位", Kind: "number"}
	if name, ok := node.Points.Text(data.PointTypeDescription, ""); ok && strings.TrimSpace(name) != "" {
		point.Name = name
	}
	if unit, ok := node.Points.Text(data.PointTypeUnits, ""); ok {
		point.Unit = unit
	}
	for _, item := range node.Points {
		if item.Type == data.PointTypeValue && item.DataType == data.PointDataTypeString {
			point.Kind = "text"
		}
	}
	point.Writable = generatedScreenWritable(node)
	return point
}

// generatedScreenWritable mirrors the operator write rule: test variables and
// writable Modbus coils/registers. A preview never uses this to write; it only
// decides whether a screen's write binding is legitimate.
func generatedScreenWritable(node data.NodeEdge) bool {
	if node.Type == data.NodeTypeVariable {
		return true
	}
	if node.Type != "modbusIo" {
		return false
	}
	ioType, _ := node.Points.Text("modbusIoType", "")
	readOnly, _ := node.Points.Value("readOnly", "")
	return readOnly == 0 && (ioType == "modbusCoil" || ioType == "modbusHoldingRegister")
}

func (h *Handler) generatedScreenRead(w http.ResponseWriter, r *http.Request, identity Identity, id string) {
	var input struct {
		IDs []string `json:"ids"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if len(input.IDs) > maxGeneratedScreenIDs {
		writeError(w, http.StatusBadRequest, "invalid_read")
		return
	}
	session, ok := h.generatedScreens.Get(id, identity.UserID)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	writeJSON(w, map[string]any{"values": session.Sim.Read(input.IDs), "simulated": true})
}

// generatedScreenWrite applies a screen's write binding to the preview
// simulator. There is deliberately no device path from this handler.
func (h *Handler) generatedScreenWrite(w http.ResponseWriter, r *http.Request, identity Identity, id string) {
	var input struct {
		PointID string  `json:"point_id"`
		Value   float64 `json:"value"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	session, ok := h.generatedScreens.Get(id, identity.UserID)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	outcome := session.Sim.Write(input.PointID, input.Value)
	if outcome.Status != genscreen.WriteSimulated {
		writeJSONStatus(w, http.StatusConflict, map[string]any{
			"error": "write_rejected", "reason": outcome.Reason,
		})
		return
	}
	writeJSON(w, outcome)
}

func (h *Handler) generatedScreenDiagnostics(w http.ResponseWriter, r *http.Request, identity Identity, id string) {
	var input struct {
		Findings []genscreen.Finding `json:"findings"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	findings, ok := genscreen.SanitizeClientFindings(input.Findings)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_diagnostics")
		return
	}
	session, ok := h.generatedScreens.Get(id, identity.UserID)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	document := genscreen.Parse(session.Snapshot().Source)
	findings = document.AttachLocations(findings)
	writeJSON(w, map[string]any{"report": session.SetClient(findings, time.Now())})
}

// generatedScreenConfirm is the publish gate. Runtime and layout checks must
// have run and every blocking finding must be gone; only then is the source
// kept as a project draft.
func (h *Handler) generatedScreenConfirm(w http.ResponseWriter, r *http.Request, identity Identity, id string) {
	session, ok := h.generatedScreens.Get(id, identity.UserID)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	report := session.Report()
	if !report.Ready {
		writeJSONStatus(w, http.StatusConflict, map[string]any{"error": "checks_failed", "report": report})
		return
	}
	if !session.HasClientChecks() {
		writeJSONStatus(w, http.StatusConflict, map[string]any{"error": "checks_required", "report": report})
		return
	}
	if !h.allows(identity, session.NodeID) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	checkedAt := session.CheckedAt()
	snapshot := session.Snapshot()
	draft, err := json.Marshal(map[string]any{
		"source": snapshot.Source, "points": snapshot.Points, "report": report,
		"checked_at": checkedAt, "simulator": "preview", "node_id": session.NodeID,
		"revision": snapshot.Revision,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encode_failed")
		return
	}
	point := data.NewPointString(generatedScreenDraftType, "", string(draft))
	point.Origin = identity.UserID
	if err := client.SendNodePoint(h.nc, session.NodeID, point, true); err != nil {
		writeError(w, http.StatusBadGateway, "save_failed")
		return
	}
	writeJSON(w, map[string]any{"confirmed": true, "node_id": session.NodeID})
}

// generatedScreenFrame serves the sandboxed document. It is authenticated by
// the session capability rather than the platform session, because a frame
// navigation cannot carry the bearer token and must never receive it.
func (h *Handler) generatedScreenFrame(w http.ResponseWriter, r *http.Request, id string) {
	session, ok := h.generatedScreens.ByCapability(r.URL.Query().Get("cap"))
	if !ok || session.ID != id {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	nonce := generatedScreenNonce()
	document := genscreen.Parse(session.Snapshot().Source)

	header := w.Header()
	header.Set("Content-Type", "text/html; charset=utf-8")
	header.Set("Content-Security-Policy", genscreen.ContentSecurityPolicy(nonce))
	header.Del("X-Frame-Options")
	header.Set("Cache-Control", "no-store")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Cross-Origin-Resource-Policy", "same-origin")
	_, _ = w.Write([]byte(document.Render(nonce)))
}

func generatedScreenNonce() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		panic("appapi: no randomness for preview nonce")
	}
	return hex.EncodeToString(buffer)
}

func writeJSONStatus(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
