package appapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	domainaccess "fastiot/internal/domain/access"
	"github.com/simpleiot/simpleiot/data"
)

// All observations use the authenticated HTTP boundary, including reloads.
func ledgerSnapshot(t *testing.T, base, token, project string) []data.NodeEdge {
	t.Helper()
	var result struct {
		Nodes []data.NodeEdge `json:"nodes"`
	}
	status, body := projectCall(t, http.MethodGet, base+"/snapshot", token, project, nil, &result)
	if status != 200 {
		t.Fatalf("snapshot: %d %s", status, body)
	}
	return result.Nodes
}

func TestLedgerMemberEditsRejectStaleVersionAcrossRestart(t *testing.T) {
	dir, password := t.TempDir(), data.NewID()
	base, first, _, stop := startInputServer(t, dir, password)
	second := loginAPI(t, base, "admin", password)
	root := snapshotRoot(t, base, first)
	unit := createNodeAPI(t, base, first, "unit", root, "Supply", 0)
	input := map[string]any{"description": "Supply", "members_version": 0, "members": []map[string]any{{"role": "drive", "required": true}}}
	var saved struct {
		Version int      `json:"members_version"`
		Missing []string `json:"missing_required"`
	}
	status, body := projectCall(t, http.MethodPatch, base+"/nodes/"+unit, first, root, input, &saved)
	if status != 200 || saved.Version != 1 || len(saved.Missing) != 1 || saved.Missing[0] != "drive" {
		t.Fatalf("first save must return revision 1 and explicit required gap: %d %s", status, body)
	}
	status, body = projectCall(t, http.MethodPatch, base+"/nodes/"+unit, second, root, input, nil)
	if status != 409 || !strings.Contains(body, "members_version_conflict") {
		t.Fatalf("stale editor: %d %s", status, body)
	}
	stop()
	base, first, _, _ = startInputServer(t, dir, password)
	status, body = projectCall(t, http.MethodPatch, base+"/nodes/"+unit, first, root, input, nil)
	if status != 409 {
		t.Fatalf("restart lost revision: %d %s", status, body)
	}
	savedNode := ledgerNamed(t, ledgerSnapshot(t, base, first, root), root, "Supply")
	version, _ := savedNode.Points.Value(domainaccess.MembersVersionPoint, "")
	if version != 1 {
		t.Fatalf("reload version = %v", version)
	}
	delete(input, "members_version")
	status, body = projectCall(t, http.MethodPatch, base+"/nodes/"+unit, first, root, input, nil)
	if status != 400 || !strings.Contains(body, "members_version_required") {
		t.Fatalf("unversioned overwrite: %d %s", status, body)
	}
}

func ledgerNamed(t *testing.T, nodes []data.NodeEdge, parent, name string) data.NodeEdge {
	t.Helper()
	for _, node := range nodes {
		if node.Parent == parent && node.Desc() == name {
			return node
		}
	}
	t.Fatalf("missing %s under %s", name, parent)
	return data.NodeEdge{}
}

func TestLedgerTemplatesCreateOnlyUnboundStructure(t *testing.T) {
	base, admin, _, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, admin)
	for template, count := range map[string]int{"blank": 1, "pump-house": 5, "storage": 6} {
		var created struct {
			ID      string `json:"id"`
			Created int    `json:"created"`
		}
		status, body := projectCall(t, http.MethodPost, base+"/nodes", admin, root, map[string]any{"parent": root, "description": template, "site_template": template}, &created)
		if status != 200 || created.Created != count {
			t.Fatalf("%s template: %d %s", template, status, body)
		}
	}
	for _, node := range ledgerSnapshot(t, base, admin, root) {
		if node.Parent == "root" || node.Type == "user" {
			continue
		}
		for _, point := range node.Points {
			switch point.Type {
			case "description", "nodeType":
			case domainaccess.MembersPoint:
				var members []domainaccess.MemberRole
				if json.Unmarshal([]byte(point.Txt()), &members) != nil {
					t.Fatal("malformed template members")
				}
				for _, member := range members {
					if member.NodeID != "" {
						t.Fatal("template carried an old binding")
					}
				}
			default:
				t.Fatalf("template carried address, credential or runtime metadata: %s", point.Type)
			}
		}
	}
}

