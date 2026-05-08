# Feature Specification: Today's-Spend Tracking in Served Subscription-Userinfo

**Feature Branch**: `011-daily-spend-tracking`
**Created**: 2026-05-02
**Status**: 🅿️ **Parked / Deprioritized** — captured for future pickup. The operator has explicitly deprioritized this feature; something more important is being shipped first. Do NOT implement until the operator reactivates.
**Input**: Continues 010-daily-traffic-header — the served `Subscription-Userinfo` header currently always reports `upload=0; download=0`, so the client UI's usage bar stays at 0% all day even as the user spends from today's allowance. This feature makes the bar reflect today's consumption.

**Anchor**: Builds on [`010-daily-traffic-header/spec.md`](../010-daily-traffic-header/spec.md). The daily-allowance figure from 001 FR-011b stays the math source of truth; what changes is how that figure is composed into the `upload`/`download`/`total` triple and which "next midnight" the `expire` points at.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Today's spend visible in the client UI as the user consumes traffic (Priority: P1)

The user wants their proxy client (Mihomo / Clash) to show a usage bar that fills up over the course of the day as they consume their daily allowance — not a flat 0% bar. The "remaining" number from 010 is correct, but the visual progress is missing, which makes it hard to glance-check "have I used most of today's budget?" without doing math against the previous request.

To deliver this, the served `Subscription-Userinfo` header needs to report bytes-spent-since-today's-local-midnight as `upload + download`, and a stable `total` for the day (= today's allowance + today's spend) so the client UI's "used / total" ratio is meaningful. Today's local-midnight is **America/Toronto local time** so the daily boundary aligns with the operator's day, not UTC.

**Why this priority**: This is the only user story. It corrects the client-UI display gap that 010 left in place. The remaining number stays correct; only `upload`, `download`, and `total` change shape.

