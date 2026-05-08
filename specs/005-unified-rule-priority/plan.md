# Implementation Plan: Unified Rule Priority

**Branch**: `005-unified-rule-priority` | **Date**: 2026-05-01 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/005-unified-rule-priority/spec.md`

## Summary

Merge upstream-source rules and custom-rule-set rules into a single descending-priority stream so that priority is comparable across both contributor kinds. Replace the current `# --- upstream ---` divider and per-priority custom-rule dividers with one priority-bucket header comment per bucket, formatted `# --- priority N (contributor-list) ---`. This corrects the sort-direction inconsistency between feature 002 (upstream sources sorted descending — z-index style, FR-005a) and feature 003 (custom rules sorted ascending) by adopting a single descending sort. Touches `internal/merge/` and `internal/output/`; no new packages.

## Technical Context

**Language/Version**: Go 1.25 (declared 1.22+)
**Primary Dependencies**: `gopkg.in/yaml.v3` (YAML node tree manipulation), `log/slog`, `golang.org/x/sync/singleflight`
**Storage**: N/A (stateless transformation)
**Testing**: Go `testing` package, `bradleyjkemp/cupaloy/v2` for snapshots
**Target Platform**: Linux server (containerized)
**Project Type**: Web service (subscription aggregator)
**Performance Goals**: O(N + M) merge where N = total upstream rules and M = total custom rules; no measurable change vs. today
**Constraints**: Output must remain valid YAML; byte-identical determinism preserved (SC-004)
**Scale/Scope**: typical config has 2–4 upstream sources, 5–20 custom rule sets, ~1500–2500 rules total

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

### Principle I: Unified Transformation Core
✅ **PASS** — All changes are inside the merge core (`internal/merge/`) and output adapter (`internal/output/`). Single pipeline; both delivery modes (subscription + override-mode placeholder) consume the same `MergeResult`.

### Principle II: Deterministic Transformation
✅ **PASS** — Sort by (priority desc, name asc) is total and stable. Tie-break is alphabetical Unicode codepoint order. No map iteration, no time.Now(), no rand. SC-004 mandates 100×byte-identical fetches; existing determinism integration test continues to apply.

### Principle III: CSV Rules — Strict Schema, Loud Failure
✅ **N/A** — This feature does not touch CSV parsing. Existing strict validation in `internal/config/subscriptions.go` (priority field) and existing custom-rules loader (negative priority rejection) are unchanged.

### Principle IV: Test-First, Real-Input Integration (NON-NEGOTIABLE)
✅ **PASS** — Plan front-loads unit tests for the new merge function and output normalizer (TDD per Constitution). Integration test required: TC-I-005-01 covers interleaved upstream + custom priority order; TC-I-005-02 covers contributor-list comment format; TC-I-005-03 covers determinism. Integration snapshot in `internal/integration/testdata/snapshots/served-config.snap.yaml` will be regenerated with manual review.

### Principle V: Observable Routing & Source-Merge Decisions
✅ **PASS** — Merge decisions become more visible (header comments name contributors per bucket). No new log fields required; the served YAML itself satisfies the operator-observability ask in spec SC-003. Existing `served subscription` log line remains unchanged.

### Routing & Security Constraints
✅ **PASS** — No routing-policy change (corporate isolation, multi-subscription mixing strategies remain). No new secrets surface; comments contain contributor *names* (already non-sensitive, derived from `subscriptions.csv` `name` column and custom-rule YAML `name` field).

**Re-check after Phase 1 design** (post Phase 1 below): ✅ All gates still pass — no scope creep.

## Project Structure

### Documentation (this feature)

```text
specs/005-unified-rule-priority/
├── plan.md              # This file
├── research.md          # Phase 0: design decisions and alternatives rejected
├── data-model.md        # Phase 1: Contributor + MergeResult shape
├── quickstart.md        # Phase 1: operator guide (what changes in served YAML)
└── tasks.md             # Phase 2 output (created by /speckit-tasks)
```

### Source Code (repository root)

```text
internal/
├── merge/
│   ├── rules.go                # NEW MergeUnifiedRules; existing MergeCustomRulesWithPriorities deprecated/removed
│   ├── rules_test.go           # New unit tests TC-U-MERGE-UNIFIED-01..06
│   └── pipeline.go             # Build() switches to MergeUnifiedRules; MergedConfig adds RuleContributors []string
├── output/
│   ├── subscription_mode.go    # normalizeRulesPriorityComments replaces normalizeRulesComments
│   └── subscription_mode_test.go # New tests TC-U-OUTPUT-PRIORITY-01..05
└── integration/
    ├── pipeline_test.go         # Add TC-I-005-01, TC-I-005-02, TC-I-005-03
    └── testdata/snapshots/
        └── served-config.snap.yaml  # Regenerate with new formatting
```

**Structure Decision**: Changes are localized to two existing packages (`internal/merge/`, `internal/output/`). No new packages, no new public API surface beyond the renamed merge function. The pipeline glue in `cmd/server/main.go` does not change — `Pipeline.WithCustomRules` already takes the custom rule sets and the subscriptions are already passed in via `NewPipeline`.

## Complexity Tracking

No constitution violations. All changes are within existing principles.

The intentional behavior change — rule order and comment format differ from today — is justified by the spec (it is the *point* of the feature). Snapshot diff is required and reviewable per Constitution's Snapshot Stability Gate.

