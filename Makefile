.PHONY: build test test-race test-unit test-integration snapshot-update lint vet check docker docker-build docker-push docker-push-latest config-sync rules-sync pod-restart cache-clear clean run help release-patch release-minor release-major release

# Operator-private overrides — see .env.example. Optional; safe to skip.
ifneq (,$(wildcard .env))
include .env
export
endif

BINARY := bin/server
PKG := ./...
INTEGRATION_PKG := ./internal/integration/...

# 009 + 013: image build & publish.
# Default IMAGE_REPO is GHCR derived from the GitHub remote owner. Override
# via `IMAGE_REPO=<custom>` (in .env or env) for private mirror / custom
# registry; the existing `make docker-push` flow keeps working with any
# operator-set value. The owner regex matches both ssh
# (git@github.com:owner/repo.git) and https (https://github.com/owner/repo)
# remote URL forms.
IMAGE_OWNER := $(shell git remote get-url origin 2>/dev/null | sed -E 's#.*github.com[:/]([^/]+)/.*#\1#' | head -1)
IMAGE_REPO ?= ghcr.io/$(IMAGE_OWNER)/honkai-rule-server
IMAGE_TAG  ?= $(shell git rev-parse --short HEAD)
IMAGE      := $(IMAGE_REPO):$(IMAGE_TAG)

# 009: config sync — all live targets require KUBE_CONTEXT and NAMESPACE.
# Names follow the chart's release pattern: ConfigMap, PVC, and
# Deployment are all `<release>{,-config,-data}`. The default
# release name is `honkai-rule-server`.
DEPLOYMENT_NAME ?= honkai-rule-server
CONFIG_MAP_NAME ?= honkai-rule-server-config
RULES_SYNC_POD  ?= honkai-rules-sync
# PVC_NAME matches the chart's PVC for a release named honkai-rule-server
# (`{{ .Release.Name }}-data`). Override on the command line for charts
# that render a different PVC name (e.g., `<release>-honkai-data`):
# `make rules-sync PVC_NAME=...`.
PVC_NAME        ?= honkai-rule-server-data

# _rules_sync_apply: substitutes the __RULES_SYNC_POD__ and __PVC_NAME__
# placeholders in deploy/rules-sync-pod.yaml with the current Make variable
# values, then pipes the rendered manifest to `kubectl apply -f -`. Used
# by both `rules-sync` and `cache-clear`. The placeholders are not valid
# Kubernetes identifiers, so direct `kubectl apply -f deploy/...yaml` is
# intentionally non-functional — see the file's header for context.
define _rules_sync_apply
	@sed -e 's|__RULES_SYNC_POD__|$(RULES_SYNC_POD)|g' \
	     -e 's|__PVC_NAME__|$(PVC_NAME)|g' \
	     deploy/rules-sync-pod.yaml \
	  | kubectl --context $(KUBE_CONTEXT) -n $(NAMESPACE) apply -f -
endef

build:
	go build -o $(BINARY) ./cmd/server

test:
	go test $(PKG)

test-race:
	go test -race $(PKG)

test-unit:
	go test $$(go list $(PKG) | grep -v /integration)

test-integration:
	go test $(INTEGRATION_PKG)

snapshot-update:
	UPDATE_SNAPSHOTS=true go test $(INTEGRATION_PKG)

vet:
	go vet $(PKG)

lint: vet
	@command -v staticcheck >/dev/null 2>&1 && staticcheck $(PKG) || echo "staticcheck not installed; skipping (install: go install honnef.co/go/tools/cmd/staticcheck@latest)"

# CI gate: vet + lint + test + verify no inadvertently-modified snapshots
check: vet lint test
	@git diff --exit-code || (echo "Working tree dirty after tests — committed snapshots changed unexpectedly"; exit 1)

docker:
	docker build -t honkai-rule-server:dev .

