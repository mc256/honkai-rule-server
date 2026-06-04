# Feature Specification: Rule Set Support

**Feature Branch**: `016-rule-set-support`  
**Created**: 2026-06-03  
**Status**: Draft  
**Input**: User description: "We want to support the RULE-SET rule, which applies a set of rules to a specific target (e.g. `RULE-SET,Local-IP,DIRECT`), where the named set is defined in an upstream source's `rule-providers:` block. Follow the same namespacing pattern used elsewhere by adding the service-provider prefix to the rule-provider name (e.g. `srcA_Local-IP`, `srcB_Local-IP`) so each provider's ownership is identifiable and providers with the same name across sources do not collide."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - RULE-SET rules survive aggregation and the client loads them (Priority: P1)

A subscriber points their Mihomo client at the aggregator. One or more upstream
sources route traffic with `RULE-SET,<name>,<target>` lines and declare those
named sets in a `rule-providers:` block. Today the aggregator passes the
`RULE-SET` lines through but drops the backing `rule-providers:` definitions, so
the served config references undefined providers and the client refuses to load
(or silently ignores those rules). After this feature, the served config carries
both the `RULE-SET` rules and a merged `rule-providers:` block that defines every
referenced provider, so the client loads cleanly and the rule sets take effect.

**Why this priority**: Without the backing definitions the served config is
broken for any source that uses `RULE-SET`. This is the core value — making
`RULE-SET`-based upstreams usable at all.

**Independent Test**: Configure one upstream source whose payload contains a
`rule-providers:` block and at least one `RULE-SET` rule. Fetch the served
config and confirm it contains a `rule-providers:` block defining every provider
referenced by a surviving `RULE-SET` rule, and that the config passes Mihomo
validation.

**Acceptance Scenarios**:

1. **Given** an upstream source whose payload has a `rule-providers:` block with `Local-IP` and a rule `RULE-SET,Local-IP,DIRECT`, **When** the served config is produced, **Then** the output contains a `rule-providers:` block defining the namespaced provider and a `RULE-SET` rule whose provider field references that same namespaced name.
2. **Given** a served config produced from sources that use `RULE-SET`, **When** it is loaded by a Mihomo client, **Then** the client loads without "rule-provider not found" errors.
3. **Given** an upstream source that has no `rule-providers:` block and no `RULE-SET` rules, **When** the served config is produced, **Then** the output is byte-for-byte identical to what it would have been before this feature (no empty `rule-providers:` block is emitted).

---

### User Story 2 - Per-source namespacing identifies ownership and prevents collisions (Priority: P2)

Two upstream sources (`srcA` and `srcB`) both define a provider literally
named `Local-IP`. The operator wants to tell at a glance which provider came from
which source, and the merged config must not have two providers fighting over the
same key. After this feature, each provider key is prefixed with its source name
(`srcA_Local-IP`, `srcB_Local-IP`), and every `RULE-SET` rule that
referenced the bare name is rewritten to reference its own source's prefixed name.

**Why this priority**: Namespacing is what makes multi-source aggregation safe and
auditable, consistent with how proxies, proxy-groups, and rule targets are already
prefixed. It builds directly on US1's plumbing.

**Independent Test**: Configure two sources that each define a provider with the
same bare name and each route to it via `RULE-SET`. Fetch the served config and
confirm both providers appear under distinct source-prefixed keys and each
source's `RULE-SET` rule references its own prefixed provider.

**Acceptance Scenarios**:

1. **Given** sources `srcA` and `srcB` each defining a provider `Local-IP`, **When** the served config is produced, **Then** the `rule-providers:` block contains both `srcA_Local-IP` and `srcB_Local-IP` as distinct entries.
2. **Given** `srcA`'s rule `RULE-SET,Local-IP,DIRECT`, **When** namespacing is applied, **Then** the served rule is `RULE-SET,srcA_Local-IP,DIRECT`.
3. **Given** a provider whose definition references a proxy or proxy-group for fetching (e.g. a non-builtin `proxy:` value), **When** namespacing is applied, **Then** that reference is rewritten to the source-prefixed proxy/group name, while builtin targets (e.g. `DIRECT`) are left unchanged.
4. **Given** providers from two sources sharing a local cache `path:`, **When** the merged `rule-providers:` block is produced, **Then** each provider's on-disk cache path is distinct so the two sources do not overwrite each other's downloaded rule set.

