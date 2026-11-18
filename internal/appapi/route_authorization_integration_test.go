package appapi

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"fastiot/internal/audit"
	"fastiot/internal/calculation"
	"fastiot/internal/ems/alarm"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/emsapi"
	"fastiot/internal/virtualinput"
	"github.com/simpleiot/simpleiot/data"
)

func TestChannelRoutesUseConfigureAndDeleteActions(t *testing.T) {
	base, admin, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	t.Cleanup(service.Close)
	api := emsapi.New(service, time.Minute)
	api.Configure = NewChannelAdmin(h.nc)
	begun := 0
	api.BeginAlarmConfiguration = func() func() {
		begun++
		return func() {}
	}
	h.SetEMS(api)
	fixture := &pointConfigFixture{base: base, token: admin}
	root := snapshotRoot(t, base, admin)
	creator := inputAccount(t, fixture, root, "viewer", []string{ActionConfigure})
	deleter := inputAccount(t, fixture, root, "viewer", []string{ActionDelete})

	payload := map[string]any{
		"parent": root, "protocol": "tcp", "address": "127.0.0.1:1502", "period_ms": 1000,
		"devices": []map[string]any{{
			"server_id": 1,
			"points": []map[string]any{{
				"name": "power", "function": "holdingRegister", "address": 0,
				"format": "uint16", "scale": 1, "kind": "number", "read_only": true,
			}},
		}},
	}
	var created struct {
		ChannelID string `json:"channel_id"`
	}
	status, body := call(t, http.MethodPost, base+"/ems/channels", creator, payload, &created)
	if status != http.StatusOK || created.ChannelID == "" {
		t.Fatalf("configure action could not create channel: %d %s", status, body)
	}
	status, body = call(t, http.MethodDelete, base+"/ems/channels/"+created.ChannelID, creator, nil, nil)
	if status != http.StatusForbidden {
		t.Fatalf("configure action deleted channel without delete action: %d %s", status, body)
	}
	if begun != 1 {
		t.Fatalf("denied delete entered configuration boundary: %d", begun)
	}
	status, body = call(t, http.MethodDelete, base+"/ems/channels/"+created.ChannelID, deleter, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("delete action could not delete channel: %d %s", status, body)
	}
	if begun != 2 {
		t.Fatalf("channel create/delete configuration boundaries = %d, want 2", begun)
	}
}

func TestRuntimeRoutesUseActionSet(t *testing.T) {
	base, admin, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	t.Cleanup(service.Close)
	h.SetEMS(emsapi.New(service, time.Minute))
	fixture := &pointConfigFixture{base: base, token: admin}
	root := snapshotRoot(t, base, admin)
	point := createNodeAPI(t, base, admin, "variable", root, "temperature", 0)
	raised, err := service.Alarms.Raise(time.Now(), "temperature:root", alarm.Warning, "hot", "46", root, point)
	if err != nil {
		t.Fatal(err)
	}
	viewer := inputAccount(t, fixture, root, "viewer", nil)
	operator := inputAccount(t, fixture, root, "operator", nil)
	ackOnly := inputAccount(t, fixture, root, "viewer", []string{ActionAlarmAck})
	maintainOnly := inputAccount(t, fixture, root, "viewer", []string{ActionAlarmMaintain})

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		status, body := call(t, method, base+"/ems/frames", viewer, nil, nil)
		if status != http.StatusForbidden {
			t.Fatalf("viewer %s frames: %d %s", method, status, body)
		}
		status, body = call(t, method, base+"/ems/frames", operator, nil, nil)
		if status != http.StatusOK {
			t.Fatalf("operator %s frames: %d %s", method, status, body)
		}
	}
	status, body := call(t, http.MethodPost, base+"/ems/alarms/"+raised.ID+"/ack", maintainOnly, map[string]any{"note": "no"}, nil)
	if status != http.StatusForbidden {
		t.Fatalf("maintain-only account acknowledged alarm: %d %s", status, body)
	}
	status, body = call(t, http.MethodPost, base+"/ems/alarms/"+raised.ID+"/ack", ackOnly, map[string]any{"note": "ack"}, nil)
	if status != http.StatusOK {
		t.Fatalf("ack action rejected: %d %s", status, body)
	}
	maintenance := map[string]any{"until": time.Now().Add(time.Hour).Format(time.RFC3339), "reason": "service"}
	status, body = call(t, http.MethodPost, base+"/ems/alarms/"+raised.ID+"/maintenance", ackOnly, maintenance, nil)
	if status != http.StatusForbidden {
		t.Fatalf("ack-only account maintained alarm: %d %s", status, body)
	}
	status, body = call(t, http.MethodPost, base+"/ems/alarms/"+raised.ID+"/maintenance", maintainOnly, maintenance, nil)
	if status != http.StatusOK {
		t.Fatalf("maintain action rejected: %d %s", status, body)
	}
	status, body = call(t, http.MethodDelete, base+"/ems/alarms/"+raised.ID+"/maintenance", maintainOnly, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("maintenance release rejected: %d %s", status, body)
	}
	if _, err := service.Alarms.Clear(time.Now(), raised.Key); err != nil {
		t.Fatal(err)
	}
	status, body = call(t, http.MethodPost, base+"/ems/alarms/"+raised.ID+"/close", ackOnly, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("close action rejected: %d %s", status, body)
	}
}

