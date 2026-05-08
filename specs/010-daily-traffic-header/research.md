# Research: Daily-Available Traffic in Served Subscription Header

**Feature**: 010-daily-traffic-header
**Date**: 2026-05-01

This document captures the six narrow design decisions for the deploy of the daily-allowance value into the public `Subscription-Userinfo` HTTP response header. The math is unchanged from 001 FR-011b; only the transport surface and the encoding are at stake.

---

## R1 — Encoding split between `upload` / `download` / `total`

**Decision**: Use `upload=0; download=0; total=daily_allowance_bytes; expire=tomorrow_unix`.

**Rationale**:
- Simplest encoding satisfying FR-003's invariant `total − upload − download = daily_allowance`.
- Matches the user's mental model ("daily available traffic") cleanly: the client UI reads "you have N bytes, none used yet" — accurate within the daily-budget framing.
- Snapshot-friendly: two of the three byte fields are constant zero, so the snapshot diff is small and the failure mode for a regression (`upload≠0` or `download≠0`) is obvious in the test output.

**Alternatives considered**:
- *Preserve aggregated historical `upload`/`download`*: keeps cumulative usage visible alongside the daily figure. Rejected because the cumulative usage doesn't reset daily, so the displayed "used / total" ratio would be misleading in a daily-budget UX (e.g., displayed "used = 50 GB / total = 71 GB" looks like 70% consumed when in fact the user has spent 0 of today's 21 GB allowance).
- *Encode as `upload=daily_allowance/2; download=daily_allowance/2; total=daily_allowance`*: would still satisfy the invariant (remaining = 0) but flips the semantics — the client would render "100% consumed". Rejected as actively wrong for the intended UX.

---

## R2 — Definition of "tomorrow"

**Decision**: `expire = unix(NextMidnightUTC(now))` where `NextMidnightUTC(now) = time.Date(y, m, d+1, 0, 0, 0, 0, time.UTC)` and `(y, m, d)` are taken from `now.UTC()`.

**Rationale**:
- Deterministic and single-valued for any input request time.
- Stable across the UTC calendar day → SC-006 holds (two requests within the same UTC day return identical header bytes against the same input snapshot).
- UTC has no DST, so the helper has no timezone-corner cases to defend against.
- Aligns with how the rest of the server handles upstream `expire` timestamps (Unix seconds, no timezone affixed).

