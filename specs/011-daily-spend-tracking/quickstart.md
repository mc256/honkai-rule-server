# Quickstart: Today's-Spend Tracking in Served Subscription-Userinfo

**Feature**: 011-daily-spend-tracking
**Audience**: Operators of a deployed honkai-rule-server (e.g., the cluster)

This guide assumes the change has shipped and you want to verify it in a live deployment, troubleshoot a misreported value, or understand what the client UI now displays.

---

## What changed for clients

The client UI's usage bar now fills up over the course of the day as you consume traffic, instead of staying flat at 0% (the 010 behavior). The "remaining" number still tracks today's budget; the "used" number now tracks what you've actually spent since today's local midnight.

**"Today" is America/Toronto local time**, not UTC. The daily-budget boundary fires at 00:00 Toronto time — which is 04:00 UTC during EST (Nov–Mar) or 04:00 UTC during EDT (Mar–Nov, when EDT is UTC-4). DST transitions are handled correctly by the IANA timezone database.

---

## Verify the served header

Bronya / Mihomo client UA is required by the existing `HONKAI_RULE_CLIENT_UA=Bronya/` access-control filter.

```bash
TOKEN="<your-token>"
PREFIX="<your-32-char-hex-path-prefix>"   # e.g. 965ac4f224a1ab9930fab004e16b9fd7
URL="https://example.com/${PREFIX}/?token=${TOKEN}"

curl -fsS -A "Bronya/1.0" -D - -o /tmp/body.yaml "$URL" | grep -iE '^(subscription-userinfo|profile-update-interval):'
```

Expected output shape:

```text
Subscription-Userinfo: upload=12345; download=87654; total=22660578304; expire=1746230400
Profile-Update-Interval: 24
```

