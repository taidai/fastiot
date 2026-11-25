package appapi

import (
	"net/http"
	"testing"
)

func TestScreenConfiguratorDerivesLegacyPointIDsForOverview(t *testing.T) {
	f := newPointConfigFixture(t)
	root := snapshotRoot(t, f.base, f.token)
	area := createNodeAPI(t, f.base, f.token, "group", root, "客户总览区域", 0)
	point := createNodeAPI(t, f.base, f.token, "variable", area, "总功率", 0)
	draft := automaticScreen(area, 0)
	draft["blocks"].([]any)[1] = map[string]any{"type": "kpi", "binding": map[string]any{"metrics": []any{
		map[string]any{"point_id": point},
		map[string]any{"metric": "active_alarms"},
		map[string]any{"metric": "communication_success_rate"},
		map[string]any{"metric": "data_freshness"},
	}}, "span": 2}
	status, body := call(t, http.MethodPut, f.base+"/ems/screen/"+area, f.token, draft, nil)
	if status != http.StatusOK {
		t.Fatalf("save point-bound structured screen: %d %s", status, body)
	}
	var screen struct {
		PointIDs []string `json:"point_ids"`
	}
	status, body = call(t, http.MethodGet, f.base+"/ems/screen/"+area, f.token, nil, &screen)
	if status != http.StatusOK || len(screen.PointIDs) != 1 || screen.PointIDs[0] != point {
		t.Fatalf("overview-compatible point_ids were not derived: %d %s", status, body)
	}
}
