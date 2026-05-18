# Feature Specification: Prune Empty Proxy-Groups for Mihomo Compatibility

**Feature Branch**: `015-remove-empty-proxy-groups`  
**Created**: 2026-05-17  
**Status**: Draft  
**Input**: User description: "We need to adjust the server so that it works with the Mihomo client: If the \"proxies\" in the items in proxy-groups are empty, we should remove that item"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Served config loads in the Mihomo client (Priority: P1)

An operator points their Mihomo client at the rule server. The served configuration
sometimes contains proxy-group items whose member list (`proxies:`) is empty — for
example an auto-emitted region or continent group that ended up with no proxies, or
an operator-declared group whose members were all dropped during merging. Mihomo
rejects a configuration that contains a proxy-group with no members, so the client
fails to load the whole profile. The operator wants the server to drop those empty
groups so the rest of the configuration still loads.

**Why this priority**: This is the entire purpose of the feature. Without it, a
single empty group makes the served configuration unusable in Mihomo — the client
discards everything, not just the empty group. Delivering only this story already
restores a working profile for affected operators.

**Independent Test**: Construct a configuration where at least one auto-emitted or
operator-declared proxy-group resolves to an empty `proxies:` list, request the
served YAML, and confirm that group is absent while every non-empty group remains
and the served document is otherwise unchanged.

**Acceptance Scenarios**:

1. **Given** a configuration that would emit a proxy-group with an empty `proxies:`
   list, **When** a client requests the served configuration, **Then** that
   proxy-group item is absent from the served `proxy-groups:` block.
2. **Given** a configuration where every proxy-group has at least one member,
   **When** a client requests the served configuration, **Then** the served
   `proxy-groups:` block is byte-for-byte identical to what it was before this
   feature.
3. **Given** a served configuration after pruning, **When** the Mihomo client loads
   it, **Then** the client accepts the profile without an "empty proxy-group"
   validation error.
4. **Given** several proxy-groups, only some of which are empty, **When** the
   configuration is served, **Then** the non-empty groups keep their original
   relative order and all of their attributes.

---

### User Story 2 - Removing an empty group leaves no broken references (Priority: P2)

Proxy-groups can list other proxy-groups as members. When an empty group is removed,
the served configuration must not be left with a member entry that points at a name
that no longer exists — Mihomo rejects dangling references just as it rejects empty
groups.

**Why this priority**: Pruning the empty groups themselves (P1) restores a loadable
profile for the common case. Cleaning up the references those removals leave behind
makes the fix robust for configurations where a surviving group happened to list a
removed group; it is not required for the simplest case to work.

**Independent Test**: Construct a configuration with a surviving group that lists an
empty (and therefore removed) group among its members, request the served YAML, and
confirm the surviving group no longer lists the removed name.

**Acceptance Scenarios**:

1. **Given** group D that lists removed group E among several real proxy members,
   **When** the configuration is served, **Then** D remains but no longer lists E.
2. **Given** the always-present `Proxies` selector that references a group which is
   pruned, **When** the configuration is served, **Then** `Proxies` remains and no
   longer lists the pruned group.

---

### User Story 3 - Routing rules never point at a removed group (Priority: P3)

Routing rules name a proxy-group as their target. If a rule's target group is pruned
for being empty, the served rule list would reference a group that no longer exists,
which Mihomo also rejects. The served configuration must remain internally
consistent: every rule target must resolve to a group (or proxy) that is still
present.

**Why this priority**: Empty groups are most often auto-emitted region/continent
groups that custom rules rarely target directly, so this situation is uncommon. It
still must be handled for the served configuration to be guaranteed loadable, but it
is the least frequently exercised path.

**Independent Test**: Construct a configuration with a routing rule whose target is a
group that resolves to empty, request the served YAML, and confirm the served rule
list contains no rule pointing at a removed group.

**Acceptance Scenarios**:

1. **Given** a routing rule whose target group is pruned for being empty, **When**
   the configuration is served, **Then** that rule's target is the configured
   fallback rule target and no rule references the removed group name.

---

### Edge Cases

- **All other groups empty**: every proxy-group except the always-present `Proxies`
  selector resolves to empty. All of them are removed; the `Proxies` selector is
  retained per FR-007; the rest of the configuration is served unchanged.
- **The always-present `Proxies` selector is empty**: it is exempt from pruning and
  retained regardless — see FR-007.
- **A group is empty before merging but non-empty after**: pruning is evaluated only
  on the fully assembled, final group set, so a group that gains members during
  merging or region grouping is never wrongly removed.
- **A group has a `proxies:` key present but explicitly empty (`[]`) vs. the key
  absent entirely**: both are treated as "empty members" and pruned.
