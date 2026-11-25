package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"fastiot/internal/appapi"
	"fastiot/internal/ems/historyquery"
	"github.com/simpleiot/simpleiot/data"
)

func aggregateAPI(t *testing.T, method, path, token string, body, out any) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(method, "http://127.0.0.1:18105/api"+path, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := (&http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	content, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatalf("%s %s: %d %s", method, path, response.StatusCode, content)
	}
	if out != nil {
		if err := json.Unmarshal(content, out); err != nil {
			t.Fatal(err)
		}
	}
}

func TestApplicationProducesAggregatesWithoutReaders(t *testing.T) {
	password := data.NewID()
	t.Setenv("FASTEMS_ADMIN_PASSWORD", password)
	t.Setenv("FASTEMS_SEED_DEMO", "0")
	port, err := FreePort()
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{DataDir: t.TempDir(), NATSPort: port, HTTPPort: "18105", Version: "aggregate-test"}
	application, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { application.Close() }()
	var session struct {
		Token string `json:"token"`
	}
	aggregateAPI(t, "POST", "/login", "", map[string]string{"email": "admin", "password": password}, &session)
	var snapshot struct {
		Nodes []data.NodeEdge `json:"nodes"`
	}
	aggregateAPI(t, "GET", "/snapshot", session.Token, nil, &snapshot)
	root := ""
	for _, node := range snapshot.Nodes {
		if node.Parent == "root" {
			root = node.ID
		}
	}
	create := func(typ, parent string, value float64) string {
		var result struct {
			ID string `json:"id"`
		}
		aggregateAPI(t, "POST", "/nodes", session.Token, map[string]any{"type": typ, "parent": parent, "description": "isolated aggregate", "value": value}, &result)
		return result.ID
	}
	group := create("group", root, 0)
	member := create("variable", group, 7)
	before := time.Now()
	config := appapi.AggregateConfig{Group: group, Op: "sum", Members: []string{member}, Units: "kW"}
	aggregateAPI(t, "PUT", "/aggregate", session.Token, config, nil)
	// No browser, HTTP read, or snapshot request runs during this interval.
	time.Sleep(2500 * time.Millisecond)
	var result appapi.AggregateResult
	aggregateAPI(t, "GET", "/aggregate?group="+group, session.Token, nil, &result)
	if result.Value == nil || *result.Value != 7 || result.Quality != "good" || result.Valid != 1 || result.Time == nil || result.Time.After(before) {
		t.Fatalf("no reader-independent result with original observation time: %+v", result)
	}
	var history struct {
		Results []historyquery.PointResult `json:"results"`
	}
	aggregateAPI(t, "GET", "/history?node_id="+group+"&type=easyemsAggregateValue", session.Token, nil, &history)
	if len(history.Results) != 1 || len(history.Results[0].Records) < 2 {
		t.Fatalf("no continuous unattended history: %+v", history)
	}
	for _, record := range history.Results[0].Records {
		if record.Value == nil || *record.Value != 7 || record.Quality != "good" || !record.SourceTime.Equal(*result.Time) || !record.ReceivedTime.After(record.SourceTime) {
			t.Fatalf("history substituted evaluation time or lost quality: %+v", record)
		}
		var evidence appapi.AggregateResult
		if err := json.Unmarshal([]byte(record.Text), &evidence); err != nil || evidence.Valid != 1 || evidence.Total != 1 {
			t.Fatalf("history lacks member evidence: %+v %v", record, err)
		}
	}
	prior := result.EvaluatedAt
	application.Close()
	select {
	case <-application.Exited():
	default:
		t.Fatal("old runtime did not finish shutdown")
	}
	stoppedAt := time.Now()
	time.Sleep(1100 * time.Millisecond)
	resumedAt := time.Now()
	application, err = Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Definitions resume with no logged-in reader after a real storage restart.
	time.Sleep(2100 * time.Millisecond)
	aggregateAPI(t, "POST", "/login", "", map[string]string{"email": "admin", "password": password}, &session)
	aggregateAPI(t, "GET", "/aggregate?group="+group, session.Token, nil, &result)
	if !result.Configured || !result.EvaluatedAt.After(prior) || result.Config.Group != group || len(result.Config.Members) != 1 || result.Config.Members[0] != member {
		t.Fatalf("definition did not resume: %+v", result)
	}
	if result.Value != nil && (result.Time == nil || result.Time.After(before)) {
		t.Fatal("restart manufactured source freshness")
	}
	aggregateAPI(t, "GET", "/history?node_id="+group+"&type=easyemsAggregateValue", session.Token, nil, &history)
	if len(history.Results) != 1 || len(history.Results[0].Records) < 4 {
		t.Fatalf("history did not survive and progress after restart: %+v", history)
	}
	for _, record := range history.Results[0].Records {
		if record.ReceivedTime.After(stoppedAt) && record.ReceivedTime.Before(resumedAt) {
			t.Fatalf("old sampler produced after shutdown: %+v", record)
		}
	}
}
