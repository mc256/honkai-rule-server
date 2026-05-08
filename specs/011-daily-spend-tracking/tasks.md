---
description: "Task list for 011-daily-spend-tracking"
---

# Tasks: Today's-Spend Tracking in Served Subscription-Userinfo

**Input**: Design documents from `/specs/011-daily-spend-tracking/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/served-subscription.changes.md, quickstart.md

**Tests**: REQUIRED. Constitution Principle IV (Test-First, Real-Input Integration) is non-negotiable for any change to the merge layer or its output adapter. Every implementation task is preceded by a failing test.

**Organization**: Single user story (US1). Phases follow the test-first cadence: foundational `dailyspend` package (test → impl) → US1 wiring + output (test → impl) → snapshot refresh → polish.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: User story (US1) — applied to phase 3 tasks only
- All paths absolute from repo root: `/home/maverick/development/honkai-rule-server/`

## Path Conventions

- **Single Go module**: source in `internal/`, tests co-located with source files (`*_test.go`)
- **New package**: `internal/dailyspend/`
- **Snapshots**: `internal/integration/testdata/snapshots/`
- **Spec artifacts**: `specs/011-daily-spend-tracking/`

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: None required. Go module, test framework, linter, CI gates already in place from feature 001. No new packages beyond the new `internal/dailyspend/` (created as part of Phase 2). No new dependencies.

*(No tasks in this phase.)*

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Add the `internal/dailyspend/` package — the new `Snapshot` type, both snapshotter implementations (file-backed + in-memory), and the pure helpers (`IsRolloverNeeded`, `ComputeUsedToday`, `SplitUsedToday`, `CaptureSnapshot`). These have no consumers until US1 lands, but US1 cannot proceed until they exist.

**⚠️ CRITICAL**: US1 cannot begin until Phase 2 is complete and all foundational tests pass.

- [X] T001 Create the new package directory `internal/dailyspend/`. Add an empty `package dailyspend` declaration in `internal/dailyspend/snapshot.go` so subsequent test edits compile.

- [X] T002 Write failing tests in `internal/dailyspend/snapshot_test.go` for the `Snapshot` JSON round-trip + atomic-rename: `TestSnapshot_RoundTrip` (Save then Load returns identical), `TestSnapshot_AtomicRename` (mid-write crash leaves the original file intact — simulate by removing the rename step, assert the .tmp exists but the original is unchanged), `TestSnapshot_LoadMissing` (returns (nil, nil) when file doesn't exist), `TestSnapshot_LoadCorrupted` (returns (nil, nil) + INFO-level log line on JSON parse error per FR-007), `TestSnapshot_LoadInvalidLocalDate` (returns (nil, nil) + log when LocalDate doesn't parse as YYYY-MM-DD).

- [X] T003 Write failing tests in `internal/dailyspend/snapshot_test.go` for the rollover predicate: `TestIsRolloverNeeded_NilSnapshot` (true), `TestIsRolloverNeeded_SameDay` (false when snapshot.LocalDate matches today in loc), `TestIsRolloverNeeded_NextDay` (true after clock advance past local midnight), `TestIsRolloverNeeded_DSTSpringForward` (exactly one rollover across the 23-hour day in America/Toronto), `TestIsRolloverNeeded_DSTFallBack` (exactly one rollover across the 25-hour day), `TestIsRolloverNeeded_YearBoundary` (rollover fires at 00:00 Jan 1 local).

- [X] T004 Write failing tests in `internal/dailyspend/snapshot_test.go` for `ComputeUsedToday`: `TestComputeUsedToday_TwoSources` (sum of clamped deltas), `TestComputeUsedToday_NewSourceNoBaseline` (new source contributes 0 per FR-008), `TestComputeUsedToday_ProviderReset` (current < baseline contributes 0 — clamped per FR-008).

- [X] T005 Write failing tests in `internal/dailyspend/snapshot_test.go` for `SplitUsedToday`: `TestSplitUsedToday_AllUpload` (source with ratio 1.0 puts all spend into upload), `TestSplitUsedToday_AllDownload` (ratio 0.0 puts all into download), `TestSplitUsedToday_Mixed` (weighted-average across sources; sum equals usedToday ±1 byte).

- [X] T006 Write failing tests in `internal/dailyspend/snapshot_test.go` for `CaptureSnapshot`: `TestCaptureSnapshot_TwoSourcesFR011b` (canonical FR-011b fixture produces expected baselines + ratios + LocalDate), `TestCaptureSnapshot_ZeroUploadRatio` (source with `upload+download==0` gets ratio 0.0 per R3 in research.md).

- [X] T007 Write failing tests in `internal/dailyspend/snapshot_test.go` for `MapSnapshotter`: `TestMapSnapshotter_SaveLoad` (round-trip via the test impl). The interface satisfaction is verified at the use-site (Pipeline) — no separate compile-time assertion needed because Go uses structural typing.

- [X] T008 Run `go test ./internal/dailyspend/...` and confirm all T002–T007 tests fail with the expected diagnostic ("undefined: Snapshot", "undefined: NewFileSnapshotter", etc.). Record the failing test names.

- [X] T009 Add the `dailyspend.Snapshot` struct to `internal/dailyspend/snapshot.go` per `data-model.md`. Five fields with the JSON tags exactly as specified: `LocalDate`, `SnapshotUnix`, `AllowanceTodayBytes`, `Baselines`, `BaselineUploadRatios`.

- [X] T010 Add the `dailyspend.FileSnapshotter` type + `NewFileSnapshotter` + `Load` + `Save` methods to `internal/dailyspend/snapshot.go`. `Load` returns `(nil, nil)` on missing/corrupt (logged INFO); `(nil, err)` on real I/O failure. `Save` writes to `path + ".tmp"`, calls `(*os.File).Sync()`, closes, then `os.Rename` to the final path. Validation in `Load`: `LocalDate` parses as YYYY-MM-DD, `Baselines` and `BaselineUploadRatios` have matching key sets, `AllowanceTodayBytes >= 0`, ratios in `[0.0, 1.0]`.

- [X] T011 Add the `dailyspend.MapSnapshotter` type + `NewMapSnapshotter` + `Load`/`Save`/`Current` methods to `internal/dailyspend/snapshot.go`. Wraps `*Snapshot` with `sync.Mutex`; `Save` deep-copies the input to avoid aliasing.

- [X] T012 Add the pure helpers `IsRolloverNeeded`, `ComputeUsedToday`, `SplitUsedToday`, `CaptureSnapshot` to `internal/dailyspend/snapshot.go` per the signatures in `data-model.md`. `CaptureSnapshot` populates `BaselineUploadRatios` per R3: `ratio = upload / (upload + download)` when denominator > 0; `0.0` when 0.

- [X] T013 Run `go test ./internal/dailyspend/...` and confirm all tests now pass.

**Checkpoint**: Foundation ready — US1 can begin.

---

## Phase 3: User Story 1 - Today's spend visible in the client UI as the user consumes traffic (Priority: P1) 🎯 MVP

**Goal**: Switch the served `Subscription-Userinfo` header from 010's static-allowance encoding to the spend-tracking encoding (`upload+download = used_today`, `total = allowance + used_today`, `expire = next 00:00 America/Toronto`). The bar in the client UI fills as the user consumes; pod restart preserves spend via the PVC snapshot; DST and provider-counter-resets handled correctly.

**Independent Test**: Build a `Pipeline` with a `MapSnapshotter` initialized with known baselines + a fixed clock 12:00 of the snapshot's local date in America/Toronto, plus per-source userinfo whose `upload+download` exceeds the baselines by N bytes. Render the served body. Assert the response `Subscription-Userinfo` header parses to `upload + download = N ± rounding`, `total = baselines.allowance + N`, `expire = unix(next 00:00 America/Toronto)`. Advance the clock past the next local midnight, render again. Assert the snapshotter's `Current()` returns a fresh snapshot (new LocalDate, new baselines from current upstream) and the served `upload + download` resets toward 0.

### Tests for User Story 1 ⚠️ (REQUIRED — Test-First per Constitution Principle IV)

- [X] T014 [US1] Write failing table-driven tests in `internal/merge/traffic_test.go` for `ComposeServedTrafficHeaderWithSpend`. Cover the 8 acceptance scenarios from `spec.md`: (1) midnight first-of-day request → used ≈ 0; (2) mid-day after N bytes → used == N, total == allowance + N; (3) overspend → total - upload - download may be negative (allowed); (4) crossing local midnight regenerates snapshot (returns a non-nil newSnapshot pointer different from input); (5) pod restart simulation → snapshot loaded carries used forward; (6) provider reset → clamped to 0 per FR-008; (7) DST transition → exactly one rollover; (8) first boot → initialize from current, used = 0.

- [X] T015 [US1] Extend the existing `merge.ServedTrafficHeader` struct in `internal/merge/traffic.go` with the six new fields per `data-model.md`: `UsedTodayBytes int64`, `TotalBytes int64`, `UploadBytes int64`, `DownloadBytes int64`, `SnapshotLocalDate string`, `RolloverFired bool`. Doc comments cite 011 FR-001/FR-002/FR-011. Existing `DailyAllowanceBytes` and `ExpireUnix` fields stay.

- [X] T016 [US1] Run `go test ./internal/merge/...` and confirm T014's tests fail with "undefined: ComposeServedTrafficHeaderWithSpend" or signature-mismatch on the new struct fields.

### Implementation for User Story 1

- [X] T017 [US1] Add the new helper `merge.ComposeServedTrafficHeaderWithSpend(perSource, clk, snapshot, loc) (*ServedTrafficHeader, *dailyspend.Snapshot)` to `internal/merge/traffic.go` per `data-model.md`. Returns `(nil, nil)` when no source contributed userinfo (010 FR-006 preserved). When `IsRolloverNeeded` is true, captures via `CaptureSnapshot`, computes `used_today` and the upload/download split off the new snapshot, returns `(header, newSnapshot)` so the caller persists. When current, computes off the existing snapshot and returns `(header, snapshot)`. Sets `RolloverFired = true` only when the captured snapshot is new.

- [X] T018 [US1] Update the existing `merge.ComposeServedTrafficHeader` in `internal/merge/traffic.go` to delegate: `h, _ := ComposeServedTrafficHeaderWithSpend(perSource, clk, nil, time.UTC); return h`. Backward-compatible — same external signature, same behavior for callers that don't need spend tracking.

- [X] T019 [US1] Add the `snapshotter` and `budgetLocation` fields to `Pipeline` in `internal/merge/pipeline.go`. The `snapshotter` field is typed as the structural interface `interface { Load() (*dailyspend.Snapshot, error); Save(*dailyspend.Snapshot) error }` per R1 in `research.md`. Add builder methods `WithSnapshotter(s ...) *Pipeline` and `WithBudgetLocation(loc *time.Location) *Pipeline`, mirroring the existing `WithFallbackRuleTarget` pattern.

- [X] T020 [US1] Update `Pipeline.Build()` in `internal/merge/pipeline.go` to use the new spend-aware path when a snapshotter is configured. Per the snippet in `data-model.md`: load snapshot, call `ComposeServedTrafficHeaderWithSpend`, save the new snapshot if returned different, fall back to plain `ComposeServedTrafficHeader` when `snapshotter == nil`. `Save` errors are logged WARN but do not fail the request (the in-memory header is already correct for this request).

- [X] T021 [US1] Update `internal/output/subscription_mode.go::Render` to use the new fields when emitting the `Subscription-Userinfo` header. Replace the 010 line `fmt.Sprintf("upload=0; download=0; total=%d; expire=%d", h.DailyAllowanceBytes, h.ExpireUnix)` with `fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d", h.UploadBytes, h.DownloadBytes, h.TotalBytes, h.ExpireUnix)`. The omit-when-nil pattern (`if h := merged.ServedTrafficHeader; h != nil`) is preserved.

