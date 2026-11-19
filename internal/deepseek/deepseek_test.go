package deepseek

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func stubModel(t *testing.T, content string) (*httptest.Server, *int32, *string, *string) {
	t.Helper()
	var calls int32
	var lastAuth, lastBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		lastAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		lastBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}}},
		})
	}))
	t.Cleanup(server.Close)
	return server, &calls, &lastAuth, &lastBody
}

func configured(server *httptest.Server) *Service {
	return NewService(Credential{APIKey: "test-key", BaseURL: server.URL, Model: "stub-model"})
}

func cleanRequest() Request {
	return Request{
		Instruction: "生成一个泵房画面，显示液位和压力，单位为 m 和 kPa。",
		Context:     `{"title":"泵房画面","columns":3,"point_ids":["a","b"]}`,
		Points: []PointSummary{
			{ID: "level-id", Name: "水箱液位", Type: "number", Unit: "m"},
			{ID: "pressure-id", Name: "出口压力", Type: "number", Unit: "kPa"},
		},
	}
}

func TestFromEnvUsesUserProvidedCredentialAndDefaults(t *testing.T) {
	env := map[string]string{"DEEPSEEK_API_KEY": "sk-user-provided"}
	cred := FromEnv(func(key string) string { return env[key] })
	if !cred.Configured() {
		t.Fatal("credential should be configured when DEEPSEEK_API_KEY is set")
	}
	if cred.BaseURL != DefaultBaseURL || cred.Model != DefaultModel {
		t.Fatalf("defaults not applied: %+v", cred)
	}

	missing := FromEnv(func(string) string { return "" })
	if missing.Configured() {
		t.Fatal("credential must be unconfigured without DEEPSEEK_API_KEY")
	}
}

func TestStatusNeverExposesCredential(t *testing.T) {
	service := NewService(Credential{APIKey: "sk-test-secret", BaseURL: "https://example.invalid", Model: "deepseek-chat"})
	status := service.Status()
	if !status.Configured || status.Model != "deepseek-chat" {
		t.Fatalf("status = %+v", status)
	}
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("sk-test-secret")) {
		t.Fatalf("status leaked the credential: %s", raw)
	}
}

func TestManifestListsThreeOutboundCategories(t *testing.T) {
	req := cleanRequest()
	req.Screenshots = []Screenshot{{Name: "标注.png", MediaType: "image/png", Annotation: "把液位卡片移到左上角", Reviewed: true}}
	manifest := BuildManifest(req)
	if manifest.Blocked {
		t.Fatalf("clean manifest blocked: %+v", manifest.BlockReasons)
	}
	keys := make([]string, 0, len(manifest.Categories))
	for _, category := range manifest.Categories {
		keys = append(keys, category.Key)
		if category.Label == "" || category.Summary == "" {
			t.Fatalf("category lacks label/summary: %+v", category)
		}
	}
	if strings.Join(keys, ",") != "code,device_model,screenshots" {
		t.Fatalf("categories = %v", keys)
	}
	if len(manifest.DeviceModel) != 2 || manifest.DeviceModel[0].Name != "水箱液位" || manifest.DeviceModel[0].ID != "level-id" {
		t.Fatalf("device model = %+v", manifest.DeviceModel)
	}
	if len(manifest.Screenshots) != 1 || manifest.Screenshots[0].SendsPixels {
		t.Fatalf("screenshot must not send pixels: %+v", manifest.Screenshots)
	}
}

func TestManifestScansSensitiveText(t *testing.T) {
	cases := []struct {
		name        string
		instruction string
		kind        string
		secret      string
	}{
		{"ip", "设备 IP 192.168.1.20，请生成画面", "ip_address", "192.168.1.20"},
		{"phone", "现场联系人 13800138000", "phone", "13800138000"},
		{"email", "账号 ops@example.com 登录", "account", "ops@example.com"},
		{"credential", "密码: Hunter2!", "credential", "Hunter2!"},
		{"serial", "序列号 SN: A1B2C3D4E5", "serial", "A1B2C3D4E5"},
		{"address", "现场地址：北京市朝阳区建国路88号", "address", "建国路88号"},
		{"api_key", "用 sk-abcdefghijklmnopqrstuvwxyz 调用", "credential", "sk-abcdefghijklmnopqrstuvwxyz"},
		{"ipv6", "网关 fe80::1ff:fe23:4567:890a", "ip_address", "fe80::1ff:fe23:4567:890a"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			manifest := BuildManifest(Request{Instruction: test.instruction})
			if !manifest.Blocked {
				t.Fatalf("sensitive text was not blocked: %+v", manifest)
			}
			var found *Finding
			for index := range manifest.Findings {
				if manifest.Findings[index].Kind == test.kind {
					found = &manifest.Findings[index]
				}
			}
			if found == nil {
				t.Fatalf("no %s finding: %+v", test.kind, manifest.Findings)
			}
			if found.Category != "code" || found.Field != "instruction" || found.Line != 1 || found.Position < 0 {
				t.Fatalf("finding lacks location: %+v", found)
			}
			if strings.Contains(found.Preview, test.secret) {
				t.Fatalf("finding preview leaked the secret: %q", found.Preview)
			}
			if found.ID == "" || found.Blocking == false {
				t.Fatalf("finding id/blocking missing: %+v", found)
			}
		})
	}
}

