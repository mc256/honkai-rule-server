---

description: "Task list for feature 008-dialer-proxy-fanout"
---

# Tasks: Dialer-Proxy Fan-Out for Own Proxies

**Input**: Design documents from `/specs/008-dialer-proxy-fanout/`
**Prerequisites**: plan.md (loaded), spec.md (loaded), research.md (loaded), data-model.md (loaded), contracts/served-subscription.changes.md (loaded), quickstart.md (loaded)

**Tests**: REQUIRED — Constitution Principle IV (NON-NEGOTIABLE) mandates test-first ordering for any change to the merge transformation core. Both unit tests (per new classifier/decision branch) and at least one integration test against the merged multi-subscription input shape are required.

**Organization**: Tasks are grouped by user story so each story can be implemented and validated independently. The three P1 stories ship as one PR per the spec's "scope-coupled" rationale, but the task ordering inside the PR keeps story boundaries visible for review.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: Which user story this task belongs to (US1, US2, US3); omitted in Setup/Foundational/Polish phases
- File paths are absolute-from-repo-root, exactly as referenced in plan.md and data-model.md

## Path Conventions

Single Go module rooted at the repo. Source lives under `internal/`, tests live alongside source files (`*_test.go`), integration tests under `internal/integration/`, snapshot fixtures under `internal/integration/testdata/snapshots/`.

---

## Phase 1: Setup

**Purpose**: Seed the new file so subsequent tasks can append to it without merge conflicts on creation.

- [X] T001 Create `internal/merge/fanout.go` with package declaration `merge`, imports for `gopkg.in/yaml.v3`, and an exported function stub `func AppendFanoutProxies(ownProxies []*yaml.Node, mergedGroups []*yaml.Node, proxiesGroupName string) ([]*yaml.Node, int) { return nil, 0 }`. Add a doc comment summarizing the contract per data-model.md (signature + ordering invariants + FR-005 skip behavior). Also create `internal/merge/fanout_test.go` with package `merge` and import block (`testing`, `gopkg.in/yaml.v3`); leave the file body empty for upcoming test functions.

---

## Phase 2: Foundational

**Purpose**: Blocking prerequisites for all user stories.

No foundational tasks. The skeleton from T001 is sufficient infrastructure; the merge layer's existing helpers (`cloneNode`, `setMappingValue`, `getMappingField`, `mappingMembers`) are already in place and need no changes.

---

## Phase 3: User Story 1 — Per-region/per-continent fan-out (Priority: P1) 🎯 MVP

**Goal**: For every operator-declared own-proxy and every server-emitted `_region_*`/`_continent_*` group (including `_region_UNKNOWN`), emit one fan-out proxy in the served `proxies:` block named `via_<G>__<P>` carrying the source own-proxy's connection fields verbatim plus `dialer-proxy: <full-group-name>`. Honor FR-005's per-own-proxy skip rule (own-proxies that already declare `dialer-proxy` are excluded from fan-out entirely).

**Independent Test**: Build a `Pipeline` with one upstream contributing two CN-classifiable proxies (post-prefix `alpha_*`) and three own-proxies declared in `own-proxies.yaml` (none with `dialer-proxy`). Build the merged config; assert that `MergedConfig.Proxies` contains the three rewritten own-proxies (`_<original>`) AND, for each own-proxy, one fan-out copy per emitted `_region_*`/`_continent_*`/`_region_UNKNOWN` group with the correct `dialer-proxy` value pointing back to the group. Independent of US2's AUTO emission and US3's Proxies-group exclusion.

### Tests for User Story 1 ⚠️ (Write FIRST, ensure they FAIL before T007)

