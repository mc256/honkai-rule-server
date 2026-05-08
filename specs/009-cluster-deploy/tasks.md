---

description: "Task list for feature 009-cluster-deploy"
---

# Tasks: Deploy honkai-rule-server to the cluster

**Input**: Design documents from `/specs/009-cluster-deploy/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/deployment.md, quickstart.md

**Tests**: Smoke-only — Constitution Principle IV's test-first mandate scopes to changes inside the merge transformation core. This feature touches no Go source, so the existing automated test suite continues to gate the *image*; the chart, Application, and sync workflows are validated by the SC-002 / SC-003 / SC-004 smoke checks defined in spec.md.

**Organization**: Three P1 stories, executed sequentially because each depends on the previous one's deliverable (US1 publishes the image SHA → US2's chart pins that SHA → US3's sync workflows touch the resources US2 created).

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: Which user story this task belongs to (US1 / US2 / US3)
- File paths are absolute from each repo's root. Two repos are involved:
  - `honkai-rule-server` (this repo) — Makefile, Dockerfile, helper-pod manifest, spec docs
  - `<your-iac-repo>` (separate repo, `/home/maverick/development/<your-iac-repo>`) — chart + Argo CD Application

## Path Conventions

- `honkai-rule-server` paths: `Dockerfile`, `Makefile`, `deploy/<file>`, `specs/009-cluster-deploy/<file>`
- `<your-iac-repo>` paths: `applications/<file>`, `charts/honkai-rule-server/<file>`

---

## Phase 1: Setup

**Purpose**: Verify operator-side prerequisites before any work touches the live cluster.

- [X] T001 Verify operator prerequisites: (a) `docker login registry.example.com` succeeds (or stored credential helper covers it); (b) `kubectl config get-contexts` includes a context for your cluster; (c) the operator has push access to `<your-iac-repo-url>`. Document any missing prerequisites in a session note before proceeding to Phase 3.

---

## Phase 2: Foundational

**Purpose**: Blocking prerequisites for all user stories.

No foundational tasks. The image publish (US1) is itself the precondition for US2; US2's resources are the precondition for US3. The phases sequence enforces this without a separate Foundational layer.

---

## Phase 3: User Story 1 — Image is published to the operator's container registry (Priority: P1)

**Goal**: A SHA-tagged image at `registry.example.com/library/honkai-rule-server:<sha>` containing the binary plus the served-config template baked at `/etc/honkai/served-config.template.yaml`.

**Independent Test**: `docker pull registry.example.com/library/honkai-rule-server:<sha>` succeeds; `docker run --rm <image> --help` (or equivalent) returns without crashing; `docker image inspect <image> | jq '.[0].Config.Cmd, .[0].Config.Entrypoint'` shows `/server` as entrypoint.

### Implementation for User Story 1

- [X] T002 [US1] Modify `honkai-rule-server/Dockerfile`: in the final-stage section (after the `FROM scratch` line and the existing `COPY` for the binary), add `COPY --from=build /src/templates/served-config.template.yaml /etc/honkai/served-config.template.yaml` so the served-config template is baked into the image at the path the chart's `SERVED_CONFIG_TEMPLATE_PATH` will reference. Do NOT change the build-stage flags (`-trimpath`, `-ldflags="-s -w"`) — Constitution II's reproducibility requirement.
- [X] T003 [US1] Modify `honkai-rule-server/Makefile`: add three new variables and three new targets near the existing `docker:` target.

  Variables:
  ```makefile
  IMAGE_REPO ?= registry.example.com/library/honkai-rule-server
  IMAGE_TAG  ?= $(shell git rev-parse --short HEAD)
  IMAGE      := $(IMAGE_REPO):$(IMAGE_TAG)
  ```

  Targets (`docker-build` builds; `docker-push` builds + pushes the SHA tag; `docker-push-latest` adds `:latest`):
  ```makefile
  .PHONY: docker-build docker-push docker-push-latest

  docker-build:
  	docker buildx build --platform linux/amd64 -t $(IMAGE) --load .

  docker-push: docker-build
  	docker push $(IMAGE)
  	@echo "Pushed $(IMAGE)"
  	@echo "To pin in <your-iac-repo>/charts/honkai-rule-server/values.yaml: image.tag: $(IMAGE_TAG)"

  docker-push-latest: docker-push
  	docker tag $(IMAGE) $(IMAGE_REPO):latest
  	docker push $(IMAGE_REPO):latest
  	@echo "Also pushed $(IMAGE_REPO):latest"
  ```

  Add `docker-build docker-push docker-push-latest` to the existing `.PHONY` list (or to the new one above — both work; keep .PHONY hygienic).
- [X] T004 [US1] Run `make docker-push` against the live registry. Capture the resulting SHA tag (e.g., `abc1234`) — it's needed verbatim by T013 (US2). If the push fails on auth, fix `docker login registry.example.com` and retry; do NOT proceed to US2 with a missing or stale SHA.

**Checkpoint**: User Story 1 complete. The image exists at `registry.example.com/library/honkai-rule-server:<captured-sha>`.

---

## Phase 4: User Story 2 — The server is reachable on example.com (Priority: P1)

**Goal**: Helm chart in `<your-iac-repo>/charts/honkai-rule-server/` + Argo CD Application + Argo CD-driven deploy to the cluster, served at `https://example.com/<random-hex>/<token>/config`.

