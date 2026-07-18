package cicd_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/paultanay/rewind/internal/model"
	"github.com/paultanay/rewind/internal/sources/cicd"
)

var base = time.Date(2026, 7, 9, 14, 20, 0, 0, time.UTC)
var window = model.TimeRange{From: base, To: base.Add(45 * time.Minute)}

// ─── GitHub fixture tests ─────────────────────────────────────────────────────

func TestGitHub_DeploymentInWindow(t *testing.T) {
	t.Parallel()

	// Serve a single deployment within the search window (window.From - 2h → window.To).
	deployAt := base.Add(-30 * time.Minute) // 30m before incident — prime suspect
	payload, _ := json.Marshal([]map[string]any{
		{
			"id":          42,
			"created_at":  deployAt.Format(time.RFC3339),
			"environment": "production",
			"ref":         "v2.3.1",
			"sha":         "a1b2c3d4e5f6",
			"description": "Deploy checkout v2.3.1",
			"creator":     map[string]string{"login": "alice"},
			"url":         "https://api.github.com/repos/myorg/checkout/deployments/42",
		},
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(payload) //nolint:errcheck
	}))
	defer srv.Close()

	c := &cicd.Collector{
		GitHub: cicd.GitHubConfig{
			Token: "test-token",
			Repos: []string{"myorg/checkout"},
		},
		Version: "test",
	}
	c.SetHTTPClient(patchedClient(srv.URL, "https://api.github.com"))

	result, err := c.Collect(context.Background(),
		model.Scope{Namespaces: []string{"shop"}, Services: []string{"checkout"}},
		window,
	)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(result.Events) == 0 {
		t.Fatal("expected at least one Deploy event, got none")
	}

	evt := result.Events[0]
	if evt.Kind != model.EventKindDeploy {
		t.Errorf("event kind = %v, want Deploy", evt.Kind)
	}
	if evt.Severity != model.SeverityNotable {
		t.Errorf("severity = %v, want Notable", evt.Severity)
	}
	if evt.SourceRef.SourceName != "cicd" {
		t.Errorf("sourceRef.sourceName = %q, want cicd", evt.SourceRef.SourceName)
	}
	// Detail must contain commit SHA and author.
	for _, want := range []string{"a1b2c3d4e5f6", "alice", "v2.3.1"} {
		if !contains(evt.Detail, want) {
			t.Errorf("Detail missing %q: %s", want, evt.Detail)
		}
	}
}

func TestGitHub_DeploymentOutsideWindow(t *testing.T) {
	t.Parallel()
	// Deployment 4h before incident — outside the 2h lookback.
	deployAt := base.Add(-4 * time.Hour)
	payload, _ := json.Marshal([]map[string]any{
		{
			"id": 10, "created_at": deployAt.Format(time.RFC3339),
			"environment": "production", "ref": "v2.3.0", "sha": "000000",
			"creator": map[string]string{"login": "bob"},
		},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(payload) //nolint:errcheck
	}))
	defer srv.Close()

	c := &cicd.Collector{
		GitHub:  cicd.GitHubConfig{Token: "test", Repos: []string{"org/checkout"}},
		Version: "test",
	}
	c.SetHTTPClient(patchedClient(srv.URL, "https://api.github.com"))

	result, _ := c.Collect(context.Background(),
		model.Scope{Services: []string{"checkout"}},
		window,
	)
	if len(result.Events) != 0 {
		t.Errorf("expected 0 events outside lookback, got %d", len(result.Events))
	}
}

// ─── GitLab fixture tests ─────────────────────────────────────────────────────

func TestGitLab_DeploymentInWindow(t *testing.T) {
	t.Parallel()
	deployAt := base.Add(-15 * time.Minute)
	payload, _ := json.Marshal([]map[string]any{
		{
			"id":         99,
			"created_at": deployAt.Format(time.RFC3339),
			"status":     "success",
			"ref":        "v2.3.1",
			"sha":        "deadbeef1234",
			"environment": map[string]string{"name": "production"},
			"user":       map[string]string{"username": "alice"},
		},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(payload) //nolint:errcheck
	}))
	defer srv.Close()

	c := &cicd.Collector{
		GitLab: cicd.GitLabConfig{
			BaseURL:  srv.URL,
			Token:    "test-token",
			Projects: []string{"mygroup/checkout"},
		},
		Version: "test",
	}

	result, err := c.Collect(context.Background(),
		model.Scope{Namespaces: []string{"shop"}, Services: []string{"checkout"}},
		window,
	)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(result.Events) == 0 {
		t.Fatal("expected Deploy event from GitLab, got none")
	}
	evt := result.Events[0]
	if evt.Kind != model.EventKindDeploy {
		t.Errorf("kind = %v, want Deploy", evt.Kind)
	}
	for _, want := range []string{"deadbeef1234", "alice", "v2.3.1"} {
		if !contains(evt.Detail, want) {
			t.Errorf("Detail missing %q", want)
		}
	}
}

func TestGitLab_FailedDeploymentExcluded(t *testing.T) {
	t.Parallel()
	deployAt := base.Add(-10 * time.Minute)
	payload, _ := json.Marshal([]map[string]any{
		{
			"id": 5, "created_at": deployAt.Format(time.RFC3339),
			"status": "failed", "ref": "v2.3.1", "sha": "aabbcc",
			"environment": map[string]string{"name": "production"},
			"user":        map[string]string{"username": "bob"},
		},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(payload) //nolint:errcheck
	}))
	defer srv.Close()

	c := &cicd.Collector{
		GitLab:  cicd.GitLabConfig{BaseURL: srv.URL, Token: "t", Projects: []string{"org/checkout"}},
		Version: "test",
	}
	result, _ := c.Collect(context.Background(),
		model.Scope{Services: []string{"checkout"}},
		window,
	)
	if len(result.Events) != 0 {
		t.Errorf("failed deployment should be excluded, got %d events", len(result.Events))
	}
}

func TestCollector_Name(t *testing.T) {
	c := &cicd.Collector{}
	if c.Name() != "cicd" {
		t.Errorf("Name() = %q, want cicd", c.Name())
	}
}

func TestCollector_BothDisabled(t *testing.T) {
	t.Parallel()
	c := &cicd.Collector{
		GitHub:  cicd.GitHubConfig{Disabled: true},
		GitLab:  cicd.GitLabConfig{Disabled: true},
		Version: "test",
	}
	result, err := c.Collect(context.Background(), model.Scope{}, window)
	if err != nil {
		t.Errorf("both disabled should not error: %v", err)
	}
	if len(result.Events) != 0 {
		t.Errorf("expected 0 events when both disabled, got %d", len(result.Events))
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub ||
		(len(s) > 0 && (containsHelper(s, sub))))
}

func containsHelper(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// patchedClient returns an *http.Client whose transport rewrites requests
// from realBase to testBase, allowing us to redirect GitHub API calls to
// the local test server.
func patchedClient(testBase, realBase string) *http.Client {
	return &http.Client{
		Transport: &rewriteTransport{testBase: testBase, realBase: realBase},
	}
}

type rewriteTransport struct {
	testBase string
	realBase string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	rawURL := cloned.URL.String()
	rawURL = replacePrefix(rawURL, t.realBase, t.testBase)
	u, _ := cloned.URL.Parse(rawURL)
	cloned.URL = u
	cloned.Host = u.Host
	return http.DefaultTransport.RoundTrip(cloned)
}

func replacePrefix(s, old, new string) string {
	if len(s) >= len(old) && s[:len(old)] == old {
		return new + s[len(old):]
	}
	return s
}
