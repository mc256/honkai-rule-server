# Implementation Plan: Subscription Aggregator (Multi-Source Merge + Own Proxies + Traffic Accounting)

**Branch**: `001-subscription-aggregator` | **Date**: 2026-04-30 (rev 2 — Go) | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/001-subscription-aggregator/spec.md`

## Summary

The MVP server fetches multiple upstream Mihomo/Clash subscriptions on independent per-source background goroutines, merges their `proxies` / `proxy-groups` / `rules` blocks (with deterministic name-collision resolution and CSS `z-index`-style per-source rule priority), folds in user-declared own-proxies and own-proxy-groups from a separate YAML file, renders the result through a server-side Clash-config template, and serves it from a single token-authenticated HTTPS endpoint. The response carries a Clash-format `Subscription-Userinfo` header with **aggregated** quota figures (sum of upload/download/total, earliest non-zero `expire`) and a `Profile-Update-Interval` header. A `/health` endpoint exposes per-upstream fetch state and a per-source-weighted **daily allowance** figure. Configuration: subscriptions in CSV (`name`, `link`, `priority`, `enable` + optional TTL/stale columns) — schema and column names match the format used in `example/subscriptions.csv`; own-proxies in a YAML file with `proxies` and `proxy-groups` keys.

**Technical approach**: pure-functional transformation core (`internal/merge/`) operating on snapshotted upstream payloads + own-proxies + traffic metadata, separated from the side-effecting fetcher (`internal/fetcher/`) so determinism and snapshot testing are tractable. Output adapter is a single render function today (subscription mode); structured to accept an override-mode adapter later without re-shaping the merge layer. Single static binary deployed in a minimal container.

## Technical Context

**Language/Version**: **Go 1.23**. (Choice rationale: long-running HTTP server with per-source background fetchers, K8s deployment, structured logging — Go's goroutine model and standard library cover ~80% of needs out of the box. Go 1.22's `net/http` pattern routing means we don't need a third-party router for two endpoints; Go 1.21's `log/slog` gives us structured JSON logging without a third-party logger. Single static binary keeps the deployment image small.)
**Primary Dependencies**: `gopkg.in/yaml.v3` (parse/emit YAML using `yaml.Node` so key order is preserved — essential for byte-identical output per Constitution Principle II), `github.com/fsnotify/fsnotify` (cross-platform file-watcher for hot reload of CSV / YAML / tokens), `github.com/bradleyjkemp/cupaloy/v2` (snapshot tests with `-update` flag UX matching Vitest), `golang.org/x/sync/singleflight` (per-source single-flight coordination so a thundering herd of clients triggers exactly one upstream fetch).
**Storage**: In-memory cache (`map[string]*CachedPayload` guarded by `sync.RWMutex`) for upstream payloads + parsed metadata. Optional disk-persisted cache (write-on-fetch JSON to `${CACHE_DIR}/${name}.json`) so a pod restart in K8s does not flush cache and re-hammer upstreams. Token store: single JSON file at a server-config path, hot-reloadable via `fsnotify`.
**Testing**: stdlib `testing` package + `cupaloy/v2` for file-backed snapshots committed under `internal/integration/testdata/snapshots/`. Tests next to source files as `*_test.go`; integration tests in their own package (`internal/integration`). Time injected via a `Clock` interface so daily-allowance tests are deterministic.
**Target Platform**: Linux container (`FROM scratch` with a CGO-disabled static binary — image ≈15 MB). Production deployment: Kubernetes; TLS terminated at Ingress; server listens plain HTTP on a ClusterIP service. Local dev: `go run ./cmd/server`.
**Project Type**: Single-project backend service (no frontend, no mobile, no CLI).
**Performance Goals**: p95 < 20ms to serve a subscription request from the in-memory cache (Go's goroutine scheduling and `sync.RWMutex` make this trivially achievable; cache absorbs all client-side load per FR-003a). Background fetcher: one goroutine per source on a `time.Ticker` matching `ttl_seconds` (default 1h); zero upstream fetches on the request path.
**Constraints**: Byte-identical output across runs given identical inputs (Constitution Principle II) — implemented by holding upstream payloads as `yaml.Node` (order-preserving) and using stable sort orders everywhere a sort happens. Memory ceiling: ≤128Mi (Go's smaller runtime footprint vs Node lets us drop one tier). Cold-start: server MUST refuse client traffic with 503 until the bootstrap state machine reports every enabled source as `succeeded` (FR-003b).
**Scale/Scope**: 2–10 upstreams typical, 100s of proxies aggregate. 1–10 own-proxies. 1–20 client tokens. ~2500 lines of Go total expected (Go is more verbose than TS for the schema-validation surface but more concise everywhere else; net wash).
**Module path**: `github.com/<owner>/honkai-rule-server` — placeholder. Change with `go mod edit -module github.com/<your-org>/honkai-rule-server` when the GitHub repo is created. Internal imports below assume this module path.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

Evaluated against `.specify/memory/constitution.md` v1.0.0 (ratified 2026-04-26).

| Principle / Constraint | Status | Notes |
|---|---|---|
| **I. Unified Transformation Core** | PASS | One `internal/merge/Pipeline` produces an internal merged-config struct. The output adapter (`internal/output/SubscriptionMode`) is a thin render step; the override-mode adapter (deferred) plugs in at the same boundary. No mode-only logic deeper than the adapter. |
| **II. Deterministic Transformation** | PASS | Merge layer is pure (no `time.Now()`, no `rand`, no map-iteration order assumptions — every map iterated is sorted by key first; `yaml.Node` round-trips preserve upstream key order). Daily-allowance calc takes a `Clock` interface so tests inject a fixed `time.Time`. Snapshot tests against committed fixtures pin output stability. |
| **III. CSV Rules — Strict Schema, Loud Failure** | PASS (vacuously for the *rules* CSV — out of scope for MVP) and PASS (substantively for the *subscriptions* CSV — FR-001a/b enforce strict schema, no silent skip). Spec explicitly distinguishes the two artifacts (subscriptions CSV is secret-bearing per FR-016; rules CSV is the future Principle-III artifact and is reviewable). |
| **IV. Test-First, Real-Input Integration (NON-NEGOTIABLE)** | PASS | Test Plan section (this document, end) lists snapshot fixtures and integration tests against `example/3rd-party-sub/{alpha,beta}.yaml` and `example/subscriptions.csv`. Order-of-work: test → fail → implement → green. CI gate: snapshot drift fails the build. |
| **V. Observable Routing & Source-Merge Decisions** | PASS | `log/slog` JSON handler emits structured logs at every gate: per-fetch (sanitized URL, status, payload bytes, cache-hit/miss, applied failure rule), per-merge (proxy-name collision + suffix applied, proxy-group same-name union + attribute conflict, rules priority order), per-served-request (token-hash prefix, contributing sources, header values). Verbosity configurable via `LOG_LEVEL`. |
| **Routing — corporate isolation** | N/A | No corporate routing introduced in this MVP. Will become relevant when the rules-CSV feature lands. |
| **Routing — multi-subscription collision resolution** | PASS | Per-source suffix `<name>@<source-name>`; deterministic; logged on collision. Asserted by integration test against the two example upstreams (no real collision in fixtures, so test injects a synthetic one). |
| **Routing — fetch failure modes** | PASS | Per-source TTL + per-source stale-on-error window from CSV; bootstrap fail-closed; degraded source flagged in `/health`. |
| **Routing — carve-outs** | N/A | No carve-outs in MVP. |
| **Security — secrets boundary** | PASS | Subscriptions CSV path + own-proxies YAML path + token store path all sourced from env vars; `.gitignore` carries `*.csv` and `tokens.json` patterns under `config/`. The example file we ship is a deliberate sample (real tokens from the user's example will be replaced with synthetic values in the test fixtures). |
| **Security — sanitized output** | PASS | Output adapter strips upstream `Subscription-Userinfo` echoes, never includes upstream URL anywhere in the served body, never logs token plaintext (logs `sha256:<first-12-hex>` via `observability.SanitizeToken`). |
| **Security — CSV reviewable, not secret** | N/A for this CSV (subscriptions CSV is secret-bearing); the rules CSV (Principle III) will satisfy this when it lands. |

**Verdict**: All gates pass. **No Complexity Tracking entries required.**

## Project Structure

### Documentation (this feature)

```text
specs/001-subscription-aggregator/
├── spec.md                                       # Feature specification (input)
├── plan.md                                       # This file
├── research.md                                   # Phase 0 output
├── data-model.md                                 # Phase 1 output
├── quickstart.md                                 # Phase 1 output
├── contracts/
│   ├── served-subscription.openapi.yaml         # GET / contract (language-agnostic)
│   └── health.openapi.yaml                      # GET /health contract (language-agnostic)
├── templates/
│   └── served-config.template.yaml              # Draft of the v1 served-config template
├── checklists/
│   └── requirements.md                          # Spec quality checklist
└── tasks.md                                      # (Phase 2 — produced by /speckit-tasks)
```

### Source Code (repository root)

Standard Go layout: `cmd/` for binary entry points, `internal/` for everything that should not be importable by external modules.

```text
cmd/
└── server/
    └── main.go                                  # Entry point: load config -> start scheduler -> start server

