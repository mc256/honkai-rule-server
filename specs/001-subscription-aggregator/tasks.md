# Tasks: Subscription Aggregator (Multi-Source Merge + Own Proxies + Traffic Accounting)

**Input**: Design documents from `/specs/001-subscription-aggregator/`
**Prerequisites**: plan.md ✅, spec.md ✅, research.md ✅, data-model.md ✅, contracts/ ✅, quickstart.md ✅

**Tests**: REQUIRED (Constitution Principle IV — Test-First, Real-Input Integration is NON-NEGOTIABLE). Tests are written **before** the matching implementation; CI snapshot-drift gate fails the build on any deterministic-output regression.

**Organization**: Tasks are grouped by user story (US1–US4 from spec.md). Each story is independently testable and demoable; the dependency-driven order below is US1 → US2 → US3 → US4 (US4's headers consume US3's data, so its phase trails US3 even though both carry priority P2 — see Dependencies section).

**Tech stack** (from plan.md / research.md): Go 1.23, stdlib `net/http` (1.22 pattern routing), `log/slog`, `gopkg.in/yaml.v3` (using `yaml.Node`), `encoding/csv`, `cupaloy/v2` (snapshots), `fsnotify`, `golang.org/x/sync/singleflight`. Module path placeholder: `github.com/<owner>/honkai-rule-server`.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: Which user story this task belongs to (US1 / US2 / US3 / US4); foundational and polish tasks have no story label
- Every task includes the exact file path it touches

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Project scaffolding so subsequent phases can compile and test.

- [X] T001 Initialize Go module at repo root: `go mod init github.com/<owner>/honkai-rule-server` (use the placeholder; document `go mod edit -module` swap in README per quickstart §8)
- [X] T002 Create the directory tree per plan.md: `cmd/server/`, `internal/{clock,config,fetcher,merge,output,observability,server/routes}/`, `internal/integration/testdata/{fixtures/upstream,snapshots}/`, `templates/`, `config/`, `deploy/k8s/`
- [X] T003 [P] Create root `.gitignore` ignoring `bin/`, `.cache/`, `*.exe`, `*.test`, `coverage.out`, `vendor/`
- [X] T004 [P] Create `config/.gitignore` ignoring `subscriptions.csv`, `own-proxies.yaml`, `tokens.json` (these are operator secrets per FR-016)
- [X] T005 [P] Create `Makefile` at repo root with targets: `build` (`go build -o bin/server ./cmd/server`), `test` (`go test -race ./...`), `snapshot-update` (`UPDATE_SNAPSHOTS=true go test ./internal/integration/...`), `lint` (`go vet ./... && staticcheck ./...`), `check` (`vet + staticcheck + test + git diff --exit-code`), `docker` (image build)
- [X] T006 [P] Create multi-stage `Dockerfile`: `golang:1.23-alpine` build stage producing a `CGO_ENABLED=0 -ldflags="-s -w"` static binary; `FROM scratch` final stage copying the binary plus `/etc/ssl/certs/ca-certificates.crt` (from alpine); `EXPOSE 8080`; `ENTRYPOINT ["/server"]`
- [X] T007 [P] Create root `README.md` with one-paragraph project description and links to `specs/001-subscription-aggregator/{spec,plan,quickstart}.md`
- [X] T008 Add direct dependencies via `go get`: `gopkg.in/yaml.v3`, `github.com/fsnotify/fsnotify`, `github.com/bradleyjkemp/cupaloy/v2`, `golang.org/x/sync`. Run `go mod tidy`; commit `go.sum`

**Checkpoint**: Repo compiles to an empty binary; tests run (vacuously); CI gate scaffolding is in place.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Cross-cutting utilities and test fixtures every user story depends on.

**⚠️ CRITICAL**: No user-story work can begin until this phase is complete.

### Test fixtures (copy real data from `example/` so all stories use the same baseline)

- [X] T009 [P] Move the served-config template draft from `specs/001-subscription-aggregator/templates/served-config.template.yaml` to repo `templates/served-config.template.yaml` (the production location per plan.md). Update spec dir to remove the duplicate; update the spec/plan/quickstart cross-references to the new path
- [X] T010 [P] Copy `example/3rd-party-sub/alpha.yaml` → `internal/integration/testdata/fixtures/upstream/alpha.yaml` verbatim
- [X] T011 [P] Copy `example/3rd-party-sub/beta.yaml` → `internal/integration/testdata/fixtures/upstream/beta.yaml` verbatim
- [X] T012 [P] Create `internal/integration/testdata/fixtures/subscriptions.csv` matching the schema of `example/subscriptions.csv` (columns: `name,link,priority,enable`) with two rows pointing at the test HTTP server URLs (URLs are placeholders — the integration-test harness will substitute them per-test) and synthetic tokens
- [X] T013 [P] Create `internal/integration/testdata/fixtures/own-proxies.yaml` with two synthetic proxies (one `trojan`, one `vless`) and one `select`-type proxy-group `My-Own` referencing them
- [X] T014 [P] Create `internal/integration/testdata/fixtures/tokens.json` with one valid token, one revoked token, and one ExpiresAt-in-the-future token

### Cross-cutting Go packages

