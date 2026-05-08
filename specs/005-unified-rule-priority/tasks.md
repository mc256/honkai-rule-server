---

description: "Task list for 005-unified-rule-priority"
---

# Tasks: Unified Rule Priority

**Input**: Design documents from `/specs/005-unified-rule-priority/`
**Prerequisites**: plan.md (required), spec.md (required), research.md, data-model.md, quickstart.md

**Tests**: Per Constitution Principle IV (NON-NEGOTIABLE), every unit test MUST be committed before the implementation it validates. Test tasks are explicitly included and must land first within each user story phase.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (US1, US2)
- Include exact file paths in descriptions

## Path Conventions

- `internal/merge/rules.go` — merge core (priority sort + contributor metadata)
- `internal/merge/rules_test.go` — unit tests for merge layer
- `internal/merge/pipeline.go` — `Pipeline.Build()` glue and `MergedConfig` struct
- `internal/output/subscription_mode.go` — YAML normalization (priority comments)
- `internal/output/subscription_mode_test.go` — unit tests for output adapter
- `internal/integration/pipeline_test.go` — end-to-end integration tests
- `internal/integration/testdata/snapshots/served-config.snap.yaml` — committed snapshot

---

## Phase 1: Setup

**Purpose**: Orient on the existing merge and output code that will be modified; identify deletion targets

- [X] T001 [P] Read `internal/merge/rules.go` end-to-end to understand `MergeCustomRules`, `MergeCustomRulesWithPriorities`, `MergeResult`, and the existing trailing-rule drop interaction; confirm both functions are package-internal (only callers are in `pipeline.go`)
- [X] T002 [P] Read `internal/merge/pipeline.go` (focus on `MergedConfig` struct, `Pipeline.Build()`, `sortSourcesByPriority`) to identify the call site that will switch to `MergeUnifiedRules` and the fields that need extension
- [X] T003 [P] Read `internal/output/subscription_mode.go` (focus on `normalizeRulesComments` and `Render()`) to identify the function that will be replaced by `normalizeRulesPriorityComments`

**Checkpoint**: Code structure understood; all functions targeted for modification or deletion are identified

---

## Phase 2: Foundational

**Purpose**: None — there is no shared infrastructure outside of user story phases for this feature. The merge-layer change is the central behavior of US1; the output-adapter change is the central behavior of US2. Both are scoped within their respective user story phases.

**Checkpoint**: Proceed directly to Phase 3 (User Story 1)

---

## Phase 3: User Story 1 — Unified Priority Merge (Priority: P1) MVP

**Goal**: Upstream-source rules and custom-rule-set rules merge into a single descending-priority stream so that priority is comparable across both contributor kinds

**Independent Test**: Configure one upstream source (priority 1000) with a rule for `example.com → AUTO` and one custom rule set (priority 2000) with a rule for `example.com → REJECT`; verify the custom rule appears at a lower index in `MergedConfig.Rules` than the upstream rule

**Dependency**: None — this is the first user story phase

### Tests for User Story 1 (write FIRST, verify they FAIL)

- [X] T004 [US1] Write TC-U-MERGE-UNIFIED-01 through TC-U-MERGE-UNIFIED-06 tests in `internal/merge/rules_test.go` for the new `MergeUnifiedRules` function: (01) two upstream sources at priorities 1000 and 2000 with no custom rules emit rules in priority-2000-then-1000 order with parallel `Contributors` populated; (02) two custom rule sets at priorities 300 and 1500 with no upstream sources emit rules in priority-1500-then-300 order; (03) one upstream priority 1000 + one custom priority 2000 emits custom rules first; (04) one upstream priority 2000 + one custom priority 1000 emits upstream rules first; (05) tie-break: upstream `alpha` priority 1000 + custom `corporate` priority 1000 emits in alphabetical name order (corporate first), with `Priorities` showing `1000` for both blocks and `Contributors` distinguishing them; (06) MATCH fallback is always last with `Priorities[len-1] == 0` and `Contributors[len-1] == ""`. All six tests must compile but fail (`MergeUnifiedRules` not yet defined).

