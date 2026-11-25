package appapi

import (
	"net/http"
	"testing"
)

func automaticScreen(layer string, version int) map[string]any {
	return map[string]any{
		"layer": layer, "version": version, "title": "自动画面", "columns": 2,
		"slots": map[string]any{"top": []string{"status", "kpi"}, "main": []string{}, "side": []string{}},
		"blocks": []any{
			map[string]any{"type": "status", "binding": map[string]any{}, "span": 2},
			map[string]any{"type": "kpi", "binding": map[string]any{"metrics": []any{
				map[string]any{"metric": "online_devices"},
				map[string]any{"metric": "active_alarms"},
				map[string]any{"metric": "communication_success_rate"},
				map[string]any{"metric": "data_freshness"},
			}}, "span": 2},
		},
	}
}

func TestScreenConfiguratorResolvesNearestAncestorAndVersions(t *testing.T) {
	f := newPointConfigFixture(t)
	root := snapshotRoot(t, f.base, f.token)
	area := createNodeAPI(t, f.base, f.token, "group", root, "一层区域", 0)
	child := createNodeAPI(t, f.base, f.token, "group", area, "二层区域", 0)

	status, body := call(t, http.MethodPut, f.base+"/ems/screen/"+area, f.token, automaticScreen(area, 0), nil)
	if status != http.StatusOK {
		t.Fatalf("save structured screen: %d %s", status, body)
	}
	var inherited map[string]any
	status, body = call(t, http.MethodGet, f.base+"/ems/screen/"+child, f.token, nil, &inherited)
	if status != http.StatusOK || inherited["inherited_from"] != area || inherited["version"] != float64(1) {
		t.Fatalf("nearest ancestor was not resolved: %d %s", status, body)
	}

	draft := automaticScreen(child, 0)
	status, body = call(t, http.MethodPut, f.base+"/ems/screen/"+child, f.token, draft, nil)
	if status != http.StatusOK {
		t.Fatalf("save inherited draft as own screen: %d %s", status, body)
	}
	draft["version"] = 0
	status, body = call(t, http.MethodPut, f.base+"/ems/screen/"+child, f.token, draft, nil)
	if status != http.StatusConflict {
		t.Fatalf("stale version accepted: %d %s", status, body)
	}
	var own map[string]any
	status, body = call(t, http.MethodGet, f.base+"/ems/screen/"+child, f.token, nil, &own)
	if status != http.StatusOK || own["inherited_from"] != nil || own["layer"] != child || own["version"] != float64(1) {
		t.Fatalf("own screen did not supersede ancestor: %d %s", status, body)
	}
}

func TestScreenConfiguratorAppliesToSameKind(t *testing.T) {
	f := newPointConfigFixture(t)
	root := snapshotRoot(t, f.base, f.token)
	area := createNodeAPI(t, f.base, f.token, "group", root, "区域甲", 0)
	sibling := createNodeAPI(t, f.base, f.token, "group", root, "区域乙", 0)
	createNodeAPI(t, f.base, f.token, "variable", root, "不同类点位", 0)

	draft := automaticScreen(area, 0)
	draft["apply_to_same"] = true
	var saved struct {
		Saved   bool `json:"saved"`
		Applied int  `json:"applied"`
	}
	status, body := call(t, http.MethodPut, f.base+"/ems/screen/"+area, f.token, draft, &saved)
	if status != http.StatusOK || !saved.Saved || saved.Applied != 2 {
		t.Fatalf("same-kind apply: %d %s %+v", status, body, saved)
	}
	var target map[string]any
	status, body = call(t, http.MethodGet, f.base+"/ems/screen/"+sibling, f.token, nil, &target)
	if status != http.StatusOK || target["inherited_from"] != nil || target["layer"] != sibling || target["version"] != float64(1) {
		t.Fatalf("same-kind target has no own saved document: %d %s", status, body)
	}
}