# 009 US1: build + push the SHA-tagged image to the registry. The chart's
# values.yaml pins image.tag to this SHA.
docker-build:
	docker buildx build --platform linux/amd64 -t $(IMAGE) --load .

docker-push: docker-build
	docker push $(IMAGE)
	@echo ""
	@echo "Pushed $(IMAGE)"
	@echo "Pin in <your-iac-repo>/charts/honkai-rule-server/values.yaml: image.tag: $(IMAGE_TAG)"

# 009: also push the :latest tag pointing at the same image. Opt-in;
# default `docker-push` never advances :latest silently.
docker-push-latest: docker-push
	docker tag $(IMAGE) $(IMAGE_REPO):latest
	docker push $(IMAGE_REPO):latest
	@echo "Also pushed $(IMAGE_REPO):latest"

# 009 US3: atomically replace the ConfigMap content from the local working
# tree's three operator-managed files. Refuses to run without explicit
# KUBE_CONTEXT and NAMESPACE — the targets MUST never silently use
# whatever context is currently active (009 FR-016/-017/-020).
config-sync:
	@test -n "$(KUBE_CONTEXT)" || (echo "ERROR: set KUBE_CONTEXT=<context>  (e.g., make config-sync KUBE_CONTEXT=<your-context> NAMESPACE=cms)"; exit 1)
	@test -n "$(NAMESPACE)" || (echo "ERROR: set NAMESPACE=<namespace>  (e.g., make config-sync KUBE_CONTEXT=<your-context> NAMESPACE=cms)"; exit 1)
	@test -f config/subscriptions.csv || (echo "ERROR: config/subscriptions.csv missing"; exit 1)
	@test -f config/own-proxies.yaml || (echo "ERROR: config/own-proxies.yaml missing"; exit 1)
	@test -f config/tokens.json || (echo "ERROR: config/tokens.json missing"; exit 1)
	@echo "config-sync: context=$(KUBE_CONTEXT) namespace=$(NAMESPACE) configmap=$(CONFIG_MAP_NAME)"
	@kubectl --context $(KUBE_CONTEXT) -n $(NAMESPACE) get configmap $(CONFIG_MAP_NAME) >/dev/null \
		|| (echo "ERROR: ConfigMap $(CONFIG_MAP_NAME) does not exist in $(KUBE_CONTEXT)/$(NAMESPACE) — chart must deploy it first"; exit 1)
	kubectl --context $(KUBE_CONTEXT) -n $(NAMESPACE) create configmap $(CONFIG_MAP_NAME) \
		--from-file=subscriptions.csv=config/subscriptions.csv \
		--from-file=own-proxies.yaml=config/own-proxies.yaml \
		--from-file=tokens.json=config/tokens.json \
		--dry-run=client -o yaml \
		| kubectl --context $(KUBE_CONTEXT) -n $(NAMESPACE) apply -f -
	@echo "config-sync: done. Pod will hot-reload within ~60s."

