# Feature Specification: Unified Rule Priority

**Feature Branch**: `005-unified-rule-priority`  
**Created**: 2026-05-01  
**Status**: Draft  
**Input**: User description: "The rules from upstream should also be ranked with the customized rules, and further more, they should print out the priority in the output yaml file."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Interleave upstream rules with custom rules by priority (Priority: P1)

An operator runs the server with two upstream subscriptions (`alpha` priority 1000, `beta` priority 2000) and several custom rule files (priorities 150, 200, 250, 300, 1000). Today, every upstream rule is emitted as a single block before any custom rule, regardless of source priority. The operator wants the served `rules:` block to honor a single priority order across both kinds of contributors so that a higher-priority custom rule set can pre-empt a lower-priority upstream source, and a higher-priority upstream source can pre-empt a lower-priority custom rule set.

**Why this priority**: This is the core value of the feature. Without unified ordering, "priority" on custom rules is misleading because no custom rule — at any priority — can ever override an upstream rule. Operators need this to do precedence-based routing without manually editing upstream lists.

**Independent Test**: Configure one upstream source at priority 1000 with a rule for `example.com → AUTO`, and one custom rule set at priority 2000 with a rule for `example.com → REJECT`. Fetch the merged subscription and verify the custom rule appears earlier than the upstream rule (and is therefore matched first by the proxy client).

**Acceptance Scenarios**:

1. **Given** an upstream source priority 1000 and a custom rule set priority 2000, **When** the operator fetches the merged subscription, **Then** the custom rule set's rules appear before the upstream source's rules in the served `rules:` block.
2. **Given** an upstream source priority 2000 and a custom rule set priority 1000, **When** the operator fetches the merged subscription, **Then** the upstream source's rules appear before the custom rule set's rules.
3. **Given** two upstream sources (priority 1000 and 2000) and three custom rule sets (priorities 150, 1500, 3000), **When** the operator fetches the merged subscription, **Then** rules appear in priority-descending order: custom 3000 → upstream 2000 → custom 1500 → upstream 1000 → custom 150 → MATCH fallback.
4. **Given** a custom rule set with priority equal to an upstream source priority, **When** the operator fetches the merged subscription, **Then** the contributors at that priority appear in alphabetical order by name (deterministic), with one priority-bucket header comment naming both contributors.

---

### User Story 2 - Priority-bucket header comments visible in served output (Priority: P2)

An operator inspecting the served YAML wants to know, by reading the file alone, which priority bucket each rule belongs to and which upstream source or custom rule set contributed it. Today the output has a single `# --- upstream ---` divider and per-priority dividers only for custom rules; the operator cannot tell from the file which upstream source supplied a given rule, nor see upstream sources' priority numbers.

**Why this priority**: Observability for operators debugging routing decisions. P2 because the priority order from US1 is the load-bearing behavior; the comments make it inspectable but the system works without them.

**Independent Test**: Configure the server with one upstream source at priority 2000 named `beta` and one custom rule set at priority 1000 named `corporate`. Fetch the merged subscription. Verify the `rules:` block contains exactly two header comments: `# --- priority 2000 (beta) ---` before the upstream rules and `# --- priority 1000 (corporate) ---` before the custom rules. Verify no `# --- upstream ---` comment remains.

**Acceptance Scenarios**:

1. **Given** rules from a single contributor at priority N, **When** the operator inspects the served YAML, **Then** a single header comment `# --- priority N (<contributor-name>) ---` precedes that contributor's rules.
2. **Given** rules from two contributors sharing priority N, **When** the operator inspects the served YAML, **Then** a single header comment `# --- priority N (<name-a>, <name-b>) ---` precedes both contributors' rules, with names alphabetically ordered.
3. **Given** a config where upstream rules and custom rules are interleaved across priority buckets, **When** the operator inspects the served YAML, **Then** the priority headers appear in descending order matching the rule order, with one header per priority transition.
4. **Given** a config with only upstream rules and no custom rule sets, **When** the operator inspects the served YAML, **Then** the rules are still grouped under priority headers (one per upstream source priority); the `# --- upstream ---` legacy comment no longer appears anywhere.
5. **Given** a config with no upstream rules and no custom rules, **When** the operator inspects the served YAML, **Then** the `rules:` block contains only the server-emitted `MATCH` fallback with no priority header.

---

### Edge Cases

