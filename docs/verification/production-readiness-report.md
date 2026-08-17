# Production-readiness implementation report

Date: 2026-08-17
Branch: feat/production-readiness

## Outcome

This branch upgrades Rewind from an inconsistent technical prototype to a
testable public-alpha foundation. It does not claim production readiness,
internet-scale reliability, or a mature hosted product.

## Implemented

- Canonical entity identity with strict legacy-ID normalization.
- Deterministic topology construction, Tempo call-edge consumption, and
  correlation tie-breakers.
- Correct ok/partial/failed source status classification.
- Prometheus scope/query filtering, escaping, stable IDs, and deterministic
  output.
- Kubernetes partial-error propagation and structured service scope matching.
- Versioned source-result fixtures, safe bundle paths, and offline replay that
  rebuilds analysis input from fixtures.
- Rebuilt embedded UI with verdict summary, source health, replay scrubber,
  evidence timeline, entity lanes, sparklines, responsive layout, and safe
  text rendering.
- Valid golangci-lint v2 configuration, make verify, CI alignment, and
  contributor/security/release documentation.

## Verification evidence

The following passed on this branch:

- go test -count=1 ./...
- go vet ./...
- pinned golangci-lint-2.12.2-windows-amd64.exe config verify
- pinned golangci-lint-2.12.2-windows-amd64.exe run ./... --timeout 5m
- go build -o <temporary path> ./cmd/rewind
- all five demo scenarios: bad-deploy, oom-cascade, node-pressure,
  cpu-throttle, and false-positive
- saved demo archive contains incident.json and sources/demo.json
- replayed saved bundle offline and parsed JSON output successfully
- embedded UI JavaScript node --check
- git diff --check

The Docker practical test also passed from a clean Compose stack:

- checkout -> payments load path generated healthy and failure telemetry.
- Prometheus returned 5 signals for checkout.
- Alertmanager returned the injected critical alert event.
- Loki and Tempo endpoints were reachable.
- The current local harness validates Tempo reachability but does not yet
  inject a production-compatible trace payload; Tempo signals remain a
  follow-up integration test.
- The exported archive was replayed after all Compose containers were stopped.
- Live and replay entity/event/signal counts matched.
- The result correctly reported no trigger because this scenario contained no
  deployment or CI/CD trigger; alert evidence alone did not create a cause.

## Known limitations

- Visual browser verification was unavailable; see ui-checklist.md.
- Source fixtures currently store normalized collector results plus optional
  raw metadata. They are replayable, but not yet a faithful capture of every
  native Prometheus/Loki/Tempo HTTP response.
- Several live-source event IDs remain generated per collection, so identical
  live runs are not globally byte-identical even though analysis ordering is
  deterministic.
- The UI is still a single embedded HTML asset without a frontend build
  pipeline.
- Testdata is not yet a broad ten-plus golden corpus.
- Kubernetes, CI/CD, logs, traces, and alert integrations still need
  production-scale pagination, authentication, rate-limit, and contract
  testing against real service versions.

## PR summary

Recommended PR title: feat: harden incident collection and offline replay

The PR should present this as a public-alpha reliability and trust milestone,
with the limitations above stated plainly.