# 009 US3: sync the local config/custom-rules/ folder to the deployed PVC
# via a short-lived busybox helper pod. The runtime image is FROM scratch
# (no shell), so we cannot kubectl-cp directly into the application pod;
# the helper pod mounts the same PVC and accepts a tar stream.
rules-sync:
	@test -n "$(KUBE_CONTEXT)" || (echo "ERROR: set KUBE_CONTEXT=<context>  (e.g., make rules-sync KUBE_CONTEXT=<your-context> NAMESPACE=cms)"; exit 1)
	@test -n "$(NAMESPACE)" || (echo "ERROR: set NAMESPACE=<namespace>"; exit 1)
	@test -d config/custom-rules || (echo "ERROR: config/custom-rules/ directory missing"; exit 1)
	@test -f deploy/rules-sync-pod.yaml || (echo "ERROR: deploy/rules-sync-pod.yaml missing"; exit 1)
	@echo "rules-sync: context=$(KUBE_CONTEXT) namespace=$(NAMESPACE) helper=$(RULES_SYNC_POD) pvc=$(PVC_NAME)"
	@kubectl --context $(KUBE_CONTEXT) -n $(NAMESPACE) delete pod $(RULES_SYNC_POD) --ignore-not-found --wait=true >/dev/null
	$(_rules_sync_apply)
	@kubectl --context $(KUBE_CONTEXT) -n $(NAMESPACE) wait --for=condition=Ready pod/$(RULES_SYNC_POD) --timeout=60s
	@kubectl --context $(KUBE_CONTEXT) -n $(NAMESPACE) exec $(RULES_SYNC_POD) -- sh -c 'rm -rf /data/custom-rules/* && mkdir -p /data/custom-rules'
	@tar -cf - -C config/custom-rules . | kubectl --context $(KUBE_CONTEXT) -n $(NAMESPACE) exec -i $(RULES_SYNC_POD) -- sh -c 'tar -xf - -C /data/custom-rules/'
	@kubectl --context $(KUBE_CONTEXT) -n $(NAMESPACE) delete pod $(RULES_SYNC_POD) --wait=true
	@echo "rules-sync: done. Pod will hot-reload within ~250ms (fsnotify debounce)."
	@echo "If the new rules don't appear in the served body, fsnotify may have"
	@echo "missed the event across the bind-mount boundary — run 'make pod-restart'"
	@echo "to force a fresh load."

# 009: hard-reload — restart the application pod. Use when fsnotify hot-reload
# missed an update (e.g., after rules-sync, see the note above) or any time
# the operator wants a clean state. Cache survives (PVC). Bounded ≤30s downtime
# (Recreate strategy + RWO PVC remount).
pod-restart:
	@test -n "$(KUBE_CONTEXT)" || (echo "ERROR: set KUBE_CONTEXT=<context>"; exit 1)
	@test -n "$(NAMESPACE)" || (echo "ERROR: set NAMESPACE=<namespace>"; exit 1)
	@echo "pod-restart: rollout restart deploy/$(DEPLOYMENT_NAME) in $(KUBE_CONTEXT)/$(NAMESPACE)"
	kubectl --context $(KUBE_CONTEXT) -n $(NAMESPACE) rollout restart deploy/$(DEPLOYMENT_NAME)
	kubectl --context $(KUBE_CONTEXT) -n $(NAMESPACE) rollout status deploy/$(DEPLOYMENT_NAME) --timeout=120s
	@echo "pod-restart: done."

# 009: clear the upstream cache, then rollout restart. Use when the operator
# wants to force a cold re-fetch of all upstream subscriptions (e.g., after
# changing subscription URLs, or when an upstream provider has rotated TLS).
# The cache survives normal restarts; this target deliberately wipes it.
cache-clear:
	@test -n "$(KUBE_CONTEXT)" || (echo "ERROR: set KUBE_CONTEXT=<context>"; exit 1)
	@test -n "$(NAMESPACE)" || (echo "ERROR: set NAMESPACE=<namespace>"; exit 1)
	@test -f deploy/rules-sync-pod.yaml || (echo "ERROR: deploy/rules-sync-pod.yaml missing"; exit 1)
	@echo "cache-clear: clearing /data/cache/ via helper pod (pvc=$(PVC_NAME)), then rollout restart"
	@kubectl --context $(KUBE_CONTEXT) -n $(NAMESPACE) delete pod $(RULES_SYNC_POD) --ignore-not-found --wait=true >/dev/null
	$(_rules_sync_apply)
	@kubectl --context $(KUBE_CONTEXT) -n $(NAMESPACE) wait --for=condition=Ready pod/$(RULES_SYNC_POD) --timeout=60s
	@kubectl --context $(KUBE_CONTEXT) -n $(NAMESPACE) exec $(RULES_SYNC_POD) -- sh -c 'rm -rf /data/cache/* && mkdir -p /data/cache'
	@kubectl --context $(KUBE_CONTEXT) -n $(NAMESPACE) delete pod $(RULES_SYNC_POD) --wait=true
	$(MAKE) pod-restart KUBE_CONTEXT=$(KUBE_CONTEXT) NAMESPACE=$(NAMESPACE)
	@echo "cache-clear: done. Upstream subscriptions will be re-fetched on next request."