- [X] T002 [US1] Add unit test `TestAppendFanoutProxies_BasicRegionFanout` to `internal/merge/fanout_test.go`: synthesize one own-proxy node (`name: _markham`, plus `type: ss`, `server: 173.32.232.215`, `port: 8080`, `cipher: xchacha20-ietf-poly1305`, `password: pw`, `udp: true`, `ip-version: ipv4`) and a `mergedGroups` slice containing two region groups (`_region_HK`, `_region_JP`). Call `AppendFanoutProxies(ownProxies, mergedGroups, "Proxies")`. Assert the returned slice contains exactly two entries (US1 scope; AUTO will land in US2 and update this assertion); each entry has a `name` field equal to `via_region_HK__markham` or `via_region_JP__markham`; each carries every field from the source own-proxy verbatim except `name`; each carries `dialer-proxy: _region_HK` or `dialer-proxy: _region_JP` matching its name. Skipped count = 0.
- [X] T003 [US1] Add unit test `TestAppendFanoutProxies_ContinentAndUnknown` to `internal/merge/fanout_test.go`: synthesize one own-proxy and a `mergedGroups` slice with `_region_UNKNOWN` and `_continent_AS`. Assert the returned slice contains entries `via_region_UNKNOWN__<own>` (with `dialer-proxy: _region_UNKNOWN`) and `via_continent_AS__<own>` (with `dialer-proxy: _continent_AS`). Asserts that `_region_UNKNOWN` is not specially excluded from fan-out and that `_continent_*` groups are matched alongside `_region_*` groups.
- [X] T004 [US1] Add unit test `TestAppendFanoutProxies_DeterministicOrder` to `internal/merge/fanout_test.go`: synthesize two own-proxies (`_first`, `_second`) and a `mergedGroups` slice with `[_region_JP, _region_HK, _continent_AS]` in that exact slice order. Call the function twice and assert both returned slices have the same length, the same entry names, in the same order: per-own-proxy block contiguous (all `_first`'s copies, then all `_second`'s copies); within each block, copies appear in mergedGroups order (so `via_region_JP__first`, `via_region_HK__first`, `via_continent_AS__first`, then the same for `_second`). Note: AUTO ordering (AUTO before per-group) is added in US2 and updates this assertion.
- [X] T005 [US1] Add unit test `TestAppendFanoutProxies_FieldCopyVerbatim` to `internal/merge/fanout_test.go`: synthesize one own-proxy with a representative spread of fields (`type`, `server`, `port`, `cipher`, `password`, `udp`, `udp-over-tcp`, `udp-over-tcp-version`, `ip-version`) plus one `_region_X` group. Call the function and pick out the one fan-out entry. For every source-own field except `name`, assert `getMappingField(fanoutEntry, key)` equals `getMappingField(sourceOwn, key)`. Assert `getMappingField(fanoutEntry, "name") == "via_region_X__<own>"` and `getMappingField(fanoutEntry, "dialer-proxy") == "_region_X"`.
- [X] T006 [US1] Add unit test `TestAppendFanoutProxies_SkipsExplicitDialerProxy` to `internal/merge/fanout_test.go`: synthesize two own-proxies — one without `dialer-proxy` (`_a`) and one with `dialer-proxy: DIRECT` (`_b`) — plus a `mergedGroups` slice with `_region_JP` and `_region_HK`. Call the function. Assert (a) the returned slice contains entries only for `_a` (`via_region_JP__a`, `via_region_HK__a` — total 2 in US1 scope, will become 3 with AUTO in US2), zero entries for `_b`; (b) the returned skipped count = 1.

### Implementation for User Story 1

