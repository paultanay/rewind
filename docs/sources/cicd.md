# Source: GitHub / GitLab (CI/CD)

Rewind queries GitHub Actions and GitLab CI/CD APIs in **read-only** mode
to pull deployment and pipeline events into the incident timeline.

---

## Configuration

```yaml
# rewind.yaml
cicd:
  # GitHub configuration
  github:
    token: ""          # Personal Access Token or GitHub App token (read:repo, read:deployments)
    owner: ""          # GitHub org or user (e.g. "myorg")
    repo: ""           # Repository name (e.g. "backend")
    # Optional: restrict to specific environment (e.g. "production")
    environment: ""

  # GitLab configuration
  gitlab:
    url: https://gitlab.com
    token: ""          # Personal Access Token (read_api scope)
    project_id: ""     # Numeric project ID or "namespace/project"
    environment: ""
```

Environment overrides:
| Variable | Overrides |
|---|---|
| `REWIND_GITHUB_TOKEN` | `cicd.github.token` |
| `REWIND_GITLAB_TOKEN` | `cicd.gitlab.token` |

---

## What Rewind collects

### GitHub

```
GET /repos/{owner}/{repo}/deployments?environment=production&per_page=100
GET /repos/{owner}/{repo}/deployments/{id}/statuses
```

Deployments with `success` status within the window are converted to
`model.Event (Kind: Deploy)` including:
- Deployment SHA and ref (branch/tag)
- Triggered-by user
- Environment name
- GitHub Actions run URL (stored in `SourceRef.URL` for deep-link)

### GitLab

```
GET /projects/{id}/deployments?order_by=created_at&updated_after=<from>
GET /projects/{id}/pipelines?updated_after=<from>&status=success
```

---

## Correlation rules

| Rule | Usage |
|---|---|
| RW001 | Deploy event is the primary trigger for the deploy→metric-change-point chain |
| RW002 | ConfigChange event (pipeline with config-only diff) triggers the config-drift chain |
