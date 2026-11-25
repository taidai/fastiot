package appapi

import (
	"encoding/json"
	"strings"
	"time"

	"fastiot/internal/decision"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

type ruleAccessView struct {
	Draft       bool `json:"draft_restricted,omitempty"`
	Applied     bool `json:"applied_restricted,omitempty"`
	History     bool `json:"history_restricted,omitempty"`
	Trace       bool `json:"trace_restricted,omitempty"`
	Diagnostics bool `json:"diagnostics_restricted,omitempty"`
}

func projectedRuleText(point data.Point, text string) data.Point {
	projected := data.NewPointString(point.Type, point.Key, text)
	projected.Time, projected.Origin, projected.Tombstone = point.Time, point.Origin, point.Tombstone
	return projected
}

// Read visibility is not execution eligibility: disabled/unverified but visible
// references remain readable. Never mutate the stored definition or evidence.
func (h *Handler) readableRuleDocument(identity Identity, doc decision.Document) bool {
	for _, target := range doc.Bindings.Points {
		if !h.inputVisible(identity, target) {
			return false
		}
	}
	for _, bindings := range []map[string]decision.Target{doc.Bindings.Points, doc.Bindings.Actions} {
		for _, target := range bindings {
			if target.NodeID == "" || !h.allows(identity, target.NodeID) {
				return false
			}
		}
	}
	for _, row := range doc.Table.Rows {
		for _, action := range row.Actions {
			if action.Readback != nil && action.Readback.Target.NodeID != "" && !h.allows(identity, action.Readback.Target.NodeID) {
				return false
			}
		}
	}
	return true
}

func (h *Handler) readableRuleTrace(identity Identity, nodeID string, report decision.CheckReport, doc decision.Document) bool {
	if report.RuleID != nodeID {
		return false
	}
	for _, input := range report.Inputs {
		if input.Observation != nil && !h.inputEvidenceVisible(identity, *input.Observation) {
			return false
		}
		id := input.Target.NodeID
		if id == "" {
			id = doc.Bindings.Points[input.Input].NodeID
		}
		if id == "" && (strings.HasPrefix(input.Input, "time:") || strings.HasPrefix(input.Input, "__decision_")) {
			continue
		}
		if id == "" || !h.allows(identity, id) {
			return false
		}
	}
	for _, action := range report.Actions {
		if action.Kind == "notify" {
			declared := false
			for _, row := range doc.Table.Rows {
				if row.ID == report.Row {
					for _, source := range row.Actions {
						declared = declared || (source.ActionID == action.Output && source.Notification != nil)
					}
				}
			}
			if !declared || action.CommandID != "" || action.Target.NodeID != "" {
				return false
			}
			continue
		}
		id := action.Target.NodeID
		if id == "" {
			id = doc.Bindings.Actions[action.Output].NodeID
		}
		if id == "" || !h.allows(identity, id) {
			return false
		}
		for _, share := range action.Shares {
			if !h.allows(identity, share.MemberID) {
				return false
			}
		}
		if action.CommandID != "" {
			manager := h.commandManager()
			if manager == nil {
				return false
			}
			outcome, found, err := manager.Find(action.CommandID)
			if err != nil || !found {
				return false
			}
			for _, write := range outcome.Writes {
				if !h.allows(identity, write.Point) {
					return false
				}
			}
			for _, readback := range outcome.Readback {
				if !h.allows(identity, readback.Point) {
					return false
				}
			}
			for _, observed := range outcome.Observed {
				if !h.allows(identity, observed.Point) {
					return false
				}
			}
		}
	}
	return true
}

// publicRuleNodes is an outbound projection only. Apply it at both snapshot and
// WS exits, not to visibleNodes (which engineering and persistence also use).
func (h *Handler) publicRuleNodes(identity Identity, nodes []data.NodeEdge) []data.NodeEdge {
	out := append([]data.NodeEdge(nil), nodes...)
	root, rootErr := client.GetRootNode(h.nc)
	fullScope := rootErr == nil && h.allows(identity, root.ID)
	for i, node := range out {
		if node.Type != "decisionTable" {
			continue
		}
		access := ruleAccessView{}
		documents := map[string]decision.Document{}
		readable := map[string]bool{}
		pointAllowed := map[string]bool{}
		for _, point := range node.Points {
			if point.Tombstone%2 != 0 {
				continue
			}
			var doc decision.Document
			var versionID string
			switch point.Type {
			case "draft", "applied":
				if point.Txt() == "" {
					continue
				}
				ok := json.Unmarshal([]byte(point.Txt()), &doc) == nil && h.readableRuleDocument(identity, doc)
				pointAllowed[point.Type+"/"+point.Key] = ok
				if point.Type == "draft" {
					access.Draft = !ok
				} else {
					access.Applied = !ok
				}
				versionID = doc.Revision
			case "history":
				var version RuleVersion
				ok := json.Unmarshal([]byte(point.Txt()), &version) == nil && h.readableRuleDocument(identity, version.Document)
				pointAllowed[point.Type+"/"+point.Key] = ok
				access.History = access.History || !ok
				doc, versionID = version.Document, version.ID
			default:
				continue
			}
			if versionID != "" {
				documents[versionID] = doc
				readable[versionID] = pointAllowed[point.Type+"/"+point.Key]
			}
			if doc.Revision != "" {
				documents[doc.Revision] = doc
				readable[doc.Revision] = pointAllowed[point.Type+"/"+point.Key]
			}
		}
		out[i].Points = nil
		hasReadableTrace := false
		for _, point := range node.Points {
			if point.Tombstone%2 != 0 {
				continue
			}
			switch point.Type {
			case ruleAuthorizationPoint, "ruleAccess":
				continue
			case "draft", "applied", "history":
				if point.Txt() != "" && !pointAllowed[point.Type+"/"+point.Key] {
					continue
				}
			case "lastCheck":
				if point.Txt() == "" {
					continue
				}
				var report decision.CheckReport
				if json.Unmarshal([]byte(point.Txt()), &report) != nil || !readable[report.SourceRevision] || !h.readableRuleTrace(identity, node.ID, report, documents[report.SourceRevision]) {
					access.Trace = true
					continue
				}
				hasReadableTrace = true
				// Free-form runtime diagnostics can name external safety inputs whose old
				// configuration is not part of this report. Do not infer their visibility.
				if !fullScope {
					changed := false
					for n := range report.Actions {
						if report.Actions[n].Reason != "" {
							report.Actions[n].Reason = "运行诊断详情受限"
							changed = true
						}
					}
					if changed {
						raw, _ := json.Marshal(report)
						point = projectedRuleText(point, string(raw))
						access.Diagnostics = true
					}
				}
			case "lastExecution":
				var reservation decision.ExecutionReservation
				if json.Unmarshal([]byte(point.Txt()), &reservation) != nil || !readable[reservation.Revision] {
					access.Trace = true
					continue
				}
			case "lastMatch", "error":
				continue // emitted after trace visibility is known
			}
			out[i].Points = append(out[i].Points, point)
		}
		for _, point := range node.Points {
			if point.Tombstone%2 != 0 {
				continue
			}
			switch point.Type {
			case "lastMatch":
				if point.Txt() != "" && (!hasReadableTrace || access.Trace) {
					access.Trace = true
					continue
				}
			case "error":
				if point.Txt() != "" && (!fullScope || !hasReadableTrace || access.Trace) {
					point = projectedRuleText(point, "运行诊断详情受限")
					access.Diagnostics = true
				}
			default:
				continue
			}
			out[i].Points = append(out[i].Points, point)
		}
		if access != (ruleAccessView{}) {
			raw, _ := json.Marshal(access)
			flag := data.NewPointString("ruleAccess", "0", string(raw))
			flag.Time = time.Time{}
			out[i].Points = append(out[i].Points, flag)
		}
	}
	return out
}
