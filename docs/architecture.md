# Architecture

Rewind is a read-only pull tool. It gathers a bounded window from configured
observability systems, translates each response into the shared Go incident
model, runs deterministic analysis, and renders that same model to terminal,
Markdown, JSON, or the embedded local UI.

![Rewind data flow](assets/architecture.svg)

## Boundaries

| Boundary | Responsibility | Trust rule |
| --- | --- | --- |
| CLI | Parse scope, time window, output, and config | Never sends data except through configured collectors |
| Sources | Pull and translate native APIs | Read-only requests; report partial failures |
| `internal/model` | Shared vocabulary for events, signals, entities, and verdicts | Source-specific types do not cross this boundary |
| Analysis | Detect change-points, build topology, and apply RW001-RW010 | Same input produces the same sorted result |
| Bundle | Store incident plus bounded raw source fixtures | Offline replay must not contact live systems |
| Server/UI | Serve one incident and the embedded frontend locally | Binds to `127.0.0.1`; browser is read-only |

## Runtime flow

1. `rewind investigate` resolves configuration and the requested time range.
2. Enabled collectors run with the configured source timeout.
3. The runner records a source report as `ok`, `partial`, `failed`, or
   `skipped`; useful data is retained even when a collector returns an error.
4. Collectors translate native identities into canonical IDs such as
   `service/shop/checkout` and return model events/signals/entities.
5. Analysis detects signal change-points, coalesces repeated restarts, builds
   explicit topology, and applies the correlation rules.
6. Results are sorted with stable tie-breakers before rendering or export.
7. A bundle contains `incident.json` and optional source fixtures. `rewind ui`
   reads it locally and exposes only the incident JSON endpoint.

## Collector contract

```go
type Collector interface {
    Name() string
    Check(ctx context.Context) error
    Collect(ctx context.Context, scope Scope, window TimeRange) CollectResult
}
```

Collectors must:

- use read-only APIs and honor cancellation;
- translate native identifiers into `internal/model` identities;
- return useful data alongside an error when only part of a source failed;
- keep bounded samples and deterministic ordering; and
- include enough source reference to let a reviewer understand provenance.

## Analysis contract

The correlation engine is rule-based, not an ML classifier. A hypothesis is a
ranked explanation assembled from temporal distance, magnitude, topology, and
corroboration. A confidence label is a decision-support signal, not a proof.

Important invariants:

- alerts add corroboration but never become a trigger (`RW010`);
- hypotheses with the same trigger are merged and sorted deterministically;
- canonical entity IDs are used for topology and rendering; and
- missing or failed sources remain visible in the result.

## Local HTTP API

The server is stateless and bound to loopback:

| Endpoint | Response |
| --- | --- |
| `GET /api/health` | `{ "status": "ok" }` |
| `GET /api/incident` | JSON-encoded `model.Incident` |
| `GET /*` | Embedded UI with SPA fallback |

The UI makes one read-only request to `/api/incident`. It does not send bundle
data to a remote service or require a third-party asset host.

## Failure semantics

One unavailable source does not turn a useful investigation into a false
success or abort the other collectors. The overall CLI exit status still
communicates critical findings and total source failure:

| Exit code | Meaning |
| --- | --- |
| 0 | Completed without critical findings |
| 1 | Completed with a critical/high-confidence finding |
| 2 | Usage or configuration error |
| 3 | All configured sources failed |
| 4 | Internal error |

## Repository layout

```text
cmd/rewind/              CLI commands
internal/model/          shared incident contract
internal/sources/        Prometheus, Kubernetes, Loki, Tempo, alert, CI/CD
internal/analyze/        change-point, topology, and correlation rules
internal/bundle/         portable export/import/replay
internal/server/         loopback API and embedded UI
web/                     maintainable frontend source and build
docs/                    public concepts, source, rule, and operations guides
testdata/                offline fixtures and Docker practical test
```
