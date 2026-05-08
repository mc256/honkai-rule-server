# Feature Specification: Preserve Emoji in Served YAML

**Feature Branch**: `006-fix-emoji-yaml-escape`  
**Created**: 2026-05-01  
**Status**: Draft  
**Input**: User description: "I think I will need to fix a bug, we need to support emoji. But the yaml output is \"\U0001F530\" rather than \"🔰\""

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Operator reads emoji as literal characters in served YAML (Priority: P1) MVP

An operator inspects the served subscription YAML to debug a routing decision or audit which proxies a client receives. Many upstream providers name their proxies with emoji prefixes (`🔰` for primary, `🎁` for auto-select, `🌈` for fallback, `🚀` for direct, etc.) — these are visual cues the operator relies on to scan the file at a glance. Today the served YAML escapes every emoji as `\Uxxxxxxxx` (Unicode escape sequence), so a name like `alpha_🔰国外流量` appears as `alpha_\U0001F530国外流量`. The operator wants the served YAML to contain literal UTF-8 emoji bytes so the file is readable in a normal text editor or terminal.

**Why this priority**: This is the entire bug. Operator readability of the served file is the single user value being delivered. Operators have already encountered this issue (the user reported it).

**Independent Test**: Fetch the served subscription with a token; confirm the response body contains literal emoji bytes (e.g., `🔰`) and contains zero `\Uxxxxxxxx` escape sequences anywhere.

**Acceptance Scenarios**:

1. **Given** an upstream proxy named `🔰 USA-Premium`, **When** the operator fetches the served subscription, **Then** the served YAML contains the literal characters `🔰 USA-Premium` (with provider prefix, per existing 002 namespacing) and does NOT contain `\U0001F530`.
2. **Given** an upstream proxy with a non-BMP character anywhere in its name (any character above U+FFFF — emoji, supplementary CJK ideographs, mathematical symbols, etc.), **When** the operator fetches the served subscription, **Then** that character is rendered as its raw UTF-8 byte sequence in the response body.
3. **Given** a proxy-group whose `name` or whose `proxies:` member list contains emoji (e.g., a custom proxy-group `🚀 Auto`), **When** the operator fetches the served subscription, **Then** the proxy-group name and member references both contain literal emoji bytes.
4. **Given** a custom rule from `config/custom-rules/*.yaml` whose target group name contains emoji (e.g., `DOMAIN-SUFFIX,example.com,🔰 Premium`), **When** the operator fetches the served subscription, **Then** the rule appears in the served `rules:` block with the literal emoji preserved.
5. **Given** the served body is parsed by a downstream Mihomo / Clash client, **When** the client interprets the YAML, **Then** the routing behavior is unchanged from today (the `\Uxxxxxxxx` escape is semantically equivalent to the literal byte; this fix is readability-only and MUST NOT alter rule semantics).

---

### Edge Cases

