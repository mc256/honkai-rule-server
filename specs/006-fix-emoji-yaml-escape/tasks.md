---

description: "Task list for 006-fix-emoji-yaml-escape"
---

# Tasks: Preserve Emoji in Served YAML

**Input**: Design documents from `/specs/006-fix-emoji-yaml-escape/`
**Prerequisites**: plan.md (required), spec.md (required), research.md, data-model.md, quickstart.md

**Tests**: Per Constitution Principle IV (NON-NEGOTIABLE), every unit test MUST be committed before the implementation it validates. Test tasks are explicitly included and must land first within each user story phase.

**Organization**: Single user story (P1). Phase 1 has one orientation read; Phase 2 is empty; Phase 3 is the TDD-ordered implementation; Phase 4 is snapshot regen + check + finalize.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (US1)
- Include exact file paths in descriptions

## Path Conventions

- `internal/output/subscription_mode.go` — output adapter; the only production file touched
- `internal/output/subscription_mode_test.go` — unit tests for the new helper
- `internal/integration/pipeline_test.go` — integration test TC-I-006-01
- `internal/integration/testdata/snapshots/served-config.snap.yaml` — committed snapshot; regenerated after the fix

---

## Phase 1: Setup

**Purpose**: Confirm the exact insertion point in `Render()` and the byte shape of yaml.v3's escape output

- [X] T001 Read `internal/output/subscription_mode.go` and locate `(s *SubscriptionMode).Render(...)`. Confirm the exact line where the new `unescapeSupplementaryPlane` call must be inserted: immediately after `enc.Close()` returns (currently around line 117), before `return &Rendered{Body: buf.Bytes(), Headers: headers}, nil`. The call mutates the bytes returned in `Rendered.Body`.

**Checkpoint**: Insertion point confirmed; ready to write tests

---

## Phase 2: Foundational

(Empty — no shared infrastructure outside the single user story.)

---

## Phase 3: User Story 1 — Operator readability of emoji in served YAML (Priority: P1) MVP

**Goal**: yaml.v3's `\Uxxxxxxxx` escape sequences for supplementary-plane Unicode (emoji, supplementary CJK, mathematical alphanumerics) are rewritten to literal UTF-8 bytes at the output adapter's emit boundary, while leaving every other byte (control-character escapes, literal backslashes in operator content, all non-double-quoted content) untouched

**Independent Test**: Construct a `MergedConfig` with an upstream proxy named `🔰 USA-Premium` (the actual code point U+1F530, not the ASCII escape); call `(*SubscriptionMode).Render(...)`; assert (a) the rendered body contains the literal substring `🔰 USA-Premium` (after namespacing, e.g., `alpha_🔰 USA-Premium`), (b) the rendered body contains zero occurrences of the regex `\\U[0-9A-Fa-f]{8}`, and (c) yaml-parsing the body yields a `name` field that equals the source string byte-for-byte

**Dependency**: None — this is the first user story phase

### Tests for User Story 1 (write FIRST, verify they FAIL)

- [X] T002 [US1] In `internal/output/subscription_mode_test.go`: write `TestUnescapeSupplementaryPlane_SingleEmoji` (TC-U-OUTPUT-EMOJI-01). Call `unescapeSupplementaryPlane([]byte("name: \"\\U0001F530\"\n"))`; assert the returned bytes contain the substring `"🔰"` (literal UTF-8) and contain zero `\\U` substrings. The test must compile but fail because `unescapeSupplementaryPlane` is not yet defined.
- [X] T003 [US1] In `internal/output/subscription_mode_test.go`: write `TestUnescapeSupplementaryPlane_MixedBMP` (TC-U-OUTPUT-EMOJI-02). Input `"name: \"alpha_\\U0001F530国外流量\"\n"`; assert output contains the literal substring `alpha_🔰国外流量` (with both the emoji and the CJK ideographs as literals). The CJK characters were already literal pre-fix; they MUST remain literal post-fix (no regression).
- [X] T004 [US1] In `internal/output/subscription_mode_test.go`: write `TestUnescapeSupplementaryPlane_AsciiOnly` (TC-U-OUTPUT-EMOJI-03). Input is a yaml-encoded body containing only ASCII (e.g., `"name: hello-world\nport: 7890\n"`); assert the output bytes are byte-identical to the input (no transformation when there is nothing to rewrite).
- [X] T005 [US1] In `internal/output/subscription_mode_test.go`: write `TestUnescapeSupplementaryPlane_LiteralBackslashU` (TC-U-OUTPUT-EMOJI-04). This is the critical safety case. Encode a Go string whose VALUE is the 12 ASCII characters `\U0001F530` (i.e., backslash + U + 8 hex digits, NOT the code point U+1F530) using `yaml.NewEncoder` to a `bytes.Buffer`; the encoder will emit `"\\U0001F530"` (escaped backslash). Pass the encoded bytes to `unescapeSupplementaryPlane`; assert the output STILL contains `\\U0001F530` (helper recognized that the leading backslash escapes the second one, so this is not an escape sequence). The output must not contain any literal `🔰` byte sequence.
- [X] T006 [US1] In `internal/output/subscription_mode_test.go`: write `TestUnescapeSupplementaryPlane_ControlCharEscape` (TC-U-OUTPUT-EMOJI-05). Encode a string containing a literal tab character with `yaml.NewEncoder`; yaml.v3 will emit `\t` inside a double-quoted scalar. Pass the encoded bytes to `unescapeSupplementaryPlane`; assert the output STILL contains the `\t` escape (helper does not touch any escape that is not exactly `\Uxxxxxxxx`).
- [X] T007 [US1] In `internal/output/subscription_mode_test.go`: write `TestUnescapeSupplementaryPlane_RoundTrip` (TC-U-OUTPUT-EMOJI-06). Encode a yaml mapping `name: "alpha_🔰 USA-Premium"` via `yaml.NewEncoder`; pass through the helper; then `yaml.Unmarshal` the result back into a `map[string]string`. Assert the parsed value equals the source string byte-for-byte (`"alpha_🔰 USA-Premium"`). This is the load-bearing correctness check identified in research D4.
- [X] T008 [US1] Run `go test ./internal/output/... -run TestUnescapeSupplementaryPlane` and verify all six tests fail with `undefined: unescapeSupplementaryPlane` (compile error). This is the red phase of TDD.

