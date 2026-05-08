# Implementation Plan: Ascending Priority Sort

**Branch**: `007-ascending-priority-sort` | **Date**: 2026-05-01 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/007-ascending-priority-sort/spec.md`

## Summary

Reverse feature 005's rule sort direction from descending to ascending. The implementation is a single comparator flip in `internal/merge/rules.go` plus assertion updates in 7 affected tests. No struct changes, no new files, no behavior change outside the rule-merge sort key. Proxy-collision resolution (`sortSourcesByPriority` in `pipeline.go`) keeps its existing descending semantic — it governs *collision tie-breaks*, not *routing precedence*, and this feature is scoped to the latter.

## Technical Context

**Language/Version**: Go 1.25 (declared 1.22+)
**Primary Dependencies**: `gopkg.in/yaml.v3` (unchanged), stdlib `sort`
**Storage**: N/A (stateless transformation)
**Testing**: Go `testing`, `bradleyjkemp/cupaloy/v2` for snapshots
**Target Platform**: Linux server (containerized)
**Project Type**: Web service (subscription aggregator)
**Performance Goals**: Identical to today (sort.SliceStable is O(N log N) on a small slice; comparator change is one CPU op)
**Constraints**: Determinism preserved (SC-002); no semantic change to non-rule fields
**Scale/Scope**: ~12 contributors typical; sort cost is negligible

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

### Principle I: Unified Transformation Core
✅ **PASS** — Change is one line in the merge core; output adapter unchanged. Single pipeline.

### Principle II: Deterministic Transformation
✅ **PASS** — Comparator remains total and stable: `(Priority asc, Name asc)` is a strict weak ordering with deterministic tie-break. Two requests against the same input still produce byte-identical output.

### Principle III: CSV Rules — Strict Schema, Loud Failure
✅ **N/A** — No CSV-related changes.

### Principle IV: Test-First, Real-Input Integration (NON-NEGOTIABLE)
✅ **PASS** — Existing TC-U-MERGE-UNIFIED-* tests are updated with new expected values *before* the comparator flip; tests then fail (red); comparator flip makes them pass (green). Integration tests TestI_005_01, TestI_005_02, TestI_003_01 are updated similarly.

### Principle V: Observable Routing & Source-Merge Decisions
✅ **PASS** — No log changes. The behavior change *is* observable in the served YAML (rule-block order is reversed), which matches operator intent.

### Routing & Security Constraints
✅ **PASS** — No security surface change. Routing semantics: now correctly match operator priority intuition (lower number = matches first).

**Re-check after Phase 1 design**: ✅ All gates still pass.

## Project Structure

### Documentation (this feature)

```text
specs/007-ascending-priority-sort/
├── plan.md              # This file
├── research.md          # Phase 0: scope decisions (rule sort vs. proxy collision sort)
├── data-model.md        # Empty — no entity or struct changes
├── quickstart.md        # Operator-facing before/after diff
└── tasks.md             # Phase 2 output (created by /speckit-tasks)
```

### Source Code (repository root)

Files touched:

```text
internal/merge/
├── rules.go                      # ONE LINE CHANGE: line 82, flip > to < in comparator
│                                  # Plus doc-comment update at line 36 (desc → asc)
└── rules_test.go                 # 6 tests: flip wantPrios/wantContribs arrays for
                                   # TC-U-MERGE-UNIFIED-01..06
internal/integration/
├── pipeline_test.go              # 3 tests: flip position assertions
│                                  # - TestI_005_01_UnifiedPriorityOrder
│                                  # - TestI_005_02_PriorityBucketHeaderComments (add order assert)
│                                  # - TestI_003_01_CustomRulesInOutput (flip upstream<custom)
└── testdata/snapshots/
    └── served-config.snap.yaml   # Regenerate; rule-block order reverses;
                                   # priority-header comments reorder; rule strings unchanged
```

Files NOT touched:

- `internal/merge/pipeline.go` — `sortSourcesByPriority` keeps descending (proxy collision tie-break, not routing precedence). Documented in research D1.
- `internal/merge/proxies.go`, `proxy_groups.go` — collision-resolution behavior unchanged.
- `internal/output/subscription_mode.go` — output adapter reads priorities in whatever order the merge gives it; no logic change needed.
- `cmd/server/main.go` — no wiring change.

**Structure Decision**: Surgical comparator flip + assertion updates. No new packages, no new files, no API changes.

## Complexity Tracking

No constitution violations. The implementation is the smallest possible change at the merge layer; the test surface is the smallest set of assertions that depend on rule order.

The intentional kept-descending behavior of `sortSourcesByPriority` (proxy collision) is a *not-touched* surface, not a justified violation. The two sorts serve different purposes (routing precedence vs. proxy collision tie-break), so they reasonably diverge.

---

## Phase 0: Research

Resolved in `research.md`. Key decisions:

| Question | Decision | Rationale |
|---|---|---|
| Should `sortSourcesByPriority` (in pipeline.go) also flip to ascending? | **No.** That sort governs proxy/group collision tie-breaks (existing 002 behavior), not rule routing precedence. The user's request is scoped to rule order. | Different semantic domain; flipping would change collision behavior without operator request and break existing 002 expectations. |
| Should the MATCH fallback's priority value (currently `0`) change? | **No.** MATCH is appended after sorting in the merge code (`rules.go:108`); its priority value is a metadata sentinel for the output adapter, not a sort key. Ascending sort has no impact on its position. | Already implemented correctly; MATCH stays last by sequence, not by sort. |
| What about contributor name tie-break? | **Keep alphabetical ascending.** | Spec FR-002 explicitly preserves it; alphabetical is intuitive and deterministic. |
| Can features 006 and 007 land in either order? | **Yes — independent.** 006 touches `internal/output/subscription_mode.go` (byte transform); 007 touches `internal/merge/rules.go` (sort comparator). Different files, different layers. | Confirmed via file-touch audit. |
| Does any caller depend on the current descending order beyond the listed tests? | **No.** `MergedConfig.Rules` is consumed by `internal/output/subscription_mode.go` (which iterates in sequence — order-agnostic) and the integration test fixtures listed above. No production code branches on priority value. | Confirmed via grep audit (see research.md). |

## Phase 1: Design & Contracts

### Data Model

See `data-model.md`. No new entities. No struct field changes. The `MergeResult.Priorities` and `MergeResult.Contributors` slices stay parallel to `Rules`; only the order in which contributors are walked into them changes.

### Key Interfaces

**The single change in `internal/merge/rules.go`**:

```go
// Before (feature 005 descending):
sort.SliceStable(contributors, func(i, j int) bool {
    if contributors[i].Priority != contributors[j].Priority {
        return contributors[i].Priority > contributors[j].Priority   // DESC
    }
    return contributors[i].Name < contributors[j].Name
})