**Independent Test**: Configure two upstream subscriptions whose `cumulative_used = upload + download` is known at 00:00 America/Toronto on test day D, then advance the clock to 12:00 of the same day with the upstreams reporting `cumulative_used + N` total bytes consumed since midnight. Fetch the served subscription. Assert the response `Subscription-Userinfo` header reports `upload + download = N ± 1 byte` and `total − upload − download = today's allowance computed at midnight`. Re-fetch at 23:59 with `cumulative_used + M`; assert `upload + download = M`. Cross the local midnight; assert the next fetch reports a fresh `upload + download` near 0 and a fresh `total` (today's new allowance).

**Acceptance Scenarios**:

1. **Given** two upstream subscriptions A and B at exactly 00:00 America/Toronto with known per-source `cumulative_used`, **When** the first request of the day arrives, **Then** the server captures the per-source baselines + today's allowance, persists the snapshot, and returns a header with `upload + download ≈ 0` and `total ≈ today's allowance`.
2. **Given** the same setup but the request arrives at 12:00 America/Toronto with each upstream's `cumulative_used` increased by N_i since midnight, **When** the request is served, **Then** the header reports `upload + download = Σ N_i ± rounding` and `total = today's allowance + Σ N_i` (so `remaining = today's allowance − Σ N_i`).
3. **Given** the user has consumed *more* than today's allowance (overspend), **When** the request is served, **Then** the header is still well-formed; `total − upload − download` may be negative — the client UI handles the overflow gracefully (per operator decision).
4. **Given** the snapshot was captured yesterday at 00:00 America/Toronto, **When** the first request after today's 00:00 America/Toronto local-midnight arrives, **Then** the server captures fresh baselines (current per-source `cumulative_used`) and a fresh allowance (`ComputeDailyAllowance` at the new midnight), atomically replaces the snapshot, and returns a header reflecting the new day.
5. **Given** a pod restart mid-day, **When** the next request after restart arrives, **Then** the snapshot is loaded from durable storage and the served header reflects the same `total` and `upload + download` it would have reported just before restart (give-or-take cumulative_used drift since the last request).
6. **Given** an upstream provider's monthly counter resets (`cumulative_used` drops below the captured baseline), **When** the next request is served, **Then** that source's contribution to `upload + download` is clamped to `0` (no negative bytes); at next local-midnight rollover, the baseline refreshes to the new lower value naturally.
7. **Given** a daylight-saving-time boundary in America/Toronto (one 23-hour day in spring; one 25-hour day in fall), **When** "today's local midnight" is computed across that boundary, **Then** the rollover fires exactly once at the correct local time, and `expire` points at the next local 00:00 (which differs from now+24h by ±1 hour relative to UTC during DST transitions).
8. **Given** a server with no snapshot file (first boot, or PVC reset), **When** the first request arrives, **Then** the server initializes baselines from current upstream values, computes today's allowance from current state, persists the snapshot, and serves a header with `upload + download = 0` and `total = today's allowance`.

### Edge Cases

- **Snapshot file is corrupted** (truncated write, JSON parse error): treat as "no snapshot file" → reinitialize from current state, log a warning. The current day's `upload + download` resets to 0; allowance is recomputed.
- **Source disappears from cache** (was present at midnight, gone now): skip from the `Σ` sum (consistent with 001 FR-012). The baseline stays in the snapshot until next midnight in case the source returns. If it's still gone at next midnight, the new snapshot drops it.
- **Source first appears mid-day** (operator added a new subscription after midnight): no baseline → contributes `0` to `upload + download` today. At next midnight rollover the new source gets a baseline. Today's allowance does NOT recompute mid-day to include the new source — operators accept that adding a subscription mid-day takes one day to fully reflect (predictable behavior trumps mid-day budget jumps).
- **Server clock skew backward** across local midnight: `cumulative_used − baseline` could go negative if the rollover already happened in a prior request and the clock then jumped back. Clamp the per-source contribution to `0`.
- **Concurrent requests at the rollover instant**: the snapshot replacement must be atomic (write to `/data/today-zero.json.tmp`, fsync, rename). Concurrent readers see either the old or the new snapshot — never a partial file.
- **`Subscription-Userinfo` absent on every source** (per 010 FR-006): the served header is omitted entirely. This feature does not change the omission rule.
- **All sources expired or fully consumed** (010 FR-007): `today's allowance = 0`. The header still emits `upload + download = today's spend` and `total = today's spend` (so `remaining = 0`). The client UI shows the bar full at 100% used.
- **All-no-expiry case** (every source `expire == 0`): today's allowance comes from `noExpiryRemainingBytes`, baselines and rollover work the same way. The "no-expiry pool drains over time" framing is consistent: each day's allowance treats the pool as freely-spendable-today, and tomorrow's allowance recomputes from the now-smaller remaining no-expiry pool.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: When serving the merged subscription, the server MUST compute `used_today = Σ_i max(0, cumulative_used_i − baseline_i)` where `cumulative_used_i = upload_i + download_i` (current upstream-reported value) and `baseline_i` is the value captured at today's local-midnight for source `i`. Sources without a baseline (newly appeared since midnight) contribute `0`.

- **FR-002**: The server MUST emit `Subscription-Userinfo: upload=<U>; download=<D>; total=<T>; expire=<E>` where:
  - `U + D = used_today`
  - `T = allowance_today + used_today`
  - `E = unix(next 00:00 America/Toronto local time)`
  - The `U`/`D` split MUST mirror the upstream upload/download ratio captured at today's local-midnight (per operator requirement: keep `upload` non-zero so the field is "real" for any future feature that wants to use it). Each source's `(upload_i, download_i)` ratio is captured into `baseline_upload_ratios[source]` at midnight; `U = used_today × weighted_avg(ratios)` and `D = used_today − U`. Tolerance ±1 byte for rounding.

- **FR-003**: The displayed `total` MUST be **stable through the day** (it does NOT recompute the per-day-rate every request). `allowance_today` is captured exactly once per local-day at the midnight rollover (or at first-boot initialization) and held until the next midnight rollover.

- **FR-004**: The midnight rollover MUST be **lazy** (request-driven). On every served request the server compares the current local-day to the snapshot's `snapshot_local_date`. If they differ, the server captures fresh baselines from current per-source `cumulative_used`, fresh `baseline_upload_ratios` from current per-source `(upload_i, download_i)`, fresh `allowance_today` via `ComputeDailyAllowance` evaluated at the now-current clock, and atomically replaces the snapshot. Then proceeds with FR-001/FR-002 against the new snapshot. No background timer is required.

- **FR-005**: The snapshot MUST persist across pod restarts. The location is one file on the existing PVC mounted at `/data`. The exact path is `/data/today-zero.json` (single file, no subdirectory). Write-then-rename atomicity MUST be preserved on rollover (write to `.tmp`, `fsync`, `rename`).

- **FR-006**: The snapshot file schema (JSON) MUST be:
  ```jsonc
  {
    "snapshot_local_date": "2026-05-02",          // YYYY-MM-DD in America/Toronto
    "snapshot_unix":       1746158400,             // unix seconds of the captured-at instant
    "allowance_today_bytes": 1459296771,           // pinned allowance for the day
    "baselines": {                                 // cumulative_used = upload + download per source
      "alpha":     242977323837,
      "beta":  15025246189
    },
    "baseline_upload_ratios": {                    // upload / (upload + download) per source, [0..1]
      "alpha":     0.10,
      "beta": 0.12
    }
  }
  ```
  Per-source map keys MUST exactly match the source `Name` from the subscriptions CSV (001 FR-001).

- **FR-007**: When the snapshot file is missing OR fails to parse OR has an obviously corrupted shape (e.g. `snapshot_local_date` not a valid YYYY-MM-DD), the server MUST treat the situation as "fresh init": capture baselines + allowance + ratios from current state, persist the new snapshot, log a warning at INFO level (not ERROR — the corruption is recoverable in a single request). Today's `used_today` resets to `0` after a corruption-recovery.

- **FR-008**: When the server detects that an upstream's `cumulative_used` is **less than** the captured `baseline_i` (provider's monthly billing reset), the per-source contribution to `used_today` MUST be clamped to `0`. The baseline is NOT mutated mid-day; it gets refreshed naturally at the next local-midnight rollover.

