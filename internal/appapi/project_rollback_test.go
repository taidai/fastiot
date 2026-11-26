package appapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fastiot/internal/decision"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

func TestProjectRollbackRestoresReferencedRootResources(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, token)
	site := createNodeAPI(t, base, token, NodeTypeSite, root, "Rollback package", 0)
	device := createNodeAPI(t, base, token, "device", site, "Bound device", 0)
	bus, err := CreateDomainNode(h.nc, root, data.NodeTypeModbus, map[string]string{"description": "original bus"})
	if err != nil {
		t.Fatal(err)
	}
	point, err := CreateDomainNode(h.nc, bus, data.NodeTypeModbusIO, map[string]string{"description": "bound point"})
	if err != nil {
		t.Fatal(err)
	}
	model, _ := json.Marshal(unitModelConfig{BusinessNodeID: device, PointMap: map[string]string{"power": point}})
	if err := client.SendNodePoint(h.nc, bus, data.NewPointString(unitModelPoint, "1", string(model)), true); err != nil {
		t.Fatal(err)
	}
	identity := Identity{UserID: "admin", Policy: AccessPolicy{Admin: true}, Anchors: []string{root}}
	nodes, _, _, err := h.collectProjectPackageNodes(site)
	if err != nil {
		t.Fatal(err)
	}
	version, err := h.snapshotNodes(identity, site, "complete package baseline", nodes)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.saveVersion(version); err != nil {
		t.Fatal(err)
	}
	if err := client.SendNodePoint(h.nc, bus, data.NewPointString("description", "0", "changed bus"), true); err != nil {
		t.Fatal(err)
	}

	status, body := call(t, http.MethodPost, base+"/projects/rollback", token, rollbackRequest{Site: site, Version: version.ID}, nil)
	if status != http.StatusOK {
		t.Fatalf("rollback: %d %s", status, body)
	}
	waitFor(t, func() bool {
		nodes, err := client.GetNodes(h.nc, "all", bus, "", false)
		return err == nil && len(nodes) == 1 && nodes[0].Desc() == "original bus"
	}, "referenced root channel restored")
}

func TestProjectRollbackRequiresSavedBaseline(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, token)
	site := createNodeAPI(t, base, token, NodeTypeSite, root, "Baseline target", 0)
	createNodeAPI(t, base, token, "variable", site, "Configuration", 0)
	identity := Identity{UserID: "admin", Policy: AccessPolicy{Admin: true}, Anchors: []string{root}}
	version := freezeRecoveryBaseline(t, h, identity, site, "original")
	if err := client.SendNodePoint(h.nc, site, data.NewPointString("description", "0", "current must survive"), true); err != nil {
		t.Fatal(err)
	}

	// 回退前必须冻结并持久化一份可恢复的当前基线；响应回读该基线，
	// 且其中必须仍是回退前的“current must survive”状态，而不是目标版本。
	var result rollbackResult
	status, body := call(t, http.MethodPost, base+"/projects/rollback", token, rollbackRequest{Site: site, Version: version.ID}, &result)
	if status != http.StatusOK {
		t.Fatalf("rollback: %d %s", status, body)
	}
	if result.Baseline == "" || result.Baseline == version.ID {
		t.Fatalf("rollback must persist a distinct pre-write baseline: %s", body)
	}
	baseline, err := h.loadVersion(site, result.Baseline)
	if err != nil {
		t.Fatalf("saved baseline is not re-loadable: %v", err)
	}
	foundCurrent := false
	for _, node := range baseline.Nodes {
		if text, _ := node.Points.Text("description", ""); node.ID == site && text == "current must survive" {
			foundCurrent = true
		}
	}
	if !foundCurrent {
		t.Fatalf("pre-write baseline does not capture the current state: %s", body)
	}
}

