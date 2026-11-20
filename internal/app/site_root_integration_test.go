package app

import (
	"encoding/json"
	"testing"

	"github.com/simpleiot/simpleiot/data"
)

// 站点根（实例根，ID 由部署决定）是数据根，不是设备。客户设备树必须把它
// 当容器渲染，设备清单与设备计数都不能把它算进去。这里通过真实 HTTP 快照
// 断言，而不是只改前端渲染。
func TestSnapshotSiteRootIsContainerNotDevice(t *testing.T) {
	password := data.NewID()
	t.Setenv("FASTEMS_ADMIN_PASSWORD", password)
	t.Setenv("FASTEMS_SEED_DEMO", "0")
	application := startTestApp(t, t.TempDir())
	token := loginApp(t, application, password)

	snapshot := snapshotNodes(t, application, token)
	if len(snapshot.Roots) != 1 {
		t.Fatalf("roots = %v, want exactly one instance root", snapshot.Roots)
	}
	root := snapshot.Roots[0]

	devices := []data.NodeEdge{}
	for _, node := range snapshot.Nodes {
		if node.Type == data.NodeTypeDevice {
			devices = append(devices, node)
		}
	}
	for _, device := range devices {
		if device.ID == root {
			t.Fatalf("site root %q is listed as a device", root)
		}
	}
	if len(devices) != 0 {
		t.Fatalf("device count on a fresh instance = %d (%v), want 0", len(devices), devices)
	}

	var rootNode *data.NodeEdge
	for i := range snapshot.Nodes {
		if snapshot.Nodes[i].ID == root {
			rootNode = &snapshot.Nodes[i]
		}
	}
	if rootNode == nil {
		t.Fatalf("snapshot lost the site root %q; it must stay the data root", root)
	}
	if rootNode.Type == data.NodeTypeDevice {
		t.Fatalf("site root %q is still typed %q; the tree must render it as a container", root, rootNode.Type)
	}

	// 交付视图的设备管理不受影响：设备台账仍然照常返回。
	var ledger struct {
		Devices []json.RawMessage `json:"devices"`
	}
	mustCall(t, application, "GET", "/device-ledger", token, nil, &ledger)
	if ledger.Devices == nil {
		t.Fatal("device ledger returned null devices")
	}
}
