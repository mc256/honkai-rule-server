# Contract Changes: Served Subscription Response (011)

**Feature**: 011-daily-spend-tracking
**Date**: 2026-05-02
**Supersedes**: [010 FR-002](../../010-daily-traffic-header/spec.md#fr-002) (the "expire = next 00:00 UTC" rule) AND [010 FR-003](../../010-daily-traffic-header/spec.md#fr-003)'s recommended encoding (`upload=0; download=0; total=allowance`)

This document is a delta against the served-subscription contract. Only the lines that change vs. 010 are listed.

---

## What changes

### `Subscription-Userinfo` response header — values

**Before (010)**: `upload=0; download=0; total=<daily_allowance>; expire=<next_00:00_UTC>`. The `total - upload - download` invariant equals the daily allowance; the bar in the client UI stays at 0% used all day.

**After (011)**: `upload=<U>; download=<D>; total=<T>; expire=<E>` where:
- `U + D = used_today` (sum of clamped per-source deltas since today's local midnight).
- `T = allowance_today + used_today` (so `remaining = T - U - D = allowance_today - used_today`).
- `E = unix(next 00:00 in America/Toronto local time)` (NOT UTC).
- The `U`/`D` split mirrors the upstream upload/download ratio captured at the local-midnight rollover (per source, weighted by per-source `used_today` contribution). For sources with zero historical upload, the ratio is 0 (everything attributed to download).

### Wire format

**Unchanged**. Still `upload=<bytes>; download=<bytes>; total=<bytes>; expire=<unix_seconds>`. Stock Mihomo / Clash client UIs render the new values without any client-side change.

### Header omission rule

**Unchanged from 010 FR-006**. When no source contributed userinfo, the response carries no `Subscription-Userinfo` header.

### Overflow case (new — not previously possible under 010)

When `used_today > allowance_today` (user spent more than today's budget — possible with aggressive consumption or a stale snapshot), `total - upload - download` may be negative. **Acceptable per operator decision** (spec acceptance scenario 3 / SC-005); stock client UIs render the bar over-full and continue working without errors.

---

## What does NOT change

- **`Profile-Update-Interval` response header**: unchanged (per 001 FR-011a).
- **`Content-Type` / `Cache-Control` headers**: unchanged.
- **200 response body bytes**: unchanged. Snapshot suite for the served body does not move.
- **503 bootstrap responses**: unchanged (per 001 FR-003b).
- **401 token-rejection responses**: unchanged (per 001 FR-014, FR-019b).
- **`/health` JSON shape**: unchanged. The `dailyAllowance` triple still has its three components; the `aggregatedSubscriptionUserinfo` block still has the raw aggregates. The snapshot is NOT exposed on `/health` — debuggability is via the new structured log fields per FR-011 and via direct snapshot-file inspection per the quickstart.

---

## Verification

After deploy:

```bash
TOKEN="<token>"
PREFIX="<prefix>"

# Header presence and shape
curl -fsS -A "Bronya/1.0" -D - -o /dev/null \
  "https://example.com/${PREFIX}/?token=${TOKEN}" \
  | grep -i '^subscription-userinfo:'
# Expected: Subscription-Userinfo: upload=<U>; download=<D>; total=<T>; expire=<E>
# Where U + D == used_today (grows through the day), T == allowance + used_today,
# E == unix(next 00:00 America/Toronto)

# Cross-check via /health (intra-cluster)
kubectl --context cluster -n cms exec deploy/honkai-rule-server -- \
  wget -qO- http://localhost:8080/health \
  | jq '.dailyAllowance'

# Sanity: served T - (U + D) ≈ dailyAllowance.perDayRateBytes + .noExpiryRemainingBytes
# (exact identity at midnight; within rounding through the day).
```

---

## Backward compatibility

- **Stock Mihomo / Clash clients**: render the new values transparently — no per-client change. The bar in the usage display now actually fills as the user consumes traffic (was static at 0% under 010).
- **Clients that depended on `upload == 0; download == 0` from 010**: that was an undocumented implementation detail of the encoding choice. Treat the wire format per 001 FR-005b's contract: integer values, semicolon-space separated. The numeric values evolve.
- **Clients that compute `remaining = total - upload - download` and use it as a daily budget**: still correct under 011. The number now decreases through the day (was static under 010), which matches user expectation.
- **Clients running on a non-Toronto timezone but configured against this server**: the `expire` value is in absolute Unix seconds, so the client converts to its own local time correctly. The "expires in N hours" countdown the client UI shows will reflect the correct relative time. Only the Toronto operator's mental model of "today" maps directly to `expire`'s day-boundary.

No bump of an explicit version number is needed; the served subscription is consumed by clients that have always tolerated value drift across upstream subscription providers.
