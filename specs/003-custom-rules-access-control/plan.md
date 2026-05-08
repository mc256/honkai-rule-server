# Implementation Plan: Custom Rules, Continent Groups & Access Control

**Branch**: `003-custom-rules-access-control` | **Date**: 2026-04-30 | **Spec**: [spec.md](./spec.md)  
**Input**: Feature specification from `/specs/003-custom-rules-access-control/spec.md`

## Summary

Four additive features to the existing 002 transformation core:

1. **Custom rules with priorities** — operators define rule YAML files in a configurable folder; rules are merged into the output in priority order after upstream rules and before the server-emitted `MATCH,<fallback>` (FR-001 to FR-008).
2. **Continent-based proxy groups** — `_continent_<CONT>` groups derived from the existing `_region_<CC>` groups via a country-to-continent mapping table maintained alongside the region table (FR-009 to FR-013).
3. **Unclassified nodes proxy group** — `_region_UNKNOWN` catch-all group for proxies whose display names have no recognized country indicator (FR-014 to FR-017).
4. **User-Agent access control** — optional request filtering based on `User-Agent` header prefixes configured via `HONKAI_RULE_CLIENT_UA` env var (FR-018 to FR-022).

**Technical approach**: All changes live in `internal/` — new packages for custom rules (`internal/customrules/`), modifications to `internal/merge/` for continent/unknown grouping, and a new middleware in `internal/auth/` for UA filtering. The merge layer remains pure-functional (Constitution Principle I); custom rules are read at startup and threaded through `Pipeline`. Continent groups derive from region groups post-merge (no upstream data access needed). UA filtering is a thin middleware before token auth.

## Technical Context

