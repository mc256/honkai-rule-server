# Feature Specification: URL-Test for Auto-Emitted Regional & Continent Proxy Groups

**Feature Branch**: `012-url-test-region-groups`
**Created**: 2026-05-02
**Status**: Draft
**Input**: User description: "For each of the regional proxy groups (starts with `_region` or `_continent`), instead of using 'select' type, we want to use 'url-test' type. This way we can set the URL and interval for testing the availability of the nodes in the proxy group, and then we can automatically switch to another node in the proxy group if the current node is not available. This will improve the reliability of the proxy groups and also improve the user experience."

**Anchors**:
- [`002-namespacing-and-regions/spec.md`](../002-namespacing-and-regions/spec.md) introduced the `_region_<CC>` groups.
- [`003-custom-rules-access-control/spec.md`](../003-custom-rules-access-control/spec.md) extended this with `_continent_<CONT>` and `_region_UNKNOWN` groups.
- This feature changes only the **`type`** of those auto-emitted groups (and adds the supporting health-check fields). The membership (which proxies belong to which region/continent) and the always-present `Proxies` selector group are unchanged.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Automatic failover when a regional node goes unhealthy (Priority: P1)

The user routes traffic through a regional proxy group (e.g., `_region_JP` for Japan-only services). The group currently includes several upstream nodes from different providers (`alpha_jp1`, `alpha_jp2`, `beta_jp1`, …). Today the group is a `select` type — Mihomo picks the user's manually-chosen node (or the first one if none chosen) and sticks with it until the user notices a failure and manually switches. If a node goes down at 3 AM, the user's traffic stalls until they wake up and pick a different one.

The user wants the group to **probe each node periodically** against a known-good URL and **automatically pick a healthy one**. If the current node fails the probe N times, traffic transparently moves to the next-best healthy node in the same region. The user doesn't see the failure; the connection just keeps working.

**Why this priority**: This is the only user story. The change is small in scope but the reliability win is large — the rest of the project's machinery already routes through `_region_*` and `_continent_*` groups; making them self-healing improves every existing rule that targets a region or continent.

**Independent Test**: Configure a `_region_JP` group with three Japan-tagged proxies where the second one is reachable and the others time out on the configured probe URL. Fetch the served subscription with a Mihomo client. Issue a request through Japan-tagged routing. Assert: (a) the served `_region_JP` group is rendered as a `url-test`-type proxy group with the operator-configured `url`, `interval`, `timeout`, `max-failed-times`, and `lazy` fields, (b) the Mihomo client routes the request through the second (healthy) proxy without manual intervention, (c) when the second proxy is taken down and the others come up, the next request transparently routes through one of the now-healthy proxies within the configured `interval` window.

**Acceptance Scenarios**:

1. **Given** a region group `_region_JP` with three member proxies, **When** the served subscription is fetched, **Then** the group renders with `type: url-test` and the operator-configured health-check fields (`url`, `interval`, `timeout`, `max-failed-times`, `lazy`).
2. **Given** the same group, **When** the served subscription is fetched at two different times against the same input snapshot, **Then** the rendered group is byte-identical (constitution principle II preserved).
3. **Given** a continent group `_continent_AS` containing several `_region_*` groups, **When** the served subscription is fetched, **Then** the continent group renders with `type: url-test` (same convention as regions) and its members are still the underlying region groups (membership unchanged from 003).
4. **Given** a region group `_region_UNKNOWN` (catch-all for proxies whose country could not be inferred per 002 / 003), **When** the served subscription is fetched, **Then** the group renders with `type: url-test` (the prefix rule applies regardless of whether the suffix is a real country code).
5. **Given** the always-present `Proxies` group (FR-009a from 001), **When** the served subscription is fetched, **Then** that group remains `type: select` (manual override stays available; only `_region_*` and `_continent_*` groups switch to `url-test`).
6. **Given** a region group with exactly one member proxy, **When** the served subscription is fetched, **Then** the group still renders with `type: url-test` (single-member url-test is well-defined: client always uses the one member if healthy, otherwise the group reports unavailable).
7. **Given** a region group whose only member proxy is unreachable per the probe, **When** the Mihomo client tries to use it, **Then** the client falls through per its standard url-test behaviour (no special server-side rule beyond emitting `url-test`).
8. **Given** the operator overrides any of the health-check fields via configuration (e.g., a longer `interval`), **When** the served subscription is fetched, **Then** the rendered groups reflect the override and the change is noted in startup logs.