func TestScreenConfiguratorValidatesBindingsAndKeepsSimpleCompatibility(t *testing.T) {
	f := newPointConfigFixture(t)
	root := snapshotRoot(t, f.base, f.token)
	area := createNodeAPI(t, f.base, f.token, "group", root, "区域甲", 0)
	other := createNodeAPI(t, f.base, f.token, "group", root, "区域乙", 0)
	localPoint := createNodeAPI(t, f.base, f.token, "variable", area, "本层点位", 0)
	outsidePoint := createNodeAPI(t, f.base, f.token, "variable", other, "层外点位", 0)

	simple := map[string]any{"title": "兼容画面", "columns": 4, "point_ids": []string{localPoint}}
	status, body := call(t, http.MethodPut, f.base+"/ems/screen/"+area, f.token, simple, nil)
	if status != http.StatusOK {
		t.Fatalf("simple screen compatibility broken: %d %s", status, body)
	}
	var loaded struct {
		Title    string   `json:"title"`
		Columns  int      `json:"columns"`
		PointIDs []string `json:"point_ids"`
	}
	status, body = call(t, http.MethodGet, f.base+"/ems/screen/"+area, f.token, nil, &loaded)
	if status != http.StatusOK || loaded.Title != "兼容画面" || loaded.Columns != 4 || len(loaded.PointIDs) != 1 || loaded.PointIDs[0] != localPoint {
		t.Fatalf("simple screen read compatibility broken: %d %s %+v", status, body, loaded)
	}

	simple["point_ids"] = []string{outsidePoint}
	status, body = call(t, http.MethodPut, f.base+"/ems/screen/"+area, f.token, simple, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("out-of-scope simple binding accepted: %d %s", status, body)
	}

	draft := automaticScreen(other, 0)
	draft["version"] = 0
	blocks := draft["blocks"].([]any)
	blocks[1] = map[string]any{"type": "kpi", "binding": map[string]any{"metrics": []any{
		map[string]any{"point_id": outsidePoint},
		map[string]any{"metric": "active_alarms"},
		map[string]any{"metric": "communication_success_rate"},
		map[string]any{"metric": "data_freshness"},
	}}, "span": 2}
	// A point on the target itself is valid; a point in the sibling subtree is not.
	draft["layer"] = area
	status, body = call(t, http.MethodPut, f.base+"/ems/screen/"+area, f.token, draft, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("out-of-scope structured binding accepted: %d %s", status, body)
	}

	draft = automaticScreen(area, 0)
	blocks = draft["blocks"].([]any)
	blocks[0] = map[string]any{"type": "thirdPartyChart", "binding": map[string]any{}, "span": 2}
	draft["slots"] = map[string]any{"top": []string{"thirdPartyChart", "kpi"}, "main": []string{}, "side": []string{}}
	status, body = call(t, http.MethodPut, f.base+"/ems/screen/"+area, f.token, draft, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("unknown block accepted: %d %s", status, body)
	}
}

func TestScreenConfiguratorSameKindApplyStaysInsideAuthorizedScope(t *testing.T) {
	f := newPointConfigFixture(t)
	root := snapshotRoot(t, f.base, f.token)
	inside := createNodeAPI(t, f.base, f.token, "group", root, "Allowed", 0)
	child := createNodeAPI(t, f.base, f.token, "group", inside, "Allowed child", 0)
	outside := createNodeAPI(t, f.base, f.token, "group", root, "Private", 0)
	token := inputAccount(t, f, inside, "viewer", []string{ActionConfigure})
	draft := automaticScreen(inside, 0)
	draft["apply_to_same"] = true
	var result struct {
		Applied int `json:"applied"`
	}
	status, body := call(t, http.MethodPut, f.base+"/ems/screen/"+inside, token, draft, &result)
	if status != http.StatusOK || result.Applied != 2 {
		t.Fatalf("authorized batch: %d %s", status, body)
	}
	status, _ = call(t, http.MethodGet, f.base+"/ems/screen/"+child, token, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("child not saved: %d", status)
	}
	status, _ = call(t, http.MethodGet, f.base+"/ems/screen/"+outside, f.token, nil, nil)
	if status != http.StatusNotFound {
		t.Fatalf("out of scope target modified: %d", status)
	}
	status, _ = call(t, http.MethodPut, f.base+"/ems/screen/"+outside, token, automaticScreen(outside, 0), nil)
	if status != http.StatusForbidden {
		t.Fatalf("unauthorized direct save: %d", status)
	}
}

func TestScreenConfiguratorLegacyWriteCannotBypassSavedVersion(t *testing.T) {
	f := newPointConfigFixture(t)
	root := snapshotRoot(t, f.base, f.token)
	status, body := call(t, http.MethodPut, f.base+"/ems/screen/"+root, f.token, automaticScreen(root, 0), nil)
	if status != http.StatusOK {
		t.Fatalf("seed: %d %s", status, body)
	}
	status, body = call(t, http.MethodPut, f.base+"/ems/screen/"+root, f.token, map[string]any{"title": "stale legacy client", "point_ids": []string{}}, nil)
	if status != http.StatusConflict {
		t.Fatalf("legacy version bypass: %d %s", status, body)
	}
}

func TestScreenConfiguratorBindsOnlyPointsOfBoundBusinessDevices(t *testing.T) {
	base, token, _, ids := overviewLiveFixture(t)
	unit := createNodeAPI(t, base, token, "unit", ids["North"], "Referenced member", 0)
	status, body := call(t, http.MethodPatch, base+"/nodes/"+unit, token, map[string]any{"description": "Referenced member", "members_version": 0, "members": []map[string]any{{"role": "drive", "node_id": ids["North pump"], "required": true}}}, nil)
	if status != http.StatusOK {
		t.Fatalf("member binding: %d %s", status, body)
	}
	for _, item := range []struct {
		layer, point string
		want         int
	}{
		{ids["North"], ids["Zero pressure"], http.StatusOK},
		{unit, ids["Zero pressure"], http.StatusOK},
		{ids["North"], ids["South pressure"], http.StatusBadRequest},
		{ids["root"], ids["Unbound telemetry"], http.StatusBadRequest},
	} {
		draft := automaticScreen(item.layer, 0)
		if item.layer == ids["North"] && item.want != http.StatusOK {
			draft["version"] = 1
		}
		draft["blocks"] = append(draft["blocks"].([]any), map[string]any{"type": "trend", "binding": map[string]any{"point_id": item.point}, "span": 2})
		draft["slots"].(map[string]any)["side"] = []string{"trend"}
		status, body := call(t, http.MethodPut, base+"/ems/screen/"+item.layer, token, draft, nil)
		if status != item.want {
			t.Fatalf("binding scope %s: got %d want %d: %s", item.point, status, item.want, body)
		}
	}
}

