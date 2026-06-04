# Phase 0 Research: Rule Set Support

All Technical Context items resolved against the existing codebase; no external
NEEDS CLARIFICATION remained after the spec's interactive scope decision
(**Support & namespace**). Decisions below are grounded in the current
`internal/merge` and `internal/output` implementations.

## D1 — Where to read the `rule-providers:` block

**Decision**: Read it in `Pipeline.Build()`'s existing per-source cache walk, with
a new `findChildMapping(root, "rule-providers")` helper in `yamlutil.go` (sibling
to `findChildSequence`). `rule-providers` is a YAML *mapping* (key = provider
name, value = provider definition mapping), so the sequence helper does not apply.

**Rationale**: The build loop already parses each cached payload and pulls
`proxies` / `proxy-groups` / `rules` via `findChildSequence`. Adding a mapping
read in the same pass reuses the parse and keeps the source-by-source structure.

**Alternatives considered**:
- *Read in the fetcher/cache layer.* Rejected — the cache stores raw payloads; the
  merge layer is the single place that interprets payload structure (Principle I).
- *Unmarshal into a typed struct.* Rejected — the rest of the pipeline manipulates
  `*yaml.Node` to preserve upstream field order/comments/styles and to round-trip
  unknown fields verbatim. A typed struct would drop unknown provider fields.

## D2 — Representing per-source providers (determinism)

**Decision**: Carry each source's providers as the cloned `*yaml.Node`
MappingNode (ordered key/value pairs), namespaced in place. Merge by appending
each source's pairs into one fresh MappingNode in the existing deterministic
`contributing` order. Reference-filter by walking the merged node's pairs in
order and dropping pairs whose key is not in the referenced set.

**Rationale**: Principle II forbids relying on Go map iteration order. A
`*yaml.Node` mapping is an ordered slice (`Content[i]`=key, `Content[i+1]`=value),
so iteration is deterministic and upstream key order is preserved. Namespacing
guarantees cross-source key uniqueness, so a plain append needs no conflict
resolution.

**Alternatives considered**:
- *`map[string]*yaml.Node` per source.* Rejected — reintroduces map iteration
  nondeterminism at merge/emit time.
- *Sort merged keys alphabetically.* Rejected — unnecessary; source-order +
  upstream-order is already deterministic and keeps each source's providers
  grouped, which is more readable for operators.

## D3 — Namespacing the `RULE-SET` provider-name field

**Decision**: Extend the per-rule rewriter in `namespace.go`. Today
`rewriteRuleTarget` rewrites only the *target* (rightmost non-modifier field). Add
a step: when `parts[0] == "RULE-SET"` and `len(parts) >= 2`, prefix `parts[1]`
(the provider name) with `sourceName + "_"`. The target rewrite still runs and
correctly identifies `parts[2]` (e.g. `DIRECT`) as the target.

**Rationale**: Keeps all per-rule field namespacing in one function with one set of
split/join semantics, consistent with how proxy/group/target prefixing already
works. Provider names are never built-in identifiers, so the prefix is
unconditional (unlike the target, which skips `DIRECT`/`REJECT`/…).

**Edge handling**: `RULE-SET,Local-IP,DIRECT,no-resolve` → split is
`[RULE-SET, Local-IP, DIRECT, no-resolve]`; `parts[1]` is prefixed; the existing
modifier-aware target scan leaves `DIRECT` (built-in) unchanged and `no-resolve`
in place. Result: `RULE-SET,srcA_Local-IP,DIRECT,no-resolve`.

**Alternatives considered**:
- *Separate second pass over rules in the pipeline.* Rejected — two split/join
  passes per rule and a second place that must know the RULE-SET layout; more
  surface for drift.

## D4 — Namespacing the provider definition body (`path`, `proxy`)

**Decision**: In `ruleset.go`'s per-source provider rewrite, for each provider:
(1) prefix the mapping key with `sourceName + "_"`; (2) if a `path:` field is
present, rewrite it to a source-distinct value derived from the namespaced key
(replace the basename, preserve directory + extension, e.g.
`./ruleset/Local-IP.mrs` → `./ruleset/srcA_Local-IP.mrs`); (3) if a `proxy:` field
is present and its value is not a built-in target, prefix it with `sourceName +
"_"` (reusing `builtinTargets`). All other fields (`type`, `behavior`, `format`,
`url`, `interval`, …) are preserved verbatim.

**Rationale**: FR-008 — two sources defining `Local-IP` would otherwise both write
`./ruleset/Local-IP.mrs` on the client and clobber each other. Deriving the path
from the (already unique) namespaced key makes paths unique by construction. FR-007
— a provider may fetch its ruleset *through* a proxy/group, which is a source-scoped
name that must be prefixed exactly like a group member reference; built-ins like
`DIRECT` stay as-is.

