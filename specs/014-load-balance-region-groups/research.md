# Phase 0 Research: Load-Balance Region & Continent Groups

**Feature**: `014-load-balance-region-groups`
**Date**: 2026-05-08

The spec contains no `[NEEDS CLARIFICATION]` markers; all unknowns were resolved during `/speckit-clarify` (see `spec.md` § Clarifications). Phase 0 documents the nine narrow design decisions that shape the Phase 1 artifacts and Phase 2 task list.

---

## Decision 1: Env-var schema — six knobs, separate `LOAD_BALANCE_*` namespace

**Decision**: Introduce six separate env vars (`LOAD_BALANCE_URL`, `LOAD_BALANCE_INTERVAL_SECONDS`, `LOAD_BALANCE_TIMEOUT_MS`, `LOAD_BALANCE_MAX_FAILED_TIMES`, `LOAD_BALANCE_LAZY`, `LOAD_BALANCE_STRATEGY`) loaded from the OS environment at startup via the existing `env.Getenv` pattern in `internal/config/server.go::Load()`. Defaults from spec FR-003 apply when unset / empty.

**Rationale**:
- Matches the project's strong existing pattern (`URL_TEST_*` in 012, `FALLBACK_RULE_TARGET` in 010, `PROXIES_GROUP_NAME` in 003): one env var per knob.
- Keeps Helm `values.yaml` flat and reviewable.
- Per-knob tunability without a custom parser or YAML config file.
- Separate namespace from `URL_TEST_*` reflects that the two probe sets describe semantically different behaviors with diverging tuned defaults (interval 300 vs 10, timeout 1500 vs 3000). Operators tuning one shouldn't accidentally re-tune the other.

**Alternatives considered**:
- **Single combined namespace** (e.g., reuse `URL_TEST_*` for both): rejected — the user's example explicitly differs from 012's defaults; aliasing would force operators tuning one probe to inadvertently retune the other.
- **JSON / YAML config blob**: rejected — adds a parser, breaks the existing flat-env convention, and provides no benefit at this scale (six knobs).
- **Per-region overrides** (e.g., `LOAD_BALANCE_INTERVAL_SECONDS_JP`): rejected for v1 — adds a config-of-configs surface that hasn't been requested. Spec Assumptions reserves this as a future extension.

---

## Decision 2: Units explicit in env names

**Decision**: `LOAD_BALANCE_INTERVAL_SECONDS` (seconds) and `LOAD_BALANCE_TIMEOUT_MS` (milliseconds) carry their unit in the name. The corresponding YAML fields (`interval` / `timeout`) keep Mihomo's standard names without unit suffixes.

**Rationale**:
- Mihomo's `interval` is in seconds; `timeout` is in milliseconds. Without a unit hint, an operator setting `LOAD_BALANCE_TIMEOUT=1500` could reasonably read either "1.5 seconds" or "1500 seconds" (some tools differ).
- Mirrors 012's `URL_TEST_INTERVAL_SECONDS` / `URL_TEST_TIMEOUT_MS`.
- Operator-facing surface (env names) gains clarity at zero cost; client-facing surface (YAML) remains stock-Mihomo-compatible.

**Alternatives considered**:
- **Unit-less names** (`LOAD_BALANCE_INTERVAL`, `LOAD_BALANCE_TIMEOUT`): rejected — silent unit confusion is a known operational hazard.
- **Use Go duration strings** (`LOAD_BALANCE_INTERVAL=300s`): rejected — Mihomo doesn't use duration strings, the YAML stays integer-typed, and the env-to-YAML conversion would need a parsed-then-re-serialized round-trip with no operator benefit.

---

## Decision 3: Strategy enum — accept all three Mihomo values

**Decision**: `LOAD_BALANCE_STRATEGY` accepts exactly the three Mihomo-defined string literals: `round-robin`, `consistent-hashing`, `sticky-sessions`. Any other value (including casing variants like `Round-Robin`) causes startup abort. Default `round-robin`. Validated by a string-set check in `LoadBalanceParams.Validate()`.

**Rationale**:
- User clarification (Q2) accepted "all three" over "only round-robin".
- Matches Mihomo's wire format exactly — operators see the same identifier in env, in YAML, and in Mihomo docs.
- Validation cost is trivial (a 3-element set lookup); the loud-fail catches typos at deploy time, not at probe time.
- Case-sensitive match because Mihomo itself is case-sensitive on these literals — accepting `Round-Robin` would render an invalid YAML to the client.

**Alternatives considered**:
- **Restrict to `round-robin` only**: rejected per user Q2 answer A; would force a code change for any operator who later wants `consistent-hashing`.
- **Pass the value through unvalidated**: rejected — typos would surface as silent client-side parse errors against served YAML rather than as deploy-time configuration errors.

---

## Decision 4: Validation = startup-fatal with accumulated errors