internal/
├── config/
│   ├── subscriptions.go                         # CSV loader + schema validation (FR-001a, FR-001b)
│   ├── subscriptions_test.go
│   ├── own_proxies.go                           # Own-proxies YAML loader + validation (FR-006, FR-007)
│   ├── own_proxies_test.go
│   ├── tokens.go                                # Token store (file-backed JSON, hot-reloadable; FR-019..019a)
│   ├── tokens_test.go
│   ├── server.go                                # Env-var bound server config (paths, defaults, ports)
│   └── testdata/                                # Per-package fixtures for unit tests
├── fetcher/
│   ├── http.go                                  # Upstream fetch via net/http; per-fetch context.WithTimeout (FR-005b, FR-010)
│   ├── http_test.go
│   ├── headers.go                               # Subscription-Userinfo + Profile-Update-Interval parsers
│   ├── headers_test.go
│   ├── cache.go                                 # In-memory map (RWMutex) + optional disk JSON persistence (FR-003)
│   ├── cache_test.go
│   ├── scheduler.go                             # Per-source goroutine on time.Ticker; bootstrap state machine (FR-003a, FR-003b)
│   └── scheduler_test.go
├── merge/
│   ├── proxies.go                               # Proxy union, name-collision suffix (FR-002, FR-008)
│   ├── proxies_test.go
│   ├── proxy_groups.go                          # Same-name union merge (FR-008a) + always-present `Proxies` group (FR-009a)
│   ├── proxy_groups_test.go
│   ├── rules.go                                 # Priority-ordered merge (FR-005a)
│   ├── rules_test.go
│   ├── traffic.go                               # Aggregation + per-source weighted daily allowance (FR-010..FR-012, FR-011b)
│   ├── traffic_test.go
│   ├── pipeline.go                              # Orchestrates merge -> renders internal MergedConfig
│   └── pipeline_test.go
├── output/
│   ├── subscription_mode.go                     # Renders MergedConfig + served-config template -> YAML body + headers (FR-005c, FR-011, FR-011a)
│   └── subscription_mode_test.go
├── server/
│   ├── app.go                                   # net/http mux, route registration, graceful shutdown
│   ├── auth.go                                  # Token validation middleware
│   ├── auth_test.go
│   └── routes/
│       ├── subscription.go                      # GET / (token-authed) (FR-019, FR-019b)
│       ├── subscription_test.go
│       ├── health.go                            # GET /health (FR-015 + daily allowance from FR-011b)
│       └── health_test.go
├── observability/
│   ├── logger.go                                # log/slog JSON handler + SanitizeToken helper (FR-013, FR-014)
│   └── logger_test.go
├── clock/
│   └── clock.go                                 # Clock interface + RealClock + FakeClock (for deterministic time in tests)
└── integration/                                 # Cross-package integration tests
    ├── pipeline_test.go                         # TC-I-* full pipeline tests
    ├── snapshot_test.go                         # TC-S-* snapshot tests
    └── testdata/
        ├── fixtures/
        │   ├── subscriptions.csv                # Copy of example/subscriptions.csv with synthetic tokens
        │   ├── upstream/
        │   │   ├── alpha.yaml                   # Copy of example/3rd-party-sub/alpha.yaml
        │   │   └── beta.yaml               # Copy of example/3rd-party-sub/beta.yaml
        │   ├── own-proxies.yaml                 # Synthetic own-proxies fixture
        │   └── tokens.json                      # Synthetic tokens fixture
        └── snapshots/
            ├── served-config.snap.yaml          # TC-S-01
            ├── subscription-userinfo.snap.txt   # TC-S-02
            └── health.snap.json                 # TC-S-03