func TestProjectRollbackUnsupportedResourceIsTraceableAndRetryable(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, token)
	site := createNodeAPI(t, base, token, NodeTypeSite, root, "Supported baseline", 0)
	createNodeAPI(t, base, token, "variable", site, "Configuration", 0)
	identity := Identity{UserID: "admin", Policy: AccessPolicy{Admin: true}, Anchors: []string{root}}
	good := freezeRecoveryBaseline(t, h, identity, site, "supported")
	bad, err := h.loadVersion(site, good.ID)
	if err != nil {
		t.Fatal(err)
	}
	bad.ID = "pv-" + data.NewID()
	unsupportedID := data.NewID()
	bad.Nodes = append(bad.Nodes, versionNode{ID: unsupportedID, Type: calculationType, Parent: site})
	if err := h.saveVersion(bad); err != nil {
		t.Fatal(err)
	}
	if err := client.SendNodePoint(h.nc, site, data.NewPointString("description", "0", "current before retry"), true); err != nil {
		t.Fatal(err)
	}

	status, body := call(t, http.MethodPost, base+"/projects/rollback", token, rollbackRequest{Site: site, Version: bad.ID}, nil)
	if status != http.StatusConflict {
		t.Fatalf("unsupported rollback = %d %s", status, body)
	}
	var failure struct {
		Error     string `json:"error"`
		Retryable bool   `json:"retryable"`
		Blockers  []struct {
			Code     string `json:"code"`
			Scope    string `json:"scope"`
			NodeID   string `json:"node_id"`
			NodeType string `json:"node_type"`
		} `json:"blockers"`
	}
	if err := json.Unmarshal([]byte(body), &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Error != "calculation_rollback_unsupported" || !failure.Retryable || len(failure.Blockers) != 1 {
		t.Fatalf("unsupported response is not actionable: %s", body)
	}
	blocker := failure.Blockers[0]
	if blocker.Code != failure.Error || blocker.Scope != "version" || blocker.NodeID != unsupportedID || blocker.NodeType != calculationType {
		t.Fatalf("unsupported blocker lost resource identity: %s", body)
	}
	nodes, err := client.GetNodes(h.nc, "all", site, "", false)
	if err != nil || len(nodes) != 1 || nodes[0].Desc() != "current before retry" {
		t.Fatalf("blocked rollback wrote configuration: %+v, %v", nodes, err)
	}

	status, body = call(t, http.MethodPost, base+"/projects/rollback", token, rollbackRequest{Site: site, Version: good.ID}, nil)
	if status != http.StatusOK {
		t.Fatalf("supported retry = %d %s", status, body)
	}
	var result struct {
		Version  string `json:"version"`
		Baseline string `json:"baseline"`
	}
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatal(err)
	}
	if result.Version != good.ID || result.Baseline == "" || result.Baseline == good.ID {
		t.Fatalf("retry did not return its new baseline: %s", body)
	}
	baseline, err := h.loadVersion(site, result.Baseline)
	if err != nil || baseline.Deliverable {
		t.Fatalf("retry baseline is not a readable internal snapshot: %+v, %v", baseline, err)
	}
	waitFor(t, func() bool {
		nodes, err := client.GetNodes(h.nc, "all", site, "", false)
		return err == nil && len(nodes) == 1 && nodes[0].Desc() == "Supported baseline"
	}, "supported retry restores configuration")
}

func TestProjectRollbackValidatesOriginalAndBaseline(t *testing.T) {
	tests := []struct {
		name           string
		want           string
		prepare        func(*projectVersion)
		stripIntegrity bool
		invalidCurrent bool
	}{
		{name: "missing integrity", want: "version_integrity_missing", stripIntegrity: true},
		{name: "deliverable package missing", want: "version_package_missing", prepare: func(version *projectVersion) {
			version.Deliverable = true
			version.Fingerprint = engineeringFingerprint(version.Nodes, nil)
			version.Package = nil
		}},
		{name: "unresolved target reference", want: "package_reference_unresolved", prepare: func(version *projectVersion) {
			version.Nodes[0].Points = append(version.Nodes[0].Points, data.NewPointString(dailyStatsPoint, "0", `{"version":1,"time_zone":"UTC","freshness":{"point_id":"missing-target","max_age_seconds":60}}`))
		}},
		{name: "unrestorable current baseline", want: "rollback_baseline_unrestorable", invalidCurrent: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
			root := snapshotRoot(t, base, token)
			site := createNodeAPI(t, base, token, NodeTypeSite, root, "Validation target", 0)
			createNodeAPI(t, base, token, "variable", site, "Configuration", 0)
			identity := Identity{UserID: "admin", Policy: AccessPolicy{Admin: true}, Anchors: []string{root}}
			good := freezeRecoveryBaseline(t, h, identity, site, "valid baseline")
			candidate, err := h.loadVersion(site, good.ID)
			if err != nil {
				t.Fatal(err)
			}
			candidate.ID = "pv-" + data.NewID()
			if tc.prepare != nil {
				tc.prepare(&candidate)
			}
			if err := h.saveVersion(candidate); err != nil {
				t.Fatal(err)
			}
			if tc.stripIntegrity {
				path := filepath.Join(h.projectDir(site), candidate.ID+".json")
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var stored map[string]any
				if err := json.Unmarshal(raw, &stored); err != nil {
					t.Fatal(err)
				}
				delete(stored, "integrity")
				raw, _ = json.Marshal(stored)
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := client.SendNodePoint(h.nc, site, data.NewPointString("description", "0", "current before validation"), true); err != nil {
				t.Fatal(err)
			}
			if tc.invalidCurrent {
				if err := client.SendNodePoint(h.nc, site, data.NewPointString(dailyStatsPoint, "0", `{"version":1,"time_zone":"UTC","freshness":{"point_id":"missing-current","max_age_seconds":60}}`), true); err != nil {
					t.Fatal(err)
				}
			}
			status, body := call(t, http.MethodPost, base+"/projects/rollback", token, rollbackRequest{Site: site, Version: candidate.ID}, nil)
			if status != http.StatusConflict || !strings.Contains(body, tc.want) {
				t.Fatalf("validation = %d %s, want %s", status, body, tc.want)
			}
			nodes, err := client.GetNodes(h.nc, "all", site, "", false)
			if err != nil || len(nodes) != 1 || nodes[0].Desc() != "current before validation" {
				t.Fatalf("validation failure wrote configuration: %+v, %v", nodes, err)
			}
		})
	}
}

