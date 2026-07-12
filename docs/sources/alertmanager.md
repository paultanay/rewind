# Source: Alertmanager

Rewind queries Alertmanager using its **REST API v2** in read-only mode,
pulling alerts that fired within the investigation window. Alerts are
used as **corroborating evidence** for correlation rules — never as
root-cause triggers (see rule RW010).

---

## Configuration

```yaml
# rewind.yaml
alertmanager:
  url: http://alertmanager.monitoring.svc:9093
  username: ""
  password: ""
  token: ""
  insecure_skip_verify: false
  timeout: 15s
```

Environment overrides: `REWIND_ALERTMANAGER_URL`, `REWIND_ALERTMANAGER_TOKEN`

---

## What Rewind collects

```
GET /api/v2/alerts?active=true&silenced=false&inhibited=false&startTime=<from>&endTime=<to>
```

Each alert is converted to a `model.Event`:
- `Kind: AlertFired` — firing alert with severity label
- `Kind: AlertResolved` — resolved alert (included for timeline context)

Alert labels (`alertname`, `namespace`, `service`, `severity`) are used to
infer the target entity ID and severity level.

---

## Design invariant: alerts are evidence, not causes

Per **RW010** (enforced invariant):

> An alert is always a *symptom* or *corroboration* of a causal chain, never
> a root-cause trigger in itself. Rewind will never produce a hypothesis
> whose trigger is an alert event.

This prevents the trivially unhelpful verdict: *"the system broke because
an alert fired."* Alerts add score weight to hypotheses already grounded
in metric change-points or deployment events.

---

## Correlation rules

| Rule | Usage |
|---|---|
| RW001 | AlertFired after deploy boosts deploy-trigger score |
| RW003 | AlertFired after OOMKill confirms cascade severity |
| RW010 | Enforces alerts-as-evidence invariant across all rules |
