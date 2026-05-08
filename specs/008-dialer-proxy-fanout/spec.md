# Feature Specification: Dialer-Proxy Fan-Out for Own Proxies

**Feature Branch**: `008-dialer-proxy-fanout`
**Created**: 2026-05-01
**Status**: Draft
**Input**: User description: "Set the Dialer Proxy for all our own nodes (just Proxies, not Proxy Groups). For each operator-provided own-proxy, generate a copy per server-emitted region/continent proxy group with `dialer-proxy` set to that group, named `via_<group>__<original>`. Additionally generate one `via_AUTO__<original>` copy per own-proxy whose `dialer-proxy` is the always-present `Proxies` selector group. Do not include own-proxies (originals or any of the generated copies) in the always-present `Proxies` selector group."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Reach an own-exit through a chosen relay region (Priority: P1)

An operator has declared a small set of own-proxies (e.g., `montreal`, `montreal-spare`, `markham` in `own-proxies.yaml`). They also expose many server-emitted regional pools — one `_region_<CC>` group per upstream-classified country, plus continent roll-ups `_continent_<CONT>` and the catch-all `_region_UNKNOWN`. The operator wants every own-exit to be reachable "via" each available regional pool: a chained connection where the user dials a regional pool first, and that pool then dials the chosen own-exit. This lets the operator say things like "send my browsing through Japan-then-Markham" without manually composing a chain per region per own-exit.

**Why this priority**: This is the entire feature. Without the fan-out, the operator would have to hand-author one node per (own-exit, region) pairing in `own-proxies.yaml`, which is error-prone and explodes as new regions appear automatically from upstream classification. P1 because it's the only deliverable that produces user-visible value.

**Independent Test**: With one upstream that contributes proxies in two countries (e.g., JP and HK) and three own-proxies declared, fetching the merged config returns the original three own-proxies plus six fan-out proxies named `via_region_JP__<own>` and `via_region_HK__<own>`. Each fan-out proxy has the same connection fields (server, port, cipher, password, etc.) as its source own-proxy and adds `dialer-proxy: _region_JP` (or `_region_HK`). When the upstream contributes a continent-classifiable mix, additional `via_continent_<CONT>__<own>` fan-out proxies appear matching every emitted `_continent_<CONT>` group.

**Acceptance Scenarios**:

1. **Given** an operator declares own-proxies `montreal` and `markham` and the upstream pool produces region groups `_region_JP` and `_region_HK`, **When** the merged config is served, **Then** the served `proxies:` block contains the originals (`_montreal`, `_markham`) AND four fan-out copies: `via_region_JP__montreal`, `via_region_HK__montreal`, `via_region_JP__markham`, `via_region_HK__markham`. Each fan-out copy carries every non-name field from its source own-proxy verbatim AND adds `dialer-proxy: _region_<CC>` matching the group it was fanned out for.
2. **Given** the upstream classification also yields `_continent_AS` and `_region_UNKNOWN`, **When** the merged config is served, **Then** the fan-out also produces `via_continent_AS__<own>` and `via_region_UNKNOWN__<own>` for every own-proxy, with `dialer-proxy:` set to `_continent_AS` and `_region_UNKNOWN` respectively.
3. **Given** a fan-out proxy `via_region_JP__markham` is referenced from a custom routing rule (e.g., a custom rule with target `via_region_JP__markham`), **When** the served YAML is parsed by a Mihomo client, **Then** the client resolves the proxy, dials `_region_JP` first, and tunnels through to the Markham endpoint.
4. **Given** any number of own-proxies are declared and any number `M` of `_region_*`/`_continent_*` groups are emitted, **When** the merged config is served, **Then** exactly `len(own-proxies) × (M + 1)` fan-out copies appear (one per Cartesian pair plus one `via_AUTO__<own>` per own-proxy), with no duplicates, no missing pairs, and deterministic ordering across reloads.

---

### User Story 2 - One AUTO chain per own-exit through whatever Proxies points at (Priority: P1)

In addition to the per-region/per-continent fan-out, the operator wants a single AUTO variant per own-proxy: a fan-out copy named `via_AUTO__<own>` whose `dialer-proxy` is the always-present `Proxies` selector group. This lets the user pick once at the global `Proxies` selector ("any HK exit", "any Asia continent pool", "this specific upstream proxy") and have all `via_AUTO__<own>` traffic transparently chain through that selection. The operator thus avoids picking N region groups every time the upstream pool shifts; they pick once via `Proxies`, and every own-exit follows.

