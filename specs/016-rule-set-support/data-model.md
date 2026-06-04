# Phase 1 Data Model: Rule Set Support

This feature manipulates YAML node structures already flowing through the merge
pipeline; it introduces no persisted entities. The "entities" below describe the
in-memory shapes and the one new `MergedConfig` field.

## Entity: Rule Provider (definition)

The value of one key in an upstream `rule-providers:` mapping. Carried as a
`*yaml.Node` (MappingNode) to round-trip unknown fields verbatim.

| Field (upstream)   | Kind     | Handling in this feature |
|--------------------|----------|--------------------------|
| (mapping key)      | string   | **Namespaced** → `<source>_<key>` (FR-002) |
| `type`             | scalar   | preserved verbatim |
| `behavior`         | scalar   | preserved verbatim |
| `format`           | scalar   | preserved verbatim |
| `url`              | scalar   | preserved verbatim (public CDN URL; no secret) |
| `path`             | scalar   | **Rewritten** to a source-distinct path derived from the namespaced key (FR-008); preserved-absent if not present |
| `proxy`            | scalar   | **Namespaced** if non-built-in (`<source>_<proxy>`); built-ins (`DIRECT`/…) unchanged (FR-007) |
| `interval`         | scalar   | preserved verbatim |
| any other field    | any      | preserved verbatim |

**Validation / robustness**:
- A `rule-providers` entry whose value is not a MappingNode is **skipped** with an
  `slog` event (spec Edge Cases — malformed provider definition).
- A provider that no surviving `RULE-SET` rule references is **omitted** from the
  merged block (FR-010).

## Entity: RULE-SET Rule

A rule string of the form `RULE-SET,<provider-name>,<target>[,<modifier>...]`.

| Field index | Meaning        | Handling |
|-------------|----------------|----------|
| `parts[0]`  | `RULE-SET`     | unchanged (rule-type literal) |
| `parts[1]`  | provider name  | **Namespaced** → `<source>_<name>` (FR-003), unconditional |
| target field| routing target | namespaced if non-built-in via the existing target rewriter (FR-004) |
| modifiers   | `no-resolve` … | unchanged, kept in place |

**Lifecycle**:
1. Per-source namespacing rewrites `parts[1]` and the target.
2. Per-source drop: if `parts[1]` (namespaced) ∉ that source's provider keys →
   rule removed + logged (FR-009).
3. Survives into `MergeUnifiedRules` and participates in priority ordering at its
   source's declared priority (FR-014), like any other rule.
4. Subject to 002 trailing-rule drop and 015 prune/retarget exactly as any rule
   (FR-015). 015 may rewrite the *target* but never `parts[1]`.

## Entity: Merged rule-providers block

The aggregated output mapping. New nullable field on the existing struct:

```go
// internal/merge/pipeline.go — MergedConfig
type MergedConfig struct {
    // ... existing fields ...

    // RuleProviders is the merged, namespaced `rule-providers:` mapping node,
    // containing only providers referenced by a surviving RULE-SET rule.
    // nil → no surviving RULE-SET rule referenced any provider; the output
    // adapter then emits no `rule-providers:` key (FR-006).
    RuleProviders *yaml.Node
}
```

**Construction (deterministic, Principle II)**:
- Built by appending each contributing source's namespaced provider key/value
  pairs into one fresh MappingNode, in `contributing` source order, preserving
  each source's upstream key order.
- Then filtered to the set of provider names referenced by the final `Rules`
  slice. Empty result → `nil`.

**Invariants**:
- Every key in `RuleProviders` is referenced by ≥1 surviving `RULE-SET` rule
  (SC-001).
- Every surviving `RULE-SET` rule's `parts[1]` is a key in `RuleProviders`
  (no dangling reference, SC-001).
- Keys are globally unique (source-prefix guarantees it, FR-012).

## New helper signatures (pure functions)

```go
// internal/merge/yamlutil.go
func findChildMapping(root *yaml.Node, key string) *yaml.Node // MappingNode value or nil

// internal/merge/ruleset.go
// Clone + namespace one source's rule-providers mapping (keys, path, proxy).
func RewriteSourceRuleProviders(sourceName string, rp *yaml.Node) *yaml.Node

// Drop RULE-SET rules whose (already-namespaced) provider key is undefined in the
// given source provider-key set. Returns kept rules + dropped descriptors (log).
func DropUnbackedRuleSetRules(rules []string, providerKeys map[string]bool) (kept []string, dropped []DroppedRuleSet)

// Merge per-source provider nodes (in order) and keep only referenced keys.
// referenced is collected from the final rule slice. Returns nil if none kept.
func MergeRuleProviders(perSource []*yaml.Node, referenced map[string]bool) *yaml.Node

// Collect provider names referenced by RULE-SET rules in the final rule slice.
func ReferencedRuleProviders(rules []string) map[string]bool
```

`DroppedRuleSet` carries `{Source, Provider, Rule string}` for the FR-011 log.

## Relationships

```
upstream payload ──findChildMapping──▶ per-source rule-providers node
        │                                          │
        │ RewriteSource (rules: prefix RULE-SET    │ RewriteSourceRuleProviders
        │   provider field + target)               │   (prefix key, path, proxy)
        ▼                                          ▼
 namespaced rules ──DropUnbackedRuleSetRules──▶ kept rules    namespaced providers
        │                                                            │
        ▼ MergeUnifiedRules (priority order, trailing drop, MATCH)   │
   final Rules ──(015 prune/retarget)──▶ FINAL Rules                 │
        │                                                            │
        └────ReferencedRuleProviders──▶ referenced set ──MergeRuleProviders──┘
                                                                     │
                                                                     ▼
                                                    MergedConfig.RuleProviders
                                                                     │
                                              output Render: setMappingValue(rule-providers)
```
