package access

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCanPlaceEnforcesBusinessHierarchy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, child, parent string
		root, want          bool
	}{
		{name: "site below instance root", child: NodeSite, parent: NodeSiteRoot, root: true, want: true},
		{name: "device below stored device root", child: NodeDevice, parent: NodeDevice, root: true, want: true},
		{name: "area below stored device root", child: NodeArea, parent: NodeDevice, root: true, want: true},
		{name: "area below site", child: NodeArea, parent: NodeSite, want: true},
		{name: "legacy group behaves as area", child: NodeUnit, parent: "group", want: true},
		{name: "unit below area", child: NodeUnit, parent: NodeArea, want: true},
		{name: "device below unit", child: NodeDevice, parent: NodeUnit, want: true},
		{name: "energy device below energy unit", child: NodeDevice, parent: NodeEnergyUnit, want: true},
		{name: "plant below site", child: NodePlant, parent: NodeSite, want: true},
		{name: "energy unit below plant", child: NodeEnergyUnit, parent: NodePlant, want: true},
		{name: "energy unit below generic area", child: NodeEnergyUnit, parent: NodeArea, want: true},
		{name: "energy unit below site without plant", child: NodeEnergyUnit, parent: NodeSite, want: true},
		{name: "no nested site", child: NodeSite, parent: NodeSite, want: false},
		{name: "no area below unit", child: NodeArea, parent: NodeUnit, want: false},
		{name: "no nested unit", child: NodeUnit, parent: NodeUnit, want: false},
		{name: "no children below device", child: NodeDevice, parent: NodeDevice, want: false},
		{name: "no business node below modbus", child: NodeDevice, parent: "modbus", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := CanPlace(test.child, test.parent, test.root); got != test.want {
				t.Fatalf("CanPlace(%q, %q, %v) = %v, want %v", test.child, test.parent, test.root, got, test.want)
			}
		})
	}
}

func TestSiteTemplatesContainStructureAndRolesOnly(t *testing.T) {
	t.Parallel()
	templates := SiteTemplates()
	if len(templates) != 3 {
		t.Fatalf("templates = %d, want blank, pump and storage", len(templates))
	}
	for _, template := range templates {
		if err := ValidateSiteTemplate(template); err != nil {
			t.Errorf("built-in template %s violates hierarchy: %v", template.ID, err)
		}
		encoded, err := json.Marshal(template)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ToLower(string(encoded))
		for _, forbidden := range []string{"password", "token", "secret", "uri", "baud", "station"} {
			if strings.Contains(text, forbidden) {
				t.Errorf("template %s contains connection field %q", template.ID, forbidden)
			}
		}
	}
	pump, ok := SiteTemplateByID("pump-house")
	if !ok || len(pump.Nodes) == 0 || len(pump.Nodes[0].Children) == 0 {
		t.Fatalf("pump template has no reusable structure: %+v", pump)
	}
	storage, ok := SiteTemplateByID("storage")
	if !ok || len(storage.Nodes) == 0 || storage.Nodes[0].Type != NodeArea {
		t.Fatalf("storage template does not start with an area: %+v", storage)
	}
}

func TestValidateSiteTemplateRejectsInvalidNestedType(t *testing.T) {
	t.Parallel()
	template := SiteTemplate{ID: "bad", Nodes: []TemplateNode{{Name: "device", Type: NodeDevice, Children: []TemplateNode{{Name: "area", Type: NodeArea}}}}}
	if err := ValidateSiteTemplate(template); err == nil {
		t.Fatal("template with an area below a device passed validation")
	}
}

func TestValidateMembersAllowsDraftButRejectsDuplicateRoles(t *testing.T) {
	t.Parallel()
	valid := []MemberRole{{Role: "驱动", Required: true}, {Role: "压力", NodeID: "meter-1"}}
	if err := ValidateMembers(valid); err != nil {
		t.Fatalf("draft member roles rejected: %v", err)
	}
	for name, members := range map[string][]MemberRole{
		"empty role":     {{NodeID: "device-1"}},
		"duplicate role": {{Role: "驱动"}, {Role: " 驱动 ", NodeID: "device-2"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateMembers(members); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestCopyRulesKeepStructureAndRebindOnlyInternalMembers(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{NodeSite, NodePlant, NodeArea, "group", NodeUnit, NodeEnergyUnit, NodeDevice, "variable"} {
		if !CopyableNodeType(typ) {
			t.Errorf("structural type %s is not copyable", typ)
		}
	}
	for _, typ := range []string{"modbus", "modbusIo", "user", "deviceCred", "decisionTable"} {
		if CopyableNodeType(typ) {
			t.Errorf("connection, identity or rule type %s must not be copied as structure", typ)
		}
	}
	members := []MemberRole{{Role: "驱动", NodeID: "old-device", Required: true}, {Role: "外部测量", NodeID: "outside", Required: true}, {Role: "待绑定", Required: false}}
	rewritten, unresolved := RewriteMembers(members, map[string]string{"old-device": "new-device"})
	if len(unresolved) != 1 || unresolved[0] != "outside" {
		t.Fatalf("unresolved = %#v, want outside", unresolved)
	}
	if rewritten[0].NodeID != "new-device" || rewritten[1].NodeID != "" || rewritten[2].NodeID != "" {
		t.Fatalf("rewritten members = %+v", rewritten)
	}
}
