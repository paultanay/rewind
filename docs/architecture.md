# Architecture

> This document mirrors §5–§12 of the product specification.
> For the rule catalog detail, see `docs/rules/`.

---

## 1. Design principles

- **Read-only pull model.** Rewind never writes to production systems.
  Every collector opens a read-only connection, respects source timeouts,
  and degrades gracefully on failure — a failing source contributes
  an error to `SourceReport` but never aborts the investigation.

- **Deterministic analysis.** The correlation engine uses static rules,
  not ML or probabilistic models. Given the same input, Rewind always
  produces the same verdict. This makes postmortems auditable and unit
  tests trivial.

- **No storage, no agents.** The tool is a single statically-linked binary.
  Results are written to stdout or to a portable `.rewind` bundle file.
  Nothing is persisted between runs.

- **Bundle portability.** A `.rewind` bundle (zip + JSON) contains enough
  raw data to reproduce the terminal and web UI output on any machine
  with no network access.

---

## 2. Component overview

```
cmd/rewind   (cobra CLI)
  investigate | ui | demo | sources | explain | export | import

        ▼ buildRegistry()
  sources (parallel errgroup, 15s timeout each)
  prometheus/  kubernetes/  loki/  tempo/  alertmanager/  cicd/

        ▼ model.Incident  (Events + Signals + Entities)

  analyze.RunFull
  ① changepoint.Detect  (CUSUM + PELT) → Anomaly events
  ② correlate.CoalesceRestarts (RW009) → CrashLoop events
  ③ topology.Build → EntityGraph (BFS adjacency)
  ④ correlate.Run (RW001–RW010) → Hypotheses with scores
  ⑤ correlate.Assemble → Verdict (sorted, calibrated)

        ▼
  render/terminal  |  render/markdown  |  server/ (embedded SPA)
```

---

## 3. Data model

### `model.Incident`

The central data structure. Created by the CLI, populated by collectors,
enriched by the analysis engine, then rendered.

```
Incident
├── ID             string           unique ID (timestamp-based)
├── Window         TimeRange        investigation window (from → to)
├── Scope          Scope            namespaces + services filter
├── Meta           Meta             version, schema, timestamps
├── Entities[]     Entity           nodes in the topology graph
├── Events[]       Event            deploys, alerts, OOMKills, anomalies…
├── Signals[]      Signal           metric time series (points + metadata)
├── Sources[]      SourceReport     per-source status + counts
└── Verdict        *Verdict         correlation engine output
```

### Event kind taxonomy

`Deploy`, `ConfigChange`, `OOMKill`, `Restart`, `PodKilled`, `NodePressure`,
`ProbeFailed`, `AlertFired`, `AlertResolved`, `LogBurst`, `TraceErrorSpike`,
`CrashLoop` (synthetic, RW009), `Anomaly` (synthetic, changepoint).

### Metric constants

`latency.p99`, `latency.p95`, `error.rate`, `cpu.usage`, `cpu.throttle`,
`memory.usage`, `restarts`, `replicas`, `queue.lag`, `disk.io`,
`trace.error.rate`, `trace.latency.p99`.

---

## 4. Source collectors

Each collector implements `sources.Collector`:

```go
type Collector interface {
    Name()    string
    Check(ctx context.Context) error
    Collect(ctx context.Context, scope Scope, window TimeRange) CollectResult
}
```

| Collector | Key signals | Key events |
|-----------|------------|------------|
| Prometheus | latency.p99, error.rate, cpu.*, memory.usage | — |
| Kubernetes | — | Deploy, OOMKill, Restart, ProbeFailed, NodePressure |
| GitHub/GitLab | — | Deploy (from pipeline runs) |
| Loki | log.error.rate | LogBurst |
| Tempo | trace.error.rate, trace.latency.p99 | TraceErrorSpike; topology edges |
| Alertmanager | — | AlertFired, AlertResolved |

---

## 5. Change-point detection

Two detectors run on every signal (`internal/analyze/changepoint/`):

**CUSUM** — detects persistent mean shifts. Threshold: `k * stdev(series)`, k=1.5.

**PELT** — detects multiple change-points via penalised least squares (BIC penalty).

Results are merged and de-duplicated by proximity (within 2 minutes → keep
highest-confidence). Each anomaly becomes a `model.Event{Kind: "Anomaly"}`.

---

## 6. Correlation engine (RW001–RW010)

Located in `internal/analyze/correlate/`. The engine is a rule catalog
where each rule returns zero or more `Hypothesis` structs.

**Scoring:**
```
score = temporal_score(gap) × magnitude_factor × Σ(corroboration_bonus)
```

**Confidence tiers:** `HIGH` (≥0.7), `MEDIUM` (≥0.45), `LOW` (≥0.25), `SPECULATIVE` (<0.25).

**Invariants:**
- `RW010`: `AlertFired` events **never** create trigger hypotheses. They add +0.10 as corroboration.
- Duplicate triggers (same event, multiple rules) are merged with concatenated chains.
- Hypotheses with score < 0.10 are pruned.

---

## 7. Bundle format

A `.rewind` bundle is a ZIP archive:

```
incident.json          model.Incident (schemaVersion mandatory)
sources/
  prometheus.json      raw CollectResult per source
  kubernetes.json
  …
```

- `export → import → export` is byte-identical (modulo Meta.CreatedAt)
- Unknown JSON fields preserved (forward-compatible)
- Signals downsampled to 500 points max; logs sampled to 200 lines max (< 5 MB)

---

## 8. Web UI

`internal/server/` — SPA embedded via `//go:embed ui/dist`. Bound to `127.0.0.1` only.

```
GET /api/incident   model.Incident as JSON
GET /api/health     {"status":"ok"}
GET /*              SPA (index.html fallback for client-side routing)
```

---

## 9. Exit codes

| Code | Meaning |
|------|---------|
| 0 | Complete, no Critical findings |
| 1 | Complete, ≥1 HIGH/Critical finding |
| 2 | Usage error |
| 3 | All sources failed |
| 4 | Internal error |
