package genscreen

import (
	"strings"
	"testing"
)

func TestExtractTakesTheDocumentOutOfAModelReply(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"plain", "<html><body>hi</body></html>", "<html><body>hi</body></html>"},
		{"html fence", "```html\n<html><body>hi</body></html>\n```", "<html><body>hi</body></html>"},
		{"bare fence", "```\n<div>hi</div>\n```", "<div>hi</div>"},
		{"chatter around", "这是画面：\n```html\n<div>hi</div>\n```\n如需修改请告诉我。", "<div>hi</div>"},
		{"doctype kept", "<!doctype html>\n<div>hi</div>", "<!doctype html>\n<div>hi</div>"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := Extract(test.content); got != test.want {
				t.Fatalf("Extract = %q, want %q", got, test.want)
			}
		})
	}
}

func TestExtractRejectsEmptyReply(t *testing.T) {
	if got := Extract("   \n```html\n\n```\n"); got != "" {
		t.Fatalf("Extract empty = %q", got)
	}
}

func TestValidateLocatesEveryBindingProblem(t *testing.T) {
	source := strings.Join([]string{
		"<html><head><title>泵房</title></head>",
		"<body>",
		"<h1>泵房画面</h1>",
		`<span data-fastiot-point="level">--</span>`,
		`<span data-fastiot-point="missing">--</span>`,
		`<span data-fastiot-point="elsewhere">--</span>`,
		`<span data-fastiot-point="other">--</span>`,
		`<button data-fastiot-write="level" data-fastiot-value="1">启动</button>`,
		`<button data-fastiot-write="pump" data-fastiot-value="abc">启动</button>`,
		`<div data-fastiot-write="pump" data-fastiot-value="1">启动</div>`,
		"</body></html>",
	}, "\n")
	resolve := func(id string) (Point, Status) {
		switch id {
		case "level":
			return Point{ID: id, Name: "水箱液位", Kind: "number", Unit: "%", Writable: false}, PointOK
		case "pump":
			return Point{ID: id, Name: "1#泵", Kind: "number", Writable: true}, PointOK
		case "missing":
			return Point{}, PointMissing
		case "elsewhere":
			return Point{}, PointOutOfScope
		case "other":
			return Point{ID: id, Name: "其它"}, PointNotSelected
		}
		return Point{}, PointMissing
	}

	report := Parse(source).Validate(resolve)
	if report.Ready {
		t.Fatal("a screen with blocking findings must not be ready")
	}

	expect := []struct {
		code     string
		line     int
		element  string
		binding  string
		severity string
	}{
		{"binding_unknown", 5, "e6", "missing", SeverityBlocking},
		{"binding_out_of_scope", 6, "e7", "elsewhere", SeverityBlocking},
		{"binding_not_selected", 7, "e8", "other", SeverityBlocking},
		{"write_read_only", 8, "e9", "level", SeverityBlocking},
		{"write_value_invalid", 9, "e10", "pump", SeverityBlocking},
		{"write_element_not_button", 10, "e11", "pump", SeverityWarning},
	}
	for _, want := range expect {
		finding, ok := findCode(report.Findings, want.code)
		if !ok {
			t.Fatalf("missing finding %s in %+v", want.code, report.Findings)
		}
		if finding.Line != want.line || finding.Element != want.element || finding.Binding != want.binding || finding.Severity != want.severity {
			t.Fatalf("%s = line %d element %q binding %q severity %q, want %d/%q/%q/%q",
				want.code, finding.Line, finding.Element, finding.Binding, finding.Severity,
				want.line, want.element, want.binding, want.severity)
		}
		if finding.Column == 0 || finding.Message == "" {
			t.Fatalf("%s must carry a column and a message: %+v", want.code, finding)
		}
	}
	for _, finding := range report.Findings {
		if finding.Origin != OriginStatic || finding.Element == "" {
			t.Fatalf("static finding must be located: %+v", finding)
		}
	}
}

func TestValidateAcceptsACleanScreen(t *testing.T) {
	source := `<html><head><title>x</title></head><body>
<span data-fastiot-point="level"></span>
<button data-fastiot-write="pump" data-fastiot-value="1">启动</button>
<script>document.querySelectorAll("[data-fastiot-point]").forEach(function (node) { node.textContent = "" })</script>
</body></html>`
	resolve := func(id string) (Point, Status) {
		if id == "pump" {
			return Point{ID: id, Kind: "number", Writable: true}, PointOK
		}
		return Point{ID: id, Kind: "number"}, PointOK
	}
	report := Parse(source).Validate(resolve)
	if !report.Ready || len(report.Findings) != 0 {
		t.Fatalf("clean screen = ready %v findings %+v", report.Ready, report.Findings)
	}
}

