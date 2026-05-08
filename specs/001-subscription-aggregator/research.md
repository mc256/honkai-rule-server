# Research: Subscription Aggregator (Phase 0, rev 2 — Go)

**Feature**: `001-subscription-aggregator` | **Date**: 2026-04-30

This document records the technology, format, and operational-pattern choices made during Phase 0. The spec resolved every `NEEDS CLARIFICATION` marker before this phase started, so research focuses on best-practice selection for already-decided requirements rather than open questions. **Rev 2 (2026-04-30)**: language switched from TypeScript/Node to Go at user request; all R-numbered decisions below are the Go-flavored set. The TS-flavored rev 1 of this document is preserved in git history (commit `7d9febf`).

---

## R1. Language and runtime

- **Decision**: Go 1.23.
- **Rationale**: (a) Long-running HTTP server with many concurrent goroutines (per-source background fetchers, single-flight coordinators, request handlers) is exactly what Go is best at. (b) Single static binary (`CGO_ENABLED=0`) deploys as ~15MB `FROM scratch` image — smaller and faster cold-start than a Node container. (c) `log/slog` (1.21+) gives us structured JSON logging without a third-party logger. (d) `net/http` pattern routing (1.22+) handles our two endpoints without a router dependency. (e) `internal/` package convention enforces the merge-layer / fetcher-layer separation at compile time — the merge layer literally cannot import from outside the module.
- **Alternatives considered**:
  - **TypeScript / Node 22** (rev 1 choice): the `example/proxy-subscription/` sandbox uses Node, so reusing the runtime had some appeal. Rejected on rev 2: the sandbox is a reference, not a dependency we import; Go's deployment story and concurrency primitives are a better fit for a long-running server than Node's single-event-loop model + pm2 / clustering.
  - **Python**: fine fit, but requires a WSGI/ASGI story, slower cold-start, and YAML round-trips lose key order without extra care.
  - **Rust**: overkill at this scale; loses the rapid-iteration ergonomics for ~zero observable benefit on a service that mostly does I/O and string manipulation.

---

## R2. HTTP framework

- **Decision**: Standard library `net/http` with Go 1.22's pattern routing (`mux.HandleFunc("GET /", handler)`); no third-party framework.
- **Rationale**: Two endpoints (`GET /` and `GET /health`). Pattern routing handles method + path matching natively. Middleware (token auth, request-logging) is a thin `func(http.Handler) http.Handler` chain — readable in 30 lines. Adding `chi` / `gin` / `echo` for two endpoints is overkill and opaque. Stdlib also gives us `http.Server.Shutdown(ctx)` for graceful shutdown out of the box.
- **Alternatives considered**:
  - **`chi`**: idiomatic, lightweight, popular. Would be the right choice if we had ~10+ endpoints with nested route groups. Rejected at this size.
  - **`gin` / `echo` / `fiber`**: all heavier than needed; `fiber` is on `fasthttp` which doesn't share `net/http`'s `Request`/`Response` types and complicates testing with `httptest`. Rejected.

---

## R3. Logger

- **Decision**: `log/slog` (stdlib) with the JSON handler (`slog.NewJSONHandler`).
- **Rationale**: Structured logs are a first-party concern in modern Go. `slog.With(...)` lets us attach per-source context (e.g., `slog.With("source", "alpha")`) once and have every subsequent log line carry it — equivalent to Pino's child loggers. Set as the default via `slog.SetDefault` so package-level `slog.Info(...)` calls work everywhere. `LOG_LEVEL` env-var maps directly to `slog.LevelVar`.
- **Alternatives considered**:
  - **`zap` (Uber)**: faster than `slog` (~2-3×) but the stdlib version is fast enough at our request rate (~ <100 req/s typical). Adding a dep for a perf advantage we won't notice is not worth it.
  - **`zerolog`**: similar tradeoff to zap.
  - **`logrus`**: legacy; `slog` has eaten its lunch.

---

## R4. YAML library

