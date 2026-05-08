# Feature Specification: Custom Rules, Continent Groups & Access Control

**Feature Branch**: `003-custom-rules-access-control`  
**Created**: 2026-04-30  
**Status**: Draft  
**Input**: User description: "Add continent-based proxy groups (_continent_EU, _continent_AS, etc.) alongside existing region groups; add an _region_UNKNOWN group for unclassified nodes; allow operators to define custom prioritized rules via YAML files in a folder; enforce User-Agent header filtering to restrict API access to authorized clients."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Custom Rules with Priority Ordering (Priority: P1)

Operators want to inject their own routing rules into the merged subscription output with explicit priority control. By placing YAML files in a designated folder, operators can define rules that are inserted into the final rule list in a deterministic, priority-ordered sequence. This allows operators to block ads, route specific domains through particular regions, or apply any custom routing logic without modifying upstream subscription data.

**Why this priority**: Custom rules are the primary operator control mechanism for traffic routing. Without this, operators cannot express their routing preferences beyond what upstreams provide. This is the foundational capability that the other features build upon.

**Independent Test**: Place a YAML file in the custom rules folder with priority 500 containing `DOMAIN,example.com,REJECT`; fetch the merged subscription; verify the rule appears in the correct position relative to upstream rules (before the server-emitted MATCH fallback).

**Acceptance Scenarios**:

1. **Given** a custom rules file `my-rules.yaml` with `priority: 500` and `rules: [DOMAIN,ad.com,REJECT]`, **When** the merge runs, **Then** the served config contains the rule `DOMAIN,ad.com,REJECT` at the priority-ordered position (after all upstream rules with implicit priority < 500, before any custom rules with priority > 500).
2. **Given** two custom rule files `rules-a.yaml` (priority 100) and `rules-b.yaml` (priority 200), **When** the merge runs, **Then** rules from `rules-a.yaml` appear before rules from `rules-b.yaml` in the final rule list.
3. **Given** a custom rule targets `_region_US` (`DOMAIN-SUFFIX,google.com,_region_US`), **When** the merge runs and at least one US-classified proxy exists, **Then** the rule appears exactly as written in the served config and routes matching traffic through the `_region_US` proxy group.
4. **Given** a custom rule file with `priority: 0`, **When** the merge runs, **Then** those rules appear before any upstream-contributed rules (upstream rules are implicitly treated as having no explicit priority and are ordered by source priority).
5. **Given** a custom rules folder containing three YAML files, **When** two files have the same priority value, **Then** file ordering within the same priority level is deterministic (alphabetical by filename).
6. **Given** a custom rule file containing all rule types from the Mihomo specification (DOMAIN, DOMAIN-SUFFIX, DOMAIN-KEYWORD, DOMAIN-WILDCARD, DOMAIN-REGEX, GEOSITE, IP-CIDR, IP-CIDR6, IP-SUFFIX, IP-ASN, GEOIP, SRC-*, DST-PORT, SRC-PORT, IN-*, PROCESS-*, UID, NETWORK, DSCP, RULE-SET, AND, OR, NOT, SUB-RULE), **When** the merge runs, **Then** all rules are preserved verbatim in the output (no rewriting of custom rule content).

---

### User Story 2 - Continent-Based Proxy Groups (Priority: P2)

Operators want to route traffic through proxies located in specific continents (Europe, Asia, North America, etc.) without needing to enumerate individual countries. The server automatically creates `_continent_EU`, `_continent_AS`, `_continent_NA`, etc. proxy groups by aggregating the existing `_region_<CC>` groups based on a maintained country-to-continent mapping.

**Why this priority**: This is a convenience layer on top of the existing region grouping from 002. It reduces operator burden when configuring rules that should apply to an entire continent rather than specific countries.

**Independent Test**: Provide upstream subscriptions with nodes classified into `_region_US`, `_region_CN`, `_region_DE`; fetch the merged subscription; verify the served config contains `_continent_NA` with US members, `_continent_AS` with CN members, and `_continent_EU` with DE members.

**Acceptance Scenarios**:

1. **Given** an upstream contributes proxies classified into `_region_US`, `_region_CA`, and `_region_MX`, **When** the merge runs, **Then** the served config contains a `_continent_NA` (North America) group whose members are all proxies from those three region groups.
2. **Given** an upstream contributes proxies classified into `_region_CN`, `_region_JP`, `_region_SG`, and `_region_HK`, **When** the merge runs, **Then** the served config contains a `_continent_AS` (Asia) group whose members are all proxies from those region groups.
3. **Given** an upstream contributes proxies classified into `_region_DE`, `_region_FR`, `_region_GB`, and `_region_NL`, **When** the merge runs, **Then** the served config contains a `_continent_EU` (Europe) group whose members are all proxies from those region groups.
4. **Given** no proxies are classified into any African country, **When** the merge runs, **Then** the served config does NOT contain a `_continent_AF` group (empty continent groups are omitted, matching the region group behavior from 002).
5. **Given** a custom rule targets `_continent_EU` (`DOMAIN-SUFFIX,eu-site.com,_continent_EU`), **When** the merge runs and at least one EU-classified proxy exists, **Then** the rule appears verbatim in the output and routes matching traffic through the continent group.
6. **Given** the same set of upstream payloads, **When** the merge runs twice in succession, **Then** the membership and ordering of every `_continent_*` group is byte-identical across both runs (deterministic transformation per Constitution Principle II).

---

### User Story 3 - Unclassified Nodes Proxy Group (Priority: P3)

Operators want a catch-all proxy group containing all nodes that could not be classified into any specific region. This `_region_UNKNOWN` group allows operators to apply rules to nodes whose display names do not contain a recognized country indicator, ensuring no proxy is "orphaned" from rule targeting.

**Why this priority**: This provides a safety net for unclassified nodes. While less critical than custom rules or continent groups, it ensures all proxies remain accessible for rule targeting regardless of classification status.

**Independent Test**: Provide an upstream subscription with a proxy whose display name contains no recognizable country indicator; fetch the merged subscription; verify the served config contains `_region_UNKNOWN` with that proxy as a member.

**Acceptance Scenarios**:

1. **Given** an upstream contributes a proxy named `mystery-node` with no country indicator in its display name, **When** the merge runs, **Then** the served config contains a `_region_UNKNOWN` group whose members include `alpha_mystery-node` (the prefixed form).
2. **Given** two upstreams contribute three unclassified proxies total, **When** the merge runs, **Then** the `_region_UNKNOWN` group contains all three proxies in source-priority order.
3. **Given** every upstream proxy is successfully classified into a country, **When** the merge runs, **Then** the served config does NOT contain a `_region_UNKNOWN` group (empty groups are omitted).
4. **Given** a custom rule targets `_region_UNKNOWN` (`DOMAIN-SUFFIX,fallback.com,_region_UNKNOWN`), **When** the merge runs and at least one unclassified proxy exists, **Then** the rule appears verbatim and routes matching traffic through the unclassified group.
5. **Given** an unclassified proxy is later reclassified (operator extends the translation table), **When** the merge runs again, **Then** the proxy moves from `_region_UNKNOWN` to the appropriate `_region_<CC>` group, and `_region_UNKNOWN` membership decreases accordingly.

---

### User Story 4 - User-Agent Access Control (Priority: P4)

Operators want to restrict API access to authorized clients by validating the User-Agent header. Requests from clients without an approved User-Agent prefix receive a 403 Forbidden response. The list of approved prefixes is configured via an environment variable, allowing deployment-time configuration without code changes. If no restriction is configured, all requests are accepted.

**Why this priority**: This is an operational security feature that protects the service from unauthorized access. While valuable, it is independent of the core rule/group functionality and can be enabled/disabled without affecting other features.

**Independent Test**: Set `HONKAI_RULE_CLIENT_UA=Honkai-Rule-Client,curl`; send a request with User-Agent `Honkai-Rule-Client/1.0` and verify 200 OK; send a request with User-Agent `Mozilla/5.0` and verify 403 Forbidden.

**Acceptance Scenarios**:

1. **Given** `HONKAI_RULE_CLIENT_UA` is set to `Honkai-Rule-Client,curl`, **When** a request arrives with User-Agent `Honkai-Rule-Client/1.0`, **Then** the request is processed normally and the merged config is returned with HTTP 200.
2. **Given** `HONKAI_RULE_CLIENT_UA` is set to `Honkai-Rule-Client,curl`, **When** a request arrives with User-Agent `curl/7.68.0`, **Then** the request is processed normally and the merged config is returned with HTTP 200.
3. **Given** `HONKAI_RULE_CLIENT_UA` is set to `Honkai-Rule-Client,curl`, **When** a request arrives with User-Agent `Mozilla/5.0`, **Then** the server responds with HTTP 403 Forbidden and no config body.
4. **Given** `HONKAI_RULE_CLIENT_UA` is not set or is empty, **When** any request arrives regardless of User-Agent, **Then** the request is processed normally (UA validation is disabled).
5. **Given** `HONKAI_RULE_CLIENT_UA` is set to a single prefix `MyClient`, **When** a request arrives with User-Agent `MyClient`, **Then** the request is processed normally (exact prefix match).
6. **Given** the UA validation rejects a request, **When** the 403 response is logged, **Then** the log entry includes the rejected User-Agent value for operational visibility.

