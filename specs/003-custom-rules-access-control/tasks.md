# Tasks: Custom Rules, Continent Groups & Access Control

**Input**: Design documents from `/specs/003-custom-rules-access-control/`
**Prerequisites**: plan.md (required), spec.md (required), data-model.md, contracts/

**Tests**: Per Constitution Principle IV (NON-NEGOTIABLE), every unit test MUST be committed before the implementation it validates. Test tasks are explicitly included and must land first within each user story phase.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (US1, US2, US3, US4)
- Include exact file paths in descriptions

## Path Conventions

- `internal/customrules/` — new package for custom rule types and file loading
- `internal/config/server.go` — env var bindings for new config fields
- `internal/merge/` — transformation core (rules.go, region.go, region_table.go, pipeline.go)
- `internal/auth/ua_filter.go` — new UA filtering middleware
- `internal/server/app.go` — HTTP route wiring
- `internal/integration/` — end-to-end integration tests and snapshots

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Module path correction and new package scaffolding that all stories depend on

- [x] T001 Update module path in `go.mod` from `github.com/junlinchen/honkai-rule-server` to `github.com/mc256/honkai-rule-server`; update all import paths in every `*.go` file under `cmd/`, `internal/`, and `example/` to match the new module path
- [x] T002 [P] Create `internal/customrules/types.go` with the `CustomRuleSet` struct: `Name string`, `Priority int`, `Rules []string`; include the `customRuleSetFile` unexported struct for YAML deserialization (with `Name *string`, `Priority *int`, `Rules []string` to detect missing fields vs zero values); no methods needed beyond the type definition
- [x] T003 [P] Add `CustomRulesPath string` (default `"./custom-rules/"`, env var `CUSTOM_RULES_PATH`) and `AllowedUserAgentPrefixes []string` (env var `HONKAI_RULE_CLIENT_UA`, nil when unset/empty) fields to `ServerConfig` in `internal/config/server.go`; add parsing logic in `Load()` following the existing `UPSTREAM_USER_AGENT` pattern — for `CUSTOM_RULES_PATH` read and default like other path vars; for `HONKAI_RULE_CLIENT_UA` parse comma-separated string into `[]string` with whitespace trimming, set to nil when empty/unset

**Checkpoint**: Module path updated, new types and config fields ready for test-first development

---

## Phase 2: Foundational — Config Binding Tests

**Purpose**: Verify the new env var bindings work correctly before any feature code depends on them

- [x] T004 [P] Add TC-U-ENV-CUSTOM-RULES-01 and TC-U-ENV-CUSTOM-RULES-02 tests in `internal/config/server_test.go`: verify `CustomRulesPath` defaults to `"./custom-rules/"` when env unset, and reads `CUSTOM_RULES_PATH=/etc/rules` correctly
- [x] T005 [P] Add TC-U-ENV-UA-01, TC-U-ENV-UA-02, and TC-U-ENV-UA-03 tests in `internal/config/server_test.go`: verify `AllowedUserAgentPrefixes` is nil when env unset, is nil when env is empty string, and parses `HONKAI_RULE_CLIENT_UA=Honkai-Rule-Client,curl` into `["Honkai-Rule-Client", "curl"]` with whitespace trimming

**Checkpoint**: Config bindings tested — foundational phase complete, user stories can begin

---

## Phase 3: User Story 1 — Custom Rules with Priority Ordering (Priority: P1) MVP

**Goal**: Operators can define custom routing rules in YAML files that are inserted into the merged output in priority order after upstream rules and before the MATCH fallback

**Independent Test**: Place a YAML file with `priority: 500` and `rules: [DOMAIN,example.com,REJECT]` in the custom rules folder; fetch merged subscription; verify the rule appears after upstream rules and before `MATCH,auto`

### Tests for User Story 1 (write FIRST, verify they FAIL)

- [x] T006 [P] [US1] Write TC-U-CR-LOAD-01 through TC-U-CR-LOAD-10 tests in `internal/customrules/loader_test.go`: test single valid file, priority sorting, same-priority alphabetical tiebreak, empty folder, nonexistent folder (warning log), missing name (fallback to filename), missing priority (default 1000), invalid YAML syntax, non-integer priority, empty rules list; use `t.TempDir()` for filesystem fixtures
- [x] T007 [P] [US1] Write TC-U-RULES-CUSTOM-01 through TC-U-RULES-CUSTOM-06 tests in `internal/merge/rules_test.go`: test custom rules inserted between upstream and MATCH fallback, two custom rule sets ordered by priority, custom rule targeting `_region_US` preserved verbatim, custom rule targeting `_continent_EU` preserved verbatim, no custom rules matches 002 behavior, complex Mihomo syntax (`AND`, `OR`, `NOT`, `SUB-RULE`, `RULE-SET`) preserved verbatim

