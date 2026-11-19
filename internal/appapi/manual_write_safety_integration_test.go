package appapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"fastiot/internal/ems/runtime"
	"fastiot/internal/ems/safety"
	"fastiot/internal/emsapi"
	siotclient "github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

// A manual point write must obey the same safety envelope as automatic
// commands: the clamped value is what reaches valueSet and the response.
func TestManualPointWriteClampsThroughSafetyEnvelope(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, 15*time.Second, 5*time.Second, 100)
	defer service.Close()
	h.SetEMS(emsapi.New(service, 15*time.Second))
	fixture := &pointConfigFixture{base: base, token: token}
	busID := fixture.createBusPeriod(t, "manual clamp bus", 50)
	pointID := fixture.createPoint(t, busID, ModbusIOInput{
		Description: "power", ServerID: 1, Address: 10,
		IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1,
	})
	min, max := 0.0, 60.0
	encoded, _ := json.Marshal(safety.Config{Min: &min, Max: &max})
	if err := siotclient.SendNodePoint(h.nc, pointID, data.NewPointString("easyemsSafety", "", string(encoded)), true); err != nil {
		t.Fatal(err)
	}

	var result CommandResult
	status, body := call(t, http.MethodPost, base+"/nodes/"+pointID+"/value", token,
		map[string]any{"value": 90, "request_id": data.NewID()}, &result)
	if status != http.StatusOK {
		t.Fatalf("clamped manual write: %d %s", status, body)
	}
	if result.Target != 60 {
		t.Fatalf("response target = %v, want clamped 60: %s", result.Target, body)
	}
	node := fixture.node(t, pointID)
	value, ok := node.Points.Value(data.PointTypeValueSet, "")
	if !ok || value != 60 {
		t.Fatalf("valueSet = %v (present %v), want clamped 60", value, ok)
	}
}

// A manual point write that violates a safety condition must not reach
// valueSet at all, and must explain why.
func TestManualPointWriteRefusesWhenSafetyConditionFails(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, 15*time.Second, 5*time.Second, 100)
	defer service.Close()
	h.SetEMS(emsapi.New(service, 15*time.Second))
	fixture := &pointConfigFixture{base: base, token: token}
	busID := fixture.createBusPeriod(t, "manual refuse bus", 50)
	pointID := fixture.createPoint(t, busID, ModbusIOInput{
		Description: "power", ServerID: 1, Address: 10,
		IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1,
	})
	min, max := 0.0, 60.0
	encoded, _ := json.Marshal(safety.Config{
		Min: &min, Max: &max,
		Conditions: []safety.ConditionConfig{{PointID: "unobserved-soc", Operator: "ge", Value: 10}},
	})
	if err := siotclient.SendNodePoint(h.nc, pointID, data.NewPointString("easyemsSafety", "", string(encoded)), true); err != nil {
		t.Fatal(err)
	}

	status, body := call(t, http.MethodPost, base+"/nodes/"+pointID+"/value", token,
		map[string]any{"value": 30, "request_id": data.NewID()}, nil)
	if status != http.StatusConflict {
		t.Fatalf("refused manual write = %d %s, want 409", status, body)
	}
	if !strings.Contains(body, "unobserved-soc") {
		t.Fatalf("refusal must name the blocking input: %s", body)
	}
	if _, ok := fixture.node(t, pointID).Points.Find(data.PointTypeValueSet, ""); ok {
		t.Fatal("valueSet was written despite safety refusal")
	}
}
