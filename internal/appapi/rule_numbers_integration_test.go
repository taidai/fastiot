package appapi

import (
	"net/http"
	"testing"

	"github.com/simpleiot/simpleiot/data"
)

func TestRuleNumericFieldsNeverDefaultNullOrMissingToZero(t *testing.T) {
	base, token, _, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, token)
	target := createNodeAPI(t, base, token, "variable", root, "numeric contract", 0)
	for _, tc := range []struct {
		name  string
		value any
		omit  bool
		hold  any
		delay any
		want  int
	}{
		{name: "explicit zero", value: 0, want: 200},
		{name: "null action", value: nil, want: 400},
		{name: "missing action", omit: true, want: 400},
		{name: "NaN string", value: "NaN", want: 400},
		{name: "null hold", value: 1, hold: "null", want: 400},
		{name: "overflowing delay", value: 1, delay: int64(9000000000000000), want: 400},
	} {
		action := map[string]any{"action_id": "a"}
		if !tc.omit {
			action["value"] = tc.value
		}
		if tc.delay != nil {
			action["delay_ms"] = tc.delay
		}
		row := map[string]any{"row_id": "r", "actions": []any{action}}
		if tc.hold != nil {
			row["hold_ms"] = nil
		}
		document := map[string]any{"scope": "self", "table": map[string]any{"rows": []any{row}}, "bindings": map[string]any{"actions": map[string]any{"a": map[string]any{"node_id": target}}}}
		status, body := call(t, http.MethodPost, base+"/rules", token, map[string]any{"parent": root, "description": tc.name, "document": document}, nil)
		if status != tc.want {
			t.Errorf("%s: %d %s want %d", tc.name, status, body, tc.want)
		}
	}
}
