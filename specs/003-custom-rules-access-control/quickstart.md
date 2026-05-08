# Quickstart: Custom Rules, Continent Groups & Access Control

**Feature**: 003-custom-rules-access-control  
**Date**: 2026-04-30

## Overview

This feature adds four new capabilities to the Honkai Rule Server:

1. **Custom rules with priorities** — operator-defined routing rules in YAML files
2. **Continent-based proxy groups** — `_continent_EU`, `_continent_AS`, etc.
3. **Unknown region group** — `_region_UNKNOWN` for unclassified nodes
4. **User-Agent access control** — restrict access to authorized clients

## Operator Guide

### 1. Custom Rules Setup

#### Folder Structure

Create a folder for custom rule files:

```bash
mkdir -p ./custom-rules
```

Set the environment variable (optional; defaults to `./custom-rules/`):

```bash
export CUSTOM_RULES_PATH=/path/to/custom-rules/
```

#### Create a Custom Rule File

Create `custom-rules/my-rules.yaml`:

```yaml
name: my-customized-rule-1
priority: 1000
rules:
  - DOMAIN,ad.doubleclick.net,REJECT
  - DOMAIN-SUFFIX,google.com,_region_US
  - DOMAIN-SUFFIX,google.cn,_region_CN
  - IP-CIDR,10.0.0.0/8,DIRECT,no-resolve
  - GEOIP,CN,DIRECT
```

#### Priority Ordering

Rules are inserted in this order:

1. **Upstream rules** (from subscriptions) — ordered by source priority
2. **Custom rules** — ordered by `priority` field (lower = earlier)
3. **MATCH fallback** — server-emitted, always last

For same priority: alphabetical by filename.

#### Available Target Groups

| Target | Example | Description |
|---|---|---|
| Built-in | `DIRECT`, `REJECT` | No prefix |
| Region | `_region_US` | All US nodes |
| Continent | `_continent_EU` | All European nodes |
| Unknown | `_region_UNKNOWN` | Unclassified nodes |
| Provider | `alpha_Auto` | Specific upstream group |
| Own-group | `_my-group` | Operator-defined |

### 2. Continent and Unknown Groups

These groups are **automatically generated** — no configuration needed.

#### Continent Groups

- `_continent_AF` — Africa
- `_continent_AS` — Asia (CN, JP, KR, SG, HK, TW, etc.)
- `_continent_EU` — Europe (DE, FR, GB, NL, etc.)
- `_continent_NA` — North America (US, CA, MX)
- `_continent_SA` — South America (BR, AR)
- `_continent_OC` — Oceania (AU, NZ)
- `_continent_AN` — Antarctica

#### Unknown Group

`_region_UNKNOWN` contains nodes whose display names have no recognized country indicator (no emoji flag, Chinese name, or English name from the table).

**Use in rules**:

```yaml
rules:
  - DOMAIN-SUFFIX,fallback-service.com,_region_UNKNOWN
```

### 3. User-Agent Access Control

#### Enable UA Filtering

Set the environment variable with allowed prefixes:

```bash
export HONKAI_RULE_CLIENT_UA="Honkai-Rule-Client,curl"
```

#### Behavior

- Requests with UA starting with `Honkai-Rule-Client` or `curl` → allowed
- Requests with other UA (e.g., `Mozilla/5.0`) → **403 Forbidden**
- If unset or empty → **all requests allowed** (disabled)

#### Example Clients

```bash
# Allowed
curl -H "User-Agent: Honkai-Rule-Client/1.0" "http://server/?token=xxx"
curl "http://server/?token=xxx"  # curl's default UA matches

# Blocked
curl -H "User-Agent: Mozilla/5.0" "http://server/?token=xxx"  # 403
```

### 4. Full Environment Variable Example

