# Feature Specification: Daily-Available Traffic in Served Subscription Header

**Feature Branch**: `010-daily-traffic-header`
**Created**: 2026-05-01
**Status**: Draft
**Input**: User description: "I think there was some issues on the data amount returned by the server. As I mentioned in the previous requirement, we want to not just calculate the sum of remaining traffic, we want to assume that the traffic will expire 'tomorrow' and calculate the daily available traffic"

**Anchor**: This feature surfaces the **daily allowance** figure already defined and computed by [`001-subscription-aggregator/spec.md` FR-011b](../001-subscription-aggregator/spec.md#fr-011b) on the public `Subscription-Userinfo` HTTP response header, replacing the raw sum-of-remaining values that header currently carries (per 001 FR-011). The math is unchanged; only the transport surface changes.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Display today's available traffic in the client UI (Priority: P1)

The user wants their proxy client (Mihomo / Clash and the various forks built on top of it) to display a number that means "the bytes I can spend today" — not "the sum of all remaining quota across every upstream provider". Today the served subscription advertises the raw sum-of-remaining as the public traffic figure; that number overstates daily-spendable quota because providers expire on different dates and the user can't burn it all at once. The user wants the figure shown in the standard client UI to reflect the per-day-spendable budget instead, so glancing at the usage bar communicates "I should pace myself today" or "I can stream a movie".

To make stock client UIs render this without any per-client configuration, the server reuses the existing `Subscription-Userinfo` header convention but swaps in (a) the daily-allowance figure as effective-remaining and (b) "tomorrow" as the expiry — so the client's built-in usage display reads "X bytes remaining, expires tomorrow", which matches the daily-budgeting mental model the user actually uses.

**Why this priority**: This is the only user story. It corrects a misleading number that every client sees and is required for the served subscription to deliver its intended budgeting value. The internal computation already exists (the `DailyAllowance` figure defined by 001 FR-011b is exposed today on the health surface); this feature wires that figure into the client-visible header.

**Independent Test**: Configure two upstream subscriptions with known per-source traffic metadata where the per-source-weighted daily allowance is well-defined (e.g., source A `total=200GB / used=50GB / expire=now+30d`, source B `total=100GB / used=20GB / expire=now+5d` → daily allowance = 21 GB/day per 001 FR-011b's worked example). Fetch the served subscription. Assert the response `Subscription-Userinfo` header's `total − upload − download` equals 21 GB ± rounding and `expire` equals the next 00:00 UTC after the request time, regardless of how the figure is split between `total`, `upload`, and `download`.

**Acceptance Scenarios**:

1. **Given** sources A (`total=200GB / used=50GB / expire=now+30d`) and B (`total=100GB / used=20GB / expire=now+5d`), **When** a client fetches the served subscription, **Then** the response `Subscription-Userinfo` header reports `total − upload − download = 21 GB ± rounding` (= 5 GB/day from A + 16 GB/day from B per 001 FR-011b) and `expire` equals the next 00:00 UTC after the request time.
2. **Given** every upstream is on a no-expiry plan (every source reports `expire=0`), **When** a client fetches, **Then** the served `total − upload − download` equals the sum of `max(0, total_j − upload_j − download_j)` across the no-expiry sources (the no-expiry pool is treated as freely spendable today) and `expire` still equals the next 00:00 UTC.
3. **Given** one source is no-expiry and another has `expire=now+5d`, **When** a client fetches, **Then** the served `total − upload − download` equals `(no-expiry remaining) + (per-day rate of expiring sources)` and `expire` equals the next 00:00 UTC.
4. **Given** every upstream's `expire` is in the past (operator hasn't renewed any source), **When** a client fetches, **Then** the served `total − upload − download` is exactly `0` (not negative, not stale, not Infinity) and `expire` still equals the next 00:00 UTC; the expired sources surface on the health surface so the operator notices.
5. **Given** an upstream's `Subscription-Userinfo` is missing entirely (per 001 FR-012), **When** a client fetches, **Then** the missing source contributes nothing (no fake-zero) and the served daily-allowance figure is computed from the remaining sources.
6. **Given** a fixed set of upstream traffic figures and a fixed request time, **When** the same set is served twice without input changes, **Then** the served `Subscription-Userinfo` header bytes are identical (the "tomorrow" rule is deterministic — see FR-005).
7. **Given** the operator inspects the health surface, **When** they read traffic metadata, **Then** they see *both* the raw aggregated `upload/download/total/expire` (sum-of-remaining + earliest non-zero expire — the figures previously served on the public header) *and* the three-component `DailyAllowance` from 001 FR-011b — sufficient to diagnose a misreported number without opening upstream payloads.

### Edge Cases

- **Daily allowance is zero** (every source either expired or fully consumed): served `total − upload − download` MUST be exactly `0` and `expire` MUST still be the next 00:00 UTC; the client UI shows the usage bar full and "expires tomorrow", correctly conveying "no spendable budget today".
- **A source's used bytes exceed its total** (provider returned an inconsistent snapshot): the per-source remaining MUST be clamped to `0` (it MUST NOT subtract negatively from the daily allowance); the condition MUST be recorded in logs. (This invariant already lives in 001 FR-011b's `max(0, …)` clause.)
- **Bootstrap window** (server hasn't completed first-fetch on every source): the served endpoint already returns 503 until ready (per 001 FR-003b); this feature does NOT change that. Once warm, the daily-allowance encoding follows the same semantics whether all sources or only some have data.
- **All sources have no-expiry plans**: served `expire` MUST still equal the next 00:00 UTC (uniform UX), even though internally the no-expiry pool isn't time-bound. The health surface MUST continue to distinguish per-day-rate (`0` in this case) vs. no-expiry-remaining (`Σ remaining`) per 001 FR-011b.
- **Request time near midnight UTC**: the "tomorrow" rule MUST be deterministic and well-defined for any request time, including 23:59:59 UTC (the expire deadline may be a second away — that's fine; the next client refresh picks up the new day's budget on its next refresh interval).
- **Same `Subscription-Userinfo` snapshot served many times within seconds**: the header value MUST be recomputed from current inputs and the current request time per request (consistent with 001 FR-011b's "recomputed per request"). The response body MAY remain cached across requests within the same second; only the header recomputes.
- **Aggregated traffic metadata totally absent** (every source omitted `Subscription-Userinfo`): the server MUST omit the `Subscription-Userinfo` response header entirely (consistent with 001 FR-012's "missing source contributes nothing" rule); it MUST NOT emit a fake `total=0; upload=0; download=0; expire=<tomorrow>` that would falsely advertise "you have no quota at all".

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: When serving the merged subscription, the server MUST emit a `Subscription-Userinfo` response header whose `total − upload − download` equals the **daily allowance** as defined in 001 FR-011b: the per-source weighted per-day rate over sources where `expire_i > now_unix`, plus the no-expiry remaining sum over sources where `expire_j == 0`. Sources whose `expire > 0` is in the past MUST contribute `0`. Per-source remaining MUST be clamped to `0` before contributing (no negative contributions).

- **FR-002**: The same response header MUST set `expire` to a "tomorrow" Unix timestamp, defined as the **next 00:00 UTC strictly after the current request time**. Examples: a request at `2026-05-01 23:59:30 UTC` produces `expire = unix(2026-05-02 00:00:00 UTC)`; a request at `2026-05-01 00:00:01 UTC` produces `expire = unix(2026-05-02 00:00:00 UTC)`; a request exactly at `2026-05-01 00:00:00 UTC` produces `expire = unix(2026-05-02 00:00:00 UTC)` (strictly after).

- **FR-003**: The on-the-wire format of the `Subscription-Userinfo` header MUST remain `upload=<bytes>; download=<bytes>; total=<bytes>; expire=<unix_seconds>` (integer values, semicolon-space separated) so stock Mihomo / Clash client UIs render it without modification (continuing 001 FR-011's wire-format contract). The exact split among `upload`, `download`, and `total` is unconstrained provided the invariant `total − upload − download = daily_allowance` holds. The recommended encoding is `upload=0; download=0; total=<daily_allowance>; expire=<tomorrow_unix>`; alternative encodings (e.g., preserving aggregated historical usage in `upload`/`download`) are permitted as long as the remaining-byte invariant holds.

- **FR-004**: This feature **supersedes 001 FR-011's behavior** for the served `Subscription-Userinfo` header. The raw aggregated `upload/download/total/expire` figures (sum-of-remaining + earliest non-zero expire) MUST remain available on the health surface so operators can still inspect both views without opening upstream payloads. 001 FR-011's wire format remains the contract; only the *values* embedded in that wire format change.

- **FR-005**: The served `Subscription-Userinfo` header MUST be deterministic given fixed inputs and a fixed request time, so snapshot tests stay reproducible (consistent with Constitution Principle II and 001 FR-004's determinism contract). The "tomorrow" computation MUST use a clock interface that test code can inject (the existing `clock.Clock` injection pattern already used by `ComputeDailyAllowance`).

- **FR-006**: When every source's `Subscription-Userinfo` is absent (no source contributes traffic metadata), the server MUST omit the `Subscription-Userinfo` response header entirely. It MUST NOT emit `total=0; upload=0; download=0; expire=<tomorrow>` in that case, because that would falsely advertise "zero spendable quota" rather than "metadata not available".

- **FR-007**: The `Profile-Update-Interval` header (per 001 FR-011a) is NOT changed by this feature; the existing aggregation rule and value continue to apply. Clients honoring `Profile-Update-Interval` will refresh on their cadence and pick up the new day's budget on each refresh.

- **FR-008**: Logs MUST include the served daily-allowance figure (in bytes) and the served `expire` value on each request that returns a body, alongside the existing per-request fields (token hash per 001 FR-014, source counts, etc.). At debug verbosity the per-source contributions to the daily allowance (per-day rate per source, no-expiry remaining per source, expired-source flags) MUST be loggable for operator troubleshooting.

### Key Entities

- **Daily Allowance (served)**: The single bytes-per-day figure carried as `total − upload − download` on the public `Subscription-Userinfo` response header. Computed per request from current upstream traffic metadata and the current request time. The math is exactly 001 FR-011b: per-source weighted per-day rate over expiring sources plus no-expiry remaining sum. This feature does not change the math, only the transport.
- **Tomorrow timestamp**: A Unix timestamp equal to the next 00:00 UTC strictly after the current request time. The canonical `expire` value embedded in the served `Subscription-Userinfo` header, regardless of any upstream `expire` value.
- **Raw aggregates (health-only)**: The previously-served sum-of-remaining `upload/download/total` and earliest-non-zero `expire`. After this feature, these continue to be computed but are no longer carried on the public `Subscription-Userinfo` response header; they remain on the health surface for operator debugging.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For the canonical two-source fixture from 001 FR-011b (source A `total=200GB / used=50GB / expire=now+30d`; source B `total=100GB / used=20GB / expire=now+5d`), the served `Subscription-Userinfo` header's `total − upload − download` equals **21 GB ± rounding** on every request, regardless of when within the day the request arrives.
- **SC-002**: For the same fixture, the served `expire` Unix timestamp equals `unix(next_midnight_utc(now))` on every request.
- **SC-003**: When the user opens a stock Mihomo / Clash client UI configured with the served subscription endpoint, the displayed remaining-traffic value equals the daily allowance (e.g. ~21 GB for the canonical fixture) and the displayed expiry reads "tomorrow" / "<24h" / equivalent — without any per-client configuration.
- **SC-004**: When every source's `expire` is in the past, the served header's `total − upload − download` reads exactly `0` and the served `expire` still reads the next 00:00 UTC; no negative or Infinity value is ever served.
- **SC-005**: When every source is on a no-expiry plan, the served header's `total − upload − download` equals the sum of remaining bytes across those sources, and the health surface continues to distinguish per-day-rate (`0`) from no-expiry-remaining (`Σ remaining`) per 001 FR-011b.
- **SC-006**: Two requests served within the same UTC calendar day against the same upstream snapshot return the same `Subscription-Userinfo` header bytes (the "tomorrow" timestamp does not advance second-by-second; it advances at the day boundary).
- **SC-007**: When every source's `Subscription-Userinfo` is absent, the response carries no `Subscription-Userinfo` header (rather than a misleading "zero quota" value).
- **SC-008**: The operator can read both the raw aggregated `upload/download/total/expire` figures and the daily-allowance components from the health surface in a single request — sufficient to diagnose a misreported header value without opening upstream payloads.

## Assumptions

- Stock Mihomo / Clash clients render "remaining = total − upload − download" in their usage bar and treat `expire` as a wall-clock deadline. This is the convention 001 FR-011 already relies on; this feature does not change that contract, only which numbers go into those fields.
- "Tomorrow" is canonically the next 00:00 UTC strictly after the request time. The user phrased the requirement as "the traffic will expire tomorrow" without committing to a timezone; UTC is the conservative default consistent with how the server already handles upstream `expire` timestamps (Unix seconds, no timezone). A future feature may add an operator-configurable timezone override; out of scope here.
- The recommended encoding (`upload=0; download=0; total=daily_allowance; expire=tomorrow`) is compatible with stock client UIs. Implementations MAY choose an alternative encoding (e.g., to preserve aggregated historical usage in `upload`/`download`) provided the remaining-byte invariant holds; both are acceptable.
- The internal `DailyAllowance` computation (001 FR-011b, already implemented in `internal/merge/traffic.go::ComputeDailyAllowance`) is correct and remains the canonical source of the daily-allowance figure. This feature does not modify that math; it only changes which transport surface (public response header vs. health-only) exposes the figure.
- Operators want both views (raw aggregates and daily allowance) on the health surface for debugging. Raw aggregates are not removed from the health surface — only from the public `Subscription-Userinfo` response header.
- Test coverage: existing `ComputeDailyAllowance` unit tests cover the math; the new behavior is additive at the output / header-emission layer and is testable with a small set of fixture-driven snapshots that inject a fixed clock.
- Clients on the previous header semantics will not see an error or break — they'll simply see a different (smaller, more honest) remaining-traffic number after the next subscription refresh. No client-side migration is required.