---

### User Story 3 - A broken or unbacked RULE-SET reference never breaks the whole config (Priority: P3)

An upstream source emits `RULE-SET,SomeName,DIRECT` but never defines `SomeName`
in a `rule-providers:` block (an upstream authoring mistake). Rather than serving a
config that fails to load for every subscriber, the aggregator drops the
unbacked `RULE-SET` rule and serves the rest of the config intact, logging the
drop for operator visibility.

**Why this priority**: Defensive robustness. The aggregator serves many clients;
one malformed upstream must not take down everyone's config. Lower priority
because it is an error-handling refinement on top of the happy path.

**Independent Test**: Configure a source with a `RULE-SET` rule whose named
provider is absent from its `rule-providers:` block. Fetch the served config and
confirm the unbacked rule is absent while all other rules remain, and that the
config still loads.

**Acceptance Scenarios**:

1. **Given** a `RULE-SET,SomeName,DIRECT` rule with no matching provider in that source, **When** the served config is produced, **Then** that rule is omitted from the served rules and the served `rule-providers:` block contains no `SomeName` entry.
2. **Given** the same scenario, **When** the config is produced, **Then** a log entry records the dropped rule and the missing provider name for operator diagnosis.

---

### Edge Cases

- **No rule-providers anywhere**: When no contributing source supplies a `rule-providers:` block, the served config MUST NOT contain a `rule-providers:` key at all, and existing output MUST be unchanged.
- **`RULE-SET` with trailing modifiers**: A rule like `RULE-SET,Local-IP,DIRECT,no-resolve` MUST have its provider-name field (the second field) namespaced while the modifier and target are handled by existing rules — modifiers stay in place; builtin targets stay unchanged.
- **Provider defined but never referenced**: If a source defines a provider that no surviving `RULE-SET` rule references, the unreferenced provider definition is omitted from the served `rule-providers:` block (it is dead weight that the client would otherwise fetch for nothing).
- **Trailing-rule drop interaction**: The existing per-source trailing-rule drop (feature 002) still applies; if a source's trailing rule happens to be a `RULE-SET`, it is dropped like any other trailing rule, and its provider is treated as unreferenced.
- **Empty-group prune interaction (015)**: If a `RULE-SET` rule's *target* is a proxy-group that gets pruned for being empty, the existing prune pass (015) retargets that rule to the configured fallback target; the rule remains a `RULE-SET` rule and keeps its provider reference intact.
- **Custom rules and own-config**: Operator-authored custom rule sets (003) and the own-proxies/own-groups config are not "service providers" and carry no source prefix; `RULE-SET` namespacing applies only to upstream subscription sources. (See Assumptions.)
- **Malformed provider definition**: A `rule-providers:` entry that is not a well-formed mapping is skipped with a log entry rather than aborting the whole merge.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST read the `rule-providers:` block, when present, from each contributing upstream source's fetched payload.
- **FR-002**: For each upstream source, the system MUST prefix every rule-provider key with that source's provider name and a single underscore (e.g. provider `Local-IP` from source `srcA` becomes `srcA_Local-IP`), matching the existing namespacing pattern used for proxies, proxy-groups, and rule targets.
- **FR-003**: For each upstream source, the system MUST rewrite the provider-name field of every `RULE-SET` rule (the field immediately following `RULE-SET`) to the same source-prefixed name, so each rewritten rule references the provider definition from its own source.
- **FR-004**: The system MUST continue to namespace the `RULE-SET` rule's target field (the routing target) exactly as it does for all other rules today (source-prefix non-builtin targets; leave builtin targets such as `DIRECT`/`REJECT` unchanged).
- **FR-005**: The system MUST emit a single merged `rule-providers:` block in the served config containing the namespaced definitions of every provider referenced by a surviving `RULE-SET` rule.
- **FR-006**: When no surviving `RULE-SET` rule references any provider, the system MUST NOT emit a `rule-providers:` key in the served config.
- **FR-007**: Within each rule-provider definition, the system MUST rewrite any reference to a source proxy or proxy-group used for fetching the rule set to the source-prefixed name, while leaving builtin targets unchanged.
- **FR-008**: The system MUST ensure each emitted rule-provider definition has a distinct local cache path so that two sources defining a provider with the same bare name do not overwrite each other's downloaded data.
- **FR-009**: The system MUST drop any `RULE-SET` rule whose referenced provider is not defined in that source's `rule-providers:` block, and MUST NOT emit a definition for that missing provider.
- **FR-010**: The system MUST omit from the served `rule-providers:` block any namespaced provider definition that no surviving `RULE-SET` rule references.
- **FR-011**: The system MUST log (at an operator-visible level) each dropped unbacked `RULE-SET` rule with its source and the missing provider name, and each skipped malformed provider definition.
- **FR-012**: Providers from different sources MUST NOT collide in the merged block; the source-prefix in FR-002 is the collision-avoidance mechanism, and two distinct sources defining the same bare provider name MUST both appear under their respective prefixed keys.
- **FR-013**: Output MUST be byte-for-byte unchanged for any served config whose contributing sources supply neither a `rule-providers:` block nor any `RULE-SET` rule.
- **FR-014**: `RULE-SET` rules MUST continue to participate in the unified rule-priority ordering (005/007) at their source's declared priority, exactly like any other rule line (no special isolation or reordering).
- **FR-015**: The feature MUST preserve the existing per-source trailing-rule drop (002) and empty-group prune/retarget (015) behaviors; a `RULE-SET` rule is subject to both exactly as any other rule.

