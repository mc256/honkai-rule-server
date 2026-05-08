# Research: Custom Rules, Continent Groups & Access Control

**Feature**: 003-custom-rules-access-control  
**Date**: 2026-04-30

## Decisions

### D1: Custom Rules File Format — YAML with Priority

**Decision**: Each custom rule file is a YAML document with `name`, `priority`, and `rules` fields.

**Rationale**: 
- YAML is human-readable and already used throughout the project (`own-proxies.yaml`, upstream configs).
- Priority ordering allows operators to control rule precedence without renaming files.
- The `name` field provides operator-friendly identification in logs; fallback to filename if omitted.

**Alternatives considered**:
- JSON files: rejected because YAML is already the project's configuration language; JSON would be inconsistent.
- Filename-derived priority (e.g., `001-my-rules.yaml`): rejected because explicit `priority` field is clearer and allows reordering without renaming.
- Single monolithic YAML file: rejected because per-file organization matches operator mental model (one file per rule theme: ad-blocking, geo-routing, etc.).

### D2: Custom Rules Folder Path — Environment Variable

**Decision**: Configurable via `CUSTOM_RULES_PATH` env var, defaulting to `./custom-rules/`.

**Rationale**: 
- Matches existing env var pattern from 001/002 (`SUBSCRIPTIONS_CSV_PATH`, `OWN_PROXIES_YAML_PATH`, etc.).
- Allows deployment flexibility (different paths for dev, staging, prod).
- Default path is relative to working directory, consistent with other path defaults.

**Alternatives considered**:
- Hardcoded path `/etc/honkai/custom-rules`: rejected because it limits deployment flexibility.
- Relative path only: rejected because operators may want absolute paths in containerized deployments.

### D3: Continent Codes — Two-Letter Standard

**Decision**: Use two-letter continent codes: AF, AS, EU, NA, SA, OC, AN.

**Rationale**: 
- Maintains naming consistency with two-letter ISO 3166-1 alpha-2 country codes.
- Keeps group names concise (`_continent_EU` vs `_continent_Europe`).
- Industry-standard codes used by geolocation services and Mihomo itself.

**Alternatives considered**:
- Full continent names: rejected because longer names would increase visual clutter in client UI.
- Three-letter codes (e.g., EUR, ASN): rejected because ISO 3166-1 alpha-2 is already established for regions.

### D4: Country-to-Continent Mapping — In-Code Table

**Decision**: Maintain `countryToContinent` as an ordered slice in `internal/merge/region_table.go`, similar to `regionTable`.

**Rationale**: 
- Deterministic (Constitution Principle II): slice iteration order is fixed, unlike Go map iteration.
- Reviewable: all mappings visible in a single code file, version-controlled.
- Extensible: operators can submit PRs to add mappings for edge cases.

**Alternatives considered**:
- External JSON/YAML file: rejected because it introduces another config file and nondeterminism (file read order).
- Go map: rejected because map iteration order is nondeterministic, violating Constitution Principle II.
- Third-party geolocation library: rejected because it adds an external dependency and may not cover all edge cases.

### D5: `_region_UNKNOWN` Naming — Follow Region Family

**Decision**: Use `_region_UNKNOWN` (not `_unknown_region` or `_unclassified`).

**Rationale**: 
- Follows the `_region_*` naming family from 002, making it discoverable alongside country groups.
- `UNKNOWN` is uppercase like country codes, maintaining visual consistency.
- Operators targeting unclassified nodes naturally reach for `_region_*` prefix.

**Alternatives considered**:
- `_unclassified`: rejected because it doesn't follow the established naming convention.
- `_continent_UNKNOWN`: rejected because "unknown" is not a continent; it's a classification state.

### D6: User-Agent Matching — Literal Prefix

**Decision**: Case-sensitive literal prefix matching.

**Rationale**: 
- Simple to understand and implement (no regex, no glob patterns).
- User-Agent strings are typically case-specific (e.g., `curl`, `Mozilla`, `Honkai-Rule-Client`).
- Matches the user's described intent: "Honkai-Rule-Client or curl (prefixes)".

**Alternatives considered**:
- Case-insensitive matching: rejected because User-Agent strings are case-specific and case folding adds complexity.
- Regex matching: rejected because it's overkill for prefix matching and introduces escaping complexity.
- Full string match: rejected because User-Agent strings often have version suffixes (e.g., `curl/7.68.0`).

### D7: UA Filtering Placement — Middleware Before Token Auth

**Decision**: UA middleware wraps the subscription handler before `auth.RequireToken`.

**Rationale**: 
- Rejecting unauthorized clients early avoids token lookup overhead.
- Consistent with HTTP middleware layering: UA check → token check → handler.
- UA rejection (403) is logged without token context (client hasn't provided one yet).

**Alternatives considered**:
- After token auth: rejected because it wastes token validation effort on unauthorized clients.
- Inside the handler: rejected because middleware is cleaner and reusable.

### D8: Custom Rules Failure Mode — Warn + Skip

**Decision**: Parse errors in custom rule files log a warning and skip that file; other files continue.

**Rationale**: 
- Consistent with 002's name-validation soft-failure (warn + skip row).
- Operator typo shouldn't take down the entire service.
- Logged warnings preserve observability (Principle V).

**Alternatives considered**:
- Loud failure (abort startup): rejected because it would cause service outage on a single typo.
- Silent skip: rejected because operators need visibility into which files were ignored.

## Implementation Notes

### Custom Rules YAML Schema

```yaml
name: string           # optional; defaults to filename (without .yaml)
priority: integer      # optional; defaults to 1000
rules:
  - DOMAIN,example.com,REJECT
  - DOMAIN-SUFFIX,google.com,_region_US
  - IP-CIDR,10.0.0.0/8,DIRECT,no-resolve
  # ... any valid Mihomo rule
```

### Country-to-Continent Mapping (subset)

| Country Code | Continent Code | Notes |
|---|---|---|
| CN, HK, TW, JP, KR, SG, VN, TH, MY, PH, ID, IN | AS | Asia |
| US, CA, MX | NA | North America |
| DE, FR, GB, NL, CH, SE, NO, DK, FI, ES, IT, IE, PL, UA | EU | Europe |
| AU, NZ | OC | Oceania |
| BR, AR | SA | South America |
| ZA, NG, EG | AF | Africa |
| AQ | AN | Antarctica |

Full mapping will cover all ISO 3166-1 alpha-2 codes.

### Module Path Correction

User clarified the Go module path is `github.com/mc256/honkai-rule-server`, not `github.com/junlinchen/honkai-rule-server`. Update `go.mod` and all import paths.