package authz

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminOnlyRouteCarriesNoAction(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/delivery/check", nil)
	requirement, ok := Required(request)
	if !ok {
		t.Fatal("delivery/check route not declared")
	}
	if !requirement.AdminOnly {
		t.Fatalf("AdminOnly = false, want true")
	}
	if requirement.Action != "" {
		t.Fatalf("Action = %q, want empty; admin-only semantics belong to AdminOnly", requirement.Action)
	}
}

func TestAdminOnlyRequirementIgnoresActionGrants(t *testing.T) {
	requirement, ok := Required(httptest.NewRequest(http.MethodGet, "/api/delivery/check", nil))
	if !ok {
		t.Fatal("delivery/check route not declared")
	}
	if !requirement.Allowed(true, func(string) bool { return false }) {
		t.Fatal("admin denied admin-only route")
	}
	if requirement.Allowed(false, func(string) bool { return true }) {
		t.Fatal("non-admin allowed admin-only route")
	}
}

func TestNonAdminRouteCarriesItsRealAction(t *testing.T) {
	cases := []struct {
		method, path, action string
	}{
		{http.MethodGet, "audit", AuditView},
		{http.MethodPost, "virtual-inputs/x/apply", VirtualInputApply},
		{http.MethodPost, "calculations/x/apply", CalculationApply},
		{http.MethodPatch, "nodes/x", Configure},
		{http.MethodDelete, "nodes/x", Delete},
	}
	for _, c := range cases {
		request := httptest.NewRequest(c.method, "/api/"+c.path, nil)
		requirement, ok := Required(request)
		if !ok {
			t.Errorf("%s %s not declared", c.method, c.path)
			continue
		}
		if requirement.AdminOnly {
			t.Errorf("%s %s AdminOnly = true, want false", c.method, c.path)
		}
		if requirement.Action != c.action {
			t.Errorf("%s %s Action = %q, want %q", c.method, c.path, requirement.Action, c.action)
		}
	}
}

// TestDeclaredRoutesExposeOnlyRealActions pins the declaration invariant: an
// exported Requirement.Action is either empty or one of the canonical action
// names, never an internal routing sentinel.
func TestDeclaredRoutesExposeOnlyRealActions(t *testing.T) {
	real := make(map[string]bool, len(All))
	for _, action := range All {
		real[action] = true
	}
	for _, route := range routeRequirements {
		path := strings.ReplaceAll(route.path, "*", "x")
		request := httptest.NewRequest(route.method, "/api/"+path, nil)
		requirement, ok := Required(request)
		if !ok {
			t.Errorf("%s %s declaration did not resolve", route.method, route.path)
			continue
		}
		if requirement.Action != "" && !real[requirement.Action] {
			t.Errorf("%s %s Action = %q, want empty or a canonical action", route.method, route.path, requirement.Action)
		}
	}
}
