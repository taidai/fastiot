package appapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"fastiot/internal/deepseek"
	"fastiot/internal/genscreen"
)

type revisionStub struct {
	server   *httptest.Server
	mu       sync.Mutex
	content  string
	calls    int
	lastBody string
}

func newRevisionStub(t *testing.T) *revisionStub {
	t.Helper()
	stub := &revisionStub{}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		stub.mu.Lock()
		stub.calls++
		stub.lastBody = string(raw)
		content := stub.content
		stub.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}}},
		})
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

func (s *revisionStub) set(content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.content = content
}

func (s *revisionStub) sent() (int, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls, s.lastBody
}

// TestGeneratedScreenRevisionAppliesInSameIsolatedPreview proves the whole
// continuation path: a follow-up instruction is previewed as outbound content,
// the model's modified HTML replaces the preview source, the same sandbox
// serves it under the same policy, and the publish gate needs fresh checks.
func TestGeneratedScreenRevisionAppliesInSameIsolatedPreview(t *testing.T) {
	stub := newRevisionStub(t)
	base, _ := startDeepSeekServer(t, "9861", "9860", deepseek.Credential{APIKey: "test-key", BaseURL: stub.server.URL, Model: "stub-model"})
	token := loginAPI(t, base, "admin", "easyems-deepseek-test")
	root := snapshotRoot(t, base, token)
	levelID := createNodeAPI(t, base, token, "variable", root, "水箱液位", 42.5)

	initial := "```html\n<html><head><style>body{font-family:sans-serif}</style></head>\n<body>\n" +
		`<div id="tank" class="card"><h1>泵房</h1><span data-fastiot-point="` + levelID + `" data-fastiot-format="number">--</span></div>` + "\n" +
		"</body></html>\n```"
	revised := "```html\n<html><head><style>body{font-family:sans-serif}</style></head>\n<body>\n" +
		`<div id="tank" class="card red"><h1>泵房 V2</h1><span data-fastiot-point="` + levelID + `" data-fastiot-format="number">--</span></div>` + "\n" +
		"</body></html>\n```"

	stub.set(initial)
	var generated struct {
		Content string `json:"content"`
	}
	code, raw := call(t, http.MethodPost, base+"/deepseek/generate", token, map[string]any{
		"instruction": "生成泵房画面", "point_ids": []string{levelID}, "confirmed": true,
	}, &generated)
	if code != http.StatusOK {
		t.Fatalf("generate = %d %s", code, raw)
	}
	var preview struct {
		ID         string            `json:"id"`
		Capability string            `json:"capability"`
		FrameURL   string            `json:"frame_url"`
		Report     genscreen.Report  `json:"report"`
		Points     []genscreen.Point `json:"points"`
	}
	code, raw = call(t, http.MethodPost, base+"/generated-screens/preview", token, map[string]any{
		"node_id": root, "source": generated.Content, "point_ids": []string{levelID},
	}, &preview)
	if code != http.StatusOK || preview.ID == "" {
		t.Fatalf("preview = %d %s", code, raw)
	}

	// The outbound preview shows exactly what the revision will send: the new
	// instruction plus the current screen source.
	stub.set(revised)
	var outbound struct {
		Manifest deepseek.Manifest `json:"manifest"`
		Revision int               `json:"revision"`
	}
	code, raw = call(t, http.MethodPost, base+"/generated-screens/"+preview.ID+"/revision/preview", token, map[string]any{
		"instruction": "把水泵卡片改成红色并更新标题",
	}, &outbound)
	if code != http.StatusOK {
		t.Fatalf("revision preview = %d %s", code, raw)
	}
	if outbound.Manifest.Blocked || !strings.Contains(outbound.Manifest.Code.Context, "泵房") {
		t.Fatalf("outbound preview missing the current screen: %+v", outbound.Manifest)
	}
	if outbound.Manifest.Code.Instruction != "把水泵卡片改成红色并更新标题" {
		t.Fatalf("outbound instruction = %q", outbound.Manifest.Code.Instruction)
	}

	// Nothing leaves before the deliverer confirms, even with an instruction.
	calls, _ := stub.sent()
	code, raw = call(t, http.MethodPost, base+"/generated-screens/"+preview.ID+"/revision", token, map[string]any{
		"instruction": "把水泵卡片改成红色并更新标题",
	}, nil)
	if code != http.StatusBadRequest || !strings.Contains(raw, "confirmation_required") {
		t.Fatalf("unconfirmed revision = %d %s", code, raw)
	}
	if now, _ := stub.sent(); now != calls {
		t.Fatalf("model called %d times without confirmation", now-calls)
	}

	var applied struct {
		Content  string            `json:"content"`
		FrameURL string            `json:"frame_url"`
		Revision int               `json:"revision"`
		Report   genscreen.Report  `json:"report"`
		Changes  []genscreen.Change `json:"changes"`
	}
	code, raw = call(t, http.MethodPost, base+"/generated-screens/"+preview.ID+"/revision", token, map[string]any{
		"instruction": "把水泵卡片改成红色并更新标题", "confirmed": true,
	}, &applied)
	if code != http.StatusOK {
		t.Fatalf("revision = %d %s", code, raw)
	}
	if applied.Revision != 1 || !strings.Contains(applied.FrameURL, "rev=1") {
		t.Fatalf("revision identity = %+v", applied)
	}
	if !applied.Report.Ready {
		t.Fatalf("revised screen must pass static checks: %+v", applied.Report)
	}
	if len(applied.Changes) < 2 {
		t.Fatalf("revision did not report where it changed: %+v", applied.Changes)
	}
	attribute, text := false, false
	for _, change := range applied.Changes {
		if change.Kind == "attribute" && strings.Contains(change.Selector, "div#tank") && strings.Contains(change.Detail, "class") {
			attribute = true
		}
		if change.Kind == "text" && strings.Contains(change.Selector, "h1") && strings.Contains(change.Detail, "泵房 V2") {
			text = true
		}
	}
	if !attribute || !text {
		t.Fatalf("changes = %+v", applied.Changes)
	}

	// The payload carried the instruction and the existing screen, and never
	// the platform credential.
	if sent, body := stub.sent(); sent != calls+1 || !strings.Contains(body, "把水泵卡片改成红色并更新标题") || !strings.Contains(body, "泵房") {
		t.Fatalf("model payload = %d %s", sent, body)
	}
	if _, body := stub.sent(); strings.Contains(body, "test-key") {
		t.Fatal("credential leaked to the model")
	}

	// The same sandboxed frame now serves the modified markup with the same
	// strict policy; the capability is unchanged.
	frameURL := strings.TrimSuffix(base, "/api") + applied.FrameURL
	response, err := http.Get(frameURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "泵房 V2") {
		t.Fatalf("revised frame = %d %s", response.StatusCode, body)
	}
	for _, want := range []string{"connect-src 'none'", "script-src 'nonce-", "default-src 'none'"} {
		if !strings.Contains(response.Header.Get("Content-Security-Policy"), want) {
			t.Fatalf("revised frame lost %q: %s", want, response.Header.Get("Content-Security-Policy"))
		}
	}
	if !strings.Contains(string(body), "data-fastiot-runtime") {
		t.Fatal("revised frame lost the trusted runtime")
	}

	// The publish gate is not inherited: the modified document must run its
	// browser checks again.
	code, raw = call(t, http.MethodPost, base+"/generated-screens/"+preview.ID+"/confirm", token, map[string]any{}, nil)
	if code != http.StatusConflict || !strings.Contains(raw, "checks_required") {
		t.Fatalf("confirm without fresh checks = %d %s", code, raw)
	}
	var checked struct {
		Report genscreen.Report `json:"report"`
	}
	code, raw = call(t, http.MethodPost, base+"/generated-screens/"+preview.ID+"/diagnostics", token, map[string]any{
		"findings": []map[string]any{},
	}, &checked)
	if code != http.StatusOK || !checked.Report.Ready {
		t.Fatalf("fresh checks = %d %s", code, raw)
	}
	code, raw = call(t, http.MethodPost, base+"/generated-screens/"+preview.ID+"/confirm", token, map[string]any{}, nil)
	if code != http.StatusOK {
		t.Fatalf("confirm after fresh checks = %d %s", code, raw)
	}
}

