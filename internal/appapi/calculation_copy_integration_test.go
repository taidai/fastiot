package appapi

import (
	"fastiot/internal/ems/runtime"
	"fastiot/internal/emsapi"
	"net/http"
	"testing"
	"time"

	"fastiot/internal/calculation"
	"github.com/simpleiot/simpleiot/data"
)

func TestCalculationProjectCopyRemapsDraftReferences(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	h.SetEMS(emsapi.New(service, time.Minute))
	root := snapshotRoot(t, base, token)
	site, err := CreateDomainNode(h.nc, root, NodeTypeSite, map[string]string{"description": "calculation project"})
	if err != nil {
		t.Fatal(err)
	}
	parent := createNodeAPI(t, base, token, "group", site, "calculation points", 0)
	var bus struct {
		ID string `json:"id"`
	}
	status, body := call(t, http.MethodPost, base+"/modbus", token, ModbusConnectionInput{Parent: site, Description: "source", Protocol: data.PointValueTCP, URI: "127.0.0.1:1502", PollPeriod: 1000, Timeout: 100}, &bus)
	if status != 200 {
		t.Fatal(body)
	}
	f := &pointConfigFixture{base: base, token: token}
	point := func(name string, address int) string {
		return f.createPoint(t, bus.ID, ModbusIOInput{Description: name, ServerID: 1, Address: address, IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", ReadOnly: true, Scale: 1, Units: "kW"})
	}
	a, b, capacity := point("A", 10), point("B", 20), point("capacity", 30)
	create := func(name string, def calculation.Definition) string {
		t.Helper()
		var out calcResponse
		status, body := call(t, http.MethodPost, base+"/calculations", token, map[string]any{"parent": parent, "name": name, "definition": def, "operation_id": name}, &out)
		if status != 200 {
			t.Fatal(body)
		}
		status, body = call(t, http.MethodPost, base+"/calculations/"+out.ID+"/apply", token, map[string]any{"revision": out.Revision, "operation_id": "apply-" + name}, &out)
		if status != 200 {
			t.Fatal(body)
		}
		return out.ID
	}
	total := create("total", calculation.Definition{Operator: "add", Inputs: []string{a, b}, Kind: "number", Unit: "kW"})
	ratio := create("ratio", calculation.Definition{Operator: "divide", Inputs: []string{total, capacity}, Kind: "number", Unit: "1"})
	var outside inputResponse
	definition := virtualCapacity("40")
	definition.Unit = "kW"
	status, body = call(t, http.MethodPost, base+"/virtual-inputs", token, map[string]any{"parent": root, "name": "outside-source", "source": "configuration", "definition": definition, "operation_id": "outside"}, &outside)
	if status != 200 {
		t.Fatal(body)
	}
	status, body = call(t, http.MethodPost, base+"/virtual-inputs/"+outside.ID+"/apply", token, map[string]any{"revision": outside.Revision, "operation_id": "outside-apply"}, &outside)
	if status != 200 {
		t.Fatal(body)
	}
	create("external", calculation.Definition{Operator: "add", Inputs: []string{a, outside.ID}, Kind: "number", Unit: "kW"})
	var version versionSummary
	status, body = call(t, http.MethodPost, base+"/projects/publish", token, publishRequest{Site: site, Note: "isolated configuration fixture, not hardware acceptance", Confirm: true}, &version)
	if status != 200 {
		t.Fatalf("publish fixture configuration: %d %s", status, body)
	}
	var cloned reuseResult
	status, body = call(t, http.MethodPost, base+"/projects/reuse", token, reuseRequest{Source: site, Name: "copy"}, &cloned)
	if status != 200 {
		t.Fatalf("copy: %d %s", status, body)
	}
	var listed struct {
		Calculations []struct {
			ID      string                  `json:"id"`
			Parent  string                  `json:"parent"`
			Name    string                  `json:"name"`
			Version uint64                  `json:"version"`
			Draft   *calculation.Definition `json:"draft"`
			Applied *calculation.Definition `json:"applied"`
		} `json:"calculations"`
	}
	status, body = call(t, http.MethodGet, base+"/calculations", token, nil, &listed)
	if status != 200 {
		t.Fatal(body)
	}
	copiedParent := ""
	for _, node := range snapshotNodes(t, base, token) {
		if node.Parent == cloned.SiteID && node.Type == data.NodeTypeGroup && node.Desc() == "calculation points" {
			copiedParent = node.ID
		}
	}
	if copiedParent == "" {
		t.Fatal("missing copied calculation container")
	}
	old := map[string]bool{a: true, b: true, capacity: true, total: true, ratio: true}
	copied := 0
	for _, item := range listed.Calculations {
		if item.Parent != copiedParent {
			continue
		}
		copied++
		if item.Applied != nil || item.Version != 0 || item.Draft == nil {
			t.Fatalf("copied formula remained active: %+v", item)
		}
		if item.Name == "external" {
			if item.Draft.Inputs[1] != "" {
				t.Fatal("external source identity survived copy")
			}
			status, _ := call(t, http.MethodPost, base+"/calculations/"+item.ID+"/apply", token, map[string]any{"revision": uint64(1), "operation_id": "unbound-apply"}, nil)
			if status != 400 {
				t.Fatalf("unbound copy activated: %d", status)
			}
			continue
		}
		for _, ref := range item.Draft.Inputs {
			if ref == "" || old[ref] {
				t.Fatalf("internal copied reference was not remapped: %q", ref)
			}
		}
	}
	if copied != 3 {
		t.Fatalf("copied %d calculation points", copied)
	}
	status, body = call(t, http.MethodPatch, base+"/nodes/"+parent, token, map[string]any{"description": "keep this metadata"}, nil)
	if status != 200 {
		t.Fatal(body)
	}
	status, body = call(t, http.MethodPost, base+"/projects/rollback", token, rollbackRequest{Site: site, Version: version.ID}, nil)
	if status != 409 {
		t.Fatalf("unsupported calculation rollback accepted: %d %s", status, body)
	}
	if f.node(t, parent).Desc() != "keep this metadata" {
		t.Fatal("rollback wrote unrelated configuration before rejecting")
	}
}
