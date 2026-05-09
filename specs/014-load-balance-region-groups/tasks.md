# Tasks: Load-Balance Variants of Auto-Emitted Region & Continent Proxy Groups

**Input**: Design documents from `/specs/014-load-balance-region-groups/`
**Prerequisites**: plan.md (required), spec.md (required), research.md, data-model.md, contracts/, quickstart.md

**Tests**: REQUIRED. Constitution Principle IV (Test-First, Real-Input Integration) is NON-NEGOTIABLE for this project. Every task that modifies the transformation core, classifiers, or proxy-group construction has a paired `_test.go` task placed immediately before it. Tests must be written and fail before implementation lands.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story. The two user stories in `spec.md` (US1 — emit lb groups; US2 — fan-out for own-proxies through lb groups) share Constitution-Principle-I/V foundations consolidated in Phase 2.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: Which user story this task belongs to (US1, US2)
- Include exact file paths in descriptions

## Path Conventions

Single Go module rooted at the repository. Production code lives under `internal/`; tests live alongside source files (Go convention: `foo.go` ↔ `foo_test.go`); integration snapshots live under `internal/integration/testdata/snapshots/`. Paths shown are absolute-from-repo-root and match the layout in `plan.md` § Project Structure.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Project initialization and basic structure.

This feature requires no setup tasks. The Go module, testing harness (`go test`), snapshot library (`bradleyjkemp/cupaloy/v2`), linter (`staticcheck`), and CI checks are already in place from previous features. The `014-load-balance-region-groups` git branch and spec directory are already created.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Configuration plumbing that BOTH user stories depend on. The `LoadBalanceParams` struct, env-var loader, validation, and the merge-package mirror struct must exist before US1 can emit lb groups (US1 needs to read params) and before US2 can be exercised in the integration harness.

**⚠️ CRITICAL**: No user story work can begin until this phase is complete.

- [ ] T001 [P] Add table tests for `config.LoadBalanceParams.{Load,Validate}` covering: all defaults applied when env vars unset; all six env vars set with valid values; non-integer `LOAD_BALANCE_INTERVAL_SECONDS`; zero / negative `LOAD_BALANCE_TIMEOUT_MS`; non-bool `LOAD_BALANCE_LAZY`; unknown `LOAD_BALANCE_STRATEGY` value; case-mismatched strategy (e.g., `Round-Robin`); multiple simultaneous errors accumulate into a single error message — in `internal/config/server_test.go`.

- [ ] T002 Implement `config.LoadBalanceParams` struct (with FR-003 defaults: URL gstatic 204, IntervalSeconds 300, TimeoutMS 1500, MaxFailedTimes 3, Lazy true, Strategy round-robin), the six `LOAD_BALANCE_*` env-var loader block, and accumulated-error validation — in `internal/config/server.go`. Mirror 012's `URL_TEST_*` block exactly. Add the strategy enum check `{round-robin, consistent-hashing, sticky-sessions}` (case-sensitive). T001's tests must now pass.

- [ ] T003 [P] Add `merge.LoadBalanceParams` mirror struct (identical fields to `config.LoadBalanceParams`, no method dependency on config) — in `internal/merge/region.go`, alongside the existing `merge.URLTestParams`. Documents the Principle-I cross-package isolation rationale in a comment.

**Checkpoint**: Foundation ready — `LoadBalanceParams` is loadable, validated, and bridge-able into the merge layer. User story implementation can now begin.

---

## Phase 3: User Story 1 - Spread regional traffic across all healthy nodes simultaneously (Priority: P1) 🎯 MVP

**Goal**: For every existing `_region_<CC>` / `_region_UNKNOWN` / `_continent_<CONT>` url-test group emitted today, the server emits a paired `_lb_<...>` sibling of `type: load-balance` carrying the same member list and the six FR-003 fields. The new lb groups appear as direct members of the always-present `Proxies` selector. The startup log surfaces the resolved `LoadBalanceParams`.

**Independent Test**: Configure two upstream sources contributing JP and HK proxies. With this story complete (and US2 NOT yet implemented), fetch the served subscription. Assert: every existing `_region_<CC>` / `_continent_<CONT>` group has a paired `_lb_<...>` sibling immediately after it in `proxy-groups:` carrying the same member list, `type: load-balance`, and the six lb fields in the documented order. Assert the always-present `Proxies` selector contains every `_lb_<...>` group as a direct member alongside the existing url-test references. Assert the startup log emits a `load_balance_params` line listing all six resolved fields. Assert url-test groups (012) are byte-identical to pre-feature output.

