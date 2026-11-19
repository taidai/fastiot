package appapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/audit"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/ems/safety"
	"fastiot/internal/emsapi"
	"fastiot/internal/genscreen"
	siotclient "github.com/simpleiot/simpleiot/client"
)

type screenPreview struct {
	ID         string            `json:"id"`
	Capability string            `json:"capability"`
	FrameURL   string            `json:"frame_url"`
	Points     []genscreen.Point `json:"points"`
	Report     genscreen.Report  `json:"report"`
}

// createActionPreview starts a preview whose model is the given points and
// whose only interaction is one write button per point.
func createActionPreview(t *testing.T, base, token, nodeID string, pointIDs []string) screenPreview {
	t.Helper()
	var source strings.Builder
	source.WriteString("<html><head></head><body>\n")
	for _, id := range pointIDs {
		source.WriteString(`<button data-fastiot-write="` + id + `" data-fastiot-value="1">写</button>` + "\n")
	}
	source.WriteString("</body></html>")
	var preview screenPreview
	status, body := call(t, http.MethodPost, base+"/generated-screens/preview", token, map[string]any{
		"node_id": nodeID, "source": source.String(), "point_ids": pointIDs,
	}, &preview)
	if status != http.StatusOK || preview.ID == "" {
		t.Fatalf("preview = %d %s", status, body)
	}
	return preview
}

// screenActionWrite posts one generated-page operation through the host
// channel, exactly as the preview page does on the frame's behalf.
func screenActionWrite(t *testing.T, base, token, id, pointID string, value float64) (int, string) {
	t.Helper()
	return call(t, http.MethodPost, base+"/generated-screens/"+id+"/write", token, map[string]any{
		"point_id": pointID, "value": value,
	}, nil)
}