func TestLedgerTemplateCopyBindingsAndScopePersist(t *testing.T) {
	dir, password := t.TempDir(), data.NewID()
	base, admin, _, stop := startInputServer(t, dir, password)
	root := snapshotRoot(t, base, admin)
	var site struct {
		ID      string `json:"id"`
		Created int    `json:"created"`
	}
	status, body := projectCall(t, http.MethodPost, base+"/nodes", admin, root, map[string]any{"parent": root, "description": "Pump Site", "site_template": "pump-house"}, &site)
	if status != 200 || site.Created != 5 {
		t.Fatalf("template: %d %s", status, body)
	}
	nodes := ledgerSnapshot(t, base, admin, root)
	area := ledgerNamed(t, nodes, site.ID, "泵房")
	unit := ledgerNamed(t, nodes, area.ID, "供水单元")
	drive := ledgerNamed(t, nodes, unit.ID, "泵驱动")
	pressure := ledgerNamed(t, nodes, area.ID, "总管压力测量")
	// New templates contain only structure, descriptions and unbound member roles.
	for _, node := range []data.NodeEdge{area, unit, drive, pressure} {
		for _, p := range node.Points {
			if p.Type != "description" && p.Type != "nodeType" && p.Type != domainaccess.MembersPoint {
				t.Fatalf("template carried nonstructural point: %s", p.Type)
			}
		}
	}
	createNodeAPI(t, base, admin, "variable", drive.ID, "Test observation", 42)
	members := []domainaccess.MemberRole{{Role: "drive", NodeID: drive.ID, Required: true}, {Role: "pressure", NodeID: pressure.ID, Required: true}, {Role: "optional"}}
	input := map[string]any{"description": "供水单元", "members_version": 0, "members": members}
	status, body = projectCall(t, http.MethodPatch, base+"/nodes/"+unit.ID, admin, root, input, nil)
	if status != 200 {
		t.Fatalf("bind: %d %s", status, body)
	}
	var copy struct {
		ID      string   `json:"id"`
		Created int      `json:"created"`
		Confirm []string `json:"confirm"`
	}
	status, body = projectCall(t, http.MethodPost, base+"/nodes/"+unit.ID+"/copy", admin, root, map[string]any{"parent": area.ID, "description": "Supply copy"}, &copy)
	if status != 200 || copy.Created != 3 || len(copy.Confirm) != 1 || copy.Confirm[0] != pressure.ID {
		t.Fatalf("copy: %d %s", status, body)
	}
	checkCopy := func() {
		t.Helper()
		nodes = ledgerSnapshot(t, base, admin, root)
		copiedUnit := ledgerNamed(t, nodes, area.ID, "Supply copy")
		copiedDrive := ledgerNamed(t, nodes, copiedUnit.ID, "泵驱动")
		copiedPoint := ledgerNamed(t, nodes, copiedDrive.ID, "Test observation")
		if _, found := copiedPoint.Points.Value("value", ""); found {
			t.Fatal("copy carried runtime observation")
		}
		version, _ := copiedUnit.Points.Value(domainaccess.MembersVersionPoint, "")
		if version != 0 {
			t.Fatalf("new independent copy inherited old edit revision: %v", version)
		}
		raw, _ := copiedUnit.Points.Text(domainaccess.MembersPoint, "")
		var roles []domainaccess.MemberRole
		if json.Unmarshal([]byte(raw), &roles) != nil || len(roles) != 3 || roles[0].NodeID != copiedDrive.ID || roles[0].NodeID == drive.ID || roles[1].NodeID != "" || !roles[1].Required {
			t.Fatalf("copy references: %s", raw)
		}
	}
	checkCopy()
	for _, typ := range []string{"site", "area", "unit", "device", "group"} {
		status, body = projectCall(t, http.MethodPost, base+"/nodes", admin, root, map[string]any{"parent": drive.ID, "type": typ, "description": "illegal child"}, nil)
		if status != 400 || !strings.Contains(body, "invalid_parent_type") {
			t.Fatalf("illegal %s beneath device: %d %s", typ, status, body)
		}
	}
	implementer := inputAccount(t, &pointConfigFixture{base: base, token: admin}, unit.ID, "implementer", nil)
	input["members_version"] = 1
	status, body = projectCall(t, http.MethodPatch, base+"/nodes/"+unit.ID, implementer, root, input, nil)
	if status != 403 {
		t.Fatalf("out-of-scope member reference: %d %s", status, body)
	}
	status, body = projectCall(t, http.MethodPost, base+"/nodes/"+unit.ID+"/copy", implementer, root, map[string]any{"parent": area.ID, "description": "forbidden copy"}, nil)
	if status != 400 || !strings.Contains(body, "invalid_copy") {
		t.Fatalf("out-of-scope destination: %d %s", status, body)
	}
	members[0].NodeID = area.ID
	input["members"] = members
	status, body = projectCall(t, http.MethodPatch, base+"/nodes/"+unit.ID, admin, root, input, nil)
	if status != 400 || !strings.Contains(body, "member_requires_device_or_point") {
		t.Fatalf("illegal member kind: %d %s", status, body)
	}
	stop()
	base, admin, _, _ = startInputServer(t, dir, password)
	checkCopy()
}
