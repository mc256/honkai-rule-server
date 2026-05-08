---
description: "Task list for 012-url-test-region-groups"
---

# Tasks: URL-Test for Auto-Emitted Regional & Continent Proxy Groups

**Input**: Design documents from `/specs/012-url-test-region-groups/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/served-subscription.changes.md, quickstart.md

**Tests**: REQUIRED. Constitution Principle IV (Test-First, Real-Input Integration) is non-negotiable for any change to the merge layer or its output adapter. Every implementation task is preceded by a failing test.

**Organization**: Single user story (US1). Phases follow the test-first cadence: write tests → verify fail → implement → verify pass → refresh snapshots → polish.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: User story (US1) — applied to phase 3 tasks only
- All paths are absolute from repo root: `/home/maverick/development/honkai-rule-server/`

## Path Conventions

- **Single Go module**: source in `internal/`, tests co-located with source files (`*_test.go`)
- **Snapshots**: `internal/integration/testdata/snapshots/`
- **Spec artifacts**: `specs/012-url-test-region-groups/`

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: None required. Go module, test framework (`go test` + `cupaloy/v2`), linter (`staticcheck`), and CI gates (`make check`) are already in place from feature 001. No new packages, no new dependencies.

*(No tasks in this phase.)*

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Add the `URLTestParams` config struct + env-var loading + validation. These have no consumers until US1 lands, but US1 cannot proceed until they exist.

**⚠️ CRITICAL**: US1 cannot begin until Phase 2 is complete and all foundational tests pass.

- [X] T001 Write failing table-driven tests for `config.URLTestParams` env loading + validation in `internal/config/server_test.go`. Cover the nine cases listed in `data-model.md`'s "Tests added → server_test.go" table: all defaults (empty MapEnv), all five overridden, empty string treated as unset, non-integer interval, zero / negative integers, invalid bool, multiple violations bundled in one error message. Each test constructs a `MapEnv{}`, calls `Load(MapEnv)`, and asserts both the resolved `URLTestParams` field values and the (non-)error return.

- [X] T002 Run `go test ./internal/config/...` and confirm the new tests from T001 fail with "undefined: URLTestParams" / "field URLTestParams not found on Server" / etc. Record the failing test names.

- [X] T003 Add the `config.URLTestParams` struct to `internal/config/server.go` per `data-model.md`. Five exported fields: `URL string`, `IntervalSeconds int`, `TimeoutMS int`, `MaxFailedTimes int`, `Lazy bool`. Doc comment cites 012 FR-003 / FR-004 / FR-004a.

- [X] T004 Add a `URLTestParams URLTestParams` field to the existing `Server` struct in `internal/config/server.go`, adjacent to the existing `FallbackRuleTarget` field. Doc comment cites 012 FR-004.

- [X] T005 In `internal/config/server.go::Load`, initialize `cfg.URLTestParams` with the five defaults from FR-003 (`URL = "https://www.gstatic.com/generate_204"`, `IntervalSeconds = 10`, `TimeoutMS = 3000`, `MaxFailedTimes = 3`, `Lazy = true`). Then read each of the five env vars (`URL_TEST_URL`, `URL_TEST_INTERVAL_SECONDS`, `URL_TEST_TIMEOUT_MS`, `URL_TEST_MAX_FAILED_TIMES`, `URL_TEST_LAZY`) via `env.Getenv`. Empty / unset → leave at default; non-empty → parse and assign per the type rules in `data-model.md`. Use `strconv.Atoi` for ints and `strconv.ParseBool` for the bool. Parse failures get accumulated into a violation list (do NOT short-circuit; surface all errors at once).

- [X] T006 Add a `URLTestParams.Validate() error` method to `internal/config/server.go` per `data-model.md`. Returns nil when all five fields satisfy their constraints (`URL` non-empty after defaulting; the three integers ≥ 1; `Lazy` is a real bool — always true after `ParseBool`). Returns an error in the format documented in `data-model.md` (`URLTestParams validation failed: KEY1=VALUE1 (constraint); KEY2="VALUE2" (constraint)`). Call `Validate()` from `Load` after env-var assignment; propagate any error so `cmd/server/main.go`'s existing exit-on-Load-error logic kicks in.

- [X] T007 Run `go test ./internal/config/...` and confirm all tests now pass — including the existing `Server` tests (sanity: T003–T006 must not regress those).

**Checkpoint**: Foundation ready — US1 can begin.

---

## Phase 3: User Story 1 - Automatic failover when a regional node goes unhealthy (Priority: P1) 🎯 MVP

**Goal**: Convert auto-emitted `_region_*` and `_continent_*` proxy groups from `type: select` to `type: url-test` with the five health-check fields populated from `URLTestParams`. Always-present `Proxies` selector and operator-defined custom proxy groups are untouched.

**Independent Test**: Configure a fixture with two upstream subscriptions whose proxies map to at least two countries on the same continent. Build a `Pipeline` with a `URLTestParams` carrying the operator-confirmed defaults (FR-003). Render the served body. Assert: every `_region_*` and `_continent_*` group has `type: url-test` and the five fields in the documented order; the `Proxies` group still has `type: select`; any operator-defined custom groups (loaded from `config/custom-rules/`) flow through with their original `type` unchanged.

### Tests for User Story 1 ⚠️ (REQUIRED — Test-First per Constitution Principle IV)

- [X] T008 [US1] Write failing unit tests for `merge.newURLTestGroup` in `internal/merge/region_test.go`. Cover: default-params construction (asserts the emitted node has all 8 fields — name, type, proxies, url, interval, timeout, max-failed-times, lazy — with the FR-003 default values); overridden-params construction (asserts the values flow through verbatim); single-member group (still emits all 5 health-check fields, just with one proxy). Each test constructs a `merge.URLTestParams` value and a slice of member names, calls `newURLTestGroup`, then walks the resulting `*yaml.Node` to assert content.

- [X] T009 [US1] Write failing unit tests for `merge.AppendRegionGroups` and `merge.AppendContinentGroups` with the new `URLTestParams` argument in `internal/merge/region_test.go`. Cover: a region group emits with `type: url-test` and the 5 fields (no longer `type: select`); a `_region_UNKNOWN` group also emits with `type: url-test` (FR-001 prefix rule); a continent group emits with `type: url-test` and the 5 fields (FR-002).

- [X] T010 [US1] Write failing unit test for the extended `output.reorderProxyGroupFields` in `internal/output/subscription_mode_test.go`. Construct a YAML mapping node with the 8 keys in scrambled order (e.g., `lazy, url, name, max-failed-times, type, interval, proxies, timeout`); call `reorderProxyGroupFields`; assert the resulting key order is `name, type, proxies, url, interval, timeout, max-failed-times, lazy` per FR-007.

- [X] T011 [US1] Run `go test ./internal/merge/... ./internal/output/...` and confirm T008 + T009 + T010 fail with the expected diagnostic ("undefined: newURLTestGroup", function-signature mismatch on `AppendRegionGroups`, key not at expected position).

### Implementation for User Story 1

- [X] T012 [US1] Add the `merge.URLTestParams` type (mirror of `config.URLTestParams`) in a new file `internal/merge/url_test_params.go` (or inside `region.go` if you prefer a single-file change). Five exported fields with the same shape as `config.URLTestParams`. This keeps `internal/merge/` free of `internal/config` imports per Constitution Principle I.

- [X] T013 [US1] Add the `urlTestParams URLTestParams` field to the `Pipeline` struct in `internal/merge/pipeline.go`, adjacent to the existing `fallbackRuleTarget` field. Add the builder method `WithURLTestParams(p URLTestParams) *Pipeline` mirroring `WithFallbackRuleTarget`'s pattern (set the field, return the receiver).

- [X] T014 [US1] Add the `newURLTestGroup(name string, members []string, params URLTestParams) *yaml.Node` helper in `internal/merge/region.go` per `data-model.md`. Constructs the mapping node with all 8 fields using the existing `setMappingValue` / `setMappingMembers` helpers. The integer fields use `strconv.Itoa(...)` with `Tag: "!!int"`; the bool uses `strconv.FormatBool(...)` with `Tag: "!!bool"`.

- [X] T015 [US1] Update `AppendRegionGroups` in `internal/merge/region.go` to accept a new `params URLTestParams` argument and replace **both** in-function `&yaml.Node{Kind: yaml.MappingNode}` constructions (the per-CC group at line ~57 and the `_region_UNKNOWN` group at line ~69) with calls to `newURLTestGroup(groupName, members, params)`.

- [X] T016 [US1] Update `AppendContinentGroups` in `internal/merge/region.go` to accept a new `params URLTestParams` argument and replace the single `&yaml.Node{Kind: yaml.MappingNode}` construction (line ~168) with a call to `newURLTestGroup(groupName, membersOrdered, params)`.

- [X] T017 [US1] Update `Pipeline.Build()` in `internal/merge/pipeline.go` to pass `p.urlTestParams` to both `AppendRegionGroups(...)` and `AppendContinentGroups(...)` calls. The existing call signatures grow by one argument each.

- [X] T018 [US1] Wire the configuration in `cmd/server/main.go`: copy fields from `cfg.URLTestParams` (the `config.URLTestParams` value) into a `merge.URLTestParams` value and pass it via `pipeline.WithURLTestParams(...)` in the existing builder chain (next to `WithFallbackRuleTarget(...)`).

- [X] T019 [US1] Extend `reorderProxyGroupFields` in `internal/output/subscription_mode.go` per `data-model.md`. After the existing three `moveFieldToPosition` calls (`name → 0`, `type → 2`, `proxies → 4`), add five more for the url-test fields (`url → 6`, `interval → 8`, `timeout → 10`, `max-failed-times → 12`, `lazy → 14`). The helper is a no-op when the key is absent so calling these on non-url-test groups is safe.

- [X] T020 [US1] Add the startup log line in `internal/server/app.go` (or wherever `cfg` is consumed at startup, alongside the existing token-store / subscription-CSV resolved-config logs). One `slog.Info("url_test_params resolved", ...)` line emitting all five resolved values per FR-008. Use the existing structured-log style.

- [X] T021 [US1] Run `go test ./internal/config/... ./internal/merge/... ./internal/output/...` and confirm T008 + T009 + T010 now pass. Then run `go test ./...` and confirm no regression in any other package.

### Snapshot refresh

- [X] T022 [US1] Run `UPDATE_SNAPSHOTS=true go test ./internal/integration/...` to regenerate any snapshots that include `_region_*` / `_continent_*` group bytes. Run `git diff internal/integration/testdata/snapshots/` and verify the diff is **exclusively** within `_region_*` / `_continent_*` group blocks: every such block's `type:` line changes from `select` to `url-test` and 5 new fields appear; nothing else in the snapshot files changes. If the diff includes anything outside those group blocks, that is a regression — investigate before committing.

- [X] T023 [US1] Run `make check` (full `vet + lint + test + git-diff-clean` gate). Must pass clean. The `git diff --exit-code` step at the end will fail if any changes are still uncommitted; that's the snapshot-stability gate doing its job. If the underlying tests all pass, the gate failure is benign for this in-progress work — the actual snapshot drift will be staged when committing.

**Checkpoint**: User Story 1 complete. The served body now emits `type: url-test` for all auto-region / auto-continent groups with operator-configurable health-check fields. Manual override remains available via the always-present `Proxies` selector.

---

## Phase 4: Polish & Cross-Cutting Concerns

**Purpose**: Verify deployment-time and operator-facing artifacts; close out the spec lifecycle. None of these affect production behavior.

- [X] T024 Run through `specs/012-url-test-region-groups/quickstart.md` against a local `make run` instance (or wait for production deploy). Confirm each verification command returns the expected output. Note any divergences in a quickstart errata block; fix the quickstart, not the code, unless the divergence is a real bug.

- [X] T025 [P] Confirm the SPECKIT block in `CLAUDE.md` lists 012 as the active feature with a key-reading bullet, marks 010 as done, and notes 011 as parked. (Already updated in this session via the plan's Agent context update step; this is a verification-only check before merge.)

- [X] T026 [P] Confirm `.specify/feature.json` points at `specs/012-url-test-region-groups`. (Already updated in this session; verification-only check.)

- [X] T027 Verify no 008-dialer-proxy-fanout / 010-daily-traffic-header snapshots changed beyond the `_region_*` / `_continent_*` group bodies. Run `git diff --stat internal/integration/testdata/snapshots/` and inspect any non-region-group changes — should be none. If the always-present `Proxies` group block changed, that is a regression (FR-005 says it must stay `select`).

- [X] T028 Update `specs/012-url-test-region-groups/checklists/requirements.md` to mark validation iteration complete (already passing per `/speckit-specify`; this is a final pre-merge re-check that nothing in the plan / data-model introduced spec-level inconsistency).

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1 (Setup)**: Empty — no work.
- **Phase 2 (Foundational)**: T001 → T002 → T003–T006 (sequential because they all touch `internal/config/server.go`) → T007.
- **Phase 3 (US1)**: Depends on Phase 2 completion. Internal ordering:
  - Tests first: T008–T010 → T011 (verify fail).
  - Implementation: T012 (new file, `[P]`-eligible against the rest of merge) → T013 (pipeline.go) → T014 (region.go helper) → T015 (region.go AppendRegionGroups, depends on T014) → T016 (region.go AppendContinentGroups, depends on T014) → T017 (pipeline.go Build, depends on T015 + T016) → T018 (cmd/server/main.go, depends on T013 + T017) → T019 (subscription_mode.go, independent of T013–T018) → T020 (app.go, depends on T004).
  - Verification: T021 (re-run tests).
  - Snapshots: T022 → T023.
- **Phase 4 (Polish)**: T025 + T026 are `[P]` (different verification tasks); T024 + T027 + T028 are sequential reviews.

### Within User Story 1

- Tests MUST be written and FAIL before implementation (T008–T010 before T012–T020).
- `merge.URLTestParams` type (T012) before `Pipeline.WithURLTestParams` (T013).
- `newURLTestGroup` helper (T014) before its callers (T015 + T016).
- Both `Append*` updates (T015 + T016) before `Pipeline.Build()` change (T017).
- All in-pipeline changes (T013–T017) before main wiring (T018).
- Output formatter change (T019) is independent of the merge-side changes after T012; can run in parallel with T013–T018 if the developer is comfortable juggling.

### Parallel Opportunities

- Phase 2: T003–T006 cannot run in parallel (same file).
- Phase 3: T019 and T020 touch different files (`output/subscription_mode.go` vs. `server/app.go`) so they can run in parallel after their respective dependencies (T012, T004) land.
- Phase 4: T025 and T026 marked `[P]`.

---

## Implementation Strategy

### MVP First (US1 = entire feature)

This feature has only one user story. The MVP is the full feature, reached at the end of Phase 3.

1. Complete Phase 2 (foundational `URLTestParams` + tests).
2. Complete Phase 3 tests-first (T008–T011).
3. Land Phase 3 implementation (T012–T020).
4. Verify (T021), refresh snapshots (T022), `make check` (T023).
5. **STOP and VALIDATE**: smoke-test against a local instance per `quickstart.md` (T024).
6. Deploy.

### Incremental delivery

Phase 2 alone (`URLTestParams` + tests) is mergeable as a no-op refactor: the new struct exists but no production code reads it yet. Acceptable split if the operator wants to land the config layer separately.

Phase 3 lands the behavior change in one commit. Splitting Phase 3 further is not recommended — the output adapter switch (T019) and the snapshot refresh (T022) must land together to keep `make check` green.

### Single-developer cadence

Sequential: T001 → T002 → T003 → T004 → T005 → T006 → T007 → T008 → T009 → T010 → T011 → T012 → T013 → T014 → T015 → T016 → T017 → T018 → T019 → T020 → T021 → T022 → T023 → T024 → T025 → T026 → T027 → T028. Total 28 tasks. Realistic completion window: ~2–4 hours for an experienced contributor familiar with the merge layer.

---

## Notes

- The custom user-defined proxy groups loaded from `config/custom-rules/` flow through `Pipeline.Build()` separately and are NOT touched by `AppendRegionGroups` / `AppendContinentGroups`. T009's tests confirm the prefix rule is scoped correctly.
- The `_region_UNKNOWN` group also gets `type: url-test` per FR-001's prefix rule. Useful side effect: it's no longer a useless static dump but a self-balancing pool of "miscellaneous" proxies. Documented in `quickstart.md`.
- The 5 health-check fields' YAML emission style follows 004's block-style proxy-group convention. They render in the order specified in FR-003 (`url`, `interval`, `timeout`, `max-failed-times`, `lazy`) for readability and snapshot reviewability.
- The PR description for the eventual ship should explicitly state: "every `_region_*` and `_continent_*` group block in the served snapshots changes `type: select` to `type: url-test` and gains 5 fields; nothing else changes." Reviewers can verify the diff matches that claim.
