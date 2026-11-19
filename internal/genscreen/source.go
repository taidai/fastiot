// Package genscreen turns the model's untrusted HTML reply into a previewable
// screen: it locates every binding and unsafe construct, renders a document
// with a trusted bridge, and keeps the preview's data inside a simulator.
package genscreen

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// Finding origins and severities. A blocking finding suppresses confirmation.
const (
	OriginStatic  = "static"
	OriginRuntime = "runtime"
	OriginLayout  = "layout"

	SeverityBlocking = "blocking"
	SeverityWarning  = "warning"
)

// Point is one device-model entry the screen may bind. Kind is "number" or
// "text"; Writable says whether a screen may request a change to it.
type Point struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Unit     string `json:"unit,omitempty"`
	Writable bool   `json:"writable"`
}

// Status is the outcome of resolving a bound point ID against the project.
type Status int

const (
	PointOK Status = iota
	PointMissing
	PointOutOfScope
	PointNotSelected
)

// Resolver answers whether a point exists, is visible to the caller, and was
// included in the generation request. It is called once per distinct ID.
type Resolver func(id string) (Point, Status)

// Finding is one located problem. Element is the marker the rendered document
// carries, so the preview can highlight the exact element.
type Finding struct {
	Code     string             `json:"code"`
	Origin   string             `json:"origin"`
	Severity string             `json:"severity"`
	Message  string             `json:"message"`
	Binding  string             `json:"binding,omitempty"`
	Element  string             `json:"element,omitempty"`
	Selector string             `json:"selector,omitempty"`
	Line     int                `json:"line,omitempty"`
	Column   int                `json:"column,omitempty"`
	Detail   map[string]float64 `json:"detail,omitempty"`
}

// Report is the merged check result. Ready means no blocking finding remains.
type Report struct {
	Findings []Finding `json:"findings"`
	Ready    bool      `json:"ready"`
	Blocking int       `json:"blocking"`
	Warnings int       `json:"warnings"`
}

// Element is one start tag of the generated document.
type Element struct {
	Marker   string
	Tag      string
	Attrs    map[string]string
	Selector string
	Line     int
	Column   int
	Start    int
	End      int
	// Text is the element's own text content, whitespace-collapsed. Script and
	// style bodies are captured separately.
	Text string
}

type chunk struct {
	Element Element
	Text    string
	Offset  int
}

// Document is the tokenized model source. Line and column are 1-based and
// point into exactly the text the model returned.
type Document struct {
	Source    string
	Elements  []Element
	Scripts   []chunk
	Styles    []chunk
	lineStart []int
}

var voidTags = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"param": true, "source": true, "track": true, "wbr": true,
}

// maxBindings bounds resolver work for one untrusted document.
const maxBindings = 200

// Extract returns the HTML document from a model reply. DeepSeek often wraps
// it in a fenced block with a sentence before and after.
func Extract(content string) string {
	source := strings.TrimSpace(content)
	open := strings.Index(source, "```")
	if open < 0 {
		return source
	}
	rest := source[open+3:]
	if newline := strings.IndexByte(rest, '\n'); newline >= 0 {
		language := strings.TrimSpace(rest[:newline])
		if !strings.Contains(language, "<") {
			rest = rest[newline+1:]
		}
	}
	if close := strings.Index(rest, "```"); close >= 0 {
		rest = rest[:close]
	}
	return strings.TrimSpace(rest)
}

