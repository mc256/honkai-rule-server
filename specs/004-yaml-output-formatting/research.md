# Research: YAML Output Formatting

**Feature**: 004-yaml-output-formatting
**Date**: 2026-04-30

## Research Questions

### RQ-1: yaml.v3 Comment Support

**Question**: How to insert comments at specific positions in YAML output using `gopkg.in/yaml.v3`?

**Investigation**: Examined yaml.v3 node structure and encoder behavior.

**Findings**:
- Every `yaml.Node` has three comment fields:
  - `HeadComment`: Placed before the node, on its own line
  - `LineComment`: Placed on the same line after the node
  - `FootComment`: Placed after the node, on its own line
- Comments are preserved through `yaml.Marshal` and `Encoder.Encode`
- Setting `FootComment` on a sequence element creates a comment after that element
- Setting `HeadComment` on a sequence element creates a comment before that element

**Decision**: Use `HeadComment` on the first rule of each priority level to mark boundaries.

**Rationale**: HeadComment appears on its own line before the node, which is exactly what we want for priority markers.

**Alternatives Considered**:
- `FootComment` on last rule of previous group — less intuitive (comment after item, not before group)
- `LineComment` — would appear after the rule on same line, cluttering output

### RQ-2: Block Style Enforcement

**Question**: How to force specific nodes to render in block vs flow style?

**Investigation**: Tested yaml.v3 Style field values.

**Findings**:
- `Style = 0` or `yaml.BlockStyle` (0): emitter chooses natural style, typically block for mappings
- `Style = yaml.FlowStyle` (8): forces `{key: val, ...}` single-line format
- Style is inherited by children unless explicitly set
- Setting `Style = 0` on a mapping that was previously `FlowStyle` causes it to render as block

**Decision**: For proxy-groups, ensure `Style = 0` on each proxy-group mapping node. Do NOT recursively set block style on nested sequences/lists (let them be natural).

**Rationale**: Block style on the main mapping gives readable output; nested `proxies:` list can be whatever is natural.

**Alternatives Considered**:
- Set block style recursively — would affect nested `headers:` in `ws-opts`, making them block instead of flow (undesirable)

### RQ-3: Field Ordering Stability

**Question**: Is reordering `Content` array safe and stable?

**Investigation**: Analyzed yaml.Node Content structure.

**Findings**:
- `Content` for a mapping is alternating: `[key0, val0, key1, val1, ...]`
- Keys are scalar nodes with `.Value` set to the field name
- Values can be any node type (scalar, mapping, sequence)
- Reordering preserves all other node properties (Style, Tag, Comments)
- The yaml.v3 encoder iterates Content in order when emitting

**Decision**: Reorder by swapping pairs: find `name` pair, swap to position 0-1; find `type` pair, swap to position 2-3; find `proxies` pair, swap to position 4-5.

**Rationale**: Simple swap approach preserves all other content; no need for complex slice manipulation.

**Alternatives Considered**:
- Create new Content array with reordered elements — unnecessary complexity
- Only reorder first three fields, leave rest untouched — same as decision

### RQ-4: Priority Metadata from Merge

**Question**: How to track which rule came from which priority level?

**Investigation**: Analyzed `MergeCustomRules` implementation.

**Findings**:
- Current implementation merges all rules into flat `[]string`
- Custom rules are already sorted by priority
- Upstream rules come first (priority conceptually 0)
- No metadata returned about priority boundaries

**Decision**: Create new function `MergeCustomRulesWithPriorities` that returns both rules and a parallel `[]int` array indicating priority for each rule.

**Rationale**: Parallel array is minimal change; caller can use it to insert comments without restructuring existing code significantly.

**Alternatives Considered**:
- Return structured slices grouped by priority — larger refactoring, unnecessary complexity
- Track priority boundaries as (index, priority) pairs — more complex to consume

## Summary

All research questions resolved. Implementation approach:
1. Extend `MergeCustomRules` to return priority metadata
2. Add `normalizeRulesComments()` to set HeadComment on rules
3. Add `normalizeProxyGroupStyle()` to enforce block style and reorder fields
4. Call these in `Render()` after existing `normalizeProxyStyle()`