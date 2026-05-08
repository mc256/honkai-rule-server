# Data Model: URL-Test for Auto-Emitted Regional & Continent Proxy Groups

**Feature**: 012-url-test-region-groups
**Date**: 2026-05-02

This document captures the new struct, the env-var binding, the validation rules, and the emit-shape change. All additions are pure (no new I/O beyond env-var reads at startup).

---

## New struct: `config.URLTestParams`

Location: `internal/config/server.go`

```go
// URLTestParams holds the five health-check fields the server emits on
// every auto-emitted _region_* / _continent_* proxy group per 012 FR-001
// through FR-004. Loaded from env vars at startup (see Load); validated
// per FR-004a (loud-fail on bad input).
type URLTestParams struct {
    URL             string // YAML field "url".              Default: "https://www.gstatic.com/generate_204".
    IntervalSeconds int    // YAML field "interval".         Default: 10. Must be >= 1.
    TimeoutMS       int    // YAML field "timeout".          Default: 3000. Must be >= 1.
    MaxFailedTimes  int    // YAML field "max-failed-times". Default: 3. Must be >= 1.
    Lazy            bool   // YAML field "lazy".             Default: true.
}
```

**Why a dedicated struct vs. five fields on `Server`**: groups concerns. The five values are conceptually one thing ("how to probe a url-test group"), and the struct passes through `Pipeline` to `region.go` as a single value. A future evolution (per-CC overrides) drops in by extending the struct (e.g., adding an `Overrides map[string]URLTestParams` field), keeping the rest of the codebase stable.

**Exported, by-value passing**: the struct is small (≤40 bytes) and immutable after `Load()`. Pass-by-value matches the existing `*Pipeline` builder methods that take primitive arguments (`WithProxiesGroupName(string)`, `WithFallbackRuleTarget(string)`).

---

## Env-var binding (per FR-004 + FR-004a)

| Env var | Default | Type | Validation |
|---|---|---|---|
| `URL_TEST_URL` | `https://www.gstatic.com/generate_204` | string | empty / unset → use default; non-empty → use as-is (no URL parsing) |
| `URL_TEST_INTERVAL_SECONDS` | `10` | int | empty / unset → use default; non-empty → must parse as `strconv.Atoi` and `>= 1` |
| `URL_TEST_TIMEOUT_MS` | `3000` | int | empty / unset → use default; non-empty → must parse as `strconv.Atoi` and `>= 1` |
| `URL_TEST_MAX_FAILED_TIMES` | `3` | int | empty / unset → use default; non-empty → must parse as `strconv.Atoi` and `>= 1` |
| `URL_TEST_LAZY` | `true` | bool | empty / unset → use default; non-empty → must parse as `strconv.ParseBool` (accepts `1`, `t`, `T`, `TRUE`, `true`, `True`, `0`, `f`, `F`, `FALSE`, `false`, `False`) |

**Loading order** in `Load()`:
1. Initialize `URLTestParams` with the five defaults from the table.
2. Read each env var via `env.Getenv(key)`. If empty / unset, leave the field at its default.
3. If non-empty, parse and assign. If parsing fails or the result violates the validation rule, append a structured error (see "Validate" below) and continue (so all errors surface in one shot, rather than the operator fixing one and discovering the next on the next restart).
4. After loading all env vars, call `Validate()`. If it returns non-nil, propagate up; `cmd/server/main.go` exits non-zero with the structured error log.

---

## Validation: `URLTestParams.Validate() error`

Returns a single error wrapping all field-level violations. Format:

```text
URLTestParams validation failed: URL_TEST_INTERVAL_SECONDS=0 (must be >= 1); URL_TEST_LAZY="maybe" (must be true or false)
```

Each violation is one semicolon-separated clause that names the offending env var, its value (quoted for strings, bare for numbers), and the constraint. The operator can fix all errors before the next restart.

Implementation can use a simple `[]string` accumulator → `errors.New(strings.Join(...))`, or `errors.Join` (Go 1.20+) with one error per violation. Either is fine; the textual format is what matters for operator readability.

---

## `Pipeline.urlTestParams` (new field)

Location: `internal/merge/pipeline.go`

```go
type Pipeline struct {
    // ... existing fields ...

    // urlTestParams is the health-check parameter set emitted on every
    // auto-emitted _region_* / _continent_* proxy group per 012. Set via
    // WithURLTestParams; zero-value means "use FR-003 defaults" (matches
    // pre-012 behavior of select-type groups with no health checks, which
    // the spec explicitly replaces).
    urlTestParams URLTestParams
}
```