- **FR-009**: Sources whose `Subscription-Userinfo` is absent at request time (per 001 FR-012) MUST be skipped from the `Σ` sum. The header omission rule from 010 FR-006 still applies: if EVERY source omits userinfo, the response carries no `Subscription-Userinfo` header (rather than reporting `total = today's spend` for an empty source set).

- **FR-010**: The "today's local-midnight" computation MUST use the IANA timezone identifier `America/Toronto` (not a fixed UTC offset; not the server's local timezone if the container runs in UTC). Both standard time (EST, UTC-5) and daylight time (EDT, UTC-4) MUST be handled correctly via the timezone database. Daylight-saving transitions MUST be handled via standard date-add semantics (the next 00:00 local time is computed from `now`'s local date + 1 day at 00:00 local — this naturally yields a 23-hour next-midnight in spring-forward and 25-hour in fall-back).

- **FR-011**: Logs MUST include, on every served request that returns a body:
  - `served_used_today_bytes` (= `U + D`)
  - `served_total_bytes` (= `T`)
  - `snapshot_local_date` (= `snapshot_local_date` in effect for this request)
  - `rollover_fired` (boolean — true if this request triggered the local-midnight rollover)

- **FR-012**: This feature SUPERSEDES 010 FR-002 and 010 FR-003 for the served `Subscription-Userinfo` values. The wire-format contract from 010 FR-003 (and 001 FR-011 before it) remains unchanged. The omit-when-no-userinfo rule from 010 FR-006 is preserved.

- **FR-013**: Determinism: the served header MUST be a deterministic function of (per-source userinfo at request time, snapshot file contents, current clock). Snapshot tests inject all three (current clock via existing `clock.Clock`; snapshot via a new injectable interface). The merge transformation core MUST stay pure — file I/O for the snapshot lives outside the pure-merge boundary, in a new internal package.

### Key Entities

