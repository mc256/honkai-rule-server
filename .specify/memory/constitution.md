<!--
SYNC IMPACT REPORT
==================
Version change: 1.0.0 (redraft, pre-adoption) → 1.0.0
Bump rationale: This is a pre-adoption redraft of the initial v1.0.0 constitution.
The previous draft assumed a static override-JS generator (matching the example/
sandbox); user clarification reframed the project as a server with two delivery
modes, multi-subscription aggregation, and a CSV-based rules input. No spec, plan,
or task has consumed the previous draft, so the version is held at 1.0.0 rather
than bumped. Once adopted by a plan/spec, future changes follow the
MAJOR/MINOR/PATCH policy in Governance.

Modified principles (vs. previous v1.0.0 draft — all five reshaped, new I added):
  - NEW   → I. Unified Transformation Core
  - II    Deterministic Generation → II. Deterministic Transformation
            (scope: transform function, not upstream fetch payload)
  - II    Source-of-Truth Inputs → III. CSV Rules — Strict Schema, Loud Failure
            (Markdown whitelists + YAML customized-rules collapsed into one
             CSV format covering all Mihomo rule types)
  - III+IV Test-First + Integration → IV. Test-First, Real-Input Integration
            (one principle, NON-NEGOTIABLE; integration MUST cover merged
             multi-subscription inputs and BOTH delivery modes)
  - V     Observable Routing → V. Observable Routing & Source-Merge Decisions
            (extended to upstream fetches and merge conflict resolution)

Added sections:
  - Core Principles I–V (above)
  - Routing & Security Constraints — adds multi-subscription mixing rules and
    upstream-fetch failure-mode requirements
  - Development Workflow — replaces "regenerate and commit served artifact" with
    a snapshot-stability gate covering both delivery modes
  - Governance (versioning + amendment procedure)

Removed sections (vs. previous v1.0.0 draft): none structural; "Source-of-Truth
Inputs" was folded into the CSV principle and Routing & Security Constraints.

Templates requiring updates:
  - .specify/templates/plan-template.md ✅ aligned (Constitution Check gate
    resolves at /speckit-plan time against this file)
  - .specify/templates/spec-template.md ✅ aligned
  - .specify/templates/tasks-template.md ✅ aligned (testing/observability/
    integration task categories cover the reshaped principles)
  - .specify/templates/checklist-template.md ✅ aligned
  - CLAUDE.md ✅ aligned
  - README.md ⚠ pending — still a one-line stub. Not constitution-blocking.

Deferred items / TODOs:
  - TODO(PROJECT_SCOPE_DOC): Expand README.md once transport, deployment surface,
    and CSV column schema are decided.
  - TODO(CSV_SCHEMA): The exact CSV column set is intentionally left to the first
    /speckit-plan to specify (column names, optional fields, escape rules).
    Principle III binds the *requirements* of the schema, not its concrete shape.
-->

# Honkai Rule Server Constitution

## Project Scope

The Honkai Rule Server fetches one or more upstream third-party Mihomo
subscriptions, merges them, applies a customized rule set defined in CSV,
and delivers the result via two modes:

- **Subscription mode:** Mihomo/Sparkle clients subscribe directly to a URL
  served by this project; the server returns a ready-to-use, fully transformed
  config.
- **Override mode:** the server emits an `override.js` payload (the same
  `main(config)` shape as the `example/` sandbox) that clients paste into
  Mihomo's Override JS field.

Both modes are produced by a single transformation core; they differ only in
the output adapter.

## Core Principles

### I. Unified Transformation Core

Subscription mode and override mode MUST be produced by a single
transformation pipeline operating on the same inputs. No forked pipelines, no
copy-pasted classifiers, no mode-only logic deeper than a thin output adapter
at the boundary. Adding a feature in one mode without it being available in
the other MUST be rejected unless explicitly justified in the plan's
Complexity Tracking.

**Rationale:** The whole point of supporting both modes is consistent
behavior. Two divergent pipelines would re-introduce silent drift at a
coarser scale than hand-edited generated files — the exact bug class this
project exists to avoid.

### II. Deterministic Transformation

Given a fixed set of inputs (snapshotted upstream subscriptions, the rules
CSV, exit-proxy definitions, server config), the transformation MUST produce
byte-identical output across runs. Sources of nondeterminism (timestamps,
map iteration order, random IDs, locale-dependent sorting) MUST be
eliminated or pinned. Determinism MUST be verifiable by feeding committed
fixture inputs through the pipeline and asserting output stability.

