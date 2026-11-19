package genscreen

import (
	_ "embed"
	"regexp"
)

// ThreeScript is the Three.js build shipped with the platform. It is vendored
// from the npm tarball three@0.159.0 (build/three.min.js), the last release
// that still publishes a classic global build, and its bytes are pinned by
// ThreeSHA256. A preview frame carries it inline: no CDN, no network, and the
// same response nonce as every other script.
//
//go:embed three.min.js
var ThreeScript string

const (
	// ThreeVersion is the npm release the embedded build comes from.
	ThreeVersion = "0.159.0"
	// ThreeSHA256 pins the exact vendored bytes.
	ThreeSHA256 = "7b1c5d75b28d9de15042e2b374f83566d8c7146697af8fdeb4558b0fb528a585"
)

// ThreeInline is the bundle as a frame carries it: compacted to one line so
// the dependency does not shift the line numbers findings report against the
// model's own source.
var ThreeInline = compactScript(ThreeScript)

var threeReference = regexp.MustCompile(`\bTHREE\b`)

// UsesThree reports whether the generated scripts rely on the bundled Three.js
// global. Only those screens pay for the dependency.
func (d *Document) UsesThree() bool {
	for _, script := range d.Scripts {
		if isJavaScript(script.Element) && threeReference.MatchString(script.Text) {
			return true
		}
	}
	return false
}
