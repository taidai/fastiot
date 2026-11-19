package appapi

import (
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	siotclient "github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/deepseek"
	"fastiot/internal/ems/profile"
	"fastiot/internal/emsapi"
	"fastiot/internal/genscreen"
)

// screenSource is a small 3D screen: it binds one point declaratively, one
// through a write button and both again from a script, which is where the
// identifiers a package has to rewrite actually appear.
func screenSource(level, pressure string) string {
	return `<html><head><title>泵房</title></head>
<body>
<canvas id="scene"></canvas>
<span data-fastiot-point="` + level + `" data-fastiot-format="number">--</span>
<button data-fastiot-write="` + level + `" data-fastiot-value="1">启动</button>
<script>
const scene = new THREE.Scene();
document.addEventListener("fastiot:values", function (event) {
  const level = event.detail.values['` + level + `'];
  const pressure = event.detail.values['` + pressure + `'];
  if (level && pressure) { scene.name = String(level.value) + String(pressure.value); }
});
</script>
</body></html>`
}

// screenProject builds a site with one device and two variables, then confirms
// a screen draft on the device exactly as the preview confirm step stores it.
func screenProject(t *testing.T, handler *Handler, root string) (site, device, level, pressure string) {
	t.Helper()
	var err error
	site, err = CreateDomainNode(handler.nc, root, NodeTypeSite, map[string]string{"description": "画面工程"})
	if err != nil {
		t.Fatal(err)
	}
	device, err = CreateDomainNode(handler.nc, site, data.NodeTypeDevice, map[string]string{"description": "泵房"})
	if err != nil {
		t.Fatal(err)
	}
	level, err = CreateDomainNode(handler.nc, device, data.NodeTypeVariable, map[string]string{"description": "液位", "units": "%"})
	if err != nil {
		t.Fatal(err)
	}
	pressure, err = CreateDomainNode(handler.nc, device, data.NodeTypeVariable, map[string]string{"description": "压力", "units": "kPa"})
	if err != nil {
		t.Fatal(err)
	}
	saveScreenDraft(t, handler, device, screenSource(level, pressure), []genscreen.Point{
		{ID: level, Name: "液位", Kind: "number", Unit: "%", Writable: true},
		{ID: pressure, Name: "压力", Kind: "number", Unit: "kPa"},
	})
	waitForNodePoints(t, handler.nc, device, generatedScreenDraftType)
	return site, device, level, pressure
}

func saveScreenDraft(t *testing.T, handler *Handler, nodeID, source string, points []genscreen.Point) {
	t.Helper()
	body := generatedScreenDraftBody(&genscreen.Session{Source: source, Points: points, NodeID: nodeID}, genscreen.Report{}, time.Now())
	body["revision"] = 1
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := siotclient.SendNodePoint(handler.nc, nodeID, data.NewPointString(generatedScreenDraftType, "", string(encoded)), true); err != nil {
		t.Fatal(err)
	}
}

type screenDraftBody struct {
	Source   string            `json:"source"`
	NodeID   string            `json:"node_id"`
	Artifact string            `json:"artifact"`
	Build    genscreen.Build   `json:"build"`
	Points   []genscreen.Point `json:"points"`
}

