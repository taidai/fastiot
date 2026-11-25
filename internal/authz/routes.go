package authz

import (
	"encoding/json"
	"net/http"
	"strings"

	"fastiot/internal/industry"
)

type routeRequirement struct {
	method    string
	path      string
	action    string
	adminOnly bool
	reason    string
	targets   []targetSpec
	packageID string
}

// routeCalculation and routeVirtualInput are internal dispatch markers: the
// real action depends on the request's operation. They never leave Required as
// Requirement.Action.
const (
	routeCalculation  = "route_calculation"
	routeVirtualInput = "route_virtual_input"
)

func authenticatedRoute(method, path string) routeRequirement {
	return routeRequirement{method: method, path: path}
}

func actionRoute(method, path, action, reason string) routeRequirement {
	return routeRequirement{method: method, path: path, action: action, reason: reason}
}

// adminOnlyRoute declares a route restricted to admins without introducing an
// action: the restriction is carried by AdminOnly, not by a pseudo-action name.
func adminOnlyRoute(method, path, reason string) routeRequirement {
	return routeRequirement{method: method, path: path, adminOnly: true, reason: reason}
}

// inPackage marks a route as belonging to one installable industry package.
// The product API refuses such routes while the package is not installed, so
// the declaration is the single source of what a package owns.
func (r routeRequirement) inPackage(id string) routeRequirement {
	r.packageID = id
	return r
}