func TestManifestAllowsCleanInstruction(t *testing.T) {
	manifest := BuildManifest(cleanRequest())
	if manifest.Blocked || len(manifest.Findings) != 0 {
		t.Fatalf("clean request blocked: %+v", manifest)
	}
}

func TestScreenshotMustBeReviewed(t *testing.T) {
	req := cleanRequest()
	req.Screenshots = []Screenshot{{Name: "标注.png", MediaType: "image/png", Annotation: "把卡片放大"}}
	manifest := BuildManifest(req)
	if !manifest.Blocked || !contains(manifest.BlockReasons, "screenshot_unreviewed") {
		t.Fatalf("unreviewed screenshot must block: %+v", manifest.BlockReasons)
	}
}

func TestScreenshotAnnotationIsScanned(t *testing.T) {
	req := cleanRequest()
	req.Screenshots = []Screenshot{{Name: "标注.png", MediaType: "image/png", Annotation: "把 10.0.0.5 这张图里的卡片放大", Reviewed: true}}
	manifest := BuildManifest(req)
	if !manifest.Blocked {
		t.Fatal("annotated address must block")
	}
	finding := manifest.Findings[0]
	if finding.Category != "screenshot" || finding.Item != 0 || finding.Field != "annotation" || finding.Kind != "ip_address" {
		t.Fatalf("finding = %+v", finding)
	}
}

func TestScreenshotPNGMetadataIsScanned(t *testing.T) {
	png := pngWithText("Site", "server 10.0.0.5")
	req := cleanRequest()
	req.Screenshots = []Screenshot{{
		Name: "截图.png", MediaType: "image/png",
		Data: base64.StdEncoding.EncodeToString(png), Reviewed: true,
	}}
	manifest := BuildManifest(req)
	if !manifest.Blocked {
		t.Fatalf("PNG text metadata must be scanned: %+v", manifest)
	}
	finding := manifest.Findings[0]
	if finding.Category != "screenshot" || finding.Field != "metadata" || finding.Kind != "ip_address" {
		t.Fatalf("finding = %+v", finding)
	}
	summary := manifest.Screenshots[0]
	if summary.Bytes != len(png) || summary.Width != 4 || summary.Height != 3 {
		t.Fatalf("screenshot summary = %+v", summary)
	}
}

func TestInvalidScreenshotDataBlocks(t *testing.T) {
	req := cleanRequest()
	req.Screenshots = []Screenshot{{Name: "坏图.png", MediaType: "image/png", Data: "not-base64!!", Reviewed: true}}
	manifest := BuildManifest(req)
	if !manifest.Blocked || !contains(manifest.BlockReasons, "screenshot_invalid") {
		t.Fatalf("invalid screenshot must block: %+v", manifest.BlockReasons)
	}
}

func TestMessagesExcludeLiveValuesAndPixels(t *testing.T) {
	png := pngWithText("Note", "export pressure")
	req := cleanRequest()
	req.Screenshots = []Screenshot{{
		Name: "截图.png", MediaType: "image/png",
		Data: base64.StdEncoding.EncodeToString(png), Annotation: "把压力卡片放大", Reviewed: true,
	}}
	manifest := BuildManifest(req)
	raw, err := json.Marshal(MessagesForManifest(manifest))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "水箱液位") || !strings.Contains(text, "kPa") {
		t.Fatalf("device model structure missing: %s", text)
	}
	if !strings.Contains(text, "id=level-id") || !strings.Contains(text, "id=pressure-id") {
		t.Fatalf("point ids missing, so the model cannot bind anything: %s", text)
	}
	if !strings.Contains(text, "把压力卡片放大") {
		t.Fatalf("screenshot annotation missing: %s", text)
	}
	if strings.Contains(text, base64.StdEncoding.EncodeToString(png)) {
		t.Fatal("screenshot pixels must never leave")
	}
	if strings.Contains(text, "42.5") || strings.Contains(text, `"value"`) || strings.Contains(text, `"time"`) {
		t.Fatalf("live values leaked into the outbound payload: %s", text)
	}
}