### Implementation for User Story 1

- [x] T008 [P] [US1] Implement `Load(folder string) ([]CustomRuleSet, error)` in `internal/customrules/loader.go`: read `*.yaml` files from folder using `os.ReadDir`; parse each with `gopkg.in/yaml.v3`; apply defaults (name from filename sans `.yaml`, priority 1000); log warnings for missing fields, errors for parse failures (skip file, continue); sort result by (Priority ascending, Name ascending); return nil slice (not error) for nonexistent/empty folder; log warning via `slog` for nonexistent folder
- [x] T009 [US1] Add `MergeCustomRules(upstreamRules []string, custom []CustomRuleSet, fallbackRuleTarget string) []string` to `internal/merge/rules.go`: concatenate upstream rules, then all custom rules in priority order (already sorted by Load), then `MATCH,<fallbackRuleTarget>`; this replaces the current `MergeRulesWithFallback` behavior for callers that have custom rules — keep `MergeRulesWithFallback` unchanged for backward compatibility
- [x] T010 [US1] Thread custom rules through `Pipeline` in `internal/merge/pipeline.go`: add `customRules []customrules.CustomRuleSet` field; add `WithCustomRules(rules []customrules.CustomRuleSet)` builder method; in `Build()`, replace the `MergeRulesWithFallback` call with `MergeCustomRules(rulesPerSource, contributing, p.customRules, p.fallbackRuleTarget)`; if custom rules is empty, behavior is identical to 002

**Checkpoint**: US1 complete — operators can define custom rules that appear in the served config at priority-ordered positions

---

## Phase 4: User Story 2 — Continent-Based Proxy Groups (Priority: P2)

**Goal**: Server automatically creates `_continent_<CONT>` proxy groups by aggregating `_region_<CC>` groups via a country-to-continent mapping table

**Independent Test**: Provide upstreams with proxies classified into `_region_US`, `_region_CN`, `_region_DE`; verify served config contains `_continent_NA`, `_continent_AS`, `_continent_EU` with correct membership

### Tests for User Story 2 (write FIRST, verify they FAIL)

- [x] T011 [P] [US2] Write TC-U-CONTINENT-01 through TC-U-CONTINENT-07 and TC-U-CONTINENT-ORDER-01 tests in `internal/merge/region_test.go`: test single-region continent group, multi-region continent group union, continent membership ordering (grouped by region code alphabetical, then proxy order within region), no regions yields no continents, unmapped country code logs warning and excludes proxy, continent groups appended to Proxies group, determinism across two calls
- [x] T012 [P] [US2] Add continent mapping coverage tests in `internal/merge/region_table_test.go`: verify every entry in `countryToContinent` has a valid two-letter continent code from {AF, AS, EU, NA, SA, OC, AN}; verify all country codes from the region table have a continent mapping; add `init()` validation that panics on invalid entries

### Implementation for User Story 2

- [x] T013 [P] [US2] Add `countryToContinent` mapping table in `internal/merge/region_table.go`: `var countryToContinent = []countryContinentEntry{...}` covering all ~195 ISO 3166-1 alpha-2 codes mapped to one of {AF, AS, EU, NA, SA, OC, AN}; add `continentOf(cc string) (string, bool)` lookup function; add `init()` validation that panics on duplicate CC or invalid continent code; store as ordered slice (not map) for deterministic iteration
- [x] T014 [US2] Implement `AppendContinentGroups(groups []*yaml.Node, regionGroupNames []string, regionGroupMembers map[string][]string, proxiesGroupName string, unmappedContinentLogger func(cc string)) []*yaml.Node` in `internal/merge/region.go`: for each region group name `_region_<CC>`, look up continent via `continentOf(CC)`; collect proxies per continent; emit `_continent_<CONT>` groups of type `select` with deterministic member ordering (region code alphabetical, then proxy order within region); skip unmapped CCs (log once); skip empty continents; append continent group names to Proxies group member list
- [x] T015 [US2] Wire continent groups into `Pipeline.Build()` in `internal/merge/pipeline.go`: after the existing `AppendRegionGroups` call, collect the region group names and their member lists; call `AppendContinentGroups` with the collected data and the proxiesGroupName

**Checkpoint**: US2 complete — continent groups appear in served config alongside region groups

---

