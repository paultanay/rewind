# Production Readiness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

## Goal

Turn Rewind from a credible technical prototype into a reliable public alpha: one stable incident contract, correct source collection and replay semantics, deterministic analysis, honest failure reporting, a maintainable UI delivery path, and documentation/CI that match the implementation.

This plan deliberately prioritises correctness and trust over breadth. Existing user changes in the original `main` checkout are preserved; this isolated branch starts from the last committed revision and will independently re-establish the intended CI/lint/toolchain contract.

## Architecture and product decisions

- `internal/model` remains the only cross-layer vocabulary. Source packages may translate native identifiers, but analysis, rendering, server, and bundles must not import source-specific types.
- Entity identity is canonical and stable: `service/<namespace>/<name>`, `deployment/<namespace>/<name>`, `pod/<namespace>/<name>`, and `node/<name>`. Legacy `svc/`, `deploy/`, and `pod/` identifiers are accepted at bundle boundaries and normalised before analysis.
- Ownership edges are explicit graph data. A collector may provide `Owner`, but topology construction also consumes declared service/call relationships rather than relying on opaque labels that analysis silently ignores.
- A collector may return useful partial data with an error. The runner records `partial` when data exists with an error, `failed` when no useful data exists with an error, and `ok` otherwise. Source reports are deterministic and never fabricate success.
- A `.rewind` bundle is a portable replay artifact, not merely an incident snapshot. It contains deterministic incident data plus one raw fixture per source when available; replay must use those fixtures without contacting live systems.
- Analysis output is deterministic for identical input. All map-derived and concurrent collections are sorted with stable tie-breakers before correlation and rendering.
- The first public UI remains a dependency-free embedded UI if that is the current repository constraint, but its source must be maintainable, accessible, responsive, and backed by a versioned API contract. A build pipeline is a follow-up only if it materially improves maintainability after the contract is correct.

## Tech stack and verification commands

- Go 1.25 as declared by `go.mod`, CI, README, and Makefile.
- Existing Go dependencies and `httptest` fixtures; no new runtime dependency unless a measured gap justifies it.
- Go formatting: `gofmt -w` and `go vet ./...`.
- Tests: `go test -count=1 ./...` with a writable task-local `GOCACHE` if the host Go cache is unavailable.
- Lint: repository-pinned `golangci-lint` v2 configuration validated with the available v2 binary.
- CLI smoke tests: build `rewind`, run every demo scenario, save a bundle, import/replay it, and verify the archive contents.
- UI verification: server endpoint tests plus a browser smoke test when the local browser tool is available; otherwise document the unavailable visual check explicitly.

---

## Phase 0 — Baseline and branch safety

- [x] Record `git status`, tool versions, package test results, `go vet`, build status, lint status, demo output, and bundle contents in `docs/verification/production-readiness-baseline.md`.
- [x] Keep the original checkout untouched and confirm this branch contains only intentional implementation changes.
- [x] Inspect the original uncommitted CI/lint/spec changes and reapply their valid intent deliberately rather than copying malformed configuration blindly.

## Phase 1 — Toolchain, lint, and repository contract

- [x] Align `go.mod`, `README.md`, `Makefile`, Dockerfile, GitHub Actions, and release metadata on one supported Go version and one reproducible lint version.
- [x] Replace the invalid mixed golangci-lint v1/v2 configuration with a schema-valid v2 configuration. Keep the enabled analyzers narrow enough to be actionable, but do not hide newly introduced production defects with broad exclusions.
- [x] Add a small `make verify` target that runs format check, vet, tests, lint, and build in a predictable order.
- [x] Add `.gitignore` entries for generated local binaries, caches, coverage files, and downloaded tooling; do not ignore source, fixtures, documentation, or UI assets.
- [ ] Add a repository hygiene check that rejects tracked/generated artifacts and detects stale references to unsupported Go or lint versions.

Tests and gates:

- [ ] `go test -count=1 ./...`
- [ ] `go vet ./...`
- [ ] `golangci-lint config verify`
- [ ] `golangci-lint run ./...`
- [ ] `go build ./cmd/rewind`

## Phase 2 — Canonical identity and topology correctness

