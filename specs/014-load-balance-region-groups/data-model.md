# Phase 1 Data Model: Load-Balance Region & Continent Groups

**Feature**: `014-load-balance-region-groups`
**Date**: 2026-05-08

This document specifies the new data types introduced by the feature, their fields, validation rules, and how they thread through the existing pipeline. No new database, file format, or persistent storage is introduced — `LoadBalanceParams` is a single value-typed struct loaded from environment variables at startup and held immutable for the pod's lifetime.

---

## 1. `config.LoadBalanceParams`

**Location**: `internal/config/server.go` (new struct, mirrors the existing `URLTestParams`)

**Purpose**: Holds the six Mihomo `load-balance` proxy-group fields, configurable via the `LOAD_BALANCE_*` environment variables (spec FR-004).

```go
// LoadBalanceParams holds the six Mihomo load-balance health-check + strategy
// fields the server emits on every auto-emitted _lb_region_* / _lb_continent_*
// proxy group per 014 FR-003 / FR-004. Loaded by Load() from the six
// LOAD_BALANCE_* env vars and validated per FR-005 (loud-fail per Constitution
// Principle III).
type LoadBalanceParams struct {
    URL             string // YAML field "url".              Default: https://www.gstatic.com/generate_204.
    IntervalSeconds int    // YAML field "interval".         Default: 300. Must be >= 1.
    TimeoutMS       int    // YAML field "timeout".          Default: 1500. Must be >= 1.
    MaxFailedTimes  int    // YAML field "max-failed-times". Default: 3. Must be >= 1.
    Lazy            bool   // YAML field "lazy".             Default: true.
    Strategy        string // YAML field "strategy".         Default: "round-robin".
                           // Must be one of: round-robin, consistent-hashing, sticky-sessions.
}
```

### Field defaults (FR-003)

| Field | Default | Source |
|---|---|---|
| `URL` | `https://www.gstatic.com/generate_204` | User-supplied example; matches 012's url-test default. |
| `IntervalSeconds` | `300` | User-supplied example. |
| `TimeoutMS` | `1500` | User-supplied example. |
| `MaxFailedTimes` | `3` | User-supplied example. |
| `Lazy` | `true` | User-supplied example. |
| `Strategy` | `round-robin` | User-supplied example. |

### Validation rules (FR-005)

