# Implementation Plan: Preserve Emoji in Served YAML

**Branch**: `006-fix-emoji-yaml-escape` | **Date**: 2026-05-01 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/006-fix-emoji-yaml-escape/spec.md`

## Summary

`gopkg.in/yaml.v3`'s emitter unconditionally escapes every Unicode code point above U+FFFF as `\Uxxxxxxxx`, regardless of the node's `Style`. This produces unreadable output like `"alpha_\U0001F530国外流量"` for proxy names that should appear as `alpha_🔰国外流量`. The fix is a single new helper, `unescapeSupplementaryPlane(body []byte) []byte`, applied to the encoded YAML inside `Render()` after `enc.Close()` and before returning. The helper walks the byte stream, recognizes `\Uxxxxxxxx` escapes inside double-quoted strings, and replaces them with literal UTF-8 bytes — leaving every other byte (control-character escapes, literal backslashes in operator-supplied content, all non-double-quoted content) untouched.

## Technical Context

**Language/Version**: Go 1.25 (declared 1.22+)
**Primary Dependencies**: `gopkg.in/yaml.v3` (encoder, unchanged), stdlib `unicode/utf8`, stdlib `strconv`
**Storage**: N/A (stateless transformation)
**Testing**: Go `testing`, `bradleyjkemp/cupaloy/v2` for snapshots
**Target Platform**: Linux server (containerized)
**Project Type**: Web service (subscription aggregator)
**Performance Goals**: O(N) in body size; ~175 KB typical body, transform runs in microseconds; no measurable change vs. current `Render()` cost
**Constraints**: Output must remain byte-stable across requests (SC-004); round-trip parse must yield identical strings (SC-005); no semantic change to routing (FR-007)
**Scale/Scope**: ~759 escape occurrences in current production body; transform is a single linear scan

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

### Principle I: Unified Transformation Core
✅ **PASS** — All changes inside the output adapter (`internal/output/`). Single transformation pipeline; future override-mode adapter would apply the same helper at its own emit boundary.

### Principle II: Deterministic Transformation
✅ **PASS** — Byte transform is purely functional: same input bytes → same output bytes. No randomness, no time, no map iteration. SC-004 (100× byte-identical) preserved.

### Principle III: CSV Rules — Strict Schema, Loud Failure
✅ **N/A** — No CSV-related changes.

### Principle IV: Test-First, Real-Input Integration (NON-NEGOTIABLE)
✅ **PASS** — Plan front-loads unit tests (TDD) for the helper before its implementation. Round-trip integration test (TC-I-006-01) validates that yaml-parsing the post-fix body returns string values byte-equal to the input MergedConfig — the round-trip property is the load-bearing safety check.

### Principle V: Observable Routing & Source-Merge Decisions
✅ **PASS** — No new log fields; no behavior change visible in logs. The user-facing observability win is in the served YAML itself (operators can read it).

### Routing & Security Constraints
✅ **PASS** — No routing change. No secrets change (the helper only operates on bytes, doesn't read auth material). No new API surface.

**Re-check after Phase 1 design** (post Phase 1 below): ✅ All gates still pass.

## Project Structure

### Documentation (this feature)

```text
specs/006-fix-emoji-yaml-escape/
├── plan.md              # This file
├── research.md          # Phase 0: experimentally verified style-override-doesn't-work
├── data-model.md        # Minimal — no new entities (linked anyway for completeness)
├── quickstart.md        # Operator-facing before/after diff
└── tasks.md             # Phase 2 output (created by /speckit-tasks)
```

### Source Code (repository root)

```text
internal/output/
├── subscription_mode.go         # Add unescapeSupplementaryPlane(); call it at end of Render()
└── subscription_mode_test.go    # Add TC-U-OUTPUT-EMOJI-01..06 unit tests
internal/integration/
├── pipeline_test.go             # Add TestI_006_01_RoundTripEmoji (parse rendered body, compare strings)
└── testdata/snapshots/
    └── served-config.snap.yaml  # Regenerate — 759 escape→literal substitutions, no other content changes