func TestProjectReadRoutesRequireConfigure(t *testing.T) {
	base, admin, _, _ := startInputServer(t, t.TempDir(), data.NewID())
	fixture := &pointConfigFixture{base: base, token: admin}
	root := snapshotRoot(t, base, admin)
	viewer := inputAccount(t, fixture, root, "viewer", nil)
	configurer := inputAccount(t, fixture, root, "viewer", []string{ActionConfigure})
	for _, path := range []string{"/projects/sources", "/projects/reuse/preview?source=" + root, "/projects/versions?site=" + root} {
		status, body := call(t, http.MethodGet, base+path, viewer, nil, nil)
		if status != http.StatusForbidden {
			t.Errorf("viewer %s: %d %s", path, status, body)
		}
		status, body = call(t, http.MethodGet, base+path, configurer, nil, nil)
		if status == http.StatusForbidden {
			t.Errorf("configure action %s: %d %s", path, status, body)
		}
	}
}

func TestVirtualInputImpactFailsClosedWhenActionIsUnresolved(t *testing.T) {
	base, admin, _, _ := startInputServer(t, t.TempDir(), data.NewID())
	fixture := &pointConfigFixture{base: base, token: admin}
	root := snapshotRoot(t, base, admin)
	viewer := inputAccount(t, fixture, root, "viewer", nil)
	min, max := 0.0, 10.0
	definition := virtualinput.Definition{Kind: "number", Unit: "kW", Purpose: "test", Min: &min, Max: &max, Validity: "constant", Value: json.RawMessage("5")}
	var input inputResponse
	status, body := call(t, http.MethodPost, base+"/virtual-inputs", admin, map[string]any{"parent": root, "name": "capacity", "source": "configuration", "definition": definition, "operation_id": "create"}, &input)
	if status != http.StatusOK {
		t.Fatalf("create: %d %s", status, body)
	}
	status, body = call(t, http.MethodPost, base+"/virtual-inputs/"+input.ID+"/apply", admin, map[string]any{"revision": input.Revision, "operation_id": "apply"}, &input)
	if status != http.StatusOK {
		t.Fatalf("apply: %d %s", status, body)
	}
	body = strings.Repeat(" ", 1<<20+1) + `{"action":"apply","revision":` + strconv.FormatUint(input.Revision, 10) + `,"operation_id":"preview"}`
	for _, token := range []string{viewer, admin} {
		request, err := http.NewRequest(http.MethodPost, base+"/virtual-inputs/"+input.ID+"/impact", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if token == viewer && response.StatusCode != http.StatusForbidden {
			t.Errorf("unresolved viewer impact = %d, want 403", response.StatusCode)
		}
		if token == admin && response.StatusCode != http.StatusOK {
			t.Errorf("admin impact with large valid body = %d, want 200", response.StatusCode)
		}
	}
}

func TestRoleDefaultsForEveryActionOverHTTP(t *testing.T) {
	base, admin, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	t.Cleanup(service.Close)
	h.SetEMS(emsapi.New(service, time.Minute))
	h.SetAudit(audit.New(filepath.Join(t.TempDir(), "audit.jsonl")))
	fixture := &pointConfigFixture{base: base, token: admin}
	root := snapshotRoot(t, base, admin)
	point := createNodeAPI(t, base, admin, "variable", root, "temperature", 0)
	tokens := map[string]string{"admin": admin}
	for _, role := range []string{"implementer", "operator", "viewer"} {
		tokens[role] = inputAccount(t, fixture, root, role, nil)
	}
	raised, err := service.Alarms.Raise(time.Now(), "temperature:root", alarm.Warning, "hot", "46", root, point)
	if err != nil {
		t.Fatal(err)
	}
	min, max := 0.0, 10.0
	definition := virtualinput.Definition{Kind: "number", Unit: "kW", Purpose: "test", Min: &min, Max: &max, Validity: "timed", TTLSeconds: 60, RuntimeEditable: true}
	var input inputResponse
	status, body := call(t, http.MethodPost, base+"/virtual-inputs", admin, map[string]any{"parent": root, "name": "manual", "source": "manual", "definition": definition, "operation_id": "create"}, &input)
	if status != http.StatusOK {
		t.Fatalf("create input: %d %s", status, body)
	}
	status, body = call(t, http.MethodPost, base+"/virtual-inputs/"+input.ID+"/apply", admin, map[string]any{"revision": input.Revision, "operation_id": "apply"}, &input)
	if status != http.StatusOK {
		t.Fatalf("apply input: %d %s", status, body)
	}
	var calculationPoint calcResponse
	calculationDefinition := calculation.Definition{Operator: "add", Inputs: []string{input.ID, input.ID}, Kind: "number", Unit: "kW"}
	status, body = call(t, http.MethodPost, base+"/calculations", admin, map[string]any{"parent": root, "name": "double", "definition": calculationDefinition, "operation_id": "create"}, &calculationPoint)
	if status != http.StatusOK {
		t.Fatalf("create calculation: %d %s", status, body)
	}
	var rule struct {
		ID string `json:"id"`
	}
	status, body = call(t, http.MethodPost, base+"/rules", admin, map[string]any{"parent": root, "description": "empty", "document": map[string]any{"table": map[string]any{"rows": []any{}}}}, &rule)
	if status != http.StatusOK {
		t.Fatalf("create rule: %d %s", status, body)
	}

	allowedRoles := map[string]map[string]bool{
		ActionCalculationApply:  {"admin": true, "implementer": true},
		ActionVirtualInputApply: {"admin": true, "implementer": true},
		ActionVirtualInputWrite: {"admin": true, "implementer": true, "operator": true},
		ActionConfigure:         {"admin": true, "implementer": true},
		ActionRulePublish:       {"admin": true, "implementer": true},
		ActionDelete:            {"admin": true, "implementer": true},
		ActionExport:            {"admin": true, "implementer": true},
		ActionWriteValue:        {"admin": true, "implementer": true, "operator": true},
		ActionCommand:           {"admin": true, "implementer": true, "operator": true},
		ActionAlarmAck:          {"admin": true, "implementer": true, "operator": true},
		ActionAlarmMaintain:     {"admin": true, "implementer": true, "operator": true},
		ActionAccountManage:     {"admin": true},
		ActionAuditView:         {"admin": true},
		ActionSiteSettings:      {"admin": true},
	}
	for _, action := range allActions {
		for _, role := range []string{"admin", "implementer", "operator", "viewer"} {
			allowed := allowedRoles[action][role]
			method, path, payload := http.MethodGet, "", any(nil)
			switch action {
			case ActionConfigure:
				method, path, payload = http.MethodPost, "/nodes", map[string]any{}
			case ActionWriteValue:
				method, path, payload = http.MethodPost, "/nodes/"+point+"/value", map[string]any{"value": 1, "point_type": "invalid"}
			case ActionCommand:
				path = "/ems/frames"
			case ActionRulePublish:
				method, path, payload = http.MethodPost, "/rules/"+rule.ID+"/enable", map[string]any{"enabled": false}
			case ActionAlarmAck:
				method, path, payload = http.MethodPost, "/ems/alarms/"+raised.ID+"/ack", map[string]any{}
			case ActionAlarmMaintain:
				method, path, payload = http.MethodPost, "/ems/alarms/"+raised.ID+"/maintenance", map[string]any{"until": time.Now().Add(time.Hour).Format(time.RFC3339), "reason": "test"}
			case ActionDelete:
				method, payload = http.MethodDelete, map[string]any{"parent": root}
				target := point
				if allowed {
					target = createNodeAPI(t, base, admin, "variable", root, "delete-"+role+"-"+action, 0)
				}
				path = "/nodes/" + target
			case ActionExport:
				path = "/history/export"
			case ActionAccountManage:
				path = "/users"
			case ActionAuditView:
				path = "/audit"
			case ActionSiteSettings:
				method, path, payload = http.MethodPut, "/history-settings", map[string]any{"limit": 1000}
			case ActionVirtualInputApply:
				method, path, payload = http.MethodPost, "/virtual-inputs/"+input.ID+"/apply", map[string]any{"revision": 0, "operation_id": "apply-" + role}
			case ActionVirtualInputWrite:
				method, path, payload = http.MethodPost, "/virtual-inputs/"+input.ID+"/value", map[string]any{"revision": 0, "operation_id": "write-" + role, "value": 5}
			case ActionCalculationApply:
				method, path, payload = http.MethodPost, "/calculations/"+calculationPoint.ID+"/apply", map[string]any{"revision": 0, "operation_id": "apply-" + role}
			}
			status, body := call(t, method, base+path, tokens[role], payload, nil)
			if allowed && status == http.StatusForbidden {
				t.Errorf("%s %s was forbidden: %s", role, action, body)
			}
			if !allowed && status != http.StatusForbidden {
				t.Errorf("%s %s = %d, want 403: %s", role, action, status, body)
			}
		}
	}
}

func TestDirectScopeTargetsOverHTTP(t *testing.T) {
	base, admin, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	t.Cleanup(service.Close)
	h.SetEMS(emsapi.New(service, time.Minute))
	fixture := &pointConfigFixture{base: base, token: admin}
	root := snapshotRoot(t, base, admin)
	a := createNodeAPI(t, base, admin, "group", root, "A", 0)
	b := createNodeAPI(t, base, admin, "group", root, "B", 0)
	pointA := createNodeAPI(t, base, admin, "variable", a, "A temperature", 0)
	pointB := createNodeAPI(t, base, admin, "variable", b, "B temperature", 0)
	implementer := inputAccount(t, fixture, a, "implementer", nil)
	alarmA, err := service.Alarms.Raise(time.Now(), "temperature:A", alarm.Warning, "A", "46", a, pointA)
	if err != nil {
		t.Fatal(err)
	}
	alarmB, err := service.Alarms.Raise(time.Now(), "temperature:B", alarm.Warning, "B", "46", b, pointB)
	if err != nil {
		t.Fatal(err)
	}

	status, body := call(t, http.MethodPost, base+"/nodes/"+pointB+"/value", implementer, map[string]any{"value": 1, "point_type": "invalid"}, nil)
	if status != http.StatusForbidden || !strings.Contains(body, "forbidden") {
		t.Errorf("path target outside scope: %d %s", status, body)
	}
	status, body = call(t, http.MethodPost, base+"/nodes/"+pointA+"/value", implementer, map[string]any{"value": 1, "point_type": "invalid"}, nil)
	if status != http.StatusBadRequest {
		t.Errorf("path target inside scope: %d %s", status, body)
	}
	status, body = call(t, http.MethodGet, base+"/projects/versions?site="+b, implementer, nil, nil)
	if status != http.StatusForbidden {
		t.Errorf("query target outside scope: %d %s", status, body)
	}
	status, body = call(t, http.MethodGet, base+"/projects/versions?site="+a, implementer, nil, nil)
	if status != http.StatusOK {
		t.Errorf("query target inside scope: %d %s", status, body)
	}
	status, body = call(t, http.MethodGet, base+"/history?node_id="+pointB, implementer, nil, nil)
	if status != http.StatusForbidden || !strings.Contains(body, "history_forbidden") {
		t.Errorf("history target outside scope: %d %s", status, body)
	}
	status, body = call(t, http.MethodPost, base+"/nodes", implementer, map[string]any{"parent": b, "type": "variable", "description": "outside"}, nil)
	if status != http.StatusBadRequest || !strings.Contains(body, "invalid_node") {
		t.Errorf("body target outside scope: %d %s", status, body)
	}
	status, body = call(t, http.MethodPost, base+"/nodes", implementer, map[string]any{"parent": a, "type": "variable", "description": "inside"}, nil)
	if status != http.StatusOK {
		t.Errorf("body target inside scope: %d %s", status, body)
	}
	status, body = call(t, http.MethodPost, base+"/ems/alarms/"+alarmB.ID+"/ack", implementer, map[string]any{}, nil)
	if status != http.StatusNotFound || !strings.Contains(body, "alarm_not_found") {
		t.Errorf("alarm target outside scope: %d %s", status, body)
	}
	status, body = call(t, http.MethodPost, base+"/ems/alarms/"+alarmA.ID+"/ack", implementer, map[string]any{}, nil)
	if status != http.StatusOK {
		t.Errorf("alarm target inside scope: %d %s", status, body)
	}
}

func TestBodyScopeFailsClosedWhenJSONIsUnresolved(t *testing.T) {
	base, admin, _, _ := startInputServer(t, t.TempDir(), data.NewID())
	fixture := &pointConfigFixture{base: base, token: admin}
	root := snapshotRoot(t, base, admin)
	a := createNodeAPI(t, base, admin, "group", root, "A", 0)
	b := createNodeAPI(t, base, admin, "group", root, "B", 0)
	implementer := inputAccount(t, fixture, a, "implementer", nil)
	for _, test := range []struct {
		name, token, parent string
		want                int
	}{
		{"unresolved scope denied", implementer, b, http.StatusBadRequest},
		{"admin body still reaches handler", admin, a, http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := strings.Repeat(" ", 1<<20+1) + `{"parent":` + strconv.Quote(test.parent) + `,"type":"variable","description":"large"}`
			request, err := http.NewRequest(http.MethodPost, base+"/nodes", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+test.token)
			request.Header.Set("Content-Type", "application/json")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != test.want {
				t.Errorf("status = %d, want %d", response.StatusCode, test.want)
			}
		})
	}
}