### Tests for User Story 1 (write FIRST, ensure they FAIL before implementation)

- [ ] T004 [P] [US1] Add unit tests for `newLoadBalanceGroup` covering: emitted node has correct `name`, `type: load-balance`, `proxies` member list, and the six lb fields with correct types and values; field-emission order matches the user-supplied example — in `internal/merge/region_test.go`.

- [ ] T005 [P] [US1] Add unit tests for paired emission in `AppendRegionGroups`: given upstream proxies in two countries, the returned `groups` slice contains `_region_JP`, `_lb_region_JP`, `_region_HK`, `_lb_region_HK` in that order; both members of each pair have the same `proxies:` member list; the lb sibling carries `type: load-balance` and the six fields populated from the passed `LoadBalanceParams`; `_region_UNKNOWN` and `_lb_region_UNKNOWN` follow the same paired pattern when unclassified proxies exist — in `internal/merge/region_test.go`.

- [ ] T006 [P] [US1] Add unit tests for paired emission in `AppendContinentGroups`: given two regions in the same continent, the returned `groups` slice contains `_continent_AS` followed by `_lb_continent_AS`; the lb continent group carries the same flat-union member list as the url-test continent group (per 003 FR-011), `type: load-balance`, and the six lb fields — in `internal/merge/region_test.go`.

- [ ] T007 [P] [US1] Add unit tests asserting the always-present `Proxies` selector group's `proxies:` member list contains both `_region_<CC>` and `_lb_region_<CC>` entries (interleaved per FR-013) and both `_continent_<CONT>` and `_lb_continent_<CONT>` entries — in `internal/merge/region_test.go` (or `proxy_groups_test.go` if more natural).

- [ ] T008 [P] [US1] Add unit tests for `reorderProxyGroupFields` covering load-balance groups: given a `type: load-balance` group with the nine fields in arbitrary input order, the output content order is `name, type, proxies, url, interval, lazy, strategy, timeout, max-failed-times`; existing url-test groups remain in their 012 order (`name, type, proxies, url, interval, timeout, max-failed-times, lazy`) — in `internal/output/subscription_mode_test.go`.

