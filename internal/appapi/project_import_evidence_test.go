package appapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/ems/profile"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/emsapi"
)

type ticket21ImportEvidence struct {
	ID                string    `json:"id"`
	Actor             string    `json:"actor"`
	SourcePackage     string    `json:"source_package"`
	SourceVersion     string    `json:"source_version"`
	SourceFingerprint string    `json:"source_fingerprint"`
	Target            string    `json:"target"`
	Time              time.Time `json:"time"`
	Result            string    `json:"result"`
	Reason            string    `json:"reason"`
	NodeIDs           []string  `json:"node_ids"`
}

func TestTicket21ExternalPackageImportEvidence(t *testing.T) {
	sourceDir, sourcePassword := t.TempDir(), data.NewID()
	source, sourceToken, sourceHandler, _ := startInputServer(t, sourceDir, sourcePassword)
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	t.Cleanup(service.Close)
	sourceHandler.SetEMS(emsapi.New(service, time.Minute))
	root := snapshotRoot(t, source, sourceToken)
	site := createNodeAPI(t, source, sourceToken, NodeTypeSite, root, "External published project", 0)
	createNodeAPI(t, source, sourceToken, data.NodeTypeVariable, site, "Software observation", 0)

	var gates preflightReport
	status, body := call(t, http.MethodGet, source+"/delivery/gates?site="+site, sourceToken, nil, &gates)
	if status != http.StatusOK || gates.Fingerprint == "" {
		t.Fatalf("source preflight: %d %s", status, body)
	}
	var published versionSummary
	status, body = call(t, http.MethodPost, source+"/projects/publish", sourceToken, map[string]any{
		"site": site, "expected_fingerprint": gates.Fingerprint,
	}, &published)
	if status != http.StatusOK {
		t.Fatalf("source publish: %d %s", status, body)
	}
	var pkg projectPackage
	status, body = call(t, http.MethodGet, source+"/projects/export?site="+site+"&version="+published.ID, sourceToken, nil, &pkg)
	if status != http.StatusOK || !pkg.Deliverable || pkg.SourceVersion != published.ID || pkg.SourceFingerprint == "" {
		t.Fatalf("source export: %d %s", status, body)
	}

	targetDir, targetPassword := t.TempDir(), data.NewID()
	target, targetToken, targetHandler, stopTarget := startInputServer(t, targetDir, targetPassword)
	targetHandler.SetEMS(&emsapi.Handler{Profiles: profile.Default()})
	targetRoot := snapshotRoot(t, target, targetToken)
	var imported struct {
		SiteID    string                 `json:"site_id"`
		VersionID string                 `json:"version_id"`
		NodeIDs   []string               `json:"node_ids"`
		Evidence  ticket21ImportEvidence `json:"evidence"`
	}
	status, body = call(t, http.MethodPost, target+"/projects/import", targetToken, map[string]any{
		"package": pkg, "parent": targetRoot,
	}, &imported)
	if status != http.StatusOK {
		t.Fatalf("external import without a target publication: %d %s", status, body)
	}
	evidence := imported.Evidence
	if evidence.ID == "" || evidence.Actor == "" || evidence.SourcePackage != pkg.ID ||
		evidence.SourceVersion != pkg.SourceVersion || evidence.SourceFingerprint != pkg.SourceFingerprint ||
		evidence.Target != imported.SiteID || evidence.Time.IsZero() || evidence.Result != "success" ||
		len(evidence.NodeIDs) != len(imported.NodeIDs) {
		t.Fatalf("import evidence is incomplete: %s", body)
	}

	readEvidence := func(base, token string) ticket21ImportEvidence {
		t.Helper()
		var versions struct {
			Versions []struct {
				ID     string                  `json:"id"`
				Import *ticket21ImportEvidence `json:"import"`
			} `json:"versions"`
		}
		status, body := call(t, http.MethodGet, base+"/projects/versions?site="+imported.SiteID, token, nil, &versions)
		if status != http.StatusOK {
			t.Fatalf("read imported versions: %d %s", status, body)
		}
		for _, version := range versions.Versions {
			if version.ID == imported.VersionID && version.Import != nil {
				return *version.Import
			}
		}
		t.Fatalf("persisted import evidence missing: %s", body)
		return ticket21ImportEvidence{}
	}
	persisted := readEvidence(target, targetToken)
	if persisted.ID != evidence.ID || persisted.SourceFingerprint != evidence.SourceFingerprint || persisted.Target != imported.SiteID {
		t.Fatalf("persisted evidence = %#v, want %#v", persisted, evidence)
	}

	beforeFailure := len(snapshotNodes(t, target, targetToken))
	clone := func() projectPackage {
		t.Helper()
		raw, err := json.Marshal(pkg)
		if err != nil {
			t.Fatal(err)
		}
		var copied projectPackage
		if err := json.Unmarshal(raw, &copied); err != nil {
			t.Fatal(err)
		}
		return copied
	}
	missing := clone()
	missing.Dependencies = append(missing.Dependencies, packageDependency{Kind: dependencyKindProfile, ID: "target-does-not-have-this", Version: "private-test-version"})
	missing.seal()
	status, body = call(t, http.MethodPost, target+"/projects/import", targetToken, map[string]any{"package": missing, "parent": targetRoot}, nil)
	if status != http.StatusBadRequest || !strings.Contains(body, "missing_dependency:profile:target-does-not-have-this") {
		t.Fatalf("dependency refusal: %d %s", status, body)
	}
	if after := len(snapshotNodes(t, target, targetToken)); after != beforeFailure {
		t.Fatalf("dependency failure left nodes: before=%d after=%d", beforeFailure, after)
	}

	tampered := clone()
	tampered.Name = "tampered after sealing"
	status, body = call(t, http.MethodPost, target+"/projects/import", targetToken, map[string]any{"package": tampered, "parent": targetRoot}, nil)
	if status != http.StatusBadRequest || !strings.Contains(body, "package_integrity_mismatch") {
		t.Fatalf("tamper refusal: %d %s", status, body)
	}
	if after := len(snapshotNodes(t, target, targetToken)); after != beforeFailure {
		t.Fatalf("tamper failure left nodes: before=%d after=%d", beforeFailure, after)
	}

	stopTarget()
	target, targetToken, _, _ = startInputServer(t, targetDir, targetPassword)
	restarted := readEvidence(target, targetToken)
	if restarted.ID != evidence.ID || restarted.SourceVersion != pkg.SourceVersion || restarted.SourceFingerprint != pkg.SourceFingerprint {
		t.Fatalf("restart lost import evidence: %#v", restarted)
	}
}
