package appapi

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/ems/runtime"
	"fastiot/internal/emsapi"
	"fastiot/internal/industry"
)

const packageNotInstalled = "industry_package_not_installed"

func packageFixture(t *testing.T) (base, token string, registry *industry.Registry, handler *Handler) {
	t.Helper()
	dir := t.TempDir()
	base, token, handler, _ = startInputServer(t, dir, data.NewID())
	service := runtime.NewService(nil, 15*time.Second, 5*time.Second, 100)
	t.Cleanup(service.Close)
	handler.SetEMS(emsapi.New(service, 15*time.Second))
	var err error
	registry, err = industry.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	handler.SetPackages(registry)
	return base, token, registry, handler
}

func TestIndustryPackageInstallUninstallBoundary(t *testing.T) {
	base, token, registry, _ := packageFixture(t)

	status, body := call(t, http.MethodGet, base+"/industry/packages", token, nil, nil)
	if status != http.StatusOK || !strings.Contains(body, `"installed":true`) || !strings.Contains(body, industry.EMSPackageVersion) {
		t.Fatalf("GET packages = %d %s", status, body)
	}
	if status, body := call(t, http.MethodGet, base+"/energy-units", token, nil, nil); status != http.StatusOK {
		t.Fatalf("installed package energy units = %d %s", status, body)
	}

	if status, body := call(t, http.MethodDelete, base+"/industry/packages/"+industry.EMSPackageID, token, nil, nil); status != http.StatusOK {
		t.Fatalf("uninstall = %d %s", status, body)
	}
	if registry.Installed(industry.EMSPackageID) {
		t.Fatal("uninstall did not deactivate the package")
	}

	owned := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/energy-units", nil},
		{http.MethodPost, "/energy-units", map[string]any{"description": "柜 1", "rated_power_kw": 100}},
		{http.MethodGet, "/resource-groups", nil},
		{http.MethodGet, "/plants", nil},
		{http.MethodGet, "/demand?site=" + "x", nil},
		{http.MethodPut, "/tariff", map[string]any{"root": "x", "price": 0.6}},
		{http.MethodGet, "/projects/plan96?site=" + "x", nil},
		{http.MethodGet, "/ems/allocators/legacy", nil},
		{http.MethodPost, "/ems/allocation/preview", map[string]any{}},
	}
	for _, request := range owned {
		status, body := call(t, request.method, base+request.path, token, request.body, nil)
		if status != http.StatusConflict || !strings.Contains(body, packageNotInstalled) {
			t.Errorf("%s %s with package uninstalled = %d %s, want 409 %s", request.method, request.path, status, body, packageNotInstalled)
		}
	}
	for _, node := range snapshotNodes(t, base, token) {
		if node.Type == NodeTypeEnergyUnit || node.Type == NodeTypePlant || node.Type == NodeTypeResourceGroup {
			t.Fatalf("a refused install-capable request created a package data model: %s %s", node.Type, node.ID)
		}
	}

	baseRoutes := []string{
		"/snapshot", "/profile-verifications", "/history-settings",
		"/users", "/delivery/check", "/ems/alarms", "/ems/state",
	}
	for _, path := range baseRoutes {
		if status, body := call(t, http.MethodGet, base+path, token, nil, nil); status != http.StatusOK {
			t.Errorf("base route %s with package uninstalled = %d %s, want 200", path, status, body)
		}
	}
	// Safety constraints belong to the base command path, not to the package.
	if status, body := call(t, http.MethodPut, base+"/safety", token, map[string]any{}, nil); status != http.StatusBadRequest {
		t.Errorf("base safety route with package uninstalled = %d %s, want 400 invalid_safety", status, body)
	}

	status, body = call(t, http.MethodPost, base+"/industry/packages", token, map[string]string{
		"id": industry.EMSPackageID, "version": industry.EMSPackageVersion,
	}, nil)
	if status != http.StatusOK || !strings.Contains(body, `"installed":true`) {
		t.Fatalf("install = %d %s", status, body)
	}
	if status, body := call(t, http.MethodGet, base+"/energy-units", token, nil, nil); status != http.StatusOK {
		t.Fatalf("reinstalled package energy units = %d %s", status, body)
	}
}

