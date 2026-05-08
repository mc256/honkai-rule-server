# Quickstart: Daily-Available Traffic in Served Subscription Header

**Feature**: 010-daily-traffic-header
**Audience**: Operators of a deployed honkai-rule-server (e.g., the cluster)

This guide assumes the change has shipped and you want to verify it in a live deployment, troubleshoot a misreported figure, or understand what the client UI displays.

---

## What changed for clients

The number your Mihomo / Clash client displays as "remaining traffic" is now **today's spendable budget** (the per-source-weighted per-day allowance) rather than the **sum of all remaining quota across every upstream provider**. The `expire` your client UI shows now points at the next 00:00 UTC, regardless of when individual upstream subscriptions actually expire.

The intent: the usage bar should communicate "should I pace myself today?" rather than "I have so much I'll never run out". The math is unchanged from 001 FR-011b — only which figure goes into the public header changed.

---

## Verify the served header

Bronya / Mihomo client UA is required by the `HONKAI_RULE_CLIENT_UA=Bronya/` access-control filter (see 003-custom-rules-access-control).

```bash
TOKEN="<your-token>"
PREFIX="<your-32-char-hex-path-prefix>"   # e.g. 965ac4f224a1ab9930fab004e16b9fd7
URL="https://example.com/${PREFIX}/?token=${TOKEN}"

curl -fsS -A "Bronya/1.0" -D - -o /tmp/body.yaml "$URL" | grep -iE '^(subscription-userinfo|profile-update-interval):'
```

Expected output shape:

```text
Subscription-Userinfo: upload=0; download=0; total=22548578304; expire=1746230400
Profile-Update-Interval: 24
```

Where:
- `total = 22_548_578_304` is the daily allowance in bytes (= 21 GiB for the canonical FR-011b fixture).
- `expire = 1_746_230_400` is `unix(2026-05-03 00:00:00 UTC)` if your request was during 2026-05-02 UTC.
- `upload = 0` and `download = 0` always, in the recommended encoding.

If the `Subscription-Userinfo` header is **absent**, that's the FR-006 case: no upstream contributed traffic metadata in the current cache snapshot. Check `/health` to see which sources are missing userinfo (next section).

---

## Cross-check via `/health`

The `/health` endpoint is unauthenticated by design but exposed only on the cluster-internal Service (not via the public Ingress) — so you check it from inside the cluster, not from the public URL.

```bash
kubectl --context cluster -n cms exec -it deploy/honkai-rule-server -- \
  wget -qO- http://localhost:8080/health | jq '.dailyAllowance, .aggregatedSubscriptionUserinfo'
```

Expected fields:

```json
{
  "perDayRateBytes": 22548578304,        // contribution from sources with expire>0
  "noExpiryRemainingBytes": 0,            // contribution from sources with expire=0
  "expiredSourceFlags": []                // sources whose expire is in the past
}
{
  "upload": 75161927680,                  // raw aggregated upload across all sources
  "download": 0,                          //  …
  "total": 322122547200,                  //  …
  "expire": 1746576000                    // earliest non-zero expire across sources
}
```

**Sanity check**: the served header's `total − upload − download` should equal `dailyAllowance.perDayRateBytes + dailyAllowance.noExpiryRemainingBytes` to the byte. If they disagree, file a bug — both are derived from the same `ComputeDailyAllowance` call against the same per-source snapshot and should always agree.

The raw aggregates under `aggregatedSubscriptionUserinfo` are kept on `/health` for operator debugging — they're the values clients used to see in the public header before this feature shipped.

---

## Troubleshooting

### "The number in my client looks too small"

That's the feature working as intended. The previous figure was the **sum** of remaining quota across all upstream providers — a number you couldn't actually spend at one rate because providers expire on different dates. The new figure is **what you can spend today** without overshooting any single provider's expiry.

Cross-check via `/health.dailyAllowance` to see the per-component breakdown. If you have one provider expiring in 5 days with 80 GB remaining and another expiring in 30 days with 150 GB remaining, the daily-spendable rate is `80/5 + 150/30 = 16 + 5 = 21 GB/day`, and that's what the header reports. Spending 21 GB/day for 5 days uses up provider B and resets the math; spending more risks running provider B out before its renewal.

### "The `expire` in the header is hours away — is my subscription about to die?"

No. The header's `expire` is the synthetic "tomorrow" deadline (next 00:00 UTC). It rolls forward to the *next* tomorrow once the calendar day flips. Your actual upstream subscriptions' expiry dates are in `/health.aggregatedSubscriptionUserinfo.expire` and `/health.dailyAllowance.expiredSourceFlags`.

### "The `Subscription-Userinfo` header is missing entirely"

That means no upstream supplied a `Subscription-Userinfo` value in the current cache snapshot. Check `/health` for the per-source state, and check pod logs for `subscription-userinfo header missing` warnings. Likely causes:
- All upstream subscriptions are new (haven't completed their first fetch yet).
- All providers have changed their response shape (rare).
- The cache was wiped and is mid-warmup.

### "The figure changes between requests within seconds"

Inside the same UTC calendar day with the same upstream snapshot, the served `Subscription-Userinfo` bytes MUST be identical (010 SC-006). If you're seeing per-second drift:
- Confirm you're hitting the same pod (use `kubectl logs` to check).
- Confirm the upstream cache hasn't refreshed between requests (the bound is the slowest-source TTL).
- If the drift is real and within-day, it's a bug — file it with the request times and per-request `served_daily_allowance_bytes` log values.

### "I want to see the per-source contribution to debug a number that looks wrong"

Set the server's `LOG_LEVEL=debug` (via the chart's env), restart the pod, hit the served URL once, then read the pod logs:

```bash
kubectl --context cluster -n cms logs deploy/honkai-rule-server --tail=50 \
  | grep 'served daily allowance breakdown'
```

You'll see one line per request with `per_day_rate_bytes`, `no_expiry_remaining_bytes`, and `expired_source_flags` — same shape as `/health.dailyAllowance` but per-served-request rather than on-demand.

Don't leave `LOG_LEVEL=debug` running indefinitely; the per-request debug volume is high.

---

## Edge cases worth knowing

- **All upstream subscriptions are no-expiry plans**: `expire` in the served header is still the next 00:00 UTC; `total` is the sum of all sources' remaining bytes. `/health.dailyAllowance.perDayRateBytes` is `0` and `noExpiryRemainingBytes` carries the figure.
- **All upstream subscriptions are expired** (operator hasn't renewed any of them): served `total = 0`, `expire = next 00:00 UTC`. The client UI shows the usage bar full and "expires tomorrow" — correctly conveying "no spendable budget today". The expired sources also surface in `/health.dailyAllowance.expiredSourceFlags`.
- **Server crossing the UTC midnight boundary**: served `expire` rolls forward by one day in a single jump. Within-day SC-006 still holds; across-day determinism requires the input snapshot to be unchanged.
- **A Mihomo client polls every hour and parses the header**: each poll sees the same `total` (assuming the input snapshot is unchanged) and an `expire` that decrements toward the next UTC midnight. The displayed "expires in X hours" countdown reduces over the day, then resets at midnight.