```

**Structure Decision**: Changes are localized to `internal/output/subscription_mode.go` and its test files. No new packages, no new public API surface, no merge-layer changes. The only operator-visible change is in the rendered bytes.

## Complexity Tracking

No constitution violations. The fix is a single helper function (~40 lines + test coverage) operating at the existing output-adapter boundary.

The chosen implementation (post-encode byte walk) was selected over two alternatives (style override at the node level, fork of yaml.v3). Rationale and rejected alternatives are documented in research.md.

---

## Phase 0: Research

Resolved in `research.md`. Key findings:

| Question | Answer | Source |
|---|---|---|
| Does setting `node.Style = yaml.SingleQuotedStyle` (or any other style) prevent the `\Uxxxxxxxx` escape? | **No.** yaml.v3 unilaterally promotes any string containing characters >U+FFFF to DoubleQuotedStyle and escapes. Verified experimentally with a 5-style probe. | Phase-0 experiment, archived in research.md |
| Does yaml.v3's parser correctly handle literal-UTF-8 emoji? | **Yes.** Round-tripping `'alpha_🔰国外流量'` (single-quoted, literal bytes) returns the exact source string. | Phase-0 experiment |
| Inside what string-style contexts does yaml.v3 emit `\Uxxxxxxxx`? | **Only inside double-quoted strings.** Single-quoted, plain, literal, folded styles either don't escape or aren't chosen by the emitter for our content. The emitter promotes to double-quoted *because* of the supplementary-plane character — and *then* escapes it. | yaml.v3 source (`emitterc.go`, `yaml_emitter_select_scalar_style`); confirmed by experiment |
| Should the byte walk track double-quote context? | **Yes.** Operator-supplied content (custom rules, proxy names) can contain a literal `\U` sequence — the walk must distinguish escape syntax (inside double-quoted) from literal characters (anywhere else, or escaped backslashes inside double-quoted). | Safety analysis, research.md D2 |
| Backslash counting? | **Required.** `\\U` inside a double-quoted string is a literal `\U` (escaped backslash + literal `U`), NOT an escape sequence. The walker must count consecutive backslashes; only an odd count followed by `U` constitutes the escape. | YAML 1.2 §5.7, research.md D2 |
| Edge case: `\xhh` and other control-character escapes? | **Pass through unchanged** per FR-006. Only `\Uxxxxxxxx` (8 hex digits) is rewritten. | Spec FR-006 |

## Phase 1: Design & Contracts

### Data Model

See `data-model.md`. No new entities. No struct changes. The fix is purely byte-level.

### Key Interfaces

**One new unexported helper in `internal/output/subscription_mode.go`**:

```go
// unescapeSupplementaryPlane walks the encoded YAML bytes and replaces
// "\Uxxxxxxxx" escape sequences (inside double-quoted strings only) with
// the literal UTF-8 bytes of the corresponding code point. yaml.v3's
// emitter generates these escapes for any code point above U+FFFF
// regardless of node Style; this helper makes the served bytes readable
// while preserving valid YAML.
//
// Outside double-quoted strings: every byte passes through unchanged.
// Inside double-quoted strings: backslash escape sequences are recognized.
// Only `\Uxxxxxxxx` (capital U + exactly 8 hex digits) is rewritten;
// every other escape (`\xhh`, `\n`, `\\`, `\"`, etc.) passes through.
//
// The helper assumes its input is well-formed YAML produced by yaml.v3;
// malformed input (e.g., a `\U` inside double-quoted with fewer than 8 hex
// digits) is left unchanged and not rewritten.
func unescapeSupplementaryPlane(body []byte) []byte
```

**Render() integration** (one new line):

```go
// inside (s *SubscriptionMode).Render(...):
// ... existing encoder path ...
if err := enc.Close(); err != nil {
    return nil, fmt.Errorf("output: close encoder: %w", err)
}
out := unescapeSupplementaryPlane(buf.Bytes()) // NEW
return &Rendered{Body: out, Headers: headers}, nil
```

### Test Plan

**Unit tests** (`internal/output/subscription_mode_test.go`):

- **TC-U-OUTPUT-EMOJI-01**: Single emoji `🔰` (U+1F530) at start of string → literal bytes in output, no `\U` substring.
- **TC-U-OUTPUT-EMOJI-02**: Mixed BMP + non-BMP, e.g. `🔰 国外流量` → both render as literals; CJK ideographs (already literal pre-fix) unchanged.
- **TC-U-OUTPUT-EMOJI-03**: ASCII-only string → no transformation, byte-identical pre/post helper.
- **TC-U-OUTPUT-EMOJI-04**: Operator content containing literal ASCII `\U0001F530` (as 12 ASCII bytes, not the code point) → yaml.v3 emits `\\U0001F530` (escaped backslash); the helper MUST NOT rewrite it. Critical safety case.
- **TC-U-OUTPUT-EMOJI-05**: `\xhh` control-character escape (e.g., literal tab inside a double-quoted string) → passes through unchanged.
- **TC-U-OUTPUT-EMOJI-06**: Round-trip — encode a MergedConfig with emoji, run helper, parse result with yaml.v3, assert decoded strings equal source strings byte-for-byte.

**Integration test** (`internal/integration/pipeline_test.go`):

- **TC-I-006-01 `TestI_006_01_RoundTripEmoji`**: Construct a Pipeline with an upstream YAML containing emoji-named proxies (`🔰 USA-Premium`, `🎁 Auto`, etc.), call the full `Pipeline.Build()` → `SubscriptionMode.Render()` chain. Assert (a) zero `\U` substrings in the rendered body, (b) yaml-parsed names equal the namespaced source names byte-for-byte (e.g., `alpha_🔰 USA-Premium`).

**Snapshot regeneration**: `internal/integration/testdata/snapshots/served-config.snap.yaml` will lose all 759 `\Uxxxxxxxx` occurrences and gain literal UTF-8 emoji bytes. Manual review confirms the diff is escape→literal only (no rule, proxy, or group content moves).

### Quickstart

See `quickstart.md` for the operator-facing before/after.

### Agent Context

`CLAUDE.md` updated to add 006 under the "Key reading" section and update the active-feature pointer.

---

## Phase 2 (preview)

`/speckit-tasks` will produce `tasks.md` with this rough phasing:

- **Phase 1 (Setup)**: orient on `subscription_mode.go` `Render()` flow and existing helpers; identify exact insertion point.
- **Phase 2 (Foundational)**: empty — no shared infrastructure outside the single user story.
- **Phase 3 (US1 — Operator readability)**: TDD tests TC-U-OUTPUT-EMOJI-01..06 first, then `unescapeSupplementaryPlane` implementation, then `Render()` wire-up, then integration test TC-I-006-01.
- **Phase 4 (Polish)**: regenerate snapshot, manual review, `make check`, mark tasks complete.