- [X] T015 [P] `internal/clock/clock.go` — `Clock` interface with `Now() time.Time`; `RealClock` (calls `time.Now()`); `FakeClock` struct with `sync.RWMutex`-guarded `now` field, `NewFakeClock(t)`, `Now()`, `Advance(d)`. Per data-model.md §Clock and research R17
- [X] T016 [P] `internal/observability/logger.go` — `New(level slog.Level) *slog.Logger` factory returning a JSON-handler logger writing to `os.Stdout`; `SanitizeToken(token string) string` returning `"sha256:" + hex.EncodeToString(sha256.Sum256([]byte(token))[:6])` (12 hex chars). Per FR-013, FR-014, SC-011
- [X] T017 [P] `internal/observability/logger_test.go` — TC-U-TOK-05: `SanitizeToken("anything")` returns a 19-char string starting with `"sha256:"`; same input yields same output; different inputs yield different outputs

### Header parsers (foundational because the fetcher captures headers regardless of which story consumes them)

- [X] T018 [P] `internal/fetcher/headers.go` — `ParseSubscriptionUserinfo(s string) (*SubscriptionUserinfo, []string, error)` returning the parsed struct, a slice of missing field names, and a parse error if the format is malformed beyond recovery; `ParseProfileUpdateInterval(s string) (int, bool)` returning hours + ok. Wire format per FR-005b
- [X] T019 [P] `internal/fetcher/headers_test.go` — TC-U-HDR-01..06: real alpha-example header value; missing-field flag; `expire=0` no-expiry semantics; unparseable; integer hours parse; missing interval

### Server config (env-bound; no file watch)

- [X] T020 `internal/config/server.go` — `ServerConfig` struct per data-model.md §ServerConfig; `Load(env Env) (*ServerConfig, error)` with explicit `Env` interface (`Getenv(key string) string`) so tests inject fakes; sane defaults for the optional vars
- [X] T021 `internal/config/server_test.go` — happy-path env, missing required env (returns wrapped error), invalid integer env, default substitution

### Integration test harness scaffold

- [X] T022 `internal/integration/testmain_test.go` — `TestMain` hook that creates the cupaloy snapshot dir, configures a per-package logger to a buffer for assertion, and exposes a `setupTestServer(t, opts)` helper used by every story's integration tests (returns `*httptest.Server` + a stub `UpstreamRegistry` letting tests respond to specific upstream URLs)

**Checkpoint**: Foundation ready. Every package above passes its own unit tests; user-story implementation can begin.

---

## Phase 3: User Story 1 — Aggregate Multiple Upstream Subscriptions (Priority: P1) 🎯 MVP

**Goal**: A single token-authenticated `GET /` endpoint serves a merged Mihomo/Clash subscription whose proxies, proxy-groups, and rules are the union (or priority-ordered concatenation) of every enabled upstream in the subscriptions CSV. Cold-start fail-closed; cache absorbs all client traffic; deterministic output across runs.

**Independent Test**: With both `example/3rd-party-sub/alpha.yaml` and `example/3rd-party-sub/beta.yaml` served by an `httptest.Server`, a `GET /?token=<valid>` returns 200 with a Clash YAML body whose proxies are the union of both upstreams', whose rules emit beta (priority 2000) before alpha (priority 1000), and whose response is byte-identical across 100 sequential requests.

### Tests for User Story 1 (write FIRST, see them FAIL, then implement)

#### Unit tests (all [P] — different files)

- [X] T023 [P] [US1] `internal/config/subscriptions_test.go` — TC-U-CSV-01..10: loads `example/subscriptions.csv` (two rows, integer priorities, `Enable`); missing file; missing required column header; duplicate `name`; non-integer `priority`; invalid `enable` (`"yes"`); unknown column; `Disable` row; invalid `link` URL; optional `ttl_seconds` parsing
- [X] T024 [P] [US1] `internal/config/tokens_test.go` — TC-U-TOK-01..04: valid token returns active record; unknown token returns nil-false (and log buffer never contains the token plaintext); revoked token returns nil-false; hot reload picks up a newly-added token within debounce window (test uses a chan signaled by the watcher to await reload)
- [X] T025 [P] [US1] `internal/fetcher/http_test.go` — happy path (200 + body + headers parsed); 503 from upstream (returns `OutcomeHTTPError`); slow upstream past `context.WithTimeout` (returns `OutcomeTimeout`); body unparseable as YAML (returns `OutcomeParseError`)
- [X] T026 [P] [US1] `internal/fetcher/cache_test.go` — TC-U-CACHE-01..04: within TTL → cache hit, no fetch; past TTL + within stale window + last refresh failed → stale cache + flag; past TTL + past stale window → nil; single-flight (100 concurrent `Get` calls during refresh-in-progress trigger exactly 1 fetch — uses `golang.org/x/sync/singleflight`)
- [X] T027 [P] [US1] `internal/fetcher/scheduler_test.go` — bootstrap state machine transitions (pending → fetching → succeeded; pending → fetching → retrying → failed); `time.Ticker` fires exactly one fetch per source per TTL; Coordinator `Ready` channel closes only after every enabled source has terminal state
- [X] T028 [P] [US1] `internal/merge/proxies_test.go` — TC-U-MERGE-PROXY-01: two upstreams with same-named proxy → output has both with `<name>@<source-name>` suffix; collision recorded in `[]ProxyCollision`. TC-U-MERGE-PROXY-02 is partly here (own-proxy precedence test deferred to US2)
- [X] T029 [P] [US1] `internal/merge/proxy_groups_test.go` — TC-U-MERGE-GROUP-01: same-name `select` groups across upstreams → single group with member-list union, dedup, order from highest-priority source first. TC-U-MERGE-GROUP-02: `select` vs `url-test` type conflict → highest-priority source wins; conflict in `[]GroupConflict`. (TC-U-MERGE-GROUP-03 always-present `Proxies` group is in US2)
- [X] T030 [P] [US1] `internal/merge/rules_test.go` — TC-U-MERGE-RULES-01: source A priority 2000 + source B priority 1000 → A's rules emit before B's; intra-source order preserved. TC-U-MERGE-RULES-02: equal priorities broken by row order
- [X] T031 [P] [US1] `internal/merge/pipeline_test.go` — pipeline orchestration: given stub upstream payloads + empty own-proxies, returns a `MergedConfig` with expected proxies/groups/rules cardinality and the contributing-sources list; verifies pipeline does NO I/O (passes a "must-not-be-called" fake fetcher)
- [X] T032 [P] [US1] `internal/output/subscription_mode_test.go` — template substitution: given a `MergedConfig` and the served-config template, renders YAML body where the three `__MERGED_*__` placeholders are replaced; result round-trips through `yaml.Unmarshal` cleanly; sets Content-Type. (Header emission for Subscription-Userinfo / Profile-Update-Interval is in US4)
- [X] T033 [P] [US1] `internal/server/auth_test.go` — middleware: missing `?token=` → 401; unknown token → 401 + log carries `sha256:<12-hex>`; revoked token → 401; valid token → handler runs with the `*TokenRecord` injected via context
- [X] T034 [P] [US1] `internal/server/routes/subscription_test.go` — handler: stubs the merge pipeline + output adapter; asserts response code, Content-Type, body length > 0; asserts that bootstrap-not-ready returns 503 with the `warming_up` JSON shape per `contracts/served-subscription.openapi.yaml`

