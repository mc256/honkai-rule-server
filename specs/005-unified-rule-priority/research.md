# Phase 0 Research: Unified Rule Priority

**Feature**: [spec.md](./spec.md) | **Plan**: [plan.md](./plan.md) | **Date**: 2026-05-01

## Decisions

### D1. New merge function vs. extending the existing one

**Decision**: Add `MergeUnifiedRules` and remove both `MergeCustomRules` and `MergeCustomRulesWithPriorities`.

**Rationale**: The existing `MergeCustomRulesWithPriorities` returns `[]int` priorities where `0` is overloaded to mean "this is an upstream rule, ignore the priority for sorting." That overload is incompatible with the spec's FR-001 (single priority stream) — there is no longer a "this is an upstream rule" flag because upstream and custom rules sort together. Renaming would not change the semantic; the function name and signature both lie about what they do. Cleanest: replace.

**Alternatives considered**:
- *Extend `MergeCustomRulesWithPriorities` to take subscription rows and reuse the function name*: rejected — the name says "Custom Rules" but the function would now own upstream ordering too. Confusing.
- *Add upstream priorities into the perSource map's value type*: rejected — that map is `map[string][]string` (rule strings), and changing it ripples through 003's tests and pipeline. The new function localizes the change.

### D2. No backwards-compatibility shim for the deleted functions

**Decision**: Delete `MergeCustomRules` and `MergeCustomRulesWithPriorities`. Delete their tests in `internal/merge/rules_test.go` (replace with TC-U-MERGE-UNIFIED-01..06).

**Rationale**: Both functions are package-internal. The only call site is `Pipeline.Build()` in `internal/merge/pipeline.go`. Per the project's CLAUDE.md guidance ("delete unused code rather than keep dead exports") and the constitution's simplicity bias, no shim is justified.

**Alternatives considered**:
- *Keep `MergeCustomRules` for tests*: rejected — the test cases for "custom rules inserted between upstream and MATCH fallback" (TC-U-RULES-CUSTOM-01..06 from 003) describe behavior that no longer exists. Replacement is more honest than parallel-suite drift.

### D3. Per-rule contributor as parallel `[]string` vs. bucket-keyed map

**Decision**: Carry contributor name as a third parallel slice (`MergeResult.Contributors []string`), aligned with `Rules` and `Priorities`.

**Rationale**: The output adapter walks rules in order. Parallel slices let it detect a bucket boundary by comparing `Priorities[i] != Priorities[i-1]` and immediately know which contributor names to splice into the header comment by walking forward until the next boundary. A `map[int][]string` keyed by priority would still need an iteration order, and the adapter would need a second pass to associate rules with their bucket.

**Alternatives considered**:
- *Bucket struct with `(priority, contributors, rules)` triples returned from merge*: rejected because two consumers exist (output adapter for comments + the existing rule-list field on `MergedConfig`). A flat parallel-array shape lets both consumers walk the same data structure.
- *Embed contributor name as a comment in each rule string*: rejected — pollutes the rule strings with non-Mihomo syntax; downstream code that splits on `,` could break.

### D4. Contributor-list rendering for multi-contributor buckets

**Decision**: Comma-separated, alphabetical: `# --- priority 1000 (corporate, alpha) ---`.

**Rationale**: Spec FR-005 specifies this format. Alphabetical is the natural deterministic order; matches the spec's FR-003 tie-break for rule ordering, so the comment order matches the rule-block order within the bucket.

**Alternatives considered**:
- *Order contributors by rule count*: rejected — non-deterministic if two contributors have identical rule counts, and operator inspection benefit is minimal.
- *Show only the first contributor with `+N more`*: rejected — needlessly clever; bucket sizes are small (1–3 contributors typical).

### D5. Removing `# --- upstream ---` legacy comment

**Decision**: Remove entirely. No transition period.

**Rationale**: The comment was introduced by feature 004 and exists only in the served YAML, which downstream proxy clients ignore. No automated tooling consumes it. Operators reading the YAML directly will see per-priority headers instead, which carry strictly more information (priority value + contributor name).

**Alternatives considered**:
- *Keep `# --- upstream ---` as an additional comment above the first upstream bucket*: rejected — duplicative with the per-priority header that already names the upstream source. The spec's FR-009 is explicit about removal.

### D6. Sort direction: descending across the board

**Decision**: All contributors sort descending by priority. Higher priority emits earlier in the served YAML, which is matched first by Mihomo/Clash (rules are evaluated top-to-bottom).

**Rationale**: Feature 002 already established this ("CSS z-index style: higher priority emits earlier in the served `rules` block", FR-005a). Feature 003 introduced an inverted sort for custom rules — likely an oversight, since the spec for 003 does not explicitly justify ascending. Unifying to descending matches both the project's existing convention and the operator's intuition (priority N > priority M ⇒ N's rule wins).

**Alternatives considered**:
- *Ascending across the board*: rejected — would require flipping the existing upstream-source order in `internal/merge/pipeline.go`'s `sortSourcesByPriority`, breaking determinism for users with no custom rules. Higher operator-impact than flipping the custom-rule sort.

### D7. `MATCH` fallback emission

**Decision**: Continue to append `MATCH,<fallback-target>` as the last rule. Tag it with priority 0 and contributor "" in `MergeResult`. The output adapter MUST NOT emit a header comment when `priority == 0 && contributor == ""`.

**Rationale**: Spec FR-008 is explicit. The (0, "") tuple is a sentinel; no operator-supplied bucket can produce that combination because (a) priorities are non-negative integers from operator config, and (b) names are non-empty strings (validated at load time for both upstream and custom).

**Alternatives considered**:
- *Use `priority == -1` as the MATCH sentinel*: rejected — negative priorities are already validated against in the custom-rules loader; introducing a "negative is allowed for the server's own rule" exception muddies the validation contract.

## Open Questions

None. All design decisions resolved.

## Verified Assumptions

- `gopkg.in/yaml.v3`'s `HeadComment` field renders as a comment line directly above the node during marshalling. Verified during feature 004 implementation; behavior is unchanged.
- Setting `HeadComment` on a sequence-element node (a rule string in the `rules:` block) places the comment between the previous element and the current element with correct indentation. Verified by feature 004's existing `# --- priority N ---` rendering.
- `config.SubscriptionRow.Priority` is an `int`; `customrules.CustomRuleSet.Priority` is also an `int`. No type unification needed.
- `subscriptions.csv` enforces non-empty `name` per existing 002 validation. Custom-rule loader fills `Name` from filename when missing. Both contributor names are guaranteed non-empty at merge time.
