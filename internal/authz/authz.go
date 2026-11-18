// Package authz owns the action vocabulary and the single resolution of a
// user's effective actions from role defaults or an explicit whitelist.
package authz

// Action names shared by the product API, EMS API and frontend.
const (
	Configure         = "configure"
	WriteValue        = "write_value"
	Command           = "command"
	RulePublish       = "rule_publish"
	AlarmAck          = "alarm_ack"
	AlarmMaintain     = "alarm_maintain"
	Delete            = "delete"
	Export            = "export"
	AccountManage     = "account_manage"
	AuditView         = "audit_view"
	SiteSettings      = "site_settings"
	VirtualInputApply = "virtual_input_apply"
	VirtualInputWrite = "virtual_input_write"
	CalculationApply  = "calculation_apply"
)

// All is the canonical action order exposed to clients.
var All = []string{
	Configure, WriteValue, Command, RulePublish, AlarmAck, AlarmMaintain,
	Delete, Export, AccountManage, AuditView, SiteSettings,
	VirtualInputApply, VirtualInputWrite, CalculationApply,
}

// Set is a resolved capability set.
type Set map[string]bool

func (s Set) Allowed(action string) bool { return s[action] }

// List returns allowed actions in canonical order.
func (s Set) List() []string {
	if s == nil {
		return nil
	}
	list := make([]string, 0, len(s))
	for _, action := range All {
		if s[action] {
			list = append(list, action)
		}
	}
	return list
}

func defaults(role string) []string {
	switch role {
	case "admin":
		return All
	case "implementer":
		return []string{
			Configure, RulePublish, Delete, Export,
			VirtualInputApply, CalculationApply,
			WriteValue, Command, AlarmAck, AlarmMaintain, VirtualInputWrite,
		}
	case "operator":
		return []string{WriteValue, Command, AlarmAck, AlarmMaintain, VirtualInputWrite}
	default:
		return nil
	}
}

// Resolve returns the effective actions. Admin always receives everything; a
// non-empty whitelist is absolute; otherwise the role default applies.
func Resolve(admin bool, role string, whitelist []string) Set {
	set := Set{}
	if admin {
		for _, action := range All {
			set[action] = true
		}
		return set
	}
	if len(whitelist) > 0 {
		for _, action := range whitelist {
			set[action] = true
		}
		return set
	}
	for _, action := range defaults(role) {
		set[action] = true
	}
	return set
}
