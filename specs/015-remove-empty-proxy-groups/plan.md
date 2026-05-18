# Implementation Plan: Prune Empty Proxy-Groups for Mihomo Compatibility

**Branch**: `015-remove-empty-proxy-groups` | **Date**: 2026-05-17 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/015-remove-empty-proxy-groups/spec.md`

## Summary

The served Clash/Mihomo configuration can contain a proxy-group whose `proxies:`
member list is empty (an operator-declared own-group with no members, or an
upstream-contributed group whose members were all dropped during namespacing/merge).
Mihomo rejects the whole profile when any proxy-group has no members, so the client
fails to load. This feature adds a final pruning pass in the transformation core that
removes every empty proxy-group, drops the dangling member references those removals
leave behind, and redirects any routing rule whose target group was removed to the
configured fallback rule target — with the single exception that the always-present
`Proxies` selector is never removed.

Technical approach: a new pure function `PruneEmptyProxyGroups` in `internal/merge/`
runs at the end of `Pipeline.Build()`, after all group construction and fan-out. It
performs a single removal pass (per FR-005 — no cascading), a member-reference
cleanup pass, and a rule-target rewrite pass, and returns the pruned group set plus
the rewritten rule slice and a list of structured events for logging (Principle V).

## Technical Context

**Language/Version**: Go 1.25 toolchain (module declares Go 1.22+)  
**Primary Dependencies**: stdlib `net/http`, `log/slog`; `gopkg.in/yaml.v3` (proxy-group nodes are `*yaml.Node`); `bradleyjkemp/cupaloy/v2` (snapshot tests)  
**Storage**: N/A — pure in-memory transformation step; no new persisted state  
**Testing**: `go test` (unit + integration), `staticcheck`, `go vet`, cupaloy/integration snapshot drift check — all via `make check`  
**Target Platform**: Linux server container (`FROM scratch` runtime image)  
**Project Type**: Single-project web service (subscription-mode output adapter; override mode is a future adapter on the same core)  
**Performance Goals**: Per-request transform; pruning is O(G) for removal + O(G·M) for reference cleanup where G = proxy-group count (tens) and M = members per group — negligible against the existing merge cost  
**Constraints**: Output MUST stay byte-deterministic (Principle II) and byte-stable on the committed snapshot when no group is empty (FR-010, snapshot-stability gate)  
**Scale/Scope**: Tens of proxy-groups per served configuration; one new merge-core file plus one integration fixture/snapshot

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-checked after Phase 1 design.*

- **I. Unified Transformation Core** — PASS. Pruning is a single step inside the
  shared `merge` pipeline (`Pipeline.Build()`), not in an output adapter. Both
  subscription mode and the future override mode consume the already-pruned
  `MergedConfig`, so the behavior is mode-agnostic by construction. No forked logic.
- **II. Deterministic Transformation** — PASS. The prune function iterates the
  existing ordered `[]*yaml.Node` group slice and the ordered `[]string` rule slice;
  it introduces no maps-in-iteration, timestamps, or randomness. Given fixed inputs
  it produces byte-identical output. Verified by the snapshot-stability gate.
- **III. CSV Rules — Strict Schema, Loud Failure** — PASS (with note). Principle III
  binds *load-time* validation of CSV rule rows: a row naming a non-existent target
  group must loud-fail when the CSV is loaded. This feature does not relax that. A
  rule reaching the prune step is already well-formed and was valid when loaded; its
  target group existed and only became absent because the *server* pruned an empty
  group at serve time. Redirecting that rule to the fallback target (FR-008) is a
  deliberate, logged transformation (Principle V), not a silent best-effort skip.
  See `research.md` for the rule-target extraction approach.
- **IV. Test-First, Real-Input Integration (NON-NEGOTIABLE)** — PASS. Plan sequences
  unit tests for `PruneEmptyProxyGroups` (empty-group removal, the protected
  `Proxies` selector, dangling-reference cleanup, rule retarget, no-op) written
  before implementation, plus a new integration fixture whose merged output contains
  an empty operator group, with a committed snapshot. The existing
  `served-config.snap.yaml` is confirmed to contain no empty proxy-group, so it stays
  byte-unchanged (FR-010).
- **V. Observable Routing & Source-Merge Decisions** — PASS. FR-011 is satisfied by
  emitting one structured `slog` event listing every pruned proxy-group and every
  rule whose target was redirected (rule index, old target, new target). This is the
  merge-decision visibility Principle V requires.
- **Routing & Security Constraints** — PASS (with documented risk). Corporate
  isolation requires corporate-classified traffic to route only through the Montreal
  exit. If a corporate rule's target group were pruned, FR-008 would redirect it to
  the fallback target — a routing change. Mitigation: the corporate target group is
  operator-defined and expected to always have members, so it is not a prune
  candidate; and every retarget is surfaced in the FR-011 structured log, so a prune
  affecting a corporate rule is visible rather than silent. No secrets are read or
  written by this step; the served output is unchanged except for group/rule
  removal/rewrite, so the sanitized-output guarantee is preserved.

**Result**: All gates pass. No Complexity Tracking entries required.

## Project Structure

### Documentation (this feature)

```text
specs/015-remove-empty-proxy-groups/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/
│   └── served-config-proxy-groups.md   # Served-config proxy-group invariant
├── checklists/
│   └── requirements.md  # Spec quality checklist (from /speckit-specify)
└── tasks.md             # Phase 2 output (/speckit-tasks — NOT created here)
```

### Source Code (repository root)

```text
internal/merge/
├── prune.go             # NEW — PruneEmptyProxyGroups + helpers (pure function)
├── prune_test.go        # NEW — unit tests (test-first)
├── pipeline.go          # MODIFIED — call prune at end of Build(); emit FR-011 log
├── proxy_groups.go      # reference only — mappingMembers / setMappingMembers reuse
└── rules.go             # reference only — rule string format / fallback target

internal/integration/
├── testdata/fixtures/upstream/
│   └── <new fixture>.yaml            # NEW — upstream payload yielding an empty group
├── testdata/fixtures/subscriptions.csv  # MODIFIED — register the new fixture source
├── testdata/snapshots/
│   └── served-config-prune.snap.yaml # NEW — snapshot for the empty-group scenario
└── prune_test.go                     # NEW — integration test for the prune scenario
```

**Structure Decision**: Single Go project, unchanged. All transformation logic lives
in `internal/merge/` per Constitution Principle I; the pruning step is one new file
there plus a call site in `pipeline.go`. No new package is introduced — pruning
operates on the same `[]*yaml.Node` / `[]string` types the pipeline already passes
around, so a new package would only add an import boundary with one caller (Principle
"Simplicity bias"). Integration coverage reuses the existing fixture/snapshot harness.

## Complexity Tracking

> No Constitution Check violations. This section intentionally left empty.