- **Mixed Unicode planes in one name**: a name like `🔰 国外流量` mixes BMP (CJK ideographs) and non-BMP (emoji). Today only the non-BMP characters are escaped; the CJK ideographs render as literals. After the fix both render as literals. The mid-string boundary must not introduce stray bytes.
- **Empty or ASCII-only names**: names containing zero non-BMP characters (e.g., `direct`, `auto-select`, `Berry-HK-01`) are already rendered as literals today. Fix MUST NOT regress this — they remain rendered as literals.
- **Other escape contexts**: yaml.v3 also emits `\xNN` for some control characters (e.g., a literal tab inside a double-quoted string). This bug applies only to characters above U+FFFF that are valid printable Unicode. Control characters outside the printable range MUST continue to be escaped (preserving valid YAML).
- **Header values**: the `Subscription-Userinfo` and `Profile-Update-Interval` HTTP response headers contain only ASCII (numeric values + ASCII separators); not affected.
- **Round-trip safety**: if the served YAML is parsed by yaml.v3 (the reverse operation), the parsed result MUST equal the input MergedConfig's string values byte-for-byte. The fix MUST NOT introduce double-encoding, mojibake, or character drops.
- **Snapshot regeneration**: the committed integration snapshot `internal/integration/testdata/snapshots/served-config.snap.yaml` currently contains 759 `\Uxxxxxxxx` escapes — these all become literal emoji after the fix. Snapshot must be regenerated with manual review of the diff to confirm only escape→literal substitutions, no other content changes.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The served subscription YAML body MUST render every Unicode character above U+FFFF (supplementary plane) as its literal UTF-8 byte sequence, not as a `\Uxxxxxxxx` escape.
- **FR-002**: The served body MUST remain valid YAML — parseable by `gopkg.in/yaml.v3`, mihomo, and clash without errors.
- **FR-003**: The fix MUST apply uniformly across every section of the served body where Unicode characters above U+FFFF can appear: `proxies` list (proxy `name` fields), `proxy-groups` list (group `name` fields and `proxies:` member name references), `rules` list (rule strings, including target group names that reference emoji-named groups).
- **FR-004**: Round-trip stability — parsing the served body with `gopkg.in/yaml.v3` MUST yield string values that are byte-identical to the corresponding strings in the source `MergedConfig` for every field touched by FR-003.
- **FR-005**: Determinism — two requests against the same input state MUST produce byte-identical response bodies (existing SC-004 contract from feature 005, unchanged by this fix).
- **FR-006**: Control characters that yaml.v3 currently escapes for valid-YAML reasons (tab, newline inside a double-quoted scalar, etc.) MUST continue to be escaped exactly as today. This fix is scoped to printable supplementary-plane characters only.
- **FR-007**: The fix MUST NOT alter the routing semantics carried by the served YAML. Mihomo / Clash clients receiving the post-fix body MUST make identical routing decisions to clients receiving the pre-fix body for the same input state.

### Key Entities *(include if feature involves data)*

No new entities. The fix operates on the bytes emitted by the existing output adapter; no new data structures, no schema changes.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For any input state, the served body returned by `GET /` (subscription endpoint) contains zero occurrences of the regex `\\U[0-9A-Fa-f]{8}` (the literal escape syntax).
- **SC-002**: For any proxy name in the input state that contains characters above U+FFFF, an exact substring match of that name (in its UTF-8 byte form, namespaced per feature 002) is present in the served body.
- **SC-003**: An operator viewing the served body in any standard UTF-8 text editor (e.g., `less`, `cat`, `vim`, browser) sees emoji rendered as their actual visual glyphs, not as `\Uxxxxxxxx` text.
- **SC-004**: 100 sequential subscription fetches against an unchanged input state produce 100 byte-identical responses (existing determinism contract preserved).
- **SC-005**: Parsing any served body with `gopkg.in/yaml.v3` yields string values that are byte-equal to the corresponding `MergedConfig.Proxies` / `ProxyGroups` / `Rules` source values (round-trip stability).

## Assumptions

- The bug is a known limitation of `gopkg.in/yaml.v3`'s emitter, which classifies all characters above U+FFFF as non-printable and escapes them via `\Uxxxxxxxx` regardless of node `Style`. The existing `resetScalarStyles` helper in `internal/output/subscription_mode.go` does not address this; despite setting every scalar's Style to default, the emitter still escapes supplementary-plane characters. The fix therefore needs to operate at a different layer (post-encode byte transform, custom emitter wrapper, or fork). Implementation choice is left to the plan phase.
- Downstream Mihomo / Clash clients accept both `\Uxxxxxxxx` and literal UTF-8 forms equivalently — the bug is readability-only, not functional. This assumption is consistent with YAML 1.2 spec compliance: `\Uxxxxxxxx` and the equivalent UTF-8 byte sequence are interchangeable in any valid YAML parser. No client-compatibility changes are expected.
- Operators are the primary consumers of the readability win. Automated downstream consumers (clients) see no behavior change.
- Snapshot fixtures will be regenerated as part of the fix. The diff is limited to escape→literal substitutions; no rule, proxy, or group content changes.
- Round-trip correctness is testable via unit tests that yaml-parse a rendered body and compare strings byte-by-byte against the source MergedConfig.
- The issue is bounded to the YAML output adapter (`internal/output/subscription_mode.go`). The merge core (`internal/merge/`) operates on UTF-8 strings throughout and does not introduce escapes; verified by code inspection (no `strconv.QuoteRune`, no manual `\U` formatting in merge code).
- Performance impact is negligible — the existing `Render()` already walks the full document tree (for `resetScalarStyles`, `stripComments`, etc.); any byte-level post-processing or alternative emit path stays O(N) in body size, well below the request budget.