// TestProjectPackageCarriesScreenSourceArtifactAndAssets proves the whole
// loop: a confirmed screen travels with the project, its build inputs are
// pinned, every instance renders the same artifact, and the source that comes
// out carries no old identifiers.
func TestProjectPackageCarriesScreenSourceArtifactAndAssets(t *testing.T) {
	baseA, tokenA, handlerA, _ := startInputServer(t, t.TempDir(), data.NewID())
	handlerA.SetEMS(&emsapi.Handler{Profiles: profile.Default()})
	rootA := snapshotRoot(t, baseA, tokenA)
	siteA, deviceA, levelA, pressureA := screenProject(t, handlerA, rootA)

	var exported projectPackage
	status, body := call(t, http.MethodGet, baseA+"/projects/export?site="+siteA, tokenA, nil, &exported)
	if status != http.StatusOK {
		t.Fatalf("export: %d %s", status, body)
	}
	if len(exported.Screens) != 1 {
		t.Fatalf("screens in package = %#v", exported.Screens)
	}
	screen := exported.Screens[0]
	if screen.Source == "" || screen.Artifact == "" || len(screen.Bindings) != 2 {
		t.Fatalf("screen entry = %#v", screen)
	}
	if screen.Build != genscreen.BuildFor(screen.Source) {
		t.Fatalf("build pins = %#v", screen.Build)
	}
	if genscreen.Fingerprint(screen.Source) != screen.Artifact {
		t.Fatal("artifact does not name the frame its source builds")
	}
	if screen.Build.ThreeVersion != genscreen.ThreeVersion || screen.Build.ThreeSHA256 != genscreen.ThreeSHA256 {
		t.Fatalf("3D screen did not pin the bundled Three.js: %#v", screen.Build)
	}
	encoded, _ := json.Marshal(exported)
	for _, leaked := range []string{siteA, deviceA, levelA, pressureA} {
		if strings.Contains(string(encoded), leaked) {
			t.Fatalf("package leaked the old identifier %q", leaked)
		}
	}
	if hits := deepseek.ScanCredentials("package", "package", string(encoded)); len(hits) > 0 {
		t.Fatalf("package carries a credential: %#v", hits)
	}
	for _, node := range exported.Nodes {
		if _, ok := screenDraftPoint(node.Points); ok {
			t.Fatal("the draft point travelled outside the screens entry")
		}
	}

	// 同一配置再次导出，画面源码与产物指纹不变。
	var again projectPackage
	if status, body := call(t, http.MethodGet, baseA+"/projects/export?site="+siteA, tokenA, nil, &again); status != http.StatusOK {
		t.Fatalf("second export: %d %s", status, body)
	}
	if !reflect.DeepEqual(exported.Screens, again.Screens) {
		t.Fatalf("export is not reproducible:\n%#v\n%#v", exported.Screens, again.Screens)
	}

	// 干净实例导入：画面换成本实例的 ID，产物声明原样保留。
	baseB, tokenB, handlerB, _ := startInputServer(t, t.TempDir(), data.NewID())
	handlerB.SetEMS(&emsapi.Handler{Profiles: profile.Default()})
	var imported importProjectResult
	if status, body := call(t, http.MethodPost, baseB+"/projects/import", tokenB, map[string]any{"package": exported}, &imported); status != http.StatusOK {
		t.Fatalf("import: %d %s", status, body)
	}
	deviceB, newLevel, newPressure := importedDevice(t, handlerB, imported.SiteID)
	draft := readScreenDraft(t, deviceB)
	if draft.Artifact != screen.Artifact || draft.Build != screen.Build {
		t.Fatalf("imported artifact = %q %#v", draft.Artifact, draft.Build)
	}
	if draft.NodeID != deviceB.ID || strings.Contains(draft.Source, levelA) || strings.Contains(draft.Source, pressureA) {
		t.Fatalf("imported screen still names the source instance: %#v", draft)
	}
	if !strings.Contains(draft.Source, newLevel.ID) || !strings.Contains(draft.Source, newPressure.ID) {
		t.Fatalf("imported screen does not bind the imported points:\n%s", draft.Source)
	}
	if got := len(draft.Points); got != 2 || draft.Points[0].ID != newLevel.ID || draft.Points[1].ID != newPressure.ID {
		t.Fatalf("imported screen points = %#v", draft.Points)
	}
	// 画面在新实例上可渲染，且每个绑定都落在导入后的点位上。
	frame := genscreen.Parse(draft.Source).Render(genscreen.CanonicalNonce)
	if !strings.Contains(frame, "data-fastiot-runtime") || !strings.Contains(frame, newLevel.ID) {
		t.Fatal("imported screen does not build a frame")
	}
	points := map[string]genscreen.Point{}
	for _, point := range draft.Points {
		points[point.ID] = point
	}
	report := genscreen.Parse(draft.Source).Validate(func(id string) (genscreen.Point, genscreen.Status) {
		if point, ok := points[id]; ok {
			return point, genscreen.PointOK
		}
		return genscreen.Point{}, genscreen.PointMissing
	})
	if report.Blocking != 0 {
		t.Fatalf("imported screen has blocking findings: %#v", report.Findings)
	}

	// 从导入后的实例再导出，画面源码与产物指纹与源实例一致。
	var reExported projectPackage
	if status, body := call(t, http.MethodGet, baseB+"/projects/export?site="+imported.SiteID, tokenB, nil, &reExported); status != http.StatusOK {
		t.Fatalf("re-export: %d %s", status, body)
	}
	if len(reExported.Screens) != 1 {
		t.Fatalf("re-exported screens = %#v", reExported.Screens)
	}
	round := reExported.Screens[0]
	if round.Source != screen.Source || round.Artifact != screen.Artifact || round.Build != screen.Build {
		t.Fatalf("re-export changed the screen:\n%q\n%q", round.Source, screen.Source)
	}
	if !reflect.DeepEqual(bindingTokens(round), bindingTokens(screen)) {
		t.Fatalf("binding tokens changed: %#v vs %#v", bindingTokens(round), bindingTokens(screen))
	}

	// 同一包重复部署到另一台干净实例，产物仍然相同。
	baseC, tokenC, handlerC, _ := startInputServer(t, t.TempDir(), data.NewID())
	handlerC.SetEMS(&emsapi.Handler{Profiles: profile.Default()})
	var second importProjectResult
	if status, body := call(t, http.MethodPost, baseC+"/projects/import", tokenC, map[string]any{"package": exported}, &second); status != http.StatusOK {
		t.Fatalf("second import: %d %s", status, body)
	}
	deviceC, _, _ := importedDevice(t, handlerC, second.SiteID)
	if other := readScreenDraft(t, deviceC); other.Artifact != screen.Artifact || other.Build != screen.Build {
		t.Fatalf("second deployment built %q %#v", other.Artifact, other.Build)
	}
}

