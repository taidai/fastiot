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
	"fastiot/internal/notification"
)

// Opt-in private fixture. It uses actual accounts and disk, restarts before
// serving H5, and never mounts device/notification transport clients.
func TestNotificationBrowserFixture(t *testing.T) {
	if os.Getenv("FASTIOT_INBOX_BROWSER") != "1" {
		t.Skip("opt-in private inbox fixture")
	}
	const password = "inbox-private-fixture"
	dir := t.TempDir()
	base, admin, h, stop := startInputServer(t, dir, password)
	root := snapshotRoot(t, base, admin)
	site := createNodeAPI(t, base, admin, "site", root, "通知验证现场", 0)
	outside := createNodeAPI(t, base, admin, "site", root, "范围外现场", 0)
	alice := inboxAccount(t, base, admin, site, "alice@example.test", password)
	bob := inboxAccount(t, base, admin, site, "bob@example.test", password)
	inboxAccount(t, base, admin, outside, "outside@example.test", password)
	req := notification.Request{RuleID: inboxRule(t, h, site), SourceRevision: "fixture-revision", ExecutionID: "fixture-execution", ActionID: "fixture-step-0", ScopeNodeID: site, RecipientIDs: []string{alice, bob}, Title: "请检查泵房压力", Body: "这是已持久创建的隔离测试通知。<script>window.inboxInjected=true</script>"}
	created, err := h.CreateRuleNotification(req)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	base, _, h, _ = startInputServer(t, dir, password)
	retry, err := h.CreateRuleNotification(req)
	if err != nil || !retry.Duplicate || retry.ID != created.ID {
		t.Fatal("durable restart/replay proof failed", retry, err)
	}
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	h.SetEMS(emsapi.New(service, time.Minute))
	target, _ := url.Parse(base[:len(base)-len("/api")])
	mux := http.NewServeMux()
	mux.Handle("/api/", httputil.NewSingleHostReverseProxy(target))
	mux.Handle("/uniapp/", http.StripPrefix("/uniapp/", http.FileServer(http.Dir("../web/dist/uniapp"))))
	t.Log("private inbox fixture ready :18131; durable creation and server restart replay verified")
	if err := http.ListenAndServe(":18131", mux); err != nil {
		t.Fatal(err)
	}
}
