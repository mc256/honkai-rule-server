# Quickstart: Subscription Aggregator (rev 2 — Go)

**Feature**: `001-subscription-aggregator` | **Date**: 2026-04-30

This is the operator's "go from zero to a working merged subscription" guide once the implementation lands. Run from the repository root.

---

## Prerequisites

- **Go 1.23.x** — `go version` should print `go1.23.x`. Install from https://go.dev/dl/ or via `gvm` / `asdf`.
- That's it. No databases, no Redis, no Node, no Python.

---

## 1. Build

```bash
go mod tidy             # first time only — fetches the four direct deps
go build -o bin/server ./cmd/server
```

Or run directly without building:

```bash
go run ./cmd/server
```

---

## 2. Prepare configuration files

The server reads four files at startup:

| File | Env var | Format | Example fixture |
|---|---|---|---|
| Subscriptions CSV | `SUBSCRIPTIONS_CSV_PATH` | CSV (FR-001a) | `example/subscriptions.csv` |
| Own-proxies YAML | `OWN_PROXIES_YAML_PATH` | YAML, `proxies` + `proxy-groups` keys | (create — see below) |
| Tokens JSON | `TOKENS_PATH` | JSON | (create — see below) |
| Served-config template | `SERVED_CONFIG_TEMPLATE_PATH` | YAML with `__MERGED_PROXIES__`, `__MERGED_PROXY_GROUPS__`, `__MERGED_RULES__` placeholders | `templates/served-config.template.yaml` |

### 2a. Subscriptions CSV

Copy the example:

```bash
mkdir -p config
cp example/subscriptions.csv config/subscriptions.csv
```

Schema (matches `example/subscriptions.csv`):

```csv
name,link,priority,enable
alpha,https://upstream.example.com/link/<your-token>?clash=1,1000,Enable
beta,https://upstream.example.com:8443/<your-path-token>,2000,Enable
```

- **`name`**: unique short label, used internally and in collision suffixes (`<proxy-name>@<source-name>`).
- **`link`**: full upstream subscription URL including any embedded credentials.
- **`priority`**: integer; **higher number = rules emit earlier in the merged output**. CSS `z-index` style. Above, `beta` rules go first.
- **`enable`**: `Enable` or `Disable` (case-insensitive). Disabled rows are validated but skipped.

Optional columns: `ttl_seconds`, `stale_on_error_seconds` (override per-source defaults).

### 2b. Own-proxies YAML

Create `config/own-proxies.yaml`:

```yaml
proxies:
  - name: my-home-server
    type: trojan
    server: home.example.com
    port: 443
    password: <your-password>
    sni: home.example.com

proxy-groups:
  - name: My-Own
    type: select
    proxies:
      - my-home-server
```

Empty arrays are valid (`proxies: []` and `proxy-groups: []`) — start there if you have no own proxies yet.

### 2c. Tokens JSON

Generate a token and create `config/tokens.json`:

```bash
TOKEN=$(openssl rand -hex 32)
cat > config/tokens.json <<EOF
{
  "tokens": [
    {
      "token": "$TOKEN",
      "label": "my-laptop",
      "issued_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
      "revoked": false
    }
  ]
}
EOF
echo "Save your subscription URL: http://localhost:8080/?token=$TOKEN"
```

### 2d. Served-config template

The default template ships at `templates/served-config.template.yaml`. Tune `mode`, `dns`, `mixed-port`, etc. to your preference. Leave the three `__MERGED_*__` placeholders unchanged.

---

## 3. Run the server

```bash
SUBSCRIPTIONS_CSV_PATH=./config/subscriptions.csv \
OWN_PROXIES_YAML_PATH=./config/own-proxies.yaml \
TOKENS_PATH=./config/tokens.json \
SERVED_CONFIG_TEMPLATE_PATH=./templates/served-config.template.yaml \
CACHE_DIR=./.cache \
PORT=8080 \
LOG_LEVEL=info \
go run ./cmd/server
```