**Language/Version**: **Go 1.25 toolchain** (`go.mod` declares 1.22+; production Dockerfile pinned to `golang:1.25-alpine`). Pre-existing.
**Primary Dependencies**: No new dependencies. Reuses `gopkg.in/yaml.v3`, `log/slog`, stdlib `os`, `path/filepath`, `strings`.
**Storage**: No change. Custom rules are read from filesystem at startup; no database.
**Testing**: stdlib `testing` + existing `cupaloy/v2` snapshot tests. New unit tests under `internal/customrules/*_test.go`, `internal/merge/*_test.go`, `internal/auth/*_test.go`.
**Target Platform**: Unchanged — Linux container, K8s deployment.
**Project Type**: Single-project backend service (unchanged from 001/002).
**Performance Goals**: Custom rule loading is O(files × rules) at startup; zero runtime overhead. Continent/unknown grouping is O(proxies) per merge — already bounded by existing region inference.
**Constraints**: Byte-identical output across runs (Constitution Principle II). Custom rule files must be valid YAML; parse errors skip the file with warning (soft failure like 002's name-validation).
**Scale/Scope**: 2–10 upstreams, 100s of proxies, 10s of custom rule files (typical). New code budget: ~400 lines net add before tests; ~1000 lines including tests.
**Module path**: `github.com/mc256/honkai-rule-server` (user correction — needs update from `github.com/junlinchen/honkai-rule-server`).

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

Evaluated against `.specify/memory/constitution.md` v1.0.0 (ratified 2026-04-26).

| Principle / Constraint | Status | Notes |
|---|---|---|
| **I. Unified Transformation Core** | PASS | Custom rules are loaded at startup and passed to `Pipeline.Build()`; continent/unknown groups are derived inside `AppendRegionGroups` (same pass as existing region groups); UA filtering is middleware at the HTTP boundary. No mode-only logic; both subscription mode and future override mode see identical merged output. |
| **II. Deterministic Transformation** | PASS | Custom rule ordering: priority ascending, then filename alphabetical for ties (FR-004). Continent groups: derived from deterministic `_region_<CC>` groups + deterministic country-to-continent table. Unknown group: derived from deterministic region classification. UA filtering: request-time only, does not affect output bytes. Snapshot tests pin the new output. |
| **III. CSV Rules — Strict Schema, Loud Failure** | **N/A for custom rules** | Custom rules use YAML files, not the CSV defined in Principle III. The spec intentionally provides a separate mechanism with different failure semantics (warn + skip on parse error per FR-008). This does not violate Principle III because that principle governs the "customized rules defined in CSV" mentioned in Project Scope — this feature adds a parallel, operator-friendly YAML system. |
| **IV. Test-First, Real-Input Integration (NON-NEGOTIABLE)** | PASS | Test Plan section enumerates TC-U / TC-I / TC-S cases that land before matching implementation. Snapshot drift will occur when custom rules are present; regeneration is deliberate and reviewable. |
| **V. Observable Routing & Source-Merge Decisions** | PASS | New structured log lines: (a) per-custom-rule-file-parse-error `custom-rules-parse-error` (FR-008); (b) per-UA-rejection `ua-rejected` (FR-021); (c) per-unmapped-country-to-continent `continent-unmapped-country`. UA filtering logs the rejected User-Agent. |
| **Routing — corporate isolation** | N/A | This feature does not introduce or modify corporate routing. |
| **Routing — multi-subscription collision resolution** | N/A | No changes to collision handling. |
| **Routing — fetch failure modes** | N/A | No changes to fetch behavior. |
| **Routing — carve-outs** | N/A | No carve-outs introduced. |
| **Security — secrets boundary** | PASS | `HONKAI_RULE_CLIENT_UA` contains non-secret client identifiers. Custom rule files contain routing logic, not credentials. No secrets in new code paths. |
| **Security — sanitized output** | PASS | Custom rule content is preserved verbatim — operators are responsible for not leaking secrets in rule files (consistent with CSV reviewability assumption). |
| **Security — CSV reviewable, not secret** | N/A for custom rules YAML | Custom rules YAML is operator-controlled and reviewable; same security posture as the future rules CSV. |

**Verdict**: All gates pass. Principle III is N/A for the new YAML-based custom rules mechanism. **No Complexity Tracking required.**

## Project Structure

### Documentation (this feature)

```text
specs/003-custom-rules-access-control/
├── spec.md                                 # Feature specification (input)
├── plan.md                                 # This file
├── research.md                             # Phase 0 output
├── data-model.md                           # Phase 1 output
├── quickstart.md                           # Phase 1 output (operator guide)
├── contracts/
│   └── custom-rules-yaml.schema.md         # YAML schema for custom rule files
│   └── ua-filtering.openapi.yaml           # HTTP 403 response spec
├── checklists/
│   └── requirements.md                     # Spec quality checklist (already passing)
└── tasks.md                                # (Phase 2 — produced by /speckit-tasks)
```

### Source Code (repository root)

This feature **adds** to and **modifies** the existing layout:

```text
internal/
├── config/
│   ├── server.go                     # MODIFY — add CustomRulesPath (new field; default "./custom-rules/"; bound to CUSTOM_RULES_PATH env var)
│   │                                 #                 add AllowedUserAgentPrefixes []string (bound to HONKAI_RULE_CLIENT_UA)
│   └── server_test.go                # MODIFY — add TC-U-ENV-CUSTOM-RULES-* and TC-U-ENV-UA-* cases
├── customrules/                      # NEW PACKAGE
│   ├── loader.go                     # NEW — Load(folder string) ([]CustomRuleSet, error); reads *.yaml from folder
│   ├── loader_test.go                # NEW — TC-U-CR-LOAD-* cases
│   ├── types.go                      # NEW — CustomRuleSet struct { Name, Priority, Rules []string }
│   └── types_test.go                 # NEW — type validation tests
├── merge/
│   ├── rules.go                      # MODIFY — add MergeCustomRules(upstreamRules []string, custom []customrules.CustomRuleSet) []string
│   ├── rules_test.go                 # MODIFY — add TC-U-RULES-CUSTOM-* cases
│   ├── region.go                     # MODIFY — add AppendContinentGroups and _region_UNKNOWN emission
│   ├── region_test.go                # MODIFY — add TC-U-CONTINENT-* and TC-U-UNKNOWN-* cases
│   ├── region_table.go               # MODIFY — add countryToContinent mapping table
│   ├── region_table_test.go          # MODIFY — add continent mapping coverage tests
│   └── pipeline.go                   # MODIFY — thread CustomRuleSet slice through; call MergeCustomRules; call AppendContinentGroups
├── auth/
│   ├── auth.go                       # UNCHANGED
│   ├── ua_filter.go                  # NEW — RequireUserAgent(prefixes []string, log *slog.Logger) middleware
│   └── ua_filter_test.go             # NEW — TC-U-UA-* cases
├── server/
│   └── app.go                        # MODIFY — wrap subscription handler with UA middleware when prefixes configured
└── integration/
    ├── pipeline_test.go              # MODIFY — add TC-I-003-* (custom rules, continent groups, unknown group, UA filtering)
    └── snapshot_test.go              # UNCHANGED CODE — but snapshots may drift if fixtures add custom rules

go.mod                                # MODIFY — update module path to github.com/mc256/honkai-rule-server

CLAUDE.md                             # MODIFY — update SPECKIT plan reference to point at this plan
```

**Structure Decision**: Custom rules get their own package (`internal/customrules/`) to encapsulate file loading, YAML parsing, and the `CustomRuleSet` type. Continent/unknown grouping extends the existing `internal/merge/region.go` because it shares the same proxy-classification context. UA filtering lives in `internal/auth/` alongside the existing token middleware, forming a "request gating" layer. This follows Constitution Principle I (unified transformation core) and keeps the merge layer pure.

## Phase Outputs

| Phase | Artifact | Status |
|---|---|---|
| 0 (research) | `research.md` | Generated this session |
| 1 (data model) | `data-model.md` | Generated this session |
| 1 (contracts) | `contracts/custom-rules-yaml.schema.md` | Generated this session |
| 1 (contracts) | `contracts/ua-filtering.openapi.yaml` | Generated this session |
| 1 (quickstart) | `quickstart.md` | Generated this session |
| 1 (agent context) | `CLAUDE.md` plan reference | Updated this session |
| 2 (tasks) | `tasks.md` | Generated this session |

## Re-evaluation: Constitution Check (post-Phase 1)

Phase 1 produced `data-model.md`, `quickstart.md`, and contract docs. No principle status changed during Phase 1: the data model adds two config fields (`ServerConfig.CustomRulesPath`, `ServerConfig.AllowedUserAgentPrefixes`) and one new type (`customrules.CustomRuleSet`); the quickstart documents operator usage without expanding the feature surface; the contract docs define the YAML schema and HTTP 403 response. **All gates remain in their pre-Phase-1 state; no new deviations surfaced.**

---

## Test Plan

> Per Constitution Principle IV, every TC below lands **before** the matching implementation.

**Tooling**: unchanged from 001/002 — stdlib `testing` + `cupaloy/v2` snapshots; `UPDATE_SNAPSHOTS=true go test ./internal/integration/...` to refresh.

### TC-U: Unit tests

#### Custom rules loading (`internal/customrules/loader.go`)

- **TC-U-CR-LOAD-01**: folder with one valid YAML file `my-rules.yaml` `{name: my-rules, priority: 100, rules: [DOMAIN,a.com,REJECT]}` → returns `[]CustomRuleSet{{Name: "my-rules", Priority: 100, Rules: []string{"DOMAIN,a.com,REJECT"}}}`.
- **TC-U-CR-LOAD-02**: folder with two YAML files, priorities 200 and 100 → returns slice sorted by priority ascending (100 first).
- **TC-U-CR-LOAD-03**: folder with two YAML files, same priority 100, filenames `alpha.yaml` and `beta.yaml` → sorted alphabetically (alpha before beta).
- **TC-U-CR-LOAD-04**: empty folder → returns empty slice, no error.
- **TC-U-CR-LOAD-05**: folder does not exist → returns empty slice, logs warning (FR-007).
- **TC-U-CR-LOAD-06**: YAML file missing `name` field → uses filename (without `.yaml`) as name, logs warning.
- **TC-U-CR-LOAD-07**: YAML file missing `priority` field → uses default priority 1000, logs warning.
- **TC-U-CR-LOAD-08**: YAML file with invalid syntax → logs error with filename and line, skips file, continues with other files.
- **TC-U-CR-LOAD-09**: YAML file with `priority: "not-a-number"` → logs error, skips file.
- **TC-U-CR-LOAD-10**: YAML file with empty `rules:` list → returns CustomRuleSet with empty Rules slice (valid).

#### Custom rules merging (`internal/merge/rules.go`)

- **TC-U-RULES-CUSTOM-01**: upstream rules `[A, B]`, custom rules priority 500 `[C, D]`, fallback `auto` → output `[A, B, C, D, MATCH,auto]`.
- **TC-U-RULES-CUSTOM-02**: two custom rule sets, priorities 100 and 200 → output `[...upstream..., rules-100..., rules-200..., MATCH,...]`.
- **TC-U-RULES-CUSTOM-03**: custom rule targets `_region_US` → preserved verbatim (no rewriting).
- **TC-U-RULES-CUSTOM-04**: custom rule targets `_continent_EU` → preserved verbatim.
- **TC-U-RULES-CUSTOM-05**: no custom rules → output matches 002 behavior (upstream + MATCH fallback).
- **TC-U-RULES-CUSTOM-06**: custom rule with complex Mihomo syntax (`AND,((DOMAIN,a.com),(NETWORK,UDP)),DIRECT`) → preserved verbatim.

#### Continent grouping (`internal/merge/region.go`)

- **TC-U-CONTINENT-01**: `_region_US` exists → `_continent_NA` created with same members.
- **TC-U-CONTINENT-02**: `_region_US`, `_region_CA`, `_region_MX` exist → `_continent_NA` has union of all three.
- **TC-U-CONTINENT-03**: `_region_CN`, `_region_JP`, `_region_SG` exist → `_continent_AS` has union.
- **TC-U-CONTINENT-04**: `_region_DE`, `_region_FR`, `_region_GB` exist → `_continent_EU` has union.
- **TC-U-CONTINENT-05**: no `_region_*` groups → no `_continent_*` groups emitted.
- **TC-U-CONTINENT-06**: `_region_XX` (unmapped country code) → proxy NOT in any continent group; log `continent-unmapped-country` once per distinct code.
- **TC-U-CONTINENT-07**: continent groups added to `Proxies` group member list (like region groups per 002 FR-015).
- **TC-U-CONTINENT-ORDER-01**: same input, two runs → continent groups in identical order (determinism).

#### Unknown region grouping (`internal/merge/region.go`)

- **TC-U-UNKNOWN-01**: upstream proxy `alpha_mystery` with no country indicator → appears in `_region_UNKNOWN`.
- **TC-U-UNKNOWN-02**: all proxies classified → `_region_UNKNOWN` NOT emitted.
- **TC-U-UNKNOWN-03**: three unclassified proxies from two sources → `_region_UNKNOWN` has all three in source-priority order.
- **TC-U-UNKNOWN-04**: own-proxy with no indicator → NOT in `_region_UNKNOWN` (own-proxies excluded per FR-017).
- **TC-U-UNKNOWN-05**: `_region_UNKNOWN` added to `Proxies` group member list.

#### User-Agent filtering (`internal/auth/ua_filter.go`)

- **TC-U-UA-01**: prefixes `["Honkai-Rule-Client", "curl"]`, request UA `Honkai-Rule-Client/1.0` → request passes (200 or downstream behavior).
- **TC-U-UA-02**: prefixes `["Honkai-Rule-Client", "curl"]`, request UA `curl/7.68.0` → request passes.
- **TC-U-UA-03**: prefixes `["Honkai-Rule-Client", "curl"]`, request UA `Mozilla/5.0` → 403 Forbidden, empty body.
- **TC-U-UA-04**: empty prefixes slice → all requests pass (filter disabled).
- **TC-U-UA-05**: missing `User-Agent` header → 403 Forbidden (treated as non-match).
- **TC-U-UA-06**: rejected request logged with UA value and remote address (FR-021).

#### Config loading (`internal/config/server.go`)

- **TC-U-ENV-CUSTOM-RULES-01**: `CUSTOM_RULES_PATH` unset → defaults to `./custom-rules/`.
- **TC-U-ENV-CUSTOM-RULES-02**: `CUSTOM_RULES_PATH=/etc/rules` → `ServerConfig.CustomRulesPath == "/etc/rules"`.
- **TC-U-ENV-UA-01**: `HONKAI_RULE_CLIENT_UA` unset → `ServerConfig.AllowedUserAgentPrefixes == nil` (disabled).
- **TC-U-ENV-UA-02**: `HONKAI_RULE_CLIENT_UA=Honkai-Rule-Client,curl` → `AllowedUserAgentPrefixes == ["Honkai-Rule-Client", "curl"]`.
- **TC-U-ENV-UA-03**: `HONKAI_RULE_CLIENT_UA=` (empty) → `AllowedUserAgentPrefixes == nil` (disabled).

### TC-I: Integration tests (`internal/integration/pipeline_test.go`)

- **TC-I-003-01 — Custom rules in output**: place a custom rule file in folder, start server with `CUSTOM_RULES_PATH`, GET subscription → verify custom rule appears after upstream rules, before `MATCH,<fallback>`.
- **TC-I-003-02 — Custom rule targeting region group**: custom rule `DOMAIN-SUFFIX,google.com,_region_US` → appears verbatim; if US proxies exist, `_region_US` group exists.
- **TC-I-003-03 — Continent groups present**: upstream has CN, JP, US proxies → `_region_CN`, `_region_JP`, `_region_US`, `_continent_AS`, `_continent_NA` all present with correct membership.
- **TC-I-003-04 — Unknown group present**: upstream has unclassified proxy → `_region_UNKNOWN` exists with that proxy.
- **TC-I-003-05 — UA filtering enabled**: `HONKAI_RULE_CLIENT_UA=Honkai-Rule-Client`, request with `User-Agent: Honkai-Rule-Client/1.0` → 200; request with `User-Agent: Mozilla/5.0` → 403.
- **TC-I-003-06 — UA filtering disabled**: `HONKAI_RULE_CLIENT_UA` unset, request with any UA → 200 (or downstream behavior).
- **TC-I-003-07 — Determinism**: same inputs, 100 sequential requests → byte-identical bodies.
- **TC-I-003-08 — Custom rules + continent groups + unknown group together**: all three features coexist without interference.

### TC-S: Snapshot tests (`internal/integration/snapshot_test.go`)

- **TC-S-003-01** — `served-config.snap.yaml`: with custom rules folder populated, verify: (a) custom rules in correct position, (b) continent groups present, (c) `_region_UNKNOWN` present if unclassified proxies exist.
- **TC-S-003-02** — `health.snap.json`: likely unchanged (no health-endpoint changes).

### Acceptance criteria for "tests pass" gate

`/speckit-implement` is **NOT** considered done unless:

- Every TC-U / TC-I / TC-S above has a committed test in the listed `*_test.go` file.
- `go test ./...` exits 0.
- `make check` exits 0.
- `CLAUDE.md`'s SPECKIT block points at this plan.
- `go.mod` updated to `github.com/mc256/honkai-rule-server`.