- [X] T007 [US1] Replace the stub body of `AppendFanoutProxies` in `internal/merge/fanout.go` with the per-group emission logic: outer loop over `ownProxies`, skip any whose mapping already has `dialer-proxy` (via `getMappingField(p, "dialer-proxy") != ""`) — increment `skipped`. For each non-skipped own-proxy, scan `mergedGroups` in slice order, pick those whose `name` starts with `_region_` or `_continent_`, and for each: `clone := cloneNode(ownProxy)`, `setMappingValue(clone, "name", scalar("via_"+stripUnderscore(groupName)+"__"+stripUnderscore(ownName)))`, `setMappingValue(clone, "dialer-proxy", scalar(groupName))`, append `clone` to the result slice. Add a small unexported helper `stripUnderscore(s string) string` that returns `s[1:]` if `s` begins with `_`, otherwise `s` unchanged. Add a small unexported helper `scalar(s string) *yaml.Node` returning a string scalar node — only if not already present in `yamlutil.go` (search first; reuse if found). All five US1 unit tests (T002–T006) must pass.
- [X] T008 [US1] Wire `AppendFanoutProxies` into `internal/merge/pipeline.go` `Build()`. Insert the call immediately after the existing `AppendContinentGroups` block (currently around line 249) and before the `aggregatedUI := AggregateSubscriptionUserinfo(...)` line. The call: `fanoutProxies, _ := AppendFanoutProxies(rewrittenOwnProxies, mergedGroups, p.proxiesGroupName)` followed by `mergedProxies = append(mergedProxies, fanoutProxies...)`. Discard the skipped count for now (consumed in T016). Existing pipeline tests in `internal/merge/pipeline_test.go` must continue to pass; if any assert exact `len(mergedProxies)` and the fixture has own-proxies, update those assertions to reflect the new fan-out tail.
- [X] T009 [US1] Add integration test `TestI_008_01_PerGroupFanoutInServedBody` in `internal/integration/pipeline_test.go`. Set up a Pipeline with one synthetic upstream payload contributing two CN-classifiable proxies and an own-proxies YAML containing two own-proxies (no `dialer-proxy` on either) and one own-group. Build, marshal proxies to a name set, and assert: (a) every `_region_*`/`_continent_*` group emitted by the merge has a corresponding `via_<group-stripped>__<own-stripped>` entry in `MergedConfig.Proxies` for each own-proxy (use `cloneNode`-aware comparisons rather than YAML diff); (b) every such fan-out entry has `dialer-proxy:` matching its target group; (c) the original own-proxies (`_<own>`) remain in `MergedConfig.Proxies` unchanged. AUTO copies will be checked in US2's integration test (T013); just don't assert their absence here so US2 can drop in cleanly.

**Checkpoint**: User Story 1 complete. Run `go test ./internal/merge/ ./internal/integration/`. The per-region/per-continent fan-out is fully functional and tested; the served body now contains `via_<region|continent>_<…>__<own>` entries for each own-proxy without an explicit dialer-proxy.

---

## Phase 4: User Story 2 — AUTO variant per own-proxy (Priority: P1)

