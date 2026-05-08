---

description: "Task list for 007-ascending-priority-sort"
---

# Tasks: Ascending Priority Sort

**Input**: Design documents from `/specs/007-ascending-priority-sort/`
**Prerequisites**: plan.md (required), spec.md (required), research.md, data-model.md, quickstart.md

**Tests**: Per Constitution Principle IV (NON-NEGOTIABLE), every unit test MUST be committed before the implementation it validates. Test tasks are explicitly included and must land first within each user story phase.

**Organization**: This feature has a single user story (P1). The phase structure below uses Phase 3 for the implementation work and Phase 4 for snapshot regeneration. Phases 1 and 2 are intentionally empty — there is no shared infrastructure or foundational work needed for a one-line comparator flip.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (US1)
- Include exact file paths in descriptions

## Path Conventions

- `internal/merge/rules.go` — the merge core; contains the comparator that defines rule sort order
- `internal/merge/rules_test.go` — unit tests TC-U-MERGE-UNIFIED-01..06 from feature 005
- `internal/integration/pipeline_test.go` — integration tests TestI_005_01, TestI_005_02, TestI_003_01
- `internal/integration/testdata/snapshots/served-config.snap.yaml` — committed integration snapshot

---

## Phase 1: Setup

(Empty — the implementation surface is a single line; no orientation reads beyond the plan are required.)

---

## Phase 2: Foundational

(Empty — no shared infrastructure outside of the single user story.)

---

## Phase 3: User Story 1 — Ascending Priority Sort (Priority: P1) MVP

**Goal**: Reverse the rule sort comparator from descending to ascending so that lower priority numbers emit earlier in the served `rules:` block, matching operator routing intuition

**Independent Test**: Configure one upstream `alpha` priority 1000 with a rule for `chrome.test → alpha_proxy`, and one custom rule set `early-exit-google-chrome` priority 200 with a rule for `chrome.test → DIRECT`; build the pipeline; verify the priority-200 custom rule appears at a lower index in `MergedConfig.Rules` than the priority-1000 upstream rule

**Dependency**: None — this is the first user story phase

### Tests for User Story 1 (write/update FIRST, verify they FAIL)