// Parse tokenizes the source leniently. Malformed HTML still yields the tags
// that were written, which is what the checks need to report.
func Parse(source string) *Document {
	document := &Document{Source: source, lineStart: lineStarts(source)}
	z := html.NewTokenizer(strings.NewReader(source))
	type frame struct {
		tag     string
		counts  map[string]int
		path    string
		element int
	}
	var stack []frame
	offset := 0
	openScript, openStyle := -1, -1
	for {
		tokenType := z.Next()
		if tokenType == html.ErrorToken {
			break
		}
		raw := z.Raw()
		start := offset
		offset += len(raw)
		switch tokenType {
		case html.StartTagToken, html.SelfClosingTagToken:
			token := z.Token()
			element := Element{
				Tag:   strings.ToLower(token.Data),
				Attrs: map[string]string{},
				Start: start,
				End:   offset,
			}
			element.Line, element.Column = document.position(start)
			for _, attribute := range token.Attr {
				element.Attrs[strings.ToLower(attribute.Key)] = attribute.Val
			}
			parentPath := ""
			var parentCounts map[string]int
			if len(stack) > 0 {
				parentPath = stack[len(stack)-1].path
				parentCounts = stack[len(stack)-1].counts
			}
			label := element.Tag
			if id := strings.TrimSpace(element.Attrs["id"]); id != "" {
				label += "#" + id
			} else {
				count := 1
				if parentCounts != nil {
					count = parentCounts[element.Tag] + 1
				}
				label += ":nth-of-type(" + strconv.Itoa(count) + ")"
			}
			element.Selector = label
			if parentPath != "" {
				element.Selector = parentPath + " > " + label
			}
			element.Marker = "e" + strconv.Itoa(len(document.Elements))
			document.Elements = append(document.Elements, element)
			push := tokenType == html.StartTagToken && !voidTags[element.Tag]
			index := len(document.Elements) - 1
			if push {
				counts := map[string]int{}
				if len(stack) > 0 {
					for key, value := range stack[len(stack)-1].counts {
						counts[key] = value
					}
				}
				counts[element.Tag]++
				stack = append(stack, frame{tag: element.Tag, counts: counts, path: element.Selector, element: index})
			}
			if element.Tag == "script" && push {
				openScript = len(document.Elements) - 1
			}
			if element.Tag == "style" && push {
				openStyle = len(document.Elements) - 1
			}
		case html.EndTagToken:
			tag := strings.ToLower(z.Token().Data)
			for len(stack) > 0 {
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if top.tag == tag {
					break
				}
			}
			if tag == "script" {
				openScript = -1
			}
			if tag == "style" {
				openStyle = -1
			}
		case html.TextToken:
			text := string(z.Text())
			if openScript >= 0 && openScript < len(document.Elements) {
				document.Scripts = append(document.Scripts, chunk{Element: document.Elements[openScript], Text: text, Offset: start})
			} else if openStyle >= 0 && openStyle < len(document.Elements) {
				document.Styles = append(document.Styles, chunk{Element: document.Elements[openStyle], Text: text, Offset: start})
			} else if len(stack) > 0 {
				document.Elements[stack[len(stack)-1].element].Text += text
			}
		}
	}
	for index := range document.Elements {
		document.Elements[index].Text = strings.Join(strings.Fields(document.Elements[index].Text), " ")
	}
	return document
}

func lineStarts(source string) []int {
	starts := []int{0}
	for index := 0; index < len(source); index++ {
		if source[index] == '\n' {
			starts = append(starts, index+1)
		}
	}
	return starts
}

func (d *Document) position(offset int) (int, int) {
	line := sort.Search(len(d.lineStart), func(index int) bool { return d.lineStart[index] > offset })
	if line == 0 {
		line = 1
	}
	column := offset - d.lineStart[line-1] + 1
	return line, column
}

type resolved struct {
	point  Point
	status Status
}