**Independent Test**: `kubectl get application honkai-rule-server -n argocd -o jsonpath='{.status.health.status}'` returns `Healthy`; `curl -fsS https://example.com/<prefix>/<token>/config` returns 200 + parseable Mihomo body. Per spec SC-002.

### Implementation for User Story 2

- [X] T005 [US2] Create `<your-iac-repo>/charts/honkai-rule-server/Chart.yaml` with `apiVersion: v2`, `name: honkai-rule-server`, `description: Aggregated Mihomo subscription server`, `type: application`, `version: 0.1.0`, `appVersion: "1.0.0"`. Mirror the existing `charts/honkai-rule-server/Chart.yaml` structure.
- [X] T006 [US2] Create `<your-iac-repo>/charts/honkai-rule-server/templates/_helpers.tpl` defining `honkai-rule-server.name`, `honkai-rule-server.fullname`, `honkai-rule-server.chart`, `honkai-rule-server.labels`, `honkai-rule-server.selectorLabels` — copy a Helm chart's standard `_helpers.tpl` and adapt with the chart name `honkai-rule-server`.
- [X] T007 [US2] Generate a fresh 32-character lowercase hex prefix once: `openssl rand -hex 16`. Capture the value (e.g., `0123456789abcdef0123456789abcdef`) — it's referenced verbatim by T008 and the operator's eventual curl in T021.
- [X] T008 [US2] Create `<your-iac-repo>/charts/honkai-rule-server/values.yaml` per the schema in `specs/009-cluster-deploy/data-model.md` § "Helm chart values". Set `image.repository: registry.example.com/library/honkai-rule-server`, `image.tag` to the SHA captured in T004, `image.pullPolicy: IfNotPresent`. Set `imagePullSecrets: [{name: registry-local}]`. Set `ingress.pathPrefix` to the hex string captured in T007 (no leading slash — the template prepends one). Set the rest of the values per the data-model.md table; default env vars (port, logLevel, etc.) match the project's `Load()` defaults.
- [X] T009 [US2] Create `<your-iac-repo>/charts/honkai-rule-server/templates/deployment.yaml` per `data-model.md` § "Deployment" — single container, `Recreate` strategy, port 8080 named `http`, all required env vars (`SUBSCRIPTIONS_CSV_PATH`, `OWN_PROXIES_YAML_PATH`, `TOKENS_PATH`, `SERVED_CONFIG_TEMPLATE_PATH`, `CACHE_DIR`, `CUSTOM_RULES_PATH`) plus optional ones from `.Values.env.*`, livenessProbe + readinessProbe `httpGet /health` on port `http` with `initialDelaySeconds: 15`, volumeMounts for `config` (subPath per key) and `data` (mountPath `/data`).
- [X] T010 [US2] Create `<your-iac-repo>/charts/honkai-rule-server/templates/service.yaml` per `data-model.md` § "Service" — ClusterIP, port 80 → targetPort `http`. Mirror the existing `charts/honkai-rule-server/templates/service.yaml` structure with name from `honkai-rule-server.fullname`.
- [X] T011 [US2] Create `<your-iac-repo>/charts/honkai-rule-server/templates/ingress.yaml` per `data-model.md` § "Ingress" — contour ingressClass, both `example.com` and `www.example.com` hosts, single path `/{{ .Values.ingress.pathPrefix }}` of `pathType: Prefix` routing to the chart's Service:80, TLS section with chart-owned `secretName: honkai-rule-server-tls` covering both hosts, cert-manager annotations `cluster-issuer: letsencrypt-prod` and `acme.cert-manager.io/http01-ingress-class: contour`.
- [X] T012 [US2] Create `<your-iac-repo>/charts/honkai-rule-server/templates/configmap.yaml` declaring a ConfigMap named `{{ include "honkai-rule-server.fullname" . }}-config` with three keys (`subscriptions.csv`, `own-proxies.yaml`, `tokens.json`). Initial content: source from chart-bundled `files/` directory if present (`{{ .Files.Get "files/subscriptions.csv" }}` etc.), else empty placeholders that the operator replaces via `make config-sync` immediately after first deploy. Document this in the chart's NOTES.txt.
- [X] T013 [US2] Create `<your-iac-repo>/charts/honkai-rule-server/templates/pvc.yaml` per `data-model.md` § "PVC" — name `{{ include "honkai-rule-server.fullname" . }}-data`, RWO, 1Gi, storageClass `local-path`, conditionally rendered when `.Values.persistence.enabled` (mirroring the existing chart's pattern).
- [X] T014 [US2] Create `<your-iac-repo>/applications/honkai-rule-server.yaml` — Argo CD Application object with `metadata.name: honkai-rule-server`, `metadata.namespace: argocd`, `spec.destination.namespace: cms`, `spec.destination.server: https://kubernetes.default.svc`, `spec.project: default`, `spec.source.path: charts/honkai-rule-server/`, `spec.source.repoURL: <your-iac-repo-url>`, `spec.source.targetRevision: master`, `spec.syncPolicy.automated: {}`. Mirror the structure of the existing `applications/static.yaml` entries.
- [X] T015 [US2] Commit and push the chart + Application changes to the IaC repo's master branch (or open a PR per the operator's IaC review policy). Argo CD's automated sync should pick up the new Application within ~30 s.
- [X] T016 [US2] Verify the Argo CD sync completed and the pod is healthy: run `kubectl --context cluster get application honkai-rule-server -n argocd -o jsonpath='{.status.sync.status}{":"}{.status.health.status}{"\n"}'` and confirm it shows `Synced:Healthy`. If `OutOfSync` or `Degraded`, run `kubectl --context cluster describe application honkai-rule-server -n argocd` and resolve before continuing. Then `kubectl --context cluster get pod -n cms -l app.kubernetes.io/name=honkai-rule-server` should show `1/1 Running`.

