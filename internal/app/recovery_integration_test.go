package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fastiot/internal/backup"
	"fastiot/internal/sim/pumphouse"
	"github.com/simpleiot/simpleiot/data"
)

// 这组测试证明两件事：干净实例（空数据目录、只有本机回环通信）能导入项目包并
// 真正跑起来；离线灾难恢复把实例恢复到备份快照那一刻，并明确说出快照之后的
// 数据不在其中，且与版本回退不是同一个操作。

type appSnapshot struct {
	Roots []string        `json:"roots"`
	Nodes []data.NodeEdge `json:"nodes"`
}

func startTestApp(t *testing.T, dir string) *App {
	t.Helper()
	port, err := FreePort()
	if err != nil {
		t.Fatal(err)
	}
	application, err := Start(context.Background(), Config{
		DataDir: dir, NATSPort: port, DisableHTTP: true, Version: "recovery-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	return application
}

func loginApp(t *testing.T, application *App, password string) string {
	t.Helper()
	encoded, _ := json.Marshal(map[string]string{"email": "admin", "password": password})
	deadline := time.Now().Add(10 * time.Second)
	for {
		request := httptest.NewRequest("POST", "/api/login", bytes.NewReader(encoded))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		application.ServeHTTP(response, request)
		if response.Code == 503 && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		if response.Code != 200 {
			t.Fatalf("login: %d %s", response.Code, response.Body.String())
		}
		var session struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
			t.Fatal(err)
		}
		return session.Token
	}
}

func callApp(t *testing.T, application *App, method, path, token string, body any) (int, []byte) {
	t.Helper()
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, "/api"+path, bytes.NewReader(encoded))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	application.ServeHTTP(response, request)
	return response.Code, response.Body.Bytes()
}

func mustCall(t *testing.T, application *App, method, path, token string, body any, out any) {
	t.Helper()
	status, payload := callApp(t, application, method, path, token, body)
	if status != 200 {
		t.Fatalf("%s %s: %d %s", method, path, status, payload)
	}
	if out != nil {
		if err := json.Unmarshal(payload, out); err != nil {
			t.Fatal(err)
		}
	}
}

func postRaw(t *testing.T, application *App, path, token, contentType string, body []byte) {
	t.Helper()
	request := httptest.NewRequest("POST", "/api"+path, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", contentType)
	response := httptest.NewRecorder()
	application.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("POST %s: %d %s", path, response.Code, response.Body.String())
	}
}

func snapshotNodes(t *testing.T, application *App, token string) appSnapshot {
	t.Helper()
	var snapshot appSnapshot
	mustCall(t, application, "GET", "/snapshot", token, nil, &snapshot)
	return snapshot
}

func nodeByDescription(nodes []data.NodeEdge, description string) (data.NodeEdge, bool) {
	for _, node := range nodes {
		if node.Desc() == description {
			return node, true
		}
	}
	return data.NodeEdge{}, false
}

func nodeByType(nodes []data.NodeEdge, nodeType string) (data.NodeEdge, bool) {
	for _, node := range nodes {
		if node.Type == nodeType {
			return node, true
		}
	}
	return data.NodeEdge{}, false
}

func pointValue(node data.NodeEdge, pointType string) (float64, bool) {
	point, ok := node.Points.Find(pointType, "")
	if !ok {
		return 0, false
	}
	value, err := point.ValueFloat()
	if err != nil {
		return 0, false
	}
	return value, true
}

func waitFor(t *testing.T, label string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", label)
}

// bindPumpHouseChannel creates a real Modbus TCP channel bound to the sample
// simulator and imports its point table. The address is a loopback endpoint;
// no network beyond this machine is involved.
func bindPumpHouseChannel(t *testing.T, application *App, token, parent, address string) string {
	t.Helper()
	var channel struct {
		ID string `json:"id"`
	}
	mustCall(t, application, "POST", "/modbus", token, map[string]any{
		"parent": parent, "description": "泵房模拟控制器（模拟）", "protocol": "TCP",
		"uri": address, "poll_period": 200, "timeout": 500,
	}, &channel)
	postRaw(t, application, "/modbus/"+channel.ID+"/io/import", token, "text/csv; charset=utf-8", pumphouse.PointTableCSV(1))
	return channel.ID
}

