# Custom Rules YAML Schema

**Feature**: 003-custom-rules-access-control  
**Version**: 1.0  
**Date**: 2026-04-30

## Schema Definition

```yaml
# Custom Rule Set Definition
# File naming: <any-name>.yaml (name does not affect behavior)

# Optional: Human-readable identifier for logs.
# If omitted, defaults to filename without .yaml extension.
name: string

# Optional: Priority for ordering (lower = earlier in output).
# If omitted, defaults to 1000.
# Range: any integer (typical: 0-9999)
priority: integer

# Required: List of Mihomo rule strings.
# Each rule is a single line matching Mihomo's rule syntax.
# Rules are preserved verbatim (no rewriting by server).
rules:
  - string
  - string
  - ...
```

## Examples

### Basic Ad Blocking

```yaml
name: ad-blocking
priority: 100
rules:
  - DOMAIN,ad.doubleclick.net,REJECT
  - DOMAIN-SUFFIX,adservice.google.com,REJECT
  - DOMAIN-KEYWORD,advertisement,REJECT
```

### Region-Based Routing

```yaml
name: geo-routing
priority: 500
rules:
  - DOMAIN-SUFFIX,google.com,_region_US
  - DOMAIN-SUFFIX,google.cn,_region_CN
  - DOMAIN-SUFFIX,google.jp,_region_JP
```

### Continent-Based Routing

```yaml
name: continent-routing
priority: 600
rules:
  - DOMAIN-SUFFIX,eu-gov-site.com,_continent_EU
  - DOMAIN-SUFFIX,asia-media.com,_continent_AS
```

### Unclassified Node Routing

```yaml
name: fallback-routing
priority: 900
rules:
  - DOMAIN-SUFFIX,backup-service.com,_region_UNKNOWN
```

### Complex Rule Types

```yaml
name: advanced-routing
priority: 200
rules:
  # Domain rules
  - DOMAIN,example.com,PROXY
  - DOMAIN-SUFFIX,example.org,PROXY
  - DOMAIN-KEYWORD,example,PROXY
  - DOMAIN-WILDCARD,*.example.net,PROXY
  - DOMAIN-REGEX,^www\.example\.,PROXY
  - GEOSITE,youtube,PROXY
  
  # IP rules
  - IP-CIDR,10.0.0.0/8,DIRECT,no-resolve
  - IP-CIDR6,2001:db8::/32,DIRECT
  - IP-SUFFIX,8.8.8.8/24,PROXY
  - IP-ASN,13335,PROXY
  - GEOIP,CN,DIRECT
  
  # Source-based rules
  - SRC-GEOIP,us,DIRECT
  - SRC-IP-ASN,9808,DIRECT
  - SRC-IP-CIDR,192.168.1.0/24,DIRECT
  
  # Port rules
  - DST-PORT,80,DIRECT
  - SRC-PORT,7777,DIRECT
  
  # Inbound rules
  - IN-PORT,7890,PROXY
  - IN-TYPE,SOCKS,PROXY
  
  # Process rules
  - PROCESS-NAME,curl,PROXY
  - PROCESS-NAME-REGEX,curl$,PROXY
  
  # Logical rules
  - AND,((DOMAIN,baidu.com),(NETWORK,UDP)),DIRECT
  - OR,((NETWORK,UDP),(DOMAIN,baidu.com)),REJECT
  - NOT,((DOMAIN,baidu.com)),PROXY
  
  # Rule set reference
  - RULE-SET,geosite-category,PROXY
```

## Validation Rules

| Field | Required | Type | Default on Missing | Failure Mode |
|---|---|---|---|---|
| `name` | No | string | filename sans `.yaml` | — |
| `priority` | No | integer | 1000 | If non-integer: log error, skip file |
| `rules` | No | array of strings | empty array | If non-array: log error, skip file |

## Ordering Rules

1. Upstream rules (from subscriptions) come first, ordered by source priority (descending).
2. Custom rules are inserted after upstream rules, ordered by:
   - `priority` ascending (lower number = earlier)
   - `name` alphabetical (for same priority)
3. Server-emitted `MATCH,<fallback>` is always last.

## Error Handling

- **File missing**: No error; folder may be empty.
- **Folder missing**: Log warning; proceed with empty custom rules.
- **YAML parse error**: Log error with filename and details; skip that file.
- **Invalid priority type**: Log error; skip that file.
- **Invalid rules type**: Log error; skip that file.

## Target Groups Available

Custom rules can target:

| Target Type | Example | Description |
|---|---|---|
| Built-in | `DIRECT`, `REJECT`, `REJECT-DROP`, `PASS` | No prefix required |
| Upstream group | `<provider>_<group>` | From subscription (002 prefixed) |
| Own-group | `_<name>` | Operator-defined group |
| Region group | `_region_<CC>` | Country-based (e.g., `_region_US`) |
| Continent group | `_continent_<CONT>` | Continent-based (e.g., `_continent_EU`) |
| Unknown group | `_region_UNKNOWN` | Unclassified nodes |
| Global group | `Proxies` | All proxies |

## File Organization Recommendations

```
custom-rules/
├── 001-ad-blocking.yaml      # Priority 100 — earliest, blocks ads
├── 002-geo-routing.yaml      # Priority 500 — routing by geography
├── 003-corporate.yaml        # Priority 700 — corporate traffic rules
├── 004-fallback.yaml         # Priority 900 — fallback routing
└── 999-final-catch.yaml      # Priority 9999 — near MATCH, final override
```

Note: Filename numbers are convention only; actual ordering is by `priority` field.