**Checkpoint**: User Story 2 complete. Argo CD reports Healthy; the pod is running; the service is reachable inside the cluster (verifiable with `kubectl --context cluster run -it --rm test --image=alpine -n cms -- wget -qO- http://<release>-honkai-rule-server/health`). External reachability via the Ingress depends on cert-manager finishing the cert issuance — typically <60 s after the Ingress object exists. The full URL test is in T021.

---

## Phase 5: User Story 3 — Operator syncs config and custom rules from this repo (Priority: P1)

**Goal**: Two Makefile targets (`config-sync`, `rules-sync`) plus a helper-pod manifest that let the operator push local `config/` changes to the deployed cluster.

**Independent Test**: After both targets are implemented, edit `config/own-proxies.yaml` locally, run `make config-sync KUBE_CONTEXT=<your-context> NAMESPACE=cms`, observe the ConfigMap update via `kubectl --context cluster get configmap -n cms <name> -o yaml`, then run `curl -fsS https://example.com/<prefix>/<token>/config | yq '.proxies | map(select(.name | test("^_"))) | length'` to confirm the new own-proxy count. Same flow for `rules-sync` with a freshly-added file in `config/custom-rules/`.

### Implementation for User Story 3

- [X] T017 [US3] Create `honkai-rule-server/deploy/rules-sync-pod.yaml` declaring a single-pod manifest with these properties: `apiVersion: v1`, `kind: Pod`, `metadata.name: honkai-rule-server-rules-sync`, `metadata.namespace: cms` (overridable via the Makefile's `NAMESPACE`), one container named `sync` running `image: busybox:1.36`, `command: ["sleep", "infinity"]` so the pod stays alive long enough for `kubectl cp`, `volumeMounts: [{name: data, mountPath: /data}]`, single `volume` referencing `persistentVolumeClaim.claimName: <release>-honkai-rule-server-data` (the chart's PVC name; templated via Makefile sed substitution at apply time). Include a comment block at the top of the file explaining its single-purpose lifecycle (apply, cp, delete) and warning operators not to leave it running.
- [X] T018 [US3] Add to `honkai-rule-server/Makefile`: a `config-sync` target that takes `KUBE_CONTEXT` and `NAMESPACE` make variables (refusing to run if either is unset). Body:
  ```makefile
  CONFIG_MAP_NAME ?= honkai-rule-server-config

  .PHONY: config-sync
  config-sync:
  	@test -n "$(KUBE_CONTEXT)" || (echo "ERROR: set KUBE_CONTEXT=<context>"; exit 1)
  	@test -n "$(NAMESPACE)" || (echo "ERROR: set NAMESPACE=<namespace>"; exit 1)
  	@echo "Syncing ConfigMap $(CONFIG_MAP_NAME) to context=$(KUBE_CONTEXT) namespace=$(NAMESPACE)"
  	@kubectl --context $(KUBE_CONTEXT) -n $(NAMESPACE) get configmap $(CONFIG_MAP_NAME) >/dev/null \
  		|| (echo "ERROR: ConfigMap $(CONFIG_MAP_NAME) does not exist in $(KUBE_CONTEXT)/$(NAMESPACE) — chart must deploy it first"; exit 1)
  	kubectl --context $(KUBE_CONTEXT) -n $(NAMESPACE) create configmap $(CONFIG_MAP_NAME) \
  		--from-file=subscriptions.csv=config/subscriptions.csv \
  		--from-file=own-proxies.yaml=config/own-proxies.yaml \
  		--from-file=tokens.json=config/tokens.json \
  		--dry-run=client -o yaml \
  		| kubectl --context $(KUBE_CONTEXT) -n $(NAMESPACE) apply -f -
  ```
