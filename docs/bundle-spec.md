# Bundle Specification — `.rewind` format

Version: **1** (current)  
Implemented in: `internal/bundle/`

---

## Overview

A `.rewind` file is a **gzipped tar archive** that is a complete, portable,
self-contained incident record. It can be:

- Opened with `rewind ui` on any machine, with zero network access
- Replayed with `rewind investigate --replay` to re-run analysis on the
  original data with an updated version of rewind
- Diffed between incidents to understand regression patterns
- Attached to a postmortem issue as the complete evidence record

---

## File layout

```
incident.rewind          (gzip-compressed tar)
├── incident.json        required — model.Incident JSON
└── sources/             optional — raw source fixture data
    ├── prometheus.json
    ├── kubernetes.json
    ├── loki.json
    ├── tempo.json
    ├── github.json
    ├── gitlab.json
    └── alertmanager.json
```

---

## `incident.json` schema

The root object is `model.Incident` serialised as JSON with two-space
indentation for human readability.

### Required fields

| Field | Type | Description |
|---|---|---|
| `id` | string | Incident identifier (e.g. `inc-20260709-142000-a1b2c3d4`) |
| `window` | object | `{from: RFC3339, to: RFC3339}` |
| `meta.schemaVersion` | int | Must be `1` (current). Higher values will be rejected by older builds. |
| `meta.rewindVersion` | string | Semver of the tool that produced this bundle |
| `meta.createdAt` | RFC3339 | When the investigation ran |

### Optional fields

All other fields in `model.Incident` are optional. An empty `events` or
`signals` array is valid. A missing `verdict` means analysis has not run
(e.g. the bundle was exported mid-collection).

### Forward compatibility

- **Unknown fields are ignored on read.** Newer versions of rewind may add
  fields; older readers can still parse the known portion, but exporting with
  an older reader does not preserve fields it does not understand.
- **Schema version guard.** If `meta.schemaVersion` is greater than the
  reader's `CurrentSchemaVersion`, the reader must return an error and
  advise the user to upgrade.

---

## Size guidelines

Collectors enforce the following sampling rules to keep bundles portable:

| Data type | Limit |
|---|---|
| Signal points | ≤500 per signal (downsampled by collector) |
| Log sample lines | ≤20 per log burst event |
| Trace exemplars | ≤5 trace IDs per TraceErrorSpike event |
| Raw fixture data | Summarised; not raw logs/traces |

Typical bundle size: **1–5 MB** for a 45-minute incident window.

---

## Offline and reproducibility guarantees

Export → import → export **must** produce byte-identical output, with the
exception of `meta.createdAt` which reflects the time of each export.

This is guaranteed by:
- Deterministic JSON serialisation (sorted map keys via standard `encoding/json`)
- Fixed tar entry mod times (Unix epoch 0)
- Gzip best-compression level

The embedded UI reads `incident.json` through the local loopback server and does
not contact Prometheus, Loki, Tempo, Kubernetes, or any remote asset host while
viewing a bundle.

---

## CLI operations

```bash
# Export during investigation
rewind investigate --from 14:00 --to 14:45 -o incident.rewind

# Import and render
rewind import incident.rewind
rewind import incident.rewind --format md

# Re-export (upgrade schema or normalise)
rewind export incident.rewind incident-v2.rewind

# Replay analysis on bundle data
rewind investigate --replay incident.rewind

# Open web UI
rewind ui incident.rewind
```

---

## Versioning policy

The schema version is incremented only for **breaking changes** that prevent
older readers from correctly parsing the `incident.json`. Additive changes
(new optional fields) do not require a version bump.

Current breaking change log:

| Version | Changes |
|---|---|
| 1 | Initial format |
