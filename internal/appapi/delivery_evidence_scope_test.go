package appapi

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/audit"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/emsapi"
)

func TestDeliveryEvidenceLimitsPreflightAuditAndAccountsToVisibleScope(t *testing.T) {
	base, admin, handler, _ := startInputServer(t, t.TempDir(), data.NewID())
	handler.SetEMS(emsapi.New(runtime.NewService(nil, 15*time.Second, 5*time.Second, 100), 15*time.Second))
	handler.SetAudit(audit.New(filepath.Join(t.TempDir(), "audit.jsonl")))
	root := snapshotRoot(t, base, admin)
	siteA, err := CreateDomainNode(handler.nc, root, NodeTypeSite, map[string]string{"description": "可见 A 站"})
	if err != nil {
		t.Fatal(err)
	}
	siteB, err := CreateDomainNode(handler.nc, root, NodeTypeSite, map[string]string{"description": "越权 B 站"})
	if err != nil {
		t.Fatal(err)
	}
	for _, site := range []string{siteA, siteB} {
		status, body := call(t, http.MethodPost, base+"/delivery/preflight?site="+site, admin, nil, nil)
		if status != http.StatusOK {
			t.Fatalf("preflight %s: %d %s", site, status, body)
		}
	}

	email, password := "export-a@fixture.local", "export-a-password"
	status, body := call(t, http.MethodPost, base+"/users", admin, map[string]any{
		"parent": siteA, "email": email, "password": password, "role": "implementer",
	}, nil)
	if status != http.StatusOK {
		t.Fatalf("create export-only account: %d %s", status, body)
	}
	token := loginAPI(t, base, email, password)
	status, payload := call(t, http.MethodGet, base+"/delivery/evidence", token, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("scoped evidence: %d %s", status, payload)
	}
	archive, err := zip.NewReader(bytes.NewReader([]byte(payload)), int64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	contents := map[string]string{}
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		contents[file.Name] = string(raw)
	}
	preflight := contents["05-预检记录.csv"]
	if !strings.Contains(preflight, siteA) {
		t.Fatal("visible site's persisted preflight is missing")
	}
	if strings.Contains(preflight, siteB) || strings.Contains(preflight, "越权 B 站") {
		t.Fatal("scoped export leaked another site's latest preflight")
	}
	if !strings.Contains(contents["07-审计记录.csv"], "当前账号无审计查看权限") || strings.Contains(contents["07-审计记录.csv"], "交付预检") {
		t.Fatal("export without audit_view exposed audit records")
	}
	permissions := contents["08-权限矩阵.csv"]
	if !strings.Contains(permissions, email) || strings.Contains(permissions, "admin,admin,") {
		t.Fatal("export without account_manage must contain only the current account")
	}
}
