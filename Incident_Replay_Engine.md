```markdown
# AGENTS.md — Project "Rewind"
## The Production Incident Replay Engine

You are the sole senior engineer building this product end to end. Read this
entire document before writing any code. Every decision not explicitly left
open here has already been made — do not deviate. Where judgment is required,
optimize for: correctness > operational safety > developer experience >
performance > feature count.

---

## 1. Vision

**One sentence:** Rewind reconstructs a production incident as a single,
scrubbable timeline — deployments, metric anomalies, log error bursts,
Kubernetes events, and traces, all correlated and causally ranked — like
watching CCTV footage of your system instead of tab-switching between five
dashboards.

**The problem.** When production breaks, an engineer opens Grafana for
metrics, Loki/Kibana for logs, Jaeger/Tempo for traces, `kubectl get events`,
the CI/CD pipeline page, and the git log — six tools, six time axes, six
mental contexts — and manually builds the story: "deploy at 14:20 → CPU up →
Kafka lag → pod OOMKilled → 500s." That story-building is the actual work of
incident response, it takes the most senior person in the room 30–90 minutes,
and no free tool does it. Observability vendors sell fragments of this
(Grafana Sift, Datadog Watchdog) behind paywalls; open source has the *data*
(Prometheus, Loki, Tempo, K8s API) but not the *narrative layer*.

**The product.** Rewind is that narrative layer. Critically, it is NOT
another storage/agent/observability stack. It is a **stateless-by-default
investigator** that, given a time window, pulls from the observability
systems a team already runs, normalizes everything into one event model,
detects change-points, ranks likely causes, and renders one timeline —
in the terminal or a local web UI — plus an exportable, self-contained
**incident bundle** file anyone can replay later without access to any of
the source systems.

**The demo that must work on day one of v1.0:** against a local kind cluster
running a demo app with Prometheus + Loki, the user breaks the app with a bad
deploy, then runs
`rewind investigate --from 14:00 --to 14:45 --namespace shop`
and gets, in under 30 seconds, a terminal timeline showing the deployment
marker, the latency change-point 40s later, the error-log burst, the
OOMKill event, and a ranked verdict: *"most likely trigger: deployment of
checkout v2.3.1 (confidence: high; 4 corroborating signals)"* — then
`rewind ui` opens the same incident as a scrubbable browser timeline, and
`rewind export` produces `incident-2026-07-09.rewind` that replays on a
laptop with zero cluster access.

**Who uses it:** on-call engineers mid-incident, teams writing postmortems
(the bundle IS the postmortem evidence), and platform teams who embed
`rewind investigate` into their incident tooling.

**Non-goals (never build these):** a metrics/log/trace *store* (never
persist raw telemetry long-term), an agent/daemonset, an alerting system, a
Grafana replacement, dashboards, a SaaS, auto-remediation, AI/LLM analysis
in v1 (design the verdict engine so an LLM explainer can be added later, but
v1 causality is deterministic and explainable).

---

## 2. Product principles (enforce in every decision)

- **Single static binary.** CLI + embedded web UI in one `go install`-able
  artifact. No database to run, no server to deploy. State lives in bundle
  files.
- **Read-only, always.** Rewind only ever queries. It must be safe to run
  against production observability endpoints mid-incident. Every source
  client is read-only by construction.
- **Pull, don't collect.** v1 is post-hoc: query existing systems for a
  window. (A continuous "flight recorder" mode is a documented v2 ambition,
  not v1.)
- **Every conclusion is explainable.** No black boxes: every causal claim in
  a verdict lists the exact signals, timestamps, and rule that produced it.
  Confidence is always labeled (`high`/`medium`/`speculative`).
- **Degrade gracefully.** Rewind must produce a useful timeline from
  Prometheus alone, get better with K8s events, better still with Loki, etc.
  Missing sources reduce richness, never cause failure.
- **The bundle is sacred.** A `.rewind` file is complete, portable,
  versioned, and diffable evidence. Export/import round-trips losslessly.

---

## 3. Naming, license, repo

- Binary and module name: `rewind`. Module path: the repository's own hosting
  path (e.g. `gitlab.com/<owner>/rewind`).
- License: Apache-2.0. Include `LICENSE`.
- Language: Go, latest stable (1.23+), CGO disabled. Web UI: see §11.

---

## 4. Scope of v1.0

**Sources (in):** Prometheus (and API-compatible: Thanos/Mimir/VictoriaMetrics),
Loki, Kubernetes API (events, pod lifecycle, deployment rollouts), Tempo
(trace summaries only), GitLab & GitHub deployment/pipeline events via their
REST APIs, Alertmanager (fired alerts as timeline markers).

**Out (v1):** Elasticsearch/Kibana, Datadog/NewRelic, eBPF, continuous
recording mode, LLM narration, multi-incident storage server, RBAC/multi-user.

**Commands:** `investigate`, `ui`, `export`, `import`, `sources`, `demo`,
`version`.

---

## 5. Architecture overview

```
 ┌──────────────────────── Sources (read-only clients) ────────────────────────┐
 │ Prometheus │ Loki │ K8s API │ Tempo │ GitLab/GitHub │ Alertmanager          │
 └─────┬──────────┬───────┬────────┬────────────┬──────────────┬──────────────┘
       ▼          ▼       ▼        ▼            ▼              ▼
 ┌─────────────────────────────────────────────────────────────┐
 │              Collectors (parallel, per-source)              │
 │   window in → normalized []Event + []Series out             │
 └───────────────────────────┬─────────────────────────────────┘
                             ▼
 ┌─────────────────────────────────────────────────────────────┐
 │                      Incident Model                         │
 │  Events (point-in-time) · Signals (time series + detected   │
 │  change-points) · Entities (service/pod/node topology)      │
 └───────────────────────────┬─────────────────────────────────┘
                             ▼
 ┌─────────────────────────────────────────────────────────────┐
 │                    Analysis Engine                          │
 │  1. Change-point detection on signals                       │
 │  2. Entity graph construction (K8s ownership + kube service │
 │     topology + label joins)                                 │
 │  3. Correlation & causal ranking (rules, §10)               │
 │  4. Verdict generation                                      │
 └───────────────────────────┬─────────────────────────────────┘
                             ▼
 ┌──────────────────────────────────────────────┐   ┌──────────────────────┐
 │  Renderers: terminal timeline │ web UI │ md  │   │  Bundle (.rewind)    │
 └──────────────────────────────────────────────┘   │  export / import     │
                                                    └──────────────────────┘
