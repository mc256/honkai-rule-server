# Phase 0 Research: Dialer-Proxy Fan-Out for Own Proxies

## R1. Where in the pipeline does fan-out execute?

- **Decision**: Inside `internal/merge/Pipeline.Build()`, immediately after `AppendContinentGroups` and immediately before constructing the `MergedConfig` return value.
- **Rationale**: Fan-out reads `mergedGroups` to enumerate `_region_*` and `_continent_*` targets — those targets only exist after `AppendRegionGroups` and `AppendContinentGroups` have run. Fan-out also writes to `mergedProxies`. Both reads and writes must happen *after* `AppendProxiesGroup` to honor FR-007/FR-008 (the global `Proxies` selector's member list snapshots `mergedProxies` at the time of the call, so emitting fan-out copies later naturally excludes them). Placing the call at the tail of `Build()` keeps all merge-layer mutations co-located in one function and avoids a second pipeline invocation.
- **Alternatives considered**:
  - **Inside `MergeProxies`**: rejected. `MergeProxies` would need to know about `_region_*`/`_continent_*` groups, breaking the layering — those groups are downstream artifacts, not inputs to proxy merging.
  - **Post-pipeline output adapter step**: rejected. Constitution Principle I forbids mode-specific transformation logic; emitting fan-out only in subscription mode (or only in override mode) would diverge the cores.
  - **Pre-`RewriteOwn` (i.e., on raw `own-proxies.yaml` nodes)**: rejected. Pre-rewrite names lack the `_` prefix, so generating `via_region_JP__<original>` and setting `dialer-proxy: _region_JP` would not match the post-rewrite group name in the served body. Fan-out must run after rewrite.

## R2. How does fan-out detect own-proxies that opt out (FR-005)?

- **Decision**: Use `getMappingField(node, "dialer-proxy") != ""` — the existing helper from `yamlutil.go` — on each rewritten own-proxy node. A non-empty value (regardless of whether it points to a real group, the literal `DIRECT`, or anything else operator-supplied) signals "operator has expressed an explicit chain choice" and skips both per-region/per-continent and AUTO fan-out for that proxy.
- **Rationale**: Same probe used elsewhere in the merge layer (e.g., `getMappingField(p, "name")` in `region.go`). No regex on bytes (we have parsed nodes); no separate config knob (single source of truth in the YAML).
- **Alternatives considered**:
  - **Treat `dialer-proxy: ""` (empty) as "fan out anyway"**: rejected. An empty string for `dialer-proxy` would be invalid YAML configuration anyway; operators who genuinely want fan-out simply omit the field.
  - **A dedicated boolean key like `fanout: false`**: rejected. Adds a new schema knob that would need to be documented, validated, and maintained — meanwhile the natural opt-out (set `dialer-proxy` directly) is already expressive.

## R3. Naming convention for fan-out copies (FR-002, FR-004a)

- **Decision**:
  - Per-group: `via_<G>__<P>` where `<G>` = target group name with one leading `_` stripped (`_region_JP` → `region_JP`), `<P>` = own-proxy name with one leading `_` stripped (`_markham` → `markham`). Separator is exactly two underscores `__`.
  - AUTO: `via_AUTO__<P>` (literal `AUTO` substituted for `<G>`). Dialer-proxy value is the literal string `Proxies` — the always-present global selector group's name (no leading underscore — the global selector is special, not a server-emitted underscore-prefixed group).
- **Rationale**: Lifted verbatim from the operator's example `via_region_JP__markham` paired with `dialer-proxy: _region_JP`. The double-underscore separator visually distinguishes the group portion from the own-proxy portion even when the own-proxy name itself contains underscores (e.g., `montreal-spare`). The `AUTO` literal is uppercase to mirror Mihomo's convention for built-in identifiers (`DIRECT`, `REJECT`) — it's a server-emitted special token, not a real group name. Using `Proxies` (capital P, no underscore) for the AUTO dialer-proxy value matches the actual group name from 001 FR-009a, not a synthesized `_proxies` form.
- **Alternatives considered**:
  - **Single underscore separator (`via_region_JP_markham`)**: rejected. Disambiguating where the group ends and the own-proxy starts becomes lossy when own-proxy names contain underscores.
  - **Hyphen separator (`via-region_JP--markham`)**: rejected. Mihomo proxy names allow underscores and hyphens but mixing them inconsistently inside one synthesized identifier is harder to read and easier to typo in custom rules.
  - **`via_PROXIES__<P>` for AUTO (mirroring _region_/_continent_)**: rejected. The literal `AUTO` reads more clearly as "auto-pick whichever group is currently selected globally" — the user's prompt named it AUTO explicitly.

## R4. Field copy semantics for fan-out copies (FR-003)

- **Decision**: Use the existing `cloneNode()` deep-copy helper from `yamlutil.go`, then `setMappingValue(clone, "name", ...)` and `setMappingValue(clone, "dialer-proxy", ...)`. `setMappingValue` semantics: if the key exists, replace the value node in place; otherwise append at the end. Existing field order is preserved on update; new fields land at the tail.
- **Rationale**: Same helpers already used by `RewriteOwn` and the rest of the merge layer — keeps the pipeline uniform. Comments / anchors / explicit YAML tags on the source mapping are not preserved (the merge layer has worked at the parsed-node level since 001; this is the established status quo, documented in the spec's Assumptions).
- **Alternatives considered**:
  - **Manual field-by-field copy**: rejected. `cloneNode()` is already exercised across `MergeProxies`, `MergeProxyGroups`, `RewriteSource`, `RewriteOwn` — duplicating its logic in fan-out would be premature divergence.
  - **Re-marshaling the source own-proxy and re-parsing**: rejected. Slower, no benefit, and would re-introduce yaml.v3's `\Uxxxxxxxx` escape behavior that 006 went out of its way to neutralize at output time.

## R5. `Proxies` selector exclusion mechanics (FR-007/FR-008)

- **Decision**: At the existing `allNames` collection site in `Pipeline.Build()` (lines ~200–205 of `pipeline.go`), filter out any name with a leading `_`. This excludes own-proxies (the only `_`-prefixed entries in `mergedProxies`; region/continent groups live in `mergedGroups`, not `mergedProxies`, so they're not in `allNames` to begin with). The `via_*` fan-out copies are not yet in `mergedProxies` at the time of the filter (fan-out runs later in `Build()`), so they're naturally excluded with no extra logic.
- **Rationale**: One-line filter, locally scoped to the call site, no signature changes to `AppendProxiesGroup`. Preserves `AppendProxiesGroup`'s existing union-with-existing-group semantics for any pre-existing `Proxies` group (e.g., one declared by an upstream).
- **Alternatives considered**:
  - **Add a filter callback parameter to `AppendProxiesGroup`**: rejected. New signature, same outcome. The filter logic is trivial enough to inline.
  - **Filter at `MergeProxies` time (omit own-proxies from `mergedProxies` for the Proxies-group-building step but keep them for emission)**: rejected. Would require tracking two parallel slices through `Build()`; far more invasive than a one-line filter at the call site.
  - **Compute `Proxies` membership only after fan-out and explicitly include only upstream-prefixed proxies**: rejected. Same outcome, more code to read and review.

## R6. Edge case: `Proxies` group already declared upstream

- **Decision**: Do nothing special. `AppendProxiesGroup` already handles the "group exists, augment its members with the union" case; the filter from R5 only governs the names *we contribute*. If an upstream contribution to `Proxies` happened to include `_my-home-trojan` (theoretically impossible after 002's prefix scheme, since upstream never produces leading-underscore names), the union would carry that pre-existing entry through unchanged.
- **Rationale**: Consistent with the existing collision-resolution model; no new failure mode introduced.
- **Alternatives considered**:
  - **Strip `_*` and `via_*` from the post-`AppendProxiesGroup` result**: rejected. Adds a sweep over the group's member list to clean up entries we didn't add, which violates the "filter what we contribute, leave external state alone" principle.

## R7. Observability: structured log line for fan-out activity

- **Decision**: One `slog.Info` line per `Pipeline.Build()` call, immediately after fan-out runs:

```go
slog.Info("fanout-emitted",
    "event", "fanout-emitted",
    "own_proxy_count", len(rewrittenOwnProxies),
    "skipped_explicit_dialer", skippedCount,
    "target_group_count", targetCount,
    "emitted_count", emittedCount,
)
```

- **Rationale**: Constitution Principle V mandates per-merge structured logging of decisions. Fan-out is a merge-time decision (which N-by-M cross-product to materialize, which own-proxies to skip per FR-005). One line per build summarizes the actionable signal — operators can spot "expected 30 fan-out copies, got 0" mid-flight without per-copy noise. Per-copy logging at this volume would be `O(N×(M+1))` lines per build, drowning the signal.
- **Alternatives considered**:
  - **Log only when `skippedCount > 0`**: rejected. The zero-skip case is exactly the steady-state happy path operators want to confirm is healthy; suppressing it makes "no fan-out happening" diagnostics harder.
  - **Per-fan-out-copy `slog.Debug`**: deferred. Useful for one-off debugging but not steady-state observability. If needed later, can be added without API impact (slog allows runtime level filtering).
  - **Counter metric (Prometheus-style)**: out of scope — the project does not currently emit metrics; introducing them is its own initiative.

## Constitution Re-check Summary

All seven decisions remain consistent with the Phase 0 Constitution Check evaluation in `plan.md`. No drift. Phase 1 design proceeds.
