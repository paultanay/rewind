# Source: Prometheus

Rewind uses the Prometheus HTTP API (v1) in **read-only** mode — it only issues
`/api/v1/query_range` and `/api/v1/query` calls. It is safe to run against
production Prometheus instances.

**API-compatible backends:** Thanos, Mimir, VictoriaMetrics, Grafana Mimir —
any endpoint that exposes the standard Prometheus HTTP API.

---

## Configuration

```yaml
# rewind.yaml
prometheus:
  url: http://prometheus.monitoring.svc:9090
  # Optional: HTTP Basic Auth
  username: ""
  password: ""
  # Optional: Bearer token (overrides username/password)
  token: ""
  # TLS (set to true for self-signed certs in dev; never in production)
  insecure_skip_verify: false
  # Per-source query timeout (overrides global source_timeout)
  timeout: 30s
```

Environment variable overrides:
| Variable | Overrides |
|---|---|
| `REWIND_PROMETHEUS_URL` | `prometheus.url` |
| `REWIND_PROMETHEUS_TOKEN` | `prometheus.token` |

---

## Collected metrics

| Rewind metric constant | PromQL query (simplified) | Unit |
|---|---|---|
| `latency.p99` | `histogram_quantile(0.99, rate(http_request_duration_seconds_bucket[5m]))` | seconds |
| `latency.p50` | `histogram_quantile(0.50, ...)` | seconds |
| `error.rate` | `rate(http_requests_total{code=~"5.."}[5m]) / rate(http_requests_total[5m])` | ratio |
| `throughput` | `rate(http_requests_total[5m])` | req/s |
| `cpu.throttle` | `rate(container_cpu_cfs_throttled_seconds_total[5m]) / rate(container_cpu_cfs_periods_total[5m])` | ratio |
| `memory.usage` | `container_memory_working_set_bytes / container_spec_memory_limit_bytes` | ratio |
| `restarts` | `kube_pod_container_status_restarts_total` | count |
| `queue.lag` | `kafka_consumer_group_lag` (if present) | messages |

Metrics are downsampled to at most 360 points per signal (spec §8).

---

## Connectivity check

```bash
rewind sources
```

Output example:
```
✓ prometheus    reachable  (http://localhost:9090)
```

If unreachable, the error message includes the exact HTTP error or dial failure.
Rewind degrades gracefully — a Prometheus failure reduces richness but does not
abort the investigation.

---

## Change-point detection

Prometheus signals are processed by two detectors (spec §9):

1. **Baseline deviation** (median + MAD, k=5, minRun=3) — detects sustained
   excursions from the pre-incident baseline.
2. **PELT** (Pruned Exact Linear Time, normal-mean cost) — detects abrupt
   mean shifts within the window.

Results are merged; nearby change-points collapse to the strongest score.
At most 5 change-points per signal are retained.

---

## Correlation rules that use Prometheus signals

| Rule | Metric used |
|---|---|
| RW001 | latency.p99, error.rate (change-point after deploy) |
| RW003 | memory.usage (spike before OOMKill) |
| RW004 | cpu.throttle (sustained high) |
| RW006 | error.rate, latency.p99 (after node pressure event) |
| RW007 | queue.lag (before consumer latency) |
| RW009 | restarts (crash-loop pattern) |