```

`investigate` = collect → model → analyze → render terminal + write bundle.
`ui` = serve embedded SPA over a bundle (or straight from an investigation).
Everything downstream of the collectors operates ONLY on the incident model —
renderers and the analysis engine must have zero knowledge of any source API.

---

## 6. Package layout

```
rewind/
├── cmd/rewind/main.go             # thin main; wiring only
├── internal/
│   ├── cli/                       # cobra commands, config, exit codes
│   ├── model/                     # Event, Signal, Entity, Incident, Verdict
│   ├── sources/
│   │   ├── source.go              # Collector interface + registry
│   │   ├── prometheus/
│   │   ├── loki/
│   │   ├── kubernetes/
│   │   ├── tempo/
│   │   ├── cicd/                  # gitlab + github deployment events
│   │   └── alertmanager/
│   ├── analyze/
│   │   ├── changepoint/           # detection algorithms (§9)
│   │   ├── topology/              # entity graph construction
│   │   ├── correlate/             # rule-based causal ranking (§10)
│   │   └── verdict/
│   ├── bundle/                    # .rewind file format, export/import
│   ├── render/
│   │   ├── terminal/              # the flagship TUI timeline
│   │   └── markdown/              # postmortem-ready output
│   ├── server/                    # local HTTP server for the UI
│   └── demo/                      # `rewind demo`: kind + demo app + break-it
├── ui/                            # web frontend (built asset embedded via go:embed)
├── docs/                          # architecture.md, sources/, rules/, bundle-spec.md
├── testdata/                      # recorded source responses + golden bundles/verdicts
├── .golangci.yml
├── .gitlab-ci.yml
├── .goreleaser.yaml
├── Makefile
└── README.md
```

---

## 7. Core domain model (define first, exactly this shape)

```go
// model package — the single vocabulary of the whole product.

