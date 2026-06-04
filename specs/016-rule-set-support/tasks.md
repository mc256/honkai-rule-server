---
description: "Task list for Rule Set Support"
---

# Tasks: Rule Set Support

**Input**: Design documents from `/specs/016-rule-set-support/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/served-config-rule-providers.md

**Tests**: REQUIRED. Constitution Principle IV (Test-First, Real-Input Integration) is
NON-NEGOTIABLE for any change to the transformation core, classifiers, proxy-group
construction, or merge logic. Every implementation task is preceded by failing unit/
integration tests in its phase.

**Organization**: Tasks are grouped by user story (US1 P1, US2 P2, US3 P3) so each
story is an independently testable, independently deliverable increment.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: US1 / US2 / US3 (Setup, Foundational, Polish carry no story label)
- All paths are repository-relative.

## Path Conventions

Single Go project. Transformation core in `internal/merge/`, output adapter in
`internal/output/`, integration fixtures/snapshots in `internal/integration/`.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Create the new merge-core file the feature lives in.

- [ ] T001 [P] Create stub `internal/merge/ruleset.go` with `package merge`, the `gopkg.in/yaml.v3` import, and a file-level doc comment describing it as the pure rule-provider namespacing/merge/prune/drop module (per plan.md)
- [ ] T002 [P] Create stub `internal/merge/ruleset_test.go` with `package merge` and the test imports (`testing`, `gopkg.in/yaml.v3`)

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The YAML-mapping reader and the `MergedConfig` field that every user
story builds on.

**⚠️ CRITICAL**: No user story work can begin until this phase is complete.

- [ ] T003 Write failing unit test for `findChildMapping` in `internal/merge/yamlutil_test.go` (create file if absent): returns the MappingNode value for a top-level key, `nil` when the key is absent or the value is not a MappingNode
- [ ] T004 Implement `findChildMapping(root *yaml.Node, key string) *yaml.Node` in `internal/merge/yamlutil.go` mirroring `findChildSequence` but guarding `yaml.MappingNode` (makes T003 pass)
- [ ] T005 Add nullable field `RuleProviders *yaml.Node` (with the data-model.md doc comment: nil ⇒ omit `rule-providers:` key) to the `MergedConfig` struct in `internal/merge/pipeline.go`

**Checkpoint**: Mapping reader + struct field exist; `make check` still green (field unused, no behavior change). User stories can now begin.

---

## Phase 3: User Story 1 - RULE-SET rules survive aggregation and the client loads them (Priority: P1) 🎯 MVP

**Goal**: For a single source declaring a `rule-providers:` block and backed
`RULE-SET` rules, the served config carries namespaced provider definitions and
namespaced `RULE-SET` references so Mihomo loads without "rule-provider not found".

**Independent Test**: Configure one source with a `rule-providers:` block and a
backed `RULE-SET` rule; fetch the served config; confirm it contains a
`rule-providers:` block defining the namespaced provider and a `RULE-SET` rule
referencing that same namespaced name, and that no unbacked references remain.

### Tests for User Story 1 (write first, must FAIL)

- [ ] T006 [P] [US1] Add RULE-SET provider-field rewrite cases to `internal/merge/namespace_test.go` covering the real-upstream shapes: (a) builtin target `RULE-SET,Local-IP,DIRECT` → `RULE-SET,<src>_Local-IP,DIRECT`; (b) **non-builtin proxy-group target + modifier** `RULE-SET,Local-IP,SomeGroup,no-resolve` → `RULE-SET,<src>_Local-IP,<src>_SomeGroup,no-resolve` (field[1] AND the group target both prefixed, `no-resolve` preserved in place); (c) non-builtin target without modifier `RULE-SET,China-Site,SomeGroup` → both prefixed; (d) non-RULE-SET rules unchanged; (e) **guard (I1):** a malformed 2-field `RULE-SET,Local-IP` (no target) prefixes field[1] exactly once and never double-prefixes (the target-finder must not treat field[1] as the target)
- [ ] T007 [P] [US1] Add `RewriteSourceRuleProviders` tests to `internal/merge/ruleset_test.go`: prefixes every provider key with `<src>_`; prefixes a non-built-in `proxy:` fetch-through field, leaves `DIRECT` unchanged; preserves `type`/`behavior`/`format`/`url`/`interval` and unknown fields verbatim
- [ ] T008 [P] [US1] Add `ReferencedRuleProviders` + `MergeRuleProviders` tests to `internal/merge/ruleset_test.go`: ReferencedRuleProviders collects field[1] of `RULE-SET` rules from a final rule slice; MergeRuleProviders appends per-source nodes in order, keeps only keys in the referenced set, returns `nil` when the referenced set is empty (FR-006)
- [ ] T009 [P] [US1] Add Render tests to `internal/output/subscription_mode_test.go`: emits a top-level `rule-providers:` mapping when `MergedConfig.RuleProviders` is non-nil; emits no `rule-providers:` key when it is nil
- [ ] T010 [US1] Add single-source integration test in `internal/integration/ruleset_test.go` with a new fixture `internal/integration/testdata/fixtures/upstream/<src>-ruleset.yaml` **shaped after the real upstream example** (generic/synthetic names only — no identifiable strings): a `rule-providers:` block with multiple providers each carrying `type`/`behavior`/`format`/`url`/`path`/`proxy: DIRECT`/`interval`, plus backed `RULE-SET` rules that route to a **non-builtin proxy-group target** (one with a `no-resolve` modifier), where that target group is defined in the source's `proxy-groups:` so it survives namespacing; register the source in `internal/integration/testdata/fixtures/subscriptions.csv`; assert the served body has the namespaced provider keys, namespaced `RULE-SET` provider fields, and namespaced group targets. **FR-014:** include at least one non-RULE-SET rule at a different priority and assert the `RULE-SET` rules appear in the correct priority-interleaved position (not grouped/relocated) (test FAILS before implementation)

### Implementation for User Story 1

- [ ] T011 [US1] Implement the RULE-SET provider-name (field[1]) prefix in the rule rewriter in `internal/merge/namespace.go` (when the first comma-field is `RULE-SET` and `len(parts) >= 2`, prefix `parts[1]` with `<src>_`; keep the existing modifier-aware target rewrite). **Guard (I1):** for `RULE-SET`, the target rewrite MUST never select index 1 — skip the target rewrite when the resolved target index is 1 (e.g. a 2-field `RULE-SET,Name`) so field[1] is prefixed exactly once and never double-prefixed — makes T006 (incl. the guard case) pass
- [ ] T012 [P] [US1] Implement `RewriteSourceRuleProviders(sourceName string, rp *yaml.Node) *yaml.Node` in `internal/merge/ruleset.go`: clone the mapping, prefix each key, prefix a non-built-in `proxy:` value via `builtinTargets`, preserve all other fields (path rewrite deferred to US2) — makes T007 pass
- [ ] T013 [P] [US1] Implement `ReferencedRuleProviders(rules []string) map[string]bool` and `MergeRuleProviders(perSource []*yaml.Node, referenced map[string]bool) *yaml.Node` in `internal/merge/ruleset.go` (ordered append + reference filter + nil-when-empty) — makes T008 pass
- [ ] T014 [US1] Wire into `Pipeline.Build()` in `internal/merge/pipeline.go`: read each source's `rule-providers` via `findChildMapping` in the existing cache walk; call `RewriteSourceRuleProviders` per source collecting per-source nodes in `contributing` order; after the rule slice is final (post-`PruneEmptyProxyGroups`) call `ReferencedRuleProviders` then `MergeRuleProviders`; assign `MergedConfig.RuleProviders`; emit one `ruleset-merged` slog summary event (providers merged / pruned-as-unreferenced counts)
- [ ] T015 [US1] Emit the block in `internal/output/subscription_mode.go` `Render`: after the existing `proxies`/`proxy-groups`/`rules` writes, add a guarded `if merged.RuleProviders != nil { setMappingValue(root, "rule-providers", merged.RuleProviders) }` — makes T009 pass
- [ ] T016 [US1] Generate and commit the snapshot `internal/integration/testdata/snapshots/served-config-ruleset.snap.yaml` (this committed snapshot is the FR-014 ordering oracle — review it to confirm RULE-SET lines sit at the right priority positions); run `internal/integration/ruleset_test.go` green (T010) and confirm `make check` passes with all pre-existing snapshots byte-unchanged (FR-013)

**Checkpoint**: A single source's RULE-SET rules and namespaced rule-providers are served and load in Mihomo. MVP complete.

---

## Phase 4: User Story 2 - Per-source namespacing identifies ownership and prevents collisions (Priority: P2)

**Goal**: Two sources defining the same bare provider name produce two distinct,
source-attributable keys with distinct client cache paths; each source's `RULE-SET`
rule references its own prefixed provider.

**Independent Test**: Configure two sources that each define a provider with the
same bare name; fetch the served config; confirm both providers appear under
distinct `<src>_`-prefixed keys with distinct `path:` values and each `RULE-SET`
rule references its own source's key.

### Tests for User Story 2 (write first, must FAIL)

- [ ] T017 [P] [US2] Add `path:` rewrite tests to `internal/merge/ruleset_test.go`: `RewriteSourceRuleProviders` rewrites a present `path:` to a source-distinct value derived from the namespaced key (`./ruleset/Local-IP.mrs` → `./ruleset/<src>_Local-IP.mrs`, directory + extension preserved); an absent `path:` stays absent (none injected)
- [ ] T018 [P] [US2] Add cross-source collision test to `internal/merge/ruleset_test.go`: two source nodes each defining bare `Local-IP`, both referenced, merge to two distinct keys `<srcA>_Local-IP` and `<srcB>_Local-IP` with their respective distinct paths (FR-012)
- [ ] T019 [US2] Extend `internal/integration/ruleset_test.go` + fixtures with a second source defining the same provider name and routing to it via `RULE-SET`; assert both prefixed keys present with distinct paths and each rule references its own key (test FAILS before T020)

### Implementation for User Story 2

- [ ] T020 [US2] Add the source-distinct `path:` rewrite to `RewriteSourceRuleProviders` in `internal/merge/ruleset.go` (replace the basename with the namespaced key, preserve dir + extension; only when `path:` is present) — makes T017 pass
- [ ] T021 [US2] Confirm `MergeRuleProviders` handles two sources with no key change needed (namespacing guarantees uniqueness); regenerate `served-config-ruleset.snap.yaml` for the two-source fixture and run integration green — makes T018, T019 pass

**Checkpoint**: Same-named providers from different sources coexist collision-free with distinct cache paths and clear ownership.

---

## Phase 5: User Story 3 - A broken or unbacked RULE-SET reference never breaks the whole config (Priority: P3)

**Goal**: An unbacked `RULE-SET` reference is dropped (and logged) rather than
breaking the served config; defined-but-unreferenced providers are pruned;
malformed provider entries are skipped.

**Independent Test**: Configure a source with a `RULE-SET` rule whose provider is
absent from its `rule-providers:` block (and a separate defined-but-unreferenced
provider); fetch the served config; confirm the unbacked rule is absent, the
unreferenced provider is absent, the rest of the config is intact and valid, and
log entries record the drop/skip.

### Tests for User Story 3 (write first, must FAIL)

- [ ] T022 [P] [US3] Add `DropUnbackedRuleSetRules` tests to `internal/merge/ruleset_test.go`: removes `RULE-SET` rules whose (namespaced) provider is not in the source's provider-key set, returns kept rules plus `DroppedRuleSet` descriptors `{Source, Provider, Rule}`; leaves non-RULE-SET rules and backed RULE-SET rules untouched
- [ ] T023 [P] [US3] Add malformed-skip + unreferenced-prune tests to `internal/merge/ruleset_test.go`: `RewriteSourceRuleProviders` skips a provider entry whose value is not a MappingNode and surfaces a skip descriptor; `MergeRuleProviders` omits providers no surviving `RULE-SET` rule references (FR-010)
- [ ] T024 [US3] Extend `internal/integration/ruleset_test.go` + fixtures with (a) an unbacked `RULE-SET` reference and (b) a defined-but-unreferenced provider; assert the unbacked rule is absent from `rules:`, the unreferenced provider is absent from `rule-providers:`, all other rules/providers remain, and the served config is structurally valid. **FR-015:** also include a backed `RULE-SET` rule whose *target group* is empty and therefore pruned by 015, and assert the rule survives with its provider field intact but its target retargeted to the configured fallback (test FAILS before T025/T027)

### Implementation for User Story 3

- [ ] T025 [P] [US3] Implement `DropUnbackedRuleSetRules(rules []string, providerKeys map[string]bool) (kept []string, dropped []DroppedRuleSet)` and the `DroppedRuleSet` type in `internal/merge/ruleset.go` — makes T022 pass
- [ ] T026 [P] [US3] Add the malformed-provider skip (non-MappingNode value) to `RewriteSourceRuleProviders` in `internal/merge/ruleset.go`, returning skip descriptors; confirm the US1 reference-filter already satisfies the unreferenced-prune assertion — makes T023 pass
- [ ] T027 [US3] Wire `DropUnbackedRuleSetRules` into `Pipeline.Build()` in `internal/merge/pipeline.go` per source, after `RewriteSource`/`RewriteSourceRuleProviders` and BEFORE `MergeUnifiedRules` (using the source's namespaced provider-key set); emit FR-011 `ruleset-rule-dropped` slog events per dropped rule and `ruleset-provider-skipped` per malformed entry
- [ ] T028 [US3] Regenerate `served-config-ruleset.snap.yaml` for the unbacked/unreferenced fixture and run integration green — makes T024 pass

**Checkpoint**: All three user stories independently functional; a malformed upstream degrades gracefully.

---

## Phase 6: Polish & Cross-Cutting Concerns

- [ ] T029 [P] Run `make check` (vet + staticcheck + unit + integration + snapshot drift) and confirm every pre-existing snapshot is byte-unchanged (FR-013) and the new snapshot is committed
- [ ] T030 [P] Verify the `quickstart.md` before/after YAML and log-event examples match the actual served output and emitted `slog` events; correct any drift in `specs/016-rule-set-support/quickstart.md`
- [ ] T031 Run the repo sensitive-name guard over new fixtures/snapshots/tests (`git ls-files -z | xargs -0 grep -lE 'starlitedge|starlit|蓝莓桥|erwan|berrypass'`) and confirm zero matches; use generic `srcA`/`srcB`-style source names in all new fixtures

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies — start immediately.
- **Foundational (Phase 2)**: Depends on Setup. T004 (after T003) and T005 BLOCK all user stories.
- **User Stories (Phase 3–5)**: All depend on Foundational. US1 is the MVP. US2 and US3 extend `ruleset.go` / the integration fixture authored in US1, so they are most naturally done in priority order (US1 → US2 → US3); each remains independently testable against its own fixture extension.
- **Polish (Phase 6)**: Depends on all targeted stories being complete.

### User Story Dependencies

- **US1 (P1)**: Foundational only. Establishes `RewriteSourceRuleProviders` (keys + proxy), `ReferencedRuleProviders`, `MergeRuleProviders`, the rule-field rewrite, pipeline wiring, output emit, and the base integration fixture/snapshot.
- **US2 (P2)**: Extends `RewriteSourceRuleProviders` (adds `path:` rewrite) and the integration fixture (second source). Independently testable; builds on US1's plumbing.
- **US3 (P3)**: Adds `DropUnbackedRuleSetRules` + malformed-skip + pipeline drop wiring + logs, and extends the fixture (unbacked/unreferenced). Independently testable.

### Within Each User Story

- Tests are written first and MUST fail before implementation (Principle IV).
- In `ruleset.go`, the three US1 functions can be implemented in parallel (T012, T013 are independent; T011 is in `namespace.go`). US3's `DropUnbackedRuleSetRules` (T025) and malformed-skip (T026) are independent edits within `ruleset.go` but touch the same file — sequence them if a single worker.
- Pipeline edits (T005 → T014 → T027) touch `internal/merge/pipeline.go` and MUST be sequential.
- Snapshot regeneration (T016, T021, T028) is the last step of each story after its impl lands.

### Parallel Opportunities

- Setup: T001, T002 in parallel.
- US1 tests: T006, T007, T008, T009 in parallel (distinct files / distinct test funcs); T010 after fixture exists.
- US1 impl: T012 and T013 in parallel (both `ruleset.go` but independent functions — coordinate if one worker); T011 (`namespace.go`) parallel to both.
- US2 tests T017, T018 in parallel. US3 tests T022, T023 in parallel.
- Polish: T029, T030 in parallel; T031 independent.

---

## Parallel Example: User Story 1 tests

```bash
# After Foundational (T005) completes, launch the US1 failing tests together:
#   T006 internal/merge/namespace_test.go        (RULE-SET field[1] rewrite)
#   T007 internal/merge/ruleset_test.go          (RewriteSourceRuleProviders)
#   T008 internal/merge/ruleset_test.go          (Referenced + MergeRuleProviders)
#   T009 internal/output/subscription_mode_test.go (emit/omit rule-providers)
go test ./internal/merge/ ./internal/output/ -run 'RuleSet|RuleProvider|RuleProviders' # expect FAIL
```

---

## Implementation Strategy

### MVP First

Complete **Phase 1 → Phase 2 → Phase 3 (US1)**. At the US1 checkpoint a single
source's `RULE-SET` rules load correctly in Mihomo — the core value. Ship/validate
before extending.

### Incremental Delivery

1. US1 (MVP): single-source happy path + emit + base snapshot.
2. US2: multi-source collision safety + distinct cache paths.
3. US3: graceful degradation for malformed upstreams + observability.

Each story regenerates only the feature snapshot; the snapshot-stability gate
proves all other served configs are byte-unchanged (FR-013) at every step.
