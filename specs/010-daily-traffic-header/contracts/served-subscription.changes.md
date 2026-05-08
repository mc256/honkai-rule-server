# Contract Changes: Served Subscription Response

**Feature**: 010-daily-traffic-header
**Date**: 2026-05-01
**Supersedes for the public header**: [001-subscription-aggregator FR-011](../../001-subscription-aggregator/spec.md#fr-011)
**Math anchor**: [001-subscription-aggregator FR-011b](../../001-subscription-aggregator/spec.md#fr-011b)

This document is a delta against the served-subscription contract established by feature 001. It is *not* a full reissue — only the lines that change are listed.

---

## What changes

### `Subscription-Userinfo` response header — values

**Before** (per 001 FR-011): the header carries the per-source-summed raw aggregates: `upload`, `download`, and `total` are summed across every source that supplied userinfo, and `expire` is the earliest non-zero `expire` across those sources (or `0` when every source reported `expire=0`).

**After** (per 010 FR-001 + FR-002): the header carries a daily-spendable encoding:

- `total − upload − download` equals the **daily allowance** as defined by 001 FR-011b: per-source weighted per-day rate over sources where `expire_i > now_unix`, plus no-expiry remaining sum over sources where `expire_j == 0`. Sources whose `expire > 0` is in the past contribute `0`. Per-source remaining is clamped to `0` (no negative contributions).
- `expire` equals the next 00:00 UTC strictly after the request time.

**Recommended encoding** (per 010 FR-003): `upload=0; download=0; total=<daily_allowance_bytes>; expire=<next_midnight_utc_unix>`. Alternative encodings preserving the `total − upload − download = daily_allowance` invariant are permitted.

### `Subscription-Userinfo` response header — omission

**Before**: header always emitted in 200 responses (the existing nil-check at `internal/output/subscription_mode.go:137` is structurally there but never trips because `AggregateSubscriptionUserinfo` returns a non-nil zero struct on empty input — so the de-facto behavior was to emit `upload=0; download=0; total=0; expire=0`).

**After** (per 010 FR-006): header is **omitted entirely** when no source contributed `Subscription-Userinfo`. Clients that key off header presence will see a missing header rather than a misleading "zero quota" advertisement.

### Wire format

**Unchanged**. The header remains `upload=<bytes>; download=<bytes>; total=<bytes>; expire=<unix_seconds>` (integers, semicolon-space separated). No new fields, no new format. Stock Mihomo / Clash client UIs render the new values without modification.

---

## What does NOT change

- `Profile-Update-Interval` response header — unchanged (per 001 FR-011a).
- `Content-Type` response header — unchanged (`application/yaml; charset=utf-8` or the conventional Clash type).
- `Cache-Control` response header — unchanged (`no-store, no-cache, must-revalidate`).
- `200` response body bytes — unchanged. Snapshot bytes for the served body do not change in this feature.
- `503` bootstrap responses — unchanged (per 001 FR-003b).
- `401` token-rejection responses — unchanged (per 001 FR-014, FR-019b).
- `/health` JSON shape — unchanged. The `dailyAllowance` object still has three components (`perDayRateBytes`, `noExpiryRemainingBytes`, `expiredSourceFlags`); the raw-aggregate `upload/download/total/expire` figures still appear under their existing key. Operators see exactly the same `/health` JSON they see today.

---

## Verification

After deploy:

```bash
TOKEN="<token>"
URL="https://<host>/<prefix>/?token=${TOKEN}"

# Header presence and shape
curl -fsS -A "Bronya/1.0" -D - -o /dev/null "$URL" | grep -i '^subscription-userinfo:'
# Expected: Subscription-Userinfo: upload=0; download=0; total=<N>; expire=<U>

# Cross-check vs. /health (intra-cluster)
curl -fsS http://honkai-rule-server.cms.svc:8080/health | jq '.dailyAllowance'
# Expected: { perDayRateBytes: <P>, noExpiryRemainingBytes: <Q>, expiredSourceFlags: [...] }
# Sanity: <N> from the header should equal <P> + <Q>.
```

If `<N> != <P> + <Q>`, that's a bug — both are derived from the same `ComputeDailyAllowance` call against the same per-source userinfo snapshot; they should agree to the byte.

---

## Backward compatibility for clients

- **Stock Mihomo / Clash clients**: render the new values as "remaining = N" and "expires in <24h". No client-side change.
- **Clients that parsed the previous raw aggregates as a budget figure**: see a smaller, more honest number after their next subscription refresh. Behavior is otherwise identical (request shape, auth, body format, body content).
- **Clients that depended on the de-facto `upload=0; download=0; total=0; expire=0` no-userinfo behavior**: see the header omitted entirely. Treat absence as "metadata unavailable", not "zero quota". A `Subscription-Userinfo` header is now strictly informative — its absence is not an error.

No bump of an explicit version number is needed; the served subscription is consumed by clients that have always tolerated header drift across upstream subscription providers.