- [ ] T009 [P] [US1] Add unit/integration test asserting startup emits a `load_balance_params` structured log line with the six resolved field values — in the appropriate test file under `internal/server/` (mirror 012's url-test startup-log assertion if present, otherwise add a new assertion harness).

- [ ] T010 [P] [US1] Add `Pipeline.WithLoadBalanceParams` builder test: a `Pipeline` constructed without the call has zero-valued `loadBalanceParams`; with the call, `Build()` threads the value into the emitted `_lb_*` group fields — in `internal/merge/pipeline_test.go`.

### Implementation for User Story 1

- [ ] T011 [US1] Add `Pipeline.loadBalanceParams` field (type `LoadBalanceParams`) and the `Pipeline.WithLoadBalanceParams(params LoadBalanceParams) *Pipeline` builder method — in `internal/merge/pipeline.go`. Mirrors `WithURLTestParams` exactly. T010 must now pass.

- [ ] T012 [US1] Add `newLoadBalanceGroup(name string, members []string, params LoadBalanceParams) *yaml.Node` helper that emits the nine key/value pairs in the order: `name, type=load-balance, proxies, url, interval, lazy, strategy, timeout, max-failed-times` — in `internal/merge/region.go`. Use `setMappingValue` and `setMappingMembers` per the existing pattern. T004 must now pass.

- [ ] T013 [US1] Modify `AppendRegionGroups` to accept a new `loadBalanceParams LoadBalanceParams` positional parameter and, in the country-loop and the `_region_UNKNOWN` block, append a paired `_lb_region_<CC>` (or `_lb_region_UNKNOWN`) group via `newLoadBalanceGroup` immediately after each url-test sibling. Append both names to `regionGroupNames` so the `Proxies` selector membership block picks both up — in `internal/merge/region.go`. T005 + T007 must now pass.

- [ ] T014 [US1] Modify `AppendContinentGroups` to accept the same new positional `loadBalanceParams` parameter and, in the continent-loop, append a paired `_lb_continent_<CONT>` group via `newLoadBalanceGroup` carrying the same flat-union member list as its url-test sibling. Append both names to `continentGroupNames` — in `internal/merge/region.go`. T006 must now pass.

- [ ] T015 [US1] Update both `AppendRegionGroups` and `AppendContinentGroups` call sites in `Pipeline.Build()` to pass `p.loadBalanceParams` as the new argument — in `internal/merge/pipeline.go`.

- [ ] T016 [US1] Extend `reorderProxyGroupFields` in the output formatter to position the `strategy` field and reorder `lazy` to its load-balance position. Implementation note: emit two ordered passes (one for url-test order, one for load-balance order) gated by the group's `type` field, OR use a single unified `moveFieldToPosition` call list with target positions chosen to satisfy both orders simultaneously (see research Decision 8). Pick whichever produces the smallest snapshot diff — in `internal/output/subscription_mode.go`. T008 must now pass.

- [ ] T017 [US1] Wire `LoadBalanceParams` through pipeline construction in the server bootstrap: copy `cfg.LoadBalanceParams` (config struct) → `merge.LoadBalanceParams` (mirror struct) and call `pipe.WithLoadBalanceParams(...)` — in `internal/server/app.go`. Mirror the existing `WithURLTestParams` wiring exactly.

- [ ] T018 [US1] Add a startup `slog.Info` line `event=load_balance_params` listing the resolved `url`, `interval_seconds`, `timeout_ms`, `max_failed_times`, `lazy`, `strategy` fields — in `internal/server/app.go`, alongside the existing 012 `url_test_params` log line. T009 must now pass.

**Checkpoint**: At this point, US1 should be fully functional. Fetching the served subscription returns paired url-test/lb groups in `proxy-groups:` and `Proxies` selector membership; startup log surfaces the resolved lb params. The fan-out section is unchanged from 008 (US2 not yet wired). All US1 unit tests + the existing 012 url-test snapshot fixtures still pass byte-for-byte.

---

## Phase 4: User Story 2 - Reach own-exits through a load-balanced regional pool (Priority: P1)

**Goal**: 008's existing fan-out machinery generates `via_lb_region_<CC>__<own>` and `via_lb_continent_<CONT>__<own>` copies for every own-proxy without an explicit `dialer-proxy`, paired with the existing `via_region_*` / `via_continent_*` copies. The 008 AUTO copy and the per-own-proxy skip rule remain unchanged.

**Independent Test**: With one upstream contributing JP and HK proxies and three operator-declared own-proxies (`montreal`, `montreal-spare`, `markham`, none with explicit `dialer-proxy`), fetch the merged config. Assert: for every emitted `_lb_region_<CC>` group AND every emitted `_lb_continent_<CONT>` group, every own-proxy yields exactly one fan-out copy whose name matches `via_lb_region_<CC>__<own>` or `via_lb_continent_<CONT>__<own>`, whose `dialer-proxy` field equals the corresponding `_lb_*` group, and whose other fields match the source own-proxy verbatim. Assert that 008's existing `via_region_*`, `via_continent_*`, and `via_AUTO__*` copies are still present byte-unchanged. Assert that the always-present `Proxies` selector excludes all `via_lb_*` names (008 FR-008 invariant).

### Tests for User Story 2 (write FIRST, ensure they FAIL before implementation)

- [ ] T019 [P] [US2] Add unit tests for `AppendFanoutProxies` covering the widened predicate: given a `mergedGroups` slice containing `_region_JP`, `_lb_region_JP`, `_continent_AS`, `_lb_continent_AS`, and one own-proxy without explicit `dialer-proxy`, the returned `fanout` slice contains AUTO + four per-group copies in the deterministic order `via_AUTO__<own>`, `via_region_JP__<own>`, `via_lb_region_JP__<own>`, `via_continent_AS__<own>`, `via_lb_continent_AS__<own>` — in `internal/merge/fanout_test.go`.

- [ ] T020 [P] [US2] Add unit tests asserting `via_lb_region_<CC>__<own>` and `via_lb_continent_<CONT>__<own>` copies have `dialer-proxy` set to the FULL lb group name (`_lb_region_JP`, `_lb_continent_AS`) and that all other fields are deep-cloned verbatim from the source own-proxy — in `internal/merge/fanout_test.go`.

- [ ] T021 [P] [US2] Add unit test asserting 008's per-own-proxy skip rule (FR-005) suppresses ALL `via_*` copies (including `via_lb_*`) when the source own-proxy has an explicit `dialer-proxy:` field — in `internal/merge/fanout_test.go`.

### Implementation for User Story 2

- [ ] T022 [US2] Widen the prefix predicate in `AppendFanoutProxies` from `strings.HasPrefix(name, "_region_") || strings.HasPrefix(name, "_continent_")` to additionally accept `_lb_region_` and `_lb_continent_` prefixes (single conditional with four `HasPrefix` calls per research Decision 7). No other change in the function — `stripUnderscore`, the AUTO loop, and the deep-clone semantics all flow through unchanged — in `internal/merge/fanout.go`. T019 + T020 + T021 must now pass.

**Checkpoint**: At this point, US1 + US2 are both fully functional. Fetching the served subscription returns paired groups, paired fan-out copies, and the existing 008 / 012 outputs unchanged.

---

## Phase 5: Polish & Cross-Cutting Concerns

**Purpose**: Snapshot refresh and end-to-end verification. The snapshot suite is the project's primary correctness gate (Constitution Principle II + Snapshot Stability Gate); refreshing it deliberately and reviewing the diff is the artifact reviewers will read.

- [ ] T023 Refresh integration snapshots by running `UPDATE_SNAPSHOTS=true go test ./internal/integration/...`. Visually inspect the diff and confirm it is confined to: (a) new `_lb_region_*` / `_lb_continent_*` groups in `proxy-groups:`, paired immediately after their url-test siblings; (b) new `via_lb_region_*__<own>` / `via_lb_continent_*__<own>` entries in `proxies:`, interleaved with existing `via_region_*` / `via_continent_*` entries; (c) new `_lb_*` references in the `Proxies` selector's member list, interleaved with existing url-test references; (d) NO byte-changes in any pre-existing line. Commit the refreshed snapshots — in `internal/integration/testdata/snapshots/`.

- [ ] T024 Run the full check suite (`make check`) — vet, staticcheck, all tests, snapshot-drift check. Fix any newly-flagged issues. Confirm all 014 tests pass and existing 012 / 008 / 003 / 002 snapshot tests continue to pass byte-for-byte.

- [ ] T025 Walk the operator quickstart end-to-end against a local dev pod (or a kind/minikube deployment): set a non-default `LOAD_BALANCE_INTERVAL_SECONDS=600` and `LOAD_BALANCE_STRATEGY=consistent-hashing` in the dev env block; restart; confirm `kubectl logs | grep load_balance_params` shows the override; confirm the served body's `_lb_*` groups carry the override values; confirm `via_lb_*` fan-out entries appear; confirm one of the four invalid-env-var cases from `quickstart.md` § 6 produces a clear startup error and pod failure — see `specs/014-load-balance-region-groups/quickstart.md`.

- [ ] T026 [P] Verify `CLAUDE.md` already references this feature (added during `/speckit-plan`). If a downstream feature has been added since planning, confirm the 014 entry is intact and the key-reading bullet still points at `specs/014-load-balance-region-groups/plan.md`.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No-op for this feature.
- **Foundational (Phase 2)**: T001 → T002 (test-first). T003 is independent of T001/T002 (different file). T001+T003 can run in parallel; T002 depends on T001.
- **User Story 1 (Phase 3)**: Depends on Phase 2 completion. Within US1, T010 → T011 (test-first builder); T004 → T012 (test-first helper); T005 → T013, T006 → T014 (test-first emission); T015 depends on T013 and T014; T008 → T016 (test-first ordering); T009 → T018 (test-first log). T017 depends on T011 (needs the builder). All US1 tests (T004–T010) can run in parallel.
- **User Story 2 (Phase 4)**: Depends on Phase 3 completion (T013 must exist so `_lb_*` groups appear in `mergedGroups` for the fan-out to find). T019 + T020 + T021 [P] → T022.
- **Polish (Phase 5)**: T023 depends on US1 + US2 complete. T024 depends on T023. T025 depends on T024. T026 is independent.

### User Story Dependencies

- **US1 (P1)**: Depends on Phase 2. The MVP increment — shipping this alone gives users `_lb_*` groups they can target via custom rules; fan-out copies are absent, but the core lb-group capability works.
- **US2 (P1)**: Depends on US1 (the fan-out reads the `_lb_*` groups out of `mergedGroups`). Cannot ship alone; ships as the second increment of this feature.

### Within Each User Story

- Tests (T001, T004–T010, T019–T021) MUST be written and FAIL before the corresponding implementation lands (Constitution Principle IV).
- Helpers and structs (`newLoadBalanceGroup`, `merge.LoadBalanceParams`, `Pipeline.loadBalanceParams`) before the emit functions that consume them.
- Emit functions (`AppendRegionGroups`, `AppendContinentGroups`) before the field-ordering pass and before the fan-out predicate widening (the lb-typed nodes must exist for those to operate on).
- Pipeline construction wiring (`server/app.go`) after the builder and the threading both exist.
- Snapshot refresh after all unit tests pass (T023 in Phase 5).

### Parallel Opportunities

- **Phase 2**: T001 [P] ∥ T003 [P] (different files). T002 follows T001.
- **Phase 3 tests**: T004 ∥ T005 ∥ T006 ∥ T007 ∥ T008 ∥ T009 ∥ T010 (six different files; can run as independent test-writing tasks).
- **Phase 3 implementation**: T011 ∥ T012 (different files); T013 → T014 share `region.go` (sequential within the same file); T015 follows T013+T014; T016 ∥ T017 (different files); T018 follows T017.
- **Phase 4 tests**: T019 ∥ T020 ∥ T021 (same file; running in parallel means concurrent authoring, not concurrent test invocation).
- **Phase 5**: T026 ∥ T023 (different concerns).

---

## Parallel Example: User Story 1

```bash
# Launch all US1 test-writing tasks together (different files, no inter-task deps):
Task: T004 — newLoadBalanceGroup field-shape unit tests in internal/merge/region_test.go
Task: T005 — AppendRegionGroups paired-emission unit tests in internal/merge/region_test.go
Task: T006 — AppendContinentGroups paired-emission unit tests in internal/merge/region_test.go
Task: T007 — Proxies selector membership unit tests in internal/merge/region_test.go
Task: T008 — reorderProxyGroupFields load-balance ordering unit tests in internal/output/subscription_mode_test.go
Task: T009 — startup load_balance_params log assertion in internal/server/<test>
Task: T010 — Pipeline.WithLoadBalanceParams builder unit tests in internal/merge/pipeline_test.go

# After tests fail (compile failures count), launch the parallel-isolated impl tasks:
Task: T011 — Pipeline.WithLoadBalanceParams + field in internal/merge/pipeline.go
Task: T012 — newLoadBalanceGroup helper in internal/merge/region.go
# (T013 + T014 + T015 share files / depend on T011/T012; serialize them)
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 2 (T001–T003) — `LoadBalanceParams` plumbed end-to-end into the merge package.
2. Complete Phase 3 (T004–T018) — `_lb_*` groups emitted, `Proxies` selector populated, startup log line.
3. **STOP and VALIDATE**: run `make check` and verify (a) US1 unit tests pass, (b) no 012 / 008 snapshot bytes change yet (fan-out untouched), (c) the new lb-group bytes appear in snapshot diffs.
4. Deploy/demo if ready — users can target `_lb_*` groups via custom rules even without the fan-out copies.

### Incremental Delivery

1. Complete Phase 2 + Phase 3 → US1 ready (MVP).
2. Add Phase 4 (T019–T022) → fan-out widening produces `via_lb_*` copies.
3. Refresh snapshots (T023), run `make check` (T024), operator-validate via quickstart (T025).
4. Each story adds value without breaking previous stories — US1 ships standalone, US2 layers on top.

### Single-Developer Strategy (this feature's likely staffing)

1. Read `plan.md` and `research.md` to internalize the nine design decisions.
2. Foundation: T001 + T003 in parallel terminal tabs; T002 once T001's tests are red.
3. US1: write all seven tests (T004–T010), confirm red, then implement T011 → T012 → T013 → T014 → T015 → T016 → T017 → T018 in that order. Re-run tests after each step.
4. US2: write all three tests (T019–T021), confirm red, then implement T022 (single-line predicate change).
5. Polish: T023 (snapshot refresh, careful diff inspection), T024 (`make check`), T025 (operator quickstart walkthrough), T026 (CLAUDE.md sanity check).
6. Open PR with the snapshot diff quoted in the description so reviewers know what to expect.

---

## Notes

- [P] tasks = different files, no dependencies on incomplete tasks.
- [Story] label maps each task to its user story for traceability.
- Constitution Principle IV (Test-First): tests precede implementation; tests must fail before implementation lands. Compile failures count as failures in Go.
- Constitution Principle II (Determinism): T023 snapshot refresh is the single deterministic-bytes verification artifact for this feature; the snapshot drift check in `make check` enforces it on every CI run thereafter.
- Constitution Principle V (Observability): T018 surfaces the resolved load-balance config at startup; existing 008 `fanout-emitted` log line continues to fire and its `target_group_count` will roughly double after T013/T014/T022 ship — this is the operator-visible signal that the feature is active.
- Avoid: vague tasks, same-file edit conflicts (note T013/T014/T015 all touch `internal/merge/region.go` + `pipeline.go` and must serialize), cross-story dependencies that break US1's independent shippability.