func testScreenshotData(t *testing.T) string {
	t.Helper()
	buffer := &bytes.Buffer{}
	if err := png.Encode(buffer, image.NewRGBA(image.Rect(0, 0, 8, 6))); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buffer.Bytes())
}

// TestGeneratedScreenRevisionFromAnnotatedScreenshot proves an uploaded
// annotated screenshot is reviewed and scanned before sending, its region note
// reaches the model, and the image pixels do not.
func TestGeneratedScreenRevisionFromAnnotatedScreenshot(t *testing.T) {
	stub := newRevisionStub(t)
	base, _ := startDeepSeekServer(t, "9871", "9870", deepseek.Credential{APIKey: "test-key", BaseURL: stub.server.URL, Model: "stub-model"})
	token := loginAPI(t, base, "admin", "easyems-deepseek-test")
	root := snapshotRoot(t, base, token)
	levelID := createNodeAPI(t, base, token, "variable", root, "出口压力", 12)

	source := "<html><head></head><body>\n<span data-fastiot-point=\"" + levelID + "\">--</span>\n</body></html>"
	var preview struct {
		ID string `json:"id"`
	}
	code, raw := call(t, http.MethodPost, base+"/generated-screens/preview", token, map[string]any{
		"node_id": root, "source": source, "point_ids": []string{levelID},
	}, &preview)
	if code != http.StatusOK {
		t.Fatalf("preview = %d %s", code, raw)
	}
	data := testScreenshotData(t)
	revised := "<html><head></head><body>\n<h1>压力卡片</h1>\n<span data-fastiot-point=\"" + levelID + "\">--</span>\n</body></html>"
	stub.set(revised)

	region := func(note string) []map[string]any {
		return []map[string]any{{"x": 0.8, "y": 0.2, "note": note}}
	}

	// An unreviewed screenshot is blocked before any HTTP call.
	code, raw = call(t, http.MethodPost, base+"/generated-screens/"+preview.ID+"/revision", token, map[string]any{
		"instruction": "按标注修改", "confirmed": true,
		"screenshots": []map[string]any{{
			"name": "标注.png", "media_type": "image/png", "data": data, "reviewed": false, "regions": region("把右上角卡片改成红色"),
		}},
	}, nil)
	if code != http.StatusConflict || !strings.Contains(raw, "screenshot_unreviewed") {
		t.Fatalf("unreviewed screenshot = %d %s", code, raw)
	}
	if calls, _ := stub.sent(); calls != 0 {
		t.Fatalf("model called %d times for an unreviewed screenshot", calls)
	}

	// The region note is scanned like every other outbound string.
	code, raw = call(t, http.MethodPost, base+"/generated-screens/"+preview.ID+"/revision", token, map[string]any{
		"instruction": "按标注修改", "confirmed": true,
		"screenshots": []map[string]any{{
			"name": "标注.png", "media_type": "image/png", "data": data, "reviewed": true, "regions": region("把 10.0.0.5 那张卡片改成红色"),
		}},
	}, nil)
	if code != http.StatusConflict || !strings.Contains(raw, "sensitive_content") {
		t.Fatalf("sensitive region note = %d %s", code, raw)
	}
	if calls, _ := stub.sent(); calls != 0 {
		t.Fatalf("model called %d times for a blocked observation", calls)
	}

	// A reviewed annotation whose note names no region is refused: the model
	// could not know where to change the screen.
	code, raw = call(t, http.MethodPost, base+"/generated-screens/"+preview.ID+"/revision/preview", token, map[string]any{
		"instruction": "按标注修改",
		"screenshots": []map[string]any{{
			"name": "标注.png", "media_type": "image/png", "data": data, "reviewed": true, "regions": []map[string]any{},
		}},
	}, nil)
	if code != http.StatusBadRequest || !strings.Contains(raw, "annotation_required") {
		t.Fatalf("unannotated screenshot = %d %s", code, raw)
	}

	// The outbound preview shows the region, the image size and no pixels.
	var outbound struct {
		Manifest deepseek.Manifest `json:"manifest"`
	}
	code, raw = call(t, http.MethodPost, base+"/generated-screens/"+preview.ID+"/revision/preview", token, map[string]any{
		"instruction": "按标注修改",
		"screenshots": []map[string]any{{
			"name": "标注.png", "media_type": "image/png", "data": data, "reviewed": true, "regions": region("右上角卡片改成红色"),
		}},
	}, &outbound)
	if code != http.StatusOK {
		t.Fatalf("annotated preview = %d %s", code, raw)
	}
	shot := outbound.Manifest.Screenshots[0]
	if shot.SendsPixels || shot.Width != 8 || shot.Height != 6 || len(shot.Regions) != 1 || shot.Regions[0].Note != "右上角卡片改成红色" {
		t.Fatalf("screenshot summary = %+v", shot)
	}

	var applied struct {
		Revision int              `json:"revision"`
		Report   genscreen.Report `json:"report"`
	}
	code, raw = call(t, http.MethodPost, base+"/generated-screens/"+preview.ID+"/revision", token, map[string]any{
		"instruction": "按标注修改", "confirmed": true,
		"screenshots": []map[string]any{{
			"name": "标注.png", "media_type": "image/png", "data": data, "reviewed": true, "regions": region("右上角卡片改成红色"),
		}},
	}, &applied)
	if code != http.StatusOK || applied.Revision != 1 {
		t.Fatalf("annotated revision = %d %s", code, raw)
	}
	calls, body := stub.sent()
	if calls != 1 {
		t.Fatalf("model calls = %d", calls)
	}
	for _, want := range []string{"右上角卡片改成红色", "80%", "20%"} {
		if !strings.Contains(body, want) {
			t.Fatalf("payload missing %q: %s", want, body)
		}
	}
	pixels := strings.TrimPrefix(data, "data:image/png;base64,")
	if strings.Contains(body, "data:image/png;base64") || strings.Contains(body, pixels) {
		t.Fatal("screenshot pixels were forwarded")
	}
}
