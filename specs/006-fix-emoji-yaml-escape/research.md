# Phase 0 Research: Preserve Emoji in Served YAML

**Feature**: [spec.md](./spec.md) | **Plan**: [plan.md](./plan.md) | **Date**: 2026-05-01

## Decisions

### D1. Implementation layer: post-encode byte transform

**Decision**: Add an unexported helper `unescapeSupplementaryPlane(body []byte) []byte` in `internal/output/subscription_mode.go`. Call it inside `Render()` after `enc.Close()` and before constructing `Rendered{Body: ...}`.

**Rationale**: A live experiment (Phase-0 probe, archived below) confirmed that `gopkg.in/yaml.v3` unilaterally promotes any string containing a code point above U+FFFF to `DoubleQuotedStyle` and emits `\Uxxxxxxxx`, *regardless of the node's `Style` field*. The existing `resetScalarStyles` helper sets every scalar's `Style` to 0 (default) — which yaml.v3 then ignores for these strings. Therefore the only points where we can intervene are:

1. **Before encoding**: pre-emit the strings ourselves (build a custom emitter or fork). Heavy.
2. **After encoding**: rewrite the emitter's output bytes. Light.

Option 2 is the right scope: ~40 lines, single boundary, easy to test with a round-trip property.

**Alternatives considered**:

- *Set `node.Style = yaml.SingleQuotedStyle` on every scalar in `resetScalarStyles`*: rejected — experiment showed yaml.v3 promotes to DoubleQuoted anyway. The user-set Style is overridden when the emitter sees a "non-printable" character.
- *Fork `gopkg.in/yaml.v3` and patch `is_printable`*: rejected — we'd carry a fork forever, and upstream maintainers have not accepted similar patches in the past (yaml.v3 errs on the side of conservative compatibility). Burden disproportionate to benefit.
- *Switch to a different YAML library (e.g., `goccy/go-yaml`)*: rejected — too disruptive. The library is woven through `internal/output/`, `internal/merge/`, `internal/customrules/`, and the integration snapshot. A migration is a multi-feature epic, not a bug fix.
- *Replace yaml.v3's emitter with a hand-rolled minimal YAML printer*: rejected — yaml.v3 handles many edge cases (collisions, tags, quoting rules) we'd need to reimplement. Maintenance cost too high.

### D2. Walk strategy: backslash-counting state machine inside double-quoted strings

**Decision**: The helper walks the byte stream once, tracking three pieces of state:
- Whether we're currently inside a double-quoted string (`inDQ`).
- Whether the previous byte was an unescaped backslash (`prevIsEscape`).
- The current write position into a fresh output buffer.

When `inDQ && !prevIsEscape && byte == '\\' && next == 'U' && next 8 are hex digits`, decode and rewrite. Otherwise pass the byte through.

