# Tasks: YAML Output Formatting

**Input**: Design documents from `/specs/004-yaml-output-formatting/`
**Prerequisites**: plan.md (required), spec.md (required), research.md

**Tests**: Per Constitution Principle IV (NON-NEGOTIABLE), every unit test MUST be committed before the implementation it validates. Test tasks are explicitly included and must land first within each user story phase.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (US1, US2, US3)
- Include exact file paths in descriptions

## Path Conventions

- `internal/output/subscription_mode.go` — YAML output normalization (block style, field ordering, comments)
- `internal/output/subscription_mode_test.go` — Unit tests for formatting
- `internal/merge/rules.go` — Priority metadata extension
- `internal/merge/rules_test.go` — Tests for priority tracking
- `internal/integration/testdata/snapshots/` — Integration snapshots

---

## Phase 1: Setup

**Purpose**: No new packages needed; verify existing code structure and prepare for test-first development

- [x] T001 Read `internal/output/subscription_mode.go` to understand current `Render()` flow and existing `normalizeProxyStyle()` implementation; identify where to add new normalization functions
- [x] T002 [P] Read `internal/merge/rules.go` to understand current `MergeCustomRules()` signature and implementation; identify extension point for priority metadata

**Checkpoint**: Code structure understood; ready for test-first development

---

## Phase 2: Foundational — Priority Metadata Infrastructure

**Purpose**: Extend merge layer to return priority information needed for comment insertion (US3 dependency)

### Tests for Phase 2 (write FIRST, verify they FAIL)

- [x] T003 Write TC-U-RULES-PRIORITY-01 through TC-U-RULES-PRIORITY-04 tests in `internal/merge/rules_test.go`: test `MergeCustomRulesWithPriorities` returns correct parallel priority array for upstream-only rules (all 0), single custom rule set (all same priority), multiple custom rule sets at different priorities (priority changes at boundaries), mixed upstream + custom (0 for upstream, priority for custom)

### Implementation for Phase 2

- [x] T004 Implement `MergeResult` struct and `MergeCustomRulesWithPriorities()` function in `internal/merge/rules.go`: returns `MergeResult{Rules []string, Priorities []int}` where Priorities is parallel array with 0 for upstream rules and actual priority value for custom rules; reuse existing sorting logic from `MergeCustomRules`

**Checkpoint**: Priority metadata available for comment insertion — US3 can proceed

---

## Phase 3: User Story 1 — Proxy-Groups Block Format (Priority: P1) MVP

**Goal**: All proxy-groups render in readable multi-line block format (not flow/folded `{...}`)

**Independent Test**: Serve a config with upstream proxy-groups in flow style; verify rendered output has all proxy-groups in block format (each field on separate line)

### Tests for User Story 1 (write FIRST, verify they FAIL)

- [x] T005 [P] Write TC-U-OUTPUT-BLOCK-01 through TC-U-OUTPUT-BLOCK-04 tests in `internal/output/subscription_mode_test.go`: test proxy-group from upstream with flow style becomes block style, proxy-group already in block style stays block style, proxy-group with nested mapping (ws-opts) has outer block but nested stays natural, multiple proxy-groups all become block style

### Implementation for User Story 1

- [x] T006 Implement `normalizeProxyGroupStyle()` in `internal/output/subscription_mode.go`: find `proxy-groups` sequence in root; for each group, set `node.Style = 0` (block style) on the mapping node; do NOT recursively set style on nested nodes; call this in `Render()` after `normalizeProxyStyle()` and before `resetScalarStyles()`

**Checkpoint**: US1 complete — proxy-groups render in readable block format

---

## Phase 4: User Story 2 — Field Ordering in Proxy-Groups (Priority: P2)

**Goal**: First three fields of each proxy-group appear in order: `name`, `type`, `proxies`

**Independent Test**: Serve a config with proxy-group having fields in arbitrary order; verify rendered output has `name` first, `type` second, `proxies` third

### Tests for User Story 2 (write FIRST, verify they FAIL)

- [x] T007 [P] Write TC-U-OUTPUT-ORDER-01 through TC-U-OUTPUT-ORDER-04 tests in `internal/output/subscription_mode_test.go`: test proxy-group with fields in random order gets reordered (name, type, proxies first), proxy-group missing `proxies` field gets name and type first only, proxy-group with additional fields beyond proxies preserves their relative order, proxy-group already in correct order stays unchanged

### Implementation for User Story 2

- [x] T008 Extend `normalizeProxyGroupStyle()` in `internal/output/subscription_mode.go` to reorder fields: for each proxy-group mapping node, find `name` key-value pair and swap to position 0-1, find `type` pair and swap to position 2-3, find `proxies` pair and swap to position 4-5; if any field missing, skip that reorder step; preserve remaining fields after position 5 in original order

**Checkpoint**: US2 complete — proxy-groups have consistent field ordering

---

## Phase 5: User Story 3 — Priority Comments in Rules (Priority: P3)

