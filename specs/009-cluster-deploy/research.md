# Phase 0 Research: Deploy honkai-rule-server to the cluster

## R1. Image build flags & layer layout

- **Decision**: Reuse the existing multi-stage Dockerfile (`golang:1.25-alpine` build → `FROM scratch` runtime). Add one line: `COPY --from=build /src/templates/served-config.template.yaml /etc/honkai/served-config.template.yaml`. Preserve the existing `-trimpath` and `-ldflags="-s -w"` flags. The runtime `SERVED_CONFIG_TEMPLATE_PATH` env var defaults to `/etc/honkai/served-config.template.yaml`.
- **Rationale**: The served-config template is a static asset that ships with the codebase — bundling it with the binary keeps the chart's ConfigMap focused on operator-managed inputs and avoids a fourth ConfigMap key that operators must remember to populate. The Dockerfile change is one line; the existing `-trimpath` / `-ldflags="-s -w"` flags continue to satisfy Constitution Principle II (deterministic build).
- **Alternatives considered**:
  - **Template in the ConfigMap**: rejected. Adds a 4th key the operator must remember; the template is part of the codebase, not config; rotating the template requires a rebuild anyway (the `__MERGED_*__` placeholders are coupled to the merge layer).
  - **Template downloaded from a URL at startup**: rejected. Adds a network dependency on a path that today has none; introduces a fail-mode for cold starts.

## R2. Tag strategy

- **Decision**: Default tag = `git rev-parse --short HEAD` (7 chars). Two Makefile targets: `make docker-push` pushes only the SHA tag; `make docker-push-latest` pushes the SHA tag AND a `:latest` alias. The chart's `values.yaml` always pins `image.tag` to a specific SHA; Argo CD detects drift when the operator commits a new SHA.
- **Rationale**: Reproducibility (Constitution II) requires that a tagged artifact be immutable. SHA-tagged images are immutable; `:latest` is mutable. Defaulting to SHA + opt-in `:latest` makes the safe path the default. Argo CD's drift detection works against any tag, but pinning to SHA gives the operator clear rollback semantics ("`image.tag: abc1234` → `image.tag: def5678`") rather than "redeploy and hope".
- **Alternatives considered**:
  - **Always push `:latest`**: rejected. `:latest` makes "what's running in the cluster" non-deterministic. Argo CD would still see the same `:latest` string in `values.yaml` and never detect drift (until `imagePullPolicy: Always` happens to win a race).
  - **Build-time semver tags**: rejected. Premature for a single-tenant deployment with no external consumers. Adopt later if the project grows multi-environment promotion needs.
  - **Date-stamped tags**: rejected. Same dev-time as SHA, but loses the "exact git state" provenance.

## R3. PVC sync mechanism

- **Decision**: Short-lived helper pod, declared in `deploy/rules-sync-pod.yaml`, mounting the same PVC at `/data`. `make rules-sync` runs (a) `kubectl apply -f deploy/rules-sync-pod.yaml` (creates a `busybox`-based pod), (b) waits for `Ready`, (c) `kubectl cp` of the local `config/custom-rules/` directory contents to `/data/custom-rules/` in the helper pod, (d) `kubectl delete pod`. The helper pod has the SAME `nodeSelector` as the application pod so the RWO PVC can be mounted by both pods on the same node concurrently.
- **Rationale**: The runtime image is `FROM scratch` — no shell, no `tar`, so `kubectl exec`/`kubectl cp` against the application pod fails. A helper pod with `busybox` is the standard workaround. The "true sync" semantic (delete files removed locally) is accomplished by `kubectl exec helper-pod -- sh -c 'rm -rf /data/custom-rules/* && tar -xzf - -C /data/custom-rules/'` reading a tar stream from `kubectl cp -`. Idempotent across retries.
- **Alternatives considered**:
  - **Init container in the app pod**: rejected. Forces a pod restart for every rules-sync, defeating the hot-reload contract from 001 FR-017 (250 ms fsnotify debounce).
  - **A long-running sidecar that polls a git repo**: rejected. Adds runtime complexity (auth tokens, polling cadence, network deps) for a workflow that runs maybe a few times a week.
  - **Direct write via `kubectl exec` against the app pod**: rejected. The scratch image has no shell, so `exec` would fail.
  - **A persistent helper pod kept running**: rejected. Idle resource cost; the operator only sync-runs occasionally.

## R4. ConfigMap update mechanism

- **Decision**: `make config-sync` runs:
  ```sh
  kubectl create configmap <name> \
    --from-file=subscriptions.csv=config/subscriptions.csv \
    --from-file=own-proxies.yaml=config/own-proxies.yaml \
    --from-file=tokens.json=config/tokens.json \
    --dry-run=client -o yaml \
    | kubectl apply -f -
  ```
  This is an atomic replace: the new ConfigMap revision is applied only if all three files were read successfully. The Deployment's `subPath`-mounted files refresh within kubelet's mount-projection window (configurable via `--sync-frequency`, default ~30s; observed ≤60s end-to-end including fsnotify debounce).
