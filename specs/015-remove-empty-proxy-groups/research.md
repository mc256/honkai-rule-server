# Phase 0 Research: Prune Empty Proxy-Groups

All spec clarifications were resolved during `/speckit-specify` (FR-005 single pass,
FR-007 `Proxies` exempt, FR-008 redirect to fallback target). No `NEEDS
CLARIFICATION` markers remain in Technical Context. The research below records the
design decisions needed to implement against the existing `internal/merge` pipeline.

## Decision 1 — Where the prune step runs

**Decision**: Add the prune step at the end of `Pipeline.Build()`, immediately before
the `MergedConfig` struct is constructed — after `AppendFanoutProxies` has completed.

**Rationale**: FR-004 requires pruning to see the fully assembled group set. By the
end of `Build()`, `mergedGroups` has had all merging, `AppendProxiesGroup`,
`AppendRegionGroups`, `AppendContinentGroups`, and fan-out applied; it is final and
no later step mutates group membership. Running last also means a single, easily
audited call site.

**Interaction with fan-out**: `AppendFanoutProxies` emits `via_*` proxies whose
`dialer-proxy:` names a `_region_*` / `_continent_*` / `_lb_*` group. Those
auto-emitted groups are **never empty by construction** — `AppendRegionGroups` and
`AppendContinentGroups` only emit a group when its member slice is non-empty (a
`_region_<CC>` is created from a non-empty `ccToProxies[cc]`, `_region_UNKNOWN` only
when `len(unclassifiedProxies) > 0`, and continent groups aggregate non-empty region
groups). Therefore the prune step never removes a group that a `via_*` proxy's
`dialer-proxy` points at, and running prune after fan-out leaves no dangling
`dialer-proxy`. Confirmed against `region.go` and `fanout.go`.

**Alternatives considered**: Running prune inside the `output` adapter — rejected,
violates Principle I (the adapter must stay a thin mode boundary; pruning is a
transformation and must be shared by both modes). Running prune before fan-out — no
benefit, since the only fan-out targets are never-empty auto-emitted groups.

## Decision 2 — What "empty" means and which groups are candidates

**Decision**: A proxy-group is empty when `mappingMembers(group, "proxies")` returns
zero entries — covering both an explicit `proxies: []` and an absent `proxies:` key
(FR-002). Every group is a removal candidate **except** the always-present `Proxies`
selector, identified by `Pipeline.proxiesGroupName` (FR-007).

**Rationale**: `mappingMembers` already normalizes both empty forms. In practice the
only groups that can be empty are operator-declared own-groups and
upstream-contributed groups — the auto-emitted `_region_*` / `_continent_*` / `_lb_*`
groups are non-empty by construction (Decision 1). The `Proxies` selector is exempt
per the resolved FR-007: in normal operation it aggregates every upstream proxy and
every auto-emitted group and is non-empty, so the exemption is not expected to leave
an empty group in the served output.

**Fallback-target group protection**: When `Pipeline.fallbackRuleTarget` names a
proxy-group (rather than a Mihomo built-in like `DIRECT`/`REJECT`/`PASS`), that group
is also protected from pruning. This guarantees the FR-008 retarget always lands on a
group that is present in the served output. Built-in targets are not proxy-groups so
they are never prune candidates anyway. The protected-name set is therefore
`{proxiesGroupName} ∪ ({fallbackRuleTarget} if it matches a group name)`.

## Decision 3 — Single pass, then reference cleanup (FR-005 / FR-006)

**Decision**: One removal pass collects the names of all empty, non-protected groups
and drops those group nodes from the slice. Then a reference-cleanup pass walks every
surviving group and removes, from each group's `proxies:` member list, any entry
equal to a removed group's name. The cleanup pass does **not** trigger further
removal even if it empties a group (FR-005 — cascading is out of scope).

**Rationale**: Matches the resolved FR-005 (single pass) and FR-006 (no dangling
member references). Because auto-emitted groups reference proxies or non-empty region
groups — not chains of operator groups — a group emptied purely by cleanup does not
arise with the server's own topology; the single-pass limitation only matters for
deeply nested operator-declared groups, which is the accepted scope boundary
documented in the spec's Edge Cases.