**Goal**: For every operator-declared own-proxy (subject to the FR-005 skip rule from US1), emit exactly one additional fan-out copy named `via_AUTO__<own>` whose `dialer-proxy` is the literal string `Proxies` (the always-present global selector group's name from 001 FR-009a). Emit AUTO before any per-region/per-continent copy for the same own-proxy. AUTO is unconditional on region/continent group counts (one per own-proxy regardless of whether `mergedGroups` contains zero, one, or many region/continent entries).

**Independent Test**: Build a `Pipeline` with three own-proxies (none with `dialer-proxy`) and zero upstream subscriptions (so zero `_region_*`/`_continent_*` groups). Build the merged config; assert `MergedConfig.Proxies` contains exactly three fan-out copies — `via_AUTO__<own1>`, `via_AUTO__<own2>`, `via_AUTO__<own3>` — each with `dialer-proxy: Proxies` and every other field copied verbatim from the source own-proxy. Independent of US1's per-group emission (degenerate region/continent set isolates the test) and US3's Proxies-group exclusion.

### Tests for User Story 2 ⚠️ (Write FIRST, ensure they FAIL before T012)

- [X] T010 [US2] Add unit test `TestAppendFanoutProxies_AutoOnlyWhenNoRegionGroups` to `internal/merge/fanout_test.go`: synthesize one own-proxy and an empty `mergedGroups` slice. Call `AppendFanoutProxies(ownProxies, mergedGroups, "Proxies")`. Assert the returned slice contains exactly one entry: `via_AUTO__<own>` with `dialer-proxy: Proxies`; every other source-own field is present verbatim. Skipped count = 0.
- [X] T011 [US2] Add unit test `TestAppendFanoutProxies_AutoEmittedBeforePerGroup` to `internal/merge/fanout_test.go`: synthesize one own-proxy and `mergedGroups = [_region_JP]`. Call the function. Assert the returned slice has exactly two entries in order: index 0 is `via_AUTO__<own>` with `dialer-proxy: Proxies`; index 1 is `via_region_JP__<own>` with `dialer-proxy: _region_JP`. Update existing US1 tests T002, T004, T006 to reflect the new (US2-augmented) total counts and ordering — each own-proxy now contributes 1 AUTO + N per-group, with AUTO at the head of the per-own-proxy block.

### Implementation for User Story 2

- [X] T012 [US2] Extend `AppendFanoutProxies` in `internal/merge/fanout.go` with the AUTO branch. Inside the outer own-proxy loop, before the inner per-group loop, emit one AUTO copy: `clone := cloneNode(ownProxy)`, `setMappingValue(clone, "name", scalar("via_AUTO__"+stripUnderscore(ownName)))`, `setMappingValue(clone, "dialer-proxy", scalar(proxiesGroupName))` (where `proxiesGroupName` is the function's third parameter, defaulting to `"Proxies"` if empty). Append the AUTO clone to the result slice before iterating mergedGroups. Verify the FR-005 skip rule still skips AUTO emission for own-proxies with explicit `dialer-proxy` (it lives in the same outer-loop guard as US1; should require no additional logic). All US1 + US2 unit tests must pass.

### Integration for User Story 2

- [X] T013 [US2] Add integration test `TestI_008_02_AutoCopyInServedBody` in `internal/integration/pipeline_test.go`. Reuse the synthetic Pipeline setup from TestI_008_01 (or factor a helper). Build, then assert: (a) `MergedConfig.Proxies` contains `via_AUTO__<own>` for each own-proxy in the fixture; (b) each AUTO entry's `dialer-proxy` field equals the literal string `Proxies` (not `_Proxies`, not `_proxies` — exact string match); (c) ordering — the AUTO entry for each own-proxy precedes any `via_region_*`/`via_continent_*` entry for the same own-proxy in the slice.

**Checkpoint**: User Stories 1 + 2 complete. Run `go test ./internal/merge/ ./internal/integration/`. The served body now contains both per-region/per-continent and AUTO fan-out copies in the correct deterministic order.

---

## Phase 5: User Story 3 — Exclude own-proxies and via_* from the global Proxies selector (Priority: P1)

**Goal**: The always-present `Proxies` selector group's `proxies:` member list MUST NOT contain any own-proxy (a name starting with `_` followed by something that does not match `region_*` or `continent_*`) and MUST NOT contain any fan-out copy (a name starting with `via_`). Operator-declared own-groups (`_<original-group>`) are unchanged in their relationship to the Proxies group (still NOT direct members of Proxies, matching the pre-008 behavior).

**Independent Test**: Build a Pipeline with one upstream contributing classifiable proxies + two own-proxies + one own-group. Build; locate the `Proxies` proxy-group; iterate its `proxies:` member list. Assert zero entries with names starting with `_` followed by anything other than `region_` or `continent_`, and zero entries with names starting with `via_`. The `_region_*`, `_continent_*`, and upstream-prefixed names remain present. Independent of US1/US2 emission (the Proxies selector exclusion holds whether or not fan-out copies exist).

### Tests for User Story 3 ⚠️ (Write FIRST, ensure they FAIL before T015)

- [X] T014 [US3] Add integration test `TestI_008_03_ProxiesGroupExcludesOwnAndViaProxies` in `internal/integration/pipeline_test.go`. Reuse or extend the synthetic Pipeline setup from TestI_008_01/02 (so US1 and US2 fan-out copies exist by this point). After Build, find the proxy-group whose `name == "Proxies"` in `MergedConfig.ProxyGroups`; extract its `proxies:` member list. Assert: (a) no member's name has the prefix `_` AND a non-`region_`/non-`continent_` suffix (i.e., no own-proxies); (b) no member's name has the prefix `via_`; (c) at least one member's name matches the upstream prefix (e.g., `alpha_*` or `beta_*`); (d) at least one member's name starts with `_region_` and at least one starts with `_continent_` (when the fixture upstream classifies enough nodes — guarded by an `if len(...) > 0` if the fixture is variable).

### Implementation for User Story 3

- [X] T015 [US3] Modify the `allNames` collection block in `internal/merge/pipeline.go` (currently around lines 200–205, immediately before the `mergedGroups = AppendProxiesGroup(...)` call). Inside the loop, add a guard so names starting with `_` are skipped: `if name == "" || strings.HasPrefix(name, "_") { continue }`. Add the import for `strings` if not already present. The `via_*` exclusion is naturally enforced because fan-out copies are appended to `mergedProxies` *after* this block (T008's wire-up ordering); no additional filter is needed for them. Existing TestI_01_HappyPath assertions in `internal/integration/pipeline_test.go` may now need updating — any assertion that own-proxies appear in the Proxies group's member list must be inverted. Update those assertions accordingly.

**Checkpoint**: All three P1 user stories complete. Run `go test ./internal/merge/ ./internal/integration/`. Fan-out emits both AUTO and per-group copies; the global Proxies selector contains only upstream-prefixed proxies and `_region_*`/`_continent_*` group names.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Observability, end-to-end determinism check, snapshot regeneration, and final validation.

- [X] T016 Add a structured `slog.Info("fanout-emitted", ...)` log line in `internal/merge/pipeline.go` immediately after the `mergedProxies = append(mergedProxies, fanoutProxies...)` line from T008. Capture the skipped count from `AppendFanoutProxies` (was discarded in T008 — change `_` to a named variable). Fields: `event="fanout-emitted"`, `own_proxy_count=len(rewrittenOwnProxies)`, `skipped_explicit_dialer=skipped`, `target_group_count=<count of region/continent groups in mergedGroups>`, `emitted_count=len(fanoutProxies)`. Verify the log line fires exactly once per `Build()` invocation (not per fan-out copy) — Constitution V mandates summary-level observability, not per-copy spam.
- [X] T017 Add integration test `TestI_008_04_FanoutDeterminism` in `internal/integration/pipeline_test.go`. Build the same Pipeline twice in sequence; collect the two `MergedConfig.Proxies` slices; for each pair of corresponding indices assert byte-equal `name` and `dialer-proxy` fields. Stronger form: marshal `MergedConfig` to YAML twice and assert SHA-256 hashes match. This covers FR-015 (byte-identical fan-out across reloads with same inputs).
- [X] T018 Add integration test `TestI_008_05_ExplicitDialerProxyEndToEnd` in `internal/integration/pipeline_test.go`. Set up a Pipeline whose own-proxies fixture includes one own-proxy with `dialer-proxy: DIRECT` declared. Build; assert `MergedConfig.Proxies` contains the original `_<own-with-explicit-dialer>` exactly once with its `dialer-proxy` value preserved as `DIRECT`; assert no `via_AUTO__<that-own>` or `via_region_*__<that-own>` or `via_continent_*__<that-own>` entries appear. (Other own-proxies without explicit `dialer-proxy` continue to receive full fan-out.) Covers FR-005 end-to-end.
- [X] T019 Regenerate the integration snapshot at `internal/integration/testdata/snapshots/served-config.snap.yaml` with `UPDATE_SNAPSHOTS=true go test ./internal/integration/...`. Inspect the diff: confirm only fan-out additions to `proxies:`, removal of own-proxy entries from the `Proxies` group's member list, and no other drift. Stage the regenerated snapshot. The PR description must explicitly call out the regeneration as deliberate per the Snapshot Stability Gate in the constitution.
- [X] T020 Run `make check` and confirm clean output: `go vet ./...`, `staticcheck ./...`, `go test ./...`, snapshot-drift check all pass. If staticcheck flags anything in the new code (unused vars, unused functions, redundant nil checks), fix in place rather than suppressing.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1: T001)**: No dependencies — can start immediately.
- **Foundational (Phase 2)**: Empty — no blocker between Setup and Phase 3.
- **User Story 1 (Phase 3: T002–T009)**: Depends on T001 (file skeleton). T002–T006 (unit tests) must be authored before T007 (implementation) per Constitution Principle IV. T007 must precede T008 (wire-up) which must precede T009 (integration test) — the integration test exercises the wired pipeline.
- **User Story 2 (Phase 4: T010–T013)**: Depends on US1 implementation (T007, T008) — extends the same `AppendFanoutProxies` function. T010–T011 (tests) precede T012 (implementation), which precedes T013 (integration test).
- **User Story 3 (Phase 5: T014–T015)**: Depends on US1's wire-up (T008) so the integration test exercises a wired pipeline. T014 (test) precedes T015 (implementation). Independent of US2's AUTO emission.
- **Polish (Phase 6: T016–T020)**: Depends on US1 + US2 + US3 completion. T016 reads the skipped count produced by US1's logic. T017–T018 are end-to-end checks that need the full pipeline to be wired. T019 (snapshot regen) MUST run after all behavior changes are settled. T020 is the final gate.

### Within-Story Test-First Ordering (Constitution IV)

- US1: T002 → T003 → T004 → T005 → T006 (all FAIL) → T007 (PASS) → T008 → T009
- US2: T010 → T011 (FAIL) → T012 (PASS) → T013
- US3: T014 (FAIL) → T015 (PASS)

### Parallel Opportunities

- T002–T006 could in principle be authored in parallel by multiple developers, but they all live in `internal/merge/fanout_test.go` so file-level coordination is required (sequential commits, parallel authoring). Not marked [P] per the strict same-file convention.
- T009, T013, T014, T017, T018 all live in `internal/integration/pipeline_test.go` (separate test functions, no compile-time interdependence) — same file constraint applies.
- T016 (pipeline.go) and the integration tests (pipeline_test.go) live in different files and have no compile-time coupling once US1/US2/US3 are implemented; they can be done in either order, though logically T016 ships before final snapshot regen so the structured log appears in steady-state telemetry.

No tasks are marked [P] in this list because the feature's compact surface (one new file, one wire-up file, one integration test file, one snapshot) keeps almost every task within an already-touched file. Sequential ordering matches the actual file-coordination cost.

---

## Implementation Strategy

### MVP (User Story 1 only)

1. Phase 1 (T001) — skeleton.
2. Phase 3 US1 (T002–T009) — per-region/per-continent fan-out wired and tested.
3. **STOP and validate**: Run `go test ./internal/merge/ ./internal/integration/` — the per-group fan-out works end-to-end. Snapshot regen is NOT done at this point; CI snapshot drift will fail until Phase 6 T019 lands. For an MVP review, run the unit tests + selected integration tests directly.

### Full P1 delivery (recommended)

The spec's three P1 stories ship together because shipping US1 without US3 produces visibly worse Mihomo UX (selector cluttered with N×(M+1) fan-out entries), and shipping US1 without US2 leaves the most common operator workflow (pick once globally, all own-exits follow) unimplemented. Run all phases T001 → T020 in sequence; one PR.

### Observability sequencing

T016 (the structured log) lands in Phase 6 rather than Phase 3 because it depends on the skipped count from `AppendFanoutProxies`, which only becomes meaningful after US1's FR-005 skip implementation is complete. Adding the log earlier would either log zero-as-placeholder or pull the skipped count out of US1 prematurely.

### Snapshot regeneration

T019 is the LAST behavior-impacting task. Regenerating earlier (e.g., after each story) would produce three intermediate snapshots that all need re-baselining; doing it once at the end makes the snapshot diff legible to reviewers and matches the project's constitution-mandated discipline.

---

## Notes

- The new `AppendFanoutProxies` function is the only new public API surface; everything else is internal mutation of the existing pipeline. No change to `cmd/server/`, `internal/output/`, `internal/server/`, `internal/auth/`, `internal/customrules/`, or `internal/fetcher/`.
- `cloneNode`, `setMappingValue`, `getMappingField`, `mappingMembers` are existing helpers in `internal/merge/yamlutil.go`. Reuse them; do not duplicate logic.
- yaml.v3's `\Uxxxxxxxx` emoji escape behavior is already neutralized at output time (006). Fan-out copies that include emoji in source own-proxy fields will continue to render as literal UTF-8.
- The strengthened `^[a-z]+$` rule on `SubscriptionRow.Name` (002 deviation) means upstream prefixes will never collide with `via_*` or `_*` namespaces. No new collision-prevention logic is needed.
- `make check` is the single command that runs `go vet`, `staticcheck`, `go test ./...`, and snapshot-drift check — used in T020 as the final gate.
