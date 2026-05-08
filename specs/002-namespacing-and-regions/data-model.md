# Data Model: Provider Namespacing & Region Grouping

**Feature**: `002-namespacing-and-regions` | **Date**: 2026-04-30

This feature adds **two** typed entities and **strengthens validation** on one existing entity. No new persistent storage; all state is in-memory and derives from already-loaded config + cache.

Module path: `github.com/junlinchen/honkai-rule-server`.

---

## 1. Strengthened: `SubscriptionRow.Name` validation

Existing entity from 001 (`internal/config/subscriptions.go`). The struct is **unchanged**:

```go
type SubscriptionRow struct {
    Name                string
    Link                string
    Priority            int
    Enable              bool
    TTLSeconds          int
    StaleOnErrorSeconds int
}
```

What changes is the validation logic in `parseSubscriptions` / its row validator:

- **New rule (FR-001)**: `Name` MUST match `^[a-z]+$` — one or more ASCII lowercase letters; no digits, no underscores, no whitespace, no non-ASCII.
- **New outcome**: violations of this rule do **NOT** raise `*ConfigValidationError` (existing 001 type) and do **NOT** abort the load. They produce:
  1. A `slog.Warn`-level structured log line with key `"event": "name-format-violation"` and field `"name": <offending value>`.
  2. The offending row is **excluded** from the returned `[]SubscriptionRow` slice.
  3. Loading continues for all other rows.
- **Existing rules preserved**: the `name` non-empty rule, duplicate-`name` loud-failure, link-URL parse, `priority` integer, `enable` Enable/Disable casing, unknown-column rejection — all unchanged from 001 FR-001b.

**Implementation note**: the validator must run name-format check BEFORE the duplicate-name check, otherwise two rows with the same lowercase form (one violating, one valid) would mis-trigger the duplicate detector. Sequence: name-format → soft-skip if violated → emit warn → continue. Then run duplicate detection on the remaining rows.

**Test coverage**: TC-U-CSV-NAME-01..07 in plan.md.

---

## 2. New: `ServerConfig.FallbackRuleTarget`

Existing struct `internal/config/ServerConfig` gets one new field:

```go
type ServerConfig struct {
    // ... existing fields unchanged ...
    
    // FallbackRuleTarget is the target of the single server-emitted MATCH rule
    // appended at the end of the merged rules block (FR-010).
    // Defaults to "auto"; overridable via the FALLBACK_RULE_TARGET env var.
    // Not validated — passed through verbatim per spec Assumption A7a.
    FallbackRuleTarget string
}
```

**Default**: `"auto"`. Set in `Load()` before the env-var sweep so an unset/empty env preserves the default.

**Env-var binding**: in `Load()`, after the existing `UPSTREAM_USER_AGENT` block:

```go
if v := env.Getenv("FALLBACK_RULE_TARGET"); v != "" {
    cfg.FallbackRuleTarget = v
}
```

**Plumbing**: `cmd/server/main.go` passes `cfg.FallbackRuleTarget` to `merge.NewPipeline(...)` — this requires extending `NewPipeline`'s signature OR adding a `WithFallbackRuleTarget` builder method (mirrors the existing `WithProxiesGroupName` pattern). **Decision**: use a builder method `WithFallbackRuleTarget(string) *Pipeline` for parity.

**Pipeline storage**: new field on `Pipeline`:

```go
type Pipeline struct {
    // ... existing fields unchanged ...
    fallbackRuleTarget string // empty defaults to "auto" inside MergeRules
}
```

**Usage**: `Build()` calls `MergeRules(rulesPerSource, contributing, p.fallbackRuleTarget)`.

**Test coverage**: TC-U-ENV-FALLBACK-01..05 in plan.md.

---

## 3. New: Region-table entry type

Lives in `internal/merge/region_table.go`:

```go
package merge

// regionEntry is one row of the country-indicator → ISO 3166-1 alpha-2 lookup
// table used by region inference (FR-011 / FR-012).
//
// Lookup is by substring match (strings.Contains) and is iterated in slice
// order, so entries that should match more specifically (e.g., 中国香港) MUST
// appear before more general entries (中国) — order is part of the contract.
type regionEntry struct {
    Indicator string // human-readable substring to match against the proxy display name
    Code      string // ISO 3166-1 alpha-2 (uppercase)
    Lang      string // "zh" or "en"; used by precedence rules in inferCountry
}

// regionTable is the package-level, compile-time-fixed table. Order matters
// for determinism (Constitution Principle II) AND for specificity (more
// specific entries first).
var regionTable = []regionEntry{
    // Specific Chinese entries (more specific before more general)
    {Indicator: "中国香港", Code: "HK", Lang: "zh"},
    {Indicator: "中国台湾", Code: "TW", Lang: "zh"},
    {Indicator: "中国澳门", Code: "MO", Lang: "zh"},
    
    // General Chinese entries
    {Indicator: "中国", Code: "CN", Lang: "zh"},
    {Indicator: "美国", Code: "US", Lang: "zh"},
    {Indicator: "日本", Code: "JP", Lang: "zh"},
    {Indicator: "韩国", Code: "KR", Lang: "zh"},
    {Indicator: "香港", Code: "HK", Lang: "zh"},
    {Indicator: "台湾", Code: "TW", Lang: "zh"},
    {Indicator: "臺灣", Code: "TW", Lang: "zh"},
    {Indicator: "新加坡", Code: "SG", Lang: "zh"},
    // ... (full seed list per research.md R8)
    
    // English entries (lowercase substring match against ToLower(name))
    {Indicator: "hong kong", Code: "HK", Lang: "en"},
    {Indicator: "united states", Code: "US", Lang: "en"},
    // ... (full seed list per research.md R8)
}
```

