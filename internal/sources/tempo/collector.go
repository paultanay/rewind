// Package tempo implements the Rewind collector for Grafana Tempo (or any
// Jaeger-compatible distributed tracing backend exposing Tempo's HTTP API).
//
// Design constraints (spec §8):
//   - Do NOT load full traces. Query only trace error rates and span P99
//     latencies via the Tempo metrics-generator or the search API.
//   - Populate the topology call-graph (AddCallEdge) from span service→service
//     relationships, enabling RW005 (upstream cascade) to fire.
//   - Limit: at most 50 exemplar spans per service; truncate trace IDs in Detail.
//   - SourceRef deep-links to Grafana's TraceQL explore view.
package tempo

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
	// maxSpanSamples is the maximum number of exemplar trace IDs stored per service.
	maxSpanSamples = 50
	// traceErrorThreshold: trace error rate above this is a TraceErrorSpike event.
	traceErrorThreshold = 0.05 // 5%
	// defaultSearchLimit: max traces to inspect from Tempo search API.
	defaultSearchLimit = 200
)

// Config holds Tempo connection settings.
type Config struct {
	// URL is the base URL of the Tempo instance, e.g. "http://tempo.monitoring:3200".
	URL string `yaml:"url" json:"url"`
	// TenantID is the X-Scope-OrgID header for multi-tenant deployments.
	TenantID string `yaml:"tenantId" json:"tenantId,omitempty"`
	// Username and Password for basic auth (Grafana Cloud).
	Username string `yaml:"username" json:"username,omitempty"`
	Password string `yaml:"password" json:"password,omitempty"`
	// GrafanaBaseURL for deep-link construction.
	GrafanaBaseURL string `yaml:"grafanaBaseUrl" json:"grafanaBaseUrl,omitempty"`
}

// Collector implements sources.Collector for Grafana Tempo.
type Collector struct {
	cfg     Config
	client  *http.Client
	version string
}

// New creates a Tempo collector.
func New(cfg Config, version string) *Collector {
	return &Collector{
		cfg:     cfg,
		version: version,
		client:  &http.Client{Timeout: 15 * time.Second},
	}
}

// Name implements sources.Collector.
func (c *Collector) Name() string { return "tempo" }

