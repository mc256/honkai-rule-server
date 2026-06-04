# Quickstart: Rule Set Support

Operator- and developer-facing guide to how the aggregator handles upstream
`RULE-SET` rules and their `rule-providers:` definitions.

## What changed

Before this feature, the aggregator read only `proxies`, `proxy-groups`, and
`rules` from each upstream subscription. Any `RULE-SET` rule was served with a
dangling reference (its provider was dropped), so Mihomo refused to load the
config. Now the aggregator reads each source's `rule-providers:` block, namespaces
it, and serves a merged `rule-providers:` block alongside the rules.

## Behavior at a glance

Given upstream source `srcA` whose payload contains:

```yaml
rules:
  - RULE-SET,Local-IP,DIRECT
  - RULE-SET,China-Site,DIRECT
rule-providers:
  Local-IP:
    type: http
    behavior: ipcidr
    format: mrs
    url: 'https://cdn.example.com/.../Local-IP.mrs'
    path: ./ruleset/Local-IP.mrs
    proxy: DIRECT
    interval: 86400
  China-Site:
    type: http
    behavior: domain
    format: mrs
    url: 'https://cdn.example.com/.../China-Site.mrs'
    path: ./ruleset/China-Site.mrs
    proxy: DIRECT
    interval: 86400
```

the served config contains:

```yaml
rules:
  - RULE-SET,srcA_Local-IP,DIRECT
  - RULE-SET,srcA_China-Site,DIRECT
  # ... interleaved by priority with other sources' rules, then MATCH,<fallback>
rule-providers:
  srcA_Local-IP:
    type: http
    behavior: ipcidr
    format: mrs
    url: 'https://cdn.example.com/.../Local-IP.mrs'
    path: ./ruleset/srcA_Local-IP.mrs   # source-distinct path
    proxy: DIRECT
    interval: 86400
  srcA_China-Site:
    type: http
    behavior: domain
    format: mrs
    url: 'https://cdn.example.com/.../China-Site.mrs'
    path: ./ruleset/srcA_China-Site.mrs
    proxy: DIRECT
    interval: 86400
```

If a second source `srcB` also defines `Local-IP`, both appear as distinct keys
(`srcA_Local-IP`, `srcB_Local-IP`) with distinct cache paths — no collision.

## Rules to remember

- **Provider names are prefixed with the source name** (`srcA_Local-IP`),
  identical to how proxies, proxy-groups, and rule targets are already namespaced.
- **`RULE-SET` rules are still priority-ordered** like any other rule — no special
  placement.
- **Unbacked `RULE-SET` rules are dropped, not fatal.** If an upstream emits
  `RULE-SET,Foo,DIRECT` but never defines `Foo`, that one rule is removed and the
  rest of the config is served normally. Look for the log event below.
- **Unused providers are not served.** A provider no surviving `RULE-SET` rule
  references is omitted.
- **No `RULE-SET` anywhere → no `rule-providers:` block,** and the served output is
  byte-identical to before this feature.

## Where to look in the code

- `internal/merge/yamlutil.go` — `findChildMapping` (reads the `rule-providers:`
  mapping).
- `internal/merge/namespace.go` — rule rewriter; prefixes the `RULE-SET`
  provider-name field.
- `internal/merge/ruleset.go` — `RewriteSourceRuleProviders`,
  `DropUnbackedRuleSetRules`, `ReferencedRuleProviders`, `MergeRuleProviders`.
- `internal/merge/pipeline.go` — orchestration; sets `MergedConfig.RuleProviders`.
- `internal/output/subscription_mode.go` — emits the `rule-providers:` key when
  non-nil.

## Diagnostics (structured logs)

```
event=ruleset-rule-dropped     source=<src> provider=<src_name> rule="RULE-SET,..."
event=ruleset-provider-skipped  source=<src> provider=<name> reason=malformed
event=ruleset-merged  providers_merged=<n> rules_dropped=<n> providers_skipped=<n>
```

## Verifying locally

```sh
make check        # vet + staticcheck + unit/integration tests + snapshot drift
```

The new integration fixture exercises: namespaced providers from two sources, a
shared bare name resolved to distinct keys, an unbacked `RULE-SET` reference that
gets dropped, and a provider that is defined but unreferenced (pruned). The
committed snapshot `served-config-ruleset.snap.yaml` is the expected served body;
existing snapshots stay byte-unchanged.
