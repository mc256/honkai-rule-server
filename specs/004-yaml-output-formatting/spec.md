# Feature Specification: YAML Output Formatting

**Feature Branch**: `004-yaml-output-formatting`  
**Created**: 2026-04-30  
**Status**: Draft  
**Input**: User description: "YAML output formatting improvements: proxy-groups block style, field ordering, rule priority comments"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Proxy-Groups in Readable Block Format (Priority: P1)

Operators viewing the served config want proxy-groups to appear in readable multi-line block format (not folded single-line), making it easy to see group membership at a glance.

**Why this priority**: Readability is the primary value proposition - operators frequently inspect proxy-groups to verify routing configuration.

**Independent Test**: Serve a config with multiple proxy-groups; verify each group renders as multi-line block format with fields on separate lines.

**Acceptance Scenarios**:

1. **Given** a merged config with proxy-groups, **When** rendered to YAML, **Then** each proxy-group appears as block format (multi-line, not `{name: ..., type: ...}`)
2. **Given** a proxy-group with many members, **When** rendered, **Then** the `proxies:` list is clearly visible as a sequence with each member on its own line

---

### User Story 2 - Consistent Field Ordering in Proxy-Groups (Priority: P2)

Operators want the first three fields of each proxy-group to appear in a consistent order: `name`, `type`, `proxies`. This makes visual scanning predictable.

**Why this priority**: Improves visual consistency but less critical than overall readability.

**Independent Test**: Serve a config; verify all proxy-groups have `name`, `type`, `proxies` as the first three fields in that order.

**Acceptance Scenarios**:

1. **Given** a proxy-group from upstream with fields in arbitrary order, **When** rendered, **Then** fields are reordered so `name` appears first, `type` second, `proxies` third
2. **Given** a proxy-group with additional fields beyond the first three, **When** rendered, **Then** those additional fields appear after `proxies` in their original relative order

---

### User Story 3 - Priority Comments in Rules Section (Priority: P3)

Operators want to see comments marking where rule priority changes occur in the rules list. When rules from different priority levels are merged, a comment should indicate the priority break point.

**Why this priority**: Nice-to-have for debugging rule ordering; rules already work correctly without comments.

**Independent Test**: Serve a config with custom rules at different priorities; verify comments appear between priority levels.

**Acceptance Scenarios**:

1. **Given** custom rules with priorities 100, 500, 1000, **When** rendered, **Then** comments appear before each priority group indicating the priority level
2. **Given** only upstream rules (no custom rules), **When** rendered, **Then** no priority comments appear (or a single comment marks the upstream rules section)
3. **Given** custom rules all at the same priority, **When** rendered, **Then** a single comment marks that priority level

---

### Edge Cases

- What happens when a proxy-group has no `proxies` field (empty group)?
- What happens when a proxy-group has no `type` field (malformed upstream)?
- What happens when custom rules have no explicit priority (default 1000)?
- What happens when there are zero custom rules?

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Proxy-groups MUST render in YAML block format (multi-line, not flow/folded style)
- **FR-002**: Each proxy-group MUST have fields `name`, `type`, `proxies` appear as the first three fields in that order
- **FR-003**: Additional proxy-group fields beyond the first three MUST retain their original relative order
- **FR-004**: The rules section MAY include comments indicating priority break points between rule groups
- **FR-005**: Priority comments MUST appear on their own line before the first rule of that priority level
- **FR-006**: Priority comments MUST NOT appear after every rule (only at priority boundaries)
- **FR-007**: Existing proxy flow-style formatting (from feature 003) MUST remain unchanged

### Key Entities

- **Proxy-Group**: A named group of proxies with a type (select, url-test, etc.) and member list. Rendered in block format with ordered fields.
- **Priority Comment**: A YAML comment (`# ...`) inserted at rule priority boundaries to aid readability.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: All proxy-groups in served config render in block format (verifiable by parsing output)
- **SC-002**: Field ordering is consistent across all proxy-groups (first three fields: name, type, proxies)
- **SC-003**: Priority comments appear only at priority boundaries, not after every rule
- **SC-004**: Served config remains valid YAML that parses correctly

## Assumptions

- The YAML library supports inserting comments at specific positions in the output (feasibility needs verification during planning)
- Priority comments are informational only and do not affect rule semantics
- Upstream proxy-groups may have fields in any order; reordering is safe
- The `proxies` field may be absent in some proxy-groups (empty groups)
- Feature 003's proxy flow-style implementation is the baseline; this feature builds on it