- [x] Add `internal/model/identity.go` with constructors and parsers for canonical entity IDs. Make normalisation idempotent and reject ambiguous identifiers rather than guessing.
- [x] Add table-driven model tests covering namespaces, names containing dashes, legacy IDs, malformed IDs, and round trips.
- [x] Update demo data, Kubernetes translation, Prometheus translation, and any correlation helpers to use the canonical constructors.
- [x] Extend the topology model with explicit call/relationship edges where required by RW005, while retaining ownership edges for Kubernetes hierarchy.
- [x] Add topology tests for pod→deployment→service, pod→node, and upstream→downstream call paths, including disconnected and duplicate-edge cases.
- [x] Make graph construction and adjacency traversal deterministic.

Tests and gates:

- [ ] New identity and topology tests fail before implementation, then pass after implementation.
- [ ] Existing correlation tests pass with canonical IDs only.
- [ ] `go test -count=1 ./internal/model ./internal/analyze/...`

## Phase 3 — Collector contract and deterministic runner

- [x] Add a source result/status contract test using fake collectors for success, partial data plus error, empty failure, timeout, cancellation, and panic-free concurrent execution.
- [x] Update `internal/sources/runner.go` to classify `ok`, `partial`, `failed`, and `skipped` correctly, preserve source endpoint/error provenance, and sort reports/raw-source keys deterministically.
- [ ] Ensure nil result slices/maps are safe and avoid retaining mutable collector-owned memory.
- [ ] Define and test how an investigation’s overall exit status is derived from source reports without turning one unavailable source into a false incident or a false clean bill of health.

Tests and gates:

- [ ] `go test -count=1 ./internal/sources/... ./internal/cli/...`
- [ ] Add an integration test proving a failed source is visible in the exported incident and terminal/JSON output.

## Phase 4 — Prometheus collection and raw replay fixtures

- [ ] Add failing HTTP fixture tests for scoped queries with namespaces, services, labels, empty namespace scope, multiple namespaces, extra query templates, malformed responses, and non-2xx responses.
- [x] Fix query-template selection so `EntityKinds` is honoured and default queries do not silently disappear when namespace scope is omitted.
- [x] Centralise PromQL label escaping and selector construction; do not produce empty selectors, duplicate matchers, or injection-prone raw interpolation.
- [x] Ensure every collected series has a canonical entity ID, stable signal ID, sorted points, and source metadata sufficient to explain the query.
- [x] Replace metadata-only `RawFixture` values with a versioned fixture envelope containing request identity and the response payloads needed for offline replay, while retaining backward compatibility for schema v1 imports.
- [x] Add tests proving a saved Prometheus fixture can be replayed without a network client.

Tests and gates:

- [ ] Prometheus collector unit and `httptest` integration tests pass.
- [ ] `go test -count=1 ./internal/sources/prometheus`

## Phase 5 — Kubernetes, logs, traces, CI/CD, and alerts

- [ ] Add Kubernetes fixture tests for deployment identity, owner references, pod/service association, namespace filtering, event classification, node pressure, eviction, probe failure, OOMKill, and restart events.
- [x] Stop ignoring namespace discovery and collection errors; return partial results with actionable source reports.
- [x] Replace substring-only scope matching with structured namespace/service/label matching.
- [ ] Add Loki/Tempo/CI/CD/Alertmanager fixture assertions for stable entity IDs, deep links, pagination/truncation behavior, and partial failures.
- [x] Translate Tempo service-call edges into topology edges consumed by analysis, and test RW005 with a trace fixture.
- [x] Make every source normalize timestamps to UTC and sort output before returning.

Tests and gates:

- [ ] `go test -count=1 ./internal/sources/kubernetes ./internal/sources/loki ./internal/sources/tempo ./internal/sources/cicd ./internal/sources/alertmanager`
- [ ] An end-to-end fixture test demonstrates a Kubernetes deployment, Prometheus anomaly, and trace edge joining into one incident.

## Phase 6 — Analysis quality and determinism

