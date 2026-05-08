# Phase 1 Data Model: Dialer-Proxy Fan-Out for Own Proxies

## Overview

This feature introduces zero new persistent entities and zero new on-disk schemas. It extends the in-memory `MergedConfig.Proxies` slice produced by `internal/merge/Pipeline.Build()` with synthesized fan-out copies of operator-declared own-proxies. All entities below are transient runtime objects — they live only inside one `Build()` invocation.

## Entities

### Fan-out Proxy *(new, runtime-only)*

A synthesized `*yaml.Node` (mapping kind) appended to `MergedConfig.Proxies`. Represents a clone of an operator-declared own-proxy with two fields rewritten/added so that Mihomo establishes a chained connection through a chosen relay group when the proxy is invoked.

| Field | Source | Value |
|-------|--------|-------|
| `name` | synthesized | Per-group: `via_<G>__<P>` where `<G>` = target group name with leading `_` stripped, `<P>` = source own-proxy name with leading `_` stripped. AUTO: `via_AUTO__<P>`. Separator between `<G>` and `<P>` is exactly two underscores. |
| `dialer-proxy` | synthesized | Per-group: full target group name with leading `_` (e.g., `_region_JP`, `_continent_AS`, `_region_UNKNOWN`). AUTO: literal `Proxies` (the always-present global selector group's name from 001 FR-009a). |
| `type`, `server`, `port`, `password`, `cipher`, `udp`, `udp-over-tcp`, `udp-over-tcp-version`, `ip-version`, ... (all other fields) | copied verbatim from source own-proxy via `cloneNode()` | unchanged |

**Field ordering**: source own-proxy's mapping order, with `name` substituted in place. `dialer-proxy` is appended at the end if absent in the source (almost always the case — own-proxies that already declare `dialer-proxy` are skipped per FR-005, so the original mapping does not include the field).

**Lifetime**: created during `Pipeline.Build()`, returned in `MergedConfig.Proxies`, marshaled to YAML by the output adapters, garbage-collected after the response is written.

### AUTO Fan-out Proxy *(special case of Fan-out Proxy)*

Same shape as Fan-out Proxy, but:
- `name` = `via_AUTO__<P>` (literal `AUTO` token in place of the stripped group name)
- `dialer-proxy` = literal `Proxies` (no leading underscore — the global selector group is named `Proxies`, not `_Proxies`)
- Emitted exactly once per source own-proxy (subject to FR-005), regardless of how many `_region_*`/`_continent_*` groups exist in `mergedGroups`.

Intent: lets a Mihomo client "pick once" at the global `Proxies` selector and have all `via_AUTO__<own>` traffic chain transparently through the current selection. Selection state lives in the client; the served YAML is unchanged across selections.

## Relationships

```text
own-proxies.yaml (operator-declared)
    │
    ▼
config.LoadOwnProxies()                       ── parse, validate ──>  config.OwnProxiesFile
    │
    ▼
merge.RewriteOwn(ownProxies, ownGroups)       ── add `_` prefix ──>  []*yaml.Node (rewrittenOwnProxies, rewrittenOwnGroups)
    │
    ├──> merge.MergeProxies(...)              ── union with upstream ──> mergedProxies
    │
    └──> merge.AppendFanoutProxies(           ── reads ──┐
              rewrittenOwnProxies,                       │
              mergedGroups,           ◄── reads region/continent group names ───┐
              proxiesGroupName,                          │                       │
          )                                              │                       │
              │                                          │                       │
              ▼                                          │                       │
          fanoutProxies ([]*yaml.Node) ────── appended to ────────►  mergedProxies (final)
                                                                                 │
                                                                                 ▼
                                                                       MergedConfig.Proxies (output)
```

## Validation Rules

The fan-out function does not introduce new validation surface. It assumes:

1. `rewrittenOwnProxies` are well-formed mapping nodes with at least a `name` field — this is the post-`RewriteOwn` invariant and already enforced upstream.
2. `mergedGroups` entries with `_region_` or `_continent_` prefix are well-formed mapping nodes with at least a `name` field — invariant from 002 / 003.
3. The Proxies group's name is non-empty (defaults to `"Proxies"` per `AppendProxiesGroup` if not configured).

If any of the above invariants are violated, fan-out emits zero copies for the offending input rather than crashing — the upstream layer is responsible for surfacing the malformed-node error before Build() reaches fan-out.

## Ordering Invariants (FR-006)

Deterministic emission across reloads is a Constitution Principle II requirement. The fan-out function's iteration order:

1. **Outer loop**: own-proxies in `rewrittenOwnProxies` slice order. This slice is produced by `RewriteOwn` which preserves the order from `OwnProxiesFile.Proxies` which preserves the YAML declaration order from the operator's `own-proxies.yaml`.
2. **Inner loop emits in this order, per own-proxy**:
   1. **AUTO copy first** — exactly one, named `via_AUTO__<P>` with `dialer-proxy: Proxies`.
   2. **Per-region/per-continent copies** — one per target group, where target groups are scanned from `mergedGroups` in declaration order. After 002/003 the order in `mergedGroups` is: upstream-prefixed groups (in source-priority then per-source order), own-groups (in `own-proxies.yaml` order, post-`_` rewrite), then the always-present `Proxies` group (from `AppendProxiesGroup`), then `_region_<CC>` (alphabetical by CC, ASCII-uppercase sort), `_region_UNKNOWN` (last among regions), then `_continent_<CONT>` (alphabetical by CONT). The fan-out filter picks only those whose name starts with `_region_` or `_continent_` — preserving the source-of-truth `mergedGroups` order without re-sorting.
3. **No interleaving**: each own-proxy emits its full inner-loop block contiguously; no later own-proxy's copies appear between an earlier own-proxy's AUTO and the earlier's per-group copies.

## Counts

For an own-proxies file with N entries (zero of which declare `dialer-proxy`) and a merged config emitting M groups whose name starts with `_region_` or `_continent_`:

| Slice | Count |
|-------|------:|
| Original own-proxies (post-`_` rewrite) | N |
| AUTO fan-out copies | N |
| Per-region/per-continent fan-out copies | N × M |
| **Total own-derived entries in served `proxies:`** | **N × (M + 2)** (originals + AUTO + per-group) |

If `K` of the N own-proxies declare an explicit `dialer-proxy`, the AUTO and per-group counts each subtract `K`:

| Slice | Count |
|-------|------:|
| Original own-proxies | N (all retained, K unchanged from operator YAML) |
| AUTO fan-out copies | N − K |
| Per-region/per-continent fan-out copies | (N − K) × M |
| **Total own-derived entries in served `proxies:`** | **N + (N − K) × (M + 1)** |

## Pipeline Wire-up

`Pipeline.Build()` after this feature:

```text
1. fetch per-source proxies/groups/rules from cache         (existing)
2. RewriteSource(...)                                       (existing, 002)
3. RewriteOwn(p.ownProxies, p.ownGroups)                    (existing, 002)
   → rewrittenOwnProxies, rewrittenOwnGroups
4. MergeProxies(...)                                         (existing)
   → mergedProxies, collisions
5. MergeProxyGroups(...)                                     (existing)
   → mergedGroups, conflicts
6. MergeUnifiedRules(...)                                    (existing, 005)
7. AppendProxiesGroup(mergedGroups, allNames, "Proxies")    (existing — but allNames now filters out names starting with `_`, per FR-007/-008)
8. AppendRegionGroups(...)                                   (existing, 002)
9. AppendContinentGroups(...)                                (existing, 003)
10. AppendFanoutProxies(rewrittenOwnProxies, mergedGroups,   (NEW)
        p.proxiesGroupName)
    → fanoutProxies
11. mergedProxies = append(mergedProxies, fanoutProxies...) (NEW)
12. log "fanout-emitted" structured event                    (NEW)
13. return MergedConfig{...}
```

`AppendFanoutProxies` is a pure function. Its signature:

```go
// AppendFanoutProxies generates one fan-out copy per (own-proxy, target group)
// pair plus one AUTO copy per own-proxy. Target groups are those entries in
// `mergedGroups` whose `name` starts with `_region_` or `_continent_`.
// The literal `proxiesGroupName` (default "Proxies") is the dialer-proxy
// value used by AUTO copies.
//
// Own-proxies whose mapping declares `dialer-proxy` are skipped entirely
// (no AUTO copy, no per-group copies) per FR-005.
//
// Returns the slice of fan-out *yaml.Node entries in deterministic order
// (FR-006), and the count of own-proxies that were skipped due to FR-005.
func AppendFanoutProxies(
    ownProxies []*yaml.Node,
    mergedGroups []*yaml.Node,
    proxiesGroupName string,
) (fanout []*yaml.Node, skipped int)
```

The function's implementation is in `internal/merge/fanout.go`. It uses `cloneNode` and `setMappingValue` from `yamlutil.go`. No new helper APIs are introduced.
