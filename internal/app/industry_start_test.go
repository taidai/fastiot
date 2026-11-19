package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/industry"
)

// TestStartServesBaseWithEMSUninstalled boots the real application on a site
// whose persisted package state says the EMS package is not installed. The
// base API must come up and serve; the package API must refuse, not vanish.
func TestStartServesBaseWithEMSUninstalled(t *testing.T) {
	dir := t.TempDir()
	registry, err := industry.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Uninstall(industry.EMSPackageID); err != nil {
		t.Fatal(err)
	}
	password := data.NewID()
	t.Setenv("FASTEMS_ADMIN_PASSWORD", password)
	t.Setenv("FASTEMS_SEED_DEMO", "0")
	httpPort, err := FreePort()
	if err != nil {
		t.Fatal(err)
	}
	natsPort, err := FreePort()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	application, err := Start(ctx, Config{
		DataDir: dir, HTTPPort: strconv.Itoa(httpPort), NATSPort: natsPort, Version: "0.1.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = application.Close() }()

	base := fmt.Sprintf("http://127.0.0.1:%d/api", httpPort)
	token := loginForTest(t, base, "admin", password)
	if status, body := apiCall(t, http.MethodGet, base+"/energy-units", token, nil); status != http.StatusConflict || !bytes.Contains(body, []byte("industry_package_not_installed")) {
		t.Fatalf("package route after restart = %d %s", status, body)
	}
	if status, body := apiCall(t, http.MethodGet, base+"/snapshot", token, nil); status != http.StatusOK {
		t.Fatalf("base snapshot after restart = %d %s", status, body)
	}
}

func loginForTest(t *testing.T, base, email, password string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"email": email, "password": password})
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Post(base+"/login", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil || result.Token == "" {
		t.Fatalf("login = %d %v", response.StatusCode, err)
	}
	return result.Token
}

func apiCall(t *testing.T, method, url, token string, body []byte) (int, []byte) {
	t.Helper()
	request, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var payload bytes.Buffer
	_, _ = payload.ReadFrom(response.Body)
	return response.StatusCode, payload.Bytes()
}
