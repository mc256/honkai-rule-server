# Feature Specification: Load-Balance Variants of Auto-Emitted Region & Continent Proxy Groups

**Feature Branch**: `014-load-balance-region-groups`
**Created**: 2026-05-08
**Status**: Draft
**Input**: User description: "We now have _region and _continent proxy group. But they are using url-test. I want to create _lb_region and _lb_continent that uses load-balance. (...example yaml with type: load-balance, url, interval: 300, lazy, strategy: round-robin, timeout: 1500, max-failed-times: 3...). This should be generated before multiplying our own proxies, so that we should have `via_lb_region_JP__alpha` where alpha is one of our own proxies."

**Anchors**:
- [`002-namespacing-and-regions/spec.md`](../002-namespacing-and-regions/spec.md) — introduced `_region_<CC>` groups with country-code inference.
- [`003-custom-rules-access-control/spec.md`](../003-custom-rules-access-control/spec.md) — introduced `_continent_<CONT>` and `_region_UNKNOWN` catch-all groups.
- [`008-dialer-proxy-fanout/spec.md`](../008-dialer-proxy-fanout/spec.md) — fans out own-proxies into `via_<group>__<own>` copies per server-emitted region/continent group; this feature's new groups must participate in that same fan-out.
- [`012-url-test-region-groups/spec.md`](../012-url-test-region-groups/spec.md) — converted `_region_*` and `_continent_*` groups from `select` to `url-test` and introduced the five `URL_TEST_*` env vars; this feature is the parallel-group sibling that adds `load-balance` variants without touching the url-test groups.
- The existing `_region_<CC>`, `_region_UNKNOWN`, and `_continent_<CONT>` groups (per 002 / 003 / 012) and the always-present `Proxies` selector (001 FR-009a) are unchanged by this feature. New parallel groups are emitted **in addition** to them.

## Clarifications

### Session 2026-05-08

- Q: Should `_lb_continent_<CONT>` use the same `proxies:` member list as `_continent_<CONT>` (which is a flat union of all underlying upstream proxy names per 003 FR-011), or instead reference the new `_lb_region_<CC>` groups? → A: Same member list as `_continent_<CONT>` — verbatim flat union of upstream proxy names produced by `AppendContinentGroups`'s region-grouped concatenation. Only the proxy-group-level configuration (`type: load-balance`, the load-balance health-check + strategy fields) differs. The continent-level lb group does NOT nest `_lb_region_<CC>` (or any other group) inside another load-balance.
- Q: Should the system also emit an AUTO load-balance fan-out copy (e.g., `via_lb_AUTO__<own>`) per own-proxy, paralleling 008's `via_AUTO__<own>`? → A: No. The lb fan-out covers ONLY `via_lb_region_*__<own>` and `via_lb_continent_*__<own>`. 008's existing `via_AUTO__<own>` (whose `dialer-proxy` is the always-present `select` group `Proxies`) is unchanged and not duplicated as a load-balance variant. No new top-level load-balance selector is introduced.
- Q: Should `LOAD_BALANCE_STRATEGY` accept all three Mihomo strategies (`round-robin`, `consistent-hashing`, `sticky-sessions`), or restrict to `round-robin`? → A: Accept all three. Default `round-robin`; startup loud-fail on any other value (per FR-005). Mihomo's wire format is preserved exactly; operators can opt into `consistent-hashing` or `sticky-sessions` without a code change.
- Q: Should the new `_lb_region_*` / `_lb_continent_*` groups be added as direct members of the always-present `Proxies` selector (alongside the existing `_region_*` / `_continent_*` entries)? → A: Yes. They join the `Proxies` selector as direct members so users see lb regional pools side-by-side with url-test pools in the Mihomo UI, preserving discoverability. The roughly-doubled selector size is acceptable for v1.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Spread regional traffic across all healthy nodes simultaneously (Priority: P1)

