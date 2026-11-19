package genscreen

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// TestFrameArtifactIsDeterministic pins the build: the same source produces
// the same fingerprint every time, a changed byte changes it, and the
// fingerprint is the digest of the frame at the canonical nonce rather than of
// the source text alone.
func TestFrameArtifactIsDeterministic(t *testing.T) {
	source := `<html><head></head><body><span data-fastiot-point="level">--</span></body></html>`
	first := Fingerprint(source)
	if first == "" || Fingerprint(source) != first {
		t.Fatal("screen fingerprint is not stable")
	}
	if Fingerprint(source+" ") == first {
		t.Fatal("screen fingerprint does not track the source")
	}
	frame := Parse(source).Render(CanonicalNonce)
	sum := sha256.Sum256([]byte(frame))
	if first != hex.EncodeToString(sum[:]) {
		t.Fatalf("fingerprint is not the digest of the canonical frame")
	}
	if !strings.Contains(frame, `nonce="`+CanonicalNonce+`"`) {
		t.Fatal("the canonical frame does not carry the canonical nonce")
	}
}

// TestBuildPinsThePiecesAFrameNeeds keeps the artifact honest: it names the
// exact bridge and, only for 3D screens, the exact bundled Three.js release.
func TestBuildPinsThePiecesAFrameNeeds(t *testing.T) {
	twoD := BuildFor(`<html><body><span data-fastiot-point="p">--</span></body></html>`)
	runtime := sha256.Sum256([]byte(RuntimeScript))
	if twoD.Runtime != hex.EncodeToString(runtime[:]) {
		t.Fatalf("runtime pin = %q, want the embedded bridge bytes", twoD.Runtime)
	}
	if twoD.ThreeVersion != "" || twoD.ThreeSHA256 != "" {
		t.Fatalf("a 2D screen pinned a 3D dependency: %#v", twoD)
	}
	three := BuildFor(threeScreen)
	if three.Runtime != twoD.Runtime || three.ThreeVersion != ThreeVersion || three.ThreeSHA256 != ThreeSHA256 {
		t.Fatalf("3D screen build = %#v", three)
	}
	if three == twoD {
		t.Fatal("a 3D screen must build differently from a 2D one")
	}
}
