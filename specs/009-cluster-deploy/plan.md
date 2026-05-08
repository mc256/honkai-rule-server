# Implementation Plan: Deploy honkai-rule-server to the cluster

**Branch**: `009-cluster-deploy` | **Date**: 2026-05-01 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/009-cluster-deploy/spec.md`

## Summary

Two-repo deliverable. In `honkai-rule-server` (this repo): add Makefile targets for image publish (`make docker-push` / `make docker-push-latest`) and config sync (`make config-sync`, `make rules-sync`); add a small helper Job manifest under `deploy/` for the rules-sync workflow; document in `specs/009-cluster-deploy/quickstart.md`. In `<your-iac-repo>` (separate repo): a new chart at `charts/honkai-rule-server/` (Deployment, Service, Ingress, ConfigMap, PVC) and a new Argo CD Application at `applications/honkai-rule-server.yaml`. Image is SHA-tagged; chart pins the SHA. End-to-end smoke check via `curl https://example.com/<random-prefix>/<token>/config`.

The merge transformation core is unchanged. No Go source code is modified — the `SERVED_CONFIG_TEMPLATE_PATH` env var already accepts the baked-in path per 001's existing config layer.

## Technical Context

**Language/Version**: Go 1.25 toolchain (declared 1.22+) — unchanged
**Primary Dependencies**: existing — no new Go deps. Operator-side adds `kubectl` and `docker buildx` to the toolchain expectation.
**Storage**: ConfigMap (4-ish KiB total: subscriptions.csv ≈ a few hundred bytes, own-proxies.yaml ≈ 1 KiB, tokens.json ≈ a few hundred bytes — all comfortably under k8s's 1 MiB ConfigMap limit) + 1 GiB PVC (custom-rules + cache).
**Testing**: existing `make check` for the Go side; deployment validated by manual smoke (SC-002/-003/-004). No new automated cluster-level tests (out of scope per spec Assumption "Test coverage").
**Target Platform**: Kubernetes (cluster), Linux/amd64 nodes, contour ingressClass, cert-manager letsencrypt-prod issuer.
**Project Type**: Single Go module + IaC artifacts. The IaC artifacts span two repos (honkai-rule-server's `deploy/` for the helper Job, <your-iac-repo> for the chart + Application).
**Performance Goals**: Build+push ≤5 min (SC-001); end-to-end sync→served ≤60 s (SC-003/-004). No new pod-runtime perf goals.
**Constraints**: PVC sync against a `FROM scratch` runtime image (no shell), so `kubectl cp` requires a helper pod (research §R3). Single-replica, Recreate strategy (RWO PVC). letsencrypt rate limits — share the existing wildcard-style host pattern; no per-deploy cert issuance.
**Scale/Scope**: Single instance. Operator population: 1 (the IaC author). Mihomo client population: a handful (per the existing example.com tenants).

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Justification |
|-----------|--------|---------------|
| **I. Unified Transformation Core** | PASS — N/A | Feature does not touch the merge layer. Both subscription mode and override mode (when added) consume the same `MergedConfig`; the deployment surface is mode-agnostic. |
| **II. Deterministic Transformation** | PASS — preserved | Image build uses the existing `-trimpath` + `-ldflags="-s -w"` flags. Image tag = git short SHA, so the same commit always produces the same image identifier. PVC + ConfigMap content is the *input* to the determinism contract; the running pod's behavior remains pure given fixed inputs. |
| **III. CSV Rules** | PASS — preserved | The CSV schema is unchanged. The `subscriptions.csv` file is now sourced from a ConfigMap instead of a local file path, but the parser and loud-failure semantics are identical. |
| **IV. Test-First, Real-Input Integration (NON-NEGOTIABLE)** | PASS — scope-limited | The merge transformation core is not modified, so the test-first mandate does not apply to deployment glue. The Go unit + integration tests still gate the *image*; the chart and Makefile additions are exercised manually via SC-002/-003/-004 smoke checks. The plan documents which tests stay green and which artifacts are smoke-validated. |
| **V. Observable Routing & Source-Merge Decisions** | PASS — preserved | The pod's existing `slog` JSON output is captured by the cluster's existing log aggregation (Assumption: same pipeline used by other example.com services). The "structured machine-parseable form" mandate is satisfied by the existing log layer. |
| **Routing — Corporate isolation** | PASS — N/A | Corporate-isolation rules are CSV-driven and unchanged. The deployment surface does not touch routing logic. |
| **Routing — multi-subscription collision resolution** | PASS — N/A | No change. |
| **Routing — fetch failure modes** | PASS — preserved | Per-source TTL / stale-on-error settings come from the same CSV (now in ConfigMap). The existing fail-closed boundary applies; the deployment does not introduce a new failure mode. |
| **Security — Secrets boundary** | PASS — with discipline | Subscription URLs and exit-proxy credentials live in a ConfigMap rather than env vars or a Secret. **This is a deviation from the constitution's "loaded from environment variables or a secrets store" wording** — see Complexity Tracking. The mitigation: the ConfigMap is namespaced and access-controlled like a Secret in the operator's RBAC posture; the operator may upgrade to a Secret in a follow-up feature without code changes (the loader reads files from a path; the source object type is opaque). |
| **Security — Sanitized output** | PASS — preserved | Served-mode output is generated by the same code path as today; no upstream credentials leak into the served body. |
| **Security — CSV is reviewable, not secret** | PASS — preserved | The CSV in the ConfigMap is the same artifact format as today. Any secrets it contains live in the same place they live today (operator's local copy + the deployed ConfigMap). |
| **Snapshot stability gate** | PASS — N/A | No snapshot changes. The deployment artifacts are not part of the snapshot suite. |
| **Diff-reviewable changes** | PASS | Two PRs (image side: Makefile + Dockerfile + spec docs; IaC side: chart + Application). Each PR is a self-contained diff. |
| **Both modes covered, every change** | PASS — N/A | Feature does not touch the transformation core. Both modes behave identically under deployment. |
| **Simplicity bias** | PASS | Existing chart pattern reused (`charts/honkai-rule-server/` as the template). One new chart, one new Application, four Makefile targets. No frameworks, no plugin systems, no new abstractions. |

### Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| ConfigMap (not Secret) for subscriptions / own-proxies / tokens | Operator's stated preference: "stored in the ConfigMap instead of the PVC because those files are not expected to be changed frequently". The operator runs the cluster, sets the RBAC, and decides where these files live; the constitution leaves "secrets store" undefined for ambiguous-secrecy artifacts like a small operator-only namespace. | A `Secret` would technically satisfy the literal constitution wording but offers no real protection over a ConfigMap in this single-tenant cluster (same RBAC surface, same etcd-encryption posture). Using a Secret would also force a parallel Makefile target that wraps the secret in opaque base64 — adding ceremony with no security gain at this point. The deviation is documented; a follow-up feature can migrate to SealedSecret/ExternalSecret without code changes. |

## Project Structure

### Documentation (this feature)

```text
specs/009-cluster-deploy/
├── plan.md              # This file
├── research.md          # Phase 0 — design decisions for the deploy surface
├── data-model.md        # Phase 1 — chart values shape, ConfigMap/PVC layout, env var matrix
├── quickstart.md        # Phase 1 — operator guide (build, deploy, sync, troubleshoot)
├── contracts/
│   └── deployment.md    # Phase 1 — deploy/runtime contract (image entry, mount paths, ports)
└── tasks.md             # Phase 2 — produced by /speckit-tasks
```

### Source Code (this repo)

```text
honkai-rule-server/
├── cmd/                                        # UNCHANGED
├── internal/                                   # UNCHANGED
├── templates/
│   └── served-config.template.yaml             # UNCHANGED (will be COPY'd into image)
├── config/
│   ├── subscriptions.csv                       # UNCHANGED (operator's local source-of-truth)
│   ├── own-proxies.yaml                        # UNCHANGED
│   ├── tokens.json                             # UNCHANGED
│   └── custom-rules/                           # UNCHANGED (target for `make rules-sync`)
├── deploy/
│   └── rules-sync-pod.yaml                     # NEW — helper-pod manifest used by `make rules-sync`
├── Dockerfile                                  # MODIFY — add `COPY templates/served-config.template.yaml /etc/honkai/served-config.template.yaml`
├── Makefile                                    # MODIFY — add docker-push, docker-push-latest, config-sync, rules-sync targets
└── specs/009-cluster-deploy/                   # documentation tree above
```

### Source Code (<your-iac-repo> repo, separate PR)

```text
<your-iac-repo>/
├── applications/
│   ├── static.yaml                             # UNCHANGED
│   └── honkai-rule-server.yaml                 # NEW — Argo CD Application
└── charts/
    └── honkai-rule-server/                     # NEW
        ├── Chart.yaml
        ├── values.yaml
        └── templates/
            ├── _helpers.tpl
            ├── deployment.yaml
            ├── service.yaml
            ├── ingress.yaml
            ├── configmap.yaml
            └── pvc.yaml
```

**Structure Decision**: Two PRs, two repos. Image PR (this repo) lands first and produces a SHA-tagged image at the registry. IaC PR (<your-iac-repo>) lands second, with the chart's `image.tag` pinned to that SHA. Argo CD's automated sync rolls the new pod once the IaC PR merges. The cross-repo coupling point is exactly one string (the SHA in `values.yaml`); no other coordination is required.

## Phase 0: Outline & Research

The spec leaves no `[NEEDS CLARIFICATION]` markers. The Phase 0 deliverable documents seven design decisions for the deploy surface; full form lives in `research.md`.

1. **Image build flags & layer layout**: Reuse the existing Dockerfile (multi-stage `golang:1.25-alpine` → `FROM scratch`). Add one `COPY templates/served-config.template.yaml /etc/honkai/served-config.template.yaml` line so the served template is baked in. The chart's `SERVED_CONFIG_TEMPLATE_PATH` defaults to `/etc/honkai/served-config.template.yaml`.

2. **Tag strategy**: Default tag is `git rev-parse --short HEAD` (7 chars). `make docker-push` → SHA-only push; `make docker-push-latest` → SHA + `:latest` push (opt-in, separate target). Chart pins to SHA in committed values.yaml; Argo CD detects drift and syncs.

3. **PVC sync mechanism**: A short-lived helper pod approach. `deploy/rules-sync-pod.yaml` declares a busybox pod that mounts the same PVC; `make rules-sync` applies it, `kubectl cp`'s files in, then deletes it. Idempotent — safe to re-run while the application pod is still serving (RWO PVC permits multiple readers/writers within the same node when both pods are scheduled there; the helper pod's nodeSelector matches the app pod's affinity to avoid scheduling-skew).

4. **ConfigMap update mechanism**: `make config-sync` runs `kubectl create configmap <name> --from-file=... --dry-run=client -o yaml | kubectl apply -f -` for atomic replace. The pod's `subPath`-mounted files refresh within kubelet's mount-projection refresh window (≤30 s by default).

5. **Random hex path prefix**: One 32-character lowercase hex string committed to `charts/honkai-rule-server/values.yaml` under `ingress.pathPrefix`. The string is generated once at chart-authoring time (e.g., `openssl rand -hex 16`) and committed verbatim. Rotation is an explicit chart edit; no Makefile target generates or rotates it.

6. **Mount layout in the pod**:
   - `/etc/honkai/subscriptions.csv` → ConfigMap key
   - `/etc/honkai/own-proxies.yaml` → ConfigMap key
   - `/etc/honkai/tokens.json` → ConfigMap key
   - `/etc/honkai/served-config.template.yaml` → baked into image
   - `/data/custom-rules/` → PVC subdirectory (operator-managed)
   - `/data/cache/` → PVC subdirectory (server-managed; persists across restarts per SC-005)

   Env vars: `SUBSCRIPTIONS_CSV_PATH=/etc/honkai/subscriptions.csv`, `OWN_PROXIES_YAML_PATH=/etc/honkai/own-proxies.yaml`, `TOKENS_PATH=/etc/honkai/tokens.json`, `SERVED_CONFIG_TEMPLATE_PATH=/etc/honkai/served-config.template.yaml`, `CACHE_DIR=/data/cache`, `CUSTOM_RULES_PATH=/data/custom-rules`.

7. **Probe configuration**: The project ships an unauthenticated `/health` endpoint per 001 FR-019d. livenessProbe / readinessProbe `httpGet` against `/health` on container port 8080, initialDelaySeconds 15 (covers the bootstrap window per 001 FR-002 — up to ~15 s on a cold start). If the implementation phase discovers `/health` does not exist or requires auth, fall back to a TCP-socket probe on port 8080.

**Output**: `research.md` documenting the seven decisions with rationale + rejected alternatives.

## Phase 1: Design & Contracts

**Prerequisites**: `research.md` complete

### Data Model

`data-model.md` covers:

- **Chart values** (`values.yaml` shape): `image.repository`, `image.tag`, `image.pullPolicy`, `imagePullSecrets`, `service.{type,port}`, `ingress.{enabled,className,annotations,pathPrefix,hosts,tls}`, `persistence.{enabled,storageClass,accessMode,size}`, `configMap.{subscriptionsKey,ownProxiesKey,tokensKey}`, `env.{logLevel,port,proxiesGroupName,fallbackRuleTarget,...}`, `resources`, `nodeSelector`, `tolerations`, `affinity`. Default values mirror the existing `charts/honkai-rule-server/values.yaml` where applicable.

- **ConfigMap layout**: name = `{{ include "honkai-rule-server.fullname" . }}-config`, namespace = chart's release namespace (cms), three keys (`subscriptions.csv`, `own-proxies.yaml`, `tokens.json`). Mounted via `subPath` so each file lands as a regular file rather than a symlink-into-projection (avoids in-place edit ordering issues with fsnotify).

- **PVC layout**: name = `{{ include "honkai-rule-server.fullname" . }}-data`. Mount: `/data`. Subdirectories created by the application on first run (cache) or by the rules-sync helper (custom-rules).

- **Env-var matrix**: full table of every env var the project's `Load()` reads, mapped to chart values, with defaults — referenced in research §R6.

### Contracts

`contracts/deployment.md` covers:

- **Image entrypoint**: `/server` (unchanged from existing Dockerfile).
- **Image baked paths**: `/etc/honkai/served-config.template.yaml`.
- **Required runtime mounts**: `/etc/honkai/subscriptions.csv`, `/etc/honkai/own-proxies.yaml`, `/etc/honkai/tokens.json`, `/data/cache`, `/data/custom-rules` (latter optional but recommended).
- **Network**: container port 8080, named `http`. Service routes port 80 → targetPort `http`. Ingress routes `<host>/<pathPrefix>/...` to the service's port 80.
- **Probes**: `httpGet /health port: http` for both liveness + readiness, initialDelaySeconds 15 (covers the bootstrap window per 001 FR-002). Falls back to TCP-socket probe if the server does not implement `/health`.
- **PVC interface**: the chart owns the PVC's spec; the application reads/writes inside it. Sync targets touch the PVC content via a helper pod, never via the application pod.
- **ConfigMap interface**: the chart owns the ConfigMap's spec (keys + initial content from a chart-level seed values file or from operator-supplied files). Sync targets replace content atomically via `kubectl apply` with `--dry-run=client -o yaml` piped templating.

### Quickstart

`quickstart.md` covers (operator-facing):

1. **Build & publish** — `make docker-push` from a clean tree.
2. **Initial deploy** — Update `charts/honkai-rule-server/values.yaml` `image.tag` in the IaC repo, commit, push, merge; Argo CD auto-syncs. First-time deploy may have empty PVC (custom-rules empty); cache populates from upstream fetches.
3. **Seed config** — `make config-sync KUBE_CONTEXT=<your-context> NAMESPACE=cms` to push the operator's local subscriptions/own-proxies/tokens.
4. **Seed rules** — `make rules-sync KUBE_CONTEXT=<your-context> NAMESPACE=cms` to push the local custom-rules folder.
5. **Verify** — `curl -fsS https://example.com/<random-prefix>/<token>/config` returns 200 + parseable Mihomo body.
6. **Iterate** — edit local files; re-run the relevant sync target; check the served body within 60 s.
7. **Troubleshoot** — `kubectl logs deploy/honkai-rule-server -n cms` for pod-side issues; `kubectl get application honkai-rule-server -n argocd -o yaml` for Argo CD status; `kubectl describe ingress -n cms` for cert-manager / contour state.
8. **Rollback** — Edit `image.tag` in IaC repo to a prior SHA; commit; Argo CD rolls back.

### Agent context update

Update `CLAUDE.md` to mark 008 as fully implemented and 009 as the active feature; add a new bullet referencing `specs/009-cluster-deploy/plan.md`.

## Phases (after this command)

This command stops here. Next: `/speckit-tasks` produces `tasks.md` with the dependency-ordered task list for both repos.