clean:
	rm -rf bin/ .cache/ coverage.out

run:
	@test -n "$$SUBSCRIPTIONS_CSV_PATH" || (echo "Set SUBSCRIPTIONS_CSV_PATH (and other env vars per quickstart §3)"; exit 1)
	go run ./cmd/server

# 013: SemVer release targets. See specs/013-ci-container-release/contracts/make-targets-contract.md
# and RELEASING.md for full operator docs.

# Shared precondition checks for all release targets. Loud-fail per Constitution
# Principle III applied to operator UX. Sets PHONY-style helper variables.
define _release_preconditions
	@if ! git diff --quiet || ! git diff --cached --quiet; then \
		echo "ERROR: working tree is dirty; commit or stash first"; exit 1; \
	fi
	@CURRENT_BRANCH=$$(git rev-parse --abbrev-ref HEAD); \
	EXPECTED_BRANCH=$${RELEASE_FROM_BRANCH:-master}; \
	if [ "$$CURRENT_BRANCH" != "$$EXPECTED_BRANCH" ]; then \
		echo "ERROR: not on $$EXPECTED_BRANCH (currently on $$CURRENT_BRANCH); set RELEASE_FROM_BRANCH=$$CURRENT_BRANCH if intentional"; exit 1; \
	fi
endef

release-patch:
	$(_release_preconditions)
	@. scripts/release-bump.sh && \
	LAST=$$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null || true); \
	NEXT=$$(bump_patch "$$LAST") || exit $$?; \
	if [ "$(DRY_RUN)" = "1" ]; then \
		echo "Would create tag: $$NEXT at $$(git rev-parse --short HEAD)"; exit 0; \
	fi; \
	git tag -a -m "Release $$NEXT" "$$NEXT" && \
	git push origin "$$NEXT" && \
	echo "" && \
	echo "Pushed $$NEXT — watch the release workflow at:" && \
	echo "  https://github.com/$$(git remote get-url origin | sed -E 's#.*github.com[:/]([^/]+/[^/.]+)(\.git)?#\1#')/actions"

release-minor:
	$(_release_preconditions)
	@. scripts/release-bump.sh && \
	LAST=$$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null || true); \
	NEXT=$$(bump_minor "$$LAST") || exit $$?; \
	if [ "$(DRY_RUN)" = "1" ]; then \
		echo "Would create tag: $$NEXT at $$(git rev-parse --short HEAD)"; exit 0; \
	fi; \
	git tag -a -m "Release $$NEXT" "$$NEXT" && \
	git push origin "$$NEXT" && \
	echo "" && \
	echo "Pushed $$NEXT — watch the release workflow at:" && \
	echo "  https://github.com/$$(git remote get-url origin | sed -E 's#.*github.com[:/]([^/]+/[^/.]+)(\.git)?#\1#')/actions"

release-major:
	$(_release_preconditions)
	@. scripts/release-bump.sh && \
	LAST=$$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null || true); \
	NEXT=$$(bump_major "$$LAST") || exit $$?; \
	if [ "$(DRY_RUN)" = "1" ]; then \
		echo "Would create tag: $$NEXT at $$(git rev-parse --short HEAD)"; exit 0; \
	fi; \
	git tag -a -m "Release $$NEXT" "$$NEXT" && \
	git push origin "$$NEXT" && \
	echo "" && \
	echo "Pushed $$NEXT — watch the release workflow at:" && \
	echo "  https://github.com/$$(git remote get-url origin | sed -E 's#.*github.com[:/]([^/]+/[^/.]+)(\.git)?#\1#')/actions"