- **Single-pass scope**: a group that becomes empty only because its members were
  removed during reference cleanup (FR-006) is not itself removed — cascading
  removal is out of scope (FR-005). With the server's auto-emitted group topology
  (region/continent/load-balance groups whose members are proxies or non-empty
  region groups), this situation is not expected to arise in practice.
- **Pruning is a no-op**: when no group is empty, the served document is unchanged
  and snapshot tests show no drift.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST remove, from the served configuration, every proxy-group
  item whose member list (`proxies:`) is empty.
- **FR-002**: System MUST treat a proxy-group as empty when its `proxies:` list
  contains no entries, whether the key is present with an empty list or absent
  entirely.
- **FR-003**: Pruning MUST apply uniformly to every proxy-group regardless of origin
  — auto-emitted region (`_region_*`), continent (`_continent_*`), and load-balance
  (`_lb_*`) groups; operator-declared own-groups; and upstream-contributed groups —
  with the sole exception of the always-present `Proxies` selector (see FR-007).
- **FR-004**: Pruning MUST be evaluated on the fully assembled final proxy-group set,
  after all merging, namespacing, region/continent/load-balance group emission, and
  fan-out have completed, so that a group empty only transiently is never removed.
- **FR-005**: Pruning MUST be a single removal pass over the fully assembled
  proxy-group set. The system is NOT required to re-evaluate group emptiness after
  removal; cascading removal through nested groups is out of scope.
- **FR-006**: After removing empty groups, the served configuration MUST NOT contain
  any remaining proxy-group that lists a removed group's name as a member; such
  dangling member entries MUST be dropped.
- **FR-007**: The always-present `Proxies` selector MUST be exempt from pruning — it
  MUST be retained even when its member list is empty, preserving the always-present
  `Proxies` group guarantee from feature 001 (FR-009a).
- **FR-008**: When a routing rule's target is a proxy-group that was pruned, the
  system MUST redirect that rule's target to the configured fallback rule target so
  the rule still resolves to a destination that is present in the served
  configuration.
- **FR-009**: System MUST preserve the original relative order and all attributes
  (type, URL, interval, strategy, etc.) of every proxy-group that is not removed.
- **FR-010**: When no proxy-group is empty, the served configuration MUST be
  byte-for-byte identical to its pre-feature output.
- **FR-011**: System MUST record, in operational logs, which proxy-groups were
  pruned and which routing rules were redirected to the fallback target, so
  operators can understand why a group is missing or a rule's target changed.

### Key Entities *(include if data involved)*

- **Proxy-group**: A named selectable or auto-strategy group in the served
  configuration, characterized by a name, a type, a member list (`proxies:`), and
  optional behavior attributes. A group is the unit considered for removal.
- **Member reference**: An entry in a proxy-group's member list. A member may name
  either an individual proxy or another proxy-group.
- **Routing rule**: A served rule whose target names a proxy-group (or proxy). Its
  target is the link that can become dangling when a group is pruned.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A served configuration that contains one or more empty proxy-groups
  loads successfully in the Mihomo client, with no "empty proxy-group" validation
  error.
- **SC-002**: 100% of empty proxy-groups are absent from the served configuration,
  and 100% of non-empty proxy-groups remain.
- **SC-003**: For a configuration with no empty proxy-groups, the served output is
  byte-for-byte unchanged, confirmed by zero snapshot drift.
- **SC-004**: The served configuration contains no proxy-group member reference, and
  no routing rule target, that names a proxy-group absent from the served
  `proxy-groups:` block.
- **SC-005**: Operators can identify, from logs alone, every proxy-group that was
  removed and every routing rule whose target was redirected, without inspecting the
  served output.

## Assumptions

- "Works with the Mihomo client" specifically means the served configuration passes
  Mihomo's profile validation; Mihomo rejects a proxy-group with an empty member
  list and rejects references to a proxy-group name that is not defined.
- Empty proxy-groups arise from normal operation (e.g. an auto-emitted region group
  with no qualifying proxies, or an operator/upstream group whose members were all
  dropped during merging or namespacing) — they are not treated as configuration
  errors that should abort serving.
- This feature only removes proxy-group items and rewrites references to removed
  groups; it does not create, rename, reorder, or otherwise modify the attributes of
  groups that remain.
- In normal operation the always-present `Proxies` selector has at least one member
  (it aggregates every upstream proxy and every auto-emitted region/continent/
  load-balance group), so retaining it per FR-007 is not expected to leave an empty
  group in the served configuration in practice.
- A configured fallback rule target exists and is itself a valid, present
  destination, so redirecting an orphaned rule to it (FR-008) always yields a
  loadable rule.
- The behavior is the server's default; no new operator configuration switch is
  introduced to opt in or out of pruning.
- All output continues to flow through the existing deterministic, snapshot-tested
  serving path, so the change must remain deterministic.
