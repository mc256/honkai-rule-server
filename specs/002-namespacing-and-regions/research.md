# Research: Provider Namespacing & Region Grouping (Phase 0)

**Feature**: `002-namespacing-and-regions` | **Date**: 2026-04-30

This document records the technical decisions made during Phase 0. The spec resolved its single `[NEEDS CLARIFICATION]` marker before this phase started (FR-010 final fallback rule = `MATCH,auto` overridable via `FALLBACK_RULE_TARGET`); research therefore focuses on best-practice selection for already-decided requirements rather than open questions.

---

## R1. Where to apply the prefix rewrite — pre-merge per source vs. post-merge

- **Decision**: **Pre-merge, per source**. Each source's loaded `proxies` / `proxy-groups` / `rules` slices are rewritten by `internal/merge/namespace.RewriteSource(name, proxies, groups, rules)` *before* being handed to `MergeProxies` / `MergeProxyGroups` / `MergeRules`.
- **Rationale**: (a) the existing merge primitives (`internal/merge/proxies.go`, `proxy_groups.go`, `rules.go`) are unchanged in shape — they continue to operate on opaque named entities, just with already-prefixed names; (b) any cross-reference inside a source (a group's `proxies:` member list pointing to a same-source proxy by name) is rewritten consistently because both endpoints are rewritten in the same pass; (c) testing the rewriter is a pure-function test on `*yaml.Node` — no need to plumb merge-state context through; (d) the 001 collision-suffix path (`<name>@<source>`) becomes structurally dead for cross-source collisions, which is the desired outcome (cross-source collisions are now impossible).
- **Alternatives considered**:
  - **Post-merge**, walking the merged structure and rewriting names whose source-of-origin is recorded out-of-band: rejected because it requires plumbing per-entity provenance through every merge primitive — invasive change to 001's data shapes.
  - **At-load time**, rewriting in `internal/config/own_proxies.go` and at the upstream-fetch boundary: rejected because upstream payloads live in the cache and are reused across reloads — a CSV `name` change without a config reload would silently produce a stale rewrite. Pre-merge re-runs every time the pipeline runs, so source-name changes always take effect on the next merge.

---

## R2. Built-in target whitelist

- **Decision**: The fixed set of identifiers exempt from prefixing in both proxy-group `proxies:` member lists and rule targets is: `DIRECT`, `REJECT`, `REJECT-DROP`, `PASS`. Comparison is **case-sensitive** (Mihomo's documented spelling is uppercase; lowercase variants in upstream payloads have been observed but they are rare and treating them as user-named groups is harmless — they would be prefixed to `<provider>_direct` and dangle).
- **Rationale**: These are Mihomo's documented built-in actions. They are syntactic literals, not user-defined names; prefixing them would break routing entirely (`alpha_DIRECT` is not a known target).
- **Alternatives considered**:
  - **Case-insensitive comparison**: would silently fix typos in upstream payloads but masks a real upstream config bug. Loud over silent: leave case-insensitive matching out; if an operator finds a real-world payload with `direct` (lowercase), open a follow-up issue.
  - **Extending the list with `GLOBAL`** (Mihomo's "current global proxy" meta-name): considered but `GLOBAL` is not a member of the documented action set in Mihomo's `rule-providers` spec; it appears in some YAML examples as a proxy-group name rather than an action. Treat it as a regular name (prefix it).

---

## R3. Rule-target identification — splitting on the last comma

- **Decision**: A Mihomo rule is `TYPE,VALUE,TARGET[,MODIFIER]`. Identify the target by parsing on commas: split into `n` parts; if the last part is one of `no-resolve` / `src` / `dport` / known modifiers, the target is `parts[n-2]`; otherwise the target is `parts[n-1]`. The simplest correct implementation is: **find the rightmost comma-separated field that is NOT a known rule modifier**, then rewrite that field.
- **Rationale**: Mihomo rules carry the target as the final non-modifier field. Splitting blindly on the last comma works for ~95% of rules; the tiny minority with trailing modifiers (`,no-resolve`, etc.) need the explicit modifier whitelist. The whitelist of modifiers is small and slow-changing.
- **Implementation note**: For maintainability, define `var ruleModifiers = map[string]bool{"no-resolve": true, "src": true, "dport": true}` in `namespace.go` and use it from `rewriteRuleTarget`. The unit tests TC-U-NS-RULE-04 + TC-U-NS-RULE-05 pin both shapes.
- **Alternatives considered**:
  - **Full Mihomo rule grammar parser**: overkill for one-field rewrite. We don't need to validate rule shape; if an upstream emits a malformed rule, the post-rewrite Mihomo client will fail loudly downstream — which is fine.
  - **Regex-based replace**: brittle for rules whose value contains commas (e.g., `IP-CIDR,10.0.0.0/8,Proxy`). Splitting and re-joining via stdlib `strings.Split` / `strings.Join` is clearer and Go-idiomatic.

---

## R4. Trailing-rule drop sequencing

- **Decision**: Drop the trailing rule from each source's rule slice **before** the namespace rewrite is applied. Order: `loadSourceRules → dropTrailing → rewriteTargets → mergeRules → appendFallback`.
- **Rationale**: Dropping first means the rewrite pass sees N-1 rules per source and never has to touch the would-be-dropped trailing rule (microscopic perf win, no semantic difference). The `dropTrailing` step is a pure slice-truncation; sequencing it first also makes the `trailing-rule-drop:noop` log line trivially correct (emit if `len(rules) == 0` going in).
- **Alternatives considered**:
  - **Drop after rewriting**: works equally; chosen sequencing is cosmetic. Pinned in TC-U-RULES-DROP-* test ordering for clarity.

---

## R5. Server-emitted final fallback — where to construct

- **Decision**: `MergeRules` (in `internal/merge/rules.go`) takes a new parameter `fallbackTarget string` and appends `"MATCH," + fallbackTarget` as the last element of its return slice. The pipeline (`internal/merge/pipeline.go`) reads `ServerConfig.FallbackRuleTarget` and passes it through. The server-emitted rule is **never** subject to namespace rewriting (the rewrite operates per-source on each source's slice; the fallback is appended *after* all per-source rules are merged).
- **Rationale**: Centralizes the fallback logic at the merge boundary — same place rule concatenation already happens. Avoids leaking an "is this a server-emitted rule?" predicate into the rewriter (the rewriter sees only per-source slices and never sees the fallback). Spec FR-010 explicitly requires the fallback to bypass rewriting.
- **Alternatives considered**:
  - **Append in the output adapter** (`internal/output/subscription_mode.go` from 001's plan): rejected — the output adapter is a thin renderer; routing logic belongs in `internal/merge/`.
  - **A standalone `AppendFallbackRule` function** called from the pipeline after `MergeRules`: works but adds an extra function call site for one line of logic. Inline in `MergeRules` is simpler.

---

## R6. Region table format and storage

- **Decision**: A package-level `var regionTable = []regionEntry{ ... }` in `internal/merge/region_table.go` — an **ordered slice** of `{Indicator string, Code string, Lang string}` triples. Slice order is fixed at compile time; lookup is `for _, e := range regionTable { if strings.Contains(name, e.Indicator) { return e.Code, true } }`. The Lang field carries `"zh"` or `"en"` so the precedence rule from FR-012 can prefer Chinese matches over English when both exist.
- **Rationale**: (a) Slice iteration order is deterministic in Go (Constitution Principle II); a `map[string]string` would give nondeterministic iteration and break snapshot stability the moment two indicators in the same input both matched. (b) `strings.Contains` is O(N×M) per proxy name — fine at the 50-entry table size. (c) Compile-time data file is reviewable as a unified diff when operators extend coverage. (d) Lang tagging enables the precedence rule without a second pass.
- **Alternatives considered**:
  - **`map[string]string`**: rejected for nondeterministic iteration order — a real correctness risk for snapshot stability.
  - **Pre-compiled regex**: overkill; `strings.Contains` on a 50-entry list is microseconds.
  - **Embedding a CSV via `//go:embed`**: would let operators extend coverage without rebuilding, but adds a startup-time parse step and a runtime-load failure mode for what is fundamentally static reference data. The Go-source path keeps everything compile-checked.

---

## R7. Emoji regional-indicator decoder

- **Decision**: Dedicated function `decodeRegionalIndicatorPair(s string) (code string, ok bool)` in `region_table.go`. Walk the string by rune; when two consecutive runes are both in U+1F1E6..U+1F1FF, subtract `0x1F1E6` from each and add `'A'` to produce the alpha-2 letters; return the uppercase 2-character string. The decoder is a pure function with no table.
- **Rationale**: The Unicode block `Regional Indicator Symbol Letter A..Z` (U+1F1E6..U+1F1FF) maps 1-to-1 with the 26 ISO basic Latin letters; an emoji "flag" is just two adjacent regional-indicator codepoints. Decoding by codepoint arithmetic is a constant-time operation per pair and needs no table — the decoder is correct-by-construction for every present and future ISO 3166-1 alpha-2 code, including future code assignments.
- **Edge cases pinned by tests**:
  - Single regional indicator without a partner: skip (not a flag).
  - Three consecutive indicators (rare): the first two form one flag; the third stands alone (skipped).
  - Indicators interleaved with other characters: `decode("Foo 🇨🇳 Bar")` finds the pair and returns `("CN", true)`.
- **Alternatives considered**:
  - **Hard-coding every flag emoji as a table entry** (e.g., `{"🇨🇳", "CN", "emoji"}`): would require 200+ entries, exhaustively duplicating data the Unicode standard already specifies. Rejected.

---

## R8. Initial Chinese name table content

- **Decision**: Seed `regionTable` with at least the country/region names that the operator's two example upstreams (alpha + beta) use, **plus** the top ~30 markets common in Chinese-market subscription providers. Table content reviewed at PR time; future additions are small reviewable diffs. The implementation PR is responsible for surveying the two committed upstream fixtures and confirming every node with a Chinese country indicator gets classified.
- **Rationale**: A small table optimizes for clarity and review-ability over exhaustive coverage; FR-014's "log unmapped indicator" line gives operators a clean signal to extend the table when they see misses.
- **Initial seed list (zh; pre-PR draft)**:
  - 中国 → CN, 美国 → US, 日本 → JP, 韩国 → KR, 香港 → HK, 台湾 → TW, 臺灣 → TW (traditional), 新加坡 → SG, 英国 → GB, 德国 → DE, 法国 → FR, 加拿大 → CA, 澳大利亚 → AU, 俄罗斯 → RU, 印度 → IN, 越南 → VN, 泰国 → TH, 马来西亚 → MY, 菲律宾 → PH, 印度尼西亚 → ID, 巴西 → BR, 阿根廷 → AR, 土耳其 → TR, 沙特阿拉伯 → SA, 阿联酋 → AE, 以色列 → IL, 乌克兰 → UA, 波兰 → PL, 荷兰 → NL, 瑞士 → CH, 瑞典 → SE, 挪威 → NO, 丹麦 → DK, 芬兰 → FI, 西班牙 → ES, 意大利 → IT, 爱尔兰 → IE.
- **Initial seed list (en; pre-PR draft)**: lowercase substring matches against `strings.ToLower(name)` for: `hong kong → HK`, `singapore → SG`, `united states → US`, `united kingdom → GB`, `japan → JP`, `germany → DE`, `france → FR`, `taiwan → TW`, plus the rest of the zh list translated.
- **Alternatives considered**:
  - **Auto-generating the table from a third-party country-name dataset** (`golang.org/x/text/language` or similar): rejected — the dataset is overkill for this use case and introduces a non-stdlib dep with broad surface.

---

## R9. Region-group naming convention

- **Decision**: `region_<CC>` where `<CC>` is the **uppercase** ISO 3166-1 alpha-2 code (e.g., `region_CN`, `region_HK`). The literal prefix is the lowercase string `region_`; the alpha-2 suffix is uppercase per ISO standard. **Crucially, this name does NOT match the strengthened FR-001 `^[a-z]+$` rule that applies to CSV `name` values** — but FR-001 governs the source `name` column, not the names of server-emitted groups. The `region_` prefix is a server-defined literal that operators don't configure.
- **Rationale**: (a) prefix `region_` mirrors the per-source prefix scheme; visually consistent ("everything namespaced gets a prefix"); (b) uppercase CC matches ISO standard so operators reading the served config recognize the codes; (c) the underscore between prefix and CC is consistent with the `<provider>_<original>` form. (d) does not collide with any per-source prefix because no source `name` can equal the literal `region` (FR-001 permits `^[a-z]+$` so technically `name=region` would collide with `region_CN`, but that is an upstream operator's choice and they can rename) — a soft conflict, not a structural one. Documented in `quickstart.md`.
- **Alternatives considered**:
  - **`region-CN`** (hyphen): hyphens are unusual in Mihomo proxy-group names; underscore is more common.
  - **`Region_CN`** (capitalized prefix): inconsistent with the lowercase per-provider prefix.
  - **`zone_CN`** / **`country_CN`**: bikeshed; `region_` is the term the user used in the spec.

---

## R10. `FALLBACK_RULE_TARGET` env-var binding

- **Decision**: Add `FallbackRuleTarget string` to `internal/config/ServerConfig` with default `"auto"`. Bound in `Load()` after the existing `UPSTREAM_USER_AGENT` binding using the same idiom: `if v := env.Getenv("FALLBACK_RULE_TARGET"); v != "" { cfg.FallbackRuleTarget = v }`. No validation on the value (per Assumption A7a).
- **Rationale**: Matches existing 001 env-var pattern (`LOG_LEVEL`, `PROXIES_GROUP_NAME`, `UPSTREAM_USER_AGENT`); minimal surface; one new field, one new binding, one new test.
- **Alternatives considered**:
  - **Per-source override** (per-CSV-row `fallback_rule_target` column): out of scope; the spec frames the fallback as a single server-wide rule appended after merge.

---

## R11. Snapshot regeneration and review process

- **Decision**: Regenerate the three 001 snapshots (`served-config.snap.yaml`, `subscription-userinfo.snap.txt`, `health.snap.json`) in the same PR that lands this feature. Reviewer attention specifically required on:
  - Every prefixed name in `served-config.snap.yaml` reads sensibly (no surprise mojibake on the original Chinese / emoji portions).
  - The very last `rules:` entry is `MATCH,auto`.
  - Every emitted `region_<CC>` group has plausible membership (HK members are HK nodes, etc.).
  - `health.snap.json` shows the renamed source `beta` (was `beta`); no other drift.
- **Rationale**: Per Constitution's Development Workflow ("Updating snapshots is a deliberate, reviewable action; the PR MUST state why the change is intentional"), the PR description must call out the rename and the prefix-introduced drift.
- **Implementation note**: `make check` will fail until snapshots are regenerated. The PR sequencing is: implement → run `UPDATE_SNAPSHOTS=true go test ./internal/integration/...` → review the snapshot diffs as part of the PR.