func importedDevice(t *testing.T, handler *Handler, siteID string) (device, level, pressure data.NodeEdge) {
	t.Helper()
	nodes, err := handler.loadSubtree(siteID)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		switch node.Desc() {
		case "泵房":
			device = node
		case "液位":
			level = node
		case "压力":
			pressure = node
		}
	}
	if device.ID == "" || level.ID == "" || pressure.ID == "" {
		t.Fatalf("imported model incomplete: %v", nodes)
	}
	return device, level, pressure
}

func readScreenDraft(t *testing.T, node data.NodeEdge) screenDraftBody {
	t.Helper()
	point, ok := screenDraftPoint(node.Points)
	if !ok {
		t.Fatalf("node %s has no screen draft", node.Desc())
	}
	var draft screenDraftBody
	if err := json.Unmarshal([]byte(point.Txt()), &draft); err != nil {
		t.Fatal(err)
	}
	return draft
}

func bindingTokens(screen packageScreen) []string {
	tokens := make([]string, 0, len(screen.Bindings))
	for token := range screen.Bindings {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	return tokens
}

// TestProjectImportRefusesBrokenScreensBeforeCreatingNodes covers every way a
// screen can arrive incomplete: the import must name the screen and leave the
// site untouched instead of importing a degraded page.
func TestProjectImportRefusesBrokenScreensBeforeCreatingNodes(t *testing.T) {
	base, token, handler, _ := startInputServer(t, t.TempDir(), data.NewID())
	handler.SetEMS(&emsapi.Handler{Profiles: profile.Default()})

	source := `<html><body><span data-fastiot-point="id1">--</span></body></html>`
	good := func() packageScreen {
		return packageScreen{
			Node: "n2", Source: source, Bindings: map[string]string{"id1": "n3"},
			Artifact: genscreen.Fingerprint(source), Build: genscreen.BuildFor(source),
		}
	}
	project := func(screens ...packageScreen) projectPackage {
		pkg := managedPackage(
			packageNode{Ref: "n1", Type: NodeTypeSite, Points: data.Points{data.NewPointString("description", "", "画面工程")}},
			packageNode{Ref: "n2", Type: data.NodeTypeDevice, Parent: "n1", Points: data.Points{data.NewPointString("description", "", "泵房")}},
			packageNode{Ref: "n3", Type: data.NodeTypeVariable, Parent: "n2", Points: data.Points{data.NewPointString("description", "", "液位")}},
		)
		pkg.Screens = screens
		return pkg
	}

	noSource := good()
	noSource.Source = ""
	noSource.Artifact = ""
	badArtifact := good()
	badArtifact.Artifact = strings.Repeat("0", 64)
	badBuild := good()
	badBuild.Build.Runtime = strings.Repeat("0", 64)
	ghostBinding := good()
	ghostBinding.Bindings = map[string]string{"id1": "ghost"}
	secret := good()
	secret.Source = `<html><body><script>const apiKey = "sk-live-0123456789abcdef";</script></body></html>`
	secret.Artifact = genscreen.Fingerprint(secret.Source)
	secret.Build = genscreen.BuildFor(secret.Source)

	// 画面只存在于节点点位上、没有任何构建声明：属于缺画面，不能照单导入。
	draftOnly := project()
	staleDraft, _ := json.Marshal(map[string]any{"source": source, "node_id": "old-site-node"})
	draftOnly.Nodes[1].Points = append(draftOnly.Nodes[1].Points,
		data.NewPointString(generatedScreenDraftType, "", string(staleDraft)))

	cases := []struct {
		name string
		pkg  projectPackage
		want string
	}{
		{"missing_source", project(noSource), "missing_screen:n2"},
		{"artifact_mismatch", project(badArtifact), "screen_artifact_mismatch:n2"},
		{"build_mismatch", project(badBuild), "screen_build_mismatch:n2"},
		{"binding_outside_package", project(ghostBinding), "missing_screen_binding:n2"},
		{"declared_pointer_only", draftOnly, "missing_screen:n2"},
		{"credential_in_source", project(secret), "screen_contains_secret:n2"},
		{"duplicate_screen", project(good(), good()), "invalid_package_structure:screen_duplicate"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			before, _ := siotclient.GetNodes(handler.nc, "all", "all", "", false)
			status, body := call(t, http.MethodPost, base+"/projects/import", token, map[string]any{"package": test.pkg}, nil)
			if status != http.StatusBadRequest || !strings.Contains(body, test.want) {
				t.Fatalf("import = %d %s, want %s", status, body, test.want)
			}
			after, _ := siotclient.GetNodes(handler.nc, "all", "all", "", false)
			if len(after) != len(before) {
				t.Fatalf("refused import left %d extra nodes", len(after)-len(before))
			}
		})
	}

	// 同一条通道正常时仍然可以导入，证明上面的拒绝不是一刀切。
	var imported importProjectResult
	if status, body := call(t, http.MethodPost, base+"/projects/import", token, map[string]any{"package": project(good())}, &imported); status != http.StatusOK {
		t.Fatalf("valid screen import = %d %s", status, body)
	}
}

