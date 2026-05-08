# Phase 1 Data Model: Unified Rule Priority

**Feature**: [spec.md](./spec.md) | **Plan**: [plan.md](./plan.md) | **Date**: 2026-05-01

This feature introduces no new persistent storage and no new public types. The
data-model below describes the in-memory structures used during the merge
transformation.

## In-memory entities

### `contributor` (unexported, transient)

A unified view of either an upstream subscription source or a custom rule set
during the merge step.

| Field | Type | Source | Notes |
|---|---|---|---|
| `Name` | `string` | upstream: `SubscriptionRow.Name`; custom: `CustomRuleSet.Name` | Sort key 2 (asc); used in header comments. Non-empty. |
| `Priority` | `int` | upstream: `SubscriptionRow.Priority`; custom: `CustomRuleSet.Priority` | Sort key 1 (desc). Non-negative. |
| `Rules` | `[]string` | upstream: `perSource[Name]` after trailing-rule drop; custom: `CustomRuleSet.Rules` | Verbatim rule strings. May be empty. |

**Lifecycle**: constructed inside `MergeUnifiedRules`, used only within that
function call, discarded on return.

**Validation**: none at this layer. Validation already happens at load time —
upstream priority and name in `internal/config/subscriptions.go`, custom-rule
priority in `internal/customrules/loader.go`.

### `MergeResult` (existing — extended)

Returned by `MergeUnifiedRules`. Three parallel slices, all of equal length.

| Field | Type | Existing? | Description |
|---|---|---|---|
| `Rules` | `[]string` | Existing | Ordered rule strings as they appear in the served `rules:` block. |
| `Priorities` | `[]int` | Existing | Parallel to `Rules`. The priority of the contributor that supplied each rule. `0` for the trailing `MATCH` fallback. |
| `Contributors` | `[]string` | **NEW** | Parallel to `Rules`. The name of the contributor that supplied each rule. `""` for the trailing `MATCH` fallback. |

**Invariant**: `len(Rules) == len(Priorities) == len(Contributors)`. The
function panics on internal violation; integration tests assert this.

**Ordering invariant**: `Priorities` is non-strictly descending until the final
element (the `MATCH` fallback at priority 0). When two adjacent rules have the
same priority, their `Contributors` values may differ (multi-contributor
bucket); within a single contributor's rules, `Contributors[i] == Contributors[i-1]`.

### `MergedConfig` (existing — extended)

The transformation output consumed by the output adapter.

| Field | Existing? | Change |
|---|---|---|
| `Proxies`, `ProxyGroups`, `Rules`, `RulePriorities`, `ContributingSources`, `Collisions`, `GroupConflicts`, `AggregatedSubscriptionUserinfo`, `AggregatedProfileUpdateIntervalHours` | All existing | Unchanged. |
| `RuleContributors` | **NEW** | `[]string` parallel to `Rules` and `RulePriorities`. Sourced from `MergeResult.Contributors`. |

## Removed entities

None. The merge layer's internal helper functions (`MergeCustomRules`,
`MergeCustomRulesWithPriorities`) are deleted — see plan.md and research.md
D1 — but they are unexported behavior, not persisted entities.

## State transitions

Stateless. The pipeline is invoked per-request via `Pipeline.Build()`; there is
no persisted state for this feature.

## Validation rules summary

The merge layer assumes its inputs are already validated:

- `upstreamPerSource` keys correspond to enabled subscription rows (caller's
  responsibility).
- `upstreamSources` priorities are non-negative integers (validated at CSV
  load).
- `customs` priorities are non-negative integers (validated at YAML load).
- `customs[i].Name` is non-empty (filename fallback at load).
- `customs[i].Rules` is non-nil (loader normalizes nil → empty slice).

If any of these invariants is violated, the merge function's behavior is
undefined; integration tests guard against regressions in the upstream
loaders.
