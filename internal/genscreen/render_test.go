package genscreen

import (
	"regexp"
	"strings"
	"testing"
)

const testNonce = "0123456789abcdef0123456789abcdef"

var elementMarkers = regexp.MustCompile(`data-fastiot-el="e[0-9]+"`)

func TestRenderMarksEveryElementAndScript(t *testing.T) {
	source := `<html><head><title>泵房</title><script>window.x = 1</script></head>
<body><div class="grid"><img src="data:image/png;base64,AA" /><span data-fastiot-point="level">--</span></div></body></html>`
	out := Parse(source).Render(testNonce)

	if got := len(elementMarkers.FindAllString(out, -1)); got != 8 {
		t.Fatalf("expected a marker on each of the 8 elements, got %d:\n%s", got, out)
	}
	if !strings.Contains(out, `<script data-fastiot-runtime="1" nonce="`+testNonce+`">`) {
		t.Fatalf("runtime script missing or lacks the nonce:\n%s", out)
	}
	if !strings.Contains(out, `<script nonce="`+testNonce+`"`) || !strings.Contains(out, `>window.x = 1</script>`) {
		t.Fatalf("model script did not receive the nonce:\n%s", out)
	}
	if !strings.Contains(out, `<img src="data:image/png;base64,AA" data-fastiot-el="e6"/>`) {
		t.Fatalf("self-closing tag marker broke the tag:\n%s", out)
	}
	if strings.Index(out, "data-fastiot-runtime") > strings.Index(out, "<title") {
		t.Fatalf("runtime must load before the screen's own scripts:\n%s", out)
	}
	if !strings.Contains(out, "泵房画面") && !strings.Contains(out, "泵房") {
		t.Fatalf("original content lost:\n%s", out)
	}
}

func TestRenderWithoutHeadStillLoadsTheRuntimeFirst(t *testing.T) {
	out := Parse(`<div>hi</div>`).Render(testNonce)
	if !strings.HasPrefix(out, `<script data-fastiot-runtime="1" nonce="`) {
		t.Fatalf("runtime must lead a plain fragment:\n%s", out)
	}
	if !strings.Contains(out, `<div data-fastiot-el="e0">hi</div>`) {
		t.Fatalf("fragment content changed:\n%s", out)
	}
}

func TestRenderRejectsANonceThatCouldBreakOut(t *testing.T) {
	out := Parse(`<div>hi</div>`).Render(`" onload="alert(1)`)
	if strings.Contains(out, `onload=`) {
		t.Fatalf("nonce was not escaped:\n%s", out)
	}
}

func TestRenderKeepsModelScriptTextVerbatim(t *testing.T) {
	source := `<html><body><script>const tpl = "<span data-fastiot-point='nope'>"</script></body></html>`
	out := Parse(source).Render(testNonce)
	if !strings.Contains(out, `const tpl = "<span data-fastiot-point='nope'>"`) {
		t.Fatalf("script text was rewritten:\n%s", out)
	}
}

func TestRenderKeepsTheModelLineNumbers(t *testing.T) {
	source := "<html>\n<head>\n<title>x</title>\n</head>\n<body>\n<span data-fastiot-point=\"level\"></span>\n</body>\n</html>"
	out := Parse(source).Render(testNonce)
	if strings.Count(out, "\n") != strings.Count(source, "\n") {
		t.Fatalf("render changed the line count:\n%s", out)
	}
	if strings.Contains(compactScript(RuntimeScript), "\n") {
		t.Fatal("the embedded runtime must be compacted before serving")
	}
}

func TestAttachLocationsFindsTheElementAtAnErrorLine(t *testing.T) {
	source := "<html><body>\n<div data-fastiot-el=\"\">x</div>\n<script>throw new Error(\"boom\")</script>\n</body></html>"
	document := Parse(source)
	findings := document.AttachLocations([]Finding{
		{Code: "runtime_error", Origin: OriginRuntime, Severity: SeverityBlocking, Message: "boom", Line: 3, Column: 8},
		{Code: "layout_overflow", Origin: OriginLayout, Severity: SeverityBlocking, Message: "wide", Element: "e2", Selector: "body > div"},
	})
	if findings[0].Element != "e3" || findings[0].Selector == "" {
		t.Fatalf("runtime error not located: %+v", findings[0])
	}
	if findings[1].Element != "e2" {
		t.Fatalf("existing location overwritten: %+v", findings[1])
	}
}

func TestContentSecurityPolicyIsStrict(t *testing.T) {
	policy := ContentSecurityPolicy(testNonce)
	for _, want := range []string{
		"default-src 'none'",
		"script-src 'nonce-" + testNonce + "'",
		"connect-src 'none'",
		"object-src 'none'",
		"frame-src 'none'",
		"form-action 'none'",
		"base-uri 'none'",
		"frame-ancestors 'self'",
		"style-src 'unsafe-inline'",
		"img-src data:",
	} {
		if !strings.Contains(policy, want) {
			t.Fatalf("policy missing %q: %s", want, policy)
		}
	}
	if strings.Contains(policy, "unsafe-eval") {
		t.Fatalf("policy allows eval: %s", policy)
	}
	if strings.Contains(policy, "script-src 'unsafe-inline'") {
		t.Fatalf("policy allows inline script: %s", policy)
	}
	if strings.Contains(policy, "connect-src 'self'") {
		t.Fatalf("policy allows platform connections: %s", policy)
	}
}

func TestRuntimeScriptImplementsTheBridge(t *testing.T) {
	for _, want := range []string{
		`"fastiot:init"`,
		`"fastiot:ready"`,
		`"fastiot:values"`,
		`"fastiot:write"`,
		`"fastiot:write-result"`,
		`"fastiot:error"`,
		`"fastiot:findings"`,
		`"fastiot:check"`,
		`"fastiot:locate"`,
		`data-fastiot-point`,
		`data-fastiot-write`,
		`data-fastiot-el`,
		`addEventListener("error"`,
		`addEventListener("unhandledrejection"`,
		`scrollWidth`,
		`getBoundingClientRect`,
	} {
		if !strings.Contains(RuntimeScript, want) {
			t.Fatalf("runtime script missing %q", want)
		}
	}
	for _, forbidden := range []string{"eval(", "new Function", "fetch(", "localStorage", "document.cookie"} {
		if strings.Contains(RuntimeScript, forbidden) {
			t.Fatalf("trusted runtime contains forbidden construct %q", forbidden)
		}
	}
}