- [x] Add a determinism test that runs `analyze.Run` repeatedly on the same incident and compares canonical JSON bytes.
- [x] Add stable sorting/tie-breakers for entities, events, signals, change-points, hypotheses, chain links, and rule IDs.
- [x] Make rule metadata the single source of truth and reconcile `RW001`–`RW010` names/descriptions/docs with executable behavior. Do not leave README and rule pages describing different rules.
- [ ] Fix topology-aware rule joins to use canonical identity and explicit graph relationships rather than exact source-specific IDs or ignored label conventions.
- [ ] Preserve no-trigger semantics: alert evidence alone must never create a causal trigger, and partial source availability must be visible in the explanation.
- [ ] Expand golden incidents to at least ten deterministic cases covering positive, negative, partial-source, cross-entity, and replay scenarios.

Tests and gates:

- [ ] Rule-level tests cover every rule’s positive and negative path.
- [ ] Golden scenario outputs are reviewed and stored as stable JSON/Markdown fixtures.
- [ ] `go test -count=1 ./internal/analyze/... ./internal/e2e`

## Phase 7 — Bundle format and actual offline replay

- [ ] Add failing tests for archive layout, deterministic entry names, schema validation, unknown-field compatibility, corruption, missing fixtures, and path traversal protection.
- [x] Make `bundle.Export` include incident metadata and all available raw source fixtures with explicit content type/version metadata.
- [x] Make import validate the archive before exposing data to analysis; reject unsafe paths and malformed JSON with useful errors.
- [x] Implement replay as a fixture-backed collection path that reconstructs the incident from recorded source data and reruns analysis, rather than merely re-analyzing the already-derived incident snapshot.
- [x] Ensure demo `--save` produces the same complete replayable shape promised by the documentation.

Tests and gates:

- [ ] `go test -count=1 ./internal/bundle ./internal/cli`
- [ ] CLI smoke: save, list archive entries, replay offline, and compare deterministic verdict output.

## Phase 8 — API and UI rehabilitation

- [ ] Add an API contract test for the incident payload, source-status payload, error responses, and static asset fallback.
- [x] Refactor the embedded UI into named, maintainable sections: incident header, verdict summary, source health, timeline scrubber, entity lanes, signal cards, and evidence details.
- [x] Make the primary workflow usable without decorative noise: visible hierarchy, keyboard focus, responsive layout, accessible colors, empty/partial/error states, and useful deep links.
- [x] Add stable iconography using a consistent local SVG/icon set or text-safe symbols; do not introduce arbitrary external assets or broken remote URLs.
- [x] Render all user/source text safely as text content; never interpolate untrusted data into executable HTML.
- [ ] Add UI fixtures for no-trigger, partial-source, long-event, empty-signal, and many-entity incidents.
- [ ] Perform browser smoke verification if available and record screenshots or explicitly record the environment limitation.

Tests and gates:

- [ ] `go test -count=1 ./internal/server`
- [ ] Static UI asset inspection and API contract tests pass.
- [ ] Manual/browser checklist is recorded in `docs/verification/ui-checklist.md`.

## Phase 9 — Documentation, community readiness, and release evidence

- [x] Rewrite README claims to describe the verified public-alpha scope, limitations, supported sources, replay guarantees, and failure semantics.
- [x] Synchronize `Incident_Replay_Engine.md`, architecture docs, source docs, rule pages, bundle spec, walkthrough, and configuration reference with code.
- [x] Add `CONTRIBUTING.md`, issue templates, a security policy, a changelog/release process, and a reproducible demo walkthrough.
- [ ] Add a generated or checked rule/source catalog so documentation drift is caught in CI.
- [ ] Add release metadata and an artifact smoke test for Linux/macOS/Windows builds when the local toolchain supports them.
- [ ] Use the HertzBeat repository only as a quality reference for documentation/UI structure; do not copy code or assets without compatible licensing and attribution.

Tests and gates:

- [ ] README quick-start commands work from a clean checkout.
- [ ] Every documented demo scenario exists and produces the documented result.
- [ ] `make verify` passes in the isolated branch.

## Final review and handoff

- [x] Run the full verification matrix after all implementation work, with fresh command output captured.
- [x] Review the diff for accidental generated files, secrets, broad lint suppressions, dead code, duplicated contracts, and unsupported product claims.
- [x] Produce `docs/verification/production-readiness-report.md` containing: implemented changes, evidence commands/results, remaining limitations, known unsupported cases, UI verification status, and a precise PR summary.
- [ ] Do not claim production readiness or millions of users. The honest target for this branch is a trustworthy, testable public alpha with a clear path to production hardening.