templates/
└── served-config.template.yaml                  # Production location of the served-config template (FR-005c)

config/
├── .gitignore                                   # Ignores subscriptions.csv, own-proxies.yaml, tokens.json
└── README.md                                    # Operator notes on file layout

deploy/
└── k8s/
    ├── deployment.yaml                          # Container spec, env, mounts
    ├── service.yaml                             # ClusterIP
    ├── ingress.yaml                             # TLS termination (FR-019c)
    └── configmap-served-template.yaml           # served-config.template.yaml as ConfigMap
                                                  # secrets (CSV, own-proxies, tokens) come from a Secret
                                                  # mounted at env-pointed paths

go.mod
go.sum
Dockerfile                                       # Multi-stage: golang:1.23-alpine -> scratch (static binary)
Makefile                                         # build / test / snapshot-update / lint / docker
README.md
```

**Structure Decision**: Standard Go layout with `cmd/` for the binary entry point and `internal/` for all packages (preventing accidental external imports). The `internal/merge/` package is the unified transformation core (Constitution Principle I); `internal/output/` holds the only adapter shipped in v1. The `internal/fetcher/` package is the only source of nondeterminism and is fully isolated from `internal/merge/` so the latter is testable against pure inputs. Time is sourced from an injected `internal/clock.Clock` so daily-allowance and bootstrap-window tests are deterministic.

## Phase Outputs

| Phase | Artifact | Status |
|---|---|---|
| 0 (research) | `research.md` | Generated this session (rev 2 for Go) |
| 1 (data model) | `data-model.md` | Generated this session (rev 2 for Go) |
| 1 (contracts) | `contracts/served-subscription.openapi.yaml`, `contracts/health.openapi.yaml` | Unchanged from rev 1 (language-agnostic) |
| 1 (quickstart) | `quickstart.md` | Generated this session (rev 2 for Go) |
| 1 (agent context) | `CLAUDE.md` plan reference | Already pointed at this plan; no update needed |
| 2 (tasks) | `tasks.md` | **Pending** — produced by `/speckit-tasks` |

## Re-evaluation: Constitution Check (post-Phase 1)

After designing the data model and contracts in Go, no principle violations surfaced. The `internal/merge/` package remains pure-functional (verified by interface design — every function takes its inputs explicitly, returns a value, performs no I/O); the output adapter is the only mode-aware component; the snapshot test catalog (Test Plan below) covers both the merge layer and the rendered served body. Go's package-internal visibility (`internal/`) gives us a sharper boundary than TS module exports — even if a future contributor wanted to reach into the merge layer from outside the module, the compiler refuses. **All gates remain green; no Complexity Tracking required.**

## Complexity Tracking

> No entries — Constitution Check passed without justified deviations.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| _(none)_ | _(none)_ | _(none)_ |

---

## Test Plan

> Per the user's request, this section enumerates the concrete test cases that will land **before** the matching implementation (Constitution Principle IV). Fixtures reference real files at `example/3rd-party-sub/` and `example/subscriptions.csv` so the tests exercise real provider output shapes (Chinese / emoji proxy names, flow-style YAML, mixed proxy types: `hysteria2`, `trojan`, `vless`, `vmess`).

**Tooling**: stdlib `testing` package; snapshots via `github.com/bradleyjkemp/cupaloy/v2` (file-backed under `internal/integration/testdata/snapshots/`, updated with `UPDATE_SNAPSHOTS=true go test ./...`). Time injected via `internal/clock.FakeClock` so daily-allowance and TTL tests are deterministic. Run all tests with `go test ./...`; integration-only with `go test ./internal/integration/...`.

### Test Pyramid

```text
         +----------------------+
         | Snapshot tests (3)   |   Pin byte-identical merged output (FR-004 / SC-002 / Principle II)
         +----------------------+
         | Integration tests    |   Full pipeline: CSV -> fetcher (httptest) -> merge -> output -> http.Client
         |   (~ 18 cases)       |
         +----------------------+
         |   Unit tests (~50)   |   Per-package: parsers, validators, merge primitives, traffic math
         +----------------------+
