# Operator Quickstart: Unified Rule Priority

**Feature**: [spec.md](./spec.md) | **Plan**: [plan.md](./plan.md) | **Date**: 2026-05-01

## What's changing

Two operator-visible behaviors change in the served `rules:` block:

1. **Rule order**: upstream rules and custom rules now interleave by priority. Higher priority wins (z-index style — matches the existing convention for upstream sources).
2. **Comment format**: the single `# --- upstream ---` divider is replaced by per-priority headers that name the contributors at each priority level.

No environment variables, CSV columns, or custom-rule YAML fields change.

## Before / after

Suppose `subscriptions.csv` has two sources:

```csv
name,link,priority,enable
alpha,https://alpha.example.com/...,1000,Enable
beta,https://berry.example.com/...,2000,Enable
```

And `config/custom-rules/` has two files:

```yaml
# corporate.yaml
name: corporate
priority: 1500
rules:
  - DOMAIN-SUFFIX,corp.example.com,DIRECT
  - DOMAIN,blocked.example.com,REJECT
```

```yaml
# whitelist.yaml
name: whitelist
priority: 300
rules:
  - DOMAIN-SUFFIX,docs.example.com,auto
```

### Today (feature 004 baseline)

```yaml
rules:
  # --- upstream ---
  - <beta rules — all of them, in source order>
  - <alpha rules — all of them, in source order>
  # --- priority 300 ---
  - DOMAIN-SUFFIX,docs.example.com,auto
  # --- priority 1500 ---
  - DOMAIN-SUFFIX,corp.example.com,DIRECT
  - DOMAIN,blocked.example.com,REJECT
  - MATCH,auto
```

### After feature 005

```yaml
rules:
  # --- priority 2000 (beta) ---
  - <beta rules — all of them, in source order>
  # --- priority 1500 (corporate) ---
  - DOMAIN-SUFFIX,corp.example.com,DIRECT
  - DOMAIN,blocked.example.com,REJECT
  # --- priority 1000 (alpha) ---
  - <alpha rules — all of them, in source order>
  # --- priority 300 (whitelist) ---
  - DOMAIN-SUFFIX,docs.example.com,auto
  - MATCH,auto
```

Note: `corporate` (priority 1500) now appears **between** `beta` (2000) and `alpha` (1000). In the old layout it sat after both. This is the central behavior change.

## Multi-contributor buckets

If two contributors share a priority, they coexist in one bucket with both names in the header:

```yaml
# corporate.yaml has priority 1000
# alpha source has priority 1000
```

Renders as:

```yaml
rules:
  # --- priority 1000 (corporate, alpha) ---
  - <alpha rules>
  - <corporate rules>
```

Names in the header are alphabetical. Within the bucket, contributors emit their rule blocks in the same alphabetical order. `alpha` precedes `corporate` alphabetically? No — `c` < `e`. Re-rendered correctly:

```yaml
  # --- priority 1000 (corporate, alpha) ---
  - <corporate rules>
  - <alpha rules>
```

## What does NOT change

- The `MATCH,<fallback>` rule is still appended last with no header comment.
- Per-source proxy prefixing (`alpha_…`, `beta_…`) — feature 002.
- Region/continent/unknown proxy groups — feature 002/003.
- Custom-rule YAML schema — feature 003.
- Trailing-rule drop on each upstream source's rules — feature 002.
- All non-`rules:` sections of the served YAML.

## Updating snapshots

If you maintain a downstream test that snapshots the server's served YAML, you'll need to refresh the expected output once. The shape of `rules:` changes; everything else is byte-identical.

This project's `internal/integration/testdata/snapshots/served-config.snap.yaml` is regenerated as part of feature 005 implementation (`UPDATE_SNAPSHOTS=true go test ./internal/integration/...`) with manual review of the diff.

## Verifying the behavior

```bash
# Restart the server with custom rules configured (already true since 003).
CUSTOM_RULES_PATH=./config/custom-rules \
  SUBSCRIPTIONS_CSV_PATH=./config/subscriptions.csv \
  ... go run ./cmd/server

# Fetch and grep for priority headers:
curl -s "http://localhost:8080/?token=<your-token>" \
  -H "User-Agent: clash-meta/v1.18.0" | grep -E '^\s*# --- priority'
# Should print one line per priority bucket, in descending priority order.
# Should NOT print "# --- upstream ---" anywhere.
```