// routeRequirements declares every authenticated product and EMS route. An
// empty action means any authenticated identity may read or simulate; a route
// restricted to admins says so with adminOnly rather than with an action name.
// Targets declare direct object scope; list filters and derived references stay
// in the owning module.
var routeRequirements = []routeRequirement{
	authenticatedRoute(http.MethodGet, "ws"),
	authenticatedRoute(http.MethodGet, "metrics"),
	authenticatedRoute(http.MethodGet, "snapshot"),
	authenticatedRoute(http.MethodGet, "projects"),
	authenticatedRoute(http.MethodGet, "audit-settings"),
	actionRoute(http.MethodPut, "audit-settings", SiteSettings, "admin_required"),
	actionRoute(http.MethodGet, "audit", AuditView, "admin_required"),
	actionRoute(http.MethodGet, "audit/export", AuditView, "admin_required"),
	actionRoute(http.MethodGet, "recovery", SiteSettings, "admin_required"),
	authenticatedRoute(http.MethodGet, "history-settings"),
	actionRoute(http.MethodPut, "history-settings", SiteSettings, "admin_required"),
	actionRoute(http.MethodGet, "profile-verifications", Export, "admin_required"),
	authenticatedRoute(http.MethodGet, "deepseek/status"),
	actionRoute(http.MethodPost, "deepseek/preview", Configure, "admin_required"),
	actionRoute(http.MethodPost, "deepseek/generate", Configure, "admin_required"),
	actionRoute(http.MethodPost, "generated-screens/preview", Configure, "admin_required").bodyTarget("node_id"),
	actionRoute(http.MethodPost, "generated-screens/*/read", Configure, "admin_required"),
	actionRoute(http.MethodPost, "generated-screens/*/write", Configure, "admin_required"),
	actionRoute(http.MethodPost, "generated-screens/*/diagnostics", Configure, "admin_required"),
	actionRoute(http.MethodPost, "generated-screens/*/confirm", Configure, "admin_required"),
	actionRoute(http.MethodPost, "generated-screens/*/revision/preview", Configure, "admin_required"),
	actionRoute(http.MethodPost, "generated-screens/*/revision", Configure, "admin_required"),
	authenticatedRoute(http.MethodGet, "alarm-events"),
	authenticatedRoute(http.MethodGet, "alarm-stats"),
	authenticatedRoute(http.MethodGet, "device-ledger").queryTarget("site"),
	adminOnlyRoute(http.MethodGet, "delivery/check", "admin_required"),
	actionRoute(http.MethodGet, "delivery/evidence", Export, "admin_required"),
	actionRoute(http.MethodGet, "delivery/gates", Configure, "admin_required").queryTarget("site"),
	actionRoute(http.MethodPost, "delivery/preflight", Configure, "admin_required").queryTarget("site"),
	actionRoute(http.MethodGet, "delivery/preflight", Configure, "admin_required").queryTarget("site"),
	actionRoute(http.MethodGet, "projects/sources", Configure, "admin_required"),
	actionRoute(http.MethodGet, "projects/reuse/preview", Configure, "admin_required").queryTargetError("source", 400, "source_forbidden"),
	actionRoute(http.MethodPost, "projects/reuse", Configure, "admin_required").bodyTargetError("source", 400, "source_forbidden"),
	actionRoute(http.MethodPost, "projects/publish", Configure, "admin_required").bodyTargetError("site", 400, "source_forbidden"),
	actionRoute(http.MethodGet, "projects/versions", Configure, "admin_required").queryTarget("site"),
	actionRoute(http.MethodPost, "projects/rollback", Configure, "admin_required").bodyTarget("site"),
	actionRoute(http.MethodPost, "projects/upgrade/preview", Configure, "admin_required").bodyTargetError("site", 400, "source_forbidden"),
	actionRoute(http.MethodPost, "projects/upgrade", Configure, "admin_required").bodyTargetError("site", 400, "source_forbidden"),
	actionRoute(http.MethodGet, "projects/export", Export, "export_required").queryTargetError("site", 400, "source_forbidden"),
	actionRoute(http.MethodPost, "projects/import", Configure, "admin_required").bodyTarget("parent"),
	authenticatedRoute(http.MethodGet, "demand").queryTarget("site").inPackage(industry.EMSPackageID),
	authenticatedRoute(http.MethodGet, "projects/plan96").queryTarget("site").inPackage(industry.EMSPackageID),
	actionRoute(http.MethodPut, "projects/plan96", Configure, "admin_required").bodyTarget("site").inPackage(industry.EMSPackageID),
	authenticatedRoute(http.MethodGet, "history").queryTargetError("node_id", 403, "history_forbidden"),
	actionRoute(http.MethodGet, "history/export", Export, "export_required").queryTargetError("node_id", 403, "history_forbidden"),
	actionRoute(http.MethodPost, "templates", Configure, "admin_required").bodyTargetError("parent", 400, "invalid_template").bodyTarget("source_bus_id"),
	actionRoute(http.MethodPost, "templates/*/apply", Configure, "admin_required").pathTarget(1).bodyTarget("bus_id"),
	actionRoute(http.MethodPost, "modbus", Configure, "admin_required").bodyTargetError("parent", 400, "invalid_modbus_connection"),
	actionRoute(http.MethodPost, "modbus/test", Configure, "admin_required"),
	actionRoute(http.MethodPost, "modbus/*/line-test", Configure, "admin_required").pathTarget(1),
	actionRoute(http.MethodPut, "modbus/*", Configure, "admin_required").pathTarget(1),
	actionRoute(http.MethodPut, "modbus/io/*", Configure, "admin_required").pathTarget(2),
	actionRoute(http.MethodGet, "modbus/*/io/export", Export, "admin_required").pathTarget(1),
	actionRoute(http.MethodPost, "modbus/*/io/import", Configure, "admin_required").pathTarget(1),
	actionRoute(http.MethodPost, "modbus/*/io", Configure, "admin_required").pathTarget(1),
	authenticatedRoute(http.MethodGet, "modbus/*/units/*/model").pathTarget(1),
	actionRoute(http.MethodPut, "modbus/*/units/*/model", Configure, "admin_required").pathTarget(1),
	actionRoute(http.MethodPost, "modbus/*/units/apply", Configure, "admin_required").pathTarget(1),
	actionRoute(http.MethodGet, "modbus/*/units/*/commands", Command, "operator_required").pathTarget(1),
	actionRoute(http.MethodPost, "modbus/*/units/*/commands", Command, "operator_required").pathTarget(1),
	actionRoute(http.MethodGet, "modbus/*/units/*/commands/*", Command, "operator_required").pathTarget(1),
	authenticatedRoute(http.MethodGet, "calculations"),
	actionRoute(http.MethodPost, "calculations", Configure, "operation_not_allowed").bodyTarget("parent"),
	authenticatedRoute(http.MethodGet, "calculations/*").pathTarget(1),
	actionRoute(http.MethodPost, "calculations/*/*", routeCalculation, "operation_not_allowed").pathTarget(1),
	authenticatedRoute(http.MethodGet, "virtual-inputs"),
	actionRoute(http.MethodPost, "virtual-inputs", Configure, "operation_not_allowed").bodyTarget("parent"),
	authenticatedRoute(http.MethodGet, "virtual-inputs/*").pathTarget(1),
	actionRoute(http.MethodPost, "virtual-inputs/*/*", routeVirtualInput, "operation_not_allowed").pathTarget(1),
	actionRoute(http.MethodPost, "nodes", Configure, "admin_required").bodyTargetError("parent", 400, "invalid_node"),
	actionRoute(http.MethodPatch, "nodes/*", Configure, "admin_required").pathTarget(1),
	actionRoute(http.MethodDelete, "nodes/*", Delete, "admin_required").pathTarget(1).bodyTargetError("parent", 400, "invalid_parent"),
	actionRoute(http.MethodPost, "nodes/*/move", Configure, "admin_required").pathTarget(1).bodyTargetError("old_parent", 403, "source_forbidden").bodyTargetError("new_parent", 403, "destination_forbidden"),
	actionRoute(http.MethodPost, "nodes/*/value", WriteValue, "operation_not_allowed").pathTarget(1),
	actionRoute(http.MethodPost, "nodes/*/manual", WriteValue, "operation_not_allowed").pathTarget(1),
	actionRoute(http.MethodPost, "rules", Configure, "admin_required").bodyTargetError("parent", 400, "invalid_rule"),
	actionRoute(http.MethodPut, "rules/*/draft", Configure, "admin_required").pathTarget(1),
	actionRoute(http.MethodPost, "rules/*/publish", RulePublish, "admin_required").pathTarget(1),
	actionRoute(http.MethodPost, "rules/*/copy", Configure, "admin_required").pathTarget(1),
	actionRoute(http.MethodPost, "rules/*/restore", RulePublish, "admin_required").pathTarget(1),
	actionRoute(http.MethodPost, "rules/*/enable", RulePublish, "admin_required").pathTarget(1),
	authenticatedRoute(http.MethodPost, "rules/*/test").pathTarget(1),
	authenticatedRoute(http.MethodPost, "rules/*/scenarios").pathTarget(1),
	authenticatedRoute(http.MethodPost, "rules/*/diff").pathTarget(1),
	authenticatedRoute(http.MethodPost, "rules/*/replay").pathTarget(1),
	authenticatedRoute(http.MethodPost, "rules/*/deps").pathTarget(1),
	actionRoute(http.MethodPut, "tariff", SiteSettings, "admin_required").bodyTargetError("root", 403, "no_access").inPackage(industry.EMSPackageID),
	authenticatedRoute(http.MethodGet, "aggregate").queryTargetError("group", 403, "no_access"),
	actionRoute(http.MethodPut, "aggregate", Configure, "admin_required").bodyTargetError("group", 400, "invalid_group"),
	authenticatedRoute(http.MethodGet, "energy-units").inPackage(industry.EMSPackageID),
	actionRoute(http.MethodPost, "energy-units", Configure, "admin_required").bodyTarget("plant_id").inPackage(industry.EMSPackageID),
	authenticatedRoute(http.MethodGet, "energy-units/*/rule").pathTarget(1).inPackage(industry.EMSPackageID),
	actionRoute(http.MethodPut, "energy-units/*/rule", Configure, "admin_required").pathTarget(1).inPackage(industry.EMSPackageID),
	actionRoute(http.MethodPost, "energy-units/*/rule/publish", Configure, "admin_required").pathTarget(1).inPackage(industry.EMSPackageID),
	authenticatedRoute(http.MethodPost, "energy-units/*/rule/simulate").pathTarget(1).inPackage(industry.EMSPackageID),
	authenticatedRoute(http.MethodGet, "energy-units/*/allocator").pathTarget(1).inPackage(industry.EMSPackageID),
	actionRoute(http.MethodPut, "energy-units/*/allocator", Configure, "admin_required").pathTarget(1).inPackage(industry.EMSPackageID),
	authenticatedRoute(http.MethodGet, "energy-units/*/devices").pathTarget(1).inPackage(industry.EMSPackageID),
	actionRoute(http.MethodPost, "energy-units/*/devices", Configure, "admin_required").pathTarget(1).inPackage(industry.EMSPackageID),
	authenticatedRoute(http.MethodGet, "energy-units/*/capability-inputs").pathTarget(1).inPackage(industry.EMSPackageID),
	actionRoute(http.MethodPut, "energy-units/*/capability-inputs", Configure, "admin_required").pathTarget(1).inPackage(industry.EMSPackageID),
	authenticatedRoute(http.MethodGet, "energy-units/*/forecast-inputs").pathTarget(1).inPackage(industry.EMSPackageID),
	actionRoute(http.MethodPut, "energy-units/*/forecast-inputs", Configure, "admin_required").pathTarget(1).inPackage(industry.EMSPackageID),
	authenticatedRoute(http.MethodGet, "energy-units/*/dispatch-records").pathTarget(1).inPackage(industry.EMSPackageID),
	actionRoute(http.MethodPost, "energy-units/*/events", Command, "command_required").pathTarget(1).inPackage(industry.EMSPackageID),
	actionRoute(http.MethodPut, "energy-units/*/mode", Command, "command_required").pathTarget(1).inPackage(industry.EMSPackageID),
	actionRoute(http.MethodPost, "resource-groups", Configure, "admin_required").bodyTarget("members").inPackage(industry.EMSPackageID),
	authenticatedRoute(http.MethodGet, "resource-groups").inPackage(industry.EMSPackageID),
	actionRoute(http.MethodPost, "resource-groups/*/dispatch", Command, "command_required").pathTarget(1).inPackage(industry.EMSPackageID),
	authenticatedRoute(http.MethodGet, "opcua/channels"),
	actionRoute(http.MethodPost, "opcua/channels", Configure, "admin_required").bodyTarget("parent"),
	authenticatedRoute(http.MethodGet, "opcua/channels/*/points").pathTarget(2),
	actionRoute(http.MethodPost, "opcua/channels/*/points", Configure, "admin_required").pathTarget(2),
	authenticatedRoute(http.MethodGet, "sites/*/target").pathTarget(1).inPackage(industry.EMSPackageID),
	actionRoute(http.MethodPut, "sites/*/target", Configure, "admin_required").pathTarget(1).inPackage(industry.EMSPackageID),
	authenticatedRoute(http.MethodGet, "plants").inPackage(industry.EMSPackageID),
	authenticatedRoute(http.MethodGet, "plants/*/pcc").pathTarget(1).inPackage(industry.EMSPackageID),
	actionRoute(http.MethodPut, "plants/*/pcc", Configure, "admin_required").pathTarget(1).inPackage(industry.EMSPackageID),
	authenticatedRoute(http.MethodGet, "plants/*/phase-verdict").pathTarget(1).inPackage(industry.EMSPackageID),
	authenticatedRoute(http.MethodGet, "plants/*/strategy").pathTarget(1).inPackage(industry.EMSPackageID),
	actionRoute(http.MethodPut, "plants/*/strategy", Configure, "admin_required").pathTarget(1).inPackage(industry.EMSPackageID),
	actionRoute(http.MethodPut, "safety", Configure, "admin_required").bodyTargetError("node", 400, "invalid_safety"),
	authenticatedRoute(http.MethodGet, "alarm-types").queryTargetError("root", 400, "invalid_root"),
	actionRoute(http.MethodPut, "alarm-types", Configure, "admin_required").queryTargetError("root", 400, "invalid_root"),
	actionRoute(http.MethodDelete, "alarm-types", Configure, "admin_required").queryTargetError("root", 400, "invalid_root"),
	actionRoute(http.MethodPost, "users", AccountManage, "admin_required").bodyTargetError("parent", 400, "invalid_user"),
	actionRoute(http.MethodGet, "users", AccountManage, "admin_required"),
	actionRoute(http.MethodPut, "users/*", AccountManage, "admin_required"),
	actionRoute(http.MethodPost, "users/*/password", AccountManage, "admin_required"),
	authenticatedRoute(http.MethodGet, "ems/screen/*").pathTarget(2),
	actionRoute(http.MethodPut, "ems/screen/*", Configure, "admin_required").pathTarget(2),
	actionRoute(http.MethodPost, "ems/acceptance/run", Configure, "admin_required"),
	authenticatedRoute(http.MethodGet, "ems/acceptance/latest"),
	authenticatedRoute(http.MethodGet, "ems/allocators/*").pathTarget(2).inPackage(industry.EMSPackageID),
	actionRoute(http.MethodPut, "ems/allocators/*", Configure, "admin_required").pathTarget(2).inPackage(industry.EMSPackageID),
	authenticatedRoute(http.MethodPost, "ems/allocators/*/simulate").pathTarget(2).inPackage(industry.EMSPackageID),
	authenticatedRoute(http.MethodGet, "ems/allocators/*/history").pathTarget(2).inPackage(industry.EMSPackageID),
	authenticatedRoute(http.MethodGet, "ems/quality"),
	actionRoute(http.MethodGet, "ems/frames", Command, "operator_required"),
	actionRoute(http.MethodDelete, "ems/frames", Command, "operator_required"),
	authenticatedRoute(http.MethodGet, "ems/state"),
	authenticatedRoute(http.MethodGet, "ems/channels"),
	actionRoute(http.MethodPost, "ems/channels", Configure, "admin_required").bodyTarget("parent"),
	actionRoute(http.MethodDelete, "ems/channels/*", Delete, "admin_required").pathTarget(2),
	actionRoute(http.MethodPost, "ems/channels/*/restart", Configure, "admin_required").pathTarget(2),
	authenticatedRoute(http.MethodGet, "ems/alarms"),
	actionRoute(http.MethodPost, "ems/alarms/*/ack", AlarmAck, "operator_required").alarmTarget(2),
	actionRoute(http.MethodPost, "ems/alarms/*/close", AlarmAck, "operator_required").alarmTarget(2),
	actionRoute(http.MethodPost, "ems/alarms/*/maintenance", AlarmMaintain, "operator_required").alarmTarget(2),
	actionRoute(http.MethodDelete, "ems/alarms/*/maintenance", AlarmMaintain, "operator_required").alarmTarget(2),
	authenticatedRoute(http.MethodGet, "ems/history").queryTarget("point_id"),
	actionRoute(http.MethodGet, "ems/history/export", Export, "export_required").queryTarget("point_id"),
	authenticatedRoute(http.MethodGet, "ems/runtime"),
	actionRoute(http.MethodPost, "ems/scan", Configure, "admin_required"),
	actionRoute(http.MethodPost, "ems/identify", Configure, "admin_required"),
	authenticatedRoute(http.MethodGet, "ems/profiles"),
	actionRoute(http.MethodGet, "ems/profiles/*/points.csv", Export, "export_required"),
	authenticatedRoute(http.MethodPost, "ems/allocation/preview").inPackage(industry.EMSPackageID),
	authenticatedRoute(http.MethodGet, "industry/packages"),
	actionRoute(http.MethodPost, "industry/packages", SiteSettings, "admin_required"),
	actionRoute(http.MethodDelete, "industry/packages/*", SiteSettings, "admin_required"),
	authenticatedRoute(http.MethodGet, "templates"),
	actionRoute(http.MethodPost, "access/batch", Configure, "admin_required"),
	actionRoute(http.MethodPost, "nodes/*/copy", Configure, "admin_required").pathTarget(1).bodyTargetError("parent", 400, "invalid_copy"),
	authenticatedRoute(http.MethodGet, "modbus/*/units/*/verify").pathTarget(1),
	authenticatedRoute(http.MethodPost, "modbus/*/units/*/verify").pathTarget(1),
	authenticatedRoute(http.MethodGet, "state-machines"),
	actionRoute(http.MethodPost, "state-machines", Configure, "operation_not_allowed").bodyTarget("parent"),
	authenticatedRoute(http.MethodGet, "state-machines/*").pathTarget(1),
	actionRoute(http.MethodPost, "state-machines/*/draft", Configure, "operation_not_allowed").pathTarget(1),
	actionRoute(http.MethodPost, "state-machines/*/publish", RulePublish, "operation_not_allowed").pathTarget(1),
	authenticatedRoute(http.MethodPost, "state-machines/*/simulate").pathTarget(1),
	actionRoute(http.MethodPost, "state-machines/*/execute", Command, "operation_not_allowed").pathTarget(1),
	authenticatedRoute(http.MethodGet, "overview/handover").queryTarget("layer"),
	actionRoute(http.MethodPut, "overview/handover", Command, "operator_required").queryTarget("layer"),
}

