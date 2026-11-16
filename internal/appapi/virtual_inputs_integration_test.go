package appapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"fastiot/internal/virtualinput"
	"github.com/simpleiot/simpleiot/data"
)

func virtualCapacity(value string) virtualinput.Definition {
	min, max := 0.0, 200.0
	return virtualinput.Definition{Kind: "number", Unit: "kWh", Purpose: "容量上限", Min: &min, Max: &max, Validity: "constant", Value: json.RawMessage(value)}
}

type inputResponse struct {
	ID       string            `json:"id"`
	Revision uint64            `json:"revision"`
	State    virtualinput.View `json:"state"`
}

func inputAccount(t *testing.T, f *pointConfigFixture, parent, role string, actions []string) string {
	t.Helper()
	email, password := data.NewID()+"@fixture.local", data.NewID()
	status, body := call(t, http.MethodPost, f.base+"/users", f.token, map[string]any{"parent": parent, "email": email, "password": password, "role": role, "actions": actions}, nil)
	if status != 200 {
		t.Fatalf("create scoped account: %d %s", status, body)
	}
	return loginAPI(t, f.base, email, password)
}

func TestVirtualInputCreationRetryAndSnapshotIsolation(t *testing.T) {
	f := newPointConfigFixture(t)
	root := snapshotRoot(t, f.base, f.token)
	payload := map[string]any{"parent": root, "name": "capacity", "source": "configuration", "definition": virtualCapacity("100"), "operation_id": "create-retry"}
	var first, retry inputResponse
	status, body := call(t, http.MethodPost, f.base+"/virtual-inputs", f.token, payload, &first)
	if status != 200 {
		t.Fatalf("create: %d %s", status, body)
	}
	status, body = call(t, http.MethodPost, f.base+"/virtual-inputs", f.token, payload, &retry)
	if status != 200 || retry.ID != first.ID {
		t.Errorf("retry created duplicate identity: %d %s", status, body)
	}
	payload["definition"] = virtualCapacity("150")
	status, _ = call(t, http.MethodPost, f.base+"/virtual-inputs", f.token, payload, nil)
	if status != 409 {
		t.Errorf("same operation changed payload: %d", status)
	}
	viewer := inputAccount(t, f, root, "viewer", nil)
	ws, response, err := websocket.DefaultDialer.Dial(strings.Replace(f.base, "http://", "ws://", 1)+"/ws", http.Header{"Authorization": []string{"Bearer " + viewer}})
	if err != nil {
		t.Fatalf("websocket upgrade: %v, response status: %v", err, response.StatusCode)
	}
	defer ws.Close()
	_, message, err := ws.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(message), virtualInputDocument) {
		t.Error("websocket disclosed private document")
	}
	for _, node := range snapshotNodes(t, f.base, viewer) {
		if node.ID == first.ID {
			for _, p := range node.Points {
				if p.Type == virtualInputDocument {
					t.Error("snapshot disclosed private input document")
				}
			}
		}
	}
}

func TestVirtualInputAuthorizedTimedWrites(t *testing.T) {
	f := newPointConfigFixture(t)
	root := snapshotRoot(t, f.base, f.token)
	group := createNodeAPI(t, f.base, f.token, "group", root, "input-project", 0)
	operator := inputAccount(t, f, group, "operator", nil)
	viewer := inputAccount(t, f, group, "viewer", nil)
	outside := inputAccount(t, f, createNodeAPI(t, f.base, f.token, "group", root, "outside", 0), "operator", nil)
	min, max := -10.0, 10.0
	definition := virtualinput.Definition{Kind: "number", Unit: "kW", Purpose: "manual demand", Min: &min, Max: &max, Validity: "timed", TTLSeconds: 60, RuntimeEditable: true}
	var input inputResponse
	status, body := call(t, http.MethodPost, f.base+"/virtual-inputs", f.token, map[string]any{"parent": group, "name": "demand", "source": "manual", "definition": definition, "operation_id": "create"}, &input)
	if status != 200 {
		t.Fatalf("create: %d %s", status, body)
	}
	url := f.base + "/virtual-inputs/" + input.ID
	status, body = call(t, http.MethodPost, url+"/apply", f.token, map[string]any{"revision": input.Revision, "operation_id": "apply"}, &input)
	if status != 200 {
		t.Fatalf("apply: %d %s", status, body)
	}
	payload := map[string]any{"revision": input.Revision, "operation_id": "write", "value": 5}
	for _, token := range []string{viewer, outside} {
		status, _ = call(t, http.MethodPost, url+"/value", token, payload, nil)
		if status != 403 {
			t.Errorf("unauthorized write: %d", status)
		}
	}
	payload["value"] = 11
	status, _ = call(t, http.MethodPost, url+"/value", operator, payload, nil)
	if status != 400 {
		t.Errorf("out of range write: %d", status)
	}
	payload["value"] = 5
	status, body = call(t, http.MethodPost, url+"/value", operator, payload, &input)
	if status != 200 || string(input.State.Value) != "5" || input.State.Source != "manual" {
		t.Fatalf("authorized write: %d %s", status, body)
	}
	status, body = call(t, http.MethodPost, url+"/value", operator, payload, &input)
	if status != 200 || string(input.State.Value) != "5" {
		t.Fatalf("retry: %d %s", status, body)
	}
	status, body = call(t, http.MethodGet, url, viewer, nil, &input)
	if status != 200 || string(input.State.Value) != "5" {
		t.Fatalf("scoped reading: %d %s", status, body)
	}
}