- [X] T019 [US3] Add to `honkai-rule-server/Makefile`: a `rules-sync` target. Body:
  ```makefile
  RULES_SYNC_POD ?= honkai-rule-server-rules-sync

  .PHONY: rules-sync
  rules-sync:
  	@test -n "$(KUBE_CONTEXT)" || (echo "ERROR: set KUBE_CONTEXT=<context>"; exit 1)
  	@test -n "$(NAMESPACE)" || (echo "ERROR: set NAMESPACE=<namespace>"; exit 1)
  	@echo "Syncing custom-rules to PVC via helper pod $(RULES_SYNC_POD) in $(KUBE_CONTEXT)/$(NAMESPACE)"
  	@kubectl --context $(KUBE_CONTEXT) -n $(NAMESPACE) apply -f deploy/rules-sync-pod.yaml
  	@kubectl --context $(KUBE_CONTEXT) -n $(NAMESPACE) wait --for=condition=Ready pod/$(RULES_SYNC_POD) --timeout=60s
  	@kubectl --context $(KUBE_CONTEXT) -n $(NAMESPACE) exec $(RULES_SYNC_POD) -- sh -c 'rm -rf /data/custom-rules/* && mkdir -p /data/custom-rules'
  	@tar -czf - -C config/custom-rules . | kubectl --context $(KUBE_CONTEXT) -n $(NAMESPACE) exec -i $(RULES_SYNC_POD) -- sh -c 'tar -xzf - -C /data/custom-rules/'
  	@kubectl --context $(KUBE_CONTEXT) -n $(NAMESPACE) delete pod $(RULES_SYNC_POD) --wait=true
  	@echo "rules-sync complete"
  ```
  Use `trap` (or a wrapper) to ensure the helper pod is deleted even if a step in the middle fails — leaving a long-running busybox pod is undesirable. A simple way: wrap the body in a shell `set -e; ... ; cleanup` block; an even simpler way: add a `kubectl delete pod ... --ignore-not-found` line before the apply, so re-runs always start clean.
