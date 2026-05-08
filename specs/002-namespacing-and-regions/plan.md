# Implementation Plan: Provider Namespacing & Region Grouping (with Trailing-Rule Drop)

**Branch**: `002-namespacing-and-regions` | **Date**: 2026-04-30 | **Spec**: [spec.md](./spec.md)  
**Input**: Feature specification from `/specs/002-namespacing-and-regions/spec.md`

## Summary

Three additive changes to the existing 001 transformation core (`internal/merge/`), bound together because they share the same per-source rewrite pass:

1. **Provider-prefix namespacing** — every proxy, every proxy-group, and every rule target contributed by source `<name>` is rewritten to `<name>_<original>` *before* the merge sees it (US1, FR-004 / FR-005 / FR-006). Built-in identifiers (`DIRECT`, `REJECT`, `REJECT-DROP`, `PASS`) are never prefixed. The CSV `name` column is now constrained to `^[a-z]+$` (FR-001); rows that violate the rule are warn-skipped at load time so other rows still merge (FR-002, intentional deviation from Constitution Principle III's loud-failure default — see Complexity Tracking).
2. **Trailing-rule drop + server-emitted final fallback** — every source's last rule is dropped unconditionally before its rules are concatenated into the merged output (FR-008), then the server emits exactly one `MATCH,<target>` rule at the very end (FR-010 / FR-010a). Default target literal: `auto`; overridable via the new `FALLBACK_RULE_TARGET` env var (consistent with the existing `LOG_LEVEL` / `PROXIES_GROUP_NAME` / `UPSTREAM_USER_AGENT` pattern in `internal/config/server.go`).
3. **Region (ISO 3166-1 alpha-2) grouping** — for every proxy whose original display name carries a recognized country indicator (emoji regional-indicator pair, Chinese country/region name, or English country/region name from a maintained table), a derived `_region_<CC>` proxy-group is emitted with type `select` whose member list is the namespaced proxies inferred to that country (FR-011 / FR-013). Emoji detection is a runtime decode (the alpha-2 letters are encoded directly in U+1F1E6–U+1F1FF); Chinese & English names live in a curated table in a new file `internal/merge/region_table.go`. Region groups with empty membership are omitted (FR-013); region groups are appended as additional members of the always-present `Proxies` selectable group from 001's FR-009a (FR-015).

**Technical approach**: stay inside `internal/merge/` for everything except the new env var (`internal/config/server.go`) and the strengthened `name` validation (`internal/config/subscriptions.go`). The merge layer remains pure-functional (Constitution Principle I); the prefix rewrite happens *per source, pre-merge*, which keeps the downstream collision/group-union/rule-concatenation code structurally unchanged. The 001 collision-suffix machinery (`<name>@<source>`) becomes structurally dead for **cross-source** collisions (prefixes prevent them) but remains live for **own-proxies vs. upstream** collisions and intra-source duplicates — kept as defense-in-depth, doc updated to clarify scope. Snapshot fixtures will drift; refresh in the same PR with `UPDATE_SNAPSHOTS=true go test ./internal/integration/...`.

## Technical Context

**Language/Version**: **Go 1.25 toolchain** (`go.mod` declares 1.22+; production Dockerfile pinned to `golang:1.25-alpine` per c13f009 / b4f9447). Pre-existing.  
**Primary Dependencies**: No new dependencies. Reuses `gopkg.in/yaml.v3` (order-preserving YAML round-trip), `log/slog` (structured warnings), `bradleyjkemp/cupaloy/v2` (snapshot tests). The country-indicator table is plain Go data — no third-party country library.  
**Storage**: No change. In-memory cache + optional disk persistence behavior is unaffected by this feature.  
**Testing**: stdlib `testing` + `cupaloy/v2`. New unit tests under `internal/merge/*_test.go` and `internal/config/*_test.go`. New integration cases in `internal/integration/pipeline_test.go`. Existing snapshots at `internal/integration/testdata/snapshots/` will drift — regenerated and re-reviewed in this PR.  
**Target Platform**: Unchanged — Linux container, K8s deployment, scratch base image.  
**Project Type**: Single-project backend service (unchanged from 001).  
**Performance Goals**: Unchanged. Prefix rewrite is O(N) over proxies/groups/rules per source; for the operator's 2-source / ≤200-proxy workload this is microseconds. Region inference is a single linear scan of the original display name against a ~50-entry table per proxy — well under 1 ms per merge.  
**Constraints**: Byte-identical output across runs (Constitution Principle II) preserved by: (a) deterministic prefix rewrite (pure string concat); (b) deterministic region-table iteration order (table is a slice of (string, code) pairs in fixed order, not a map); (c) deterministic emoji decode (pure unicode-codepoint arithmetic); (d) `FALLBACK_RULE_TARGET` resolved once at config-load time and threaded through `Pipeline` rather than read per-merge.  
**Scale/Scope**: 2–10 upstreams typical, 100s of proxies aggregate (unchanged from 001). New code budget: ~250 lines net add (namespace pass + region inference + table + tests scaffolding) before tests; ~800 lines including tests.  
**Module path**: `github.com/junlinchen/honkai-rule-server` (resolved from 001).

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

Evaluated against `.specify/memory/constitution.md` v1.0.0 (ratified 2026-04-26).

| Principle / Constraint | Status | Notes |
|---|---|---|
| **I. Unified Transformation Core** | PASS | Every change lives in `internal/merge/` (the pure-functional core) plus two thin config touchpoints (`internal/config/subscriptions.go` for the strengthened `name` validation; `internal/config/server.go` for the new env var). No mode-only logic; the override-mode adapter (deferred) will see the same prefixed/regionalized merged output. |
| **II. Deterministic Transformation** | PASS | Prefix rewrite is pure string concat. Region table is an ordered slice (not a Go map — map iteration order would break determinism). Emoji decode is pure codepoint arithmetic. Region group membership is sorted by source priority (already deterministic in 001). `FALLBACK_RULE_TARGET` is captured at config-load time, not read per-request. Snapshot tests pin the new output (US1+US2+US3 produce visible diffs in `served-config.snap.yaml`; refreshed in the same PR with explicit reviewer attention per the Development Workflow snapshot-stability gate). |
| **III. CSV Rules — Strict Schema, Loud Failure** | **PARTIAL — see Complexity Tracking** | The strengthened `name` validation (FR-001 `^[a-z]+$`) is enforced as **warn + skip the offending row** rather than loud-fail-and-abort, per spec FR-002. This deviates from Principle III's "Rows that fail validation … MUST cause loud failure at load time. Silent skips … are forbidden" rule. The deviation is justified in Complexity Tracking; rationale is that the operator's stated intent is "ignore the row and warn" so the rest of the merge keeps working — a stronger property than loud-fail for an aggregator that already tolerates missing upstreams. |
| **IV. Test-First, Real-Input Integration (NON-NEGOTIABLE)** | PASS | Test Plan section enumerates concrete TC-U / TC-I / TC-S cases that land **before** matching implementation. Order-of-work: test → fail → implement → green. Snapshot drift will fail CI until 001's snapshots are regenerated; the regeneration is itself a deliberate reviewable action. |
| **V. Observable Routing & Source-Merge Decisions** | PASS | New structured log lines: (a) per-name-violation `name-format-violation` (FR-002); (b) per-trailing-rule-drop `trailing-rule-drop:noop` for empty rule lists (FR-009); (c) per-region-table-miss `region-unmapped-indicator` deduplicated within a merge (FR-014); (d) startup `fallback-rule-target` line recording the resolved value (FR-010). All sourced from `slog` per 001's R3. |
| **Routing — corporate isolation** | N/A | This feature does not introduce or modify corporate routing. (Will become relevant when the rules-CSV feature lands.) |
| **Routing — multi-subscription collision resolution** | PASS — strengthened | The constitution's literal phrasing "the resolution strategy MUST be explicit, deterministic, and applied uniformly (e.g., per-source name suffix). Silent overwrite is forbidden" is satisfied: per-source **prefix** is explicit, deterministic, applied uniformly (the `e.g.` is illustrative, not prescriptive). The new strategy makes cross-source collisions structurally impossible, which is *stronger* than the 001 suffix-on-collision strategy. The 001 suffix machinery is kept as defense-in-depth for own-proxies-vs-upstream collisions. |
| **Routing — fetch failure modes** | N/A | This feature does not change cache, fetch, or stale-on-error behavior. |
| **Routing — carve-outs** | N/A | No carve-outs introduced. |
| **Security — secrets boundary** | PASS | Touched files (`internal/merge/namespace.go`, `internal/merge/region.go`, `internal/merge/region_table.go`, `internal/config/subscriptions.go`, `internal/config/server.go`) handle no secrets. Source `name` values logged on violation are intentionally non-secret per the existing 001 sanitization rule. |
| **Security — sanitized output** | PASS | Output adapter unchanged. Prefixed names contain no upstream credentials; region group names are static (`_region_<CC>`). |
| **Security — CSV reviewable, not secret** | N/A for the **subscriptions** CSV (still secret-bearing per 001 FR-016); the rules CSV does not exist yet. |

**Verdict**: All gates pass except Principle III, which has a justified entry in Complexity Tracking below. **No additional Complexity Tracking required.**

## Project Structure

### Documentation (this feature)

```text
specs/002-namespacing-and-regions/
├── spec.md                                       # Feature specification (input)
├── plan.md                                       # This file
├── research.md                                   # Phase 0 output
├── data-model.md                                 # Phase 1 output
├── quickstart.md                                 # Phase 1 output (operator migration guide)
├── contracts/
│   └── served-subscription.changes.md            # Diff vs. 001's served-subscription contract (no shape change, names re-prefixed; new region groups; trailing MATCH)
├── checklists/
│   └── requirements.md                           # Spec quality checklist (already passing)
└── tasks.md                                      # (Phase 2 — produced by /speckit-tasks)
```

### Source Code (repository root)

This feature **modifies** the existing 001 layout — it does not create new top-level directories. New files are nested in already-established packages.

```text
internal/
├── config/
│   ├── subscriptions.go            # MODIFY — add `^[a-z]+$` validation on Name; emit warn-skip outcome distinct from loud-fail (FR-001/FR-002)
│   ├── subscriptions_test.go       # MODIFY — add TC-U-CSV-NAME-* cases
│   └── server.go                   # MODIFY — add ServerConfig.FallbackRuleTarget (new field; default "auto"; bound to FALLBACK_RULE_TARGET env var)
│       server_test.go              # MODIFY — add TC-U-ENV-FALLBACK-* cases
├── merge/
│   ├── namespace.go                # NEW — per-source prefix rewrite for proxies, groups (incl. member-list refs), and rules; built-in target whitelist (FR-004/FR-005/FR-006)
│   ├── namespace_test.go           # NEW — TC-U-NS-* cases
│   ├── region.go                   # NEW — country inference + _region_<CC> group emission (FR-012/FR-013/FR-014/FR-015/FR-016)
│   ├── region_test.go              # NEW — TC-U-REGION-* cases
│   ├── region_table.go             # NEW — ordered slice of {indicator, alpha2} pairs for Chinese/English names; emoji decoder is here too (FR-011)
│   ├── region_table_test.go        # NEW — table coverage tests + emoji decoder tests
│   ├── rules.go                    # MODIFY — drop trailing rule per source before concatenation; append server-emitted MATCH at end (FR-008/FR-010/FR-010a)
│   ├── rules_test.go               # MODIFY — add TC-U-RULES-DROP-* and TC-U-RULES-FALLBACK-* cases
│   ├── proxies.go                  # DOC-ONLY MODIFY — clarify that the `<name>@<source>` collision-suffix path is dead for cross-source collisions (impossible after prefix); kept for own-proxies vs upstream only
│   ├── proxy_groups.go             # DOC-ONLY MODIFY — clarify that cross-source same-name unions are now impossible (each source's groups are prefixed first); same-name unions remain for own-groups vs upstream
│   └── pipeline.go                 # MODIFY — call namespace.RewriteSource per source between cache-walk and merge; thread FallbackRuleTarget into MergeRules; call AppendRegionGroups after AppendProxiesGroup
├── integration/
│   ├── pipeline_test.go            # MODIFY — add TC-I-002-* (prefixed names visible end-to-end; region groups present; trailing MATCH appended; FALLBACK_RULE_TARGET override)
│   ├── snapshot_test.go            # UNCHANGED CODE — but committed snapshot bytes will drift; regenerate with UPDATE_SNAPSHOTS=true
│   └── testdata/
│       ├── fixtures/
│       │   ├── subscriptions.csv   # MODIFY (if needed) — ensure example names already pass `^[a-z]+$` (current `alpha`, `beta` — `beta` contains an underscore; rename to `beta` to satisfy FR-001) ⚠ migration touch-point
│       │   ├── upstream/
│       │   │   ├── alpha.yaml      # UNCHANGED
│       │   │   └── beta.yaml  # UNCHANGED
│       │   ├── own-proxies.yaml    # UNCHANGED CONTENT — but every own-proxy/own-group will be rewritten to `_<original>` at merge time per FR-007a/FR-007b (clarification 2026-04-30)
│       │   └── tokens.json         # UNCHANGED
│       └── snapshots/
│           ├── served-config.snap.yaml          # WILL DRIFT — regenerate
│           ├── subscription-userinfo.snap.txt   # UNCHANGED (traffic aggregation untouched)
│           └── health.snap.json                 # WILL DRIFT IF the source name changed (`beta` → `beta`); otherwise unchanged

example/
└── subscriptions.csv               # MODIFY — rename `beta` to `beta` for the example to pass the new FR-001 rule (operator migration baseline)

CLAUDE.md                           # MODIFY — update SPECKIT plan reference to point at this plan

# All other files unchanged.
```

**Structure Decision**: All transformation logic lives inside `internal/merge/` (Constitution Principle I — pure-functional core). The two config touchpoints (`internal/config/subscriptions.go` for `name` validation, `internal/config/server.go` for `FALLBACK_RULE_TARGET`) are at the system boundary — input parsing and env-var binding — which is where the constitution permits side effects. The new `region_table.go` is intentionally a separate file so future extensions to the country mapping table land as small, reviewable diffs without touching the inference logic. The `internal/integration/testdata/fixtures/subscriptions.csv` rename of `beta` → `beta` is the only operator-visible migration step; documented in `quickstart.md`.

## Phase Outputs

| Phase | Artifact | Status |
|---|---|---|
| 0 (research) | `research.md` | Generated this session |
| 1 (data model) | `data-model.md` | Generated this session |
| 1 (contracts) | `contracts/served-subscription.changes.md` | Generated this session (delta-only — 001's `served-subscription.openapi.yaml` shape is unchanged) |
| 1 (quickstart) | `quickstart.md` | Generated this session |
| 1 (agent context) | `CLAUDE.md` plan reference | Updated this session |
| 2 (tasks) | `tasks.md` | **Pending** — produced by `/speckit-tasks` |

## Re-evaluation: Constitution Check (post-Phase 1)

Phase 1 produced `data-model.md`, `quickstart.md`, and the contract delta doc. No principle status changed during Phase 1: the data model adds one config field (`ServerConfig.FallbackRuleTarget`) and one validation rule (`SubscriptionRow.Name` matches `^[a-z]+$`) — neither introduces side effects in the merge layer; the quickstart documents the operator migration without expanding the feature surface; the contract delta doc explicitly notes that `served-subscription.openapi.yaml` shape is unchanged (the response body is still a Clash YAML config — only the **content** of `name` fields and the **presence** of `_region_*` groups change). Principle III deviation noted in Complexity Tracking still holds; no new deviations surfaced. **All gates remain in their pre-Phase-1 state; Complexity Tracking unchanged.**

## Complexity Tracking

> One justified deviation from Constitution Principle III (CSV Rules — Strict Schema, Loud Failure).

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| `internal/config/subscriptions.go` validates the new FR-001 `^[a-z]+$` rule on `name` as **warn + skip the offending row** rather than loud-fail-abort | Spec FR-002 explicitly states the offending row MUST be excluded and other rows MUST keep loading. Loud-fail-abort would prevent every other row from contributing to the merge, defeating the purpose of an aggregator that already tolerates missing upstreams (001 FR-003 stale-on-error rule). The user's stated intent is "Please ignore [the subscription]" — soft-skip is the literal interpretation. | Loud-fail-abort would shrink operator blast radius for typos, but at the cost of taking the entire service offline on a single character class violation in one row. The aggregator's existing tolerance posture (degraded-but-serving on per-source failure) makes loud-fail here inconsistent: every other input failure mode the server tolerates per-source, only this one would crash the load. Logging the violation as a distinct structured event (`name-format-violation`, separate from the existing `disabled` event) preserves observability (Principle V) so the failure is not silent — it is loud in logs even though it is soft in load-time control flow. |

---

## Test Plan

> Per Constitution Principle IV, every TC below lands **before** the matching implementation. Fixtures continue to use the real provider payloads at `example/3rd-party-sub/{alpha,beta}.yaml` so emoji / Chinese / English display names are exercised against real shapes.

**Tooling**: unchanged from 001 — stdlib `testing` + `cupaloy/v2` snapshots; `UPDATE_SNAPSHOTS=true go test ./internal/integration/...` to refresh.

### TC-U: Unit tests

#### Strengthened name validation (`internal/config/subscriptions.go`)

- **TC-U-CSV-NAME-01**: row with `name=alpha` (lowercase, all letters) loads normally with `Enable=true`.
- **TC-U-CSV-NAME-02**: row with `name=beta` (contains underscore) is **excluded** from the returned slice, the loader emits a `slog.Warn` event with key `name-format-violation` carrying the offending value, and the load **does not abort** other rows.
- **TC-U-CSV-NAME-03**: row with `name=Alpha2024` (uppercase + digit) — same as above.
- **TC-U-CSV-NAME-04**: row with `name=` (empty) — same as above (the `+` quantifier in `^[a-z]+$` rejects empty).
- **TC-U-CSV-NAME-05**: a CSV containing one valid row and one violating row → returned slice has exactly one row (the valid one); one warning event was emitted.
- **TC-U-CSV-NAME-06**: violation does NOT raise `*ConfigValidationError` (existing 001 type) — soft skip is a *new* code path; tests assert `error` is nil.
- **TC-U-CSV-NAME-07**: duplicate `name` values across rows still raise loud failure (existing 001 behavior preserved); the duplicate-detection runs on the post-name-validation set, so a violation followed by a valid row with the same lowercase form is NOT a duplicate.

#### `FALLBACK_RULE_TARGET` env-var binding (`internal/config/server.go`)

- **TC-U-ENV-FALLBACK-01**: env unset → `ServerConfig.FallbackRuleTarget` defaults to `"auto"`.
- **TC-U-ENV-FALLBACK-02**: env empty string `""` → defaults to `"auto"` (matches existing pattern of "empty means default").
- **TC-U-ENV-FALLBACK-03**: env `FALLBACK_RULE_TARGET=DIRECT` → `ServerConfig.FallbackRuleTarget == "DIRECT"`.
- **TC-U-ENV-FALLBACK-04**: env `FALLBACK_RULE_TARGET=alpha_Auto` → value passed through verbatim (no validation; the spec deliberately punts validation to the operator — see Assumption A7a).
- **TC-U-ENV-FALLBACK-05**: startup log line records the resolved value (FR-010 observability hook).

#### Namespace rewriter (`internal/merge/namespace.go`)

- **TC-U-NS-PROXY-01**: a proxy with `name: Node1` → rewritten to `alpha_Node1`.
- **TC-U-NS-PROXY-02**: a proxy with `name: 香港 01` (non-ASCII original) → rewritten to `alpha_香港 01` (the original may contain anything; only the prefix is constrained).
- **TC-U-NS-GROUP-01**: a `select` group `{name: Auto, proxies: [Node1, Node2]}` → rewritten to `{name: alpha_Auto, proxies: [alpha_Node1, alpha_Node2]}`.
- **TC-U-NS-GROUP-02**: a `select` group `{name: Auto, proxies: [Node1, DIRECT, REJECT]}` → rewritten to `{name: alpha_Auto, proxies: [alpha_Node1, DIRECT, REJECT]}` (built-ins untouched).
- **TC-U-NS-GROUP-03**: a `relay` group `{name: Chain, proxies: [NodeA, NodeB]}` → rewritten to `{name: alpha_Chain, proxies: [alpha_NodeA, alpha_NodeB]}` (relay member list also rewritten).
- **TC-U-NS-RULE-01**: rule `DOMAIN,a.test,Auto` → rewritten to `DOMAIN,a.test,alpha_Auto`.
- **TC-U-NS-RULE-02**: rule `DOMAIN,a.test,DIRECT` → unchanged (built-in target).
- **TC-U-NS-RULE-03**: rule `MATCH,REJECT-DROP` → unchanged.
- **TC-U-NS-RULE-04**: rule with optional modifier `DOMAIN-SUFFIX,foo,Auto,no-resolve` → target rewritten, `no-resolve` modifier untouched.
- **TC-U-NS-RULE-05**: rule with comma in matcher value (rare but legal in Mihomo, e.g., `IP-CIDR,10.0.0.0/8,Proxy`) — splitting on the **last** comma identifies the target correctly.
- **TC-U-NS-IDEMPOTENT-01**: applying the rewriter twice in succession is **NOT** idempotent — second pass produces `alpha_alpha_Node1`. The pipeline must call it exactly once per source. Test asserts the wrong-twice result so the call site is auditable.
- **TC-U-NS-OWN-PROXY-01**: an own-proxy `{name: my-server, ...}` → rewritten to `{name: _my-server, ...}` (single leading underscore per FR-007a).
- **TC-U-NS-OWN-PROXY-02**: an own-proxy whose name already starts with `_` (e.g., `_legacy`) → rewritten to `__legacy` (literal prefix application; not coerced to single-underscore).
- **TC-U-NS-OWN-GROUP-01**: an own-group `{name: my-pool, type: select, proxies: [my-server, DIRECT]}` → rewritten to `{name: _my-pool, type: select, proxies: [_my-server, DIRECT]}` (group renamed, own-proxy reference rewritten, built-in untouched per FR-007b).
- **TC-U-NS-OWN-GROUP-02**: an own-group's `proxies:` list referencing another own-group by name (a `select` group whose member list contains `my-pool` referring to another own-group named `my-pool`) → rewritten to `_my-pool` (own-group-to-own-group references are also rewritten).

#### Trailing-rule drop + final fallback (`internal/merge/rules.go`)

- **TC-U-RULES-DROP-01**: a source's rules `[DOMAIN,a.test,Auto, MATCH,auto]` after trailing-drop → `[DOMAIN,a.test,Auto]`.
- **TC-U-RULES-DROP-02**: a source's rules `[]` (empty) after trailing-drop → `[]` (no-op); test captures the emitted `trailing-rule-drop:noop` log event.
- **TC-U-RULES-DROP-03**: a source's rules with one entry `[MATCH,DIRECT]` → `[]` (the single entry IS the trailing rule).
- **TC-U-RULES-DROP-04**: trailing entry that isn't `MATCH,*` (e.g., `DOMAIN,foo,Auto`) is still dropped (FR-008 unconditional).
- **TC-U-RULES-FALLBACK-01**: `MergeRules` with `FallbackRuleTarget="auto"` and two sources whose post-drop rules are `[A]` and `[B]` → output `[A, B, MATCH,auto]`.
- **TC-U-RULES-FALLBACK-02**: `FallbackRuleTarget="DIRECT"` → output ends with `MATCH,DIRECT`.
- **TC-U-RULES-FALLBACK-03**: every source contributes zero rules (or all `Disable`d) → output is exactly `[MATCH,<target>]` (single-element list, FR-010a unconditional).
- **TC-U-RULES-FALLBACK-04**: server-emitted fallback is **never** prefixed (the rewriter sees per-source rules; the fallback is appended in `MergeRules` after rewriting).

#### Region inference + group emission (`internal/merge/region.go`)

- **TC-U-REGION-EMOJI-01**: name containing `🇨🇳` (U+1F1E8 U+1F1F3) → inferred `CN`.
- **TC-U-REGION-EMOJI-02**: name containing `🇺🇸` → inferred `US`.
- **TC-U-REGION-EMOJI-03**: name containing `🇭🇰` → inferred `HK`.
- **TC-U-REGION-EMOJI-04**: name containing both `🇨🇳` and Chinese `美国` → emoji wins (precedence).
- **TC-U-REGION-CN-01**: name containing `中国` → `CN`.
- **TC-U-REGION-CN-02**: name containing `美国` → `US`.
- **TC-U-REGION-CN-03**: name containing `香港` → `HK`.
- **TC-U-REGION-CN-04**: name containing `台湾` / `臺灣` (simplified + traditional) — both → `TW`.
- **TC-U-REGION-EN-01**: name containing `Hong Kong` (case-insensitive) → `HK`.
- **TC-U-REGION-EN-02**: name containing `United States` → `US`.
- **TC-U-REGION-MISS-01**: name `Backup Premium` (no indicator) → `(none, false)`; one `region-unmapped-indicator` log event captured (deduplicated within a single merge — see TC-U-REGION-MISS-02).
- **TC-U-REGION-MISS-02**: 10 nodes named `Backup Premium`, `Backup Pro`, `Backup Lite` — three distinct unmapped names → exactly three log events (per FR-014 dedup-by-fragment).
- **TC-U-REGION-EMOJI-DECODE-01**: `decodeRegionalIndicatorPair("🇨🇳")` returns `("CN", true)` via codepoint arithmetic — no table lookup.
- **TC-U-REGION-EMOJI-DECODE-02**: `decodeRegionalIndicatorPair("🇿🇿")` (the `ZZ` user-assigned code) — returns `("ZZ", true)`. The decoder doesn't validate against the ISO list; it only validates the Unicode-block range. (A future check could reject reserved codes, but is out of scope.)
- **TC-U-REGION-GROUP-01**: 3 namespaced proxies `[alpha_HK01, alpha_HK02, beta_HK03]` all inferring `HK` → emits `_region_HK` group of type `select` with members `[alpha_HK01, alpha_HK02, beta_HK03]` in source-priority order.
- **TC-U-REGION-GROUP-02**: 0 proxies inferring `HK` → no `_region_HK` group emitted (FR-013 empty-group suppression).
- **TC-U-REGION-OWN-EXCLUDED-01**: an own-proxy whose display name is `🇨🇦 my-server` → does NOT influence region inference (FR-012 own-proxy exclusion); when no upstream contributes a CA-classified proxy, no `_region_CA` group is emitted; the own-proxy still appears in the merged output as `_🇨🇦 my-server`.
- **TC-U-REGION-DETERMINISM-01**: same input, two consecutive `AppendRegionGroups` calls → byte-identical output (Constitution Principle II); enforced by sorting region codes alpha-ascending before emission.
- **TC-U-REGION-PROXIES-01**: emitted `_region_*` groups are appended as additional members of the always-present `Proxies` group (FR-015) — verified by parsing the output and asserting `Proxies.proxies` contains every emitted `_region_<CC>` name in deterministic order.

### TC-I: Integration tests (`internal/integration/pipeline_test.go`)

Fixtures unchanged from 001 (two committed upstream YAMLs + own-proxies). The fixture CSV is migrated: `beta` → `beta`.

- **TC-I-002-01 — End-to-end prefixed names**: GET `/?token=<valid>` → 200; parse the body; assert every proxy name starts with either `alpha_`, `beta_`, or `_` (own-proxy underscore prefix per FR-007a); every proxy-group name starts with `alpha_`, `beta_`, `_region_`, or `_` (own-group underscore prefix per FR-007b), or equals the literal `Proxies` (the always-present group from FR-009a, never prefixed); every rule's target is either `<provider>_<name>`, `_<own-name>`, `_region_<CC>`, `Proxies`, or one of `DIRECT`/`REJECT`/`REJECT-DROP`/`PASS`.
- **TC-I-002-02 — Group member references rewritten**: parse a known upstream group from the fixture (e.g., alpha's `🔰国外流量`-named group), assert it now appears as `alpha_🔰国外流量` and that every entry in its `proxies:` list is a prefixed proxy name (or a built-in).
- **TC-I-002-03 — Trailing rule dropped + fallback emitted**: assert the merged `rules:` block does not contain any of the upstreams' original trailing rules (extracted from the fixture YAML pre-merge for cross-check); assert the very last rule is exactly `MATCH,auto`.
- **TC-I-002-04 — `FALLBACK_RULE_TARGET=DIRECT` override**: re-run the pipeline with `FALLBACK_RULE_TARGET=DIRECT` in the test env; assert the last rule is `MATCH,DIRECT`; assert every other rule is byte-identical to TC-I-002-03 (the override changes exactly one byte block).
- **TC-I-002-05 — Region groups present**: assert the merged config contains at least the `_region_CN` and `_region_HK` groups (the operator's two upstreams both contain HK and CN nodes by display name); assert every member of each region group is an upstream-prefixed proxy name (`<provider>_<original>`) — never an own-proxy (`_<original>`); assert the region groups appear as members of the `Proxies` group.
- **TC-I-002-06 — Name violation warn-skip path**: write a fixture CSV with a third row `name=Bad_Source,...,Enable`; load via `internal/config/LoadSubscriptions`; assert returned slice has 2 rows (not 3), assert the captured logger has one `name-format-violation` event naming `Bad_Source`, assert the resulting merged config contains zero proxies/groups/rules from `Bad_Source`. The pipeline runs to completion (no error).
- **TC-I-002-07 — Determinism re-affirmed**: 100 sequential `/?token=<valid>` requests → byte-identical body (sha256 over each); covers prefix + region + fallback together.
- **TC-I-002-08 — Cross-source collision impossible**: synthesize two upstream stubs each contributing a proxy literally named `Node1` (same as 001's TC-I-06) — assert merged config contains both `alpha_Node1` and `beta_Node1`, AND that the 001 collision-suffix machinery (`<name>@<source>`) was NOT invoked (no `ProxyCollision` entries in the returned `MergedConfig.Collisions` for cross-source pairs).
- **TC-I-002-09 — Own-proxy underscore-prefixed**: own-proxies fixture's proxies appear in the merged config with `_<original>` names (FR-007a). Own-groups are renamed to `_<original>` and any member-list reference to an own-proxy is rewritten to `_<original>` (FR-007b). Built-in identifiers (DIRECT/REJECT/etc.) inside own-group member lists are left unchanged. Documented in data-model.md.
- **TC-I-002-10 — Own-proxy excluded from region groups**: an own-proxy fixture entry whose display name contains `🇨🇦` (country emoji) → the merged config contains a proxy `_🇨🇦 <original-rest>` AND does NOT contain `_region_CA` UNLESS an upstream also contributes a CA-classified proxy (FR-012 own-proxy exclusion).

### TC-S: Snapshot tests (`internal/integration/snapshot_test.go`)

- **TC-S-002-01** — `served-config.snap.yaml`: existing committed snapshot will drift heavily (every upstream-sourced proxy/group name changes; rules block grows by N region members + 1 trailing MATCH; new region groups appear). Regenerate with `UPDATE_SNAPSHOTS=true`; reviewer attention specifically on (a) every new prefixed name reads sensibly, (b) the final rule is `MATCH,auto`, (c) every emitted `_region_<CC>` group has plausible membership.
- **TC-S-002-02** — `subscription-userinfo.snap.txt`: **unchanged** (this feature does not touch traffic aggregation).
- **TC-S-002-03** — `health.snap.json`: **likely unchanged**, but if the per-source field uses the old `beta` name, the rename to `beta` will produce a one-key drift. Regenerate; verify the only drift is the source-name rename.

### Acceptance criteria for "tests pass" gate

`/speckit-implement` is **NOT** considered done unless:

- Every TC-U / TC-I / TC-S above has a committed test in the listed `*_test.go` file (Go function names matching the TC ids for grep-ability).
- `go test ./...` exits 0.
- `make check` exits 0 — covers `go vet`, `staticcheck`, full test run, AND the snapshot-drift `git diff --exit-code` check (per 001's Development Workflow gate).
- The example fixture rename (`beta` → `beta`) is reflected in `example/subscriptions.csv` AND `internal/integration/testdata/fixtures/subscriptions.csv` AND in any committed quickstart text that names sources.
- `CLAUDE.md`'s SPECKIT block points at this plan.