Upstream fetches are not deterministic; this principle binds the transform
function, not the fetched payload. The fetch layer MUST therefore be
separable from the transform layer so the latter can be exercised against
fixed inputs.

**Rationale:** Without determinism, the snapshot tests required by Principle
IV are useless — every run produces a "diff", and reviewers learn to ignore
them. Diff-fatigue is how routing regressions ship.

### III. CSV Rules — Strict Schema, Loud Failure

Customized rules MUST be defined in CSV with an explicit, versioned column
schema. The schema MUST support the full breadth of Mihomo rule types
relevant to routing — at minimum: `DOMAIN-SUFFIX`, `DOMAIN-KEYWORD`,
`DOMAIN`, `IP-CIDR`, `IP-CIDR6`, `GEOIP`, `IP-ASN`, `PROCESS-NAME`,
`PROCESS-PATH`, `DST-PORT`, `SRC-IP-CIDR`, `RULE-SET`, and `MATCH` — with
type-aware validation per row.

Rows that fail validation (unknown rule type, ill-formed CIDR, missing
required column, target group that does not exist) MUST cause loud failure
at load time. Silent skips, default-fallbacks to `DOMAIN-SUFFIX`, or
"best-effort" parsing are forbidden.

Rows should also include a column for human-readable comments to document
the rationale for carve-outs and non-obvious rules inline with the data.

**Rationale:** The Markdown whitelist format from the sandbox could only
express domain-style and CIDR rules; every richer rule type required a
separate file or a YAML escape hatch. Concentrating routing into one CSV
makes the routing surface reviewable as a single artifact, but only if the
parser refuses to guess.

### IV. Test-First, Real-Input Integration (NON-NEGOTIABLE)

Every change to the transformation core, classifiers, proxy-group
construction, or merge logic MUST land with tests written first. Required
coverage:

- **Targeted unit tests** for any new classifier, decision branch, or
  validation rule.
- **Snapshot tests** against committed fixture inputs (rules CSV +
  multi-subscription bundle + exit proxies) producing committed expected
  outputs for **both** subscription mode and override mode.
- **At least one integration test** that exercises the full pipeline end-to-
  end against a representative *merged* multi-subscription input — covering
  proxy-name collisions, region-classification overlap, and conflicting
  metadata between sources.

Order of work: tests written → tests fail → implementation lands → tests
pass. PRs that change the transformation core without updated snapshots for
both modes MUST be rejected.

**Rationale:** Misrouted traffic is a silent compliance failure that
production telemetry catches only after the affected requests are served.
Multi-subscription merge bugs (a corporate-tagged proxy from one source
silently shadowed by an unrelated proxy of the same name from another) are
invisible without integration coverage against the real merged shape.

### V. Observable Routing & Source-Merge Decisions

The server MUST emit, in structured machine-parseable form:

- **Per request (subscription mode and override-emit endpoint):** the rule
  that matched, the proxy group selected, and the exit proxy used where
  applicable.
- **Per upstream fetch:** source URL (sanitized of credentials), HTTP status
  or fetch error, payload size, cache-hit/miss, and the rule applied on
  failure (stale-served vs. fail-closed; see Routing & Security Constraints).
- **Per merge:** any proxy-name collision and the deterministic resolution
  chosen, any rule-target group reference that could not be satisfied
  (rejected per Principle III), and the count of nodes contributed by each
  source.

Log verbosity and any PII-bearing fields MUST be configurable. Logs MUST
NOT contain subscription credentials, exit-proxy authentication, or full
request bodies by default.

**Rationale:** Two opaque-failure modes ship in projects like this one:
"why did request X go through proxy Y" and "why did the served config
suddenly stop including node Z". Decision-level and merge-level structured
logs make both directly answerable instead of requiring state
reconstruction from memory.

## Routing & Security Constraints

### Routing

- **Corporate isolation:** Traffic classified as corporate MUST route
  exclusively through the Montreal exit. Any change to corporate rule
  membership, the corporate target group, or its exit binding MUST be
  reviewed by a code owner and called out in the PR description.
- **Multi-subscription mixing — collision resolution:** When two upstream
  subscriptions contribute proxies with the same name, the resolution
  strategy MUST be explicit, deterministic, and applied uniformly (e.g.,
  per-source name suffix). Silent overwrite is forbidden. The chosen
  strategy MUST be documented in the active plan and asserted by an
  integration test.
