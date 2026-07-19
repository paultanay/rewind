// Package loki implements the Rewind collector for Grafana Loki (or any
// LogQL-compatible log aggregation system).
//
// Design constraints (spec §8):
//   - Do NOT pull raw logs wholesale. Query only count_over_time of
//     error-level patterns to detect LogBurst events.
//   - Fetch at most maxSampleLines (default 20) sample log lines around
//     each burst for the timeline Detail.
//   - SourceRef deep-links back to Loki/Grafana for the full log view.
//   - Read-only; honors context cancellation and 15s source timeout.
package loki

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/paultanay/rewind/internal/model"
	"github.com/paultanay/rewind/internal/sources"
)

const (
	// maxSampleLines is the maximum number of log lines to fetch per burst.
	maxSampleLines = 20
	// burstThresholdMultiplier: a log error rate is a "burst" if it exceeds
	// the baseline by this factor (default 3×). Too low → false positives;
	// too high → misses real bursts.
	burstThresholdMultiplier = 3.0
	// minBurstRate is the absolute minimum error log rate to report (errors/s).
	// Prevents false positives on very low-traffic services.
	minBurstRate = 0.1
)

// Config holds all Loki connection settings. Populated from rewind.yaml.
type Config struct {
	// URL is the base URL of the Loki instance, e.g. "http://loki.monitoring:3100".
	URL string `yaml:"url" json:"url"`
	// TenantID is the X-Scope-OrgID header value for multi-tenant Loki.
	// Leave empty for single-tenant deployments.
	TenantID string `yaml:"tenantId" json:"tenantId,omitempty"`
	// Username and Password for HTTP basic auth (e.g. Grafana Cloud).
	Username string `yaml:"username" json:"username,omitempty"`
	Password string `yaml:"password" json:"password,omitempty"`
	// MaxSampleLines overrides the default 20 sample lines per burst.
	MaxSampleLines int `yaml:"maxSampleLines" json:"maxSampleLines,omitempty"`
	// GrafanaBaseURL is used to construct deep-link URLs into Grafana's Explore.
	// Optional. E.g. "https://grafana.example.com"
	GrafanaBaseURL string `yaml:"grafanaBaseUrl" json:"grafanaBaseUrl,omitempty"`
}

// Collector implements sources.Collector for Loki.
type Collector struct {
	cfg    Config
	client *http.Client
	// version is injected for the User-Agent header.
	version string
}