func callRawJSON(t *testing.T, method, url, token, body string) (int, string) {
	t.Helper()
	request, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(raw)
}

func TestBodyScopeMatchesJSONFieldFoldingAndOrder(t *testing.T) {
	base, admin, _, _ := startInputServer(t, t.TempDir(), data.NewID())
	fixture := &pointConfigFixture{base: base, token: admin}
	root := snapshotRoot(t, base, admin)
	a := createNodeAPI(t, base, admin, "group", root, "A", 0)
	b := createNodeAPI(t, base, admin, "group", root, "B", 0)
	point := createNodeAPI(t, base, admin, "variable", a, "temperature", 0)
	implementer := inputAccount(t, fixture, a, "implementer", nil)

	status, body := callRawJSON(t, http.MethodPost, base+"/nodes/"+point+"/move", implementer,
		`{"OLD_PARENT":`+strconv.Quote(a)+`,"NEW_PARENT":`+strconv.Quote(b)+`}`)
	if status != http.StatusForbidden || !strings.Contains(body, "destination_forbidden") {
		t.Errorf("uppercase move target bypassed scope: %d %s", status, body)
	}
	status, body = callRawJSON(t, http.MethodPost, base+"/nodes", implementer,
		`{"parent":`+strconv.Quote(a)+`,"PARENT":`+strconv.Quote(b)+`,"type":"variable","description":"last outside"}`)
	if status != http.StatusBadRequest || !strings.Contains(body, "invalid_node") {
		t.Errorf("last duplicate target outside scope: %d %s", status, body)
	}
	status, body = callRawJSON(t, http.MethodPost, base+"/nodes", implementer,
		`{"parent":`+strconv.Quote(b)+`,"PARENT":`+strconv.Quote(a)+`,"type":"variable","description":"last inside"}`)
	if status != http.StatusOK {
		t.Errorf("last duplicate target inside scope: %d %s", status, body)
	}
}

