# Implementation Plan: Load-Balance Variants of Auto-Emitted Region & Continent Proxy Groups

**Branch**: `014-load-balance-region-groups` | **Date**: 2026-05-08 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/014-load-balance-region-groups/spec.md`

## Summary

Additive emission of `_lb_region_<CC>` / `_lb_continent_<CONT>` proxy groups (type `load-balance`) carrying the same member lists as their existing `_region_<CC>` / `_continent_<CONT>` siblings (012's url-test groups stay untouched). New emission lives in `internal/merge/region.go` next to the existing `newURLTestGroup` helper: a sibling `newLoadBalanceGroup` that emits the six load-balance fields (`url`, `interval`, `lazy`, `strategy`, `timeout`, `max-failed-times`). `AppendRegionGroups` and `AppendContinentGroups` extend their signatures to accept a `LoadBalanceParams` value alongside the existing `URLTestParams`, and on each emit step append the lb sibling immediately after the url-test group (paired layout per spec FR-013), while also adding the lb group's name to the always-present `Proxies` selector.

008's `AppendFanoutProxies` predicate widens from `_region_`/`_continent_` to also include `_lb_region_`/`_lb_continent_`, so the existing fan-out machinery generates `via_lb_region_<CC>__<own>` / `via_lb_continent_<CONT>__<own>` for free — no new fan-out path. Pipeline ordering is unchanged: lb-group emission slots into the same `AppendRegionGroups` / `AppendContinentGroups` steps that already run before `AppendFanoutProxies`, so spec FR-012's "before fan-out" constraint is satisfied without restructuring `Pipeline.Build`.

Configuration plumbing mirrors 012 exactly: a new `LoadBalanceParams` struct in `internal/config/server.go` loaded from six `LOAD_BALANCE_*` env vars (default values from spec FR-003), validated at startup with loud-fail on bad input (Constitution Principle III, including a new strategy-enum check). A `Pipeline.WithLoadBalanceParams` builder threads the value into `merge`. The output formatter's `reorderProxyGroupFields` extends to handle the lb field positions (`url`, `interval`, `lazy`, `strategy`, `timeout`, `max-failed-times` — the order in spec FR-006 / the user's example).

The merge transformation core stays pure. Determinism is preserved: `LoadBalanceParams` is config-time-fixed, every emitted lb group is a pure function of `(members, lbParams)`. Snapshots refresh deterministically — one per-fixture diff containing exactly: new lb groups in `proxy-groups:`, new `via_lb_*` copies in `proxies:`, new lb-group references in the `Proxies` selector. Existing url-test groups, the `Proxies` selector's pre-existing entries, custom rules, and operator-declared own-groups are byte-unchanged.

## Technical Context

**Language/Version**: Go 1.25 toolchain (declared 1.22+) — unchanged
**Primary Dependencies**: existing — no new Go deps. Mihomo's `load-balance` group type is wire-format-only; no client-side or runtime dependency on this end.
**Storage**: N/A — values are env-var-derived at startup, not persisted.
**Testing**: existing — `go test`, `bradleyjkemp/cupaloy/v2` snapshots; new unit tests for `LoadBalanceParams.{Load,Validate}` + the strategy enum + per-field emission + the extended fan-out predicate; existing snapshot tests pick up the deterministic diff.
**Target Platform**: same as the rest of the server (Linux, Kubernetes); no platform-specific bits.
**Project Type**: Single Go module.
**Performance Goals**: emission is O(groups) constant-time additions and the fan-out doubles its output volume per (own × lb-group) pair. With ≤10 own-proxies × ≤30 url-test groups → ≤30 lb-groups, additional fan-out stays within 008's existing 50ms budget.
**Constraints**: deterministic given fixed inputs (FR-008); loud-fail at startup on invalid env vars (FR-005 per Constitution Principle III).
**Scale/Scope**: same handful of upstream subscriptions; ~10–50 url-test region groups → matching count of lb region groups; ≤7 continent groups → matching count of lb continent groups.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Justification |
|-----------|--------|---------------|
| **I. Unified Transformation Core** | PASS | Change is in `internal/merge/region.go` + `internal/merge/fanout.go` — both feed a single `MergedConfig`. lb groups and fan-out copies are emitted into the YAML node tree the same way for both subscription mode (only mode shipped today) and any future override-mode adapter. |
| **II. Deterministic Transformation** | PASS | `LoadBalanceParams` is read from env at startup and held immutable for the pod's lifetime. Emitted YAML is byte-identical for the same fixture across runs. Snapshot tests inject a fixed `LoadBalanceParams` so determinism is testable. The paired emission order (`_region_<CC>` then `_lb_region_<CC>`) is sequential and stable. The fan-out's outer loop (own-proxies in slice order) and inner loop (target groups in `mergedGroups` order) are unchanged; widening the prefix predicate adds entries to the inner loop in the same deterministic order. |
| **III. CSV Rules — Strict Schema, Loud Failure** | PASS — applied to env vars | FR-005 applies the loud-fail principle to env-var validation: invalid integer / boolean / strategy-enum values cause startup abort with a structured error log naming the offending env var and value. Errors accumulate (matching 012's pattern) so the operator sees all problems at once. No silent fallbacks. |
| **IV. Test-First, Real-Input Integration (NON-NEGOTIABLE)** | PASS | Tests written first per Phase plan: `LoadBalanceParams.{Load,Validate}` table tests over valid + invalid values (including the new strategy enum); `newLoadBalanceGroup` field-shape test; `AppendRegionGroups`/`AppendContinentGroups` paired-emission tests; `AppendFanoutProxies` widened-predicate test producing `via_lb_*` copies; existing snapshot suite catches end-to-end byte-stability after the snapshot refresh. |
| **V. Observable Routing & Source-Merge Decisions** | PASS — extended | FR-009 adds a startup `slog.Info` line listing the resolved `url`, `interval`, `timeout`, `max-failed-times`, `lazy`, `strategy` for the lb parameter set (distinct from 012's url-test log line so operators can grep for either). `AppendFanoutProxies`'s existing structured `fanout-emitted` log line continues to fire; its `target_group_count` doubles to reflect the additional lb groups, surfacing the change without a code edit. |
| **Routing — Corporate isolation** | PASS — N/A | No routing-rule change. |
| **Routing — multi-subscription collision resolution** | PASS — N/A | No collision-resolution change. The new fan-out names (`via_lb_*`) live in a separate namespace and cannot collide with upstream-prefixed or own-prefixed names (analyzed in 008). |
| **Routing — fetch failure modes** | PASS — preserved | No fetch-layer change. |
| **Security — Secrets boundary** | PASS — N/A | No new credential / token. The `LOAD_BALANCE_URL` is operator-chosen and reviewable. |
| **Security — Sanitized output** | PASS — preserved | No upstream credential or URL leaks; `LOAD_BALANCE_URL` is the operator's own probe URL, not an upstream one. |
| **Security — CSV is reviewable, not secret** | PASS — N/A | No CSV change. |
| **Snapshot stability gate** | PASS | Snapshot diffs are deterministic and confined to: new `_lb_*` group blocks in `proxy-groups:`, new `via_lb_*` entries in `proxies:`, new `_lb_*` references in the `Proxies` selector's `proxies:` list. PR description spells out the expected diff so reviewers know the snapshot bytes are intentional. |
| **Diff-reviewable changes** | PASS | One PR; files affected listed in Project Structure. |
| **Both modes covered, every change** | PASS — scope-limited | Override-mode adapter does not yet exist in the repo. Region/continent groups (and now lb siblings) are produced once on the merge layer and consumed by whichever adapter renders them; the future override adapter inherits the new shape automatically. |
| **Simplicity bias** | PASS | One new struct, one new builder method, one helper function (`newLoadBalanceGroup`), two ~10-line additions in `AppendRegionGroups`/`AppendContinentGroups`, one one-line predicate widening in `AppendFanoutProxies`, one extension to the existing field-ordering pass, one env-var loader block mirroring 012's exactly. No new packages, no abstractions, no plugin layers. |

### Complexity Tracking

No violations. Plan follows the existing `Pipeline.WithXxx` config-builder pattern, the existing env-var plumbing pattern from 003 / 010 / 012, and the existing fan-out machinery from 008.

## Project Structure

### Documentation (this feature)

```text
specs/014-load-balance-region-groups/
├── plan.md                                    # This file
├── research.md                                # Phase 0 — design decisions
├── data-model.md                              # Phase 1 — LoadBalanceParams + emit shape
├── contracts/
│   └── served-subscription.changes.md         # Phase 1 — delta vs current served format
├── quickstart.md                              # Phase 1 — operator verification + troubleshooting
├── checklists/
│   └── requirements.md                        # already created by /speckit-specify
└── tasks.md                                   # Phase 2 output (/speckit-tasks — NOT created here)
```

### Source Code (repository root)

```text
honkai-rule-server/
├── internal/
│   ├── config/
│   │   ├── server.go                          # MODIFY — add LoadBalanceParams struct + LOAD_BALANCE_* env-var loader
│   │   └── server_test.go                     # MODIFY — table tests for env load + validate (valid + invalid, incl. strategy enum)
│   ├── merge/
│   │   ├── pipeline.go                        # MODIFY — add WithLoadBalanceParams; thread to AppendRegionGroups/AppendContinentGroups
│   │   ├── pipeline_test.go                   # MODIFY — assert lb groups appear paired with url-test groups in mergedGroups
│   │   ├── region.go                          # MODIFY — add LoadBalanceParams (mirror struct), newLoadBalanceGroup, paired emission in AppendRegionGroups + AppendContinentGroups
│   │   ├── region_test.go                     # MODIFY — assert paired emission and lb field shape
│   │   ├── fanout.go                          # MODIFY — extend prefix predicate to include `_lb_region_` / `_lb_continent_`
│   │   └── fanout_test.go                     # MODIFY — assert via_lb_* fan-out copies generated alongside via_region_*/via_continent_*
│   ├── output/
│   │   ├── subscription_mode.go               # MODIFY — extend reorderProxyGroupFields with the lb field positions (url/interval/lazy/strategy/timeout/max-failed-times)
│   │   └── subscription_mode_test.go          # MODIFY — assert field ordering for load-balance groups (distinct from url-test order)
│   ├── server/app.go                          # MODIFY — startup slog.Info of resolved LoadBalanceParams (FR-009)
│   └── integration/
│       └── testdata/snapshots/                # MODIFY — refresh subscription-mode snapshots that include _region_*/_continent_* (now also their _lb_* siblings + via_lb_* fan-out)
└── specs/014-load-balance-region-groups/      # documentation tree above
```

**Structure Decision**: Single project, no new packages. Change rides the existing `Pipeline` builder pattern + existing env-var plumbing + existing fan-out machinery. Output formatting changes are localized to `subscription_mode.go`'s field-ordering pass. The struct duplication between `config.LoadBalanceParams` and `merge.LoadBalanceParams` mirrors 012's existing `URLTestParams` duplication (Constitution Principle I — keeps `internal/merge` free of `internal/config` imports).

## Phase 0: Outline & Research

The spec leaves no `[NEEDS CLARIFICATION]` markers. The Phase 0 deliverable documents nine narrow design decisions:

1. **Env-var schema (`LoadBalanceParams`)**: Six separate env vars per FR-004 — one per Mihomo field. Each parsed with the existing `env.Getenv` pattern in `internal/config/server.go::Load()`. Defaults applied when unset / empty (matching 012's `URL_TEST_*` "empty → default" semantic). Rationale: matches project's strong existing pattern; per-knob tunability without a custom parser; flat Helm `values.yaml`. Separate namespace (`LOAD_BALANCE_*` not aliased onto `URL_TEST_*`) per spec Assumptions — the two probe sets describe semantically different behaviors with diverging tuned defaults (interval 300 vs 10, timeout 1500 vs 3000).

2. **Units explicit in env names**: `LOAD_BALANCE_INTERVAL_SECONDS` and `LOAD_BALANCE_TIMEOUT_MS` carry their unit in the name, mirroring 012. Mihomo's own field names (`interval` / `timeout`) are unit-asymmetric; the env layer adds clarity, the YAML layer keeps the standard name.

3. **Strategy enum validation**: `LOAD_BALANCE_STRATEGY` accepts exactly `round-robin`, `consistent-hashing`, or `sticky-sessions` (the three Mihomo-defined values). Invalid values cause startup abort. Implemented as a string-set check in `LoadBalanceParams.Validate()`. Rationale: matches Mihomo's wire format exactly; keeps the operator surface tight without locking out future strategy users.

4. **Validation = startup-fatal (Constitution Principle III)**: Validation accumulates errors into a single per-namespace error message (matching 012's `URLTestParams validation failed: ...` format). Rationale: bad probe config is operator-visible at the next routine restart, not silently masked; accumulation lets operators fix all knobs in one redeploy cycle.

5. **Plumbing = `Pipeline` builder**: Add `Pipeline.WithLoadBalanceParams(p LoadBalanceParams) *Pipeline` mirroring `WithURLTestParams`. The struct is held on `Pipeline` and passed by value into `AppendRegionGroups` / `AppendContinentGroups` via an additional parameter. Rationale: matches existing builder convention; keeps emit functions pure (params in, nodes out).

6. **Paired emission ordering** (FR-013): For every `_region_<CC>` group emitted, the lb sibling `_lb_region_<CC>` is appended to `groups` immediately after, inside the same loop iteration in `AppendRegionGroups`. Same pattern for continents in `AppendContinentGroups`. The `Proxies` selector member-list addition runs once at the end of each function, with both `_region_*` and `_lb_region_*` names appended in interleaved order. Rationale: paired adjacency in `proxy-groups:` makes the served YAML readable (operators see "JP url-test" / "JP load-balance" side by side); no separate accumulator is needed.

7. **Fan-out prefix widening** (FR-014a): Single change in `AppendFanoutProxies` — the `strings.HasPrefix(name, "_region_") || strings.HasPrefix(name, "_continent_")` check becomes `strings.HasPrefix(name, "_region_") || strings.HasPrefix(name, "_continent_") || strings.HasPrefix(name, "_lb_region_") || strings.HasPrefix(name, "_lb_continent_")`. The existing `stripUnderscore(groupName)` then naturally yields `lb_region_JP` for `_lb_region_JP`, producing `via_lb_region_JP__<own>` per the spec's example. The `dialer-proxy` is set to the full `_lb_region_JP` group name. Rationale: minimal one-line change; reuses 100% of 008's machinery; outer/inner loop ordering unchanged; deterministic.

8. **YAML field order for load-balance groups** (FR-006): After 004's existing `name, type, proxies` ordering, the six lb fields render in the order from FR-006 / the user's example: `url, interval, lazy, strategy, timeout, max-failed-times`. This differs from url-test order (`url, interval, timeout, max-failed-times, lazy`) — `lazy` and `strategy` come earlier in lb. Implemented by extending `reorderProxyGroupFields` in `subscription_mode.go` to call `moveFieldToPosition(n, "strategy", N)` for an appropriate target position, and to use a single ordered list of field names that covers both url-test and load-balance groups (since `moveFieldToPosition` is a no-op when the key is absent, listing both groups' fields in one pass is safe). Rationale: preserves 004's readability contract; matches exactly the shape the user wrote.

9. **Snapshot test refresh strategy**: Run `UPDATE_SNAPSHOTS=true` after the implementation lands, inspect the resulting diff to confirm it's confined to the documented additions (lb groups, lb fan-out copies, lb references in `Proxies` selector), commit. PR description spells out the expected diff so reviewers know the snapshot bytes are intentional.

**Output**: `research.md` documenting the nine decisions with rationale + rejected alternatives.

## Phase 1: Design & Contracts

**Prerequisites**: `research.md` complete

### Data Model

`data-model.md` covers:

- **`config.LoadBalanceParams`** (new struct in `internal/config/server.go`):
  ```go
  type LoadBalanceParams struct {
      URL             string // YAML "url".              Default https://www.gstatic.com/generate_204.
      IntervalSeconds int    // YAML "interval".         Default 300. Must be >= 1.
      TimeoutMS       int    // YAML "timeout".          Default 1500. Must be >= 1.
      MaxFailedTimes  int    // YAML "max-failed-times". Default 3. Must be >= 1.
      Lazy            bool   // YAML "lazy".             Default true.
      Strategy        string // YAML "strategy".         Default "round-robin"; one of round-robin/consistent-hashing/sticky-sessions.
  }
  ```
  Loaded by `Load()` from the six env vars in FR-004; validated per FR-005. Held on `ServerConfig.LoadBalanceParams`.

- **`merge.LoadBalanceParams`** (new struct in `internal/merge/region.go`, mirroring `merge.URLTestParams`'s pattern): identical fields, present so `internal/merge` stays free of `internal/config` imports per Constitution Principle I.

- **`Pipeline.loadBalanceParams`** (new field on `merge.Pipeline`, default zero-value): populated via the new builder method `WithLoadBalanceParams(p LoadBalanceParams) *Pipeline`. Threaded into `AppendRegionGroups` and `AppendContinentGroups` alongside the existing `urlTestParams`.

- **Emit shape** (per lb group): the existing `name`, `type`, `proxies` triple, with `type` set to `"load-balance"` and six new key/value pairs (`url`, `interval`, `lazy`, `strategy`, `timeout`, `max-failed-times`) in that order.

### Contracts

`contracts/served-subscription.changes.md` covers:

- **What changes**: every `_region_<CC>` / `_continent_<CONT>` / `_region_UNKNOWN` group in `proxy-groups:` gains a `_lb_<...>` sibling immediately after; every own-proxy without an explicit `dialer-proxy` gains additional `via_lb_region_<CC>__<own>` / `via_lb_continent_<CONT>__<own>` entries in `proxies:`; the always-present `Proxies` selector group's member list gains every `_lb_*` group as a direct entry.
- **What does not change**: every existing `_region_*` / `_continent_*` group's bytes; the `Proxies` selector's pre-existing entries (existing region/continent group references, upstream proxies, own-groups); operator-defined custom proxy groups; existing `via_region_*` / `via_continent_*` / `via_AUTO__*` entries; rules; `Subscription-Userinfo` header; `/health` JSON.
- **Wire format**: stock Mihomo / Clash already supports `type: load-balance` with the six fields; the on-the-wire YAML stays valid.

### Quickstart

`quickstart.md` covers (operator-facing):

1. **Verify the served body**: `curl -fsS -A "Bronya/1.0" https://example.com/<prefix>/?token=<TOKEN> | yq '.proxy-groups[] | select(.name | startswith("_lb_"))'` — every lb group reports `type: load-balance` and the six fields.
2. **Verify the operator config**: `kubectl logs deploy/honkai-rule-server -n cms | grep load_balance` — startup line lists the resolved `url`, `interval`, `timeout`, `max-failed-times`, `lazy`, `strategy`.
3. **Override a parameter**: bump `strategy` via the chart's env block (`LOAD_BALANCE_STRATEGY=consistent-hashing`), redeploy, smoke-test the new value appears.
4. **Verify the fan-out**: `curl -fsS … | yq '.proxies[] | select(.name | test("^via_lb_"))'` — each entry has `dialer-proxy: _lb_region_<CC>` (or `_lb_continent_<CONT>`).
5. **Real-client load-balance smoke test**: with `_lb_region_JP` selected (or addressed via a custom rule), open many parallel connections; observe Mihomo client logs / dashboard reporting different first-hop nodes per connection.
6. **Troubleshoot bad config**: malformed env var (e.g., `LOAD_BALANCE_STRATEGY=random`) → pod fails to start → `kubectl describe pod` shows the structured error log identifying the offending env var; correct it in the chart, redeploy.

### Agent context update

Update the lines between `<!-- SPECKIT START -->` and `<!-- SPECKIT END -->` in `CLAUDE.md`:
- Add **014 (load-balance-region-groups)** as the active feature, with a one-line summary pointing at this plan.
- Add a key-reading bullet pointing at `specs/014-load-balance-region-groups/plan.md`.

## Phases (after this command)

This command stops here. Next: `/speckit-tasks` produces `tasks.md` with the dependency-ordered task list (test-first per Constitution Principle IV: env-var validation tests → `LoadBalanceParams` impl → emit-site tests → emit-site impl → fan-out predicate test → fan-out predicate impl → field-ordering test → field-ordering impl → startup-log → snapshot refresh → `make check`).