func TestProjectRollbackPreservesRuntimeFactsAndControlState(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, token)
	site := createNodeAPI(t, base, token, NodeTypeSite, root, "Runtime preservation", 0)
	rule, err := CreateDomainNode(h.nc, site, "decisionTable", map[string]string{"description": "Control rule"})
	if err != nil {
		t.Fatal(err)
	}
	machine, err := CreateDomainNode(h.nc, site, stateMachineType, map[string]string{"description": "Machine process"})
	if err != nil {
		t.Fatal(err)
	}
	document, err := decision.NewStateMachineDocument(decision.StateMachineDefinition{InitialState: "idle", States: []string{"idle", "running"}})
	if err != nil {
		t.Fatal(err)
	}
	document.LastExecution = &decision.StateMachineExecution{RequestID: "old-execution", Version: 1}
	writeDocument := func(doc decision.StateMachineDocument) {
		encoded, _ := json.Marshal(doc)
		if err := client.SendNodePoint(h.nc, machine, data.NewPointString(stateMachineDocumentPoint, "0", string(encoded)), true); err != nil {
			t.Fatal(err)
		}
	}
	writeDocument(document)
	for _, point := range []data.Point{
		data.NewPointFloat("enabled", "", 1),
		data.NewPointString("valueSet", "", "old-command"),
		data.NewPointString(decision.RuleExecutionFactPoint, "", "old-execution-fact"),
	} {
		if err := client.SendNodePoint(h.nc, rule, point, true); err != nil {
			t.Fatal(err)
		}
	}
	identity := Identity{UserID: "admin", Policy: AccessPolicy{Admin: true}, Anchors: []string{root}}
	version := freezeRecoveryBaseline(t, h, identity, site, "configuration only")
	stored, err := h.loadVersion(site, version.ID)
	if err != nil {
		t.Fatal(err)
	}
	legacyControl := false
	for i, node := range stored.Nodes {
		if node.ID != rule {
			continue
		}
		legacyControl = true
		if _, found := node.Points.Value("enabled", ""); !found {
			stored.Nodes[i].Points = append(stored.Nodes[i].Points, data.NewPointFloat("enabled", "", 1))
		}
	}
	if !legacyControl {
		t.Fatal("fixture did not seal the decision table")
	}
	stored.ID = "pv-" + data.NewID()
	if err := h.saveVersion(stored); err != nil {
		t.Fatal(err)
	}
	version.ID = stored.ID
	for _, point := range []data.Point{
		data.NewPointFloat("enabled", "", 0),
		data.NewPointString("valueSet", "", "current-command"),
		data.NewPointString(decision.RuleExecutionFactPoint, "", "current-execution-fact"),
	} {
		if err := client.SendNodePoint(h.nc, rule, point, true); err != nil {
			t.Fatal(err)
		}
	}
	document.CurrentState = "running"
	document.Revision = 2
	document.LastExecution = &decision.StateMachineExecution{RequestID: "current-execution", Version: 2}
	writeDocument(document)
	enabledWrites, err := h.nc.SubscribeSync(client.SubjectNodePoints(rule) + ".enabled.>")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = enabledWrites.Unsubscribe() })
	if err := h.nc.Flush(); err != nil {
		t.Fatal(err)
	}

	status, body := call(t, http.MethodPost, base+"/projects/rollback", token, rollbackRequest{Site: site, Version: version.ID}, nil)
	if status != http.StatusOK {
		t.Fatalf("rollback: %d %s", status, body)
	}
	if message, err := enabledWrites.NextMsg(2 * time.Second); err == nil {
		t.Fatalf("rollback republished decision-table control state: %+v", message)
	}
	waitFor(t, func() bool {
		nodes, err := client.GetNodes(h.nc, "all", machine, "", false)
		if err != nil || len(nodes) != 1 {
			return false
		}
		current, err := decodeStateMachine(nodes[0])
		return err == nil && current.CurrentState == "running" && current.LastExecution != nil && current.LastExecution.RequestID == "current-execution"
	}, "machine process facts preserved")
	nodes, err := client.GetNodes(h.nc, "all", rule, "", false)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("rule readback: %+v, %v", nodes, err)
	}
	enabled, _ := nodes[0].Points.Value("enabled", "")
	command, _ := nodes[0].Points.Text("valueSet", "")
	execution, _ := nodes[0].Points.Text(decision.RuleExecutionFactPoint, "")
	if enabled != 0 || command != "current-command" || execution != "current-execution-fact" {
		t.Fatalf("rollback restored control or runtime facts: enabled=%v command=%q execution=%q", enabled, command, execution)
	}
}

