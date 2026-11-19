package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"fastiot/internal/sim/pumphouse"
	"github.com/simpleiot/simpleiot/data"
)

// 升级的三方对照走真实 HTTP：旧基线 A → 现场改 B → 新模板 C。冲突未裁决时升级被拒绝且
// 现场不变；裁决后只写入决定的内容。升级是版本级操作，回滚到升级前版本后，升级窗口内
// 新增的报警、命令与审计历史仍在，不是整目录恢复。

type upgradePreviewResponse struct {
	Site     string `json:"site"`
	SiteName string `json:"site_name"`
	Baseline struct {
		Version string `json:"version"`
	} `json:"baseline"`
	Template struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"template"`
	Summary struct {
		Changed      int `json:"changed"`
		Conflicts    int `json:"conflicts"`
		Incompatible int `json:"incompatible"`
	} `json:"summary"`
	Blocked   bool                  `json:"blocked"`
	Blockers  []string              `json:"blockers"`
	Conflicts []upgradeConflictBody `json:"conflicts"`
	Nodes     []upgradeNodeDiffBody `json:"nodes"`
}

type upgradeConflictBody struct {
	ID       string `json:"id"`
	Node     string `json:"node"`
	Point    string `json:"point"`
	Reason   string `json:"reason"`
	Base     string `json:"base"`
	Local    string `json:"local"`
	Template string `json:"template"`
}

type upgradeNodeDiffBody struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Status string `json:"status"`
	Points []struct {
		Point    string `json:"point"`
		Status   string `json:"status"`
		Base     string `json:"base"`
		Local    string `json:"local"`
		Template string `json:"template"`
		Conflict string `json:"conflict"`
	} `json:"points"`
}

type upgradeApplyResponse struct {
	Site     string `json:"site"`
	Base     string `json:"base"`
	Template string `json:"template"`
	Updated  int    `json:"updated"`
	Created  int    `json:"created"`
	Kept     int    `json:"kept"`
	Before   string `json:"before_version"`
	Version  string `json:"version"`
}

func findUpgradeNode(t *testing.T, preview upgradePreviewResponse, name string) upgradeNodeDiffBody {
	t.Helper()
	for _, node := range preview.Nodes {
		if node.Name == name {
			return node
		}
	}
	t.Fatalf("preview has no node %q", name)
	return upgradeNodeDiffBody{}
}

// newTemplateFromExport 把导出包改成"新模板"：改一个工程点（轮询周期），并新增一个变量。
// 这正是交付人员拿到新版模板时的做法，不改动现场。
func newTemplateFromExport(t *testing.T, exported json.RawMessage, pollPeriod float64, extraName string) json.RawMessage {
	t.Helper()
	var pkg map[string]any
	if err := json.Unmarshal(exported, &pkg); err != nil {
		t.Fatal(err)
	}
	nodes, ok := pkg["nodes"].([]any)
	if !ok || len(nodes) == 0 {
		t.Fatal("exported package has no nodes")
	}
	siteRef := ""
	for _, item := range nodes {
		node := item.(map[string]any)
		if node["type"] == "site" {
			siteRef = node["ref"].(string)
		}
		if node["type"] != "modbus" {
			continue
		}
		points, _ := node["points"].([]any)
		next := make([]any, 0, len(points))
		for _, point := range points {
			entry := point.(map[string]any)
			if entry["type"] == data.PointTypePollPeriod {
				encoded, err := json.Marshal(data.NewPointFloat(data.PointTypePollPeriod, "0", pollPeriod))
				if err != nil {
					t.Fatal(err)
				}
				next = append(next, json.RawMessage(encoded))
				continue
			}
			next = append(next, point)
		}
		node["points"] = next
	}
	if siteRef == "" {
		t.Fatal("exported package has no site")
	}
	description, _ := json.Marshal(data.NewPointString("description", "0", extraName))
	nodes = append(nodes, map[string]any{
		"ref": "n99", "type": "variable", "parent": siteRef,
		"points": []any{json.RawMessage(description)},
	})
	pkg["nodes"] = nodes
	pkg["id"] = "pp-upgrade-template"
	pkg["name"] = "升级演练模板"
	encoded, err := json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestProjectUpgradeConflictBlocksThenRollbackKeepsUpgradeHistory(t *testing.T) {
	password := data.NewID()
	t.Setenv("FASTEMS_ADMIN_PASSWORD", password)
	t.Setenv("FASTEMS_SEED_DEMO", "0")

	plant := pumphouse.New(pumphouse.DefaultConfig())
	simulator, err := plant.ServeTCP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { simulator.Close() })

	application := startTestApp(t, t.TempDir())
	token := loginApp(t, application, password)
	snapshot := snapshotNodes(t, application, token)
	root := snapshot.Roots[0]

	mustCall(t, application, "POST", "/energy-units", token, map[string]any{
		"description": "升级演练工程", "rated_power_kw": 50,
	}, nil)
	snapshot = snapshotNodes(t, application, token)
	site, ok := nodeByType(snapshot.Nodes, "site")
	if !ok {
		t.Fatal("project has no site")
	}
	channelID := bindPumpHouseChannel(t, application, token, site.ID, simulator.Addr())
	ioPoints := pointIDsOf(t, application, token, channelID)
	levelID, ok := ioPoints[pumphouse.PointLevel]
	if !ok {
		t.Fatal("point table has no tank level")
	}
	pumpCommandID, ok := ioPoints[pumphouse.PointPump1Command]
	if !ok {
		t.Fatal("point table has no pump command")
	}
	var meter struct {
		ID string `json:"id"`
	}
	mustCall(t, application, "POST", "/nodes", token, map[string]any{
		"parent": site.ID, "type": "variable", "description": "计量点",
	}, &meter)

	// 基线 A：轮询周期 200。
	var baseline struct {
		ID string `json:"id"`
	}
	mustCall(t, application, "POST", "/projects/publish", token, map[string]any{
		"site": site.ID, "note": "升级前基线", "confirm": true,
	}, &baseline)
	if baseline.ID == "" {
		t.Fatal("publish returned no baseline version")
	}

	// 现场定制 B：真实 HTTP 改通道轮询周期。
	writeChannel := func(period float64) {
		mustCall(t, application, "PUT", "/modbus/"+channelID, token, map[string]any{
			"description": "泵房模拟控制器（模拟）", "protocol": "TCP",
			"uri": simulator.Addr(), "poll_period": period, "timeout": 500,
		}, nil)
	}
	writeChannel(250)
	waitFor(t, "channel poll period 250", func() bool {
		for _, node := range snapshotNodes(t, application, token).Nodes {
			if node.ID != channelID {
				continue
			}
			value, ok := pointValue(node, data.PointTypePollPeriod)
			return ok && value == 250
		}
		return false
	})

	// 新模板 C：导出当前包，改轮询周期为 300 并新增一个变量。
	var exported json.RawMessage
	mustCall(t, application, "GET", "/projects/export?site="+site.ID, token, nil, &exported)
	template := newTemplateFromExport(t, exported, 300, "模板新增")

	// 三方对照：旧基线、现场、新模板的值同时可见。
	var preview upgradePreviewResponse
	mustCall(t, application, "POST", "/projects/upgrade/preview", token, map[string]any{
		"site": site.ID, "base": baseline.ID, "package": template,
	}, &preview)
	if preview.Baseline.Version != baseline.ID || preview.Template.Name != "升级演练模板" {
		t.Fatalf("preview identity = %+v", preview)
	}
	conflictID := ""
	for _, conflict := range preview.Conflicts {
		if conflict.Point != data.PointTypePollPeriod {
			continue
		}
		conflictID = conflict.ID
		if conflict.Base != "200" || conflict.Local != "250" || conflict.Template != "300" {
			t.Fatalf("three-way poll period = %+v", conflict)
		}
	}
	if conflictID == "" || !preview.Blocked || preview.Summary.Conflicts == 0 {
		t.Fatalf("preview did not show the unresolved conflict: %+v", preview)
	}
	added := findUpgradeNode(t, preview, "模板新增")
	if added.Status != "added_by_template" {
		t.Fatalf("template node status = %+v", added)
	}
	if _, found := nodeByDescription(snapshotNodes(t, application, token).Nodes, "模板新增"); found {
		t.Fatal("preview created the template node before anyone confirmed the upgrade")
	}

	// 未裁决：升级被拒绝，现场保持原样。
	status, body := callApp(t, application, "POST", "/projects/upgrade", token, map[string]any{
		"site": site.ID, "base": baseline.ID, "package": template,
	})
	if status != 409 || !strings.Contains(string(body), "upgrade_blocked") {
		t.Fatalf("upgrade without resolutions = %d %s", status, body)
	}
	for _, node := range snapshotNodes(t, application, token).Nodes {
		if node.ID == channelID {
			if value, _ := pointValue(node, data.PointTypePollPeriod); value != 250 {
				t.Fatalf("blocked upgrade changed the live poll period: %v", value)
			}
		}
	}
	if _, found := nodeByDescription(snapshotNodes(t, application, token).Nodes, "模板新增"); found {
		t.Fatal("blocked upgrade created the template node")
	}

	// 人员裁决采用新版：只写这一项与新模板新增的对象。
	var applied upgradeApplyResponse
	mustCall(t, application, "POST", "/projects/upgrade", token, map[string]any{
		"site": site.ID, "base": baseline.ID, "package": template,
		"resolutions": map[string]string{conflictID: "template"},
	}, &applied)
	if applied.Before == "" || applied.Version == "" || applied.Before == applied.Version {
		t.Fatalf("upgrade did not freeze before/after versions: %+v", applied)
	}
	upgradedSnapshot := snapshotNodes(t, application, token)
	channelUpgraded := false
	for _, node := range upgradedSnapshot.Nodes {
		if node.ID == channelID {
			value, _ := pointValue(node, data.PointTypePollPeriod)
			channelUpgraded = value == 300
		}
	}
	if !channelUpgraded {
		t.Fatal("resolved upgrade did not apply the template value")
	}
	if _, found := nodeByDescription(upgradedSnapshot.Nodes, "模板新增"); !found {
		t.Fatal("resolved upgrade did not create the template node")
	}

	// 升级窗口内新增的报警：真实液位触发。
	mustCall(t, application, "PUT", "/alarm-types?root="+root, token, map[string]any{
		"root": root,
		"type": map[string]any{
			"code": "LEVEL_HIGH", "name": "升级窗口液位报警", "severity": "warning", "trigger": "gt",
			"point_id": levelID, "value": 0, "confirm": "manual", "record_recovery": true, "enabled": true,
		},
	}, nil)
	var occurrence string
	waitFor(t, "alarm raised during the upgrade window", func() bool {
		var listed struct {
			Alarms []struct {
				ID     string `json:"id"`
				Title  string `json:"title"`
				Active bool   `json:"active"`
			} `json:"alarms"`
		}
		mustCall(t, application, "GET", "/ems/alarms", token, nil, &listed)
		for _, record := range listed.Alarms {
			if record.Title == "升级窗口液位报警" && record.Active {
				occurrence = record.ID
				return true
			}
		}
		return false
	})

	// 升级窗口内新增的命令：写泵启停命令点，命令结果投影到该点位。
	requestID := data.NewID()
	mustCall(t, application, "POST", "/nodes/"+pumpCommandID+"/value", token, map[string]any{
		"value": 1, "point_type": "valueSet", "request_id": requestID,
	}, nil)
	lastCommandSeen := func() bool {
		for _, node := range snapshotNodes(t, application, token).Nodes {
			if node.ID != pumpCommandID {
				continue
			}
			if point, ok := node.Points.Find("lastCommand", ""); ok && strings.Contains(point.Txt(), requestID) {
				return true
			}
		}
		return false
	}
	waitFor(t, "command result projected on the point", lastCommandSeen)

	var auditBeforeRollback struct {
		Records []struct {
			Action string `json:"action"`
		} `json:"records"`
	}
	mustCall(t, application, "GET", "/audit", token, nil, &auditBeforeRollback)

	// 回滚到升级前版本：工程配置回到 250，升级窗口内的报警、命令与审计仍在。
	var rolled struct {
		Version string `json:"version"`
		Updated int    `json:"updated"`
	}
	mustCall(t, application, "POST", "/projects/rollback", token, map[string]any{
		"site": site.ID, "version": applied.Before,
	}, &rolled)
	if rolled.Version != applied.Before {
		t.Fatalf("rollback result = %+v", rolled)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		rolledBack := false
		for _, node := range snapshotNodes(t, application, token).Nodes {
			if node.ID == channelID {
				if value, _ := pointValue(node, data.PointTypePollPeriod); value == 250 {
					rolledBack = true
				}
			}
		}
		if rolledBack {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("rollback did not restore the pre-upgrade engineering configuration")
		}
		time.Sleep(50 * time.Millisecond)
	}

	var events struct {
		Events []json.RawMessage `json:"events"`
	}
	mustCall(t, application, "GET", "/alarm-events?occurrence_id="+occurrence, token, nil, &events)
	if len(events.Events) == 0 {
		t.Fatal("rollback removed the alarm raised during the upgrade window")
	}
	if !lastCommandSeen() {
		t.Fatal("rollback removed the command recorded during the upgrade window")
	}
	var auditAfterRollback struct {
		Records []struct {
			Action string `json:"action"`
		} `json:"records"`
	}
	mustCall(t, application, "GET", "/audit", token, nil, &auditAfterRollback)
	actions := map[string]bool{}
	for _, record := range auditAfterRollback.Records {
		actions[record.Action] = true
	}
	if !actions["项目升级"] || !actions["项目回退"] {
		t.Fatalf("audit actions after rollback = %v", actions)
	}
	if len(auditAfterRollback.Records) < len(auditBeforeRollback.Records) {
		t.Fatal("rollback dropped audit history")
	}
}
