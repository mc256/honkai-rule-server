# Implementation Plan: Rule Set Support

**Branch**: `016-rule-set-support` | **Date**: 2026-06-04 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/016-rule-set-support/spec.md`

## Summary

Upstream Mihomo subscriptions can route traffic with `RULE-SET,<name>,<target>`
rules whose named sets are declared in a `rule-providers:` block. Today the
transformation core reads only `proxies`, `proxy-groups`, and `rules` from each
cached payload; it drops the `rule-providers:` block entirely. The served config
therefore carries `RULE-SET` lines that reference undefined providers, which makes
the client refuse to load. This feature reads each source's `rule-providers:`
block, namespaces both the provider keys and the `RULE-SET` rule references with
the existing `<source>_` prefix (so `Local-IP` from two sources becomes
`srcA_Local-IP` / `srcB_Local-IP` and never collides), and emits a single merged
`rule-providers:` block containing exactly the providers that surviving `RULE-SET`
rules reference. Unbacked references are dropped (and logged) so one malformed
upstream cannot break every subscriber's config.

Technical approach: extend the existing per-source namespacing pass to (a) prefix
the `RULE-SET` provider-name field in `namespace.go`'s rule rewriter, and (b)
namespace each source's `rule-providers:` mapping in a new pure file
`internal/merge/ruleset.go` (clone, prefix keys, make the local cache `path`
source-distinct, prefix any non-builtin fetch-through `proxy`). Per source, drop
`RULE-SET` rules whose provider is undefined *before* the unified rule merge so the
priority/contributor parallel slices stay aligned by construction. After the rule
slice is final, build the merged `rule-providers` node from the referenced
providers only, threading it through `MergedConfig` to the output adapter, which
emits a `rule-providers:` key when the node is non-nil. No new package, no new
delivery-mode logic.

## Technical Context

**Language/Version**: Go 1.25 toolchain (module declares Go 1.22+)  
**Primary Dependencies**: stdlib `net/http`, `log/slog`; `gopkg.in/yaml.v3` (rule-provider definitions and rule strings are `*yaml.Node` / `string`); `bradleyjkemp/cupaloy/v2` (snapshot tests)  
**Storage**: N/A — pure in-memory transformation step; no new persisted server state (the `path:` field rewritten into provider defs is the *client's* on-disk cache path, not the server's)  
**Testing**: `go test` (unit + integration), `staticcheck`, `go vet`, cupaloy/integration snapshot drift check — all via `make check`  
**Target Platform**: Linux server container (`FROM scratch` runtime image)  
**Project Type**: Single-project web service (subscription-mode output adapter; override mode is a future adapter on the same core)  
**Performance Goals**: Per-request transform; rule-provider handling is O(P) per source for namespacing (P = providers per source, single digits) plus one O(R) scan of the final rule slice for reference collection (R = total rules, hundreds) — negligible against the existing merge cost  
**Constraints**: Output MUST stay byte-deterministic (Principle II) and byte-identical on every committed snapshot whose sources supply no `rule-providers:` block and no `RULE-SET` rule (FR-013, snapshot-stability gate)  
**Scale/Scope**: Single-digit rule-providers per source, a handful of sources; one new merge-core file, edits to `namespace.go` / `pipeline.go` / `yamlutil.go` and the output adapter, plus one integration fixture/snapshot

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-checked after Phase 1 design.*

- **I. Unified Transformation Core** — PASS. All new logic lives in the shared
  `merge` pipeline: provider-field rewriting in `namespace.go`, the rule-provider
  namespace/merge/prune in `ruleset.go`, orchestrated by `Pipeline.Build()`. The
  output adapter gains only a thin `setMappingValue(root, "rule-providers", …)`
  emit guarded on a non-nil node — no classification or mode-only branching. Both
  subscription mode and the future override mode consume the same already-merged
  `MergedConfig.RuleProviders`.
- **II. Deterministic Transformation** — PASS. Per-source providers are carried as
  ordered `*yaml.Node` mapping pairs (not Go maps), merged in the existing
  deterministic `contributing`-source order, and reference-filtered by walking the
  ordered final rule slice. No map iteration, timestamps, or randomness is
  introduced. The reference set is a `map[string]bool` used only for membership
  tests, never for ordering. Determinism is asserted by the snapshot gate.
- **III. CSV Rules — Strict Schema, Loud Failure** — PASS (with note). Principle
  III binds *load-time* validation of the operator's customized-rules input. The
  `rule-providers:` blocks handled here come from *fetched upstream subscriptions*,
  not the operator CSV; the fetch payload is explicitly out of the determinism/
  strict-schema boundary (Principle II names upstream payloads as nondeterministic
  input). Dropping an *unbacked* upstream `RULE-SET` rule (FR-009) is not a silent
  best-effort parse of operator input — it is a deliberate, logged repair of a
  malformed upstream (Principle V), exactly analogous to 015's logged rule
  retarget and 002's documented warn-and-skip of malformed upstream rows. A
  `RULE-SET` row in the operator CSV still validates at load time per Principle III
  (the constitution's enumerated rule types already include `RULE-SET`); this
  feature does not relax that path. See `research.md` for the drop-vs-fail
  rationale.
- **IV. Test-First, Real-Input Integration (NON-NEGOTIABLE)** — PASS. The plan
  sequences unit tests first for: the `RULE-SET` provider-field rewrite
  (`namespace_test.go`), per-source provider namespacing + path/proxy rewrite,
  per-source unbacked-rule drop, cross-source merge + reference-prune, and the
  no-providers no-op (`ruleset_test.go`); plus a new integration fixture whose
  merged output contains namespaced `rule-providers` and `RULE-SET` rules, with a
  committed snapshot. Existing snapshots are confirmed to contain no `RULE-SET` /
  `rule-providers` (grep over `internal/**` testdata returns nothing), so they stay
  byte-unchanged (FR-013). **Scope note (pre-existing repo-wide deviation):**
  Principle IV asks for snapshots in *both* subscription and override modes; this
  feature, like 009–015, covers subscription mode only because the override-mode
  adapter is not yet implemented. The transformation core is mode-agnostic, so the
  override adapter will inherit `MergedConfig.RuleProviders` unchanged when it lands.
- **V. Observable Routing & Source-Merge Decisions** — PASS. FR-011 is satisfied
  by structured `slog` events: one per dropped unbacked `RULE-SET` rule (source +
  missing provider name), one per skipped malformed provider definition, and a
  per-build summary (counts of providers merged, providers pruned-as-unreferenced,
  rules dropped). This is the merge-decision visibility Principle V requires for a
  step that removes rules and providers.
- **Routing & Security Constraints** — PASS. No secrets are read or written:
  rule-provider definitions carry a public CDN `url` and a client-side `path`, no
  credentials; the sanitized-output guarantee is preserved because the emitted
  block is a verbatim-but-namespaced copy of upstream public fields. Corporate
  isolation is unaffected — `RULE-SET` rules keep their original target (only the
  provider-name and non-builtin target fields are prefixed, exactly as every other
  rule's target is today). Collision resolution follows the constitution's
  mandated per-source-prefix strategy (FR-002/FR-012), applied uniformly and
  asserted by integration test.

**Result**: All gates pass. No Complexity Tracking entries required.

## Project Structure

### Documentation (this feature)

```text
specs/016-rule-set-support/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/
│   └── served-config-rule-providers.md   # Served-config rule-providers invariant
├── checklists/
│   └── requirements.md  # Spec quality checklist (from /speckit-specify)
└── tasks.md             # Phase 2 output (/speckit-tasks — NOT created here)
```

### Source Code (repository root)

```text
internal/merge/
├── ruleset.go           # NEW — rule-provider namespacing, merge, reference-prune,
│                        #       unbacked-rule drop (pure functions)
├── ruleset_test.go      # NEW — unit tests (test-first)
├── namespace.go         # MODIFIED — rule rewriter also prefixes the RULE-SET
│                        #            provider-name field (field[1])
├── namespace_test.go    # MODIFIED — RULE-SET provider-field rewrite cases
├── yamlutil.go          # MODIFIED — add findChildMapping helper
├── yamlutil_test.go     # MODIFIED (if present) — findChildMapping cases
├── pipeline.go          # MODIFIED — read rule-providers per source; namespace;
│                        #            drop unbacked; merge + reference-prune;
│                        #            set MergedConfig.RuleProviders; FR-011 logs
└── pipeline_test.go     # MODIFIED — end-to-end rule-providers wiring

internal/output/
├── subscription_mode.go      # MODIFIED — emit `rule-providers:` when non-nil
└── subscription_mode_test.go # MODIFIED — rule-providers emission + omission

internal/integration/
├── testdata/fixtures/upstream/
│   └── <new fixture>.yaml               # NEW — payload with rule-providers + RULE-SET
├── testdata/fixtures/subscriptions.csv  # MODIFIED — register the new fixture source
├── testdata/snapshots/
│   └── served-config-ruleset.snap.yaml  # NEW — snapshot for the rule-set scenario
└── ruleset_test.go                      # NEW — integration test for the scenario
```

**Structure Decision**: Single Go project, unchanged. New transformation logic
lives in `internal/merge/` per Constitution Principle I — one new focused file
(`ruleset.go`, mirroring the `prune.go` precedent from 015) plus surgical edits to
the existing rule rewriter and the pipeline orchestration. No new package: the
rule-provider data is the same `*yaml.Node` / `[]string` the pipeline already
threads, so a separate package would add an import boundary with a single caller
(Simplicity bias). `MergedConfig` gains one nullable field (`RuleProviders
*yaml.Node`) and the output adapter gains one guarded emit, keeping the two
delivery modes mode-agnostic.

## Complexity Tracking

> No Constitution Check violations. This section intentionally left empty.