func TestGenerateDoesNotCallModelWithoutCredential(t *testing.T) {
	server, calls, _, _ := stubModel(t, "unused")
	service := NewService(Credential{BaseURL: server.URL, Model: "stub-model"})
	_, err := service.Generate(context.Background(), cleanRequest())
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
	if got := atomic.LoadInt32(calls); got != 0 {
		t.Fatalf("model endpoint called %d times without credential", got)
	}
}

func TestGenerateRefusesBlockedManifest(t *testing.T) {
	server, calls, _, _ := stubModel(t, "unused")
	service := configured(server)
	req := cleanRequest()
	req.Instruction = "把 10.0.0.5 的数值显示出来"
	_, err := service.Generate(context.Background(), req)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked", err)
	}
	if got := atomic.LoadInt32(calls); got != 0 {
		t.Fatalf("model endpoint called %d times for a blocked request", got)
	}
}

func TestGenerateSendsManifestAndReturnsContent(t *testing.T) {
	server, calls, auth, body := stubModel(t, "<html>泵房</html>")
	service := configured(server)
	req := cleanRequest()
	result, err := service.Generate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "<html>泵房</html>" || result.Model != "stub-model" {
		t.Fatalf("result = %+v", result)
	}
	if atomic.LoadInt32(calls) != 1 {
		t.Fatalf("calls = %d", *calls)
	}
	if *auth != "Bearer test-key" {
		t.Fatalf("authorization header = %q", *auth)
	}
	for _, want := range []string{"水箱液位", "泵房画面", "生成一个泵房画面"} {
		if !strings.Contains(*body, want) {
			t.Fatalf("payload missing %q: %s", want, *body)
		}
	}
	if strings.Contains(*body, "test-key") {
		t.Fatal("payload must not contain the credential")
	}
}

func TestAcknowledgedFindingAllowsGenerate(t *testing.T) {
	server, calls, _, _ := stubModel(t, "ok")
	service := configured(server)
	req := cleanRequest()
	req.Instruction = "把 10.0.0.5 的数值显示出来"
	manifest := service.Preview(req)
	if !manifest.Blocked {
		t.Fatal("preview should block before acknowledgement")
	}
	for _, finding := range manifest.Findings {
		req.Acknowledged = append(req.Acknowledged, finding.ID)
	}
	if _, err := service.Generate(context.Background(), req); err != nil {
		t.Fatalf("acknowledged request should generate: %v", err)
	}
	if atomic.LoadInt32(calls) != 1 {
		t.Fatalf("calls = %d", *calls)
	}
}

func TestEmptyInstructionBlocks(t *testing.T) {
	manifest := BuildManifest(Request{Instruction: "   "})
	if !manifest.Blocked || !contains(manifest.BlockReasons, "instruction_required") {
		t.Fatalf("empty instruction must block: %+v", manifest.BlockReasons)
	}
}

// TestMessagesOfferTheBundledThreeAndForbidExternalAssets keeps the model
// guidance in step with the offline preview: the only 3D dependency is the
// bundled global, live values arrive as an event, and assets stay inline.
func TestMessagesOfferTheBundledThreeAndForbidExternalAssets(t *testing.T) {
	raw, err := json.Marshal(MessagesForManifest(BuildManifest(cleanRequest())))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{"Three.js", "THREE", "fastiot:values", "CDN", "data: URI"} {
		if !strings.Contains(text, want) {
			t.Fatalf("model guidance is missing %q: %s", want, text)
		}
	}
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func pngWithText(keyword, text string) []byte {
	var buf bytes.Buffer
	buf.Write([]byte{137, 80, 78, 71, 13, 10, 26, 10})
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], 4)
	binary.BigEndian.PutUint32(ihdr[4:8], 3)
	ihdr[8] = 8
	ihdr[9] = 2
	writeChunk(&buf, "IHDR", ihdr)
	writeChunk(&buf, "tEXt", append(append([]byte(keyword), 0), []byte(text)...))
	writeChunk(&buf, "IEND", nil)
	return buf.Bytes()
}

func writeChunk(buf *bytes.Buffer, kind string, data []byte) {
	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, uint32(len(data)))
	buf.Write(length)
	buf.WriteString(kind)
	buf.Write(data)
	crc := crc32.NewIEEE()
	crc.Write([]byte(kind))
	crc.Write(data)
	sum := make([]byte, 4)
	binary.BigEndian.PutUint32(sum, crc.Sum32())
	buf.Write(sum)
}