- **Trailing-rule drop interaction**: Upstream sources still drop their last rule per FR-008 from feature 002. The drop applies before priority bucketing, so an upstream contributor with only one rule contributes zero rules and MUST NOT produce a header comment for an empty bucket.
- **Empty custom rule set at a given priority**: A custom rule set whose `rules` list is empty MUST NOT produce a header comment for an empty bucket.
- **Priority 0**: Reserved for the server-emitted `MATCH` fallback. Operator-supplied custom rule sets and upstream sources MUST NOT produce a priority-0 bucket header. The fallback `MATCH` rule remains the last entry in the rules block and is emitted without a preceding header comment.
- **Negative custom-rule priority**: Already rejected at load time per existing loader (warn + skip file). No new behavior needed.
- **Same name across upstream and custom**: An upstream source named `corporate` (priority 2000) and a custom rule set named `corporate` (priority 2000) collide. The header is `# --- priority 2000 (corporate, corporate) ---` and the rules concatenate in source-type-then-name order: upstream rules first, then custom rules within the bucket. (Operators should rename to avoid this; the system tolerates it.)
- **Snapshot regeneration**: Existing integration snapshot fixtures will produce different output (different comments, different rule order if any custom rules exist in fixtures). Snapshots must be regenerated with manual review.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST place every rule from an upstream source and every rule from a custom rule set into a single ordered stream sorted by descending priority before emitting the served `rules:` block.
- **FR-002**: System MUST treat upstream-source priority and custom-rule-set priority as values on the same number line (no separate buckets, no scaling, no offset).
- **FR-003**: When two or more contributors share the same priority value, System MUST order their rule blocks alphabetically by contributor name (case-sensitive Unicode codepoint order, matching the existing custom-rule tie-breaker from feature 003).
- **FR-004**: System MUST preserve the relative order of rules within a single contributor exactly as they appear in that contributor's source (upstream source: as fetched, with the trailing rule dropped per FR-008 of feature 002; custom rule set: as listed in the YAML file).
- **FR-005**: System MUST emit exactly one header comment per priority bucket in the served `rules:` block, formatted as `# --- priority <N> (<contributor-list>) ---`, where `<contributor-list>` is a comma-separated alphabetical list of the names of every upstream source and custom rule set that contributed at least one rule to that bucket.
- **FR-006**: System MUST emit the priority-bucket header comment as the YAML head-comment of the first rule in the bucket, so it appears on its own line immediately before that rule.
- **FR-007**: System MUST NOT emit a priority-bucket header for a bucket that contains zero rules (e.g., an upstream source whose only rule was the trailing drop, or a custom rule set with an empty `rules` list).
- **FR-008**: System MUST append the server-emitted `MATCH,<fallback-target>` rule as the last entry in the served `rules:` block with no preceding header comment, regardless of priority configuration.
- **FR-009**: System MUST NOT emit the legacy `# --- upstream ---` comment anywhere in the served output. This comment is replaced by per-priority headers.
- **FR-010**: System MUST produce byte-identical served output for two requests against the same input state (determinism). Tie-breaking by name and stable iteration over contributors are required.

### Key Entities *(include if feature involves data)*

- **PriorityBucket**: Represents a group of rules sharing the same priority value. Has a priority number, an alphabetically-ordered list of contributor names, and an ordered list of rule strings. Emitted in the served output as one header comment followed by its rule strings.
- **Contributor**: Either an upstream source (from `subscriptions.csv`) or a custom rule set (from `config/custom-rules/*.yaml`). Has a name (case-sensitive string) and a priority (non-negative integer). Both kinds of contributor are equivalent for sorting purposes.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For any input state combining upstream sources at priorities P1, P2, ... and custom rule sets at priorities Q1, Q2, ..., the served `rules:` block lists priority buckets in strictly descending priority order, with no priority value appearing twice.
- **SC-002**: For any served `rules:` block, every non-MATCH rule is preceded (directly or indirectly) by exactly one priority-bucket header comment that names the contributor that supplied the rule.
- **SC-003**: An operator inspecting the served YAML can identify the priority and contributor of any rule by reading only the file (no need to consult `subscriptions.csv` or the custom-rules folder).
- **SC-004**: 100 sequential subscription fetches against an unchanged input state produce 100 byte-identical responses (determinism — same as today's contract for feature 002 and 003).
- **SC-005**: Existing routing behavior for users with no custom rule sets is preserved relative to today: with custom rules absent, only the comment format changes (per-source priority headers replace the single `# --- upstream ---` comment); rule order is unchanged.

## Assumptions

- Operators understand that this changes user-visible YAML output: header comments differ from today's, and rule order differs from today's whenever custom rule sets exist. Snapshot regeneration in `internal/integration/testdata/snapshots/` is expected and must be manually reviewed.
- Operators with existing custom rule sets relied on "all upstream first, then custom" behavior coincidentally — they have not yet started using custom-rule priority values to override upstream rules (the product was just shipped). Therefore the breaking change in rule order has acceptable blast radius.
- The custom-rules sort direction in feature 003's spec was a defect: it sorted ascending while feature 002 sorted upstream sources descending. This feature corrects to a single descending sort across both, matching the CSS z-index semantics already documented in feature 002 (FR-005a).
- Priority values are non-negative integers. Existing upstream priorities (`subscriptions.csv` column) and custom-rule priorities (YAML field) keep their existing types and validation. No migration of operator-supplied priority values is required.
- Comment delimiter style (`# --- priority N (...) ---`) is chosen for visual consistency with feature 004's existing `# --- priority N ---` format. The contributor-list parenthetical is the only addition; downstream YAML parsers ignore comments so this has no functional impact on Mihomo/Clash clients.
- The `# --- upstream ---` comment from feature 004 is removed entirely; no caller depends on its presence. Operators reading older docs may need a CHANGELOG entry, but the user-facing contract is comment text only.