**Alternatives considered**:
- *Drop the `path` field and let Mihomo auto-derive.* Rejected — relies on
  client-version behavior and could still collide; explicit distinct paths are
  safer and match how upstreams author the field.
- *Hash the path.* Rejected — opaque and harder to debug than the readable
  namespaced basename.

## D5 — When to drop unbacked `RULE-SET` rules (FR-009)

**Decision**: Drop per source, *before* `MergeUnifiedRules`. After a source's
rules and providers are namespaced, remove any `RULE-SET,<name>,…` whose `<name>`
is not a key in that source's namespaced provider set; emit an `slog` event per
drop.

**Rationale**: `MergeUnifiedRules` builds the three parallel slices (`Rules`,
`Priorities`, `Contributors`) from scratch from each contributor's rule list.
Dropping before the merge means the parallel slices are aligned by construction —
no post-hoc index surgery (which is what would be needed if we dropped after the
merge, and which would risk desyncing the priority-comment rendering). Because
names are namespaced per source, a global post-merge check would be equivalent,
but the per-source point is simpler and localizes the log to the offending source.

**Alternatives considered**:
- *Loud-fail the whole build on an unbacked reference.* Rejected — violates the
  spec's robustness goal (US3); the server fans out to many clients and one bad
  upstream must not take everyone down. Consistent with 002's warn-and-skip
  precedent for malformed upstream input and 015's logged repair.
- *Keep the rule, synthesize an empty provider.* Rejected — an empty/unfetchable
  provider is itself a load risk and hides the upstream error.

## D6 — Building the merged block + pruning unreferenced providers (FR-005/006/010)

**Decision**: After the rule slice is final (post `MergeUnifiedRules`, and after
015's `PruneEmptyProxyGroups`, which only retargets rules and never changes the
provider field), scan the final `Rules` for `RULE-SET` lines and collect the set
of referenced provider names. Build `MergedConfig.RuleProviders` as a fresh
MappingNode containing only the merged providers whose key is in that set. If the
set is empty (no surviving `RULE-SET` rule), leave `RuleProviders` nil so the
output adapter emits no `rule-providers:` key (FR-006).

**Rationale**: Trailing-rule drop (002) and the per-source unbacked drop (D5) can
remove `RULE-SET` rules, so the authoritative reference set is only known from the
*final* rule slice. Pruning unreferenced providers (FR-010) avoids shipping a
provider the client would fetch but never consult.

**Ordering vs. 015 prune**: Independent. 015 rewrites a rule's *target* when its
target group was pruned; it never touches the `RULE-SET` provider field (field[1])
and never removes a rule. So collecting references after 015 is safe and uses the
truly-final slice.

## D7 — Threading to output and emit placement

**Decision**: Add `RuleProviders *yaml.Node` to `MergedConfig`. In
`output/subscription_mode.go` `Render`, after the existing `setMappingValue` calls
for `proxies`/`proxy-groups`/`rules`, add `if merged.RuleProviders != nil {
setMappingValue(root, "rule-providers", merged.RuleProviders) }`. The template has
no `rule-providers` key (verified — only a comment mentions it), so
`setMappingValue` appends it at the end of the document mapping. The existing
`stripComments` + `resetScalarStyles` passes already run over the whole document,
so the new block is normalized like everything else.

**Rationale**: One nullable field + one guarded emit keeps both delivery modes
mode-agnostic (Principle I). Mihomo does not require `rule-providers` to appear in
a particular position relative to `rules`, so end-of-document placement is valid.

**Alternatives considered**:
- *Insert `rule-providers` immediately before `rules` for readability.* Deferred —
  cosmetic; would require positional insertion logic. End-append is simplest and
  valid. Can revisit if operators ask.
- *No field-order normalization within provider defs.* Accepted — provider defs are
  small and upstream order is acceptable; no FR mandates a field order (contrast
  proxy-groups, which 004/012/014 do mandate).

## D8 — Snapshot strategy (Principle IV / FR-013)

**Decision**: Add one new integration fixture (an upstream payload with a
`rule-providers:` block and `RULE-SET` rules, including one unbacked reference to
exercise US3) registered in the integration `subscriptions.csv`, with a new
committed snapshot `served-config-ruleset.snap.yaml`. Do not touch existing
fixtures/snapshots.

**Rationale**: No existing fixture contains `RULE-SET` / `rule-providers` (grep
over `internal/**` testdata returns nothing), so every existing snapshot is
byte-unchanged, directly demonstrating FR-013. The new fixture/snapshot is the
real-merged-input integration coverage Principle IV requires for merge-core
changes.
