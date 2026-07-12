# Rewind — Enterprise Validation Walkthrough

> **Goal:** Run a fully realistic MAANG-grade distributed system inside Docker,
> break it in three ways, and validate Rewind reconstructs every incident correctly.
> This is exactly how an SRE at Google, Netflix, or Stripe would onboard the tool.

---

## What you're building

```
┌─────────────────────────────────────────────────────────────────┐
│  Docker network: rewind-lab                                     │
│                                                                 │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────────┐  │
│  │ frontend │→ │ checkout │→ │ payments │  │   postgres   │  │
│  │ :3000    │  │ :8080    │  │ :8081    │  │   :5432      │  │
│  └──────────┘  └──────────┘  └──────────┘  └──────────────┘  │
│       ↓metrics      ↓metrics      ↓metrics       ↓metrics     │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │  Prometheus :9090  │  Loki :3100  │  Tempo :3200         │  │
│  └──────────────────────────────────────────────────────────┘  │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │  Alertmanager :9093  │  Grafana :3001 (optional)         │  │
│  └──────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
```

Three microservices simulate a real e-commerce backend. You'll break it in three
ways and run `rewind investigate` after each to validate the verdict.

---

## Prerequisites

- Docker Desktop (already installed ✅)
- Go 1.23+ (already installed ✅)
- `rewind` binary built locally

---

## Step 1 — Create the lab directory

```powershell
mkdir "d:\Coding Files\Projects\rewind-lab"
cd "d:\Coding Files\Projects\rewind-lab"
```

---

## Step 2 — Build the mock microservices

Create `services/checkout/main.go`:

```go
// checkout service — simulates a real HTTP microservice with Prometheus metrics
package main

import (
    "fmt"
    "log"
    "math/rand"
    "net/http"
    "os"
    "time"

    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
    latency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
        Name:    "http_request_duration_seconds",
        Help:    "HTTP latency",
        Buckets: []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10},
    }, []string{"path", "status"})

    requests = prometheus.NewCounterVec(prometheus.CounterOpts{
        Name: "http_requests_total",
        Help: "Total HTTP requests",
    }, []string{"path", "status"})

    // Failure mode controlled by env var: INJECT_FAILURE=latency|errors|oom
    failureMode = os.Getenv("INJECT_FAILURE")
)

func init() {
    prometheus.MustRegister(latency, requests)
}

func handler(w http.ResponseWriter, r *http.Request) {
    start := time.Now()
    status := "200"
    extra := 0 * time.Millisecond

    switch failureMode {
    case "latency":
        extra = time.Duration(800+rand.Intn(1200)) * time.Millisecond
    case "errors":
        if rand.Float64() < 0.35 {
            status = "500"
            http.Error(w, "internal error", 500)
        }
    case "oom":
        // Simulate memory growth (controlled leak for demo only)
        _ = make([]byte, 1<<20) // 1MB per request
    }

    time.Sleep(extra)
    elapsed := time.Since(start).Seconds()
    latency.WithLabelValues(r.URL.Path, status).Observe(elapsed)
    requests.WithLabelValues(r.URL.Path, status).Inc()

    if status == "200" {
        fmt.Fprintln(w, `{"status":"ok","service":"checkout"}`)
    }
}

func main() {
    mux := http.NewServeMux()
    mux.Handle("/metrics", promhttp.Handler())
    mux.HandleFunc("/checkout", handler)
    mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        fmt.Fprintln(w, `{"status":"ok"}`)
    })

    log.Printf("checkout starting on :8080 (failure_mode=%q)", failureMode)
    if err := http.ListenAndServe(":8080", mux); err != nil {
        log.Fatal(err)
    }
}
```

Create `services/checkout/go.mod`:
```
module checkout

go 1.23

require github.com/prometheus/client_golang v1.19.1
```

Create `services/checkout/Dockerfile`:
```dockerfile
FROM golang:1.23-alpine AS build
WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download 2>/dev/null || true
COPY *.go ./
RUN CGO_ENABLED=0 go build -o checkout .

FROM gcr.io/distroless/static
COPY --from=build /app/checkout /checkout
ENTRYPOINT ["/checkout"]
```

