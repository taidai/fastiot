package appapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"fastiot/internal/calculation"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

const calculationType = "calculationPoint"
const calculationDocument = "calculationDocument"
const ActionCalculationApply = "calculation_apply"

type calculationRequest struct {
	Parent       string                 `json:"parent"`
	Name         string                 `json:"name"`
	Definition   calculation.Definition `json:"definition"`
	Revision     uint64                 `json:"revision"`
	OperationID  string                 `json:"operation_id"`
	Values       map[string]float64     `json:"values,omitempty"`
	Times        map[string]time.Time   `json:"times,omitempty"`
	Confirmation string                 `json:"confirmation,omitempty"`
}
type calculationResponse struct {
	ID          string                  `json:"id"`
	Parent      string                  `json:"parent"`
	Name        string                  `json:"name"`
	Revision    uint64                  `json:"revision"`
	Version     uint64                  `json:"version"`
	Draft       *calculation.Definition `json:"draft,omitempty"`
	Applied     *calculation.Definition `json:"applied,omitempty"`
	State       any                     `json:"state"`
	Permissions map[string]bool         `json:"permissions"`
}
type calculationCatalog struct {
	nodes     map[string]data.NodeEdge
	documents map[string]calculation.Document
	sources   map[string]calculation.Metadata
}