- [X] T022 [US1] Add the two new env vars to `internal/config/server.go`: `TODAY_ZERO_PATH` (default `/data/today-zero.json`, no validation), `DAILY_BUDGET_TIMEZONE` (default `America/Toronto`, validated via `time.LoadLocation` at Load time per R7). On invalid timezone return a structured error naming the offending value. Add corresponding `TodayZeroPath string`, `DailyBudgetTimezone string`, `BudgetLocation *time.Location` fields to `ServerConfig`.

- [X] T023 [US1] Add tests in `internal/config/server_test.go` for the two new env vars: defaults (no env set), TODAY_ZERO_PATH override, DAILY_BUDGET_TIMEZONE override (valid IANA name), DAILY_BUDGET_TIMEZONE invalid (e.g., "Mars/Olympus") returns error mentioning the env var.

- [X] T024 [US1] Wire the snapshotter + budget location into the production pipeline in `cmd/server/main.go`. Extend the existing builder chain with `.WithSnapshotter(dailyspend.NewFileSnapshotter(cfg.TodayZeroPath)).WithBudgetLocation(cfg.BudgetLocation)`. Add the import for `internal/dailyspend`.

- [X] T025 [US1] Add the four new info-level log fields per FR-011 to `internal/server/routes/subscription.go::Subscription`. Extend the existing fields slice with: `served_used_today_bytes`, `served_total_bytes`, `snapshot_local_date`, `rollover_fired`. All come from `merged.ServedTrafficHeader`'s new fields; nil-header → emit `-1`/`""`/`false` sentinels (consistent with the 010 sentinel pattern).