### Edge Cases

- **Region/continent group has zero members** (no proxies matched the country code, but the group somehow got emitted): the group still renders as `url-test`. Mihomo treats zero-member url-test as unavailable. This case shouldn't arise in practice (groups are only emitted when at least one proxy maps to them, per 002 FR / 003 FR), but the rendering rule is robust either way.
- **A user-defined custom proxy group with the same `_region_` or `_continent_` prefix**: by convention the underscore prefix is reserved for server-emitted groups. If an upstream provider includes a group with such a prefix, 002's namespacing already prefixes it with the provider name (e.g., `alpha__region_JP`), so it does NOT collide with the server-emitted `_region_JP`. The url-test rule applies only to groups whose name starts with `_region_` or `_continent_` (i.e., emitted by the server, not by an upstream).
- **Operator misconfigures the health-check URL** (typo, returns 5xx, requires auth): all members of every `_region_*` / `_continent_*` group fail their probes simultaneously. Mihomo's url-test fallback (route through the "least-bad" node, or refuse) takes over. The server-side change is still correct — the misconfiguration is operator-visible. The startup log of the resolved `url` value (FR-008) helps the operator notice.
- **`Profile-Update-Interval` shrinks the client's refresh frequency below the `interval` health-check value**: harmless. The client probes independently of subscription refresh; the two cadences don't interact.
- **A previously-`select`-type `_region_*` group exists in a downstream user's saved local config** (because the user copy-pasted the served body and modified it): irrelevant. The server emits a fresh body on every request; downstream copies are out of scope. After this feature ships, all served bodies have the new type.
- **Mihomo / Clash version that doesn't support url-test** (very old fork): out of scope. The project already targets stock Mihomo / Clash, where url-test has been supported for years.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: When emitting an auto-region group (any group whose `name` starts with `_region_`, including `_region_UNKNOWN`), the server MUST emit it with `type: url-test` (NOT `select`).

- **FR-002**: When emitting an auto-continent group (any group whose `name` starts with `_continent_`), the server MUST emit it with `type: url-test` (NOT `select`).

- **FR-003**: For every group emitted under FR-001 / FR-002, the server MUST populate the following health-check fields:
  - `url` — the HTTP(S) URL the client probes for liveness (operator-configured; default `https://www.gstatic.com/generate_204`).
  - `interval` — seconds between probes (operator-configured; default `10`).
  - `timeout` — milliseconds before a probe is considered failed (operator-configured; default `3000`).
  - `max-failed-times` — number of consecutive failed probes before the client treats the proxy as unhealthy (operator-configured; default `3`).
  - `lazy` — boolean; if `true`, probes only run when the group is actively in use (operator-configured; default `true`).