func TestValidateFlagsConstructsTheSandboxForbids(t *testing.T) {
	source := `<html><body>
<script>
const raw = eval("1+1")
fetch("/api/nodes")
localStorage.setItem("x", "1")
window.parent.postMessage("x", "*")
</script>
</body></html>`
	report := Parse(source).Validate(func(id string) (Point, Status) { return Point{}, PointMissing })
	for _, line := range []int{3, 4, 5, 6} {
		found := false
		for _, finding := range report.Findings {
			if finding.Code == "forbidden_api" && finding.Line == line && finding.Element == "e2" {
				found = true
			}
		}
		if !found {
			t.Fatalf("no forbidden_api finding on line %d: %+v", line, report.Findings)
		}
	}
	if report.Ready {
		t.Fatal("forbidden API use must block preview confirmation")
	}
}

func TestValidateIgnoresNonScriptDataBlocks(t *testing.T) {
	source := `<html><body><script type="application/json">{"note":"fetch( is only text"}</script></body></html>`
	report := Parse(source).Validate(func(id string) (Point, Status) { return Point{}, PointMissing })
	if len(report.Findings) != 0 || !report.Ready {
		t.Fatalf("JSON data block was treated as code: %+v", report.Findings)
	}
}

func TestValidateRejectsExternalResources(t *testing.T) {
	source := `<html><body>
<img src="https://cdn.example.com/logo.png"/>
<img src="data:image/png;base64,AAAA"/>
<a href="#detail">详情</a>
<link rel="stylesheet" href="https://example.com/a.css"/>
</body></html>`
	report := Parse(source).Validate(func(id string) (Point, Status) { return Point{}, PointMissing })
	want := map[int]bool{2: true, 5: true}
	for _, finding := range report.Findings {
		if finding.Code != "external_resource" {
			t.Fatalf("unexpected finding %+v", finding)
		}
		if !want[finding.Line] {
			t.Fatalf("external_resource on unexpected line %d", finding.Line)
		}
		delete(want, finding.Line)
	}
	if len(want) != 0 {
		t.Fatalf("external resources not reported on lines %v", want)
	}
}

func TestValidateRejectsMetaRefresh(t *testing.T) {
	source := `<html><head><meta http-equiv="refresh" content="0;url=https://example.com"></head><body></body></html>`
	report := Parse(source).Validate(func(id string) (Point, Status) { return Point{}, PointMissing })
	if len(report.Findings) != 1 || report.Findings[0].Code != "external_resource" {
		t.Fatalf("meta refresh findings = %+v", report.Findings)
	}
}

func TestValidateRejectsInlineEventHandlers(t *testing.T) {
	source := `<html><body><button onclick="start()">启动</button></body></html>`
	report := Parse(source).Validate(func(id string) (Point, Status) { return Point{}, PointMissing })
	if len(report.Findings) != 1 || report.Findings[0].Code != "inline_handler" || report.Findings[0].Line != 1 {
		t.Fatalf("inline handler findings = %+v", report.Findings)
	}
}

func TestValidateReportsMissingWriteValue(t *testing.T) {
	source := `<html><body><button data-fastiot-write="pump">启动</button></body></html>`
	report := Parse(source).Validate(func(id string) (Point, Status) {
		return Point{ID: id, Kind: "number", Writable: true}, PointOK
	})
	if len(report.Findings) != 1 || report.Findings[0].Code != "write_value_invalid" {
		t.Fatalf("missing value findings = %+v", report.Findings)
	}
}

func TestValidateBoundsBindingCount(t *testing.T) {
	var source strings.Builder
	for i := 0; i < maxBindings+20; i++ {
		source.WriteString(`<span data-fastiot-point="p"></span>`)
	}
	report := Parse(source.String()).Validate(func(id string) (Point, Status) {
		return Point{ID: id, Kind: "number"}, PointOK
	})
	if _, ok := findCode(report.Findings, "too_many_bindings"); !ok {
		t.Fatalf("binding cap not reported: %+v", report.Findings)
	}
	if report.Ready {
		t.Fatal("an oversized screen must not be ready")
	}
}

func findCode(findings []Finding, code string) (Finding, bool) {
	for _, finding := range findings {
		if finding.Code == code {
			return finding, true
		}
	}
	return Finding{}, false
}
