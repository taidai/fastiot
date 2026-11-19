package deepseek

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func revisionRequest() Request {
	req := cleanRequest()
	req.Mode = ModeRevise
	req.Instruction = "在现有画面上把水泵卡片改成红色"
	req.Context = `<div id="tank" class="card"><h1>泵房</h1><span data-fastiot-point="level-id">--</span></div>`
	return req
}

// TestRevisionManifestShowsRegionsWithoutPixels proves an annotated screenshot
// is reviewed like any other: regions are listed for the user, their notes are
// scanned, and the image itself never leaves.
func TestRevisionManifestShowsRegionsWithoutPixels(t *testing.T) {
	png := pngWithText("Note", "export pressure")
	req := revisionRequest()
	req.Screenshots = []Screenshot{{
		Name: "标注.png", MediaType: "image/png", Data: base64.StdEncoding.EncodeToString(png),
		Regions: []Region{{X: 0.25, Y: 0.5, Note: "右上角卡片改成红色"}}, Reviewed: true,
	}}
	manifest := BuildManifest(req)
	if manifest.Mode != ModeRevise {
		t.Fatalf("mode = %q", manifest.Mode)
	}
	if manifest.Blocked {
		t.Fatalf("clean revision blocked: %+v", manifest.BlockReasons)
	}
	summary := manifest.Screenshots[0]
	if summary.SendsPixels || len(summary.Regions) != 1 || summary.Regions[0].Note != "右上角卡片改成红色" {
		t.Fatalf("screenshot summary = %+v", summary)
	}
	raw, err := json.Marshal(MessagesForManifest(manifest))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{"现有画面 HTML", `div id=\"tank\"`, "右上角卡片改成红色", "25%", "50%"} {
		if !strings.Contains(text, want) {
			t.Fatalf("revise payload missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, base64.StdEncoding.EncodeToString(png)) {
		t.Fatal("screenshot pixels must never leave")
	}
}

// TestRevisionRegionNoteIsScanned proves a note on an annotated region passes
// the same sensitive-content check as any other outbound text.
func TestRevisionRegionNoteIsScanned(t *testing.T) {
	req := revisionRequest()
	req.Screenshots = []Screenshot{{
		Name: "标注.png", MediaType: "image/png",
		Regions: []Region{{X: 0.5, Y: 0.5, Note: "把 10.0.0.5 那张卡片放大"}}, Reviewed: true,
	}}
	manifest := BuildManifest(req)
	if !manifest.Blocked {
		t.Fatal("region note with an address must block")
	}
	found := false
	for _, finding := range manifest.Findings {
		if finding.Category == "screenshot" && finding.Field == "region" && finding.Kind == "ip_address" {
			found = true
		}
	}
	if !found {
		t.Fatalf("region finding missing: %+v", manifest.Findings)
	}
}

// TestInvalidRegionBlocks keeps malformed annotation geometry from reaching
// the model or the screen.
func TestInvalidRegionBlocks(t *testing.T) {
	req := revisionRequest()
	req.Screenshots = []Screenshot{{
		Name: "标注.png", MediaType: "image/png",
		Regions: []Region{{X: 1.5, Y: 0.2, Note: "把卡片放大"}}, Reviewed: true,
	}}
	manifest := BuildManifest(req)
	if !manifest.Blocked || !contains(manifest.BlockReasons, "screenshot_annotation_invalid") {
		t.Fatalf("invalid region must block: %+v", manifest.BlockReasons)
	}
}

// TestGenerateModeKeepsGenerationPrompt guards the first-generation wording:
// revision must not silently change the existing flow.
func TestGenerateModeKeepsGenerationPrompt(t *testing.T) {
	manifest := BuildManifest(cleanRequest())
	raw, err := json.Marshal(MessagesForManifest(manifest))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "现有画面 HTML") {
		t.Fatalf("generate prompt must not ask for a revision: %s", raw)
	}
}
