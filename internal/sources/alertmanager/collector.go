// Package alertmanager implements the Rewind collector for Prometheus Alertmanager.
//
// Design constraints (spec §8):
//   - Query only the /api/v2/alerts endpoint (read-only).
//   - Each active alert → an AlertFired event; each resolved alert → AlertResolved.
//   - Do NOT create hypotheses from alerts alone (enforced by RW010).
//   - Deduplicate alerts by fingerprint (Alertmanager provides them).
//   - SourceRef deep-links to the Alertmanager web UI.
package alertmanager

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

// Config holds Alertmanager connection settings.
type Config struct {
	// URL is the base URL, e.g. "http://alertmanager.monitoring:9093".
	URL string `yaml:"url" json:"url"`
	// Username and Password for HTTP basic auth.
	Username string `yaml:"username" json:"username,omitempty"`
	Password string `yaml:"password" json:"password,omitempty"`
}

// Collector implements sources.Collector for Prometheus Alertmanager.
type Collector struct {
	cfg     Config
	client  *http.Client
	version string
}

// New creates an Alertmanager collector.
func New(cfg Config, version string) *Collector {
	return &Collector{
		cfg:     cfg,
		version: version,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// Name implements sources.Collector.
func (c *Collector) Name() string { return "alertmanager" }

// Check verifies the Alertmanager endpoint is reachable.
func (c *Collector) Check(ctx context.Context) error {
	if c.cfg.URL == "" {
		return fmt.Errorf("alertmanager.url not configured")
	}
	reqURL := strings.TrimRight(c.cfg.URL, "/") + "/api/v2/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return err
	}
	c.setHeaders(req)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("alertmanager connectivity check: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("alertmanager returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// Collect queries Alertmanager for alerts active during the given window.
// Resolved-but-within-window alerts are returned as AlertResolved events.
// Active-at-window-end alerts are returned as AlertFired events.
// No signals are returned — Alertmanager is event-only.
func (c *Collector) Collect(
	ctx context.Context,
	scope model.Scope,
	window model.TimeRange,
) (sources.CollectResult, error) {
	if c.cfg.URL == "" {
		return sources.CollectResult{}, fmt.Errorf("alertmanager.url not configured")
	}

	rawAlerts, err := c.fetchAlerts(ctx, scope)
	if err != nil {
		return sources.CollectResult{}, fmt.Errorf("alertmanager fetch: %w", err)
	}

	seen := make(map[string]bool)
	var result sources.CollectResult

	for _, a := range rawAlerts {
		// Skip alerts completely outside the window.
		if a.StartsAt.After(window.To) {
			continue
		}
		if a.EndsAt != nil && !a.EndsAt.IsZero() && a.EndsAt.Before(window.From) {
			continue
		}

		// Deduplicate by fingerprint.
		fp := a.Fingerprint
		if fp == "" {
			fp = a.Labels["alertname"] + "|" + a.Labels["namespace"]
		}
		if seen[fp] {
			continue
		}
		seen[fp] = true

		// Map alert labels to entity ID.
		entityID := alertEntityID(a)

		// Determine severity.
		sev := model.SeverityNotable
		if a.Labels["severity"] == "critical" || a.Labels["severity"] == "page" {
			sev = model.SeverityCritical
		}

		title := a.Labels["alertname"]
		if title == "" {
			title = "Alert"
		}

		at := a.StartsAt
		if at.Before(window.From) {
			at = window.From
		}

		detail := c.formatAlertDetail(a)
		deepLink := c.alertDeepLink(a)

		// Always emit an AlertFired event at the alert's start time.
		result.Events = append(result.Events, model.Event{
			ID:       "am-" + fp,
			At:       at,
			Kind:     model.EventKindAlertFired,
			EntityID: entityID,
			Severity: sev,
			Title:    title,
			Detail:   detail,
			SourceRef: model.SourceRef{
				SourceName: "alertmanager",
				URL:        deepLink,
			},
		})

		// If the alert resolved inside the window, also emit an AlertResolved event.
		if a.EndsAt != nil && !a.EndsAt.IsZero() &&
			a.EndsAt.After(window.From) && !a.EndsAt.After(window.To) {
			result.Events = append(result.Events, model.Event{
				ID:       "am-resolved-" + fp,
				At:       *a.EndsAt,
				Kind:     model.EventKindAlertResolved,
				EntityID: entityID,
				Severity: model.SeverityInfo,
				Title:    "Resolved: " + title,
				SourceRef: model.SourceRef{
					SourceName: "alertmanager",
					URL:        deepLink,
				},
			})
		}
	}

	return result, nil
}

// ─── Alertmanager HTTP API ────────────────────────────────────────────────────

type amAlert struct {
	Fingerprint string            `json:"fingerprint"`
	Status      struct {
		State string `json:"state"` // "active", "suppressed", "unprocessed"
	} `json:"status"`
	StartsAt    time.Time  `json:"startsAt"`
	EndsAt      *time.Time `json:"endsAt"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	Receivers   []struct {
		Name string `json:"name"`
	} `json:"receivers"`
	GeneratorURL string `json:"generatorURL"`
}

func (c *Collector) fetchAlerts(ctx context.Context, scope model.Scope) ([]amAlert, error) {
	reqURL := strings.TrimRight(c.cfg.URL, "/") + "/api/v2/alerts"
	params := url.Values{}
	// Filter by namespace if provided.
	for _, ns := range scope.Namespaces {
		params.Add("filter", fmt.Sprintf(`namespace="%s"`, ns))
	}
	// Also include service-level filters.
	for _, svc := range scope.Services {
		params.Add("filter", fmt.Sprintf(`app="%s"`, svc))
	}
	if len(params) > 0 {
		reqURL += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("alertmanager /api/v2/alerts returned HTTP %d: %s",
			resp.StatusCode, string(body[:min(200, len(body))]))
	}

	var alerts []amAlert
	if err := json.Unmarshal(body, &alerts); err != nil {
		return nil, fmt.Errorf("alertmanager response parse: %w", err)
	}
	return alerts, nil
}

func (c *Collector) setHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "rewind/"+c.version)
	if c.cfg.Username != "" {
		req.SetBasicAuth(c.cfg.Username, c.cfg.Password)
	}
}

func (c *Collector) alertDeepLink(a amAlert) string {
	if a.GeneratorURL != "" {
		return a.GeneratorURL
	}
	// Fall back to the Alertmanager UI filtered by fingerprint.
	return strings.TrimRight(c.cfg.URL, "/") + "/#/alerts?fingerprint=" + a.Fingerprint
}

func (c *Collector) formatAlertDetail(a amAlert) string {
	var sb strings.Builder
	if v := a.Annotations["summary"]; v != "" {
		sb.WriteString("Summary: " + v + "\n")
	}
	if v := a.Annotations["description"]; v != "" {
		sb.WriteString("Description: " + v + "\n")
	}
	sb.WriteString(fmt.Sprintf("State: %s\n", a.Status.State))
	if !a.StartsAt.IsZero() {
		sb.WriteString(fmt.Sprintf("Started: %s\n", a.StartsAt.UTC().Format(time.RFC3339)))
	}
	for k, v := range a.Labels {
		if k == "alertname" {
			continue
		}
		sb.WriteString(fmt.Sprintf("  %s=%s\n", k, v))
	}
	return sb.String()
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// alertEntityID maps alert labels to a rewind entity ID.
// Priority: pod label > deployment label > service/app label > namespace.
func alertEntityID(a amAlert) string {
	ns := a.Labels["namespace"]
	if pod := a.Labels["pod"]; pod != "" && ns != "" {
		return "pod/" + ns + "/" + pod
	}
	if dep := a.Labels["deployment"]; dep != "" && ns != "" {
		return "deploy/" + ns + "/" + dep
	}
	if app := a.Labels["app"]; app != "" && ns != "" {
		return "svc/" + ns + "/" + app
	}
	if svc := a.Labels["service"]; svc != "" && ns != "" {
		return "svc/" + ns + "/" + svc
	}
	if ns != "" {
		return "ns/" + ns
	}
	return "cluster/default"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Ensure Collector satisfies the sources.Collector interface.
var _ sources.Collector = (*Collector)(nil)
