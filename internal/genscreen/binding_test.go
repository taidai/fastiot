package genscreen

import (
	"sort"
	"testing"
)

// TestFindBindingsLocatesAttributeAndScriptIDs scans for every place a
// document names a point: the binding attributes and the quoted literals 3D
// scripts read values with. Ranges index the original source so a caller can
// replace exactly those bytes.
func TestFindBindingsLocatesAttributeAndScriptIDs(t *testing.T) {
	source := `<html><body>
<span data-fastiot-point="level" data-fastiot-format="number">--</span>
<button data-fastiot-write="pump">启动</button>
<script>
const value = event.detail.values['level'].value;
const pump = "pump";
const label = "number";
</script>
</body></html>`
	bindings := Parse(source).FindBindings()
	if len(bindings) == 0 {
		t.Fatal("no bindings found")
	}
	if !sort.SliceIsSorted(bindings, func(i, j int) bool { return bindings[i].Start < bindings[j].Start }) {
		t.Fatal("bindings are not ordered by position")
	}
	type seen struct {
		declared int
		literals int
	}
	counts := map[string]seen{}
	for _, binding := range bindings {
		if binding.Start < 0 || binding.End > len(source) || source[binding.Start:binding.End] != binding.ID {
			t.Fatalf("binding range does not name %q: %#v", binding.ID, binding)
		}
		entry := counts[binding.ID]
		if binding.Declared {
			entry.declared++
		} else {
			entry.literals++
		}
		counts[binding.ID] = entry
	}
	if counts["level"] != (seen{declared: 1, literals: 1}) {
		t.Fatalf("level occurrences = %#v, want the attribute plus the script literal", counts["level"])
	}
	if counts["pump"] != (seen{declared: 1, literals: 1}) {
		t.Fatalf("pump occurrences = %#v", counts["pump"])
	}
	if counts["number"] != (seen{literals: 1}) {
		t.Fatalf("number occurrences = %#v, want one plain literal", counts["number"])
	}
}

// TestFindBindingsIgnoresStylesAndMarkupText keeps the scanner on the two
// places the bridge actually reads from.
func TestFindBindingsIgnoresStylesAndMarkupText(t *testing.T) {
	source := `<html><head><style>.level { color: red }</style></head>
<body><p data-fastiot-point="level">level</p><script src="x"></script></body></html>`
	bindings := Parse(source).FindBindings()
	if len(bindings) != 1 || bindings[0].ID != "level" || !bindings[0].Declared {
		t.Fatalf("bindings = %#v", bindings)
	}
}