---

### Edge Cases

- **Custom rules folder does not exist**: Server logs a warning at startup and proceeds with zero custom rules (no error, no crash).
- **Custom rules folder exists but is empty**: Server proceeds normally with zero custom rules (no warning needed, valid configuration).
- **Custom rules YAML file has invalid syntax**: Server logs an error identifying the file and line, skips that file's rules, and continues processing other files.
- **Custom rules file is missing the `name` or `priority` field**: Server uses the filename (without `.yaml`) as the rule name and assigns default priority 1000; a warning is logged.
- **Custom rules file has `priority` set to a non-integer**: Server logs an error and skips that file's rules.
- **Custom rule targets a non-existent proxy group** (e.g., `_region_XX` for an unassigned country): The rule is still included verbatim; Mihomo's standard fallback behavior applies at runtime.
- **Country code maps to a continent that has no other members**: If that country is the only member of its continent group, the continent group is still emitted (non-empty).
- **A proxy is classified into a country whose continent is unmapped**: The proxy appears in its `_region_<CC>` group but NOT in any `_continent_*` group; a structured log records the unmapped country code once.
- **Both `_region_<CC>` and `_region_UNKNOWN` would be empty**: Both are omitted from the output.
- **UA prefix contains special characters**: Prefixes are matched as literal string prefixes; no regex or glob interpretation.
- **UA header is missing entirely from request**: Treated as a non-match; 403 returned if `HONKAI_RULE_CLIENT_UA` is set.
- **Request with a valid UA prefix but extra whitespace**: Standard HTTP header trimming applies; valid prefix is detected correctly.

## Requirements *(mandatory)*

### Functional Requirements

#### Custom Rules with Priorities

