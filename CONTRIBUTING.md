# Contributing to Rewind

Thanks for helping improve Rewind. Start with an issue for substantial behavior
changes so the incident contract and replay guarantees stay explicit.

## Development

Requirements: Go 1.25 or newer.

```bash
go test -count=1 ./...
go vet ./...
make verify
```

Keep changes small and explain operational behavior in tests. New collectors
must translate native data into `internal/model`, report partial failures,
use canonical entity IDs, sort their output, and add an offline fixture test
when replay behavior changes.

Do not commit credentials, generated binaries, incident bundles, or local
configuration. Frontend dependencies belong in `web/package-lock.json`, not in
`web/node_modules`.

Pull requests should include:

- the user-visible or operational behavior that changed;
- tests and verification commands actually run;
- fixture or bundle compatibility considerations; and
- known limitations and follow-up work.

Keep commit messages factual and scoped. Avoid claiming production readiness,
root-cause certainty, or performance characteristics without evidence.
