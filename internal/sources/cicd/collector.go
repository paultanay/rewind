// Package cicd implements the CI/CD source collector for Rewind.
// It fetches deployment and pipeline events from GitHub and GitLab via their
// REST APIs and maps them to model.EventKindDeploy events.
//
// Design decisions from spec §8:
//   - Lookback window: incident window + 2h before (deploys before the window
//     are prime suspects and must be included).
//   - Both GitHub and GitLab run as sub-collectors under one Collector; which
//     one fires depends on configuration (token + repos/projects present).
//   - Read-only: only GET requests, no write operations.
//   - Pure stdlib HTTP client — no third-party API client libraries.
//   - Recorded httptest fixture tests cover both providers.
package cicd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/paultanay/rewind/internal/model"
	"github.com/paultanay/rewind/internal/sources"
)

const sourceName = "cicd"

// lookbackExtra is the extra window before the incident From time that we
// search for deployments. Deploys 2h before an incident are prime suspects.
const lookbackExtra = 2 * time.Hour

// Collector fetches deployment events from GitHub and/or GitLab.
type Collector struct {
	GitHub GitHubConfig
	GitLab GitLabConfig
	// Version is the rewind binary version (used in User-Agent).
	Version string

	httpClient *http.Client
}

// GitHubConfig holds GitHub API credentials and repository list.
type GitHubConfig struct {
	Token string
	// Repos is a list of "owner/repo" strings.
	Repos    []string
	Disabled bool
}

// GitLabConfig holds GitLab API credentials and project list.
type GitLabConfig struct {
	// BaseURL defaults to https://gitlab.com
	BaseURL  string
	Token    string
	Projects []string
	Disabled bool
}

// Name implements sources.Collector.
func (c *Collector) Name() string { return sourceName }

