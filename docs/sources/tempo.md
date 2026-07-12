# Source: Tempo

Rewind queries Grafana Tempo using the **Tempo HTTP API v2** in read-only
mode. It collects **trace summaries only** — no full trace spans are stored
in the bundle (respecting spec §4: no telemetry storage).

---

## Configuration

```yaml
# rewind.yaml
tempo:
  url: http://tempo.monitoring.svc:3200
  username: ""
  password: ""
  token: ""
  insecure_skip_verify: false
  timeout: 30s
```

Environment overrides: `REWIND_TEMPO_URL`, `REWIND_TEMPO_TOKEN`

---

## What Rewind collects

Rewind issues tag-search queries to find traces with error spans:

```
GET /api/search?tags=status.code%3D2&start=<from>&end=<to>&limit=50
```

For each matched trace, Rewind reads the root span to extract:
- Service name, operation name, duration
- HTTP status code / gRPC code

Traces with error status are converted to `model.Event (Kind: TraceErrorSpike)`
and a `model.Signal (metric: trace.error.rate)` is synthesized per service.

Tempo also enriches the **entity topology graph** with service call edges
(`caller → callee`), which improves ProximityScore calculations in all rules.

---

## Correlation rules

| Rule | Usage |
|---|---|
| RW008 | Trace error spike co-occurring with log burst → cascade RCA |
| All rules | Improved ProximityScore from call-graph edges |
