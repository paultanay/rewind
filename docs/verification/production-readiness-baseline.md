# Production-readiness baseline

Date: 2026-08-17  
Branch: `feat/production-readiness`  
Commit under test: `8a5ca83`

## Environment

- OS: Windows PowerShell
- Go: `go1.25.6 windows/amd64`
- Repository state at baseline: clean apart from the implementation plan created on this branch.
- The original `main` checkout contains separate uncommitted user files and was not modified.

## Verification results

| Check | Result | Evidence |
|---|---|---|
| Full Go tests | PASS | `go test -count=1 ./...` — all packages passed |
| Vet | PASS | `go vet ./...` |
| Build | PASS | `go build ./cmd/rewind` |
| Linter availability | FAIL | `golangci-lint` is not on PATH; repository-local v2.12.2 binary was used for validation |
| Linter config | FAIL | v2.12.2 reports `unsupported version of the configuration: ""` for the committed v1 configuration |

## CLI/demo observations

All five documented demo scenarios executed without a process failure:

- `bad-deploy`: HIGH RW001 verdict.
- `oom-cascade`: OOM evidence and RW009 crash-loop hypothesis.
- `node-pressure`: RW006 speculative verdict.
- `cpu-throttle`: RW004 speculative verdict.
- `false-positive`: no clear trigger and notable anomalies only.

The saved `bad-deploy` bundle was inspected with `tar -tzf`. It contained only:

```text
incident.json
```

No `sources/*.json` entries were produced by the demo, despite the bundle and README contracts describing raw source fixtures and offline replay. This is a confirmed product contract gap, not an assumption.

## Baseline conclusion

The current branch is buildable and has useful analysis behavior, but it is not yet a trustworthy public alpha. The first implementation slice must fix the lint/toolchain contract and establish tests around deterministic source status and replay artifacts before expanding the UI or source surface.