```

### TC-U: Unit tests

#### Subscriptions CSV loader (`internal/config/subscriptions.go` + `subscriptions_test.go`)

- **TC-U-CSV-01** Loads `example/subscriptions.csv` and returns two rows: `{Name: "alpha", Link: "...upstream.example.com...", Priority: 1000, Enable: true}` and `{Name: "beta", Link: "...provider.example.com...", Priority: 2000, Enable: true}`.
- **TC-U-CSV-02** File missing → returns `*ConfigLoadError` wrapping the `os.PathError` with the missing path in `.Error()`.
- **TC-U-CSV-03** Missing `name` column header → `*ConfigSchemaError` listing missing required columns.
- **TC-U-CSV-04** Duplicate `name` value across rows → `*ConfigValidationError` naming the duplicate.
- **TC-U-CSV-05** Non-integer `priority` (e.g., `"high"`) → `*ConfigValidationError`.
- **TC-U-CSV-06** `enable` value `"yes"` → `*ConfigValidationError` (only `Enable`/`Disable` accepted, case-insensitive — use `strings.EqualFold`).
- **TC-U-CSV-07** Unknown column `"foo"` → `*ConfigSchemaError` (no silent ignore).
- **TC-U-CSV-08** `Disable` row is loaded, validated, returned with `Enable: false`.
- **TC-U-CSV-09** `link` is not a valid URL (parsed via `net/url.Parse`) → `*ConfigValidationError`.
- **TC-U-CSV-10** Optional `ttl_seconds` and `stale_on_error_seconds` parse as integers when present; absent values stay zero (caller substitutes `ServerConfig` defaults).

#### Own-proxies YAML loader (`internal/config/own_proxies.go`)

- **TC-U-OWN-01** Loads a fixture with `proxies` (2 entries) and `proxy-groups` (1 entry) and returns `*OwnProxiesFile` with parsed `yaml.Node` slices.
- **TC-U-OWN-02** Missing required field on a proxy (`server`, `port`, or `type`) → `*OwnProxyValidationError` naming the offending entry.
- **TC-U-OWN-03** Duplicate proxy `name` within file → `*OwnProxyValidationError`.
- **TC-U-OWN-04** `proxy-groups` entry references a non-existent proxy name → `*OwnProxyValidationError` (catches typos before merge).
- **TC-U-OWN-05** Empty `proxies: []` and empty `proxy-groups: []` are valid (the user has no own-proxies yet).

#### Token store (`internal/config/tokens.go`)

- **TC-U-TOK-01** Valid token in store → `Lookup(token)` returns `(*TokenRecord, true)`.
- **TC-U-TOK-02** Unknown token → `Lookup` returns `(nil, false)`; no log line carries the token plaintext (verified by capturing the test logger's buffer).
- **TC-U-TOK-03** Revoked token (`Revoked: true`) → `Lookup` returns `(nil, false)` even though the record exists.
- **TC-U-TOK-04** Hot reload: write a new token to the store file → next `Lookup` sees it within the debounce window (test uses `chan` from the watcher to await reload).
- **TC-U-TOK-05** `observability.SanitizeToken` produces `sha256:<12-hex>` from any token.

#### Header parsers (`internal/fetcher/headers.go`)

- **TC-U-HDR-01** `Subscription-Userinfo: upload=23398198706; download=203036431271; total=654791671808; expire=1804180937` (real value from alpha example) → `&SubscriptionUserinfo{Upload: 23398198706, Download: 203036431271, Total: 654791671808, Expire: 1804180937}`.
- **TC-U-HDR-02** Missing field (e.g., no `expire=`) → returns the parsed fields and a `Missing []string{"expire"}` slice (do NOT default `expire` to `0`; `0` is a meaningful "no-expiry" signal).
- **TC-U-HDR-03** `expire=0` parses to `Expire: 0` and the consumer treats it as no-expiry per FR-010.
- **TC-U-HDR-04** Unparseable string → returns `nil, err`; the fetcher logs the failure; the source contributes no traffic numbers (FR-012).
- **TC-U-HDR-05** `Profile-Update-Interval: 12` → `12` (hours).
- **TC-U-HDR-06** Missing `Profile-Update-Interval` → returns `0, false`; aggregator uses configured fallback.

#### Cache (`internal/fetcher/cache.go`)

- **TC-U-CACHE-01** Within TTL → `Get` returns cached value, no fetch invoked (verified by counting calls to a stub `Fetcher`).
- **TC-U-CACHE-02** Past TTL but within stale-on-error window AND last refresh attempt failed → `Get` returns stale cache + `Stale: true`.
- **TC-U-CACHE-03** Past TTL + past stale window → `Get` returns `nil`; bootstrap state machine treats this as fail-closed.
- **TC-U-CACHE-04** Single-flight: 100 concurrent `Get` calls during a refresh-in-progress trigger exactly **one** upstream fetch (verified by atomic counter on the stub `Fetcher`; uses `golang.org/x/sync/singleflight`).

#### Merge primitives (`internal/merge/`)

- **TC-U-MERGE-PROXY-01** Two upstreams contribute identically-named proxies → output contains both with deterministic suffix `<name>@<source-name>`; collision recorded in returned `[]ProxyCollision`.
- **TC-U-MERGE-PROXY-02** Own-proxy name equals an upstream proxy name → upstream entry is suffixed; own-proxy keeps its name (FR-008).
- **TC-U-MERGE-GROUP-01** Two upstreams contribute proxy-group `Auto` (`type: select` and `type: select`) with member sets `[A, B]` and `[B, C]` → output has one `Auto` group with members `[A, B, C]` (deduplicated, order from highest-priority source first then appended).
- **TC-U-MERGE-GROUP-02** Type conflict (`select` vs `url-test`) → highest-priority source wins; conflict appears in `[]GroupConflict` returned by the merge function.
- **TC-U-MERGE-GROUP-03** Always-present `Proxies` select group is appended even when no upstream defined any group; its members are the union of all proxies after suffixing (FR-009a).
- **TC-U-MERGE-RULES-01** Source A priority 2000, source B priority 1000 → A's rules emit first, B's rules second; intra-source order preserved.
- **TC-U-MERGE-RULES-02** Two sources with equal priority → row order in CSV breaks the tie deterministically.
- **TC-U-TRAFFIC-01** Aggregation: source A `(upload=10737418240, download=42949672960, total=214748364800, expire=now+30d)`, source B `(upload=5368709120, download=16106127360, total=107374182400, expire=now+5d)` → aggregated `Subscription-Userinfo` has `upload=16106127360; download=59055800320; total=322122547200; expire=<now+5d>`.
- **TC-U-TRAFFIC-02** Daily-allowance per-source weighted: same inputs as above (using `clock.FakeClock`) → per-day rate ≈ `5GB/day + 16GB/day = 21GB/day` (within rounding); no-expiry-remaining = 0; expired-source-flags empty.
- **TC-U-TRAFFIC-03** Source has `Expire=0` → contributes its remaining bytes to no-expiry-remaining; per-day rate excludes it; aggregated `expire` from other sources unchanged.
- **TC-U-TRAFFIC-04** Source has `Expire < now` → 0 contribution to per-day rate; expired-source-flags includes the source name.
- **TC-U-TRAFFIC-05** Profile-Update-Interval aggregation: A=12h, B=24h → output 12h (minimum non-zero).
- **TC-U-TRAFFIC-06** All sources omit interval header → output uses configured default (e.g., 12h).

### TC-I: Integration tests (`internal/integration/pipeline_test.go`)

Fixtures under `internal/integration/testdata/fixtures/`. Upstream HTTP is mocked via `httptest.NewServer` in each test; the fetcher's HTTP client is pointed at the test server's URL. The `clock.FakeClock` is used throughout so TTL/stale/daily-allowance behavior is deterministic.

- **TC-I-01 — Happy path, both upstreams reachable**: GET `/?token=<valid>` → 200, `Content-Type: application/yaml; charset=utf-8`, body parses as Clash config (round-trip with `yaml.v3`), `proxies` contains the union (every proxy from alpha.yaml + every proxy from beta.yaml + the 2 own-proxies), `proxy-groups` contains all upstream groups + own group + the always-present `Proxies` group, `rules` block emits beta rules first (priority 2000) then alpha rules (priority 1000), response `Subscription-Userinfo` header carries the aggregated values, `Profile-Update-Interval` carries the minimum.
- **TC-I-02 — Alpha unreachable, cache fresh**: switch the alpha stub to 503 mid-test (after warm cache); next request still includes alpha's proxies; `/health` shows alpha as `degraded` and `serving_from_cache: true`.
- **TC-I-03 — Alpha unreachable, cache stale beyond stale-on-error**: stub alpha to 503 + advance fake clock past stale window; merged output excludes alpha's proxies; `/health` shows alpha as `failed_no_cache`. Server still serves successfully if `beta` is healthy.
- **TC-I-04 — Both upstreams unreachable, no cache (cold start)**: `/` returns 503; `/health` reports `warming_up` then `bootstrap_failed`. (FR-003b)
- **TC-I-05 — `enable=Disable` row**: edit the CSV fixture so alpha has `enable=Disable` → trigger reload via fsnotify → alpha is loaded, validated, but never fetched and not present in output; startup log enumerates disabled sources. (SC-013)
- **TC-I-06 — Proxy name collision**: inject a synthetic proxy named identically across upstream stubs → output contains both with `@alpha` and `@beta` suffixes; log captures the collision.
- **TC-I-07 — Proxy-group same-name union**: inject a synthetic same-named group across upstream stubs → single output group with deduplicated member union. (SC-014)
- **TC-I-08 — Token authentication**:
  - `/` (no token) → 401, no body, no `Subscription-Userinfo` header in response.
  - `/?token=<unknown>` → 401, log carries `sha256:<12-hex>` not the token plaintext.
  - `/?token=<revoked>` → 401.
  - `/?token=<valid>` → 200.
- **TC-I-09 — Determinism**: 100 sequential `/?token=<valid>` requests → byte-identical response bodies and headers (verified by `sha256` over each body). (SC-002)
- **TC-I-10 — Cache absorbs traffic**: 100 concurrent client requests via `errgroup` → upstream-fetch counter (atomic int on the stub) increments by 0; only the background scheduler's per-source `time.Ticker` triggers fetches. (SC-012, FR-003a)
- **TC-I-11 — Background fetch schedule**: advance fake clock by `ttl_seconds`; verify exactly one fetch per source occurs at the next tick.
- **TC-I-12 — Single-flight**: 100 concurrent fetches arriving exactly when the cache expires → exactly one upstream HTTP call per source (verified via the atomic counter on the stub).
- **TC-I-13 — Daily allowance per-source weighted**: synthetic Subscription-Userinfo headers — A: `total=200GB / used=50GB / expire=now+30d`; B: `total=100GB / used=20GB / expire=now+5d` → `/health` JSON `dailyAllowance.perDayRateBytes` ≈ 21GB. (SC-015)
- **TC-I-14 — No-expiry remaining**: both upstreams report `expire=0` → `/health` reports `dailyAllowance.perDayRateBytes: 0` and `dailyAllowance.noExpiryRemainingBytes: <sum>`; served `Subscription-Userinfo` carries `expire=0`.
- **TC-I-15 — Expired-source flag**: source A has `expire = now - 1d` → A flagged on health surface (`dailyAllowance.expiredSourceFlags: ["alpha"]`), contributes 0 to daily allowance, served `expire` reflects next-soonest non-zero.
- **TC-I-16 — Own-proxy + own-proxy-group**: own-proxies fixture contributes 2 proxies and 1 group; both appear in served output; own-proxies are members of the always-present `Proxies` group.
- **TC-I-17 — Config reload error doesn't crash server**: corrupt the CSV mid-run → fsnotify event → reload fails → previous valid config still serving traffic; failure logged. (FR-017)
- **TC-I-18 — Sanitized output**: parse the served body with `yaml.v3` and `strings.Contains` for any of: an upstream URL (path token), upstream-side `Subscription-Userinfo` echo, raw token from `tokens.json` → 0 matches in all cases. (SC-007)

### TC-S: Snapshot tests (`internal/integration/snapshot_test.go`)

Snapshots written/read by `cupaloy/v2`. Update with `UPDATE_SNAPSHOTS=true go test ./internal/integration/...`. Inputs frozen across all snapshot tests: the two committed upstream fixtures, the own-proxies fixture, the served-config template, and a fixed `clock.FakeClock` time `2026-04-30T00:00:00Z`.

- **TC-S-01 — `served-config.snap.yaml`** (full body): committed at `internal/integration/testdata/snapshots/`. Drift fails the test; updates require explicit env var plus PR reviewer sign-off (per Constitution Principle II + Development Workflow snapshot-stability gate).
- **TC-S-02 — `subscription-userinfo.snap.txt`** (string): committed snapshot carries the exact wire-format string the server emits for the fixture inputs.
- **TC-S-03 — `health.snap.json`**: committed snapshot under fixed inputs + fixed clock; covers per-upstream state, daily-allowance triple (per-day rate, no-expiry remaining, expired flags), and bootstrap status.

### Acceptance criteria for "tests pass" gate

A `/speckit-implement` run on this plan is **NOT** considered done unless:

- Every TC-U / TC-I / TC-S above has a committed test in the listed `*_test.go` file (Go test function names matching the TC ids for grep-ability — e.g., `func TestCSV_01_LoadsExampleFile(t *testing.T)`).
- `go test ./...` exits 0 from a clean checkout against the committed fixtures.
- Snapshot files are committed (no `t.Skip`, no `t.Skipf`).
- The snapshot-drift CI gate is wired up (a `make check` target that runs `go test ./...` AND `git diff --exit-code` so any inadvertent snapshot change fails CI) and demonstrably fails when a developer mutates `internal/merge/` without updating snapshots.
- `go vet ./...` and `staticcheck ./...` pass without warnings.