**Alternatives considered**:
- *`now + 86400`* (rolling 24-hour window): rejected because `expire` would advance second-by-second, breaking SC-006. Two requests served seconds apart would have different `expire` values even though the input is unchanged.
- *Local-server-timezone midnight*: rejected. Introduces an operator-configurable timezone with no current value (the server runs in the cluster's UTC environment). Out of scope; can be added as a follow-up flag if any deployment ever needs it.
- *Fixed `expire = 0`*: rejected because clients interpret `expire=0` as "no expiry", which would defeat the user's stated framing ("the traffic will expire tomorrow"). The expiring deadline is *the* signal the user wants the client UI to display.

---

## R3 — Where the daily-allowance figure is computed

**Decision**: Precompute in `Pipeline.Build()` and stash on `MergedConfig` as a new pointer field `*ServedTrafficHeader`. The output adapter reads the field as-is.

**Rationale**:
- Matches the existing pattern in `internal/merge/pipeline.go::Build()`: every other public-header-bound figure (`AggregatedSubscriptionUserinfo`, `AggregatedProfileUpdateIntervalHours`) is precomputed and stored on `MergedConfig`. Adding one more field is the minimal, idiomatic change.
- Keeps `internal/output/subscription_mode.go::Render` a pure renderer with no clock or per-source userinfo dependency. Makes the override-mode adapter (when it lands) trivially inherit the same figure — no fork, no copy of the arithmetic.
- `Build()` is already called per request from the route handler, with `clock.Clock` injected at construction time. Per-request recomputation is preserved without new wiring.

**Alternatives considered**:
- *Compute in the output adapter*: would couple the adapter to (a) the clock interface and (b) the per-source userinfo map (which lives on the pipeline today). Rejected as a pointless layering violation.
- *Compute lazily in a header-emit helper called from the route handler*: spreads header-related logic across two layers. Rejected; less testable and breaks the "all served-header values precomputed on `MergedConfig`" invariant.

---

## R4 — Header omission rule

**Decision**: Set `MergedConfig.ServedTrafficHeader` to `nil` when no source contributed `Subscription-Userinfo` userinfo (i.e., `len(perSourceUserinfo) == 0`). The output adapter omits the `Subscription-Userinfo` response header when the field is nil.

**Rationale**:
- Required by spec FR-006: emitting `total=0; upload=0; download=0; expire=<tomorrow>` would falsely advertise "you have zero quota at all", which is wrong — the truth is "the server doesn't have traffic metadata yet".
- Consistent with 001 FR-012's "missing source contributes nothing — never zero" rule.
- Tightens the existing nil-check at `internal/output/subscription_mode.go:137`, which today is structurally there but never trips because `AggregateSubscriptionUserinfo` returns a non-nil zero struct on empty input.

**Alternatives considered**:
- *Emit `total=0` when no source contributed*: rejected per FR-006. Misleads the client UI into rendering an exhausted bar.
- *Emit `expire=0` and `total=very-large` to disambiguate "unknown"*: rejected. There's no convention for "unknown" in the existing wire format; clients would interpret it as a real value.

---

## R5 — Per-source debug logging

**Decision**: At the route handler level, after `Render` returns, emit `slog.Debug("served daily allowance breakdown", "per_day_rate_bytes", X, "no_expiry_remaining_bytes", Y, "expired_source_flags", […])` using the `DailyAllowance` recomputed via `Pipeline.ComputeDailyAllowance()`. The handler also adds two `slog.Info` fields per FR-008 alongside the existing `served subscription` log line: `served_daily_allowance_bytes` and `served_expire_unix`.

**Rationale**:
- Keeps the `internal/merge/` package free of `*slog.Logger` parameters, preserving Constitution Principle II's "pure transform" boundary.
- The handler already has access to `Pipeline`, the rendered headers, and the request context — it can emit both the served value (info) and the operator-debugging breakdown (debug) in one place.
- Cheap: `ComputeDailyAllowance` is the same function `/health` calls; running it twice per request (once during `Build()`'s composer, once for the breakdown log) is fine at the current request rate. If profiling ever shows it's not, the breakdown can be precomputed and stashed on `MergedConfig` as a memoized field — but that's a future micro-optimization, not a current need.

**Alternatives considered**:
- *Log inside `ComputeDailyAllowance`*: rejected. Adds a logger dependency to a pure function and to its callers; muddles the test surface (you'd have to silence the logger in unit tests).
- *Skip per-source debug logging entirely*: rejected. FR-008 calls it out specifically as a needed operator-troubleshooting affordance for the inevitable "the displayed figure looks wrong, why?" support case.

---

## R6 — Test fixture for SC-001's 21 GB/day worked example

**Decision**: Fixture-driven table test in `internal/output/subscription_mode_test.go` constructs two `fetcher.SubscriptionUserinfo` records matching 001 FR-011b's worked example:
- Source A: `total = 200 * 1024^3`, `download = 50 * 1024^3`, `upload = 0`, `expire = clk.Now() + 30*86400`.
- Source B: `total = 100 * 1024^3`, `download = 20 * 1024^3`, `upload = 0`, `expire = clk.Now() + 5*86400`.
Fixed clock at `2026-05-01 12:00:00 UTC`. Asserts the served `Subscription-Userinfo` header parses to `total − upload − download == 21 * 1024^3` ± 1 byte tolerance (rounding of the per-day rate may produce an off-by-1 for non-integer divisions; the spec's "± rounding" language covers this) and `expire == unix(2026-05-02 00:00:00 UTC)`.

**Rationale**:
- Anchors the test directly to 001 FR-011b's worked example so any future arithmetic regression in `ComputeDailyAllowance` (which this feature depends on but does not modify) is caught at the output layer too.
- The 1-byte tolerance is realistic: `30 days, 200GB, 50GB used → remaining 150GB / 30 = 5GB/day exactly` and `5 days, 100GB, 20GB used → 80GB / 5 = 16GB/day exactly`, so the canonical fixture happens to be integer-clean. The tolerance is for snapshot insurance against future fixture tweaks that introduce non-integer divisions.

**Alternatives considered**:
- *Snapshot the full response body bytes*: rejected. The body bytes don't change in this feature; only the header bytes do. A header-only assertion is more focused and less brittle.
- *Property-test the per-day-rate identity for randomly generated source pairs*: out of scope for this PR; would be a nice complement but isn't required by the constitution (Principle IV asks for fixture-driven snapshots, not property tests).
