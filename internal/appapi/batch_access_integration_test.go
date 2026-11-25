package appapi

import (
	domainaccess "fastiot/internal/domain/access"
	"fastiot/internal/ems/bridge"
	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/modbusrt"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/emsapi"
	"fastiot/internal/sim/pumphouse"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The reviewed batch contract is opt-in; raw/legacy callers keep their contract.
func TestReviewedBatchPreviewFencesWholeBatchBeforeWrites(t *testing.T) {
	f, bus, device, _ := singleDeviceFixture(t)
	input := map[string]any{"csv": fmt.Sprintf("device,profile,channel,station\n%s,access-sim,%s,1\n", device, bus), "dry_run": true}
	var preview struct {
		Version string `json:"preview_version"`
		Applied int    `json:"applied"`
	}
	status, body := call(t, http.MethodPost, f.base+"/access/batch/reviewed", f.token, input, &preview)
	if status != 200 || preview.Version == "" || preview.Applied != 0 {
		t.Fatalf("reviewed preview: %d %s", status, body)
	}
	var view unitModelView
	call(t, http.MethodGet, f.base+"/modbus/"+bus+"/units/1/model", f.token, nil, &view)
	if view.Model != nil || len(view.Points) != 0 {
		t.Fatal("preview wrote configuration")
	}
	input["dry_run"] = false
	status, body = call(t, http.MethodPost, f.base+"/access/batch/reviewed", f.token, input, nil)
	if status != 409 {
		t.Fatalf("missing review accepted: %d %s", status, body)
	}
	input["preview_version"] = preview.Version
	loadSingleDeviceProfile(t, f.handler, strings.Replace(singleDeviceProfile, `"scale":0.1`, `"scale":0.2`, 1))
	status, body = call(t, http.MethodPost, f.base+"/access/batch/reviewed", f.token, input, nil)
	if status != 409 {
		t.Fatalf("changed Profile accepted: %d %s", status, body)
	}
	call(t, http.MethodGet, f.base+"/modbus/"+bus+"/units/1/model", f.token, nil, &view)
	if view.Model != nil || len(view.Points) != 0 {
		t.Fatal("stale review partially wrote configuration")
	}
}

func TestReviewedBatchReportsAllConflictsAndProfilePlan(t *testing.T) {
	f, bus, device, _ := singleDeviceFixture(t)
	var parent string
	for _, node := range snapshotNodes(t, f.base, f.token) {
		if node.ID == device {
			parent = node.Parent
		}
	}
	second := createNodeAPI(t, f.base, f.token, "device", parent, "second", 0)
	third := createNodeAPI(t, f.base, f.token, "device", parent, "third", 0)
	input := map[string]any{"csv": fmt.Sprintf("device,profile,channel,station\n%s,access-sim,%s,1\n%s,access-sim,%s,1\n%s,access-sim,%s,2\n", device, bus, second, bus, third, bus), "dry_run": true}
	var response struct {
		Version string                    `json:"preview_version"`
		Rows    []domainaccess.BindingRow `json:"rows"`
		Applied int                       `json:"applied"`
		Plans   map[int]struct {
			Match        profileApplyResult `json:"match"`
			CreatePoints []ModbusIOInput    `json:"create_points"`
			Required     []string           `json:"missing_required"`
		} `json:"plans"`
	}
	status, body := call(t, http.MethodPost, f.base+"/access/batch/reviewed", f.token, input, &response)
	if status != 200 || len(response.Rows) != 3 || response.Rows[0].Valid || response.Rows[1].Valid || !response.Rows[2].Valid {
		t.Fatalf("whole-batch conflicts: %d %s", status, body)
	}
	plan := response.Plans[4]
	if len(plan.CreatePoints) != 2 || plan.CreatePoints[0].Address != 100 || len(plan.Required) != 1 || plan.Required[0] != "水箱液位" || len(plan.Match.Missing) != 2 {
		t.Fatalf("missing Profile creation diff: %s", body)
	}
	input["dry_run"], input["preview_version"] = false, response.Version
	status, body = call(t, http.MethodPost, f.base+"/access/batch/reviewed", f.token, input, &response)
	if status != 200 || response.Applied != 1 || response.Rows[2].Status != "mapped" || response.Rows[0].Status != "blocked" {
		t.Fatalf("mixed apply: %d %s", status, body)
	}
	var view unitModelView
	call(t, http.MethodGet, f.base+"/modbus/"+bus+"/units/2/model", f.token, nil, &view)
	if view.Model == nil || view.Model.BusinessNodeID != third || view.Model.Verified || len(view.Model.PointMap) != 2 {
		t.Fatalf("saved model: %+v", view)
	}
	// Replayed apply cannot create a second mapping; fresh preview reports already bound.
	status, body = call(t, http.MethodPost, f.base+"/access/batch/reviewed", f.token, input, nil)
	if status != 409 {
		t.Fatalf("replayed review: %d %s", status, body)
	}
	input["dry_run"] = true
	call(t, http.MethodPost, f.base+"/access/batch/reviewed", f.token, input, &response)
	if response.Rows[2].Valid || !strings.Contains(strings.Join(response.Rows[2].Errors, ";"), "已绑定") {
		t.Fatal("retry hid persisted binding")
	}
}

func TestReviewedBatchCannotRebindDeviceThroughHiddenChannel(t *testing.T) {
	f, bus, device, _ := singleDeviceFixture(t)
	var site string
	for _, node := range snapshotNodes(t, f.base, f.token) {
		if node.ID == device {
			site = node.Parent
		}
	}
	hidden := f.createBus(t, "hidden binding channel")
	csv := fmt.Sprintf("device,profile,channel,station\n%s,access-sim,%s,1\n", device, hidden)
	// Legacy compatibility: its established no-preview contract still works.
	status, body := call(t, http.MethodPost, f.base+"/access/batch", f.token, map[string]any{"csv": csv, "dry_run": false}, nil)
	if status != 200 {
		t.Fatalf("legacy contract: %d %s", status, body)
	}
	scoped := inputAccount(t, f, site, "implementer", []string{"configure"})
	csv = fmt.Sprintf("device,profile,channel,station\n%s,access-sim,%s,1\n", device, bus)
	var response struct {
		Rows    []domainaccess.BindingRow `json:"rows"`
		Version string                    `json:"preview_version"`
		Applied int                       `json:"applied"`
	}
	input := map[string]any{"csv": csv, "dry_run": true}
	status, body = call(t, http.MethodPost, f.base+"/access/batch/reviewed", scoped, input, &response)
	if status != 200 || len(response.Rows) != 1 || response.Rows[0].Valid {
		t.Fatalf("hidden binding duplicated: %d %s", status, body)
	}
	input["dry_run"], input["preview_version"] = false, response.Version
	call(t, http.MethodPost, f.base+"/access/batch/reviewed", scoped, input, &response)
	if response.Applied != 0 {
		t.Fatal("hidden binding overwritten")
	}
	viewer := inputAccount(t, f, site, "viewer", nil)
	status, _ = call(t, http.MethodPost, f.base+"/access/batch/reviewed", viewer, input, nil)
	if status != 403 {
		t.Fatalf("viewer could configure: %d", status)
	}
	// A channel-only editor cannot attach an unseen business Device.
	channelOnly := inputAccount(t, f, bus, "implementer", []string{"configure"})
	input["dry_run"] = true
	response.Rows = nil
	status, body = call(t, http.MethodPost, f.base+"/access/batch/reviewed", channelOnly, input, &response)
	if status != 200 || response.Rows[0].Valid || response.Rows[0].DeviceID != "" {
		t.Fatalf("foreign Device exposed: %d %s", status, body)
	}
}

func TestReviewedBatchInputIsBoundedBeforeAnyWrites(t *testing.T) {
	f, bus, device, _ := singleDeviceFixture(t)
	header := "device,profile,channel,station\n"
	row := fmt.Sprintf("%s,access-sim,%s,1\n", device, bus)
	for _, csv := range []string{
		header + row + strings.Repeat(" ", 256*1024),
		header + strings.Repeat(row, 257),
		header + strings.Repeat("x", 257) + ",access-sim," + bus + ",1\n",
		header + strings.TrimSpace(row) + ",ignored\n",
		"device,device,channel,station\n" + row,
	} {
		status, body := call(t, http.MethodPost, f.base+"/access/batch/reviewed", f.token, map[string]any{"csv": csv, "dry_run": false}, nil)
		if status != 400 {
			t.Fatalf("unbounded/malformed CSV: %d %s", status, body)
		}
	}
	var view unitModelView
	call(t, http.MethodGet, f.base+"/modbus/"+bus+"/units/1/model", f.token, nil, &view)
	if view.Model != nil || len(view.Points) != 0 {
		t.Fatal("invalid batch wrote points")
	}
}

func TestReviewedBatchReadOnlyEvidenceAndRestart(t *testing.T) {
	simulator, err := pumphouse.New(pumphouse.DefaultConfig()).ServeTCP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer simulator.Close()
	dir, password := t.TempDir(), "isolated-batch-restart"
	writes := new(atomic.Int32)
	start := func() (*pointConfigFixture, func()) {
		base, token, h, stop := startInputServer(t, dir, password)
		service := runtime.NewService(nil, time.Minute, time.Second, 100)
		t.Cleanup(service.Close)
		h.SetEMS(emsapi.New(service, time.Minute))
		loadSingleDeviceProfile(t, h, singleDeviceProfile)
		f := &pointConfigFixture{base: base, token: token, handler: h, runtime: service}
		f.bridge = bridge.New(service, bridge.NewNodeSource(h.nc), func(protocol, address string, timeout time.Duration) (channel.Bus, error) {
			if protocol != "tcp" || address != simulator.Addr() {
				return nil, fmt.Errorf("non-fixture connection refused")
			}
			bus, err := modbusrt.OpenTCP(address, timeout)
			if err != nil {
				return nil, err
			}
			return &accessReadOnlyBus{bus, writes}, nil
		})
		return f, stop
	}
	f, stop := start()
	site := createNodeAPI(t, f.base, f.token, "site", snapshotRoot(t, f.base, f.token), "batch restart", 0)
	first := createNodeAPI(t, f.base, f.token, "device", site, "online", 0)
	second := createNodeAPI(t, f.base, f.token, "device", site, "absent", 0)
	var created struct {
		ID string `json:"id"`
	}
	status, body := call(t, http.MethodPost, f.base+"/modbus", f.token, ModbusConnectionInput{Parent: site, Description: "private simulator", Protocol: "TCP", URI: simulator.Addr(), PollPeriod: 100, Timeout: 200}, &created)
	if status != 200 {
		t.Fatal(body)
	}
	bus := created.ID
	input := map[string]any{"csv": fmt.Sprintf("device,profile,channel,station\n%s,access-sim,%s,1\n%s,access-sim,%s,2\n", first, bus, second, bus), "dry_run": true}
	var response struct {
		Version string `json:"preview_version"`
		Applied int    `json:"applied"`
	}
	call(t, http.MethodPost, f.base+"/access/batch/reviewed", f.token, input, &response)
	input["dry_run"], input["preview_version"] = false, response.Version
	status, body = call(t, http.MethodPost, f.base+"/access/batch/reviewed", f.token, input, &response)
	if status != 200 || response.Applied != 2 {
		t.Fatalf("batch apply: %d %s", status, body)
	}
	f.cycle(t)
	waitAccessSample(t, f, bus, "水箱液位", "good")
	for station := 1; station <= 2; station++ {
		for _, level := range []string{"L1", "L2"} {
			var result struct {
				Passed bool `json:"passed"`
			}
			status, body = call(t, http.MethodPost, fmt.Sprintf("%s/modbus/%s/units/%d/verify", f.base, bus, station), f.token, map[string]any{"level": level}, &result)
			if status != 200 || result.Passed != (station == 1) {
				t.Fatalf("station %d %s: %d %s", station, level, status, body)
			}
		}
	}
	var before unitModelView
	call(t, http.MethodGet, f.base+"/modbus/"+bus+"/units/1/model", f.token, nil, &before)
	stop()
	f, _ = start()
	var after unitModelView
	call(t, http.MethodGet, f.base+"/modbus/"+bus+"/units/1/model", f.token, nil, &after)
	if after.Model == nil || after.Model.BusinessNodeID != first || after.Model.PointMap["水箱液位"] != before.Model.PointMap["水箱液位"] {
		t.Fatal("restart lost batch mapping")
	}
	for station := 1; station <= 2; station++ {
		var history struct {
			Records []struct {
				Current, Passed  bool
				Actor, At, Level string
			} `json:"records"`
		}
		status, body = call(t, http.MethodGet, fmt.Sprintf("%s/modbus/%s/units/%d/verify", f.base, bus, station), f.token, nil, &history)
		if status != 200 || len(history.Records) != 2 {
			t.Fatalf("reload evidence: %d %s", status, body)
		}
		for _, record := range history.Records {
			if !record.Current || record.Passed != (station == 1) || record.Actor == "" || record.At == "" {
				t.Fatalf("untrustworthy per-device evidence: %s", body)
			}
		}
	}
	delete(after.Model.PointMap, "水箱液位")
	status, body = call(t, http.MethodPut, f.base+"/modbus/"+bus+"/units/1/model", f.token, after.Model, nil)
	if status != 200 {
		t.Fatal(body)
	}
	var verified struct {
		Passed bool `json:"passed"`
	}
	status, body = call(t, http.MethodPost, f.base+"/modbus/"+bus+"/units/1/verify", f.token, map[string]any{"level": "L2"}, &verified)
	if status != 200 || verified.Passed || !strings.Contains(body, "必需点未映射") {
		t.Fatalf("missing required point: %d %s", status, body)
	}
	simulator.Close()
	status, body = call(t, http.MethodPost, f.base+"/modbus/"+bus+"/units/1/verify", f.token, map[string]any{"level": "L1"}, &verified)
	if status != 200 || verified.Passed {
		t.Fatalf("offline: %d %s", status, body)
	}
	if writes.Load() != 0 {
		t.Fatal("batch verification attempted physical writes")
	}
}
