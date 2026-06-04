# Contract: Served-config `rule-providers:` block

The contract this feature adds to the subscription-mode served YAML. "Served
config" = the body returned by the subscription endpoint (and, in future, the
override-mode payload — same `MergedConfig` core).

## C1 — Presence

- The served config MUST contain a top-level `rule-providers:` mapping **iff** at
  least one surviving `RULE-SET` rule references a provider. (FR-005, FR-006)
- When no surviving `RULE-SET` rule references any provider, the served config MUST
  NOT contain a `rule-providers:` key at all. (FR-006)

## C2 — Keys

- Every key under `rule-providers:` MUST be of the form `<source>_<name>`, where
  `<source>` is the contributing subscription source name and `<name>` is the
  bare provider name as authored upstream. (FR-002)
- Keys MUST be globally unique across all sources. Two sources defining the same
  bare `<name>` MUST appear as two distinct keys. (FR-012)
- Every key MUST be referenced by at least one `RULE-SET` rule in the served
  `rules:` block. No unreferenced providers. (FR-010)

## C3 — Values (provider definitions)

For each provider definition value:
- All upstream fields not listed below MUST be preserved verbatim
  (`type`, `behavior`, `format`, `url`, `interval`, and any unknown field).
- If a `path:` field is present, its value MUST be source-distinct (derived from
  the namespaced key) so two same-named providers never share a client cache
  path. (FR-008)
- If a `proxy:` field is present and its value is not a built-in target
  (`DIRECT`/`REJECT`/…), it MUST be namespaced `<source>_<value>`; built-in values
  MUST be left unchanged. (FR-007)
- A malformed (non-mapping) provider entry MUST NOT appear in the output (skipped
  + logged). (Edge Cases)

## C4 — `RULE-SET` rules in the `rules:` block

- Every served `RULE-SET,<provider>,<target>[,<modifier>...]` rule MUST have
  `<provider>` equal to an existing key under `rule-providers:` (C2). No dangling
  references. (FR-009, SC-001)
- `<target>` MUST follow the same namespacing as every other rule's target:
  non-built-in targets prefixed `<source>_<target>`, built-ins unchanged.
  (FR-004)
- `RULE-SET` rules MUST appear in the same priority-ordered position they would
  occupy as any other rule of their source's priority. (FR-014)
- A `RULE-SET` rule whose referenced provider is undefined in its source MUST NOT
  appear in the served `rules:` block. (FR-009)

## C5 — Stability

- For any served config whose contributing sources supply neither a
  `rule-providers:` block nor any `RULE-SET` rule, the output MUST be
  byte-for-byte identical to the pre-feature output. (FR-013, snapshot gate)

## C6 — Observability

- Each dropped unbacked `RULE-SET` rule MUST emit a structured log event naming
  the source and the missing provider. (FR-011)
- Each skipped malformed provider definition MUST emit a structured log event.
  (FR-011)

## Verification

| Contract | Verified by |
|----------|-------------|
| C1 presence/omission | `ruleset_test.go` unit (`MergeRuleProviders` nil on empty referenced) + integration snapshot (block present) + output adapter test (omission) |
| C2 keys | `ruleset_test.go` namespacing + cross-source merge test; integration snapshot |
| C3 values | `ruleset_test.go` path/proxy rewrite + verbatim-field tests |
| C4 RULE-SET rules | `namespace_test.go` provider-field rewrite; integration snapshot; `ruleset_test.go` drop test |
| C5 stability | existing committed snapshots unchanged (drift gate) |
| C6 observability | `ruleset_test.go` / `pipeline_test.go` assert dropped/ skipped descriptors returned for logging |