func matchRoutePath(pattern, path string) bool {
	want, got := strings.Split(pattern, "/"), strings.Split(path, "/")
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		if want[i] != "*" && want[i] != got[i] {
			return false
		}
	}
	return true
}

func requestOperation(r *http.Request) string {
	fields, ok := requestJSONFields(r)
	if !ok {
		return ""
	}
	var action string
	if json.Unmarshal(fields["action"], &action) != nil {
		return ""
	}
	return action
}

func resolvedRouteAction(r *http.Request, route routeRequirement) string {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	operation := parts[len(parts)-1]
	switch route.action {
	case routeCalculation:
		switch operation {
		case "apply", "impact":
			return CalculationApply
		case "draft", "trial":
			return Configure
		}
	case routeVirtualInput:
		if operation == "impact" {
			operation = requestOperation(r)
		}
		switch operation {
		case "apply":
			return VirtualInputApply
		case "value":
			return VirtualInputWrite
		case "draft":
			return Configure
		}
	}
	return route.action
}

// Requirement is the authorization declared for one HTTP route.
type Requirement struct {
	// Action is a canonical action name from this package, or empty when the
	// route requires no action. Admin-only routes keep it empty: their
	// restriction is expressed by AdminOnly alone.
	Action string
	Reason string
	// AdminOnly restricts the route to admins regardless of action grants.
	AdminOnly     bool
	Targets       []Target
	UnknownTarget *Target
	// Package is the industry package that owns the route. Empty means the
	// route belongs to the base product and is always available.
	Package string
}