// New creates a Loki collector from config.
func New(cfg Config, version string) *Collector {
	return &Collector{
		cfg:     cfg,
		version: version,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// Name implements sources.Collector.
func (c *Collector) Name() string { return "loki" }

// Check implements sources.Collector — verifies the Loki endpoint is reachable.
func (c *Collector) Check(ctx context.Context) error {
	if c.cfg.URL == "" {
		return fmt.Errorf("loki.url not configured")
	}
	reqURL := strings.TrimRight(c.cfg.URL, "/") + "/loki/api/v1/labels"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return err
	}
	c.setHeaders(req)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("loki connectivity check: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("loki returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// Collect queries Loki for log error rates and burst events in the given window.
func (c *Collector) Collect(
	ctx context.Context,
	scope model.Scope,
	window model.TimeRange,
) (sources.CollectResult, error) {
	if c.cfg.URL == "" {
		return sources.CollectResult{}, fmt.Errorf("loki.url not configured")
	}

	type target struct{ ns, svc string }
	var targets []target
	for _, ns := range scope.Namespaces {
		if len(scope.Services) > 0 {
			for _, svc := range scope.Services {
				targets = append(targets, target{ns, svc})
			}
		} else {
			targets = append(targets, target{ns, ""})
		}
	}
	if len(targets) == 0 {
		return sources.CollectResult{}, fmt.Errorf("no namespaces in scope")
	}

	sampleLines := c.cfg.MaxSampleLines
	if sampleLines <= 0 {
		sampleLines = maxSampleLines
	}

	var result sources.CollectResult
	for _, t := range targets {
		evs, sig, err := c.collectForTarget(ctx, t.ns, t.svc, window, sampleLines)
		if err != nil {
			continue
		}
		result.Events = append(result.Events, evs...)
		if sig != nil {
			result.Signals = append(result.Signals, *sig)
		}
	}
	return result, nil
}

// collectForTarget queries error log rates for one (namespace, service) pair.
func (c *Collector) collectForTarget(
	ctx context.Context,
	ns, svc string,
	window model.TimeRange,
	sampleLines int,
) ([]model.Event, *model.Signal, error) {
	// Build a LogQL stream selector.
	// Standard label conventions: namespace=, app= (or job=, service_name=).
	streamSel := buildStreamSelector(ns, svc)

	// Query: error rate as a metric (count_over_time of error-level lines).
	// Step: 1m to keep signal ≤ 500 points (window max ~8h → 480 steps).
	step := chooseStep(window)
	errorRate, err := c.queryRange(ctx, window, step,
		fmt.Sprintf(`sum(rate({%s} |~ "(?i)(error|ERROR|FATAL|fatal|panic|PANIC)" [1m]))`, streamSel))
	if err != nil {
		return nil, nil, fmt.Errorf("loki rate query: %w", err)
	}

	entityID := entityIDFor(ns, svc)

	var points []model.Point
	for _, pt := range errorRate {
		points = append(points, model.Point{T: pt.T, V: pt.V})
	}
	if len(points) == 0 {
		return nil, nil, nil
	}

	sig := &model.Signal{
		ID:       "loki-" + entityID,
		EntityID: entityID,
		Metric:   model.MetricLogErrorRate,
		Unit:     "errors/s",
		Points:   points,
	}

	// Detect bursts: find windows where rate exceeds baseline × threshold.
	events := c.detectBursts(ctx, ns, svc, entityID, streamSel, window, points, sampleLines)

	return events, sig, nil
}

// detectBursts finds periods where the error rate significantly exceeds the
// baseline and synthesises LogBurst events. For each burst it fetches sample
// lines for the Detail field.
func (c *Collector) detectBursts(
	ctx context.Context,
	ns, svc, entityID, streamSel string,
	window model.TimeRange,
	points []model.Point,
	sampleLines int,
) []model.Event {
	if len(points) == 0 {
		return nil
	}

	// Compute baseline rate as the median of the first 25% of points.
	baseline := medianRate(points[:max(1, len(points)/4)])

	var events []model.Event
	inBurst := false
	var burstStart time.Time

	for i, pt := range points {
		threshold := maxFloat64(minBurstRate, baseline*burstThresholdMultiplier)
		isBursting := pt.V >= threshold

		if isBursting && !inBurst {
			inBurst = true
			burstStart = pt.T
		} else if !isBursting && inBurst {
			inBurst = false
			// Burst ended at points[i-1].
			burstAt := burstStart
			peakVal := maxInRange(points, burstStart, points[i-1].T)

			sampleDetail := c.fetchSamples(ctx, streamSel, burstAt, sampleLines)
			deepLink := c.grafanaExploreURL(ns, svc, burstAt)

			ev := model.Event{
				ID:       fmt.Sprintf("loki-burst-%s-%d", entityID, burstAt.Unix()),
				At:       burstAt,
				Kind:     model.EventKindLogBurst,
				EntityID: entityID,
				Severity: model.SeverityNotable,
				Title:    fmt.Sprintf("Log error burst on %s (%.2f errors/s peak)", shortName(ns, svc), peakVal),
				Detail:   sampleDetail,
				SourceRef: model.SourceRef{
					SourceName: "loki",
					URL:        deepLink,
				},
			}
			events = append(events, ev)
		}
		_ = i
	}
	// Handle burst that extends to end of window.
	if inBurst {
		burstAt := burstStart
		peakVal := maxInRange(points, burstStart, points[len(points)-1].T)
		sampleDetail := c.fetchSamples(ctx, streamSel, burstAt, sampleLines)
		deepLink := c.grafanaExploreURL(ns, svc, burstAt)
		events = append(events, model.Event{
			ID:        fmt.Sprintf("loki-burst-%s-%d", entityID, burstAt.Unix()),
			At:        burstAt,
			Kind:      model.EventKindLogBurst,
			EntityID:  entityID,
			Severity:  model.SeverityNotable,
			Title:     fmt.Sprintf("Log error burst on %s (%.2f errors/s peak)", shortName(ns, svc), peakVal),
			Detail:    sampleDetail,
			SourceRef: model.SourceRef{SourceName: "loki", URL: deepLink},
		})
	}
	return events
}

// fetchSamples retrieves up to n sample log lines around the given timestamp.
// On error it returns an empty string — the event is still useful without lines.
func (c *Collector) fetchSamples(ctx context.Context, streamSel string, at time.Time, n int) string {
	start := at.Add(-2 * time.Minute)
	end := at.Add(2 * time.Minute)

	reqURL := strings.TrimRight(c.cfg.URL, "/") + "/loki/api/v1/query_range"
	params := url.Values{}
	params.Set("query", fmt.Sprintf(`{%s} |~ "(?i)(error|fatal|panic)"`, streamSel))
	params.Set("start", fmt.Sprintf("%d", start.UnixNano()))
	params.Set("end", fmt.Sprintf("%d", end.UnixNano()))
	params.Set("limit", fmt.Sprintf("%d", n))
	params.Set("direction", "forward")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL+"?"+params.Encode(), nil)
	if err != nil {
		return ""
	}
	c.setHeaders(req)

	resp, err := c.client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return ""
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return ""
	}

	var qr queryRangeResponse
	if err := json.Unmarshal(body, &qr); err != nil {
		return ""
	}

	var lines []string
	for _, stream := range qr.Data.Result {
		for _, val := range stream.Values {
			if len(val) >= 2 {
				lines = append(lines, val[1])
			}
		}
	}
	// Sort by timestamp (values are [timestamp_ns_str, line]).
	sort.Strings(lines)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// queryRange executes a Loki metric query and returns sampled points.
func (c *Collector) queryRange(ctx context.Context, window model.TimeRange, step time.Duration, query string) ([]model.Point, error) {
	reqURL := strings.TrimRight(c.cfg.URL, "/") + "/loki/api/v1/query_range"
	params := url.Values{}
	params.Set("query", query)
	params.Set("start", fmt.Sprintf("%d", window.From.UnixNano()))
	params.Set("end", fmt.Sprintf("%d", window.To.UnixNano()))
	params.Set("step", fmt.Sprintf("%.0fs", step.Seconds()))
	params.Set("limit", "5000")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)

	// Retry once on transient errors.
	var body []byte
	for attempt := 0; attempt < 2; attempt++ {
		resp, err := c.client.Do(req)
		if err != nil {
			if attempt == 0 {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			return nil, err
		}
		body, err = io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusOK {
			break
		}
		if attempt == 0 {
			time.Sleep(500 * time.Millisecond)
		}
	}

	var qr queryRangeResponse
	if err := json.Unmarshal(body, &qr); err != nil {
		return nil, err
	}

	// For metric queries, result type is "matrix".
	var pts []model.Point
	for _, stream := range qr.Data.Result {
		for _, val := range stream.Values {
			if len(val) < 2 {
				continue
			}
			var tsNs int64
			var v float64
			if _, err := fmt.Sscanf(val[0], "%d", &tsNs); err != nil {
				continue
			}
			if _, err := fmt.Sscanf(val[1], "%f", &v); err != nil {
				continue
			}
			pts = append(pts, model.Point{
				T: time.Unix(0, tsNs),
				V: v,
			})
		}
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].T.Before(pts[j].T) })
	return pts, nil
}

