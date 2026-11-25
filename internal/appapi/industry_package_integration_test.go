package appapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/ems/runtime"
	"fastiot/internal/emsapi"
	"fastiot/internal/industry"
)

// Browser-only disposable fixture: no device clients or production store.
func TestIndustryGateBrowserBackend(t *testing.T) {
	if os.Getenv("FASTIOT_INDUSTRY_BROWSER") != "1" {
		t.Skip("browser harness is opt-in")
	}
	dir := t.TempDir()
	base, token, h, _ := startInputServer(t, dir, "industry-browser-test-only")
	registry, err := industry.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	h.SetPackages(registry)
	service := runtime.NewService(nil, 15*time.Second, 5*time.Second, 100)
	t.Cleanup(service.Close)
	h.SetEMS(emsapi.New(service, 15*time.Second))
	root := snapshotRoot(t, base, token)
	site, err := CreateDomainNode(h.nc, root, NodeTypeSite, map[string]string{"description": "Isolated IoT site"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Uninstall(industry.EMSPackageID); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", h))
	mux.HandleFunc("GET /__fixture", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ticket04-isolated-store")) })
	// Simulate existing EMS engineering content on a disabled instance. Only
	// this opt-in test process has this endpoint, never the application router.
	mux.HandleFunc("POST /__require-ems", func(w http.ResponseWriter, r *http.Request) {
		if _, err := CreateDomainNode(h.nc, site, NodeTypeEnergyUnit, map[string]string{"description": "Retained EMS configuration"}); err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /__finish", func(w http.ResponseWriter, r *http.Request) {
		select {
		case done <- struct{}{}:
		default:
		}
		w.WriteHeader(204)
	})
	srv := &http.Server{Addr: ":18084", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Error(err)
			done <- struct{}{}
		}
	}()
	defer srv.Close()
	select {
	case <-done:
	case <-time.After(8 * time.Minute):
		t.Fatal("browser did not finish")
	}
}

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

// A tariff is EMS content even without an EnergyUnit/Plant node. Activation
// must not decide whether that content declares its real package dependency.
func TestIndustryDependencyIncludesConfigurationOnBaseNodes(t *testing.T) {
	base, token, registry, h := packageFixture(t)
	root := snapshotRoot(t, base, token)
	site, err := CreateDomainNode(h.nc, root, NodeTypeSite, map[string]string{"description": "Metering site"})
	if err != nil {
		t.Fatal(err)
	}
	if status, body := call(t, http.MethodPut, base+"/tariff", token, map[string]any{"root": site, "price": 0.6}, nil); status != 200 {
		t.Fatalf("set tariff: %d %s", status, body)
	}
	var directory struct {
		Projects []projectContext `json:"projects"`
	}
	status, body := call(t, http.MethodGet, base+"/projects", token, nil, &directory)
	if status != 200 || len(directory.Projects) != 1 || len(directory.Projects[0].Dependencies) != 1 || directory.Projects[0].Dependencies[0].ID != industry.EMSPackageID {
		t.Fatalf("tariff must declare EMS dependency: %d %s", status, body)
	}
	var exported projectPackage
	status, body = call(t, http.MethodGet, base+"/projects/export?site="+site, token, nil, &exported)
	if status != 200 || len(exported.Dependencies) != 1 || exported.Dependencies[0].ID != "ems" {
		t.Fatalf("export dropped tariff dependency: %d %s", status, body)
	}
	status, body = call(t, http.MethodDelete, base+"/industry/packages/ems", token, nil, nil)
	if status != 409 || !strings.Contains(body, "package_in_use") {
		t.Fatalf("tariff uninstall: %d %s", status, body)
	}
	if _, err := registry.Uninstall("ems"); err != nil {
		t.Fatal(err)
	}
	status, body = call(t, http.MethodGet, base+"/projects", token, nil, &directory)
	if status != 200 || len(directory.Projects[0].Dependencies) != 1 {
		t.Fatalf("disabled switch erased dependency: %d %s", status, body)
	}
	status, body = call(t, http.MethodGet, base+"/projects/export?site="+site, token, nil, nil)
	if status != 400 || !strings.Contains(body, "missing_dependency:industry:ems") {
		t.Fatalf("disabled export: %d %s", status, body)
	}
	// Tombstones are not engineering content and must not make plain IoT depend on EMS.
	point := data.NewPointFloat("easyemsTariff", "", 0)
	point.Tombstone = 1
	if err := client.SendNodePoint(h.nc, site, point, true); err != nil {
		t.Fatal(err)
	}
	status, body = call(t, http.MethodGet, base+"/projects", token, nil, &directory)
	if status != 200 || len(directory.Projects[0].Dependencies) != 0 {
		t.Fatalf("tombstone retained dependency: %d %s", status, body)
	}
	if status, body := call(t, http.MethodPatch, base+"/nodes/"+site, token, map[string]any{"description": "ordinary IoT"}, nil); status != 200 {
		t.Fatalf("ordinary IoT blocked: %d %s", status, body)
	}
}

func TestIndustryGateBlocksDependentWritesWithoutProjectHeader(t *testing.T) {
	base, token, registry, h := packageFixture(t)
	root := snapshotRoot(t, base, token)
	site, err := CreateDomainNode(h.nc, root, NodeTypeSite, map[string]string{"description": "EMS site"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateDomainNode(h.nc, site, NodeTypeEnergyUnit, map[string]string{"description": "Retained energy unit"}); err != nil {
		t.Fatal(err)
	}
	channel := data.NodeEdge{ID: data.NewID(), Parent: site, Type: data.NodeTypeModbus}
	if err := client.SendNode(h.nc, channel, "test"); err != nil {
		t.Fatal(err)
	}
	// Simulate a retained project loaded on a disabled instance, not a supported
	// API uninstall (the API must refuse uninstalling in-use content).
	if _, err := registry.Uninstall(industry.EMSPackageID); err != nil {
		t.Fatal(err)
	}
	status, body := call(t, http.MethodPatch, base+"/nodes/"+site, token, map[string]any{"description": "must not change"}, nil)
	if status != 409 || !strings.Contains(body, "project_industry_dependency_unavailable") {
		t.Fatalf("base configuration must not bypass missing dependency: %d %s", status, body)
	}
	var snapshot struct {
		Industry struct{ Required, Installed, Blocked bool }
		Nodes    []data.NodeEdge
	}
	status, body = call(t, http.MethodGet, base+"/snapshot", token, nil, &snapshot)
	if status != 200 || !snapshot.Industry.Required || snapshot.Industry.Installed || !snapshot.Industry.Blocked {
		t.Fatalf("snapshot gate: %d %s", status, body)
	}
	for _, node := range snapshot.Nodes {
		if node.ID == site && nodeName(node) != "EMS site" {
			t.Fatal("denied configuration changed data")
		}
	}
	for _, level := range []string{"L1", "L2", "L3"} {
		status, body := call(t, http.MethodPost, base+"/modbus/"+channel.ID+"/units/1/verify", token, map[string]any{"level": level, "confirm": true}, nil)
		if level == "L3" {
			if status != 409 || !strings.Contains(body, "project_industry_dependency_unavailable") {
				t.Fatalf("L3 bypass: %d %s", status, body)
			}
		} else if strings.Contains(body, "industry") || status >= 500 {
			t.Fatalf("read-only verification gated: %s %d %s", level, status, body)
		}
	}
	// Reading diagnostics and account administration remains possible.
	for _, path := range []string{"/projects", "/industry/packages", "/users", "/ems/state", "/delivery/check"} {
		if status, body := call(t, http.MethodGet, base+path, token, nil, nil); status != 200 {
			t.Fatalf("recovery read %s: %d %s", path, status, body)
		}
	}
	if status, body := call(t, http.MethodPost, base+"/industry/packages", token, map[string]string{"id": "ems", "version": industry.EMSPackageVersion}, nil); status != 200 {
		t.Fatalf("reactivate: %d %s", status, body)
	}
	if status, body := call(t, http.MethodPatch, base+"/nodes/"+site, token, map[string]any{"description": "recovered"}, nil); status != 200 {
		t.Fatalf("recovered config: %d %s", status, body)
	}
	viewer := inputAccount(t, &pointConfigFixture{base: base, token: token}, site, "viewer", nil)
	if status, body := call(t, http.MethodPatch, base+"/nodes/"+site, viewer, map[string]any{"description": "forbidden"}, nil); status != 403 {
		t.Fatalf("activation granted permissions: %d %s", status, body)
	}
}

func TestIndustryActivationRequiresRuntimeRootScope(t *testing.T) {
	base, token, registry, h := packageFixture(t)
	root := snapshotRoot(t, base, token)
	site, err := CreateDomainNode(h.nc, root, NodeTypeSite, map[string]string{"description": "visible IoT"})
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := CreateDomainNode(h.nc, root, NodeTypeSite, map[string]string{"description": "hidden EMS"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateDomainNode(h.nc, hidden, NodeTypeEnergyUnit, map[string]string{"description": "hidden energy unit"}); err != nil {
		t.Fatal(err)
	}
	scoped := inputAccount(t, &pointConfigFixture{base: base, token: token}, site, "implementer", []string{ActionSiteSettings})
	status, body := call(t, http.MethodDelete, base+"/industry/packages/ems", scoped, nil, nil)
	if status != 403 || !strings.Contains(body, "industry_scope_required") {
		t.Fatalf("scoped account must not disable global capability: %d %s", status, body)
	}
	if !registry.Installed("ems") {
		t.Fatal("scoped request disabled hidden project dependency")
	}
	status, body = call(t, http.MethodPost, base+"/industry/packages", scoped, map[string]string{"id": "ems", "version": industry.EMSPackageVersion}, nil)
	if status != 403 {
		t.Fatalf("scoped activation: %d %s", status, body)
	}
	// Scope metadata never exposes the hidden dependency or object.
	var directory struct {
		Projects []projectContext `json:"projects"`
	}
	status, body = call(t, http.MethodGet, base+"/projects", scoped, nil, &directory)
	if status != 200 || len(directory.Projects[0].Dependencies) != 0 || strings.Contains(body, hidden) {
		t.Fatalf("hidden dependency leaked: %d %s", status, body)
	}
}

func TestIndustryDisabledRejectsGenericEnergyCreation(t *testing.T) {
	for _, kind := range []string{"energyUnit", "plant", "storage"} {
		t.Run(kind, func(t *testing.T) {
			base, token, registry, _ := packageFixture(t)
			root := snapshotRoot(t, base, token)
			if _, err := registry.Uninstall("ems"); err != nil {
				t.Fatal(err)
			}
			input := map[string]any{"parent": root, "type": kind, "description": "must not create"}
			if kind == "storage" {
				input["type"] = "site"
				input["site_template"] = "storage"
			}
			status, body := call(t, http.MethodPost, base+"/nodes", token, input, nil)
			if status != 409 || !strings.Contains(body, packageNotInstalled) {
				t.Fatalf("generic EMS create: %d %s", status, body)
			}
			for _, node := range snapshotNodes(t, base, token) {
				if nodeName(node) == "must not create" {
					t.Fatal("rejected creation left nodes")
				}
			}
			input["type"] = "site"
			input["site_template"] = "pump-house"
			if status, body := call(t, http.MethodPost, base+"/nodes", token, input, nil); status != 200 {
				t.Fatalf("ordinary IoT template: %d %s", status, body)
			}
		})
	}
}

func TestIndustryDependencyFollowsAuthorizedChildWithoutExposingAncestors(t *testing.T) {
	base, token, registry, h := packageFixture(t)
	root := snapshotRoot(t, base, token)
	unit, err := CreateDomainNode(h.nc, root, NodeTypeEnergyUnit, map[string]string{"description": "private ancestor name"})
	if err != nil {
		t.Fatal(err)
	}
	device := data.NodeEdge{ID: data.NewID(), Parent: unit, Type: data.NodeTypeModbus, Points: data.Points{data.NewPointString("description", "", "authorized device channel")}}
	if err := client.SendNode(h.nc, device, "test"); err != nil {
		t.Fatal(err)
	}
	scoped := inputAccount(t, &pointConfigFixture{base: base, token: token}, device.ID, "implementer", nil)
	if _, err := registry.Uninstall("ems"); err != nil {
		t.Fatal(err)
	}
	status, body := call(t, http.MethodPost, base+"/nodes", scoped, map[string]any{"parent": device.ID, "type": "variable", "description": "bypass"}, nil)
	if status != 409 || !strings.Contains(body, "project_industry_dependency_unavailable") {
		t.Fatalf("ancestor dependency bypass: %d %s", status, body)
	}
	var snapshot struct {
		Industry struct{ Required, Blocked bool }
		Nodes    []data.NodeEdge
	}
	status, body = call(t, http.MethodGet, base+"/snapshot", scoped, nil, &snapshot)
	if status != 200 || !snapshot.Industry.Blocked || !snapshot.Industry.Required {
		t.Fatalf("child snapshot gate: %d %s", status, body)
	}
	for _, node := range snapshot.Nodes {
		if node.ID == unit || nodeName(node) == "private ancestor name" {
			t.Fatal("ancestor data leaked")
		}
	}
	var directory struct {
		Projects []projectContext `json:"projects"`
	}
	status, body = call(t, http.MethodGet, base+"/projects", scoped, nil, &directory)
	if status != 200 || len(directory.Projects[0].Dependencies) != 1 || strings.Contains(body, "private ancestor name") {
		t.Fatalf("child directory dependency: %d %s", status, body)
	}
	ordinary := data.NodeEdge{ID: data.NewID(), Parent: root, Type: data.NodeTypeModbus}
	if err := client.SendNode(h.nc, ordinary, "test"); err != nil {
		t.Fatal(err)
	}
	plain := inputAccount(t, &pointConfigFixture{base: base, token: token}, ordinary.ID, "implementer", nil)
	status, body = call(t, http.MethodGet, base+"/snapshot", plain, nil, &snapshot)
	if status != 200 || snapshot.Industry.Required || snapshot.Industry.Blocked {
		t.Fatalf("hidden sibling must not gate ordinary IoT: %d %s", status, body)
	}
	status, body = call(t, http.MethodPost, base+"/nodes", plain, map[string]any{"parent": ordinary.ID, "type": "variable", "description": "allowed"}, nil)
	if status != 200 {
		t.Fatalf("ordinary scoped configuration: %d %s", status, body)
	}
	status, body = call(t, http.MethodGet, base+"/projects", plain, nil, &directory)
	if status != 200 || len(directory.Projects[0].Dependencies) != 0 || strings.Contains(body, unit) {
		t.Fatalf("hidden sibling directory leak: %d %s", status, body)
	}
	// A malformed multi-parent path is unknown, not proof of no dependency.
	ordinary.Parent = unit
	if err := client.SendNode(h.nc, ordinary, "test"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/snapshot", "/projects"} {
		status, body = call(t, http.MethodGet, base+path, plain, nil, nil)
		if status != 503 || !strings.Contains(body, "project_context_unavailable") || strings.Contains(body, unit) || strings.Contains(body, ordinary.ID) {
			t.Fatalf("ambiguous ancestry must fail closed without identity leaks: %d %s", status, body)
		}
	}
	status, body = call(t, http.MethodPost, base+"/nodes", plain, map[string]any{"parent": ordinary.ID, "type": "variable", "description": "must not create"}, nil)
	if status != 503 {
		t.Fatalf("unknown dependency allowed configuration: %d %s", status, body)
	}
}

func TestIndustryGatePersistsAcrossRealStoreRestart(t *testing.T) {
	dir, password := t.TempDir(), data.NewID()
	base, token, h, stop := startInputServer(t, dir, password)
	registry, err := industry.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	h.SetPackages(registry)
	root := snapshotRoot(t, base, token)
	if _, err := CreateDomainNode(h.nc, root, NodeTypeEnergyUnit, map[string]string{"description": "retained engineering"}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Uninstall("ems"); err != nil {
		t.Fatal(err)
	}
	stop()
	for _, installed := range []bool{false, true} {
		base, token, h, stop = startInputServer(t, dir, password)
		registry, err = industry.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		h.SetPackages(registry)
		var snapshot struct {
			Industry struct{ Required, Installed, Blocked bool }
		}
		status, body := call(t, http.MethodGet, base+"/snapshot", token, nil, &snapshot)
		if status != 200 || !snapshot.Industry.Required || snapshot.Industry.Installed != installed || snapshot.Industry.Blocked == installed {
			t.Fatalf("restart gate: %d %s", status, body)
		}
		if !installed {
			status, body = call(t, http.MethodPost, base+"/industry/packages", token, map[string]string{"id": "ems", "version": industry.EMSPackageVersion}, nil)
			if status != 200 {
				t.Fatalf("activation: %d %s", status, body)
			}
		}
		stop()
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
