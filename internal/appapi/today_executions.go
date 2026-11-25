package appapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"fastiot/internal/dailystats"
	"fastiot/internal/decision"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

type executionActionView struct {
	ID              string   `json:"id"`
	Output          string   `json:"output"`
	Target          string   `json:"target,omitempty"`
	Kind            string   `json:"kind,omitempty"`
	Status          string   `json:"status"`
	Reason          string   `json:"reason,omitempty"`
	Confirmation    string   `json:"confirmation,omitempty"`
	CommandID       string   `json:"command_id,omitempty"`
	NotificationID  string   `json:"notification_id,omitempty"`
	CanonicalStatus string   `json:"canonical_status,omitempty"`
	Targets         []string `json:"targets,omitempty"`
	recordedTargets []string
	receiptScope    string
}
type executionView struct {
	ID             string                `json:"id"`
	SourceID       string                `json:"source_id"`
	SourceType     string                `json:"source_type"`
	SourceName     string                `json:"source_name"`
	SourceRevision string                `json:"source_revision"`
	ExecutionID    string                `json:"execution_id"`
	At             time.Time             `json:"at"`
	Status         string                `json:"status"`
	Reason         string                `json:"reason"`
	Counted        bool                  `json:"counted"`
	Actions        []executionActionView `json:"actions"`
}
type executionPage struct {
	Items          []executionView   `json:"items"`
	Total          int               `json:"total"`
	ExecutionCount int               `json:"execution_count"`
	Offset         int               `json:"offset"`
	Limit          int               `json:"limit"`
	Window         dailystats.Window `json:"window"`
	Restricted     bool              `json:"restricted"`
	Coverage       struct {
		Complete    bool   `json:"complete"`
		Rules       string `json:"rules"`
		Machines    string `json:"machines"`
		TargetScope string `json:"target_scope"`
	} `json:"coverage"`
}

// Command outcomes remain owned by the canonical journal. Historical process
// status is not upgraded by a later command result. Every payload reference is
// rechecked even though this projection returns only identity/status metadata.
func (h *Handler) executionCommand(identity Identity, id string, action *executionActionView) bool {
	if id == "" {
		return true
	}
	action.CommandID = id
	action.CanonicalStatus = "unknown"
	manager := h.commandManager()
	if manager == nil {
		return true
	}
	outcome, found, err := manager.Find(id)
	if err != nil {
		return false
	}
	if !found {
		return true
	}
	for _, write := range outcome.Writes {
		action.recordedTargets = append(action.recordedTargets, write.Point)
		if !h.allows(identity, write.Point) {
			return false
		}
	}
	for _, read := range outcome.Readback {
		if !h.allows(identity, read.Point) {
			return false
		}
	}
	for _, sample := range outcome.Observed {
		if !h.allows(identity, sample.Point) {
			return false
		}
	}
	action.CanonicalStatus = string(outcome.State)
	if action.Confirmation == "" && action.Status == "confirmed" {
		if outcome.External {
			action.Confirmation = "software"
		} else if len(outcome.Observed) > 0 {
			action.Confirmation = "device_readback"
		}
	}
	return true
}

// Ancestor sources qualify only through recorded action targets. Reuse the
// screen's tree/member scope resolver, but never today's transport/Profile
// binding to invent a historical business owner for an old raw point.
func (h *Handler) executionLayer(identity Identity, layer string, row executionView, local bool) (executionView, bool) {
	actions := []executionActionView{}
	hidden := false
	for _, action := range row.Actions {
		targets := append([]string{action.Target}, action.recordedTargets...)
		matches := map[string]bool{}
		for _, id := range targets {
			if id != "" && !matches[id] && h.allows(identity, id) && h.screenReferenceInLayer(identity, layer, id) {
				action.Targets = append(action.Targets, id)
				matches[id] = true
			}
		}
		receiptInLayer := action.receiptScope != "" && h.screenReferenceInLayer(identity, layer, action.receiptScope)
		if len(action.Targets) == 0 && !receiptInLayer && !(local && action.Target == "" && len(action.recordedTargets) == 0 && action.Kind != "notify") {
			hidden = true
			continue
		}
		if !matches[action.Target] {
			action.Target = ""
		}
		if !local && action.Reason != "" {
			action.Reason = "跨层执行诊断未包含在本层投影"
		}
		actions = append(actions, action)
	}
	if !local && len(actions) == 0 {
		return row, false
	}
	row.Actions = actions
	if !local || hidden {
		row.Reason = "跨层来源；仅展示记录目标属于本层的动作"
	}
	return row, true
}