- **Multi-subscription mixing — fetch failure modes:** Behavior on upstream
  fetch failure MUST be explicit: per-source TTL, stale-on-error window,
  and the fail-closed boundary (when no usable cache exists). Defaults MUST
  be configurable; the chosen defaults MUST be documented. A served
  response that silently drops a failed source is forbidden — the failure
  MUST surface in logs (Principle V) and, where applicable, in a server
  health endpoint.
- **Carve-outs are explicit:** Any "no-chain" or direct-exit rule (e.g.,
  anime traffic going direct via JP) MUST be documented inline in the rules
  CSV via a comment column or adjacent comment row. Carve-outs MUST NOT be
  introduced silently as a side effect of unrelated changes.

### Security

- **Secrets boundary:** Subscription URLs containing tokens, exit-proxy
  credentials, server-side auth keys, and any third-party tokens MUST be
  loaded from environment variables or a secrets store. They MUST NOT be
  committed to the repository, embedded in served output, included in
  override-mode payloads, or written to logs (see Principle V).
- **Sanitized output:** Served subscription-mode YAML and emitted
  override-mode JS MUST NOT echo raw upstream subscription URLs or any
  authentication material from the fetch layer. The transform layer MUST
  produce client-facing output that is independent of upstream credentials.
- **CSV is reviewable, not secret:** The rules CSV is a routing
  declaration. It MUST NOT contain secrets and MAY be assumed publishable
  by reviewers.

## Development Workflow

- **Snapshot stability gate:** A committed fixture suite (rules CSV +
  multi-subscription input bundle + exit proxies) maps to committed
  expected outputs for both subscription mode and override mode. CI MUST
  fail on snapshot drift. Updating snapshots is a deliberate, reviewable
  action; the PR MUST state why the change is intentional.
- **Diff-reviewable changes:** Changes to the rules CSV, classifiers, or
  merge logic MUST be reviewable as a unified diff alongside the
  corresponding snapshot diffs. PR descriptions MUST link to or quote the
  test that demonstrates the change.
- **Both modes covered, every change:** Any change to the transformation
  core MUST update snapshots for both delivery modes, even when the
  developer believes only one is affected. If a change genuinely only
  affects one mode, the no-op snapshot of the other still confirms it.
- **Constitution Check on every plan:** Every `/speckit-plan` output MUST
  include a Constitution Check section addressing Principles I–V and the
  Routing & Security Constraints. Violations require an entry in the plan's
  Complexity Tracking with a justification and a rejected simpler
  alternative.
- **Simplicity bias:** New abstractions (frameworks, code-generation
  layers, config-of-configs, plugin systems) MUST be justified against a
  direct implementation. Three similar lines of code is acceptable; a
  premature abstraction with one caller is not.

## Governance

This constitution supersedes ad-hoc practices for this repository. Where a
tool, library, or workflow conflicts with a principle, the principle wins
or the principle is amended via the procedure below — it MUST NOT be
silently bypassed.

- **Amendment procedure:** Open a PR modifying
  `.specify/memory/constitution.md`. Update the version per the rules
  below, refresh `Last Amended`, and update any dependent templates flagged
  by the Sync Impact Report. The PR MUST describe the motivation and the
  user-visible impact.
- **Versioning policy:**
  - **MAJOR** — a principle is removed, redefined in a backward-incompatible
    way, or governance is materially restructured.
  - **MINOR** — a new principle or section is added, or an existing
    principle is materially expanded.
  - **PATCH** — clarifications, wording, or typo fixes that do not change
    the rule's intent.
  When the bump is ambiguous, the PR description MUST state the chosen bump
  and why before the constitution is merged. Pre-adoption redrafts (no
  spec/plan/task has consumed the prior version) MAY be made in place
  without a bump; the Sync Impact Report MUST note this explicitly.
- **Compliance review:** Every plan and tasks document MUST reference this
  constitution and confirm alignment. Reviewers MAY block merges that fail
  Constitution Check without a Complexity Tracking justification.
- **Runtime guidance:** Operational details (technology choices, transport,
  build commands, deployment topology, the concrete CSV column schema)
  live in `CLAUDE.md` and the active plan under `specs/<feature>/plan.md`.
  This constitution intentionally does not fix those choices.

**Version**: 1.0.0 | **Ratified**: 2026-04-26 | **Last Amended**: 2026-04-26