type Incident struct {
    ID        string
    Window    TimeRange
    Scope     Scope            // namespaces/services/labels the user asked about
    Entities  []Entity
    Events    []Event
    Signals   []Signal
    Verdict   *Verdict
    Sources   []SourceReport   // which sources answered, errors, latencies
    Meta      Meta             // tool version, created at, bundle schema version
}

type Entity struct {              // node in the topology graph
    ID    string                  // "svc/shop/checkout", "pod/shop/checkout-7d9f..."
    Kind  EntityKind              // Service, Deployment, Pod, Node, Queue, Database
    Owner string                  // parent entity ID (pod → deployment → service)
    Labels map[string]string
}

type Event struct {               // discrete, point-in-time
    ID        string
    At        time.Time
    Kind      EventKind           // Deploy, ConfigChange, PodKilled, OOMKill,
                                  // Restart, ScaleChange, AlertFired, NodePressure,
                                  // ProbeFailed, LogBurst, TraceErrorSpike...
    EntityID  string
    Severity  Severity            // Info, Notable, Critical
    Title     string              // one line, human
    Detail    string
    SourceRef SourceRef           // source name + native ID/URL for deep-linking
}

type Signal struct {              // continuous
    ID          string
    EntityID    string
    Metric      string            // canonical name, e.g. "latency.p99", "cpu.usage"
    Unit        string
    Points      []Point           // downsampled to ≤ 500 points per signal
    ChangePoints []ChangePoint    // filled by analysis
}

type ChangePoint struct {
    At         time.Time
    Direction  Direction          // Up, Down
    Magnitude  float64            // e.g. 3.4 = 3.4× the pre-window baseline
    Score      float64            // detector confidence 0..1
}