func (h *Handler) ruleExecutionViews(identity Identity, node data.NodeEdge, fullScope bool) ([]executionView, bool, error) {
	facts := map[string]decision.RuleExecutionFact{}
	denied := map[string]bool{}
	restricted := false
	for _, point := range node.Points {
		if point.Type != decision.RuleExecutionFactPoint || point.Tombstone%2 != 0 {
			continue
		}
		var fact decision.RuleExecutionFact
		if json.Unmarshal([]byte(point.Txt()), &fact) != nil || fact.Schema != 1 || fact.Sequence == 0 || fact.Report.ExecutionID == "" || fact.Report.RuleID != node.ID || fact.Report.SourceRevision != fact.Source.Revision || fact.StartedAt.IsZero() {
			return nil, false, fmt.Errorf("invalid execution fact")
		}
		id := fact.Report.ExecutionID
		if !h.canReadNotification(identity, fact.ScopeNodeID) || !h.readableRuleDocument(identity, fact.Source) || !h.readableRuleTrace(identity, node.ID, fact.Report, fact.Source) {
			denied[id] = true
			restricted = true
		}
		if old, ok := facts[id]; !ok || fact.Sequence > old.Sequence {
			facts[id] = fact
		}
	}
	rows := []executionView{}
	build := func(report decision.CheckReport, at time.Time, counted bool, scope string) {
		key := report.ExecutionID
		if key == "" {
			key = report.At.String()
		}
		row := executionView{ID: machineIdentity("rule", node.ID, report.SourceRevision, key), SourceID: node.ID, SourceType: "rule", SourceName: nodeName(node), SourceRevision: report.SourceRevision, ExecutionID: report.ExecutionID, At: at, Status: report.State, Reason: report.State, Counted: counted, Actions: []executionActionView{}}
		if report.State == "reserved" {
			row.Status = "unknown"
			row.Reason = "execution reserved without a recorded outcome; never replayed"
		}
		for i, check := range report.Actions {
			a := executionActionView{Output: check.Output, Target: check.Target.NodeID, Kind: check.Kind, Status: check.Status, Reason: check.Reason, Confirmation: check.Confirmation, NotificationID: check.NotificationID}
			if a.Kind == "notify" {
				a.receiptScope = scope
			}
			for _, share := range check.Shares {
				a.recordedTargets = append(a.recordedTargets, share.MemberID)
			}
			if report.ExecutionID != "" {
				a.ID = fmt.Sprintf("%s-%d", report.ExecutionID, i)
			}
			if !counted && a.Kind == "notify" && (a.NotificationID != "" || a.Status == "created") {
				a.NotificationID = ""
				a.Status = "unknown"
				a.Confirmation = ""
				a.Reason = "legacy receipt scope not captured"
			}
			if !h.executionCommand(identity, check.CommandID, &a) {
				restricted = true
				return
			}
			if !fullScope && a.Reason != "" {
				a.Reason = "运行诊断详情受限"
			}
			row.Actions = append(row.Actions, a)
		}
		rows = append(rows, row)
	}
	for id, fact := range facts {
		if !denied[id] {
			build(fact.Report, fact.StartedAt, true, fact.ScopeNodeID)
		}
	}
	// Legacy/no-execution checks are explicitly uncounted. Use the established
	// scoped outbound projection; a filtered fact must never reappear via fallback.
	projected := h.publicRuleNodes(identity, []data.NodeEdge{node})[0]
	for _, point := range projected.Points {
		if point.Type != "lastCheck" {
			continue
		}
		var report decision.CheckReport
		if json.Unmarshal([]byte(point.Txt()), &report) != nil {
			return nil, false, fmt.Errorf("invalid rule trace")
		}
		if _, captured := facts[report.ExecutionID]; captured {
			continue
		}
		build(report, report.At, false, node.Parent)
	}
	return rows, restricted, nil
}

