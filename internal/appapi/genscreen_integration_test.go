package appapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/deepseek"
	"fastiot/internal/genscreen"
)

// TestGeneratedScreenRunsIsolatedWithSimulatedDevice proves the whole path a
// deliverer takes: a stub model returns HTML, the preview locates every
// binding problem, the sandboxed frame is served with a strict policy, and
// writes during preview never reach the node's real value.
func TestGeneratedScreenRunsIsolatedWithSimulatedDevice(t *testing.T) {
	screen := new(string)
	stub := newScreenStub(t, screen)
	base, nc := startDeepSeekServer(t, "9841", "9840", deepseek.Credential{APIKey: "test-key", BaseURL: stub, Model: "stub-model"})
	token := loginAPI(t, base, "admin", "easyems-deepseek-test")
	root := snapshotRoot(t, base, token)
	levelID := createNodeAPI(t, base, token, "variable", root, "水箱液位", 42.5)

	*screen = "```html\n<html><head><style>body{font-family:sans-serif}</style></head>\n<body>\n" +
		`<div class="tank"><span data-fastiot-point="` + levelID + `" data-fastiot-format="number">--</span></div>` + "\n" +
		`<button data-fastiot-write="` + levelID + `" data-fastiot-value="1">启动</button>` + "\n" +
		`<span data-fastiot-point="missing-point">--</span>` + "\n" +
		"</body></html>\n```"

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
		Points     []genscreen.Point `json:"points"`
		Report     genscreen.Report  `json:"report"`
		Simulated  bool              `json:"simulated"`
	}
	code, raw = call(t, http.MethodPost, base+"/generated-screens/preview", token, map[string]any{
		"node_id": root, "source": generated.Content, "point_ids": []string{levelID},
	}, &preview)
	if code != http.StatusOK || !preview.Simulated || preview.ID == "" || preview.Capability == "" {
		t.Fatalf("preview = %d %s", code, raw)
	}
	if preview.Report.Ready {
		t.Fatalf("missing binding must not be ready: %+v", preview.Report)
	}
	finding, ok := findingOfCode(preview.Report.Findings, "binding_unknown")
	if !ok || finding.Line != 5 || finding.Element == "" || finding.Binding != "missing-point" {
		t.Fatalf("binding_unknown not located: %+v", preview.Report.Findings)
	}

	// The frame is served for the sandbox without a platform credential and
	// carries a policy that forbids network, evaluation and framing by others.
	frameURL := strings.TrimSuffix(base, "/api") + preview.FrameURL
	response, err := http.Get(frameURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("frame = %d %s", response.StatusCode, body)
	}
	policy := response.Header.Get("Content-Security-Policy")
	for _, want := range []string{"connect-src 'none'", "script-src 'nonce-", "frame-ancestors 'self'", "default-src 'none'"} {
		if !strings.Contains(policy, want) {
			t.Fatalf("frame policy missing %q: %s", want, policy)
		}
	}
	if response.Header.Get("X-Frame-Options") != "" {
		t.Fatalf("frame can never be embedded: X-Frame-Options = %s", response.Header.Get("X-Frame-Options"))
	}
	markup := string(body)
	for _, want := range []string{"data-fastiot-runtime", `data-fastiot-el="e`, levelID, "missing-point"} {
		if !strings.Contains(markup, want) {
			t.Fatalf("frame missing %q", want)
		}
	}
	broken := strings.Replace(frameURL, preview.Capability, strings.Repeat("ab", 32), 1)
	response, err = http.Get(broken)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("frame with a foreign capability = %d", response.StatusCode)
	}

	var readings struct {
		Values    map[string]genscreen.Value `json:"values"`
		Simulated bool                       `json:"simulated"`
	}
	code, raw = call(t, http.MethodPost, base+"/generated-screens/"+preview.ID+"/read", token, map[string]any{
		"ids": []string{levelID, "missing-point"},
	}, &readings)
	if code != http.StatusOK || !readings.Simulated {
		t.Fatalf("read = %d %s", code, raw)
	}
	if _, ok := readings.Values["missing-point"]; ok {
		t.Fatalf("read served a point outside the catalog: %s", raw)
	}
	reading, ok := readings.Values[levelID]
	if !ok || !reading.Simulated || reading.Quality != genscreen.QualityGood || reading.Time.IsZero() {
		t.Fatalf("reading = %+v", reading)
	}

	var written struct {
		Status string               `json:"status"`
		Value  genscreen.Value      `json:"value"`
		Entry  genscreen.WriteEntry `json:"entry"`
	}
	code, raw = call(t, http.MethodPost, base+"/generated-screens/"+preview.ID+"/write", token, map[string]any{
		"point_id": levelID, "value": 7,
	}, &written)
	if code != http.StatusOK || written.Status != genscreen.WriteSimulated || written.Entry.Point != levelID {
		t.Fatalf("write = %d %s", code, raw)
	}
	code, raw = call(t, http.MethodPost, base+"/generated-screens/"+preview.ID+"/write", token, map[string]any{
		"point_id": "missing-point", "value": 7,
	}, nil)
	if code != http.StatusConflict {
		t.Fatalf("write outside the preview = %d %s", code, raw)
	}

	// The real point still holds the value it had before the preview write.
	stored, err := client.GetNodes(nc, "all", levelID, "", false)
	if err != nil || len(stored) == 0 {
		t.Fatalf("stored point unreadable: %v", err)
	}
	value, ok := stored[0].Points.Value(data.PointTypeValue, "")
	if !ok || value != 42.5 {
		t.Fatalf("preview write reached the real point: value = %v ok=%v", value, ok)
	}

	// A browser-reported runtime error is located back to an element.
	var checked struct {
		Report genscreen.Report `json:"report"`
	}
	code, raw = call(t, http.MethodPost, base+"/generated-screens/"+preview.ID+"/diagnostics", token, map[string]any{
		"findings": []map[string]any{{
			"code": "runtime_error", "origin": "runtime", "severity": "blocking",
			"message": "level is not defined", "line": 5, "column": 3,
		}},
	}, &checked)
	if code != http.StatusOK {
		t.Fatalf("diagnostics = %d %s", code, raw)
	}
	runtime, ok := findingOfCode(checked.Report.Findings, "runtime_error")
	if !ok || runtime.Element == "" || runtime.Selector == "" || runtime.Line != 5 {
		t.Fatalf("runtime error not located: %+v", checked.Report.Findings)
	}

	code, raw = call(t, http.MethodPost, base+"/generated-screens/"+preview.ID+"/confirm", token, map[string]any{}, nil)
	if code != http.StatusConflict || !strings.Contains(raw, "checks_failed") {
		t.Fatalf("confirm with blocking findings = %d %s", code, raw)
	}

	// A clean screen still cannot be confirmed before the browser checks ran.
	cleanSource := "<html><head></head><body>\n<span data-fastiot-point=\"" + levelID + "\"></span>\n</body></html>"
	var clean struct {
		ID     string           `json:"id"`
		Report genscreen.Report `json:"report"`
	}
	code, raw = call(t, http.MethodPost, base+"/generated-screens/preview", token, map[string]any{
		"node_id": root, "source": cleanSource, "point_ids": []string{levelID},
	}, &clean)
	if code != http.StatusOK || !clean.Report.Ready {
		t.Fatalf("clean preview = %d %s", code, raw)
	}
	code, raw = call(t, http.MethodPost, base+"/generated-screens/"+clean.ID+"/confirm", token, map[string]any{}, nil)
	if code != http.StatusConflict || !strings.Contains(raw, "checks_required") {
		t.Fatalf("confirm without checks = %d %s", code, raw)
	}
	code, raw = call(t, http.MethodPost, base+"/generated-screens/"+clean.ID+"/diagnostics", token, map[string]any{
		"findings": []map[string]any{},
	}, &checked)
	if code != http.StatusOK || !checked.Report.Ready {
		t.Fatalf("clean diagnostics = %d %s", code, raw)
	}
	var confirmed struct {
		Confirmed bool   `json:"confirmed"`
		NodeID    string `json:"node_id"`
	}
	code, raw = call(t, http.MethodPost, base+"/generated-screens/"+clean.ID+"/confirm", token, map[string]any{}, &confirmed)
	if code != http.StatusOK || !confirmed.Confirmed || confirmed.NodeID != root {
		t.Fatalf("confirm = %d %s", code, raw)
	}
	nodes, err := client.GetNodes(nc, "all", root, "", false)
	if err != nil || len(nodes) == 0 {
		t.Fatalf("root unreadable: %v", err)
	}
	draft, ok := nodes[0].Points.Find(generatedScreenDraftType, "")
	if !ok || !strings.Contains(draft.Txt(), levelID) {
		t.Fatalf("confirmed screen draft not stored on the node: %+v", nodes[0].Points)
	}
}