#### Integration tests (all [P] — different files; use `setupTestServer` from T022)

- [X] T035 [P] [US1] `internal/integration/pipeline_test.go` — TC-I-01 (happy path: both upstreams reachable, body is union of proxies + groups + priority-ordered rules); TC-I-04 (cold start, both unreachable, no cache → 503 + `bootstrap_failed`)
- [X] T036 [P] [US1] `internal/integration/collision_test.go` — TC-I-06 (synthetic same-named proxy across upstreams → both with `@<source>` suffix; collision in logs); TC-I-07 (synthetic same-named group across upstreams → unioned members; conflict logged)
- [X] T037 [P] [US1] `internal/integration/auth_test.go` — TC-I-08: no token → 401; unknown → 401 (verify log has hashed prefix); revoked → 401; valid → 200
- [X] T038 [P] [US1] `internal/integration/determinism_test.go` — TC-I-09: 100 sequential `GET /?token=<valid>` requests → identical SHA-256 over body across all 100
- [X] T039 [P] [US1] `internal/integration/cache_test.go` — TC-I-10 (100 concurrent client requests via `errgroup` → upstream-fetch counter on stub increments by 0); TC-I-11 (advance fake clock by `ttl_seconds` → exactly one fetch per source); TC-I-12 (100 concurrent fetches at cache-expiry instant → exactly one upstream HTTP call per source via singleflight)
- [X] T040 [P] [US1] `internal/integration/reload_test.go` — TC-I-17: corrupt the CSV mid-run → fsnotify event fires → reload errors → previous valid config still serves traffic; failure logged
- [X] T041 [P] [US1] `internal/integration/snapshot_test.go` — TC-S-01 stub: registers the snapshot test with cupaloy but expects to fail until T056 generates the baseline file

### Implementation for User Story 1 (write AFTER tests are red)

