# Research: URL-Test for Auto-Emitted Regional & Continent Proxy Groups

**Feature**: 012-url-test-region-groups
**Date**: 2026-05-02

Seven design decisions for converting `_region_*` / `_continent_*` groups from `select` to `url-test`.

---

## R1 — Env-var schema (`URLTestParams`)

**Decision**: Five separate env vars per FR-004, one per Mihomo url-test field. Read in `internal/config/server.go::Load()` using the existing `env.Getenv("KEY")` pattern. Empty / unset values fall back to the FR-003 defaults.

**Rationale**:
- Matches the project's strong existing pattern. Every other config knob is its own env var (`HONKAI_RULE_CLIENT_UA`, `FALLBACK_RULE_TARGET`, `URL_PATH_PREFIX`, `PROXIES_GROUP_NAME`).
- Operator can tune one knob without touching the others.
- Helm chart `values.yaml` stays flat and readable.
- Type coercion is per-field and unambiguous (string / int / bool).
- Clear error messages on validation failure ("URL_TEST_INTERVAL_SECONDS must be a positive integer, got X").

**Alternatives considered**:
- *One composite env var* (`URL_TEST_PARAMS=url=...,interval=10,...`): rejected. Custom format means a custom parser and worse error messages. No real benefit at this size.
- *YAML template file mounted via ConfigMap*: rejected. Solves a future-proofing problem we don't have — Mihomo's url-test schema is stable, and any new field would require server-code changes anyway. Adds a runtime dependency (file load + parse) for no current benefit.

---

## R2 — Units explicit in env names