---

## Phase 0: Research

Resolved in `research.md`. Summary of decisions:

| Question | Decision | Rationale |
|---|---|---|
| Single merge function or extend existing one? | New `MergeUnifiedRules`; remove `MergeCustomRulesWithPriorities`. | The old function's "0 means upstream" sentinel cannot represent per-rule contributor names. Cleaner to start fresh than to bolt on a parallel string array to a function whose name lies. |
| Backwards compatibility shim for `MergeCustomRules` and `MergeCustomRulesWithPriorities`? | None. Delete both. | Internal API; only `Pipeline.Build()` calls them. Per CLAUDE.md guidance: delete dead code rather than keep unused exports. |
| Carry contributor names parallel to rules, or as bucket-keyed maps? | Parallel `[]string` alongside Rules and Priorities. | Output adapter walks rules in order; parallel arrays make boundary detection a single index comparison. Map keyed by priority would require a second iteration order decision. |
| How to format the contributor list when multiple share a priority? | Comma-separated alphabetical: `(name-a, name-b)`. | Matches spec FR-005 verbatim; alphabetical is a natural and deterministic order. |
| Do we keep `# --- upstream ---` for back-compat? | No, remove. | Spec FR-009 is explicit; replaced by per-priority headers. |
| What about `mc.RulePriorities`? Does it stay int? | Stay int (priority value). Add `mc.RuleContributors []string` parallel. | Minimal struct change; existing 004 normalizer already understands priority transitions. |

## Phase 1: Design & Contracts

### Data Model

See `data-model.md`. Key change: introduce `Contributor` as a transient struct in `internal/merge/rules.go`:

```go
// Contributor unifies upstream sources and custom rule sets for priority-merge.
// Constructed inside MergeUnifiedRules; not exported for caller use.
type contributor struct {
    Name     string   // source name (e.g., "beta") or custom set name
    Priority int      // descending sort key
    Rules    []string // already trailing-dropped for upstream; verbatim for custom
}
```

`MergeResult` extends to carry per-rule contributor name:

```go
type MergeResult struct {
    Rules        []string
    Priorities   []int
    Contributors []string  // NEW — parallel; "" for the MATCH fallback
}
```

`MergedConfig` (in `pipeline.go`) gains:

```go
type MergedConfig struct {
    // ... existing fields ...
    RuleContributors []string  // NEW — parallel to Rules and RulePriorities
}
```

### Key Interfaces

**Single new merge function** (replaces `MergeCustomRules` and `MergeCustomRulesWithPriorities`):

```go
// MergeUnifiedRules merges upstream rules and custom rule sets into a single
// priority-descending stream. Each upstream source contributes its rules
// (with the trailing rule dropped per FR-008 of feature 002) at the priority
// declared in subscriptions.csv. Each custom rule set contributes its rules
// at its declared priority. Sort key: (Priority desc, Name asc).
//
// Returns rules + parallel priority + parallel contributor-name arrays.
// The MATCH,<fallback> rule is always last with priority 0 and contributor "".
func MergeUnifiedRules(
    upstreamPerSource map[string][]string,
    upstreamSources []config.SubscriptionRow, // priority + name; pre-sorted is fine
    customs []customrules.CustomRuleSet,
    fallbackRuleTarget string,
) MergeResult
```

**Output adapter normalizer** (replaces `normalizeRulesComments`):

```go
// normalizeRulesPriorityComments attaches one head comment per priority bucket
// to the rules sequence in the served YAML. Comment format:
//   "# --- priority N (contributor-a, contributor-b) ---"
// Boundary detection: priorities[i] != priorities[i-1] (or i == 0).
// MATCH fallback (priority 0, contributor "") gets no head comment.
func normalizeRulesPriorityComments(root *yaml.Node, priorities []int, contributors []string)
```

### Quickstart

See `quickstart.md` for operator-facing diff with examples. Key visible changes:

- `# --- upstream ---` is gone.
- Each priority bucket now has its own header comment.
- Custom rules with priority higher than an upstream source's priority now appear *before* that upstream source's rules.

### Agent Context

`CLAUDE.md` updated to add the 005 feature line under "Key reading" and bump the active-feature pointer.

---

## Phase 2 (preview)

`/speckit-tasks` will produce `tasks.md` with this rough phasing:

- **Phase 1 (Setup)**: read existing merge/output code; identify deletion targets (`MergeCustomRules`, `MergeCustomRulesWithPriorities`).
- **Phase 2 (Foundational — TDD merge layer)**: write `MergeUnifiedRules` tests TC-U-MERGE-UNIFIED-01..06 first, then implementation, then update `Pipeline.Build()` and `MergedConfig`. Snapshot tests will fail at this point (rule order changed) — expected.
- **Phase 3 (US1 verification — output adapter still works)**: existing rule order tests should pass; integration test TC-I-005-01 added.
- **Phase 4 (US2 — header comments)**: write `normalizeRulesPriorityComments` tests TC-U-OUTPUT-PRIORITY-01..05 first, then implementation; integration tests TC-I-005-02 + TC-I-005-03 added.
- **Phase 5 (Polish)**: regenerate `served-config.snap.yaml` with `UPDATE_SNAPSHOTS=true`, manual review of the diff (custom-rule reordering + comment-format change), `make check`, commit.