> Repeat the same structure for `services/frontend` (port 3000) and
> `services/payments` (port 8081) — same code, different service name env var.

---

## Step 3 — docker-compose.yml

Create `docker-compose.yml` in the lab root:

```yaml
version: "3.9"

networks:
  rewind-lab:
    driver: bridge

volumes:
  prometheus_data:
  loki_data:
  tempo_data:

services:
  # ── Microservices ──────────────────────────────────────────────────────────
  frontend:
    build: ./services/checkout   # reuse same image, different env
    environment:
      INJECT_FAILURE: ""
      SERVICE_NAME: frontend
    ports:
      - "3000:8080"
    networks: [rewind-lab]
    labels:
      logging: "promtail"

  checkout:
    build: ./services/checkout
    environment:
      INJECT_FAILURE: ""
      SERVICE_NAME: checkout
    ports:
      - "8080:8080"
    networks: [rewind-lab]
    labels:
      logging: "promtail"

  payments:
    build: ./services/checkout
    environment:
      INJECT_FAILURE: ""
      SERVICE_NAME: payments
    ports:
      - "8081:8080"
    networks: [rewind-lab]
    labels:
      logging: "promtail"

  # ── Prometheus ─────────────────────────────────────────────────────────────
  prometheus:
    image: prom/prometheus:v2.51.2
    volumes:
      - ./config/prometheus.yml:/etc/prometheus/prometheus.yml:ro
      - ./config/alert.rules.yml:/etc/prometheus/alert.rules.yml:ro
      - prometheus_data:/prometheus
    command:
      - --config.file=/etc/prometheus/prometheus.yml
      - --storage.tsdb.retention.time=2h
    ports:
      - "9090:9090"
    networks: [rewind-lab]

  # ── Alertmanager ───────────────────────────────────────────────────────────
  alertmanager:
    image: prom/alertmanager:v0.27.0
    volumes:
      - ./config/alertmanager.yml:/etc/alertmanager/alertmanager.yml:ro
    ports:
      - "9093:9093"
    networks: [rewind-lab]

  # ── Loki ───────────────────────────────────────────────────────────────────
  loki:
    image: grafana/loki:2.9.7
    volumes:
      - ./config/loki.yml:/etc/loki/loki.yml:ro
      - loki_data:/loki
    command: -config.file=/etc/loki/loki.yml
    ports:
      - "3100:3100"
    networks: [rewind-lab]

  # ── Promtail (ships Docker logs → Loki) ────────────────────────────────────
  promtail:
    image: grafana/promtail:2.9.7
    volumes:
      - /var/lib/docker/containers:/var/lib/docker/containers:ro
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - ./config/promtail.yml:/etc/promtail/config.yml:ro
    command: -config.file=/etc/promtail/config.yml
    networks: [rewind-lab]
    depends_on: [loki]

  # ── Tempo ──────────────────────────────────────────────────────────────────
  tempo:
    image: grafana/tempo:2.4.2
    volumes:
      - ./config/tempo.yml:/etc/tempo.yml:ro
      - tempo_data:/tmp/tempo
    command: -config.file=/etc/tempo.yml
    ports:
      - "3200:3200"
    networks: [rewind-lab]

  # ── Grafana (optional — visual verification) ───────────────────────────────
  grafana:
    image: grafana/grafana:10.4.3
    environment:
      GF_AUTH_ANONYMOUS_ENABLED: "true"
      GF_AUTH_ANONYMOUS_ORG_ROLE: "Admin"
    volumes:
      - ./config/grafana-datasources.yml:/etc/grafana/provisioning/datasources/datasources.yml:ro
    ports:
      - "3001:3000"
    networks: [rewind-lab]
    depends_on: [prometheus, loki, tempo]

  # ── Load generator ─────────────────────────────────────────────────────────
  loadgen:
    image: alpine/curl:latest
    entrypoint: /bin/sh
    command: >
      -c "while true; do
        curl -sf http://checkout:8080/checkout > /dev/null 2>&1;
        curl -sf http://payments:8080/checkout > /dev/null 2>&1;
        curl -sf http://frontend:8080/checkout > /dev/null 2>&1;
        sleep 0.5;
      done"
    networks: [rewind-lab]
    depends_on: [checkout, payments, frontend]
```

