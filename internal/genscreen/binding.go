package genscreen

import (
	"regexp"
	"sort"
	"strings"
)

// Binding is one place a document names a point ID: a bridge binding
// attribute (Declared) or a quoted literal in a generated script, which is
// where 3D screens read realtime values. Start and End index the source.
type Binding struct {
	ID       string
	Start    int
	End      int
	Declared bool
}

var bindingAttributes = []string{"data-fastiot-point", "data-fastiot-write"}

var quotedLiteral = regexp.MustCompile(`["']([A-Za-z0-9_-]{1,64})["']`)

// FindBindings locates the identifiers a frame exchanges with its host. The
// caller decides which of them are point IDs; an attribute is a binding by
// definition, a script literal is only a candidate.
func (d *Document) FindBindings() []Binding {
	bindings := []Binding{}
	for _, element := range d.Elements {
		tag := d.Source[element.Start:element.End]
		for _, name := range bindingAttributes {
			id, ok := element.Attrs[name]
			if !ok {
				continue
			}
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			nameAt := strings.Index(tag, name)
			if nameAt < 0 {
				continue
			}
			valueAt := strings.Index(tag[nameAt+len(name):], id)
			if valueAt < 0 {
				continue
			}
			start := element.Start + nameAt + len(name) + valueAt
			bindings = append(bindings, Binding{ID: id, Start: start, End: start + len(id), Declared: true})
		}
	}
	for _, script := range d.Scripts {
		if !isJavaScript(script.Element) {
			continue
		}
		for _, match := range quotedLiteral.FindAllStringSubmatchIndex(script.Text, -1) {
			bindings = append(bindings, Binding{
				ID:    script.Text[match[2]:match[3]],
				Start: script.Offset + match[2],
				End:   script.Offset + match[3],
			})
		}
	}
	sort.SliceStable(bindings, func(i, j int) bool { return bindings[i].Start < bindings[j].Start })
	return bindings
}