- **Decision**: `gopkg.in/yaml.v3` (Canonical's library, the de-facto Go YAML standard).
- **Rationale**: (a) Supports `yaml.Node` for order-preserving parse/emit cycles — essential for byte-identical output (Constitution Principle II); without this, Go's `map[string]interface{}` decoding loses key order on emission. (b) Handles both block and flow YAML styles found in real upstream payloads (compare `alpha.yaml` block-style vs `beta.yaml` flow-style; the merge layer needs to round-trip both faithfully). (c) UTF-8 / emoji handling is correct (e.g., `🔰国外流量` in alpha, `蓝莓桥` in beta). (d) Mature, no recent breaking changes.
- **Strategy**: Parse upstream payloads into `*yaml.Node` (not strongly-typed structs) so unknown fields pass through unmodified. Use `node.Decode(&typed)` only for the specific fields the merge layer needs (proxy/group `name`, group `type`, group `proxies` member list). Re-emit by walking the `*yaml.Node` tree and substituting only the changed nodes. This keeps the merge layer agnostic to the long-tail of provider-specific proxy fields (`reality-opts`, `ws-opts`, `flow`, `client-fingerprint`, etc.).
- **Alternatives considered**:
  - **`go-yaml/yaml.v2`**: older, no `yaml.Node` API, loses key order. Rejected.
  - **`goccy/go-yaml`**: faster, more features, but a smaller community and historic API churn. Stability wins.
  - **`sigs.k8s.io/yaml`** (JSON-bridge): this is YAML-via-JSON for Kubernetes object marshaling — wrong tool for our use case.

---

## R5. CSV parser

- **Decision**: `encoding/csv` (stdlib).
- **Rationale**: Subscriptions CSV is small (≤10 rows typical, hundreds even in pathological cases). Stdlib `csv.Reader` handles header-by-name reading via a small wrapper, quoted fields with embedded commas, and RFC-4180 escaping. We hand-write the schema validator (no struct-tag-based system in stdlib) but the validation surface is finite and the extra ~80 LOC is worth zero runtime deps.
- **Alternatives considered**:
  - **`gocarina/gocsv`**: struct-tag-based; nicer DX. Rejected for one new dep when we'd write ~80 LOC of validation either way (the schema is custom enough — `Enable`/`Disable` casing rule, `priority` integer, unknown-column rejection — that struct tags don't free us).

---

## R6. HTTP client for upstream fetches

- **Decision**: Stdlib `net/http.Client` with a configured `Transport` (per-source connection pool via separate `Client` instances if needed — likely overkill; one shared client is fine), and `context.WithTimeout` per fetch.
- **Rationale**: Stdlib handles HTTPS, redirects, header access, and timeouts. We don't need anything fancy. Each fetch goroutine builds a `*http.Request` with `req.WithContext(ctxWithTimeout)`, calls `client.Do(req)`, reads the body, and parses headers. The cancellation story is exactly `context.WithTimeout` + `defer cancel()`.
- **Alternatives considered**:
  - **`resty`**: extra dep, ergonomic sugar we don't need.
  - **`fasthttp`**: doesn't share stdlib types; complicates testing with `httptest`.

---

## R7. Test framework + snapshot strategy

- **Decision**: Stdlib `testing` package for everything; `github.com/bradleyjkemp/cupaloy/v2` for file-backed snapshots committed under `internal/integration/testdata/snapshots/`.
- **Rationale**: (a) Stdlib `testing` is the Go standard — `t.Run` for subtests, `-race` for race detection, `-cover` for coverage. (b) `cupaloy/v2` is mature, has a `-update` env-var (`UPDATE_SNAPSHOTS=true`) matching Vitest's UX, supports diff output on failure, and writes snapshots to predictable paths under `.snapshots/` so they're reviewable in PR diffs. (c) Snapshot updates require an explicit env var, so accidental CI-time regenerates are impossible.
- **Alternatives considered**:
  - **`testify`**: nicer assertion DX (`assert.Equal`) but we don't strictly need it; `t.Errorf` + a small `diff` helper covers the same ground without a dep. Lean toward stdlib.
  - **Hand-rolled snapshots**: compare-to-file with an `-update` flag is ~30 LOC; acceptable but `cupaloy` is well-tested across the patterns we need (multiline diffs, nested directories, scrubbers for non-deterministic fields).
  - **`gotest.tools/v3/golden`**: similar to cupaloy; cupaloy edges out on `-update` flag UX and active maintenance.

---

## R8. Cache shape

