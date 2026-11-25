package appapi

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"testing"
	"time"

	"fastiot/internal/ems/runtime"
	"fastiot/internal/emsapi"
)

// Opt-in fixture for accounts.browser.mjs: fresh store, fake accounts, no device
// clients. Serve the built H5 files beside the real Go API on loopback port 18099.
func TestAccountsBrowserFixture(t *testing.T) {
	if os.Getenv("FASTIOT_ACCOUNT_BROWSER") != "1" {
		t.Skip("opt-in browser fixture")
	}
	const password = "ticket19-isolated-fixture"
	base, admin, handler, _ := startInputServer(t, t.TempDir(), password)
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	handler.SetEMS(emsapi.New(service, time.Minute))
	root := snapshotRoot(t, base, admin)
	site := createNodeAPI(t, base, admin, "site", root, "Account fixture A", 0)
	outside := createNodeAPI(t, base, admin, "site", root, "Account fixture B", 0)
	for _, user := range []struct {
		email, parent, role string
		actions             []string
	}{
		{"operator@example.test", site, "operator", nil},
		{"viewer@example.test", outside, "viewer", nil},
		{"manager@example.test", site, "viewer", []string{"account_manage"}},
	} {
		status, body := call(t, "POST", base+"/users", admin, map[string]any{"parent": user.parent, "email": user.email, "password": password, "role": user.role, "actions": user.actions}, nil)
		if status != 200 {
			t.Fatalf("fixture user: %d %s", status, body)
		}
	}
	target, _ := url.Parse(base[:len(base)-len("/api")])
	mux := http.NewServeMux()
	mux.Handle("/api/", httputil.NewSingleHostReverseProxy(target))
	mux.Handle("/uniapp/", http.StripPrefix("/uniapp/", http.FileServer(http.Dir("../web/dist/uniapp"))))
	t.Log("isolated account browser fixture ready on :18099")
	if err := http.ListenAndServe(":18099", mux); err != nil {
		t.Fatal(err)
	}
}
