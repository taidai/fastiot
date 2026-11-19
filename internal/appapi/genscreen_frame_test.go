package appapi

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"fastiot/internal/genscreen"
)

var policyNonce = regexp.MustCompile(`script-src 'nonce-([A-Za-z0-9]+)'`)

// frameFor serves one preview frame straight from the store, so this test
// covers the exact response a sandboxed iframe receives without a live site.
func frameFor(t *testing.T, source string) (*httptest.ResponseRecorder, *genscreen.Session) {
	t.Helper()
	store := genscreen.NewStore(4, time.Hour)
	session := store.Put(&genscreen.Session{Owner: "user-1", NodeID: "node-1", Source: source})
	handler := &Handler{generatedScreens: store}
	request := httptest.NewRequest(http.MethodGet, "/api/generated-screens/"+session.ID+"/frame?cap="+session.Capability, nil)
	recorder := httptest.NewRecorder()
	handler.generatedScreenFrame(recorder, request, session.ID)
	return recorder, session
}

// TestGeneratedScreenFrameShipsThreeInlineAndIsolated proves the 3D frame is
// self-contained: the pinned bundle is inlined with the response nonce, the
// policy is unchanged, and the document references no external script or CDN.
func TestGeneratedScreenFrameShipsThreeInlineAndIsolated(t *testing.T) {
	recorder, _ := frameFor(t, `<html><head><title>储能舱</title></head>
<body><canvas id="c"></canvas><script>const scene = new THREE.Scene();</script></body></html>`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("frame = %d %s", recorder.Code, recorder.Body.String())
	}
	header := recorder.Header()
	match := policyNonce.FindStringSubmatch(header.Get("Content-Security-Policy"))
	if match == nil {
		t.Fatalf("frame policy has no response nonce: %s", header.Get("Content-Security-Policy"))
	}
	nonce := match[1]
	if got, want := header.Get("Content-Security-Policy"), genscreen.ContentSecurityPolicy(nonce); got != want {
		t.Fatalf("policy = %q, want %q", got, want)
	}
	if header.Get("X-Frame-Options") != "" {
		t.Fatalf("the isolated frame must be embeddable: X-Frame-Options = %s", header.Get("X-Frame-Options"))
	}
	if header.Get("Cross-Origin-Resource-Policy") != "same-origin" || header.Get("Cache-Control") != "no-store" {
		t.Fatalf("frame headers = %+v", header)
	}

	body := recorder.Body.String()
	if !strings.Contains(body, `<script data-fastiot-three="`+genscreen.ThreeVersion+`" nonce="`+nonce+`">`) {
		t.Fatal("the frame does not carry the bundled Three.js with the response nonce")
	}
	tail := genscreen.ThreeInline[len(genscreen.ThreeInline)-200:]
	if !strings.Contains(body, tail) {
		t.Fatal("the inlined Three.js bundle is truncated")
	}
	for _, banned := range []string{"<script src", "@import", "url(http", "url(//"} {
		if strings.Contains(body, banned) {
			t.Fatalf("the frame references a loadable external resource: %s", banned)
		}
	}
	if !strings.Contains(body, "data-fastiot-runtime") {
		t.Fatal("frame lost its trusted bridge")
	}
}

func TestGeneratedScreenFrameSkipsThreeForA2DScreen(t *testing.T) {
	recorder, _ := frameFor(t, `<html><body><span data-fastiot-point="p">--</span></body></html>`)
	if strings.Contains(recorder.Body.String(), "data-fastiot-three") {
		t.Fatal("a 2D frame must not carry the 3D bundle")
	}
}

func TestGeneratedScreenFrameRejectsAForeignCapability(t *testing.T) {
	store := genscreen.NewStore(4, time.Hour)
	session := store.Put(&genscreen.Session{Owner: "user-1", NodeID: "node-1", Source: "<html><body>hi</body></html>"})
	handler := &Handler{generatedScreens: store}
	request := httptest.NewRequest(http.MethodGet, "/api/generated-screens/"+session.ID+"/frame?cap="+strings.Repeat("ab", 32), nil)
	recorder := httptest.NewRecorder()
	handler.generatedScreenFrame(recorder, request, session.ID)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("frame with a foreign capability = %d", recorder.Code)
	}
}

func TestGeneratedScreenDraftRecordsThePinnedThree(t *testing.T) {
	now := time.Now()
	three := generatedScreenDraftBody(
		&genscreen.Session{NodeID: "node-1", Source: `<html><script>new THREE.Scene();</script></html>`},
		genscreen.Report{}, now,
	)
	meta, ok := three["three"].(map[string]string)
	if !ok || meta["version"] != genscreen.ThreeVersion || meta["sha256"] != genscreen.ThreeSHA256 {
		t.Fatalf("a 3D draft must pin the runtime it was checked against: %+v", three["three"])
	}
	plain := generatedScreenDraftBody(
		&genscreen.Session{NodeID: "node-1", Source: "<html><body>hi</body></html>"},
		genscreen.Report{}, now,
	)
	if _, ok := plain["three"]; ok {
		t.Fatal("a 2D draft must not claim a 3D dependency")
	}
}