---

## Step 4 — Configuration files

### `config/prometheus.yml`

```yaml
global:
  scrape_interval: 15s
  evaluation_interval: 15s

rule_files:
  - /etc/prometheus/alert.rules.yml

alerting:
  alertmanagers:
    - static_configs:
        - targets: [alertmanager:9093]

scrape_configs:
  - job_name: checkout
    static_configs:
      - targets: [checkout:8080]
    relabel_configs:
      - target_label: service_name
        replacement: checkout

  - job_name: payments
    static_configs:
      - targets: [payments:8080]
    relabel_configs:
      - target_label: service_name
        replacement: payments

  - job_name: frontend
    static_configs:
      - targets: [frontend:8080]
    relabel_configs:
      - target_label: service_name
        replacement: frontend
```

### `config/alert.rules.yml`

```yaml
groups:
  - name: rewind-lab
    rules:
      - alert: HighLatency
        expr: >
          histogram_quantile(0.99,
            rate(http_request_duration_seconds_bucket[2m])
          ) > 1.0
        for: 1m
        labels:
          severity: warning
        annotations:
          summary: "High p99 latency on {{ $labels.job }}"

      - alert: HighErrorRate
        expr: >
          rate(http_requests_total{status="500"}[2m]) /
          rate(http_requests_total[2m]) > 0.05
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "High error rate on {{ $labels.job }}"
```

### `config/alertmanager.yml`

```yaml
route:
  receiver: noop
receivers:
  - name: noop
```

### `config/loki.yml`

```yaml
auth_enabled: false

server:
  http_listen_port: 3100

ingester:
  lifecycler:
    ring:
      kvstore:
        store: inmemory
      replication_factor: 1

schema_config:
  configs:
    - from: "2024-01-01"
      store: boltdb-shipper
      object_store: filesystem
      schema: v11
      index:
        prefix: index_
        period: 24h

storage_config:
  boltdb_shipper:
    active_index_directory: /loki/index
    cache_location: /loki/cache
  filesystem:
    directory: /loki/chunks

limits_config:
  enforce_metric_name: false
  reject_old_samples: true
  reject_old_samples_max_age: 168h

chunk_store_config:
  max_look_back_period: 0s

table_manager:
  retention_deletes_enabled: false
  retention_period: 0s
```

### `config/promtail.yml`

```yaml
server:
  http_listen_port: 9080

positions:
  filename: /tmp/positions.yaml

clients:
  - url: http://loki:3100/loki/api/v1/push

scrape_configs:
  - job_name: docker
    docker_sd_configs:
      - host: unix:///var/run/docker.sock
        refresh_interval: 5s
        filters:
          - name: label
            values: [logging=promtail]
    relabel_configs:
      - source_labels: [__meta_docker_container_name]
        target_label: container
      - source_labels: [__meta_docker_container_label_com_docker_compose_service]
        target_label: service_name
```

### `config/tempo.yml`

```yaml
server:
  http_listen_port: 3200

distributor:
  receivers:
    otlp:
      protocols:
        http:
          endpoint: 0.0.0.0:4318

storage:
  trace:
    backend: local
    local:
      path: /tmp/tempo/traces
```

### `config/grafana-datasources.yml`

```yaml
apiVersion: 1
datasources:
  - name: Prometheus
    type: prometheus
    url: http://prometheus:9090
    isDefault: true
  - name: Loki
    type: loki
    url: http://loki:3100
  - name: Tempo
    type: tempo
    url: http://tempo:3200
```

---

## Step 5 — `rewind.yaml` for the lab

Create `rewind.yaml` in `d:\Coding Files\Projects\Incident-Replay-Engine\`:

```yaml
prometheus:
  url: http://localhost:9090

