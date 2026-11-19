package appapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/simpleiot/simpleiot/api"
	"github.com/simpleiot/simpleiot/data"
	"github.com/simpleiot/simpleiot/server"

	"fastiot/internal/deepseek"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/emsapi"
)

type deepseekStub struct {
	server   *httptest.Server
	calls    *int32
	lastAuth *string
	lastBody *string
}

func newDeepSeekStub(t *testing.T, content string) *deepseekStub {
	t.Helper()
	stub := &deepseekStub{calls: new(int32), lastAuth: new(string), lastBody: new(string)}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(stub.calls, 1)
		*stub.lastAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		*stub.lastBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}}},
		})
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

func startDeepSeekServer(t *testing.T, httpPort, natsPort string, credential deepseek.Credential) (string, *nats.Conn) {
	t.Helper()
	password := "easyems-deepseek-test"
	t.Setenv("FASTEMS_ADMIN_PASSWORD", password)
	dataDir := filepath.Join(t.TempDir(), "data")
	ready := new(atomic.Bool)
	options := server.Options{
		ID: "easyems-deepseek-test-" + natsPort, DataDir: dataDir, HTTPPort: httpPort,
		NatsPort: mustAtoi(t, natsPort), NatsServer: "nats://127.0.0.1:" + natsPort, AuthToken: data.NewID(),
		DeviceAuth: server.DeviceAuthRequired, CustomUIFS: fstest.MapFS{"index.html": {Data: []byte("easyems")}},
		AppOnly: true, NatsLoopback: true, ProvisioningDisable: true,
		AppHandlerFactory: func(nc *nats.Conn, users api.UserAuthority) http.Handler {
			handler := New(nc, users, ready)
			handler.SetDataDir(dataDir)
			handler.SetEMS(emsapi.New(runtime.NewService(nil, 15*time.Second, 5*time.Second, 100), 15*time.Second))
			handler.SetDeepSeek(deepseek.NewService(credential))
			return handler
		},
	}
	siot, nc, err := server.NewServer(options)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- siot.Run() }()
	t.Cleanup(func() {
		siot.Stop(nil)
		<-done
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := siot.WaitStart(ctx); err != nil {
		t.Fatal(err)
	}
	if err := Bootstrap(nc, dataDir); err != nil {
		t.Fatal(err)
	}
	ready.Store(true)
	base := "http://127.0.0.1:" + httpPort + "/api"
	waitFor(t, func() bool {
		response, err := http.Get(base + "/health")
		if err != nil {
			return false
		}
		response.Body.Close()
		return response.StatusCode == http.StatusOK
	}, "HTTP readiness")
	return base, nc
}

func TestDeepSeekOutboundPreviewAndGenerate(t *testing.T) {
	stub := newDeepSeekStub(t, "<html>泵房画面</html>")
	base, _ := startDeepSeekServer(t, "9761", "9760", deepseek.Credential{APIKey: "test-key", BaseURL: stub.server.URL, Model: "stub-model"})
	token := loginAPI(t, base, "admin", "easyems-deepseek-test")
	root := snapshotRoot(t, base, token)
	pointID := createNodeAPI(t, base, token, "variable", root, "水箱液位", 42.5)

	var status deepseek.Status
	code, raw := call(t, http.MethodGet, base+"/deepseek/status", token, nil, &status)
	if code != http.StatusOK || !status.Configured || status.Model != "stub-model" {
		t.Fatalf("status = %d %s", code, raw)
	}
	if strings.Contains(raw, "test-key") {
		t.Fatalf("status leaked credential: %s", raw)
	}

	instruction := "生成一个泵房画面，显示水箱液位，单位 m。"
	var preview struct {
		Configured bool              `json:"configured"`
		Manifest   deepseek.Manifest `json:"manifest"`
	}
	code, raw = call(t, http.MethodPost, base+"/deepseek/preview", token, map[string]any{
		"instruction": instruction,
		"point_ids":   []string{pointID},
	}, &preview)
	if code != http.StatusOK || !preview.Configured || preview.Manifest.Blocked {
		t.Fatalf("preview = %d %s", code, raw)
	}
	manifest := preview.Manifest
	if len(manifest.Categories) != 3 || len(manifest.DeviceModel) != 1 || manifest.DeviceModel[0].Name != "水箱液位" {
		t.Fatalf("manifest wrong: %s", raw)
	}
	if manifest.DeviceModel[0].ID != pointID {
		t.Fatalf("manifest point id = %q, want %q", manifest.DeviceModel[0].ID, pointID)
	}
	if manifest.DeviceModel[0].Type != "number" {
		t.Fatalf("point type = %q", manifest.DeviceModel[0].Type)
	}
	if strings.Contains(raw, "42.5") || strings.Contains(raw, `"value"`) || strings.Contains(raw, `"time"`) {
		t.Fatalf("preview leaked live data: %s", raw)
	}
	if atomic.LoadInt32(stub.calls) != 0 {
		t.Fatal("preview must not call the model")
	}

	// Confirmation is explicit: without it nothing leaves.
	code, raw = call(t, http.MethodPost, base+"/deepseek/generate", token, map[string]any{
		"instruction": instruction,
		"point_ids":   []string{pointID},
	}, nil)
	if code != http.StatusBadRequest || !strings.Contains(raw, "confirmation_required") {
		t.Fatalf("unconfirmed generate = %d %s", code, raw)
	}
	if atomic.LoadInt32(stub.calls) != 0 {
		t.Fatal("unconfirmed generate reached the model")
	}

	// Suspected sensitive content blocks the call.
	code, raw = call(t, http.MethodPost, base+"/deepseek/generate", token, map[string]any{
		"instruction": "把 10.0.0.5 的液位显示出来",
		"point_ids":   []string{pointID},
		"confirmed":   true,
	}, nil)
	if code != http.StatusConflict || !strings.Contains(raw, "outbound_blocked") {
		t.Fatalf("blocked generate = %d %s", code, raw)
	}
	if atomic.LoadInt32(stub.calls) != 0 {
		t.Fatal("blocked request reached the model")
	}

	// Unreviewed screenshots block as well; pixels are never forwarded.
	pixel := base64.StdEncoding.EncodeToString([]byte("fake-image-bytes"))
	code, raw = call(t, http.MethodPost, base+"/deepseek/generate", token, map[string]any{
		"instruction": instruction,
		"point_ids":   []string{pointID},
		"screenshots": []map[string]any{{"name": "标注.png", "media_type": "image/png", "data": pixel, "annotation": "把卡片放大"}},
		"confirmed":   true,
	}, nil)
	if code != http.StatusConflict || !strings.Contains(raw, "screenshot_unreviewed") {
		t.Fatalf("unreviewed screenshot = %d %s", code, raw)
	}
	if atomic.LoadInt32(stub.calls) != 0 {
		t.Fatal("unreviewed screenshot reached the model")
	}

	var result struct {
		Content  string            `json:"content"`
		Model    string            `json:"model"`
		Manifest deepseek.Manifest `json:"manifest"`
	}
	code, raw = call(t, http.MethodPost, base+"/deepseek/generate", token, map[string]any{
		"instruction": instruction,
		"point_ids":   []string{pointID},
		"screenshots": []map[string]any{{"name": "标注.png", "media_type": "image/png", "data": pixel, "annotation": "把卡片放大", "reviewed": true}},
		"confirmed":   true,
	}, &result)
	if code != http.StatusOK || result.Content != "<html>泵房画面</html>" || result.Model != "stub-model" {
		t.Fatalf("generate = %d %s", code, raw)
	}
	if atomic.LoadInt32(stub.calls) != 1 {
		t.Fatalf("model calls = %d", *stub.calls)
	}
	if *stub.lastAuth != "Bearer test-key" {
		t.Fatalf("authorization = %q", *stub.lastAuth)
	}
	for _, want := range []string{instruction, "水箱液位", "把卡片放大", "id=" + pointID} {
		if !strings.Contains(*stub.lastBody, want) {
			t.Fatalf("outbound body missing %q: %s", want, *stub.lastBody)
		}
	}
	for _, unwanted := range []string{"42.5", pixel, "test-key"} {
		if strings.Contains(*stub.lastBody, unwanted) {
			t.Fatalf("outbound body leaked %q: %s", unwanted, *stub.lastBody)
		}
	}
}

func TestDeepSeekUnconfiguredMakesNoModelCall(t *testing.T) {
	stub := newDeepSeekStub(t, "unused")
	base, _ := startDeepSeekServer(t, "9771", "9770", deepseek.Credential{BaseURL: stub.server.URL, Model: "stub-model"})
	token := loginAPI(t, base, "admin", "easyems-deepseek-test")
	root := snapshotRoot(t, base, token)
	pointID := createNodeAPI(t, base, token, "variable", root, "出口压力", 1.2)

	var status deepseek.Status
	code, raw := call(t, http.MethodGet, base+"/deepseek/status", token, nil, &status)
	if code != http.StatusOK || status.Configured {
		t.Fatalf("status = %d %s", code, raw)
	}

	instruction := "生成一个泵房画面，显示出口压力。"
	var preview struct {
		Configured bool              `json:"configured"`
		Manifest   deepseek.Manifest `json:"manifest"`
	}
	code, raw = call(t, http.MethodPost, base+"/deepseek/preview", token, map[string]any{
		"instruction": instruction,
		"point_ids":   []string{pointID},
	}, &preview)
	if code != http.StatusOK || preview.Configured || preview.Manifest.Blocked {
		t.Fatalf("preview should still work locally: %d %s", code, raw)
	}

	code, raw = call(t, http.MethodPost, base+"/deepseek/generate", token, map[string]any{
		"instruction": instruction,
		"point_ids":   []string{pointID},
		"confirmed":   true,
	}, nil)
	if code != http.StatusConflict || !strings.Contains(raw, "deepseek_not_configured") {
		t.Fatalf("generate without credential = %d %s", code, raw)
	}
	if atomic.LoadInt32(stub.calls) != 0 {
		t.Fatalf("unconfigured service called the model %d times", *stub.calls)
	}
}

func TestDeepSeekRoutesRequireConfigurePermission(t *testing.T) {
	base, _ := startDeepSeekServer(t, "9781", "9780", deepseek.Credential{APIKey: "test-key", BaseURL: "http://127.0.0.1:9", Model: "stub"})
	admin := loginAPI(t, base, "admin", "easyems-deepseek-test")
	fixture := &pointConfigFixture{base: base, token: admin}
	root := snapshotRoot(t, base, admin)
	viewer := inputAccount(t, fixture, root, "viewer", nil)

	for _, path := range []string{"/deepseek/preview", "/deepseek/generate"} {
		code, raw := call(t, http.MethodPost, base+path, viewer, map[string]any{"instruction": "生成画面"}, nil)
		if code != http.StatusForbidden {
			t.Fatalf("%s as viewer = %d %s", path, code, raw)
		}
	}
}

func mustAtoi(t *testing.T, value string) int {
	t.Helper()
	result, err := strconv.Atoi(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