// After (feature 007 ascending):
sort.SliceStable(contributors, func(i, j int) bool {
    if contributors[i].Priority != contributors[j].Priority {
        return contributors[i].Priority < contributors[j].Priority   // ASC
    }
    return contributors[i].Name < contributors[j].Name
})
```

Doc-comment update (line 36):

```go
// Before:
// at its declared priority. Sort key: (Priority desc, Name asc).
// After:
// at its declared priority. Sort key: (Priority asc, Name asc).
```

### Test Updates Inventory

**`internal/merge/rules_test.go`** — flip expected arrays for 6 tests:

| Test | Old wantPrios | New wantPrios | Old wantContribs | New wantContribs |
|---|---|---|---|---|
| TC-U-MERGE-UNIFIED-01 | `[2000, 1000, 1000, 0]` | `[1000, 1000, 2000, 0]` | `[beta, alpha, alpha, ""]` | `[alpha, alpha, beta, ""]` |
| TC-U-MERGE-UNIFIED-02 | `[1500, 1500, 300, 0]` | `[300, 1500, 1500, 0]` | `[high, high, low, ""]` | `[low, high, high, ""]` |
| TC-U-MERGE-UNIFIED-03 | `[2000, 1000, 0]` | `[1000, 2000, 0]` | `[corporate, alpha, ""]` | `[alpha, corporate, ""]` |
| TC-U-MERGE-UNIFIED-04 | `[2000, 1000, 0]` | `[1000, 2000, 0]` | `[beta, corporate, ""]` | `[corporate, beta, ""]` |
| TC-U-MERGE-UNIFIED-05 | `[1000, 1000, 1000, 0]` | unchanged (all same priority; alphabetical tie-break preserves order) | unchanged | unchanged |
| TC-U-MERGE-UNIFIED-06 | `[1000, 500, 0]` | `[500, 1000, 0]` | `[normal, real, ""]` | `[real, normal, ""]` |

`Rules` arrays for each test must also be reordered to match the new contributor order.

**`internal/integration/pipeline_test.go`** — 3 tests:

| Test | Change |
|---|---|
| `TestI_005_01_UnifiedPriorityOrder` | Position invariant flips: `highUpstream < highCustom < lowUpstream < lowCustom < match` becomes `lowCustom < lowUpstream < highCustom < highUpstream < match`. The 5-rule scenario stays valid; only the expected order reverses. |
| `TestI_005_02_PriorityBucketHeaderComments` | Currently checks for substring presence of both `# --- priority 2000 (beta) ---` and `# --- priority 1000 (corporate) ---` without asserting order. Add an explicit ordering assertion: `index(priority 1000 header) < index(priority 2000 header)`. This makes the ascending-direction guarantee testable in the rendered YAML. |
| `TestI_003_01_CustomRulesInOutput` | Position assertion `upstreamIdx < customRejectIdx && customRejectIdx < matchIdx` becomes `customRejectIdx < upstreamIdx && upstreamIdx < matchIdx` (custom priority 500 < upstream priority 1000 means custom emits first under ascending). Priority *value* assertions (`mc.RulePriorities[upstreamIdx] != 1000`) are unaffected — those test the metadata at a position, not the position itself. |

**Snapshot regeneration**: `internal/integration/testdata/snapshots/served-config.snap.yaml` is regenerated with `UPDATE_SNAPSHOTS=true`. The diff is contained: rule strings, proxy entries, group entries unchanged; the `rules:` block is reordered by priority bucket; the `# --- priority N (...) ---` headers reappear in ascending order.

### Quickstart

See `quickstart.md` for operator-facing before/after.

### Agent Context

`CLAUDE.md` updated to add 007 under "Key reading" and update the active-feature pointer.

---

## Phase 2 (preview)

`/speckit-tasks` will produce ~7 tasks:

- **Phase 1 (Setup)**: empty — the implementation is so localized that no orientation reads are required beyond what's already in the plan.
- **Phase 2 (Foundational)**: empty — no shared infrastructure.
- **Phase 3 (US1 — Ascending Priority Sort)**:
  1. Update TC-U-MERGE-UNIFIED-01..06 expected arrays per the inventory table; verify they fail.
  2. Update doc comment on line 36 (`desc` → `asc`).
  3. Flip comparator on line 82 (`>` → `<`); verify TC-U-MERGE-UNIFIED-* now pass.
  4. Update TestI_003_01 position assertion.
  5. Update TestI_005_01 position assertion.
  6. Update TestI_005_02 — add explicit ordering assertion for priority-bucket header substrings.
- **Phase 4 (Polish)**:
  7. Regenerate snapshot, run `make check`, mark tasks complete.
