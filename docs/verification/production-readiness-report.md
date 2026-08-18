# Public-alpha verification report

Date: 2026-08-18

## Scope

This report records the verification work that established Rewind's public
alpha foundation. It is evidence of the checks performed at that point, not a
claim of production readiness, internet-scale reliability, or a mature hosted
product.

## Implemented

- Canonical entity identity with strict legacy-ID normalization.
- Deterministic topology construction, Tempo call-edge consumption, and
  correlation tie-breakers.
- Correct `ok`/`partial`/`failed` source-status classification.
- Prometheus scope/query filtering, escaping, stable IDs, and deterministic
  output.
- Kubernetes partial-error propagation and structured service-scope matching.
- Versioned source-result fixtures, safe bundle paths, and offline replay that
  rebuilds analysis input from fixtures.
- Embedded UI with a verdict summary, source health, replay scrubber, evidence
  timeline, entity lanes, responsive layout, accessible focus states, and safe
  text rendering.
- Public README, architecture and investigation diagrams, operational guidance,
  repository hygiene checks, and Apache-2.0 metadata.

## Verification evidence

The following checks passed during the public-alpha hardening work:

- `go test -count=1 ./...`
- `go vet ./...`
- pinned `golangci-lint` v2.12.2 configuration and repository checks
- `go build ./cmd/rewind`
- all five demo scenarios: `bad-deploy`, `oom-cascade`, `node-pressure`,
  `cpu-throttle`, and `false-positive`
- saved demo archive inspection and offline replay
- embedded UI JavaScript syntax validation
- frontend install, typecheck, test, and production build
- nested `REWIND_*` configuration override regression testing
- repository hygiene and documentation checks
- `git diff --check`

The Docker practical test also passed from a clean Compose stack:

- checkout-to-payments traffic generated healthy and failure telemetry;
- Prometheus returned five signals for checkout;
- Alertmanager returned the injected critical alert;
- Loki and Tempo endpoints were reachable;
- the exported archive replayed after the Compose containers were stopped;
- live and replay entity, event, and signal counts matched; and
- the run printed `OFFLINE_REPLAY_OK`.

The practical harness validates Tempo reachability but does not yet inject a
production-compatible trace payload. That remains an integration-test gap.

## Known limitations

- Source fixtures store normalized collector results plus optional raw metadata;
  they are replayable but are not a faithful capture of every native
  Prometheus, Loki, or Tempo HTTP response.
- Several live-source event IDs are generated per collection, so identical live
  runs are not globally byte-identical even though analysis ordering is
  deterministic.
- The committed UI image is a manual practical-validation capture. Automated
  browser interaction testing remains a follow-up.
- The testdata corpus is not yet a broad ten-plus golden corpus.
- Kubernetes, CI/CD, logs, traces, and alert integrations still need
  production-scale pagination, authentication, rate-limit, and contract tests
  against real service versions.

The current release and future work should continue to state these boundaries
plainly.
