# Rewind Architecture

This document is the canonical reference for Rewind's internal design.
It mirrors the spec in `Incident_Replay_Engine.md` §5–§12 and is kept
up to date as implementation progresses.

---

## 1. Guiding constraints

Every architectural decision is weighed against these priorities (in order):

1. **Correctness** — wrong verdicts are worse than no verdict
2. **Operational safety** — read-only, safe to run against production
3. **Developer experience** — single binary, zero dependencies to operate
4. **Performance** — under 30 seconds end-to-end for any realistic incident
5. **Feature count** — defer everything not needed for the core narrative

---

## 2. System diagram

```
 ┌──────────────────────── Sources (read-only clients) ────────────────────────┐
 │ Prometheus │ Loki │ K8s API │ Tempo │ GitLab/GitHub │ Alertmanager          │
 └─────┬──────────┬───────┬────────┬────────────┬──────────────┬──────────────┘
       ▼          ▼       ▼        ▼            ▼              ▼
 ┌─────────────────────────────────────────────────────────────┐
 │              Collectors (parallel, per-source)              │
 │   window in → normalised []Event + []Series out             │
 └───────────────────────────┬─────────────────────────────────┘
                             ▼
 ┌─────────────────────────────────────────────────────────────┐
 │                      Incident Model                         │
 │  Events · Signals (+ ChangePoints) · Entities              │
 └───────────────────────────┬─────────────────────────────────┘
                             ▼
 ┌─────────────────────────────────────────────────────────────┐
 │                    Analysis Engine                          │
 │  1. Change-point detection  (analyze/changepoint)           │
 │  2. Entity graph            (analyze/topology)              │
 │  3. Causal ranking          (analyze/correlate)             │
 │  4. Verdict generation      (analyze/verdict)               │
 └───────────────────────────┬─────────────────────────────────┘
                             ▼
 ┌──────────────────────────────────────────────┐   ┌──────────────────────┐
 │  Renderers: terminal timeline │ web UI │ md  │   │  Bundle (.rewind)    │
 └──────────────────────────────────────────────┘   │  export / import     │
                                                    └──────────────────────┘
```

**Key invariant:** Everything downstream of the collectors operates *only*
on `model.Incident`. No renderer or analysis function imports a source package.

---

## 3. Package map

| Package | Responsibility |
|---|---|
| `cmd/rewind` | Thin entry point, version injection |
| `internal/cli` | Cobra commands, config loading, exit codes |
| `internal/model` | Single canonical vocabulary (types + helpers) |
| `internal/sources` | Collector interface, registry, parallel runner |
| `internal/sources/prometheus` | Prometheus/Thanos/Mimir/VM collector |
| `internal/sources/loki` | Loki log-burst collector |
| `internal/sources/kubernetes` | K8s events, rollouts, entity graph |
| `internal/sources/tempo` | Tempo trace error rate collector |
| `internal/sources/cicd` | GitHub + GitLab deployment events |
| `internal/sources/alertmanager` | Alertmanager fired-alert collector |
| `internal/analyze/changepoint` | Baseline-deviation + PELT detectors |
| `internal/analyze/topology` | Entity graph construction |
| `internal/analyze/correlate` | Rule-based causal ranking (RW001–RW010) |
| `internal/analyze/verdict` | Hypothesis assembly + confidence calibration |
| `internal/bundle` | .rewind file format, export/import |
| `internal/render/terminal` | Flagship TUI timeline renderer |
| `internal/render/markdown` | Postmortem-ready Markdown renderer |
| `internal/server` | Local HTTP server for web UI |
| `internal/demo` | `rewind demo` cluster setup and scenario runner |
| `ui/` | Svelte/Preact SPA (built artifact embedded via go:embed) |

---

## 4. Domain model

See `internal/model/types.go` for the full type definitions.

Core types:

- **`Incident`** — root type; everything hangs off this
- **`Entity`** — node in the topology graph (Service, Pod, Deployment, Node, Queue, Database)
- **`Event`** — discrete point-in-time occurrence (Deploy, OOMKill, AlertFired, ...)
- **`Signal`** — named time series scoped to an entity, with detected `ChangePoint`s
- **`Verdict`** — ordered list of `Hypothesis` objects produced by the analysis engine
- **`SourceReport`** — collector outcome (ok/partial/failed), for transparency

Canonical metric names are defined as constants in `model/types.go`. Every
collector maps its native metrics to these constants. The analysis engine and
renderers reference *only* these names.

---

## 5. Collector contract

```go
type Collector interface {
    Name() string
    Collect(ctx context.Context, scope model.Scope, window model.TimeRange) (CollectResult, error)
    Check(ctx context.Context) error
}
```

Collectors run concurrently via `sources.RunAll`. A failing collector
produces a `SourceReport{Status: "failed"}` and does not affect other
collectors. The analysis engine always receives whatever partial data was
collected.

Every collector is tested against recorded HTTP fixtures in `testdata/fixtures/`
(httptest record/replay pattern). Integration tests (build tag `integration`)
run against real systems via testcontainers.

---

## 6. Analysis pipeline

### 6.1 Change-point detection (`analyze/changepoint`)

Two detectors run on every Signal:

1. **Baseline deviation** — median + MAD over the baseline window. Flags
   sustained excursions beyond k·MAD (default k=5) lasting ≥3 consecutive points.

2. **PELT** — offline change-point detection with a normal-mean cost function.
   Implemented in pure Go, no external stats dependencies.

Results are merged: change-points within 2× the step interval collapse to
the strongest. Maximum 5 change-points per signal.

### 6.2 Topology (`analyze/topology`)

Builds a directed ownership graph from Kubernetes resources:
pod → replica set → deployment → service. Also incorporates call-graph
edges from Tempo when available.

### 6.3 Correlation (`analyze/correlate`)

Rule-based, deterministic, no ML. Rules RW001–RW010 are defined in
`internal/analyze/correlate/rules.go` and documented individually in
`docs/rules/`. Each rule scores (trigger event → observed effect) edges.
The engine assembles the top 3 causal chains into Hypotheses.

See `docs/rules/` for individual rule documentation.

### 6.4 Confidence calibration

- **High**: trigger + ≥3 corroborating signals, no competitor within 20% of score
- **Medium**: 2+ corroborations, or close competitor
- **Speculative**: everything else

If no chain exceeds the minimum score floor, `Verdict.NoTriggerFound = true`
and the most notable anomalies are listed instead.

---

## 7. Bundle format

`.rewind` files are gzipped tar archives:

```
incident.json     — JSON-encoded model.Incident (schemaVersion mandatory)
sources/          — raw source fixture data for replay
  prometheus.json
  kubernetes.json
  ...
```

Schema version: `1` (current). Forward-compatible: unknown fields preserved.
Export → import → export is byte-identical (modulo Meta.CreatedAt).
Typical bundle size: < 5 MB.

See `docs/bundle-spec.md` for the complete format specification.

---

## 8. Build phases

| Phase | Deliverable | Status |
|---|---|---|
| 1 | Skeleton + model + bundle + terminal renderer | ✅ complete |
| 2 | Prometheus collector + change-point detection | planned |
| 3 | Kubernetes + CI/CD collectors, entity graph | planned |
| 4 | Correlation engine RW001–RW010, golden corpus | planned |
| 5 | Loki + Tempo + Alertmanager | planned |
| 6 | Web UI + demo + v1.0 polish | planned |

---

## 9. Engineering standards

- Idiomatic Go; no panics outside `main` init
- Errors wrapped with `%w` and context at every boundary
- Interfaces defined at the consumer, not the producer
- No global state beyond cobra wiring
- Context propagation everywhere
- All collector concurrency via bounded goroutines (sync.WaitGroup + per-source timeout)
- CGO disabled for portable static binaries
- Coverage gate ≥80% on `model`, `analyze`, `bundle`
