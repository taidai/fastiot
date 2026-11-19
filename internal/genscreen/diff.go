package genscreen

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// Change is one located difference between the screen before and after a
// revision. It tells the deliverer where the model changed the screen; removed
// elements have no marker because they no longer exist in the preview.
type Change struct {
	Kind     string `json:"kind"`
	Selector string `json:"selector"`
	Element  string `json:"element,omitempty"`
	Line     int    `json:"line,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

const maxReportedChanges = 50

// Diff reports what changed between two parsed documents, matched by selector
// path. Attribute, text, style and script edits are located; unchanged
// elements are not reported.
func Diff(before, after *Document) []Change {
	previous := map[string]Element{}
	for _, element := range before.Elements {
		if _, ok := previous[element.Selector]; !ok {
			previous[element.Selector] = element
		}
	}
	current := map[string]Element{}
	changes := make([]Change, 0, 8)
	for _, element := range after.Elements {
		if _, ok := current[element.Selector]; ok {
			continue
		}
		current[element.Selector] = element
		old, existed := previous[element.Selector]
		if !existed {
			changes = append(changes, Change{
				Kind: "added", Selector: element.Selector, Element: element.Marker,
				Line: element.Line, Detail: textPreview(element.Text),
			})
			continue
		}
		if names := changedAttributes(old.Attrs, element.Attrs); names != "" {
			changes = append(changes, Change{
				Kind: "attribute", Selector: element.Selector, Element: element.Marker,
				Line: element.Line, Detail: "属性 " + names,
			})
		}
		if old.Text != element.Text {
			changes = append(changes, Change{
				Kind: "text", Selector: element.Selector, Element: element.Marker,
				Line: element.Line, Detail: textPreview(element.Text),
			})
		}
	}
	removed := make([]Element, 0)
	for selector, element := range previous {
		if _, ok := current[selector]; !ok {
			removed = append(removed, element)
		}
	}
	sort.Slice(removed, func(i, j int) bool { return removed[i].Selector < removed[j].Selector })
	for _, element := range removed {
		changes = append(changes, Change{Kind: "removed", Selector: element.Selector})
	}
	changes = append(changes, contentChanges(before.Styles, after.Styles, "style", "样式已更新")...)
	changes = append(changes, contentChanges(before.Scripts, after.Scripts, "script", "脚本已更新")...)
	if len(changes) > maxReportedChanges {
		changes = changes[:maxReportedChanges]
	}
	return changes
}

// contentChanges compares the bodies of style or script elements by selector.
func contentChanges(before, after []chunk, kind, detail string) []Change {
	previous := map[string]string{}
	for _, item := range before {
		previous[item.Element.Selector] += item.Text
	}
	current := map[string]string{}
	changes := []Change{}
	for _, item := range after {
		current[item.Element.Selector] += item.Text
	}
	for selector, text := range current {
		if previous[selector] == text {
			continue
		}
		changes = append(changes, Change{Kind: kind, Selector: selector, Detail: detail})
	}
	// Locate each reported element so the preview can highlight it.
	for index := range changes {
		for _, item := range after {
			if item.Element.Selector == changes[index].Selector {
				changes[index].Element = item.Element.Marker
				changes[index].Line = item.Element.Line
				break
			}
		}
	}
	return changes
}

func changedAttributes(before, after map[string]string) string {
	names := []string{}
	seen := map[string]bool{}
	for name := range before {
		seen[name] = true
	}
	for name := range after {
		seen[name] = true
	}
	for name := range seen {
		if before[name] != after[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return strings.Join(names, "、")
}

func textPreview(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if utf8.RuneCountInString(text) <= 80 {
		return text
	}
	return string([]rune(text)[:80]) + "…"
}
