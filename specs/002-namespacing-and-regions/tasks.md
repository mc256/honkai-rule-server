# Tasks: Provider Namespacing & Region Grouping (with Trailing-Rule Drop)

**Input**: Design documents from `/specs/002-namespacing-and-regions/`  
**Prerequisites**: `plan.md` ✅, `spec.md` ✅, `research.md` ✅, `data-model.md` ✅, `contracts/served-subscription.changes.md` ✅, `quickstart.md` ✅

**Tests**: Required by Constitution Principle IV (Test-First, Real-Input Integration — NON-NEGOTIABLE). Order of work per task pair: write the test → verify it fails (`go test` returns nonzero, typically because the symbol doesn't exist yet) → land the implementation → verify the test passes.

**Organization**: Tasks are grouped by the three user stories from `spec.md` so each story can be implemented and verified independently. US1 (P1) is the MVP — the prefix-namespacing precondition; US2 (P2) is the trailing-rule drop + server-emitted fallback; US3 (P3) is the region grouping.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks in this list)
- **[Story]**: Which user story this task belongs to (US1 / US2 / US3); omitted for Setup, Foundational, and Polish phases
- File paths in descriptions are absolute or repo-root-relative

## Path Conventions

Single Go project — paths reference the existing 001 layout (`cmd/server/`, `internal/`, `example/`, `templates/`). New files are placed inside already-established packages per `plan.md` § Project Structure.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Establish the FR-001 `^[a-z]+$` invariant on existing fixtures BEFORE the strengthened validator lands. Running these renames first means the existing 001 test suite continues to pass during Phase 3 implementation; running them later would briefly break every test that loads the fixture.

- [X] T001 [P] Rename source name `beta` → `beta` in `example/subscriptions.csv` (operator-visible baseline for the new FR-001 rule). Old name violates `^[a-z]+$` (contains underscore); after the FR-001 validator lands in T005 it would be silently warn-skipped, breaking documented operator workflows.
- [X] T002 [P] Rename source name `beta` → `beta` in `internal/integration/testdata/fixtures/subscriptions.csv` (test-fixture parity). Same reason as T001 but for the integration-test fixture; without this rename the existing 001 integration tests would lose their beta contributions once the validator lands.

**Checkpoint**: After T001 + T002, `go test ./...` against an unmodified codebase still passes (the 001 loose-name validator accepts `beta` exactly the same as `beta`).

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: None for this feature. 002 builds entirely on 001's existing project layout — no new top-level directories, no new dependencies, no new build pipelines. The fixture renames in Phase 1 are the only cross-cutting prerequisite, and they happen there because they precede every test in every story phase.

(Phase intentionally empty.)

**Checkpoint**: User story implementation can begin after Phase 1 is complete.

---

## Phase 3: User Story 1 — Provider-Prefixed Namespacing of Proxies, Groups, and Rule Targets (Priority: P1) 🎯 MVP

**Goal**: Every upstream-sourced proxy, proxy-group, and rule target in the merged output is rewritten with `<provider>_` prefix. Every own-proxy and own-group is rewritten with a leading `_`. The CSV `name` column is constrained to `^[a-z]+$` with warn-skip on violators. Cross-source name collisions become structurally impossible.

**Independent Test**: Configure two upstreams with overlapping proxy names; fetch the merged subscription; assert every merged proxy name starts with `<provider>_` or `_` (own); every group name does the same or equals the literal `Proxies`; every rule's non-built-in target is rewritten. Configure a CSV row with `name=Bad_Source` (uppercase + underscore); load via `LoadSubscriptions`; assert the row is excluded from the returned slice and a `name-format-violation` warning was emitted.

### Tests for User Story 1 (Constitution Principle IV — write FIRST; tests fail; then implement) ⚠️