### Implementation for User Story 1

- [X] T005 [US1] In `internal/merge/rules.go`: extend `MergeResult` struct to add `Contributors []string` field (parallel to `Rules` and `Priorities`); document the parallel-array invariant
- [X] T006 [US1] In `internal/merge/rules.go`: implement `MergeUnifiedRules(upstreamPerSource map[string][]string, upstreamSources []config.SubscriptionRow, customs []customrules.CustomRuleSet, fallbackRuleTarget string) MergeResult`. Build a unified `[]contributor` (unexported struct with Name/Priority/Rules) by iterating `upstreamSources` (apply trailing-rule drop on each `upstreamPerSource[name]`, skip empty post-drop lists) and `customs` (use rules verbatim, skip empty rule lists). Sort by `(Priority desc, Name asc)`. Concatenate Rules, Priorities, Contributors arrays in iteration order. Append `MATCH,<fallbackRuleTarget>` with priority 0 and contributor "". Verify all six T004 tests pass.
- [X] T007 [US1] In `internal/merge/rules.go`: delete `MergeRules`, `MergeRulesWithFallback`, `MergeCustomRules`, and `MergeCustomRulesWithPriorities`; delete their associated tests in `internal/merge/rules_test.go` (TC-U-RULES-CUSTOM-* and any TC-U-RULES-PRIORITY-* tests from feature 003/004 — replaced by T004's TC-U-MERGE-UNIFIED-* tests)
- [X] T008 [US1] In `internal/merge/pipeline.go`: extend `MergedConfig` struct to add `RuleContributors []string` field (parallel to `Rules` and `RulePriorities`); document the parallel-array invariant
- [X] T009 [US1] In `internal/merge/pipeline.go`: update `Pipeline.Build()` to call `MergeUnifiedRules(rulesPerSource, sortedSources, p.customRules, p.fallbackRuleTarget)` instead of `MergeCustomRulesWithPriorities`; populate `MergedConfig.RuleContributors` from the new `MergeResult.Contributors`. Run `go build ./...` to verify the package compiles after the deletions in T007.
- [X] T010 [US1] In `internal/integration/pipeline_test.go`: add `TestI_005_01_UnifiedPriorityOrder` integration test (TC-I-005-01). Build a Pipeline with two upstream sources at priorities 1000 and 2000 (use `stubMergeCache` pattern) and two custom rule sets at priorities 1500 and 300 (use `customrules.Load` from `t.TempDir()`); call `Pipeline.Build()`; verify rule positions in `mc.Rules` follow the descending-priority order: priority-2000 upstream rules → priority-1500 custom → priority-1000 upstream → priority-300 custom → MATCH; verify `mc.RulePriorities` and `mc.RuleContributors` are parallel and consistent.

**Checkpoint**: US1 complete — `go test ./internal/merge/... ./internal/integration/...` passes. Note: snapshot drift is expected at this point (rule order changed); snapshots are deliberately NOT regenerated until Phase 5.

---

## Phase 4: User Story 2 — Priority-Bucket Header Comments (Priority: P2)

**Goal**: Each priority bucket in the served `rules:` block is preceded by a single header comment `# --- priority N (contributor-list) ---`; the legacy `# --- upstream ---` comment is removed entirely

**Independent Test**: Configure a server with one upstream source `beta` (priority 2000) and one custom rule set `corporate` (priority 1000); fetch the merged subscription; verify exactly two header comments appear in the served `rules:` block — `# --- priority 2000 (beta) ---` before the upstream rules and `# --- priority 1000 (corporate) ---` before the custom rules — and no `# --- upstream ---` comment

**Dependency**: Requires Phase 3 (US1) — needs `MergedConfig.RuleContributors` populated

### Tests for User Story 2 (write FIRST, verify they FAIL)

- [X] T011 [US2] Write TC-U-OUTPUT-PRIORITY-01 through TC-U-OUTPUT-PRIORITY-05 tests in `internal/output/subscription_mode_test.go` for the new `normalizeRulesPriorityComments(root *yaml.Node, priorities []int, contributors []string)` function: (01) single contributor `alpha` priority 1000 with three rules → exactly one head comment `# --- priority 1000 (alpha) ---` on the first rule; (02) two contributors `corporate` and `alpha` sharing priority 1000 → single head comment `# --- priority 1000 (corporate, alpha) ---` on the first rule of the bucket; (03) three priority buckets at 2000/1500/1000 with one contributor each → three head comments in descending priority order, each on the bucket's first rule; (04) priority 0 with empty contributor (the MATCH fallback) → no head comment emitted; (05) verify the output adapter no longer emits the legacy `# --- upstream ---` string anywhere (assert via substring search on the rendered YAML). All five tests must compile but fail (function not yet defined).

### Implementation for User Story 2

- [X] T012 [US2] In `internal/output/subscription_mode.go`: implement `normalizeRulesPriorityComments(root *yaml.Node, priorities []int, contributors []string)`. Find the `rules` sequence at the document root. Iterate rules with their parallel priority and contributor; at each bucket boundary (`priorities[i] != priorities[i-1]` or `i == 0`), if `priorities[i] != 0` (skip MATCH fallback), collect all contributors in this bucket by walking forward until the next boundary, deduplicate while preserving order, sort alphabetically, format `# --- priority N (a, b, c) ---`, set as `HeadComment` on the rule node at index `i`. The format string must match TC-U-OUTPUT-PRIORITY-* expectations exactly. Verify all five T011 tests pass.
- [X] T013 [US2] In `internal/output/subscription_mode.go`: remove the existing `normalizeRulesComments` function and its call site in `Render()`; replace the call with `normalizeRulesPriorityComments(root, mc.RulePriorities, mc.RuleContributors)`. Update the `Render` signature or threading logic if needed to make `mc.RuleContributors` available (mirror the existing `mc.RulePriorities` plumbing from feature 004). Run `go build ./...` to verify the package compiles.
- [X] T014 [US2] In `internal/integration/pipeline_test.go`: add `TestI_005_02_PriorityBucketHeaderComments` integration test (TC-I-005-02). Build a Pipeline with one upstream `beta` priority 2000 and one custom `corporate` priority 1000; serve via the same path as `TestI_002_05_RegionGroups` (Pipeline → adapter → byte body); verify the served body contains the substring `# --- priority 2000 (beta) ---` before any custom rule, the substring `# --- priority 1000 (corporate) ---` before the custom rules, and does NOT contain the substring `# --- upstream ---`.
- [X] T015 [US2] In `internal/integration/pipeline_test.go`: add `TestI_005_03_UnifiedPriorityDeterminism` integration test (TC-I-005-03). Issue 100 sequential subscription fetches against a fixture with both upstream and custom contributors at multiple priorities; assert all 100 SHA-256 hashes of the served body are byte-identical (mirrors TC-I-002-07 from feature 002). This validates SC-004 of the spec.

**Checkpoint**: US2 complete — `go test ./internal/output/... ./internal/integration/...` passes; served YAML has correct comments; legacy `# --- upstream ---` is gone

---

## Phase 5: Polish & Snapshot Regeneration

**Purpose**: Regenerate the committed integration snapshot, run the full check suite, and commit

- [X] T016 Regenerate `internal/integration/testdata/snapshots/served-config.snap.yaml` by running `UPDATE_SNAPSHOTS=true go test ./internal/integration/...`. Then manually review the diff: (a) confirm the rule order matches spec FR-001 (descending priority, alphabetical tie-break); (b) confirm the comment format matches FR-005; (c) confirm no `# --- upstream ---` substring appears (FR-009); (d) confirm rule count and bytes are sane (no rules silently dropped). The PR description must quote the diff summary per Constitution Snapshot Stability Gate.
- [X] T017 Run `make check` and verify all tests pass (`go vet`, `staticcheck`, full test suite, snapshot drift check, `git diff --exit-code`); fix any regressions found. Pay attention to tests in `internal/integration/` — `TestI_001_HappyPath`, `TestI_002_05_RegionGroups`, and `TestI_002_07_MergeDeterminism` may need assertion adjustments to tolerate the new comment format.
- [X] T018 Mark all tasks `[X]` in this file; verify `git diff --exit-code` is clean after `make check`; the speckit-implement after-hook will commit the change.

**Checkpoint**: All features complete, snapshots updated, `make check` green

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1 (Setup)**: Read-only — no dependencies, can start immediately
- **Phase 2 (Foundational)**: empty for this feature — proceed to Phase 3
- **Phase 3 (US1)**: Depends on Phase 1 (orientation); contains the merge-layer change which is the core behavior delivery
- **Phase 4 (US2)**: Depends on Phase 3 (`MergedConfig.RuleContributors` is populated by Phase 3)
- **Phase 5 (Polish)**: Depends on Phase 4 (all features must be implemented before snapshot regeneration)

### User Story Dependencies

- **US1 (P1)**: Independent of US2 at the *behavior* level — rule ordering is correct after Phase 3 alone, even though comments will be visually wrong until Phase 4. The integration test TC-I-005-01 validates US1 against `mc.Rules` positions, NOT YAML comment text.
- **US2 (P2)**: Strict dependency on US1 — `normalizeRulesPriorityComments` reads `mc.RuleContributors`, which is populated by `Pipeline.Build()` in T009.

### Within Each User Story

- Tests MUST be written and FAIL before implementation (Constitution Principle IV)
- T004 (merge tests) before T005–T007 (merge implementation + cleanup)
- T011 (output tests) before T012–T013 (output implementation)
- Integration tests (T010, T014, T015) land alongside their stories; they are the independent-test criteria

### Parallel Opportunities

```text
Phase 1: T001 [P] ── T002 [P] ── T003 [P]   (all three are read-only on different files)

Phase 3 (US1): T004 ── T005 ── T006 ── T007 ── T008 ── T009 ── T010
                                                      (sequential — same file
                                                       and dependency chain)

Phase 4 (US2): T011 ── T012 ── T013 ── T014 ── T015
                                                      (sequential — same files)

Phase 5: T016 ── T017 ── T018
```

There are no cross-phase parallelism opportunities for a single implementer, but Phase 3 and Phase 4 *could* be split between two implementers if both branches reconverge before Phase 5.

---

## Implementation Strategy

### MVP (User Story 1 alone)

If shipped without US2, the served YAML would carry the new rule order but with the old `# --- upstream ---` divider misplaced or absent depending on how `normalizeRulesComments` reacts to non-zero priorities everywhere. This is a transiently broken comment surface that does not affect routing behavior. Operators who rely on rule order would still benefit; operators who rely on comments would see degraded output.

**Recommendation**: do NOT ship US1 alone. Ship US1 + US2 together as one increment. The phase split exists for TDD ordering and tasks-file readability, not as an intentional release boundary.

### Full delivery (US1 + US2)

1. Phase 1: orient (3 read tasks)
2. Phase 3: merge-layer change + integration test TC-I-005-01
3. Phase 4: output-adapter change + integration tests TC-I-005-02 and TC-I-005-03
4. Phase 5: snapshot regen, `make check`, commit
5. Restart the live tmux server with custom rules configured; verify quickstart.md's curl-grep check passes

---

## Notes

- Constitution Principle IV (NON-NEGOTIABLE): tests land before implementation. Each user story phase lists tests first.
- Constitution Principle II (Determinism): SC-004 requires byte-identical 100-fetch behavior; TC-I-005-03 validates it.
- Snapshot regeneration in T016 is a deliberate, reviewable action per Constitution's Snapshot Stability Gate. The diff includes both rule reordering and comment-format change; the PR description must justify both.
- Per CLAUDE.md guidance, the deletions in T007 remove dead code rather than keep backward-compatibility shims (no callers exist outside of the deleted tests).
- The `cmd/server/main.go` wiring is untouched — `Pipeline.WithCustomRules` already accepts the custom rule sets; only the internal merge function changes.