- [X] T042 [US1] `internal/config/subscriptions.go` — CSV loader using `encoding/csv`; header-row schema validation; per-row validation (URL parse, integer priority, case-insensitive `Enable`/`Disable`, unknown-column rejection); typed errors `ConfigLoadError`, `ConfigSchemaError`, `ConfigValidationError`. Per FR-001a, FR-001b
- [X] T043 [US1] `internal/config/tokens.go` — `TokenStore` struct with `sync.RWMutex`-guarded `byToken map[string]*TokenRecord`; `Lookup(token)` returns `(*TokenRecord, bool)` with revoked/expired filtering; `fsnotify.Watcher` with 250ms debounce reloads on file change; reload errors keep previous store in effect with a warning log. Per FR-019, FR-019a, FR-017
- [X] T044 [US1] `internal/fetcher/http.go` — `UpstreamFetcher` struct holding `*http.Client`; `Fetch(ctx context.Context, source config.SubscriptionRow) (*UpstreamCachedPayload, *FetchResult, error)` building the request with `req.WithContext(ctxWithTimeout)`, parsing response headers via the foundational header parsers, parsing body with `yaml.v3` into `*yaml.Node` (preserves key order). Sanitizes the URL in logs (no token in path/query). Per FR-005b, FR-010, FR-013
- [X] T045 [US1] `internal/fetcher/cache.go` — In-memory cache with `sync.RWMutex`; `Get(name) (*UpstreamCachedPayload, bool)` returns the entry if within TTL or within stale-on-error window after a recent failure; `Set` stores in memory and (if `CACHE_DIR != ""`) writes JSON to `${CACHE_DIR}/${name}.json`; `LoadFromDisk(dir)` rehydrates on cold start. `singleflight.Group` keyed by source name so concurrent `RefreshIfStale` calls collapse to one upstream fetch. Per FR-003, FR-003a
- [X] T046 [US1] `internal/fetcher/scheduler.go` — `Coordinator` struct holding per-source state; `Start(ctx)` launches one goroutine per enabled source with a `time.Ticker` at the source's TTL; goroutines call `UpstreamFetcher.Fetch` then `Cache.Set` then update `SourceState`; `Coordinator.Ready` channel closes when every enabled source's bootstrap state is terminal (succeeded or failed); `Coordinator.AllSucceeded()` reports green vs degraded. Per FR-003a, FR-003b
- [X] T047 [P] [US1] `internal/merge/proxies.go` — `MergeProxies(upstream map[string][]*yaml.Node, own []*yaml.Node) ([]*yaml.Node, []ProxyCollision)`; deterministic per-source suffix `<name>@<source>`; own-proxy keeps its name on collision (FR-008); sorts upstream sources by CSV `priority` descending so the highest-priority source's name wins on a tie. Per FR-002
- [X] T048 [P] [US1] `internal/merge/proxy_groups.go` — `MergeProxyGroups(upstream map[string][]*yaml.Node, own []*yaml.Node, priorityBySource map[string]int) ([]*yaml.Node, []GroupConflict)`; same-name union of `proxies:` member lists with dedup; group `type` and other attributes from highest-priority source; conflicts recorded. Always-present `Proxies` group is added in US2 (T064)
- [X] T049 [P] [US1] `internal/merge/rules.go` — `MergeRules(upstream map[string][]string, priorityBySource map[string]int) []string`; sources sorted by priority descending, ties broken by row index in the CSV; intra-source order preserved. Per FR-005a
- [X] T050 [US1] `internal/merge/pipeline.go` — `Pipeline` struct holding cache, config, clock; `Build(now time.Time) *MergedConfig` calls the merge primitives in sequence and assembles `MergedConfig`. Pure-functional: takes all inputs explicitly, performs no I/O, no `time.Now()`. Depends on T047, T048, T049
- [X] T051 [US1] `internal/output/subscription_mode.go` — `Render(merged *MergedConfig, templatePath string) (body []byte, headers http.Header, err error)`; loads the template once (or accepts a pre-loaded `*yaml.Node`), serializes each merged block to YAML, performs string substitution at the three placeholders, re-parses and re-emits the result to ensure clean YAML output, sets `Content-Type: application/yaml; charset=utf-8`. Header emission for Subscription-Userinfo / Profile-Update-Interval / Cache-Control happens in US4 (T079). Per FR-005c
- [X] T052 [US1] `internal/server/auth.go` — `RequireToken(store *config.TokenStore, logger *slog.Logger) func(http.Handler) http.Handler` middleware: extracts `?token=` from URL, calls `store.Lookup`, on rejection returns 401 with empty body + structured log carrying `observability.SanitizeToken(token)` only. Per FR-019, FR-019a
- [X] T053 [US1] `internal/server/routes/subscription.go` — `Handler(pipeline *merge.Pipeline, adapter *output.SubscriptionMode, coordinator *fetcher.Coordinator, clock clock.Clock) http.HandlerFunc`: if `coordinator.Ready` not closed → 503 + JSON warming_up; if `coordinator.AllSucceeded() == false` → 503 + JSON bootstrap_failed; else build `MergedConfig`, render, write headers + body. Per FR-003b, FR-019b
- [X] T054 [US1] `internal/server/app.go` — `App` struct holding all dependencies; `Run(ctx context.Context)` starts the Coordinator goroutines, blocks on `Coordinator.Ready` (with timeout per `BootstrapMaxAttemptsPerSource * BootstrapAttemptDelaySeconds`), then registers handlers on a `*http.ServeMux` with Go 1.22 pattern routing (`mux.Handle("GET /", auth(subscriptionHandler))`), starts `*http.Server`, handles `SIGTERM`/`SIGINT` via `srv.Shutdown(ctx)`. Per FR-003b, FR-019c
- [X] T055 [US1] `cmd/server/main.go` — entry point: load `ServerConfig` from env, init logger, load subscriptions CSV + tokens, construct fetcher / cache / coordinator / pipeline / output adapter / app, call `app.Run(ctx)` with `signal.NotifyContext` for graceful shutdown
- [X] T056 [US1] Generate the TC-S-01 baseline snapshot: run `UPDATE_SNAPSHOTS=true go test ./internal/integration/ -run TestSnapshot_ServedConfig`; commit `internal/integration/testdata/snapshots/served-config.snap.yaml`. The fake clock for snapshot tests is fixed at `2026-04-30T00:00:00Z` per plan.md TC-S section

**Checkpoint**: User Story 1 fully functional. Operator can run `go run ./cmd/server` against the example fixtures, point a Clash client at `http://localhost:8080/?token=<valid>`, and see the merged subscription. Integration tests pass; snapshot is committed; tests for `proxies`, `proxy-groups`, and `rules` merge primitives all green. **MVP-shippable.**

---

## Phase 4: User Story 2 — Inject Own Proxy Servers (Priority: P2)

**Goal**: Operator's user-declared proxies (in a separate YAML file with `proxies` and `proxy-groups` keys) appear in the served subscription alongside upstream proxies; the served config always contains a `select`-type `Proxies` group with the union of every proxy so the client UI is selectable even if no upstream contributed a select group.

**Independent Test**: With the synthetic `internal/integration/testdata/fixtures/own-proxies.yaml` declaring two own-proxies and one own-group on top of the working US1 setup, `GET /?token=<valid>` returns a body whose `proxies` block contains the two own-proxies (by name) and whose `proxy-groups` block contains both the own-defined `My-Own` group and an always-present `Proxies` `select` group whose members are the union of all proxies.

### Tests for User Story 2 (write FIRST, see them FAIL)