- **Decision**: In-memory `map[string]*CachedPayload` guarded by `sync.RWMutex` for the hot path; optional disk-persistence layer that writes JSON snapshots to `${CACHE_DIR}/${name}.json` after every successful fetch and reads them on cold start.
- **Rationale**: (a) Two upstreams × ~80KB each fits trivially in process memory; (b) disk-persistence avoids re-hammering upstreams after a routine pod restart in K8s — without it, every restart triggers a full bootstrap fetch storm; (c) JSON (not YAML) on disk for the cache because we want fast round-trips, not human review; (d) `sync.RWMutex` lets reader requests run concurrently while a single writer-goroutine (the per-source fetcher) updates the cache slot.
- **Alternatives considered**:
  - **`sync.Map`**: hides the locking but its zero-value behavior and lack of explicit `Range` ordering trip people up. The explicit `RWMutex` over a typed map is more idiomatic at this scale.
  - **Redis / external KV**: overkill for a single-pod service. Adds another failure mode for no benefit.

---

## R9. Background scheduler

- **Decision**: One goroutine per source, each holding a `time.Ticker` whose period equals that source's `ttl_seconds`. A single `Coordinator` struct owns the bootstrap state machine; the HTTP server's `Listen` is gated on `Coordinator.Ready()` (a channel-close-when-ready pattern).
- **Rationale**: Maps directly to Go's primitives; nothing fancy. `time.Ticker` is `defer ticker.Stop()`'d on shutdown via `context.Context` cancellation. The Coordinator goroutine receives bootstrap-result events on a channel, transitions the per-source state machine, and closes the `Ready` channel once every enabled source is `Succeeded`.
- **Alternatives considered**:
  - **`robfig/cron`**: brings cron expression parsing we don't need; TTL is a number-of-seconds, not a calendar schedule.
  - **`gocraft/work` / `hibiken/asynq`**: job-queue libraries; we have a fixed-cardinality set of recurring tasks, not a queue.

---

## R10. Token store