// Allowed applies the declaration using the caller's resolved action set.
func (r Requirement) Allowed(admin bool, can func(string) bool) bool {
	if r.AdminOnly {
		return admin
	}
	return r.Action == "" || can(r.Action)
}

// ReasonRouteNotDeclared is the denial reason for a request that matches no
// route declaration. Callers must treat an unresolved route as forbidden: a
// missing table entry is a defect, never a reason to serve the request.
const ReasonRouteNotDeclared = "route_not_declared"

// Required resolves the route declaration. The bool reports whether the route
// is declared; callers must deny the request when it is false. An unresolved
// impact body fails closed for non-admin identities instead of bypassing its
// dynamic action.
func Required(r *http.Request) (Requirement, bool) {
	path := strings.TrimPrefix(strings.Trim(r.URL.Path, "/"), "api/")
	for _, route := range routeRequirements {
		if route.method != r.Method || !matchRoutePath(route.path, path) {
			continue
		}
		action := resolvedRouteAction(r, route)
		requirement := Requirement{Reason: route.reason, Package: route.packageID}
		requirement.AdminOnly = route.adminOnly || (action == routeVirtualInput && strings.HasSuffix(path, "/impact"))
		if action == routeCalculation || action == routeVirtualInput {
			action = ""
		}
		requirement.Action = action
		requirement.Targets, requirement.UnknownTarget = resolveTargets(r, route)
		return requirement, true
	}
	return Requirement{}, false
}