## Phase 5: User Story 3 — Unclassified Nodes Proxy Group (Priority: P3)

**Goal**: Server creates a `_region_UNKNOWN` group containing all upstream proxies that could not be classified into any country

**Independent Test**: Provide an upstream with a proxy whose display name has no country indicator; verify `_region_UNKNOWN` exists with that proxy as a member

### Tests for User Story 3 (write FIRST, verify they FAIL)

- [x] T016 [P] [US3] Write TC-U-UNKNOWN-01 through TC-U-UNKNOWN-05 tests in `internal/merge/region_test.go`: test unclassified proxy appears in `_region_UNKNOWN`, all proxies classified yields no unknown group, multiple unclassified proxies from two sources in source-priority order, own-proxy excluded from unknown group, unknown group added to Proxies group member list

### Implementation for User Story 3

- [x] T017 [US3] Add `_region_UNKNOWN` emission to `AppendRegionGroups` in `internal/merge/region.go`: after the existing region group loop, collect all proxy names that were NOT assigned to any `_region_<CC>` group (i.e., upstream proxies where `inferCountry` returned false); if non-empty, emit a `_region_UNKNOWN` group of type `select` with those proxies in their original order; append to Proxies group member list; this is a modification of the existing function, not a new function
- [x] T018 [US3] Verify `_region_UNKNOWN` is wired through `Pipeline.Build()` in `internal/merge/pipeline.go`: the existing `AppendRegionGroups` call already covers it (T017 modified the function in-place); confirm the unknown group is collected alongside region groups for the continent groups call in T015

**Checkpoint**: US3 complete — unclassified nodes have a dedicated group for rule targeting

---

## Phase 6: User Story 4 — User-Agent Access Control (Priority: P4)

**Goal**: Restrict API access to authorized clients by validating the User-Agent header; configurable via `HONKAI_RULE_CLIENT_UA` env var; returns 403 when UA doesn't match

**Independent Test**: Set `HONKAI_RULE_CLIENT_UA=Honkai-Rule-Client,curl`; send request with `User-Agent: Honkai-Rule-Client/1.0` → 200; send request with `User-Agent: Mozilla/5.0` → 403

### Tests for User Story 4 (write FIRST, verify they FAIL)

- [x] T019 [P] [US4] Write TC-U-UA-01 through TC-U-UA-06 tests in `internal/auth/ua_filter_test.go`: test matching prefix passes through to next handler, second prefix matches, non-matching UA returns 403, empty/nil prefixes passes all requests, missing User-Agent header returns 403, rejected request logged with UA value and remote address (capture slog output)

### Implementation for User Story 4

- [x] T020 [P] [US4] Implement `RequireUserAgent(prefixes []string, log *slog.Logger) func(http.Handler) http.Handler` in `internal/auth/ua_filter.go`: middleware that checks `r.Header.Get("User-Agent")` against configured prefixes using `strings.HasPrefix` (case-sensitive); if nil/empty prefixes, pass through; if no match, log `ua-rejected` event with `user_agent`, `remote`, `path` fields and return 403 with empty body; matching requests pass to next handler
- [x] T021 [US4] Wire UA middleware into server mux in `internal/server/app.go`: in `buildMux()`, if `a.cfg.AllowedUserAgentPrefixes` is non-nil and non-empty, wrap the subscription handler with `auth.RequireUserAgent(a.cfg.AllowedUserAgentPrefixes, a.logger)` before the existing `auth.RequireToken` middleware; the UA check runs first (before token validation), so unauthorized clients never reach the token lookup

**Checkpoint**: US4 complete — UA filtering restricts access to authorized clients when configured

---

## Phase 7: Integration Tests & Polish

**Purpose**: End-to-end validation of all features together, snapshot regeneration, and final `make check`

- [x] T022 [P] Add TC-I-003-01 through TC-I-003-08 integration tests in `internal/integration/pipeline_test.go`: test custom rules in output (position after upstream, before MATCH), custom rule targeting region group, continent groups present with correct membership, unknown group present for unclassified proxy, UA filtering enabled (200 for match, 403 for non-match), UA filtering disabled (all pass), determinism (100 sequential byte-identical requests), all features coexisting; follow existing integration test patterns (use `t.TempDir()` for custom rules fixtures, build `merge.Pipeline` directly for merge tests, use `httptest.Server` for UA tests)
- [x] T023 Regenerate integration snapshots: run `UPDATE_SNAPSHOTS=true go test ./internal/integration/...` to update `served-config.snap.yaml` with new continent groups, unknown group, and verify the snapshot content is correct (continent groups have plausible membership, unknown group contains unclassified nodes, no custom rules in default fixture since no custom-rules folder in test fixtures)
- [x] T024 Run `make check` and verify all tests pass (`go vet`, `staticcheck`, full test suite, snapshot drift check); fix any issues found
- [x] T025 Commit all changes and verify `git diff --exit-code` is clean after `make check`