var forbiddenCalls = []struct {
	name    string
	pattern *regexp.Regexp
}{
	{"eval", regexp.MustCompile(`\beval\s*\(`)},
	{"new Function", regexp.MustCompile(`\bnew\s+Function\s*\(`)},
	{"fetch", regexp.MustCompile(`\bfetch\s*\(`)},
	{"XMLHttpRequest", regexp.MustCompile(`\bXMLHttpRequest\b`)},
	{"WebSocket", regexp.MustCompile(`\bnew\s+WebSocket\b`)},
	{"sendBeacon", regexp.MustCompile(`\bnavigator\s*\.\s*sendBeacon\b`)},
	{"document.cookie", regexp.MustCompile(`\bdocument\s*\.\s*cookie\b`)},
	{"browser storage", regexp.MustCompile(`\b(localStorage|sessionStorage|indexedDB)\b`)},
	{"parent or top access", regexp.MustCompile(`\b(window\s*\.\s*parent|parent\s*\.\s*document|window\s*\.\s*top|top\s*\.\s*location)\b`)},
	{"window.open", regexp.MustCompile(`\bwindow\s*\.\s*open\s*\(`)},
	{"dynamic import", regexp.MustCompile(`\bimport\s*\(`)},
}

var externalURL = regexp.MustCompile(`(?i)\b(url\s*\(\s*['"]?|@import\s+)(https?:)?//`)

// Validate reports every located problem in the document. It never executes
// or rewrites the source.
func (d *Document) Validate(resolve Resolver) Report {
	cache := map[string]resolved{}
	lookup := func(id string) resolved {
		if cached, ok := cache[id]; ok {
			return cached
		}
		point, status := resolve(id)
		cache[id] = resolved{point: point, status: status}
		return cache[id]
	}

	findings := []Finding{}
	bindingCount := 0
	for _, element := range d.Elements {
		if _, ok := element.Attrs["data-fastiot-point"]; ok {
			bindingCount++
		}
		if _, ok := element.Attrs["data-fastiot-write"]; ok {
			bindingCount++
		}
		if bindingCount > maxBindings {
			findings = append(findings, Finding{
				Code: "too_many_bindings", Origin: OriginStatic, Severity: SeverityBlocking,
				Message: "画面绑定数量超过检查上限（" + strconv.Itoa(maxBindings) + "），请拆分画面后再预览",
				Line:    element.Line, Column: element.Column,
			})
			break
		}
		if id, ok := element.Attrs["data-fastiot-point"]; ok {
			findings = append(findings, d.bindingFinding(element, strings.TrimSpace(id), "read", lookup(strings.TrimSpace(id)))...)
		}
		if id, ok := element.Attrs["data-fastiot-write"]; ok {
			trimmed := strings.TrimSpace(id)
			findings = append(findings, d.bindingFinding(element, trimmed, "write", lookup(trimmed))...)
			if element.Tag != "button" && !strings.EqualFold(element.Attrs["role"], "button") {
				findings = append(findings, Finding{
					Code: "write_element_not_button", Origin: OriginStatic, Severity: SeverityWarning,
					Message: "写操作应放在 <button> 上，当前元素不会获得键盘与点击语义",
					Binding: trimmed, Element: element.Marker, Selector: element.Selector,
					Line: element.Line, Column: element.Column,
				})
			}
			if !numericValue(element.Attrs["data-fastiot-value"]) {
				findings = append(findings, Finding{
					Code: "write_value_invalid", Origin: OriginStatic, Severity: SeverityBlocking,
					Message: "写操作缺少有效的 data-fastiot-value 数值",
					Binding: trimmed, Element: element.Marker, Selector: element.Selector,
					Line: element.Line, Column: element.Column,
				})
			}
		}
		findings = append(findings, d.resourceFindings(element)...)
	}

	for _, script := range d.Scripts {
		if !isJavaScript(script.Element) {
			continue
		}
		for _, call := range forbiddenCalls {
			for _, match := range call.pattern.FindAllStringIndex(script.Text, 8) {
				line, column := d.position(script.Offset + match[0])
				findings = append(findings, Finding{
					Code: "forbidden_api", Origin: OriginStatic, Severity: SeverityBlocking,
					Message: "生成的脚本使用了 " + call.name + "；隔离预览不提供网络、宿主或执行任意代码的能力",
					Element: script.Element.Marker, Selector: script.Element.Selector,
					Line: line, Column: column,
				})
			}
		}
	}
	for _, style := range d.Styles {
		for _, match := range externalURL.FindAllStringIndex(style.Text, 4) {
			line, column := d.position(style.Offset + match[0])
			findings = append(findings, Finding{
				Code: "external_resource", Origin: OriginStatic, Severity: SeverityBlocking,
				Message: "样式引用了外部资源；隔离预览只允许内联样式与 data: 图片",
				Element: style.Element.Marker, Selector: style.Element.Selector,
				Line: line, Column: column,
			})
		}
	}
	return MergeReports(findings, nil)
}