Note: `URLTestParams` is in the `config` package, not `merge`. The cleanest signature uses the qualified name, e.g. `urlTestParams config.URLTestParams`. Alternatively, declare a local `merge.URLTestParams` struct mirroring `config.URLTestParams` to avoid the cross-package coupling — `merge` would not import `config`. The plan recommends the latter (mirror locally) to keep `internal/merge/` dependency-free per Constitution Principle I.

**Decision (mirrored locally)**: declare `merge.URLTestParams` as a separate struct shape-identical to `config.URLTestParams`. The route handler / startup code copies fields across the boundary. This adds ~10 lines of declaration + a copy function but keeps the merge package free of `internal/config` imports.

```go
// In internal/merge/region.go (or a new internal/merge/url_test.go):
type URLTestParams struct {
    URL             string
    IntervalSeconds int
    TimeoutMS       int
    MaxFailedTimes  int
    Lazy            bool
}
```

**Builder method**:

```go
// WithURLTestParams sets the health-check parameter set for emitted
// _region_* / _continent_* groups (012 FR-001..FR-004). Returns the
// receiver for chaining.
func (p *Pipeline) WithURLTestParams(params URLTestParams) *Pipeline {
    p.urlTestParams = params
    return p
}
```

**Wired** in `cmd/server/main.go` next to the existing `WithProxiesGroupName(...).WithFallbackRuleTarget(...)` chain. The startup code copies `cfg.URLTestParams` (config package) into `merge.URLTestParams`.

---

## Emit shape change

Location: `internal/merge/region.go`

**Before** (both emit sites: line 57–61 and line 168–172):

```go
g := &yaml.Node{Kind: yaml.MappingNode}
setMappingValue(g, "name", &yaml.Node{Kind: yaml.ScalarNode, Value: groupName})
setMappingValue(g, "type", &yaml.Node{Kind: yaml.ScalarNode, Value: "select"})
setMappingMembers(g, "proxies", members)
groups = append(groups, g)
```

**After** (helper-applied to both emit sites):

```go
g := newURLTestGroup(groupName, members, p.urlTestParams)
groups = append(groups, g)
```

where `newURLTestGroup` is a small new helper in `region.go`:

```go
// newURLTestGroup constructs a url-test-type proxy group with all five
// health-check fields populated from params. Field order at construction
// time: name, type, proxies, url, interval, timeout, max-failed-times,
// lazy. The output formatter's reorderProxyGroupFields pass enforces the
// final on-the-wire order (012 FR-007 + 004's convention).
func newURLTestGroup(name string, members []string, params URLTestParams) *yaml.Node {
    g := &yaml.Node{Kind: yaml.MappingNode}
    setMappingValue(g, "name", &yaml.Node{Kind: yaml.ScalarNode, Value: name})
    setMappingValue(g, "type", &yaml.Node{Kind: yaml.ScalarNode, Value: "url-test"})
    setMappingMembers(g, "proxies", members)
    setMappingValue(g, "url", &yaml.Node{Kind: yaml.ScalarNode, Value: params.URL})
    setMappingValue(g, "interval", &yaml.Node{Kind: yaml.ScalarNode, Value: strconv.Itoa(params.IntervalSeconds), Tag: "!!int"})
    setMappingValue(g, "timeout", &yaml.Node{Kind: yaml.ScalarNode, Value: strconv.Itoa(params.TimeoutMS), Tag: "!!int"})
    setMappingValue(g, "max-failed-times", &yaml.Node{Kind: yaml.ScalarNode, Value: strconv.Itoa(params.MaxFailedTimes), Tag: "!!int"})
    setMappingValue(g, "lazy", &yaml.Node{Kind: yaml.ScalarNode, Value: strconv.FormatBool(params.Lazy), Tag: "!!bool"})
    return g
}
```

The two functions (`AppendRegionGroups`, `AppendContinentGroups`) accept a new `params URLTestParams` argument and pass it through. Their callers in `pipeline.go::Build()` use `p.urlTestParams`.

---

## Field ordering (output formatter extension)

Location: `internal/output/subscription_mode.go::reorderProxyGroupFields`

**Before** (function only positions `name`, `type`, `proxies`):

```go
func reorderProxyGroupFields(n *yaml.Node) {
    if n.Kind != yaml.MappingNode || len(n.Content) < 2 {
        return
    }
    moveFieldToPosition(n, "name", 0)
    moveFieldToPosition(n, "type", 2)
    moveFieldToPosition(n, "proxies", 4)
}
```