The user routes traffic that should fan out across all healthy proxies in a region, not pin to the single fastest one. The current `_region_JP` group (added by 012) is `url-test` — it picks the lowest-latency Japan node and sends every connection through it; the other Japan nodes sit idle. For workloads where the user wants per-connection diversity (parallel downloads, multiple browser tabs hitting the same site, anti-fingerprinting), they would prefer to distribute connections across `alpha_jp1`, `alpha_jp2`, `beta_jp1`, … round-robin.

The user wants a parallel proxy group `_lb_region_JP` of type `load-balance` (Mihomo's built-in load-balancer) carrying the same Japan member proxies as `_region_JP` but distributing connections across them per the configured strategy (default `round-robin`). The url-test group (`_region_JP`) remains available for failover-pinning workloads; the new `_lb_region_JP` group is opt-in via custom rules.

**Why this priority**: This is the primary user value. Without it, users who want per-connection load distribution have no way to express it via the served subscription — they'd have to hand-author a load-balance group in their local config and lose the auto-namespacing/fan-out benefits. P1 because the rest of the feature (continent variant, fan-out compatibility) is structural support for this story.

**Independent Test**: Configure two upstream sources contributing several JP-tagged proxies. Fetch the served subscription with a Mihomo client. Issue many concurrent requests through a custom rule targeting `_lb_region_JP`. Assert: (a) the served `_lb_region_JP` group renders with `type: load-balance`, the operator-configured `url`, `interval`, `timeout`, `max-failed-times`, `lazy`, and `strategy: round-robin` (or whatever strategy is configured), (b) its `proxies:` member list is identical to `_region_JP`'s member list, (c) connections are distributed across all healthy members rather than pinned to one, (d) the original `_region_JP` group still exists and still has `type: url-test` with its 012-defined fields.

**Acceptance Scenarios**:

1. **Given** a region group `_region_JP` exists with three member proxies, **When** the served subscription is fetched, **Then** a parallel group `_lb_region_JP` is also emitted with the same three member proxies, `type: load-balance`, and the operator-configured load-balance health-check + strategy fields.
2. **Given** the same fixture, **When** the served subscription is fetched, **Then** the original `_region_JP` group is still emitted with `type: url-test` and its 012-defined fields — the lb variant is additive, never a replacement.
3. **Given** a continent group `_continent_AS` exists with several member region groups, **When** the served subscription is fetched, **Then** a parallel `_lb_continent_AS` group is emitted with the same members but `type: load-balance` (members are the underlying `_region_<CC>` groups, NOT the `_lb_region_<CC>` groups; see Edge Cases).
4. **Given** a region group `_region_UNKNOWN` exists (catch-all per 003), **When** the served subscription is fetched, **Then** a parallel `_lb_region_UNKNOWN` is emitted (the prefix rule applies regardless of whether the suffix is a real country code).
5. **Given** the served subscription, **When** the always-present `Proxies` selector group is inspected, **Then** the new `_lb_region_*` and `_lb_continent_*` groups appear as direct members of its `proxies:` list (alongside the existing `_region_*` / `_continent_*` groups), so users can pick a load-balanced regional pool from the global selector UI.
6. **Given** the operator overrides any of the load-balance health-check fields or `strategy` via configuration, **When** the served subscription is fetched, **Then** the rendered lb groups reflect the override and the change is noted in startup logs.
7. **Given** the served subscription is fetched twice with the same upstream snapshot and same operator config, **When** the byte output is compared, **Then** the two responses are byte-identical (Constitution Principle II — determinism preserved).

---

### User Story 2 - Reach own-exits through a load-balanced regional pool (Priority: P1)

The operator declares own-proxies (e.g., `montreal`, `markham` per `own-proxies.yaml`) and wants traffic to chain through a regional load-balanced pool first, then exit through the chosen own-node — analogous to 008's `via_region_JP__markham` but using the new `_lb_region_*` group as the dialer instead of the url-test pool. The intent is identical to 008's per-region fan-out, but the chain begins with a load-balanced first hop rather than a latency-pinned one. This produces fan-out names like `via_lb_region_JP__markham`.

**Why this priority**: This is the second deliverable named explicitly in the user request ("we should have `via_lb_region_JP__alpha`"). Without it, US1 produces useful regional load-balanced groups but operators cannot chain through their own exits via load-balanced first hops — losing parity with 008's existing `via_region_JP__*` pattern. P1 because the user explicitly described it.

**Independent Test**: With one upstream contributing JP and HK proxies and three operator-declared own-proxies (`montreal`, `montreal-spare`, `markham`), fetch the merged config. Assert that for every emitted `_lb_region_<CC>` group AND every emitted `_lb_continent_<CONT>` group, every own-proxy (without an explicit `dialer-proxy`) yields exactly one fan-out copy whose name matches `via_lb_region_<CC>__<own>` or `via_lb_continent_<CONT>__<own>`, whose `dialer-proxy` field equals the corresponding `_lb_region_<CC>` / `_lb_continent_<CONT>` group, and whose other fields match the source own-proxy verbatim. Assert that 008's existing `via_region_<CC>__<own>` and `via_continent_<CONT>__<own>` fan-out copies are still present unchanged. Assert the AUTO copy (`via_AUTO__<own>`) is still emitted exactly once per own-proxy (not duplicated by this feature).

**Acceptance Scenarios**:

1. **Given** an own-proxy `markham` (no explicit `dialer-proxy`) and an emitted `_lb_region_JP`, **When** the merged config is served, **Then** the served `proxies:` block contains a fan-out copy named `via_lb_region_JP__markham` whose `dialer-proxy: _lb_region_JP` is set, with all other connection fields copied from `_markham` verbatim.
2. **Given** the same own-proxy and an emitted `_lb_continent_AS`, **When** the merged config is served, **Then** a fan-out copy `via_lb_continent_AS__markham` is also emitted with `dialer-proxy: _lb_continent_AS`.
3. **Given** an own-proxy with an explicit `dialer-proxy:` field declared in `own-proxies.yaml`, **When** the merged config is served, **Then** **no** `via_lb_region_*__<own>` or `via_lb_continent_*__<own>` copies are emitted for that own-proxy (consistent with 008 FR-005's per-own-proxy skip rule). The skip rule also already suppressed 008's `via_region_*` and AUTO copies for that own-proxy; that pre-existing behavior is unchanged.
4. **Given** the always-present `Proxies` selector group, **When** the merged config is served, **Then** the new `via_lb_region_*__<own>` and `via_lb_continent_*__<own>` fan-out copies are **not** members of its `proxies:` list (consistent with 008 FR-008's exclusion of all `via_*` names from `Proxies`).
5. **Given** N own-proxies (none with explicit `dialer-proxy`) and M emitted `_region_*`/`_continent_*` groups (which equals the count of emitted `_lb_region_*`/`_lb_continent_*` groups, since they are 1:1), **When** the merged config is served, **Then** the fan-out section contains exactly `N × (1 + 2M)` own-derived copies — N AUTO copies + N×M `via_region_*`/`via_continent_*` copies (from 008) + N×M `via_lb_region_*`/`via_lb_continent_*` copies (this feature). Total own-derived entries in `proxies:` (originals + fan-outs) = N + N × (1 + 2M).

---

### Edge Cases

- **`_lb_continent_<CONT>` membership composition**: A continent group's members in 003 FR-011 are a flat union of all upstream proxy names from the constituent `_region_<CC>` groups (NOT region-group references, despite the name suggesting nesting). The `_lb_continent_<CONT>` parallel group uses the SAME flat member list as `_continent_<CONT>` — i.e., it round-robins across all upstream proxies in that continent directly, with no nested groups. Justification: this matches the user's clarification ("same as _continent_<CONT>") and avoids any nested-group construction that would either double-randomize (if pointed at `_lb_region_<CC>`) or surprise the operator with type-mismatched references. Documented as a deliberate design choice in Assumptions.
- **Empty member list** (a region whose proxies all dropped out): the parallel `_lb_*` group is still emitted iff its url-test sibling is emitted (1:1 emission rule). Mihomo's behavior on a zero-member load-balance group is to refuse the connection — same fallback as a zero-member url-test group. This case shouldn't arise in practice (the upstream snapshot would have to drop a region between snapshot loads), but the rendering rule is robust.
- **Single-member `_lb_region_<CC>`**: load-balance with one member degenerates to "always pick that one" — semantically equivalent to a url-test with one member. Still emitted for naming consistency; no special-case suppression.
- **Operator misconfigures the load-balance probe URL or strategy** (typo / unsupported strategy value): startup loud-fails per Constitution Principle III for the strategy enum (FR-005); the URL is parsed as-is per 012's precedent.
- **Custom rules already targeting `_region_*` or `_continent_*`**: unchanged — those groups are unchanged. Custom rules targeting the new `_lb_region_*` / `_lb_continent_*` names work because those names now exist in the served `proxy-groups:` block. No rule-validation pass is required.
- **A user-defined custom proxy-group with the prefix `_lb_region_` or `_lb_continent_`**: by convention the underscore prefix is reserved for server-emitted groups; the same 002-derived namespacing prefix-rule applies. If an upstream provider includes a group with such a prefix, 002's per-source prefixing prepends the source name (`alpha__lb_region_JP`), so it cannot collide with the server-emitted `_lb_region_JP`.
- **Operator declares an own-proxy literally named `lb_region_JP__markham`** (or any name colliding with a fan-out): the leading-underscore rewrite makes it `_lb_region_JP__markham`, which is distinct from the fan-out `via_lb_region_JP__markham` (which carries the `via_` prefix). Per 008's analysis, fan-out names in the `via_*` namespace cannot collide with own-proxy names in the `_*` namespace. No conflict possible.
- **Mihomo / Clash version that doesn't support load-balance**: out of scope. The project already targets stock Mihomo, where `load-balance` (with `round-robin`, `consistent-hashing`, `sticky-sessions` strategies) has been supported for years.
- **`Profile-Update-Interval` shrinks the client's refresh frequency below the load-balance `interval` health-check value**: harmless (independent of subscription refresh; same as 012's analysis).

## Requirements *(mandatory)*

### Functional Requirements

#### Emission of parallel load-balance groups

- **FR-001**: For every server-emitted region group `_region_<CC>` (per 002 FR-013, including `_region_UNKNOWN` per 003), the server MUST also emit a parallel proxy group `_lb_region_<CC>` carrying the same `proxies:` member list and `type: load-balance`.

- **FR-002**: For every server-emitted continent group `_continent_<CONT>` (per 003 FR-009 / FR-011), the server MUST also emit a parallel proxy group `_lb_continent_<CONT>` carrying the same `proxies:` member list (per 003 FR-011: a flat union of all upstream proxy names from the constituent `_region_<CC>` groups, ordered region-CC alphabetical then by proxy order within region — NOT `_region_<CC>` references and NOT `_lb_region_<CC>` references) and `type: load-balance`.

- **FR-003**: For every group emitted under FR-001 / FR-002, the server MUST populate the following load-balance health-check + strategy fields:
  - `url` — the HTTP(S) URL the client probes for liveness (operator-configured; default `https://www.gstatic.com/generate_204`).
  - `interval` — seconds between probes (operator-configured; default `300`).
  - `timeout` — milliseconds before a probe is considered failed (operator-configured; default `1500`).
  - `max-failed-times` — number of consecutive failed probes before the client treats the proxy as unhealthy (operator-configured; default `3`).
  - `lazy` — boolean; if `true`, probes only run when the group is actively in use (operator-configured; default `true`).
  - `strategy` — one of `round-robin`, `consistent-hashing`, `sticky-sessions` (operator-configured; default `round-robin`).

- **FR-004**: The six load-balance fields MUST be operator-configurable via environment variables, one variable per field, in the same pattern used by 012's `URL_TEST_*` vars but under a separate `LOAD_BALANCE_*` namespace (so url-test and load-balance defaults can diverge — they describe semantically different probes):

  | Env var | Default | Type | YAML field |
  |---|---|---|---|
  | `LOAD_BALANCE_URL` | `https://www.gstatic.com/generate_204` | string | `url` |
  | `LOAD_BALANCE_INTERVAL_SECONDS` | `300` | integer | `interval` |
  | `LOAD_BALANCE_TIMEOUT_MS` | `1500` | integer | `timeout` |
  | `LOAD_BALANCE_MAX_FAILED_TIMES` | `3` | integer | `max-failed-times` |
  | `LOAD_BALANCE_LAZY` | `true` | boolean | `lazy` |
  | `LOAD_BALANCE_STRATEGY` | `round-robin` | enum | `strategy` |

  Defaults match the user-confirmed example exactly. Unset or empty env vars MUST fall back to these defaults (consistent with 010's `FALLBACK_RULE_TARGET` and 012's `URL_TEST_*` "empty → default" behavior). A single set of values applies to ALL auto-emitted `_lb_region_*` / `_lb_continent_*` groups; per-group overrides are out of scope for this feature.

- **FR-005**: Validation at startup — invalid values MUST cause a loud, fatal startup failure (Constitution Principle III: strict-schema-loud-fail). Specifically:
  - `LOAD_BALANCE_INTERVAL_SECONDS`, `LOAD_BALANCE_TIMEOUT_MS`, `LOAD_BALANCE_MAX_FAILED_TIMES` MUST be integers ≥ 1. A non-integer value, a negative value, or zero MUST cause startup to abort with a structured error log identifying the offending env var and value.
  - `LOAD_BALANCE_LAZY` MUST be one of `true` / `false` (case-insensitive). Other values MUST cause startup abort.
  - `LOAD_BALANCE_STRATEGY` MUST be one of `round-robin`, `consistent-hashing`, `sticky-sessions` (case-sensitive — Mihomo accepts only those literals). Other values MUST cause startup abort.
  - `LOAD_BALANCE_URL` is parsed as-is (no scheme/format validation; operator typos surface at runtime via probe failures, which the operator notices via FR-009's startup log).

- **FR-006**: Field ordering for emitted `_lb_region_*` / `_lb_continent_*` groups MUST follow 004's existing block-style + field-ordering convention (`name`, `type`, `proxies` first), then the load-balance fields in this order: `url`, `interval`, `lazy`, `strategy`, `timeout`, `max-failed-times`. The order matches the user-supplied example exactly so the served YAML reads the same way the operator wrote it.

- **FR-007**: The url-test groups (`_region_*` and `_continent_*` per 012) MUST remain unchanged in name, type, member list, and field set. This feature is purely additive — it never modifies, replaces, or suppresses the url-test groups.

- **FR-008**: Determinism: the served YAML for any `_lb_region_*` or `_lb_continent_*` group MUST be byte-identical across two requests with the same upstream snapshot, the same operator configuration, and the same clock value. The load-balance fields are static given the operator config; they don't introduce nondeterminism.

- **FR-009**: At server startup, the resolved values of all six load-balance parameters (`url`, `interval`, `timeout`, `max-failed-times`, `lazy`, `strategy`) MUST be logged at INFO level alongside 012's existing url-test startup-log line and the rest of the startup configuration so the operator can confirm the active configuration without inspecting the served body.

#### Membership in always-present `Proxies` selector

- **FR-010**: The new `_lb_region_*` and `_lb_continent_*` groups MUST be added as direct members to the always-present `Proxies` selector group (001 FR-009a) — alongside the existing `_region_*` / `_continent_*` members. This makes the load-balanced regional pools visible in Mihomo's UI selector, parallel to how the url-test pools are visible. Ordering within the `Proxies` selector's member list MUST be deterministic (see FR-013).

- **FR-011**: The `via_lb_region_*__<own>` and `via_lb_continent_*__<own>` fan-out copies (FR-014) MUST NOT be added as direct members of the `Proxies` selector group, consistent with 008 FR-008's exclusion of all `via_*` names from `Proxies`.

#### Group emission ordering (for fan-out compatibility)

- **FR-012**: The new `_lb_region_*` / `_lb_continent_*` groups MUST be emitted into `proxy-groups:` **before** 008's own-proxy fan-out runs, so that 008's fan-out treats them as eligible target groups (per 008 FR-001's "every server-emitted proxy group whose name starts with `_region_` or `_continent_`" — see FR-014a for the prefix-match adjustment). Concretely: in the merge pipeline, the lb-group emission step MUST execute before the fan-out step.

- **FR-013**: Emission order within the `proxy-groups:` block MUST be deterministic. The recommended ordering (preserved across reloads): for each region group emitted by 012's path, emit `_region_<CC>` first then immediately `_lb_region_<CC>`; same pattern for continents (`_continent_<CONT>` then `_lb_continent_<CONT>`). This produces a readable, paired layout in the served YAML and a stable `Proxies` member list.

#### Fan-out compatibility (extending 008)

- **FR-014**: 008's fan-out rule (008 FR-001) applies to the new groups — the system MUST emit one `via_<G>__<P>` fan-out copy per (own-proxy without explicit `dialer-proxy`, server-emitted lb group). For an own-proxy `markham` and an emitted `_lb_region_JP`, this produces `via_lb_region_JP__markham` with `dialer-proxy: _lb_region_JP` and the source own-proxy's other fields verbatim.

- **FR-014a**: Adjustment to 008 FR-001's "name starts with `_region_` or `_continent_`" predicate: this feature requires the predicate to also accept `_lb_region_` and `_lb_continent_` prefixes so the fan-out treats the new groups as eligible targets. Equivalent operational definition: the fan-out targets every `proxy-groups:` entry that is server-emitted (i.e., starts with a single underscore) AND whose name suffix matches `(lb_)?(region|continent)_<token>`. Custom user-defined groups (003) and operator-declared own-groups (002) remain excluded as before.

- **FR-015**: The total fan-out count satisfies: `count(own-proxies without explicit dialer-proxy) × (1 + count(_region_*) + count(_continent_*) + count(_lb_region_*) + count(_lb_continent_*))`. Since `_lb_region_*` and `_region_*` are 1:1, and `_lb_continent_*` and `_continent_*` are 1:1, this simplifies to `N × (1 + 2M)` where M is the count of url-test region/continent groups.

- **FR-016**: 008 FR-005's per-own-proxy skip rule (an own-proxy with an explicit `dialer-proxy` field generates **no** fan-out copies) MUST also suppress the new `via_lb_*__<own>` copies. The skip rule is per-own-proxy, not per-target-group, so it applies uniformly across url-test fan-out, lb fan-out, and AUTO.

- **FR-017**: Fan-out copies generated by this feature MUST follow the same rules as 008 FR-002..FR-006: `name = via_<G>__<P>` where `<G>` is the target group name with its leading underscore stripped (so `_lb_region_JP` → `lb_region_JP`) and `<P>` is the own-proxy's name with its leading underscore stripped (so `_markham` → `markham`); separator is exactly two underscores; field-copy semantics; deterministic ordering.

#### Custom-rules and own-groups invariants

- **FR-018**: Custom user-defined proxy groups (loaded from custom-rules YAML files per 003) MUST NOT be modified by this feature. The lb conversion applies ONLY to groups whose name starts with `_region_` or `_continent_` AND were emitted by the server (i.e., the same `AppendRegionGroups` / `AppendContinentGroups` code path 012 modified). Custom rules MAY target `_lb_region_*` / `_lb_continent_*` names directly; Mihomo will resolve them as long as those groups exist in the served `proxy-groups:` block.

- **FR-019**: Operator-declared own-groups (002 FR-007b) MUST NOT be auto-rewritten to reference the new lb groups. Operators who want a load-balanced first hop in an own-group must reference `_lb_region_<CC>` / `_lb_continent_<CONT>` explicitly in their own-group's `proxies:` list.

- **FR-020**: The new lb groups MUST NOT include own-proxies as members (002 FR-012 / 003 FR-017's existing invariant — own-derived proxies are excluded from server-emitted region/continent groups regardless of variant).

### Key Entities

- **Auto-emitted load-balance region group** (`_lb_region_<CC>`, new): A proxy group server-emitted in parallel to `_region_<CC>` carrying the same member list and `type: load-balance`. One emitted per upstream-classified country (and one for `_lb_region_UNKNOWN` mirroring 003's catch-all).
- **Auto-emitted load-balance continent group** (`_lb_continent_<CONT>`, new): A proxy group server-emitted in parallel to `_continent_<CONT>` carrying the same member list (a flat union of upstream proxy names from the constituent regions per 003 FR-011) and `type: load-balance`.
- **Load-balance parameter set** (new): A 6-tuple (`url`, `interval`, `timeout`, `max-failed-times`, `lazy`, `strategy`) shared across all auto-emitted lb region and lb continent groups. Operator-configured at server startup via `LOAD_BALANCE_*` env vars; embedded verbatim in every emitted lb group's YAML. Distinct from 012's `URL_TEST_*` parameter set — they describe semantically different probes (latency-pinning vs round-robin distribution).
- **LB fan-out proxy** (new variant of 008's fan-out proxy): A synthesized proxy in the merged `proxies:` block, named `via_lb_region_<CC>__<P>` or `via_lb_continent_<CONT>__<P>`, carrying one source own-proxy's connection fields plus a `dialer-proxy: _lb_region_<CC>` (or `_lb_continent_<CONT>`) field. Emitted per (own-proxy, lb group) pair, subject to 008 FR-005's skip rule. Indistinguishable in machinery from 008's existing fan-out proxies — only the target group name differs.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For every emitted `_region_<CC>` group, a parallel `_lb_region_<CC>` group exists in the served `proxy-groups:` block with the same member list, `type: load-balance`, and the six configured load-balance fields. The same parity holds for `_continent_<CONT>` and `_lb_continent_<CONT>`.

- **SC-002**: The url-test groups (`_region_*`, `_continent_*`) in the served body are byte-identical before and after this feature ships, when the operator configuration is otherwise unchanged. Verifiable by snapshot diff: only NEW lines appear (the lb groups + the lb fan-out copies + the lb members in the `Proxies` selector); no existing lines are modified.

- **SC-003**: For a fixture with at least one `_region_*` and one `_continent_*` group, the served YAML for the new `_lb_region_*` and `_lb_continent_*` groups (and their fan-out copies) is byte-identical across two requests served against the same upstream snapshot, same operator config, and same fixed clock.

- **SC-004**: When the operator overrides any of the six load-balance fields via `LOAD_BALANCE_*` environment configuration and restarts the pod, the served body for every auto-emitted `_lb_region_*` / `_lb_continent_*` group reflects the override on the next served request. Independence from `URL_TEST_*` is verified: changing `URL_TEST_INTERVAL_SECONDS` MUST NOT affect any lb group, and changing `LOAD_BALANCE_INTERVAL_SECONDS` MUST NOT affect any url-test group.

- **SC-005**: A Mihomo client configured against the served subscription, with `_lb_region_JP` selected as the active proxy, sends consecutive connections through different healthy member proxies (per the configured `strategy`). At least 3 different members are observed as the connection's first hop across 10 connections (assuming ≥3 healthy members and `strategy: round-robin`). This validates load-balance behavior end-to-end against a real client.

- **SC-006**: Given an own-proxies file with N proxies (none with explicit `dialer-proxy`) and the merged config emitting M url-test region/continent groups (and therefore M lb region/continent groups), the served `proxies:` block contains exactly N + N×(1 + 2M) own-derived entries — N originals + N AUTO copies + N×M url-test fan-out copies + N×M lb fan-out copies.

- **SC-007**: Every lb fan-out proxy in the served body matches `^via_lb_(region|continent)_[A-Za-z]+__.+$`, has a `dialer-proxy` field equal to `_lb_<group-suffix>` (e.g., `_lb_region_JP`), and every other field equals the corresponding field in the source own-proxy byte-for-byte.

- **SC-008**: The startup log emitted on a fresh pod boot includes the resolved values of `url`, `interval`, `timeout`, `max-failed-times`, `lazy`, `strategy` for the lb parameter set as a single structured log line (distinct from 012's url-test log line so the operator can grep for either).

- **SC-009**: The always-present `Proxies` selector group's `proxies:` member list contains every `_lb_region_*` and `_lb_continent_*` group as a direct member (alongside the existing `_region_*` / `_continent_*` members per 012 / 003). It contains **zero** entries whose name starts with `via_lb_` (consistent with 008's exclusion of all `via_*` names from `Proxies`).

- **SC-010**: Custom user-defined proxy groups (loaded from `config/custom-rules/`) appearing in the served body have unchanged `type` (whatever they declared) — this feature does NOT touch their type field, even if their name happens to start with `_lb_` (which by convention should not occur, but is robust against). Verifiable by diffing the served body's custom-group section against pre-feature snapshots.

## Assumptions

- The operator's example values in the user description (`url: https://www.gstatic.com/generate_204`, `interval: 300`, `timeout: 1500`, `max-failed-times: 3`, `lazy: true`, `strategy: round-robin`) are the intended defaults. They differ from 012's url-test defaults (`interval: 10`, `timeout: 3000`) by design: load-balance probes have looser cadence because traffic is already distributed across members, so per-member health doesn't pivot routing as aggressively as in url-test.
- The lb groups are **additive**, not a replacement. The url-test groups remain emitted with their 012 semantics; `_lb_region_*` / `_lb_continent_*` are new parallel groups for users who want round-robin distribution. This was the user's explicit intent ("I want to **create** _lb_region and _lb_continent").
- The `_lb_continent_<CONT>` group's members are the flat union of upstream proxy names produced by 003's continent-emission path (matching `_continent_<CONT>` exactly), not `_region_<CC>` or `_lb_region_<CC>` references. Reasoning: this matches the user's clarification ("same as _continent_<CONT>"), and 003 FR-011 already emits flat proxy names rather than nested group references, so the lb variant inherits that shape unchanged.
- A single set of load-balance parameters is sufficient for v1; per-region or per-continent overrides are a future concern (would need a more elaborate config schema and probably aren't needed in practice).
- Mihomo / Clash `load-balance` groups are stable in modern client versions; the wire format `type: load-balance` plus the six fields is well-supported. The project already targets stock Mihomo per existing tests.
- Backward compatibility for stock Mihomo / Clash clients: no client-side change is needed. Clients refresh on their normal `Profile-Update-Interval` cadence and see the new groups + new fan-out names automatically. Existing custom rules targeting `_region_*` / `_continent_*` continue to work unchanged.
- `LOAD_BALANCE_*` env vars are a new namespace separate from `URL_TEST_*`. Reasoning: while the field set overlaps in five of six fields, the semantic intent and tuned defaults differ enough that aliasing them onto a single env var set would be confusing. Operators tuning one shouldn't accidentally re-tune the other.
- The server's existing snapshot test suite covers the served body byte-for-byte. This feature lands a deliberate, reviewable diff in those snapshots: new `_lb_region_*` / `_lb_continent_*` groups appear in `proxy-groups:`, new `via_lb_region_*__<own>` / `via_lb_continent_*__<own>` copies appear in `proxies:`, the always-present `Proxies` selector gains the `_lb_*` group references — and nothing else changes. The snapshot diff is the primary verification artifact for FR-001..FR-019.
- Test coverage: new unit tests for the lb-group emission (one per FR), the load-balance env-var parsing + validation (FR-005), the fan-out interaction (FR-014..FR-017). Existing snapshot tests get one deliberate update across the fixture suite. No new integration tests are mandatory beyond updating snapshots.
- The current project Go code (specifically `internal/merge/region.go`'s `AppendRegionGroups` and `AppendContinentGroups`, plus the 008 fan-out site) is the only place these groups are emitted. The feature scope is bounded to that file plus configuration plumbing, the `Proxies`-selector member-list builder, and snapshots — this is documented anchor for the eventual `plan.md`, not a spec-level prescription.
- Order constraint (FR-012): the new lb groups must be emitted before 008's fan-out runs. In the existing pipeline, region-group emission already precedes fan-out, so adding the lb-group emission alongside (immediately after each url-test sibling) inherits the correct ordering for free. This is documented as an implementation observation, not a new constraint.
