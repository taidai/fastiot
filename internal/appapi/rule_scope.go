package appapi

import (
	"encoding/json"
	"net/http"

	"fastiot/internal/decision"
	domainaccess "fastiot/internal/domain/access"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

// RuleParentTypes lets the SIOT manager discover rules mounted on business
// objects as well as its built-in group hierarchy.
func RuleParentTypes() []string {
	return []string{domainaccess.NodeSiteRoot, domainaccess.NodeSite, domainaccess.NodePlant, domainaccess.NodeArea, domainaccess.NodeUnit, domainaccess.NodeEnergyUnit, domainaccess.NodeDevice}
}

// ruleTargetAllowed uses the same member and device mapping declarations as the
// ledger and screen bindings. A business relationship never expands actor anchors.
func (h *Handler) ruleTargetAllowed(identity Identity, parent, scope string, target decision.Target, output bool) bool {
	if !h.allows(identity, parent) || !h.allows(identity, target.NodeID) {
		return false
	}
	nodes, err := client.GetNodes(h.nc, "all", target.NodeID, "", false)
	if err != nil || len(nodes) == 0 {
		return false
	}
	node := nodes[0]
	if node.Type != data.NodeTypeVariable && target.Key != "" && target.Key != "0" {
		return false
	}
	if decision.ValidateTarget(h.nc, node.Parent, target, output, nil) != nil {
		return false
	}
	owner := node.ID
	if node.Type == data.NodeTypeModbusIO {
		channels, err := client.GetNodes(h.nc, "all", node.Parent, "", false)
		if err != nil || len(channels) == 0 {
			return false
		}
		server, _ := node.Points.ValueInt(data.PointTypeID, "")
		if model, bound := unitModelFor(channels[0], server); bound && model.BusinessNodeID != "" {
			mapped := false
			for _, id := range model.PointMap {
				mapped = mapped || id == node.ID
			}
			if !mapped || !h.allows(identity, model.BusinessNodeID) {
				return false
			}
			owner = model.BusinessNodeID
		}
	}
	if scope == "descendants" {
		return h.screenReferenceInLayer(identity, parent, owner)
	}
	if scope != "self" {
		return false
	}
	if owner == parent || (owner == node.ID && node.Parent == parent) {
		return true
	}
	parents, err := client.GetNodes(h.nc, "all", parent, "", false)
	if err != nil || len(parents) == 0 || (parents[0].Type != domainaccess.NodeUnit && parents[0].Type != domainaccess.NodeEnergyUnit) {
		return false
	}
	text, _ := parents[0].Points.Text(domainaccess.MembersPoint, "")
	var members []domainaccess.MemberRole
	if json.Unmarshal([]byte(text), &members) != nil {
		return false
	}
	for _, member := range members {
		if member.NodeID != "" && h.allows(identity, member.NodeID) && (owner == member.NodeID || node.ID == member.NodeID || (owner == node.ID && node.Parent == member.NodeID)) {
			return true
		}
	}
	return false
}

// Rule scope never grants authority: references must also be visible to the actor.
func (h *Handler) checkedRuleDocument(w http.ResponseWriter, identity Identity, rule data.NodeEdge, document decision.Document) bool {
	if document.Scope != "self" && document.Scope != "descendants" {
		writeError(w, http.StatusBadRequest, "rule_scope_required")
		return false
	}
	if !h.ruleReferencesAllowed(identity, rule.Parent, document) {
		writeError(w, http.StatusForbidden, "target_out_of_scope")
		return false
	}
	return true
}

func (h *Handler) ruleReferencesAllowed(identity Identity, parent string, document decision.Document) bool {
	if !h.allows(identity, parent) || (document.Scope != "self" && document.Scope != "descendants") {
		return false
	}
	check := func(target decision.Target, output bool) bool {
		return h.ruleTargetAllowed(identity, parent, document.Scope, target, output)
	}
	for _, target := range document.Bindings.Points {
		if !check(target, false) {
			return false
		}
	}
	for _, target := range document.Bindings.Actions {
		if !check(target, true) {
			return false
		}
	}
	for _, row := range document.Table.Rows {
		for _, action := range row.Actions {
			if action.Notification != nil && h.ValidateNotificationRecipients(parent, action.Notification.RecipientIDs) != nil {
				return false
			}
			if action.Readback != nil && action.Readback.Target.NodeID != "" && !check(action.Readback.Target, false) {
				return false
			}
		}
	}
	writes, err := h.ruleWriteTargets(document)
	if err != nil {
		return false
	}
	for _, target := range writes {
		if !check(target, true) {
			return false
		}
	}
	return true
}