func (d *Document) bindingFinding(element Element, id, kind string, result resolved) []Finding {
	verb := "读取"
	if kind == "write" {
		verb = "写入"
	}
	if id == "" {
		return []Finding{{
			Code: "binding_unknown", Origin: OriginStatic, Severity: SeverityBlocking,
			Message: "绑定为空，无法" + verb + "任何点位",
			Element: element.Marker, Selector: element.Selector, Line: element.Line, Column: element.Column,
		}}
	}
	switch result.status {
	case PointMissing:
		return []Finding{{
			Code: "binding_unknown", Origin: OriginStatic, Severity: SeverityBlocking,
			Message: "画面绑定到不存在的点位 " + strconv.Quote(id) + "，无法" + verb,
			Binding: id, Element: element.Marker, Selector: element.Selector, Line: element.Line, Column: element.Column,
		}}
	case PointOutOfScope:
		return []Finding{{
			Code: "binding_out_of_scope", Origin: OriginStatic, Severity: SeverityBlocking,
			Message: "点位 " + strconv.Quote(id) + " 存在，但当前账号无权访问它",
			Binding: id, Element: element.Marker, Selector: element.Selector, Line: element.Line, Column: element.Column,
		}}
	case PointNotSelected:
		return []Finding{{
			Code: "binding_not_selected", Origin: OriginStatic, Severity: SeverityBlocking,
			Message: "点位 " + strconv.Quote(id) + " 存在，但不在本次生成的点位范围内",
			Binding: id, Element: element.Marker, Selector: element.Selector, Line: element.Line, Column: element.Column,
		}}
	}
	if kind == "write" && !result.point.Writable {
		return []Finding{{
			Code: "write_read_only", Origin: OriginStatic, Severity: SeverityBlocking,
			Message: "点位 " + strconv.Quote(id) + " 是只读遥测，画面不能对它发起写操作",
			Binding: id, Element: element.Marker, Selector: element.Selector, Line: element.Line, Column: element.Column,
		}}
	}
	return nil
}

func (d *Document) resourceFindings(element Element) []Finding {
	findings := []Finding{}
	if element.Tag == "meta" && strings.EqualFold(strings.TrimSpace(element.Attrs["http-equiv"]), "refresh") {
		findings = append(findings, Finding{
			Code: "external_resource", Origin: OriginStatic, Severity: SeverityBlocking,
			Message: "meta refresh 会把预览导航到别处；隔离预览不允许页面自行跳转",
			Element: element.Marker, Selector: element.Selector, Line: element.Line, Column: element.Column,
		})
	}
	names := make([]string, 0, len(element.Attrs))
	for name := range element.Attrs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if strings.HasPrefix(name, "on") && len(name) > 2 {
			findings = append(findings, Finding{
				Code: "inline_handler", Origin: OriginStatic, Severity: SeverityBlocking,
				Message: "内联事件属性 " + name + " 在预览策略下不会执行；请改用 data-fastiot-write 绑定",
				Element: element.Marker, Selector: element.Selector, Line: element.Line, Column: element.Column,
			})
			continue
		}
		switch name {
		case "src", "href", "action", "formaction", "poster", "xlink:href", "data":
			value := strings.TrimSpace(element.Attrs[name])
			if value == "" || strings.HasPrefix(value, "#") || strings.HasPrefix(value, "data:") || strings.HasPrefix(value, "about:") {
				continue
			}
			findings = append(findings, Finding{
				Code: "external_resource", Origin: OriginStatic, Severity: SeverityBlocking,
				Message: "元素引用了外部资源 " + strconv.Quote(value) + "；离线预览不会加载它",
				Element: element.Marker, Selector: element.Selector, Line: element.Line, Column: element.Column,
			})
		}
	}
	return findings
}