func readAuditRecords(t *testing.T, path string) []audit.Record {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	records := []audit.Record{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record audit.Record
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("audit line %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

func screenWriteAudits(records []audit.Record, actor, page, point string) []audit.Record {
	found := []audit.Record{}
	for _, record := range records {
		if record.Action != "控制写入" || record.Actor != actor {
			continue
		}
		if strings.Contains(record.Object, page) && strings.Contains(record.Object, point) {
			found = append(found, record)
		}
	}
	return found
}

// A generated page can only operate what the platform allows: the host channel
// requires the account's write capability even though the page itself is
// merely being previewed, refuses a page operation that arrives without the
// platform session, and records both the refusal and the accepted decision
// with the page and the point.
func TestGeneratedScreenWriteRequiresCapabilityAndAuditsDecision(t *testing.T) {
	base, admin, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	h.SetAudit(audit.New(auditPath))
	root := snapshotRoot(t, base, admin)
	pointID := createNodeAPI(t, base, admin, "variable", root, "水箱液位", 42.5)

	// Configure lets an account build and preview a screen, but it is not the
	// write capability, so the page's write must be refused.
	email, password := "screen-configurer@fixture.local", data.NewID()
	status, body := call(t, http.MethodPost, base+"/users", admin, map[string]any{
		"parent": root, "email": email, "password": password,
		"role": "viewer", "actions": []string{ActionConfigure},
	}, nil)
	if status != http.StatusOK {
		t.Fatalf("create configurer: %d %s", status, body)
	}
	configurer := loginAPI(t, base, email, password)
	preview := createActionPreview(t, base, configurer, root, []string{pointID})

	// The sandboxed frame never holds the platform session, so a request that
	// carries only the frame's own data cannot reach the channel at all.
	status, body = screenActionWrite(t, base, "", preview.ID, pointID, 1)
	if status != http.StatusUnauthorized {
		t.Fatalf("write without platform session = %d %s", status, body)
	}
	status, body = screenActionWrite(t, base, configurer, preview.ID, pointID, 1)
	if status != http.StatusForbidden {
		t.Fatalf("write without write_value capability = %d %s", status, body)
	}
	refusals := screenWriteAudits(readAuditRecords(t, auditPath), email, preview.ID, pointID)
	if len(refusals) != 1 || refusals[0].Result != "failure" ||
		!strings.Contains(refusals[0].Detail, "write_value") {
		t.Fatalf("capability refusal not audited: %+v", refusals)
	}

	// The same operation from an account that holds the capability is
	// executed and audited as an allowed decision.
	granted := createActionPreview(t, base, admin, root, []string{pointID})
	var written struct {
		Status string               `json:"status"`
		Entry  genscreen.WriteEntry `json:"entry"`
	}
	status, body = call(t, http.MethodPost, base+"/generated-screens/"+granted.ID+"/write", admin,
		map[string]any{"point_id": pointID, "value": 7}, &written)
	if status != http.StatusOK || written.Status != genscreen.WriteSimulated || written.Entry.Value != 7 {
		t.Fatalf("allowed write = %d %s", status, body)
	}
	accepted := screenWriteAudits(readAuditRecords(t, auditPath), "admin", granted.ID, pointID)
	if len(accepted) != 1 || accepted[0].Result != "success" || !strings.Contains(accepted[0].Detail, "allowed") {
		t.Fatalf("allowed write not audited: %+v", accepted)
	}
}

// The target scope is judged again when the page operates: a point that has
// moved out of the account's scope is refused even though the live preview
// still lists it, and the refusal names that reason.
func TestGeneratedScreenWriteRefusesPointOutsideAccountScope(t *testing.T) {
	base, admin, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	h.SetAudit(audit.New(auditPath))
	root := snapshotRoot(t, base, admin)
	groupID := createNodeAPI(t, base, admin, "group", root, "泵房", 0)
	pointID := createNodeAPI(t, base, admin, "variable", groupID, "水箱液位", 1)

	email, password := "screen-implementer@fixture.local", data.NewID()
	status, body := call(t, http.MethodPost, base+"/users", admin, map[string]any{
		"parent": groupID, "email": email, "password": password, "role": "implementer",
	}, nil)
	if status != http.StatusOK {
		t.Fatalf("create implementer: %d %s", status, body)
	}
	implementer := loginAPI(t, base, email, password)
	preview := createActionPreview(t, base, implementer, groupID, []string{pointID})

	status, body = call(t, http.MethodPost, base+"/nodes/"+pointID+"/move", admin, map[string]any{
		"old_parent": groupID, "new_parent": root,
	}, nil)
	if status != http.StatusOK {
		t.Fatalf("move point out of the account's scope: %d %s", status, body)
	}

	status, body = screenActionWrite(t, base, implementer, preview.ID, pointID, 1)
	if status != http.StatusForbidden {
		t.Fatalf("write outside the account's scope = %d %s", status, body)
	}
	refusals := screenWriteAudits(readAuditRecords(t, auditPath), email, preview.ID, pointID)
	if len(refusals) != 1 || refusals[0].Result != "failure" || !strings.Contains(refusals[0].Detail, "point_out_of_scope") {
		t.Fatalf("scope refusal not audited: %+v", refusals)
	}
}

// A page operation passes the same safety verdict as an operator write: the
// clamped value is what the preview device receives.
func TestGeneratedScreenWriteIsClampedBySafetyEnvelope(t *testing.T) {
	fixture, pointID := screenSafetyFixture(t, safety.Config{})
	root := snapshotRoot(t, fixture.base, fixture.token)
	preview := createActionPreview(t, fixture.base, fixture.token, root, []string{pointID})

	var written struct {
		Status string               `json:"status"`
		Entry  genscreen.WriteEntry `json:"entry"`
	}
	status, body := call(t, http.MethodPost, fixture.base+"/generated-screens/"+preview.ID+"/write", fixture.token,
		map[string]any{"point_id": pointID, "value": 90}, &written)
	if status != http.StatusOK || written.Status != genscreen.WriteSimulated {
		t.Fatalf("clamped write = %d %s", status, body)
	}
	if written.Entry.Value != 60 {
		t.Fatalf("write value = %v, want the clamped 60: %s", written.Entry.Value, body)
	}
}

// A safety verdict of refused stops the page operation and leaves the reason
// in the audit record.
func TestGeneratedScreenWriteRefusalIsAuditedWithReason(t *testing.T) {
	fixture, pointID := screenSafetyFixture(t, safety.Config{
		Conditions: []safety.ConditionConfig{{PointID: "unobserved-soc", Operator: "ge", Value: 10}},
	})
	root := snapshotRoot(t, fixture.base, fixture.token)
	preview := createActionPreview(t, fixture.base, fixture.token, root, []string{pointID})
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	fixture.handler.SetAudit(audit.New(auditPath))

	status, body := screenActionWrite(t, fixture.base, fixture.token, preview.ID, pointID, 30)
	if status != http.StatusConflict || !strings.Contains(body, "unobserved-soc") {
		t.Fatalf("unsafe page write = %d %s, want 409 naming the blocking input", status, body)
	}
	refusals := screenWriteAudits(readAuditRecords(t, auditPath), "admin", preview.ID, pointID)
	if len(refusals) != 1 || refusals[0].Result != "failure" || !strings.Contains(refusals[0].Detail, "unobserved-soc") {
		t.Fatalf("safety refusal not audited with its reason: %+v", refusals)
	}
}

// screenSafetySetup is the running server whose point carries an envelope.
type screenSafetySetup struct {
	*pointConfigFixture
	handler *Handler
}

// screenSafetyFixture builds one writable holding register with a 0..60
// envelope and returns it with the fixture and the running handler.
func screenSafetyFixture(t *testing.T, config safety.Config) (screenSafetySetup, string) {
	t.Helper()
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, 15*time.Second, 5*time.Second, 100)
	t.Cleanup(service.Close)
	h.SetEMS(emsapi.New(service, 15*time.Second))
	fixture := &pointConfigFixture{base: base, token: token}
	busID := fixture.createBusPeriod(t, "screen safety bus", 50)
	pointID := fixture.createPoint(t, busID, ModbusIOInput{
		Description: "power", ServerID: 1, Address: 10,
		IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1,
	})
	min, max := 0.0, 60.0
	config.Min, config.Max = &min, &max
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := siotclient.SendNodePoint(h.nc, pointID, data.NewPointString("easyemsSafety", "", string(encoded)), true); err != nil {
		t.Fatal(err)
	}
	return screenSafetySetup{pointConfigFixture: fixture, handler: h}, pointID
}
