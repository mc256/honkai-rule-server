# Operator Quickstart: Load-Balance Region & Continent Groups

**Feature**: `014-load-balance-region-groups`
**Audience**: Operators deploying / verifying / troubleshooting this feature.
**Date**: 2026-05-08

This quickstart walks through verifying that load-balance region/continent groups, their fan-out copies, and the `LOAD_BALANCE_*` env-var configuration are working as expected after deploying the feature.

Prerequisites:
- A running `honkai-rule-server` pod with this feature deployed.
- `kubectl`, `curl`, and `yq` available locally.
- The deployed `<TOKEN>` and `<PREFIX>` (32-char hex) from your `tokens.yaml` / `URL_PATH_PREFIX`.

---

## 1. Verify the lb groups appear in the served body

Fetch the subscription and inspect every group whose name starts with `_lb_`:

```sh
curl -fsS -A "Bronya/1.0" \
  https://example.com/<PREFIX>/?token=<TOKEN> \
  | yq '.proxy-groups[] | select(.name | startswith("_lb_"))'
```

Expected output: one entry per existing url-test region / continent / `_region_UNKNOWN` group, each with:

```yaml
name: _lb_region_JP
type: load-balance
proxies:
  - alpha_jp1
  - alpha_jp2
  - beta_jp1
url: https://www.gstatic.com/generate_204
interval: 300
lazy: true
strategy: round-robin
timeout: 1500
max-failed-times: 3
```

If you see zero entries, the feature did not deploy — check `kubectl describe pod` for startup errors (see §6 below).

---

## 2. Verify the resolved configuration was logged at startup

```sh
kubectl logs deploy/honkai-rule-server -n cms | grep load_balance_params
```

Expected single line:
```text
{"event":"load_balance_params","url":"https://www.gstatic.com/generate_204","interval_seconds":300,"timeout_ms":1500,"max_failed_times":3,"lazy":true,"strategy":"round-robin"}
```

This is the resolved configuration — defaults + any operator overrides. If a `LOAD_BALANCE_*` env var was set in the chart and the value here doesn't reflect it, your override is not in effect (likely the env block was not threaded through your Helm template).

The url-test counterpart (`url_test_params`, from 012) appears as a separate line; both should be present.

---

## 3. Override a parameter

Bump `interval` to 600 seconds (less aggressive probing) via the chart's env block. In your `values.yaml` for the `honkai-rule-server` chart:

```yaml
honkai:
  env:
    LOAD_BALANCE_INTERVAL_SECONDS: "600"
```

Sync the Argo CD app (or `helm upgrade`), wait for the pod to restart, then re-run §2:

```text
{"event":"load_balance_params",...,"interval_seconds":600,...}
```

And re-run §1 — every `_lb_*` group should now show `interval: 600`.

To switch strategy:
```yaml
honkai:
  env:
    LOAD_BALANCE_STRATEGY: "consistent-hashing"
```

Pod restart → §1 shows `strategy: consistent-hashing` on every lb group.

---

## 4. Verify the lb fan-out is generated

For every operator-declared own-proxy without an explicit `dialer-proxy`, you should see one `via_lb_region_<CC>__<own>` per emitted lb region group, and one `via_lb_continent_<CONT>__<own>` per emitted lb continent group:

```sh
curl -fsS -A "Bronya/1.0" \
  https://example.com/<PREFIX>/?token=<TOKEN> \
  | yq '.proxies[] | select(.name | test("^via_lb_"))'
```

Expected output: each entry has `dialer-proxy:` matching its name (`via_lb_region_JP__markham` → `dialer-proxy: _lb_region_JP`). The remaining fields are copied verbatim from the source own-proxy.

Sanity-check counts:
```sh
curl -fsS -A "Bronya/1.0" \
  https://example.com/<PREFIX>/?token=<TOKEN> \
  | yq '[.proxies[].name | select(startswith("via_lb_"))] | length'
```

Expected: `N × M` where N = own-proxy count (without explicit `dialer-proxy`), M = lb-group count (= url-test-group count).

---

## 5. Real-client load-balance smoke test

Deploy your Mihomo client against the new served subscription. Pick `_lb_region_JP` (or another lb group) as the active proxy in the global selector, or write a custom rule targeting it:

```text
DOMAIN-SUFFIX,jp-only-service.com,_lb_region_JP
```

Open multiple parallel connections to the targeted service (e.g., a few browser tabs hitting it simultaneously, or `curl --parallel`). In Mihomo's UI / dashboard, observe the connection log — the first hop should distribute across different members of `_lb_region_JP` per the configured `strategy`:

- `round-robin`: each new connection picks the next member in rotation.
- `consistent-hashing`: connections to the same destination IP/SNI go through the same member; different destinations spread.
- `sticky-sessions`: connections from the same source go through the same member; different sources spread.

If all connections pin to one member, the strategy is misapplied or only one member is healthy — check Mihomo's group-health view for probe status.

---

## 6. Troubleshoot bad configuration

### Symptom: pod fails to start

```sh
kubectl describe pod -l app=honkai-rule-server -n cms
```

Look for the `Events` section. The pod's main container exited with `LoadBalanceParams validation failed: ...`. Possible messages:

| Error fragment | Cause | Fix |
|---|---|---|
| `LOAD_BALANCE_INTERVAL_SECONDS="abc" (must be a positive integer)` | Non-integer value. | Set to an integer ≥ 1. |
| `LOAD_BALANCE_INTERVAL_SECONDS=-5 (must be >= 1)` | Negative or zero. | Set to ≥ 1. |
| `LOAD_BALANCE_LAZY="maybe" (must be true or false)` | Not a bool. | Use `true` / `false`. |
| `LOAD_BALANCE_STRATEGY="random" (must be round-robin, consistent-hashing, or sticky-sessions)` | Unknown strategy. | Use one of the three. |

Multiple errors accumulate into one message — fix all in one redeploy.

### Symptom: lb groups missing from served body

If §1 returns nothing but §2 shows the expected log, you may be hitting an old cached response. Force a refetch:
```sh
kubectl rollout restart deploy/honkai-rule-server -n cms
```

If the issue persists, check that the pod is actually running this feature's image (compare `kubectl describe pod | grep Image:` against the image tag for branch `014-load-balance-region-groups` or its merge commit).

### Symptom: `via_lb_*` entries missing from served body

If §1 shows lb groups but §4 returns no `via_lb_*` entries, the most likely cause is that all your own-proxies declare an explicit `dialer-proxy:` field in `own-proxies.yaml`. The fan-out skip rule (008 FR-005, applied uniformly to lb fan-out per FR-016) suppresses all `via_*` copies for such own-proxies. Remove the `dialer-proxy:` field from at least one own-proxy if you want fan-out copies for it.

---

## 7. Rollback

This feature is purely additive — rolling back to the pre-feature image removes `_lb_*` groups, `via_lb_*` fan-out copies, and the `_lb_*` references in the `Proxies` selector. Existing url-test groups (012), existing 008 fan-out copies, and operator config are unaffected.

To roll back:
```sh
helm rollback honkai-rule-server -n cms
```

Or pin the image tag in `values.yaml` to the last known-good SHA (pre-014).

---

## 8. Reference

- Feature spec: `specs/014-load-balance-region-groups/spec.md`
- Implementation plan: `specs/014-load-balance-region-groups/plan.md`
- Wire-format delta: `specs/014-load-balance-region-groups/contracts/served-subscription.changes.md`
- Mihomo `load-balance` docs: <https://wiki.metacubex.one/config/proxy-groups/load-balance/> (community wiki, link verified manually before deploy if you change strategy).
