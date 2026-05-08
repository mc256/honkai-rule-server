# Implementation Plan: YAML Output Formatting

**Branch**: `004-yaml-output-formatting` | **Date**: 2026-04-30 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/004-yaml-output-formatting/spec.md`

## Summary

Improve YAML output readability by: (1) rendering proxy-groups in block format with consistent field ordering, and (2) adding priority-level comments to the rules section. This builds on feature 003's proxy flow-style normalization.

## Technical Context

**Language/Version**: Go 1.25 (declared 1.22+)
**Primary Dependencies**: `gopkg.in/yaml.v3` (YAML node tree manipulation), `log/slog`
**Storage**: N/A (stateless transformation)
**Testing**: Go `testing` package, `bradleyjkemp/cupaloy/v2` for snapshots
**Target Platform**: Linux server (containerized)
**Project Type**: Web service (subscription aggregator)
**Performance Goals**: N/A (formatting-only, no performance impact)
**Constraints**: Output must remain valid YAML; no semantic changes to data
**Scale/Scope**: ~100 proxy-groups, ~1000 rules in typical config

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

### Principle I: Unified Transformation Core
✅ **PASS** — All changes are in the output layer (`internal/output/`). No forked pipelines.

### Principle II: Deterministic Transformation
✅ **PASS** — Field ordering is deterministic. Comments are generated from priority metadata. Output remains byte-identical for same inputs.

### Principle III: CSV Rules — Strict Schema, Loud Failure
✅ **N/A** — This feature does not touch CSV parsing.

### Principle IV: Test-First, Real-Input Integration (NON-NEGOTIABLE)
✅ **PASS** — Will write unit tests before implementation. Integration snapshots will be regenerated.

### Principle V: Observable Routing & Source-Merge Decisions
✅ **PASS** — No change to logging; formatting only.

### Security Constraints
✅ **PASS** — No secrets in output; comments contain only priority numbers (non-sensitive).

**Re-check after Phase 1**: ✅ All gates still pass.

## Project Structure

### Documentation (this feature)

```text
specs/004-yaml-output-formatting/
├── plan.md              # This file
├── research.md          # yaml.v3 comment capabilities verification
├── quickstart.md        # Operator guide for YAML formatting
└── tasks.md             # Implementation tasks (via /speckit-tasks)
```

### Source Code (repository root)

```text
internal/
├── output/
│   ├── subscription_mode.go        # Add normalizeProxyGroupStyle(), normalizeRulesComments()
│   └── subscription_mode_test.go  # Tests for formatting
├── merge/
│   ├── rules.go                   # MergeCustomRules returns priority metadata (extension)
│   └── rules_test.go              # Tests for priority tracking
└── integration/
    └── testdata/snapshots/
        └── served-config.snap.yaml  # Regenerate with new formatting
```

**Structure Decision**: Changes are localized to `internal/output/` (formatting) and `internal/merge/rules.go` (priority metadata). No new packages.

## Complexity Tracking

No constitution violations. All changes are within existing principles.

---

## Phase 0: Research

### Research Questions

1. **yaml.v3 comment support**: How to attach comments to nodes for rendering?
2. **Block style enforcement**: How to force block style on specific nodes?
3. **Field ordering stability**: Does reordering Content preserve all other node properties?

### Findings

#### 1. yaml.v3 Comment Support

`yaml.v3` supports three comment fields on every node:
- `HeadComment`: Comment before the node (on its own line)
- `LineComment`: Comment on the same line as the node
- `FootComment`: Comment after the node (on its own line)

For rules priority comments, we want comments between priority groups. Best approach:
- Set `FootComment` on the last rule of each priority level with text like `# --- priority 100 ---`
- Or set `HeadComment` on the first rule of each new priority level

**Verified**: `gopkg.in/yaml.v3` preserves comments during encoding.

#### 2. Block Style Enforcement

Setting `node.Style = 0` or `node.Style = yaml.BlockStyle` forces block format.
Setting `node.Style = yaml.FlowStyle` forces flow/folded format.

For proxy-groups: ensure `Style = 0` (default block) on each proxy-group mapping node.

#### 3. Field Ordering Stability

Reordering `node.Content` (which alternates key, value nodes) preserves all properties.
Keys are scalar nodes, values can be any node type. Reordering is safe as long as
the alternation is maintained (key0, val0, key1, val1, ...).

---

## Phase 1: Design & Contracts

### Data Model

No new entities. Existing structures:
- `CustomRuleSet.Priority` already tracks priority (used for ordering)
- `yaml.Node` tree carries formatting metadata

### Key Interfaces

**Extended rules merge signature** (internal change, not external API):

```go
// MergeResult carries rules plus priority boundary metadata for comments.
type MergeResult struct {
    Rules         []string
    Priorities    []int  // parallel to Rules; 0 for upstream, priority value for custom
}

// MergeCustomRulesWithPriorities extends MergeCustomRules to return priority metadata.
func MergeCustomRulesWithPriorities(
    perSource map[string][]string,
    sortedSources []string,
    custom []customrules.CustomRuleSet,
    fallbackRuleTarget string,
) MergeResult
```

### Output Normalization Functions

```go
// normalizeProxyGroupStyle ensures proxy-groups render in block format with ordered fields.
// Fields are reordered: name, type, proxies, then remaining fields in original order.
func normalizeProxyGroupStyle(root *yaml.Node)

// normalizeRulesComments adds priority-level comments to the rules sequence.
// A comment appears before the first rule of each new priority level.
func normalizeRulesComments(root *yaml.Node, priorities []int)
```

### Quickstart (Operator Guide)

**Feature: Proxy-Groups Block Formatting**

Proxy-groups now render in readable multi-line block format:
```yaml
proxy-groups:
  - name: Auto
    type: url-test
    proxies:
      - node1
      - node2
    url: http://test.com
    interval: 300
```

**Feature: Field Ordering**

The first three fields are always: `name`, `type`, `proxies` (in that order).

**Feature: Rule Priority Comments**

When custom rules have multiple priority levels, comments mark boundaries:
```yaml
rules:
  - DOMAIN,ad.com,REJECT
  - DOMAIN,tracker.com,REJECT
  # --- priority 100 ---
  - DOMAIN-SUFFIX,google.com,auto
  - DOMAIN-KEYWORD,youtube,auto
  # --- priority 1000 ---
  - MATCH,auto
```
