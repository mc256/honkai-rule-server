# Quickstart: YAML Output Formatting

**Feature**: 004-yaml-output-formatting
**Date**: 2026-04-30

## Overview

This feature improves the readability of served YAML config by:
1. Rendering proxy-groups in block format (multi-line)
2. Ordering proxy-group fields consistently (`name`, `type`, `proxies` first)
3. Adding priority-level comments to the rules section

## Proxy-Groups Block Format

Before (from upstream, mixed styles):
```yaml
proxy-groups:
  - {name: Auto, type: url-test, proxies: [node1, node2], url: 'http://test.com'}
  - name: Proxies
    type: select
    proxies: [node1, node2]
```

After (normalized):
```yaml
proxy-groups:
  - name: Auto
    type: url-test
    proxies:
      - node1
      - node2
    url: 'http://test.com'
  - name: Proxies
    type: select
    proxies:
      - node1
      - node2
```

**What changed**:
- All proxy-groups render as multi-line block format
- Fields `name`, `type`, `proxies` always appear first in that order
- Additional fields (like `url`, `interval`) appear after `proxies` in original order

## Rule Priority Comments

When you have custom rules at different priority levels, comments mark the boundaries:

```yaml
rules:
  # --- upstream ---
  - DOMAIN,example.com,PROXY
  - GEOIP,CN,DIRECT
  # --- priority 100 ---
  - DOMAIN-SUFFIX,ads.com,REJECT
  - DOMAIN-KEYWORD,tracker,REJECT
  # --- priority 500 ---
  - DOMAIN-SUFFIX,youtube.com,auto
  # --- priority 1000 ---
  - MATCH,auto
```

**Comment format**: `# --- priority <N> ---` or `# --- upstream ---` for rules from upstream providers.

**No comments case**: If all custom rules have the same priority, only one comment appears. If no custom rules exist, only upstream section comment appears.

## Configuration

No new configuration required. Formatting is automatic for all served configs.

## Compatibility

- Output remains valid YAML that parses correctly
- No semantic changes to routing behavior
- Comments are informational only (ignored by Mihomo parser)

## Testing

Verify formatting by fetching a served config and inspecting:
- Proxy-groups are multi-line with `name` first
- Priority comments appear at correct boundaries