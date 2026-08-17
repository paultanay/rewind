# Operations

This page covers the operational boundaries of the local CLI and the review
practices around exported incidents.

## CI and exit codes

Rewind returns a non-zero exit code for a completed investigation with a
critical finding so it can gate an automated workflow. A non-zero code does not
mean the process crashed.

| Code | Meaning | Typical action |
| --- | --- | --- |
| 0 | Complete without critical findings | Continue the workflow |
| 1 | Complete with critical/high-confidence evidence | Stop or require review |
| 2 | Usage/configuration error | Fix invocation or config |
| 3 | All configured sources failed | Restore access and retry |
| 4 | Unexpected internal error | Preserve logs and report a bug |

Use JSON output for automation and `.rewind` bundles for handoff. Do not parse
terminal prose as an API.

## Credentials and sensitive data

- Prefer environment variables or a secret manager over committing credentials.
- Use read-only source tokens with the smallest available scope.
- Never include `rewind.yaml`, local `.env` files, or generated bundles in a
  public issue without review.
- Treat bundle contents as sensitive: service names, URLs, alert labels, log
  excerpts, and trace references may reveal internal infrastructure.
- Delete temporary bundles after the postmortem retention period.

The local UI binds to loopback. It is not an authentication boundary and must
not be reverse-proxied to a shared network without adding an explicit access
control layer.

## Source failures

Run `rewind sources` before an investigation. If a source is partial, keep the
investigation result but record the error and decide whether the missing data
could change the conclusion. Retry only after correcting the underlying
timeout, endpoint, credentials, or scope problem.

## Upgrades

Before upgrading a binary:

1. retain a representative bundle from the current version;
2. run `rewind investigate --replay` with the new binary;
3. compare source reports, event/signal counts, and ranked hypotheses; and
4. record any rule or bundle-schema change in the release notes.

The bundle schema is versioned. A reader must reject a newer incompatible
schema rather than silently dropping required fields.

## Troubleshooting

```bash
rewind version
rewind sources --config rewind.yaml
rewind explain
rewind import incident.rewind
rewind investigate --replay incident.rewind --format json
```

If the UI cannot load, first verify that the bundle is readable with `rewind
import`, then run the server on another local port. If all sources fail, check
DNS/network access and credentials before interpreting the empty result.
