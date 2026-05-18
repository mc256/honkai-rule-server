# Phase 1 Data Model: Prune Empty Proxy-Groups

This feature adds no persisted storage and no new wire format. It transforms two
in-memory structures already owned by `internal/merge`. The "entities" below are the
shapes the prune step reads and rewrites.

## Existing structures (inputs / outputs — not modified in shape)

### Proxy-group node — `*yaml.Node` (mapping)

A proxy-group in `MergedConfig.ProxyGroups`. Relevant fields accessed via existing
`internal/merge` helpers:

| Field      | Access helper                     | Role in this feature                         |
|------------|-----------------------------------|----------------------------------------------|
| `name`     | `getMappingField(g, "name")`      | Identity; matched against protected names and against member references |
| `proxies`  | `mappingMembers(g, "proxies")`    | Member list; **empty → group is a removal candidate** (FR-001/FR-002) |
| `proxies`  | `setMappingMembers(g, "proxies")` | Rewritten during reference cleanup (FR-006)   |
| other      | — (untouched)                     | `type`, `url`, `interval`, `strategy`, … preserved verbatim (FR-009) |

**Validation / classification rules**:
- A group is *empty* when `mappingMembers(g, "proxies")` has length 0 (FR-002).
- A group is *protected* when its `name` is in the protected-name set
  (`proxiesGroupName`, plus `fallbackRuleTarget` when it names a group) — protected
  groups are never removed (FR-007).
- A group is *removed* when it is empty and not protected.

### Rule entry — `string` in `MergedConfig.Rules`

A Clash/Mihomo rule line, parallel to `RulePriorities` and `RuleContributors`
(equal-length slices; trailing entry is the `MATCH,<fallback>` rule).

| Aspect          | Rule in this feature                                                   |
|-----------------|------------------------------------------------------------------------|
| Target field    | Last comma-separated field, or second-to-last when the last is `no-resolve`; for `MATCH,TARGET` it is field index 1 |
| Retarget rule   | If the extracted target equals a *removed* group name → rewrite that field to `fallbackRuleTarget` (FR-008) |
| Slice alignment | Retarget rewrites in place; rule count is unchanged, so `RulePriorities` / `RuleContributors` stay aligned |

## New structure (internal, transient)

### `PruneEvent` — structured record for logging (FR-011 / Principle V)

Not persisted, not serialized to clients. Returned by `PruneEmptyProxyGroups` so
`pipeline.go` can emit `slog` events. Indicative shape:

| Field        | Type     | Meaning                                                      |
|--------------|----------|--------------------------------------------------------------|
| `Kind`       | enum     | `GroupRemoved` or `RuleRetargeted`                           |
| `GroupName`  | string   | For `GroupRemoved`: the removed group's name                 |
| `RuleIndex`  | int      | For `RuleRetargeted`: index into `Rules`                     |
| `OldTarget`  | string   | For `RuleRetargeted`: the removed group name the rule named  |
| `NewTarget`  | string   | For `RuleRetargeted`: the fallback target it was rewritten to |

The function may instead return two simple slices (`removedGroups []string`,
`retargets []struct{...}`); the exact representation is a Phase-2 implementation
detail. The data-model commitment is only that enough information flows back to
satisfy FR-011 and SC-005.

## Function contract (the unit under test)

`PruneEmptyProxyGroups` is a pure function in `internal/merge/prune.go`:

- **Inputs**: the assembled `[]*yaml.Node` proxy-groups; the `Rules` slice; the
  protected-name set (`proxiesGroupName`, optional `fallbackRuleTarget`); the
  `fallbackRuleTarget` string.
- **Outputs**: the pruned `[]*yaml.Node` (original order preserved, FR-009); the
  rewritten `Rules` slice (same length); the list of `PruneEvent`s.
- **Purity**: no I/O, no clock, no maps iterated for output ordering — deterministic
  per Principle II. Input nodes for *surviving* groups are not deep-copied; only
  their `proxies` member sequence is rewritten in place during cleanup.

## State transitions

None. This is a stateless, single-shot transformation evaluated once per
`Pipeline.Build()`.
