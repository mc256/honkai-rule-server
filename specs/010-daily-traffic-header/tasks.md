---
description: "Task list for 010-daily-traffic-header"
---

# Tasks: Daily-Available Traffic in Served Subscription Header

**Input**: Design documents from `/specs/010-daily-traffic-header/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/served-subscription.changes.md

**Tests**: REQUIRED. Constitution Principle IV (Test-First, Real-Input Integration) is non-negotiable for any change to the merge layer or its output adapter. Every implementation task is preceded by a failing test.

**Organization**: Single user story (US1). Phases follow the test-first cadence: write tests → verify fail → implement → verify pass → refresh snapshots → integration test → polish.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: User story (US1) — applied to phase 3 tasks only
- All paths absolute from repo root: `/home/maverick/development/honkai-rule-server/`

## Path Conventions

- **Single Go module**: source in `internal/`, tests co-located with source files (`*_test.go`)
- **Snapshots**: `internal/integration/testdata/snapshots/`
- **Spec artifacts**: `specs/010-daily-traffic-header/`

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: None required. The Go module, test framework (`go test` + `cupaloy/v2`), linter (`staticcheck`), and CI hooks (`make check`) are already in place from feature 001. No new packages, no new dependencies.

*(No tasks in this phase.)*

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Add the pure types and helpers that US1's wiring depends on. These have no consumers until US1 lands, so they can be added in a single commit if desired, but each step is independently verifiable.

**⚠️ CRITICAL**: US1 cannot proceed until Phase 2 is complete and all foundational tests pass.

- [X] T001 Write failing unit tests for `merge.NextMidnightUTC`, `merge.ServedTrafficHeader`, and `merge.ComposeServedTrafficHeader` in `internal/merge/traffic_test.go` — covering the cases listed under "Tests" in `specs/010-daily-traffic-header/data-model.md` (`TestNextMidnightUTC_BeforeMidnight`, `_ExactlyMidnight`, `_MonthBoundary`, `_YearBoundary`, `_NonUTCInput`; `TestComposeServedTrafficHeader_TwoSourcesFR011b`, `_NoSources`, `_AllExpired`, `_AllNoExpiry`, `_MixedExpiringAndNoExpiry`, `_NegativeRemainingClamped`, `_DeterministicWithinDay`).

- [X] T002 Run `go test ./internal/merge/...` and confirm the new tests from T001 fail with "undefined: NextMidnightUTC" / "undefined: ServedTrafficHeader" / "undefined: ComposeServedTrafficHeader". Record the failing test names.

- [X] T003 Add the `merge.ServedTrafficHeader` struct (`DailyAllowanceBytes int64`, `ExpireUnix int64`) to `internal/merge/traffic.go` per `data-model.md`. Doc comment must explain the nil-pointer omission convention (FR-006) and the non-nil-with-zero-DailyAllowanceBytes case (FR-007).

- [X] T004 Add the pure helper `merge.NextMidnightUTC(now time.Time) time.Time` to `internal/merge/traffic.go` per `data-model.md`. Strictly-after semantics: a request exactly at 00:00:00 UTC produces the *following* day's midnight.

- [X] T005 Add the pure helper `merge.ComposeServedTrafficHeader(perSource map[string]fetcher.SubscriptionUserinfo, clk clock.Clock) *ServedTrafficHeader` to `internal/merge/traffic.go` per `data-model.md`. Returns nil when `len(perSource) == 0`; otherwise computes `da := ComputeDailyAllowance(perSource, clk)` and returns `&ServedTrafficHeader{DailyAllowanceBytes: da.PerDayRateBytes + da.NoExpiryRemainingBytes, ExpireUnix: NextMidnightUTC(clk.Now()).Unix()}`.

- [X] T006 Run `go test ./internal/merge/...` and confirm all tests now pass — including the existing `ComputeDailyAllowance` tests (sanity: T003–T005 must not regress that function).

**Checkpoint**: Foundation ready — US1 can begin.

---

## Phase 3: User Story 1 - Display today's available traffic in the client UI (Priority: P1) 🎯 MVP

**Goal**: Replace the served `Subscription-Userinfo` header's raw aggregates with the daily-allowance encoding (`upload=0; download=0; total=<allowance>; expire=<next-midnight-utc-unix>`), omit the header when no source contributed userinfo, and emit the new structured log fields. Body bytes do not change.

**Independent Test**: Configure two upstream subscriptions matching 001 FR-011b's worked example (source A `total=200GB / used=50GB / expire=now+30d`; source B `total=100GB / used=20GB / expire=now+5d`). Inject a fixed clock at `2026-05-01 12:00:00 UTC`. Issue a request to the served endpoint with a valid token + `Bronya/` UA. Assert the response `Subscription-Userinfo` header parses to `total − upload − download = 21 GB ± rounding` and `expire = unix(2026-05-02 00:00:00 UTC) = 1746144000`.

### Tests for User Story 1 ⚠️ (REQUIRED — Test-First per Constitution Principle IV)

- [X] T007 [US1] Write failing table-driven tests in `internal/output/subscription_mode_test.go` covering the six cases from `data-model.md`'s "Output adapter changes" table: FR-011b worked example, all-expired, all-no-expiry, mixed expiring + no-expiry, no-userinfo-at-all (header absent), source-with-used-greater-than-total (clamped). Each row asserts the exact `Subscription-Userinfo` header string (or its absence). Construct `MergedConfig` instances directly with the new `ServedTrafficHeader` field populated as the test inputs require.

- [X] T008 [US1] Write failing integration test in `internal/integration/headers_test.go` (extend the existing file). Use the existing fixture loader to build a `Pipeline` with a fixed clock and two upstream-subscription-userinfo records matching the canonical FR-011b fixture. Issue an HTTP request through the full handler stack. Assert: (a) `Subscription-Userinfo` header equals `upload=0; download=0; total=22548578304; expire=1746144000` (computed values for the canonical fixture + fixed clock), (b) the served body bytes equal the existing snapshot bytes for the same fixture (proving the body did not change), (c) the `served subscription` log line includes `served_daily_allowance_bytes=22548578304` and `served_expire_unix=1746144000`.

- [X] T009 [US1] Run `go test ./internal/output/... ./internal/integration/...` and confirm T007 + T008 fail with the expected diff (current header carries the raw aggregates, not the daily-allowance encoding).

### Implementation for User Story 1

- [X] T010 [US1] Add the `ServedTrafficHeader *ServedTrafficHeader` field to the `MergedConfig` struct in `internal/merge/pipeline.go` per `data-model.md`. Place it adjacent to the existing `AggregatedSubscriptionUserinfo` field with a doc comment cross-referencing 010 FR-001/FR-002 + 010 FR-006.

- [X] T011 [US1] Populate `ServedTrafficHeader` in `Pipeline.Build()` in `internal/merge/pipeline.go`. Insert one line immediately after the existing `aggregatedUI := AggregateSubscriptionUserinfo(userinfoPerSource)` call: `servedTrafficHeader := ComposeServedTrafficHeader(userinfoPerSource, p.clock)`. Add the field to the returned `&MergedConfig{...}` literal.

- [X] T012 [US1] Update `internal/output/subscription_mode.go::Render` to switch the `Subscription-Userinfo` header source from `merged.AggregatedSubscriptionUserinfo` (lines 137–141) to `merged.ServedTrafficHeader`. Use the recommended encoding `fmt.Sprintf("upload=0; download=0; total=%d; expire=%d", h.DailyAllowanceBytes, h.ExpireUnix)`. The omit-when-nil pattern is preserved: `if h := merged.ServedTrafficHeader; h != nil { headers.Set(...) }`.

- [X] T013 [US1] Add the two new info-level log fields to `internal/server/routes/subscription.go::Subscription`: `served_daily_allowance_bytes` and `served_expire_unix`. Use small inline helpers that return the field's value or `-1` when `merged.ServedTrafficHeader == nil`, so the log line distinguishes "header omitted" (`-1`) from "header carried zero" (`0`).

- [X] T014 [US1] Add the debug-verbosity breakdown line to `internal/server/routes/subscription.go::Subscription`. Guard with `if deps.Logger.Enabled(r.Context(), slog.LevelDebug)` to skip the second `ComputeDailyAllowance` call when debug is off. Emit `slog.Debug("served daily allowance breakdown", "per_day_rate_bytes", da.PerDayRateBytes, "no_expiry_remaining_bytes", da.NoExpiryRemainingBytes, "expired_source_flags", da.ExpiredSourceFlags)`.

- [X] T015 [US1] Run `go test ./internal/output/... ./internal/integration/...` and confirm T007 + T008 now pass. The full `go test ./...` should also pass — tests outside `internal/output/` and `internal/integration/` should be unaffected since the `MergedConfig` change is purely additive.

### Snapshot refresh

- [X] T016 [US1] Identify which committed snapshots in `internal/integration/testdata/snapshots/` include the `Subscription-Userinfo` header bytes. Run `go test ./internal/integration/... -run TestSnapshot 2>&1 | grep -i 'subscription-userinfo\|snapshot'` to surface drift. For each snapshot showing header drift, regenerate via `make snapshot-update` (or `cupaloy`'s `UPDATE_SNAPSHOTS=true` env override). Inspect each diff manually and confirm only the `Subscription-Userinfo` header line changes — body bytes must be identical. Commit only the intentional header diffs.

- [X] T017 [US1] Run `make check` (the full vet + staticcheck + tests + snapshot-drift gate). Must pass clean. If staticcheck flags the new code, fix in place; do not suppress.

**Checkpoint**: User Story 1 complete and independently testable. The served subscription endpoint now emits the daily-allowance encoding for the public header; raw aggregates remain on `/health`; body bytes unchanged; logs include the new fields.

---

## Phase 4: Polish & Cross-Cutting Concerns

**Purpose**: Verify deployment-time and operator-facing artifacts; close out the spec lifecycle. None of these affect production behavior.

- [X] T018 Run through `specs/010-daily-traffic-header/quickstart.md` against a local `make run` instance (or a deployed pod) and confirm each verification command returns the expected output. Note any divergences in a quickstart errata block; fix the quickstart, not the code, unless the divergence is a real bug.

- [X] T019 [P] Confirm the SPECKIT block in `CLAUDE.md` lists 010 as the active feature with a key-reading bullet. (Already done in this session via the plan's Agent context update step; this task is a verification-only check before merge.)

- [X] T020 [P] Confirm `.specify/feature.json` points at `specs/010-daily-traffic-header`. (Already updated in this session; verification-only check.)

- [X] T021 Verify no 008-dialer-proxy-fanout snapshots changed as a side effect. Run `git diff --stat internal/integration/testdata/snapshots/` and confirm only files containing `Subscription-Userinfo` headers are listed. If any 008-related body snapshots changed, that is a regression — investigate before merging.

- [X] T022 Update `specs/010-daily-traffic-header/checklists/requirements.md` to mark validation iteration complete (already passing per `/speckit-specify`; this is a final pre-merge re-check that nothing in the plan/data-model introduced spec-level inconsistency).

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1 (Setup)**: Empty — no work.
- **Phase 2 (Foundational)**: T001 → T002 → T003–T005 → T006. T003, T004, T005 are sequential because they all touch `internal/merge/traffic.go` (no `[P]` markers).
- **Phase 3 (US1)**: Depends on Phase 2 completion. Internal ordering:
  - Tests first: T007 → T008 → T009 (verify fail).
  - Implementation: T010 → T011 (must follow T010; same file). T012 can run after T010 (different file). T013 + T014 share `subscription.go` → sequential.
  - Verification: T015 (re-run tests).
  - Snapshots: T016 → T017.
- **Phase 4 (Polish)**: Depends on Phase 3 completion. T019, T020 are parallelizable (`[P]`); T018 + T021 + T022 are sequential reviews.

### Within User Story 1

- Tests MUST be written and FAIL before implementation (T007–T009 before T010–T015).
- `MergedConfig` field addition (T010) before `Pipeline.Build()` populates it (T011).
- Output adapter switch (T012) after `MergedConfig` field exists (T010).
- Logging tasks (T013, T014) are independent of the output adapter change — they can run anywhere after T010 — but live in the same file, so sequence them T013 then T014.
- Snapshot refresh (T016) after all implementation tasks pass tests, before `make check` (T017).

### Parallel Opportunities

- Phase 2: T003, T004, T005 cannot run in parallel (same file). T001 standalone.
- Phase 3: T012 and T013 touch *different* files (`output/subscription_mode.go` vs. `routes/subscription.go`) so they can run in parallel after T010 lands. T014 is in `routes/subscription.go`, so it sequences after T013.
- Phase 4: T019 and T020 marked `[P]`; both are quick file-existence/content verifications.

---

## Parallel Example: Phase 3 mid-implementation

Once T010 (the `MergedConfig` field addition) has landed:

```bash
# T011 and T012 still serialize through pipeline.go/subscription_mode.go interactions,
# so the natural parallelism is at the verify-tests layer:
go test -run TestServedSubscriptionUserinfo  ./internal/output/...   # T007's test cases
go test -run TestSubscriptionUserinfoHeader  ./internal/integration/... # T008's case
```

Different developers can run T013 (info log fields) and T014 (debug breakdown) on top of T010 in parallel branches if staffed; the file conflict is small and resolvable.

---

## Implementation Strategy

### MVP First (US1 = entire feature)

This feature has only one user story. The MVP is the full feature. The MVP is reached at the end of Phase 3.

1. Complete Phase 2 (foundational helpers + their unit tests).
2. Complete Phase 3 tests-first (T007–T009).
3. Land Phase 3 implementation (T010–T015).
4. Refresh snapshots (T016) and run `make check` (T017).
5. **STOP and VALIDATE**: smoke-test against a local instance per quickstart.md (T018).
6. Deploy.

### Incremental delivery

Phase 2 alone (foundational helpers + unit tests) is mergeable as a no-op refactor: the new types and helpers exist but no production code consumes them yet. This split is acceptable if the operator wants to ship the math machinery before the behavior change.

Phase 3 lands the behavior change in one commit. Splitting Phase 3 further is not recommended — the output adapter switch (T012) and the snapshot refresh (T016) must land together to keep `make check` green.

### Single-developer cadence

Sequential: T001 → T002 → T003 → T004 → T005 → T006 → T007 → T008 → T009 → T010 → T011 → T012 → T013 → T014 → T015 → T016 → T017 → T018 → T019 → T020 → T021 → T022. Total ~22 tasks, most under 10 minutes each. Realistic completion window: half a day for an experienced contributor familiar with the merge layer.

---

## Notes

- Override mode is referenced in Constitution Principle I and in feature 001's spec but is not yet implemented in the repo. The `MergedConfig.ServedTrafficHeader` field is mode-agnostic — the future override-mode adapter will read it without further work. Constitution Principle IV's "snapshots for both modes" mandate is satisfied trivially (only one mode exists today; the no-op snapshot of the other lands automatically when override mode is implemented).
- `internal/merge/` is the pure-functional transformation core (Constitution Principle I). All Phase 2 additions are pure functions / value types — no I/O, no global state, no logger dependency. The breakdown logging in T014 lives in the route handler precisely to keep `internal/merge/` clean.
- The header-bytes change in snapshots (T016) is the first deliberate served-bytes change since the `Subscription-Userinfo` header was introduced in feature 001. Reviewers should pay particular attention to the snapshot diff: it must be header-only.
- The two new info-level log fields (T013) become part of the operator's structured-log surface from this PR forward. If the operator's log aggregation has a fixed-field schema, they may need to update an alerting query or dashboard — out of scope for this feature, but worth flagging in the PR description.