func TestGeneratedScreenPreviewRequiresConfigureAndScope(t *testing.T) {
	base, _ := startDeepSeekServer(t, "9851", "9850", deepseek.Credential{})
	admin := loginAPI(t, base, "admin", "easyems-deepseek-test")
	root := snapshotRoot(t, base, admin)
	levelID := createNodeAPI(t, base, admin, "variable", root, "水箱液位", 1)
	fixture := &pointConfigFixture{base: base, token: admin}
	viewer := inputAccount(t, fixture, root, "viewer", nil)

	body := map[string]any{"node_id": root, "source": "<div></div>", "point_ids": []string{levelID}}
	code, raw := call(t, http.MethodPost, base+"/generated-screens/preview", viewer, body, nil)
	if code != http.StatusForbidden {
		t.Fatalf("viewer preview = %d %s", code, raw)
	}
	outside := map[string]any{"node_id": data.NewID(), "source": "<div></div>", "point_ids": []string{levelID}}
	code, raw = call(t, http.MethodPost, base+"/generated-screens/preview", admin, outside, nil)
	if code != http.StatusForbidden {
		t.Fatalf("out-of-scope preview = %d %s", code, raw)
	}
}

// newScreenStub is a model endpoint whose reply is set after the product
// server starts, so the screen can bind a point created through the API.
func newScreenStub(t *testing.T, content *string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": *content}}},
		})
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func findingOfCode(findings []genscreen.Finding, code string) (genscreen.Finding, bool) {
	for _, finding := range findings {
		if finding.Code == code {
			return finding, true
		}
	}
	return genscreen.Finding{}, false
}
