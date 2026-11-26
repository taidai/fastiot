package appapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	siotclient "github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/decision"
	"fastiot/internal/notification"
)

func TestTicket21ImportReferenceClosureAndRecipientRebind(t *testing.T) {
	source, sourceToken, sourceHandler, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, source, sourceToken)
	site := createNodeAPI(t, source, sourceToken, NodeTypeSite, root, "Reference closure project", 0)
	group := createNodeAPI(t, source, sourceToken, "group", site, "Aggregate owner", 0)
	member := createNodeAPI(t, source, sourceToken, data.NodeTypeVariable, group, "Shared point", 0)
	device := createNodeAPI(t, source, sourceToken, data.NodeTypeDevice, site, "Command device", 0)
	machine, err := CreateDomainNode(sourceHandler.nc, site, stateMachineType, map[string]string{"description": "Process"})
	if err != nil {
		t.Fatal(err)
	}
	sourcePassword := data.NewID()
	sourceRecipient := inboxAccount(t, source, sourceToken, site, "source-recipient@fixture.local", sourcePassword)
	store := func(id, typ string, value any) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := siotclient.SendNodePoint(sourceHandler.nc, id, data.NewPointString(typ, "0", string(raw)), true); err != nil {
			t.Fatal(err)
		}
	}
	store(group, aggregatePoint, AggregateConfig{Group: group, Members: []string{member}, Op: "avg"})
	store(site, dailyStatsPoint, map[string]any{"version": 1, "time_zone": "UTC", "freshness": map[string]any{"point_id": member, "max_age_seconds": 60}})
	store(site, screenPointType, screenDocument{
		Layer: site, Version: 1, Columns: 2, PointIDs: []string{member},
		Slots: map[string][]string{"top": {}, "main": {"recent"}, "side": {"trend"}},
		Blocks: []screenBlock{
			{Type: "recent", Span: 1, Binding: map[string]any{"point_ids": []string{member}, "columns": []string{"value"}, "rows": 3}},
			{Type: "trend", Span: 1, Binding: map[string]any{"point_id": member}},
		},
	})
	if err := siotclient.SendNodePoint(sourceHandler.nc, device, data.NewPointString(PointTypePowerSetpoint, "0", member), true); err != nil {
		t.Fatal(err)
	}
	store(machine, stateMachineDocumentPoint, decision.StateMachineDocument{
		Schema: 1, Revision: 1,
		Draft: decision.StateMachineDefinition{
			InitialState: "idle", States: []string{"idle"},
			CommandBindings: map[string]decision.StateMachineCommandBinding{"start": {DeviceID: device, CommandID: "start"}},
		},
		CurrentState: "old-runtime-state", LastExecution: &decision.StateMachineExecution{RequestID: "old-command-request"},
	})
	document := decision.Document{
		Scope: "self", Revision: "source-rule-revision",
		Table: decision.Table{Rows: []decision.Row{{ID: "row", Actions: []decision.Action{
			{ActionID: "notice", Notification: &decision.RuleNotification{RecipientIDs: []string{sourceRecipient}, Title: "Engineering notice"}},
			{ActionID: "write", Value: 1, Readback: &decision.Readback{Target: decision.Target{NodeID: member}, TimeoutMS: 1000}},
		}}}},
		Bindings: decision.BindingSet{Actions: map[string]decision.Target{"write": {NodeID: member, PointType: data.PointTypeValueSet}}},
	}
	var rule struct {
		ID       string `json:"id"`
		Revision string `json:"revision"`
	}
	status, body := call(t, http.MethodPost, source+"/rules", sourceToken, map[string]any{"parent": group, "description": "Bound notification rule", "document": document}, &rule)
	if status != http.StatusOK {
		t.Fatalf("source rule: %d %s", status, body)
	}
	document.Revision = rule.Revision
	status, body = call(t, http.MethodPost, source+"/rules/"+rule.ID+"/publish", sourceToken, document, nil)
	if status != http.StatusOK {
		t.Fatalf("source rule publish: %d %s", status, body)
	}
	var pkg projectPackage
	status, body = call(t, http.MethodGet, source+"/projects/export?site="+site, sourceToken, nil, &pkg)
	if status != http.StatusOK {
		t.Fatalf("source export: %d %s", status, body)
	}

	target, targetToken, _, _ := startInputServer(t, t.TempDir(), data.NewID())
	var imported importProjectResult
	status, body = call(t, http.MethodPost, target+"/projects/import", targetToken, map[string]any{"package": pkg}, &imported)
	if status != http.StatusOK {
		t.Fatalf("target import: %d %s", status, body)
	}
	if strings.Contains(body, sourceRecipient) {
		t.Fatal("import response inherited the source account")
	}
	byName := map[string]data.NodeEdge{}
	for _, node := range snapshotNodes(t, target, targetToken) {
		byName[node.Desc()] = node
		if node.ID == sourceRecipient {
			t.Fatal("source account authority was recreated")
		}
	}
	newGroup, newMember, newDevice := byName["Aggregate owner"], byName["Shared point"], byName["Command device"]
	newMachine, newRule := byName["Process"], byName["Bound notification rule"]
	newSite := byName["Reference closure project"]
	if newGroup.ID == "" || newMember.ID == "" || newDevice.ID == "" || newMachine.ID == "" || newRule.ID == "" || newSite.ID == "" {
		t.Fatalf("imported reference nodes missing: %v", byName)
	}
	var aggregate AggregateConfig
	aggregateText, _ := newGroup.Points.Text(aggregatePoint, "")
	if json.Unmarshal([]byte(aggregateText), &aggregate) != nil || aggregate.Group != newGroup.ID || len(aggregate.Members) != 1 || aggregate.Members[0] != newMember.ID {
		t.Fatalf("aggregate owner/member references: %+v", aggregate)
	}
	var daily struct {
		Freshness struct {
			PointID string `json:"point_id"`
		} `json:"freshness"`
	}
	dailyText, _ := newSite.Points.Text(dailyStatsPoint, "")
	if json.Unmarshal([]byte(dailyText), &daily) != nil || daily.Freshness.PointID != newMember.ID {
		t.Fatalf("daily stats reference: %s", dailyText)
	}
	var screen screenDocument
	screenText, _ := newSite.Points.Text(screenPointType, "")
	if json.Unmarshal([]byte(screenText), &screen) != nil || screen.Layer != imported.SiteID || len(screen.PointIDs) != 1 || screen.PointIDs[0] != newMember.ID {
		t.Fatalf("screen references: %s", screenText)
	}
	for _, block := range screen.Blocks {
		encoded, _ := json.Marshal(block.Binding)
		if !strings.Contains(string(encoded), newMember.ID) || strings.Contains(string(encoded), member) {
			t.Fatalf("screen block has an unmapped reference: %s", encoded)
		}
	}
	if setpoint, _ := newDevice.Points.Text(PointTypePowerSetpoint, ""); setpoint != newMember.ID {
		t.Fatalf("raw point reference = %q, want %q", setpoint, newMember.ID)
	}
	var machineView stateMachineResponse
	status, body = call(t, http.MethodGet, target+"/state-machines/"+newMachine.ID, targetToken, nil, &machineView)
	if status != http.StatusOK || machineView.Draft == nil || machineView.Draft.CommandBindings["start"].DeviceID != newDevice.ID || machineView.CurrentState != "idle" || machineView.LastExecution != nil {
		t.Fatalf("machine import restored an old device or runtime: %d %s", status, body)
	}
	var rebound decision.Document
	draftText, _ := newRule.Points.Text("draft", "")
	if json.Unmarshal([]byte(draftText), &rebound) != nil {
		t.Fatalf("imported rule draft: %s", draftText)
	}
	if rebound.Bindings.Actions["write"].NodeID != newMember.ID || rebound.Table.Rows[0].Actions[1].Readback.Target.NodeID != newMember.ID {
		t.Fatalf("rule references: %+v", rebound)
	}
	notice := rebound.Table.Rows[0].Actions[0].Notification
	if notice == nil || len(notice.RecipientIDs) != 0 || notice.Title != "Engineering notice" {
		t.Fatalf("recipient rebinding was not forced: %+v", notice)
	}
	status, body = call(t, http.MethodPost, target+"/rules/"+newRule.ID+"/publish", targetToken, rebound, nil)
	if status != http.StatusBadRequest || !strings.Contains(body, "invalid_rule_notification") {
		t.Fatalf("empty recipients published: %d %s", status, body)
	}

	otherSite := createNodeAPI(t, target, targetToken, NodeTypeSite, snapshotRoot(t, target, targetToken), "Other target scope", 0)
	targetPassword := data.NewID()
	eligible := inboxAccount(t, target, targetToken, imported.SiteID, "eligible@fixture.local", targetPassword)
	outside := inboxAccount(t, target, targetToken, otherSite, "outside@fixture.local", targetPassword)
	notice.RecipientIDs = []string{outside}
	status, body = call(t, http.MethodPut, target+"/rules/"+newRule.ID+"/draft", targetToken, rebound, nil)
	if status != http.StatusForbidden {
		t.Fatalf("out-of-scope recipient accepted: %d %s", status, body)
	}
	notice.RecipientIDs = []string{eligible}
	var saved struct {
		Revision string `json:"revision"`
	}
	status, body = call(t, http.MethodPut, target+"/rules/"+newRule.ID+"/draft", targetToken, rebound, &saved)
	if status != http.StatusOK {
		t.Fatalf("eligible recipient rebind: %d %s", status, body)
	}
	rebound.Revision = saved.Revision
	status, body = call(t, http.MethodPost, target+"/rules/"+newRule.ID+"/publish", targetToken, rebound, nil)
	if status != http.StatusOK {
		t.Fatalf("rebind publish: %d %s", status, body)
	}
	if _, enabled := newRule.Points.Find("enabled", ""); enabled {
		t.Fatal("import or recipient rebind enabled control")
	}
	eligibleToken := loginAPI(t, target, "eligible@fixture.local", targetPassword)
	var inbox notification.Page
	status, body = call(t, http.MethodGet, target+"/notifications", eligibleToken, nil, &inbox)
	if status != http.StatusOK || len(inbox.Items) != 0 {
		t.Fatalf("recipient rebind created a notification: %d %s", status, body)
	}
}