- [X] T003 [P] [US1] Add TC-U-CSV-NAME-01 through TC-U-CSV-NAME-07 unit tests in `internal/config/subscriptions_test.go`. Cover: lowercase-only name passes; underscore name warn-skipped; uppercase+digit name warn-skipped; empty name warn-skipped; mixed-validity CSV returns only the valid row(s); violation does NOT raise `*ConfigValidationError`; duplicate-name loud-fail still triggers AFTER name-format soft-skip (sequencing guarantee).
- [X] T004 [P] [US1] Add unit tests in NEW file `internal/merge/namespace_test.go`: TC-U-NS-PROXY-01/02 (proxy name rewrite, including non-ASCII originals); TC-U-NS-GROUP-01/02/03 (select group, built-in untouched, relay member list); TC-U-NS-RULE-01..05 (target rewrite, built-in target untouched, modifier preservation, comma-in-matcher-value parsing); TC-U-NS-IDEMPOTENT-01 (asserts NON-idempotence — second pass produces double-prefix); TC-U-NS-OWN-PROXY-01/02 (own-proxy `_<orig>` rewrite, including original-already-starts-with-underscore → `__<orig>`); TC-U-NS-OWN-GROUP-01/02 (own-group rename + own-proxy member-list reference rewrite + cross-own-group reference rewrite + DIRECT untouched).

### Implementation for User Story 1

- [X] T005 [US1] Strengthen FR-001/FR-002 name validation in `internal/config/subscriptions.go`. Add a compiled `^[a-z]+$` regex (use `regexp.MustCompile` at package init for performance + once-only compile cost). In the row validator, run name-format check **before** duplicate-name detection. On violation: emit `slog.Warn` with attrs `event="name-format-violation"` and `name=<offending value>`, exclude the row from the returned slice, continue with the next row. Do NOT return a `*ConfigValidationError` (soft-skip is a new code path distinct from existing loud-fail validation per the Constitution Principle III deviation noted in plan.md Complexity Tracking).
- [X] T006 [P] [US1] Create NEW file `internal/merge/namespace.go`. Implement `RewriteSource(sourceName string, proxies, groups []*yaml.Node, rules []string) (newProxies, newGroups []*yaml.Node, newRules []string)` per `data-model.md` §5. Define the package-level constant `var builtinTargets = map[string]bool{"DIRECT": true, "REJECT": true, "REJECT-DROP": true, "PASS": true}` and `var ruleModifiers = map[string]bool{"no-resolve": true, "src": true, "dport": true}`. For proxy nodes: clone, rewrite the `name` field to `<sourceName>_<original>`. For group nodes: clone, rewrite the `name` field, and rewrite every entry of every list-of-names attribute (`proxies` for select/url-test/fallback/load-balance; `proxies` for relay's exit list — Mihomo names them all `proxies`) to `<sourceName>_<entry>` UNLESS the entry is in `builtinTargets`. For rules: split each rule on commas; identify the target field as the last comma-separated field that is NOT in `ruleModifiers`; if that target is NOT in `builtinTargets`, rewrite it to `<sourceName>_<target>`; rejoin with commas. Reuse `cloneNode` / `getMappingField` / `setMappingField` / `mappingMembers` / `setMappingMembers` from existing `internal/merge/yamlutil.go` so the rewriter does not duplicate node-traversal helpers.
- [X] T007 [US1] Add `RewriteOwn(ownProxies, ownGroups []*yaml.Node) (newProxies, newGroups []*yaml.Node)` to `internal/merge/namespace.go` (extend the file from T006). Implement leading single-underscore prefix: every own-proxy `name` → `_<original>`; every own-group `name` → `_<original>`; every entry of every group's list-of-names attribute that names an own-proxy or own-group → `_<entry>` (skip `builtinTargets`). The "is this an own-proxy/own-group" check is by membership in the input `ownProxies` / `ownGroups` slices (build a `map[string]bool` of original names from each slice, look up before rewriting; entries not in either map are passed through unchanged so an own-group's reference to e.g. an upstream `<provider>_<original>` group remains untouched).
- [X] T008 [US1] Wire the namespace pass into `Pipeline.Build()` in `internal/merge/pipeline.go`. After the per-source cache walk that populates `proxiesPerSource` / `groupsPerSource` / `rulesPerSource`, call `RewriteSource(row.Name, proxiesPerSource[row.Name], groupsPerSource[row.Name], rulesPerSource[row.Name])` for each source and write the results back into the maps. Before passing `p.ownProxies` / `p.ownGroups` to `MergeProxyGroups` (and to the implicit own-proxies pass in `MergeProxies`), call `RewriteOwn(p.ownProxies, p.ownGroups)` once and use the rewritten slices. The rewrites happen exactly once per Build per source and per own slice (per FR-016 determinism).
- [X] T009 [P] [US1] Update doc comments in `internal/merge/proxies.go` (the `MergeProxies` function comment) and `internal/merge/proxy_groups.go` (the `MergeProxyGroups` function comment) clarifying that cross-source name collisions are now structurally impossible after the prefix pass per FR-007 of 002's spec. Note that the existing `<name>@<source>` collision-suffix path remains live for: (a) own-proxy-vs-upstream duplicates; (b) intra-source duplicates (which remain a loud-fail condition per 001 FR-001b). DO NOT remove the existing collision-suffix code — it's defense-in-depth.
- [X] T010 [US1] Update existing `internal/merge/pipeline_test.go` `TestPipeline_BuildHappyPath` (and any other 001 pipeline tests) so assertions on output proxy/group/rule names expect prefixed forms (`src_a_A1`, `src_b_B1`, etc.) rather than bare names. Add `TestPipeline_BuildOwnProxyUnderscore` covering FR-007a/b: a `Pipeline` constructed with own-proxies + own-groups produces output where own entries carry `_<original>` names and own-group member-list refs to own-proxies are rewritten in lock-step.

