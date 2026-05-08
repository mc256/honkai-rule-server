# Feature Specification: Ascending Priority Sort

**Feature Branch**: `007-ascending-priority-sort`  
**Created**: 2026-05-01  
**Status**: Draft  
**Input**: User description: "I think we still have an issue about the rules. all the rules, including the rules from the upstreams should be sorted in one bucket. This was the requirement in previous 004 and 005 release."  Clarified: "For example alpha's priority in subscription is 1000, so it should be ranked after my custom rules config/custom-rules/early-exit-google-chrome.yaml which has a priority 200. The smaller number ranked first."

## Background

Feature 005 unified upstream-source rules and custom-rule-set rules into a single priority-ordered stream. The sort direction chosen was **descending** (higher priority emits earlier — "CSS z-index" semantics). Operator feedback after deployment is that this is the wrong semantic for Mihomo / Clash routing: the proxy client evaluates rules top-to-bottom, so the first matching rule wins. Operators reason about priority in routing-precedence terms — "priority 200 should be matched *before* priority 1000" — which is the opposite of z-index. This feature reverses 005's sort direction.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Lower priority number wins routing precedence (Priority: P1) MVP

An operator has an upstream subscription `alpha` declared with `priority: 1000` in `subscriptions.csv` and a custom rule file `config/custom-rules/early-exit-google-chrome.yaml` declared with `priority: 200`. The operator expects the custom rule (priority 200) to be matched *before* any rule from `alpha` (priority 1000), so that Google Chrome traffic exits via the operator-chosen path even if `alpha` would otherwise route it elsewhere. Today, with feature 005's descending sort, `alpha` rules emit first (priority 1000 > 200), so the upstream rule wins for any domain both contributors target. The operator wants the served `rules:` block to emit ascending: priority 200 first, then 1000, so the custom rule actually pre-empts the upstream as intended.

**Why this priority**: This is the entire bug. The current sort direction silently inverts every operator's priority intuition for routing — anyone using priority numbers to override upstream behavior gets the opposite of what they configured. The fix is small but the impact on correctness is large.

**Independent Test**: Configure one upstream source `alpha` at priority 1000 with a rule for `chrome.test → alpha_proxy`, and one custom rule set at priority 200 with a rule for `chrome.test → DIRECT`. Fetch the merged subscription and verify the custom rule (priority 200) appears at a lower index in the served `rules:` block than the upstream rule (priority 1000), so a downstream Mihomo client routes `chrome.test` via DIRECT.

**Acceptance Scenarios**:

1. **Given** an upstream source `alpha` priority 1000 and a custom rule set priority 200, **When** the operator fetches the merged subscription, **Then** the custom rule set's rules appear earlier in the served `rules:` block than `alpha`'s rules.
2. **Given** custom rule sets at priorities 150, 200, 250, 300 and upstream sources at priorities 1000 and 2000, **When** the operator fetches the merged subscription, **Then** the priority-bucket headers in the served YAML appear in ascending order: `# --- priority 150 ... ---` first, `# --- priority 200 ... ---` next, ..., `# --- priority 2000 ... ---` last (immediately before the unlabeled `MATCH` fallback).
3. **Given** two contributors at the same priority N, **When** the operator fetches the merged subscription, **Then** within that priority bucket the contributors are ordered alphabetically by name (unchanged from feature 005's tie-break rule).
4. **Given** the server-emitted `MATCH,<fallback>` rule, **When** the operator fetches the merged subscription, **Then** the MATCH rule remains the *last* entry in the served `rules:` block, with no priority header preceding it (unchanged from feature 005's MATCH handling).
5. **Given** a downstream Mihomo / Clash client receiving the served body, **When** the client matches a domain that is targeted by both a low-priority custom rule and a higher-priority upstream rule, **Then** the client matches the low-priority custom rule first (because it appears earlier in the file), confirming the intended routing precedence.

---

### Edge Cases

- **MATCH fallback at priority 0**: The MATCH rule has priority 0 in `MergedConfig.RulePriorities`. Under ascending sort, 0 is smaller than any operator-supplied priority and would naturally sort first — but MATCH MUST remain last (it's the catch-all). The implementation already appends MATCH after the contributor sort completes (it's not in the sorted contributors slice), so this edge case is handled by sequence rather than by sort key. No change required to MATCH handling.
- **Determinism preserved**: Reversing the sort direction is a single comparator change; tie-break by alphabetical name remains. Two requests against unchanged input still produce byte-identical responses (SC-004 of feature 005, preserved here as SC-002).
- **No operator-supplied priority 0**: Continues to be implicitly forbidden — priority 0 is reserved for the MATCH sentinel. Existing custom-rules loader rejects negative priorities; if an operator supplies priority 0 explicitly, the behavior is undefined under both old and new sort directions and is out of scope.
- **Snapshot regeneration**: The committed integration snapshot `internal/integration/testdata/snapshots/served-config.snap.yaml` will reorder its rules section to reflect the ascending order. The diff is rule-block reordering only (and the priority headers inside the file appear in the new order); no rule, proxy, or group content additions or removals.
- **Downstream-client compatibility**: The served body remains valid YAML; rule strings are unchanged; the MATCH fallback remains last. Mihomo / Clash clients evaluate rules top-to-bottom in any case; the behavior change is purely about which rule wins, which is exactly the intended fix.
- **Coexistence with feature 006 (emoji literals)**: features 006 and 007 touch different layers (006: byte-level escape rewrite in `internal/output/`; 007: sort comparator in `internal/merge/`). Both can land independently without conflict; integration tests for each feature continue to pass when both are merged.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST sort the unified rule stream in **ascending priority order**: a contributor with priority N emits before a contributor with priority M if and only if N < M. This reverses feature 005's FR-001.
- **FR-002**: When two or more contributors share the same priority, System MUST continue to order their rule blocks alphabetically by contributor name (unchanged from feature 005's FR-003).
- **FR-003**: System MUST continue to treat upstream-source priority and custom-rule-set priority as values on the same number line — same scale, single bucket per priority value (unchanged from feature 005's FR-002).
- **FR-004**: System MUST continue to emit one priority-bucket header comment per non-empty priority bucket, formatted exactly as today: `# --- priority <N> (<contributor-list>) ---` (unchanged from feature 005's FR-005, FR-006, FR-007).
- **FR-005**: System MUST continue to append the server-emitted `MATCH,<fallback>` rule as the last entry in the served `rules:` block, with no priority header preceding it (unchanged from feature 005's FR-008). The MATCH rule's position in the file MUST NOT depend on its priority value (which is `0`); it is always last by emission order.
- **FR-006**: Determinism — two requests against the same input state MUST produce byte-identical response bodies (unchanged from feature 005's FR-010).
- **FR-007**: The fix MUST NOT alter rule strings, proxy entries, proxy-group entries, or HTTP response headers. The only change is the order in which rule blocks appear inside the served `rules:` sequence (and consequently the order of the priority-header comments).

### Key Entities *(include if feature involves data)*

No new entities. The `Contributor` and `PriorityBucket` concepts from feature 005 are unchanged. Only the comparator that sorts contributors changes direction.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For any input state with contributors at priorities P1 < P2 < ... < Pn, the served `rules:` block lists priority buckets in strictly ascending priority order, with the smallest priority's rules appearing first and the largest priority's rules appearing last (immediately before the MATCH fallback).
- **SC-002**: 100 sequential subscription fetches against an unchanged input state produce 100 byte-identical responses (existing determinism contract preserved).
- **SC-003**: An operator with a custom rule set at priority N and an upstream source at priority M where N < M can verify by inspecting the served YAML that the custom rule set's `# --- priority N ---` header appears earlier in the file than the upstream's `# --- priority M ---` header — the bug case (`alpha` at 1000 emitting before `early-exit-google-chrome` at 200) is gone.
- **SC-004**: A downstream Mihomo / Clash client receiving the served body matches the lower-priority-numbered rule first when both a low-priority custom rule and a higher-priority upstream rule target the same domain, confirming the intended routing precedence.
- **SC-005**: All existing integration tests from features 005 and earlier continue to pass after their assertions are updated to reflect ascending order. The set of test scenarios covered does not shrink; only the expected rule-position values change.

## Assumptions

- Operator intent for `priority` field is "lower number = higher routing precedence." This matches Linux process priority (`nice` values), priority queue / min-heap semantics, and most operator-facing systems where `priority: 1` means "do this first." The CSS z-index analogy used in feature 005's design (`higher = on top`) was an implementation-domain analogy that did not match operator mental model. This feature corrects that.
- Operators with existing configurations who relied on feature 005's descending behavior intentionally — i.e., who set `priority: 2000` on their custom rules to make them win — will be affected. Mitigation: this is an early-stage product (features 005 and 006 are the current active branches; nothing has had time to ossify), and the user explicitly requested this change, indicating the descending semantic is incorrect for their actual use cases. The breaking change is acceptable.
- The `subscriptions.csv` `priority` column is preserved with its existing integer type. No migration is required for existing operator configs; the same numeric values now produce different routing order, which is the intended fix.
- Snapshot regeneration in `internal/integration/testdata/snapshots/served-config.snap.yaml` is expected. The diff is contained to rule-block reordering and priority-header comment reordering; no rule strings, proxy entries, group entries, or non-rules sections move.
- This feature does not change anything about `proxy-groups` ordering, region groups, continent groups, or `_region_UNKNOWN` handling. Those features (002, 003) have their own ordering rules and are unaffected.
- The fix is implementation-trivial (one comparator flip in the merge layer; ~1 line). The bulk of the work is regenerating tests and the integration snapshot to match the new expected order.
