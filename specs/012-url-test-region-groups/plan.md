# Implementation Plan: URL-Test for Auto-Emitted Regional & Continent Proxy Groups

**Branch**: `012-url-test-region-groups` | **Date**: 2026-05-02 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/012-url-test-region-groups/spec.md`

## Summary

Two-line change in `internal/merge/region.go` (`type: "select"` → `type: "url-test"` on the two emit sites for `_region_*` and `_continent_*` groups), plus five new fields on each emitted group (`url`, `interval`, `timeout`, `max-failed-times`, `lazy`). Values come from a new `URLTestParams` struct populated by `internal/config/server.go::Load()` from five env vars (per the spec's FR-004 table) with the spec's defaults. The struct flows Pipeline → `AppendRegionGroups` / `AppendContinentGroups` via a new `WithURLTestParams` builder method. Output formatter (`internal/output/subscription_mode.go::reorderProxyGroupFields`) gets a small extension to position the five new fields after `proxies` in the documented order.

The merge transformation core stays pure. Determinism is preserved: `URLTestParams` is config-time-fixed; the served YAML for any region/continent group is a pure function of (members, params). Snapshots refresh deterministically — one diff per `_region_*` / `_continent_*` group, no body bytes change outside those group blocks. The always-present `Proxies` selector group and any operator-defined custom proxy groups are untouched.

## Technical Context

**Language/Version**: Go 1.25 toolchain (declared 1.22+) — unchanged
**Primary Dependencies**: existing — no new Go deps. Mihomo's `url-test` group type is wire-format-only; no client-side or runtime dependency on this end.
**Storage**: N/A — values are env-var-derived at startup, not persisted.
**Testing**: existing — `go test`, `bradleyjkemp/cupaloy/v2` snapshots; new unit tests for `URLTestParams.{Load,Validate}` + per-field emission; existing snapshot tests pick up the deterministic diff.
**Target Platform**: same as the rest of the server (Linux, Kubernetes); no platform-specific bits.
**Project Type**: Single Go module.
**Performance Goals**: emission is O(groups) constant-time additions. No measurable hot-path impact.
**Constraints**: deterministic given fixed inputs (FR-009); loud-fail at startup on invalid env vars (FR-004a per Constitution Principle III).
**Scale/Scope**: same handful of upstream subscriptions; ~10–50 region groups in practice depending on provider geography; ≤7 continent groups.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Justification |
|-----------|--------|---------------|
| **I. Unified Transformation Core** | PASS | Change is in `internal/merge/region.go`, which feeds both subscription mode (only mode shipped) and any future override-mode adapter via a single `MergedConfig`. URL-test fields are emitted into the YAML group literals the same way for both modes. |
| **II. Deterministic Transformation** | PASS | `URLTestParams` is read from env at startup and held immutable for the pod's lifetime. Emitted YAML is byte-identical for the same fixture across runs. Snapshot tests inject a fixed `URLTestParams` so determinism is testable. |
| **III. CSV Rules — Strict Schema, Loud Failure** | PASS — applied to env vars | FR-004a applies the loud-fail principle to env-var validation: invalid integer / boolean values cause startup abort with a structured error log naming the offending env var and value. No silent fallbacks. |
| **IV. Test-First, Real-Input Integration (NON-NEGOTIABLE)** | PASS | Tests written first per Phase plan: `URLTestParams.{Load,Validate}` table tests over valid + invalid values; `AppendRegionGroups` / `AppendContinentGroups` emit-shape tests; existing snapshot suite catches end-to-end byte-stability after the snapshot refresh. |
| **V. Observable Routing & Source-Merge Decisions** | PASS — extended | FR-008 adds a startup `slog.Info` line listing the resolved `url`, `interval`, `timeout`, `max-failed-times`, `lazy`. No new credential or PII surface. |
| **Routing — Corporate isolation** | PASS — N/A | No routing-rule change. |
| **Routing — multi-subscription collision resolution** | PASS — N/A | No collision-resolution change. |
| **Routing — fetch failure modes** | PASS — preserved | No fetch-layer change. |
| **Security — Secrets boundary** | PASS — N/A | No new credential / token. The `URL_TEST_URL` is operator-chosen and reviewable. |
| **Security — Sanitized output** | PASS — preserved | No upstream credential or URL leaks; `URL_TEST_URL` is the operator's own probe URL, not an upstream one. |
| **Security — CSV is reviewable, not secret** | PASS — N/A | No CSV change. |
| **Snapshot stability gate** | PASS | Snapshot diffs are deterministic and confined to `_region_*` / `_continent_*` group blocks. PR description states "every region and continent group's `type` flips to `url-test` and gains five fields; nothing else changes." |
| **Diff-reviewable changes** | PASS | One PR; files affected listed in Project Structure. |
| **Both modes covered, every change** | PASS — scope-limited | Override-mode adapter does not yet exist in the repo. Region/continent groups are produced once on the merge layer and consumed by whichever adapter renders them; the future override adapter inherits the new shape automatically. |
| **Simplicity bias** | PASS | One new struct, one new builder method, two two-line changes in existing emit functions, one extension to the existing field-ordering pass. No new packages, no abstractions, no plugin layers. |

### Complexity Tracking

No violations. Plan follows the existing `Pipeline.WithXxx` config-builder pattern + the existing env-var plumbing pattern from 003 / 010.

## Project Structure

### Documentation (this feature)

```text
specs/012-url-test-region-groups/
├── plan.md                                    # This file
├── research.md                                # Phase 0 — design decisions
├── data-model.md                              # Phase 1 — URLTestParams + emit shape
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
│   │   ├── server.go                          # MODIFY — add URLTestParams struct, Load, Validate
│   │   └── server_test.go                     # MODIFY — table tests for env load + validate (valid + invalid)
│   ├── merge/
│   │   ├── pipeline.go                        # MODIFY — add WithURLTestParams; thread to AppendRegionGroups + AppendContinentGroups
│   │   ├── region.go                          # MODIFY — both emit sites: type=url-test + 5 new fields
│   │   └── region_test.go                     # MODIFY — assert emitted nodes carry the 5 new fields with passed-through values
│   ├── output/
│   │   ├── subscription_mode.go               # MODIFY — extend reorderProxyGroupFields for url/interval/timeout/max-failed-times/lazy
│   │   └── subscription_mode_test.go          # MODIFY — assert field ordering for url-test groups
│   ├── server/app.go                          # MODIFY — startup slog.Info of resolved URLTestParams (FR-008)
│   └── integration/
│       └── testdata/snapshots/                # MODIFY — refresh subscription-mode snapshots that include _region_* / _continent_* groups
└── specs/012-url-test-region-groups/          # documentation tree above
```

**Structure Decision**: Single project, no new packages. Change rides the existing `Pipeline` builder pattern + the existing env-var plumbing. Output formatting changes are localized to `subscription_mode.go`'s field-ordering pass.

## Phase 0: Outline & Research

The spec leaves no `[NEEDS CLARIFICATION]` markers. The Phase 0 deliverable documents seven narrow design decisions:

1. **Env-var schema (`URLTestParams`)**: Five separate env vars per FR-004 — one per Mihomo field. Each parsed with the existing `env.Getenv` pattern in `internal/config/server.go::Load()`. Defaults applied when unset / empty (matching `FALLBACK_RULE_TARGET`'s "empty → default" semantic from 010). Rationale: matches project's strong existing pattern; per-knob tunability without a custom parser; flat Helm `values.yaml`.

2. **Units explicit in env names**: `URL_TEST_INTERVAL_SECONDS` and `URL_TEST_TIMEOUT_MS` carry their unit in the name to avoid the operator-confusion-at-config-time pitfall. Mihomo's own field names (`interval` / `timeout`) are unit-asymmetric; the env layer adds clarity, the YAML layer keeps the standard name.

3. **Validation = startup-fatal (Constitution Principle III)**: `URLTestParams.Validate()` returns a typed error per offending field; `Load()` propagates it; `cmd/server/main.go` already exits non-zero on `Load()` errors. No silent fallbacks. Rationale: bad probe config is operator-visible at the next routine restart, not silently masked.

4. **Plumbing = `Pipeline` builder**: Add `Pipeline.WithURLTestParams(p URLTestParams) *Pipeline` mirroring `WithProxiesGroupName` / `WithFallbackRuleTarget`. The struct is held on `Pipeline` and passed by value into `AppendRegionGroups` / `AppendContinentGroups`. Rationale: matches existing builder convention; keeps emit functions pure (params in, nodes out).

5. **YAML field order**: After 004's existing `name, type, proxies` ordering, the five new fields render in the order from FR-003 / FR-007: `url, interval, timeout, max-failed-times, lazy`. Implemented by extending the existing `reorderProxyGroupFields` pass in `subscription_mode.go` to recognize these positions when `type=url-test`. Rationale: preserves 004's readability contract; reviewers see the same shape every time.

6. **Snapshot test refresh strategy**: Run `UPDATE_SNAPSHOTS=true` after the implementation lands, inspect the resulting diff to confirm it's confined to `_region_*` / `_continent_*` group blocks, commit. PR description spells out the expected diff so reviewers know the snapshot bytes are intentional.

7. **Future per-group override (out of scope)**: The plan deliberately does NOT support per-region or per-continent overrides of the five params. If future operator-config supports this, the natural extension point is to make `URLTestParams` a default + a per-CC override map. Today's defaults-only implementation does not foreclose that extension; the struct stays small and the override map can be added without breaking existing config.

**Output**: `research.md` documenting the seven decisions with rationale + rejected alternatives.

## Phase 1: Design & Contracts

**Prerequisites**: `research.md` complete

### Data Model

`data-model.md` covers:

- **`config.URLTestParams`** (new struct in `internal/config/server.go`):
  ```go
  type URLTestParams struct {
      URL             string // default "https://www.gstatic.com/generate_204"
      IntervalSeconds int    // default 10
      TimeoutMS       int    // default 3000
      MaxFailedTimes  int    // default 3
      Lazy            bool   // default true
  }
  ```
  Loaded by `Load()` from the five env vars in FR-004; validated by a new `Validate()` method per FR-004a. Held on `Server.URLTestParams`.

- **`Pipeline.urlTestParams`** (new field on `merge.Pipeline`, default zero-value-with-defaults): populated via the new builder method `WithURLTestParams(p URLTestParams) *Pipeline`. Threaded into `AppendRegionGroups` and `AppendContinentGroups`.

- **Emit shape** (per group): the existing `name`, `type`, `proxies` triple, with `type` now `"url-test"` and five new key/value pairs appended (`url`, `interval`, `timeout`, `max-failed-times`, `lazy`).

### Contracts

`contracts/served-subscription.changes.md` covers:

- **What changes**: every served `_region_*` / `_continent_*` group's `type` flips from `select` to `url-test`; five new fields appear after `proxies` in the documented order.
- **What does not change**: the `Proxies` always-present selector group; any operator-defined custom proxy groups; the membership lists of region / continent groups; the `_region_UNKNOWN` group's existence (follows the same prefix rule).
- **Wire format**: stock Mihomo / Clash already supports `type: url-test` with these five fields; the on-the-wire YAML stays valid.

### Quickstart

`quickstart.md` covers (operator-facing):

1. **Verify the served body**: `curl -fsS -A "Bronya/1.0" https://example.com/<prefix>/?token=<TOKEN> | yq '.proxy-groups[] | select(.name | startswith("_region_") or startswith("_continent_"))'` — every group reports `type: url-test` and the five fields.
2. **Verify the operator config**: `kubectl logs deploy/honkai-rule-server -n cms | grep url_test` — startup line lists the resolved `url`, `interval`, `timeout`, `max-failed-times`, `lazy`.
3. **Override a parameter**: bump `interval` via the chart's env block (`URL_TEST_INTERVAL_SECONDS=30`), redeploy, smoke-test the new value appears.
4. **Real-client failover smoke test**: with one region group's first member temporarily blocked by firewall rule, observe Mihomo client UI's region group switching to a healthy member within `interval × max-failed-times` seconds.
5. **Troubleshoot bad config**: malformed env var → pod fails to start → `kubectl describe pod` shows the structured error log identifying the offending env var; correct it in the chart, redeploy.

### Agent context update

Update the lines between `<!-- SPECKIT START -->` and `<!-- SPECKIT END -->` in `CLAUDE.md`:
- Mark **010 (daily-traffic-header)** as fully implemented (it is — live in production).
- Add **012 (url-test-region-groups)** as the active feature, with a one-line summary pointing at this plan.
- Add a key-reading bullet pointing at `specs/012-url-test-region-groups/plan.md`.
- (011 is parked — do NOT mark it active here; tracked by draft PR #11.)

## Phases (after this command)

This command stops here. Next: `/speckit-tasks` produces `tasks.md` with the dependency-ordered task list (test-first per Constitution Principle IV: env-var validation tests → `URLTestParams` impl → emit-site tests → emit-site impl → field-ordering test → field-ordering impl → startup-log → snapshot refresh → `make check`).