Where:
- `upload + download = 100,000` bytes (today's spend so far — grows through the day, resets at next 00:00 Toronto).
- `total = 22,660,578,304` bytes ≈ allowance (~21 GiB) + today's spend.
- `expire = 1,746,230,400` = unix(next 00:00 America/Toronto).
- `Profile-Update-Interval` unchanged from 010 (12 hours by default).

If `upload` and `download` are both 0, no spending has happened yet today — issue some traffic through the proxy and re-check. If they STAY at 0 for hours, see "Bar stuck at 0%" in Troubleshooting below.

---

## Inspect the snapshot file

The runtime image is `FROM scratch` (no shell), so direct exec doesn't work. Use a busybox helper pod that mounts the same PVC:

```bash
kubectl --context cluster -n cms apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: honkai-snap-inspect
  namespace: cms
spec:
  restartPolicy: Never
  containers:
  - name: inspect
    image: busybox:latest
    command: ["sh","-c","cat /data/today-zero.json && echo && sleep 10"]
    volumeMounts:
    - name: honkai-data
      mountPath: /data
  volumes:
  - name: honkai-data
    persistentVolumeClaim:
      claimName: honkai-rule-server-data
EOF

sleep 3 && kubectl --context cluster -n cms logs honkai-snap-inspect
kubectl --context cluster -n cms delete pod honkai-snap-inspect --wait=false
```

Expected JSON shape:

```json
{
  "snapshot_local_date": "2026-05-02",
  "snapshot_unix": 1746158400,
  "allowance_today_bytes": 22548578304,
  "baselines": {
    "alpha":     242977323837,
    "beta":  15025246189
  },
  "baseline_upload_ratios": {
    "alpha":     0.10,
    "beta": 0.12
  }
}
```

`snapshot_local_date` should match today in America/Toronto. If it's yesterday's date, the rollover hasn't fired yet — issue any served request and re-inspect.

---

## Cross-check via `/health`

```bash
kubectl --context cluster -n cms exec deploy/honkai-rule-server -- \
  wget -qO- http://localhost:8080/health | jq '.dailyAllowance'
```

Sanity: `served total - (served upload + served download)` should equal `dailyAllowance.perDayRateBytes + .noExpiryRemainingBytes` to within rounding. Both numbers come from `ComputeDailyAllowance` evaluated at slightly different instants (snapshot capture vs. /health request); they match exactly when no rollover has happened between the two requests.

---

## Force a rollover (for testing)

Edit the snapshot file's `snapshot_local_date` to a past date, then issue any served request — the rollover fires and the snapshot regenerates from current upstream state.

```bash
# Inside the helper pod (same overlay as above):
sed -i 's/"snapshot_local_date": "[^"]*"/"snapshot_local_date": "2020-01-01"/' /data/today-zero.json

# Then from your machine:
curl -fsS -A "Bronya/1.0" "$URL" > /dev/null

# Re-inspect — snapshot_local_date should now be today.
```

Useful for verifying the rollover path works end-to-end. After regeneration, `served upload + download` resets to 0 (with a fresh allowance for today).

---

## Read the structured logs

Per FR-011, every served-subscription request logs four new fields:

```bash
kubectl --context cluster -n cms logs deploy/honkai-rule-server --tail=20 \
  | grep 'served subscription'
```

Each line carries:
- `served_used_today_bytes` — what's in `upload + download` of the response header.
- `served_total_bytes` — what's in `total` of the response header.
- `snapshot_local_date` — which day the snapshot reflects.
- `rollover_fired` — `true` if this request triggered the lazy rollover (otherwise `false`).

If `rollover_fired=true` shows up multiple times in a single day for the same source set, that's a bug — there should be at most one rollover per local-day per pod.

---

## Troubleshooting

### "The bar stays at 0% all day even though I'm using traffic"

Likely causes:
1. **Upstream provider isn't reporting incrementing `cumulative_used`**: some providers update their counters once an hour or once a day. Check `/health.aggregatedSubscriptionUserinfo.upload` over time — if it doesn't grow, the provider is the issue.
2. **Snapshot file's `Baselines` were captured AT a non-midnight rollover** (corruption recovery): the baselines reflect the corruption-recovery instant, not actual midnight. The bar fills correctly from that point forward but the displayed "used" doesn't match what you'd expect for a true day. Wait for the next real local-midnight rollover.
3. **Pod recently restarted and snapshot was lost**: shouldn't happen (snapshot persists on PVC), but if the PVC was reset, the next request reinitializes baselines from current upstream state, so `used = 0` until next midnight.

### "The pod won't start after I changed `DAILY_BUDGET_TIMEZONE`"

Likely the env var value isn't a valid IANA timezone name (per FR-004a equivalent here — loud-fail per Constitution Principle III). Check:

```bash
kubectl --context cluster -n cms describe pod -l app.kubernetes.io/name=honkai-rule-server | tail -30
```

Should show a structured error log identifying the offending value. Fix the chart, push, redeploy.

Valid examples: `America/Toronto`, `America/New_York`, `Europe/London`, `UTC`, `Asia/Tokyo`. Validate with `TZ=<name> date` on any machine.

### "The snapshot file got corrupted"

Per FR-007, the server self-heals: at the next request, it logs `dailyspend snapshot file corrupted — reinitializing` (INFO level) and writes a fresh snapshot from current upstream state. The user-visible side effect is `used` resetting to 0 for the rest of today.

If this happens repeatedly (more than once per pod-lifetime), file a bug — the persistence layer is misbehaving.

### "I want to clear today's spend display without restarting the pod"

Same pattern as "Force a rollover" above — edit the snapshot's `snapshot_local_date` to a past date and issue one request. The rollover fires and `used` resets to 0.

### "The expire timestamp is in UTC, not Toronto"

Means `DAILY_BUDGET_TIMEZONE` env var was overridden to `UTC` or wasn't picked up. Check `kubectl describe deploy ... | grep DAILY_BUDGET_TIMEZONE` — if it's missing or set to `UTC`, the chart needs the right value.

---

## Edge cases worth knowing

- **All upstream subscriptions are no-expiry plans** (`expire == 0` on every source): `allowance_today` comes from `noExpiryRemainingBytes` (the "freely spendable today" pool). The snapshot still rolls over daily; today's allowance is recomputed from the now-smaller no-expiry pool. The pool naturally drains over time.
- **All upstream subscriptions are expired** (cluster-wide oversight): `allowance_today = 0`. Served `total = used_today` and `remaining = 0` once anything is spent. The bar shows 100% used immediately. Operator-visible: `/health.dailyAllowance.expiredSourceFlags` lists the offending sources; that's where to look.
- **Pod restart mid-day**: snapshot persists on PVC. Next request loads it; `used_today` reflects the correct delta from the captured baselines. No visual discontinuity.
- **DST transition in America/Toronto** (Spring Forward / Fall Back): one 23-hour day or one 25-hour day. Rollover fires exactly once at the correct local-midnight; `expire` after the transition correctly points at the next local 00:00 (which is 23 or 25 hours away in absolute time).
- **A client polling every hour over a full day**: each fetch sees an incrementing `upload + download` and a stable `total`. The "expires in N hours" countdown decreases through the day, then resets at the local-midnight boundary along with `used`.
- **Snapshot writes when the PVC is full**: `Save` returns an error which is logged WARN. The next request retries the save. The served header for the failing request is still correct (the in-memory snapshot was used for header composition). Operator should monitor PVC usage; one snapshot is ~200 bytes, so the failure mode is essentially impossible at the project's scale.