- [X] T057 [P] [US2] `internal/config/own_proxies_test.go` — TC-U-OWN-01..05: loads valid fixture (2 proxies + 1 group); missing required field on a proxy → `*OwnProxyValidationError` naming the entry; duplicate proxy name within file → error; group references non-existent proxy → error; empty `proxies: []` and `proxy-groups: []` is valid
- [X] T058 [P] [US2] Add to `internal/merge/proxy_groups_test.go` — TC-U-MERGE-GROUP-03: regardless of upstream input (even with zero upstream groups), output includes a `Proxies` `select` group whose members are the union of all proxies after suffixing
- [X] T059 [P] [US2] Add to `internal/merge/proxies_test.go` — TC-U-MERGE-PROXY-02: own-proxy named identically to an upstream proxy → upstream gets the `@<source>` suffix; own-proxy keeps its name unchanged; collision logged
- [X] T060 [P] [US2] `internal/integration/own_proxies_test.go` — TC-I-16: with the own-proxies fixture loaded, `GET /` body contains both own-proxies in the merged `proxies` list and the own group `My-Own` in `proxy-groups`; both own-proxies appear as members of the always-present `Proxies` group

### Implementation for User Story 2

- [X] T061 [US2] `internal/config/own_proxies.go` — Loader using `yaml.v3` parsing into `OwnProxiesFile` with `*yaml.Node` slices; validators extract `proxyMeta` and `proxyGroupMeta` for the small typed surface; uniqueness checks; group-references-real-proxy check. Returns typed `*OwnProxyValidationError`. Per FR-006, FR-007
- [X] T062 [US2] Update `internal/merge/proxy_groups.go` — Add `appendProxiesGroup(merged []*yaml.Node, allProxyNames []string, groupName string) []*yaml.Node` helper; call it as the last step of `MergeProxyGroups`. Group name comes from `ServerConfig.ProxiesGroupName` (default `Proxies`). Per FR-009a
- [X] T063 [US2] Update `internal/merge/pipeline.go` — `Pipeline.Build` takes the loaded `*OwnProxiesFile` as an input; threads its `Proxies` and `ProxyGroups` slices into the merge primitives so own-proxies and own-groups participate in the union and collision logic
- [X] T064 [US2] Update `cmd/server/main.go` — Load own-proxies YAML at startup via `config.LoadOwnProxies`; pass the result into the pipeline constructor; register a fsnotify watcher on the file (debounced 250ms) so a reload picks up new own-proxies without a server restart. Reload errors leave the previous file in effect (FR-017)
- [X] T065 [US2] Update the TC-S-01 baseline snapshot to include the own-proxies fixture: re-run `UPDATE_SNAPSHOTS=true go test ./internal/integration/ -run TestSnapshot_ServedConfig` and commit the updated `served-config.snap.yaml`. Verify the diff in PR review (per Constitution snapshot-stability gate)

**Checkpoint**: User Story 2 complete. Own-proxies are visible end-to-end; always-present `Proxies` group makes selection trivial in the client UI; updated snapshot pinned.

---

## Phase 5: User Story 3 — Aggregated Traffic + Daily Allowance + `/health` (Priority: P3)

**Goal**: `/health` exposes per-upstream fetch state and a three-component daily-allowance figure (per-source weighted per-day rate, no-expiry remaining bytes, expired-source flags). The aggregator reads the captured `Subscription-Userinfo` headers from the cache and computes both the aggregated header values (for US4 to emit) and the daily allowance.

**Independent Test**: With two synthetic upstream stubs returning known `Subscription-Userinfo` headers (A: `total=200GB / used=50GB / expire=now+30d`; B: `total=100GB / used=20GB / expire=now+5d`), `GET /health` returns JSON whose `dailyAllowance.perDayRateBytes ≈ 21GB` (= `5GB + 16GB`) and whose per-source state mirrors the cached values. With `expire=0` on every source, `dailyAllowance.perDayRateBytes == 0` and `dailyAllowance.noExpiryRemainingBytes` is the sum.

### Tests for User Story 3 (write FIRST)

- [X] T066 [P] [US3] `internal/merge/traffic_test.go` — TC-U-TRAFFIC-01..06: aggregation of upload/download/total + earliest-non-zero `expire`; per-source weighted daily-allowance with `clock.FakeClock`; `expire=0` source contributes to `noExpiryRemainingBytes` not the rate; `expire<now` source contributes 0 + appears in `expiredSourceFlags`; `Profile-Update-Interval` minimum-non-zero aggregation; default fallback when all sources omit
- [X] T067 [P] [US3] `internal/server/routes/health_test.go` — handler: stubs `Coordinator` + `Pipeline.ComputeDailyAllowance`; asserts response shape matches `contracts/health.openapi.yaml` (200 status code, JSON content-type, all required fields present, per-source state correctly translated)
- [X] T068 [P] [US3] `internal/integration/health_test.go` — TC-I-02 (alpha stub returns 503 mid-test after warm cache → next /health shows alpha `degraded`, `servingFromCache: true`, beta still `succeeded`); TC-I-03 (advance clock past stale window with alpha still failing → /health shows alpha `failed_no_cache`); TC-I-05 (CSV row has `enable=Disable` → alpha in /health with `enabled: false` + startup log lists disabled sources)
- [X] T069 [P] [US3] `internal/integration/daily_allowance_test.go` — TC-I-13 (per-source weighted: A 5GB/day + B 16GB/day → /health perDayRateBytes ≈ 21GB); TC-I-14 (both sources `expire=0` → perDayRateBytes 0 + noExpiryRemainingBytes is sum); TC-I-15 (one source `expire = now - 1d` → expiredSourceFlags includes that source; perDayRateBytes excludes it; aggregated `expire` reflects next-soonest)
- [X] T070 [P] [US3] `internal/integration/health_snapshot_test.go` — TC-S-03 stub: registers cupaloy snapshot test for `/health` JSON body under fixed inputs + fixed clock; expects to fail until T076 generates the baseline