Implemented in a `Validate()` method (mirroring 012's pattern):

| Field | Rule | Error format |
|---|---|---|
| `URL` | None — accepted as-is. | n/a |
| `IntervalSeconds` | Integer ≥ 1. | `LOAD_BALANCE_INTERVAL_SECONDS=%q (must be a positive integer)` or `LOAD_BALANCE_INTERVAL_SECONDS=%d (must be >= 1)` |
| `TimeoutMS` | Integer ≥ 1. | same shape |
| `MaxFailedTimes` | Integer ≥ 1. | same shape |
| `Lazy` | `strconv.ParseBool`-acceptable: `1, t, T, TRUE, true, True, 0, f, F, FALSE, false, False`. | `LOAD_BALANCE_LAZY=%q (must be true or false)` |
| `Strategy` | Membership in `{round-robin, consistent-hashing, sticky-sessions}` (case-sensitive). | `LOAD_BALANCE_STRATEGY=%q (must be round-robin, consistent-hashing, or sticky-sessions)` |

Validation accumulates all errors into a single `error` returned from `Load()`:
`LoadBalanceParams validation failed: KEY1=... (reason); KEY2=... (reason); ...`

### Lifecycle

1. **Boot**: `cmd/server/main.go` calls `config.Load(env)` which reads the six env vars, applies defaults, runs `Validate()`. Any error → `log.Fatalf` → non-zero exit (reuses existing path).
2. **Runtime (read-only)**: `ServerConfig.LoadBalanceParams` is referenced by the request-time pipeline assembly in `internal/server/app.go` and threaded into `merge.Pipeline` via `WithLoadBalanceParams`. No mutation after `Load()`.
3. **Reload**: ConfigMap-driven hot reload is NOT in scope (mirrors 012's policy). A pod restart is required to pick up env-var changes.

### Holding location

`ServerConfig.LoadBalanceParams` (new field on the existing struct in `internal/config/server.go`).

---

## 2. `merge.LoadBalanceParams` (mirror struct)

**Location**: `internal/merge/region.go` (new struct, alongside the existing `merge.URLTestParams`)

**Purpose**: Identical fields to `config.LoadBalanceParams`. Exists so `internal/merge` does not import `internal/config` (Constitution Principle I — single transformation core, no upward imports). The bridge between the two structs is a one-shot copy at pipeline-build time:

```go
// In internal/server/app.go (pipeline construction site):
mergeLBParams := merge.LoadBalanceParams{
    URL:             cfg.LoadBalanceParams.URL,
    IntervalSeconds: cfg.LoadBalanceParams.IntervalSeconds,
    TimeoutMS:       cfg.LoadBalanceParams.TimeoutMS,
    MaxFailedTimes:  cfg.LoadBalanceParams.MaxFailedTimes,
    Lazy:            cfg.LoadBalanceParams.Lazy,
    Strategy:        cfg.LoadBalanceParams.Strategy,
}
pipe := merge.NewPipeline(...).WithLoadBalanceParams(mergeLBParams)
```

The exact same shape exists for `URLTestParams` today; this is project convention, not a new pattern.

---

## 3. `Pipeline.loadBalanceParams` (new field on `merge.Pipeline`)

**Location**: `internal/merge/pipeline.go`

**Field**:
```go
type Pipeline struct {
    // ... existing fields ...

    // loadBalanceParams holds the six lb fields written into every auto-emitted
    // _lb_region_* / _lb_continent_* proxy group per 014 FR-001..FR-005. Set
    // via WithLoadBalanceParams; zero-value yields all-empty fields, which
    // would emit an unusable group — callers MUST set this from
    // cfg.LoadBalanceParams (a pipeline constructed without WithLoadBalanceParams
    // is a misconfiguration).
    loadBalanceParams LoadBalanceParams
}
```

**Builder method**:
```go
// WithLoadBalanceParams sets the load-balance parameter set for emitted
// _lb_region_* / _lb_continent_* groups (014 FR-001..FR-005). Returns the
// receiver for chaining.
func (p *Pipeline) WithLoadBalanceParams(params LoadBalanceParams) *Pipeline {
    p.loadBalanceParams = params
    return p
}
```

**Threading**: `Pipeline.Build()` passes `p.loadBalanceParams` into `AppendRegionGroups` and `AppendContinentGroups` alongside the existing `p.urlTestParams`.

---

## 4. Emit shape — `_lb_region_<CC>` / `_lb_continent_<CONT>` groups

Each emitted group is a YAML mapping node with the following key/value pairs in the documented order (FR-006 — matches the user-supplied example):

| Position | Key | Value source |
|---|---|---|
| 0 | `name` | `_lb_region_<CC>` or `_lb_continent_<CONT>` |
| 1 | `type` | `load-balance` (literal string) |
| 2 | `proxies` | Sequence: same member list as the corresponding `_region_<CC>` / `_continent_<CONT>` sibling. For region: list of upstream-prefixed proxy names. For continent: flat union of upstream-prefixed proxy names from the constituent regions per 003 FR-011 (NOT region-group references). |
| 3 | `url` | `loadBalanceParams.URL` |
| 4 | `interval` | `loadBalanceParams.IntervalSeconds` (integer) |
| 5 | `lazy` | `loadBalanceParams.Lazy` (boolean) |
| 6 | `strategy` | `loadBalanceParams.Strategy` (string) |
| 7 | `timeout` | `loadBalanceParams.TimeoutMS` (integer) |
| 8 | `max-failed-times` | `loadBalanceParams.MaxFailedTimes` (integer) |

The on-the-wire ordering is enforced post-emit by `output/subscription_mode.go::reorderProxyGroupFields`. The emit-site order may vary (helpers like `setMappingValue` always append at end if the key is absent); reorder is responsible for the canonical layout.

### Helper function

A new `newLoadBalanceGroup` helper in `internal/merge/region.go`, analogous to the existing `newURLTestGroup`:

```go
// newLoadBalanceGroup constructs a load-balance-type proxy group with the six
// load-balance fields populated from params per 014 FR-001..FR-006.
// Field-emission order matches the user-supplied example; the output formatter's
// reorderProxyGroupFields pass enforces the final on-the-wire order.
func newLoadBalanceGroup(name string, members []string, params LoadBalanceParams) *yaml.Node {
    g := &yaml.Node{Kind: yaml.MappingNode}
    setMappingValue(g, "name", &yaml.Node{Kind: yaml.ScalarNode, Value: name})
    setMappingValue(g, "type", &yaml.Node{Kind: yaml.ScalarNode, Value: "load-balance"})
    setMappingMembers(g, "proxies", members)
    setMappingValue(g, "url", &yaml.Node{Kind: yaml.ScalarNode, Value: params.URL})
    setMappingValue(g, "interval", &yaml.Node{Kind: yaml.ScalarNode, Value: strconv.Itoa(params.IntervalSeconds), Tag: "!!int"})
    setMappingValue(g, "lazy", &yaml.Node{Kind: yaml.ScalarNode, Value: strconv.FormatBool(params.Lazy), Tag: "!!bool"})
    setMappingValue(g, "strategy", &yaml.Node{Kind: yaml.ScalarNode, Value: params.Strategy})
    setMappingValue(g, "timeout", &yaml.Node{Kind: yaml.ScalarNode, Value: strconv.Itoa(params.TimeoutMS), Tag: "!!int"})
    setMappingValue(g, "max-failed-times", &yaml.Node{Kind: yaml.ScalarNode, Value: strconv.Itoa(params.MaxFailedTimes), Tag: "!!int"})
    return g
}
```

---

## 5. Fan-out copies — `via_lb_region_<CC>__<P>` / `via_lb_continent_<CONT>__<P>`

Generated by the existing `internal/merge/fanout.go::AppendFanoutProxies` function after the predicate is widened (research Decision 7). Per (own-proxy, lb-group) pair where the own-proxy has no explicit `dialer-proxy`:

| Field | Value |
|---|---|
| `name` | `via_<G>__<P>` where `<G>` = lb-group name with leading `_` stripped (so `_lb_region_JP` → `lb_region_JP`), `<P>` = own-proxy name with leading `_` stripped. |
| `dialer-proxy` | The full lb-group name including the leading `_` (e.g., `_lb_region_JP`). |
| All other fields | Deep-cloned verbatim from the source own-proxy YAML node. |

Field order in the cloned mapping follows `setMappingValue`'s "replace in place if present, else append at end" semantic. For own-proxies that lack a `dialer-proxy` field (the only kind that reach this loop) the new `dialer-proxy` is appended at the tail — same as 008's existing fan-out copies.

---

## 6. Always-present `Proxies` selector — gain new direct members

The existing `AppendRegionGroups` / `AppendContinentGroups` helpers each end by adding their emitted group names to the `Proxies` selector group's `proxies:` list. With paired emission (research Decision 6), the names `_region_<CC>` and `_lb_region_<CC>` are appended in interleaved order — preserving the visual pairing from the `proxy-groups:` block.

No code change is required in the `Proxies`-membership block beyond the existing one — the new lb names are already present in `regionGroupNames` / `continentGroupNames` by the time the membership append runs.

The fan-out exclusion rule from 008 FR-008 (no `via_*` entries in `Proxies`) is unchanged and naturally excludes `via_lb_*` entries because they share the `via_` prefix.

---

## 7. State transitions

This feature introduces no new state transitions — `LoadBalanceParams` is immutable for the pod's lifetime. The merged config is a pure function of `(cache state, config rows, own-proxies, clock, urlTestParams, loadBalanceParams)`. Constitution Principle II (deterministic transformation) is preserved.

---

## 8. Relationships to existing entities

| Existing entity | Relationship |
|---|---|
| `_region_<CC>` group (002 / 012) | The lb sibling shares its member list verbatim. The url-test group is unchanged (FR-007). |
| `_continent_<CONT>` group (003 / 012) | The lb sibling shares its (flat) member list verbatim per 003 FR-011. The url-test group is unchanged (FR-007). |
| `_region_UNKNOWN` group (003) | Has an `_lb_region_UNKNOWN` sibling per FR-001's "including `_region_UNKNOWN`" clause. |
| Always-present `Proxies` selector (001 FR-009a) | Gains `_lb_region_*` and `_lb_continent_*` entries as direct members (FR-010). |
| 008 fan-out (`via_<G>__<P>`, `via_AUTO__<P>`) | Existing copies unchanged. New `via_lb_region_<CC>__<P>` / `via_lb_continent_<CONT>__<P>` copies appear from the widened predicate. AUTO is unchanged (no AUTO_lb variant — clarification Q1 answer). |
| 008 per-own-proxy skip rule (FR-005) | Applies uniformly to lb fan-out (FR-016). An own-proxy with `dialer-proxy:` declared in `own-proxies.yaml` generates zero `via_*` entries (no AUTO, no per-region, no per-lb). |
| Operator-declared own-groups (002 FR-007b) | Not auto-mirrored as `_lb_<group>` (FR-019). Operators wanting a load-balance own-group declare it manually in `own-proxies.yaml`. |
| Custom user-defined proxy-groups (003) | Untouched. The `_lb_*` rewrite applies only to server-emitted region/continent groups (FR-018). |

---

## 9. Field count and counts-per-fixture

For a fixture with N own-proxies (none with explicit `dialer-proxy`) and M url-test region/continent groups (per 012):

- New `_lb_*` proxy groups in `proxy-groups:`: M (1:1 with url-test groups).
- New `via_lb_*` entries in `proxies:`: N × M (paired with each `via_*` from 008).
- New entries in `Proxies` selector's `proxies:`: M (interleaved with existing url-test references).
- Total own-derived entries in `proxies:` (originals + fan-outs) post-feature: N × (1 + 2M) + N originals = N + N + 2NM = 2N + 2NM = 2N(1 + M). Pre-feature was N + N(1 + M) = 2N + NM. Net increase: NM.

These counts are asserted in the integration test (SC-006).
