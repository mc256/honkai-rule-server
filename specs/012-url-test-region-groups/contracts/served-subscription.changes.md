# Contract Changes: Served Subscription Response (012)

**Feature**: 012-url-test-region-groups
**Date**: 2026-05-02
**Supersedes for `_region_*` / `_continent_*` groups**: [002 FR-008 / 003 FR-013](../../002-namespacing-and-regions/spec.md), which defined those groups as `select`-type.

This document is a delta against the served-subscription contract. It is *not* a full reissue — only the lines that change are listed.

---

## What changes

### Auto-emitted `_region_<CC>` groups

**Before**:

```yaml
- name: _region_JP
  type: select
  proxies:
    - alpha_node1
    - alpha_node2
    - beta_node1
```

**After**:

```yaml
- name: _region_JP
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

### Auto-emitted `_continent_<CONT>` groups

Same change. The `type` flips from `select` to `url-test` and the same five health-check fields appear with the same operator-configured values.

### Auto-emitted `_region_UNKNOWN` catch-all group

Same change. The prefix rule (`_region_*`) covers this group too.

### Field order

`name`, `type`, `proxies` first (per 004), then `url`, `interval`, `timeout`, `max-failed-times`, `lazy` in that order.

---

## What does NOT change

- **Always-present `Proxies` selector group** (per 001 FR-009a): stays `type: select`, no new fields.
- **Operator-defined custom proxy groups** (loaded from `config/custom-rules/`): `type` and other fields flow through unchanged regardless of name. Even if a custom group's name accidentally starts with `_region_` or `_continent_`, this feature does NOT modify it (the conversion is scoped to groups emitted by `AppendRegionGroups` / `AppendContinentGroups`, not all groups whose names happen to match the prefix).
- **Membership lists** of region / continent groups: unchanged. Same proxies appear in the same order as before.
- **`Subscription-Userinfo` / `Profile-Update-Interval` headers**: unchanged.
- **Body bytes outside `_region_*` / `_continent_*` group blocks**: unchanged (snapshot diff verification).

---

## Configuration interface

Five new env vars on the deployment, all optional with defaults:

| Env var | Default | Type | Maps to YAML field |
|---|---|---|---|
| `URL_TEST_URL` | `https://www.gstatic.com/generate_204` | string | `url` |
| `URL_TEST_INTERVAL_SECONDS` | `10` | int (≥1) | `interval` |
| `URL_TEST_TIMEOUT_MS` | `3000` | int (≥1) | `timeout` |
| `URL_TEST_MAX_FAILED_TIMES` | `3` | int (≥1) | `max-failed-times` |
| `URL_TEST_LAZY` | `true` | bool | `lazy` |

Set via the chart's `honkai.env` block in `<your-iac-repo>/charts/honkai-rule-server/values.yaml`. Validation is loud-fail at startup (Constitution Principle III). Pod restart required to apply changes.

---

## Verification

After deploy:

```bash
TOKEN="<token>"
PREFIX="<prefix>"

# Header presence and shape unchanged from 010
curl -fsS -A "Bronya/1.0" -D - -o /dev/null \
  "https://example.com/${PREFIX}/?token=${TOKEN}" \
  | grep -i '^subscription-userinfo:'

# All region + continent groups have type: url-test
curl -fsS -A "Bronya/1.0" \
  "https://example.com/${PREFIX}/?token=${TOKEN}" \
  | yq '.proxy-groups[] | select(.name | test("^_(region|continent)_")) | {name, type, url, interval, timeout, "max-failed-times", lazy}'

# Always-present Proxies group is still select
curl -fsS -A "Bronya/1.0" \
  "https://example.com/${PREFIX}/?token=${TOKEN}" \
  | yq '.proxy-groups[] | select(.name == "Proxies") | {name, type}'

# Operator's resolved URLTestParams visible in logs
kubectl --context cluster -n cms logs deploy/honkai-rule-server \
  | grep -i 'url_test_params resolved'
```

---

## Backward compatibility

- **Stock Mihomo / Clash clients**: the `url-test` group type is a long-standing standard feature. Clients fetch the new served body on their normal `Profile-Update-Interval` cadence and start probing automatically — no client-side change required.
- **Users who manually picked a node within a region group**: lose that ability for region groups. Manual override remains available via the always-present `Proxies` selector group. This trade-off is documented in the spec's Assumptions section.
- **Older Mihomo / Clash versions** that lack url-test support: out of scope. The project already targets stock Mihomo, where url-test has been supported for years.
- **Existing rules that target `_region_<CC>` or `_continent_<CONT>`**: unaffected. Rule resolution semantics ("route through this group") work identically; the group's *internal* selection mechanism changes from manual to automatic.