// Check implements sources.Collector.
func (c *Collector) Check(ctx context.Context) error {
	cl := c.client()
	var errs []string
	if !c.GitHub.Disabled && c.GitHub.Token != "" {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/rate_limit", nil)
		req.Header.Set("Authorization", "Bearer "+c.GitHub.Token)
		req.Header.Set("User-Agent", "rewind/"+c.Version)
		resp, err := cl.Do(req)
		if err != nil {
			errs = append(errs, "github: "+err.Error())
		} else {
			_ = resp.Body.Close()
			if resp.StatusCode >= 400 {
				errs = append(errs, fmt.Sprintf("github: HTTP %d", resp.StatusCode))
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// Collect implements sources.Collector.
// It fans out to GitHub and GitLab concurrently and merges results.
func (c *Collector) Collect(ctx context.Context, scope model.Scope, window model.TimeRange) (sources.CollectResult, error) {
	// Expand the search window backwards by lookbackExtra.
	searchFrom := window.From.Add(-lookbackExtra)
	searchWindow := model.TimeRange{From: searchFrom, To: window.To}

	var events []model.Event
	var entities []model.Entity
	var errs []string

	// GitHub deployments.
	if !c.GitHub.Disabled && c.GitHub.Token != "" && len(c.GitHub.Repos) > 0 {
		ghEvts, ghEnts, err := c.collectGitHub(ctx, searchWindow, scope)
		if err != nil {
			errs = append(errs, "github: "+err.Error())
		}
		events = append(events, ghEvts...)
		entities = append(entities, ghEnts...)
	}

	// GitLab deployments.
	if !c.GitLab.Disabled && c.GitLab.Token != "" && len(c.GitLab.Projects) > 0 {
		glEvts, glEnts, err := c.collectGitLab(ctx, searchWindow, scope)
		if err != nil {
			errs = append(errs, "gitlab: "+err.Error())
		}
		events = append(events, glEvts...)
		entities = append(entities, glEnts...)
	}

	var collectErr error
	if len(errs) > 0 && len(events) == 0 {
		collectErr = fmt.Errorf("%s", strings.Join(errs, "; "))
	}

	return sources.CollectResult{
		Events:   events,
		Entities: entities,
	}, collectErr
}

// ─── GitHub ───────────────────────────────────────────────────────────────────

// ghDeployment is the relevant subset of the GitHub Deployments API response.
type ghDeployment struct {
	ID          int64     `json:"id"`
	CreatedAt   time.Time `json:"created_at"`
	Environment string    `json:"environment"`
	Description string    `json:"description"`
	Ref         string    `json:"ref"`
	SHA         string    `json:"sha"`
	Creator     struct {
		Login string `json:"login"`
	} `json:"creator"`
	URL string `json:"url"`
}

func (c *Collector) collectGitHub(
	ctx context.Context,
	window model.TimeRange,
	scope model.Scope,
) ([]model.Event, []model.Entity, error) {
	var events []model.Event
	var entities []model.Entity

	for _, repo := range c.GitHub.Repos {
		// Match repo to scope services: "owner/checkout" matches service "checkout".
		svcName := repoServiceName(repo)
		if len(scope.Services) > 0 && !serviceInScope(svcName, scope.Services) {
			continue
		}

		deploys, err := c.ghListDeployments(ctx, repo, window)
		if err != nil {
			return nil, nil, fmt.Errorf("repo %s: %w", repo, err)
		}

		for _, d := range deploys {
			entityID := entityIDForService(scope, svcName)
			events = append(events, model.Event{
				ID:       model.NewEventID(),
				At:       d.CreatedAt,
				Kind:     model.EventKindDeploy,
				EntityID: entityID,
				Severity: model.SeverityNotable,
				Title:    fmt.Sprintf("Deployed %s (GitHub: %s@%s)", svcName, d.Ref, shortSHA(d.SHA)),
				Detail: fmt.Sprintf(
					"Repo: %s\nRef: %s\nSHA: %s\nAuthor: %s\nEnvironment: %s\n%s",
					repo, d.Ref, d.SHA, d.Creator.Login, d.Environment, d.Description,
				),
				SourceRef: model.SourceRef{
					SourceName: sourceName,
					NativeID:   fmt.Sprintf("github/deployment/%d", d.ID),
					URL: fmt.Sprintf(
						"https://github.com/%s/deployments/%s",
						repo, d.Environment,
					),
				},
			})
			entities = append(entities, model.Entity{
				ID:          entityID,
				Kind:        model.EntityKindService,
				DisplayName: svcName,
			})
		}
	}
	return events, entities, nil
}

func (c *Collector) ghListDeployments(ctx context.Context, repo string, window model.TimeRange) ([]ghDeployment, error) {
	endpoint := fmt.Sprintf("https://api.github.com/repos/%s/deployments?per_page=100", url.PathEscape(repo))
	data, err := c.get(ctx, endpoint, map[string]string{
		"Authorization":        "Bearer " + c.GitHub.Token,
		"Accept":               "application/vnd.github+json",
		"X-GitHub-Api-Version": "2022-11-28",
	})
	if err != nil {
		return nil, err
	}

	var deploys []ghDeployment
	if err := json.Unmarshal(data, &deploys); err != nil {
		return nil, fmt.Errorf("parse deployments: %w", err)
	}

	// Filter to window.
	var filtered []ghDeployment
	for _, d := range deploys {
		if !d.CreatedAt.Before(window.From) && !d.CreatedAt.After(window.To) {
			filtered = append(filtered, d)
		}
	}
	return filtered, nil
}

// ─── GitLab ───────────────────────────────────────────────────────────────────

// glDeployment is the relevant subset of the GitLab Deployments API response.
type glDeployment struct {
	ID          int64     `json:"id"`
	CreatedAt   time.Time `json:"created_at"`
	Environment struct {
		Name string `json:"name"`
	} `json:"environment"`
	Ref    string `json:"ref"`
	SHA    string `json:"sha"`
	Status string `json:"status"`
	User   struct {
		Username string `json:"username"`
	} `json:"user"`
}

func (c *Collector) collectGitLab(
	ctx context.Context,
	window model.TimeRange,
	scope model.Scope,
) ([]model.Event, []model.Entity, error) {
	var events []model.Event
	var entities []model.Entity

	baseURL := c.GitLab.BaseURL
	if baseURL == "" {
		baseURL = "https://gitlab.com"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	for _, project := range c.GitLab.Projects {
		svcName := repoServiceName(project)
		if len(scope.Services) > 0 && !serviceInScope(svcName, scope.Services) {
			continue
		}

		encodedProject := url.PathEscape(project)
		endpoint := fmt.Sprintf("%s/api/v4/projects/%s/deployments?per_page=100&order_by=created_at&sort=desc", baseURL, encodedProject)
		data, err := c.get(ctx, endpoint, map[string]string{
			"PRIVATE-TOKEN": c.GitLab.Token,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("project %s: %w", project, err)
		}

		var deploys []glDeployment
		if err := json.Unmarshal(data, &deploys); err != nil {
			return nil, nil, fmt.Errorf("parse deployments: %w", err)
		}

		for _, d := range deploys {
			if d.CreatedAt.Before(window.From) || d.CreatedAt.After(window.To) {
				continue
			}
			// Only include successful deployments (not failed CI runs).
			if d.Status != "" && d.Status != "success" && d.Status != "running" {
				continue
			}

			entityID := entityIDForService(scope, svcName)
			events = append(events, model.Event{
				ID:       model.NewEventID(),
				At:       d.CreatedAt,
				Kind:     model.EventKindDeploy,
				EntityID: entityID,
				Severity: model.SeverityNotable,
				Title:    fmt.Sprintf("Deployed %s (GitLab: %s@%s)", svcName, d.Ref, shortSHA(d.SHA)),
				Detail: fmt.Sprintf(
					"Project: %s\nRef: %s\nSHA: %s\nAuthor: %s\nEnvironment: %s",
					project, d.Ref, d.SHA, d.User.Username, d.Environment.Name,
				),
				SourceRef: model.SourceRef{
					SourceName: sourceName,
					NativeID:   fmt.Sprintf("gitlab/deployment/%d", d.ID),
					URL:        fmt.Sprintf("%s/%s/-/deployments/%d", baseURL, project, d.ID),
				},
			})
			entities = append(entities, model.Entity{
				ID:          entityID,
				Kind:        model.EntityKindService,
				DisplayName: svcName,
			})
		}
	}
	return events, entities, nil
}

// ─── HTTP client ──────────────────────────────────────────────────────────────

func (c *Collector) client() *http.Client {
	if c.httpClient != nil {
		return c.httpClient
	}
	c.httpClient = &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			MaxIdleConnsPerHost: 5,
			IdleConnTimeout:     30 * time.Second,
		},
	}
	return c.httpClient
}

// SetHTTPClient injects a custom HTTP client. Used in tests.
func (c *Collector) SetHTTPClient(hc *http.Client) {
	c.httpClient = hc
}

func (c *Collector) get(ctx context.Context, endpoint string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "rewind/"+c.Version)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	// Retry once on transient server errors.
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}
		resp, err := c.client().Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		_ = resp.Body.Close()

		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
			continue
		}
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
		}
		return body, nil
	}
	return nil, fmt.Errorf("after 2 attempts: %w", lastErr)
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// repoServiceName extracts the last path component from "owner/repo" or
// "group/subgroup/project". This is used to match repos to service names.
func repoServiceName(repo string) string {
	parts := strings.Split(repo, "/")
	return parts[len(parts)-1]
}

func serviceInScope(name string, services []string) bool {
	for _, s := range services {
		if strings.EqualFold(name, s) || strings.Contains(strings.ToLower(name), strings.ToLower(s)) {
			return true
		}
	}
	return false
}

func entityIDForService(scope model.Scope, svcName string) string {
	ns := ""
	if len(scope.Namespaces) > 0 {
		ns = scope.Namespaces[0]
	}
	return model.NewEntityID(model.EntityKindService, ns, svcName)
}

func shortSHA(sha string) string {
	if len(sha) >= 7 {
		return sha[:7]
	}
	return sha
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
