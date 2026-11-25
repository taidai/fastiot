package appapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"

	"fastiot/internal/authz"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

// Both stored policy roots and actual user edges must be inside the manager's
// scope. Looking at only one edge would expose mirrored accounts across sites.
func (h *Handler) accountInScope(manager Identity, nodes []data.NodeEdge) bool {
	if len(nodes) == 0 {
		return false
	}
	for _, node := range nodes {
		if !h.allows(manager, node.Parent) {
			return false
		}
		var policy AccessPolicy
		if p, ok := node.Points.Find("easyemsAccess", ""); ok {
			_ = json.Unmarshal([]byte(p.Txt()), &policy)
		}
		if policy.Admin && !manager.Policy.Admin {
			return false
		}
		for _, root := range policy.RootNodeIDs {
			if !h.allows(manager, root) {
				return false
			}
		}
	}
	return true
}

func canGrantPolicy(manager Identity, role string, actions []string) bool {
	if manager.Policy.Admin {
		return true
	}
	if role == "admin" {
		return false
	}
	for action := range authz.Resolve(false, role, actions) {
		if !canAction(manager, action) {
			return false
		}
	}
	return true
}

func (h *Handler) validAccountParent(manager Identity, id string) bool {
	if id == "" || !h.allows(manager, id) {
		return false
	}
	nodes, err := client.GetNodes(h.nc, "all", id, "", false)
	if err != nil || len(nodes) == 0 {
		return false
	}
	return nodes[0].Type != data.NodeTypeUser
}

type authorizationCheck struct {
	Operation string `json:"operation"`
	Action    string `json:"action"`
	Allowed   bool   `json:"allowed"`
	Reason    string `json:"reason"`
}

func (h *Handler) explainUserAuthorization(w http.ResponseWriter, r *http.Request, manager Identity, id string) {
	nodes, err := client.GetNodes(h.nc, "all", id, data.NodeTypeUser, false)
	if err != nil || !h.accountInScope(manager, nodes) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	target := r.URL.Query().Get("target")
	// Check the requesting manager before loading any target or subject detail.
	if target == "" || !h.allows(manager, target) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	targets, err := client.GetNodes(h.nc, "all", target, "", false)
	if err != nil || len(targets) == 0 {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	subject, active := h.identityForUser(id)
	checks := []authorizationCheck{}
	selected := r.URL.Query().Get("operation")
	for _, operation := range []string{"overview", "command", "configure", "publish", "account_manage", "audit_view"} {
		if selected != "" && selected != operation {
			continue
		}
		method, path := http.MethodGet, "/device-ledger?site="+url.QueryEscape(target)
		var body []byte
		switch operation {
		case "command":
			method, path = http.MethodPost, "/modbus/"+url.PathEscape(target)+"/units/1/commands"
		case "configure":
			method, path = http.MethodPatch, "/nodes/"+url.PathEscape(target)
		case "publish":
			method, path = http.MethodPost, "/projects/publish"
			body, _ = json.Marshal(map[string]string{"site": target})
		case "account_manage":
			path = "/users"
		case "audit_view":
			path = "/audit"
		}
		request, _ := http.NewRequest(method, path, bytes.NewReader(body))
		requirement, declared := authz.Required(request)
		check := authorizationCheck{Operation: operation, Action: requirement.Action, Reason: "no_access"}
		if active && declared {
			status, reason := h.routeAuthorization(request, subject)
			check.Allowed, check.Reason = status == http.StatusOK, reason
			// Global-list routes still need an explicit object scope for this matrix.
			if check.Allowed && !h.allows(subject, target) {
				check.Allowed, check.Reason = false, "forbidden"
			}
		}
		checks = append(checks, check)
	}
	if len(checks) == 0 {
		writeError(w, http.StatusBadRequest, "operation_not_declared")
		return
	}
	// This is authorization only, not capability/parameter/safety validation.
	// No session, token, command client, or action handler is called.
	writeJSON(w, map[string]any{"read_only": true, "user_id": id, "target_id": target, "checks": checks})
}
