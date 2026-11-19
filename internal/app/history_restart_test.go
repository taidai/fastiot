package app

import (
	"testing"

	"github.com/simpleiot/simpleiot/data"
)

// 恢复意味着重启：历史存储必须在不发生新写入的情况下仍可查询。
func TestPointHistorySurvivesRestart(t *testing.T) {
	password := data.NewID()
	t.Setenv("FASTEMS_ADMIN_PASSWORD", password)
	t.Setenv("FASTEMS_SEED_DEMO", "0")
	dir := t.TempDir()
	first := startTestApp(t, dir)
	token := loginApp(t, first, password)
	root := snapshotNodes(t, first, token).Roots[0]
	var variable struct {
		ID string `json:"id"`
	}
	mustCall(t, first, "POST", "/nodes", token, map[string]any{
		"parent": root, "type": "variable", "description": "probe", "value": 12.5,
	}, &variable)
	mustCall(t, first, "POST", "/nodes/"+variable.ID+"/value", token, map[string]any{
		"value": 13.5, "point_type": "value",
	}, nil)
	var before struct {
		TotalMatched int `json:"total_matched"`
	}
	mustCall(t, first, "GET", "/history?node_id="+variable.ID+"&type=value", token, nil, &before)
	t.Logf("before restart: %d", before.TotalMatched)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second := startTestApp(t, dir)
	secondToken := loginApp(t, second, password)
	var after struct {
		TotalMatched int `json:"total_matched"`
	}
	mustCall(t, second, "GET", "/history?node_id="+variable.ID+"&type=value", secondToken, nil, &after)
	t.Logf("after restart: %d", after.TotalMatched)
	if after.TotalMatched == 0 {
		t.Fatal("history did not survive a plain restart")
	}
}
