# Quickstart: URL-Test for Auto-Emitted Regional & Continent Proxy Groups

**Feature**: 012-url-test-region-groups
**Audience**: Operators of a deployed honkai-rule-server (e.g., the cluster)

This guide assumes the change has shipped and you want to verify it in a live deployment, troubleshoot a misreported configuration, or understand what the client UI now does for region/continent groups.

---

## What changed for clients

The `_region_<CC>` and `_continent_<CONT>` proxy groups in your served subscription are now `type: url-test` (previously `type: select`). The client probes each member proxy periodically and routes traffic through whichever member is currently fastest / healthiest. If the current member starts failing, the client transparently switches to the next-best one — no manual intervention.

The always-present `Proxies` selector group (containing all upstream proxies) is unchanged — it stays `type: select` so you can still manually pick any individual node.

---

## Verify the served body

```bash
TOKEN="<your-token>"
PREFIX="<your-32-char-hex-path-prefix>"   # e.g. 965ac4f224a1ab9930fab004e16b9fd7
URL="https://example.com/${PREFIX}/?token=${TOKEN}"

# Inspect a single region group
curl -fsS -A "Bronya/1.0" "$URL" | yq '.proxy-groups[] | select(.name == "_region_JP")'
```

Expected output:

```yaml
name: _region_JP
type: url-test
proxies:
  - alpha_node1
  - alpha_node2
  - beta_node1
url: https://www.gstatic.com/generate_204
interval: 10
timeout: 3000
max-failed-times: 3
lazy: true
```

Inspect all region + continent groups at once:

```bash
curl -fsS -A "Bronya/1.0" "$URL" | yq '.proxy-groups[] | select(.name | test("^_(region|continent)_"))'
```

Confirm the always-present Proxies selector still has `type: select`:

```bash
curl -fsS -A "Bronya/1.0" "$URL" | yq '.proxy-groups[] | select(.name == "Proxies") | .type'
```

Expected: `select` (unchanged).

---

## Verify the operator config

```bash
kubectl --context cluster -n cms logs deploy/honkai-rule-server | grep 'url_test_params resolved'
```

Expected (one line at the end of startup logs):

```text
{"time":"...","level":"INFO","msg":"url_test_params resolved","url":"https://www.gstatic.com/generate_204","interval_seconds":10,"timeout_ms":3000,"max_failed_times":3,"lazy":true}
```

If the values shown don't match what you set in the chart's `values.yaml`, the env vars aren't reaching the pod. Check `kubectl describe deploy honkai-rule-server -n cms | grep -A 10 'Environment:'` for the actual env-var values the deployment is using.

---

## Override one of the parameters

Edit `<your-iac-repo>/charts/honkai-rule-server/values.yaml`:

```yaml
honkai:
  env:
    # ... existing env entries ...
    urlTestUrl: "https://www.cloudflare.com/cdn-cgi/trace"   # different probe target
    urlTestIntervalSeconds: 30                                 # less aggressive probing
    urlTestTimeoutMs: 5000                                     # more tolerant timeout
    urlTestMaxFailedTimes: 5                                   # more failures before unhealthy
    urlTestLazy: false                                         # always probe
```

