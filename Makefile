.PHONY: build test test-race test-unit test-integration snapshot-update lint vet check docker docker-build docker-push docker-push-latest config-sync rules-sync pod-restart cache-clear clean run help

# Operator-private overrides — see .env.example. Optional; safe to skip.
ifneq (,$(wildcard .env))
include .env
export
endif

BINARY := bin/server
PKG := ./...
INTEGRATION_PKG := ./internal/integration/...

# 009: image build & publish
IMAGE_REPO ?= registry.example.com/library/honkai-rule-server
IMAGE_TAG  ?= $(shell git rev-parse --short HEAD)
IMAGE      := $(IMAGE_REPO):$(IMAGE_TAG)

# 009: config sync — all live targets require KUBE_CONTEXT and NAMESPACE.
# Names follow the chart's release pattern: ConfigMap, PVC, and
# Deployment are all `<release>{,-config,-data}`. The default
# release name is `honkai-rule-server`.
DEPLOYMENT_NAME ?= honkai-rule-server
CONFIG_MAP_NAME ?= honkai-rule-server-config
RULES_SYNC_POD  ?= honkai-rules-sync

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
	@echo "rules-sync: context=$(KUBE_CONTEXT) namespace=$(NAMESPACE) helper=$(RULES_SYNC_POD)"
	@kubectl --context $(KUBE_CONTEXT) -n $(NAMESPACE) delete pod $(RULES_SYNC_POD) --ignore-not-found --wait=true >/dev/null
	@kubectl --context $(KUBE_CONTEXT) -n $(NAMESPACE) apply -f deploy/rules-sync-pod.yaml
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
	@echo "cache-clear: clearing /data/cache/ via helper pod, then rollout restart"
	@kubectl --context $(KUBE_CONTEXT) -n $(NAMESPACE) delete pod $(RULES_SYNC_POD) --ignore-not-found --wait=true >/dev/null
	@kubectl --context $(KUBE_CONTEXT) -n $(NAMESPACE) apply -f deploy/rules-sync-pod.yaml
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
	@echo "  make rules-sync  KUBE_CONTEXT=<your-context> NAMESPACE=cms"
	@echo "    Sync config/custom-rules/ to the PVC via a short-lived busybox helper pod."
	@echo "  make pod-restart KUBE_CONTEXT=<your-context> NAMESPACE=cms"
	@echo "    Rollout restart the application pod (forces fresh rule load if"
	@echo "    fsnotify missed a rules-sync event). Cache survives."
	@echo "  make cache-clear KUBE_CONTEXT=<your-context> NAMESPACE=cms"
	@echo "    Wipe /data/cache/* and rollout restart. Forces upstream re-fetch."
