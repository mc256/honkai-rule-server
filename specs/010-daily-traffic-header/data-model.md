# Data Model: Daily-Available Traffic in Served Subscription Header

**Feature**: 010-daily-traffic-header
**Date**: 2026-05-01

This document captures the new types, the new field on `merge.MergedConfig`, and the helpers that compose them. All additions are pure (no I/O, no `time.Now()` reads outside the injected clock).

---

## New type: `merge.ServedTrafficHeader`

Location: `internal/merge/traffic.go`

```go
// ServedTrafficHeader carries the two values that go into the public
// Subscription-Userinfo response header per 010 FR-001/FR-002. This is the
// daily-spendable encoding: DailyAllowanceBytes is the figure clients see in
// their usage bar, and ExpireUnix is the "tomorrow" deadline the client UI
// uses for its countdown.
//
// nil = no source contributed userinfo; the output adapter omits the header
// entirely (010 FR-006). Non-nil with DailyAllowanceBytes == 0 is a real
// "every source is expired or fully consumed" state (010 FR-007 + edge case
// "Daily allowance is zero").
type ServedTrafficHeader struct {
    DailyAllowanceBytes int64 // = total - upload - download in the served header
    ExpireUnix          int64 // = unix(next 00:00 UTC strictly after request time)
}
```

**Why a dedicated type instead of reusing `fetcher.SubscriptionUserinfo`**: the served figure is a derived value, not raw upstream metadata. Reusing `SubscriptionUserinfo` would conflate the two roles and force the adapter to know which fields are "raw" vs. "served". A small type makes the boundary explicit.

**Why pointer-typed when stored**: enables the unambiguous "no source contributed" signal needed by FR-006. A value type with `IsZero()` would conflate "no userinfo" with "all sources expired".

---

## New field on `merge.MergedConfig`

Location: `internal/merge/pipeline.go`

```go
type MergedConfig struct {
    // ... existing fields unchanged ...

    AggregatedSubscriptionUserinfo *fetcher.SubscriptionUserinfo
    // [continued: still populated as today; consumed only by /health, not by the
    // public Subscription-Userinfo response header]

    // ServedTrafficHeader is the value the output adapter writes into the
    // public Subscription-Userinfo response header. nil = no source supplied
    // userinfo, omit the header. Computed in Build() per 010 FR-001/FR-002.
    ServedTrafficHeader *ServedTrafficHeader

    // ... rest of struct unchanged ...
}
```

**Population**: in `Pipeline.Build()`, immediately after the existing `aggregatedUI := AggregateSubscriptionUserinfo(userinfoPerSource)` call:

```go
servedTrafficHeader := ComposeServedTrafficHeader(userinfoPerSource, p.clock)
```

and then included in the returned `&MergedConfig{...}` literal. No other `Build()` change.

**Determinism**: `ComposeServedTrafficHeader` reads the clock once. `Build()` is already called per request from the route handler (`internal/server/routes/subscription.go:54`), so the clock is read per request. Within a UTC calendar day this produces byte-identical headers (SC-006); across the day boundary the `ExpireUnix` rolls forward by 86400 seconds in one step.

---

## New helper: `merge.NextMidnightUTC`

Location: `internal/merge/traffic.go`

```go
// NextMidnightUTC returns the next 00:00 UTC strictly after now. It is used
// by ComposeServedTrafficHeader to populate ServedTrafficHeader.ExpireUnix
// (010 FR-002). Pure: depends only on its argument.
//
// "Strictly after" means a request received exactly at 00:00:00 UTC produces
// the *following* day's midnight, not the current instant. This avoids an
// expire timestamp equal to the request timestamp, which clients may treat
// as already-expired.
func NextMidnightUTC(now time.Time) time.Time {
    nowUTC := now.UTC()
    return time.Date(
        nowUTC.Year(), nowUTC.Month(), nowUTC.Day()+1,
        0, 0, 0, 0, time.UTC,
    )
}
```