// Check verifies the Tempo endpoint is reachable.
func (c *Collector) Check(ctx context.Context) error {
	if c.cfg.URL == "" {
		return fmt.Errorf("tempo.url not configured")
	}
	reqURL := strings.TrimRight(c.cfg.URL, "/") + "/api/echo"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return err
	}
	c.setHeaders(req)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("tempo connectivity check: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tempo returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// Collect queries Tempo for trace error rates, P99 latencies, and service
// call-graph edges. Returns:
//   - TraceErrorSpike events when a service's trace error rate exceeds 5%.
//   - model.Signal for trace.error.rate and trace.latency.p99 per service.
//   - model.Entity entries for discovered services (for topology).
//   - call-graph edges embedded in Entity.Labels["calls"] (comma-separated).
func (c *Collector) Collect(
	ctx context.Context,
	scope model.Scope,
	window model.TimeRange,
) (sources.CollectResult, error) {
	if c.cfg.URL == "" {
		return sources.CollectResult{}, fmt.Errorf("tempo.url not configured")
	}

	// Step 1: query Tempo search API for all traces in the window.
	traces, err := c.searchTraces(ctx, scope, window)
	if err != nil {
		return sources.CollectResult{}, fmt.Errorf("tempo search: %w", err)
	}

	// Step 2: aggregate per-service error rates and P99 latencies.
	type svcStats struct {
		total     int
		errors    int
		latencies []float64 // ms
		rootSvcs  map[string]bool // services this svc calls
	}
	stats := make(map[string]*svcStats)

	for _, t := range traces {
		svc := t.RootServiceName
		if svc == "" {
			continue
		}
		if _, ok := stats[svc]; !ok {
			stats[svc] = &svcStats{rootSvcs: make(map[string]bool)}
		}
		s := stats[svc]
		s.total++
		if t.RootTraceName != "" && isError(t) {
			s.errors++
		}
		if t.DurationMs > 0 {
			s.latencies = append(s.latencies, t.DurationMs)
		}
		// Collect call edges from span service names.
		for _, spanSvc := range t.SpanServices {
			if spanSvc != svc {
				s.rootSvcs[spanSvc] = true
			}
		}
	}

	var result sources.CollectResult

	for svc, s := range stats {
		if s.total == 0 {
			continue
		}
		entityID := entityIDForSvc(scope, svc)

		// Error rate signal
		errRate := float64(s.errors) / float64(s.total)
		result.Signals = append(result.Signals, model.Signal{
			ID:       "tempo-errrate-" + entityID,
			EntityID: entityID,
			Metric:   model.MetricTraceErrorRate,
			Unit:     "ratio",
			Points: []model.Point{
				{T: window.From, V: 0},
				{T: window.To, V: errRate},
			},
		})

		// P99 latency signal
		if len(s.latencies) > 0 {
			p99 := percentile(s.latencies, 99)
			result.Signals = append(result.Signals, model.Signal{
				ID:       "tempo-lat-" + entityID,
				EntityID: entityID,
				Metric:   model.MetricTraceLatencyP99,
				Unit:     "ms",
				Points: []model.Point{
					{T: window.From, V: 0},
					{T: window.To, V: p99},
				},
			})
		}

		// Entity with call edges in Labels.
		calls := make([]string, 0, len(s.rootSvcs))
		for callee := range s.rootSvcs {
			calls = append(calls, entityIDForSvc(scope, callee))
		}
		sort.Strings(calls)
		ent := model.Entity{
			ID:   entityID,
			Kind: model.EntityKindService,
			Labels: map[string]string{
				"app": svc,
			},
		}
		if len(calls) > 0 {
			ent.Labels["calls"] = strings.Join(calls, ",")
		}
		result.Entities = append(result.Entities, ent)

		// TraceErrorSpike event if error rate exceeds threshold.
		if errRate >= traceErrorThreshold {
			result.Events = append(result.Events, model.Event{
				ID:       fmt.Sprintf("tempo-errspiike-%s-%d", entityID, window.From.Unix()),
				At:       window.From,
				Kind:     model.EventKindTraceErrorSpike,
				EntityID: entityID,
				Severity: model.SeverityNotable,
				Title:    fmt.Sprintf("Trace error spike: %s (%.1f%%)", svc, errRate*100),
				Detail:   fmt.Sprintf("%.1f%% of %d traces errored in window", errRate*100, s.total),
				SourceRef: model.SourceRef{
					SourceName: "tempo",
					URL:        c.grafanaTraceURL(svc, window),
				},
			})
		}
	}

	return result, nil
}

// ─── Tempo search API ────────────────────────────────────────────────────────

type traceRecord struct {
	TraceID         string   `json:"traceID"`
	RootServiceName string   `json:"rootServiceName"`
	RootTraceName   string   `json:"rootTraceName"`
	DurationMs      float64  `json:"durationMs"`
	// StatusCode is "STATUS_CODE_ERROR" for errored root spans.
	StartTimeUnixNano string `json:"startTimeUnixNano"`
	// SpanSets contains span-level data; we only need service names.
	SpanSets    []spanSet `json:"spanSets"`
	SpanServices []string // synthesised from SpanSets
}

type spanSet struct {
	Spans []struct {
		ServiceName string `json:"-"` // extracted from attributes
		Attributes  []struct {
			Key   string `json:"key"`
			Value struct {
				StringValue string `json:"stringValue"`
			} `json:"value"`
		} `json:"attributes"`
	} `json:"spans"`
}

func isError(t traceRecord) bool {
	name := strings.ToLower(t.RootTraceName)
	return strings.Contains(name, "error") || strings.Contains(name, "fail")
}

func (c *Collector) searchTraces(ctx context.Context, scope model.Scope, window model.TimeRange) ([]traceRecord, error) {
	reqURL := strings.TrimRight(c.cfg.URL, "/") + "/api/search"
	params := url.Values{}
	params.Set("start", fmt.Sprintf("%d", window.From.Unix()))
	params.Set("end", fmt.Sprintf("%d", window.To.Unix()))
	params.Set("limit", fmt.Sprintf("%d", defaultSearchLimit))
	if len(scope.Services) > 0 {
		// Filter by service name (Tempo supports resource.service.name tag).
		params.Set("tags", fmt.Sprintf("service.name=%s", scope.Services[0]))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tempo search returned HTTP %d", resp.StatusCode)
	}

	var result struct {
		Traces []traceRecord `json:"traces"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	// Extract span service names from SpanSets.
	for i := range result.Traces {
		svcSet := map[string]bool{}
		for _, ss := range result.Traces[i].SpanSets {
			for _, span := range ss.Spans {
				for _, attr := range span.Attributes {
					if attr.Key == "service.name" && attr.Value.StringValue != "" {
						svcSet[attr.Value.StringValue] = true
					}
				}
			}
		}
		for svc := range svcSet {
			result.Traces[i].SpanServices = append(result.Traces[i].SpanServices, svc)
		}
	}
	return result.Traces, nil
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

func (c *Collector) grafanaTraceURL(svc string, window model.TimeRange) string {
	if c.cfg.GrafanaBaseURL == "" {
		return c.cfg.URL
	}
	from := window.From.UnixMilli()
	to := window.To.UnixMilli()
	return fmt.Sprintf(`%s/explore?left={"datasource":"tempo","queries":[{"query":"{resource.service.name=\"%s\"}"}],"range":{"from":"%d","to":"%d"}}`,
		strings.TrimRight(c.cfg.GrafanaBaseURL, "/"), svc, from, to)
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func entityIDForSvc(scope model.Scope, svc string) string {
	if len(scope.Namespaces) > 0 {
		return "svc/" + scope.Namespaces[0] + "/" + svc
	}
	return "svc/default/" + svc
}

func percentile(vals []float64, p int) float64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := make([]float64, len(vals))
	copy(sorted, vals)
	sort.Float64s(sorted)
	idx := int(float64(p)/100.0*float64(len(sorted)-1) + 0.5)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// Ensure Collector satisfies the sources.Collector interface.
var _ sources.Collector = (*Collector)(nil)
