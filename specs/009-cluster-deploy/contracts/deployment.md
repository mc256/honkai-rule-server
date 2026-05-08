# Deployment Contract: honkai-rule-server on Kubernetes

This document is the runtime/deploy contract between the **image** (built from this repo) and the **chart** (in `<your-iac-repo>/charts/honkai-rule-server/`). Either side can change independently as long as both honor this contract.

## Image contract

### Entrypoint

The image's `ENTRYPOINT` is `/server` (preserved from the existing Dockerfile).

### Baked-in paths

| Path | Source | Purpose |
|------|--------|---------|
| `/server` | compiled binary | Application entrypoint |
| `/etc/honkai/served-config.template.yaml` | repo's `templates/served-config.template.yaml` | Subscription-mode YAML template (consumed by `output.SubscriptionMode`) |
| `/etc/ssl/certs/ca-certificates.crt` | builder stage's CA bundle | Required for HTTPS to upstream subscription providers |

### Required runtime env vars (no default; pod fails startup if absent)

| Env var | Expected value |
|---------|----------------|
| `SUBSCRIPTIONS_CSV_PATH` | path to a readable CSV file |
| `OWN_PROXIES_YAML_PATH` | path to a readable YAML file |
| `TOKENS_PATH` | path to a readable JSON file |
| `SERVED_CONFIG_TEMPLATE_PATH` | path to a readable YAML template; chart defaults to the baked-in path |
| `CACHE_DIR` | path to a writable directory |

### Optional env vars (defaults shown)

| Env var | Default | Notes |
|---------|---------|-------|
| `PORT` | `8080` | Container HTTP port |
| `LOG_LEVEL` | `info` | One of `debug`, `info`, `warn`, `error` |
| `PROXIES_GROUP_NAME` | `Proxies` | Always-present global selector group name |
| `FALLBACK_RULE_TARGET` | `auto` | Final `MATCH` rule target |
| `CUSTOM_RULES_PATH` | `./custom-rules/` | Folder containing custom-rules YAML files |
| `HONKAI_RULE_CLIENT_UA` | unset | Comma-separated allowed UA prefixes (003) |
| `DEFAULT_TTL_SECONDS` | `3600` | |
| `DEFAULT_STALE_ON_ERROR_SECONDS` | `86400` | |
| `DEFAULT_PROFILE_UPDATE_INTERVAL_HOURS` | `12` | |
| `BOOTSTRAP_MAX_ATTEMPTS_PER_SOURCE` | `3` | |
| `BOOTSTRAP_ATTEMPT_DELAY_SECONDS` | `5` | |

### Network surface

- Listens on TCP `${PORT}` (default 8080).
- HTTP routes (per 001):
  - `GET /health` — unauthenticated, returns 200 + JSON when healthy
  - `GET /<token>/...` — token-gated subscription endpoints
- No outbound IPC / inter-pod / sidecar dependencies. All outbound HTTPS goes to the URLs in `subscriptions.csv`.

### Filesystem expectations

- Writable: `${CACHE_DIR}` (writes per-source upstream payload caches, ~hundreds of KiB per source)
- Readable: paths set by required env vars; `${CUSTOM_RULES_PATH}` (optional, but expected if operator uses custom rules)
- Read-only otherwise.

### fsnotify reload (001 FR-017)

The pod watches `SUBSCRIPTIONS_CSV_PATH`, `OWN_PROXIES_YAML_PATH`, `TOKENS_PATH`, and the directory at `CUSTOM_RULES_PATH` for changes; rebuilds merge state on a 250 ms debounce. Reload errors keep the previous state in effect.

## Chart contract

### Mounts

The chart's Deployment MUST mount:

1. **Config** (volume `config`, source: ConfigMap `<release>-config`):
   - `/etc/honkai/subscriptions.csv` ← key `subscriptions.csv` (subPath)
   - `/etc/honkai/own-proxies.yaml` ← key `own-proxies.yaml` (subPath)
   - `/etc/honkai/tokens.json` ← key `tokens.json` (subPath)

2. **Data** (volume `data`, source: PVC `<release>-data`):
   - `/data` (no subPath); the application creates `/data/cache/` and operator-managed `/data/custom-rules/` underneath.

