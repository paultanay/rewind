# Source: Kubernetes

Rewind queries the Kubernetes API Server in **read-only** mode using the
standard `client-go` library. It issues `List` calls for Events, Pods,
ReplicaSets, Deployments, and Nodes — never writes or patches anything.

---

## Configuration

```yaml
# rewind.yaml
kubernetes:
  # Path to kubeconfig (default: $KUBECONFIG or ~/.kube/config)
  kubeconfig: ""
  # Kubernetes context to use (default: current-context)
  context: ""
  # In-cluster mode: automatically uses the pod's service account token
  # when running inside a Kubernetes cluster. Set kubeconfig: "" to enable.
  in_cluster: false
```

Environment variable overrides:
| Variable | Overrides |
|---|---|
| `KUBECONFIG` | `kubernetes.kubeconfig` (standard Kubernetes env var) |
| `REWIND_K8S_CONTEXT` | `kubernetes.context` |

---

## Collected data

| Data type | API call | Normalized to |
|---|---|---|
| Pod events (OOMKill, Eviction, Probe failures) | `v1/events` scoped by namespace+time | `model.Event` |
| Pod lifecycle (restarts, phase changes) | `v1/pods` | `model.Event` + `model.Signal (restarts)` |
| Deployment rollouts | `apps/v1/replicasets` + `v1/events` | `model.Event (Deploy kind)` |
| Node conditions (MemoryPressure, DiskPressure) | `v1/nodes` | `model.Event (NodePressure kind)` |
| Entity topology | Pod `.spec.nodeName`, owner references | `model.Entity` graph |

---

## Topology construction

Rewind reconstructs the ownership graph from owner references:

```
Pod → ReplicaSet → Deployment → Service
Pod → Node (via spec.nodeName)
```

This graph drives RW006 (node pressure → pod effects) and proximity scoring
in all correlation rules.

---

## RBAC requirements

Rewind needs the following ClusterRole (or namespaced Role) permissions:

```yaml
rules:
  - apiGroups: [""]
    resources: ["events", "pods", "nodes", "namespaces"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["apps"]
    resources: ["deployments", "replicasets"]
    verbs: ["get", "list"]
```

No write permissions are needed or used.

---

## Correlation rules that use Kubernetes data

| Rule | Event kinds used |
|---|---|
| RW001 | Deploy |
| RW002 | ConfigChange |
| RW003 | OOMKill, Restart |
| RW005 | CrashLoop, ProbeFailed |
| RW006 | NodePressure, PodKilled (Eviction) |
| RW009 | Restart (crash-loop rate) |
