package authz

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

type routeRequirement struct {
	method string
	path   string
	action string
	reason string
}

const (
	routeAdmin        = "route_admin"
	routeCalculation  = "route_calculation"
	routeVirtualInput = "route_virtual_input"
)

func authenticatedRoute(method, path string) routeRequirement {
	return routeRequirement{method: method, path: path}
}

func actionRoute(method, path, action, reason string) routeRequirement {
	return routeRequirement{method: method, path: path, action: action, reason: reason}
}

// routeRequirements declares every authenticated product and EMS route. An
// empty action means any authenticated identity may read or simulate; object
// scope remains enforced by the owning handler.
var routeRequirements = []routeRequirement{
	authenticatedRoute(http.MethodGet, "ws"),
	authenticatedRoute(http.MethodGet, "metrics"),
	authenticatedRoute(http.MethodGet, "snapshot"),
	authenticatedRoute(http.MethodGet, "audit-settings"),
	actionRoute(http.MethodPut, "audit-settings", SiteSettings, "admin_required"),
	actionRoute(http.MethodGet, "audit", AuditView, "admin_required"),
	actionRoute(http.MethodGet, "audit/export", AuditView, "admin_required"),
	authenticatedRoute(http.MethodGet, "history-settings"),
	actionRoute(http.MethodPut, "history-settings", SiteSettings, "admin_required"),
	actionRoute(http.MethodGet, "profile-verifications", Export, "admin_required"),
	authenticatedRoute(http.MethodGet, "alarm-events"),
	authenticatedRoute(http.MethodGet, "alarm-stats"),
	authenticatedRoute(http.MethodGet, "device-ledger"),
	actionRoute(http.MethodGet, "delivery/check", routeAdmin, "admin_required"),
	actionRoute(http.MethodGet, "delivery/evidence", Export, "admin_required"),
	actionRoute(http.MethodGet, "delivery/gates", Configure, "admin_required"),
	actionRoute(http.MethodPost, "delivery/preflight", Configure, "admin_required"),
	actionRoute(http.MethodGet, "delivery/preflight", Configure, "admin_required"),
	actionRoute(http.MethodGet, "projects/sources", Configure, "admin_required"),
	actionRoute(http.MethodGet, "projects/reuse/preview", Configure, "admin_required"),
	actionRoute(http.MethodPost, "projects/reuse", Configure, "admin_required"),
	actionRoute(http.MethodPost, "projects/publish", Configure, "admin_required"),
	actionRoute(http.MethodGet, "projects/versions", Configure, "admin_required"),
	actionRoute(http.MethodPost, "projects/rollback", Configure, "admin_required"),
	authenticatedRoute(http.MethodGet, "demand"),
	authenticatedRoute(http.MethodGet, "projects/plan96"),
	actionRoute(http.MethodPut, "projects/plan96", Configure, "admin_required"),
	authenticatedRoute(http.MethodGet, "history"),
	actionRoute(http.MethodGet, "history/export", Export, "export_required"),
	actionRoute(http.MethodPost, "templates", Configure, "admin_required"),
	actionRoute(http.MethodPost, "templates/*/apply", Configure, "admin_required"),
	actionRoute(http.MethodPost, "modbus", Configure, "admin_required"),
	actionRoute(http.MethodPost, "modbus/test", Configure, "admin_required"),
	actionRoute(http.MethodPost, "modbus/*/line-test", Configure, "admin_required"),
	actionRoute(http.MethodPut, "modbus/*", Configure, "admin_required"),
	actionRoute(http.MethodPut, "modbus/io/*", Configure, "admin_required"),
	actionRoute(http.MethodGet, "modbus/*/io/export", Export, "admin_required"),
	actionRoute(http.MethodPost, "modbus/*/io/import", Configure, "admin_required"),
	actionRoute(http.MethodPost, "modbus/*/io", Configure, "admin_required"),
	authenticatedRoute(http.MethodGet, "modbus/*/units/*/model"),
	actionRoute(http.MethodPut, "modbus/*/units/*/model", Configure, "admin_required"),
	actionRoute(http.MethodPost, "modbus/*/units/apply", Configure, "admin_required"),
	actionRoute(http.MethodGet, "modbus/*/units/*/commands", Command, "operator_required"),
	actionRoute(http.MethodPost, "modbus/*/units/*/commands", Command, "operator_required"),
	actionRoute(http.MethodGet, "modbus/*/units/*/commands/*", Command, "operator_required"),
	authenticatedRoute(http.MethodGet, "calculations"),
	actionRoute(http.MethodPost, "calculations", Configure, "operation_not_allowed"),
	authenticatedRoute(http.MethodGet, "calculations/*"),
	actionRoute(http.MethodPost, "calculations/*/*", routeCalculation, "operation_not_allowed"),
	authenticatedRoute(http.MethodGet, "virtual-inputs"),
	actionRoute(http.MethodPost, "virtual-inputs", Configure, "operation_not_allowed"),
	authenticatedRoute(http.MethodGet, "virtual-inputs/*"),
	actionRoute(http.MethodPost, "virtual-inputs/*/*", routeVirtualInput, "operation_not_allowed"),
	actionRoute(http.MethodPost, "nodes", Configure, "admin_required"),
	actionRoute(http.MethodPatch, "nodes/*", Configure, "admin_required"),
	actionRoute(http.MethodDelete, "nodes/*", Delete, "admin_required"),
	actionRoute(http.MethodPost, "nodes/*/move", Configure, "admin_required"),
	actionRoute(http.MethodPost, "nodes/*/value", WriteValue, "operation_not_allowed"),
	actionRoute(http.MethodPost, "nodes/*/manual", WriteValue, "operation_not_allowed"),
	actionRoute(http.MethodPost, "rules", Configure, "admin_required"),
	actionRoute(http.MethodPut, "rules/*/draft", Configure, "admin_required"),
	actionRoute(http.MethodPost, "rules/*/publish", RulePublish, "admin_required"),
	actionRoute(http.MethodPost, "rules/*/copy", Configure, "admin_required"),
	actionRoute(http.MethodPost, "rules/*/restore", RulePublish, "admin_required"),
	actionRoute(http.MethodPost, "rules/*/enable", RulePublish, "admin_required"),
	authenticatedRoute(http.MethodPost, "rules/*/test"),
	authenticatedRoute(http.MethodPost, "rules/*/scenarios"),
	authenticatedRoute(http.MethodPost, "rules/*/diff"),
	authenticatedRoute(http.MethodPost, "rules/*/replay"),
	authenticatedRoute(http.MethodPost, "rules/*/deps"),
	actionRoute(http.MethodPut, "tariff", SiteSettings, "admin_required"),
	authenticatedRoute(http.MethodGet, "aggregate"),
	actionRoute(http.MethodPut, "aggregate", Configure, "admin_required"),
	authenticatedRoute(http.MethodGet, "energy-units"),
	actionRoute(http.MethodPost, "energy-units", Configure, "admin_required"),
	authenticatedRoute(http.MethodGet, "energy-units/*/rule"),
	actionRoute(http.MethodPut, "energy-units/*/rule", Configure, "admin_required"),
	actionRoute(http.MethodPost, "energy-units/*/rule/publish", Configure, "admin_required"),
	authenticatedRoute(http.MethodPost, "energy-units/*/rule/simulate"),
	authenticatedRoute(http.MethodGet, "energy-units/*/allocator"),
	actionRoute(http.MethodPut, "energy-units/*/allocator", Configure, "admin_required"),
	authenticatedRoute(http.MethodGet, "energy-units/*/devices"),
	actionRoute(http.MethodPost, "energy-units/*/devices", Configure, "admin_required"),
	authenticatedRoute(http.MethodGet, "energy-units/*/capability-inputs"),
	actionRoute(http.MethodPut, "energy-units/*/capability-inputs", Configure, "admin_required"),
	authenticatedRoute(http.MethodGet, "energy-units/*/forecast-inputs"),
	actionRoute(http.MethodPut, "energy-units/*/forecast-inputs", Configure, "admin_required"),
	authenticatedRoute(http.MethodGet, "energy-units/*/dispatch-records"),
	actionRoute(http.MethodPost, "energy-units/*/events", Command, "command_required"),
	actionRoute(http.MethodPut, "energy-units/*/mode", Command, "command_required"),
	actionRoute(http.MethodPost, "resource-groups", Configure, "admin_required"),
	authenticatedRoute(http.MethodGet, "resource-groups"),
	actionRoute(http.MethodPost, "resource-groups/*/dispatch", Command, "command_required"),
	authenticatedRoute(http.MethodGet, "opcua/channels"),
	actionRoute(http.MethodPost, "opcua/channels", Configure, "admin_required"),
	authenticatedRoute(http.MethodGet, "opcua/channels/*/points"),
	actionRoute(http.MethodPost, "opcua/channels/*/points", Configure, "admin_required"),
	authenticatedRoute(http.MethodGet, "sites/*/target"),
	actionRoute(http.MethodPut, "sites/*/target", Configure, "admin_required"),
	authenticatedRoute(http.MethodGet, "plants"),
	authenticatedRoute(http.MethodGet, "plants/*/pcc"),
	actionRoute(http.MethodPut, "plants/*/pcc", Configure, "admin_required"),
	authenticatedRoute(http.MethodGet, "plants/*/phase-verdict"),
	authenticatedRoute(http.MethodGet, "plants/*/strategy"),
	actionRoute(http.MethodPut, "plants/*/strategy", Configure, "admin_required"),
	actionRoute(http.MethodPut, "safety", Configure, "admin_required"),
	authenticatedRoute(http.MethodGet, "alarm-types"),
	actionRoute(http.MethodPut, "alarm-types", Configure, "admin_required"),
	actionRoute(http.MethodDelete, "alarm-types", Configure, "admin_required"),
	actionRoute(http.MethodPost, "users", AccountManage, "admin_required"),
	actionRoute(http.MethodGet, "users", AccountManage, "admin_required"),
	actionRoute(http.MethodPut, "users/*", AccountManage, "admin_required"),
	actionRoute(http.MethodPost, "users/*/password", AccountManage, "admin_required"),
	authenticatedRoute(http.MethodGet, "ems/screen/*"),
	actionRoute(http.MethodPut, "ems/screen/*", Configure, "admin_required"),
	actionRoute(http.MethodPost, "ems/acceptance/run", Configure, "admin_required"),
	authenticatedRoute(http.MethodGet, "ems/acceptance/latest"),
	authenticatedRoute(http.MethodGet, "ems/allocators/*"),
	actionRoute(http.MethodPut, "ems/allocators/*", Configure, "admin_required"),
	authenticatedRoute(http.MethodPost, "ems/allocators/*/simulate"),
	authenticatedRoute(http.MethodGet, "ems/allocators/*/history"),
	authenticatedRoute(http.MethodGet, "ems/quality"),
	actionRoute(http.MethodGet, "ems/frames", Command, "operator_required"),
	actionRoute(http.MethodDelete, "ems/frames", Command, "operator_required"),
	authenticatedRoute(http.MethodGet, "ems/state"),
	authenticatedRoute(http.MethodGet, "ems/channels"),
	actionRoute(http.MethodPost, "ems/channels", Configure, "admin_required"),
	actionRoute(http.MethodDelete, "ems/channels/*", Delete, "admin_required"),
	actionRoute(http.MethodPost, "ems/channels/*/restart", Configure, "admin_required"),
	authenticatedRoute(http.MethodGet, "ems/alarms"),
	actionRoute(http.MethodPost, "ems/alarms/*/ack", AlarmAck, "operator_required"),
	actionRoute(http.MethodPost, "ems/alarms/*/close", AlarmAck, "operator_required"),
	actionRoute(http.MethodPost, "ems/alarms/*/maintenance", AlarmMaintain, "operator_required"),
	actionRoute(http.MethodDelete, "ems/alarms/*/maintenance", AlarmMaintain, "operator_required"),
	authenticatedRoute(http.MethodGet, "ems/history"),
	actionRoute(http.MethodGet, "ems/history/export", Export, "export_required"),
	authenticatedRoute(http.MethodGet, "ems/runtime"),
	actionRoute(http.MethodPost, "ems/scan", Configure, "admin_required"),
	actionRoute(http.MethodPost, "ems/identify", Configure, "admin_required"),
	authenticatedRoute(http.MethodGet, "ems/profiles"),
	actionRoute(http.MethodGet, "ems/profiles/*/points.csv", Export, "export_required"),
	authenticatedRoute(http.MethodPost, "ems/allocation/preview"),
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
	if r.Body == nil {
		return ""
	}
	original := r.Body
	prefix, _ := io.ReadAll(io.LimitReader(original, 1<<20))
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(prefix), original), original}
	var input struct {
		Action string `json:"action"`
	}
	if json.Unmarshal(prefix, &input) != nil {
		return ""
	}
	return input.Action
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
	Action    string
	Reason    string
	AdminOnly bool
}

// Allowed applies the declaration using the caller's resolved action set.
func (r Requirement) Allowed(admin bool, can func(string) bool) bool {
	if r.AdminOnly {
		return admin
	}
	return r.Action == "" || can(r.Action)
}

// Required resolves the route declaration. Unknown routes and operations
// retain the owning handler's 404 behavior. An unresolved impact body fails
// closed for non-admin identities instead of bypassing its dynamic action.
func Required(r *http.Request) (Requirement, bool) {
	path := strings.TrimPrefix(strings.Trim(r.URL.Path, "/"), "api/")
	for _, route := range routeRequirements {
		if route.method != r.Method || !matchRoutePath(route.path, path) {
			continue
		}
		action := resolvedRouteAction(r, route)
		if action == routeAdmin || (action == routeVirtualInput && strings.HasSuffix(path, "/impact")) {
			return Requirement{Reason: route.reason, AdminOnly: true}, true
		}
		if action == routeCalculation || action == routeVirtualInput {
			action = ""
		}
		return Requirement{Action: action, Reason: route.reason}, true
	}
	return Requirement{}, false
}