(Exact key names depend on the chart template wiring — adjust per your chart's structure. The env var names sent to the container MUST be `URL_TEST_URL`, `URL_TEST_INTERVAL_SECONDS`, `URL_TEST_TIMEOUT_MS`, `URL_TEST_MAX_FAILED_TIMES`, `URL_TEST_LAZY`.)

Commit, push, wait for Argo CD, then verify the new values are live:

```bash
kubectl --context cluster -n cms logs deploy/honkai-rule-server | grep 'url_test_params resolved'
curl -fsS -A "Bronya/1.0" "$URL" | yq '.proxy-groups[] | select(.name == "_region_JP") | .interval'
```

Expected: the new values appear in both the startup log and the served body.

---

## Real-client failover smoke test

This is the user-facing acceptance test from spec SC-005:

1. **Pick a region group** with at least 2 healthy members (e.g., `_region_HK` if you have multiple Hong Kong proxies).
2. **Note the currently-routed member** in the Mihomo client UI's group view (it should show which proxy is "active" / "selected by url-test").
3. **Simulate a failure** of the currently-active member: temporarily block its server IP/port at your firewall, OR pause the upstream provider's node, OR (less invasive) set `URL_TEST_INTERVAL_SECONDS=10` and `URL_TEST_MAX_FAILED_TIMES=2` so a real network hiccup cycles fast.
4. **Wait `interval × max-failed-times` seconds** (e.g., 30s for default config). The Mihomo client UI's active proxy for the group should switch to a different healthy member, with no manual action.
5. **Issue a request** through any rule that targets the region group (e.g., `DOMAIN-SUFFIX,foo.example,_region_HK`). Confirm it succeeds and traffic is going through the new member.

If the client doesn't switch, see "Troubleshooting" below.

---

## Troubleshooting

### "The pod won't start after I changed an env var"

Likely: the env var value failed validation (FR-004a loud-fail). Check:

```bash
kubectl --context cluster -n cms describe pod -l app.kubernetes.io/name=honkai-rule-server | tail -30
# OR
kubectl --context cluster -n cms logs deploy/honkai-rule-server --previous --tail=20
```

You should see a structured error log identifying the offending env var and value (e.g., `URL_TEST_INTERVAL_SECONDS=0 (must be >= 1)`). Fix the chart, push, redeploy.

### "The client UI shows red / unhealthy on every region group member"

Likely: probe URL is unreachable from the client's network. Check:

- `URL_TEST_URL` is reachable from the client side (not just from the server side — Mihomo clients run the probes themselves).
- The probe URL returns a 2xx within `URL_TEST_TIMEOUT_MS`. The default `https://www.gstatic.com/generate_204` returns 204 No Content, which is the canonical "healthy" signal.
- Firewall or DNS on the client machine isn't blocking `gstatic.com`.

If everything checks out but probes still fail, try a more permissive probe URL (`https://www.cloudflare.com/cdn-cgi/trace` returns 200 with a small body — sometimes works in regions where Google is blocked).

### "I want to manually pick a specific node within a region group"

You can't via the region group itself (it's now `url-test`). Two workarounds:

1. **Use the `Proxies` selector**: the always-present `Proxies` group still has `type: select` and contains every upstream proxy. Pick the specific node from there in the client UI.
2. **Add a custom rule** that targets a specific proxy by name: in `config/custom-rules/your-rule.yaml`, define `RULE-SET` or `DOMAIN-SUFFIX` rules with `target: alpha_specific_node` (the proxy name, not a group). The custom rules layer gives you precise per-rule control.

### "I want different probe parameters for different regions"

Out of scope for this feature — the five env vars apply globally. If you need per-region tuning, file a follow-up feature request. The spec documents this trade-off in Assumptions.

### "The startup log says url_test_params resolved with values I didn't set"

The log shows the *resolved* values after defaults are applied. Unset / empty env vars fall back to the FR-003 defaults (URL=gstatic generate_204, interval=10, timeout=3000, max-failed-times=3, lazy=true). If you want to confirm an env var is actually set (vs. defaulted), run:

```bash
kubectl --context cluster -n cms describe deploy honkai-rule-server | grep -E '^\s+URL_TEST_'
```

If a name doesn't appear in `Environment:`, it's unset and using the default.

---

## Edge cases worth knowing

- **A region group with one member**: still emitted as `url-test`. Mihomo treats single-member url-test as "use that member if healthy, otherwise nothing routes". You'll see traffic stalls if that one member fails — same UX as a single-member `select` group, but the visibility into "is this member healthy?" is now built in.
- **All members of a region group fail probes simultaneously**: Mihomo's default behavior is to route through the "least bad" member (the one with the smallest probe latency, even if it failed). Traffic may still go through but be slow / fail. Check the client UI's probe-status indicators.
- **Probe URL itself goes down** (rare for `gstatic.com`, but possible): every region/continent group's probe fails simultaneously. The client falls back per its global url-test behavior. Mitigation: change `URL_TEST_URL` to a different known-good endpoint and redeploy.
- **A new region appears mid-day** (operator added a new upstream subscription with a new country): the new `_region_<CC>` group is `type: url-test` with the configured params on the next request after the upstream cache refreshes. No special handling needed.
- **Country code can't be inferred** for a proxy: it lands in `_region_UNKNOWN`. That group is `type: url-test` too — Mihomo will probe each unclassifiable proxy and pick the healthiest. Useful side effect: `_region_UNKNOWN` is no longer a useless dump; it's a self-balancing pool of "miscellaneous" proxies.