loki:
  url: http://localhost:3100

alertmanager:
  url: http://localhost:9093

tempo:
  url: http://localhost:3200

source_timeout: 30s
```

---

## Step 6 — Start the lab

```powershell
cd "d:\Coding Files\Projects\rewind-lab"

# Build and start everything
docker compose up -d --build

# Wait 60s for Prometheus to scrape first data points
Start-Sleep -Seconds 60

# Verify sources are reachable
cd "d:\Coding Files\Projects\Incident-Replay-Engine"
bin\rewind.exe sources
```

Expected:
```
✓ prometheus      reachable
✓ loki            reachable
✓ alertmanager    reachable
✓ tempo           reachable
```

---

## Incident 1 — Bad Deploy (latency + error spike)

This is the primary RW001 scenario. Simulates deploying a bad version of checkout.

### Inject the failure

```powershell
# Record when you "deployed"
$T_DEPLOY = Get-Date -Format "HH:mm"
Write-Host "Deploy time: $T_DEPLOY"

# Inject high latency + errors into checkout
docker compose -f "d:\Coding Files\Projects\rewind-lab\docker-compose.yml" `
  exec checkout sh -c 'kill -USR1 1'

# Simpler: restart checkout with the failure mode env var
docker compose -f "d:\Coding Files\Projects\rewind-lab\docker-compose.yml" `
  up -d --no-deps --force-recreate `
  -e INJECT_FAILURE=errors checkout
```

### Wait 5 minutes for signals to build up

```powershell
Start-Sleep -Seconds 300
```

### Run Rewind

```powershell
$FROM = "-8m"   # look back 8 minutes from now

bin\rewind.exe investigate `
  --from $FROM `
  --to now `
  --config rewind.yaml `
  -o "incident-bad-deploy-$(Get-Date -Format yyyyMMdd-HHmm).rewind"
```

**Expected verdict:**
```
► [1] confidence: HIGH  rules: RW001
    trigger : metric change-point (error.rate ↑XX× on checkout)
    chain:
      → XX× error.rate change-point at HH:MM
      → alertmanager: HighErrorRate fired
```

### Open in UI

```powershell
bin\rewind.exe ui "incident-bad-deploy-*.rewind"
```

### Restore checkout

```powershell
docker compose -f "d:\Coding Files\Projects\rewind-lab\docker-compose.yml" `
  up -d --no-deps --force-recreate checkout
Start-Sleep -Seconds 120
```

---

## Incident 2 — CPU Throttling (latency degradation, no errors)

### Inject

```powershell
docker compose -f "d:\Coding Files\Projects\rewind-lab\docker-compose.yml" `
  up -d --no-deps --force-recreate `
  -e INJECT_FAILURE=latency payments
```

### Wait and investigate

```powershell
Start-Sleep -Seconds 300

bin\rewind.exe investigate `
  --from -8m --to now `
  --config rewind.yaml `
  -o "incident-cpu-$(Get-Date -Format yyyyMMdd-HHmm).rewind"
```

**Expected verdict:**
```
► [1] confidence: MEDIUM/HIGH  rules: RW004
    trigger : sustained cpu.throttle change-point on payments
```

### Restore

```powershell
docker compose -f "d:\Coding Files\Projects\rewind-lab\docker-compose.yml" `
  up -d --no-deps --force-recreate payments
```

---

## Incident 3 — Cascade failure (Prometheus + Loki corroboration)

Run both error + latency failures simultaneously and verify Rewind
correlates both signals into a single high-confidence verdict.

```powershell
# Break both checkout AND payments at the same time
docker compose -f "d:\Coding Files\Projects\rewind-lab\docker-compose.yml" `
  up -d --no-deps --force-recreate `
  -e INJECT_FAILURE=errors checkout

Start-Sleep -Seconds 30

docker compose -f "d:\Coding Files\Projects\rewind-lab\docker-compose.yml" `
  up -d --no-deps --force-recreate `
  -e INJECT_FAILURE=latency payments

# Wait for cascade signals to appear
Start-Sleep -Seconds 300

