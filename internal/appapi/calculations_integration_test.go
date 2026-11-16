package appapi

import (
	"fastiot/internal/calculation"
	"github.com/simpleiot/simpleiot/data"
	"net/http"
	"strings"
	"testing"
)

type calcResponse struct {
	ID       string                  `json:"id"`
	Revision uint64                  `json:"revision"`
	Version  uint64                  `json:"version"`
	Applied  *calculation.Definition `json:"applied"`
	State    struct {
		Quality string   `json:"quality"`
		Value   *float64 `json:"value"`
	} `json:"state"`
}

func TestCalculationApplyHasIndependentPermission(t *testing.T) {
	f := newPointConfigFixture(t)
	root := snapshotRoot(t, f.base, f.token)
	editor := inputAccount(t, f, root, "implementer", []string{"configure"})
	publisher := inputAccount(t, f, root, "viewer", []string{"calculation_apply"})
	bus := f.createBus(t, "permission-source")
	source := f.createPoint(t, bus, ModbusIOInput{Description: "P", ServerID: 1, Address: 10, IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1, ReadOnly: true, Units: "kW"})
	def := calculation.Definition{Operator: "add", Inputs: []string{source, source}, Kind: "number", Unit: "kW"}
	var out calcResponse
	status, body := call(t, http.MethodPost, f.base+"/calculations", editor, map[string]any{"parent": root, "name": "double-P", "definition": def, "operation_id": "create"}, &out)
	if status != 200 {
		t.Fatalf("editor create: %d %s", status, body)
	}
	payload := map[string]any{"revision": out.Revision, "operation_id": "apply"}
	status, _ = call(t, http.MethodPost, f.base+"/calculations/"+out.ID+"/apply", editor, payload, nil)
	if status != 403 {
		t.Fatalf("configure-only actor applied formula: %d", status)
	}
	status, body = call(t, http.MethodPost, f.base+"/calculations/"+out.ID+"/apply", publisher, payload, &out)
	if status != 200 || out.Version != 1 {
		t.Fatalf("publisher apply: %d %s", status, body)
	}
	status, _ = call(t, http.MethodPost, f.base+"/calculations/"+out.ID+"/draft", publisher, map[string]any{"revision": out.Revision, "operation_id": "draft", "definition": def}, nil)
	if status != 403 {
		t.Fatalf("apply-only actor edited formula: %d", status)
	}
}

func TestCalculationProjectionHidesRestrictedInputs(t *testing.T) {
	f := newPointConfigFixture(t)
	root := snapshotRoot(t, f.base, f.token)
	bus := f.createBus(t, "private-source")
	source := f.createPoint(t, bus, ModbusIOInput{Description: "private-reading", ServerID: 1, Address: 10, IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1, ReadOnly: true, Units: "kW"})
	var out calcResponse
	status, body := call(t, http.MethodPost, f.base+"/calculations", f.token, map[string]any{"parent": root, "name": "calculation", "definition": calculation.Definition{Operator: "add", Inputs: []string{source, source}, Kind: "number", Unit: "kW"}, "operation_id": "create"}, &out)
	if status != 200 {
		t.Fatalf("create: %d %s", status, body)
	}
	limited := inputAccount(t, f, out.ID, "viewer", nil)
	status, body = call(t, http.MethodGet, f.base+"/calculations/"+out.ID, limited, nil, nil)
	if status != 200 || strings.Contains(body, source) {
		t.Fatalf("dedicated read exposed restricted input: %d %s", status, body)
	}
	for _, node := range snapshotNodes(t, f.base, limited) {
		if node.ID == out.ID {
			for _, p := range node.Points {
				if p.Type != data.PointTypeDescription {
					t.Fatalf("public snapshot exposed private calculation point %s", p.Type)
				}
			}
		}
	}
}

