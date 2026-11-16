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

	"fastiot/internal/virtualinput"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

const virtualInputType = "virtualInput"
const virtualInputDocument = "virtualInputDocument"
const ActionVirtualInputApply = "virtual_input_apply"
const ActionVirtualInputWrite = "virtual_input_write"

type virtualInputRequest struct {
	Parent       string                  `json:"parent"`
	Name         string                  `json:"name"`
	Source       string                  `json:"source"`
	Definition   virtualinput.Definition `json:"definition"`
	Revision     uint64                  `json:"revision"`
	OperationID  string                  `json:"operation_id"`
	Value        json.RawMessage         `json:"value,omitempty"`
	Action       string                  `json:"action,omitempty"`
	Confirmation string                  `json:"confirmation,omitempty"`
}

type virtualInputResponse struct {
	ID          string                   `json:"id"`
	Parent      string                   `json:"parent"`
	Name        string                   `json:"name"`
	Revision    uint64                   `json:"revision"`
	Source      string                   `json:"source"`
	Draft       *virtualinput.Definition `json:"draft,omitempty"`
	Applied     *virtualinput.Definition `json:"applied,omitempty"`
	State       virtualinput.View        `json:"state"`
	Permissions map[string]bool          `json:"permissions"`
	LastChange  *virtualinput.Change     `json:"last_change,omitempty"`
}

func decodeVirtualInput(node data.NodeEdge) (virtualinput.Document, error) {
	var d virtualinput.Document
	text, ok := node.Points.Text(virtualInputDocument, "")
	if node.Type != virtualInputType || !ok || json.Unmarshal([]byte(text), &d) != nil || d.Schema != 1 {
		return d, errors.New("invalid_input_document")
	}
	return d, d.Validate()
}

func (h *Handler) inputClock(now time.Time) virtualinput.Context {
	wall := now.Round(0)
	if h.inputEpoch == "" || wall.Before(h.inputLastWall) {
		h.inputEpoch, h.inputStart = data.NewID(), now
	}
	h.inputLastWall = wall
	return virtualinput.Context{Now: wall, Epoch: h.inputEpoch, Monotonic: now.Sub(h.inputStart)}
}

func (h *Handler) inputResponse(node data.NodeEdge, d virtualinput.Document, identity Identity) virtualInputResponse {
	response := virtualInputResponse{ID: node.ID, Parent: node.Parent, Name: nodeName(node), Revision: d.Revision, Source: d.Source, Applied: d.Applied, State: d.Evaluate(h.inputClock(time.Now())), Permissions: map[string]bool{
		"edit": canAction(identity, ActionConfigure), "apply": canAction(identity, ActionVirtualInputApply), "write": canAction(identity, ActionVirtualInputWrite) && d.Source != "configuration" && d.Applied != nil && d.Applied.RuntimeEditable,
	}}
	if response.Permissions["edit"] || response.Permissions["apply"] {
		response.Draft = &d.Draft
		response.LastChange = d.LastChange
	}
	return response
}

