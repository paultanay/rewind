# rewind — Production Incident Replay Engine

> *Like watching CCTV footage of your system instead of tab-switching between five dashboards.*

[![Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Go 1.23+](https://img.shields.io/badge/go-1.23+-00ADD8.svg)](https://golang.org)

Rewind reconstructs a production incident as a single, scrubbable timeline —
deployments, metric anomalies, log error bursts, Kubernetes events, and traces,
all correlated and causally ranked. It tells you *why* the incident happened,
not just *what* happened.

---

## The demo that gets you running in five minutes

```bash
# Install
go install github.com/rewind-io/rewind/cmd/rewind@latest

# Spin up the demo cluster (requires Docker + kind)
rewind demo

# Or, point it at your own Prometheus + Kubernetes:
rewind investigate --from 14:00 --to 14:45 --namespace shop
```

Expected output:

```
────────────────────────────────────────────────────────────────────────────────
  rewind  │ incident timeline
  Incident  : inc-20260709-142000-a1b2c3d4
  Window    : 2026-07-09 14:00:00 UTC → 14:45:00  (45m)
  Namespace : shop
────────────────────────────────────────────────────────────────────────────────

  Sources
  prometheus       ok        1.1s   0evt 3sig
  kubernetes       ok        0.4s   5evt 0sig
  github           ok        0.3s   1evt 0sig

  Timeline
  TIME      !  TYPE          ENTITY                   DESCRIPTION
  ··············································································
  14:20:00  ◉  DEPLOY        checkout                 Deployed checkout v2.3.1
  14:20:40  ▲  ANOMALY       checkout                 ↑4.2× latency.p99  ▁▁▂▇█  conf:91%
  14:21:15  ●  OOMKILL       checkout-7d9f            OOMKilled: checkout-7d9f
  14:21:18  ●  CRASH-LOOP    checkout-7d9f            Crash loop: 3 restarts in 2m
  14:22:00  ▲  ANOMALY       checkout                 ↑8.1× error.rate  ▁▁▃▇█  conf:88%

════════════════════════════════════════════════════════════════════════════════
  VERDICT
════════════════════════════════════════════════════════════════════════════════
► [1] confidence: HIGH  rules: RW001, RW003
    trigger : Deployed checkout v2.3.1
    reason  : Deployment of checkout v2.3.1 preceded latency spike (rule RW001).
              Memory usage rose, OOMKill followed, triggering crash loop (RW003).
    chain:
      → Deploy trigger
      → latency.p99 ↑4.2×
      → OOMKill → CrashLoop
      → error.rate ↑8.1×
```

## What sources does it read from?

| Source | What it collects |
|---|---|
| **Prometheus** | RED metrics, USE metrics, container restarts, OOM events |
| **Kubernetes API** | Pod lifecycle, deployment rollouts, events, ownership graph |
| **Loki** | Error/panic/fatal log burst detection + sample lines |
| **Tempo** | Trace error spike detection, exemplar trace IDs |
| **GitHub / GitLab** | Deployment and pipeline events |
| **Alertmanager** | Fired alerts as corroborating timeline markers |

Rewind is **read-only** — safe to run against production mid-incident.

## Bundle format: self-contained incident evidence

```bash
# Investigate and save a bundle
rewind investigate --from 14:00 --to 14:45 -o incident-20260709.rewind

# Open the bundle in the web UI (offline, no cluster access needed)
rewind ui incident-20260709.rewind

# Render as Markdown for your postmortem
rewind import incident-20260709.rewind --format md > postmortem.md

# Re-run analysis on old bundle after updating rewind
rewind investigate --replay incident-20260709.rewind
```

## Commands

```
rewind investigate   Run an investigation and render the timeline
rewind ui            Open the web UI
rewind export        Re-export / upgrade a bundle
rewind import        Render a bundle
rewind sources       Check source connectivity
rewind demo          Spin up a local demo (Phase 6)
rewind explain       Explain a correlation rule (e.g. rewind explain RW001)
rewind version       Print version info
```

## Configuration

Rewind reads `./rewind.yaml` or `~/.config/rewind/rewind.yaml`.
All values can be overridden with `REWIND_*` env vars.

```yaml
# rewind.yaml
source_timeout: 15s

prometheus:
  url: http://prometheus.monitoring.svc:9090

loki:
  url: http://loki.monitoring.svc:3100

kubernetes:
  kubeconfig: ~/.kube/config
  context: prod-cluster

github:
  token: ${GITHUB_TOKEN}
  repos:
    - myorg/checkout
    - myorg/frontend

alertmanager:
  url: http://alertmanager.monitoring.svc:9093
```

## Build from source

```bash
git clone https://github.com/rewind-io/rewind
cd rewind
make build
./bin/rewind version
```

## Architecture

See [docs/architecture.md](docs/architecture.md) for the full design.

## License

Apache-2.0 — see [LICENSE](LICENSE).
