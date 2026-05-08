# Phase 1 Data Model: Preserve Emoji in Served YAML

**Feature**: [spec.md](./spec.md) | **Plan**: [plan.md](./plan.md) | **Date**: 2026-05-01

This feature introduces no new persistent storage, no new Go types, and no
schema changes. The fix is a single byte-level transform applied at the
output adapter's emit boundary.

## In-memory state

`unescapeSupplementaryPlane(body []byte) []byte` is a pure function. Its
in-function state during a single call:

| State | Type | Purpose |
|---|---|---|
| `i` | `int` | Read offset into the input slice |
| `out` | `[]byte` | Fresh allocated output buffer (capacity hint = `len(body)`) |
| `inDQ` | `bool` | Are we currently inside a double-quoted YAML scalar? |
| `prevIsBackslash` | `bool` | Was the previous *output* byte an unescaped backslash? Used to detect that the next byte is consumed as part of an escape sequence. |

No persistence. No allocation per call beyond `out` and a temporary 8-byte
hex-decode buffer (stack-allocated). The transform does not call any
external system.

## No struct or interface changes

- `MergedConfig` (in `internal/merge/pipeline.go`) — unchanged.
- `MergeResult` (in `internal/merge/rules.go`) — unchanged.
- `Rendered` (in `internal/output/subscription_mode.go`) — unchanged.
- `SubscriptionMode` (in `internal/output/subscription_mode.go`) — unchanged.
- All public method signatures — unchanged.

## Validation rules

The helper assumes its input is well-formed YAML produced by `yaml.v3`. It
does not validate the input shape. Specifically:

- `\Uhhhhhhhh` requires exactly 8 hex digits. If fewer hex digits follow, the
  helper passes the bytes through unchanged (treating the sequence as an
  unrecognized escape rather than corrupting the stream).
- The decoded code point is rendered as UTF-8 via `utf8.EncodeRune`. If the
  code point is invalid (surrogate halves, > U+10FFFF), `utf8.EncodeRune`
  emits `�` (replacement character). This is the same behavior the
  yaml.v3 parser would produce on a malformed input — so round-trip
  correctness for malformed escapes is preserved.
- Double-quote tracking is line-naive: a YAML double-quoted scalar can span
  lines (with continuation), but the *escaped* `\Uxxxxxxxx` sequence is
  always on a single line because the bytes that compose the escape are all
  ASCII. The helper does not need multi-line awareness.

## Performance envelope

| Property | Value |
|---|---|
| Pass complexity | O(N) where N = `len(body)` |
| Output size | ≤ N bytes (each rewrite shrinks: 10 ASCII bytes → ≤4 UTF-8 bytes) |
| Allocations per call | 1 (the output buffer) |
| Worst-case wall time | ~1 µs per KB on commodity hardware (no system calls) |

Current production body size: ~175 KB. Estimated transform cost: 100–200 µs.
This is dwarfed by yaml.v3's encode cost (~milliseconds) and is well below
any HTTP-handler latency budget.
