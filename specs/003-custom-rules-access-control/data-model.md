# Data Model: Custom Rules, Continent Groups & Access Control

**Feature**: 003-custom-rules-access-control  
**Date**: 2026-04-30

## New Entities

### CustomRuleSet

Represents a single custom rule file loaded from disk.

| Field | Type | Description | Validation |
|---|---|---|---|
| Name | string | Identifier for logging; defaults to filename (without `.yaml`) | Optional; non-empty string if provided |
| Priority | int | Ordering key; lower = earlier in output | Optional; defaults to 1000 |
| Rules | []string | Mihomo rule strings, preserved verbatim | Required; may be empty |

**File location**: `internal/customrules/types.go`

**Lifecycle**: Loaded once at server startup from `CUSTOM_RULES_PATH` folder; passed to `Pipeline.Build()` as a slice sorted by (Priority, Name). Not cached per-request.

### ContinentGroup

Derived proxy group representing all proxies from a continent.

| Field | Type | Description |
|---|---|---|
| Name | string | `_continent_<CONT>` where `<CONT>` is two-letter code (AF, AS, EU, NA, SA, OC, AN) |
| Type | string | Always `"select"` |
| Proxies | []string | Union of all proxy names from `_region_<CC>` groups where CC maps to CONT |

**File location**: `internal/merge/region.go` (generated dynamically during merge)

**Derivation**: Post-processing step after `_region_<CC>` groups are created; no upstream data access.

### UnknownRegionGroup

Derived proxy group for unclassified nodes.

| Field | Type | Description |
|---|---|---|
| Name | string | `"_region_UNKNOWN"` |
| Type | string | Always `"select"` |
| Proxies | []string | All upstream-prefixed proxies NOT in any `_region_<CC>` group |

**File location**: `internal/merge/region.go` (generated dynamically during merge)

**Exclusions**: Own-proxies (names starting with `_`) are never included.

## Modified Entities

### ServerConfig (internal/config/server.go)

New fields:

| Field | Type | Env Var | Default | Description |
|---|---|---|---|---|
| CustomRulesPath | string | `CUSTOM_RULES_PATH` | `./custom-rules/` | Folder containing `*.yaml` custom rule files |
| AllowedUserAgentPrefixes | []string | `HONKAI_RULE_CLIENT_UA` | nil (disabled) | Comma-separated UA prefixes; empty/nil = no filtering |

### Pipeline (internal/merge/pipeline.go)

New field:

| Field | Type | Description |
|---|---|---|
| customRules | []customrules.CustomRuleSet | Sorted by (Priority, Name); loaded once at construction |

New behavior in `Build()`:
- Call `MergeCustomRules` to insert custom rules between upstream rules and MATCH fallback.
- Call `AppendContinentGroups` after `AppendRegionGroups`.
- Create `_region_UNKNOWN` group if any unclassified proxies exist.

## Relationship Diagram

```
┌─────────────────┐
│  ServerConfig   │
│  (env vars)     │
└────────┬────────┘
         │
         ▼
┌─────────────────┐      ┌─────────────────┐
│  CustomRuleSet  │×N    │  Pipeline       │
│  (from folder)  │─────►│  - customRules  │
└─────────────────┘      │  - build()      │
                         └────────┬────────┘
                                  │
         ┌────────────────────────┼────────────────────────┐
         │                        │                        │
         ▼                        ▼                        ▼
┌─────────────────┐      ┌─────────────────┐      ┌─────────────────┐
│  MergeRules     │      │ AppendRegion    │      │ AppendContinent │
│  (with custom)  │      │ Groups          │      │ Groups          │
└────────┬────────┘      └────────┬────────┘      └────────┬────────┘
         │                        │                        │
         └────────────────────────┴────────────────────────┘
                                  │
                                  ▼
                         ┌─────────────────┐
                         │  MergedConfig   │
                         │  - Rules        │
                         │  - ProxyGroups  │
                         └─────────────────┘
```

## State Transitions

### Custom Rule Loading

```
Folder Not Exists → Empty Slice + Warning Log
Folder Exists + Empty → Empty Slice (no warning)
Folder Exists + Files → Parse Each File → []CustomRuleSet (sorted)
    Parse Error → Skip File + Error Log
    Parse Success → Append to slice
```

### Continent Group Emission

```
After AppendRegionGroups:
    For each _region_<CC>:
        CONT = countryToContinent[CC]
        If CONT unmapped:
            Log "continent-unmapped-country" (once per CC)
            Continue
        Add all proxies to _continent_<CONT>
    For each CONT with proxies:
        Emit _continent_<CONT> group
        Add to Proxies group member list
```

### Unknown Group Emission

```
After AppendRegionGroups + AppendContinentGroups:
    classifiedProxies = all proxies in _region_* groups
    unclassifiedProxies = upstreamProxies - classifiedProxies
    If len(unclassifiedProxies) > 0:
        Emit _region_UNKNOWN group
        Add to Proxies group member list
```

### User-Agent Filtering

```
Request arrives:
    If AllowedUserAgentPrefixes == nil:
        Continue to next middleware
    Else:
        UA = request.Header.Get("User-Agent")
        For each prefix in AllowedUserAgentPrefixes:
            If strings.HasPrefix(UA, prefix):
                Continue to next middleware
        Log "ua-rejected" with UA and remote address
        Return 403 Forbidden
```

## Validation Rules

### CustomRuleSet

- `Name`: If empty/missing, use filename without `.yaml` extension.
- `Priority`: If missing, use 1000. If present but not an integer, log error and skip file.
- `Rules`: If missing, use empty slice. If present but not a string array, log error and skip file.

### Country-to-Continent Mapping

- All 195 ISO 3166-1 alpha-2 codes must have a mapping.
- Continent codes must be one of: AF, AS, EU, NA, SA, OC, AN.
- Table is validated at init time: panic on duplicate CC or invalid continent code.

### User-Agent Prefixes

- Parsed from comma-separated env var value.
- Empty string or unset = disabled (nil slice).
- Whitespace around prefixes is trimmed.
- No validation of prefix content (operators may use any string).