func TestVirtualInputImpactConfirmationChangesWithDependencies(t *testing.T) {
	f := newPointConfigFixture(t)
	root := snapshotRoot(t, f.base, f.token)
	var input inputResponse
	status, body := call(t, http.MethodPost, f.base+"/virtual-inputs", f.token, map[string]any{"parent": root, "name": "capacity", "source": "configuration", "definition": virtualCapacity("100"), "operation_id": "create"}, &input)
	if status != 200 {
		t.Fatalf("create: %d %s", status, body)
	}
	screen := createNodeAPI(t, f.base, f.token, "group", root, "downstream-screen", 0)
	putScreen := func(label string) {
		t.Helper()
		status, body := call(t, http.MethodPut, f.base+"/ems/screen/"+screen, f.token, map[string]any{"widgets": []any{map[string]any{"pointId": input.ID, "label": label}}}, nil)
		if status != 200 {
			t.Fatalf("screen: %d %s", status, body)
		}
	}
	putScreen("first")
	url := f.base + "/virtual-inputs/" + input.ID
	payload := map[string]any{"revision": input.Revision, "operation_id": "apply", "action": "apply"}
	var preview struct {
		Confirmation string `json:"confirmation"`
		References   []struct {
			Name string `json:"name"`
		} `json:"references"`
	}
	status, body = call(t, http.MethodPost, url+"/impact", f.token, payload, &preview)
	if status != 200 || preview.Confirmation == "" || len(preview.References) != 1 || preview.References[0].Name != "downstream-screen" {
		t.Fatalf("impact: %d %s", status, body)
	}
	delete(payload, "action")
	status, _ = call(t, http.MethodPost, url+"/apply", f.token, payload, nil)
	if status != 409 {
		t.Errorf("missing confirmation accepted: %d", status)
	}
	payload["confirmation"] = preview.Confirmation
	putScreen("changed")
	status, _ = call(t, http.MethodPost, url+"/apply", f.token, payload, nil)
	if status != 409 {
		t.Errorf("stale impact accepted: %d", status)
	}
	payload["action"] = "apply"
	status, body = call(t, http.MethodPost, url+"/impact", f.token, payload, &preview)
	if status != 200 {
		t.Fatalf("new preview: %d %s", status, body)
	}
	delete(payload, "action")
	payload["confirmation"] = preview.Confirmation
	status, body = call(t, http.MethodPost, url+"/apply", f.token, payload, &input)
	if status != 200 || string(input.State.Value) != "100" {
		t.Fatalf("confirmed apply: %d %s", status, body)
	}
}

func TestVirtualInputRejectsInvalidStoredState(t *testing.T) {
	for _, corruption := range []string{"source", "revision", "range", "current"} {
		d, _ := virtualinput.New("configuration", virtualCapacity("100"), virtualinput.Context{Actor: "test", OperationID: "new", Now: time.Now()})
		switch corruption {
		case "source":
			d.Source = "measured"
		case "revision":
			d.Revision = 0
		case "range":
			d.Draft.Min = nil
		case "current":
			d.Current = &virtualinput.Entry{Value: json.RawMessage("999")}
		}
		encoded, _ := json.Marshal(d)
		_, err := decodeVirtualInput(data.NodeEdge{Type: virtualInputType, Points: data.Points{data.NewPointString(virtualInputDocument, "0", string(encoded))}})
		if err == nil {
			t.Errorf("accepted corrupt %s", corruption)
		}
	}
}

func TestVirtualInputClockRollbackDoesNotResurrect(t *testing.T) {
	h := New(nil, nil, nil)
	t0 := time.Now()
	c := h.inputClock(t0)
	c.Actor, c.OperationID = "operator", "new"
	min, max := -10.0, 10.0
	d, _ := virtualinput.New("manual", virtualinput.Definition{Kind: "number", Unit: "kW", Purpose: "demand", Min: &min, Max: &max, Validity: "timed", TTLSeconds: 60, RuntimeEditable: true}, c)
	c.ExpectedRevision, c.OperationID = d.Revision, "apply"
	d, _ = d.Apply(c)
	c.ExpectedRevision, c.OperationID = d.Revision, "value"
	d, _ = d.SetValue(json.RawMessage("5"), c)
	if d.Evaluate(h.inputClock(t0.Add(time.Minute))).Quality != "stale" {
		t.Fatal("not expired")
	}
	if d.Evaluate(h.inputClock(t0.Add(time.Second))).Quality == "good" {
		t.Fatal("wall rollback resurrected input")
	}
	if d.Evaluate(h.inputClock(t0.Add(2*time.Second))).Quality == "good" {
		t.Fatal("second read resurrected input")
	}
}