**Edge cases**:
- A request at `2026-05-01 23:59:59.999 UTC` returns `2026-05-02 00:00:00 UTC` (1 ms in the future).
- A request at `2026-05-01 00:00:00.000 UTC` returns `2026-05-02 00:00:00 UTC` (strictly after).
- A request at `2026-05-31 23:59:30 UTC` returns `2026-06-01 00:00:00 UTC` (Go's `time.Date` normalizes month boundaries; `d+1 == 32` becomes `2026-06-01`).
- Leap years and DST do not affect UTC.

**Tests** (`internal/merge/traffic_test.go`):
- `TestNextMidnightUTC_BeforeMidnight`: 23:59:30 UTC → next-day 00:00:00 UTC.
- `TestNextMidnightUTC_ExactlyMidnight`: 00:00:00 UTC → next-day 00:00:00 UTC (strictly after).
- `TestNextMidnightUTC_MonthBoundary`: 2026-01-31 23:59:00 UTC → 2026-02-01 00:00:00 UTC.
- `TestNextMidnightUTC_YearBoundary`: 2026-12-31 12:00:00 UTC → 2027-01-01 00:00:00 UTC.
- `TestNextMidnightUTC_NonUTCInput`: input in any timezone normalizes via `.UTC()` first; output is always in UTC.

---

## New helper: `merge.ComposeServedTrafficHeader`

Location: `internal/merge/traffic.go`

```go
// ComposeServedTrafficHeader returns the value to embed in the served
// Subscription-Userinfo response header per 010 FR-001/FR-002, or nil if no
// source contributed userinfo (010 FR-006).
//
// DailyAllowanceBytes = (per-source weighted per-day rate over expiring
// sources) + (no-expiry remaining sum), per 001 FR-011b. ExpiredSourceFlags
// from the underlying ComputeDailyAllowance contribute 0 to the sum (010
// FR-007); they remain visible on the /health surface (001 FR-015) for
// operator notice.
//
// ExpireUnix = unix(NextMidnightUTC(clk.Now())).
//
// Pure: no I/O. The clock is the only nondeterministic input and is injected.
func ComposeServedTrafficHeader(
    perSource map[string]fetcher.SubscriptionUserinfo,
    clk clock.Clock,
) *ServedTrafficHeader {
    if len(perSource) == 0 {
        return nil
    }
    da := ComputeDailyAllowance(perSource, clk)
    return &ServedTrafficHeader{
        DailyAllowanceBytes: da.PerDayRateBytes + da.NoExpiryRemainingBytes,
        ExpireUnix:          NextMidnightUTC(clk.Now()).Unix(),
    }
}
```

**Why `len(perSource) == 0` is the right omission predicate**: the caller (`Pipeline.Build()`) constructs `userinfoPerSource` by walking the cache and including only sources that contributed `Subscription-Userinfo` (per 001 FR-012). An empty map therefore means "no source had userinfo" — which is exactly FR-006's omission case. Sources whose userinfo was present but reported zero traffic still appear in the map and contribute 0 to the daily-allowance sum (FR-007's "exactly 0" case, header still emitted).

**Tests** (`internal/merge/traffic_test.go`):
- `TestComposeServedTrafficHeader_TwoSourcesFR011b`: the 5 GB/day + 16 GB/day fixture from 001 FR-011b → `DailyAllowanceBytes = 21 * 1024^3` ± rounding, `ExpireUnix = unix(next-midnight-UTC(fixed clock))`.
- `TestComposeServedTrafficHeader_NoSources`: empty map → returns nil.
- `TestComposeServedTrafficHeader_AllExpired`: every source's `expire` in the past → returns non-nil with `DailyAllowanceBytes = 0` and `ExpireUnix` set.
- `TestComposeServedTrafficHeader_AllNoExpiry`: every source's `expire == 0` → `DailyAllowanceBytes = sum of remaining`, `ExpireUnix = unix(next-midnight-UTC)`.
- `TestComposeServedTrafficHeader_MixedExpiringAndNoExpiry`: per-day-rate sum + no-expiry remaining sum, both fold into `DailyAllowanceBytes`.
- `TestComposeServedTrafficHeader_NegativeRemainingClamped`: a source with `used > total` contributes 0 (the clamping inherited from `ComputeDailyAllowance`).
- `TestComposeServedTrafficHeader_DeterministicWithinDay`: two calls with the same fixture and a clock advanced by 30 minutes (still within the same UTC day) return equal `*ServedTrafficHeader` values.

