package genscreen

import (
	"strings"
	"testing"
	"time"
)

// TestDiffLocatesChangedElements proves the revision report names what the
// model changed where: attributes, text and new elements are located back to
// selectors and lines in the modified document.
func TestDiffLocatesChangedElements(t *testing.T) {
	before := Parse(`<html><body><div id="tank" class="card"><h1>泵房</h1><span data-fastiot-point="a">--</span></div></body></html>`)
	after := Parse("<html><body><div id=\"tank\" class=\"card red\"><h1>泵房 V2</h1><span data-fastiot-point=\"a\">--</span><p>新增说明</p></div></body></html>")

	changes := Diff(before, after)
	if len(changes) != 3 {
		t.Fatalf("changes = %+v", changes)
	}
	var attribute, text, added *Change
	for index := range changes {
		change := &changes[index]
		switch {
		case change.Kind == "attribute" && strings.Contains(change.Selector, "div#tank"):
			attribute = change
		case change.Kind == "text" && strings.Contains(change.Selector, "h1"):
			text = change
		case change.Kind == "added" && strings.Contains(change.Selector, "p:nth"):
			added = change
		}
	}
	if attribute == nil || !strings.Contains(attribute.Detail, "class") || attribute.Line != 1 {
		t.Fatalf("attribute change not located: %+v", changes)
	}
	if text == nil || !strings.Contains(text.Detail, "泵房 V2") {
		t.Fatalf("text change not located: %+v", changes)
	}
	if added == nil || added.Element == "" {
		t.Fatalf("added element not reported: %+v", changes)
	}
	for _, change := range changes {
		if strings.Contains(change.Selector, "span") {
			t.Fatalf("unchanged element reported: %+v", change)
		}
	}
}

// TestSessionRevisionResetsBrowserChecks proves a modification re-enters the
// same isolated preview but its browser checks no longer apply: the publish
// gate must be satisfied again.
func TestSessionRevisionResetsBrowserChecks(t *testing.T) {
	session := &Session{Source: "<div>旧</div>", Points: []Point{{ID: "a", Name: "液位", Kind: "number"}}}
	session.SetClient([]Finding{{Code: "runtime_error", Origin: OriginRuntime, Severity: SeverityBlocking, Message: "旧错误"}}, time.Now())
	if !session.HasClientChecks() {
		t.Fatal("client checks should be recorded")
	}
	revision := session.ApplyRevision("<div>新</div>", nil)
	if revision != 1 {
		t.Fatalf("revision = %d", revision)
	}
	snapshot := session.Snapshot()
	if snapshot.Source != "<div>新</div>" || snapshot.Revision != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if session.HasClientChecks() {
		t.Fatal("a modified document must run its checks again")
	}
	if report := session.Report(); !report.Ready || len(report.Findings) != 0 {
		t.Fatalf("stale client findings survived the revision: %+v", report)
	}
}