Expected log sequence (each line is a single JSON object, formatted here for readability):

```json
{"time":"...","level":"INFO","msg":"loaded subscriptions CSV","sources":["alpha","beta"],"disabled":[]}
{"time":"...","level":"INFO","msg":"loaded own-proxies","proxies":1,"groups":1}
{"time":"...","level":"INFO","msg":"loaded token store","activeTokens":1,"revokedTokens":0}
{"time":"...","level":"INFO","msg":"bootstrap: fetching all sources","sources":["alpha","beta"]}
{"time":"...","level":"INFO","msg":"upstream fetch ok","source":"alpha","status":200,"payloadBytes":79166,"profileUpdateIntervalHours":12}
{"time":"...","level":"INFO","msg":"upstream fetch ok","source":"beta","status":200,"payloadBytes":...}
{"time":"...","level":"INFO","msg":"bootstrap complete","durationMs":1234}
{"time":"...","level":"INFO","msg":"server listening","port":8080}
```

---

## 4. Verify

### 4a. Health endpoint

```bash
curl -s http://localhost:8080/health | jq
```

Expected fields: `bootstrap: "succeeded"`, two sources both `bootstrapState: "succeeded"`, a non-zero `dailyAllowance.perDayRateBytes`.

### 4b. Subscription endpoint (subscription-only mode v1)

```bash
curl -i "http://localhost:8080/?token=$TOKEN" | head -20
```

Expected:

```
HTTP/1.1 200 OK
Content-Type: application/yaml; charset=utf-8
Subscription-Userinfo: upload=...; download=...; total=...; expire=...
Profile-Update-Interval: 12
Cache-Control: no-store, no-cache, must-revalidate

mixed-port: 7890
allow-lan: false
mode: rule
...
```

The body should be a valid Clash config; pipe to `yq .` to inspect.

### 4c. Auth rejection

```bash
curl -i http://localhost:8080/                          # 401, no body
curl -i http://localhost:8080/?token=bogus              # 401
```

The server log line for these MUST contain `sha256:<12-hex>`, **never** the token plaintext.

### 4d. Add the URL to a real Mihomo / Sparkle client

Paste `http://localhost:8080/?token=<your-token>` (or your TLS-fronted production URL) into the client's "Add Profile from URL" field. The client should:

- Show a non-empty proxy list.
- Display the aggregated usage bar (sourced from the `Subscription-Userinfo` header).
- Auto-refresh on the cadence in `Profile-Update-Interval` (12 hours by default in our example fixtures).
- Have at least the always-present `Proxies` group selectable in the UI.

---

## 5. Operate

### Reload configuration

The server hot-reloads when any of `config/subscriptions.csv`, `config/own-proxies.yaml`, or `config/tokens.json` changes on disk (debounced 250ms via `fsnotify`). Reload errors leave the previously-loaded config in effect (FR-017).

### Disable a source temporarily

Edit the CSV, change `Enable` to `Disable` for the row, save. The server logs the disabled set on next reload; the source is no longer fetched and its proxies vanish from the served output on the next request.

### Revoke a token

Edit `config/tokens.json`, set `"revoked": true` on the offending entry, save. Next request with that token gets a 401.

### See what changed in the merged output

A failed snapshot test in CI is the structural way; locally, you can re-run the merge against committed fixtures and diff the result:

```bash
go test ./internal/integration/... -run TestSnapshot_ServedConfig -v
# To accept the new output as the new baseline (after PR review):
UPDATE_SNAPSHOTS=true go test ./internal/integration/... -run TestSnapshot_
```

---

## 6. Run the tests

```bash
go test ./...                                         # all tests
go test ./internal/config/...                         # unit tests for config package only
go test -race ./...                                   # all tests + race detector (run before any PR)
go test ./internal/integration/...                    # integration + snapshot tests
UPDATE_SNAPSHOTS=true go test ./internal/integration/...  # accept new snapshots
go test -cover ./...                                  # with coverage
```