func TestVirtualInputRoleDefaults(t *testing.T) {
	for _, item := range []struct {
		role         string
		apply, write bool
	}{{"implementer", true, true}, {"operator", false, true}, {"viewer", false, false}} {
		identity := Identity{Policy: AccessPolicy{Role: item.role}}
		if canAction(identity, ActionVirtualInputApply) != item.apply || canAction(identity, ActionVirtualInputWrite) != item.write {
			t.Errorf("incorrect input capabilities for %s", item.role)
		}
	}
}

func TestVirtualInputApplyPermissionIsSeparateFromDraftEditing(t *testing.T) {
	f := newPointConfigFixture(t)
	root := snapshotRoot(t, f.base, f.token)
	editor := inputAccount(t, f, root, "implementer", []string{"configure"})
	publisher := inputAccount(t, f, root, "viewer", []string{"virtual_input_apply"})
	var input inputResponse
	status, body := call(t, http.MethodPost, f.base+"/virtual-inputs", editor, map[string]any{"parent": root, "name": "capacity", "source": "configuration", "definition": virtualCapacity("100"), "operation_id": "create"}, &input)
	if status != 200 {
		t.Fatalf("editor create: %d %s", status, body)
	}
	url := f.base + "/virtual-inputs/" + input.ID + "/apply"
	payload := map[string]any{"revision": input.Revision, "operation_id": "apply"}
	status, _ = call(t, http.MethodPost, url, editor, payload, nil)
	if status != 403 {
		t.Fatalf("configure-only actor applied input: %d", status)
	}
	status, body = call(t, http.MethodPost, url, publisher, payload, &input)
	if status != 200 || string(input.State.Value) != "100" {
		t.Fatalf("explicit publisher could not apply: %d %s", status, body)
	}
	status, _ = call(t, http.MethodPost, f.base+"/virtual-inputs/"+input.ID+"/draft", publisher, map[string]any{"revision": input.Revision, "operation_id": "draft", "definition": virtualCapacity("150")}, nil)
	if status != 403 {
		t.Fatalf("apply-only actor edited definition: %d", status)
	}
}

func TestVirtualInputHTTPDraftApplyDoesNotWriteDevices(t *testing.T) {
	f := newPointConfigFixture(t)
	bus := f.createBus(t, "canary")
	f.createPoint(t, bus, ModbusIOInput{Description: "read-only canary", ServerID: 1, Address: 10, IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", ReadOnly: true, Scale: 1})
	f.cycle(t)
	var result inputResponse
	status, body := call(t, http.MethodPost, f.base+"/virtual-inputs", f.token, map[string]any{
		"parent": snapshotRoot(t, f.base, f.token), "name": "容量上限", "source": "configuration", "definition": virtualCapacity("100"), "operation_id": "create-capacity",
	}, &result)
	if status != http.StatusOK {
		t.Fatalf("create virtual input: %d %s", status, body)
	}
	if result.ID == "" || result.State.Value != nil || result.State.Quality == "good" {
		t.Fatalf("creation fabricated active value: %+v", result)
	}
	id := result.ID
	mutate := func(path, operation string, definition any) {
		t.Helper()
		payload := map[string]any{"revision": result.Revision, "operation_id": operation}
		if definition != nil {
			payload["definition"] = definition
		}
		status, body = call(t, http.MethodPost, f.base+"/virtual-inputs/"+id+"/"+path, f.token, payload, &result)
		if status != 200 {
			t.Fatalf("%s: %d %s", path, status, body)
		}
	}
	mutate("draft", "draft-capacity", virtualCapacity("120"))
	if result.State.Value != nil {
		t.Fatal("draft changed active value")
	}
	mutate("apply", "apply-capacity", nil)
	if string(result.State.Value) != "120" || result.State.Source != "configuration" {
		t.Fatalf("applied state: %+v", result)
	}
	mutate("draft", "next-draft", virtualCapacity("150"))
	status, body = call(t, http.MethodGet, f.base+"/virtual-inputs/"+id, f.token, nil, &result)
	if status != 200 || string(result.State.Value) != "120" {
		t.Fatalf("read active state: %d %s", status, body)
	}
	for _, route := range []string{"value", "manual"} {
		payload := map[string]any{"value": 999}
		if route == "manual" {
			payload = map[string]any{"manual": true}
		}
		status, _ = call(t, http.MethodPost, f.base+"/nodes/"+id+"/"+route, f.token, payload, nil)
		if status != http.StatusForbidden {
			t.Errorf("generic %s accepted: %d", route, status)
		}
	}
	for _, p := range f.node(t, id).Points {
		if p.Type == data.PointTypeValue || p.Type == data.PointTypeValueSet || p.Type == "lastCommand" {
			t.Errorf("virtual input invented physical/command field %s", p.Type)
		}
	}
	f.cycle(t)
	if len(f.bus.written()) != 0 {
		t.Fatal("virtual input dispatched a protocol write")
	}
}
