---
description: "Task list for feature 015 — Prune Empty Proxy-Groups for Mihomo Compatibility"
---

# Tasks: Prune Empty Proxy-Groups for Mihomo Compatibility

**Input**: Design documents from `/specs/015-remove-empty-proxy-groups/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Test tasks ARE included and are **mandatory** — Constitution Principle IV
(Test-First, Real-Input Integration) is NON-NEGOTIABLE for any change to the
transformation core. Every unit test below is written first and MUST fail before its
implementation task lands.

**Organization**: Tasks are grouped by the three user stories from spec.md. The
feature is implemented as one pure function (`PruneEmptyProxyGroups`) whose three
passes map 1:1 to the three stories; per-story unit tests give each story independent
verifiability even though they share a file.

## Path Conventions

Single Go project. All transformation code in `internal/merge/`; integration harness
in `internal/integration/`. Paths below are repository-relative.

---

## Phase 1: Setup

**Purpose**: Establish a known-good baseline before changes.

- [X] T001 Confirm the working tree on branch `015-remove-empty-proxy-groups` builds clean by running `make check` and recording that it passes (vet + staticcheck + tests + snapshot drift) — this is the baseline FR-010 protects.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Create the shared prune function scaffold and wire it into the pipeline so every user story has a call site to fill in.

**⚠️ CRITICAL**: No user story work can begin until this phase is complete.

- [X] T002 Create `internal/merge/prune.go`: define the prune-result representation (removed-group names + rule-retarget records, or a `PruneEvent` slice — see `data-model.md`) and the `PruneEmptyProxyGroups` function skeleton with the signature from `data-model.md` (inputs: `[]*yaml.Node` groups, `[]string` rules, `proxiesGroupName`, `fallbackRuleTarget`); the skeleton returns its inputs unchanged with no events.
- [X] T003 Wire `PruneEmptyProxyGroups` into `Pipeline.Build()` in `internal/merge/pipeline.go` at the end of `Build()` (after `AppendFanoutProxies`, immediately before the `MergedConfig` literal), passing `mergedGroups`, `mergedRulesResult.Rules`, `p.proxiesGroupName`, `p.fallbackRuleTarget`; assign the returned groups and rules back into the `MergedConfig`. With the T002 skeleton this is a behavioral no-op that keeps the build green.
- [X] T004 [P] Create `internal/merge/prune_test.go` with `TestPruneEmptyProxyGroups_NoEmptyInput`: build a `[]*yaml.Node` group set where every group has ≥1 member, run `PruneEmptyProxyGroups`, and assert groups + rules are returned structurally unchanged with zero prune events (the FR-010 invariant guard; passes against the skeleton and must keep passing).

**Checkpoint**: Prune function exists, is called in the pipeline as a no-op, and the FR-010 guard test is green — user stories can now begin.

---

## Phase 3: User Story 1 - Served config loads in the Mihomo client (Priority: P1) 🎯 MVP

**Goal**: Remove every empty, non-protected proxy-group from the served configuration so Mihomo accepts the profile; never remove the always-present `Proxies` selector.

**Independent Test**: Build a group set containing an empty non-protected group plus an empty `Proxies` selector; after `PruneEmptyProxyGroups` the non-protected empty group is gone and `Proxies` remains, with all other groups' order and attributes intact.

### Tests for User Story 1 (write first, must FAIL) ⚠️

- [X] T005 [P] [US1] In `internal/merge/prune_test.go`, add written-first unit tests covering: an empty non-protected group is removed (FR-001/FR-002, both `proxies: []` and absent-key forms); the protected `Proxies` selector is retained when empty (FR-007); a group named by `fallbackRuleTarget` is retained when empty; surviving groups keep their original relative order and every attribute (FR-009); the case where every non-protected group is empty leaves only `Proxies`.
- [X] T006 [P] [US1] Create integration fixture `internal/integration/testdata/fixtures/upstream/prune.yaml` — an upstream payload whose merged output yields an empty proxy-group, plus (for US2/US3 coverage) a non-empty group that lists the empty group as a member and an upstream rule whose target is the empty group — and register the source in `internal/integration/testdata/fixtures/subscriptions.csv`.
- [X] T007 [P] [US1] Create `internal/integration/prune_test.go` with `TestSnapshot_PruneServedConfig`: build the pipeline against the prune fixture, render subscription mode, compare the body to `testdata/snapshots/served-config-prune.snap.yaml` (baseline generated in Polish T017), and parse-and-assert that no served proxy-group has an empty `proxies:` list except `Proxies`.

### Implementation for User Story 1

- [X] T008 [US1] In `internal/merge/prune.go`, implement empty-group detection (via `mappingMembers`), the protected-name set (`{proxiesGroupName}` ∪ `{fallbackRuleTarget}` when it matches a group name), and the single removal pass that builds a new group slice keeping only surviving nodes in original order (FR-001..FR-005, FR-009); record removed-group names in the result.
- [X] T009 [US1] In `internal/merge/pipeline.go`, emit the FR-011 structured `slog.Info` event `event="proxy-groups-pruned"` carrying `removed_count` and `removed` (group names) from the prune result, matching the existing `fanout-emitted` logging style.

**Checkpoint**: Empty groups are removed (except `Proxies`); US1 unit tests pass. MVP is functional.

---

## Phase 4: User Story 2 - Removing an empty group leaves no broken references (Priority: P2)

**Goal**: After removal, no surviving proxy-group (including `Proxies`) lists a removed group's name as a member.

**Independent Test**: Build a group set where a surviving group lists a to-be-removed empty group among real members; after `PruneEmptyProxyGroups` the surviving group no longer lists the removed name.

### Tests for User Story 2 (write first, must FAIL) ⚠️

- [X] T010 [P] [US2] In `internal/merge/prune_test.go`, add written-first unit tests: a surviving group that listed a removed group no longer lists it (FR-006); the protected `Proxies` selector has its references to removed groups dropped; member references to still-present groups and to proxies are left untouched; per FR-005 a group emptied solely by this cleanup is NOT itself removed.

### Implementation for User Story 2

- [X] T011 [US2] In `internal/merge/prune.go`, implement the member-reference cleanup pass that runs after the removal pass: for every surviving group, rewrite its `proxies` member list (via `setMappingMembers`) dropping any entry equal to a removed group's name; do not re-evaluate emptiness (single pass, FR-005).

**Checkpoint**: No dangling member references remain; US1 + US2 unit tests pass.

---

## Phase 5: User Story 3 - Routing rules never point at a removed group (Priority: P3)

**Goal**: A routing rule whose target group was removed is redirected to the configured fallback rule target.

**Independent Test**: Build a rule set with a rule targeting a to-be-removed group; after `PruneEmptyProxyGroups` that rule's target is `fallbackRuleTarget` and the `Rules`/`RulePriorities`/`RuleContributors` lengths are unchanged.

### Tests for User Story 3 (write first, must FAIL) ⚠️

- [X] T012 [US3] In `internal/merge/prune_test.go`, add written-first unit tests for the rule-target extraction helper across rule shapes: `MATCH,X`; `DOMAIN-SUFFIX,a.com,X`; `IP-CIDR,10.0.0.0/8,X,no-resolve`; `RULE-SET,name,X`; logical `AND,((DOMAIN,d),(NETWORK,tcp)),X`.
- [X] T013 [US3] In `internal/merge/prune_test.go`, add written-first unit tests for the retarget pass: a rule targeting a removed group is rewritten to `fallbackRuleTarget` (FR-008); a rule targeting a surviving group or a proxy is untouched; rule count is unchanged so parallel priority/contributor slices stay aligned.

### Implementation for User Story 3

- [X] T014 [US3] In `internal/merge/prune.go`, implement `ruleTarget(rule string) (target string, fieldIdx int, ok bool)` — `MATCH` → field index 1; otherwise the last comma-separated field, or the second-to-last when the last field is the known option `no-resolve` (see `research.md` Decision 4).
- [X] T015 [US3] In `internal/merge/prune.go`, implement the rule-retarget pass: for each rule whose extracted target equals a removed group's name, rewrite that field to `fallbackRuleTarget` in place; record (rule index, old target, new target) in the result.
- [X] T016 [US3] In `internal/merge/pipeline.go`, emit one FR-011 structured `slog.Info` event `event="rule-retargeted"` per retarget (`rule_index`, `old_target`, `new_target`) from the prune result.

**Checkpoint**: All three user stories are independently functional; all unit tests pass.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: End-to-end verification and the deterministic-output gate.

- [X] T017 Generate the integration snapshot baseline `internal/integration/testdata/snapshots/served-config-prune.snap.yaml` with `UPDATE_SNAPSHOTS=true go test ./internal/integration/ -run TestSnapshot_PruneServedConfig`, then review it against `contracts/served-config-proxy-groups.md` (no empty group except `Proxies`, no dangling member references, no dangling rule targets, surviving order/attributes preserved).
- [X] T018 [P] Run the full integration snapshot suite and confirm `internal/integration/testdata/snapshots/served-config.snap.yaml` shows zero drift — the FR-010 byte-stability proof that pruning is a no-op when no group is empty.
- [X] T019 [P] Run `make check` (vet + staticcheck + tests + snapshot-drift) and confirm it passes.
- [X] T020 Validate `specs/015-remove-empty-proxy-groups/quickstart.md`: run the listed commands and confirm the `proxy-groups-pruned` / `rule-retargeted` log events appear for the prune fixture.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies — start immediately.
- **Foundational (Phase 2)**: Depends on Setup. T002 → T003 (wiring needs the symbol); T004 can run alongside (different file). BLOCKS all user stories.
- **User Story 1 (Phase 3)**: Depends on Foundational. MVP.
- **User Story 2 (Phase 4)**: Depends on Foundational; implementation (T011) depends on US1's removal pass (T008) because it consumes the removed-group set.
- **User Story 3 (Phase 5)**: Depends on Foundational; implementation (T015) depends on US1's removed-group set (T008).
- **Polish (Phase 6)**: Depends on all user stories — the integration snapshot (T017) reflects US1+US2+US3 behavior on the shared fixture.

### User Story Dependencies

- **US1 (P1)**: Independent. The MVP — empty groups removed.
- **US2 (P2)**: Logically builds on US1 (cleans up references to the groups US1 removes). Unit-testable independently with hand-built inputs.
- **US3 (P3)**: Logically builds on US1 (retargets rules pointing at the groups US1 removes). Unit-testable independently.

### Within Each User Story

- Unit tests (Constitution Principle IV) written first and MUST FAIL before implementation.
- `prune.go` is edited by T002, T008, T011, T014, T015 in sequence (same file).
- `pipeline.go` is edited by T003, T009, T016 in sequence (same file).
- `prune_test.go` is edited by T004, T005, T010, T012, T013 in sequence (same file).

### Parallel Opportunities

- T004 is parallel with T002/T003 setup of `pipeline.go`/`prune.go` (different file; signature is known from `data-model.md`).
- Within US1, T005 / T006 / T007 touch three different files and can run in parallel.
- T018 and T019 in Polish are independent and can run in parallel.
- US2 and US3 unit-test authoring (T010, T012, T013) can be drafted in parallel by different developers once Foundational is done, since they are additive test functions.

---

## Parallel Example: User Story 1

```bash
# After Foundational (Phase 2) completes, launch the three US1 test/fixture tasks together:
Task: "T005 Unit tests for empty-group removal in internal/merge/prune_test.go"
Task: "T006 Integration fixture internal/integration/testdata/fixtures/upstream/prune.yaml + subscriptions.csv"
Task: "T007 Integration test internal/integration/prune_test.go"
```

---

## Implementation Strategy

### MVP First (User Story 1 only)

1. Phase 1: Setup — confirm baseline green.
2. Phase 2: Foundational — prune scaffold + pipeline wiring (no-op).
3. Phase 3: US1 — empty-group removal + FR-011 group log.
4. **STOP and VALIDATE**: US1 unit tests prove empty groups (except `Proxies`) are removed. A Mihomo client can now load a config that previously failed on an empty group — the core fix.

### Incremental Delivery

1. Setup + Foundational → no-op prune in place.
2. + US1 → empty groups removed (MVP — the loadability fix).
3. + US2 → dangling member references cleaned up.
4. + US3 → orphaned rules redirected to the fallback target.
5. + Polish → integration snapshot baseline + FR-010 drift proof + `make check`.

---

## Notes

- [P] tasks = different files, no dependencies on incomplete tasks.
- [Story] label maps each task to its spec.md user story for traceability.
- Tests are mandatory here (Constitution Principle IV) — verify each unit test fails before writing its implementation.
- Per the Constitution snapshot-stability gate, the snapshot generated in T017 and the zero-drift check in T018 must be called out in the PR description.
- Commit after each task or logical group.

### Implementation deviations from the planned wording

- **T006/T007**: the integration scenario does not use a fixture file registered in
  `subscriptions.csv` — the merge integration harness (`cluster_test.go`) hardcodes
  its two upstream stubs and does not read that CSV. Instead, `prune_test.go` builds
  the pipeline directly via `stubMergeCache` against an embedded crafted upstream
  payload (`pruneUpstreamYAML`), the established pattern in `pipeline_test.go`. The
  single crafted payload exercises US1 (empty group), US2 (a group referencing it),
  and US3 (a rule targeting it) together; the snapshot is `served-config-prune.snap.yaml`.
- **T009/T016**: both `slog` events were wired into `pipeline.go` together in T003's
  call site (the `PruneResult` struct carries everything both events need), rather
  than as two separate edits.