**Decision**: `URL_TEST_INTERVAL_SECONDS` and `URL_TEST_TIMEOUT_MS` carry their unit suffix in the env-var name. The YAML field names emitted in the served body remain `interval` and `timeout` (Mihomo's wire-format names).

**Rationale**:
- Mihomo's own field names are unit-asymmetric (interval=seconds, timeout=ms). An operator reading `URL_TEST_INTERVAL=10` and `URL_TEST_TIMEOUT=3000` cannot tell if those are both seconds, both ms, or different.
- Adding `_SECONDS` / `_MS` removes that ambiguity at config time.
- The YAML emission stays standard so stock Mihomo / Clash clients render it correctly.

**Alternatives considered**:
- *Match Mihomo names verbatim* (`URL_TEST_INTERVAL`, `URL_TEST_TIMEOUT`): rejected. Saves four characters in the env-var name; costs every operator a context-switch every time they see the value.

---

## R3 — Validation = startup-fatal (Constitution Principle III)

**Decision**: `URLTestParams.Validate()` returns an error per offending field. `Load()` propagates it. The existing `cmd/server/main.go` already exits non-zero on `Load()` errors. No silent fallback; no "best-effort" parsing.

**Rationale**:
- Constitution Principle III mandates loud-fail on schema violations.
- Bad probe config is operator-visible at the next routine restart (the chart ROC will show a CrashLoopBackOff with a clear log line), not silently masked behind defaults.
- The existing pattern (`HONKAI_RULE_CLIENT_UA` validation, etc.) already follows this.

**Validation rules**:
- `URL_TEST_INTERVAL_SECONDS`, `URL_TEST_TIMEOUT_MS`, `URL_TEST_MAX_FAILED_TIMES`: parse as integer; require ≥ 1 (non-positive values are nonsense for these semantics).
- `URL_TEST_LAZY`: parse as `true`/`false` (case-insensitive). Other values fail.
- `URL_TEST_URL`: parsed as-is (no scheme/format validation per spec FR-004a — operator typos surface at runtime via probe failures, which the operator notices via FR-008's startup log line).

**Alternatives considered**:
- *Soft-fall to defaults on validation failure*: rejected (Principle III).
- *Validate URL format*: rejected. URL parsing is its own snake pit (relative URLs, IDN domains, etc.); leave to Mihomo's client-side probe.

---

## R4 — Plumbing = `Pipeline` builder method

**Decision**: Add `Pipeline.WithURLTestParams(p URLTestParams) *Pipeline`, mirroring the existing `WithProxiesGroupName` and `WithFallbackRuleTarget`. The struct is held on `Pipeline.urlTestParams` and passed by value into `AppendRegionGroups` and `AppendContinentGroups`.

**Rationale**:
- Matches the existing builder convention exactly.
- Keeps the emit functions pure: they receive params as a value, return new YAML nodes. No global state, no env-var reads inside `internal/merge/`.
- Snapshot tests can construct a `Pipeline` with a fixed `URLTestParams` for determinism.

**Alternatives considered**:
- *Read env vars inside `internal/merge/region.go`*: rejected. Violates the pure-merge boundary; makes tests less hermetic.
- *Make `URLTestParams` a constructor argument to `NewPipeline`*: rejected. Existing `NewPipeline` signature is already wide; a builder method is the established pattern for optional config.

---

## R5 — YAML field order

**Decision**: After 004's existing `name, type, proxies` ordering, the five new fields render in the order from FR-003 / FR-007: `url`, `interval`, `timeout`, `max-failed-times`, `lazy`. Implement by extending the existing `reorderProxyGroupFields` pass in `subscription_mode.go` to position these fields at indices 6, 8, 10, 12, 14 when `type == "url-test"`.

**Rationale**:
- Preserves 004's readability contract (all groups share a stable field order).
- Reviewers see the same shape every time, making snapshot diffs easier to read.
- The existing `moveFieldToPosition` helper handles the swap; we just extend the call list inside `reorderProxyGroupFields`.

**Alternatives considered**:
- *Let yaml.v3 emit in insertion order*: rejected. Insertion order in `region.go` would couple the formatter to the emitter; 004 already factored this responsibility into `reorderProxyGroupFields` and we should respect that boundary.
- *Different field ordering* (e.g., `interval` first because it's most-frequently-tuned): rejected. The natural reading order is "what URL do we probe → how often → how long do we wait → how many failures count → are we lazy" — that's the order in FR-003.

---

## R6 — Snapshot test refresh strategy

**Decision**: After implementation lands, run `UPDATE_SNAPSHOTS=true go test ./internal/integration/...` to regenerate snapshots. Inspect `git diff internal/integration/testdata/snapshots/` to confirm the diff is confined to `_region_*` / `_continent_*` group blocks. Commit only after manual diff verification.

**Rationale**:
- Constitution's snapshot-stability gate requires deliberate, reviewable refreshes.
- The expected diff is well-defined: every `_region_*` / `_continent_*` group block changes from `type: select` to `type: url-test` and gains five fields. Body bytes outside those blocks should not change.
- Manual inspection catches any unexpected drift (e.g., the always-present `Proxies` group accidentally getting touched).

**Tooling**: PR description states "every region and continent group's `type` flips to `url-test` and gains the five health-check fields; nothing else changes" so reviewers can verify the diff matches that promise.

**Alternatives considered**:
- *Auto-accept all snapshot drift*: rejected. Defeats the snapshot-stability gate.
- *Skip snapshot tests for this feature*: rejected. The snapshot suite is the primary end-to-end verification artifact for the served body; bypassing it would leave the wire-format change unverified.

---

## R7 — Future per-group override (out of scope)

**Decision**: Today's `URLTestParams` is global — one set of values applies to ALL auto-emitted region and continent groups. Per-CC or per-continent overrides are NOT supported in this feature.

**Rationale**:
- The operator's stated motivation is reliability via auto-failover; per-region tuning is a secondary concern that may never be needed (probe URL is the same globally; intervals/timeouts are network conditions, not regional ones).
- Adding per-group config would require a more elaborate config format (operator can't sensibly stuff per-CC overrides into a flat env-var schema).
- Premature flexibility violates Constitution's simplicity-bias principle.

**Future-proofing**: If per-group overrides are ever needed, the natural extension is to make `URLTestParams` a default + a per-CC override map (e.g., loaded from a YAML file mounted via ConfigMap). Today's struct doesn't foreclose that extension — the override map can be added later without breaking existing config.

**Alternatives considered**:
- *Build per-group support now*: rejected. Speculative complexity; the operator hasn't asked for it.
- *Make the env-var schema extensible to per-CC keys via prefix* (`URL_TEST_INTERVAL_SECONDS_JP=20`): rejected. Discoverability nightmare; operators reading `kubectl describe deploy` would see a long list of similarly-named env vars without clear semantics.
