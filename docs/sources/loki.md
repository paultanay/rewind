# Source: Loki

Rewind queries Loki using the **LogQL HTTP API** in read-only mode —
`/loki/api/v1/query_range` only. It detects log error bursts that
corroborate other causal signals.

---

## Configuration

```yaml
# rewind.yaml
loki:
  url: http://loki.monitoring.svc:3100
  username: ""   # Grafana Cloud: org name
  password: ""   # Grafana Cloud: API key
  token: ""      # Bearer token alternative
  insecure_skip_verify: false
  timeout: 30s
```

Environment overrides: `REWIND_LOKI_URL`, `REWIND_LOKI_TOKEN`

---

## What Rewind collects

Rewind issues a rate-of-error-logs query per service:

```logql
sum by (service_name) (
  rate({namespace="<ns>", service_name=~"<svc>"} |= "error" [1m])
)
```

High-rate windows (≥5 errors/s sustained for ≥2 minutes) are converted
to `model.Event` entries with `Kind: LogBurst`. The raw log lines are
sampled (at most 20 representative lines) and stored in `Event.Detail`.

---

## Correlation rules

| Rule | Usage |
|---|---|
| RW001 | Log error burst after deploy corroborates the deploy-trigger hypothesis |
| RW008 | Trace error spike + log burst → RCA of service cascade |