- [X] T026 [US1] Run `go test ./internal/merge/... ./internal/output/... ./internal/config/...` and confirm T014 + T023 tests now pass. Then run `go test ./...` and identify any other tests that broke (likely the integration tests that snapshot the served header — addressed in T027).

### Integration test + snapshot refresh

- [X] T027 [US1] Update `internal/integration/cluster_test.go::newTestClusterWithOpts` to inject a `MapSnapshotter` with a sensible initial snapshot. Construct an initial snapshot whose `LocalDate` matches the test's fixed clock day (in America/Toronto) and whose baselines reflect the per-source userinfo expected by the existing fixtures. The integration tests then exercise the full spend-tracking path.

- [X] T028 [US1] Run `UPDATE_SNAPSHOTS=true go test ./internal/integration/...` to regenerate `subscription-userinfo.snap.txt` with the new encoding. Inspect `git diff internal/integration/testdata/snapshots/subscription-userinfo.snap.txt` and verify: `upload=`/`download=` change from 0 to small per-day-spend values; `total=` becomes `allowance + usedToday`; `expire=` shifts from 00:00 UTC to 00:00 America/Toronto on the test fixture's date. The body snapshot (`served-config.snap.yaml`) MUST NOT change — verify with `git diff --stat internal/integration/testdata/snapshots/`.

