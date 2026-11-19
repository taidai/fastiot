package decision

import "testing"

func TestWithinDeadband(t *testing.T) {
	if !withinDeadband(100, 102, 5) {
		t.Error("102 is within deadband 5 of 100")
	}
	if withinDeadband(100, 102, 1) {
		t.Error("102 is outside deadband 1 of 100")
	}
	if withinDeadband(100, 100, 0) {
		t.Error("deadband 0 must disable the check")
	}
	if withinDeadband(-100, -102, 5) != true {
		t.Error("signed magnitudes should compare by absolute difference")
	}
}