- **Rationale**: Atomic replace prevents partial-update states. Using `kubectl apply -f -` (not `kubectl create ... --replace`) preserves any operator-applied labels/annotations. The dry-run + apply pipeline is idiomatic and free of cleverness.
- **Alternatives considered**:
  - **`kubectl patch` per file**: rejected. Three patches → three transitions, possibly observed mid-sync by the running pod.
  - **`kubectl edit` interactively**: rejected. Not scriptable.
  - **`helm upgrade` for content changes**: rejected. The chart only owns the ConfigMap's *shape*, not its day-2 content; changing content via helm would couple operator runs to chart redeploys.

## R5. Random hex path prefix

- **Decision**: One 32-character lowercase hex string, generated once at chart-authoring time via `openssl rand -hex 16`, committed verbatim into `charts/honkai-rule-server/values.yaml` under `ingress.pathPrefix`. The chart concatenates it with `/` and uses it directly in the Ingress's path field. Rotation = `git commit` to the IaC repo (no Makefile target).
- **Rationale**: Matches the existing pattern in `charts/honkai-rule-server/values.yaml` (e.g., `overridePath: "/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`). The prefix is a reachability obscurity — security still depends on the per-request token gate (001 FR-018). Rotating it is a deliberate operator action, not an automated workflow, so committing the value to the chart is the right granularity.
- **Alternatives considered**:
  - **Generate at chart-render time via Helm's `randAlphaNum`**: rejected. Non-deterministic — every `helm template` produces a different prefix; Argo CD would see drift forever.
  - **Sealed-secret-style sealed prefix**: rejected. Massively over-engineered for a 32-char obscurity token.
  - **Subdomain instead of path prefix**: rejected. The user's stated requirement is "follow the same pattern" of path-based routing on `example.com`. Subdomain would force a new TLS cert.

## R6. Mount layout in the pod (env-var matrix)

- **Decision**: All required mounts and env vars per the table below.

| Env var | Value | Source | Mount path |
|---------|-------|--------|------------|
| `SUBSCRIPTIONS_CSV_PATH` | `/etc/honkai/subscriptions.csv` | ConfigMap key `subscriptions.csv` | volumeMount `config` subPath `subscriptions.csv` |
| `OWN_PROXIES_YAML_PATH` | `/etc/honkai/own-proxies.yaml` | ConfigMap key `own-proxies.yaml` | volumeMount `config` subPath `own-proxies.yaml` |
| `TOKENS_PATH` | `/etc/honkai/tokens.json` | ConfigMap key `tokens.json` | volumeMount `config` subPath `tokens.json` |
| `SERVED_CONFIG_TEMPLATE_PATH` | `/etc/honkai/served-config.template.yaml` | baked into image | (no mount) |
| `CACHE_DIR` | `/data/cache` | PVC subdirectory | volumeMount `data` subPath `cache` |
| `CUSTOM_RULES_PATH` | `/data/custom-rules` | PVC subdirectory | volumeMount `data` subPath `custom-rules` |
| `PORT` | `8080` (default) | values.yaml override available | — |
| `LOG_LEVEL` | `info` (default) | values.yaml override available | — |
| `PROXIES_GROUP_NAME` | `Proxies` (default) | values.yaml override available | — |
| `FALLBACK_RULE_TARGET` | `auto` (default) | values.yaml override available | — |
| `HONKAI_RULE_CLIENT_UA` | unset by default | values.yaml override available | — |

- **Rationale**: `subPath` mounts produce regular files (not symlinks-into-projection), which the project's `fsnotify` watcher handles correctly. Per-key mounts under one volumeMount keep the path layout clean. `/data` is the single mountpoint for the PVC; subdirectories `cache` and `custom-rules` are created by the application or by the rules-sync helper pod on first use.
- **Alternatives considered**:
  - **Project the entire ConfigMap to `/etc/honkai/` without subPath**: rejected. Changes one file → projection re-creates the entire directory → fsnotify sees mass events; it works, but subPath reduces noise.
  - **Mount each ConfigMap key as a separate volume**: rejected. Three volumes for three small files is ceremony.

## R7. Probe configuration

- **Decision**: Both livenessProbe and readinessProbe are `httpGet /health` on container port 8080 (named `http`). `initialDelaySeconds: 15` covers the bootstrap window per 001 FR-002 (cold-start fetches all upstreams). `periodSeconds: 10`, `timeoutSeconds: 3`, `failureThreshold: 3`. The `/health` endpoint is implemented in `internal/server/routes/health.go` and is unauthenticated by design (`internal/server/app.go` mounts it outside the token gate per 001 FR-019d).
- **Rationale**: Confirmed via grep that `/health` exists, returns a proper response, and is unauthenticated. Standard k8s pattern: same path for both probes; readiness drops the pod from the Service's endpoint list if it can't serve `/health`; liveness restarts the pod after sustained failure.
- **Alternatives considered**:
  - **TCP-socket probes**: rejected as primary. Doesn't catch the case where the binary is alive but the merge layer is wedged. Kept as a documented fallback if `/health` ever changes contract.
  - **`exec` probe (curl)**: rejected. The scratch image has no shell.
  - **Initial delay 0 with high failure threshold**: rejected. The bootstrap window is non-trivial; setting initial delay high is more honest than relying on "ignore failures while booting".

## Constitution Re-check Summary

All seven decisions remain consistent with the Phase 0 Constitution Check evaluation in `plan.md`. The single deviation (ConfigMap-not-Secret for subscriptions/own-proxies/tokens) is documented in plan.md Complexity Tracking with rationale. No drift. Phase 1 design proceeds.
