package emsapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fastiot/internal/authz"
	"fastiot/internal/ems/alarm"
	"fastiot/internal/ems/runtime"
)

func TestProtectedRouteActionMatrix(t *testing.T) {
	channelBody := `{"parent":"root","protocol":"tcp","address":"127.0.0.1:1502","devices":[{"server_id":1,"points":[{"name":"power","function":"holdingRegister","address":0,"format":"uint16","scale":1,"kind":"number"}]}]}`
	maintenanceBody := `{"until":"2030-01-01T00:00:00Z","reason":"maintenance"}`
	tests := []struct {
		name, method, path, body, action, reason string
	}{
		{"create channel", http.MethodPost, "/api/ems/channels", channelBody, authz.Configure, "admin_required"},
		{"delete channel", http.MethodDelete, "/api/ems/channels/c1", "", authz.Delete, "admin_required"},
		{"read frames", http.MethodGet, "/api/ems/frames", "", authz.Command, "operator_required"},
		{"clear frames", http.MethodDelete, "/api/ems/frames", "", authz.Command, "operator_required"},
		{"acknowledge alarm", http.MethodPost, "/api/ems/alarms/{alarm}/ack", "", authz.AlarmAck, "operator_required"},
		{"close alarm", http.MethodPost, "/api/ems/alarms/{alarm}/close", "", authz.AlarmAck, "operator_required"},
		{"maintain alarm", http.MethodPost, "/api/ems/alarms/{alarm}/maintenance", maintenanceBody, authz.AlarmMaintain, "operator_required"},
		{"release alarm maintenance", http.MethodDelete, "/api/ems/alarms/{alarm}/maintenance", "", authz.AlarmMaintain, "operator_required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			newHandler := func() (*Handler, string) {
				service := runtime.NewService(nil, time.Minute, time.Second, 100)
				t.Cleanup(service.Close)
				raised, err := service.Alarms.Raise(time.Now(), "temperature:d1", alarm.Warning, "hot", "46", "d1", "temp")
				if err != nil {
					t.Fatal(err)
				}
				if test.name == "close alarm" {
					if _, err := service.Alarms.Clear(time.Now(), raised.Key); err != nil {
						t.Fatal(err)
					}
				}
				handler := New(service, time.Minute)
				handler.Configure = &fakeConfigurator{}
				return handler, raised.ID
			}
			handler, id := newHandler()
			path := strings.ReplaceAll(test.path, "{alarm}", id)
			allowed := call(t, handler, Access{UserID: "operator", Actions: map[string]bool{test.action: true}}, test.method, path, test.body)
			if allowed.Code != http.StatusOK {
				t.Fatalf("%s action was rejected: %d %s", test.action, allowed.Code, allowed.Body.String())
			}

			handler, id = newHandler()
			path = strings.ReplaceAll(test.path, "{alarm}", id)
			denied := call(t, handler, Access{Actions: map[string]bool{}}, test.method, path, test.body)
			if denied.Code != http.StatusForbidden || !strings.Contains(denied.Body.String(), fmt.Sprintf("%q", test.reason)) {
				t.Fatalf("missing action: %d %s", denied.Code, denied.Body.String())
			}
		})
	}
}

func TestChannelCreateLargeBodyKeepsScopedAdminJSONError(t *testing.T) {
	for _, parent := range []string{"root", "other"} {
		t.Run(parent, func(t *testing.T) {
			service := runtime.NewService(nil, time.Minute, time.Second, 100)
			t.Cleanup(service.Close)
			handler := New(service, time.Minute)
			handler.Configure = &fakeConfigurator{}
			body := strings.Repeat(" ", 1<<20+1) + `{"parent":"` + parent + `","protocol":"tcp","address":"127.0.0.1:1502","devices":[{"server_id":1,"points":[{"name":"power","function":"holdingRegister","address":0,"format":"uint16","scale":1,"kind":"number"}]}]}`
			access := Access{Admin: true, AllowNode: func(id string) bool { return id == "root" }}
			response := call(t, handler, access, http.MethodPost, "/api/ems/channels", body)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_json") {
				t.Errorf("large body parent %s = %d %s", parent, response.Code, response.Body.String())
			}
		})
	}
}

// TestEMSUndeclaredRouteIsRejected pins the fail-closed dispatch rule: a route
// the product authorization has not declared is never served, and admin does
// not bypass the declaration.
func TestEMSUndeclaredRouteIsRejected(t *testing.T) {
	service := runtime.NewService(nil, time.Minute, time.Second, 10)
	t.Cleanup(service.Close)
	handler := New(service, time.Minute)
	handler.Authorize = func(*http.Request) (authz.Requirement, bool) { return authz.Requirement{}, false }

	response := call(t, handler, Access{Admin: true}, http.MethodGet, "/api/ems/state", "")
	if response.Code != http.StatusNotFound {
		t.Fatalf("undeclared route = %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), `"values"`) {
		t.Fatalf("undeclared route reached its handler: %s", response.Body.String())
	}
}

// TestEMSServedRoutesAreDeclared keeps the dispatch switch and the
// authorization table in sync: every route this handler serves, including the
// login-only reads, is explicitly declared.
func TestEMSServedRoutesAreDeclared(t *testing.T) {
	served := []struct{ method, path string }{
		{http.MethodGet, "/api/ems/quality"},
		{http.MethodGet, "/api/ems/frames"},
		{http.MethodDelete, "/api/ems/frames"},
		{http.MethodGet, "/api/ems/state"},
		{http.MethodGet, "/api/ems/channels"},
		{http.MethodPost, "/api/ems/channels"},
		{http.MethodDelete, "/api/ems/channels/c1"},
		{http.MethodPost, "/api/ems/channels/c1/restart"},
		{http.MethodGet, "/api/ems/alarms"},
		{http.MethodPost, "/api/ems/alarms/a1/ack"},
		{http.MethodPost, "/api/ems/alarms/a1/close"},
		{http.MethodPost, "/api/ems/alarms/a1/maintenance"},
		{http.MethodDelete, "/api/ems/alarms/a1/maintenance"},
		{http.MethodGet, "/api/ems/history?device_id=d1&point_id=p1"},
		{http.MethodGet, "/api/ems/history/export?device_id=d1&point_id=p1"},
		{http.MethodGet, "/api/ems/runtime"},
		{http.MethodPost, "/api/ems/scan"},
		{http.MethodPost, "/api/ems/identify"},
		{http.MethodGet, "/api/ems/profiles"},
		{http.MethodGet, "/api/ems/profiles/modbus-generic/points.csv"},
		{http.MethodPost, "/api/ems/allocation/preview"},
		{http.MethodPost, "/api/ems/acceptance/run"},
		{http.MethodGet, "/api/ems/acceptance/latest"},
	}
	for _, route := range served {
		request := httptest.NewRequest(route.method, route.path, nil)
		if _, ok := authz.Required(request); !ok {
			t.Errorf("%s %s is served but not declared by authz", route.method, route.path)
		}
	}
}
