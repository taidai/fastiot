package genscreen

import (
	"crypto/sha256"
	"encoding/hex"
)

// CanonicalNonce is the fixed nonce a build is rendered with. A served frame
// gets a fresh nonce per response, so the artifact needs its own to stay
// comparable across instances.
const CanonicalNonce = "0"

// Build pins every input that determines a frame besides the screen source:
// the trusted bridge and, when the screen asks for it, the bundled Three.js
// release. An instance carrying different bytes would build a different frame,
// so an import refuses the package instead of degrading.
type Build struct {
	Runtime      string `json:"runtime"`
	ThreeVersion string `json:"three_version,omitempty"`
	ThreeSHA256  string `json:"three_sha256,omitempty"`
}

var runtimeSHA256 = digest(RuntimeScript)

// BuildFor reports the build inputs the source needs.
func BuildFor(source string) Build {
	build := Build{Runtime: runtimeSHA256}
	if Parse(source).UsesThree() {
		build.ThreeVersion = ThreeVersion
		build.ThreeSHA256 = ThreeSHA256
	}
	return build
}

// Fingerprint is the digest of the frame the source builds at CanonicalNonce.
func Fingerprint(source string) string {
	return digest(Parse(source).Render(CanonicalNonce))
}

func digest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