func (c *Collector) setHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "rewind/"+c.version)
	if c.cfg.TenantID != "" {
		req.Header.Set("X-Scope-OrgID", c.cfg.TenantID)
	}
	if c.cfg.Username != "" {
		req.SetBasicAuth(c.cfg.Username, c.cfg.Password)
	}
}

// grafanaExploreURL builds a Grafana Explore deep-link for the error logs.
// Returns empty string if GrafanaBaseURL is not configured.
func (c *Collector) grafanaExploreURL(ns, svc string, at time.Time) string {
	if c.cfg.GrafanaBaseURL == "" {
		return c.cfg.URL // fall back to raw Loki URL
	}
	from := at.Add(-5 * time.Minute).UnixMilli()
	to := at.Add(10 * time.Minute).UnixMilli()
	sel := buildStreamSelector(ns, svc)
	return fmt.Sprintf(`%s/explore?orgId=1&left={"datasource":"loki","queries":[{"expr":"{%s}"}],"range":{"from":"%d","to":"%d"}}`,
		strings.TrimRight(c.cfg.GrafanaBaseURL, "/"), sel, from, to)
}

// ─── JSON response types ──────────────────────────────────────────────────────

type queryRangeResponse struct {
	Data struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Stream map[string]string `json:"stream"`
			Values [][]string        `json:"values"` // [timestamp_ns, value_or_line]
		} `json:"result"`
	} `json:"data"`
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func buildStreamSelector(ns, svc string) string {
	if svc != "" {
		return fmt.Sprintf(`namespace=%q, app=%q`, ns, svc)
	}
	return fmt.Sprintf(`namespace=%q`, ns)
}

func entityIDFor(ns, svc string) string {
	if svc != "" {
		return "svc/" + ns + "/" + svc
	}
	return "ns/" + ns
}

func shortName(ns, svc string) string {
	if svc != "" {
		return svc
	}
	return ns
}

func chooseStep(window model.TimeRange) time.Duration {
	const maxPoints = 500
	dur := window.Duration()
	step := dur / maxPoints
	if step < time.Minute {
		step = time.Minute
	}
	return step
}

func medianRate(pts []model.Point) float64 {
	if len(pts) == 0 {
		return 0
	}
	vals := make([]float64, len(pts))
	for i, p := range pts {
		vals[i] = p.V
	}
	sort.Float64s(vals)
	return vals[len(vals)/2]
}

func maxInRange(pts []model.Point, from, to time.Time) float64 {
	var m float64
	for _, p := range pts {
		if !p.T.Before(from) && !p.T.After(to) && p.V > m {
			m = p.V
		}
	}
	return m
}

func maxFloat64(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// Ensure Collector satisfies the sources.Collector interface.
var _ sources.Collector = (*Collector)(nil)