- **FR-004**: Health-check field values MUST be operator-configurable via environment variables, one variable per field (matching the project's existing pattern: `HONKAI_RULE_CLIENT_UA`, `FALLBACK_RULE_TARGET`, `URL_PATH_PREFIX`, `PROXIES_GROUP_NAME`). The five env vars, their types, defaults, and the YAML field they populate:

  | Env var | Default | Type | YAML field |
  |---|---|---|---|
  | `URL_TEST_URL` | `https://www.gstatic.com/generate_204` | string | `url` |
  | `URL_TEST_INTERVAL_SECONDS` | `10` | integer | `interval` |
  | `URL_TEST_TIMEOUT_MS` | `3000` | integer | `timeout` |
  | `URL_TEST_MAX_FAILED_TIMES` | `3` | integer | `max-failed-times` |
  | `URL_TEST_LAZY` | `true` | boolean | `lazy` |

  Defaults match the user-confirmed example exactly. Unset or empty env vars MUST fall back to these defaults (consistent with `FALLBACK_RULE_TARGET`'s "empty → default" behavior from 010). A single set of values applies to ALL auto-emitted region and continent groups; per-group overrides are out of scope for this feature.

- **FR-004a**: Validation at startup — invalid values MUST cause a loud, fatal startup failure (Constitution Principle III: strict-schema-loud-fail). Specifically:
  - `URL_TEST_INTERVAL_SECONDS`, `URL_TEST_TIMEOUT_MS`, `URL_TEST_MAX_FAILED_TIMES` MUST be integers ≥ 1. A non-integer value, a negative value, or zero MUST cause startup to abort with a structured error log identifying the offending env var and value.
  - `URL_TEST_LAZY` MUST be one of `true` / `false` (case-insensitive). Other values MUST cause startup abort.
  - `URL_TEST_URL` is parsed as-is (no scheme/format validation; operator typos surface at runtime via probe failures, which the operator notices via FR-008's startup log).

- **FR-005**: The always-present `Proxies` selector group (per 001 FR-009a) MUST remain `type: select`. This feature does NOT change manual-override groups.

- **FR-006**: Custom user-defined proxy groups (loaded from custom-rules YAML files per 003) MUST NOT be modified by this feature. Whatever `type` they declare flows through verbatim. The url-test conversion applies ONLY to groups whose name has the server-emitted prefix `_region_` or `_continent_` AND were emitted by the server in this request (i.e., the `AppendRegionGroups` / `AppendContinentGroups` code path).

- **FR-007**: Field ordering for url-test groups MUST follow 004's existing block-style + field-ordering convention (`name`, `type`, `proxies` first, then the remaining health-check fields in the order defined in FR-003). The served YAML output MUST remain readable and consistent with existing region groups apart from the type/field changes.

- **FR-008**: At server startup, the resolved values of all five health-check parameters (`url`, `interval`, `timeout`, `max-failed-times`, `lazy`) MUST be logged at INFO level alongside the existing startup-log fields (`config loaded`, `subscription sources`, etc.) so the operator can confirm the active configuration without inspecting the served body.

- **FR-009**: Determinism: the served YAML for a `_region_*` or `_continent_*` group MUST be byte-identical across two requests with the same upstream snapshot, the same operator configuration, and the same clock value. The url-test fields are static given the operator config; they don't introduce nondeterminism.

- **FR-010**: When the operator changes any of the five health-check parameters (env var update + restart), the served YAML for every `_region_*` / `_continent_*` group MUST reflect the new values starting from the next request after the pod restart. A pod restart is required (env vars are read at startup); ConfigMap-style hot-reload is NOT in scope.

### Key Entities

- **Auto-emitted region group** (`_region_<CC>`): A proxy group server-emitted per 002 / 003, named by ISO 3166-1 alpha-2 country code. Membership: every upstream proxy whose display name resolves to country `<CC>`. After this feature: type `url-test` with health-check fields.
- **Auto-emitted continent group** (`_continent_<CONT>`): A proxy group server-emitted per 003, aggregating multiple `_region_<CC>` groups via the country-to-continent mapping. Membership: a list of `_region_<CC>` group references. After this feature: type `url-test` with health-check fields.
- **Catch-all unknown-region group** (`_region_UNKNOWN`): A proxy group server-emitted per 003 for proxies whose country could not be inferred. Membership: those unclassifiable proxies. After this feature: type `url-test` with health-check fields.
- **Health-check parameter set**: A 5-tuple (`url`, `interval`, `timeout`, `max-failed-times`, `lazy`) shared across all auto-emitted region and continent groups. Operator-configured at server startup; embedded verbatim in every emitted group's YAML.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Every auto-emitted region group (`_region_*`) and continent group (`_continent_*`) in the served body has `type: url-test` and the five configured health-check fields (`url`, `interval`, `timeout`, `max-failed-times`, `lazy`).

- **SC-002**: The always-present `Proxies` group in the served body has `type: select` (unchanged) and does NOT have any of the five health-check fields.

- **SC-003**: For a fixture with at least one `_region_*` and one `_continent_*` group, the served YAML for those groups is byte-identical across two requests served against the same upstream snapshot, same operator config, and same fixed clock.

- **SC-004**: When the operator overrides any of the five health-check fields via environment configuration and restarts the pod, the served body for every auto-emitted region / continent group reflects the override on the next served request.

- **SC-005**: A Mihomo client configured against the served subscription, with a region group whose first member proxy fails the probe URL, transparently routes traffic through one of the remaining healthy members within `interval × max-failed-times` seconds (no manual intervention required). This validates the behavior end-to-end against a real client.

- **SC-006**: The startup log emitted on a fresh pod boot includes the resolved values of `url`, `interval`, `timeout`, `max-failed-times`, and `lazy` as a single structured log line, so the operator can `kubectl logs | grep` for the active configuration without inspecting the served YAML.

- **SC-007**: Custom user-defined proxy groups (loaded from `config/custom-rules/`) appearing in the served body have unchanged `type` (whatever they declared) — this feature does NOT touch their type field, even if their name happens to start with an underscore but does NOT match `_region_` or `_continent_`.

## Assumptions

- The operator's example values in the user description (`url: https://www.gstatic.com/generate_204`, `interval: 10`, `timeout: 3000`, `max-failed-times: 3`, `lazy: true`) are the intended defaults. They match what well-tuned Mihomo / Clash configurations commonly use; the `gstatic.com` 204 endpoint is a deliberate choice (small response, globally available, no auth).
- A single set of health-check parameters is sufficient for v1; per-region overrides are a future concern (would need a more elaborate config schema and probably aren't needed in practice — the same probe URL is appropriate across all regions, and intervals/timeouts are network conditions, not regional ones).
- Mihomo / Clash url-test groups are stable in modern client versions and the wire format `type: url-test` plus the five fields is well-supported. The project already targets stock Mihomo per existing tests.
- Backward compatibility for stock Mihomo / Clash clients: no client-side change is needed. Clients refresh on their normal `Profile-Update-Interval` cadence and see the new group `type` automatically. After the refresh, behavior changes from "manual select" to "auto url-test" — which is a UX improvement (auto failover), but does mean a user who previously **manually** picked a node within a region group will lose that ability for region groups specifically. They retain manual override via the always-present `Proxies` selector group (FR-005).
- The server's existing snapshot test suite covers the served body byte-for-byte. This feature lands a deliberate, reviewable diff in those snapshots: every `_region_*` and `_continent_*` group's `type: select` flips to `type: url-test` and the five new fields appear; nothing else changes. The snapshot diff is the primary verification artifact for FR-001..FR-007.
- The five health-check fields' YAML emission style follows 004's block-style proxy-group convention. They render in the order specified in FR-003 (`url`, `interval`, `timeout`, `max-failed-times`, `lazy`) for readability and reviewability.
- Test coverage: new unit tests for the url-test field emission (one per field, plus combined); existing snapshot tests get one deliberate update covering all `_region_*` / `_continent_*` groups across the fixture suite. No new integration tests are mandatory — the snapshot tests already cover end-to-end serving behavior; SC-005 (real-client failover) is operator-validated at deploy time.
- The current project Go code (specifically `internal/merge/region.go`'s `AppendRegionGroups` and `AppendContinentGroups`) is the only place these groups are emitted. The feature scope is bounded to that file plus configuration plumbing and snapshots — this is documented anchor for the eventual `plan.md`, not a spec-level prescription.
