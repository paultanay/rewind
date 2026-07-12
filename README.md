# ⏪ Rewind — Incident Replay Engine

> **Reconstruct a production incident as a single, scrubbable timeline.**
> Deployments, metric anomalies, log error bursts, Kubernetes events, and traces — all correlated and causally ranked in one command.

[![Go 1.22+](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go)](https://go.dev)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue)](LICENSE)
[![Build](https://img.shields.io/badge/build-passing-brightgreen)](../../actions)

---

## Quick start (5 minutes)

```bash
# 1. Install
go install github.com/rewind-io/rewind/cmd/rewind@latest

# 2. Run the offline demo (no cluster required)
rewind demo

# 3. Open the web UI for the demo
rewind demo --ui

# 4. Investigate a real incident
rewind investigate \
  --from 14:00 --to 14:45 \
  --namespace shop \
  --config rewind.yaml

# 5. Save and replay
rewind investigate --from -1h --namespace shop -o incident.rewind
rewind ui incident.rewind
```

`rewind demo` produces output like this:

```
────────────────────────────────────────────────────────────────
  VERDICT
────────────────────────────────────────────────────────────────
► [1] confidence: HIGH  rules: RW001
    trigger : Deployed checkout v2.3.1
    reason  : Deploy at 14:05 → latency.p99 ×6.6 at 14:07 → error.rate ×19.9 at 14:13
    chain:
      → Deploy event: Deployed checkout v2.3.1
      → 6.6× latency.p99 change-point 2m after deploy
      → 19.9× error.rate change-point 8m after deploy
```

---

## What it does

Rewind pulls from your existing observability stack — no agents, no sidecars, no schema changes — and applies a **deterministic rule engine** (RW001–RW010) to produce a ranked verdict:

| Source | What it collects |
|--------|-----------------|
| **Prometheus** | Latency, error rate, CPU, memory, restarts — change-point detection |
| **Kubernetes** | Deploys, OOMKills, probe failures, node pressure, evictions |
| **GitHub / GitLab** | CI/CD pipeline runs, merge commit timestamps |
| **Loki** | Error log burst detection (count_over_time, not raw logs) |
| **Grafana Tempo** | Trace error rates, P99 latency, service call-graph topology |
| **Alertmanager** | Alert fingerprints as corroborating evidence (never triggers) |

---

## Demo scenarios

```bash
rewind demo --scenario bad-deploy      # bad deploy → latency + error rate spike (HIGH confidence)
rewind demo --scenario oom-cascade     # OOMKill → crash loop → downstream cascade
rewind demo --scenario node-pressure   # node memory pressure → pod eviction
rewind demo --scenario cpu-throttle    # CPU throttling → p99 latency degradation
rewind demo --scenario false-positive  # noisy alerts, no real trigger (no HIGH verdict)
```

---

## Correlation rules

```bash
rewind explain            # list all rules
rewind explain RW001      # deploy → metric change-point
rewind explain RW009      # crash loop detection (event coalescing)
rewind explain RW010      # alert corroboration — alerts are symptoms, never triggers
```

| Rule | Name | When it fires |
|------|------|---------------|
| RW001 | Deploy → Metric CP | Deploy followed by change-point within 10m |
| RW002 | Config Change → Metric CP | Config change followed by change-point within 15m |
| RW003 | OOMKill Evidence | memory.usage ↑ → OOMKill → Restart chain |
| RW004 | CPU Saturation → Latency | CPU throttle CP precedes latency CP by ≤5m |
| RW005 | Upstream Cascade | Upstream error CP precedes downstream CP + call-graph path |
| RW006 | Node Pressure → Eviction | NodePressure → PodKilled within 5m |
| RW007 | Probe Failure → Restart | ProbeFailed → Restart within 3m |
| RW008 | Log Burst Correlation | LogBurst ↔ error.rate CP within 5m (corroboration) |
| RW009 | CrashLoop Detection | ≥3 restarts in 10m → synthetic CrashLoop event |
| RW010 | Alert Corroboration | AlertFired adds evidence, **never** creates a trigger |

---

## Configuration

Create `rewind.yaml` in your working directory:

```yaml
prometheus:
  url: http://prometheus.monitoring:9090

kubernetes:
  kubeconfig: ~/.kube/config   # optional, uses in-cluster config if empty
  context: my-cluster

loki:
  url: http://loki.monitoring:3100
  tenant_id: ""                # X-Scope-OrgID for multi-tenant
  grafana_base_url: https://grafana.example.com

tempo:
  url: http://tempo.monitoring:3200
  grafana_base_url: https://grafana.example.com

alertmanager:
  url: http://alertmanager.monitoring:9093

github:
  token: ${GITHUB_TOKEN}       # or REWIND_GITHUB_TOKEN env var
  repos:
    - my-org/checkout
    - my-org/payments

source_timeout: 15s
```

All fields support `REWIND_*` environment variable overrides (e.g. `REWIND_PROMETHEUS_URL`).

---

## CLI reference

```
rewind investigate --from <t> --to <t> [--namespace ns] [--service svc]
                   [--format term|md|json] [-o incident.rewind] [--replay bundle]
rewind ui          [incident.rewind] [--port 7750]
rewind demo        [--scenario bad-deploy|oom-cascade|node-pressure|cpu-throttle|false-positive]
                   [--ui] [--port 7750]
rewind sources     # connectivity + capability check per configured source
rewind explain     [RULE_ID]
rewind export      incident.rewind
rewind import      incident.rewind
rewind version
```

**Time formats:** RFC3339 (`2026-07-09T14:00:00Z`), `14:00` (today local), `-45m` (relative).

**Exit codes:** `0` = ok, `1` = Critical findings (for CI gating), `2` = usage error, `3` = all sources failed, `4` = internal error.

---

## Bundle format

Rewind exports portable `.rewind` bundles (zip + JSON) that replay fully offline:

```bash
# Save
rewind investigate --from -1h --namespace shop -o incident.rewind

# Replay (re-runs analysis engine on same raw data)
rewind investigate --replay incident.rewind

# Open in web UI
rewind ui incident.rewind
```

Bundles are forward-compatible: unknown fields are preserved; `schemaVersion` is validated.

---

## Architecture

```
Sources (parallel, read-only, 15s timeout each)
  Prometheus ── Kubernetes ── GitHub/GitLab ── Loki ── Tempo ── Alertmanager
       │
       ▼
   model.Incident  (Events + Signals + Entities)
       │
       ▼
   analyze.RunFull
   ├── changepoint.Detect     (CUSUM + PELT on every signal)
   ├── topology.Build         (entity graph from K8s + Tempo traces)
   └── correlate.Run          (RW001–RW010 deterministic rule engine)
       │
       ▼
   model.Verdict   (ranked hypotheses with confidence + causal chain)
       │
   ┌───┴────┐
   │terminal│  markdown  │  JSON  │  web UI (127.0.0.1:7750)
   └────────┘
```

See [`docs/architecture.md`](docs/architecture.md) for full detail.

---

## Development

```bash
# Build
go build ./cmd/rewind

# Test (all packages, race detector)
go test -race ./...

# Lint
golangci-lint run

# Run demo
go run ./cmd/rewind demo

# Run demo with web UI
go run ./cmd/rewind demo --ui
```

### Project structure

```
cmd/rewind/         # main entrypoint
internal/
  model/            # core types (Incident, Event, Signal, Verdict)
  analyze/          # analysis pipeline
    changepoint/    # CUSUM + PELT detectors
    correlate/      # RW001–RW010 rule engine
    topology/       # entity graph BFS utilities
  sources/          # source collectors
    prometheus/     kubernetes/  loki/  tempo/  alertmanager/  cicd/
  bundle/           # .rewind bundle export/import
  cli/              # cobra commands
  server/           # embedded web UI HTTP server
  render/
    terminal/       # ANSI timeline renderer
    markdown/       # markdown report renderer
testdata/           # golden incident fixtures
docs/               # architecture, config reference, rule pages
```

---

## Non-goals

Rewind is intentionally **not**:
- A log storage system (use Loki/Elasticsearch)
- A metrics database (use Prometheus/Thanos)
- A real-time alerting tool (use Alertmanager)
- An ML-based root cause analysis system (deterministic rules only)
- A hosted/cloud service (single binary, runs wherever Go runs)

---

## License

Apache 2.0 — see [LICENSE](LICENSE).