**Why this priority**: AUTO is the most common operator workflow — most users don't want to commit to a fixed region/continent and would rather indirect through whatever's currently selected globally. Same delivery as US1 because the AUTO copy reuses the exact same fan-out machinery (just with target group `Proxies` instead of a `_region_*`/`_continent_*` group). P1 because skipping it would leave the most common workflow unsupported.

**Independent Test**: With three own-proxies declared (`montreal`, `montreal-spare`, `markham`) and any non-empty set of `_region_*`/`_continent_*` groups, fetch the merged config; assert the served `proxies:` block contains exactly three AUTO fan-out copies — `via_AUTO__montreal`, `via_AUTO__montreal-spare`, `via_AUTO__markham` — each carrying the source own-proxy's connection fields verbatim and `dialer-proxy: Proxies` (referring to the always-present global selector). Assert that the AUTO copies appear regardless of the number or composition of `_region_*`/`_continent_*` groups, including the degenerate case where zero such groups exist.

**Acceptance Scenarios**:

1. **Given** the operator declares own-proxies `montreal` and `markham`, **When** the merged config is served, **Then** the served `proxies:` block contains `via_AUTO__montreal` and `via_AUTO__markham`, each with `dialer-proxy: Proxies`, in addition to the per-region/per-continent fan-out copies.
2. **Given** the user, in their Mihomo client, picks `_region_JP` as the active selection inside the global `Proxies` selector, **When** a routing rule targets `via_AUTO__markham`, **Then** the client establishes the chain `client → Proxies (currently _region_JP) → markham endpoint → destination`.
3. **Given** the user later switches the `Proxies` selection to `_continent_AS` or a specific upstream proxy, **When** the same `via_AUTO__markham` is invoked, **Then** the chain follows the new selection without any config reload — the served YAML is unchanged; only the client-side selector state changes.
4. **Given** an own-proxy declares an explicit `dialer-proxy:` field in `own-proxies.yaml`, **When** the merged config is served, **Then** the AUTO fan-out copy is **not** generated for that own-proxy (consistent with FR-005's per-own-proxy skip rule).

---

### User Story 3 - Keep own-proxies out of the global Proxies selector (Priority: P1)

The always-present `Proxies` selector group (per 001's FR-009a) currently lists every upstream proxy AND every own-proxy AND every server-emitted region/continent group. The operator wants own-proxies (and the new fan-out copies) excluded from this selector so the global picker stays focused on upstream pools and region/continent groups, while own-exits remain addressable through the operator's own-groups (FR-007b) or through explicit custom rules.

**Why this priority**: Scope-coupled with US1 in the same delivery: the fan-out from US1 multiplies own-proxy count by the number of region/continent groups, so without this exclusion the `Proxies` selector becomes dominated by `via_*` entries. P1 because shipping US1 without this exclusion produces visibly worse UX in Mihomo's UI, and operators may roll back rather than absorb the clutter.

**Independent Test**: Fetch the merged config; locate the always-present `Proxies` group; assert that its `proxies:` member list contains every upstream-prefixed proxy (e.g., `alpha_*`, `beta_*`), every emitted `_region_<CC>`, every emitted `_continent_<CONT>`, every operator-declared own-group (`_<group>` form), and the always-present sentinels (DIRECT/REJECT-style entries if 001 emits them) — but contains **none** of: own-proxies (`_<own>`), per-region/per-continent fan-out copies (`via_<group>__<own>`), or AUTO fan-out copies (`via_AUTO__<own>`). The own-proxies remain selectable via the operator's own-groups defined in `own-proxies.yaml`.

**Acceptance Scenarios**:

1. **Given** the operator declares two own-proxies (`montreal`, `markham`) AND one own-group `Canada-Exit-Proxies` whose `proxies:` list is `[montreal, montreal-spare, markham]`, **When** the merged config is served, **Then** the always-present `Proxies` selector contains `_Canada-Exit-Proxies` (the rewritten own-group) but does NOT contain `_montreal`, `_montreal-spare`, or `_markham` as direct members. The own-group's `proxies:` list still references the underscore-prefixed own-proxy names so the chain is intact.
2. **Given** US1's fan-out produces `via_region_JP__markham`, `via_continent_AS__markham`, and US2's fan-out produces `via_AUTO__markham`, **When** the merged config is served, **Then** the always-present `Proxies` selector contains none of those names as a direct member. Operators reach those names through custom rules (003) or by referencing them from a custom own-group declared in `own-proxies.yaml`.
3. **Given** own-proxies are excluded from `Proxies`, **When** an own-proxy is inspected for region/continent classification, **Then** it remains excluded from `_region_<CC>` and `_continent_<CONT>` groups (002 FR-012 and 003 FR-017 already enforce this — this feature does not change that behavior).

---

### Edge Cases

- **No region/continent groups exist** (e.g., zero upstreams classified, only `_region_UNKNOWN` would be emitted): Per-region/per-continent fan-out runs against any group whose name starts with `_region_` or `_continent_` — including `_region_UNKNOWN`. If the operator's upstream classification produces zero such groups in a given build, no per-region/per-continent copies are produced. The AUTO copy is still emitted per own-proxy regardless (FR-004a is independent of the region/continent group set).
- **Own-proxy already declares `dialer-proxy`**: The operator-supplied YAML carries an explicit `dialer-proxy:` field on a particular own-proxy. The fan-out is **skipped for that own-proxy** entirely (no `via_*` copies), because the operator has expressed an explicit chain choice that fan-out would override ambiguously. The original own-proxy keeps its declared `dialer-proxy` value verbatim.
- **Own-proxy name contains characters that would collide with the separator `__`**: Own-proxy names are operator-controlled. If an own-proxy is named `foo__bar`, the fan-out produces `via_region_JP__foo__bar` — readable and unambiguous because the prefix `via_<group>__` is parsed left-to-right (first `__` after the group name is the separator). No name mangling is applied.
- **Group name collision via fan-out**: A fan-out name `via_region_JP__markham` could theoretically collide with an upstream-prefixed name only if a CSV `name` of `via` were declared and that upstream contributed a proxy literally named `region_JP__markham` (highly unlikely). The pipeline retains 001's `<name>@<source>` collision-suffix path as defense-in-depth; if a collision does occur, the fan-out copy keeps its computed `via_*` name and the colliding upstream proxy receives the suffix per 001 FR-002.
- **Reload mid-flight**: An own-proxies file edit or a region-classification refresh changes the input set. The fan-out is recomputed deterministically on every Build — the served YAML reflects the new set of own-proxies × region/continent groups within one debounce window (250ms via fsnotify, per 001).
- **Own-group membership refers to an own-proxy that gets fanned out**: Own-groups continue to reference the underscore-prefixed own-proxy names (`_markham`), not any `via_*` form. This feature does not rewrite own-group member lists — operators that want a select-pool of fan-out copies must declare it explicitly in `own-proxies.yaml` (or rely on custom rules from 003).

## Requirements *(mandatory)*

### Functional Requirements

#### Fan-out generation

- **FR-001**: For every own-proxy declared in `own-proxies.yaml` (001 FR-006) and for every server-emitted proxy group whose name starts with `_region_` (002 FR-013) or `_continent_` (003 FR-009), the system MUST emit one fan-out proxy in the served `proxies:` block.
- **FR-002**: Each fan-out proxy's `name` MUST be `via_<G>__<P>`, where `<G>` is the target group's name with its single leading underscore stripped (e.g., group `_region_JP` → `<G> = region_JP`) and `<P>` is the own-proxy's name with its single leading underscore stripped (e.g., own-proxy `_markham` → `<P> = markham`). The literal separator between `<G>` and `<P>` is exactly two underscores (`__`).
- **FR-003**: Each fan-out proxy MUST carry every field from its source own-proxy verbatim **except** `name` (which is set per FR-002) and `dialer-proxy` (which is set per FR-004). Field order in the emitted YAML mapping MUST match the source own-proxy's field order, with `name` substituted in place and `dialer-proxy` appended at the end if not already present.
- **FR-004**: Each fan-out proxy MUST carry the field `dialer-proxy: <group-name>` where `<group-name>` is the **full** target group name including its leading underscore (e.g., `_region_JP`, `_continent_AS`, `_region_UNKNOWN`).
- **FR-004a**: For every own-proxy (subject to FR-005's skip rule) the system MUST additionally emit one AUTO fan-out proxy whose `name` is `via_AUTO__<P>` (where `<P>` is the own-proxy's name with its single leading underscore stripped) and whose `dialer-proxy` field equals the literal string `Proxies` — the name of the always-present global selector group emitted per 001 FR-009a. All other fields are copied from the source own-proxy under the same rules as FR-003. Exactly one AUTO copy per own-proxy is emitted regardless of how many `_region_*`/`_continent_*` groups exist (including the degenerate case where zero such groups exist).
- **FR-005**: If a source own-proxy declares an explicit `dialer-proxy` field in `own-proxies.yaml`, the system MUST NOT generate any fan-out copies for that own-proxy (neither per-region/per-continent nor AUTO). The original own-proxy is emitted unchanged (with its operator-supplied `dialer-proxy` preserved verbatim).
- **FR-006**: Fan-out generation MUST be deterministic across reloads given identical inputs: stable ordering, stable field order, stable byte output. Ordering is: outer loop over own-proxies in their `own-proxies.yaml` declaration order; inner loop emits the AUTO copy first, then one copy per `_region_*`/`_continent_*` target group in the order they appear in the merged `proxy-groups:` block (which is itself deterministic per 002/003).

#### Always-present `Proxies` selector exclusion

- **FR-007**: The always-present `Proxies` selector group (001 FR-009a) MUST NOT include any own-proxy (a proxy whose name starts with `_` followed by content that does NOT match either `region_*` or `continent_*` — i.e., own-proxies but not server-emitted region/continent groups) as a direct member of its `proxies:` list.
- **FR-008**: The always-present `Proxies` selector group MUST NOT include any fan-out proxy (a proxy whose name starts with the literal `via_`) as a direct member of its `proxies:` list.
- **FR-009**: Operator-declared own-groups (002 FR-007b's `_<original-group>` form) MUST remain members of the always-present `Proxies` selector exactly as they are today (this feature does not change FR-009a's treatment of server-emitted region/continent groups, upstream proxies, or own-groups; only the membership of own-proxies and fan-out copies is changed).
- **FR-010**: Own-groups that reference own-proxies in their `proxies:` list (e.g., the operator's `_Canada-Exit-Proxies` group with members `[_montreal, _montreal-spare, _markham]`) MUST retain those references unchanged. This feature does not rewrite own-group members; the own-proxies remain reachable through any own-group that names them.

#### Group membership of fan-out copies

- **FR-011**: Fan-out proxies MUST NOT be added to any `_region_<CC>`, `_continent_<CONT>`, or `_region_UNKNOWN` server-emitted group, even though their names embed a region/continent code. (This preserves 002 FR-012 and 003 FR-017's invariant: own-derived proxies are excluded from server-emitted region/continent groups regardless of name content.)
- **FR-012**: Fan-out proxies MUST NOT be auto-added to any operator-declared own-group. Operators who want a fan-out proxy in an own-group MUST list it explicitly in `own-proxies.yaml` (the system does not synthesize own-group memberships).
- **FR-013**: Fan-out proxies MUST NOT be added to any upstream-contributed proxy-group's `proxies:` list (no rewrite of upstream `proxies:` member lists is performed).

#### Determinism & correctness

- **FR-014**: Total fan-out count MUST equal `count(own-proxies without explicit dialer-proxy) × (1 + count(emitted _region_*, _continent_*, _region_UNKNOWN groups))` — the `+1` accounts for the AUTO copy emitted per own-proxy per FR-004a. Deviations indicate a bug.
- **FR-015**: Two consecutive served-config fetches with identical inputs (own-proxies, upstream cache, custom rules) MUST produce byte-identical fan-out sections.

### Key Entities

- **Own-Proxy**: An operator-declared proxy in `own-proxies.yaml`, post-002 rewrite carries the name `_<original>`. Source for fan-out generation. Existing entity from 001; this feature adds no new fields.
- **Server-emitted Region Group**: A `_region_<CC>` or `_region_UNKNOWN` proxy-group emitted by the merge per 002 FR-013 / 003 FR-014. Read-only input to fan-out.
- **Server-emitted Continent Group**: A `_continent_<CONT>` proxy-group emitted per 003 FR-009. Read-only input to fan-out.
- **Fan-out Proxy** (new): A synthesized proxy in the merged `proxies:` block, named `via_<G>__<P>`, that carries one source own-proxy's connection fields plus a `dialer-proxy: <group-name>` field. One per-region/per-continent fan-out proxy is emitted per (own-proxy, server-emitted region/continent group) pair, subject to FR-005's skip rule.
- **AUTO Fan-out Proxy** (new): A special-case fan-out proxy named `via_AUTO__<P>` whose `dialer-proxy` is the literal string `Proxies` (the always-present global selector). One per own-proxy, subject to FR-005's skip rule. Behaviorally lets the user pick once at the global `Proxies` selector and have all `via_AUTO__<own>` traffic chain through that selection.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Given an own-proxies file with N proxies (none with explicit `dialer-proxy`) and a merged config emitting M `_region_*`/`_continent_*` groups, the served `proxies:` block contains exactly N + (N × (M + 1)) own-derived entries — N originals plus N × M per-region/per-continent fan-out copies plus N AUTO copies.
- **SC-002**: Every per-region/per-continent fan-out proxy in the served body satisfies all three: (a) name matches `^via_(region|continent)_[A-Za-z]+__.+$`, (b) `dialer-proxy:` field equals `_<G>` where `<G>` is the segment after the leading `via_` and before the literal `__` separator, (c) every other field equals the corresponding field in the source own-proxy byte-for-byte.
- **SC-002a**: Every AUTO fan-out proxy in the served body satisfies all three: (a) name matches `^via_AUTO__.+$`, (b) `dialer-proxy:` field equals the literal string `Proxies`, (c) every other field equals the corresponding field in the source own-proxy byte-for-byte.
- **SC-003**: The always-present `Proxies` selector group's `proxies:` member list contains **zero** entries whose name starts with `_` followed by anything other than `region_` or `continent_`, and **zero** entries whose name starts with `via_`. (Verifiable by parsing the served YAML and scanning the group's member list.)
- **SC-004**: Two reloads of the same input produce byte-identical fan-out sections (snapshot stable; CI snapshot drift check passes).
- **SC-005**: A Mihomo client given the served config can route a connection through a fan-out proxy: a matching rule with target `via_region_JP__markham` results in the client establishing a chain `client → _region_JP → markham endpoint → destination` (verified end-to-end either by integration test using a fake Mihomo or by manual smoke test against a live client).
- **SC-006**: The new fan-out section adds no more than 50 ms of overhead to the merged-config build for typical inputs (≤10 own-proxies × ≤30 region/continent groups = ≤310 fan-out copies including AUTO). Justification: fan-out is `O(N×(M+1))` byte concatenation with no upstream fetches; the budget is loose to leave room for YAML node deep-copy.

## Assumptions

- **Source-of-truth for own-proxies**: This feature uses the existing `own-proxies.yaml` mechanism (001 FR-006, loaded by `internal/config/own_proxies.go`) and the existing post-002 underscore-prefix rewrite (`_<original>`). No new config file or env var is introduced.
- **Target groups**: Per-region/per-continent fan-out targets every proxy-group whose name starts with `_region_` or `_continent_` in the merged output, including `_region_UNKNOWN` and the always-present catch-all from 003. The operator's own-groups (`_<original-group>`) are NOT fan-out targets. The AUTO fan-out copy (FR-004a) targets the always-present global `Proxies` selector — exactly one AUTO copy per own-proxy regardless of the region/continent group set.
- **Original own-proxies remain emitted**: The original own-proxies (with their post-002 `_<original>` names) continue to be emitted in the served `proxies:` block. The fan-out copies are additive, not a replacement.
- **Original own-groups remain emitted**: The operator's own-groups (post-002 `_<original-group>` form) continue to be emitted in `proxy-groups:` exactly as today, including their `proxies:` member lists referencing the original own-proxies. Operators retain full control over how own-proxies are exposed via their own-groups.
- **Custom rules continue to work**: Custom rules (003) can target fan-out proxy names directly (e.g., `RULE-TYPE,...,via_region_JP__markham`), and Mihomo will resolve them as long as the proxy exists in `proxies:`. No special rule-validation pass is required.
- **No fan-out for upstream proxies**: Fan-out applies exclusively to own-proxies. Upstream-contributed proxies (post-002 `<provider>_<original>` names) are NOT fanned out, regardless of their region/continent classification. Operators who want chained routing through upstream proxies use upstream-defined groups directly.
- **No fan-out for proxy-groups**: As stated in the user description ("just Proxies, not Proxy Groups"), the fan-out applies to individual proxies only. Proxy-groups (server-emitted, own-declared, or upstream-contributed) are not fanned out.
- **Field copy semantics**: "Copy verbatim" means a YAML node deep-copy of the source own-proxy's mapping, with the `name` value replaced and a `dialer-proxy` entry inserted. Comments, anchors, and tags on the source mapping are not preserved (consistent with the rest of the merge pipeline, which works on parsed `*yaml.Node` values rather than raw bytes).
- **Operator-set `dialer-proxy` skip rule**: Per FR-005, an own-proxy that already declares `dialer-proxy` is skipped entirely for fan-out. This prevents producing N×M copies whose dialer chains contradict the operator's explicit choice and lets the operator opt out per-proxy by setting `dialer-proxy` themselves.
- **Naming conflict tolerance**: The leading-underscore convention (002 FR-007a) ensures own-proxies don't collide with upstream proxies; the `via_` prefix is reserved by this feature for fan-out copies. Operators are advised not to declare own-proxies whose names start with `via_` (no enforcement is added in this feature, but a warning may be logged if encountered).
