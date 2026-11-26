package appapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/ems/bridge"
	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/modbusrt"
	"fastiot/internal/emsapi"
)

type ticket22DeploymentRecord struct {
	ID                   string    `json:"id"`
	ProjectID            string    `json:"project_id"`
	SiteName             string    `json:"site_name"`
	VersionID            string    `json:"version_id"`
	SourceVersion        string    `json:"source_version"`
	SourceFingerprint    string    `json:"source_fingerprint"`
	Actor                string    `json:"actor"`
	RecordedAt           time.Time `json:"recorded_at"`
	Status               string    `json:"status"`
	Reason               string    `json:"reason"`
	ConfigurationApplied bool      `json:"configuration_applied"`
	AcquisitionRestored  bool      `json:"acquisition_restored"`
	ControlAuthorized    bool      `json:"control_authorized"`
	AcquisitionGaps      []string  `json:"acquisition_gaps"`
	ApplyEvidence        struct {
		Kind      string `json:"kind"`
		Reference string `json:"reference"`
	} `json:"apply_evidence"`
	AcquisitionEvidence []struct {
		ChannelID      string `json:"channel_id"`
		DeviceID       string `json:"device_id"`
		Health         string `json:"health"`
		VerificationAt string `json:"verification_at"`
		Level          string `json:"verification_level"`
		Basis          string `json:"verification_basis"`
	} `json:"acquisition_evidence"`
}

func ticket22PublishAndExport(t *testing.T, source *pointConfigFixture, sourceBus string) (string, projectPackage) {
	t.Helper()
	var verification l3Result
	status, body := call(t, http.MethodPost, source.base+"/modbus/"+sourceBus+"/units/1/verify", source.token, map[string]any{"level": "L2"}, &verification)
	if status != http.StatusOK || !verification.Passed {
		t.Fatalf("source acquisition verification: %d %s", status, body)
	}
	site := ""
	for _, node := range snapshotNodes(t, source.base, source.token) {
		if node.ID == sourceBus {
			site = node.Parent
		}
	}
	if site == "" {
		t.Fatal("source channel has no Site")
	}
	var gates preflightReport
	status, body = call(t, http.MethodGet, source.base+"/delivery/gates?site="+site, source.token, nil, &gates)
	if status != http.StatusOK || gates.Status == "fail" {
		t.Fatalf("source gates: %d %s", status, body)
	}
	var published versionSummary
	status, body = call(t, http.MethodPost, source.base+"/projects/publish", source.token, map[string]any{
		"site": site, "expected_fingerprint": gates.Fingerprint,
	}, &published)
	if status != http.StatusOK {
		t.Fatalf("source publish: %d %s", status, body)
	}
	var pkg projectPackage
	status, body = call(t, http.MethodGet, source.base+"/projects/export?site="+site+"&version="+published.ID, source.token, nil, &pkg)
	if status != http.StatusOK {
		t.Fatalf("source export: %d %s", status, body)
	}
	return site, pkg
}