### Key Entities *(include if feature involves data)*

- **Rule Provider**: A named, externally-fetched set of match rules declared in an upstream source's `rule-providers:` block. Key attributes (as authored upstream): a name (the mapping key), a behavior/type/format describing how its contents are interpreted, a remote location to fetch from, a local cache path, an optional fetch-through proxy/group reference, and a refresh interval. After this feature, the name is source-prefixed and the cache path is made source-distinct.
- **RULE-SET Rule**: A routing rule of the form `RULE-SET,<provider-name>,<target>[,<modifiers>]` that delegates matching to a named Rule Provider and routes matches to `<target>`. Its provider-name field and (non-builtin) target field are both namespaced.
- **Merged rule-providers block**: The single aggregated mapping emitted in the served config, keyed by source-prefixed provider name, containing only the providers actually referenced by surviving `RULE-SET` rules.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For a configuration whose sources use `RULE-SET` rules, 100% of providers referenced by surviving `RULE-SET` rules appear in the served `rule-providers:` block, and 100% of surviving `RULE-SET` rules reference a defined provider (no dangling references).
- **SC-002**: A served config produced from `RULE-SET`-using sources loads in the Mihomo client with zero "rule-provider not found" errors.
- **SC-003**: When two sources define a provider with the same bare name, both appear in the served config under distinct keys 100% of the time, and an operator can determine each provider's owning source from its key alone.
- **SC-004**: For any configuration whose sources supply no `rule-providers:` block and no `RULE-SET` rules, the served output is byte-for-byte identical to the pre-feature output (verified by the existing snapshot suite showing no drift).
- **SC-005**: An upstream source with an unbacked `RULE-SET` reference produces a served config that still loads, with the offending rule absent and a corresponding log entry present.

## Assumptions

- **Scope is upstream subscription sources.** The source-prefix namespacing pattern applies to fetched subscription sources only. Operator-authored custom rule sets (003) and the own-proxies/own-groups file are not treated as "service providers" and are out of scope for prefixing; if they reference rule-providers that need defining, that is handled in a separate effort.
- **Provider keys are namespaced with the existing `<source>_<name>` convention** (single underscore), identical to proxy/group/target prefixing, rather than introducing a new delimiter.
- **The cache-path distinctness (FR-008) is achieved by deriving the path from the namespaced key** (e.g. the prefixed name), so distinct keys yield distinct paths; the exact path format is an implementation detail chosen to avoid collisions.
- **Provider definition fields other than the name, fetch-through reference, and cache path are preserved verbatim** from the upstream payload (behavior, type, format, URL, interval, etc.).
- **Unreferenced providers are pruned** (FR-010) on the assumption that emitting a provider the client never consults only wastes a client-side fetch and adds noise; if an operator later wants to retain all declared providers regardless of reference, that would be a follow-up toggle.
- **`RULE-SET` is the only rule type that names a rule-provider.** Other provider-referencing constructs (if any are introduced upstream later) are out of scope.
- **Existing field-ordering / emoji-escape / priority behaviors are unchanged**; this feature adds the `rule-providers:` block and the provider-name rewrite without altering how proxies, proxy-groups, or the rules block are otherwise formatted.