func TestIndustryPackageInstallRequiresTheBundledVersion(t *testing.T) {
	base, token, registry, _ := packageFixture(t)

	status, body := call(t, http.MethodPost, base+"/industry/packages", token, map[string]string{
		"id": industry.EMSPackageID, "version": "9.9.9",
	}, nil)
	if status != http.StatusConflict || !strings.Contains(body, industry.ErrVersionUnavailable.Error()) {
		t.Fatalf("foreign version install = %d %s", status, body)
	}
	if !registry.Installed(industry.EMSPackageID) {
		t.Fatal("a rejected install changed activation")
	}
	status, body = call(t, http.MethodPost, base+"/industry/packages", token, map[string]string{
		"id": "other-package", "version": "1.0.0",
	}, nil)
	if status != http.StatusNotFound || !strings.Contains(body, industry.ErrUnknownPackage.Error()) {
		t.Fatalf("unknown package install = %d %s", status, body)
	}
}

func TestUninstallRefusedWhileEMSConfigurationExists(t *testing.T) {
	base, token, registry, _ := packageFixture(t)
	status, body := call(t, http.MethodPost, base+"/energy-units", token, map[string]any{
		"description": "储能柜 1", "rated_power_kw": 100,
	}, nil)
	if status != http.StatusOK {
		t.Fatalf("create energy unit = %d %s", status, body)
	}
	status, body = call(t, http.MethodDelete, base+"/industry/packages/"+industry.EMSPackageID, token, nil, nil)
	if status != http.StatusConflict || !strings.Contains(body, "package_in_use") || !strings.Contains(body, "能源单元") {
		t.Fatalf("uninstall with live EMS configuration = %d %s", status, body)
	}
	if !registry.Installed(industry.EMSPackageID) {
		t.Fatal("refused uninstall deactivated the package")
	}
	if status, body := call(t, http.MethodGet, base+"/energy-units", token, nil, nil); status != http.StatusOK {
		t.Fatalf("energy units after refused uninstall = %d %s", status, body)
	}
}

func TestBaseSCADASurvivesEMSUninstall(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	seed, err := industry.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Uninstall(industry.EMSPackageID); err != nil {
		t.Fatal(err)
	}
	// The site starts with the package uninstalled, as it would after a restart.
	f := newPointConfigFixtureAt(t, dir)
	registry, err := industry.Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if registry.Installed(industry.EMSPackageID) {
		t.Fatal("startup ignored the persisted uninstalled state")
	}
	f.handler.SetPackages(registry)

	busID := f.createBus(t, "Unrelated vendor")
	input := ModbusIOInput{
		Description: "Tank level", ServerID: 1, Address: 12,
		IOType: data.PointValueModbusHoldingRegister, DataFormat: "uint16", Scale: 1,
	}
	pointID := f.createPoint(t, busID, input)
	f.cycle(t)
	waitFor(t, func() bool {
		item, ok := f.node(t, pointID).Points.Find(data.PointTypeValue, "")
		value, err := item.ValueFloat()
		return ok && err == nil && value == 37
	}, "base Modbus observation with the industry package uninstalled")

	if status, body := call(t, http.MethodGet, f.base+"/modbus/"+busID+"/io/export", f.token, nil, nil); status != http.StatusOK {
		t.Fatalf("base point export = %d %s", status, body)
	}
	var packages struct {
		Packages []industry.Entry `json:"packages"`
	}
	status, body := call(t, http.MethodGet, f.base+"/industry/packages", f.token, nil, &packages)
	if status != http.StatusOK || len(packages.Packages) != 1 || packages.Packages[0].Installed {
		t.Fatalf("package state = %d %s", status, body)
	}
	if status, body := call(t, http.MethodGet, f.base+"/energy-units", f.token, nil, nil); status != http.StatusConflict {
		t.Fatalf("industry route with package uninstalled = %d %s", status, body)
	}
}

func TestIndustryPackageStateSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	registry, err := industry.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Uninstall(industry.EMSPackageID); err != nil {
		t.Fatal(err)
	}
	reopened, err := industry.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Installed(industry.EMSPackageID) {
		t.Fatal("uninstalled state did not survive restart")
	}
}
