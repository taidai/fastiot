package appapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

func TestAcceptanceEvidenceManifestScopesVersionsAndDeployments(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, token)
	site := createNodeAPI(t, base, token, NodeTypeSite, root, "Evidence site", 0)
	identity := Identity{UserID: "admin", Policy: AccessPolicy{Admin: true}, Anchors: []string{root}}

	// 一条回环通道（仿真证据）与一条非回环通道（现场证据）。
	bus, err := CreateDomainNode(h.nc, site, data.NodeTypeModbus, map[string]string{"description": "Loopback bus"})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SendNodePoints(h.nc, bus, data.Points{
		data.NewPointString(data.PointTypeProtocol, "0", data.PointValueTCP),
		data.NewPointString(data.PointTypeURI, "0", "127.0.0.1:18118"),
	}, true); err != nil {
		t.Fatal(err)
	}
	remote, err := CreateDomainNode(h.nc, site, data.NodeTypeModbus, map[string]string{"description": "Field bus"})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SendNodePoints(h.nc, remote, data.Points{
		data.NewPointString(data.PointTypeProtocol, "0", data.PointValueTCP),
		data.NewPointString(data.PointTypeURI, "0", "10.20.30.40:502"),
	}, true); err != nil {
		t.Fatal(err)
	}

	version := freezeRecoveryBaseline(t, h, identity, site, "evidence baseline")
	var record deploymentRecord
	status, body := call(t, http.MethodPost, base+"/delivery/deployments", token, map[string]any{"site": site, "version": version.ID}, &record)
	if status != http.StatusOK {
		t.Fatalf("deployment record: %d %s", status, body)
	}

	status, packed := call(t, http.MethodGet, base+"/delivery/evidence", token, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("evidence: %d", status)
	}
	raw := []byte(packed)
	archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var manifestFile *zip.File
	for _, file := range archive.File {
		if file.Name == "09-验收证据.json" {
			manifestFile = file
		}
	}
	if manifestFile == nil {
		t.Fatal("evidence manifest missing")
	}
	reader, err := manifestFile.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var manifest acceptanceManifest
	if err := json.NewDecoder(reader).Decode(&manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Format != acceptanceManifestFormat || manifest.Version != 1 || manifest.Actor == "" || manifest.ExportedAt == "" {
		t.Fatalf("manifest header: %+v", manifest)
	}
	if !manifest.Scope.Admin || len(manifest.Scope.Anchors) == 0 {
		t.Fatalf("manifest scope: %+v", manifest.Scope)
	}
	if len(manifest.Sites) != 1 {
		t.Fatalf("visible sites: %+v", manifest.Sites)
	}
	entry := manifest.Sites[0]
	if entry.SiteID != site || entry.Simulation != 1 || entry.Field != 1 {
		t.Fatalf("channel evidence classes: %+v", entry)
	}
	if entry.Version == nil || entry.Version.ID != version.ID || entry.Version.Fingerprint == "" {
		t.Fatalf("version reference: %+v", entry.Version)
	}
	if entry.Preflight.Fingerprint == "" {
		t.Fatalf("preflight fingerprint missing: %+v", entry.Preflight)
	}
	if len(entry.Deployments) != 1 || entry.Deployments[0].ID != record.ID || entry.Deployments[0].ControlAuthorized {
		t.Fatalf("deployment evidence: %+v", entry.Deployments)
	}

	// 受限账号既不能导出（缺少 export 动作），也拿不到其他 Site 的证据。
	otherSite := createNodeAPI(t, base, token, NodeTypeSite, root, "Outside evidence scope", 0)
	viewer := inputAccount(t, &pointConfigFixture{base: base, token: token}, otherSite, "viewer", nil)
	status, _ = call(t, http.MethodGet, base+"/delivery/evidence", viewer, nil, nil)
	if status != http.StatusForbidden {
		t.Fatalf("restricted evidence export allowed: %d", status)
	}
}