- **FR-001**: The server MUST read custom rule definitions from a designated folder path at startup and on each config reload. The folder path is configurable via an environment variable `CUSTOM_RULES_PATH` (default: `./custom-rules/` if unset).
- **FR-002**: Each custom rule file MUST be a YAML document with the structure: `name: <string>`, `priority: <integer>`, `rules: [<rule strings>]`. The `name` field is for operator identification (appears in logs); `priority` determines ordering relative to other custom rule sets and upstream rules.
- **FR-003**: Custom rules MUST be inserted into the merged rule list after all upstream-contributed rules (from 002's pipeline) and before the server-emitted `MATCH,<fallback>` final rule. Within custom rules, ordering MUST be by ascending `priority` value (lower priority = earlier in list).
- **FR-004**: When multiple custom rule files have identical `priority` values, ordering among them MUST be deterministic: alphabetical by filename (case-sensitive, standard ASCII ordering).
- **FR-005**: Custom rule strings MUST be preserved verbatim in the output — no prefixing, no rewriting of targets, no modification of any field. This is distinct from upstream rules which are prefixed per 002's FR-006.
- **FR-006**: A custom rule MAY target any valid policy target including: built-in identifiers (`DIRECT`, `REJECT`, `REJECT-DROP`, `PASS`), upstream-prefixed groups (`<provider>_<group>`), own-groups (`_<group>`), region groups (`_region_<CC>`), continent groups (`_continent_<CC>`), or the unknown group (`_region_UNKNOWN`). The server does not validate target existence.
- **FR-007**: If the custom rules folder does not exist at startup, the server MUST log a warning and proceed with zero custom rules (no fatal error). If the folder exists but is empty, the server proceeds normally (no warning required).
- **FR-008**: If a custom rule YAML file cannot be parsed (invalid YAML, wrong structure, missing required fields with no reasonable default), the server MUST log an error identifying the file and the specific issue, skip that file's rules, and continue processing other files.

#### Continent-Based Proxy Groups

- **FR-009**: The server MUST maintain a country-to-continent mapping table alongside the existing country-indicator translation table from 002's FR-011. The mapping MUST cover all ISO 3166-1 alpha-2 country codes and map each to exactly one continent code from the set: `AF` (Africa), `AS` (Asia), `EU` (Europe), `NA` (North America), `SA` (South America), `OC` (Oceania), `AN` (Antarctica).
- **FR-010**: For every distinct continent that has at least one classified proxy (i.e., at least one `_region_<CC>` group exists where `<CC>` maps to that continent), the server MUST emit a proxy group named `_continent_<CONT>` where `<CONT>` is the uppercase two-letter continent code. The group type MUST be `select`.
- **FR-011**: The member list of each `_continent_<CONT>` group MUST be the union of all proxies from all `_region_<CC>` groups where `<CC>` maps to `<CONT>`. Member ordering MUST be deterministic: grouped by region code (alphabetical), and within each region by the proxy's existing order in that region group.
- **FR-012**: Continent groups whose membership would be empty MUST be omitted from the merged output (consistent with 002's region group behavior).
- **FR-013**: Continent group naming follows the FR-007d convention from 002: single leading underscore indicates server-emitted, distinct from upstream-prefixed names (start with lowercase) and built-in identifiers (uppercase).

#### Unclassified Nodes Proxy Group

- **FR-014**: For every upstream-sourced proxy that is NOT classified into any `_region_<CC>` group (per 002's FR-012/FR-014), the server MUST add that proxy to a group named `_region_UNKNOWN`.
- **FR-015**: The `_region_UNKNOWN` group MUST be of type `select`. Its member list MUST be all unclassified upstream-sourced proxies in source-priority order (per 001's FR-005a).
- **FR-016**: If every upstream-sourced proxy is successfully classified into a country, the `_region_UNKNOWN` group MUST NOT be emitted (empty groups are omitted, consistent with FR-012).
- **FR-017**: Own-proxies (per 002's FR-007a) are excluded from `_region_UNKNOWN` just as they are excluded from `_region_<CC>` groups — own-proxies are never classified, never appear in region or continent groups, and are addressed only via their underscore-prefixed own-groups.

#### User-Agent Access Control

- **FR-018**: The server MUST check the `HONKAI_RULE_CLIENT_UA` environment variable at startup. If set to a non-empty value, the server MUST parse it as a comma-separated list of User-Agent prefix strings.
- **FR-019**: For every incoming HTTP request to the subscription endpoint, if `HONKAI_RULE_CLIENT_UA` is set, the server MUST validate the request's `User-Agent` header against the configured prefix list. If the User-Agent starts with any configured prefix (case-sensitive), the request is accepted. Otherwise, the server MUST respond with HTTP 403 Forbidden and no response body.
- **FR-020**: If `HONKAI_RULE_CLIENT_UA` is unset or empty, the server MUST skip User-Agent validation entirely — all requests are accepted regardless of their User-Agent header.
- **FR-021**: Rejected requests MUST be logged with the client IP (if available) and the received User-Agent value for operational visibility and troubleshooting.
- **FR-022**: User-Agent prefix matching MUST be literal string prefix matching — no glob patterns, no regex, no case folding. The configured prefix string must exactly match the beginning of the received User-Agent header value.

#### Integration with Existing Pipeline

- **FR-023**: Custom rules MUST be inserted into the rule list after 002's upstream rule processing (including prefixing and trailing-rule drop) and before 002's server-emitted `MATCH,<fallback>` rule. The final rule list order is: (1) upstream rules ordered by source priority, (2) custom rules ordered by priority, (3) server-emitted `MATCH,<fallback>`.
- **FR-024**: Continent groups and the `_region_UNKNOWN` group MUST be appended to the `Proxies` selectable group (per 002's FR-015) so they are pickable from the client UI.
- **FR-025**: The transformation MUST remain deterministic (Constitution Principle II): given identical inputs (upstream data, custom rule files, environment variables), the output MUST be byte-identical across two independent runs.

### Key Entities

- **Custom Rule File**: A YAML document in the custom rules folder containing `name`, `priority`, and `rules` fields. Defines operator-specified routing rules to be merged into the output.
- **Priority**: An integer value determining rule ordering. Lower values appear earlier in the rule list. Upstream rules have implicit priority determined by source order; custom rules interleave based on explicit priority values.
- **Continent Code**: A two-letter code from the set {AF, AS, EU, NA, SA, OC, AN} representing a continent. Mapped from ISO 3166-1 alpha-2 country codes.
- **Continent Proxy Group**: A server-emitted group named `_continent_<CONT>` containing all proxies from region groups whose countries map to that continent. Type `select`. Naming follows the underscore convention for server-emitted entities.
- **Unknown Region Proxy Group**: A server-emitted group named `_region_UNKNOWN` containing all upstream-sourced proxies that could not be classified into any country. Type `select`. Provides a catch-all for unclassified nodes.
- **User-Agent Prefix**: A literal string configured via `HONKAI_RULE_CLIENT_UA` that must match the beginning of an incoming request's User-Agent header for the request to be accepted.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator can add a new custom rule file to the designated folder, and within one config-reload cycle (or server restart), the rule appears in the served config at the correct priority-ordered position without requiring any other configuration changes.
- **SC-002**: Given two custom rule files with priorities 100 and 200, the merged config contains all rules from the priority-100 file before any rules from the priority-200 file, and both appear after upstream rules and before the final `MATCH,<fallback>`.
- **SC-003**: Given upstream proxies classified into at least three countries on the same continent, the served config contains exactly one `_continent_<CONT>` group whose membership is the union of all proxies from those three region groups.
- **SC-004**: Given at least one upstream proxy with an unrecognizable display name (no country indicator), the served config contains a `_region_UNKNOWN` group that includes that proxy in its prefixed form.
- **SC-005**: Given `HONKAI_RULE_CLIENT_UA=Honkai-Rule-Client,curl`, a request with User-Agent `Honkai-Rule-Client/1.0` receives HTTP 200 with the merged config, while a request with User-Agent `Mozilla/5.0` receives HTTP 403.
- **SC-006**: Given `HONKAI_RULE_CLIENT_UA` is unset, requests from any User-Agent (including browsers, curl, and custom clients) all receive HTTP 200 with the merged config.
- **SC-007**: The integration snapshots are updated to reflect custom rules, continent groups, and the `_region_UNKNOWN` group, and `make check`'s snapshot-drift check passes for byte-identical output across two consecutive runs (Constitution Principle II).
- **SC-008**: An operator can write a custom rule targeting `_continent_EU` or `_region_UNKNOWN`, and the rule appears verbatim in the output without server-side rewriting.
- **SC-009**: All 195 ISO 3166-1 alpha-2 country codes have a defined mapping to a continent code in the maintained table; a proxy classified into any valid country code is automatically added to the corresponding continent group.

## Assumptions

- **A1**: The custom rules folder path defaults to `./custom-rules/` relative to the server's working directory. This matches the pattern established by 001's `own-proxies.yaml` path convention. Operators can override via `CUSTOM_RULES_PATH` for deployment flexibility.
- **A2**: Custom rule YAML files use the filename pattern `<rule_name>.yaml`. The filename itself does not affect processing — the `name` field inside the YAML is for operator identification. This allows descriptive filenames without affecting behavior.
- **A3**: Continent codes follow the standard two-letter convention (AF, AS, EU, NA, SA, OC, AN) rather than full names. This maintains consistency with the two-letter country code convention and keeps group names concise (`_continent_EU` vs `_continent_Europe`).
- **A4**: The country-to-continent mapping table is maintained in code alongside the existing country-indicator translation table from 002's FR-011. Both tables are version-controlled and deterministic (Constitution Principle II).
- **A5**: User-Agent prefix matching is case-sensitive. Most User-Agent strings are case-specific (e.g., `curl`, `Mozilla`), and case-sensitive matching is simpler and more predictable than case-insensitive options.
- **A6**: The User-Agent header validation applies to all HTTP endpoints that serve subscription data. Health check endpoints (if any) may be exempt for operational convenience — this is left to implementation discretion.
- **A7**: Custom rule content is not validated against the Mihomo rule syntax. The server treats rule strings as opaque values and passes them through verbatim. Invalid rules will cause client-side errors, which is the operator's responsibility to verify during testing.
- **A8**: A custom rule can target `_region_<CC>` for a country code that has no classified proxies. The rule still appears in output; Mihomo's runtime behavior handles the missing target (typically fallback to DIRECT).
- **A9**: The `_region_UNKNOWN` group naming uses `region_` prefix (not `continent_`) because it represents the absence of region classification, not a distinct geographic region. It follows the `_region_*` naming family as a special case.
- **A10**: Custom rules are processed after the trailing-rule drop from 002. If an operator wants to emit a `MATCH` rule, they can do so via custom rules — it will appear before the server-emitted fallback `MATCH`.
- **A11**: The GitHub repository is `github.com/mc256/honkai-rule-server` (not `github.com/junlinchen/honkai-rule-server`). This is a documentation/import path correction.
