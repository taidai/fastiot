package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/simpleiot/simpleiot/data"
)

func calculationAPI(t *testing.T, a *App, method, path, token string, body any, out any) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, "/api"+path, bytes.NewReader(encoded))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	a.ServeHTTP(response, req)
	if response.Code != 200 {
		t.Fatalf("%s %s: %d %s", method, path, response.Code, response.Body.String())
	}
	if out != nil {
		if err = json.Unmarshal(response.Body.Bytes(), out); err != nil {
			t.Fatal(err)
		}
	}
}
func TestApplicationStartsAndRestartsPureConfigurationCalculations(t *testing.T) {
	password := data.NewID()
	t.Setenv("FASTEMS_ADMIN_PASSWORD", password)
	t.Setenv("FASTEMS_SEED_DEMO", "0")
	port, err := FreePort()
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{DataDir: t.TempDir(), NATSPort: port, DisableHTTP: true, Version: "calculation-test"}
	application, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { application.Close() }()
	login := func() string {
		var result struct {
			Token string `json:"token"`
		}
		calculationAPI(t, application, "POST", "/login", "", map[string]any{"email": "admin", "password": password}, &result)
		return result.Token
	}
	token := login()
	var snapshot struct {
		Nodes []data.NodeEdge `json:"nodes"`
	}
	calculationAPI(t, application, "GET", "/snapshot", token, nil, &snapshot)
	root := ""
	for _, node := range snapshot.Nodes {
		if node.Parent == "root" {
			root = node.ID
			break
		}
	}
	if root == "" {
		t.Fatal("missing project root")
	}
	type object struct {
		ID       string `json:"id"`
		Revision uint64 `json:"revision"`
		State    struct {
			Value   *float64 `json:"value"`
			Quality string   `json:"quality"`
			BatchID string   `json:"batch_id"`
		} `json:"state"`
	}
	var capacity object
	calculationAPI(t, application, "POST", "/virtual-inputs", token, map[string]any{"parent": root, "name": "capacity", "source": "configuration", "operation_id": "capacity", "definition": map[string]any{"kind": "number", "unit": "kW", "purpose": "capacity", "min": 0, "max": 200, "validity": "constant", "value": 100}}, &capacity)
	calculationAPI(t, application, "POST", "/virtual-inputs/"+capacity.ID+"/apply", token, map[string]any{"revision": capacity.Revision, "operation_id": "capacity-apply"}, &capacity)
	var total object
	calculationAPI(t, application, "POST", "/calculations", token, map[string]any{"parent": root, "name": "double-capacity", "operation_id": "total", "definition": map[string]any{"operator": "add", "kind": "number", "unit": "kW", "inputs": []string{capacity.ID, capacity.ID}}}, &total)
	calculationAPI(t, application, "POST", "/calculations/"+total.ID+"/apply", token, map[string]any{"revision": total.Revision, "operation_id": "total-apply"}, &total)
	wait := func() object {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			var result object
			calculationAPI(t, application, "GET", "/calculations/"+total.ID, token, nil, &result)
			if result.State.Value != nil && *result.State.Value == 200 && result.State.Quality == "good" {
				return result
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("application did not run its calculation loop")
		return object{}
	}
	first := wait()
	application.Close()
	application, err = Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	token = login()
	restored := wait()
	if restored.State.BatchID == first.State.BatchID {
		t.Fatal("restart reused old runtime batch identity")
	}
}