func (h *Handler) virtualInputs(w http.ResponseWriter, r *http.Request, identity Identity, path []string) {
	h.virtualInputMu.Lock()
	defer h.virtualInputMu.Unlock()
	if len(path) == 0 && r.Method == http.MethodPost {
		h.createVirtualInput(w, r, identity)
		return
	}
	if len(path) == 0 && r.Method == http.MethodGet {
		nodes, err := h.visibleNodes(identity)
		if err != nil {
			writeError(w, 503, "input_unavailable")
			return
		}
		result := []virtualInputResponse{}
		for _, node := range nodes {
			if node.Type != virtualInputType {
				continue
			}
			d, err := decodeVirtualInput(node)
			if err != nil {
				writeError(w, 503, "invalid_input_document")
				return
			}
			result = append(result, h.inputResponse(node, d, identity))
		}
		writeJSON(w, map[string]any{"inputs": result})
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
	d, err := decodeVirtualInput(node)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if len(path) == 1 && r.Method == http.MethodGet {
		writeJSON(w, h.inputResponse(node, d, identity))
		return
	}
	if len(path) != 2 || r.Method != http.MethodPost {
		writeError(w, 405, "method_not_allowed")
		return
	}
	action := path[1]
	var input virtualInputRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	preview := action == "impact"
	if preview {
		action = input.Action
	}
	permission := ActionConfigure
	if action == "apply" {
		permission = ActionVirtualInputApply
	} else if action == "value" {
		permission = ActionVirtualInputWrite
	} else if action != "draft" {
		writeError(w, 404, "not_found")
		return
	}
	if !canAction(identity, permission) {
		writeError(w, 403, "operation_not_allowed")
		return
	}
	if preview && action == "draft" {
		writeError(w, 400, "invalid_impact_action")
		return
	}
	c := h.inputClock(time.Now())
	c.Actor, c.OperationID, c.ExpectedRevision = identity.UserID, input.OperationID, input.Revision
	next := d
	if action == "draft" {
		next, err = d.SaveDraft(input.Definition, c)
	} else if action == "value" {
		next, err = d.SetValue(input.Value, c)
	} else {
		next, err = d.Apply(c)
	}
	if err != nil {
		inputError(w, err)
		return
	}
	if action != "draft" {
		impact, err := h.inputImpact(identity, node.ID, action, impactOperation{Revision: input.Revision, OperationID: input.OperationID, Value: input.Value})
		if err != nil {
			writeError(w, 503, "dependency_check_failed")
			return
		}
		if preview {
			writeJSON(w, impact)
			return
		}
		if next.Revision != d.Revision && (impact.Required || input.Confirmation != "") && input.Confirmation != impact.Confirmation {
			writeError(w, 409, "impact_confirmation_conflict")
			return
		}
	}
	if next.Revision != d.Revision {
		if err = h.persistVirtualInput(node, next, identity.UserID); err != nil {
			writeError(w, 503, "input_persistence_unconfirmed")
			return
		}
		if action != "draft" {
			h.CalculationRuntime().Invalidate([]string{node.ID})
		}
	}
	writeJSON(w, h.inputResponse(node, next, identity))
}

func inputError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if strings.HasSuffix(err.Error(), "_conflict") {
		status = http.StatusConflict
	}
	writeError(w, status, err.Error())
}

func (h *Handler) createVirtualInput(w http.ResponseWriter, r *http.Request, identity Identity) {
	if !canAction(identity, ActionConfigure) {
		writeError(w, 403, "operation_not_allowed")
		return
	}
	var input virtualInputRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Name) == "" || input.OperationID == "" {
		writeError(w, 400, "invalid_input_definition")
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
	d, err := virtualinput.New(input.Source, input.Definition, virtualinput.Context{Actor: identity.UserID, OperationID: input.OperationID, Now: time.Now()})
	if err != nil {
		inputError(w, err)
		return
	}
	encoded, err := json.Marshal(d)
	if err != nil {
		writeError(w, 400, "invalid_input_definition")
		return
	}
	key, _ := json.Marshal([]string{parent.ID, identity.UserID, input.OperationID})
	sum := sha256.Sum256(key)
	id := "vi-" + hex.EncodeToString(sum[:16])
	if data.ValidID(id) != nil {
		writeError(w, 400, "invalid_input_id")
		return
	}
	existing, err := client.GetNodes(h.nc, "all", id, "", true)
	if err != nil && !errors.Is(err, data.ErrDocumentNotFound) {
		writeError(w, 503, "input_unavailable")
		return
	}
	if len(existing) != 0 {
		saved, err := decodeVirtualInput(existing[0])
		if err != nil || existing[0].Parent != parent.ID || nodeName(existing[0]) != strings.TrimSpace(input.Name) || saved.LastChange == nil || saved.LastChange.Fingerprint != d.LastChange.Fingerprint {
			writeError(w, 409, "operation_conflict")
			return
		}
		writeJSON(w, h.inputResponse(existing[0], saved, identity))
		return
	}
	node := data.NodeEdge{ID: id, Parent: parent.ID, Type: virtualInputType, Points: data.Points{
		data.NewPointString(data.PointTypeDescription, "0", strings.TrimSpace(input.Name)), data.NewPointString(virtualInputDocument, "0", string(encoded)),
	}}
	if client.SendNode(h.nc, node, identity.UserID) != nil || h.verifyVirtualInput(node.ID, string(encoded)) != nil {
		writeError(w, 503, "input_persistence_unconfirmed")
		return
	}
	writeJSON(w, h.inputResponse(node, d, identity))
}

func (h *Handler) persistVirtualInput(node data.NodeEdge, d virtualinput.Document, actor string) error {
	encoded, err := json.Marshal(d)
	if err != nil {
		return err
	}
	point := data.NewPointString(virtualInputDocument, "", string(encoded))
	if previous, ok := node.Points.Find(virtualInputDocument, ""); ok && !point.Time.After(previous.Time) {
		point.Time = previous.Time.Add(time.Nanosecond)
	}
	point.Origin = actor
	if err = client.SendNodePoint(h.nc, node.ID, point, true); err != nil {
		return err
	}
	return h.verifyVirtualInput(node.ID, string(encoded))
}

