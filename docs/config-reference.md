# Configuration Reference

All configuration lives in `rewind.yaml`. Rewind searches for it in:
1. Path from `--config` flag
2. `./rewind.yaml` (current directory)
3. `~/.config/rewind/rewind.yaml`

Every field can also be set via an environment variable with the `REWIND_` prefix
(e.g. `REWIND_PROMETHEUS_URL`). CLI flags take highest precedence.

---

## Full schema

```yaml
# Per-source connection timeout (Go duration string).
source_timeout: 15s

prometheus:
  url: http://localhost:9090       # Required to enable source
  disabled: false
  headers:                         # Optional extra HTTP headers (e.g. auth)
    Authorization: "Bearer <token>"

kubernetes:
  kubeconfig: ""                   # Path to kubeconfig; "" = in-cluster config
  context: ""                      # kubeconfig context to use; "" = current
  disabled: false

loki:
  url: http://localhost:3100       # Required to enable source
  tenant_id: ""                   # X-Scope-OrgID for multi-tenant Loki
  username: ""                    # Basic auth username
  password: ""                    # Basic auth password
  grafana_base_url: ""            # Used to build Explore deep-links in events
  max_sample_lines: 5             # Log lines to attach per burst event (0–20)
  disabled: false

tempo:
  url: http://localhost:3200       # Required to enable source
  tenant_id: ""                   # X-Scope-OrgID for multi-tenant Tempo
  username: ""                    # Basic auth username
  password: ""                    # Basic auth password
  grafana_base_url: ""            # Used to build Explore deep-links in events
  disabled: false

alertmanager:
  url: http://localhost:9093       # Required to enable source
  username: ""                    # Basic auth username
  password: ""                    # Basic auth password
  disabled: false

github:
  token: ""                       # GitHub PAT with repo:read scope
  repos:                          # List of org/repo to scan for deploys
    - my-org/my-service
  disabled: false

gitlab:
  url: https://gitlab.com         # GitLab instance URL
  token: ""                       # GitLab PAT with read_api scope
  projects:                       # List of namespace/project to scan
    - my-group/my-service
  disabled: false
```

---

## Minimal config (Prometheus + Kubernetes only)

```yaml
prometheus:
  url: http://prometheus.monitoring.svc:9090

kubernetes:
  context: production
```

---

## Grafana Cloud example

```yaml
prometheus:
  url: https://prometheus-prod-01-eu-west-0.grafana.net/api/prom
  headers:
    Authorization: "Bearer glc_..."

loki:
  url: https://logs-prod-eu-west-0.grafana.net
  username: "12345"
  password: "glc_..."
  grafana_base_url: https://my-stack.grafana.net

tempo:
  url: https://tempo-prod-01-eu-west-0.grafana.net
  username: "67890"
  password: "glc_..."
  grafana_base_url: https://my-stack.grafana.net

alertmanager:
  url: https://alertmanager-us-central-0.grafana.net
  username: "11111"
  password: "glc_..."
```

---

## Environment variables

Any config key can be set as `REWIND_<UPPER_SNAKE_CASE>`:

```bash
export REWIND_PROMETHEUS_URL=http://prometheus:9090
export REWIND_GITHUB_TOKEN=ghp_...
export REWIND_LOKI_PASSWORD=secret
export REWIND_SOURCE_TIMEOUT=30s
```

Nested keys use `_` as separator:
- `prometheus.url` → `REWIND_PROMETHEUS_URL`
- `loki.tenant_id` → `REWIND_LOKI_TENANT_ID`
- `kubernetes.kubeconfig` → `REWIND_KUBERNETES_KUBECONFIG`

---

## Connectivity check

After editing `rewind.yaml`, verify every source is reachable:

```bash
rewind sources
```

Output:
```
Configured sources:

  prometheus           ✓ ok
  kubernetes           ✓ ok
  loki                 ✓ ok
  tempo                ✗ unreachable  (connection refused)

Not configured / disabled:
  github               no credentials/endpoint in config
  alertmanager         disabled
```

Exit code 3 if any configured source is unreachable.
