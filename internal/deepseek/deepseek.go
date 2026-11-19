package deepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// DefaultBaseURL is DeepSeek's public API root. Deliverers may point
	// DEEPSEEK_BASE_URL at a gateway they control.
	DefaultBaseURL = "https://api.deepseek.com"
	DefaultModel   = "deepseek-chat"

	envAPIKey  = "DEEPSEEK_API_KEY"
	envBaseURL = "DEEPSEEK_BASE_URL"
	envModel   = "DEEPSEEK_MODEL"
)

// Credential is the deliverer's own DeepSeek credential. fastiot never ships
// one and never persists it; it is read from the process environment.
type Credential struct {
	APIKey  string
	BaseURL string
	Model   string
}

// Configured reports whether the deliverer supplied a credential.
func (c Credential) Configured() bool { return strings.TrimSpace(c.APIKey) != "" }

// FromEnv reads the deliverer's credential. An empty key means unconfigured.
func FromEnv(getenv func(string) string) Credential {
	credential := Credential{
		APIKey:  strings.TrimSpace(getenv(envAPIKey)),
		BaseURL: strings.TrimSpace(getenv(envBaseURL)),
		Model:   strings.TrimSpace(getenv(envModel)),
	}
	if credential.BaseURL == "" {
		credential.BaseURL = DefaultBaseURL
	}
	if credential.Model == "" {
		credential.Model = DefaultModel
	}
	return credential
}

// Status is safe to expose to the browser: it never carries the key.
type Status struct {
	Configured bool   `json:"configured"`
	Model      string `json:"model"`
}

// Service owns the deliverer's credential and talks to the model endpoint.
type Service struct {
	credential Credential
	client     *http.Client
}

func NewService(credential Credential) *Service {
	if credential.BaseURL == "" {
		credential.BaseURL = DefaultBaseURL
	}
	if credential.Model == "" {
		credential.Model = DefaultModel
	}
	return &Service{credential: credential, client: &http.Client{Timeout: 120 * time.Second}}
}

func (s *Service) Status() Status {
	if s == nil {
		return Status{Model: DefaultModel}
	}
	return Status{Configured: s.credential.Configured(), Model: s.credential.Model}
}

// Preview builds exactly the outbound list Generate would send. It never
// performs a network call.
func (s *Service) Preview(req Request) Manifest { return BuildManifest(req) }

var (
	ErrNotConfigured = errors.New("deepseek_not_configured")
	ErrBlocked       = errors.New("outbound_blocked")
)

type Result struct {
	Manifest Manifest
	Model    string
	Content  string
}

// Generate refuses to call the model unless a credential is configured and
// the outbound manifest is unblocked.
func (s *Service) Generate(ctx context.Context, req Request) (Result, error) {
	manifest := BuildManifest(req)
	result := Result{Manifest: manifest}
	if s == nil || !s.credential.Configured() {
		return result, ErrNotConfigured
	}
	result.Model = s.credential.Model
	if manifest.Blocked {
		return result, ErrBlocked
	}
	content, err := s.call(ctx, manifest)
	if err != nil {
		return result, err
	}
	result.Content = content
	return result, nil
}

func (s *Service) call(ctx context.Context, manifest Manifest) (string, error) {
	body, err := json.Marshal(map[string]any{
		"model":    s.credential.Model,
		"messages": MessagesForManifest(manifest),
		"stream":   false,
	})
	if err != nil {
		return "", err
	}
	endpoint := strings.TrimRight(s.credential.BaseURL, "/") + "/chat/completions"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+s.credential.APIKey)
	response, err := s.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("deepseek request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return "", fmt.Errorf("deepseek returned status %d", response.StatusCode)
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&parsed); err != nil {
		return "", fmt.Errorf("deepseek response unreadable: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("deepseek returned no content")
	}
	return parsed.Choices[0].Message.Content, nil
}
