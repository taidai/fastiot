package appapi

import (
	"net/http"
	"slices"
	"testing"

	"github.com/simpleiot/simpleiot/data"
)

func TestSnapshotExposesEffectiveActions(t *testing.T) {
	base, admin, _, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, admin)
	f := &pointConfigFixture{base: base, token: admin}

	viewer := inputAccount(t, f, root, "viewer", []string{ActionExport})
	var snapshot struct {
		User struct {
			Actions []string `json:"actions"`
		} `json:"user"`
	}
	status, body := call(t, http.MethodGet, base+"/snapshot", viewer, nil, &snapshot)
	if status != http.StatusOK {
		t.Fatalf("viewer snapshot = %d %s", status, body)
	}
	if !slices.Equal(snapshot.User.Actions, []string{ActionExport}) {
		t.Fatalf("viewer actions = %v", snapshot.User.Actions)
	}

	implementer := inputAccount(t, f, root, "implementer", nil)
	var defaulted struct {
		User struct {
			Actions []string `json:"actions"`
		} `json:"user"`
	}
	status, body = call(t, http.MethodGet, base+"/snapshot", implementer, nil, &defaulted)
	if status != http.StatusOK {
		t.Fatalf("implementer snapshot = %d %s", status, body)
	}
	for _, action := range []string{ActionConfigure, ActionExport, ActionWriteValue, ActionCommand} {
		if !slices.Contains(defaulted.User.Actions, action) {
			t.Fatalf("implementer missing %s: %v", action, defaulted.User.Actions)
		}
	}
	if slices.Contains(defaulted.User.Actions, ActionAccountManage) {
		t.Fatalf("implementer received account management: %v", defaulted.User.Actions)
	}
}
