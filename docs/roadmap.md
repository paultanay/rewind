# Rewind v2.0 Roadmap

Items explicitly out-of-scope for v1.0. See `Incident_Replay_Engine.md §1 Non-goals`.

## v2 Candidates

- **Flight-recorder mode** — continuous background collection writing rolling bundles
- **LLM narration layer** — natural-language postmortem generation on top of deterministic verdicts
- **Elasticsearch / OpenSearch** source adapter
- **Datadog / New Relic** read-only API adapter
- **Multi-incident diffing** — compare two bundles to spot regressions
- **eBPF network graph** — enrich topology with live connection data
- **Hosted bundle storage** — team-shared incident library with RBAC
- **`rewind watch` alert integration** — trigger investigation automatically on Alertmanager webhook
- **Grafana panel embed** — render Rewind verdicts inline in existing dashboards

## Known Limitations (v1.0)

- `rewind demo` uses synthetic offline scenarios; the `kind` cluster live demo path is v2
- CPU throttle and node-pressure confidence currently SPECULATIVE — more signal sources improve this
- No Windows Kubernetes credential helper; KUBECONFIG must be explicit on Windows
