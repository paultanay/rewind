# Contributing to Rewind

Thanks for helping improve Rewind. Start with an issue for substantial
behavior changes so the incident contract and replay guarantees stay explicit.

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
configuration. Pull requests should include the verification commands run and
call out known limitations.
