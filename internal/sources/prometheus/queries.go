// Package prometheus implements the Prometheus (and API-compatible: Thanos,
// Mimir, VictoriaMetrics) collector for Rewind.
//
// Design decisions from spec §8:
//   - Queries a curated default query set (RED + USE per entity kind).
//   - Step is chosen so each series ≤ 500 points.
//   - Fetches a baseline window (same duration immediately before the incident
//     window, plus same window 24h earlier when available).
//   - Counter resets are handled by rate() queries, not raw counter values.
//   - Custom queries can be added via config (not yet wired, reserved field).
package prometheus

import "github.com/paultanay/rewind/internal/model"

// QueryTemplate defines one Prometheus query to execute per entity.
// The Metric field is the canonical model metric name the result maps to.
type QueryTemplate struct {
	// Metric is the canonical model.Metric* constant this query maps to.
	Metric string
	// PromQL is the query string. {{.Namespace}} and {{.Service}} are
	// substituted before execution.
	PromQL string
	// Unit is the display unit for the signal (ms, %, req/s, …).
	Unit string
	// EntityKinds restricts which entity kinds this query applies to.
	// Empty means all kinds.
	EntityKinds []model.EntityKind
}

// defaultQueries is the curated default query set. Every query uses rate()
// or histogram functions so counter resets are handled by Prometheus itself.
//
// Naming conventions:
//   - {namespace} substituted from scope
//   - {service}   substituted per-service
//   - Queries are written to work with standard instrumentation:
//     kube-state-metrics, kube-prometheus-stack, and OpenTelemetry Collector.
var defaultQueries = []QueryTemplate{
	// ── RED: Rate ──────────────────────────────────────────────────────────
	{
		Metric: model.MetricRequestRate,
		PromQL: `sum(rate(http_requests_total{namespace="{namespace}",service="{service}"}[2m]))`,
		Unit:   "req/s",
	},

	// ── RED: Errors ────────────────────────────────────────────────────────
	{
		Metric: model.MetricErrorRate,
		PromQL: `sum(rate(http_requests_total{namespace="{namespace}",service="{service}",code=~"5.."}[2m])) /` +
			` sum(rate(http_requests_total{namespace="{namespace}",service="{service}"}[2m]))`,
		Unit: "ratio",
	},

	// ── RED: Duration (p50, p95, p99) ─────────────────────────────────────
	{
		Metric: model.MetricLatencyP50,
		PromQL: `histogram_quantile(0.50, sum by(le) (rate(` +
			`http_request_duration_seconds_bucket{namespace="{namespace}",service="{service}"}[2m]))) * 1000`,
		Unit: "ms",
	},
	{
		Metric: model.MetricLatencyP95,
		PromQL: `histogram_quantile(0.95, sum by(le) (rate(` +
			`http_request_duration_seconds_bucket{namespace="{namespace}",service="{service}"}[2m]))) * 1000`,
		Unit: "ms",
	},
	{
		Metric: model.MetricLatencyP99,
		PromQL: `histogram_quantile(0.99, sum by(le) (rate(` +
			`http_request_duration_seconds_bucket{namespace="{namespace}",service="{service}"}[2m]))) * 1000`,
		Unit: "ms",
	},

	// ── USE: CPU utilisation ───────────────────────────────────────────────
	{
		Metric: model.MetricCPUUsage,
		PromQL: `sum(rate(container_cpu_usage_seconds_total{` +
			`namespace="{namespace}",container="{service}"}[2m])) /` +
			` sum(kube_pod_container_resource_limits{` +
			`namespace="{namespace}",container="{service}",resource="cpu"}) * 100`,
		Unit: "%",
	},

	// ── USE: CPU throttling ────────────────────────────────────────────────
	{
		Metric: model.MetricCPUThrottle,
		PromQL: `sum(rate(container_cpu_cfs_throttled_seconds_total{` +
			`namespace="{namespace}",container="{service}"}[2m])) /` +
			` sum(rate(container_cpu_cfs_periods_total{` +
			`namespace="{namespace}",container="{service}"}[2m])) * 100`,
		Unit: "%",
	},

	// ── USE: Memory utilisation ────────────────────────────────────────────
	{
		Metric: model.MetricMemoryUsage,
		PromQL: `sum(container_memory_working_set_bytes{` +
			`namespace="{namespace}",container="{service}"}) /` +
			` sum(kube_pod_container_resource_limits{` +
			`namespace="{namespace}",container="{service}",resource="memory"}) * 100`,
		Unit: "%",
	},

	// ── Container restarts ─────────────────────────────────────────────────
	{
		Metric: model.MetricRestarts,
		PromQL: `sum(increase(kube_pod_container_status_restarts_total{` +
			`namespace="{namespace}",container="{service}"}[5m]))`,
		Unit: "count",
	},
}

// nodeQueries are executed for Node entities (USE metrics for nodes).
var nodeQueries = []QueryTemplate{
	{
		Metric:      model.MetricCPUUsage,
		PromQL:      `100 - (avg by (node) (rate(node_cpu_seconds_total{mode="idle",node="{service}"}[2m])) * 100)`,
		Unit:        "%",
		EntityKinds: []model.EntityKind{model.EntityKindNode},
	},
	{
		Metric:      model.MetricMemoryUsage,
		PromQL:      `(1 - (node_memory_MemAvailable_bytes{node="{service}"} / node_memory_MemTotal_bytes{node="{service}"})) * 100`,
		Unit:        "%",
		EntityKinds: []model.EntityKind{model.EntityKindNode},
	},
}
