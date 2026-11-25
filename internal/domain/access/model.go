// Package access contains the pure modeling and validation rules for generic
// site access. Persistence and transports stay in appapi and the existing EMS
// packages; this package does not define a second device model.
package access

import (
	"fmt"
	"strings"
)

const (
	NodeSite       = "site"
	NodeSiteRoot   = "siteRoot"
	NodePlant      = "plant"
	NodeArea       = "area"
	NodeUnit       = "unit"
	NodeEnergyUnit = "energyUnit"
	NodeDevice     = "device"

	MembersPoint      = "easyemsUnitMembers"
	VerificationPoint = "easyemsAccessVerification"
)

// MemberRole binds one stable business role to an existing Device or Point.
// An empty NodeID is a valid incomplete draft; Required reports the resulting
// capability gap without inventing a target.
type MemberRole struct {
	Role     string `json:"role"`
	NodeID   string `json:"node_id,omitempty"`
	Required bool   `json:"required"`
}

// TemplateNode is one structural placeholder. Templates never carry a channel,
// station, credential, live value or source node ID.
type TemplateNode struct {
	Name     string         `json:"name"`
	Type     string         `json:"type"`
	Members  []MemberRole   `json:"members,omitempty"`
	Children []TemplateNode `json:"children,omitempty"`
}

type SiteTemplate struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Nodes       []TemplateNode `json:"nodes"`
}

// CanPlace is the server-side hierarchy constraint for newly created business
// nodes. Legacy group/variable creation keeps its existing behavior in appapi.
func CanPlace(childType, parentType string, parentIsRoot bool) bool {
	if parentIsRoot {
		parentType = NodeSiteRoot
	}
	if childType == NodeSite {
		return parentIsRoot || parentType == NodeSiteRoot
	}
	switch parentType {
	case NodeSiteRoot, NodeSite:
		return childType == NodeArea || childType == NodeUnit || childType == NodeEnergyUnit || childType == NodeDevice || childType == NodePlant
	case NodePlant:
		return childType == NodeArea || childType == NodeUnit || childType == NodeEnergyUnit || childType == NodeDevice
	case NodeArea, "group":
		return childType == NodeArea || childType == NodeUnit || childType == NodeEnergyUnit || childType == NodeDevice
	case NodeUnit, NodeEnergyUnit:
		return childType == NodeDevice
	default:
		return false
	}
}

// AllowedChildTypes drives the same choices in the UI; the backend remains the
// authority and re-checks every write.
func AllowedChildTypes(parentType string, parentIsRoot bool) []string {
	candidates := []string{NodeSite, NodePlant, NodeArea, NodeUnit, NodeEnergyUnit, NodeDevice}
	allowed := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if CanPlace(candidate, parentType, parentIsRoot) {
			allowed = append(allowed, candidate)
		}
	}
	return allowed
}

func SiteTemplates() []SiteTemplate {
	return []SiteTemplate{
		{ID: "blank", Name: "空白现场", Description: "只建现场根对象，再逐层搭建。", Nodes: []TemplateNode{}},
		{ID: "pump-house", Name: "供水泵房", Description: "区域、供水单元、泵驱动与压力测量占位。", Nodes: []TemplateNode{
			{Name: "泵房", Type: NodeArea, Children: []TemplateNode{
				{Name: "供水单元", Type: NodeUnit, Members: []MemberRole{{Role: "驱动", Required: true}, {Role: "压力测量", Required: true}}, Children: []TemplateNode{{Name: "泵驱动", Type: NodeDevice}}},
				{Name: "总管压力测量", Type: NodeDevice},
			}},
		}},
		{ID: "storage", Name: "储能现场", Description: "储能区、能源单元与 PCS/BMS/电表占位。", Nodes: []TemplateNode{
			{Name: "储能区", Type: NodeArea, Children: []TemplateNode{
				{Name: "储能单元", Type: NodeEnergyUnit, Members: []MemberRole{{Role: "功率执行", Required: true}, {Role: "电池管理", Required: true}, {Role: "单元电表"}}, Children: []TemplateNode{
					{Name: "PCS", Type: NodeDevice}, {Name: "BMS", Type: NodeDevice}, {Name: "单元电表", Type: NodeDevice},
				}},
			}},
		}},
	}
}

func SiteTemplateByID(id string) (SiteTemplate, bool) {
	for _, template := range SiteTemplates() {
		if template.ID == id {
			return template, true
		}
	}
	return SiteTemplate{}, false
}

func ValidateSiteTemplate(template SiteTemplate) error {
	for _, node := range template.Nodes {
		if err := validateTemplateNode(node, NodeSite); err != nil {
			return err
		}
	}
	return nil
}

func validateTemplateNode(node TemplateNode, parentType string) error {
	if strings.TrimSpace(node.Name) == "" || !CanPlace(node.Type, parentType, false) {
		return fmt.Errorf("invalid template node %s (%s below %s)", node.Name, node.Type, parentType)
	}
	if err := ValidateMembers(node.Members); err != nil {
		return err
	}
	for _, child := range node.Children {
		if err := validateTemplateNode(child, node.Type); err != nil {
			return err
		}
	}
	return nil
}

func ValidateMembers(members []MemberRole) error {
	seen := map[string]bool{}
	for _, member := range members {
		role := strings.TrimSpace(member.Role)
		if role == "" {
			return fmt.Errorf("member role is required")
		}
		key := strings.ToLower(role)
		if seen[key] {
			return fmt.Errorf("duplicate member role %s", role)
		}
		seen[key] = true
	}
	return nil
}

// CopyableNodeType limits subtree copy to business structure and simple points.
// Connections, credentials and executable rule documents require their own
// delivery/rebinding flows and are not silently duplicated here.
func CopyableNodeType(nodeType string) bool {
	switch nodeType {
	case NodeSite, NodePlant, NodeArea, "group", NodeUnit, NodeEnergyUnit, NodeDevice, "variable":
		return true
	default:
		return false
	}
}

// RewriteMembers rewrites references contained in the copied subtree. External
// references become explicit unbound gaps instead of pointing back to the
// source site's devices.
func RewriteMembers(members []MemberRole, idMap map[string]string) ([]MemberRole, []string) {
	rewritten := append([]MemberRole(nil), members...)
	unresolved := []string{}
	for i := range rewritten {
		if rewritten[i].NodeID == "" {
			continue
		}
		if mapped, ok := idMap[rewritten[i].NodeID]; ok {
			rewritten[i].NodeID = mapped
			continue
		}
		unresolved = append(unresolved, rewritten[i].NodeID)
		rewritten[i].NodeID = ""
	}
	return rewritten, unresolved
}