**Checkpoint**: All features complete, tested, and passing CI

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies — start immediately
- **Foundational (Phase 2)**: Depends on Phase 1 (T003 config fields must exist) — BLOCKS all user stories
- **US1 Custom Rules (Phase 3)**: Depends on Phase 2 — can start as soon as config tests pass
- **US2 Continent Groups (Phase 4)**: Depends on Phase 2 — can run in parallel with US1 (different files)
- **US3 Unknown Group (Phase 5)**: Depends on US2 (modifies same `region.go` function) — must follow US2
- **US4 UA Filtering (Phase 6)**: Depends on Phase 2 (config field) — can run in parallel with US1/US2/US3
- **Integration (Phase 7)**: Depends on ALL user stories — runs last

### User Story Dependencies

- **US1 (P1)**: Independent — starts after Phase 2
- **US2 (P2)**: Independent — starts after Phase 2; parallel with US1
- **US3 (P3)**: Depends on US2 (both modify `internal/merge/region.go`) — sequential after US2
- **US4 (P4)**: Independent — starts after Phase 2; parallel with US1/US2/US3

### Within Each User Story

- Tests MUST be written and FAIL before implementation (Constitution Principle IV)
- Types/types definitions before loader/implementation
- Loader before pipeline integration
- Implementation before integration tests

### Parallel Opportunities

Within a phase, tasks marked `[P]` operate on different files and can run concurrently:

```
Phase 1: T001 ──┬── T002 [P] ──┬── Phase 2
               └── T003 [P] ──┘

Phase 2: T004 [P] ──┬── Phase 3/4/5/6
         T005 [P] ──┘

Phase 3+4 (parallel):  T006-T009 (US1 files)  ║  T011-T013 (US2 files)  ║  T019-T020 (US4 files)
                       T010 (US1 pipeline)     ║  T014-T015 (US2 region) ║  T021 (US4 server)

Phase 5 (after US2): T016-T017-T018 (US3 files)

Phase 7: T022 [P] ── T023 ── T024 ── T025
```

---

## Parallel Example: US1 + US2 + US4

With three developers or parallel agents:

```bash
# Agent A: User Story 1 (Custom Rules)
Task T006, T007  # tests (parallel)
Task T008        # loader implementation
Task T009        # merge implementation
Task T010        # pipeline wiring

# Agent B: User Story 2 (Continent Groups)
Task T011, T012  # tests (parallel)
Task T013        # continent table
Task T014        # AppendContinentGroups
Task T015        # pipeline wiring

# Agent C: User Story 4 (UA Filtering)
Task T019        # tests
Task T020        # middleware
Task T021        # server wiring
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1: Setup (module path + scaffolding)
2. Complete Phase 2: Foundational config tests
3. Complete Phase 3: User Story 1 (custom rules)
4. **STOP and VALIDATE**: Operators can add custom rules; run `make check`
5. Deploy if ready — custom rules deliver immediate operator value

### Incremental Delivery

1. Setup + Foundational → Config bindings ready
2. US1 → Custom rules working → Deploy (MVP!)
3. US2 → Continent groups → Deploy
4. US3 → Unknown group → Deploy (tiny diff, same file as US2)
5. US4 → UA filtering → Deploy
6. Integration → Snapshots regenerated → Final CI pass

### Shared File Conflicts

- `internal/merge/pipeline.go` is modified by T010 (US1), T015 (US2), and T018 (US3) — these MUST be sequential
- `internal/merge/region.go` is modified by T014 (US2) and T017 (US3) — sequential
- `internal/merge/region_test.go` is modified by T011 (US2) and T016 (US3) — sequential

---

## Notes

- All test tasks reference specific TC IDs from the plan's Test Plan section for grep-ability
- Constitution Principle IV requires tests land BEFORE implementation — each phase lists tests first
- The module path change (T001) is a broad find-and-replace that should be verified with `go build ./...` after completion
- US3 (unknown group) is intentionally small — it's a ~20-line addition to `AppendRegionGroups` in `region.go`
- Snapshot regeneration (T023) requires manual review of the generated snapshot content
- `make check` includes `git diff --exit-code` so working tree must be clean after tests pass
