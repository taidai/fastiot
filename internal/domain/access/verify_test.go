package access

import (
	"strings"
	"testing"

	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/profile"
)

func verificationProfile() profile.Profile {
	return profile.Profile{
		ID: "safe-test",
		PointSpecs: []profile.PointSpec{
			{Name: "测试定值", Function: channel.HoldingRegister, ReadOnly: false, Note: "L3_SAFE 固定值写回读"},
			{Name: "状态", Function: channel.InputRegister, ReadOnly: true},
			{Name: "普通定值", Function: channel.HoldingRegister, ReadOnly: false},
			{Name: "可选测量", Function: channel.InputRegister, ReadOnly: true, Note: "OPTIONAL"},
		},
		CommandSpecs: []profile.CommandSpec{
			{ID: "safe", Name: "安全写回读", Point: "测试定值", Value: 1, Readback: "状态", ReadbackValue: 1},
			{ID: "ordinary", Name: "普通启动", Point: "普通定值", Value: 1, Readback: "状态", ReadbackValue: 1},
			{ID: "parameter", Name: "参数命令", Point: "测试定值", Readback: "状态", Parameters: []profile.CommandParameter{{Name: "value", Point: "测试定值"}}},
			{ID: "no-readback", Name: "没有回读", Point: "测试定值", Value: 1},
		},
	}
}

func TestRequiredPointSpecsOnlyExcludesExplicitOptionalPoints(t *testing.T) {
	t.Parallel()
	required := RequiredPointSpecs(verificationProfile())
	if len(required) != 3 {
		t.Fatalf("required = %d, want 3", len(required))
	}
	for _, spec := range required {
		if spec.Name == "可选测量" {
			t.Fatal("OPTIONAL point counted as required")
		}
	}
}

func TestSafeCommandRequiresProfileDeclarationFixedValueAndReadback(t *testing.T) {
	t.Parallel()
	prof := verificationProfile()
	command, err := SafeCommand(prof, "safe")
	if err != nil || command.ID != "safe" {
		t.Fatalf("safe command rejected: %+v %v", command, err)
	}
	for _, id := range []string{"ordinary", "parameter", "no-readback", "missing"} {
		t.Run(id, func(t *testing.T) {
			if _, err := SafeCommand(prof, id); err == nil {
				t.Fatal("unsafe or incomplete command accepted")
			} else if id == "ordinary" && !strings.Contains(err.Error(), "L3_SAFE") {
				t.Fatalf("ordinary command error does not explain missing declaration: %v", err)
			}
		})
	}
}

func TestCommandOutcomeRequiresConfirmedReadback(t *testing.T) {
	t.Parallel()
	for _, state := range []string{"failed", "unknown", "submitted", "superseded", "confirmed"} {
		if !TerminalCommandState(state) {
			t.Errorf("terminal state %s still treated as pending", state)
		}
	}
	for _, state := range []string{"accepted", "executing"} {
		if TerminalCommandState(state) {
			t.Errorf("pending state %s treated as complete", state)
		}
	}
	if CommandOutcomePassed("submitted") || CommandOutcomePassed("unknown") || !CommandOutcomePassed("confirmed") {
		t.Fatal("only confirmed readback may pass L3")
	}
}