- **Today-zero snapshot**: The persistent record of "where the upstream counters were at today's local midnight" plus "what today's pinned allowance is" plus "the upload/download ratio at midnight per source". One file on the PVC; one logical record per local-day. Rolled over lazily on the first request after each midnight.
- **Today's spend (`used_today`)**: A derived per-request integer. Sum across upstream sources of the difference between current `cumulative_used` and the snapshot's per-source `baseline`, with clamping for resets. Drives the served `upload + download` value.
- **Today's allowance (`allowance_today`)**: A pinned-at-midnight integer carried in the snapshot. Computed once per local-day. Drives the served `total = allowance_today + used_today`. The `total` is stable across all requests of the same local-day (assuming no rollover between requests).
- **Local-day boundary**: 00:00 America/Toronto. The unit of time over which `used_today` accumulates and at which the snapshot rolls over.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For a fixed two-source fixture with known per-source `cumulative_used` deltas N_a, N_b across a single local-day, the served `Subscription-Userinfo` header's `upload + download = N_a + N_b ± 1 byte` for every request issued during that day.

- **SC-002**: For the same fixture, the served `total` is byte-identical across two requests issued at any two times during the same local-day (assuming no upstream counter changes between them and no rollover between them).

- **SC-003**: When a request crosses the local-midnight boundary (one request at 23:59 America/Toronto, the next at 00:01 the following local-day), the second request's `upload + download` is at most the upstream `cumulative_used` delta between the two requests (i.e., it does NOT include yesterday's spend) and the second request's `total` reflects a freshly computed `allowance_today`.

- **SC-004**: After a pod restart at any time during the local-day, the next served request's `upload + download` and `total` differ from the pre-restart values by at most the upstream `cumulative_used` delta accumulated during the restart window.

- **SC-005**: When the user spends more than `allowance_today` in a single local-day, the server still returns a 200 response with a well-formed `Subscription-Userinfo` header. The `total − upload − download` value may be negative; this is acceptable per operator decision and the client UI is expected to render it without breaking.

- **SC-006**: Across a daylight-saving-time boundary in America/Toronto, exactly one rollover fires (no double-rollover, no missed rollover), and `expire` after the boundary points at the correct next local 00:00 (which is 23 or 25 wall-clock hours away depending on direction).

- **SC-007**: Operators can read both `served_used_today_bytes`, `served_total_bytes`, `snapshot_local_date`, and `rollover_fired` fields from the structured request log line, sufficient to debug a misreported display without inspecting the snapshot file.

- **SC-008**: The cross-check identity holds: `served_total = served_upload + served_download + (allowance_today_bytes from /health)`. Any drift between the served header and `/health.dailyAllowance` (recomputed live) at a request not crossing midnight is bounded by the rounding tolerance from 001 FR-011b's `ceil()`.

## Assumptions

- The operator is in America/Toronto and the daily-budget mental model is "the day that resets at 00:00 Eastern". A future feature MAY make the timezone configurable; out of scope here.
- The PVC at `/data` is durable, single-writer (Recreate strategy + RWO), and survives pod restarts. Snapshot I/O reuses this guarantee.
- The upstream `cumulative_used` is monotonically non-decreasing within a billing cycle and resets at the provider's billing-cycle boundary (typically monthly). The clamp-on-reset rule (FR-008) handles the latter.
- Stock Mihomo / Clash clients render `total − upload − download` as "remaining" and `upload + download` as "used", and they tolerate `total < upload + download` (overflow case) without crashing or refusing the subscription. The operator has accepted this trade-off (acceptance scenario 3 / SC-005) so the spec does not need to clamp.
- The 010 daily-allowance computation (`ComputeDailyAllowance`) is correct and remains the source-of-truth for today's allowance. This feature does not modify that math; it captures its result at midnight and pins the result for the day.
- Test coverage: new unit tests for the snapshot file format + rollover logic + clamp + DST handling; one new integration test that simulates a clock advancing across a local-midnight boundary and asserts the served header transitions correctly. Existing snapshot tests get an additional injected snapshot fixture.
- Backward compatibility: clients on the 010 encoding will see `upload + download` change from `0` to a real number, and `total` change from "daily allowance" to "daily allowance + used". The `remaining` they compute (= `total − upload − download`) stays roughly the same except now decreasing through the day instead of staying flat. No client-side code change required.
- The split of `used_today` into `upload` vs `download` is informational only — the spec's correctness condition is on the SUM. The ratio approach is for future-proofing per the operator's note that "we do it in a standard approach for future improvement".