### Implementation for User Story 1

- [X] T009 [US1] In `internal/output/subscription_mode.go`: implement `unescapeSupplementaryPlane(body []byte) []byte` per the design in plan.md. The function walks the input bytes once with three pieces of state: `i` (read offset), `inDQ` (inside a double-quoted YAML scalar), `prevIsBackslash` (was the previous byte inside `inDQ` a not-yet-consumed backslash?). When `inDQ && prevIsBackslash && body[i] == 'U'` and the next 8 bytes are hex digits, decode them via `strconv.ParseInt(..., 16, 32)`, encode the resulting code point with `utf8.EncodeRune`, append the UTF-8 bytes to the output, advance `i` by 9, clear `prevIsBackslash`. In all other cases, append the byte verbatim, update `inDQ` on unescaped `"`, update `prevIsBackslash` on `\` (and clear it on the next byte). Add the doc comment from plan.md verbatim. Required imports: add `sort` (already present), `strconv`, `unicode/utf8` (`strings` may also already be present from feature 005 — verify).
- [X] T010 [US1] Run `go test ./internal/output/... -run TestUnescapeSupplementaryPlane` and verify all six tests pass (green phase). If any test fails, debug the helper before proceeding.
- [X] T011 [US1] In `internal/output/subscription_mode.go`: wire the helper into `Render()` — after the `enc.Close()` block (currently around line 117) and before constructing the `Rendered` return value, add `body := unescapeSupplementaryPlane(buf.Bytes())` and use `body` as the `Rendered.Body` value. Remove or rename `buf.Bytes()` from the original return statement to point at the post-transform bytes.
- [X] T012 [US1] In `internal/integration/pipeline_test.go`: add `TestI_006_01_RoundTripEmoji` (TC-I-006-01). Construct a `stubMergeCache` with an upstream YAML containing emoji-named proxies and groups (e.g., `🔰 USA-Premium`, `🎁 Auto`); build a Pipeline; call `Pipeline.Build()` then render via the existing `renderViaAdapter` helper (introduced in feature 005's TC-I-005-02). Assert: (a) the rendered body contains zero occurrences of the regex `\\U[0-9A-Fa-f]{8}` (use `regexp.MustCompile`); (b) the rendered body contains literal substring `alpha_🔰 USA-Premium` and `alpha_🎁 Auto`; (c) `yaml.Unmarshal(body, &doc)` succeeds and `doc.Proxies[0].Name == "alpha_🔰 USA-Premium"` (round-trip equality).
- [X] T013 [US1] Run `go test ./internal/integration/... -run TestI_006_01_RoundTripEmoji -v` and verify the test passes.

**Checkpoint**: US1 complete — `go test ./internal/output/... ./internal/integration/...` passes for all rule-related tests. Note: snapshot drift is expected at this point because the existing snapshot still contains 759 `\Uxxxxxxxx` escapes; regeneration is in Phase 4.

---

## Phase 4: Polish & Snapshot Regeneration

**Purpose**: Regenerate the committed integration snapshot to reflect literal UTF-8 emoji, run the full check suite, and finalize

- [X] T014 Regenerate `internal/integration/testdata/snapshots/served-config.snap.yaml` by running `UPDATE_SNAPSHOTS=true go test ./internal/integration/...`. Then manually review the diff: confirm (a) every `\U[0-9A-Fa-f]{8}` substring is replaced by the corresponding literal UTF-8 emoji bytes (expected: 759 such substitutions); (b) no rule strings, proxy names, group names, or non-rules sections move or change content; (c) the file is still valid YAML (run `yaml.Unmarshal` mentally on a few rule lines and confirm the names parse to the expected human-readable strings). Per Constitution Snapshot Stability Gate, the PR description must quote the diff summary.
- [X] T015 Run `make check` and verify all gates pass (`go vet`, `staticcheck`, full test suite, snapshot drift check). Fix any unexpected regressions; if `git diff --exit-code` flags only the files this PR touches (subscription_mode.go, subscription_mode_test.go, pipeline_test.go, served-config.snap.yaml, plus this tasks.md), that is the expected pre-commit state and the after-implement hook commit will resolve it.
- [X] T016 Mark all tasks `[X]` in this file; verify `git diff --exit-code` is clean after the after-implement hook commits the change.

**Checkpoint**: All features complete, snapshot updated, `make check` green

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1 (Setup)**: standalone read-only orientation — proceed to Phase 3
- **Phase 2 (Foundational)**: empty — proceed to Phase 3
- **Phase 3 (US1)**: contains all the implementation; tasks within follow strict TDD order (T002–T008 update tests *before* T009–T011 add the implementation)
- **Phase 4 (Polish)**: depends on Phase 3 (snapshot regeneration requires the helper to be in place)

### User Story Dependencies

- **US1 (P1)**: Standalone — this is the only user story.

### Within User Story 1

- T002–T007 (write 6 unit tests) before T008 (verify they fail) before T009 (implement helper) before T010 (verify they pass).
- T011 (wire into Render()) before T012 (write integration test) before T013 (verify integration test passes).
- T009 logically depends on T002–T008, but the helper code can be drafted alongside the tests; the gate is T010 (all unit tests must pass).

### Parallel Opportunities

```text
Phase 3:  T002 [P] ── T003 [P] ── T004 [P] ── T005 [P] ── T006 [P] ── T007 [P]   (six unit-test
                                                                                  functions in
                                                                                  the same file
                                                                                  but disjoint;
                                                                                  practical to
                                                                                  batch as one
                                                                                  Edit)
          T008 (gate)
          T009                                                                    (single helper)
          T010 (gate)
          T011                                                                    (Render() wire-up)
          T012                                                                    (integration test)
          T013 (gate)

