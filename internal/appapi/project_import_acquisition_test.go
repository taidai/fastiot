package appapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/simpleiot/simpleiot/data"

	domainaccess "fastiot/internal/domain/access"
	"fastiot/internal/ems/bridge"
	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/modbusrt"
	"fastiot/internal/emsapi"
)

func TestTicket21RebindThenSimulatorAcquisitionEvidence(t *testing.T) {
	source, sourceBus, sim := l3Fixture(t)
	source.handler.SetDataDir(source.dir)
	var sourceVerification l3Result
	status, body := call(t, http.MethodPost, source.base+"/modbus/"+sourceBus+"/units/1/verify", source.token, map[string]any{"level": "L2"}, &sourceVerification)
	if status != http.StatusOK || !sourceVerification.Passed {
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
	for _, node := range pkg.Nodes {
		for _, point := range node.Points {
			if point.Type == domainaccess.VerificationPoint {
				t.Fatal("published package retained old verification")
			}
		}
	}

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
	status, body = call(t, http.MethodPost, target.base+"/projects/import", target.token, map[string]any{"package": pkg}, &imported)
	if status != http.StatusOK || imported.Evidence.SourceFingerprint != pkg.SourceFingerprint {
		t.Fatalf("target import: %d %s", status, body)
	}
	targetBus := ""
	for _, node := range snapshotNodes(t, target.base, target.token) {
		if node.ID == sourceBus || node.Type != data.NodeTypeModbus {
			continue
		}
		targetBus = node.ID
		if _, exists := node.Points.Find(data.PointTypeURI, ""); exists {
			t.Fatal("import restored the source address")
		}
	}
	if targetBus == "" {
		t.Fatal("imported target channel missing")
	}
	var oldHistory struct {
		Records []domainaccess.VerificationRecord `json:"records"`
	}
	status, body = call(t, http.MethodGet, target.base+"/modbus/"+targetBus+"/units/1/verify", target.token, nil, &oldHistory)
	if status != http.StatusOK || len(oldHistory.Records) != 0 {
		t.Fatalf("source verification was inherited: %d %s", status, body)
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

	var verified struct {
		Passed   bool                            `json:"passed"`
		Recorded bool                            `json:"recorded"`
		Reasons  []string                        `json:"reasons"`
		Record   domainaccess.VerificationRecord `json:"record"`
	}
	status, body = call(t, http.MethodPost, target.base+"/modbus/"+targetBus+"/units/1/verify", target.token, map[string]any{"level": "L2"}, &verified)
	if status != http.StatusOK || !verified.Passed || !verified.Recorded {
		t.Fatalf("target acquisition verification: %d %s", status, body)
	}
	record := verified.Record
	if record.At == "" || record.Actor == "" || !record.Current || record.Basis == "" ||
		record.ProfileVersion == "" || record.MappingVersion == "" || record.DeviceID != targetBus+"/1" {
		t.Fatalf("target verification evidence is incomplete: %s", body)
	}
	var history struct {
		Records []domainaccess.VerificationRecord `json:"records"`
	}
	status, body = call(t, http.MethodGet, target.base+"/modbus/"+targetBus+"/units/1/verify", target.token, nil, &history)
	if status != http.StatusOK || len(history.Records) != 1 || history.Records[0].DeviceID != targetBus+"/1" || !history.Records[0].Passed {
		t.Fatalf("saved acquisition evidence: %d %s", status, body)
	}
	if writes := sim.written(); len(writes) != 0 {
		t.Fatalf("import/rebind/L2 issued physical writes: %v", writes)
	}
}