# Explicit-version target. Use for skip-ahead, RC tags, or hotfix backports.
# All post-precondition logic is a single shell invocation so DRY_RUN's `exit 0`
# actually halts the recipe before any git side effect.
release:
	@if [ -z "$(VERSION)" ]; then echo "ERROR: VERSION required (e.g., make release VERSION=v2.0.0)"; exit 1; fi
	$(_release_preconditions)
	@. scripts/release-bump.sh && \
	if ! validate_version "$(VERSION)"; then \
		echo "ERROR: VERSION must match vMAJOR.MINOR.PATCH[-PRERELEASE], got: $(VERSION)"; exit 1; \
	fi; \
	if git rev-parse "$(VERSION)" >/dev/null 2>&1; then \
		echo "ERROR: tag $(VERSION) already exists locally"; exit 1; \
	fi; \
	if [ "$$(git ls-remote --tags origin "refs/tags/$(VERSION)" 2>/dev/null | wc -l)" -ne 0 ]; then \
		echo "ERROR: tag $(VERSION) already exists on origin"; exit 1; \
	fi; \
	if [ "$(DRY_RUN)" = "1" ]; then \
		echo "Would create tag: $(VERSION) at $$(git rev-parse --short HEAD)"; exit 0; \
	fi; \
	git tag -a -m "Release $(VERSION)" "$(VERSION)" && \
	git push origin "$(VERSION)" && \
	echo "" && \
	echo "Pushed $(VERSION) — watch the release workflow at:" && \
	echo "  https://github.com/$$(git remote get-url origin | sed -E 's#.*github.com[:/]([^/]+/[^/.]+)(\.git)?#\1#')/actions"

help:
	@echo "Common targets:"
	@echo "  make build             - compile to bin/server"
	@echo "  make test              - go test ./..."
	@echo "  make check             - vet + lint + test + snapshot-drift gate"
	@echo ""
	@echo "Image (009):"
	@echo "  make docker-build                          - buildx the image (linux/amd64)"
	@echo "  make docker-push                           - build + push :<git-sha>"
	@echo "  make docker-push-latest                    - build + push :<git-sha> AND :latest"
	@echo ""
	@echo "Cluster sync (009; both require KUBE_CONTEXT and NAMESPACE):"
	@echo "  make config-sync KUBE_CONTEXT=<your-context> NAMESPACE=cms"
	@echo "    Atomically replaces the ConfigMap with config/{subscriptions.csv,own-proxies.yaml,tokens.json}."
	@echo "  make rules-sync  KUBE_CONTEXT=<your-context> NAMESPACE=cms [PVC_NAME=<pvc>]"
	@echo "    Sync config/custom-rules/ to the PVC via a short-lived busybox helper pod."
	@echo "    PVC_NAME defaults to honkai-rule-server-data; override when the chart's"
	@echo "    release name produces a different PVC name (e.g., \`<release>-honkai-data\`)."
	@echo "  make pod-restart KUBE_CONTEXT=<your-context> NAMESPACE=cms"
	@echo "    Rollout restart the application pod (forces fresh rule load if"
	@echo "    fsnotify missed a rules-sync event). Cache survives."
	@echo "  make cache-clear KUBE_CONTEXT=<your-context> NAMESPACE=cms"
	@echo "    Wipe /data/cache/* and rollout restart. Forces upstream re-fetch."
	@echo ""
	@echo "Release (013; see RELEASING.md):"
	@echo "  make release-patch         - Cut a patch release (vX.Y.(Z+1)) from the most recent tag"
	@echo "  make release-minor         - Cut a minor release (vX.(Y+1).0) from the most recent tag"
	@echo "  make release-major         - Cut a major release (v(X+1).0.0); creates v1.0.0 if no prior tag"
	@echo "  make release VERSION=v...  - Cut an explicit-version release (RC / hotfix / skip-ahead)"
	@echo "                               Use DRY_RUN=1 to preview without creating/pushing the tag"
	@echo "                               Use RELEASE_FROM_BRANCH=<branch> for hotfix branches"