func TestTicket22DeploymentRecordsDerivedFromApplyAndAcquisition(t *testing.T) {
	source, sourceBus, sim := l3Fixture(t)
	source.handler.SetDataDir(source.dir)
	_, pkg := ticket22PublishAndExport(t, source, sourceBus)

	target := newPointConfigFixture(t)
	target.handler.SetDataDir(target.dir)
	target.handler.SetEMS(emsapi.New(target.runtime, time.Second))
	loadSingleDeviceProfile(t, target.handler, l3Profile)
	target.bridge = bridge.New(target.runtime, bridge.NewNodeSource(target.handler.nc), func(protocol, address string, timeout time.Duration) (channel.Bus, error) {
		if protocol != "tcp" || address != "127.0.0.1:18118" {
			return nil, fmt.Errorf("non-fixture address refused")
		}
		return modbusrt.OpenTCP(address, timeout)
	})
	var imported importProjectResult
	status, body := call(t, http.MethodPost, target.base+"/projects/import", target.token, map[string]any{"package": pkg}, &imported)
	if status != http.StatusOK || imported.VersionID == "" {
		t.Fatalf("target import: %d %s", status, body)
	}
	targetBus := ""
	for _, node := range snapshotNodes(t, target.base, target.token) {
		if node.Type == data.NodeTypeModbus {
			targetBus = node.ID
		}
	}
	if targetBus == "" {
		t.Fatal("imported target channel missing")
	}

	// No record may claim success from client fields. Before rebind there is no
	// acquisition evidence, so the server-derived record must be "applied" only.
	forged := map[string]any{
		"site": imported.SiteID, "version": imported.VersionID,
		"status": "success", "configuration_applied": true, "acquisition_restored": true, "control_authorized": true,
	}
	var before ticket22DeploymentRecord
	status, body = call(t, http.MethodPost, target.base+"/delivery/deployments", target.token, forged, &before)
	if status != http.StatusOK {
		t.Fatalf("create pre-acquisition deployment record: %d %s", status, body)
	}
	if before.Status != "applied" || !before.ConfigurationApplied || before.AcquisitionRestored || before.ControlAuthorized {
		t.Fatalf("client-supplied success was not derived: %+v", before)
	}
	if before.VersionID != imported.VersionID || before.SourceVersion != pkg.SourceVersion || before.SourceFingerprint != pkg.SourceFingerprint ||
		before.Actor == "" || before.RecordedAt.IsZero() || before.ApplyEvidence.Kind != "project_import" || before.Reason == "" || len(before.AcquisitionGaps) == 0 {
		t.Fatalf("applied record lacks identity/apply evidence: %+v", before)
	}

	status, body = call(t, http.MethodPut, target.base+"/modbus/"+targetBus, target.token, ModbusConnectionInput{
		Description: "Rebound private simulator", Protocol: "TCP", URI: "127.0.0.1:18118", PollPeriod: 50, Timeout: 100,
	}, nil)
	if status != http.StatusOK {
		t.Fatalf("target rebind: %d %s", status, body)
	}
	if err := target.bridge.Sync(time.Now()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = target.runtime.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, func() bool { return target.runtime.State.Health(targetBus+"/1") == "healthy" }, "target simulator acquisition")
	var verification l3Result
	status, body = call(t, http.MethodPost, target.base+"/modbus/"+targetBus+"/units/1/verify", target.token, map[string]any{"level": "L2"}, &verification)
	if status != http.StatusOK || !verification.Passed {
		t.Fatalf("target acquisition verification: %d %s", status, body)
	}

	// The same immutable version and current instance evidence now produce a
	// "running" record. Control stays unapproved in both records.
	var running ticket22DeploymentRecord
	status, body = call(t, http.MethodPost, target.base+"/delivery/deployments", target.token, map[string]any{
		"site": imported.SiteID, "version": imported.VersionID,
		"status": "failed", "control_authorized": true,
	}, &running)
	if status != http.StatusOK {
		t.Fatalf("create running deployment record: %d %s", status, body)
	}
	if running.Status != "running" || !running.ConfigurationApplied || !running.AcquisitionRestored || running.ControlAuthorized {
		t.Fatalf("acquisition evidence did not derive running state: %+v", running)
	}
	if len(running.AcquisitionEvidence) != 1 || running.AcquisitionEvidence[0].DeviceID != targetBus+"/1" ||
		running.AcquisitionEvidence[0].Health != "healthy" || running.AcquisitionEvidence[0].VerificationAt == "" ||
		running.AcquisitionEvidence[0].Level != "L2" || running.AcquisitionEvidence[0].Basis == "" {
		t.Fatalf("acquisition evidence missing: %+v", running.AcquisitionEvidence)
	}
	if len(running.AcquisitionGaps) != 0 {
		t.Fatalf("unexpected acquisition gaps: %v", running.AcquisitionGaps)
	}

	var listed struct {
		Records []ticket22DeploymentRecord `json:"records"`
	}
	status, body = call(t, http.MethodGet, target.base+"/delivery/deployments?site="+imported.SiteID, target.token, nil, &listed)
	if status != http.StatusOK || len(listed.Records) != 2 {
		t.Fatalf("deployment list is not append-only: %d %s", status, body)
	}
	if listed.Records[0].ID != running.ID || listed.Records[1].ID != before.ID {
		t.Fatalf("deployment list order/identity: %+v", listed.Records)
	}
	var read ticket22DeploymentRecord
	status, body = call(t, http.MethodGet, target.base+"/delivery/deployments/"+running.ID+"?site="+imported.SiteID, target.token, nil, &read)
	if status != http.StatusOK || read.ID != running.ID || read.Status != "running" {
		t.Fatalf("deployment read: %d %s", status, body)
	}

	// A stale/mismatched current configuration is refused with an explanation.
	createNodeAPI(t, target.base, target.token, data.NodeTypeVariable, imported.SiteID, "changed after import", 0)
	status, body = call(t, http.MethodPost, target.base+"/delivery/deployments", target.token, map[string]any{"site": imported.SiteID, "version": imported.VersionID}, nil)
	if status != http.StatusConflict || !strings.Contains(body, "version_fingerprint_mismatch") {
		t.Fatalf("stale version was not refused: %d %s", status, body)
	}
	status, body = call(t, http.MethodPost, target.base+"/delivery/deployments", target.token, map[string]any{"site": imported.SiteID, "version": "pv-missing"}, nil)
	if status != http.StatusNotFound {
		t.Fatalf("unknown version: %d %s", status, body)
	}

	// Deployment records are runtime facts and must not travel in the package.
	var exported projectPackage
	status, body = call(t, http.MethodGet, target.base+"/projects/export?site="+imported.SiteID, target.token, nil, &exported)
	if status != http.StatusOK {
		t.Fatalf("target export: %d %s", status, body)
	}
	if strings.Contains(body, running.ID) || strings.Contains(body, "acquisition_restored") || strings.Contains(body, "configuration_applied") {
		t.Fatal("deployment record leaked into the project package")
	}

	// A restricted account cannot read records outside its own scope.
	otherSite := createNodeAPI(t, target.base, target.token, NodeTypeSite, snapshotRoot(t, target.base, target.token), "Outside deployment scope", 0)
	viewer := inputAccount(t, target, otherSite, "viewer", nil)
	status, body = call(t, http.MethodGet, target.base+"/delivery/deployments?site="+imported.SiteID, viewer, nil, nil)
	if status != http.StatusForbidden {
		t.Fatalf("restricted list leaked deployment scope: %d %s", status, body)
	}
	status, body = call(t, http.MethodGet, target.base+"/delivery/deployments?site="+otherSite, viewer, nil, &listed)
	if status != http.StatusOK || len(listed.Records) != 0 {
		t.Fatalf("restricted list: %d %s", status, body)
	}
	status, body = call(t, http.MethodPost, target.base+"/delivery/deployments", viewer, map[string]any{"site": imported.SiteID, "version": imported.VersionID}, nil)
	if status != http.StatusForbidden {
		t.Fatalf("restricted create: %d %s", status, body)
	}

	if writes := sim.written(); len(writes) != 0 {
		t.Fatalf("deployment recording issued physical writes: %v", writes)
	}
}