- [X] T020 [US3] Run `make config-sync KUBE_CONTEXT=<your-context> NAMESPACE=cms` against the live cluster. Verify the ConfigMap content matches the local files: `kubectl --context cluster -n cms get configmap honkai-rule-server-config -o jsonpath='{.data.subscriptions\.csv}' | head -3` should show the local `config/subscriptions.csv`'s first three lines verbatim. Repeat the spot-check for `own-proxies.yaml` and `tokens.json`. Wait 60 s and confirm via `kubectl logs deploy/honkai-rule-server -n cms` that the pod observed the config change (look for `loaded subscriptions` / `loaded own-proxies` log lines after the sync timestamp).
- [X] T021 [US3] Run `make rules-sync KUBE_CONTEXT=<your-context> NAMESPACE=cms` against the live cluster. Verify the PVC content via a fresh helper-pod inspection: `kubectl --context cluster run --rm -it inspect --image=busybox:1.36 -n cms --overrides='{"spec":{"volumes":[{"name":"data","persistentVolumeClaim":{"claimName":"honkai-rule-server-data"}}],"containers":[{"name":"inspect","image":"busybox:1.36","stdin":true,"tty":true,"volumeMounts":[{"name":"data","mountPath":"/data"}]}]}}' -- ls -la /data/custom-rules/` should show every file from `config/custom-rules/`. (Alternative: skip the inspect pod and trust the next curl in T022 to surface any divergence indirectly.)

**Checkpoint**: User Story 3 complete. Both sync targets work end-to-end against the live cluster.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: End-to-end smoke validation, documentation polish, branch hygiene.

- [X] T022 Run the end-to-end smoke check from `quickstart.md` § 5: `curl -fsS "https://example.com/<prefix>/<token>/config"` returns HTTP 200 + `Content-Type: application/yaml` + a parseable Mihomo body. Confirm Subscription-Userinfo and Profile-Update-Interval headers are present (`curl -I` for headers). Per SC-002, SC-006, SC-009.
- [ ] T023 Hot-reload smoke check: add a single rule to `config/custom-rules/<priority>-test.yaml`, run `make rules-sync KUBE_CONTEXT=<your-context> NAMESPACE=cms`, wait 60 s, then `curl -fsS "https://example.com/<prefix>/<token>/config" | grep "<test-rule-target>"` returns the expected line. Per SC-003.
- [X] T024 Document the Makefile additions in this repo's main README (or in `specs/009-cluster-deploy/quickstart.md` only — operator's preference). Link the IaC PR from this repo's PR description so the cross-repo coupling is visible to reviewers.
- [X] T025 Run `make check` locally (vet + lint + test + snapshot drift) to confirm the Dockerfile / Makefile changes haven't broken anything Go-side. Expected: clean.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (T001)**: No dependencies — can start immediately. Establishes operator-side prerequisites.
- **US1 (T002–T004)**: Depends on T001 (operator must have docker login). Produces a SHA-tagged image at the registry. Output: the SHA string.
- **US2 (T005–T016)**: Depends on US1's SHA — T008 references it verbatim in `values.yaml`. T015 (commit + push to IaC) is the gating event; once Argo CD syncs (verified by T016), the deployment is live.
- **US3 (T017–T021)**: Depends on US2 because the helper pod claims the PVC the chart created (T013), and the sync targets touch the ConfigMap the chart created (T012). T017 (helper pod manifest) can be authored in parallel with US2's chart work, but T020/T021 (running the syncs against the live cluster) require US2 complete.
- **Polish (T022–T025)**: Depends on US1+US2+US3 complete. T022 verifies the steady-state URL; T023 verifies the iterate-cycle; T025 ensures no Go-side regression.