Snapshot updates require explicit `UPDATE_SNAPSHOTS=true` AND a PR review (per Constitution Development Workflow snapshot-stability gate).

A `make check` target is recommended in the Makefile that runs `go vet ./... && staticcheck ./... && go test ./... && git diff --exit-code` so CI catches inadvertently-modified snapshots.

---

## 7. Build for deployment

### Local container build

```bash
docker build -t honkai-rule-server:dev .
docker run --rm -p 8080:8080 \
  -v $(pwd)/config:/config:ro \
  -v $(pwd)/templates:/templates:ro \
  -v $(pwd)/.cache:/cache \
  -e SUBSCRIPTIONS_CSV_PATH=/config/subscriptions.csv \
  -e OWN_PROXIES_YAML_PATH=/config/own-proxies.yaml \
  -e TOKENS_PATH=/config/tokens.json \
  -e SERVED_CONFIG_TEMPLATE_PATH=/templates/served-config.template.yaml \
  -e CACHE_DIR=/cache \
  -e PORT=8080 \
  honkai-rule-server:dev
```

The Dockerfile is multi-stage: `golang:1.23-alpine` builds a static binary (`CGO_ENABLED=0 go build -ldflags="-s -w"`), the final stage is `FROM scratch` with only the binary + a CA bundle (~15MB image).

### Deploy to Kubernetes

Manifests under `deploy/k8s/`:

```bash
# Create the secret containing the three secret-bearing files
kubectl create secret generic honkai-rule-server-config \
  --from-file=subscriptions.csv=./config/subscriptions.csv \
  --from-file=own-proxies.yaml=./config/own-proxies.yaml \
  --from-file=tokens.json=./config/tokens.json

# Apply manifests
kubectl apply -f deploy/k8s/configmap-served-template.yaml
kubectl apply -f deploy/k8s/deployment.yaml
kubectl apply -f deploy/k8s/service.yaml
kubectl apply -f deploy/k8s/ingress.yaml
```

The Ingress handles TLS termination per FR-019c. Expose only the served subscription path; do **not** expose `/health` to the public internet (it leaks per-upstream state to anyone who can reach it).

---

## 8. Module path (one-time setup)

The committed `go.mod` uses a placeholder module path: `github.com/<owner>/honkai-rule-server`. After creating the GitHub repository, update the module path:

```bash
go mod edit -module github.com/<your-org>/honkai-rule-server
go mod tidy
goimports -w .   # update import paths in any code that referenced the module path explicitly
```

This is a one-time action; subsequent contributors don't need to repeat it.

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| 503 on first request | Bootstrap window still open | Wait. Check `/health` for which sources are `bootstrapState: pending`. |
| 503 persists | An enabled source has no cache and is failing fetches | Check logs for `upstream fetch failed` lines. Either fix the upstream credentials in CSV, mark the source `Disable`, or wait for retries to succeed. |
| 401 on every request | Token typo or revocation | `cat config/tokens.json` and verify the URL token matches an active record. |
| Proxy list missing some upstreams | Source disabled or stale-no-cache | `curl /health \| jq '.sources'` and look for `enabled: false` or `lastFetchOutcome` other than `success`. |
| Snapshot test fails after legitimate change | Expected — snapshot drift is the deterministic-output gate | Run `UPDATE_SNAPSHOTS=true go test ./internal/integration/...`, **manually inspect** the diff in PR, get reviewer approval per Constitution Development Workflow. |
| Logs contain a raw token | A code path bypassed `observability.SanitizeToken()` — security regression | File a P0; revert the offending change; rotate every active token. |
| `go: github.com/...: no required module provides package` | Module path mismatch (you renamed it but didn't update imports) | `goimports -w .` then `go mod tidy`. |
| Race detector failures (`go test -race`) | Concurrent map access in cache or token store | The `RWMutex` in `internal/fetcher/cache.go` and `internal/config/tokens.go` is the canonical guard. Any new shared state needs the same treatment — file a P1. |