func TestScreenConfiguratorRejectsNonPointAndUnknownBindingFields(t *testing.T) {
	f := newPointConfigFixture(t)
	root := snapshotRoot(t, f.base, f.token)
	area := createNodeAPI(t, f.base, f.token, "group", root, "Not a point", 0)
	for _, binding := range []map[string]any{
		{"point_id": area},
		{"point_id": "", "script": "not allowed"},
	} {
		draft := automaticScreen(root, 0)
		draft["blocks"] = append(draft["blocks"].([]any), map[string]any{"type": "trend", "binding": binding, "span": 2})
		draft["slots"].(map[string]any)["side"] = []string{"trend"}
		status, body := call(t, http.MethodPut, f.base+"/ems/screen/"+root, f.token, draft, nil)
		if status != http.StatusBadRequest {
			t.Fatalf("non-declarative/non-point binding accepted: %d %s", status, body)
		}
	}
}

func TestScreenConfiguratorSameKindPreflightsEveryVersion(t *testing.T) {
	f := newPointConfigFixture(t)
	root := snapshotRoot(t, f.base, f.token)
	area := createNodeAPI(t, f.base, f.token, "group", root, "Source", 0)
	sibling := createNodeAPI(t, f.base, f.token, "group", root, "Target", 0)
	status, body := call(t, http.MethodPut, f.base+"/ems/screen/"+sibling, f.token, automaticScreen(sibling, 0), nil)
	if status != http.StatusOK {
		t.Fatalf("seed: %d %s", status, body)
	}
	draft := automaticScreen(area, 0)
	draft["apply_to_same"] = true
	for _, versions := range []any{nil, map[string]int{sibling: 0}} {
		draft["target_versions"] = versions
		status, body = call(t, http.MethodPut, f.base+"/ems/screen/"+area, f.token, draft, nil)
		if status != http.StatusConflict {
			t.Fatalf("stale/unknown sibling overwritten: %d %s", status, body)
		}
		status, _ = call(t, http.MethodGet, f.base+"/ems/screen/"+area, f.token, nil, nil)
		if status != http.StatusNotFound {
			t.Fatalf("failed preflight partially saved source: %d", status)
		}
	}
	draft["target_versions"] = map[string]int{sibling: 1}
	status, body = call(t, http.MethodPut, f.base+"/ems/screen/"+area, f.token, draft, nil)
	if status != http.StatusOK {
		t.Fatalf("fresh batch rejected: %d %s", status, body)
	}
	var saved map[string]any
	call(t, http.MethodGet, f.base+"/ems/screen/"+sibling, f.token, nil, &saved)
	if saved["version"] != float64(2) {
		t.Fatalf("target version did not advance: %+v", saved)
	}
}

func TestScreenConfiguratorRejectsMovingBlocksAcrossFixedSlots(t *testing.T) {
	f := newPointConfigFixture(t)
	root := snapshotRoot(t, f.base, f.token)
	draft := automaticScreen(root, 0)
	draft["slots"] = map[string]any{"top": []string{"kpi"}, "main": []string{"status"}, "side": []string{}}
	status, body := call(t, http.MethodPut, f.base+"/ems/screen/"+root, f.token, draft, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("fixed-slot violation accepted: %d %s", status, body)
	}
}

func TestScreenConfiguratorRejectsApplyWithOutOfScopeTarget(t *testing.T) {
	f := newPointConfigFixture(t)
	root := snapshotRoot(t, f.base, f.token)
	area := createNodeAPI(t, f.base, f.token, "group", root, "区域甲", 0)
	sibling := createNodeAPI(t, f.base, f.token, "group", root, "区域乙", 0)
	point := createNodeAPI(t, f.base, f.token, "variable", area, "只属于区域甲", 0)

	draft := automaticScreen(area, 0)
	draft["blocks"].([]any)[1] = map[string]any{"type": "kpi", "binding": map[string]any{"metrics": []any{
		map[string]any{"point_id": point},
		map[string]any{"metric": "active_alarms"},
		map[string]any{"metric": "communication_success_rate"},
		map[string]any{"metric": "data_freshness"},
	}}, "span": 2}
	draft["apply_to_same"] = true
	status, body := call(t, http.MethodPut, f.base+"/ems/screen/"+area, f.token, draft, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("same-kind apply copied an out-of-scope binding: %d %s", status, body)
	}
	status, _ = call(t, http.MethodGet, f.base+"/ems/screen/"+sibling, f.token, nil, nil)
	if status != http.StatusNotFound {
		t.Fatalf("failed apply partially saved a target: %d", status)
	}
}