- [X] T029 [US1] Run `make check` underlying gates (vet + staticcheck + tests + race). Must pass clean. The `git diff --exit-code` step at the end will surface any unstaged work; that's the snapshot-stability gate doing its job.

**Checkpoint**: User Story 1 complete. The served subscription endpoint now emits the spend-tracking encoding; the bar in the Mihomo / Clash client UI fills through the day; pod restart preserves spend via the PVC snapshot; DST + provider-resets handled correctly.

---

## Phase 4: Polish & Cross-Cutting Concerns

**Purpose**: Verify deployment-time and operator-facing artifacts; close out the spec lifecycle. None of these affect production behavior.

- [X] T030 Run through `specs/011-daily-spend-tracking/quickstart.md` against a local `make run` instance (or wait for production deploy + use `kubectl exec`-style helper-pod inspection). Confirm each verification command returns the expected output. Note any divergences in a quickstart errata block; fix the quickstart, not the code, unless the divergence is a real bug.

- [X] T031 [P] Confirm the SPECKIT block in `CLAUDE.md` lists 011 as the active feature with a key-reading bullet, marks 012 as done. (Already updated in this session via the plan's Agent context update step; this is a verification-only check before merge.)

- [X] T032 [P] Confirm `.specify/feature.json` points at `specs/011-daily-spend-tracking`. (Already updated; verification-only.)

- [X] T033 Verify no 008/010/012 snapshots changed beyond the deliberate header diff in `subscription-userinfo.snap.txt`. Run `git diff --stat internal/integration/testdata/snapshots/` and inspect any non-header changes — should be none. If `served-config.snap.yaml` (body) changed, that's a regression — investigate before merging.

- [X] T034 Update `specs/011-daily-spend-tracking/checklists/requirements.md` to mark validation iteration complete (was already passing per `/speckit-specify`; this is a final pre-merge re-check that nothing in the plan/data-model introduced spec-level inconsistency). The "two minor open items" noted there were resolved in research.md §R3 and §R5 — note that resolution in the checklist.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1 (Setup)**: Empty — no work.
- **Phase 2 (Foundational)**: T001 → T002–T007 (test files; can ALL run in any order — same file though, so write them sequentially in practice) → T008 (verify fail) → T009–T012 (impl; sequential because they all touch the same `snapshot.go` file) → T013 (verify pass).
- **Phase 3 (US1)**: Depends on Phase 2 completion.
  - Tests first: T014 → T015 (struct extension) → T016 (verify fail).
  - Implementation order: T017 (compose helper) → T018 (010 wrapper) → T019 (Pipeline fields/builders) → T020 (Build wiring) → T021 (output adapter) → T022 (config) → T023 (config tests) → T024 (main.go wiring) → T025 (route logs).
  - Verification: T026 (re-run unit tests).
  - Integration + snapshot: T027 → T028 → T029.
- **Phase 4 (Polish)**: Depends on Phase 3 completion. T031 + T032 are `[P]` (quick existence checks); T030, T033, T034 are sequential reviews.

### Within User Story 1

- Struct extension (T015) before the helper that populates it (T017).
- Compose helper (T017) before its wrapper (T018) and before Pipeline.Build wiring (T020).
- Pipeline fields/builders (T019) before main.go wiring (T024).
- Output adapter change (T021) is independent of Pipeline-side changes after T015 — could run in parallel branch with T017–T020 if a developer wanted, but file conflicts suggest sequential is safer.
- Config (T022) and main.go wiring (T024) are independent after T019 lands; main.go also needs T022 (for `cfg.BudgetLocation`).
- Route logs (T025) only need T015's struct extension to compile.
- Integration test (T027) and snapshot refresh (T028) need everything T014–T026 first.

### Parallel Opportunities

- Phase 2: T009–T012 cannot run in parallel (same file). T002–T007 are also same-file but can be drafted in any order — practical to write top-to-bottom.
- Phase 3: T021 (output) and T025 (route logs) touch different files (`output/subscription_mode.go` vs `routes/subscription.go`) so they can run in parallel after their respective dependencies (T015) land.
- Phase 4: T031 + T032 marked `[P]`.

---

## Implementation Strategy

### MVP First (US1 = entire feature)

This feature has only one user story. The MVP is the full feature, reached at the end of Phase 3.

1. Complete Phase 2 (foundational `dailyspend` package + tests).
2. Complete Phase 3 tests-first (T014–T016).
3. Land Phase 3 implementation (T017–T025).
4. Verify (T026), refresh integration snapshots (T027–T028), `make check` (T029).
5. **STOP and VALIDATE**: smoke-test against a local instance per `quickstart.md` (T030).
6. Deploy.

### Incremental delivery

Phase 2 alone (the new `dailyspend` package + tests) is mergeable as a no-op refactor: the package exists but no production code consumes it. Acceptable split if you want to land the foundation separately for review.

Phase 3 lands the behavior change in one commit. Splitting Phase 3 further is not recommended — the output adapter switch (T021) and the snapshot refresh (T028) must land together to keep `make check` green.

### Single-developer cadence

Sequential: T001 → T002 → ... → T034 (skipping the empty Phase 1). Total 34 tasks. Realistic completion window: ~3–5 hours for an experienced contributor familiar with the merge layer (the `dailyspend` package plus the wiring is the bulk of the work; the wiring itself is ~30 lines spread across 6 files).

---

## Notes

- The new `internal/dailyspend/` package is self-contained: no imports from other internal packages except the stdlib + `internal/fetcher` (for `SubscriptionUserinfo`) and `time`. Keeps the dependency graph clean.
- The structural-typed `Snapshotter` interface inside `merge.Pipeline` (T019) avoids a `merge → dailyspend` import cycle. The merge package DOES import `internal/dailyspend` for the `*dailyspend.Snapshot` type, but that's a one-way import — dailyspend never imports merge.
- Snapshot writes during `Pipeline.Build` are best-effort: a `Save` failure is logged WARN but doesn't fail the request (the served header for THIS request is already correct from the in-memory snapshot). The next request will retry the save naturally.
- `ComposeServedTrafficHeader` (the 010 helper) becoming a thin wrapper means existing callers (currently only `Pipeline.Build` itself, when `snapshotter == nil`) continue to work without modification. Snapshot tests that don't inject a snapshotter still get the 010 behavior — which keeps Phase 2 mergeable as a no-op refactor.
- The integration snapshot `subscription-userinfo.snap.txt` will change. Reviewers should expect: `upload=`/`download=` non-zero, `total=` larger than 010's, `expire=` Toronto-midnight (4 hours later than 010's UTC-midnight on the same date).
- The body snapshot `served-config.snap.yaml` will NOT change. T033 explicitly verifies this.
- Per Constitution Principle IV's "snapshots for both modes" mandate: override mode adapter doesn't yet exist; the future override adapter will inherit the new spend-aware composition automatically once it consumes `MergedConfig.ServedTrafficHeader`.
- The complexity-tracking deviation (snapshot corruption recoverable, not loud-fail) is implemented in T010's `Load` returning `(nil, nil)` on parse error. The recovery is observable via the INFO log line emitted alongside.