func TestCalculationHTTPDraftApplyAndReadOnly(t *testing.T) {
	f := newPointConfigFixture(t)
	root := snapshotRoot(t, f.base, f.token)
	bus := f.createBus(t, "isolated")
	a := f.createPoint(t, bus, ModbusIOInput{Description: "A", ServerID: 1, Address: 10, IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1, ReadOnly: true, Units: "kW"})
	b := f.createPoint(t, bus, ModbusIOInput{Description: "B", ServerID: 2, Address: 10, IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1, ReadOnly: true, Units: "kW"})
	def := calculation.Definition{Operator: "add", Inputs: []string{a, b}, Kind: "number", Unit: "kW"}
	var result calcResponse
	status, body := call(t, http.MethodPost, f.base+"/calculations", f.token, map[string]any{"parent": root, "name": "总功率", "definition": def, "operation_id": "new-total"}, &result)
	if status != 200 {
		t.Fatalf("create calculation: %d %s", status, body)
	}
	if result.ID == "" || result.Applied != nil || result.Version != 0 || result.State.Value != nil {
		t.Fatalf("creation fabricated active result: %+v", result)
	}
	id := result.ID
	status, body = call(t, http.MethodPost, f.base+"/calculations/"+id+"/apply", f.token, map[string]any{"revision": result.Revision, "operation_id": "apply-total"}, &result)
	if status != 200 || result.Version != 1 || result.Applied == nil {
		t.Fatalf("apply: %d %s", status, body)
	}
	appliedRevision := result.Revision
	changed := def
	changed.Operator = "divide"
	changed.Unit = "1"
	status, body = call(t, http.MethodPost, f.base+"/calculations/"+id+"/draft", f.token, map[string]any{"revision": result.Revision, "operation_id": "draft-ratio", "definition": changed}, &result)
	if status != 200 || result.Version != 1 || result.Applied.Operator != "add" {
		t.Fatalf("draft altered active formula: %d %s", status, body)
	}
	status, _ = call(t, http.MethodPost, f.base+"/calculations/"+id+"/apply", f.token, map[string]any{"revision": appliedRevision, "operation_id": "stale-apply"}, nil)
	if status != 409 {
		t.Fatalf("stale apply accepted: %d", status)
	}
	wrong := def
	wrong.Unit = "kWh"
	status, body = call(t, http.MethodPost, f.base+"/calculations/"+id+"/draft", f.token, map[string]any{"revision": result.Revision, "operation_id": "bad-unit-draft", "definition": wrong}, &result)
	if status != 200 {
		t.Fatalf("save draft for diagnosis: %d %s", status, body)
	}
	status, _ = call(t, http.MethodPost, f.base+"/calculations/"+id+"/apply", f.token, map[string]any{"revision": result.Revision, "operation_id": "bad-unit-apply"}, nil)
	if status != 400 {
		t.Fatalf("unit-invalid formula applied: %d", status)
	}
	status, body = call(t, http.MethodGet, f.base+"/calculations/"+id, f.token, nil, &result)
	if status != 200 || result.Version != 1 || result.Applied.Unit != "kW" || result.Applied.Operator != "add" {
		t.Fatalf("rejected application altered active formula: %d %s", status, body)
	}
	status, _ = call(t, http.MethodPost, f.base+"/nodes/"+id+"/value", f.token, map[string]any{"value": 999}, nil)
	if status != 403 {
		t.Fatalf("calculation accepted direct value write: %d", status)
	}
	status, _ = call(t, http.MethodPost, f.base+"/nodes/"+id+"/manual", f.token, map[string]any{"manual": true}, nil)
	if status != 403 {
		t.Fatalf("calculation accepted manual control: %d", status)
	}
	for _, p := range f.node(t, id).Points {
		if p.Type == data.PointTypeValue || p.Type == data.PointTypeValueSet || p.Type == "lastCommand" {
			t.Errorf("invented physical/command field %s", p.Type)
		}
	}
	f.cycle(t)
	if len(f.bus.written()) != 0 {
		t.Fatal("calculation configuration wrote a device")
	}
}