```bash
# Existing (from 001/002)
export SUBSCRIPTIONS_CSV_PATH=./subscriptions.csv
export OWN_PROXIES_YAML_PATH=./own-proxies.yaml
export TOKENS_PATH=./tokens.json
export SERVED_CONFIG_TEMPLATE_PATH=./template.yaml
export CACHE_DIR=./cache/

# New (003)
export CUSTOM_RULES_PATH=./custom-rules/         # Optional; default ./custom-rules/
export HONKAI_RULE_CLIENT_UA="Honkai-Rule-Client,curl"  # Optional; default disabled
export FALLBACK_RULE_TARGET=auto                  # From 002; unchanged
```

### 5. Testing Your Configuration

#### Verify Custom Rules Appear

```bash
curl -H "User-Agent: Honkai-Rule-Client" "http://localhost:8080/?token=xxx" | grep "DOMAIN,ad.doubleclick.net,REJECT"
```

#### Verify Continent Groups

```bash
curl -H "User-Agent: Honkai-Rule-Client" "http://localhost:8080/?token=xxx" | grep "_continent"
```

#### Verify Unknown Group

```bash
curl -H "User-Agent: Honkai-Rule-Client" "http://localhost:8080/?token=xxx" | grep "_region_UNKNOWN"
```

#### Verify UA Filtering

```bash
# Should succeed
curl -H "User-Agent: Honkai-Rule-Client/1.0" "http://localhost:8080/?token=xxx" -w "%{http_code}\n"

# Should return 403
curl -H "User-Agent: Mozilla/5.0" "http://localhost:8080/?token=xxx" -w "%{http_code}\n"
```

## Migration from Previous Versions

No migration needed. This feature is **additive**:

- Existing subscriptions CSV unchanged
- Existing own-proxies YAML unchanged
- Existing token file unchanged
- If you don't set `CUSTOM_RULES_PATH` or `HONKAI_RULE_CLIENT_UA`, behavior matches 002

## Troubleshooting

### Custom Rules Not Appearing

1. Check folder exists: `ls $CUSTOM_RULES_PATH`
2. Check YAML syntax: `cat custom-rules/my-rules.yaml`
3. Check server logs for parse errors

### UA Filtering Blocking Legitimate Clients

1. Check your UA string matches a configured prefix exactly
2. Check logs for `ua-rejected` events showing the received UA
3. Temporarily disable: `unset HONKAI_RULE_CLIENT_UA`

### Unknown Group Empty

This is expected if all nodes are classified. Check:

1. Node display names contain country indicators (emoji, Chinese names, English names)
2. If a country is missing from the table, file a PR to extend `region_table.go`

## Sample Custom Rules Files

### Ad Blocking (Priority 100)

```yaml
name: ad-blocking
priority: 100
rules:
  - DOMAIN-SUFFIX,doubleclick.net,REJECT
  - DOMAIN-SUFFIX,googlesyndication.com,REJECT
  - DOMAIN-KEYWORD,ads,REJECT
  - DOMAIN-REGEX,^ad[sx]?\.,REJECT
```

### Streaming Services (Priority 500)

```yaml
name: streaming
priority: 500
rules:
  - DOMAIN-SUFFIX,netflix.com,_region_US
  - DOMAIN-SUFFIX,netflix.com,_continent_NA
  - DOMAIN-SUFFIX,bbc.co.uk,_region_GB
  - DOMAIN-SUFFIX,abema.tv,_region_JP
  - DOMAIN-SUFFIX,bilibili.com,_region_CN
```

### Corporate Traffic (Priority 700)

```yaml
name: corporate
priority: 700
rules:
  - DOMAIN-SUFFIX,corp.example.com,PROXY
  - DOMAIN-SUFFIX,internal.example.com,DIRECT
  - IP-CIDR,192.168.0.0/16,DIRECT,no-resolve
```

### Fallback (Priority 900)

```yaml
name: fallback
priority: 900
rules:
  - DOMAIN-SUFFIX,backup.net,_region_UNKNOWN
  - MATCH,PROXY  # Note: server still appends MATCH,auto at the end
```