### Implementation for User Story 3

- [X] T071 [US3] `internal/merge/traffic.go` — `AggregateSubscriptionUserinfo(perSource map[string]fetcher.SubscriptionUserinfo) fetcher.SubscriptionUserinfo` (sum of upload/download/total, earliest non-zero `expire`, or 0 if all zero). `AggregateProfileUpdateInterval(perSource map[string]int, defaultHours int) int` (minimum non-zero, fall back to default). `ComputeDailyAllowance(perSource map[string]fetcher.SubscriptionUserinfo, clk clock.Clock) DailyAllowance` per the FR-011b formula (per-source weighted; separate no-expiry remaining; expired-source flags). All three are pure functions. Per FR-011, FR-011a, FR-011b
- [X] T072 [US3] Update `internal/merge/pipeline.go` — `Pipeline.Build` populates `MergedConfig.AggregatedSubscriptionUserinfo` and `MergedConfig.AggregatedProfileUpdateIntervalHours` via the new traffic functions; expose a separate `Pipeline.ComputeDailyAllowance(now time.Time) DailyAllowance` for the `/health` handler (it's a different output shape from `MergedConfig`)
- [X] T073 [US3] `internal/server/routes/health.go` — `Handler(coordinator *fetcher.Coordinator, pipeline *merge.Pipeline, clock clock.Clock) http.HandlerFunc`: builds the `HealthResponse` from `Coordinator.SourceStates()` + `Pipeline.ComputeDailyAllowance(clock.Now())`; serializes to JSON per `contracts/health.openapi.yaml`; returns 503 if `coordinator.Ready` not closed or any source's `bootstrapState == failed`. Per FR-015
- [X] T074 [US3] Update `internal/fetcher/scheduler.go` — Add `Coordinator.SourceStates() []SourceState` returning a snapshot of every source's per-source state (struct from data-model.md §SourceState). Threaded with the coordinator's mutex
- [X] T075 [US3] Update `internal/server/app.go` — Register `mux.Handle("GET /health", healthHandler)` (no auth middleware on `/health` per the contracts file's note about cluster-internal-only exposure); update `cmd/server/main.go` to wire the dependencies
- [X] T076 [US3] Generate the TC-S-03 baseline snapshot: `UPDATE_SNAPSHOTS=true go test ./internal/integration/ -run TestSnapshot_Health`; commit `internal/integration/testdata/snapshots/health.snap.json`

**Checkpoint**: User Story 3 complete. Operator can `curl /health | jq` and see per-source state plus the three daily-allowance components. The aggregated `Subscription-Userinfo` and `Profile-Update-Interval` values exist on `MergedConfig` but are not yet emitted as response headers (that's US4).

---

## Phase 6: User Story 4 — Drop-In Clash Subscription Server Behavior (Priority: P2)

**Goal**: The served `GET /` response carries the `Subscription-Userinfo` and `Profile-Update-Interval` HTTP response headers that stock Mihomo / Sparkle clients consume, plus `Content-Type: application/yaml; charset=utf-8` and `Cache-Control: no-store, no-cache, must-revalidate`. A stock client given the URL with a valid token shows the usage bar and respects the suggested update interval without any custom configuration.

**Note on dependency order**: US4 carries priority P2 (above US3's P3) but its implementation consumes US3's aggregated header values. Implementing US4 before US3 would mean emitting `Subscription-Userinfo: upload=0; download=0; total=0; expire=0` — meaningless. We therefore defer US4 to after US3. (Spec author's intent: P2 reflects "important UX guarantee", not implementation order.)

**Independent Test**: A `GET /?token=<valid>` request returns `Content-Type: application/yaml; charset=utf-8`, a well-formed `Subscription-Userinfo` header matching the wire format `upload=N; download=N; total=N; expire=N`, and a `Profile-Update-Interval` header carrying the aggregated minimum hours. A 401 response carries no `Subscription-Userinfo` header (avoiding quota leakage to unauthenticated requesters).

### Tests for User Story 4 (write FIRST)

- [X] T077 [P] [US4] Update `internal/output/subscription_mode_test.go` — Add assertions: emitted headers include `Subscription-Userinfo` with the wire-format string; `Profile-Update-Interval` with integer hours; `Content-Type: application/yaml; charset=utf-8`; `Cache-Control: no-store, no-cache, must-revalidate`
- [X] T078 [P] [US4] `internal/integration/headers_test.go` — Extends TC-I-01 to assert response headers; adds verification that the 401 path (no/invalid token) emits **no** `Subscription-Userinfo` header in the response per `contracts/served-subscription.openapi.yaml`
- [X] T079 [P] [US4] `internal/integration/headers_snapshot_test.go` — TC-S-02 stub: snapshots the exact `Subscription-Userinfo` wire-format string emitted by the server under fixed inputs + fixed clock

### Implementation for User Story 4

- [X] T080 [US4] Update `internal/output/subscription_mode.go` — `Render` now also returns the response headers for emission: `Subscription-Userinfo` formatted as `fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d", ...)` from `MergedConfig.AggregatedSubscriptionUserinfo`; `Profile-Update-Interval: %d` from the aggregated hours field; `Content-Type: application/yaml; charset=utf-8`; `Cache-Control: no-store, no-cache, must-revalidate`. Per FR-011, FR-011a, FR-019b
- [X] T081 [US4] Update `internal/server/routes/subscription.go` — Handler writes the headers returned from `output.Render` BEFORE writing the body (mandatory order in `net/http`); ensure `w.Header().Set` for each header
- [X] T082 [US4] Update `internal/server/auth.go` — When rejecting with 401, explicitly do NOT set any quota-bearing headers; ensure response body is empty; write a structured log line via `observability.SanitizeToken`
- [X] T083 [US4] Generate the TC-S-02 baseline snapshot: `UPDATE_SNAPSHOTS=true go test ./internal/integration/ -run TestSnapshot_SubscriptionUserinfo`; commit `internal/integration/testdata/snapshots/subscription-userinfo.snap.txt`
- [X] T084 [US4] Manual validation per quickstart §4d: `go run ./cmd/server` against the example fixtures, paste the URL into a real Mihomo / Sparkle client, verify the proxy list populates, the usage bar shows the aggregated quota, and the auto-refresh interval matches `Profile-Update-Interval`. Document the validation result in the PR description

**Checkpoint**: User Story 4 complete. Stock Clash clients treat the URL as a normal subscription server. All four user stories are independently testable and demoable; the snapshot suite (TC-S-01, 02, 03) is committed and gates the deterministic-output contract.

---

## Phase 7: Polish & Cross-Cutting Concerns

- [X] T085 [P] `internal/integration/sanitization_test.go` — TC-I-18: parse the served body via `yaml.v3`; `strings.Contains` for any of: an upstream URL (path token), upstream-side `Subscription-Userinfo` echo, raw token from `tokens.json`. Capture the test logger buffer and verify zero token-plaintext occurrences. Per SC-007
- [X] T086 [P] Add `staticcheck` to `Makefile` `lint` and `check` targets; run `staticcheck ./...` and resolve every warning (zero exemptions)
- [X] T087 [P] CI configuration (e.g., `.github/workflows/ci.yml` if using GitHub Actions): runs `make check` (vet + staticcheck + test + git diff exit-code) on every PR; the `git diff --exit-code` step catches any inadvertently-modified snapshot files
- [X] T088 [P] `deploy/k8s/deployment.yaml` — Single-replica Deployment; image `honkai-rule-server:latest`; resources `requests: {cpu: 50m, memory: 64Mi}, limits: {cpu: 500m, memory: 128Mi}`; readiness probe = `GET /health` (success only when bootstrap done); liveness probe = TCP port 8080; secret mount at `/secret/`; configmap mount at `/template/`; env vars per quickstart §3
- [X] T089 [P] `deploy/k8s/service.yaml` — `ClusterIP` exposing port 8080
- [X] T090 [P] `deploy/k8s/ingress.yaml` — TLS termination + path routing only `/` (NOT `/health`) to the public; per FR-019c
- [X] T091 [P] `deploy/k8s/configmap-served-template.yaml` — ConfigMap whose value is the contents of `templates/served-config.template.yaml`
- [X] T092 [P] `config/README.md` — Operator notes: file layout, how to generate tokens, how to disable a source, secret-rotation procedure
- [X] T093 Update root `README.md` with sections: Overview, Quickstart link, Architecture (link to plan + data-model), Deployment (k8s manifests), Tests (`make test`, `make snapshot-update`), Module-path swap instructions
- [X] T094 Run quickstart.md end-to-end against the committed `example/` fixtures: `cp example/subscriptions.csv config/subscriptions.csv`, fabricate `config/own-proxies.yaml` and `config/tokens.json` per quickstart §2b/2c, `go run ./cmd/server`, verify `/health` and `/?token=<tok>` per §4. Document any gaps as follow-up issues
- [X] T095 Update `CLAUDE.md` to reflect that the server is now built (not just planned): change "read the current plan" wording to "see the current plan and the implemented packages under `internal/`"

**Checkpoint**: Service is production-deployable; CI gates every PR; sanitization audit passes; quickstart works end-to-end.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1, T001–T008)**: No dependencies. Can start immediately.
- **Foundational (Phase 2, T009–T022)**: Depends on Setup. **BLOCKS all user stories.**
- **User Story 1 (Phase 3, T023–T056)**: Depends on Foundational. The MVP — ship this and stop if needed.
- **User Story 2 (Phase 4, T057–T065)**: Depends on US1's merge primitives + pipeline.
- **User Story 3 (Phase 5, T066–T076)**: Depends on US1's fetcher + cache + pipeline (consumes captured headers).
- **User Story 4 (Phase 6, T077–T084)**: Depends on US1's HTTP route + output adapter AND on US3's aggregated header values (US4 emits what US3 computes).
- **Polish (Phase 7, T085–T095)**: Depends on every story being complete.

### User Story Dependencies (the priority-vs-implementation-order tension)

- **US1 (P1)** — no story dependencies
- **US2 (P2)** — depends on US1's merge primitives
- **US3 (P3)** — depends on US1's fetcher + pipeline (independent of US2)
- **US4 (P2, but implemented after US3)** — depends on US3's aggregated header values; without US3 it would emit zero/null headers, which is worse than not emitting them

If you must ship a partial product before US3 lands, US4 can ship a degraded version that emits `Content-Type` + `Cache-Control` only, with `Subscription-Userinfo` / `Profile-Update-Interval` headers omitted. The 4-user-story priority order in the spec (P1, P2, P3, P2) reflects business value, not technical sequencing.

### Within Each Story

- Tests are written FIRST in their dedicated test files; CI gates that they exist and fail before implementation lands (per Constitution Principle IV)
- Models / structs before services; services before HTTP handlers
- Pure-functional `merge/` primitives before the pipeline that orchestrates them
- Pipeline before the output adapter
- Output adapter before the HTTP route
- HTTP route before `cmd/server/main.go` wiring

### Parallel Opportunities

- All [P] Setup tasks (T003–T007) can run in parallel after T002 creates the directory structure
- All [P] Foundational tasks (fixtures T009–T014; clock T015; logger T016–T017; headers T018–T019; testmain T022) can run in parallel; only the typed-config trio (T020–T021) is sequential
- Within US1: all 12 unit-test files (T023–T034) are [P]; all 7 integration-test files (T035–T041) are [P]; the merge primitives (T047, T048, T049) are [P]
- Within US2: T057–T060 are [P]; T061 has no deps; T062–T065 are sequential
- Within US3: T066–T070 are [P]; T071–T076 are sequential within the package
- Within US4: T077–T079 are [P]; T080–T084 are sequential
- All Phase-7 polish tasks T085–T091 are [P] (different files / different concerns)

---

## Parallel Example: User Story 1 unit tests

```bash
# Launch all 12 US1 unit-test files together (one developer per file or one LLM per file):
Task: "internal/config/subscriptions_test.go — TC-U-CSV-01..10"
Task: "internal/config/tokens_test.go — TC-U-TOK-01..04"
Task: "internal/fetcher/http_test.go — happy path, timeout, non-200, parse error"
Task: "internal/fetcher/cache_test.go — TC-U-CACHE-01..04"
Task: "internal/fetcher/scheduler_test.go — bootstrap state machine + ticker"
Task: "internal/merge/proxies_test.go — TC-U-MERGE-PROXY-01"
Task: "internal/merge/proxy_groups_test.go — TC-U-MERGE-GROUP-01, 02"
Task: "internal/merge/rules_test.go — TC-U-MERGE-RULES-01, 02"
Task: "internal/merge/pipeline_test.go — orchestration with stubs"
Task: "internal/output/subscription_mode_test.go — template rendering"
Task: "internal/server/auth_test.go — token middleware"
Task: "internal/server/routes/subscription_test.go — handler"
```

After all tests are red (compiling but failing), proceed to implementation.

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Phase 1: Setup (T001–T008)
2. Phase 2: Foundational (T009–T022) — **CRITICAL** path
3. Phase 3: User Story 1 — tests first (T023–T041), then implementation (T042–T056)
4. **STOP and VALIDATE**: Run the integration tests, generate the TC-S-01 snapshot, manually paste the URL into a Clash client, verify the merged proxy list appears
5. **Ship the MVP** — operator gets value (one URL replaces N URLs in their client) without US2/US3/US4

### Incremental Delivery

1. MVP (US1) — server returns the merged subscription
2. Add US2 — own-proxies appear in the merged output; selectability guaranteed by the `Proxies` group
3. Add US3 — operator can read per-source health and daily-allowance figures via `/health`
4. Add US4 — stock client UX (usage bar, auto-refresh interval); snapshot suite complete
5. Polish phase — sanitization audit, K8s manifests, CI gates, README

### Parallel Team Strategy

- Two engineers can comfortably work US2 and US3 in parallel after US1 lands (different files; only the pipeline integration touches a shared file, and that's a small merge)
- US4 must wait for US3 to land (its implementation reads US3's aggregated values)
- Polish-phase tasks fan out completely across the team

---

## Notes

- **Test format expectation**: Each test function name should embed the TC id for grep-ability — e.g., `func TestCSV_01_LoadsExampleFile(t *testing.T) { ... }`, `func TestI_01_HappyPath(t *testing.T) { ... }`, `func TestSnapshot_ServedConfig(t *testing.T) { ... }`. This makes `go test -run TestCSV_01` immediately useful
- **Snapshot updates**: `UPDATE_SNAPSHOTS=true go test ./internal/integration/...` regenerates the three snapshot files. Updates require explicit reviewer sign-off in the PR (per Constitution Development Workflow snapshot-stability gate)
- **Race detector**: Always run `go test -race ./...` before any PR — the cache and token store are shared mutable state guarded by `sync.RWMutex`; `-race` will catch any new shared-state mistake
- **Module path**: The placeholder `github.com/<owner>/honkai-rule-server` should be swapped to the actual repo path on first commit via `go mod edit -module ...` and `go mod tidy` — see quickstart §8
- **Avoid**: cross-story dependencies that break independence; `time.Now()` calls in `internal/merge/` (use the injected `Clock`); logging tokens in plaintext (always go through `observability.SanitizeToken`); silent error swallowing in any loader (Constitution Principle III demands loud failure)

### Total task count: **95**

| Phase | Tasks | Story | Test count | Impl count |
|---|---|---|---|---|
| 1 Setup | T001–T008 | — | 0 | 8 |
| 2 Foundational | T009–T022 | — | 2 | 12 |
| 3 US1 (P1, MVP) | T023–T056 | US1 | 19 | 15 |
| 4 US2 (P2) | T057–T065 | US2 | 4 | 5 |
| 5 US3 (P3) | T066–T076 | US3 | 5 | 6 |
| 6 US4 (P2) | T077–T084 | US4 | 3 | 5 |
| 7 Polish | T085–T095 | — | 1 | 10 |

**Test tasks**: 34 (≈36% of all tasks). **Implementation tasks**: 61. Tests-to-impl ratio ≈ 1:1.8 (high; appropriate for a feature where Constitution Principle IV is non-negotiable and snapshot stability is the primary correctness gate).

### Suggested MVP scope

**Phases 1 + 2 + 3 only** (T001–T056): delivers a fully-functional aggregator that solves the primary user need ("one URL replaces N URLs in my client") with cold-start fail-closed safety, byte-identical output, token authentication, and 100% test coverage of the listed TC-U / TC-I / TC-S ids in scope for US1. Ship this; defer US2/US3/US4 to subsequent milestones.