func pointIDsOf(t *testing.T, application *App, token, channelID string) map[string]string {
	t.Helper()
	var model struct {
		Points []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"points"`
	}
	mustCall(t, application, "GET", "/modbus/"+channelID+"/units/1/model", token, nil, &model)
	ids := map[string]string{}
	for _, point := range model.Points {
		ids[point.Name] = point.ID
	}
	return ids
}

// TestCleanInstanceImportsProjectPackageAndRuns 从空数据目录开始：导出项目包，
// 在另一个空实例导入，重新绑定回环地址后轮询到真实寄存器值。
func TestCleanInstanceImportsProjectPackageAndRuns(t *testing.T) {
	password := data.NewID()
	t.Setenv("FASTEMS_ADMIN_PASSWORD", password)
	t.Setenv("FASTEMS_SEED_DEMO", "0")

	plant := pumphouse.New(pumphouse.DefaultConfig())
	simulator, err := plant.ServeTCP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { simulator.Close() })

	source := startTestApp(t, t.TempDir())
	sourceToken := loginApp(t, source, password)
	snapshot := snapshotNodes(t, source, sourceToken)
	if len(snapshot.Roots) != 1 {
		t.Fatalf("source roots = %v", snapshot.Roots)
	}

	// 建工程：能源单元自动带出 Site → Plant，再挂一条被模拟器的总线。
	mustCall(t, source, "POST", "/energy-units", sourceToken, map[string]any{
		"description": "泵房交付工程", "rated_power_kw": 100,
	}, nil)
	snapshot = snapshotNodes(t, source, sourceToken)
	site, ok := nodeByType(snapshot.Nodes, "site")
	if !ok {
		t.Fatal("source has no site")
	}
	channelID := bindPumpHouseChannel(t, source, sourceToken, site.ID, "127.0.0.1:1")
	if _, ok := pointIDsOf(t, source, sourceToken, channelID)[pumphouse.PointLevel]; !ok {
		t.Fatal("source point table has no tank level")
	}

	var exported json.RawMessage
	mustCall(t, source, "GET", "/projects/export?site="+site.ID, sourceToken, nil, &exported)
	if strings.Contains(string(exported), "127.0.0.1:1") {
		t.Fatal("exported package carries the source site's connection address")
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}

	// 干净实例：新的空数据目录，导入后重新绑定本机回环地址。
	clean := startTestApp(t, t.TempDir())
	cleanToken := loginApp(t, clean, password)
	var imported struct {
		SiteID string   `json:"site_id"`
		Name   string   `json:"name"`
		Errors []string `json:"-"`
	}
	mustCall(t, clean, "POST", "/projects/import", cleanToken, map[string]any{"package": exported}, &imported)
	if imported.SiteID == "" {
		t.Fatal("import returned no site")
	}

	cleanSnapshot := snapshotNodes(t, clean, cleanToken)
	cleanSite, ok := nodeByType(cleanSnapshot.Nodes, "site")
	if !ok {
		t.Fatal("clean instance has no imported site")
	}
	cleanChannel, ok := nodeByDescription(cleanSnapshot.Nodes, "泵房模拟控制器（模拟）")
	if !ok {
		t.Fatal("imported channel is missing")
	}
	address, ok := cleanChannel.Points.Text(data.PointTypeURI, "")
	if ok && address != "" {
		t.Fatalf("imported channel kept a connection address: %q", address)
	}
	mustCall(t, clean, "PUT", "/modbus/"+cleanChannel.ID, cleanToken, map[string]any{
		"description": "泵房模拟控制器（模拟）", "protocol": "TCP",
		"uri": simulator.Addr(), "poll_period": 200, "timeout": 500,
	}, nil)

	waitFor(t, "polled tank level on the clean instance", func() bool {
		current := snapshotNodes(t, clean, cleanToken)
		level, ok := nodeByDescription(current.Nodes, pumphouse.PointLevel)
		if !ok {
			return false
		}
		value, ok := pointValue(level, data.PointTypeValue)
		return ok && value > 0
	})

	// 同一次导入已经冻结了可复用、可回退的工程版本。
	var sources struct {
		Sources []struct {
			ID       string `json:"id"`
			Versions int    `json:"versions"`
		} `json:"sources"`
	}
	mustCall(t, clean, "GET", "/projects/sources", cleanToken, nil, &sources)
	found := false
	for _, item := range sources.Sources {
		if item.ID == cleanSite.ID && item.Versions > 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("imported project is not reusable: %#v", sources.Sources)
	}

	// 干净实例还没有任何备份或恢复记录，界面据此显示真实状态而不是虚构。
	var status backup.Status
	mustCall(t, clean, "GET", "/recovery", cleanToken, nil, &status)
	if status.Restored || status.Backup != nil {
		t.Fatalf("clean instance reports backup history: %#v", status)
	}
	if !status.OfflineOnly || !strings.Contains(status.VersionRollback, "版本回退") {
		t.Fatalf("recovery status must state offline-only and rollback distinction: %#v", status)
	}
}

// TestOfflineRestoreKeepsHistoryAlarmsAndAudit 备份前产生的历史、报警与审计在
// 恢复后仍可查；快照之后新增的数据不在恢复结果中，报告写明时间点与损失窗口。
func TestOfflineRestoreKeepsHistoryAlarmsAndAudit(t *testing.T) {
	password := data.NewID()
	t.Setenv("FASTEMS_ADMIN_PASSWORD", password)
	t.Setenv("FASTEMS_SEED_DEMO", "0")

	plant := pumphouse.New(pumphouse.DefaultConfig())
	simulator, err := plant.ServeTCP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { simulator.Close() })

	dataDir := t.TempDir()
	application := startTestApp(t, dataDir)
	token := loginApp(t, application, password)
	snapshot := snapshotNodes(t, application, token)
	root := snapshot.Roots[0]

	mustCall(t, application, "POST", "/energy-units", token, map[string]any{
		"description": "恢复演练工程", "rated_power_kw": 50,
	}, nil)
	snapshot = snapshotNodes(t, application, token)
	site, _ := nodeByType(snapshot.Nodes, "site")
	channelID := bindPumpHouseChannel(t, application, token, site.ID, simulator.Addr())
	points := pointIDsOf(t, application, token, channelID)
	levelID := points[pumphouse.PointLevel]
	mustCall(t, application, "PUT", "/alarm-types?root="+root, token, map[string]any{
		"root": root,
		"type": map[string]any{
			"code": "LEVEL_HIGH", "name": "液位报警", "severity": "warning", "trigger": "gt",
			"point_id": levelID, "value": 0, "confirm": "manual", "record_recovery": true, "enabled": true,
		},
	}, nil)

	var activeOccurrence string
	waitFor(t, "alarm on the live level", func() bool {
		var listed struct {
			Alarms []struct {
				ID     string `json:"id"`
				Title  string `json:"title"`
				Active bool   `json:"active"`
			} `json:"alarms"`
		}
		mustCall(t, application, "GET", "/ems/alarms", token, nil, &listed)
		for _, record := range listed.Alarms {
			if record.Title == "液位报警" && record.Active {
				activeOccurrence = record.ID
				return true
			}
		}
		return false
	})

	// 历史：变量点写入两次真实值（落在 JetStream 存储里，不是内存运行时）。
	var variable struct {
		ID string `json:"id"`
	}
	mustCall(t, application, "POST", "/nodes", token, map[string]any{
		"parent": root, "type": "variable", "description": "恢复验收变量", "value": 12.5,
	}, &variable)
	mustCall(t, application, "POST", "/nodes/"+variable.ID+"/value", token, map[string]any{
		"value": 13.5, "point_type": "value",
	}, nil)
	var beforeBackup struct {
		TotalMatched int `json:"total_matched"`
	}
	mustCall(t, application, "GET", "/history?node_id="+variable.ID+"&type=value", token, nil, &beforeBackup)
	if beforeBackup.TotalMatched == 0 {
		t.Fatal("history is empty before the backup; the fixture never wrote a sample")
	}

	var baseline struct {
		ID string `json:"id"`
	}
	mustCall(t, application, "POST", "/projects/publish", token, map[string]any{
		"site": site.ID, "note": "恢复前基线", "confirm": true,
	}, &baseline)
	if baseline.ID == "" {
		t.Fatal("publish returned no version")
	}

	if err := application.Close(); err != nil {
		t.Fatal(err)
	}
	archive := t.TempDir() + "/snapshot.tar.gz"
	manifest, err := backup.Create(dataDir, archive, "recovery-test")
	if err != nil {
		t.Fatalf("backup: %v", err)
	}

	// 快照之后继续运行，产生一段明确落在数据损失窗口内的数据。
	application = startTestApp(t, dataDir)
	token = loginApp(t, application, password)
	mustCall(t, application, "POST", "/nodes", token, map[string]any{
		"parent": root, "type": "variable", "description": "恢复窗口内新增",
	}, nil)
	if err := application.Close(); err != nil {
		t.Fatal(err)
	}

	restoredDir := t.TempDir() + "/data"
	recovery, err := backup.Restore(archive, restoredDir)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !recovery.SnapshotAt.Equal(manifest.CreatedAt) {
		t.Fatalf("recovery snapshot = %v, manifest = %v", recovery.SnapshotAt, manifest.CreatedAt)
	}

	restored := startTestApp(t, restoredDir)
	restoredToken := loginApp(t, restored, password)
	restoredSnapshot := snapshotNodes(t, restored, restoredToken)
	if _, ok := nodeByDescription(restoredSnapshot.Nodes, "恢复验收变量"); !ok {
		t.Fatal("pre-snapshot variable is missing after restore")
	}
	if _, ok := nodeByDescription(restoredSnapshot.Nodes, "恢复窗口内新增"); ok {
		t.Fatal("post-snapshot data appeared in the restored instance")
	}

	// 历史可查：变量点的两条值记录仍在。
	var history struct {
		TotalMatched int `json:"total_matched"`
	}
	mustCall(t, restored, "GET", "/history?node_id="+variable.ID+"&type=value", restoredToken, nil, &history)
	if history.TotalMatched == 0 {
		t.Fatal("history is empty after restore")
	}

	// 报警记录可查：恢复前的告警过程仍在。
	var events struct {
		Events []json.RawMessage `json:"events"`
	}
	mustCall(t, restored, "GET", "/alarm-events?occurrence_id="+activeOccurrence, restoredToken, nil, &events)
	if len(events.Events) == 0 {
		t.Fatal("alarm events are empty after restore")
	}
	var stats struct {
		Today struct {
			Raised int `json:"raised"`
		} `json:"today"`
	}
	mustCall(t, restored, "GET", "/alarm-stats", restoredToken, nil, &stats)

	// 审计可查：恢复前的动作与恢复本身都在。
	var auditList struct {
		Records []struct {
			Action string `json:"action"`
		} `json:"records"`
	}
	mustCall(t, restored, "GET", "/audit", restoredToken, nil, &auditList)
	actions := map[string]bool{}
	for _, record := range auditList.Records {
		actions[record.Action] = true
	}
	if !actions["项目发布"] || !actions["灾难恢复"] {
		t.Fatalf("audit actions after restore = %v", actions)
	}

	// 恢复状态写明快照时间、恢复时间与数据损失窗口。
	var status backup.Status
	mustCall(t, restored, "GET", "/recovery", restoredToken, nil, &status)
	if !status.Restored || status.Recovery == nil {
		t.Fatalf("restored instance does not report the restore: %#v", status)
	}
	if !status.Recovery.SnapshotAt.Equal(manifest.CreatedAt) || status.Recovery.DataLoss.Detail == "" {
		t.Fatalf("recovery record = %#v", status.Recovery)
	}
	if !strings.Contains(status.Note, "快照之后") {
		t.Fatalf("note does not state the data loss window: %q", status.Note)
	}

	// 版本回退是另一个操作：只重放工程配置，不改变恢复记录，也不删历史。
	statusBefore := *status.Recovery
	var rolledBack struct {
		Version string `json:"version"`
		Updated int    `json:"updated"`
		Created int    `json:"created"`
		Extra   int    `json:"extra"`
	}
	mustCall(t, restored, "POST", "/projects/rollback", restoredToken, map[string]any{
		"site": site.ID, "version": baseline.ID,
	}, &rolledBack)
	if rolledBack.Version != baseline.ID {
		t.Fatalf("rollback result = %#v", rolledBack)
	}
	var afterRollback backup.Status
	mustCall(t, restored, "GET", "/recovery", restoredToken, nil, &afterRollback)
	if afterRollback.Recovery == nil || !afterRollback.Recovery.SnapshotAt.Equal(statusBefore.SnapshotAt) ||
		!afterRollback.Recovery.RestoredAt.Equal(statusBefore.RestoredAt) {
		t.Fatal("version rollback changed the restore record; the two operations are conflated")
	}
	var stillThere struct {
		TotalMatched int `json:"total_matched"`
	}
	mustCall(t, restored, "GET", "/history?node_id="+variable.ID+"&type=value", restoredToken, nil, &stillThere)
	if stillThere.TotalMatched == 0 {
		t.Fatal("version rollback removed history; it must only replay engineering configuration")
	}
	var auditAfter struct {
		Records []struct {
			Action string `json:"action"`
		} `json:"records"`
	}
	mustCall(t, restored, "GET", "/audit", restoredToken, nil, &auditAfter)
	seenRollback := false
	for _, record := range auditAfter.Records {
		if record.Action == "项目回退" {
			seenRollback = true
		}
	}
	if !seenRollback {
		t.Fatal("version rollback was not recorded separately in the audit log")
	}
}