**Validation rules**:

- `Code` MUST be exactly two uppercase ASCII letters. Verified at startup by an `init()` function or by a unit test that walks the table.
- `Lang` MUST be `"zh"` or `"en"`. Same.
- The table MUST NOT contain duplicate `Indicator` values (would cause silent shadowing).

**Test coverage**: TC-U-REGION-CN-*, TC-U-REGION-EN-*, TC-U-REGION-EMOJI-*, TC-U-REGION-EMOJI-DECODE-* in plan.md.

---

## 4. Derived: Region group representation

Region groups are emitted directly as `*yaml.Node` mapping nodes by `internal/merge/region.go`. They are **not** persisted as a Go struct — there is no need; they live only in the merged output.

The function signature:

```go
package merge

// AppendRegionGroups appends one `_region_<CC>` proxy-group of type `select`
// for every distinct country code inferred from the `name` field of every
// **upstream-sourced** proxy in `upstreamPrefixedProxies` (FR-012/FR-013/FR-016).
// Own-proxies are explicitly excluded — they MUST NOT be passed in this
// slice; the caller (Pipeline.Build) is responsible for partitioning the
// merged proxy pool into upstream-vs-own before calling this function.
//
// Groups with empty membership are NOT emitted (FR-013).
//
// The emitted groups are also appended to `proxiesGroup` (the always-present
// `Proxies` selectable group from 001 FR-009a) per FR-015.
//
// Returns the augmented groups slice; the input `groups` is not mutated.
//
// `unmappedLogger` is invoked once per distinct unmapped name fragment
// (FR-014); pass nil to suppress logging.
func AppendRegionGroups(
    groups []*yaml.Node,
    upstreamPrefixedProxies []*yaml.Node,
    proxiesGroupName string,
    unmappedLogger func(fragment string),
) []*yaml.Node
```

**Determinism**: country codes are emitted in alpha-ascending order (`CN, HK, JP, US, ...`). Within a region group, members appear in the order they appeared in `prefixedProxies` (which is itself per-source-priority desc per 001 FR-005a — already deterministic).

**Test coverage**: TC-U-REGION-GROUP-01..02, TC-U-REGION-DETERMINISM-01, TC-U-REGION-PROXIES-01 in plan.md.

---

## 5. Derived: Namespace-rewrite outcome

Namespace rewriting produces no new struct — it mutates clones of the upstream `*yaml.Node` slices. Its only observable side effect is the rewritten names; it does not return a record of what changed (no `[]NamespaceRewrite` result slice). This is intentional: every name change is a uniform `<provider>_<original>` transformation, so a record would be redundant data.

For observability (Principle V), `slog.Debug` lines are emitted at the rewriter's call site enumerating the source name and counts of rewritten proxies / groups / rule targets. Counts are sufficient — individual name pairs would inflate logs without operator value.

**Test coverage**: TC-U-NS-* in plan.md.

---

## 6. Cross-entity invariants

After this feature lands, the following invariants hold for any merged config produced by `Pipeline.Build()`:

| Invariant | Source FR | Test case |
|---|---|---|
| Every proxy `name` either starts with `<provider>_` (where `<provider>` matches `^[a-z]+$`, upstream-sourced) or starts with `_` (own-proxy, FR-007a) | FR-004, FR-007a | TC-I-002-01 |
| Every proxy-group `name` either starts with `<provider>_`, starts with `_region_` (uppercase CC suffix), starts with `_` (own-group, FR-007b), or equals the literal `Proxies` (the always-present FR-009a group) | FR-005, FR-007b, FR-013 | TC-I-002-01 |
| Every rule's target field is `<provider>_<name>`, `_<own-name>`, `_region_<CC>`, `Proxies`, or one of `DIRECT`/`REJECT`/`REJECT-DROP`/`PASS` | FR-006 | TC-I-002-01 |
| Every name in the merged output falls into exactly one of three disjoint name-shape classes: starts with a lowercase ASCII letter (upstream-sourced), starts with `_` (operator/server-emitted), or is a built-in identifier (uppercase) | FR-007d, SC-009 | TC-I-002-01 |
| Region groups (`_region_<CC>`) contain only upstream-sourced proxies — no own-proxies, regardless of own-proxy display-name content | FR-012, FR-013 | TC-I-002-10 |
| The merged `rules:` block ends with **exactly one** `MATCH,<target>` rule, where `<target>` is `ServerConfig.FallbackRuleTarget` (default `auto`) | FR-010, FR-010a | TC-I-002-03/04 |
| No two proxies in the merged config share a name (cross-source impossible by construction; intra-source still loud-fail per existing 001 rules) | FR-007, SC-002 | TC-I-002-08 |
| The merged config is byte-identical across two consecutive runs over identical inputs (Constitution Principle II) | FR-016, SC-007 | TC-I-002-07 |
