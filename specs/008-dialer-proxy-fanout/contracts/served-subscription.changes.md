# Contract Changes: Served Subscription YAML — Feature 008

This document describes the diff to the served subscription-mode YAML body introduced by feature 008. It supersedes nothing — the contract from 001/002/003/004/005/007 remains in force; this feature only extends the `proxies:` block and tightens the `Proxies` group's member list.

## `proxies:` block — extended

### Before (post-007)

For each enabled subscription source contributing a cached payload, every upstream proxy is emitted with its `<provider>_<original>` namespace prefix (002 FR-004). For each operator-declared own-proxy, the rewritten `_<original>` form is emitted (002 FR-007a).

### After (008)

The same upstream-prefixed and own-prefixed entries are still emitted, but the block additionally contains synthesized fan-out copies of the operator's own-proxies. For an own-proxies file with N entries (K of which declare `dialer-proxy:` explicitly) and a merged config emitting M groups whose name starts with `_region_` or `_continent_`, the served `proxies:` block contains:

| Class | Form | Count |
|-------|------|------:|
| Upstream proxies (per source priority, post-prefix) | `<provider>_<original>` | unchanged |
| Own-proxies (post-rewrite) | `_<original>` | N |
| AUTO fan-out copies | `via_AUTO__<original>` with `dialer-proxy: Proxies` | N − K |
| Per-region fan-out copies | `via_region_<CC>__<original>` with `dialer-proxy: _region_<CC>` | (N − K) × (region group count) |
| Per-continent fan-out copies | `via_continent_<CONT>__<original>` with `dialer-proxy: _continent_<CONT>` | (N − K) × (continent group count) |
| `_region_UNKNOWN` fan-out copies | `via_region_UNKNOWN__<original>` with `dialer-proxy: _region_UNKNOWN` | (N − K) × (1 if `_region_UNKNOWN` is emitted, else 0) |

### Determinism

The relative order of new entries follows the ordering invariants documented in `data-model.md` § Ordering Invariants:

```text
... existing upstream + own entries ...
via_AUTO__<own1>
via_<region/continent group 1>__<own1>      [target groups in mergedGroups order]
via_<region/continent group 2>__<own1>
...
via_<region/continent group M>__<own1>
via_AUTO__<own2>
via_<region/continent group 1>__<own2>
...
```

Two consecutive served-config fetches against identical inputs produce byte-identical fan-out sections (FR-015 / Constitution Principle II).

### Field copy semantics

Each fan-out copy carries every field from the source own-proxy verbatim (server, port, type, password, cipher, udp, udp-over-tcp, ip-version, etc.). Two fields are special-cased:

- **`name`**: rewritten to the synthesized fan-out name.
- **`dialer-proxy`**: set to the target group's full name (with leading `_` for region/continent groups, or the literal `Proxies` for AUTO). If the source own-proxy already declared `dialer-proxy`, fan-out is skipped entirely for that own-proxy (no copies generated).

YAML comments, anchors, and explicit tags on the source mapping are not preserved on the fan-out copy (consistent with the rest of the merge layer's `cloneNode`/`setMappingValue` behavior).

## `proxy-groups:` block — `Proxies` group's member list tightened

### Before (post-007)

The always-present `Proxies` selector group's `proxies:` member list contains:
- Every upstream-prefixed proxy
- Every own-proxy (`_<original>`)
- Every emitted `_region_<CC>` and `_region_UNKNOWN` group name
- Every emitted `_continent_<CONT>` group name

### After (008)

The list is tightened: own-proxies (`_<original>`) are no longer direct members of `Proxies`. Fan-out copies (`via_*`) are also not direct members. Specifically:

| Member class | Before | After |
|--------------|:------:|:-----:|
| Upstream-prefixed proxies (`<provider>_<…>`) | YES | YES |
| Own-proxies (`_<own>`) | YES | **NO** (removed) |
| `_region_<CC>` and `_region_UNKNOWN` group names | YES | YES |
| `_continent_<CONT>` group names | YES | YES |
| Own-group names (`_<group>`) | already absent | absent (unchanged) |
| AUTO and per-group fan-out copies (`via_*`) | n/a | **NO** (never added) |

### Implication for clients

Mihomo clients that render `Proxies` as a UI selector will now show only upstream pools and the `_region_*`/`_continent_*` aggregations — own-proxies and `via_*` fan-out copies are reachable instead through:

1. **Operator-declared own-groups** (`_<group>` in `own-proxies.yaml`'s `proxy-groups:` block — its `proxies:` member list still names own-proxies directly).
2. **Custom rules** (003 schema) — any rule with a target equal to a fan-out proxy name (e.g., `RULE-TYPE,DOMAIN-SUFFIX,example.com,via_region_JP__markham`) routes matching traffic through the fan-out chain.
3. **Operator-declared select groups in `own-proxies.yaml`** — operators who want a UI-pickable pool of `via_*` copies can add a select group listing them by name in their own-proxies file. The merge layer does not synthesize such groups automatically.

## `rules:` block — unchanged

No changes. Custom rules from 003 may target fan-out proxy names; Mihomo resolves them as long as the proxy is declared in `proxies:` (which it now is, per the section above).

## Backwards compatibility

- **Operators who do NOT declare own-proxies**: no behavior change. Fan-out emits zero copies; `Proxies` group's member list is unchanged (it never contained own-proxies in this case).
- **Operators who declare own-proxies AND already use them via own-groups**: own-groups continue to reference own-proxies directly; no operator action required. The only visible change is the absence of own-proxies from the `Proxies` global selector — operators relying on the global selector to pick own-proxies must switch to using their own-groups (or accept the new `via_*` copies, which are also not in `Proxies`).
- **Operators who declare own-proxies AND want one to keep its own dialer chain**: set `dialer-proxy: <whatever>` on that own-proxy in `own-proxies.yaml`. Per FR-005, this opts the proxy out of fan-out entirely (no AUTO, no per-group copies); the operator's `dialer-proxy` value is preserved verbatim in the served output.
- **Operators using subscription-mode and override-mode**: both modes consume the same `MergedConfig.Proxies` from a single transformation core (Constitution Principle I), so the fan-out copies appear identically in both. No mode-specific logic is added.

## Snapshot impact

The committed integration snapshot at `internal/integration/testdata/snapshots/served-config.snap.yaml` will gain N × (M + 1) fan-out entries in the `proxies:` block and lose N entries from the `Proxies` group's `proxies:` member list. CI will fail until the snapshot is regenerated with `UPDATE_SNAPSHOTS=true go test ./internal/integration/...` — the regeneration is a deliberate, reviewable action per the Snapshot Stability Gate in the constitution and must be called out in the PR description.