### Two-repo coordination point

Exactly one cross-repo dependency: the SHA captured in T004 must appear verbatim in `values.yaml` written in T008. If the SHA changes (e.g., a follow-up commit lands on this repo's master), the operator MUST re-run T004 and update T008's value before the deploy reflects the new code.

### Within-Story Sequencing

- US1: T002 (Dockerfile) → T003 (Makefile) → T004 (push). Order matters; T004 cannot run before T003 lands the target.
- US2: T005–T014 (chart files + Application) can be written in any order — they're separate files. T015 (commit + push) must run after all of T005–T014. T016 (verify) runs after T015.
- US3: T017 (helper pod manifest) and T018/T019 (Makefile targets) are independent files; can be authored in parallel. T020/T021 (run against live cluster) run after T017–T019 lands.
- Polish: T022 → T023 → T024 → T025; T024/T025 can be done in either order.

### Parallel Opportunities

- US2 chart files (T005, T006, T009, T010, T011, T012, T013, T014) are each in their own file in the IaC repo — can be authored in parallel by multiple developers; the gating step is the single commit at T015.
- US1's T002 (Dockerfile) and US3's T017 (helper pod manifest) are in different files in this repo — can be authored in parallel.

Tasks not marked `[P]` because the natural execution order already serializes most of them through file-coordination boundaries.

---

## Implementation Strategy

### MVP (US1 only)

Image at the registry, no cluster deployment. Useful only as a building block — operators cannot use the image without US2's chart. Not a viable standalone milestone; treat US1 as table stakes.

### Recommended path

Sequential US1 → US2 → US3 → Polish, single PR per repo:

1. PR-A in this repo: T001–T004 (image build) + T017–T019 (Makefile + helper pod manifest) + T024 (docs). Merge.
2. PR-B in `<your-iac-repo>`: T005–T015 (chart + Application). Reference the SHA from PR-A. Merge.
3. Live ops: T016 (verify Argo CD sync), T020 (run config-sync), T021 (run rules-sync), T022–T023 (smoke checks), T025 (final `make check`).

PR-A and PR-B are technically independent (PR-A builds the image; PR-B pins the SHA), but PR-A must merge first because PR-B's `values.yaml` references PR-A's SHA.

### Rollback strategy

If T015 (push to IaC repo) sets up a broken state, revert the IaC commit and Argo CD reverts the deployment. If T020/T021 (sync) corrupt config, re-run the sync with the prior local files (or `git checkout`'d versions) to reset.

---

## Notes

- The chart's `values.yaml` `image.tag` is the only cross-repo coupling. All other values are defaults set in the chart and overridable per environment.
- Hex path prefix (T007) is generated once and committed. Rotating it is a chart edit + new commit + Argo CD sync. Mihomo client config files referencing the old prefix break on rotation — coordinate with consumers if rotating.
- `make config-sync` requires the ConfigMap to already exist (chart deploys it). First-time deploy: chart sees empty `files/` directory and creates an empty-key ConfigMap; operator runs `make config-sync` once to populate. The pod stays in CrashLoopBackOff until then, since the required env-var paths point at empty files. Document this in the quickstart's "Initial deploy" section (already done).
- Cert-manager rate limits: letsencrypt-prod has rate limits per registered domain. The chart's TLS Secret is named per the chart (separate from `public-tls`), so cert issuance happens once on first deploy and is renewed by cert-manager automatically. Avoid rapid Ingress recreation.
- `staticcheck` and `go vet` continue to pass without changes (no Go source modified). `make check` in T025 should be a no-op pass.