type Verdict struct {
    Hypotheses []Hypothesis       // ranked
}
type Hypothesis struct {
    TriggerEventID string         // "it started with this"
    Confidence     Confidence     // High, Medium, Speculative
    Chain          []ChainLink    // ordered narrative: event/changepoint refs
    Explanation    string         // plain English, cites rule IDs
    RuleIDs        []string
}
```

Canonical metric names (`latency.p99`, `error.rate`, `cpu.usage`,
`memory.usage`, `restarts`, `queue.lag`, ...) are defined once in `model` and
each source maps into them. This canonicalization is what makes the analysis
engine source-agnostic; treat the mapping tables as first-class, documented,
tested artifacts.

---

## 8. Collectors (per-source specification)

Common contract: `Collect(ctx, Scope, TimeRange) (Events, Signals, Entities, error)`.
All collectors run in parallel with a per-source timeout (default 15s,
configurable). A failing source becomes a `SourceReport` entry and a terminal
warning — never a fatal error. Every HTTP client: sane timeouts, retry with
backoff (max 2), user-agent `rewind/<version>`, honors proxy envs. Config
file `rewind.yaml` (+ flags + env) declares endpoints and auth.

- **Prometheus:** query_range against a curated default query set per entity
  kind (RED metrics: rate/errors/duration from common instrumentation
  conventions; USE for nodes; container restarts, OOM, CPU throttling from
  cAdvisor/kube-state-metrics). Users can add custom queries in config.
  Step chosen so each signal ≤ 500 points. Also fetch a **baseline window**
  (default: same duration immediately before the incident window, plus the
  same window 24h earlier if available) — change-point detection needs it.
- **Kubernetes:** list Events in scope (mapped to Event kinds: OOMKill,
  ProbeFailed, Scheduled, Killing, ScaleChange...), reconstruct deployment
  rollouts from ReplicaSet revision history (a rollout = Deploy event with
  image diff in Detail), build the ownership chain for the Entity graph.
  Use client-go with the user's kubeconfig; read-only verbs only.
- **Loki:** do NOT pull raw logs wholesale. Query error-ish rates
  (`count_over_time` of level=error/panic/fatal patterns) per entity to
  detect LogBurst events, then fetch only the top-N (default 20) sample log
  lines around each burst for the timeline's Detail. This restraint is an
  explicit design decision: Rewind is a narrative tool, not a log viewer —
  it deep-links back to Loki/Grafana for the full logs (SourceRef).
- **Tempo:** service-level error-span rate and p99 from the metrics-generator
  if present; otherwise search for error traces in the window and surface the
  top exemplar trace IDs as TraceErrorSpike events with deep links. Never
  ingest full traces.
- **CI/CD (GitLab + GitHub):** deployments/pipelines to the affected
  environment in an expanded window (incident window + 2h lookback —
  deploys *before* the window are prime suspects). Map to Deploy events
  with commit SHA, author, and diff link in Detail.
- **Alertmanager:** fired/resolved alerts in window → AlertFired events
  (Notable severity; alerts are symptoms, never triggers — encode that in
  correlation rules).

Testing: every collector is tested against recorded HTTP fixtures in
`testdata/` (record/replay via `httptest`); integration tests (build tag
`integration`) run against real Prometheus/Loki/kind via testcontainers.

---

## 9. Change-point detection (`analyze/changepoint`)

Purpose: turn each Signal into ≤ a handful of ChangePoints, robustly, with
zero configuration. Requirements:

- Implement **two detectors** behind one interface and combine them:
  1. Robust baseline-deviation: median + MAD over the baseline window;
     flag sustained excursions beyond k·MAD (k default 5) lasting ≥ 3 points.
  2. Offline change-point detection on the incident window: PELT or binary
     segmentation with a normal-mean cost function — implement it yourself
     in pure Go with tests against synthetic series (step, ramp, spike,
     noise-only); do not pull in a heavyweight stats dependency.
- Merge/dedupe: change-points within 2× step of each other collapse to the
  strongest. Cap at 5 per signal, keep the highest scores.
- Every detector decision must be reproducible: same input → same output
  (no randomness), because bundles must replay identically.
- Known failure modes to handle explicitly and test: counters that reset,
  gaps/NaNs, series that begin mid-window (new pods), constant series,
  seasonal daily patterns (mitigated by the 24h-ago baseline).

---

## 10. Correlation & verdict engine (`analyze/correlate`) — the crown jewel

Deterministic, rule-based, explainable. NO machine learning in v1. The
engine scores (candidate trigger event → observed effect) edges and
assembles the highest-scoring causal chains into Hypotheses.

Inputs: Events, Signals+ChangePoints, Entity graph. Core mechanics:

- **Temporal precedence:** a trigger must precede the effect. Score decays
  with gap (effects within 0–5m of a deploy score high; 2h, near zero).
- **Topological proximity:** effects on the same entity or graph-adjacent
  entities (pod↔deployment↔service, service↔service via detected call edges
  from Tempo when available) score higher than unrelated ones.
- **Rule catalog** with stable IDs, one doc page each (`docs/rules/`).
  Seed rules (implement all):
  - RW001 Deploy → any change-point on same service within 10m (the classic).
  - RW002 ConfigChange → same as RW001.
  - RW003 OOMKill chain: memory.usage change-point ↑ → OOMKill → Restart →
    error.rate ↑ on dependents.
  - RW004 Saturation: cpu.usage/throttling ↑ → latency.p99 ↑ same entity.
  - RW005 Cascade: error.rate ↑ on service B where B is a dependency of A,
    preceding A's error.rate ↑ → B is upstream cause.
  - RW006 Node pressure: NodePressure/eviction events → effects on all pods
    of that node.
  - RW007 Queue lag: queue.lag ↑ preceding consumer latency ↑.
  - RW008 Scale event: replica count ↓ (or HPA maxed) preceding saturation.
  - RW009 Crash loop: ≥3 Restart events in 10m on one pod = single
    CrashLoop event (event coalescing, not just correlation).
  - RW010 Alert-as-symptom: AlertFired never scores as a trigger, only as
    corroboration (+confidence to chains it overlaps).
- **Confidence calibration:** High = trigger + ≥3 corroborating signals
  and no competing hypothesis within 20% of its score; Medium = 2+
  corroborations or close competitors; everything else Speculative. Always
  emit up to 3 hypotheses — the tool proposes, the engineer decides. If no
  chain scores above floor, say honestly: "No clear trigger identified"
  and list the most notable anomalies instead.
- Every Hypothesis.Explanation is generated from templates that cite
  timestamps, entities, magnitudes, and rule IDs. No free-form generation.

Golden-corpus testing: `testdata/incidents/` holds ≥10 synthetic incident
scenarios (bad deploy, OOM cascade, node failure, upstream dependency
outage, noisy-no-incident, seasonal-traffic-false-positive...) as recorded
source fixtures, each with an expected verdict golden file. This corpus is
the product's regression net; extend it with every rule added.

---

## 11. Renderers & UI

- **Terminal timeline (flagship, phase-early):** a static (non-interactive
  in v1) chronological render: one column of aligned timestamps, severity
  glyphs/colors, entity tags, change-point sparkline annotations
  (`▁▁▂▇█ ↑3.4×`), followed by the verdict block. Must look outstanding —
  this is the screenshot that markets the project. Snapshot-test it.
- **Web UI (`rewind ui`):** local server (bind 127.0.0.1 by default) serving
  an embedded single-page app over one JSON endpoint (`GET /api/incident`).
  Keep frontend scope ruthless: one horizontally scrubbable timeline —
  lanes per entity, event markers, signal ribbons with shaded change-points,
  click → detail panel with SourceRef deep links, verdict sidebar. Build
  with a minimal, no-backend-framework stack (Vite + Svelte or Preact +
  a canvas/SVG timeline you write yourself; no heavy chart or component
  libraries). Production build artifact committed via CI and embedded with
  `go:embed` so `go install` users get the UI with zero node toolchain.
- **Markdown renderer:** postmortem-ready — timeline table + verdict +
  evidence links. Designed to paste into an incident issue.

---

## 12. Bundle format (`.rewind`) — `docs/bundle-spec.md`

A gzipped tar containing `incident.json` (the full Incident model,
`schemaVersion` field mandatory), plus `sources/*.json` raw fixture data for
reproducibility. Requirements: export→import→export is byte-identical
(modulo timestamps in Meta); forward-compatible reading (unknown fields
preserved); size guardrails (downsampling + log/trace sampling rules from §8
keep typical bundles under ~5 MB). `rewind ui incident.rewind` and
`rewind investigate --replay incident.rewind` (re-runs analysis on bundled
raw data — this is how analysis-engine improvements are validated against
old incidents) must both work fully offline.

---

## 13. CLI specification

```
rewind investigate --from <t> --to <t> [--namespace ns] [--service svc]...
                   [--config rewind.yaml] [--format term|md|json]
                   [-o incident.rewind] [--replay bundle]
rewind ui          [incident.rewind] [--port 0]
rewind export / import
rewind sources     # connectivity + capability check per configured source
rewind demo        # spins kind + demo app + Prometheus + Loki, breaks it, investigates
rewind explain RW001
rewind version
```

Time inputs accept RFC3339, `14:00` (today, local), and relative (`-45m`).
Exit codes: 0 ok, 1 investigation produced Critical findings (for CI-ish
use), 2 usage error, 3 all sources failed, 4 internal error. Contractual;
test them. `rewind demo` is a product feature, not a toy: it is how every
newcomer (and every conference demo) experiences the tool in five minutes —
build it in `internal/demo` with the same quality bar as everything else.

---

## 14. Engineering standards (non-negotiable)

- Idiomatic Go; no panics outside main init; errors wrapped with `%w` and
  context; interfaces defined by consumers; no global state beyond cobra
  wiring; context propagation everywhere; all collector concurrency via
  errgroup with bounded parallelism.
- Comments explain *why*; every correlation rule and metric mapping carries
  a doc citation or rationale. No dead code, no leftover TODOs — out-of-scope
  items go to `docs/roadmap.md`.
- **Testing:** unit tests throughout; recorded-fixture tests for every
  collector; synthetic-series tests for every detector edge case in §9;
  the golden incident corpus for the verdict engine (§10); terminal-render
  snapshot tests; bundle round-trip property tests; integration tag for
  kind/testcontainers e2e including the full `rewind demo` path. Race
  detector on. Coverage gate ≥ 80% on `model`, `analyze`, `bundle`.
- Lint: golangci-lint with `errcheck, govet, staticcheck, revive, gosec,
  gocritic, misspell` minimum. Frontend: eslint + prettier, CI-enforced.
- CI (.gitlab-ci.yml): lint → test → build-ui → integration (dind) →
  goreleaser release: linux/darwin/windows, amd64+arm64, checksums, plus a
  distroless image.
- Docs: README with the §1 demo as an asciinema-able quickstart,
  `docs/architecture.md` mirroring §5–§12, one page per source and per rule,
  bundle spec, config reference.

---

## 15. Build order (strict; each phase ends compilable, tested, demoable)

1. **Phase 1 — skeleton + model:** repo, CI, cobra, exit codes, the full
   `model` package with bundle read/write and round-trip tests, terminal
   renderer over hand-written fixture incidents. (Renderer-first: the
   timeline look drives everything.)
2. **Phase 2 — Prometheus + change-points:** collector, canonical metric
   mapping, both detectors, `investigate` works end to end with metrics
   only. First releasable artifact (v0.1): "anomaly timeline for any
   Prometheus."
3. **Phase 3 — Kubernetes + CI/CD sources:** events, rollout reconstruction,
   entity graph, deploy markers on the timeline (v0.2).
4. **Phase 4 — correlation engine:** rule catalog RW001–RW010, verdicts,
   golden incident corpus, `explain` (v0.3 — the product becomes *the*
   product here).
5. **Phase 5 — Loki + Tempo + Alertmanager:** log bursts, trace spikes,
   alert corroboration (v0.4).
6. **Phase 6 — web UI + demo + polish to v1.0:** embedded SPA, `rewind demo`,
   markdown renderer, docs, release engineering, README recordings.

Never start phase N+1 with failing tests in phase N. When source API
behavior is uncertain, write a recorded-fixture or integration test that
proves it rather than assuming.

---

## 16. Definition of done for v1.0

- The §1 demo works verbatim on a clean machine with Go, Docker, and kind.
- The golden corpus: correct top hypothesis on ≥ 8 of 10 scenarios, zero
  High-confidence verdicts on the two no-incident/false-positive scenarios.
- A bundle exported on one machine replays fully (terminal + UI) on another
  with no network.
- All standards in §14 pass. A stranger goes from README to a rendered
  incident timeline in under five minutes via `rewind demo`.
```

Same two closing directions as before, adapted:

- **Run the agent one phase at a time** with your review between phases. Phase 4 (the correlation engine) is where you must personally understand every rule, because that's exactly what interviewers will drill into.
- The scoped-down decisions in this file (pull-based, no storage, deterministic rules, bundle format) are what make this buildable solo. If your agent starts proposing an ingestion pipeline, ML root-cause models, or a hosted backend, that's scope creep - point it back to §1 Non-goals.

Want the Phase 1 kickoff prompt to hand the agent alongside this file?