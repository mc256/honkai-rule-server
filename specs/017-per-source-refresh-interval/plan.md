# Implementation Plan: Per-Source Refresh Interval Column

**Feature**: 017-per-source-refresh-interval  
**Status**: Implemented

## Approach

The refresh interval was already wired end-to-end via the optional `ttl_seconds` column and the
`Coordinator.ttlFor` → `runSteady` ticker path (001 / 002). This feature renames the column to
`refresh`, widens its parsing to a tri-state, and adds a "never refresh" branch to the scheduler.
No merge-layer or output-layer changes — the refresh interval is scheduling-only and does not
affect served bytes, so integration snapshots are unaffected.

## Changes

### `internal/config/subscriptions.go`
- Rename `SubscriptionRow.TTLSeconds` → `RefreshSeconds`; document the tri-state
  (`0`/absent → default, `>0` → interval, `<0` → never).
- Replace `"ttl_seconds"` with `"refresh"` in `optionalCols`.
- `parseRow`: parse `refresh` accepting any integer (including `0` and negatives); only a
  non-integer value is a `ConfigValidationError`. (Previously `ttl_seconds <= 0` was an error.)

### `internal/fetcher/scheduler.go`
- Add `neverRefreshTTL = time.Duration(math.MaxInt64)` and helper
  `refreshDisabled(row) bool { return row.RefreshSeconds < 0 }`.
- `ttlFor`: return `neverRefreshTTL` when `refreshDisabled`; else `RefreshSeconds` seconds when
  `> 0`; else `DefaultTTL`. The infinite TTL keeps the cache from ever reporting `StateStale` for
  a never-refresh source (steady-state + `/health` view).
- `fetchTTLFor`: mirrors `ttlFor` but returns `DefaultTTL` for a never-refresh source. Used by
  `refresh()` so the one-shot bootstrap still refreshes a stale disk-cached snapshot on restart
  (FR-017f) while steady-state staleness keeps the never-refresh sentinel.
- `runSteady`: when `refreshDisabled`, skip the ticker entirely and block on `ctx.Done()` (so the
  source is fetched once at bootstrap and never again, and clean shutdown via `Wait()` still works).
  Logs at info when a payload is cached, or warns when bootstrap left no payload (terminal until
  restart).

### Config + docs
- `config/subscriptions.csv` (gitignored): add the `refresh` column, existing rows set to `0`
  (default interval) to preserve behavior.
- `config/README.md`: document the tri-state `refresh` column with a table and examples.

## Tests

- `internal/config/subscriptions_test.go` — `TestCSV_10_OptionalColumns`: positive value parses;
  absent → 0; `0` accepted (default); `-1` accepted (never); non-integer → validation error on
  field `refresh`.
- `internal/fetcher/scheduler_test.go` — `TestScheduler_ttlFor` (tri-state mapping) and
  `TestScheduler_NeverRefresh` (with a 10ms default TTL, a `refresh=-1` source is fetched exactly
  once and still bootstraps successfully).

## Verification

`make check` (vet + staticcheck + tests + snapshot-drift) — vet/lint/test pass; no snapshot drift.
