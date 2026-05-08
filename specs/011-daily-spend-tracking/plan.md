# Implementation Plan: Today's-Spend Tracking in Served Subscription-Userinfo

**Branch**: `011-daily-spend-tracking` | **Date**: 2026-05-02 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/011-daily-spend-tracking/spec.md`

## Summary

Replace the 010 served-`Subscription-Userinfo` encoding (`upload=0; download=0; total=daily_allowance; expire=next_midnight_UTC`) with a within-day-spend tracker so the client UI's usage bar fills as the user consumes traffic. The new encoding splits today's spend across `upload`/`download` mirroring the upstream ratio captured at midnight, sets `total = allowance_today + used_today` (so `remaining = allowance_today − used_today`), and points `expire` at the next 00:00 in **America/Toronto local time** (not UTC) so the daily boundary aligns with the operator's day.

State persists across pod restarts in one new file `/data/today-zero.json` on the existing PVC. A new `internal/dailyspend/` package owns reading/writing the snapshot and detecting midnight rollover (lazy, request-driven). The merge layer's `ComposeServedTrafficHeader` (010) is extended with a `ComposeServedTrafficHeaderWithSpend` that takes an additional snapshot argument; the existing 010 helper stays as a thin wrapper for backward compatibility (and as the test-friendly entry point for cases that don't need spend tracking). The route handler at `internal/server/routes/subscription.go` reads/refreshes the snapshot via the new package, then passes it into pipeline composition.

The merge transformation core stays pure (Constitution Principle I): file I/O for the snapshot lives in the new `internal/dailyspend/` package, behind an interface the merge layer accepts as a value. Snapshot tests inject a stub snapshot reader alongside the existing `clock.Clock` injection so determinism (Principle II) holds.

## Technical Context

**Language/Version**: Go 1.25 toolchain (declared 1.22+) — unchanged.
**Primary Dependencies**: existing — no new Go deps. Standard `time.LoadLocation("America/Toronto")` for DST-aware local-day math; standard `os.Rename` for atomic file replace.
**Storage**: One JSON file on the existing PVC at `/data/today-zero.json`. ~200 bytes per snapshot. Atomic write via `.tmp` + `os.Rename` (POSIX atomic on the same filesystem).
**Testing**: existing — `go test`, `bradleyjkemp/cupaloy/v2` snapshots; new unit tests for snapshot read/write/rollover and for the ratio-aware compose helper. Integration tests get a stub `Snapshotter` injected to avoid touching the real PVC during tests.
**Target Platform**: same as the rest of the server (Linux, Kubernetes); the IANA timezone database must be available — Go's stdlib `time` includes a fallback database, so `time.LoadLocation("America/Toronto")` works even when the container image doesn't ship `/usr/share/zoneinfo`. Verified by 011's edge-case-handling FR-010.
**Project Type**: Single Go module.
**Performance Goals**: snapshot read = 1 file read (~ms) per served request; cache the parsed snapshot in memory after first read, only re-read after rollover. Snapshot write = 1 atomic rename per local-day (rare). No measurable hot-path impact at the project's request volume (tens of requests / day).
**Constraints**: must be deterministic given fixed inputs (FR-013); snapshot file corruption MUST recover gracefully (FR-007 — log warn, reinitialize, continue serving). The America/Toronto timezone MUST be used (not UTC, not server local) per FR-010.
**Scale/Scope**: same handful of upstream subscriptions; the snapshot has one entry per source (~10 sources max in practice).

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Justification |
|-----------|--------|---------------|
| **I. Unified Transformation Core** | PASS | The snapshot reader/writer lives in a new `internal/dailyspend/` package outside the merge core. The merge core (`internal/merge/`) accepts the snapshot as a value via a new pure helper `ComposeServedTrafficHeaderWithSpend(perSource, clk, snapshot, loc)`. Both subscription mode and any future override-mode adapter consume the same `MergedConfig.ServedTrafficHeader`, populated identically. No fork. |
| **II. Deterministic Transformation** | PASS | Served header = pure function of (per-source userinfo, clock, snapshot, location). Snapshot is injectable via interface; tests use a stub. The clock is the existing `clock.Clock`. The America/Toronto location is loaded once at startup and held immutable. Snapshot tests assert byte-stability against fixed (clock, snapshot) pairs. |
| **III. CSV Rules — Strict Schema, Loud Failure** | PASS — applied to snapshot file with documented deviation | Snapshot JSON parse errors are recoverable (FR-007: log INFO, reinitialize from current state). This deviates from "loud-fail-abort" — justified in Complexity Tracking below: failing-closed on snapshot corruption would degrade a useful (but not safety-critical) display feature into a hard outage. The merge core's CSV strictness is unchanged. |
| **IV. Test-First, Real-Input Integration (NON-NEGOTIABLE)** | PASS | Phase plan: write failing unit tests for snapshot read/write/rollover/clamp, write failing tests for `ComposeServedTrafficHeaderWithSpend`, then implement. Integration test simulates clock advancing across a local-midnight boundary and asserts the served header transitions correctly. The existing snapshot suite picks up any deterministic body diff (expected: none — body bytes don't change; only header bytes do). |
| **V. Observable Routing & Source-Merge Decisions** | PASS — extended | FR-011 adds four new structured log fields per served request: `served_used_today_bytes`, `served_total_bytes`, `snapshot_local_date`, `rollover_fired`. No new credential surface. |
| **Routing — Corporate isolation** | PASS — N/A | No routing change. |
| **Routing — multi-subscription collision resolution** | PASS — N/A | No collision change. |
| **Routing — fetch failure modes** | PASS — preserved | Bootstrap gate at `subscription.go` is unchanged; this feature only changes header-composition logic for 200 responses. |
| **Security — Secrets boundary** | PASS — N/A | No new credential / token. The snapshot file holds counter values, not secrets. |
| **Security — Sanitized output** | PASS — preserved | Header values are derived from already-sanitized per-source userinfo. |
| **Security — CSV is reviewable, not secret** | PASS — N/A | No CSV change. |
| **Snapshot stability gate** | PASS | Snapshot bytes for the served `Subscription-Userinfo` header change only when the test's stub `Snapshotter` provides realistic data. Default zero-value snapshot (no spend yet) keeps existing test fixtures' header bytes effectively unchanged from 010 (allowance unchanged; used = 0; expire shifts from UTC midnight to Toronto midnight — that's a deliberate one-time snapshot diff). The integration `served-config.snap.yaml` snapshot (body bytes) is not affected. |
| **Diff-reviewable changes** | PASS | One PR. Files affected listed in Project Structure. |
| **Both modes covered, every change** | PASS — scope-limited | Override-mode adapter does not yet exist. The `MergedConfig.ServedTrafficHeader` field is mode-agnostic; the future override adapter will inherit the new spend-aware composition automatically once it consumes the same field. |
| **Simplicity bias** | PASS | One new tiny package (`internal/dailyspend/`, ~150 LOC including tests). One new pure helper in `internal/merge/`. Two new env vars (snapshot path defaulting to `/data/today-zero.json`; budget timezone defaulting to `America/Toronto`). One new wiring step per builder method. No new abstractions, frameworks, or plugin layers. |

### Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| Snapshot file corruption is recoverable, not loud-fail-abort (deviates from Constitution Principle III applied to the snapshot JSON file format). | A corrupted snapshot is a recoverable degradation: the operator's daily-spend display becomes briefly stuck (used_today resets to 0 until next midnight), but the served subscription itself stays valid and the rules / routing continue to work. Failing closed (return 503 until operator manually fixes the file) would over-protect a usability feature at the cost of breaking the user's connectivity. | Loud-fail on JSON parse error: rejected because (a) the snapshot is a derived persistent cache, not a source of truth — the upstream counters are; (b) the operator has no way to "fix" a corrupted snapshot meaningfully (just delete and let it regenerate), so the loud-fail just adds latency before the inevitable regeneration; (c) corruption causes are limited to disk-half-full or kernel-crash mid-fsync, both of which already produce louder operator signals than this code path. The recovery path is documented and observable (FR-007 logs INFO with the recovery action). |

## Project Structure

### Documentation (this feature)

```text
specs/011-daily-spend-tracking/
├── plan.md                                    # This file
├── research.md                                # Phase 0 — design decisions
├── data-model.md                              # Phase 1 — Snapshot type, file format, helpers
├── contracts/
│   └── served-subscription.changes.md         # Phase 1 — delta vs 010 / 001 served format
├── quickstart.md                              # Phase 1 — operator verification + troubleshooting
├── checklists/
│   └── requirements.md                        # already created by /speckit-specify
└── tasks.md                                   # Phase 2 output (/speckit-tasks — NOT created here)
```

### Source Code (repository root)

```text
honkai-rule-server/
├── internal/
│   ├── dailyspend/                            # NEW package
│   │   ├── snapshot.go                        # NEW — Snapshot type + Read + Write (atomic) + Rollover detector + IsRolloverNeeded + ComputeUsedToday + SplitUsedToday
│   │   └── snapshot_test.go                   # NEW — read/write round-trip, atomic rename, corruption recovery, rollover edge cases (DST, year boundary, never-seen-source, clock-skew-backward)
│   ├── config/
│   │   ├── server.go                          # MODIFY — add TodayZeroPath (default "/data/today-zero.json") + DailyBudgetTimezone (default "America/Toronto")
│   │   └── server_test.go                     # MODIFY — env-load tests for the two new fields + validation (timezone must parse via time.LoadLocation)
│   ├── merge/
│   │   ├── traffic.go                         # MODIFY — add ComposeServedTrafficHeaderWithSpend; existing ComposeServedTrafficHeader stays as a thin wrapper
│   │   ├── pipeline.go                        # MODIFY — Pipeline gains snapshotter + budgetLocation fields + WithSnapshotter / WithBudgetLocation builders; Build() reads snapshot, computes spend-aware header, persists snapshot if rollover fired
│   │   └── traffic_test.go                    # MODIFY — table tests for ComposeServedTrafficHeaderWithSpend covering each acceptance scenario from spec.md (8 scenarios)
│   ├── server/routes/
│   │   └── subscription.go                    # MODIFY — add the four new info-log fields per FR-011 (served_used_today_bytes, served_total_bytes, snapshot_local_date, rollover_fired)
│   └── integration/
│       ├── cluster_test.go                    # MODIFY — testCluster injects a stub MapSnapshotter so determinism holds
│       └── testdata/snapshots/                # MODIFY (small) — subscription-userinfo.snap.txt regenerated to reflect new encoding (used > 0 after stub injection of realistic spend); body snapshots unchanged
└── specs/011-daily-spend-tracking/            # documentation tree above
```

**Structure Decision**: Single project. New package `internal/dailyspend/` (≤150 LOC) keeps file I/O outside the pure-merge boundary. The merge layer's `Pipeline` grows two builder-style methods (`WithSnapshotter`, `WithBudgetLocation`); production wiring in `cmd/server/main.go` constructs a real `dailyspend.FileSnapshotter` pointing at `cfg.TodayZeroPath`. Tests inject a stub.

## Phase 0: Outline & Research

The spec leaves no `[NEEDS CLARIFICATION]` markers. The Phase 0 deliverable documents seven narrow design decisions. The two open items noted in `checklists/requirements.md` (upload-ratio when source has zero historical upload; harder atomicity guarantees) are resolved here.

1. **Snapshotter interface vs. concrete file ops**: Define a small interface in `internal/merge/` (where it's consumed): `type Snapshotter interface { Load() (*dailyspend.Snapshot, error); Save(*dailyspend.Snapshot) error }`. Production: `dailyspend.FileSnapshotter` reading/writing `/data/today-zero.json` with `os.Rename` atomicity. Tests: `dailyspend.MapSnapshotter` holding state in memory. Rationale: keeps the merge layer free of file I/O (Principle I); makes integration tests hermetic; matches the existing `clock.Clock` injection pattern.

   Avoid a `merge → dailyspend` import cycle by either (a) defining the interface in a shared internal package, or (b) leveraging Go's structural typing — `merge.Pipeline.snapshotter` is typed as `interface { Load() (*dailyspend.Snapshot, error); Save(*dailyspend.Snapshot) error }` which `dailyspend.FileSnapshotter` and `dailyspend.MapSnapshotter` both satisfy. Decision: use (b) — structural typing keeps the type signature self-documenting at the use-site without a third package.

2. **Lazy rollover trigger**: The first served request after the local-day boundary triggers rollover. Implementation: `Pipeline.Build()` calls `snapshotter.Load()`, computes `IsRolloverNeeded(snapshot, clk.Now(), budgetLocation)`. If true, captures fresh state from current per-source userinfo + `ComputeDailyAllowance` at `clk.Now()`, calls `snapshotter.Save()`, returns the new snapshot. Otherwise returns the loaded snapshot as-is. Rationale: no background timer; lazy rollover scales to zero requests-per-day cleanly; cost is one file read per served request (negligible at request volume).

3. **Upload-ratio with zero historical upload** (resolves checklist open item): When a source's captured baseline has `upload + download == 0` (brand-new source with no consumption yet), set `BaselineUploadRatio = 0.0` (everything attributed to download). When `upload + download > 0`, compute `ratio = upload / (upload + download)`. Edge case: a source whose `upload == 0` but `download > 0` (typical for clients that mostly consume) gets ratio 0, which means served `upload = 0` for that source's contribution. Stock client UIs handle this fine. Rationale: avoids divide-by-zero, matches the typical proxy-client traffic shape (download-heavy), and is robust to absent upload data.

4. **America/Toronto timezone loading**: Load once at server startup via `time.LoadLocation(cfg.DailyBudgetTimezone)`, hold the resulting `*time.Location` on the `Pipeline` (via `WithBudgetLocation` builder). If `LoadLocation` fails (very rare — only if the IANA database is unavailable AND the requested zone isn't in stdlib's bundled fallback), fail loud at startup with a clear error naming the requested timezone. Rationale: stdlib already ships a fallback timezone DB so this should never fail in practice; loud-fail at startup makes any future failure operator-visible (vs. silently falling back to UTC and confusing the operator about why the daily boundary shifted).

5. **Snapshot file atomicity** (resolves checklist open item): Write to `/data/today-zero.json.tmp`, call `(*os.File).Sync()` to flush kernel buffers, close the file, then `os.Rename` to `/data/today-zero.json`. POSIX guarantees atomic rename within the same filesystem. Concurrent readers (multiple in-flight requests at midnight) see either the old or the new snapshot — never a partial file. Rationale: standard POSIX pattern; no additional locking needed because the rollover writer (in `Pipeline.Build`) is naturally serialized at typical request volume; if true contention ever happens, the worst outcome is two writers race the rename and one wins (both writes were correct, so the result is correct).

6. **Snapshot corruption recovery**: When `snapshotter.Load()` returns parse-error or the loaded data fails validation (e.g., `LocalDate` not a valid YYYY-MM-DD), `Pipeline.Build` treats it identically to "no snapshot" — captures fresh state, persists, continues. The recovery is logged at INFO (not ERROR) per FR-007 to avoid alerting on a recoverable degradation. Rationale: see Complexity Tracking entry above. The reset of `used_today` to 0 is the one user-visible side effect, which decays naturally at the next "real" rollover.

7. **Two new env vars vs one or none**: Add two env vars matching the project's existing one-env-per-knob pattern (`HONKAI_RULE_CLIENT_UA`, `FALLBACK_RULE_TARGET`, `URL_TEST_*`):
   - `TODAY_ZERO_PATH` — string; default `/data/today-zero.json`. Override useful for tests + alternative volume layouts.
   - `DAILY_BUDGET_TIMEZONE` — string; default `America/Toronto`. Parsed via `time.LoadLocation`; invalid values fail startup loud per Constitution Principle III.

   Rationale: forward-compat for operators in other timezones without code changes; matches existing env-var idiom; both have sensible defaults so the chart needs zero new env entries unless an override is desired.

**Output**: `research.md` documenting the seven decisions with rationale + rejected alternatives.

## Phase 1: Design & Contracts

**Prerequisites**: `research.md` complete

### Data Model

`data-model.md` covers:

- **`dailyspend.Snapshot`** (new exported type in `internal/dailyspend/snapshot.go`):
  ```go
  type Snapshot struct {
      LocalDate            string             `json:"snapshot_local_date"`     // "YYYY-MM-DD" in budget timezone
      SnapshotUnix         int64              `json:"snapshot_unix"`           // unix seconds when captured
      AllowanceTodayBytes  int64              `json:"allowance_today_bytes"`   // pinned daily allowance
      Baselines            map[string]int64   `json:"baselines"`               // per-source cumulative_used at midnight (= upload + download)
      BaselineUploadRatios map[string]float64 `json:"baseline_upload_ratios"`  // per-source upload / (upload+download) at midnight, [0..1]
  }
  ```

- **`dailyspend.FileSnapshotter`** (production impl): wraps a `path string`. Constructor: `NewFileSnapshotter(path string) *FileSnapshotter`. `Load() (*Snapshot, error)` returns `(nil, nil)` when the file doesn't exist (treated as "rollover needed"); returns `(nil, err)` only on real I/O failure (permission denied, etc.). Parse errors return `(nil, nil)` and emit a structured log line (caller reinit). `Save(*Snapshot) error` writes to `path + ".tmp"`, syncs, closes, renames.

- **`dailyspend.MapSnapshotter`** (test impl): wraps an in-memory `*Snapshot` with a `sync.Mutex`. Constructor: `NewMapSnapshotter(initial *Snapshot) *MapSnapshotter`. Exposes `Current() *Snapshot` for assertions.

- **Pure helpers** in `internal/dailyspend/snapshot.go`:
  - `IsRolloverNeeded(s *Snapshot, now time.Time, loc *time.Location) bool`
  - `ComputeUsedToday(perSource map[string]fetcher.SubscriptionUserinfo, s *Snapshot) int64` — sum of clamped per-source deltas
  - `SplitUsedToday(usedToday int64, perSource map[string]fetcher.SubscriptionUserinfo, s *Snapshot) (upload, download int64)` — weighted-average upload ratio across contributing sources
  - `CaptureSnapshot(perSource map[string]fetcher.SubscriptionUserinfo, allowance int64, now time.Time, loc *time.Location) *Snapshot`

- **`merge.Pipeline.snapshotter`** + **`merge.Pipeline.budgetLocation`** (new fields). Builder methods: `WithSnapshotter(s Snapshotter) *Pipeline` (Snapshotter is a structurally-typed interface declared inline) and `WithBudgetLocation(loc *time.Location) *Pipeline`. Defaults: nil snapshotter → fall back to 010 behavior (ComposeServedTrafficHeader without spend tracking); nil location → time.UTC.

- **New helper**: `merge.ComposeServedTrafficHeaderWithSpend(perSource, clk, snapshot, loc) (*ServedTrafficHeader, *Snapshot)` — pure. Returns the served header value AND the (possibly new) snapshot. Caller persists if the returned snapshot is non-nil and differs from the input.

### Contracts

`contracts/served-subscription.changes.md` covers:

- **Wire format**: unchanged. Header still emits `Subscription-Userinfo: upload=<U>; download=<D>; total=<T>; expire=<E>` with integer values, semicolon-space separated.
- **Semantic change vs. 010**:
  - `upload + download = used_today` (010 always 0; 011 grows through the day)
  - `total = allowance_today + used_today` (010 was just allowance; 011 includes spend so the bar fills)
  - `expire = unix(next 00:00 America/Toronto)` (010 was UTC; 011 is Toronto local)
- **Header omission rule**: unchanged from 010 FR-006. When no source contributed userinfo, header is omitted entirely.
- **Overflow behavior**: when `used_today > allowance_today`, `total - upload - download` may be negative. Acceptable per operator decision (acceptance scenario 3 / SC-005); stock UIs handle it.
- **`/health` JSON shape**: unchanged. The `aggregatedSubscriptionUserinfo` and `dailyAllowance` blocks are still present; the snapshot is NOT exposed on `/health` (this feature's debuggability is via the new log fields per FR-011 and via direct snapshot-file inspection per the quickstart).

### Quickstart

`quickstart.md` covers (operator-facing):

1. **Verify the served header carries the new encoding**: `curl -fsS -A "Bronya/1.0" .../?token=<TOKEN>` then inspect `Subscription-Userinfo`. Expected (no spending yet today): `upload=<small or 0>; download=<small>; total=<allowance>; expire=<next 00:00 America/Toronto>`. As the user consumes, `upload + download` grows; `total` stays stable through the day.
2. **Inspect the snapshot file directly** (helper-pod method, since the runtime image is `FROM scratch`): same `make rules-sync`-style helper-pod pattern. Mount the PVC, `cat /data/today-zero.json`, observe the captured baselines.
3. **Force a rollover for testing**: edit the snapshot file's `snapshot_local_date` to yesterday, then issue any served request — the rollover fires and the snapshot regenerates from current upstream state.
4. **Cross-check via `/health`**: `dailyAllowance.perDayRateBytes + .noExpiryRemainingBytes ≈ snapshot.allowance_today_bytes` (exact match if no rollover-since-/health, within rounding otherwise — see SC-008 for the precise identity).
5. **Troubleshoot**: missing snapshot file → snapshot regenerates on next request, log shows `dailyspend snapshot file missing — initializing`; corruption → same recovery path with `dailyspend snapshot file corrupted — reinitializing` log; bar stuck at 0% all day → likely the upstream `cumulative_used` isn't growing (provider issue) or the snapshot baselines were captured at a non-midnight rollover (corruption-recovery side effect).

### Agent context update

Update the lines between `<!-- SPECKIT START -->` and `<!-- SPECKIT END -->` in `CLAUDE.md`:
- Mark **012 (url-test-region-groups)** as fully implemented (it is — live in production after this session's deploy).
- Mark **011 (daily-spend-tracking)** status as the active feature being designed/implemented (no longer parked).
- Add a key-reading bullet pointing at `specs/011-daily-spend-tracking/plan.md`.

## Phases (after this command)

This command stops here. Next: `/speckit-tasks` produces `tasks.md` with the dependency-ordered task list (test-first per Constitution Principle IV: dailyspend snapshot tests → dailyspend impl → ComposeServedTrafficHeaderWithSpend tests → merge layer impl → Pipeline builder + Build wiring → cmd/server/main.go wiring → route-handler log fields → integration tests → snapshot refresh → `make check`).
