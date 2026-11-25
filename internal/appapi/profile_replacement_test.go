package appapi

import (
	"strings"
	"testing"

	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/ems/profile"
)

func replacementPoints(prof profile.Profile) []unitModelPointView {
	points := make([]unitModelPointView, 0, len(prof.PointSpecs))
	for _, spec := range prof.PointSpecs {
		if spec.Name == "电网频率" {
			continue // the new profile needs a point the site never had
		}
		point := unitModelPointView{
			ID: "p-" + spec.Name, Name: spec.Name, ServerID: 1, Address: int(spec.Address),
			IOType: profile.IOTypeFor(spec.Function), DataFormat: spec.Format,
			Scale: spec.Scale, Offset: spec.Offset, Unit: spec.Unit, ReadOnly: spec.ReadOnly,
		}
		switch spec.Name {
		case "电池电压":
			point.Unit = "kV"
		case "电池电流":
			point.IOType = data.PointValueModbusInputRegister
		case "IGBT 温度":
			point.Scale = 1
		case "环境温度":
			point.Offset = 5
		case "有功功率设定":
			point.ReadOnly = true
		case "开机/关机指令":
			point.IOType = data.PointValueModbusCoil
		}
		points = append(points, point)
	}
	return points
}

func TestApplyProfileReplacementListsNonEquivalentMappings(t *testing.T) {
	prof, ok := profile.Default().Get("enjo-pcs")
	if !ok {
		t.Fatal("enjo-pcs profile missing")
	}
	points := replacementPoints(prof)
	existing := unitModelConfig{
		Name: "旧型号", ProfileID: "old-vendor", PointMap: map[string]string{"电池电压": "p-电池电压"},
		Verified: true, Verifier: "张三", VerifiedAt: "2026-01-01", DeviceSerial: "SN-1",
	}
	result := applyProfile(1, prof, points, existing)

	if result.Replacement == nil || result.Replacement.From != "old-vendor" || result.Replacement.To != "enjo-pcs" {
		t.Fatalf("replacement = %+v", result.Replacement)
	}
	if result.Replacement.Equivalent {
		t.Fatal("a replacement with non-equivalent mappings must not claim equivalence")
	}
	if len(result.Replacement.Missing) != 1 || result.Replacement.Missing[0] != "电网频率" {
		t.Fatalf("missing = %v", result.Replacement.Missing)
	}

	expected := map[string]struct{ field, verdict string }{
		"电池电压":    {"单位", "mismatch"},
		"电池电流":    {"类型", "mismatch"},
		"IGBT 温度": {"缩放", "scaled"},
		"环境温度":    {"偏移", "scaled"},
		"有功功率设定":  {"读写方向", "mismatch"},
		"开机/关机指令": {"类型", "mismatch"},
	}
	verdicts := map[string]profilePointVerdict{}
	for _, verdict := range result.Points {
		verdicts[verdict.Name] = verdict
	}
	for name, want := range expected {
		verdict := verdicts[name]
		if verdict.Verdict != want.verdict {
			t.Errorf("%s verdict = %q, want %q", name, verdict.Verdict, want.verdict)
		}
		found := false
		for _, difference := range verdict.Differences {
			if difference.Field == want.field && difference.Point == name {
				found = true
			}
		}
		if !found {
			t.Errorf("%s differences = %+v, want field %q", name, verdict.Differences, want.field)
		}
	}
	if verdicts["电池电压"].Verdict == "exact" || !strings.Contains(verdicts["电池电压"].Reason, "单位") {
		t.Fatalf("verdict reason = %q", verdicts["电池电压"].Reason)
	}

	if result.Model.Verified || !result.Model.VerificationRequired || result.Model.ReplacedFrom != "old-vendor" {
		t.Fatalf("model after replacement = %+v", result.Model)
	}
	if result.Model.Verifier != "" || result.Model.VerifiedAt != "" || result.Model.DeviceSerial != "" {
		t.Fatalf("old verification must not carry over: %+v", result.Model)
	}
	if !strings.Contains(strings.Join(result.Blocked, "|"), "开机/关机指令") || !strings.Contains(strings.Join(result.Blocked, "|"), "不等价") {
		t.Fatalf("blocked = %v, want the non-equivalent command point", result.Blocked)
	}
}

func TestApplyProfileFirstMappingDoesNotRequireReverification(t *testing.T) {
	prof, ok := profile.Default().Get("enjo-pcs")
	if !ok {
		t.Fatal("enjo-pcs profile missing")
	}
	result := applyProfile(1, prof, replacementPoints(prof), unitModelConfig{})
	if result.Replacement != nil || result.Model.VerificationRequired {
		t.Fatalf("first mapping must not claim a replacement: %+v", result.Model)
	}
}

func TestUnitModelResponseHidesCommandsPendingReverification(t *testing.T) {
	registry := profile.Default()
	model := unitModelConfig{
		ProfileID: "enjo-pcs", PointMap: map[string]string{"开机/关机指令": "point-1"},
		ReplacedFrom: "old-vendor", VerificationRequired: true,
	}
	points := []unitModelPointView{{ID: "point-1", Name: "开机/关机指令", ServerID: 1}}
	view := unitModelResponse("channel-1", 1, model, true, points, registry)
	if len(view.Commands) == 0 {
		t.Fatal("commands missing")
	}
	for _, command := range view.Commands {
		if command.Available || !strings.Contains(command.Reason, "重新验证") {
			t.Fatalf("command after replacement = %+v", command)
		}
	}
	if !verificationPending(model) {
		t.Fatal("replacement model should report pending verification")
	}
	model.Verified = true
	if verificationPending(model) {
		t.Fatal("a verified model must not stay pending")
	}
}