// TestProjectExportRefusesAScreenCarryingACredential keeps secrets out of the
// package at the source: an export that would ship one fails and says where.
func TestProjectExportRefusesAScreenCarryingACredential(t *testing.T) {
	base, token, handler, _ := startInputServer(t, t.TempDir(), data.NewID())
	handler.SetEMS(&emsapi.Handler{Profiles: profile.Default()})
	root := snapshotRoot(t, base, token)
	site, err := CreateDomainNode(handler.nc, root, NodeTypeSite, map[string]string{"description": "画面工程"})
	if err != nil {
		t.Fatal(err)
	}
	device, err := CreateDomainNode(handler.nc, site, data.NodeTypeDevice, map[string]string{"description": "泵房"})
	if err != nil {
		t.Fatal(err)
	}
	level, err := CreateDomainNode(handler.nc, device, data.NodeTypeVariable, map[string]string{"description": "液位"})
	if err != nil {
		t.Fatal(err)
	}

	leaky := `<html><body><script>const apiKey = "sk-live-0123456789abcdef";</script>` +
		`<span data-fastiot-point="` + level + `">--</span></body></html>`
	saveScreenDraft(t, handler, device, leaky, []genscreen.Point{{ID: level, Kind: "number"}})
	waitForNodePoints(t, handler.nc, device, generatedScreenDraftType)

	status, body := call(t, http.MethodGet, base+"/projects/export?site="+site, token, nil, nil)
	if status != http.StatusBadRequest || !strings.Contains(body, "screen_contains_secret") {
		t.Fatalf("export = %d %s", status, body)
	}
	if strings.Contains(body, "0123456789abcdef") {
		t.Fatal("the refusal repeated the secret")
	}
}