- **Decision**: File-backed JSON token store at `${TOKENS_PATH}` (env var). One JSON object: `{"tokens": [{"token": "...", "label": "...", "revoked": false, "issued_at": "..."}, ...]}`. Hot-reloaded via `fsnotify` with a 250ms debounce; file is mounted as a Kubernetes Secret in production. Read path uses `RWMutex` so `Lookup` doesn't block during a reload.
- **Rationale**: (a) For ≤20 tokens (target scale), a JSON file is the smallest correct thing; (b) Kubernetes Secret rotation just updates the file, the watcher picks it up; (c) revocation is a flag flip, no DB needed; (d) lookup is a `for _, rec := range tokens` scan, which is fine at this scale and trivially upgradeable to a `map[string]*TokenRecord` later. (e) `fsnotify` is the de-facto Go file-watcher and handles K8s Secret atomic-replace semantics correctly (the volume's `..data` symlink swap fires a `Create` event we listen for).
- **Alternatives considered**:
  - **SQLite** via `modernc.org/sqlite` (pure Go): overkill for a list this small.
  - **In-memory only**: rejected — restart would invalidate every token.
  - **External auth service** (OAuth, JWT, etc.): overengineered for a personal/team tool.

---

## R11. Subscriptions CSV column-name reconciliation

- **Decision**: Spec column names updated 2026-04-30 to match the user-provided `example/subscriptions.csv` exactly: `name`, `link`, `priority`, `enable` (the spec previously read `url`, `rule_priority`, `enabled` boolean). The `enable` column accepts case-insensitive `Enable` / `Disable` (the user's example uses `Enable`); compared via `strings.EqualFold`.
- **Rationale**: The user's example file is the source of truth for what they actually want to feed the server. Diverging would create migration friction without benefit. Spec FR-001a / FR-001b / FR-016 and the Upstream Subscription entity definition were all updated to match.
- **Alternatives considered**:
  - **Make the parser accept both old and new names**: rejected — Constitution Principle III demands strict schema with loud failure; aliases dilute that.

---

## R12. Proxy-group same-name merge strategy

- **Decision**: Union member lists with deduplication; group `type` and other group attributes (`url`, `interval`, `tolerance`, `lazy`, etc.) come from the highest-priority source that defined the group; conflicts on attributes are recorded in a `[]GroupConflict` returned by the merge function and logged by the caller.
- **Rationale**: This matches FR-008a precisely. Customizable / geo-aware merge is deferred to a follow-up feature per the spec and per the user's clarification on 2026-04-30 ("in the next release, we will need to have customizable rules for merging proxy group, for example, merge by geo location"). The simple name-union meets the MVP UX promise of "one URL replaces N URLs in the client".
- **Note on real fixtures**: The two committed fixtures (`alpha.yaml` and `beta.yaml`) do NOT have any proxy-group name collisions in the wild — alpha uses emoji-prefixed Chinese names like `🔰国外流量` while beta uses plain Chinese names like `蓝莓桥`. The integration test injects a synthetic same-named group across both stubs to exercise the merge logic, since real-world same-name collisions are infrequent but the behavior must still be correct.

---

## R13. Always-present `Proxies` select group

- **Decision**: After upstream and own proxy-groups are merged, the pipeline appends one `select`-type group named `Proxies` (configurable name in `ServerConfig.ProxiesGroupName`, default `Proxies`) whose members are the union of all proxies in the merged output (after suffixing).
- **Rationale**: Without this, a Clash client's "Proxies" UI panel can be empty if no upstream contributed a `select` group containing every proxy. The user's prompt was "make own-proxies selectable" — the simplest way to honor that promise is to always provide one ground-truth select group containing everything, regardless of what upstream groups exist.

---

## R14. Aggregated `Subscription-Userinfo` `expire` rule + per-source-weighted daily allowance

- **Decision**: Aggregated `expire` is the **earliest non-zero** `expire` across sources (when all sources are zero, emit `expire=0`). The daily allowance is the **per-source weighted sum** `Σ_i remaining_i / days_until_expire_i` over sources with `expire_i > now`, plus a separately-reported `NoExpiryRemainingBytes` for sources with `expire_i == 0`, plus an `ExpiredSourceFlags []string` for sources whose `expire > 0` is in the past.
- **Rationale**: Confirmed by the user on 2026-04-30 (FR-011b rewrite). The earliest-non-zero `expire` is what stock Clash clients use for the "expires in X days" badge in their UI; the per-source-weighted daily allowance is meaningful in the (real) case where providers have very different expiry dates and quotas. A single `total / earliest_expire` would give wildly misleading numbers.
- **Implementation note**: The pipeline accepts a `clock.Clock` interface (with `Now() time.Time`); production wiring uses `clock.RealClock{}`, tests use `clock.NewFakeClock(time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC))`. This satisfies Principle II and lets us write deterministic snapshot tests for `/health` and the `Subscription-Userinfo` header.

---

## R15. Served-config template variable substitution

- **Decision**: Three explicit text placeholders in the template (`__MERGED_PROXIES__`, `__MERGED_PROXY_GROUPS__`, `__MERGED_RULES__`) replaced via straightforward string substitution after rendering each merged block to YAML. The template itself is parsed and re-emitted by `gopkg.in/yaml.v3` to ensure consistent quoting/escaping; the placeholders sit on their own lines as the value of their parent key.
- **Rationale**: We considered `text/template` and `html/template`, but for three substitutions in a fixed-shape document, an engine is overkill and adds escaping concerns. The string-replace approach is auditable in two function calls; tests confirm the resulting YAML parses cleanly and round-trips through `yaml.Unmarshal`.
- **Alternatives considered**:
  - **Build the entire served config programmatically (no template)**: hides the operator-tunable parts (`mode`, `dns`, ports) inside code instead of a reviewable YAML file. Rejected for operator UX.
  - **`text/template`**: extra concept (template syntax in YAML) for no benefit at three substitutions.

---

## R16. Kubernetes deployment shape

- **Decision**: Single Deployment, single ClusterIP Service, single Ingress (handles TLS termination per FR-019c). Subscriptions CSV, own-proxies YAML, and tokens JSON live in one Kubernetes Secret mounted at well-known paths inside the container; the served-config template lives in a ConfigMap. Env vars: `SUBSCRIPTIONS_CSV_PATH`, `OWN_PROXIES_YAML_PATH`, `TOKENS_PATH`, `SERVED_CONFIG_TEMPLATE_PATH`, `CACHE_DIR`, `LOG_LEVEL`, `PORT`.
- **Rationale**: Smallest deployable unit that satisfies all constraints. Hot reload of the Secret triggers config reload via `fsnotify`. No Helm chart for v1 — raw manifests are more diffable for a small surface. Container image: multi-stage Dockerfile, `golang:1.23-alpine` build stage produces a static binary (`CGO_ENABLED=0 go build`), final stage is `FROM scratch` with the binary + CA certs (~15MB image).
- **Alternatives considered**:
  - **Helm chart**: rejected for v1 — six manifest files, each ~30 lines, are easier to read directly than a templated chart.
  - **Distroless** (`gcr.io/distroless/static-debian12`): also fine; `scratch` is smaller and we don't need anything in `/etc/`. Distroless adds a CA bundle + tzdata; we add the CA bundle ourselves with a `COPY --from=alpine` step.

---

## R17. Time injection (Clock interface)

- **Decision**: An `internal/clock.Clock` interface with `Now() time.Time` method. Two implementations: `RealClock{}` (production) and `FakeClock` (tests; supports `Advance(d time.Duration)`). The merge layer, fetcher cache, and bootstrap state machine all take a `Clock` in their constructors.
- **Rationale**: Principle II demands deterministic transformation; FR-011b's daily-allowance figure depends on `now`. Tests must be able to set `now` to a fixed value to get reproducible snapshot output. The `Clock` interface is the canonical Go idiom for this; rolling our own keeps us free of `clockwork` / `benbjohnson/clock` deps.
- **Alternatives considered**:
  - **`benbjohnson/clock`**: well-known library, but ~50 LOC of our own does the job and keeps deps minimal.
  - **Globals (`var Now = time.Now`)**: works but encourages misuse; the explicit-injection pattern keeps the seams clean.

---

## R18. Build/lint/CI tooling

- **Decision**: `go build` for the binary; `go test ./...` for tests; `go vet ./...` and `staticcheck ./...` for linting; `gofmt -l .` for formatting drift. A simple `Makefile` wraps these (`make build`, `make test`, `make snapshot-update`, `make lint`, `make docker`). CI runs `make check` which is `vet + staticcheck + test + git diff --exit-code` (the last catches accidentally-modified snapshots).
- **Rationale**: The Go ecosystem's defaults are excellent. `staticcheck` catches everything `golangci-lint` would catch in our small codebase, with less config. Adding `golangci-lint` is reasonable but `staticcheck` alone is enough at this size.
- **Alternatives considered**:
  - **`golangci-lint`**: aggregator over many linters; nice for large codebases. Slight overkill here.

---

## Summary of choices (one-line)

| # | Topic | Choice |
|---|---|---|
| R1 | Language | Go 1.23 |
| R2 | HTTP framework | stdlib `net/http` (1.22 pattern routing) |
| R3 | Logger | stdlib `log/slog` (JSON handler) |
| R4 | YAML library | `gopkg.in/yaml.v3` (using `yaml.Node` for order preservation) |
| R5 | CSV parser | stdlib `encoding/csv` |
| R6 | HTTP client | stdlib `net/http.Client` + `context.WithTimeout` |
| R7 | Test framework | stdlib `testing` + `cupaloy/v2` for snapshots |
| R8 | Cache | `map[string]*CachedPayload` + `sync.RWMutex` + disk JSON persistence |
| R9 | Scheduler | per-source goroutine + `time.Ticker` + bootstrap Coordinator |
| R10 | Token store | File-backed JSON, hot-reloaded via `fsnotify`, K8s Secret |
| R11 | CSV column names | `name,link,priority,enable` (matches `example/subscriptions.csv`) |
| R12 | Proxy-group merge | Name-union, attributes from highest priority, conflicts in `[]GroupConflict` |
| R13 | `Proxies` group | Always present in output |
| R14 | Daily allowance | Per-source weighted sum + separate no-expiry remaining + expired flags |
| R15 | Template substitution | Three text placeholders, string-replace + reparse |
| R16 | K8s deployment | Single Deployment / Service / Ingress; Secrets + ConfigMap; raw manifests |
| R17 | Time injection | `internal/clock.Clock` interface, RealClock + FakeClock |
| R18 | Build/CI tooling | stdlib `go` toolchain + `staticcheck` + Makefile |

All `NEEDS CLARIFICATION` markers from the spec are already resolved (see `checklists/requirements.md` resolution log). No open research items remain blocking Phase 1.

### Module path

Module path placeholder: `github.com/<owner>/honkai-rule-server`. After creating the GitHub repository, run `go mod edit -module github.com/<your-org>/honkai-rule-server` and re-run `go mod tidy` to update import paths in any code that references the module path explicitly.

### Dependency manifest preview (`go.mod`)

```text
module github.com/<owner>/honkai-rule-server

go 1.23

require (
    github.com/bradleyjkemp/cupaloy/v2 v2.8.0
    github.com/fsnotify/fsnotify v1.7.0
    golang.org/x/sync v0.10.0
    gopkg.in/yaml.v3 v3.0.1
)
```

Four direct dependencies. Everything else (HTTP, CSV, JSON, logging, testing, file I/O, time) is stdlib.
