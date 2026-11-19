package genscreen

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// threeScreen is a small but complete 3D screen: a canvas, a scene built from
// the bundled global, and a live binding through the bridge event.
const threeScreen = `<html><head><title>储能舱</title></head>
<body>
<canvas id="scene" width="320" height="200"></canvas>
<span data-fastiot-point="level" data-fastiot-format="number">--</span>
<script>
const canvas = document.getElementById("scene");
const renderer = new THREE.WebGLRenderer({ canvas: canvas });
const scene = new THREE.Scene();
const camera = new THREE.PerspectiveCamera(50, 1.6, 0.1, 100);
const tank = new THREE.Mesh(new THREE.BoxGeometry(1, 1, 1), new THREE.MeshStandardMaterial({ color: 0x22c55e }));
scene.add(tank);
camera.position.z = 3;
renderer.render(scene, camera);
document.addEventListener("fastiot:values", function (event) {
  const level = event.detail.values.level;
  tank.position.y = Number(level.value) / 100;
});
</script>
</body></html>`

// TestEmbeddedThreeIsPinnedAndSafeToInline keeps the vendored dependency
// deterministic: the exact bytes are pinned, the licence travels with it, and
// the inline form cannot close its script tag or add line breaks.
func TestEmbeddedThreeIsPinnedAndSafeToInline(t *testing.T) {
	if ThreeScript == "" {
		t.Fatal("three.min.js is not embedded")
	}
	sum := sha256.Sum256([]byte(ThreeScript))
	if got := hex.EncodeToString(sum[:]); got != ThreeSHA256 {
		t.Fatalf("embedded Three.js changed to %s; vendor the file and its pin together", got)
	}
	if ThreeVersion != "0.159.0" {
		t.Fatalf("ThreeVersion = %q, want the vendored npm release", ThreeVersion)
	}
	for _, marker := range []string{"THREE", "WebGLRenderer", "REVISION", "SPDX-License-Identifier: MIT"} {
		if !strings.Contains(ThreeScript, marker) {
			t.Fatalf("embedded bundle is missing %q", marker)
		}
	}
	if strings.Contains(ThreeInline, "\n") {
		t.Fatal("the inline bundle must be one line so findings keep the model's line numbers")
	}
	if strings.Contains(ThreeInline, "</script") {
		t.Fatal("the inline bundle would close the injected script tag early")
	}
}

func TestRenderInjectsThreeOnlyForScreensThatUseIt(t *testing.T) {
	twoD := Parse(`<html><head></head><body><span data-fastiot-point="p">--</span></body></html>`).Render(testNonce)
	if strings.Contains(twoD, "data-fastiot-three") {
		t.Fatal("a 2D screen must not pay for the 3D bundle")
	}

	out := Parse(threeScreen).Render(testNonce)
	if !strings.Contains(out, `<script data-fastiot-three="`+ThreeVersion+`" nonce="`+testNonce+`">`) {
		t.Fatalf("the frame does not carry the local Three.js build")
	}
	if !strings.Contains(out, "REVISION") {
		t.Fatal("the injected bundle is incomplete")
	}
	if strings.Index(out, "data-fastiot-runtime") > strings.Index(out, "data-fastiot-three") {
		t.Fatal("the bridge must be installed before the 3D bundle")
	}
	if strings.Index(out, "data-fastiot-three") > strings.Index(out, "new THREE.WebGLRenderer") {
		t.Fatal("the bundle must load before the generated script runs")
	}
}

func TestThreeInjectionKeepsTheModelLineNumbers(t *testing.T) {
	out := Parse(threeScreen).Render(testNonce)
	if got, want := strings.Count(out, "\n"), strings.Count(threeScreen, "\n"); got != want {
		t.Fatalf("injection changed the line count (%d vs %d), so reported lines no longer match the model's source", got, want)
	}
}

// TestPolicyIsNotWidenedForThree pins the frame policy byte for byte: the
// bundled library is inline, so no source, CDN or network exception is needed.
func TestPolicyIsNotWidenedForThree(t *testing.T) {
	want := "default-src 'none'; script-src 'nonce-" + testNonce + "'; style-src 'unsafe-inline'; img-src data:; font-src data:; media-src data:; connect-src 'none'; object-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'"
	if got := ContentSecurityPolicy(testNonce); got != want {
		t.Fatalf("frame policy changed:\n got %s\nwant %s", got, want)
	}
}

func TestValidateAcceptsAThreeScreenAndStillBlocksAnExternalCdn(t *testing.T) {
	report := Parse(threeScreen).Validate(func(string) (Point, Status) {
		return Point{ID: "level", Name: "液位", Kind: "number"}, PointOK
	})
	if !report.Ready {
		t.Fatalf("a valid Three.js screen must pass the static checks: %+v", report.Findings)
	}

	cdn := `<html><body><script src="https://unpkg.com/three@0.159.0/build/three.min.js"></script></body></html>`
	report = Parse(cdn).Validate(func(string) (Point, Status) { return Point{}, PointMissing })
	if report.Ready {
		t.Fatal("an external CDN script must stay blocking")
	}
	found := false
	for _, finding := range report.Findings {
		if finding.Code == "external_resource" && finding.Severity == SeverityBlocking {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an external_resource finding: %+v", report.Findings)
	}
}