**Rationale**: YAML 1.2 §5.7 defines the double-quoted style as the *only* style that processes `\` escape sequences. Single-quoted, plain, literal, and folded styles emit characters literally; a backslash inside them is just a backslash byte. So the helper must distinguish double-quoted context from everything else, and within double-quoted, must count backslashes correctly to avoid rewriting `\\U0001F530` (where the leading backslash is itself escaped — the literal content is the ASCII string `\U0001F530`, NOT a code-point escape).

**Why not regex**: Go's `regexp` package uses RE2 which has no lookbehind. A naive regex `\\U[0-9A-Fa-f]{8}` matches both real escapes and false-positive content. Mitigation via alternation (`(\\\\|\\U[0-9A-Fa-f]{8})`) would still need post-match logic and adds parser complexity for no gain over the byte walk. Byte walk is faster (no regex compile, no allocation per match) and easier to read.

**Alternatives considered**:

- *Naive regex `\\U([0-9A-Fa-f]{8})`*: rejected — false-positive on `\\U0001F530` content, would corrupt operator-supplied data.
- *Regex with consume-double-backslash alternation*: rejected — adds complexity vs. byte walk, no measurable benefit.
- *yaml.v3 round-trip (parse the emitted body, re-emit)*: rejected — the second emit would produce the same escapes. Not a fix.
- *YAML node tree post-walk to override style*: rejected per D1 (yaml.v3 ignores Style for non-printable content).

### D3. What to rewrite: only `\Uxxxxxxxx` (8 hex digits)

**Decision**: Rewrite only `\U` followed by exactly 8 hex digits. Leave `\xHH` (2 hex digits, control characters) and `\uXXXX` (4 hex digits, BMP supplementary characters that are non-printable) untouched.

**Rationale**: Spec FR-006 requires that control-character escapes continue to be valid YAML. `\xHH` covers byte values 0x00–0xFF — these are non-printable control characters that MUST stay escaped to keep the YAML valid (a literal newline byte inside a double-quoted single-line string would close the string). `\uXXXX` is the 4-hex-digit form for BMP characters; in our domain, yaml.v3 has classified these as printable so this form rarely appears in our output, but we leave it alone to be safe.

The user impact case is exclusively `\Uxxxxxxxx` (8 hex digits, U+10000 and above) — emoji, supplementary CJK, mathematical alphanumerics. That's the only escape form we rewrite.

**Alternatives considered**:

- *Also rewrite `\uXXXX`*: rejected — not in the bug report; some `\uXXXX` characters are legitimately non-printable (e.g., zero-width joiner U+200D in some contexts). Keeping the helper narrowly scoped reduces blast radius.
- *Rewrite arbitrary `\xHH` sequences*: rejected — would emit raw control bytes into YAML, breaking the parser.

### D4. Round-trip safety as the load-bearing test

**Decision**: The integration test `TestI_006_01_RoundTripEmoji` is the central correctness check. It (a) constructs a MergedConfig with diverse emoji content, (b) runs the full Pipeline → Adapter → bytes chain, (c) parses the resulting bytes back with yaml.v3, (d) asserts every parsed string equals the corresponding source string byte-for-byte.

**Rationale**: The bug is silent — a downstream Mihomo client would route correctly with either the escaped or literal form. The thing that can break is *operator readability* (visible to humans) and *round-trip correctness* (visible to code that re-parses the served YAML, e.g., automated tooling). Round-trip is the property that distinguishes a real fix from a buggy fix.

**Alternatives considered**:

- *Compare rendered bytes to a fixed expected string*: too brittle — yaml.v3 may emit fields in different orders or use different quoting heuristics across versions. The round-trip property is the actual invariant.

## Phase 0 Experiment (archived)

```go
// Probe: which yaml.Style values prevent the \Uxxxxxxxx escape?
package main

import (
    "bytes"
    "fmt"
    "gopkg.in/yaml.v3"
)

func main() {
    cases := []struct {
        name  string
        style yaml.Style
    }{
        {"default(0)", 0},
        {"DoubleQuotedStyle", yaml.DoubleQuotedStyle},
        {"SingleQuotedStyle", yaml.SingleQuotedStyle},
        {"LiteralStyle", yaml.LiteralStyle},
        {"FoldedStyle", yaml.FoldedStyle},
    }
    for _, c := range cases {
        root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
            {Kind: yaml.ScalarNode, Value: "name", Tag: "!!str"},
            {Kind: yaml.ScalarNode, Value: "alpha_🔰国外流量", Tag: "!!str", Style: c.style},
        }}
        var buf bytes.Buffer
        enc := yaml.NewEncoder(&buf)
        enc.SetIndent(2)
        _ = enc.Encode(root)
        _ = enc.Close()
        fmt.Printf("[%s] %s", c.name, buf.String())
    }

    // Round-trip: parse single-quoted literal-UTF-8 and check the value.
    literal := []byte("name: 'alpha_🔰国外流量'\n")
    var n yaml.Node
    _ = yaml.Unmarshal(literal, &n)
    fmt.Printf("[parse back single-quoted] name=%q\n",
        n.Content[0].Content[1].Value)
}
```

Output (run on 2026-05-01 against `gopkg.in/yaml.v3 v3.x`):

```
[default(0)] name: "alpha_\U0001F530国外流量"
[DoubleQuotedStyle] name: "alpha_\U0001F530国外流量"
[SingleQuotedStyle] name: "alpha_\U0001F530国外流量"
[LiteralStyle] name: "alpha_\U0001F530国外流量"
[FoldedStyle] name: "alpha_\U0001F530国外流量"
[parse back single-quoted] name="alpha_🔰国外流量"
```

**Conclusion**: yaml.v3's emitter ignores requested Style and unilaterally emits `\Uxxxxxxxx`. The parser handles literal UTF-8 fine. This eliminates option 1 (style override) and confirms option 2 (post-encode byte transform) is required.

## Open Questions

None. All design decisions resolved.