// isJavaScript skips data blocks such as application/json, which are content
// rather than executable code.
func isJavaScript(element Element) bool {
	switch strings.ToLower(strings.TrimSpace(element.Attrs["type"])) {
	case "", "module", "text/javascript", "application/javascript":
		return true
	}
	return false
}

func numericValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	_, err := strconv.ParseFloat(trimmed, 64)
	return err == nil
}

// ElementForLine returns the element whose start tag contains the line, or
// the nearest element that starts before it. Browser errors report a line in
// the served document, which keeps the model's own line numbers.
func (d *Document) ElementForLine(line int) (Element, bool) {
	var found Element
	ok := false
	for _, element := range d.Elements {
		if element.Line > line {
			break
		}
		found, ok = element, true
	}
	return found, ok
}

// AttachLocations fills element and selector for findings that only carry a
// line, such as runtime errors.
func (d *Document) AttachLocations(findings []Finding) []Finding {
	for index := range findings {
		if findings[index].Element != "" || findings[index].Line <= 0 {
			continue
		}
		if element, ok := d.ElementForLine(findings[index].Line); ok {
			findings[index].Element = element.Marker
			findings[index].Selector = element.Selector
		}
	}
	return findings
}

// MergeReports combines the static findings with the ones the browser
// reported. Static findings always come first.
func MergeReports(static, client []Finding) Report {
	report := Report{Findings: []Finding{}}
	for _, list := range [][]Finding{static, client} {
		for _, finding := range list {
			if finding.Origin == "" {
				finding.Origin = OriginStatic
			}
			if finding.Severity != SeverityWarning {
				finding.Severity = SeverityBlocking
			}
			if finding.Severity == SeverityBlocking {
				report.Blocking++
			} else {
				report.Warnings++
			}
			report.Findings = append(report.Findings, finding)
		}
	}
	report.Ready = report.Blocking == 0
	return report
}

var clientCodes = map[string]bool{
	"runtime_error":    true,
	"layout_overflow":  true,
	"layout_clipped":   true,
	"layout_invisible": true,
	"layout_offscreen": true,
}

// SanitizeClientFindings accepts only the browser checks' vocabulary and
// bounds every field, so a preview client cannot inject arbitrary content.
func SanitizeClientFindings(findings []Finding) ([]Finding, bool) {
	if len(findings) > 200 {
		return nil, false
	}
	clean := make([]Finding, 0, len(findings))
	for _, finding := range findings {
		if !clientCodes[finding.Code] {
			return nil, false
		}
		if finding.Severity != SeverityBlocking && finding.Severity != SeverityWarning {
			return nil, false
		}
		if finding.Origin != OriginRuntime && finding.Origin != OriginLayout {
			return nil, false
		}
		if finding.Line < 0 || finding.Column < 0 || finding.Line > 10_000_000 || finding.Column > 10_000_000 {
			return nil, false
		}
		finding.Message = truncate(strings.TrimSpace(finding.Message), 500)
		if finding.Message == "" {
			return nil, false
		}
		finding.Binding = truncate(finding.Binding, 200)
		finding.Element = truncate(finding.Element, 200)
		finding.Selector = truncate(finding.Selector, 200)
		if len(finding.Detail) > 10 {
			return nil, false
		}
		clean = append(clean, finding)
	}
	return clean, true
}

func truncate(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}
