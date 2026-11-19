package genscreen

import (
	_ "embed"
	"regexp"
	"sort"
	"strings"
)

// RuntimeScript is the trusted bridge injected into every preview document.
// It is not model output and never runs in the platform page.
//
//go:embed runtime.js
var RuntimeScript string

const runtimeTag = `<script data-fastiot-runtime="1" nonce="`

var validNonce = regexp.MustCompile(`^[A-Za-z0-9]+$`)

// ContentSecurityPolicy is the policy served with the frame. The generated
// document may run its own code with the response nonce, but it cannot reach
// the network, the host page, forms, frames or evaluation.
func ContentSecurityPolicy(nonce string) string {
	return "default-src 'none'; " +
		"script-src 'nonce-" + safeNonce(nonce) + "'; " +
		"style-src 'unsafe-inline'; " +
		"img-src data:; " +
		"font-src data:; " +
		"media-src data:; " +
		"connect-src 'none'; " +
		"object-src 'none'; " +
		"frame-src 'none'; " +
		"form-action 'none'; " +
		"base-uri 'none'; " +
		"frame-ancestors 'self'"
}

func safeNonce(nonce string) string {
	if validNonce.MatchString(nonce) {
		return nonce
	}
	return "0"
}

// compactScript removes line breaks so the injected runtime does not shift the
// line numbers that findings report against the model's own source.
func compactScript(source string) string {
	return strings.ReplaceAll(strings.ReplaceAll(source, "\r\n", " "), "\n", " ")
}

type insertion struct {
	at    int
	order int
	text  string
}

// Render returns the served document: the model's markup byte for byte, with
// every element marked for locating, every script bound to the response
// nonce, and the trusted runtime first so it sees errors before generated
// scripts run.
func (d *Document) Render(nonce string) string {
	nonce = safeNonce(nonce)
	insertions := make([]insertion, 0, len(d.Elements)*2+1)
	headAt, htmlAt := -1, -1
	for _, element := range d.Elements {
		if element.End <= element.Start || d.Source[element.End-1] != '>' {
			continue
		}
		at := element.End - 1
		if at > element.Start && d.Source[at-1] == '/' {
			at--
		}
		space := " "
		if at > 0 && (d.Source[at-1] == ' ' || d.Source[at-1] == '\n' || d.Source[at-1] == '\t') {
			space = ""
		}
		insertions = append(insertions, insertion{at: at, order: 1, text: space + `data-fastiot-el="` + element.Marker + `"`})
		if element.Tag == "script" {
			nameEnd := element.Start + 1 + len(element.Tag)
			if nameEnd <= at {
				insertions = append(insertions, insertion{at: nameEnd, order: 0, text: ` nonce="` + nonce + `"`})
			}
		}
		if element.Tag == "head" && headAt < 0 {
			headAt = element.End
		}
		if element.Tag == "html" && htmlAt < 0 {
			htmlAt = element.End
		}
	}
	runtimeAt := 0
	if headAt >= 0 {
		runtimeAt = headAt
	} else if htmlAt >= 0 {
		runtimeAt = htmlAt
	}
	insertions = append(insertions, insertion{
		at: runtimeAt, order: 2,
		text: runtimeTag + nonce + `">` + compactScript(RuntimeScript) + `</script>`,
	})

	sort.SliceStable(insertions, func(i, j int) bool {
		if insertions[i].at != insertions[j].at {
			return insertions[i].at < insertions[j].at
		}
		return insertions[i].order < insertions[j].order
	})

	var out strings.Builder
	out.Grow(len(d.Source) + 4096)
	cursor := 0
	for _, item := range insertions {
		if item.at < cursor {
			continue
		}
		out.WriteString(d.Source[cursor:item.at])
		out.WriteString(item.text)
		cursor = item.at
	}
	out.WriteString(d.Source[cursor:])
	return out.String()
}
