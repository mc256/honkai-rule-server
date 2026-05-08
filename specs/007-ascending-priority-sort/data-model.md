# Phase 1 Data Model: Ascending Priority Sort

**Feature**: [spec.md](./spec.md) | **Plan**: [plan.md](./plan.md) | **Date**: 2026-05-01

## Overview

This feature introduces no new data types, no struct field changes, no
schema changes, and no new persistence. It is a single comparator flip
inside `MergeUnifiedRules`. The shapes of `Contributor`, `MergeResult`,
and `MergedConfig` (defined in feature 005) all remain exactly as they
are.

## What changes

The *order* in which contributors appear inside the function's local
`contributors []contributor` slice is reversed. As a consequence, the
*order* of entries in the three parallel slices `MergeResult.Rules`,
`MergeResult.Priorities`, `MergeResult.Contributors` (and therefore in
`MergedConfig.Rules`, `RulePriorities`, `RuleContributors`) is reversed.

| Slice | Length | Element invariant pre-007 | Element invariant post-007 |
|---|---|---|---|
| `Priorities` | N | non-strictly **descending** until the trailing 0 | non-strictly **ascending** until the trailing 0 |
| `Contributors` | N | parallel-aligned with `Priorities` | parallel-aligned with `Priorities` |
| `Rules` | N | parallel-aligned | parallel-aligned |

The MATCH fallback continues to be the last entry: `Priorities[N-1] == 0`
and `Contributors[N-1] == ""`. This is unchanged because MATCH is
appended after the comparator runs (see research D2).

## What does NOT change

- Field names, field types, struct shapes, public API.
- The contract of `MergeUnifiedRules` (function signature unchanged).
- Tie-break semantics: alphabetical ascending by `Name` for contributors
  sharing a priority value.
- Any of feature 005's other invariants (FR-002 single number-line, FR-005
  header comment format, FR-008 MATCH-last, FR-010 determinism).
- The `Style`, `HeadComment`, or any other YAML node attributes set by the
  output adapter.
- Snapshot fixture format (only its rule-section content changes).

## Validation rules

Same as feature 005. The function does not introduce new validation:
- Non-negative priorities (validated at load time by `internal/customrules/loader.go`).
- Non-empty contributor names (validated at load time for both upstream
  and custom contributors).

The comparator is total and stable for any non-negative integer
priority and any non-empty string name. No new degenerate cases.

## Determinism

The replacement comparator `(Priority asc, Name asc)` is a strict weak
ordering: irreflexive, asymmetric, transitive. `sort.SliceStable` with
this comparator produces a unique ordering for any given input — the
same property feature 005 had with descending. SC-002 (100×
byte-identical fetches) holds.