bin\rewind.exe investigate `
  --from -12m --to now `
  --config rewind.yaml `
  --format md `
  -o "incident-cascade-$(Get-Date -Format yyyyMMdd-HHmm).rewind" `
  > "postmortem-$(Get-Date -Format yyyyMMdd).md"

# Open the postmortem
Invoke-Item "postmortem-*.md"
```

---

## Validation checklist

After running all three incidents, verify:

| Check | Command | Pass condition |
|---|---|---|
| `rewind sources` connects | `bin\rewind.exe sources` | All ✓ green |
| Bad deploy → HIGH | Incident 1 terminal output | `confidence: HIGH, rules: RW001` |
| Bundle round-trip | `bin\rewind.exe investigate --replay incident-*.rewind` | Same verdict as original |
| Web UI loads verdict | `bin\rewind.exe ui incident-*.rewind` | Trigger text visible in sidebar |
| Markdown output | `--format md` | Renders correct table + verdict |
| Exit code for CI | `echo $LASTEXITCODE` after critical incident | Exit code 1 |
| Graceful source failure | Stop prometheus, run investigate | Warning printed, continues |

### Test graceful degradation

```powershell
# Stop prometheus — Rewind should degrade gracefully, not crash
docker compose -f "d:\Coding Files\Projects\rewind-lab\docker-compose.yml" stop prometheus

bin\rewind.exe investigate --from -5m --to now --config rewind.yaml
# Expected: "✗ prometheus  unreachable" + timeline from Loki/Alertmanager only

docker compose -f "d:\Coding Files\Projects\rewind-lab\docker-compose.yml" start prometheus
```

---

## Teardown

```powershell
docker compose -f "d:\Coding Files\Projects\rewind-lab\docker-compose.yml" down -v
```

---

## FAQ

**Q: Why not kind/Kubernetes for this lab?**
Docker Compose is intentionally simpler — it validates the same Rewind code paths
(Prometheus, Loki, Alertmanager, Tempo collectors) without requiring kind/kubectl setup.
The `rewind demo --scenario bad-deploy` path already validates the analytical engine in isolation.

**Q: Does this prove it would work at Google/Netflix scale?**
The collectors, change-point detectors, and correlation rules are all identical regardless
of whether the Prometheus endpoint returns 100 metrics or 100 million. Scale differences:
- At MAANG: multiple Prometheus shards → use Thanos/Mimir URL in `rewind.yaml`
- At MAANG: Loki with auth → add `username`/`password` in config
- At MAANG: federated Alertmanager → same API, same config

**Q: Why is there a `.gitlab-ci.yml` in the repo?**
There isn't any more — it was deleted. The repo uses `.github/workflows/ci.yml` for GitHub Actions only.

**Q: The UI says "Failed to load incident" — what's wrong?**
This means `rewind ui` isn't running. The UI fetches from `http://127.0.0.1:7750/api/incident`.
Make sure you ran `bin\rewind.exe ui incident.rewind` and kept the terminal open.

---

## Appendix — Connecting to a real production stack

Same steps, just point `rewind.yaml` at your real endpoints:

```yaml
# Production rewind.yaml
prometheus:
  url: https://prometheus.internal.mycompany.com
  token: ${PROMETHEUS_TOKEN}

loki:
  url: https://loki.internal.mycompany.com
  username: ${LOKI_USER}
  password: ${LOKI_PASSWORD}

alertmanager:
  url: https://alertmanager.internal.mycompany.com

# Kubernetes: uses $KUBECONFIG automatically
# No kubernetes: block needed if KUBECONFIG is set

# GitHub deployments
cicd:
  github:
    token: ${GITHUB_TOKEN}
    owner: mycompany
    repo: backend
    environment: production

source_timeout: 45s
```

Then during an incident:

```bash
rewind investigate \
  --from 14:00 --to 15:00 \
  --namespace production \
  --service checkout \
  -o incident-$(date +%Y%m%d-%H%M).rewind

# Share the bundle with the team — they don't need cluster access
rewind ui incident-20260712-1430.rewind
```