func TestMissingBodyTargetsKeepValidation(t *testing.T) {
	base, admin, _, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, admin)
	implementer := inputAccount(t, &pointConfigFixture{base: base, token: admin}, root, "implementer", nil)
	for _, test := range []struct {
		name, token, path, reason string
		body                      any
	}{
		{"node", implementer, "/nodes", "invalid_node", map[string]any{"type": "variable", "description": "missing parent"}},
		{"rule", implementer, "/rules", "invalid_rule", map[string]any{"description": "missing parent", "document": map[string]any{"table": map[string]any{"rows": []any{}}}}},
		{"modbus", implementer, "/modbus", "invalid_modbus_connection", map[string]any{"description": "missing parent", "protocol": "TCP", "uri": "127.0.0.1:502", "poll_period": 1000, "timeout": 500}},
		{"template", implementer, "/templates", "invalid_template", map[string]any{"description": "missing parent"}},
		{"user", admin, "/users", "invalid_user", map[string]any{"email": "person@example.test", "password": "long-test-password", "role": "viewer"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, body := call(t, http.MethodPost, base+test.path, test.token, test.body, nil)
			if status != http.StatusBadRequest || !strings.Contains(body, test.reason) {
				t.Errorf("missing parent: %d %s", status, body)
			}
		})
	}
}