**Ordering preservation**: Removal is done by building a new slice that appends only
surviving nodes in their original order; cleanup rewrites member slices in place.
Both preserve relative order and all attributes (FR-009).

## Decision 4 — Rule-target extraction and retarget (FR-008)

**Decision**: For each rule string in `MergedConfig.Rules`, extract the target field
and, if it equals a removed group's name, rewrite it to `fallbackRuleTarget`. The
parallel `RulePriorities` / `RuleContributors` slices are untouched (retarget rewrites
in place; it never drops a rule, so lengths and alignment are preserved).

**Rule-target extraction**: Clash/Mihomo rules are comma-separated
`TYPE,PAYLOAD,TARGET[,OPTION...]`, with two shapes to handle:

- `MATCH,TARGET` — the target is field index 1 (the last field). The server-emitted
  trailing `MATCH,<fallback>` rule already targets the fallback, so it is naturally a
  no-op for retargeting.
- All other types (`DOMAIN-SUFFIX`, `IP-CIDR`, `RULE-SET`, `GEOIP`, logical
  `AND`/`OR`/`NOT`, …) — the target is the **last comma-separated field**, unless
  that last field is a known trailing option, in which case it is the
  second-to-last. Known trailing option: `no-resolve` (the only standard
  per-rule option in Mihomo that follows the target). Logical rules embed commas
  inside a parenthesised payload, but the target itself never contains a comma, so
  "last field" remains correct for them.

This "last field, or second-to-last if the last field is `no-resolve`" rule is
implemented as a small `ruleTarget(rule string) (target string, idx int, ok bool)`
helper with a focused unit test per rule shape.

**Rationale**: The codebase currently treats rules as opaque strings; this is the
minimal parser that reliably identifies the target without a full rule grammar.
Misidentification risk is bounded — the only ambiguity is an unrecognized trailing
option, and the prune retarget only fires when the extracted field exactly matches a
removed group name, so a mis-extraction simply means a rule is left unchanged (the
same as today's behavior) rather than corrupted.

**Alternatives considered**: Match every comma field against the set of removed group
names — rejected as fragile (a payload substring could coincide with a group name).
Full Mihomo rule-grammar parser — rejected as disproportionate (Principle "Simplicity
bias"); the target position is well-defined without it.

## Decision 5 — Observability (FR-011 / Principle V)

**Decision**: After pruning, `Pipeline.Build()` emits one structured `slog.Info`
event, `event="proxy-groups-pruned"`, carrying the count and names of removed groups
and the count of retargeted rules; and, when any rule was retargeted, one
`slog.Info` event per retarget (`event="rule-retargeted"`) with the rule's index, old
target, and new target. This mirrors the existing `fanout-emitted` /
`region-unmapped-indicator` logging style in `pipeline.go`.

**Rationale**: Principle V requires merge-level decisions — here, "why a group is
missing" and "why a rule's target changed" — to be answerable from structured logs
alone (SC-005). Per-retarget events make a corporate-rule retarget individually
visible (see plan Constitution Check, Routing).

## Decision 6 — Snapshot strategy (Principle II / IV)

**Decision**: The existing `served-config.snap.yaml` is left untouched — its fixtures
produce no empty proxy-group (verified: every group in the committed snapshot has at
least one member), so the prune step is a no-op for it and FR-010 holds. A new
upstream fixture is added whose merged output yields an empty operator/upstream
group; it is registered in `subscriptions.csv` and captured in a new
`served-config-prune.snap.yaml`, exercised by a new integration test.

**Rationale**: Principle IV requires snapshot coverage against real merged inputs;
FR-010 requires proof of byte-stability when nothing is empty. Keeping the existing
snapshot unchanged is itself the FR-010 assertion; the new snapshot is the FR-001/
FR-006/FR-008 assertion. Override-mode snapshots are out of scope because the
override-mode adapter is not yet implemented (consistent with features 012/014);
pruning lives in the shared core, so it is inherited when that adapter lands.
