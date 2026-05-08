# honkai-rule-server

A small Go service that aggregates multiple Mihomo / Clash subscription URLs into a single token-authenticated subscription endpoint. Adds operator-declared own-proxies, exposes aggregated traffic quota in the standard `Subscription-Userinfo` header, and reports a per-source-weighted daily allowance via `/health`.

## Documentation

- **Spec**: [`specs/001-subscription-aggregator/spec.md`](specs/001-subscription-aggregator/spec.md) — what this service does and why
- **Plan**: [`specs/001-subscription-aggregator/plan.md`](specs/001-subscription-aggregator/plan.md) — tech stack, project layout, test plan
- **Quickstart**: [`specs/001-subscription-aggregator/quickstart.md`](specs/001-subscription-aggregator/quickstart.md) — go from zero to a running merged subscription
- **Tasks**: [`specs/001-subscription-aggregator/tasks.md`](specs/001-subscription-aggregator/tasks.md) — implementation task list
- **Constitution**: [`.specify/memory/constitution.md`](.specify/memory/constitution.md) — non-negotiable engineering principles for this repo

## Layout

```
cmd/server/         entry point
internal/
  clock/            Clock interface (RealClock + FakeClock for deterministic tests)
  config/           subscriptions CSV / own-proxies YAML / tokens JSON / server env loaders
  fetcher/          per-source background fetch + cache + bootstrap state machine
  merge/            pure-functional transformation core (proxies, proxy-groups, rules, traffic)
  output/           subscription-mode adapter (renders the served Clash YAML)
  observability/    log/slog + token sanitization
  server/           net/http app + auth middleware + routes
  integration/      cross-package integration + snapshot tests
templates/          served-config template (Clash globals)
config/             operator config (gitignored — see config/README.md)
deploy/k8s/         Kubernetes manifests
example/            sandbox / sample inputs (NOT imported by the server)
```

## Build & test

```bash
make build           # → bin/server
make test            # all tests
make test-race       # all tests + race detector
make snapshot-update # accept new committed snapshot baselines (requires PR review)
make check           # CI gate: vet + lint + test + verify clean working tree
```

See the [quickstart](specs/001-subscription-aggregator/quickstart.md) for end-to-end run instructions.

## Run locally

```bash
# Copy fixtures as a starting point
cp internal/integration/testdata/fixtures/subscriptions.csv config/
cp internal/integration/testdata/fixtures/own-proxies.yaml  config/
cp internal/integration/testdata/fixtures/tokens.json       config/
# Edit config/subscriptions.csv to point at your real upstream URLs.

make build
SUBSCRIPTIONS_CSV_PATH=./config/subscriptions.csv \
OWN_PROXIES_YAML_PATH=./config/own-proxies.yaml \
TOKENS_PATH=./config/tokens.json \
SERVED_CONFIG_TEMPLATE_PATH=./templates/served-config.template.yaml \
CACHE_DIR=./.cache PORT=8080 LOG_LEVEL=info \
./bin/server
```

Then `curl 'http://localhost:8080/?token=<token-from-tokens.json>'` returns the merged Clash YAML.

See the [quickstart](specs/001-subscription-aggregator/quickstart.md) for the full operator workflow including K8s deployment.

## Deployment (Kubernetes)

Manifests under [`deploy/k8s/`](deploy/k8s/):

```bash
# 1. Stuff the three secret-bearing files into a Secret
kubectl create secret generic honkai-rule-server-config \
  --from-file=subscriptions.csv=./config/subscriptions.csv \
  --from-file=own-proxies.yaml=./config/own-proxies.yaml \
  --from-file=tokens.json=./config/tokens.json

# 2. Apply manifests
kubectl apply -f deploy/k8s/configmap-served-template.yaml
kubectl apply -f deploy/k8s/deployment.yaml
kubectl apply -f deploy/k8s/service.yaml
kubectl apply -f deploy/k8s/ingress.yaml   # edit hostname first
```

The Ingress handles TLS termination per FR-019c. **Only `/` is exposed** to the public internet; `/health` is intentionally omitted (it leaks per-upstream state). Hit `/health` from the cluster-internal Service.

## Constitution + spec coverage

- All 5 Constitution principles (Unified Transformation Core, Deterministic Transformation, CSV strict-schema/loud-failure, Test-First/Real-Input Integration, Observable Routing & Source-Merge Decisions) — gates pass; see plan.md Constitution Check.
- All 27 functional requirements (FR-001..FR-019c) and 15 success criteria (SC-001..SC-015) implemented and tested. See [`specs/001-subscription-aggregator/spec.md`](specs/001-subscription-aggregator/spec.md).
- Three deterministic snapshots committed under `internal/integration/testdata/snapshots/` pin the served-config body, the `Subscription-Userinfo` wire format, and the `/health` JSON response shape (Constitution Principle II).

## Module path

The committed `go.mod` uses a placeholder owner (`github.com/junlinchen/honkai-rule-server`). After creating the actual GitHub repo, run:

```bash
go mod edit -module github.com/<your-org>/honkai-rule-server
go mod tidy
goimports -w .   # update import paths in any code that referenced the old path
```