func decodeCalculation(node data.NodeEdge) (calculation.Document, error) {
	var d calculation.Document
	text, ok := node.Points.Text(calculationDocument, "")
	if node.Type != calculationType || !ok || json.Unmarshal([]byte(text), &d) != nil || d.Schema != 1 || d.Revision == 0 || d.Draft.ValidateDraft() != nil {
		return d, errors.New("invalid_calculation_document")
	}
	if (d.Applied == nil && d.Version != 0) || (d.Applied != nil && (d.Version == 0 || d.Applied.Validate() != nil)) {
		return d, errors.New("invalid_calculation_document")
	}
	return d, nil
}
func (h *Handler) loadCalculationCatalog() (calculationCatalog, error) {
	c := calculationCatalog{nodes: map[string]data.NodeEdge{}, documents: map[string]calculation.Document{}, sources: map[string]calculation.Metadata{}}
	var nodes []data.NodeEdge
	for _, typ := range []string{calculationType, virtualInputType, data.NodeTypeModbusIO} {
		found, err := client.GetNodes(h.nc, "all", "all", typ, false)
		if err != nil {
			return c, err
		}
		nodes = append(nodes, found...)
	}
	for _, n := range nodes {
		c.nodes[n.ID] = n
		switch n.Type {
		case calculationType:
			if d, err := decodeCalculation(n); err == nil {
				c.documents[n.ID] = d
			}
		case virtualInputType:
			if d, err := decodeVirtualInput(n); err == nil && d.Applied != nil {
				c.sources[n.ID] = calculation.Metadata{Kind: d.Applied.Kind, Unit: d.Applied.Unit}
			}
		case data.NodeTypeModbusIO:
			kind := ""
			switch textPoint(n.Points, data.PointTypeModbusIOType) {
			case data.PointValueModbusHoldingRegister, data.PointValueModbusInputRegister:
				kind = "number"
			case data.PointValueModbusCoil, data.PointValueModbusDiscreteInput:
				kind = "bool"
			}
			if kind != "" {
				c.sources[n.ID] = calculation.Metadata{Kind: kind, Unit: textPoint(n.Points, data.PointTypeUnits)}
			}
		}
	}
	return c, nil
}
func (h *Handler) readableCalculation(identity Identity, definition calculation.Definition, c calculationCatalog) bool {
	seen := map[string]bool{}
	queue := append([]string(nil), definition.Inputs...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if id == "" {
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		if !h.allows(identity, id) {
			return false
		}
		if d, ok := c.documents[id]; ok && d.Applied != nil {
			queue = append(queue, d.Applied.Inputs...)
		}
	}
	return true
}
func (h *Handler) readableCalculationEvidence(identity Identity, result calculation.Result) bool {
	for _, id := range result.Definition.Inputs {
		if !h.allows(identity, id) {
			return false
		}
	}
	for _, input := range result.Inputs {
		if !h.allows(identity, input.ID) {
			return false
		}
	}
	for _, leaf := range result.Leaves {
		if !h.allows(identity, leaf.ID) {
			return false
		}
	}
	return true
}

func (h *Handler) calculationResponse(n data.NodeEdge, d calculation.Document, identity Identity, c calculationCatalog) calculationResponse {
	out := calculationResponse{ID: n.ID, Parent: n.Parent, Name: nodeName(n), Revision: d.Revision, Version: d.Version, State: map[string]any{"quality": "uninitialized", "reason": "not_applied"}, Permissions: map[string]bool{"edit": canAction(identity, ActionConfigure), "apply": canAction(identity, ActionCalculationApply)}}
	if (d.Applied != nil && !h.readableCalculation(identity, *d.Applied, c)) || !h.readableCalculation(identity, d.Draft, c) {
		out.State = map[string]any{"quality": "invalid", "reason": "restricted_dependency"}
		out.Permissions = map[string]bool{}
		return out
	}
	out.Applied = d.Applied
	if out.Permissions["edit"] || out.Permissions["apply"] {
		out.Draft = &d.Draft
	}
	if d.Applied != nil {
		out.State = map[string]any{"quality": "uninitialized", "reason": "pending_calculation"}
		if result, ok := h.CalculationRuntime().Snapshot().Results[n.ID]; ok && result.Version == d.Version {
			if !h.readableCalculationEvidence(identity, result) {
				out.State = map[string]any{"quality": "invalid", "reason": "restricted_dependency"}
				return out
			}
			if result.Quality == "good" && !result.ValidUntil.IsZero() && !time.Now().Before(result.ValidUntil) {
				last := result
				last.Retained = nil
				result.Retained = &last
				result.Value = nil
				result.Quality = "stale"
				result.Reason = "expired"
			}
			if result.Retained != nil && !h.readableCalculationEvidence(identity, *result.Retained) {
				result.Retained = nil
			}
			out.State = result
		}
	}
	return out
}
func (h *Handler) calculations(w http.ResponseWriter, r *http.Request, identity Identity, path []string) {
	h.virtualInputMu.Lock()
	defer h.virtualInputMu.Unlock()
	if len(path) == 0 && r.Method == http.MethodPost {
		h.createCalculation(w, r, identity)
		return
	}
	catalog, err := h.loadCalculationCatalog()
	if err != nil {
		writeError(w, 503, "calculation_unavailable")
		return
	}
	if len(path) == 0 && r.Method == http.MethodGet {
		values := []calculationResponse{}
		for id, d := range catalog.documents {
			if h.allows(identity, id) {
				values = append(values, h.calculationResponse(catalog.nodes[id], d, identity, catalog))
			}
		}
		sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
		writeJSON(w, map[string]any{"calculations": values})
		return
	}
	if len(path) < 1 || len(path) > 2 {
		writeError(w, 404, "not_found")
		return
	}
	node, ok := h.node(w, identity, path[0])
	if !ok {
		return
	}
	d, err := decodeCalculation(node)
	if err != nil {
		inputError(w, err)
		return
	}
	if len(path) == 1 && r.Method == http.MethodGet {
		writeJSON(w, h.calculationResponse(node, d, identity, catalog))
		return
	}
	if len(path) != 2 || r.Method != http.MethodPost {
		writeError(w, 405, "method_not_allowed")
		return
	}
	action := path[1]
	if action == "trial" {
		h.trialCalculation(w, r, identity, node, d, catalog)
		return
	}
	preview := action == "impact"
	if preview {
		action = "apply"
	}
	permission := ActionConfigure
	if action == "apply" {
		permission = ActionCalculationApply
	} else if action != "draft" {
		writeError(w, 404, "not_found")
		return
	}
	if !canAction(identity, permission) {
		writeError(w, 403, "operation_not_allowed")
		return
	}
	var request calculationRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	mutation := calculation.Mutation{ID: request.OperationID, Actor: identity.UserID, ExpectedRevision: request.Revision, Now: time.Now()}
	next := d
	if action == "draft" {
		next, err = d.SaveDraft(request.Definition, mutation)
	} else {
		next, err = d.Apply(mutation)
	}
	if err != nil {
		inputError(w, err)
		return
	}
	if !h.readableCalculation(identity, next.Draft, catalog) {
		writeError(w, 403, "restricted_dependency")
		return
	}
	if action == "apply" {
		definitions := map[string]calculation.Definition{}
		for id, doc := range catalog.documents {
			if doc.Applied != nil {
				definitions[id] = *doc.Applied
			}
		}
		definitions[node.ID] = *next.Applied
		if _, err = calculation.ValidateGraph(calculationAffectedGraph(node.ID, definitions), catalog.sources); err != nil {
			var issue *calculation.Error
			if errors.As(err, &issue) && (!h.allows(identity, issue.NodeID) || (issue.InputID != "" && !h.allows(identity, issue.InputID))) {
				writeError(w, 409, "restricted_dependency")
				return
			}
			inputError(w, err)
			return
		}
	}
	if action == "apply" {
		impact, err := h.inputImpact(identity, node.ID, action, impactOperation{Revision: request.Revision, OperationID: request.OperationID})
		if err != nil {
			writeError(w, 503, "dependency_check_failed")
			return
		}
		if preview {
			writeJSON(w, impact)
			return
		}
		if next.Version != d.Version && (impact.Required || request.Confirmation != "") && request.Confirmation != impact.Confirmation {
			writeError(w, 409, "impact_confirmation_conflict")
			return
		}
	}
	if next.Revision != d.Revision {
		if err = h.persistCalculation(node, next, identity.UserID); err != nil {
			writeError(w, 503, "calculation_persistence_unconfirmed")
			return
		}
	}
	if next.Version != d.Version {
		h.CalculationRuntime().Invalidate([]string{node.ID})
	}
	catalog.documents[node.ID] = next
	writeJSON(w, h.calculationResponse(node, next, identity, catalog))
}
func (h *Handler) createCalculation(w http.ResponseWriter, r *http.Request, identity Identity) {
	if !canAction(identity, ActionConfigure) {
		writeError(w, 403, "operation_not_allowed")
		return
	}
	var input calculationRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Name) == "" {
		writeError(w, 400, "invalid_calculation_name")
		return
	}
	parent, ok := h.node(w, identity, input.Parent)
	if !ok {
		return
	}
	if parent.Type != data.NodeTypeGroup && parent.Parent != "root" {
		writeError(w, 400, "invalid_parent")
		return
	}
	d, err := calculation.NewDocument(input.Definition, calculation.Mutation{ID: input.OperationID, Actor: identity.UserID, Now: time.Now()})
	if err != nil {
		inputError(w, err)
		return
	}
	catalog, err := h.loadCalculationCatalog()
	if err != nil {
		writeError(w, 503, "calculation_unavailable")
		return
	}
	if !h.readableCalculation(identity, input.Definition, catalog) {
		writeError(w, 403, "restricted_dependency")
		return
	}
	raw, _ := json.Marshal([]string{parent.ID, identity.UserID, input.OperationID})
	sum := sha256.Sum256(raw)
	id := "cp-" + hex.EncodeToString(sum[:16])
	existing, err := client.GetNodes(h.nc, "all", id, "", true)
	if err != nil && !errors.Is(err, data.ErrDocumentNotFound) {
		writeError(w, 503, "calculation_unavailable")
		return
	}
	if len(existing) > 0 {
		saved, err := decodeCalculation(existing[0])
		if err != nil || existing[0].Parent != parent.ID || nodeName(existing[0]) != strings.TrimSpace(input.Name) || saved.LastChange == nil || saved.LastChange.Fingerprint != d.LastChange.Fingerprint {
			writeError(w, 409, "operation_conflict")
			return
		}
		writeJSON(w, h.calculationResponse(existing[0], saved, identity, catalog))
		return
	}
	encoded, err := json.Marshal(d)
	if err != nil {
		inputError(w, err)
		return
	}
	node := data.NodeEdge{ID: id, Type: calculationType, Parent: parent.ID, Points: data.Points{data.NewPointString(data.PointTypeDescription, "0", strings.TrimSpace(input.Name)), data.NewPointString(calculationDocument, "0", string(encoded))}}
	if client.SendNode(h.nc, node, identity.UserID) != nil || h.verifyCalculation(id, string(encoded)) != nil {
		writeError(w, 503, "calculation_persistence_unconfirmed")
		return
	}
	writeJSON(w, h.calculationResponse(node, d, identity, catalog))
}
func (h *Handler) persistCalculation(node data.NodeEdge, d calculation.Document, actor string) error {
	encoded, err := json.Marshal(d)
	if err != nil {
		return err
	}
	point := data.NewPointString(calculationDocument, "", string(encoded))
	point.Origin = actor
	if previous, ok := node.Points.Find(calculationDocument, ""); ok && !point.Time.After(previous.Time) {
		point.Time = previous.Time.Add(time.Nanosecond)
	}
	if err = client.SendNodePoint(h.nc, node.ID, point, true); err != nil {
		return err
	}
	return h.verifyCalculation(node.ID, string(encoded))
}
func (h *Handler) verifyCalculation(id, expected string) error {
	nodes, err := client.GetNodes(h.nc, "all", id, "", false)
	if err != nil || len(nodes) == 0 {
		return errors.New("calculation_persistence_unconfirmed")
	}
	saved, ok := nodes[0].Points.Text(calculationDocument, "")
	if !ok || saved != expected {
		return errors.New("calculation_persistence_unconfirmed")
	}
	return nil
}