**After** (also positions the five url-test fields when present):

```go
func reorderProxyGroupFields(n *yaml.Node) {
    if n.Kind != yaml.MappingNode || len(n.Content) < 2 {
        return
    }
    moveFieldToPosition(n, "name", 0)
    moveFieldToPosition(n, "type", 2)
    moveFieldToPosition(n, "proxies", 4)
    // 012 FR-007: url-test groups carry five additional fields in this
    // order. moveFieldToPosition is a no-op when the key is absent, so
    // calling these on non-url-test groups is safe.
    moveFieldToPosition(n, "url", 6)
    moveFieldToPosition(n, "interval", 8)
    moveFieldToPosition(n, "timeout", 10)
    moveFieldToPosition(n, "max-failed-times", 12)
    moveFieldToPosition(n, "lazy", 14)
}
```

Calling `moveFieldToPosition` on a non-url-test group (e.g., the `Proxies` selector) is a safe no-op because the function returns early when the key is absent. So we don't need a `type == "url-test"` gate.

---

## Tests added

### `internal/config/server_test.go`

Table-driven tests for `URLTestParams` env loading + validation:

| Case | Input | Expected |
|---|---|---|
| All defaults (no env vars set) | `MapEnv{}` | URL=default, IntervalSeconds=10, TimeoutMS=3000, MaxFailedTimes=3, Lazy=true |
| All five overridden | `URL_TEST_URL=https://example.com/204; URL_TEST_INTERVAL_SECONDS=30; URL_TEST_TIMEOUT_MS=5000; URL_TEST_MAX_FAILED_TIMES=5; URL_TEST_LAZY=false` | values flow through |
| Empty string treated as unset | `URL_TEST_INTERVAL_SECONDS=""` | falls back to default 10 |
| Non-integer | `URL_TEST_INTERVAL_SECONDS=abc` | Validate returns error mentioning the env var + value |
| Zero | `URL_TEST_INTERVAL_SECONDS=0` | Validate returns "must be >= 1" |
| Negative | `URL_TEST_TIMEOUT_MS=-100` | Validate returns "must be >= 1" |
| Bool variant accepted | `URL_TEST_LAZY=False` | parses to false |
| Bool gibberish | `URL_TEST_LAZY=maybe` | Validate returns error |
| Multiple violations | two bad env vars | Validate returns error mentioning BOTH (one error, semicolon-joined) |

### `internal/merge/region_test.go`

Tests for the emit shape (after construction, before any output-layer formatting):

| Case | Input | Expected |
|---|---|---|
| Region group, default params | one country with two proxies | emitted node has `type=url-test`, `url=https://www.gstatic.com/generate_204`, `interval=10`, `timeout=3000`, `max-failed-times=3`, `lazy=true` |
| Region group, overridden params | URLTestParams{URL: "https://internal/", IntervalSeconds: 60, ...} | emitted node carries the overridden values verbatim |
| Continent group | three regions | emitted node has the same five fields with passed-through values |
| `_region_UNKNOWN` group | unclassified proxies | emitted node also has type=url-test (012 FR-001 prefix rule applies) |

### `internal/output/subscription_mode_test.go`

One additional test for url-test field ordering:

- Construct a `_region_JP` group with the five fields in scrambled order. Run `reorderProxyGroupFields`. Assert content order is `[name, JP, type, url-test, proxies, ..., url, ..., interval, ..., timeout, ..., max-failed-times, ..., lazy, ...]`.

### `internal/integration/testdata/snapshots/`

After implementation, run `UPDATE_SNAPSHOTS=true` and inspect the diff. Expected: every `_region_*` / `_continent_*` group block in every fixture's snapshot has its `type:` line change and gains five fields; nothing else changes.

---

## Startup logging (FR-008)

Location: `internal/server/app.go::Run` (or wherever the existing startup banner emits)

```go
slog.Info("url_test_params resolved",
    "url", cfg.URLTestParams.URL,
    "interval_seconds", cfg.URLTestParams.IntervalSeconds,
    "timeout_ms", cfg.URLTestParams.TimeoutMS,
    "max_failed_times", cfg.URLTestParams.MaxFailedTimes,
    "lazy", cfg.URLTestParams.Lazy,
)
```

Single info-level line at startup, immediately after the existing token-store / subscription-CSV resolved-config logs. Operator can `kubectl logs deploy/honkai-rule-server -n cms | grep url_test_params` to verify the active values without inspecting the served body.