func (h *Handler) verifyVirtualInput(id, expected string) error {
	nodes, err := client.GetNodes(h.nc, "all", id, "", false)
	if err != nil || len(nodes) == 0 {
		return errors.New("input_persistence_unconfirmed")
	}
	saved, ok := nodes[0].Points.Text(virtualInputDocument, "")
	if !ok || saved != expected {
		return errors.New("input_persistence_unconfirmed")
	}
	return nil
}

func inputConfiguration(node data.NodeEdge, copy bool) (string, error) {
	d, err := decodeVirtualInput(node)
	if err != nil {
		return "", err
	}
	d.Current, d.Retained, d.LastChange = nil, nil, nil
	if copy {
		d.Revision, d.Applied, d.Draft.RuntimeEditable = 1, nil, false
	}
	encoded, err := json.Marshal(d)
	return string(encoded), err
}

func publicInputNodes(nodes []data.NodeEdge) []data.NodeEdge {
	out := append([]data.NodeEdge(nil), nodes...)
	for i, node := range out {
		if node.Type != virtualInputType && node.Type != calculationType {
			continue
		}
		out[i].Points = nil
		for _, p := range node.Points {
			if p.Type == data.PointTypeDescription {
				out[i].Points = append(out[i].Points, p)
			}
		}
	}
	return out
}

type inputImpact struct {
	References   []reuseItem `json:"references"`
	Restricted   bool        `json:"restricted"`
	Required     bool        `json:"required"`
	Confirmation string      `json:"confirmation"`
	Coverage     string      `json:"coverage"`
}

type impactOperation struct {
	Revision    uint64
	OperationID string
	Value       json.RawMessage
}

func (h *Handler) inputImpact(identity Identity, id, action string, input impactOperation) (inputImpact, error) {
	var nodes []data.NodeEdge
	for _, typ := range []string{"decisionTable", data.NodeTypeGroup, data.NodeTypeDevice, calculationType} {
		found, err := client.GetNodes(h.nc, "all", "all", typ, false)
		if err != nil {
			return inputImpact{}, err
		}
		nodes = append(nodes, found...)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	result := inputImpact{References: []reuseItem{}, Coverage: "已知规则、画面与计算点定义引用；不代表全部外部消费或真实控制验证"}
	dependencies := []string{}
	for _, n := range nodes {
		for _, p := range n.Points {
			if p.Tombstone%2 != 0 {
				continue
			}
			isCalculation := n.Type == calculationType && p.Type == calculationDocument
			if p.Type != screenPointType && !(n.Type == "decisionTable" && (p.Type == "draft" || p.Type == "applied")) && !isCalculation {
				continue
			}
			var document any
			if isCalculation {
				d, err := decodeCalculation(n)
				if err != nil {
					continue
				}
				refs := append([]string(nil), d.Draft.Inputs...)
				if d.Applied != nil {
					refs = append(refs, d.Applied.Inputs...)
				}
				for _, ref := range refs {
					if ref == id {
						document = id
						break
					}
				}
			} else if json.Unmarshal([]byte(p.Txt()), &document) != nil {
				continue
			}
			if !inputReference(document, id) {
				continue
			}
			dependencies = append(dependencies, n.ID+"/"+n.Parent+"/"+p.Type+"/"+p.Txt())
			result.Required = true
			if h.allows(identity, n.ID) {
				result.References = append(result.References, reuseItem{ID: n.ID, Type: p.Type, Name: nodeName(n), Reason: "输入变更可能影响此引用；本操作不执行设备命令"})
			} else {
				result.Restricted = true
			}
		}
	}
	sort.Strings(dependencies)
	body, _ := json.Marshal([]any{id, identity.UserID, action, input.Revision, input.OperationID, input.Value, dependencies})
	sum := sha256.Sum256(body)
	result.Confirmation = hex.EncodeToString(sum[:])
	return result, nil
}

func inputReference(value any, id string) bool {
	switch value := value.(type) {
	case string:
		return value == id
	case map[string]any:
		for _, child := range value {
			if inputReference(child, id) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if inputReference(child, id) {
				return true
			}
		}
	}
	return false
}
