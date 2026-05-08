# Implementation Plan: Dialer-Proxy Fan-Out for Own Proxies

**Branch**: `008-dialer-proxy-fanout` | **Date**: 2026-05-01 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/008-dialer-proxy-fanout/spec.md`

## Summary

For every operator-declared own-proxy in `own-proxies.yaml`, emit one fan-out copy per server-emitted region/continent proxy group plus one AUTO variant — generated proxies named `via_<G>__<P>` (or `via_AUTO__<P>`) carry the source own-proxy's connection fields verbatim plus a `dialer-proxy` field referring back to the target group (or to the always-present `Proxies` selector for AUTO). Concurrently, exclude own-proxies and every `via_*` fan-out copy from the membership list of the always-present `Proxies` selector group so the global picker stays focused on upstream pools and region/continent groups.

The implementation lives in a new `internal/merge/fanout.go` module (one pure function `AppendFanoutProxies`), wired into `Pipeline.Build()` after the region/continent group passes, plus a one-line filter change at the `AppendProxiesGroup` call site to drop own-proxy names from the global `Proxies` member list. No new config files or env vars; no changes to fetch, customrules, output, or server packages beyond snapshot regeneration.

## Technical Context

**Language/Version**: Go 1.25 toolchain (declared 1.22+)
**Primary Dependencies**: `gopkg.in/yaml.v3` (existing), `log/slog` (stdlib, existing) — no new deps
**Storage**: N/A (in-memory transform)
**Testing**: `go test`, `bradleyjkemp/cupaloy/v2` for snapshot tests (existing); `make check` runs vet + staticcheck + tests + snapshot drift
**Target Platform**: Linux server (Kubernetes-deployable per 001)
**Project Type**: Single Go module with the merge layer as the unified transformation core (Constitution Principle I)
**Performance Goals**: ≤50 ms additional build time for ≤310 fan-out copies (≤10 own × ≤30 region/continent + AUTO) — see SC-006
**Constraints**: Pure-functional merge layer (no I/O, no time.Now, no map iteration order assumptions); deterministic byte output; snapshot-stable across reloads
**Scale/Scope**: Typical operator: 2–5 own-proxies × 15–20 region/continent groups + 1 AUTO ≈ 30–105 fan-out copies per build

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Justification |
|-----------|--------|---------------|
| **I. Unified Transformation Core** | PASS | Fan-out lives in `internal/merge/`; both subscription and override modes consume the same `MergedConfig.Proxies` slice. The output adapter is unchanged — it just sees more proxies. No mode-only logic introduced. |
| **II. Deterministic Transformation** | PASS | The new `AppendFanoutProxies` is pure: takes own-proxies + merged groups + Proxies group name, returns extra `*yaml.Node` slice. Outer loop iterates own-proxies in declaration order (preserved from `own-proxies.yaml`); inner loop emits AUTO first, then `_region_*`/`_continent_*` in their `mergedGroups` order (which is itself deterministic per 002/003). No `map` iteration without sort; no clock; no I/O. |
| **III. CSV Rules** | N/A | Feature does not touch rule loading or CSV parsing. |
| **IV. Test-First, Real-Input Integration (NON-NEGOTIABLE)** | PASS — with discipline | Plan declares test-first ordering (Phase 2 starts with new unit tests `internal/merge/fanout_test.go` + integration tests `internal/integration/pipeline_test.go::TestI_008_*`); snapshot regeneration is a deferred final step (one commit), not a continuous accommodation. Both subscription-mode and override-mode snapshots must re-baseline in the same PR. |
| **V. Observable Routing & Source-Merge Decisions** | PASS | A single `slog.Info` line at fan-out time per build with structured fields `event=fanout-emitted`, `own_proxy_count`, `target_group_count`, `emitted_count`, `skipped_due_to_explicit_dialer` (count of own-proxies bypassed per FR-005). No per-fan-out-copy log spam — that would be `O(N×M)` noise. |
| **Routing — multi-subscription collision resolution** | PASS — unchanged | The 002 prefix scheme and 001 `<name>@<source>` defense-in-depth path are untouched. Fan-out names live in a fresh `via_*` namespace that does not collide with upstream-prefixed (`<lowercase>_*`) or own-prefixed (`_*`) proxies. |
| **Routing — fetch failure modes** | PASS — unchanged | Fan-out is downstream of the fetch layer; failures bypass the merge entirely (sources without cached payload are skipped, per existing pipeline behavior). |
| **Security — sanitized output** | PASS | Each fan-out copy is a `cloneNode()` of an own-proxy mapping with `name` rewritten and `dialer-proxy` set. Cloning carries existing fields verbatim — same secret-handling boundary as the original own-proxies (Constitution Security: subscription URLs / exit-proxy creds live in env, not in YAML; own-proxies fields are operator-supplied to the YAML and emitted as-is, which is the same status quo as today). |
| **Snapshot stability gate** | PASS — drift expected and intentional | The integration snapshot at `internal/integration/testdata/snapshots/served-config.snap.yaml` will gain ~34 fan-out entries (2 own × 16 region/continent + 2 AUTO) and lose 2 own-proxy entries from the `Proxies` group. The PR description must call this out and re-baseline with `UPDATE_SNAPSHOTS=true`. |
| **Diff-reviewable changes** | PASS | All new logic in one file (`fanout.go`) plus one wire-up edit in `pipeline.go`; clearly diff-able alongside the snapshot delta. |
| **Both modes covered, every change** | PASS | `MergedConfig.Proxies` is the shared input to both delivery modes; the override-mode snapshot (if separately maintained) must re-baseline together with subscription mode in the same PR. |
| **Simplicity bias** | PASS | One new file, one new function, one filter line, no new abstractions. Three similar lines accepted over inventing a "FanoutStrategy" plugin. |

No Complexity Tracking entries required — all gates pass.

## Project Structure

### Documentation (this feature)

```text
specs/008-dialer-proxy-fanout/
├── plan.md              # This file
├── research.md          # Phase 0 — design decisions, alternatives considered
├── data-model.md        # Phase 1 — Fan-out Proxy entity, AUTO variant, ordering invariants
├── quickstart.md        # Phase 1 — operator-facing guide (how to use via_* names in custom rules)
├── contracts/
│   └── served-subscription.changes.md  # Diff vs prior contract (proxies+ via_*; Proxies group filter)
└── tasks.md             # Phase 2 — produced by /speckit-tasks (NOT created by /speckit-plan)
```

### Source Code (repository root)

```text
honkai-rule-server/
├── cmd/
│   └── server/
│       └── main.go                         # UNCHANGED
├── internal/
│   ├── auth/                                # UNCHANGED
│   ├── clock/                               # UNCHANGED
│   ├── config/                              # UNCHANGED (own-proxies.yaml schema unchanged)
│   ├── customrules/                         # UNCHANGED
│   ├── fetcher/                             # UNCHANGED
│   ├── merge/
│   │   ├── fanout.go                        # NEW — AppendFanoutProxies + helpers
│   │   ├── fanout_test.go                   # NEW — unit tests TC-U-FANOUT-*
│   │   ├── pipeline.go                      # MODIFY — wire AppendFanoutProxies after region/continent passes; filter own-proxies from Proxies group's allNames
│   │   ├── pipeline_test.go                 # MODIFY — update existing assertions if any reference Proxies-group membership of own-proxies
│   │   ├── namespace.go                     # UNCHANGED (RewriteOwn is the upstream of fan-out)
│   │   ├── proxies.go                       # UNCHANGED
│   │   ├── proxy_groups.go                  # UNCHANGED (AppendProxiesGroup contract preserved)
│   │   ├── region.go                        # UNCHANGED (AppendRegionGroups / AppendContinentGroups identify the targets fan-out reads)
│   │   ├── rules.go                         # UNCHANGED
│   │   ├── traffic.go                       # UNCHANGED
│   │   └── yamlutil.go                      # UNCHANGED (cloneNode / setMappingValue reused)
│   ├── integration/
│   │   ├── pipeline_test.go                 # MODIFY — add TC-I-008-01..05 (fan-out generation; Proxies-group exclusion; AUTO present; own-proxy with explicit dialer-proxy skipped; determinism)
│   │   └── testdata/snapshots/
│   │       └── served-config.snap.yaml      # MODIFY (regenerate with UPDATE_SNAPSHOTS=true) — gains 34 via_* entries; Proxies group loses 2 own-proxy entries
│   ├── observability/                       # UNCHANGED
│   ├── output/                              # UNCHANGED (subscription_mode and override_mode pass through MergedConfig.Proxies untouched)
│   └── server/                              # UNCHANGED
└── config/
    ├── own-proxies.yaml                     # UNCHANGED on disk; semantics extended (operator's existing entries get fanned out automatically)
    └── ...                                  # UNCHANGED
