# Served-Subscription Contract — Delta vs. 001

**Feature**: `002-namespacing-and-regions` | **Date**: 2026-04-30  
**Reference**: 001's `specs/001-subscription-aggregator/contracts/served-subscription.openapi.yaml` (unchanged in shape)

This feature does **not** introduce a new endpoint or change the wire contract of `GET /`. The OpenAPI document at `specs/001-subscription-aggregator/contracts/served-subscription.openapi.yaml` continues to describe the endpoint accurately:

- HTTP method, path, query params (`token=...`), authentication, status codes — **unchanged**.
- `Content-Type: application/yaml; charset=utf-8` — **unchanged**.
- `Subscription-Userinfo` and `Profile-Update-Interval` response headers — **unchanged**.
- Body is a Mihomo/Clash YAML config — **unchanged**.

What changes is the **content** of the YAML body. Below is the delta a stock client observes:

---

## Body content delta

### `proxies[*].name`

- **Before (001)**: arbitrary upstream display name; on cross-source collision, the lower-priority duplicate is suffixed `<name>@<source>` per FR-002.
- **After (002)**: every upstream-sourced proxy name is `<provider>_<original>` where `<provider>` is the source's CSV `name` (validated to match `^[a-z]+$`). Every own-proxy is rewritten to `_<original>` (single leading underscore) per FR-007a. Built-in identifiers DIRECT/REJECT/REJECT-DROP/PASS are not proxies and unaffected. The three name-shape classes (lowercase-letter-led upstream, underscore-led operator, uppercase built-in) are disjoint — every name's origin is identifiable from its first character (FR-007d).
- **Cross-source collisions**: structurally impossible. The `<name>@<source>` collision-suffix path becomes dead for cross-source pairs (still active for own-proxy vs. upstream same-name, and for intra-source duplicates which remain a loud-fail at load time).

### `proxy-groups[*].name`

- **Before (001)**: arbitrary upstream group name; same-name groups across sources are unioned into a single group whose member list is deduplicated; the always-present `Proxies` selector group is appended.
- **After (002)**: every upstream-sourced group name is `<provider>_<original>`; every own-group is `_<original>` (FR-007b). New: one `_region_<CC>` group per inferred country (uppercase CC, single leading underscore), type `select`, members are the **upstream-prefixed** proxies inferred to be in `<CC>` — own-proxies are excluded from region groups (FR-012). The always-present `Proxies` group is unchanged in name; its membership now also includes every emitted `_region_<CC>` group as additional pickable items, alongside individual upstream and own-proxy entries.

### `proxy-groups[*].proxies` (member list)

- **Before (001)**: list of proxy names (and possibly other group names) — verbatim from upstream.
- **After (002)**: every entry is rewritten to its prefixed form when it refers to an upstream proxy / group, except built-ins (DIRECT/REJECT/REJECT-DROP/PASS) which are passed through. Applies to all member-list-bearing group types: `select`, `url-test`, `fallback`, `load-balance`, `relay`.

### `rules[*]` (rule list)

- **Before (001)**: concatenation of every upstream's rule list in priority-desc order, with each rule's text verbatim from the upstream.
- **After (002)**:
  1. Every upstream's **last** rule is dropped before concatenation (FR-008 — typically the upstream's `MATCH,auto` catch-all). This prevents the highest-priority upstream's MATCH from short-circuiting later sources' rules.
  2. Every remaining rule's **target field** (the trailing token) is rewritten to `<provider>_<target>`, except for built-ins (DIRECT/REJECT/REJECT-DROP/PASS) which pass through.
  3. After concatenation, the server appends **exactly one** `MATCH,<target>` rule. The default target literal is `auto`; overridable via the `FALLBACK_RULE_TARGET` env var. This server-emitted rule is **never** prefixed.
- **Rule modifiers** (`no-resolve`, `src`, `dport`): preserved in their original position (after the target) — only the target field is rewritten.

### Other fields

`port`, `socks-port`, `mode`, `log-level`, `dns`, `tun`, `cfw-*`, and other top-level Mihomo configuration fields: **unchanged** from 001 (sourced from the served-config template, not from upstreams).

---

## Compatibility with stock Mihomo / Sparkle clients

The 002 body remains valid Clash YAML and parses cleanly in stock clients. The visible client-side changes:

- Proxy and group names in the client UI are now prefixed (`alpha_Node1` instead of `Node1`). UX implication: users see which provider each node came from at a glance.
- New `_region_<CC>` groups appear in the global selector. UX implication: users can pick "any HK exit" without caring about provider (own-proxies are excluded — picked through their underscore-prefixed own-groups instead).
- Rule traffic now matches against prefixed targets. UX implication: invisible — Mihomo resolves rule targets to whichever proxy/group has that name at parse time, and every rule in the served body has a matching target in the same body.

No client-side configuration change is required. The first profile-refresh after upgrading to 002 will pick up the new shape.

---

## Constraints carried forward from 001

All of these still hold:

- Determinism (Constitution Principle II): byte-identical body across two runs over identical inputs (now including identical `FALLBACK_RULE_TARGET` and identical region-table contents).
- Sanitized output (Security): no upstream URLs, no upstream credentials, no token plaintexts in the body.
- Token authentication on `?token=`: 401 on missing/unknown/revoked.
