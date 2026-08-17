# Practical Distributed Test Implementation Plan

Goal: provide a reproducible Docker Compose test that exercises Rewind against a small distributed application and an observability stack, then verifies offline replay.

Architecture: a Compose project runs checkout and payments services, Prometheus, Alertmanager, Loki, and Tempo. A deterministic test script generates telemetry, runs Rewind against reachable source endpoints, saves a bundle, replays it without network access, and checks expected source/report/archive behavior. The harness is isolated under testdata/practical and does not change production runtime code.

Tech stack: Docker Compose, Go demo services, Prometheus configuration, Loki/Tempo/Alertmanager configuration, and PowerShell execution on the current host.

---

### Task 1: Fix and verify CI workflow

Files: .github/workflows/ci.yml

- [x] Change golangci/golangci-lint-action from v6 to v7 because the recorded CI log rejects v2.12.2 under v6.
- [x] Set default workflow permissions to contents: read; grant release-only write permissions to the release job.
- [x] Push and confirm lint, test, and build checks complete.

### Task 2: Add the practical stack

Files: testdata/practical/docker-compose.yml, Prometheus, Alertmanager, Loki, Tempo, and service files.

- [x] Define a bounded Compose network with health checks and fixed local ports.
- [x] Run two application containers with stable service labels and a controllable failure mode.
- [x] Configure Prometheus scraping and source endpoints required by Rewind.
- [x] Configure Alertmanager, Loki, and Tempo as minimal local test dependencies.

### Task 3: Add repeatable test execution

Files: testdata/practical/run.ps1 and testdata/practical/README.md

- [x] Start and health-check the stack.
- [x] Trigger the failure scenario and wait for observable telemetry.
- [x] Run Rewind with a test configuration and save a bundle.
- [x] Verify archive entries, source reports, canonical IDs, and collected evidence.
- [x] Replay the bundle with network access disabled and compare the replay result.
- [x] Provide cleanup commands and Docker Desktop troubleshooting.

### Task 4: Run and document evidence

Files: docs/verification/production-readiness-report.md

- [x] Run the full Compose scenario locally.
- [x] Record exact commands, outcomes, and limitations.
- [ ] Commit and push the harness and CI fix.
- [ ] Confirm the PR checks pass.