func TestProjectRollbackFailureRestoresBaselineBeforeRetry(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, token)
	site := createNodeAPI(t, base, token, NodeTypeSite, root, "Original site", 0)
	group := createNodeAPI(t, base, token, "group", site, "Original group", 0)
	identity := Identity{UserID: "admin", Policy: AccessPolicy{Admin: true}, Anchors: []string{root}}
	good := freezeRecoveryBaseline(t, h, identity, site, "small original")
	candidate, err := h.loadVersion(site, good.ID)
	if err != nil {
		t.Fatal(err)
	}
	candidate.ID = "pv-" + data.NewID()
	for i, node := range candidate.Nodes {
		if node.ID == group {
			candidate.Nodes[i].Points = append(candidate.Nodes[i].Points, data.NewPointString("oversized", "0", strings.Repeat("x", 2<<20)))
		}
	}
	if err := h.saveVersion(candidate); err != nil {
		t.Fatal(err)
	}
	for id, description := range map[string]string{site: "Current site", group: "Current group"} {
		if err := client.SendNodePoint(h.nc, id, data.NewPointString("description", "0", description), true); err != nil {
			t.Fatal(err)
		}
	}

	status, body := call(t, http.MethodPost, base+"/projects/rollback", token, rollbackRequest{Site: site, Version: candidate.ID}, nil)
	if status != http.StatusBadGateway {
		t.Fatalf("failed restore = %d %s", status, body)
	}
	var failure struct {
		Error     string `json:"error"`
		Baseline  string `json:"baseline"`
		Recovered bool   `json:"recovered"`
		Retryable bool   `json:"retryable"`
	}
	if err := json.Unmarshal([]byte(body), &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Error != "rollback_failed_recovered" || failure.Baseline == "" || !failure.Recovered || !failure.Retryable {
		t.Fatalf("failure is not explainable or safe to retry: %s", body)
	}
	if _, err := h.loadVersion(site, failure.Baseline); err != nil {
		t.Fatalf("failure baseline is not readable: %v", err)
	}
	waitFor(t, func() bool {
		siteNodes, siteErr := client.GetNodes(h.nc, "all", site, "", false)
		groupNodes, groupErr := client.GetNodes(h.nc, "all", group, "", false)
		return siteErr == nil && groupErr == nil && len(siteNodes) == 1 && len(groupNodes) == 1 &&
			siteNodes[0].Desc() == "Current site" && groupNodes[0].Desc() == "Current group"
	}, "failed target restored to its pre-write baseline")

	fixed := candidate
	fixed.ID = "pv-" + data.NewID()
	for i, node := range fixed.Nodes {
		for j, point := range node.Points {
			if point.Type == "oversized" {
				fixed.Nodes[i].Points[j] = data.NewPointString(point.Type, point.Key, "small")
			}
		}
	}
	if err := h.saveVersion(fixed); err != nil {
		t.Fatal(err)
	}
	status, body = call(t, http.MethodPost, base+"/projects/rollback", token, rollbackRequest{Site: site, Version: fixed.ID}, nil)
	if status != http.StatusOK {
		t.Fatalf("retry after recovered failure = %d %s", status, body)
	}
	waitFor(t, func() bool {
		nodes, err := client.GetNodes(h.nc, "all", site, "", false)
		return err == nil && len(nodes) == 1 && nodes[0].Desc() == "Original site"
	}, "corrected target succeeds on retry")
}
