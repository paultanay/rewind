# Practical distributed test

This is a local black-box test of Rewind against a small distributed system:

```text
load generator -> checkout -> payments
                      |          |
                 Prometheus   Loki
                 Alertmanager Tempo (health/search)
```

The test creates a healthy baseline, injects a payments failure, pushes a
critical Alertmanager alert, collects the resulting Prometheus/Loki/alert
evidence, exports a bundle, stops the stack, and replays the bundle offline.

## Run

From the repository root in PowerShell:

```powershell
docker version
./testdata/practical/run.ps1

# If another local stack owns the default ports, use an isolated range.
./testdata/practical/run.ps1 -PortOffset 1000
```

The run takes roughly 3 minutes because Prometheus needs a baseline and a
post-failure interval. Use `-Keep` to leave containers running for inspection.

## What success means

- Rewind reaches Prometheus, Alertmanager, Loki, and Tempo.
- Prometheus returns at least one usable signal.
- Alertmanager returns the injected alert event.
- The bundle contains incident.json and source fixtures.
- Replay succeeds after all live containers are stopped.
- Live and replay entity/event/signal counts match.

This scenario intentionally has no deployment or CI/CD trigger. Therefore a
causal verdict is not expected: alert evidence alone must not invent a cause.
Use `rewind demo --scenario bad-deploy` separately to verify the causal-rule
path and high-confidence rendering.

## Inspect manually

While running (with the default ports):

- Checkout: http://localhost:18080/health
- Prometheus: http://localhost:19090/graph
- Alertmanager: http://localhost:19093
- Loki: http://localhost:13100/ready
- Tempo: http://localhost:13200/api/echo

With `-PortOffset 1000`, add 1000 to each port in these URLs.

To inspect the saved artifacts, use the temporary directory printed by the
script. The generated bundle is deliberately not written into the repository.

## Troubleshooting

If Docker reports a daemon error, start Docker Desktop with the Linux engine
and retry. If an old run left containers behind:

```powershell
docker compose -f testdata/practical/docker-compose.yml down --remove-orphans -v
```