**Goal**: Comments mark priority boundaries in rules section: `# --- upstream ---` before upstream rules, `# --- priority N ---` before each custom rule priority group

**Independent Test**: Serve a config with custom rules at priorities 100, 500, 1000; verify comments appear before each priority group's first rule

**Dependency**: Requires Phase 2 (T004 — `MergeCustomRulesWithPriorities`)

### Tests for User Story 3 (write FIRST, verify they FAIL)

- [x] T009 Write TC-U-OUTPUT-COMMENT-01 through TC-U-OUTPUT-COMMENT-05 tests in `internal/output/subscription_mode_test.go`: test upstream-only rules get `# --- upstream ---` comment on first rule, single custom priority gets `# --- priority N ---` comment before custom rules, multiple custom priorities get comments at each boundary, no rules produces empty rules section with no comments, verify comment appears on its own line before rule (HeadComment)

### Implementation for User Story 3

- [x] T010 Implement `normalizeRulesComments(root *yaml.Node, priorities []int)` in `internal/output/subscription_mode.go`: find `rules` sequence; iterate with priority metadata; set `HeadComment = "# --- upstream ---"` on first rule if first priority is 0; for each priority boundary (where priorities[i] != priorities[i-1]), set `HeadComment = "# --- priority N ---"` on the rule at that index; call in `Render()` with priorities from pipeline
- [x] T011 Thread priority metadata through `Render()` in `internal/output/subscription_mode.go`: `Render` signature unchanged but internally needs priorities; extend `MergedConfig` struct in `internal/merge/pipeline.go` to include `RulePriorities []int` field; populate it in `Pipeline.Build()` by calling `MergeCustomRulesWithPriorities` instead of `MergeCustomRules` when custom rules present

**Checkpoint**: US3 complete — rules section has priority boundary comments

---

## Phase 6: Integration Tests & Polish

**Purpose**: End-to-end validation, snapshot regeneration, final `make check`

- [x] T012 Regenerate integration snapshots: run `UPDATE_SNAPSHOTS=true go test ./internal/integration/...` to update `served-config.snap.yaml` with new proxy-group formatting and rule comments; manually verify snapshot content (proxy-groups are block style, fields ordered, priority comments present if custom rules in fixture)
- [x] T013 Run `make check` and verify all tests pass (`go vet`, `staticcheck`, full test suite, snapshot drift check); fix any issues found
- [x] T014 Commit all changes and verify `git diff --exit-code` is clean after `make check`

**Checkpoint**: All features complete, tested, and passing CI

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies — start immediately (read-only)
- **Foundational (Phase 2)**: No dependencies — can run in parallel with Phase 1
- **US1 Block Format (Phase 3)**: No dependencies — starts after Phase 1
- **US2 Field Ordering (Phase 4)**: Depends on US1 (modifies same function) — sequential after US1
- **US3 Comments (Phase 5)**: Depends on Phase 2 (priority metadata) — can run parallel with US1/US2 if Phase 2 complete
- **Integration (Phase 6)**: Depends on ALL user stories — runs last

### User Story Dependencies

- **US1 (P1)**: Independent — starts immediately after setup
- **US2 (P2)**: Depends on US1 (both modify `normalizeProxyGroupStyle`) — sequential
- **US3 (P3)**: Depends on Phase 2 (priority metadata) — can run parallel with US1/US2

### Within Each User Story

- Tests MUST be written and FAIL before implementation (Constitution Principle IV)
- Implementation tests the normalization logic
- Integration validates end-to-end

### Parallel Opportunities

```
Phase 1: T001 ──┬── T002 [P] ──┬── Phase 2/3
               └──             ┘

Phase 2: T003 ── T004

Phase 3: T005 [P] ── T006 ──┬── Phase 5 (if Phase 2 done)
                            └── Phase 4 (US2)

Phase 4: T007 [P] ── T008 ──┬── Phase 5
                            └── Phase 6

Phase 5: T009 ── T010 ── T011

Phase 6: T012 ── T013 ── T014
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1: Setup (read code structure)
2. Complete Phase 3: US1 (block format)
3. **STOP and VALIDATE**: Proxy-groups render in readable block format
4. Deploy if ready — immediate readability improvement

### Incremental Delivery

1. Setup → Code structure understood
2. Phase 2 → Priority metadata ready (infrastructure for US3)
3. US1 → Block format → Deploy (MVP!)
4. US2 → Field ordering → Deploy
5. US3 → Priority comments → Deploy
6. Integration → Snapshots → Final CI pass

---

## Notes

- Constitution Principle IV requires tests land BEFORE implementation — each phase lists tests first
- US2 modifies the same function as US1 (`normalizeProxyGroupStyle`) — must be sequential
- US3 requires extending `MergedConfig` struct — affects pipeline but not merge core
- Proxy flow style from feature 003 must remain unchanged (FR-007)
- Snapshot regeneration requires manual review of generated output