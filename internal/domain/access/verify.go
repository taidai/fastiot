package access

import (
	"fmt"
	"math"
	"strings"

	"fastiot/internal/ems/profile"
)

// RequiredPointSpecs uses the existing Profile as the single source. Curated
// points are required unless their note carries the explicit OPTIONAL marker.
func RequiredPointSpecs(prof profile.Profile) []profile.PointSpec {
	required := make([]profile.PointSpec, 0, len(prof.PointSpecs))
	for _, spec := range prof.PointSpecs {
		if spec.RequiredPoint() {
			required = append(required, spec)
		}
	}
	return required
}

// SafeCommand returns only a fixed-value, no-parameter command whose target
// point explicitly carries L3_SAFE and whose Profile declares a readback.
// Built-in start/stop and power commands do not meet this contract by default.
func SafeCommand(prof profile.Profile, commandID string) (profile.CommandSpec, error) {
	for _, command := range prof.CommandSpecs {
		if command.ID != commandID {
			continue
		}
		if command.Point == "" || len(command.Parameters) > 0 || math.IsNaN(command.Value) || math.IsInf(command.Value, 0) {
			return profile.CommandSpec{}, fmt.Errorf("L3 只允许固定值、无参数的安全命令")
		}
		if command.Readback == "" {
			return profile.CommandSpec{}, fmt.Errorf("L3 安全命令必须声明回读点")
		}
		for _, spec := range prof.PointSpecs {
			if spec.Name == command.Point {
				if spec.ReadOnly || !profileMarker(spec.Note, "L3_SAFE") {
					return profile.CommandSpec{}, fmt.Errorf("点位 %s 未声明 L3_SAFE", command.Point)
				}
				return command, nil
			}
		}
		return profile.CommandSpec{}, fmt.Errorf("安全命令点位未在 Profile 声明")
	}
	return profile.CommandSpec{}, fmt.Errorf("command_not_available")
}

func profileMarker(note, marker string) bool {
	for _, field := range strings.Fields(note) {
		if field == marker {
			return true
		}
	}
	return false
}

func TerminalCommandState(state string) bool {
	switch state {
	case "confirmed", "failed", "unknown", "submitted", "superseded":
		return true
	default:
		return false
	}
}

func CommandOutcomePassed(state string) bool { return state == "confirmed" }

// VerificationRecord is persisted on the channel independently of the manual
// UnitModel verification fields, so automated evidence cannot be mistaken for
// a person signing off the device.
type VerificationRecord struct {
	At             string   `json:"at"`
	Actor          string   `json:"actor"`
	Level          string   `json:"level"`
	Passed         bool     `json:"passed"`
	Reasons        []string `json:"reasons,omitempty"`
	Basis          string   `json:"basis,omitempty"`
	ProfileVersion string   `json:"profile_version,omitempty"`
	Current        bool     `json:"current"`
}

type PointVerification struct {
	Name       string `json:"name"`
	PointID    string `json:"point_id,omitempty"`
	Passed     bool   `json:"passed"`
	Quality    string `json:"quality,omitempty"`
	SourceTime string `json:"source_time,omitempty"`
	Reason     string `json:"reason,omitempty"`
}