```

**Structure Decision**: Keep the change inside `internal/merge/`. The pure-functional merge layer already owns the namespace rewrite (002), the region/continent group emission (002/003), and the rule unification (005). Fan-out is the next pure pass in the same chain. The only call-site change in `pipeline.go` is one new function call plus a tightened filter on the `allNames` slice; everything else (output adapter, server, fetch) sees an unchanged contract — `MergedConfig.Proxies` simply contains more entries.

## Phase 0: Outline & Research

The spec leaves no `[NEEDS CLARIFICATION]` markers. Research decisions are documented below for the planner record; their full form lives in `research.md`.

1. **Where does fan-out happen in the pipeline?** Decision: in `Pipeline.Build()`, *after* `AppendProxiesGroup` + `AppendRegionGroups` + `AppendContinentGroups` and *before* `return &MergedConfig{...}`. Rationale: needs the post-region/continent `mergedGroups` to enumerate `_region_*`/`_continent_*` targets; the resulting `via_*` proxies must NOT be added to the `Proxies` selector (FR-008), so emitting them after `AppendProxiesGroup` runs is the simplest exclusion. Alternatives considered: (a) Add fan-out inside `MergeProxies` — rejected, would force `MergeProxies` to know about region/continent groups (a layering inversion); (b) Add fan-out as a post-pipeline output-adapter step — rejected, breaks Constitution Principle I (one transformation core, both modes); (c) Compute fan-out from raw `own-proxies.yaml` before `RewriteOwn` — rejected, names would lack the `_` prefix and `dialer-proxy: _region_JP` would not match the post-rewrite group names.

2. **How are own-proxies detected to skip fan-out (FR-005)?** Decision: scan each own-proxy's mapping for a `dialer-proxy` key using `getMappingField(node, "dialer-proxy") != ""`. Rationale: matches the existing helper used everywhere in the merge layer; pure string check on the parsed node. Alternatives: regex on YAML bytes (rejected — we have a parsed node), or maintain a separate "skip" list (rejected — single source of truth in the YAML).

3. **Naming of fan-out copies (FR-002, FR-004a)**: Strip exactly one leading `_` from group name; strip exactly one leading `_` from own-proxy name; concatenate as `via_<stripped-group>__<stripped-own>`. AUTO uses literal `AUTO` for `<stripped-group>` and `Proxies` (no underscore prefix — that's the global selector's actual name) for the `dialer-proxy` value.

4. **Field copy semantics (FR-003)**: Use existing `cloneNode()` helper from `yamlutil.go`. After clone, `setMappingValue(clone, "name", ...)` to overwrite the name; `setMappingValue(clone, "dialer-proxy", ...)` to add or replace dialer-proxy. The mapping's existing field order is preserved by `setMappingValue` semantics (append if absent, replace in place if present). One known gap: `setMappingValue` does not preserve YAML comments/anchors/tags — already documented in the spec's Assumptions section as accepted behavior consistent with the rest of the merge layer.

5. **`Proxies` group exclusion (FR-007/-008)**: Filter the `allNames` slice in `Pipeline.Build()` (the source for `AppendProxiesGroup`) to skip any name starting with `_`. The `via_*` names are not in `mergedProxies` at the time of the filter call (fan-out runs later), so they're naturally excluded. The existing `AppendRegionGroups`/`AppendContinentGroups` paths only ever add `_region_*`/`_continent_*` group names to the `Proxies` member list — never own-proxies, never via_* — so no further changes there.

6. **Edge case: Proxies group already exists from an upstream/own contribution.** `AppendProxiesGroup` augments existing group's members with the new ones (set union). If an upstream's own `Proxies` group already contained `_my-home-trojan` (essentially impossible unless the operator fabricated one), the union would re-add the entry. Acceptance: this is the existing behavior; the feature's filter applies only to the names we *contribute*, not to names that pre-exist in the Proxies group from upstream/own sources. Documenting in research.md.

7. **Logging cadence (Constitution V)**: One `slog.Info` line per `Build()` invocation summarizing fan-out activity. Per-copy logging would emit 30–100+ lines per merge, drowning out signal. The summary line includes counts and the count of own-proxies skipped per FR-005, which is the actionable observability signal.

**Output**: `research.md` documenting the seven decisions above with rationales and rejected alternatives.

## Phase 1: Design & Contracts

**Prerequisites**: `research.md` complete

### Data Model

`data-model.md` will document:

- **Fan-out Proxy**: a `*yaml.Node` whose mapping fields are: `name` (string `via_<G>__<P>`), `dialer-proxy` (string — full target group name with leading underscore, OR literal `Proxies` for AUTO), plus every other field from the source own-proxy verbatim (server, port, type, password, cipher, etc.). Field order: source mapping order with `name` substituted in place; `dialer-proxy` appended at end.
- **AUTO Fan-out Proxy**: same shape but with name `via_AUTO__<P>` and `dialer-proxy: Proxies`.
- **Ordering invariants**: outer loop = own-proxies in `own-proxies.yaml` declaration order; inner loop = AUTO first, then each target group in `mergedGroups` declaration order (which is upstream/own first, then `_region_*` alphabetical by CC, then `_region_UNKNOWN`, then `_continent_*` alphabetical by CONT — the order produced by 002/003).
- **Pipeline relationship**: `Build()` flow now includes a `fanoutProxies := AppendFanoutProxies(rewrittenOwnProxies, mergedGroups, p.proxiesGroupName)` step, with `mergedProxies = append(mergedProxies, fanoutProxies...)` before constructing `MergedConfig`.

### Contracts

`contracts/served-subscription.changes.md` will document the diff against the prior subscription-mode contract:

- **`proxies:` block**: gains N×(M+1) entries per build (where N = own-proxies without explicit `dialer-proxy`, M = count of `_region_*`/`_continent_*` groups). Each new entry is named `via_(region|continent)_<…>__<own>` or `via_AUTO__<own>`. Each carries the source own-proxy's connection fields plus `dialer-proxy: <full-group-name | "Proxies">`.
- **`proxy-groups:` block**: unchanged at the group level. The always-present `Proxies` group's `proxies:` member list loses every entry whose name starts with `_<not-region/continent>` (own-proxies) and never gains a `via_*` entry.
- **`rules:` block**: unchanged. (Custom rules from 003 may still target fan-out names directly — Mihomo resolves any `proxies:`-declared name.)

### Quickstart

`quickstart.md` will explain to operators:

1. Existing `own-proxies.yaml` setup (unchanged — see 001 quickstart).
2. The new `via_*` proxy names and how to discover them (`curl <url>/<token>/config | yq '.proxies[].name | select(test("^via_"))'`).
3. How to route traffic through them via custom rules (003 schema unchanged):  example: `RULE-TYPE,DOMAIN-SUFFIX,example.com,via_region_JP__markham`.
4. The opt-out: setting `dialer-proxy:` on an own-proxy in `own-proxies.yaml` skips fan-out for that proxy.
5. Why the global `Proxies` selector no longer shows your own-proxies (and where to find them: own-groups, custom rules, fan-out copies).

### Agent context update

Update the active-feature pointer in `CLAUDE.md` between the `<!-- SPECKIT START -->` and `<!-- SPECKIT END -->` markers (or, if those markers don't exist in this repo's CLAUDE.md, update the inline reference) to point to `specs/008-dialer-proxy-fanout/plan.md`.

## Complexity Tracking

> Constitution Check passed cleanly. No entries.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| _none_ | _N/A_ | _N/A_ |