**Decision**: `LoadBalanceParams.Validate()` accumulates all per-field validation errors into a single `error` returned by `Load()`. The error message format mirrors 012's: `"LoadBalanceParams validation failed: KEY1=\"value1\" (reason); KEY2=\"value2\" (reason); ..."`. The server's `cmd/server/main.go` already exits non-zero on `Load()` errors; no new shutdown code is needed.

**Rationale**:
- Constitution Principle III: loud failure on bad input.
- Accumulating errors lets operators fix all knobs in one redeploy cycle — important when several `LOAD_BALANCE_*` env vars have plausibly-typo-able values.
- Mirrors 012's approach exactly so the operator experience is uniform across both env-var sets.

**Alternatives considered**:
- **Fail on first error**: rejected — forces sequential redeploys to discover all problems.
- **Warn-and-default**: rejected — Constitution Principle III explicitly forbids silent fallbacks.

---

## Decision 5: Plumbing — a new `Pipeline.WithLoadBalanceParams` builder method

**Decision**: Add `Pipeline.WithLoadBalanceParams(p LoadBalanceParams) *Pipeline` mirroring the existing `WithURLTestParams`. The field is held on `Pipeline` (`loadBalanceParams merge.LoadBalanceParams`) and passed by value into `AppendRegionGroups` and `AppendContinentGroups` via a new positional parameter. Both functions' signatures gain one parameter:

```go
func AppendRegionGroups(
    groups []*yaml.Node,
    upstreamPrefixedProxies []*yaml.Node,
    proxiesGroupName string,
    urlTestParams URLTestParams,
    loadBalanceParams LoadBalanceParams, // NEW
    unmappedLogger func(fragment string),
) []*yaml.Node
```

Same shape for `AppendContinentGroups`.

**Rationale**:
- Matches the existing builder convention exactly.
- Keeps the emit functions pure — params in, nodes out — so they remain testable in isolation.
- Adding a positional parameter rather than wrapping both params in a shared struct preserves the existing per-knob composability (a future feature could add a third `WithXxxParams` without an awkward struct).