---

## Output adapter changes

Location: `internal/output/subscription_mode.go::Render`

**Before** (lines 137–141):
```go
if ui := merged.AggregatedSubscriptionUserinfo; ui != nil {
    headers.Set("Subscription-Userinfo",
        fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d",
            ui.Upload, ui.Download, ui.Total, ui.Expire))
}
```

**After**:
```go
if h := merged.ServedTrafficHeader; h != nil {
    headers.Set("Subscription-Userinfo",
        fmt.Sprintf("upload=0; download=0; total=%d; expire=%d",
            h.DailyAllowanceBytes, h.ExpireUnix))
}
```

The `merged.AggregatedSubscriptionUserinfo` field is *not* removed — it's still consumed by `/health`. Only the public-header emission switches sources.

**Tests** (`internal/output/subscription_mode_test.go`, table-driven):

| Case | Input | Expected `Subscription-Userinfo` |
|------|-------|----------------------------------|
| FR-011b worked example | 2 sources (200GB/30d, 100GB/5d) | `upload=0; download=0; total=22548578304; expire=<next-midnight-utc-unix>` (= 21*1024³) |
| All expired | 2 sources, both `expire < now` | `upload=0; download=0; total=0; expire=<next-midnight-utc-unix>` |
| All no-expiry | 2 sources, both `expire=0` | `upload=0; download=0; total=<sum-of-remaining>; expire=<next-midnight-utc-unix>` |
| Mixed expiring + no-expiry | 1 of each | `upload=0; download=0; total=<per-day-rate + no-expiry-remaining>; expire=<next-midnight-utc-unix>` |
| No userinfo at all | empty perSource map | header absent from `headers` (`headers.Get("Subscription-Userinfo") == ""`) |
| Source's used > total | 1 source with negative remaining | `upload=0; download=0; total=0; expire=<next-midnight-utc-unix>` (clamped) |

---

## Route-handler logging changes

Location: `internal/server/routes/subscription.go::Subscription`

**Existing** "served subscription" `slog.Info` line gets two new fields per FR-008:

```go
fields := []any{
    "path", r.URL.Path,
    "contributingSources", merged.ContributingSources,
    "proxies", len(merged.Proxies),
    "groups", len(merged.ProxyGroups),
    "rules", len(merged.Rules),
    "bytes", len(rendered.Body),
    "served_daily_allowance_bytes", servedAllowance(merged), // new
    "served_expire_unix", servedExpire(merged),              // new
}
```

where `servedAllowance` and `servedExpire` are tiny inline helpers that return the field's value or `-1` when `merged.ServedTrafficHeader == nil` (so the log line distinguishes "header omitted" from "header carried zero").

**New** debug-verbosity line emitted only when `slog.Default().Enabled(ctx, slog.LevelDebug)`:

```go
da := deps.Pipeline.ComputeDailyAllowance()
deps.Logger.Debug("served daily allowance breakdown",
    "per_day_rate_bytes", da.PerDayRateBytes,
    "no_expiry_remaining_bytes", da.NoExpiryRemainingBytes,
    "expired_source_flags", da.ExpiredSourceFlags,
)
```

The `Enabled` check avoids the `ComputeDailyAllowance` second-call cost when debug logging is disabled (the common case).

**Tests** (`internal/integration/headers_test.go`): assert the served header bytes against a multi-source fixture; assert the info-level log fields' presence and types via the existing structured-logger test harness.