### Env vars set by chart

The chart MUST set, at minimum, these env vars on the container:

```yaml
- {name: SUBSCRIPTIONS_CSV_PATH,     value: /etc/honkai/subscriptions.csv}
- {name: OWN_PROXIES_YAML_PATH,      value: /etc/honkai/own-proxies.yaml}
- {name: TOKENS_PATH,                value: /etc/honkai/tokens.json}
- {name: SERVED_CONFIG_TEMPLATE_PATH, value: /etc/honkai/served-config.template.yaml}
- {name: CACHE_DIR,                  value: /data/cache}
- {name: CUSTOM_RULES_PATH,          value: /data/custom-rules}
```

Optional env vars are templated from `.Values.env.*` and emitted only if the value is non-empty.

### Probes

- `livenessProbe`: `httpGet /health` on port `http` (8080), `initialDelaySeconds: 15`.
- `readinessProbe`: same.

### Network plumbing

- Service: ClusterIP, port 80, targetPort `http` (8080).
- Ingress: `https://<host>/<pathPrefix>/...` → Service port 80, where:
  - `<host>` ∈ {`example.com`, `www.example.com`}
  - `<pathPrefix>` is the chart's committed 32-char hex string
- TLS: existing letsencrypt-prod issuer; the chart adds its hosts to a chart-owned TLS Secret, NOT to the existing `public-tls` Secret (each chart manages its own cert per the existing pattern).

### Lifecycle

- Strategy: `Recreate` (RWO PVC).
- `imagePullPolicy: IfNotPresent` (SHA-tagged images are immutable).
- `replicas: 1`.

## Sync target contracts

### `make rules-sync`

- Inputs: `KUBE_CONTEXT`, `NAMESPACE` (both required); local files under `config/custom-rules/`.
- Steps:
  1. Apply `deploy/rules-sync-pod.yaml` (helper pod with `busybox` image, mounting the same PVC).
  2. Wait for the helper pod to be `Ready`.
  3. Tar the local `config/custom-rules/` directory; `kubectl exec helper -- sh -c 'rm -rf /data/custom-rules/* && mkdir -p /data/custom-rules && tar -xzf - -C /data/custom-rules/'` reading from stdin.
  4. Delete the helper pod.
- Outputs: PVC's `/data/custom-rules/` matches the local working tree byte-for-byte (modulo metadata).
- Failure modes: if any step fails, the helper pod is deleted before the script exits non-zero (no orphans).

### `make config-sync`

- Inputs: `KUBE_CONTEXT`, `NAMESPACE` (both required); local files at `config/subscriptions.csv`, `config/own-proxies.yaml`, `config/tokens.json`.
- Steps:
  1. `kubectl create configmap <release>-config --from-file=... --dry-run=client -o yaml | kubectl apply -f -` (atomic replace).
- Outputs: ConfigMap content matches the three local files.
- Failure modes: if `kubectl apply` rejects the manifest (e.g., field validation error), the existing ConfigMap is unchanged and the script exits non-zero.

### `make docker-push`

- Inputs: clean working tree (target uses `git rev-parse --short HEAD`).
- Steps:
  1. `docker buildx build --platform linux/amd64 -t registry.example.com/library/honkai-rule-server:<sha> --load .`
  2. `docker push registry.example.com/library/honkai-rule-server:<sha>`
- Outputs: SHA-tagged image at the registry.
- Failure modes: if `docker push` fails (auth, network), the local image is preserved; the operator can retry without rebuilding.

### `make docker-push-latest`

- Same as `docker-push` plus `docker tag ... :latest && docker push ... :latest`.

## Backward compatibility

- The image continues to support the original "everything from local files" workflow described in 001's quickstart. The chart's specific env-var values do not constrain other deploy topologies.
- Existing Go unit + integration tests in this repo do not depend on the chart; they pass without any chart-side changes.
- The chart can be uninstalled cleanly: `kubectl delete application honkai-rule-server -n argocd`, then `kubectl delete namespace cms` if no longer needed. Argo CD reconciles to "no resources".
