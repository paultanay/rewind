# Roadmap

This roadmap describes work beyond the current public-alpha foundation. The
project is intentionally focused on portable incident evidence, deterministic
analysis, and offline replay before adding hosted or autonomous features.

## Current foundation

- Read-only collection from the supported observability and delivery sources.
- A canonical incident model with deterministic analysis and ranked evidence.
- Portable `.rewind` bundles that can be inspected and replayed offline.
- CLI, Markdown, JSON, and local web interfaces over the same incident model.
- A practical Docker validation stack and documented operational boundaries.

## Next milestones

### Reliability and compatibility

- Expand the golden fixture corpus beyond the current demonstration scenarios.
- Test pagination, authentication, rate limits, and API contracts against real
  service versions.
- Capture more faithful native source responses while preserving safe replay.
- Add release checks for bundle compatibility and representative upgrade paths.

### Investigation workflow

- Compare multiple incident bundles to identify recurring failure patterns.
- Improve evidence provenance and source-gap reporting for incomplete windows.
- Add more explicit operator guidance for speculative versus corroborated
  hypotheses.

### Integrations

- Add carefully scoped adapters for Elasticsearch/OpenSearch, Datadog, and
  New Relic where their read-only contracts can be tested and maintained.
- Add a `rewind watch` integration for Alertmanager-driven investigation
  requests without granting Rewind write access to production systems.

## Longer-term possibilities

These ideas remain out of the current scope and require separate design work:

- continuous flight-recorder mode with rolling bundles;
- hosted bundle storage with authentication and role-based access control;
- Grafana panel integration;
- eBPF-assisted network topology;
- optional natural-language narration on top of deterministic evidence.

## Current limitations

- The demo scenarios use synthetic offline data; the practical stack is a
  validation harness, not a claim of production equivalence.
- Some rule outputs are intentionally marked speculative when corroborating
  evidence is limited.
- The local UI is loopback-only and has no authentication boundary.
- Source adapters still need broader production-scale pagination, rate-limit,
  authentication, and contract testing.
- The project does not provide a hosted control plane, storage service, agent,
  or automatic remediation.
