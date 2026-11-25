package appapi

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

const screenPointType = "easyemsScreen"

// getScreen returns the nearest saved running-screen layout. Ancestor layouts
// are marked explicitly so an inherited draft is never mistaken for an own save.
func (h *Handler) getScreen(w http.ResponseWriter, identity Identity, id string) {
	node, ok := h.routeNode(w, id)
	if !ok {
		return
	}
	current := node
	for {
		point, found := current.Points.Find(screenPointType, "")
		if found && point.Tombstone%2 == 0 {
			raw := []byte(point.Txt())
			if current.ID == id {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(raw)
				return
			}
			var document map[string]any
			if json.Unmarshal(raw, &document) != nil {
				writeError(w, http.StatusInternalServerError, "invalid_stored_screen")
				return
			}
			document["inherited_from"] = current.ID
			writeJSON(w, document)
			return
		}
		if current.Parent == "" || current.Parent == "root" || current.Parent == current.ID {
			break
		}
		parent, ok := h.node(w, identity, current.Parent)
		if !ok {
			return
		}
		current = parent
	}
	writeError(w, http.StatusNotFound, "screen_not_found")
}

// putScreen stores layouts as node points. Structured documents are validated
// and versioned; legacy JSON remains readable and writable for existing clients.
func (h *Handler) putScreen(w http.ResponseWriter, r *http.Request, identity Identity, id string) {
	node, ok := h.routeNode(w, id)
	if !ok {
		return
	}
	if screenLayerKind(node) == "" {
		writeError(w, http.StatusBadRequest, "screen_requires_group")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<10))
	if err != nil || len(body) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_screen")
		return
	}
	var probe map[string]any
	if json.Unmarshal(body, &probe) != nil {
		writeError(w, http.StatusBadRequest, "invalid_screen")
		return
	}
	_, hasBlocks := probe["blocks"]
	_, hasSlots := probe["slots"]
	if hasBlocks || hasSlots {
		h.putStructuredScreen(w, identity, node, body, probe)
		return
	}
	if rawIDs, ok := probe["point_ids"]; ok {
		var ids []string
		if remarshalScreenBinding(rawIDs, &ids) != nil || h.validateScreenReferences(identity, id, ids) != nil {
			writeError(w, http.StatusBadRequest, "invalid_binding")
			return
		}
	}
	h.storeScreen(w, identity, id, body)
}

func (h *Handler) putStructuredScreen(w http.ResponseWriter, identity Identity, node data.NodeEdge, body []byte, probe map[string]any) {
	var document screenDocument
	if json.Unmarshal(body, &document) != nil || document.Layer != node.ID {
		writeError(w, http.StatusBadRequest, "invalid_screen")
		return
	}
	if _, present := probe["version"]; !present {
		writeError(w, http.StatusBadRequest, "invalid_version")
		return
	}
	if document.Version != savedScreenVersion(node) {
		writeError(w, http.StatusConflict, "version_conflict")
		return
	}
	targets := []data.NodeEdge{node}
	if apply, _ := probe["apply_to_same"].(bool); apply {
		var err error
		targets, err = h.sameKindScreenTargets(identity, node)
		if err != nil {
			writeError(w, http.StatusBadGateway, "save_failed")
			return
		}
	}
	payloads := make([][]byte, len(targets))
	for i, target := range targets {
		candidate := document
		candidate.Layer = target.ID
		candidate.Version = savedScreenVersion(target) + 1
		if err := h.validateScreenDocument(identity, target, &candidate); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		raw, err := json.Marshal(candidate)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_screen")
			return
		}
		payloads[i] = raw
	}
	for i, target := range targets {
		point := data.NewPointString(screenPointType, "", string(payloads[i]))
		point.Origin = identity.UserID
		if err := client.SendNodePoint(h.nc, target.ID, point, true); err != nil {
			writeError(w, http.StatusBadGateway, "save_failed")
			return
		}
	}
	writeJSON(w, map[string]any{"saved": true, "version": savedScreenVersion(node) + 1, "applied": len(targets)})
}

func (h *Handler) sameKindScreenTargets(identity Identity, selected data.NodeEdge) ([]data.NodeEdge, error) {
	project := selected
	for project.Parent != "" && project.Parent != "root" && project.Parent != project.ID {
		parents, err := client.GetNodes(h.nc, "all", project.Parent, "", false)
		if err != nil || len(parents) == 0 {
			return nil, err
		}
		project = parents[0]
	}
	nodes := []data.NodeEdge{}
	visited := map[string]bool{}
	var walk func(string) error
	walk = func(id string) error {
		if visited[id] {
			return nil
		}
		visited[id] = true
		children, err := client.GetNodes(h.nc, id, "all", "", false)
		if err != nil {
			return err
		}
		nodes = append(nodes, children...)
		for _, child := range children {
			if err := walk(child.ID); err != nil {
				return err
			}
		}
		return nil
	}
	nodes = append(nodes, project)
	if err := walk(project.ID); err != nil {
		return nil, err
	}
	kind := screenLayerKind(selected)
	targets := []data.NodeEdge{}
	for _, candidate := range nodes {
		if screenLayerKind(candidate) != kind || !h.allows(identity, candidate.ID) {
			continue
		}
		targets = append(targets, candidate)
	}
	return targets, nil
}

func (h *Handler) storeScreen(w http.ResponseWriter, identity Identity, id string, body []byte) {
	point := data.NewPointString(screenPointType, "", string(body))
	point.Origin = identity.UserID
	if err := client.SendNodePoint(h.nc, id, point, true); err != nil {
		writeError(w, http.StatusBadGateway, "save_failed")
		return
	}
	writeJSON(w, map[string]bool{"saved": true})
}
