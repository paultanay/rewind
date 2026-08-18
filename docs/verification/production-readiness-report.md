# Production-readiness implementation report

Date: 2026-08-18
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
  evidence timeline, entity lanes, sparklines, responsive layout, accessible
  focus states, and safe text rendering. The UI now has a TypeScript/Vite source
  tree and a deterministic embedded build.
- Branded README, architecture/investigation diagrams, public evaluation guide,
  operations guidance, repository hygiene checks, and Apache-2.0 metadata.

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
- npm --prefix web ci
- npm --prefix web run typecheck
- npm --prefix web test
- npm --prefix web run build
- nested `REWIND_*` configuration override regression test
- scripts/check-repository.ps1
- scripts/check-docs.ps1
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
- The run used `testdata/practical/run.ps1 -PortOffset 1000` because the
  persistent local distributed test stack already occupied the default ports.
- The run produced 1 entity, 1 alert event, and 5 signals, exported the bundle,
  stopped the observability containers, and printed `OFFLINE_REPLAY_OK`.

## Known limitations

- Source fixtures currently store normalized collector results plus optional
  raw metadata. They are replayable, but not yet a faithful capture of every
  native Prometheus/Loki/Tempo HTTP response.
- Several live-source event IDs remain generated per collection, so identical
  live runs are not globally byte-identical even though analysis ordering is
  deterministic.
- Visual browser verification remains a manual follow-up because no browser
  automation target is available in this environment.
- Testdata is not yet a broad ten-plus golden corpus.
- Kubernetes, CI/CD, logs, traces, and alert integrations still need
  production-scale pagination, authentication, rate-limit, and contract
  testing against real service versions.

## PR summary

Recommended PR title: feat: establish a credible Rewind public-alpha baseline

The PR should present this as a public-alpha reliability and trust milestone,
with the limitations above stated plainly.
