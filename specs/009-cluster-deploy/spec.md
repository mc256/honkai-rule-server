# Feature Specification: Deploy honkai-rule-server to the cluster behind example.com

**Feature Branch**: `009-cluster-deploy`
**Created**: 2026-05-01
**Status**: Draft
**Input**: User description: "Build the container image, push to registry.example.com/library/honkai-rule-server, deploy to the cluster under example.com using a random hex path prefix (matching existing services on the same domain). Custom rules live on a PVC (operator can sync `config/custom-rules/` to it via a Makefile target). Subscriptions CSV, own-proxies YAML, and tokens JSON live in a Kubernetes ConfigMap (operator can sync them via another Makefile target). The deliverable is a working URL on example.com that serves the merged subscription config."

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Image is published to the operator's container registry (Priority: P1)

The operator runs a single command from this repo and ends up with a versioned, immutable container image at `registry.example.com/library/honkai-rule-server:<tag>` that the cluster can pull. The image contains the compiled server binary plus the static served-config template baked in at a known path so the running container needs only operator-supplied config (subscriptions, own-proxies, tokens, custom rules) to come up.

**Why this priority**: Nothing else can ship without an image in the registry. P1 because every downstream story (deploy, sync, smoke-test) has this as a hard precondition.

**Independent Test**: With a clean local checkout, run the publish target. Verify the image appears in the registry under the right repository, with a tag derived from the current git short SHA, and that pulling+running the image with manually-mounted config files produces a working server on its declared port.

**Acceptance Scenarios**:

1. **Given** a clean clone of `honkai-rule-server` on the master branch, **When** the operator runs the publish target (e.g., `make docker-push`), **Then** the build produces an image tagged with the current git short SHA AND pushes it to `registry.example.com/library/honkai-rule-server`. A subsequent `docker pull registry.example.com/library/honkai-rule-server:<sha>` succeeds.
2. **Given** the published image, **When** the operator runs it locally with the four required config inputs mounted in (subscriptions.csv, own-proxies.yaml, tokens.json) and a writable cache directory, **Then** the server starts, binds to its declared port, and serves the merged subscription YAML at the configured route within 10 seconds.
3. **Given** the publish target supports a `:latest` companion tag, **When** the operator opts in, **Then** the latest tag is pushed alongside the SHA tag (the SHA tag is the canonical pinning identifier; latest is for convenience). The default behavior (just SHA) is non-destructive — it never silently advances `:latest`.

---

### User Story 2 — The server is reachable on example.com (Priority: P1)

A Helm chart in the operator's `<your-iac-repo>` repo deploys the published image to the cluster: Deployment, Service, Ingress, ConfigMap, and PVC. Argo CD continuously syncs the chart from the IaC repo. Once synced, the server is reachable at `https://example.com/<random-32-char-hex>/<token>/...` (path layout follows the existing pattern set by the operator's other services on the same domain). TLS is terminated at the Ingress by the existing cert-manager + letsencrypt-prod issuer.

**Why this priority**: The user-visible deliverable. Without this, the work is invisible. P1 because shipping US1 alone produces an image that nobody talks to.

**Independent Test**: After the IaC PR is merged and Argo CD syncs, hit `https://example.com/<random-prefix>/<valid-token>/config` from a Mihomo client (or `curl`). Expect a 200 response with `Content-Type: application/yaml` whose body contains the merged proxy/group/rule blocks (the same shape produced by the local server).

**Acceptance Scenarios**:

1. **Given** the chart is committed and Argo CD has synced, **When** the operator visits the configured URL with a valid token, **Then** the response is a 200 with `application/yaml` and a parseable Mihomo subscription body. Subscription-Userinfo and Profile-Update-Interval headers are present.
2. **Given** the chart includes a random 32-character hex path prefix in the Ingress, **When** another arbitrary request hits `example.com` without that prefix or with a different prefix, **Then** the request goes to the existing service (or returns the existing service's response/404) — the new server is reachable ONLY under its own random prefix.
3. **Given** the Ingress lists both `example.com` and `www.example.com` (matching the existing pattern), **When** the operator hits either host with the random prefix, **Then** both reach the new server. TLS is valid (signed by the existing letsencrypt-prod issuer; no operator action needed).
4. **Given** the deployment uses a 1-replica + Recreate strategy with a ReadWriteOnce PVC for state, **When** Argo CD rolls a new image revision, **Then** the previous pod is terminated before the new one starts, the PVC remounts cleanly, and downtime is bounded (≤30s in steady state). No silent data loss in the PVC.

---

### User Story 3 — Operator syncs config and custom rules from this repo (Priority: P1)

The operator edits `config/subscriptions.csv`, `config/own-proxies.yaml`, `config/tokens.json`, and/or files under `config/custom-rules/` in the `honkai-rule-server` repo. They run two distinct Makefile targets to push the changes to the running cluster: one updates the ConfigMap (subscriptions, own-proxies, tokens), the other syncs custom rules to the PVC. The pod hot-reloads when the underlying mounts change (per the project's existing fsnotify-driven 250ms debounce).

**Why this priority**: Day-2 operations. Without this, the operator has no path to update the deployed config short of editing the cluster directly — which is the exact workflow this project's IaC discipline aims to avoid. P1 because shipping US1+US2 without a sync workflow produces a deployment that nobody can iterate on.

**Independent Test**: Add a YAML rule file to `config/custom-rules/`. Run the rules-sync target. Watch the pod logs (or `kubectl exec` and inspect the mounted directory). Within one fsnotify debounce window the merge layer picks up the new rule. Hit the served URL and verify the new rule appears in the rules block at the expected priority position.

**Acceptance Scenarios**:

1. **Given** the operator changes `config/own-proxies.yaml` locally, **When** they run `make config-sync` against a target k8s context/namespace, **Then** the ConfigMap is updated in-cluster within 30 seconds. The running pod observes the mount change and rebuilds its merged config; the next request returns the updated body.
2. **Given** the operator adds a new YAML file to `config/custom-rules/<priority>-<name>.yaml`, **When** they run `make rules-sync` against a target k8s context/namespace, **Then** the file lands at the declared mount path on the PVC; the running pod's custom-rules loader observes it within one debounce window; the served body contains the new rule with its declared priority and contributor name.
3. **Given** the sync targets accept a `KUBE_CONTEXT` (or equivalent) parameter, **When** the operator points at a non-prod context, **Then** the sync targets that context only. Default is to fail-loud rather than silently sync to whatever happens to be the current `kubectl` context.
4. **Given** `make config-sync` and `make rules-sync` are independent, **When** the operator changes one but not the other, **Then** running just the corresponding target is sufficient — neither target requires the other to run first.
5. **Given** the operator's local files contain secrets (subscription URL tokens, exit-proxy passwords), **When** the sync runs, **Then** the secrets are pushed only to the configured cluster's ConfigMap/PVC and never to the local repo's git history (the source-of-truth files in `config/` are listed in `.gitignore` if they contain real secrets, OR the operator is expected to use placeholders in committed files and real values out-of-band — current convention applies, see Assumptions).

---

### Edge Cases

- **First-time deploy with empty PVC**: The custom-rules folder may not exist on the PVC at first launch. The server already tolerates a missing custom-rules folder gracefully (003 invariant); no extra logic is required. Operators run `make rules-sync` once after the first deploy to seed the PVC.
- **Sync targets a non-existent ConfigMap or PVC**: The targets must fail loudly with a clear message, not silently create. The chart owns the lifecycle of these objects; the sync targets only update their contents.
- **Sync targets a wrong cluster (operator misconfigured `KUBE_CONTEXT`)**: The sync targets MUST require an explicit context argument and refuse to use whatever context happens to be active, to avoid accidental cross-cluster writes.
- **Image registry unreachable during deploy**: Argo CD will fail the sync; the existing pod (if any) keeps serving. Operators see the failure in the Argo CD UI; no silent rollback is required (Argo CD's default behavior suffices).
- **PVC at capacity**: Custom-rules files are small (<1MB total typical). PVC sizing of 1Gi (matching existing chart pattern) is grossly oversized for the workload — capacity is not expected to be a runtime concern.
- **Two operators run `make rules-sync` concurrently**: Last writer wins; the running pod sees a sequence of fsnotify events and rebuilds for each. No correctness concern (idempotent sync).
- **Operator forgets to bump the image tag in the chart**: The chart pins the image tag to a specific SHA (or `latest`). Argo CD will detect drift and re-sync. If `:latest` is used, `imagePullPolicy: Always` ensures fresh pulls — but `:latest` is discouraged in favor of SHA tags for reproducibility (see Assumptions).
- **Merge-time crash on bad sync content**: A malformed custom-rules file (per 003 schema) causes a load-time error. The pod's existing fail-fast on bad config (Constitution Principle III) means the pod will refuse to come up; previous good config is served until restart. Operators can inspect logs and revert the bad sync.

## Requirements *(mandatory)*

### Functional Requirements

#### Image build and publish

- **FR-001**: A Makefile target MUST build a Linux/amd64 (and optionally arm64) container image from this repo's `Dockerfile`, tagged as `registry.example.com/library/honkai-rule-server:<git-short-sha>`. The git short SHA is the canonical immutable identifier.
- **FR-002**: The same target MUST push the SHA-tagged image to `registry.example.com/library/honkai-rule-server`. Authentication uses whatever Docker credentials the operator has already configured locally; the target does not handle credentials inline.
- **FR-003**: The target MUST optionally also push a `:latest` tag pointing at the same image, gated on an explicit operator opt-in (a make variable or separate target). Default behavior pushes the SHA tag only — `:latest` is never silently advanced.
- **FR-004**: The image MUST contain the compiled server binary AND the served-config template (`templates/served-config.template.yaml`) baked in at a known path. The runtime container's `SERVED_CONFIG_TEMPLATE_PATH` env var defaults to that baked path so the chart's ConfigMap need not include the template.
- **FR-005**: Image build MUST be reproducible: the same git commit produces a byte-identical layer set (modulo timestamp metadata that Docker cannot avoid). The `-trimpath` and `-ldflags="-s -w"` flags from the existing Dockerfile are preserved.

#### Cluster deployment (multi-repo: chart and application live in `<your-iac-repo>`)

- **FR-006**: A Helm chart deploys the honkai-rule-server image to the cluster with a Deployment (1 replica, Recreate strategy), a Service (ClusterIP), an Ingress (matching the existing example.com pattern), a ConfigMap (operator config), and a PVC (custom rules + cache).
- **FR-007**: An Argo CD Application object continuously syncs the chart from the `<your-iac-repo>` repo. Sync policy is `automated: {}` (matching the existing honkai-rule-server applications).
- **FR-008**: The Ingress MUST route `https://example.com/<random-32-char-hex>/...` to the new server's Service. The `<random-32-char-hex>` is a single 32-character lowercase hex string declared in the chart's values, matching the format used by the existing `overridePath` (e.g., `/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa`). The chart MUST commit a freshly-generated hex value for this feature — not reuse any existing service's prefix.
- **FR-009**: The Ingress MUST also bind `www.example.com` to the same service path (matching the existing chart's host list). TLS is provided by the same letsencrypt-prod cluster issuer; the chart adds the new hosts/paths to the existing TLS section.
- **FR-010**: The Ingress className is `contour` (matching the existing services on this domain). The chart inherits the existing `cert-manager.io/cluster-issuer: "letsencrypt-prod"` and `acme.cert-manager.io/http01-ingress-class: "contour"` annotations.
- **FR-011**: The chart MUST NOT modify or remove the existing `proxy-override-server` deployment, service, or Ingress entries. The honkai-rule-server is additive.
- **FR-012**: The Deployment's container env vars MUST include all five required vars from the project's `Load()`: `SUBSCRIPTIONS_CSV_PATH`, `OWN_PROXIES_YAML_PATH`, `TOKENS_PATH`, `SERVED_CONFIG_TEMPLATE_PATH`, `CACHE_DIR`. Optional env vars (`PORT`, `LOG_LEVEL`, `CUSTOM_RULES_PATH`, `HONKAI_RULE_CLIENT_UA`, etc.) are templated into values.yaml so operators can override per environment.

#### Storage layout

- **FR-013**: A ReadWriteOnce PVC (1 GiB, `local-path` storage class — matching the existing chart) MUST be mounted into the container at a known path (e.g., `/data/`). Inside the PVC, two subdirectories: `custom-rules/` (operator-managed via FR-016) and `cache/` (server-managed; receives upstream subscription payloads cached per 001 FR-017).
- **FR-014**: A ConfigMap (named per the chart's release) MUST contain three files keyed by their basename: `subscriptions.csv`, `own-proxies.yaml`, `tokens.json`. The Deployment mounts this ConfigMap into the container at a known path (e.g., `/etc/honkai/`); the env vars in FR-012 point at the mounted file paths.
- **FR-015**: The served-config template (`templates/served-config.template.yaml`) MUST NOT be in the ConfigMap. It is baked into the image (FR-004) so changing it requires a rebuild — the right granularity since it's part of the codebase, not operator config.

#### Sync workflows

- **FR-016**: A Makefile target (e.g., `make rules-sync KUBE_CONTEXT=<ctx> NAMESPACE=<ns>`) MUST upload every YAML file under this repo's `config/custom-rules/` folder to the deployed PVC's `custom-rules/` subdirectory. Files removed locally MUST be removed remotely (the target is a true sync, not just an upload). The target MUST refuse to run without an explicit `KUBE_CONTEXT` argument.
- **FR-017**: A separate Makefile target (e.g., `make config-sync KUBE_CONTEXT=<ctx> NAMESPACE=<ns>`) MUST update the ConfigMap with the current contents of `config/subscriptions.csv`, `config/own-proxies.yaml`, and `config/tokens.json` from this repo. The target uses `kubectl create configmap --dry-run=client | kubectl apply -f -` (or equivalent atomic replace) so partial failures do not leave the ConfigMap in an inconsistent state. The target MUST refuse to run without an explicit `KUBE_CONTEXT` argument.
- **FR-018**: Both sync targets MUST exit with a non-zero status if the target ConfigMap or PVC does not exist (the chart owns lifecycle; the targets only update content).
- **FR-019**: After either sync target runs, the running pod MUST observe the change within 60 seconds and rebuild its merged config. (This is the existing fsnotify behavior from 001 FR-017, debounced 250 ms — combined with kubelet's mount-projection refresh window, ≤60 s end-to-end is realistic.)
- **FR-020**: Sync targets MUST log clearly which cluster context, namespace, and ConfigMap/PVC name they are touching before performing the change, so the operator has a chance to abort with `^C` if the destination is wrong.

#### Observability and operability

- **FR-021**: The Deployment MUST declare livenessProbe and readinessProbe HTTP gets against the server's port and a known healthy path (e.g., `/health` or the root). Probe initial delay accounts for the bootstrap sequence (per 001 FR-002, up to ~15 s when fetching upstreams cold).
- **FR-022**: The pod MUST emit logs to stdout in the project's existing `log/slog` JSON format. The cluster's existing log aggregation picks them up automatically.

### Key Entities

- **Container image**: `registry.example.com/library/honkai-rule-server:<sha>` — built from this repo's Dockerfile, includes binary + served-config template. Immutable per tag.
- **Helm chart**: extends the existing `<your-iac-repo>/charts/honkai-rule-server/` chart. New templates: `honkai-deployment.yaml`, `honkai-service.yaml`, `honkai-configmap.yaml`. Existing `pvc.yaml` and `ingress.yaml` are extended (not replaced) to add a new PVC entry and a new Ingress path entry. New `values.yaml` block under `honkai:` covers image, pathPrefix, env, persistence, probes.
- **Argo CD Application**: reuses the existing `honkai-rule-server` Application (`<your-iac-repo>/applications/static.yaml`); no new Application object. The single Application syncs the whole chart.
- **ConfigMap**: holds three operator-managed files (subscriptions.csv, own-proxies.yaml, tokens.json). Namespaced; named per chart release. Updated by `make config-sync`.
- **PVC**: 1 GiB, RWO, `local-path` storage class. Mounted at `/data/`; contains `custom-rules/` (operator-managed via `make rules-sync`) and `cache/` (server-managed).
- **Random hex path prefix**: a 32-character lowercase hex string used as the Ingress path prefix for this service, separate from any existing service's prefix on the same domain.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: From a clean local checkout, `make docker-push` produces a SHA-tagged image at `registry.example.com/library/honkai-rule-server` in under 5 minutes (including build + push) with default Docker buildkit caching warm.
- **SC-002**: After the chart and Argo CD Application land in `<your-iac-repo>` and Argo CD's automated sync completes, `curl -fsS https://example.com/<random-prefix>/<valid-token>/config` returns HTTP 200 with `Content-Type: application/yaml` and a parseable Mihomo body containing the operator's merged proxies+groups+rules.
- **SC-003**: After `make rules-sync KUBE_CONTEXT=<your-context> NAMESPACE=cms` completes, a freshly-added rule in `config/custom-rules/` appears in the served body within 60 seconds (measured from `make` exit to the next successful `curl` showing the rule).
- **SC-004**: After `make config-sync KUBE_CONTEXT=<your-context> NAMESPACE=cms` completes, an edit to `config/subscriptions.csv` (e.g., enabling/disabling a source) is reflected in the served body within 60 seconds.
- **SC-005**: A pod restart preserves the cache directory contents (`cache/` survives across pod recreations because the PVC is RWO and mounted into both old and new pods sequentially per the Recreate strategy).
- **SC-006**: TLS validates without warnings — the existing letsencrypt-prod issuer covers the new hosts/paths and `curl` against the served URL accepts the cert without `-k`.
- **SC-007**: Both Makefile sync targets refuse to run when `KUBE_CONTEXT` is not explicitly set, with a clear error message naming the missing variable.
- **SC-008**: The chart and Argo CD Application can be removed (operator deletes the Application, then the chart's namespace) and re-added without manual cleanup steps — no leftover orphaned ConfigMaps, PVCs, or webhook resources.
- **SC-009**: The deployed instance passes the same stock-client invariants as the local test suite: every upstream proxy is reachable through the global `Proxies` selector; every own-proxy is reachable through at least one own-group; Subscription-Userinfo and Profile-Update-Interval headers are present.

## Assumptions

- **Image registry credentials**: The operator has already configured Docker login for `registry.example.com` locally (e.g., via `docker login` or a credential helper). The Makefile targets do not handle credentials inline.
- **Image tag strategy**: Default tag is the git short SHA (7 chars) of HEAD. The `:latest` tag is opt-in via a separate make variable or target. Reproducibility wins over operator convenience.
- **Chart placement**: Folded into the existing `<your-iac-repo>/charts/honkai-rule-server/` chart. New resources rendered from the chart: a `honkai-rule-server` Deployment, Service, ConfigMap, and PVC, all gated on `.Values.honkai.enabled`. The existing `Ingress` is **shared** (extended, not replaced) — a new path entry routing `/<random-hex>` to the honkai service is appended under both existing hosts; the existing override-server, log-analyzer, and static-site routes are preserved. The existing `honkai-rule-server` Argo CD Application syncs the chart, so no new Application object is added. (Revisited from the spec's original "separate chart" decision per operator request — the shared-Ingress / shared-Application story is preferred.)
- **Random path prefix**: A new 32-character lowercase hex string is generated once when the chart is authored and committed verbatim into the chart's values. The Makefile target does not generate or rotate it; rotation is an explicit chart edit.
- **Same Ingress class + cert-manager issuer**: `contour` and `letsencrypt-prod`, matching the existing example.com services. The new chart's Ingress annotations mirror the existing chart's.
- **Storage class**: `local-path` (matching the existing chart's persistence default). Future move to a more durable class is operator's decision.
- **PVC size**: 1 GiB. Custom-rules content is far under this limit; the cache directory is also small (cached upstream YAML payloads). Plenty of headroom.
- **Sync target tooling**: Both targets use `kubectl` against the operator's local kubeconfig, with the cluster context selected explicitly via `KUBE_CONTEXT`. No operator on the cluster runs the targets — they're operator-side from the IaC author's machine.
- **PVC sync mechanism**: Because the runtime image is a `FROM scratch` distroless container with no shell, `kubectl cp` cannot target the running pod. The `make rules-sync` target spawns a short-lived helper pod (e.g., `busybox` with the same PVC mounted), `kubectl cp`'s the local files into it, then deletes the helper. Alternative considered: an init container pattern — rejected because it would require restarting the application pod for every rules update, which defeats the hot-reload contract from 001 FR-017.
- **Source-of-truth for config files**: `config/own-proxies.yaml`, `config/subscriptions.csv`, `config/tokens.json` are committed in this repo with placeholder or operator-real values per current convention. Operators maintain real values out-of-band when they contain secrets (e.g., a private fork, a secrets manager, or `.gitignore`'d local files). The sync target uses whatever is in the local working tree at sync time. Existing project convention is followed; no new secrets management pattern is introduced.
- **Hot-reload window**: The pod's existing fsnotify-driven reloader runs on a 250 ms debounce (per 001). Kubernetes mount-projection updates ConfigMap-mounted files within ~30 s by default (configurable via kubelet's `--sync-frequency`). End-to-end "edit local file → see change in served body" is bounded ≤60 s — set as the SC-003/SC-004 acceptance bound.
- **Single-replica deployment**: The honkai-rule-server is stateful (cache + custom-rules) and has no inherent horizontal scaling story (Constitution Principle II's determinism applies per build; multi-replica adds load-balancer-vs-cache-coherency complexity for negligible benefit on this workload). A 1-replica + Recreate deployment matches the existing chart's pattern.
- **Test coverage**: The deployment story is exercised end-to-end via SC-002 / SC-003 / SC-004 manual smoke checks. Automated cluster-level integration tests are out of scope for this feature; the project's existing test suite covers the merge transformation core.
- **Multi-repo PR coordination**: The Makefile / Dockerfile / docs changes land in a PR against this repo. The chart + Argo CD Application changes land in a separate PR against `<your-iac-repo>`. The two PRs are coordinated by the operator (the chart's image tag references a SHA produced by an already-pushed image, so the sequence is: build+push image first, then merge the IaC PR pointing at that SHA). The plan document will lay out this two-PR sequencing in detail.
