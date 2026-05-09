# Contract Delta: Served Subscription — `_lb_region_*` / `_lb_continent_*` Additions

**Feature**: `014-load-balance-region-groups`
**Date**: 2026-05-08

This document specifies the precise diff this feature introduces in the served subscription body, along with what does NOT change. It is the reviewer's reference for snapshot diffs and the operator's reference for what to expect after deploying this feature.

---

## 1. What changes

### 1.1 New entries in `proxy-groups:` — paired adjacency with url-test siblings

For every existing `_region_<CC>` / `_region_UNKNOWN` / `_continent_<CONT>` group emitted by 002 / 003 / 012, an additional `_lb_<...>` sibling appears immediately after it in the served `proxy-groups:` block.

**Before this feature**:
```yaml
proxy-groups:
  - { name: Proxies, type: select, proxies: [..., _region_JP, _region_HK, _continent_AS, ...] }
  - { name: _region_JP, type: url-test, proxies: [alpha_jp1, alpha_jp2], url: https://www.gstatic.com/generate_204, interval: 10, timeout: 3000, max-failed-times: 3, lazy: true }
  - { name: _region_HK, type: url-test, proxies: [beta_hk1], url: ..., interval: 10, ... }
  - { name: _continent_AS, type: url-test, proxies: [alpha_jp1, alpha_jp2, beta_hk1], url: ..., interval: 10, ... }
```

**After this feature**:
```yaml
proxy-groups:
  - { name: Proxies, type: select, proxies: [..., _region_JP, _lb_region_JP, _region_HK, _lb_region_HK, _continent_AS, _lb_continent_AS, ...] }
  - { name: _region_JP, type: url-test, proxies: [alpha_jp1, alpha_jp2], url: https://www.gstatic.com/generate_204, interval: 10, timeout: 3000, max-failed-times: 3, lazy: true }
  - { name: _lb_region_JP, type: load-balance, proxies: [alpha_jp1, alpha_jp2], url: https://www.gstatic.com/generate_204, interval: 300, lazy: true, strategy: round-robin, timeout: 1500, max-failed-times: 3 }
  - { name: _region_HK, type: url-test, proxies: [beta_hk1], url: ..., interval: 10, ... }
  - { name: _lb_region_HK, type: load-balance, proxies: [beta_hk1], url: ..., interval: 300, lazy: true, strategy: round-robin, timeout: 1500, max-failed-times: 3 }
  - { name: _continent_AS, type: url-test, proxies: [alpha_jp1, alpha_jp2, beta_hk1], url: ..., interval: 10, ... }
  - { name: _lb_continent_AS, type: load-balance, proxies: [alpha_jp1, alpha_jp2, beta_hk1], url: ..., interval: 300, lazy: true, strategy: round-robin, timeout: 1500, max-failed-times: 3 }
```

### 1.2 New entries in `proxies:` — fan-out copies for own-proxies

For every own-proxy without an explicit `dialer-proxy`, additional `via_lb_region_<CC>__<own>` / `via_lb_continent_<CONT>__<own>` entries appear in `proxies:`. They interleave with the existing `via_region_*` / `via_continent_*` entries from 008 in deterministic order (per the widened `mergedGroups` traversal).

**After this feature** (illustrative — own-proxy `markham` and emitted `_region_JP` + `_lb_region_JP`):

```yaml
proxies:
  # ... existing entries ...
  - { name: _markham, type: ss, server: ..., port: ..., cipher: ..., password: ... }
  - { name: via_AUTO__markham, type: ss, ..., dialer-proxy: Proxies }
  - { name: via_region_JP__markham, type: ss, ..., dialer-proxy: _region_JP }
  - { name: via_lb_region_JP__markham, type: ss, ..., dialer-proxy: _lb_region_JP }       # NEW
  - { name: via_continent_AS__markham, type: ss, ..., dialer-proxy: _continent_AS }
  - { name: via_lb_continent_AS__markham, type: ss, ..., dialer-proxy: _lb_continent_AS } # NEW
```

### 1.3 New entries in the always-present `Proxies` selector's member list

Each `_lb_region_<CC>` and `_lb_continent_<CONT>` group is added as a direct member of the `Proxies` selector's `proxies:` list, interleaved with the existing url-test references (paired ordering per spec FR-013).

This is the same diff illustrated in §1.1's `Proxies` group — `_region_JP` followed by `_lb_region_JP`, etc.

### 1.4 New startup log line (out-of-band, in pod logs)

A single structured log line appears at server startup, distinct from 012's `url_test_params` log:

```text
{"event":"load_balance_params","url":"https://www.gstatic.com/generate_204","interval_seconds":300,"timeout_ms":1500,"max_failed_times":3,"lazy":true,"strategy":"round-robin"}
```

---

## 2. What does NOT change

### 2.1 Existing url-test groups (012)

Every `_region_<CC>` / `_region_UNKNOWN` / `_continent_<CONT>` group is byte-identical before and after this feature, given the same operator config:
- Same `name`.
- Same `type: url-test`.
- Same `proxies:` member list.
- Same five url-test fields (`url`, `interval`, `timeout`, `max-failed-times`, `lazy`) with the same `URL_TEST_*`-derived values.
- Same field ordering.

(SC-002 asserts this byte-identity by snapshot diff.)

### 2.2 Existing fan-out entries (008)

Every `via_AUTO__<own>`, `via_region_<CC>__<own>`, and `via_continent_<CONT>__<own>` is byte-identical before and after this feature. The widened predicate adds NEW entries; it does not modify existing ones.

### 2.3 Always-present `Proxies` selector — pre-existing entries

The `Proxies` selector's existing members (upstream-prefixed proxies, operator-declared own-groups, server-emitted `_region_*` / `_continent_*` references) remain in the same relative order. The new `_lb_*` references are interleaved between the url-test references — they do not displace existing entries.

### 2.4 Custom user-defined proxy-groups (003)

Custom proxy-groups loaded from `config/custom-rules/` YAML files are byte-identical before and after this feature, regardless of their type or name. The `_lb_*` rewrite applies only to server-emitted groups (FR-018).

### 2.5 Operator-declared own-groups (002 FR-007b)

Operator-declared own-groups (post-002 `_<original-group>` form) are byte-identical. They are not auto-mirrored as `_lb_<group>` variants (FR-019). Operators who want a load-balance own-group declare it manually.

### 2.6 Rules

The `rules:` block is byte-identical. Custom rules that target `_lb_region_*` / `_lb_continent_*` continue to flow through verbatim per 003's existing semantics; if such a rule targets a name that did not previously exist, the new lb groups make those targets resolvable client-side after this feature ships.

### 2.7 Headers and `/health`

- `Subscription-Userinfo` response header: unchanged.
- `Profile-Update-Interval` response header: unchanged.
- `/health` JSON: unchanged.

---

## 3. Wire format compatibility

The `load-balance` group type and its six fields are stock Mihomo / Clash wire format. Specifically:

| Field | Stock Mihomo type | Notes |
|---|---|---|
| `type: load-balance` | enum string | Supported in Mihomo since early versions; alongside `select`, `url-test`, `fallback`, `relay`. |
| `url` | string (HTTP/HTTPS) | Probe URL. |
| `interval` | integer (seconds) | Probe cadence. |
| `lazy` | boolean | Defer probing until group is in use. |
| `strategy` | enum string | `round-robin`, `consistent-hashing`, `sticky-sessions`. |
| `timeout` | integer (milliseconds) | Per-probe timeout. |
| `max-failed-times` | integer | Consecutive-failure threshold. |

The served YAML remains a valid Mihomo subscription. No client-side change is required.

---

## 4. Cardinality contract

For a served body with N own-proxies (none with explicit `dialer-proxy`) and M existing url-test region/continent groups (and therefore M new lb region/continent groups):

| Quantity | Pre-feature | Post-feature | Delta |
|---|---|---|---|
| Distinct `_region_*` / `_continent_*` groups in `proxy-groups:` | M | M | 0 |
| Distinct `_lb_*` groups in `proxy-groups:` | 0 | M | +M |
| Own-derived `via_*` entries in `proxies:` (per 008 + this feature) | N × (1 + M) | N × (1 + 2M) | +NM |
| Direct members of `Proxies` selector that are `_region_*` / `_continent_*` | M | M | 0 |
| Direct members of `Proxies` selector that are `_lb_*` | 0 | M | +M |

Asserted by SC-006 / SC-007 / SC-009.

---

## 5. Determinism contract

Two consecutive served-config fetches against an identical input snapshot, identical operator config, and identical clock value MUST produce byte-identical bodies (Constitution Principle II). This includes:
- The relative order of `_region_<CC>` and `_lb_region_<CC>` (paired adjacency).
- The relative order of `via_region_*` and `via_lb_region_*` fan-out entries.
- The relative order of references in the `Proxies` selector's member list.
- The byte values of every emitted field.

Asserted by SC-003 + the existing snapshot suite's drift check (`make check`).
