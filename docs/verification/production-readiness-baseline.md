# Historical production-readiness baseline

This document records the baseline checks performed before Rewind's first
public-alpha hardening pass. It is retained as historical engineering
evidence; the current project scope and limitations are described in the
README and [roadmap](../roadmap.md).

Date: 2026-08-17  
Commit under test: `8a5ca83`

## Environment

- OS: Windows PowerShell
- Go: `go1.25.6 windows/amd64`
- Repository state at baseline: clean

## Verification results

| Check | Result | Evidence |
| --- | --- | --- |
| Full Go tests | PASS | `go test -count=1 ./...` - all packages passed |
| Vet | PASS | `go vet ./...` |
| Linter availability | FAIL | `golangci-lint` was not on PATH; a repository-local v2.12.2 binary was used for validation |
| Linter config | FAIL | v2.12.2 reported an unsupported empty configuration version |

## CLI and demo observations

All five documented demo scenarios executed without a process failure:

- `bad-deploy`: HIGH RW001 verdict.
- `oom-cascade`: OOM evidence and RW009 crash-loop hypothesis.
- `node-pressure`: RW006 speculative verdict.
- `cpu-throttle`: RW004 speculative verdict.
- `false-positive`: no clear trigger and notable anomalies only.

The saved `bad-deploy` bundle was inspected with `tar -tzf`. It contained only
`incident.json`; no `sources/*.json` entries were produced by the demo even
though the bundle and README contracts described source fixtures and offline
replay. This was a confirmed product contract gap at that baseline.

## Baseline conclusion

The baseline was buildable and had useful analysis behavior, but it was not yet
ready for public evaluation. The subsequent hardening work addressed the lint
and toolchain contract, source-status behavior, replay artifacts, and public
documentation before the public-alpha release.
