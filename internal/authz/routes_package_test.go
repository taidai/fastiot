package authz

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"fastiot/internal/industry"
)

// TestRoutePackagesDistinguishBaseFromIndustryRoutes pins the boundary: base
// routes stay available without any industry package, and every energy-control
// route names the package that owns it.
func TestRoutePackagesDistinguishBaseFromIndustryRoutes(t *testing.T) {
	owned := []struct {
		method, path string
	}{
		{http.MethodGet, "energy-units"},
		{http.MethodPost, "energy-units"},
		{http.MethodGet, "energy-units/x/rule"},
		{http.MethodGet, "resource-groups"},
		{http.MethodPost, "resource-groups"},
		{http.MethodGet, "plants"},
		{http.MethodGet, "plants/x/pcc"},
		{http.MethodPut, "plants/x/strategy"},
		{http.MethodGet, "sites/x/target"},
		{http.MethodGet, "demand"},
		{http.MethodGet, "projects/plan96"},
		{http.MethodPut, "tariff"},
		{http.MethodGet, "ems/allocators/x"},
		{http.MethodPost, "ems/allocation/preview"},
	}
	for _, route := range owned {
		request := httptest.NewRequest(route.method, "/api/"+route.path, nil)
		requirement, ok := Required(request)
		if !ok || requirement.Package != industry.EMSPackageID {
			t.Errorf("%s %s package = %q (declared %v), want %q", route.method, route.path, requirement.Package, ok, industry.EMSPackageID)
		}
	}

	base := []struct {
		method, path string
	}{
		{http.MethodGet, "snapshot"},
		{http.MethodGet, "history"},
		{http.MethodGet, "ems/alarms"},
		{http.MethodGet, "ems/state"},
		{http.MethodGet, "ems/screen/x"},
		{http.MethodGet, "ems/profiles"},
		{http.MethodGet, "modbus/x/units/1/model"},
		{http.MethodPost, "rules/x/test"},
		{http.MethodPut, "safety"},
		{http.MethodGet, "users"},
		{http.MethodGet, "delivery/check"},
		{http.MethodGet, "industry/packages"},
	}
	for _, route := range base {
		request := httptest.NewRequest(route.method, "/api/"+route.path, nil)
		requirement, ok := Required(request)
		if !ok || requirement.Package != "" {
			t.Errorf("%s %s package = %q (declared %v), want base route", route.method, route.path, requirement.Package, ok)
		}
	}
}
