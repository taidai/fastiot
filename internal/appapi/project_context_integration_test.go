package appapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/simpleiot/simpleiot/data"
)

// Opt-in harness for the self-contained Playwright test. It never loads user
// data, starts device clients, or uses the repository's launch configuration.
func TestProjectContextBrowserBackend(t *testing.T) {
	if os.Getenv("FASTIOT_CONTEXT_BROWSER") != "1" {
		t.Skip("browser harness is opt-in")
	}
	base, token, h, _ := startInputServer(t, t.TempDir(), "context-browser-test-only")
	root := snapshotRoot(t, base, token)
	for _, name := range []string{"North Site", "South Site"} {
		if _, err := CreateDomainNode(h.nc, root, NodeTypeSite, map[string]string{"description": name}); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", h))
	mux.HandleFunc("GET /__fixture", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ticket02-isolated-store")) })
	mux.HandleFunc("POST /__finish", func(w http.ResponseWriter, r *http.Request) {
		select {
		case done <- struct{}{}:
		default:
		}
		w.WriteHeader(204)
	})
	srv := &http.Server{Addr: ":18082", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Error(err)
			done <- struct{}{}
		}
	}()
	defer srv.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Minute):
		t.Fatal("browser test did not finish")
	}
}

func projectCall(t *testing.T, method, url, token, project string, input any, output any) (int, string) {
	t.Helper()
	body, _ := json.Marshal(input)
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Fastiot-Project", project)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if output != nil && res.StatusCode == 200 {
		if err := json.Unmarshal(raw, output); err != nil {
			t.Fatal(err)
		}
	}
	return res.StatusCode, string(raw)
}

func TestProjectContextRejectsForeignProjectBeforeReadOrWrite(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, token)
	site, err := CreateDomainNode(h.nc, root, NodeTypeSite, map[string]string{"description": "not a project"})
	if err != nil {
		t.Fatal(err)
	}
	for _, project := range []string{"not-this-project", site} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			path := "/snapshot"
			if method == http.MethodPost {
				path = "/nodes"
			}
			status, body := projectCall(t, method, base+path, token, project, map[string]any{"parent": root, "type": "group", "description": "must not be created"}, nil)
			if status != 409 {
				t.Fatalf("foreign project %s: %d %s", method, status, body)
			}
		}
	}
	status, body := projectCall(t, http.MethodGet, base+"/snapshot", token, root, nil, nil)
	if status != 200 {
		t.Fatalf("valid project: %d %s", status, body)
	}
}

func TestProjectDirectoryKeepsObjectScopeAndIdentityAcrossRestart(t *testing.T) {
	dir, password := t.TempDir(), data.NewID()
	base, admin, h, stop := startInputServer(t, dir, password)
	root := snapshotRoot(t, base, admin)
	site, err := CreateDomainNode(h.nc, root, NodeTypeSite, map[string]string{"description": "visible site"})
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := CreateDomainNode(h.nc, root, NodeTypeSite, map[string]string{"description": "hidden site"})
	if err != nil {
		t.Fatal(err)
	}
	viewer := inputAccount(t, &pointConfigFixture{base: base, token: admin}, site, "viewer", nil)
	var directory struct {
		Projects []projectContext `json:"projects"`
	}
	status, body := call(t, http.MethodGet, base+"/projects", viewer, nil, &directory)
	if status != 200 || len(directory.Projects) != 1 {
		t.Fatalf("scoped directory: %d %s", status, body)
	}
	project := directory.Projects[0]
	if project.ID != root || len(project.Sites) != 1 || project.Sites[0].ID != site || len(project.ScopeRootIDs) != 1 || project.ScopeRootIDs[0] != site {
		t.Fatalf("anchor must remain an access scope, not become a project: %+v", project)
	}
	var snapshot struct {
		Nodes []data.NodeEdge `json:"nodes"`
	}
	status, body = projectCall(t, http.MethodGet, base+"/snapshot", viewer, root, nil, &snapshot)
	if status != 200 {
		t.Fatalf("scoped snapshot: %d %s", status, body)
	}
	for _, node := range snapshot.Nodes {
		if node.ID == hidden || node.ID == root {
			t.Fatalf("scope leak: %s", node.ID)
		}
	}
	status, _ = projectCall(t, http.MethodPatch, base+"/nodes/"+hidden, viewer, root, map[string]any{"description": "forbidden"}, nil)
	if status != 403 {
		t.Fatalf("viewer wrote outside scope: %d", status)
	}
	implementer := inputAccount(t, &pointConfigFixture{base: base, token: admin}, site, "implementer", nil)
	status, _ = projectCall(t, http.MethodPatch, base+"/nodes/"+hidden, implementer, root, map[string]any{"description": "forbidden"}, nil)
	if status != 403 {
		t.Fatalf("project header widened object permission: %d", status)
	}
	stop()
	base, admin, _, _ = startInputServer(t, dir, password)
	status, body = call(t, http.MethodGet, base+"/projects", admin, nil, &directory)
	if status != 200 || directory.Projects[0].ID != root || len(directory.Projects[0].Sites) != 2 {
		t.Fatalf("restart identity: %d %s", status, body)
	}
}

func TestProjectContextPreservesConfigurationRevisionContract(t *testing.T) {
	dir, password := t.TempDir(), data.NewID()
	base, first, _, stop := startInputServer(t, dir, password)
	second := loginAPI(t, base, "admin", password)
	root := snapshotRoot(t, base, first)
	area := createNodeAPI(t, base, first, "group", root, "revision scope", 0)
	url := base + "/ems/screen/" + area
	draft := automaticScreen(area, 0)
	status, body := projectCall(t, http.MethodPut, url, first, root, draft, nil)
	if status != 200 {
		t.Fatalf("first editor save: %d %s", status, body)
	}
	status, body = projectCall(t, http.MethodPut, url, second, root, draft, nil)
	if status != 409 {
		t.Fatalf("stale second editor: %d %s", status, body)
	}
	stop()
	base, token, _, _ := startInputServer(t, dir, password)
	var saved struct {
		Version int    `json:"version"`
		Layer   string `json:"layer"`
	}
	status, body = projectCall(t, http.MethodGet, base+"/ems/screen/"+area, token, root, nil, &saved)
	if status != 200 || saved.Version != 1 || saved.Layer != area {
		t.Fatalf("refresh after restart: %d %s", status, body)
	}
}

func TestProjectDirectoryUsesRootNotSites(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	root := snapshotRoot(t, base, token)
	for _, name := range []string{"North", "South"} {
		if _, err := CreateDomainNode(h.nc, root, NodeTypeSite, map[string]string{"description": name}); err != nil {
			t.Fatal(err)
		}
	}
	var directory struct {
		Projects []struct {
			ID     string   `json:"id"`
			RootID string   `json:"root_id"`
			Scope  []string `json:"scope_root_ids"`
			Sites  []struct {
				ID string `json:"id"`
			} `json:"sites"`
		} `json:"projects"`
	}
	status, body := call(t, http.MethodGet, base+"/projects", token, nil, &directory)
	if status != 200 {
		t.Fatalf("directory: %d %s", status, body)
	}
	if len(directory.Projects) != 1 || directory.Projects[0].ID != root || directory.Projects[0].RootID != root || len(directory.Projects[0].Sites) != 2 {
		t.Fatalf("project is the durable root, not a Site: %+v", directory)
	}
	status, _ = call(t, http.MethodGet, base+"/projects", "", nil, nil)
	if status != 401 {
		t.Fatalf("anonymous directory: %d", status)
	}
}