**Alternatives considered**:
- **Single shared struct** (`type ProxyGroupHealthCheckParams struct { URLTest URLTestParams; LoadBalance LoadBalanceParams }`): rejected — couples two independently-evolvable feature surfaces. If a future feature adds a third group type, the shared struct becomes a junk drawer.
- **Reading config directly inside merge**: rejected — Constitution Principle I requires the merge layer to stay free of `internal/config` imports. The mirrored `merge.LoadBalanceParams` struct (mirroring 012's `merge.URLTestParams`) is the project's existing pattern for this isolation.

---

## Decision 6: Paired emission ordering — lb sibling immediately after url-test sibling

**Decision**: In both `AppendRegionGroups` and `AppendContinentGroups`, emit the lb group in the same loop iteration that emits the url-test group:

```go
for _, cc := range ccs {
    members := ccToProxies[cc]
    groupName := "_region_" + cc
    regionGroupNames = append(regionGroupNames, groupName)
    groups = append(groups, newURLTestGroup(groupName, members, urlTestParams))

    lbGroupName := "_lb_region_" + cc                                    // NEW
    regionGroupNames = append(regionGroupNames, lbGroupName)             // NEW
    groups = append(groups, newLoadBalanceGroup(lbGroupName, members, loadBalanceParams)) // NEW
}
```

Same pattern in the `_region_UNKNOWN` block and in `AppendContinentGroups`'s continent loop. The `Proxies` selector member-list addition runs once at the end with both names already in `regionGroupNames`, so no separate accumulator or second loop is needed.

**Rationale**:
- FR-013 prescribes paired adjacency in the served `proxy-groups:` block — operators visually see "JP url-test" / "JP load-balance" side by side.
- The `Proxies` selector's member ordering naturally interleaves (`_region_AU`, `_lb_region_AU`, `_region_CA`, `_lb_region_CA`, …) which matches the visual pairing.
- Single-loop emission means no separate "second-pass" code path; reduces test surface and keeps the diff small.

**Alternatives considered**:
- **Two separate loops** (emit all url-test groups, then all lb groups): rejected — produces a `proxy-groups:` block where every JP-related group is far apart from every other, harder to read.
- **Emit lb groups in a separate function called after `AppendRegionGroups`**: rejected — duplicates the country-code partitioning logic; harder to keep determinism guarantees.

---

## Decision 7: Fan-out prefix widening — one-line predicate change

**Decision**: In `internal/merge/fanout.go::AppendFanoutProxies`, widen the predicate from:
```go
if strings.HasPrefix(name, "_region_") || strings.HasPrefix(name, "_continent_") {
```
to:
```go
if strings.HasPrefix(name, "_region_") ||
    strings.HasPrefix(name, "_continent_") ||
    strings.HasPrefix(name, "_lb_region_") ||
    strings.HasPrefix(name, "_lb_continent_") {
```

The existing `stripUnderscore(groupName)` then naturally yields `lb_region_JP` for `_lb_region_JP`, producing `via_lb_region_JP__<own>` per the user's example. The `dialer-proxy` is set to the full `_lb_region_JP` group name. No other change in `fanout.go`.

**Rationale**:
- Minimal one-line widening; reuses 100% of 008's existing fan-out machinery.
- Outer/inner loop ordering unchanged; deterministic (008 FR-006 preserved).
- The `via_lb_*` names are produced in the same `mergedGroups` order in which the lb groups were emitted in Decision 6, so paired emission carries through to paired fan-out (`via_region_JP__own`, `via_lb_region_JP__own`, `via_region_HK__own`, `via_lb_region_HK__own`, …).
- Naming collisions impossible: 008's analysis already establishes that `via_*` is a closed namespace; widening it to include `via_lb_*` keeps that closure.

**Alternatives considered**:
- **Use a regex**: rejected — `strings.HasPrefix` is faster, simpler, and test-friendly. The four prefixes are a closed set.
- **Extract a `isAutoEmittedRegionOrContinent(name)` helper**: rejected per Constitution simplicity bias — four-line conditional is clearer than a one-line helper that would need to live somewhere in the package.

---

## Decision 8: YAML field ordering for load-balance groups

**Decision**: After 004's existing `name, type, proxies` triple, the six lb fields render in the order: `url, interval, lazy, strategy, timeout, max-failed-times`. This order matches FR-006 / the user's example exactly. Implementation extends `internal/output/subscription_mode.go::reorderProxyGroupFields` with new `moveFieldToPosition` calls for `strategy`. Because `moveFieldToPosition` is a no-op when the key is absent, listing all of (url-test fields ∪ load-balance fields) in one ordering pass works cleanly: a url-test group lacks `strategy`, so the `strategy` reposition is a no-op for it; a load-balance group lacks the url-test-only positions and renders correctly.

The unified ordering call list will be:
```go
moveFieldToPosition(n, "name", 0)
moveFieldToPosition(n, "type", 2)
moveFieldToPosition(n, "proxies", 4)
moveFieldToPosition(n, "url", 6)
moveFieldToPosition(n, "interval", 8)
moveFieldToPosition(n, "lazy", 10)        // NEW position (was 14 for url-test)
moveFieldToPosition(n, "strategy", 12)    // NEW field
moveFieldToPosition(n, "timeout", 14)     // moved from 10
moveFieldToPosition(n, "max-failed-times", 16) // moved from 12
```

**Open question for review at implementation time**: Is the url-test order (`url, interval, timeout, max-failed-times, lazy`) load-bearing for any committed snapshot? If so, the unified ordering changes `lazy`'s position in url-test groups too, which would break url-test snapshots. Solution: check `type` and call separate ordered passes. This is a Phase 2 implementation refinement and surfaces in the snapshot diff.

**Rationale**:
- FR-006 / the user's example specify the exact order.
- 004's existing convention (`name, type, proxies` first) is preserved.
- Single pass with `moveFieldToPosition`'s no-op-on-missing semantic is simple and aligns with how 012 was built.

**Alternatives considered**:
- **Build emit order from scratch** (rebuild `mappingNode.Content` in target order rather than swap-into-place): rejected — `moveFieldToPosition` is the established 012 pattern; rewriting it would be a larger change with no benefit.
- **Type-conditional ordering pass**: documented as the fallback if the unified pass breaks url-test snapshots; not preferred.

---

## Decision 9: Snapshot test refresh strategy

**Decision**: After implementation, run `UPDATE_SNAPSHOTS=true go test ./internal/integration/...` and visually inspect the diff. The expected diff is exactly:
1. New `_lb_region_<CC>` and `_lb_continent_<CONT>` groups in `proxy-groups:`, paired immediately after their url-test siblings.
2. New `via_lb_region_<CC>__<own>` and `via_lb_continent_<CONT>__<own>` entries in `proxies:`, interleaved with the existing `via_region_*` / `via_continent_*` entries (per Decision 7's deterministic ordering).
3. New `_lb_*` group references in the always-present `Proxies` selector's member list, interleaved with existing `_region_*` / `_continent_*` references.

Any other diff (changed bytes in url-test groups, reordered existing entries, changed bytes in custom rules / own-groups) indicates a regression.

The PR description quotes a representative diff fragment so reviewers can sanity-check.

**Rationale**:
- Constitution Principle II + Snapshot Stability Gate.
- Confining the diff to documented additions makes the PR reviewable as a unified diff.
- Quoting in the PR description preempts diff fatigue; reviewers know what to expect.

**Alternatives considered**:
- **Programmatic snapshot diff assertions in the PR**: rejected — bradleyjkemp/cupaloy already enforces byte-identity in CI; manual reviewer-side assertions would duplicate that.

---

## Open Questions / Future Work

None blocking. Spec Assumptions documents two future-work items (per-region/per-continent override knobs; auto-mirroring of operator-declared own-groups) deliberately deferred from v1. These do not gate the current implementation.