### Integration Tests for User Story 1

- [X] T011 [US1] Add integration tests in `internal/integration/pipeline_test.go`: TC-I-002-01 (every proxy name starts with `<provider>_` or `_`; every group name starts with `<provider>_`, `_`, or equals `Proxies`; every rule's non-built-in target follows the same shape); TC-I-002-02 (group member-list references for a known upstream group are rewritten in lock-step); TC-I-002-06 (a third CSV row with `name=Bad_Source` is excluded; one `name-format-violation` log event captured; merged config contains zero contributions from `Bad_Source`); TC-I-002-08 (synthesize cross-source duplicates `Node1` from both upstreams; merged contains `alpha_Node1` AND `beta_Node1`; `MergedConfig.Collisions` has zero cross-source entries); TC-I-002-09 (own-proxy fixture entries appear with `_<original>` names; own-groups renamed; member refs rewritten; built-ins inside own-groups untouched).

**Checkpoint**: User Story 1 complete. Run `go test ./internal/config/... ./internal/merge/... ./internal/integration/...`. The cross-source-collision-impossible invariant holds; the strengthened FR-001 rule is enforced; own-proxies carry `_<original>`. Snapshot tests in `internal/integration/snapshot_test.go` will fail (snapshot regeneration is deferred to Phase 6).

---

## Phase 4: User Story 2 — Trailing-Rule Drop + Server-Emitted Final Fallback (Priority: P2)

**Goal**: Each source's last rule is dropped before merge concatenation; the server appends exactly one `MATCH,<fallbackTarget>` rule at the end of the merged `rules:` block. Default `<fallbackTarget>` is the literal `auto`; overridable via `FALLBACK_RULE_TARGET` env var.

**Independent Test**: Run the pipeline against a fixture whose first source ends with `MATCH,auto`; assert the merged `rules:` block does NOT contain that source's `MATCH,auto` (post-rewrite would have been `<provider>_auto`); assert the very last rule is exactly `MATCH,auto`. Re-run with `FALLBACK_RULE_TARGET=DIRECT` in the test env; assert the last rule changes to `MATCH,DIRECT` while every other rule is byte-identical.

### Tests for User Story 2 (write FIRST) ⚠️

- [X] T012 [P] [US2] Add TC-U-ENV-FALLBACK-01 through TC-U-ENV-FALLBACK-05 unit tests in `internal/config/server_test.go`. Cover: env unset → `FallbackRuleTarget` defaults to `"auto"`; env empty string → defaults to `"auto"`; env `"DIRECT"` → passes through verbatim; env `"alpha_Auto"` → passes through verbatim (no validation); resolved value emitted as a startup log line. Use existing `MapEnv` test helper.
- [X] T013 [P] [US2] Add unit tests in `internal/merge/rules_test.go`: TC-U-RULES-DROP-01 (`[A, MATCH,auto]` → `[A]` post-drop pre-fallback); TC-U-RULES-DROP-02 (empty rule list → no-op + `trailing-rule-drop:noop` log captured); TC-U-RULES-DROP-03 (single-rule list `[MATCH,DIRECT]` → empty); TC-U-RULES-DROP-04 (non-MATCH trailing rule still dropped per FR-008 unconditional); TC-U-RULES-FALLBACK-01 (default `auto` → output ends with `MATCH,auto`); TC-U-RULES-FALLBACK-02 (override `DIRECT` → ends with `MATCH,DIRECT`); TC-U-RULES-FALLBACK-03 (zero per-source rules → output is `[MATCH,<target>]` single-element); TC-U-RULES-FALLBACK-04 (server fallback is NOT prefixed — it's appended after per-source rewriting).

### Implementation for User Story 2

- [X] T014 [P] [US2] Add `FallbackRuleTarget string` field to `internal/config/ServerConfig` (default `"auto"` set in `Load()` before the env-var sweep, mirroring how `ProxiesGroupName` defaults to `"Proxies"`). Bind `FALLBACK_RULE_TARGET` env var in `Load()` after the existing `UPSTREAM_USER_AGENT` block, using the same idiom: `if v := env.Getenv("FALLBACK_RULE_TARGET"); v != "" { cfg.FallbackRuleTarget = v }`. After all env binding completes, emit one startup `slog.Info` with attrs `event="fallback-rule-target-resolved"` and `target=<cfg.FallbackRuleTarget>` per FR-010 observability hook.
- [X] T015 [US2] Modify `internal/merge/rules.go`. Change `MergeRules` signature from `MergeRules(perSource map[string][]string, sortedSources []string) []string` to `MergeRulesWithFallback(perSource map[string][]string, sortedSources []string, fallbackRuleTarget string) []string`. Inside the function: for each source in `sortedSources`, **drop the last entry** of `perSource[source]` if non-empty (`perSource[source] = perSource[source][:len(rules)-1]`); if the source's rule list was empty, emit `slog.Debug` with `event="trailing-rule-drop:noop"` and `source=<name>`. Then perform the existing concatenation in priority-desc order (already implemented). After concatenation, **always** append `"MATCH," + fallbackRuleTarget` (FR-010a unconditional even when no source contributed rules). The fallback rule is NOT subject to FR-006 prefixing — it's appended in this function, after per-source rewrites already happened in `RewriteSource`.
- [X] T016 [US2] Extend `merge.Pipeline` in `internal/merge/pipeline.go` with a `fallbackRuleTarget string` field and a `WithFallbackRuleTarget(s string) *Pipeline` builder method (mirror the existing `WithProxiesGroupName` shape). Inside `Build()`, change the `MergeRules(rulesPerSource, contributing)` call site to `MergeRulesWithFallback(rulesPerSource, contributing, p.fallbackRuleTarget)`.
- [X] T017 [US2] Update `cmd/server/main.go` to call `.WithFallbackRuleTarget(cfg.FallbackRuleTarget)` on the `*Pipeline` returned by `merge.NewPipeline(...)` (chain after the existing `.WithProxiesGroupName(cfg.ProxiesGroupName)` call). Without this wiring the env var is read into ServerConfig but never reaches the pipeline.

### Integration Tests for User Story 2

- [X] T018 [US2] Add integration tests in `internal/integration/pipeline_test.go`: TC-I-002-03 (assert merged `rules:` does NOT contain any of the upstreams' original trailing rules — read fixture YAML pre-merge to enumerate; assert the final element of the merged `rules:` array is exactly the string `MATCH,auto`); TC-I-002-04 (re-run the pipeline with `FALLBACK_RULE_TARGET=DIRECT` set in `MapEnv`; assert the final rule changes to `MATCH,DIRECT`; assert every other rule in the merged config is byte-identical to TC-I-002-03's output).

**Checkpoint**: User Story 2 complete. Run `go test ./...`. Merged config has zero upstream-attributable `MATCH` rules; every output ends with exactly one server-emitted `MATCH,<target>`; env override works.

---

## Phase 5: User Story 3 — Region Grouping (Priority: P3)

**Goal**: For every distinct country code inferred from upstream-sourced proxies' display names (via emoji regional-indicator decode + Chinese/English country-name table), emit one `_region_<CC>` proxy-group of type `select` whose members are the upstream-prefixed proxies. Own-proxies are explicitly excluded from region inference. Region groups are also added as members of the always-present `Proxies` selectable group.

**Independent Test**: Run the pipeline against the existing two-source fixture; assert the merged config contains at least `_region_CN` and `_region_HK` (both providers contribute CN/HK nodes); assert every member of every `_region_<CC>` group is an upstream-prefixed name (starts with `alpha_` or `beta_`); assert no own-proxy appears in any region group; assert the same fixture run twice produces byte-identical region-group membership and ordering.

### Tests for User Story 3 (write FIRST) ⚠️

- [X] T019 [P] [US3] Add unit tests in NEW file `internal/merge/region_table_test.go`. Cover the table itself: TC-U-REGION-EMOJI-DECODE-01 (`decodeRegionalIndicatorPair("🇨🇳")` returns `("CN", true)`); TC-U-REGION-EMOJI-DECODE-02 (`decodeRegionalIndicatorPair("🇿🇿")` returns `("ZZ", true)` — decoder validates Unicode-block range only); TC-U-REGION-EMOJI-01..04 (display-name-substring match for `🇨🇳` / `🇺🇸` / `🇭🇰` and emoji-vs-Chinese precedence); TC-U-REGION-CN-01..04 (中国 / 美国 / 香港 / 台湾 + 臺灣); TC-U-REGION-EN-01/02 (`Hong Kong` case-insensitive, `United States`); plus three table-invariant tests: every entry's Code is two uppercase ASCII letters; every entry's Lang ∈ {"zh","en"}; no duplicate Indicator value (catches accidental table corruption at build time).
- [X] T020 [P] [US3] Add unit tests in NEW file `internal/merge/region_test.go`. Cover the inference + group emission: TC-U-REGION-MISS-01 (unmapped name → `(none, false)` + one `region-unmapped-indicator` log event); TC-U-REGION-MISS-02 (10 nodes spanning 3 distinct unmapped fragments → exactly 3 log events, deduplicated within a single `AppendRegionGroups` call); TC-U-REGION-GROUP-01 (3 prefixed proxies all inferring HK → emits `_region_HK` group with members in input order); TC-U-REGION-GROUP-02 (zero proxies inferring HK → no `_region_HK` group emitted); TC-U-REGION-DETERMINISM-01 (two `AppendRegionGroups` calls over identical inputs → byte-identical output; CC ordering alpha-ascending); TC-U-REGION-PROXIES-01 (every emitted region-group name appears in the `Proxies` group's `proxies:` member list in deterministic order); TC-U-REGION-OWN-EXCLUDED-01 (an own-proxy `_🇨🇦 my-server` passed via the appropriate input slice does NOT yield a `_region_CA` group when no upstream contributes a CA classification).
- [X] T021 [P] [US3] Create NEW file `internal/merge/region_table.go`. Define `regionEntry` struct `{Indicator string; Code string; Lang string}`. Declare package-level `var regionTable = []regionEntry{...}` ordered slice with the seed list from `research.md` R8: specific Chinese entries first (中国香港, 中国台湾, 中国澳门), then general Chinese (中国, 美国, 日本, 韩国, 香港, 台湾, 臺灣, 新加坡, 英国, 德国, 法国, 加拿大, 澳大利亚, 俄罗斯, 印度, 越南, 泰国, 马来西亚, 菲律宾, 印度尼西亚, 巴西, 阿根廷, 土耳其, 沙特阿拉伯, 阿联酋, 以色列, 乌克兰, 波兰, 荷兰, 瑞士, 瑞典, 挪威, 丹麦, 芬兰, 西班牙, 意大利, 爱尔兰), then English entries (lowercase substring matched against `strings.ToLower(name)`: hong kong → HK, singapore → SG, united states → US, united kingdom → GB, japan → JP, germany → DE, france → FR, taiwan → TW, plus the rest of the zh list translated). Implement `decodeRegionalIndicatorPair(s string) (code string, ok bool)`: walk the string by rune; on two consecutive runes both in U+1F1E6..U+1F1FF, return uppercase 2-letter alpha-2 by codepoint arithmetic; otherwise `("", false)`. Add a package `init()` that loops the table and panics with a clear error message if any invariant fails (every Code is two uppercase ASCII letters, every Lang ∈ {"zh","en"}, no duplicate Indicator) — loud fail at server startup beats silent corruption.
- [X] T022 [US3] Create NEW file `internal/merge/region.go`. Implement `inferCountry(originalDisplayName string) (code string, ok bool)` per FR-012 precedence: (1) try `decodeRegionalIndicatorPair`; if hit, return; (2) iterate `regionTable` in slice order, return on first `Lang=="zh"` entry whose Indicator is a substring of `originalDisplayName`; (3) lowercase the input once, then iterate again returning on first `Lang=="en"` entry whose Indicator is a substring of the lowercased form; (4) `("", false)`. Implement `AppendRegionGroups(groups []*yaml.Node, upstreamPrefixedProxies []*yaml.Node, proxiesGroupName string, unmappedLogger func(fragment string)) []*yaml.Node` per `data-model.md` §4: build `map[string][]string` of CC → []prefixedName by iterating `upstreamPrefixedProxies`, calling `inferCountry` on the **original** display name (i.e., the prefixed name with the source prefix stripped — split on the first `_`, take the suffix); record un-inferred names through `unmappedLogger` (callers do their own dedup, but pass-through pattern keeps this function pure). Emit one `_region_<CC>` proxy-group for each non-empty CC, in alpha-ascending CC order; group `type` is `select`; member ordering is the input slice's order (already deterministic per FR-016). Append each emitted region-group's name to the `proxiesGroupName` group's `proxies:` list (use the existing `AppendProxiesGroup` helper or its node-mutation primitives).
- [X] T023 [US3] Wire `AppendRegionGroups` into `Pipeline.Build()` in `internal/merge/pipeline.go`. AFTER the existing `mergedGroups = AppendProxiesGroup(...)` call, partition `mergedProxies` into upstream-only (names not starting with `_`) and own (starting with `_`). Pass the upstream-only slice to `AppendRegionGroups`. The `unmappedLogger` callback uses a per-call `map[string]bool` for dedup and emits `slog.Info` with attrs `event="region-unmapped-indicator"` and `fragment=<name>` exactly once per distinct fragment within the merge.

### Integration Tests for User Story 3

- [X] T024 [US3] Add integration tests in `internal/integration/pipeline_test.go`: TC-I-002-05 (parse merged YAML; assert `_region_CN` group exists with non-empty membership; assert every member of every `_region_<CC>` group starts with an upstream prefix — `alpha_` or `beta_` — and never with `_` indicating an own-proxy; assert every emitted `_region_<CC>` name appears in the `Proxies` group's `proxies:` list); TC-I-002-07 (sha256 the merged body 100 times in sequence; assert all 100 hashes equal — covers prefix + region + fallback determinism together); TC-I-002-10 (extend the own-proxies fixture to declare an own-proxy with display name `🇨🇦 my-canada-1`; run the pipeline; assert the merged config contains a proxy `_🇨🇦 my-canada-1`; assert NO `_region_CA` group exists in the output unless an upstream also contributes a CA-classified proxy; if such an upstream exists, assert `_region_CA`'s members do NOT include `_🇨🇦 my-canada-1`).

**Checkpoint**: User Story 3 complete. Run `go test ./...`. Region groups appear deterministically; own-proxies are excluded; region groups appear as `Proxies` members.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Snapshot regeneration (an explicit, reviewable action per the Constitution's Development Workflow snapshot-stability gate), full-suite verification, and operator-facing sanity checks.

- [X] T025 Regenerate the three integration snapshots: from repo root, run `UPDATE_SNAPSHOTS=true go test ./internal/integration/...`. Inspect the resulting diffs in `internal/integration/testdata/snapshots/`: `served-config.snap.yaml` will drift heavily — every upstream-sourced proxy/group name now carries `<provider>_` prefix; every own-proxy/own-group now carries `_`; new `_region_<CC>` groups appear; the `rules:` block grows by ~N region-group members in `Proxies` and ends with `MATCH,auto`; reviewer attention specifically required on all four shape classes. `subscription-userinfo.snap.txt` should NOT drift (traffic aggregation is untouched). `health.snap.json` should drift only on the source-name rename (`beta` → `beta`) — any other drift is a bug. Files: `internal/integration/testdata/snapshots/served-config.snap.yaml`, `internal/integration/testdata/snapshots/health.snap.json`.
- [X] T026 [P] Run `make check` from repo root and verify exit 0. This wraps `go vet`, `staticcheck`, the full test suite, and the `git diff --exit-code` snapshot-drift gate from 001's Development Workflow. A non-zero exit at this stage indicates either a missed snapshot regeneration (T025 incomplete) or a regression introduced by Phase 3/4/5.
- [ ] T027 [P] Walk through `specs/002-namespacing-and-regions/quickstart.md` §5 sanity checks against a locally-running server: `curl` the served subscription with a valid token; pipe through `yq` to assert (a) every proxy `name` starts with `alpha_`, `beta_`, or `_`; (b) the final entry of `rules` is `MATCH,auto`; (c) `proxy-groups` contains at least one entry whose name starts with `_region_`. Also confirm the operator migration story in §1 reads correctly against the just-renamed `example/subscriptions.csv`. No code changes in this task — purely documentation verification against a real server.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies — start immediately.
- **Foundational (Phase 2)**: Empty for this feature.
- **User Stories (Phase 3 → 4 → 5)**: Strict priority order. US2 modifies `internal/merge/pipeline.go` (T016) which US1 already touched in T008; US3 modifies the same file again (T023). Sequencing the stories serially avoids merge conflicts on a hot file. (A multi-developer team COULD work US1+US2+US3 in parallel by carefully sequencing the `pipeline.go` edits, but the linear path is simpler and the integration-test set still validates end-to-end at every checkpoint.)
- **Polish (Phase 6)**: Depends on US1 + US2 + US3 all complete (T025 needs every visible-in-output change to be in place before snapshot regeneration is meaningful).

### User Story Dependencies

- **US1 (P1)**: Depends only on Phase 1 (renamed CSV fixtures). No dependency on US2 or US3.
- **US2 (P2)**: Depends on US1 completion — US2's `Pipeline.WithFallbackRuleTarget` builder method (T016) is added to a struct that US1 also extends (and US1's `Build()` modifications in T008 become the call sites for US2's threading).
- **US3 (P3)**: Depends on US1 completion — region inference (FR-012) operates on upstream-prefixed names (FR-004) which US1 produces; the own-proxy exclusion (FR-012) requires being able to distinguish upstream proxies from own-proxies, which US1's underscore-prefix convention enables (names starting with `_` are own).

### Within Each User Story

- Tests (T003/T004 in US1; T012/T013 in US2; T019/T020 in US3) MUST be written and FAIL (typically by the package failing to compile because the symbol doesn't yet exist) BEFORE the corresponding implementation tasks.
- Within US1: T005 (validation) and T006 (`namespace.go` create) are independent files and run [P]; T007 extends T006's file (sequential); T008 (pipeline wiring) depends on T006+T007; T009 (doc-only) is [P]; T010 (existing test updates) and T011 (new integration tests) come last in US1.
- Within US2: T012/T013 [P] tests; T014 (ServerConfig) [P]; T015 (rules.go) sequential; T016 (pipeline.go) depends on T015's signature change; T017 (main.go) depends on T014+T016; T018 (integration tests) last.
- Within US3: T019/T020/T021 [P] (different new files); T022 depends on T021 (`region.go` uses `regionTable` and `decodeRegionalIndicatorPair` from T021); T023 (pipeline.go) depends on T022; T024 (integration tests) last.

### Parallel Opportunities

- **Phase 1**: T001 + T002 in parallel — they touch different fixture files, no shared state.
- **Phase 3 / US1**: T003 + T004 in parallel (different test files); T006 [P] can start in parallel with the test files since it creates a new file; T009 [P] can run any time after T008.
- **Phase 4 / US2**: T012 + T013 + T014 in parallel (different files); T016 must wait for T015's signature change; T017 must wait for both.
- **Phase 5 / US3**: T019 + T020 + T021 in parallel (all new files in different paths). T022 sequential after T021. T023 sequential after T022.
- **Phase 6**: T026 + T027 in parallel after T025 completes.

---

## Parallel Example: User Story 1

```bash
# After Phase 1 completes, launch all four T0XX [P] tasks together:
Task: "T003 — write TC-U-CSV-NAME-* tests in internal/config/subscriptions_test.go"
Task: "T004 — write TC-U-NS-* tests in NEW file internal/merge/namespace_test.go"
Task: "T006 — implement RewriteSource in NEW file internal/merge/namespace.go"
Task: "T009 — update doc comments in internal/merge/proxies.go and internal/merge/proxy_groups.go"

# Then sequentially (tests will fail until implementations land):
Task: "T005 — strengthen FR-001 validation in internal/config/subscriptions.go"
Task: "T007 — add RewriteOwn to internal/merge/namespace.go"
Task: "T008 — wire RewriteSource + RewriteOwn into Pipeline.Build in internal/merge/pipeline.go"
Task: "T010 — update existing pipeline_test.go to expect prefixed names"
Task: "T011 — add TC-I-002-01/02/06/08/09 to internal/integration/pipeline_test.go"
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1 (T001 + T002).
2. Skip Phase 2 (empty).
3. Complete Phase 3 / US1 (T003 → T011).
4. **STOP and VALIDATE**: Run `go test ./internal/config/... ./internal/merge/... ./internal/integration/...` (snapshot tests will be red — that's expected and tracked for Phase 6). Exercise the `/?token=<valid>` endpoint from a fresh server start and confirm prefixed proxy/group/rule names in the YAML body.
5. If shipping US1 alone, regenerate snapshots (T025) and run `make check` (T026) — that yields a deployable binary with prefix-namespacing only (no trailing-rule drop, no region groups). Useful for incremental rollout.

### Incremental Delivery

1. Phase 1 → Phase 3 / US1 → Phase 6 partial (snapshot + `make check`) → ship MVP.
2. Phase 4 / US2 → Phase 6 partial → ship trailing-rule-drop + fallback.
3. Phase 5 / US3 → Phase 6 full → ship region grouping.

Each ship step includes regenerating snapshots and re-running `make check`. Each story is observably independent in the served output.

### Parallel Team Strategy

Limited parallelism: the three stories all converge on `internal/merge/pipeline.go`. Recommended split:

- **Developer A** owns Phase 1 + all of US1 (foundational structural change).
- **After US1 lands on master**, Developer B starts US2 and Developer C starts US3 in parallel (different files except for `pipeline.go` — coordinate the `pipeline.go` merge once via PR review).
- Developer A picks up Phase 6 once both US2 and US3 are in.

---

## Notes

- **[P] tasks** = different files, no dependencies on incomplete tasks in this list.
- **[Story] label** maps the task to a user story for traceability; omitted on Setup, Foundational, and Polish phases.
- **Each user story is independently shippable** — the spec deliberately structures them as additive layers (US2 + US3 are only meaningful on top of US1, but US1 is meaningful on its own).
- **Verify tests fail before implementing** per Constitution Principle IV (NON-NEGOTIABLE). In Go, "test fails" usually means the package fails to compile because the symbol doesn't yet exist — that's a valid red state.
- **Commit after each task or logical group**. Snapshot regeneration (T025) MUST be its own commit so the diff is reviewable in isolation.
- **Stop at any checkpoint** to validate the increment.
- **Avoid**: vague tasks ("add namespacing"), same-file conflicts marked [P] (always check the listed file paths), cross-story dependencies that break independence (none exist in this list — every story is gated on the previous story's checkpoint, not on a specific implementation detail).