func (h *Handler) todayExecutions(w http.ResponseWriter, r *http.Request, identity Identity) {
	limit, ok := notificationQuery(r, "layer", "date", "timezone", "limit", "offset", "source")
	if !ok {
		writeError(w, 400, "invalid_execution_query")
		return
	}
	q := r.URL.Query()
	layer := q.Get("layer")
	if layer == "" {
		writeError(w, 400, "layer_required")
		return
	}
	if _, ok := h.routeNode(w, layer); !ok {
		return
	}
	offset := 0
	if q.Get("offset") != "" {
		var err error
		offset, err = strconv.Atoi(q.Get("offset"))
		if err != nil || offset < 0 {
			writeError(w, 400, "invalid_offset")
			return
		}
	}
	source := q.Get("source")
	if source != "" && source != "rule" && source != "machine" {
		writeError(w, 400, "invalid_execution_source")
		return
	}
	window, err := dailystats.Day(q.Get("date"), q.Get("timezone"), time.Now())
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	nodes, err := h.visibleNodes(identity)
	if err != nil {
		writeError(w, 503, "execution_sources_unavailable")
		return
	}
	root, err := client.GetRootNode(h.nc)
	if err != nil {
		writeError(w, 503, "project_context_unavailable")
		return
	}
	fullScope := h.allows(identity, root.ID)
	page := executionPage{Items: []executionView{}, Limit: limit, Offset: offset, Window: window}
	page.Coverage.Rules = "immutable execution captures since instrumentation; older checks latest-only and uncounted; retained until source deletion; pre-capture gaps unknown"
	page.Coverage.Machines = "durable_event_keys; retained until source deletion; legacy history may be absent"
	page.Coverage.TargetScope = "ancestor sources require recorded target association; unassociated legacy transport points are not remapped through current Profile bindings"
	items := map[string]executionView{}
	for _, node := range nodes {
		if node.Type != stateMachineType && node.Type != "decisionTable" {
			continue
		}
		local := node.ID == layer || h.users.IsUnder(node.ID, layer)
		if !local && !h.users.IsUnder(layer, node.Parent) {
			continue
		}
		if node.Type == stateMachineType && (source == "" || source == "machine") {
			document, err := decodeStateMachine(node)
			if err != nil {
				writeError(w, 503, "invalid_state_machine_document")
				return
			}
			runs, err := machineRuns(node)
			if err != nil {
				writeError(w, 503, "invalid_state_machine_run")
				return
			}
			for _, run := range runs {
				e := run.Execution
				if !h.machineExecutionVisible(identity, &e, document, runs) {
					page.Restricted = true
					continue
				}
				row := executionView{ID: machineIdentity("machine", node.ID, run.RequestID), SourceID: node.ID, SourceType: "machine", SourceName: nodeName(node), SourceRevision: strconv.FormatUint(e.Version, 10), ExecutionID: run.RequestID, At: e.At, Status: e.Status, Reason: e.Reason, Counted: true, Actions: []executionActionView{}}
				if !run.Finished {
					row.Status = "unknown"
					row.Reason = "reserved event without a finished process result; not replayed"
				}
				allowed := true
				for i, reference := range e.Commands {
					a := executionActionView{ID: reference.RequestID, Target: reference.NodeID, Status: "unknown"}
					if i < len(e.Actions) {
						check := e.Actions[i]
						a.Output, a.Status, a.Reason, a.Confirmation = check.Output, check.Status, check.Reason, check.Confirmation
					}
					allowed = allowed && h.executionCommand(identity, reference.RequestID, &a)
					if !fullScope && a.Reason != "" {
						a.Reason = "运行诊断详情受限"
					}
					row.Actions = append(row.Actions, a)
				}
				if !allowed {
					page.Restricted = true
					continue
				}
				if !fullScope && row.Reason != "" {
					row.Reason = "运行诊断详情受限"
				}
				if scoped, include := h.executionLayer(identity, layer, row, local); include {
					items[scoped.ID] = scoped
				}
			}
		}
		if node.Type == "decisionTable" && (source == "" || source == "rule") {
			rows, restricted, err := h.ruleExecutionViews(identity, node, fullScope)
			if err != nil {
				writeError(w, 503, "invalid_rule_execution_evidence")
				return
			}
			page.Restricted = page.Restricted || restricted
			for _, row := range rows {
				if scoped, include := h.executionLayer(identity, layer, row, local); include {
					items[scoped.ID] = scoped
				}
			}
		}
	}
	for _, row := range items {
		if !row.At.Before(window.Start) && row.At.Before(window.End) {
			page.Items = append(page.Items, row)
			if row.Counted {
				page.ExecutionCount++
			}
		}
	}
	sort.Slice(page.Items, func(i, j int) bool {
		if page.Items[i].At.Equal(page.Items[j].At) {
			return page.Items[i].ID < page.Items[j].ID
		}
		return page.Items[i].At.After(page.Items[j].At)
	})
	page.Total = len(page.Items)
	start := min(offset, page.Total)
	page.Items = page.Items[start : start+min(limit, page.Total-start)]
	writeJSON(w, page)
}