Phase 4:  T014 ── T015 ── T016                                                    (sequential)
```

In practice, the six unit-test edits collapse into one batched Write of `subscription_mode_test.go`. The implementation steps (T009 helper + T011 wire-up) are short enough to land in two short Edits.

---

## Implementation Strategy

### Single-pass execution (recommended)

The change is small and tightly scoped. Natural rhythm:

1. Add the six unit tests in one batched Write of `subscription_mode_test.go` (T002–T007).
2. Run merge tests, confirm `undefined: unescapeSupplementaryPlane` (T008).
3. Implement the helper in `subscription_mode.go` (T009).
4. Run merge tests, confirm green (T010).
5. Wire the helper into `Render()` (T011).
6. Add the integration test (T012); confirm green (T013).
7. Regenerate snapshot, review diff (T014).
8. `make check`, commit (T015–T016).

### MVP scope

There is no smaller-than-MVP version of this feature. The whole change is a single helper and one wire-up site.

---

## Notes

- The critical safety case is TC-U-OUTPUT-EMOJI-04 (T005): operator-supplied content containing the literal ASCII characters `\U0001F530` must NOT be rewritten. yaml.v3 emits `\\U0001F530` (escaped backslash) for that input, and the helper must recognize the leading backslash as escaping the second one. Without this guard the helper would corrupt operator data.
- The round-trip test TC-U-OUTPUT-EMOJI-06 (T007) is the load-bearing correctness check identified in research D4. If the helper damages the byte stream in any way, the round-trip will fail.
- The `\Uxxxxxxxx` form has exactly 8 hex digits. The 4-digit form `\uXXXX` is intentionally NOT rewritten (per research D3) — some BMP characters that yaml.v3 emits as `\uXXXX` are legitimately non-printable (zero-width joiner, etc.) and changing them could break valid YAML.
- The helper does not need to handle yaml.v3's other escape forms (`\n`, `\t`, `\r`, `\0`, `\"`, `\\`, etc.) — these pass through unchanged because the helper only matches `\U` followed by 8 hex digits.
- Per CLAUDE.md guidance, the helper does not introduce comments explaining "added for feature 006" or similar PR-tracking text. The doc comment on the helper describes *what it does and why*, not the change history.
- This feature does not change `cmd/server/main.go`, `internal/customrules/`, `internal/merge/`, or any other package. Its blast radius is the single helper plus its wire-up at the output-adapter boundary.
- After this lands, when the live tmux server is restarted it should serve YAML with literal emoji. Operator verification: `curl ... | grep -cE '\\U[0-9A-Fa-f]{8}'` should print `0`, and `curl ... | grep -E 'name:.*🔰' | head -5` should print proxy names with literal emoji glyphs.