- [X] T001 [US1] In `internal/merge/rules_test.go`: update TC-U-MERGE-UNIFIED-01 (`TestMERGE_UNIFIED_01_UpstreamOnly`) — change `wantRules` to `["RULE-E1", "RULE-E2", "RULE-B1", "MATCH,auto"]`, `wantPrios` to `[]int{1000, 1000, 2000, 0}`, `wantContribs` to `[]string{"alpha", "alpha", "beta", ""}`. The test asserts alpha (priority 1000) emits *before* beta (priority 2000) under ascending sort.
- [X] T002 [US1] In `internal/merge/rules_test.go`: update TC-U-MERGE-UNIFIED-02 (`TestMERGE_UNIFIED_02_CustomOnly`) — change `wantRules` to `["L1", "H1", "H2", "MATCH,auto"]`, `wantPrios` to `[]int{300, 1500, 1500, 0}`, `wantContribs` to `[]string{"low", "high", "high", ""}`.
- [X] T003 [US1] In `internal/merge/rules_test.go`: update TC-U-MERGE-UNIFIED-03 (`TestMERGE_UNIFIED_03_CustomBeatsLowerUpstream`) — change `wantRules` to `["U1", "C1", "MATCH,auto"]`, `wantPrios` to `[]int{1000, 2000, 0}`, `wantContribs` to `[]string{"alpha", "corporate", ""}`. (Under ascending sort, lower priority emits first; in this scenario alpha=1000 < corporate=2000, so alpha now emits before corporate.) Update the test's doc comment as well to reflect the new expectation.
- [X] T004 [US1] In `internal/merge/rules_test.go`: update TC-U-MERGE-UNIFIED-04 (`TestMERGE_UNIFIED_04_UpstreamBeatsLowerCustom`) — change `wantRules` to `["C1", "U1", "MATCH,auto"]`, `wantPrios` to `[]int{1000, 2000, 0}`, `wantContribs` to `[]string{"corporate", "beta", ""}`. (corporate=1000 < beta=2000.) Update the test's doc comment.
- [X] T005 [US1] In `internal/merge/rules_test.go`: TC-U-MERGE-UNIFIED-05 (`TestMERGE_UNIFIED_05_TieBreakAlphabetical`) — verify the test still matches with no edit needed (all priorities are equal at 1000, so alphabetical tie-break preserves the order regardless of sort direction). Add a single-sentence inline comment confirming this test is invariant under the sort flip.
- [X] T006 [US1] In `internal/merge/rules_test.go`: update TC-U-MERGE-UNIFIED-06 (`TestMERGE_UNIFIED_06_MatchFallbackAndSkipEmpty`) — change `wantRules` to `["R1", "RULE-N1", "MATCH,DIRECT"]`, `wantPrios` to `[]int{500, 1000, 0}`, `wantContribs` to `[]string{"real", "normal", ""}`. (real=500 < normal=1000 under ascending.) Update the inline comment that says "normal (1000) > real (500)" to reflect ascending semantics.
- [X] T007 [US1] Run `go test ./internal/merge/... -run TestMERGE_UNIFIED` and verify the 5 updated tests now FAIL (T005 still passes since it's invariant). Failure mode is `reflect.DeepEqual` mismatches showing the old descending order in `got` vs. the new ascending order in `want`.

### Implementation for User Story 1

- [X] T008 [US1] In `internal/merge/rules.go`: flip the comparator on line 82 from `return contributors[i].Priority > contributors[j].Priority` to `return contributors[i].Priority < contributors[j].Priority`.
- [X] T009 [US1] In `internal/merge/rules.go`: update the doc comment on the `MergeUnifiedRules` function (line 36) from `Sort key: (Priority desc, Name asc).` to `Sort key: (Priority asc, Name asc).` so the comment matches the new behavior.
- [X] T010 [US1] Run `go test ./internal/merge/...` and verify all merge unit tests pass (5 reordered + TC-U-MERGE-UNIFIED-05 invariant + the original `Empty/Missing` and any other helpers).
- [X] T011 [US1] In `internal/integration/pipeline_test.go`: update `TestI_005_01_UnifiedPriorityOrder` position invariant — change the assertion `if !(highUpstreamIdx < highCustomIdx && highCustomIdx < lowUpstreamIdx && lowUpstreamIdx < lowCustomIdx && lowCustomIdx < matchIdx)` to `if !(lowCustomIdx < lowUpstreamIdx && lowUpstreamIdx < highCustomIdx && highCustomIdx < highUpstreamIdx && highUpstreamIdx < matchIdx)` (lowest priority first under ascending). Update the error message to match the new expected order.
- [X] T012 [US1] In `internal/integration/pipeline_test.go`: update `TestI_005_02_PriorityBucketHeaderComments` — keep the existing presence-of-substring assertions for both header comments, AND add a new ordering assertion: locate the index of `# --- priority 1000 (corporate) ---` in `body` (via `strings.Index`) and the index of `# --- priority 2000 (beta) ---`; assert that the priority-1000 header index is strictly *less than* the priority-2000 header index. This is the integration-level proof that ascending sort reaches the served YAML.
- [X] T013 [US1] In `internal/integration/pipeline_test.go`: update `TestI_003_01_CustomRulesInOutput` — change the position invariant from `upstreamIdx < customRejectIdx && customRejectIdx < matchIdx` (and the corresponding assertion for `customDirectIdx`) to `customRejectIdx < upstreamIdx && upstreamIdx < matchIdx` (and `customDirectIdx < upstreamIdx && upstreamIdx < matchIdx`). The setup has alpha upstream priority 1000 and corporate-block custom priority 500; under ascending, custom (500) emits before upstream (1000). Update the error messages and the inline comment accordingly. The priority *value* assertions (`mc.RulePriorities[upstreamIdx] != 1000`, `mc.RulePriorities[customRejectIdx] != 500`) are unchanged — those test the metadata at a position, not the position itself.
- [X] T014 [US1] Run `go test ./internal/integration/... -run "TestI_005|TestI_003_01"` and verify the three updated tests pass.

**Checkpoint**: US1 complete — `go test ./internal/merge/... ./internal/integration/...` passes for all rule-related tests. Note: snapshot drift is expected at this point; snapshot regeneration is in Phase 4.

---

## Phase 4: Polish & Snapshot Regeneration

**Purpose**: Regenerate the committed integration snapshot to reflect the ascending order, run the full check suite, and finalize

- [X] T015 Regenerate `internal/integration/testdata/snapshots/served-config.snap.yaml` by running `UPDATE_SNAPSHOTS=true go test ./internal/integration/...`. Then manually review the diff: confirm (a) the rule-section content shifts to ascending order — smaller priority headers appear first, larger last; (b) priority headers themselves remain unchanged in text format `# --- priority N (contributors) ---`; (c) MATCH fallback remains the last entry without a preceding header; (d) no rule, proxy, or proxy-group strings change content (only positions of rule blocks). Per Constitution Snapshot Stability Gate, the PR description must quote the diff summary.
- [X] T016 Run `make check` and verify all gates pass (`go vet`, `staticcheck`, full test suite, snapshot drift check). Fix any unexpected regressions; if `git diff --exit-code` flags only the files this PR touches (rules.go, rules_test.go, pipeline_test.go, served-config.snap.yaml, plus this tasks.md), that is the expected pre-commit state and the after-implement hook commit will resolve it.
- [X] T017 Mark all tasks `[X]` in this file; verify `git diff --exit-code` is clean after the after-implement hook commits the change.

**Checkpoint**: All features complete, snapshot updated, `make check` green

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1 (Setup)**: empty — proceed to Phase 3
- **Phase 2 (Foundational)**: empty — proceed to Phase 3
- **Phase 3 (US1)**: contains all the implementation; tasks within follow strict TDD order (T001–T007 update tests *before* T008–T009 flip the comparator)
- **Phase 4 (Polish)**: depends on Phase 3 (snapshot regeneration requires the comparator flip to be in place)

### User Story Dependencies

- **US1 (P1)**: Standalone — this is the only user story.

### Within User Story 1

- T001–T006 (update unit-test expected arrays) before T007 (verify they fail) before T008–T009 (flip comparator + comment) before T010 (verify they pass).
- T011–T013 (update integration-test position assertions) before T014 (verify they pass).
- T010 and T014 can run in either order, but both must precede Phase 4.

### Parallel Opportunities

```text
Phase 3:  T001 [P] ── T002 [P] ── T003 [P] ── T004 [P] ── T005 [P] ── T006 [P]   (all six edit
                                                                                  the same file
                                                                                  but in disjoint
                                                                                  test functions —
                                                                                  parallelizable
                                                                                  by careful diff
                                                                                  selection)
          T007 (gate)
          T008 ── T009                                                            (same file)
          T010 (gate)
          T011 [P] ── T012 [P] ── T013 [P]                                        (same file but
                                                                                   disjoint funcs)
          T014 (gate)

Phase 4:  T015 ── T016 ── T017                                                    (sequential)
```

In practice, the merge-test edits (T001–T006) are likely fastest done as a single coordinated `Edit` of `rules_test.go`. The integration-test edits (T011–T013) are similar. The compactness of the change makes parallelism a theoretical convenience rather than a meaningful speedup.

---

## Implementation Strategy

### Single-pass execution (recommended for this feature)

Because the change is so localized (one comparator flip + assertion updates in 3 files + snapshot regen), the natural rhythm is:

1. Update merge-test expectations (T001–T006) — single batched edit of `rules_test.go`.
2. Run merge tests, confirm they fail with `reflect.DeepEqual` mismatches showing old desc / new asc (T007).
3. Flip the comparator and doc comment (T008–T009) — two-line edit of `rules.go`.
4. Run merge tests, confirm green (T010).
5. Update integration-test assertions (T011–T013) — batched edit of `pipeline_test.go`.
6. Run integration tests, confirm green (T014).
7. Regenerate snapshot, review diff (T015).
8. `make check`, commit (T016–T017).

### MVP scope

There is no smaller-than-MVP version of this feature. The whole change is a single comparator flip; you cannot ship a "partial" sort direction.

---

## Notes

- The user-supplied scenario in the spec (`alpha` priority 1000 + `early-exit-google-chrome` priority 200) is exactly what TestI_003_01_CustomRulesInOutput already exercises in shape (different priority numbers, but same structural assertion). The change to T013 makes the test assert the operator's intuitive expectation.
- TC-U-MERGE-UNIFIED-05 is intentionally untouched (T005 only adds a confirming comment). Its all-equal-priorities scenario is order-direction-invariant; keeping the test unchanged proves the alphabetical tie-break is unaffected by the comparator flip.
- The snapshot regeneration in T015 is the only place where rule strings are *physically* reordered in committed file content. Reviewers can audit the snapshot diff to confirm only rule-block positions shift; no rule text, proxy text, or group text is added or removed.
- This feature does not change `cmd/server/main.go`, `internal/customrules/`, `internal/output/subscription_mode.go`, or any of features 002/003/004's behavior. Its blast radius is exactly the rule-sort comparator and its tests.
- Per CLAUDE.md guidance, the implementation does not introduce comments explaining "added for feature 007" or similar PR-tracking text. The doc comment update on line 36 reflects the new behavior, not the change history.
