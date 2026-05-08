# Feature Specification: Subscription Aggregator (Multi-Source Merge + Own Proxies + Traffic Accounting)

**Feature Branch**: `001-subscription-aggregator`
**Created**: 2026-04-29
**Last Updated**: 2026-04-30
**Status**: Draft
**Input**: User description: "we want to build a basic feature first, a Server that takes multiple subscriptions, calculate the total traffic and also add my own proxy servers"

**Resolved scope decisions (2026-04-30):**
- Delivery mode for v1: **subscription mode only** (a Clash/Mihomo subscription endpoint over HTTPS). Override mode is deferred to an upcoming release; the transformation core remains structured so an override-mode adapter can be added later without forking the pipeline (Constitution Principle I).
- Access control: **per-client tokens passed as a URL query parameter** on the served subscription endpoint. Tokens are issuable and revocable per device.
- Own-proxies in routing: **standalone selectable proxies merged into the pool** (no `dialer-proxy` chaining). Chained-exit topology stays in `example/` and lands as a follow-up feature.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Aggregate Multiple Upstream Subscriptions Into One Served Subscription (Priority: P1)

A Mihomo/Sparkle user maintains accounts with several third-party VPN providers and currently juggles multiple subscription URLs in their client. They configure this server with the list of upstream subscription URLs, point their client at the server's single subscription endpoint, and receive one merged config containing every proxy node from every upstream — deduplicated, with name collisions resolved deterministically — instead of having to manage each provider separately.

**Why this priority**: This is the core reason the project exists. Without this, none of the other stories make sense — there is no aggregated config to attach own-proxies to and no aggregated quota to report. It is the smallest slice that delivers usable value (one URL replaces N URLs in the client).

**Independent Test**: Configure two real or fixture upstream subscriptions, request the server's subscription endpoint, parse the returned config, and assert that proxies from both sources are present, that name collisions were resolved per the documented strategy, and that the existing rule blocks from each upstream are preserved or merged per the documented merge order.

**Acceptance Scenarios**:

1. **Given** the server is configured with two upstream subscription URLs, **When** a client fetches the served subscription endpoint, **Then** the response is a valid Mihomo config whose proxy list is the union of the upstreams' proxies with deterministic collision resolution applied.
2. **Given** the server is configured with three upstream subscriptions and one is temporarily unreachable, **When** a client fetches the endpoint within the configured stale-on-error window and a usable cached payload exists for the failed source, **Then** the served config still contains proxies from all three (the failed source served from cache) and the failure is recorded in structured logs.
3. **Given** two upstreams contribute a proxy with the same display name, **When** the merge runs, **Then** the resulting config contains both proxies with names disambiguated per the documented strategy (e.g., per-source suffix), and no proxy is silently dropped.
4. **Given** the same set of upstream payloads, rules, and own-proxies as a previous run, **When** the server produces a served config, **Then** the output is byte-identical to the previous run (deterministic transformation).
5. **Given** two upstreams each contribute a `rules` block and the operator has assigned upstream A a higher priority than upstream B (CSS `z-index`-style ordering), **When** the merge runs, **Then** upstream A's rules appear before upstream B's rules in the served config; within each source the relative order of rules is preserved as received.

---

### User Story 2 - Inject Own Proxy Servers Into the Merged Result (Priority: P2)

The user owns one or more personal proxy servers (e.g., self-hosted exits in specific regions for residency or privacy reasons) that are not part of any upstream subscription. They declare these proxies in server configuration; the served subscription includes them alongside the upstream-sourced proxies so they are usable by the client without any manual editing of the served config.

**Why this priority**: Adds significant value on top of P1 (the user's own infrastructure becomes selectable from the same unified config) but is meaningless without P1 — there is nothing to merge into until aggregation works. Independently testable once P1 exists.

**Independent Test**: Declare two own-proxies in server configuration on top of a working P1 setup, fetch the served subscription, and assert that both own-proxies appear in the returned config's proxy list with the configured names and that they are reachable from the client (selectable in the UI / pingable via the client's proxy test).

**Acceptance Scenarios**:

1. **Given** the server has two upstream subscriptions configured and two own-proxies declared, **When** a client fetches the served subscription, **Then** the proxy list contains the union of upstream proxies and the two own-proxies, with own-proxies clearly identifiable (e.g., distinct group / naming convention).
2. **Given** an own-proxy has a name that collides with an upstream proxy, **When** the merge runs, **Then** the collision is resolved deterministically with the own-proxy's identity preserved (own-proxies take precedence by default), and the conflict is recorded in logs.
3. **Given** an own-proxy declaration is malformed (missing required field, invalid address, unsupported type), **When** the server loads configuration, **Then** the server fails loudly at startup or config-reload time with a message identifying the offending entry — it does not silently drop the proxy.

---

### User Story 3 - Report Aggregated Traffic Quota and Daily Allowance Across All Upstreams (Priority: P3)

The user wants to know, at a glance, how much of their combined paid quota (across all upstream providers) has been consumed, how much remains, when it expires, and how much they can spend per day until expiry without overshooting. When the client fetches the served subscription, the response advertises aggregated traffic metadata (upload/download/total/expiry) — exposed via the same `Subscription-Userinfo` HTTP header that stock Clash subscription servers use, so the client's built-in usage display lights up automatically — and a derived daily-allowance figure is exposed via the health surface (and optionally as a custom response header) so the user can budget without doing the arithmetic.

**Why this priority**: A usability and budgeting improvement on top of P1+P2. The aggregation already happens in the merge, so exposing the rolled-up numbers and one derived figure is incremental. Not required for the served config to be functional, hence P3.

**Independent Test**: Configure two upstream subscriptions with known per-source traffic metadata (real `Subscription-Userinfo`-style values from the providers, or fixture values), fetch the served subscription, and assert that the `Subscription-Userinfo` header on the response equals the per-source-summed values for `upload`, `download`, and `total`, that the reported `expire` uses the documented aggregation rule, and that the derived daily-allowance figure equals `(total − upload − download) / days_until_expire` to within rounding.

**Acceptance Scenarios**:

1. **Given** two upstreams' `Subscription-Userinfo` headers report (in bytes) `upload=10737418240; download=42949672960; total=214748364800` and `upload=5368709120; download=16106127360; total=107374182400` respectively, **When** a client fetches the served subscription, **Then** the response `Subscription-Userinfo` header carries aggregated values `upload=16106127360; download=59055800320; total=322122547200; expire=<aggregated_unix>` in the wire format defined by FR-011.
2. **Given** one upstream is unreachable and no recent traffic metadata is cached for it, **When** a client fetches the served subscription, **Then** the aggregated metadata reflects the available sources, the missing source is recorded in logs, and the absence does not produce a misleading "0" that would falsely suggest free quota.
3. **Given** upstreams report different expiry timestamps, **When** the response is built, **Then** the reported `expire` follows the documented aggregation rule (e.g., earliest non-zero expiry across sources) and is consistent across requests for the same input.
4. **Given** two upstreams: source A with `total=200GB / used=50GB / expire = now + 30 days`, and source B with `total=100GB / used=20GB / expire = now + 5 days`, **When** the user reads the daily allowance, **Then** the per-day rate component equals `(150GB ÷ 30) + (80GB ÷ 5) = 5GB + 16GB = 21GB/day` — the per-source weighted sum that reflects spending each provider's remaining quota at the rate it expires.
5. **Given** every upstream reports `expire=0` (no-expiry plans), **When** the user reads the daily allowance, **Then** the per-day rate is `0`, the no-expiry-remaining figure is the sum of all sources' remaining bytes, and the served `Subscription-Userinfo` header carries `expire=0`.
6. **Given** one source has `expire = now − 1 day` (already expired, but the operator hasn't removed the row yet), **When** the user reads the daily allowance, **Then** that source contributes `0` to the per-day rate, appears in the expired-source flag list on the health surface, and the served `Subscription-Userinfo` `expire` reflects the next-soonest non-zero expiry across the remaining sources.

---

### User Story 4 - Behave Like a Stock Clash Subscription Server (Priority: P2)

The user wants the served endpoint to be a drop-in replacement for any other Clash/Mihomo subscription URL — they paste the URL (with their per-client token in the query string) into the client's "Add Profile from URL" field, and the client treats it identically to any other provider: shows the usage bar, respects the suggested update interval, and auto-refreshes the profile. No custom client logic, no per-client modifications.

**Why this priority**: This is what makes the aggregation useful day-to-day. P2 because it is independent of US3 (US3 ships the *numbers*; US4 ships the *headers and shape* that make a stock client consume them). Without US4 the user has to manually re-fetch.

**Independent Test**: Configure the server with one upstream and one own-proxy, fetch the served endpoint with a valid token from a stock Mihomo/Sparkle client, and assert: (a) the response is `Content-Type: application/yaml` (or the conventional Clash type), (b) the `Subscription-Userinfo` and `Profile-Update-Interval` headers are present and well-formed, (c) the body parses as a valid Clash config, (d) the client UI shows usage data and an update interval without manual configuration.

**Acceptance Scenarios**:

1. **Given** a request to the served endpoint with a valid token query parameter, **When** the server responds, **Then** the response includes a well-formed `Subscription-Userinfo` header with aggregated `upload; download; total; expire` and a `Profile-Update-Interval` header with the value derived from upstreams per the documented rule.
2. **Given** a request to the served endpoint with a missing, malformed, or revoked token, **When** the server processes it, **Then** the server returns an authentication error (e.g., HTTP 401/403) with no merged subscription body, no `Subscription-Userinfo` header, and a structured log entry recording the rejection (without echoing the rejected token).
3. **Given** an upstream supplies a `Profile-Update-Interval` header, **When** the server captures the upstream response, **Then** that value is stored alongside the cached payload and feeds into the aggregated `Profile-Update-Interval` returned to clients.

---

### Edge Cases

- **All upstreams fail and no cache exists** for any of them: the server MUST fail closed (do not serve a partial or empty config that the client would silently accept), surface the failure in logs, and reflect it in any health surface the server exposes.
- **An upstream returns a malformed payload** (truncated YAML, wrong content type, HTML error page): the source MUST be treated as failed for that fetch (subject to the stale-on-error rule) — it MUST NOT contaminate the merged output.
- **An upstream has zero proxies** (provider returned a valid but empty list): the source contributes nothing to the proxy pool but is still represented in traffic accounting and logs.
- **An upstream's traffic metadata is absent or unparseable**: aggregation skips that source's contribution to the rolled-up numbers and records the omission, rather than counting it as zero.
- **Own-proxy declaration is changed** (added/removed/renamed) between two requests with otherwise identical inputs: the served output reflects the change and remains deterministic for the new input set; previously cached client subscriptions become stale on next fetch.
- **Repeated rapid requests** within the cache TTL: the server serves from cache without re-fetching upstreams, and the served output is identical across those requests.
- **Subscription credentials in upstream URLs**: MUST NOT appear in the served output, in own-proxy advertisements, or in logs.
- **Two upstreams contribute conflicting rules** (same matcher, different target): the configured per-source priority order decides which one wins (the higher-priority source's rule is emitted first; a downstream Clash matcher will hit it first). The conflict MUST be recorded in logs.
- **An upstream omits `Subscription-Userinfo` or `Profile-Update-Interval`**: that source contributes no traffic numbers (per FR-012) and no update-interval value (the aggregated `Profile-Update-Interval` falls back to the documented default).
- **Aggregated `expire` is in the past** (a positive Unix timestamp earlier than `now`): the daily-allowance figure MUST be reported as `0` (not negative, not `Infinity`, not "stale") and the condition MUST be recorded in logs.
- **Aggregated `expire` is `0`** (every upstream is on a no-expiry plan): the daily-allowance figure MUST be reported as the remaining-bytes total flagged "no-expiry" rather than a per-day rate, and the served `Subscription-Userinfo` header MUST carry `expire=0`.
- **Upstream `Profile-Update-Interval` is `0` or absent on every source**: the served `Profile-Update-Interval` MUST fall back to a configured server default (in hours), recorded in logs as a fallback event.
- **A client requests with a token that was just revoked**: the server MUST reject within one request (no grace period beyond what the token store inherently allows), without leaking that the token was previously valid.
- **Two upstreams contribute proxy-groups with the same name** (e.g., both call a group `Auto`): the merge MUST union the member lists into one output group of that name; group `type` and other group attributes come from the highest-priority source, with conflicts logged. (Geo-aware proxy-group merging is a follow-up feature per FR-008a.)
- **All sources have `expire == 0`** (every plan is no-expiry): the daily-allowance per-day rate MUST be `0`, the no-expiry-remaining MUST be the sum of all remaining bytes, and the served `Subscription-Userinfo` `expire` MUST be `0`.
- **Bootstrap window elapses with at least one source still failing first fetch**: the server MUST fail closed (return 503 to clients) until the operator intervenes, NOT serve a partial config. The health surface MUST clearly identify the failing source(s).

## Requirements *(mandatory)*

### Functional Requirements

#### Subscription Aggregation

- **FR-001**: The server MUST accept a configured list of upstream subscriptions defined in a **subscriptions CSV file** (one row per upstream, one or more rows total) and produce, on request, a single merged subscription whose proxy pool is the union of all reachable upstreams' proxies.
- **FR-001a**: The subscriptions CSV MUST have an explicit, versioned column schema matching the format used in `example/subscriptions.csv`. The required columns (in any order; identified by header row) are:
  1. **`name`** — unique, stable per-source identifier (used for collision-suffixing per FR-002, for log correlation, and as the lookup key for source-specific overrides). Duplicate names MUST cause loud failure at load time.
  2. **`link`** — the upstream subscription URL the server fetches. URLs containing embedded credentials (path tokens, query tokens) are permitted in this column; see FR-016 for how the file itself is treated as secret-bearing.
  3. **`priority`** — integer used as the per-source rule priority (CSS `z-index`-style) for the merge defined in FR-005a. Higher values emit earlier in the served `rules` block. Ties are broken by row order.
  4. **`enable`** — case-insensitive string `Enable` or `Disable`. Rows with `Disable` are loaded and validated but skipped at fetch time and excluded from the merged output, with a startup log line listing disabled sources. Any other value MUST cause loud failure (no quiet "treated as disabled").
  Optional columns supported by the schema: **`ttl_seconds`** (per-source cache TTL; falls back to a server-global default), **`stale_on_error_seconds`** (per-source stale window; falls back to a server-global default). Unknown columns MUST cause loud failure at load time (no silent ignore) so a typo cannot become a missing override.
- **FR-001b**: Loading the subscriptions CSV MUST fail loudly on any of: file missing, header row missing or mismatched against the schema, duplicate `name`, malformed `link`, non-integer `priority` / `ttl_seconds` / `stale_on_error_seconds`, non-`Enable`/`Disable` value in `enable`, or unknown column. A load failure during a config reload MUST leave the previous valid configuration in effect (FR-017).
- **FR-002**: The server MUST resolve proxy-name collisions across upstreams using a deterministic, documented strategy (e.g., per-source suffix). Silent overwrite is forbidden; the strategy MUST be uniformly applied and recorded in logs when a collision occurs.
- **FR-003**: The server MUST cache each upstream's last successful fetch with a per-source TTL and MUST serve a stale cached payload within a configurable stale-on-error window when a refresh fails. When no usable cache exists for a failed source, the server MUST surface the failure in logs and MUST NOT silently drop the source from the served output.
- **FR-003a**: Upstream fetches MUST run on an **independent background schedule per source** (timer-driven, decoupled from client requests). Client requests MUST be served exclusively from the in-memory cache and MUST NOT trigger an upstream fetch — under no traffic level can a popular endpoint hammer upstream providers. The fetch schedule per source MUST honor the source's TTL (FR-001a optional `ttl_seconds`, falling back to the server-global default).
- **FR-003b**: On cold start, the server MUST attempt one fetch of every enabled source before serving client traffic. Until every enabled source has either succeeded or exhausted retries within a configurable bootstrap window, the served endpoint MUST return a service-unavailable error and the health surface MUST report "warming up". A source that fails its bootstrap fetch but has no cached payload MUST cause the server to fail closed (it MUST NOT serve a config that is silently missing one of the user's providers).
- **FR-004**: Given the same set of inputs (upstream payload snapshots, own-proxy declarations, server config), the merged subscription served by the server MUST be byte-identical across runs.
- **FR-005**: The served subscription MUST NOT echo any upstream subscription URL, query token, authentication header, or other credential material from the fetch layer.
- **FR-005a**: When upstream subscriptions include a `rules` block, the server MUST merge them in a configured per-source priority order (CSS `z-index`-style — higher priority sources' rules emit first into the served `rules` block). Within a single source, the relative order of rules MUST be preserved as received. Each upstream's priority MUST be configurable; when two sources are assigned the same priority the tiebreak MUST be deterministic (e.g., declaration order in the configuration).
- **FR-005b**: The server MUST capture, alongside each upstream's cached payload, the upstream's HTTP response headers — at minimum `Subscription-Userinfo` and `Profile-Update-Interval` — and use these as the source of truth for traffic-metadata aggregation (FR-010..FR-012) and for the aggregated `Profile-Update-Interval` returned to clients (FR-011a). The expected on-the-wire formats are: `Subscription-Userinfo: upload=<bytes>; download=<bytes>; total=<bytes>; expire=<unix_seconds>` (integer values, semicolon-space separated, `expire=0` conventionally means "no expiry") and `Profile-Update-Interval: <integer_hours>`.
- **FR-005c**: Clash configuration globals (everything in the served body that is NOT `proxies`, `proxy-groups`, or `rules` — e.g., `mixed-port`, `mode`, `dns`, `external-controller`, `log-level`) MUST come from a **server-side served-config template** that is committed alongside the server code (`templates/served-config.template.yaml`, see this spec dir for the v1 draft). Upstream globals MUST NOT be propagated to the served output. This keeps the served output independent of which upstream is "first" and keeps deterministic fixtures stable across upstream rotations.

#### Own Proxy Servers

- **FR-006**: The server MUST accept an own-proxies YAML file containing two top-level keys — `proxies` (zero or more proxy declarations in standard Clash format) and `proxy-groups` (zero or more proxy-group declarations in standard Clash format) — validate it at load time, and include both blocks in the served subscription. Own-proxies are merged into the proxy pool **as standalone selectable proxies** (no `dialer-proxy` chaining behind upstream nodes; chained-exit topology is out of scope for v1 and deferred).
- **FR-007**: Own-proxy validation failures (missing required fields, invalid addresses, unsupported types, malformed proxy-group references) MUST cause loud failure at server startup or config-reload — silent skip is forbidden. The own-proxies YAML file is secret-bearing per FR-016.
- **FR-008**: When an own-proxy name collides with an upstream proxy name, the merge MUST preserve the own-proxy's identity by default and apply the documented disambiguation strategy to the upstream entry, recording the collision in logs.
- **FR-008a**: When proxy-group names collide across sources (own-proxies YAML, upstream subscriptions), the merge MUST **union the member lists** under a single output group of that name, deduplicating members. Group `type` and other group attributes (e.g., `url`, `interval` for `url-test`) MUST come from the highest-priority source that defined the group; conflicts on these attributes MUST be recorded in logs. Customizable proxy-group merge rules (e.g., merging by geo location — country / region) are deferred to a follow-up feature.
- **FR-009**: Own-proxies MUST be identifiable in the served subscription (e.g., via a documented naming convention or a dedicated own-defined proxy group) so a client user can distinguish them from upstream-sourced proxies.
- **FR-009a**: The served subscription MUST always contain at least one `select`-type proxy-group named `Proxies` (default name; configurable via the served-config template) whose members are the union of all proxies (upstream + own). This guarantees client UI selectability even when no upstream contributed any proxy-groups and no own-proxy-groups are declared.

#### Traffic Aggregation & Daily Allowance

- **FR-010**: For each configured upstream, the server MUST capture the upstream's reported traffic metadata at fetch time by parsing the `Subscription-Userinfo` response header in the format `upload=<bytes>; download=<bytes>; total=<bytes>; expire=<unix_seconds>` and persist the parsed values (four integers) alongside the cached payload. `expire=0` MUST be interpreted as "no expiry" for that source and MUST NOT be treated as "expired in 1970".
- **FR-011**: When serving the merged subscription, the server MUST emit a `Subscription-Userinfo` response header in the same wire format (`upload=<bytes>; download=<bytes>; total=<bytes>; expire=<unix_seconds>`) carrying the aggregated `upload`, `download`, and `total` (summed across all upstreams whose metadata is currently known) and an aggregated `expire` chosen by a documented rule (default: earliest non-zero `expire` across sources; if every source reports `expire=0`, emit `expire=0`).
- **FR-011a**: When serving the merged subscription, the server MUST emit a `Profile-Update-Interval` response header carrying an integer **hours** value, derived from the upstreams' `Profile-Update-Interval` values per a documented rule (default: minimum non-zero value across sources, falling back to a configured default when no upstream supplies one).
- **FR-011b**: The server MUST compute a derived **daily allowance** as a per-source weighted sum: `Σ_i max(0, (total_i − upload_i − download_i)) ÷ max(1, ceil((expire_i − now_unix) ÷ 86400))` summed across every upstream `i` whose metadata is currently known **AND whose `expire_i > now_unix`**. The figure has units of bytes per day and MUST be exposed via the health surface (FR-015). The per-source weighted form is required because providers commonly have very different expiry dates and quotas; a single `total / earliest_expire` would be either wildly pessimistic or wildly optimistic. **Sources with `expire_i == 0`** (no-expiry plans) MUST be reported separately on the health surface as a "no-expiry remaining bytes" figure (the sum of their remaining bytes), NOT folded into the per-day rate. **Sources with `expire_i > 0` but already past** MUST contribute `0` to the daily-allowance sum and MUST be flagged separately on the health surface so the operator notices an expired upstream. The figure MUST be recomputed per request from current inputs.
- **FR-012**: Sources whose traffic metadata is absent or unparseable MUST be excluded from the rolled-up sums (not counted as zero), and their omission MUST be recorded in logs.

#### Observability & Operations

- **FR-013**: The server MUST emit structured logs for: each upstream fetch (sanitized URL, status, payload size, cache hit/miss, applied failure rule), each name collision and its resolution, each rejected own-proxy declaration, and each served-subscription request (with the inputs that contributed to the response).
- **FR-014**: Logs and any served output MUST NOT contain subscription credentials, own-proxy authentication material, or other secrets. Verbosity MUST be configurable.
- **FR-015**: The server MUST expose a health surface that reflects per-upstream fetch state (last success time, last failure reason if any, currently serving from cache yes/no) so an operator can detect a degraded source without parsing logs.

#### Configuration & Secrets

- **FR-016**: The subscriptions CSV file (FR-001a), and any own-proxy authentication material, MUST be treated as **secret-bearing** — the file path MUST be resolvable from environment / deployment configuration (not hard-coded), and the file itself MUST be excluded from the repository (`.gitignore`'d). The subscriptions CSV is explicitly **distinct** from any future "rules CSV" governed by Constitution Principle III: the rules CSV is a routing declaration that is reviewable and publishable; the subscriptions CSV carries provider tokens and is not. This distinction MUST be documented wherever the two files are referenced.
- **FR-017**: The set of upstream subscriptions and own-proxies MUST be reloadable without losing in-flight cached payloads (a config error during reload MUST leave the previously running configuration in effect, not crash the server).

#### Delivery Mode & Authentication

- **FR-018**: The v1 server MUST expose **subscription mode only** — the merged Mihomo/Sparkle config served as a Clash-format payload over HTTPS. Override-mode delivery is deferred to a follow-up release. The transformation core MUST remain factored so an override-mode adapter can be added later without forking the pipeline (Constitution Principle I).
- **FR-019**: The served subscription endpoint MUST require a **per-client token presented as a URL query parameter** on the request URL. Requests with a missing, malformed, expired, or revoked token MUST receive an authentication error and MUST NOT receive any merged subscription body, `Subscription-Userinfo` header, or other response data that could leak server state.
- **FR-019a**: Tokens MUST be issuable and revocable per device. Revocation MUST take effect on the next request (no grace window beyond what the token store inherently allows). Token values MUST NOT appear in logs in plaintext (logged as a stable hash or short prefix only).
- **FR-019b**: The served endpoint MUST behave as a drop-in Clash subscription server: a stock Mihomo/Sparkle client given the URL (with token) MUST be able to add the profile, display usage from the `Subscription-Userinfo` header, and respect the `Profile-Update-Interval` header without any custom client-side configuration.
- **FR-019c**: TLS termination is the deployment's responsibility (Kubernetes Ingress in the target environment) — the server itself MAY listen on plain HTTP inside the cluster. The spec's "served over HTTPS" requirement (FR-019, token-in-URL) is satisfied at the Ingress layer; this server MUST NOT bind to a publicly reachable interface in production.
- **FR-020**: Own-proxies MUST integrate into the served config as **standalone selectable proxies** merged into the proxy pool (no `dialer-proxy` chaining, no automatic insertion into per-region dialer groups). Per Constitution Principle I, the merge layer MUST stay structured so a future feature can introduce chained-exit topology without forking the pipeline.

### Key Entities

- **Upstream Subscription**: A configured third-party Mihomo subscription source, declared as one row in the subscriptions CSV (FR-001a). Static attributes (from the CSV row): `name` (unique identifier, used for collision-suffixing and log correlation), `link` (subscription endpoint, may embed credentials), `priority` (integer, CSS `z-index`-style), `enable` (`Enable` / `Disable`), optional `ttl_seconds`, optional `stale_on_error_seconds`. Runtime attributes (held in memory): last successful fetch timestamp, last failure reason, cached payload, captured upstream headers (at minimum `Subscription-Userinfo` and `Profile-Update-Interval`).
- **Own Proxy**: A user-declared proxy server independent of any upstream. Attributes: name, type, address, port, credential reference, region/role tag for grouping. Validated at load. Merged as a standalone selectable proxy.
- **Served Subscription**: The merged output produced by the server. Attributes: union proxy pool (upstreams + own), deterministic name resolution, priority-ordered merged rules, no embedded credentials. Delivered as a Clash-format payload alongside `Subscription-Userinfo` and `Profile-Update-Interval` response headers.
- **Client Token**: A per-device credential presented as a URL query parameter on requests to the served endpoint. Attributes: opaque value, issuance timestamp, optional expiry, revocation flag, label (for operator recognition).
- **Traffic Metadata**: Four integers parsed from a `Subscription-Userinfo` header — `upload` (bytes), `download` (bytes), `total` (bytes), `expire` (Unix seconds; `0` = no expiry). Captured per upstream; aggregated for the served subscription per documented rules.
- **Update Interval**: Integer hours parsed from a `Profile-Update-Interval` header. Captured per upstream; aggregated to a single value emitted on the served response per a documented rule.
- **Daily Allowance**: Derived figure exposed via the health surface. Three components, all computed per request from current inputs: (a) **per-day rate** = `Σ_i remaining_i ÷ days_until_expire_i` summed across sources where `expire_i > now` (this is the spendable rate today, accounting for the fact that different providers have different expiry days and different remaining quotas); (b) **no-expiry remaining** = `Σ_j remaining_j` summed across sources where `expire_j == 0` (spendable freely, not subject to a per-day rate); (c) **expired-source flags** = list of sources whose `expire > 0` but is now in the past (the operator should renew or remove these).
- **Fetch Result**: Per-upstream fetch outcome record. Attributes: source identifier, timestamp, status, payload size, cache hit/miss, applied failure rule. Drives logs and the health surface.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A user with N upstream subscriptions can replace all N URLs in their client with a single URL pointing at this server and continue to see every proxy they previously had access to (zero net loss of proxies vs. the union of upstream proxies, after deduplication).
- **SC-002**: The served subscription response, given the same inputs, is byte-identical across at least 100 consecutive requests in a fixture-driven test (deterministic transformation verified).
- **SC-003**: When one upstream is offline and a fresh-enough cached payload exists, a fetch of the served subscription still contains that upstream's proxies, and the failure is visible in the health surface within one fetch cycle.
- **SC-004**: When the user adds an own-proxy to configuration, it appears in the served subscription on the next fetch and is selectable from the client without any manual editing of the served config.
- **SC-005**: Aggregated traffic numbers reported in the served subscription equal the per-source-summed numbers within the precision the upstreams themselves report, for every upstream whose metadata was captured this fetch cycle.
- **SC-006**: An operator can determine which upstream is currently degraded (and whether it is being served from cache) from the health surface alone, without reading raw logs.
- **SC-007**: No served response, log line, or own-proxy advertisement contains an upstream credential, an own-proxy authentication material, or a client-token plaintext value in a manual review of the artifacts produced by the fixture-driven test suite.
- **SC-008**: A stock Mihomo or Sparkle client given the served URL with a valid token query parameter successfully adds the profile, displays usage data sourced from the response `Subscription-Userinfo` header, and respects the response `Profile-Update-Interval` — with no client-side configuration beyond pasting the URL.
- **SC-009**: When two upstreams contribute conflicting rules and the operator has assigned them different priorities, the higher-priority source's rule appears earlier in the served `rules` block in 100% of fixture-driven test runs (deterministic priority application).
- **SC-010**: The daily-allowance figure exposed via the health surface equals `(aggregated_total − aggregated_upload − aggregated_download) ÷ days_until_aggregated_expire` to within rounding, recomputed each request from current inputs, and reads `0` when the aggregated expiry has passed.
- **SC-011**: A request with a missing, malformed, or revoked token receives an authentication error and zero bytes of merged subscription content; the rejection is visible in structured logs without leaking the rejected token in plaintext.
- **SC-012**: Sustained client traffic of at least 100 requests/sec against the served endpoint produces zero additional upstream fetches beyond the per-source background schedule (verified by inspecting upstream-fetch counters in fixture-driven load tests). Client requests are served entirely from the in-memory cache per FR-003a.
- **SC-013**: A subscriptions CSV row with `enabled=false` is loaded, validated, surfaced in the startup log, and excluded from both the merged output and the background fetch schedule — verified end-to-end in a fixture-driven test where an enabled→disabled flip removes a source's proxies on the next reload without restarting the server.
- **SC-014**: When two upstreams contribute proxy-groups with the same name (e.g., both have an `Auto` `url-test` group), the served config contains exactly one `Auto` group whose member list is the deduplicated union of both upstreams' member lists; the conflict (if any group attribute differs) is recorded in structured logs.
- **SC-015**: The daily-allowance per-day rate exposed by the health surface, when computed against a fixture with two sources of differing remaining quota and differing expiry, equals the per-source weighted sum within rounding (e.g., 5GB/day + 16GB/day = 21GB/day for the FR-011b fixture). The figure recomputes per request without caching.

## Assumptions

- This is the foundational MVP feature; later features (CSV-driven custom rules per Constitution Principle III, override-mode delivery, chained-exit topology for own-proxies, advanced per-region routing) build on top of it and are out of scope here.
- "Multiple subscriptions" includes the N=1 case — a single upstream is a degenerate aggregation and MUST work.
- The Mihomo / Clash `Subscription-Userinfo` and `Profile-Update-Interval` HTTP-header conventions are the sources of truth for, respectively, upstream traffic metadata and upstream-suggested update interval. Concrete wire formats observed in the wild and adopted as the parser/emitter contract: `Subscription-Userinfo: upload=<bytes>; download=<bytes>; total=<bytes>; expire=<unix_seconds>` (integers, semicolon-space separated, `expire=0` means no expiry) and `Profile-Update-Interval: <integer_hours>`. Upstreams that do not provide one or both headers are handled per FR-012 and FR-011a (documented fallback).
- Other Clash-server response headers seen on real upstream subscriptions (`Profile-Web-Page-Url`, `Content-Disposition` filename, `Cache-Control: no-store, no-cache, must-revalidate`, `Content-Type: application/octet-stream`) are NOT required to be propagated by this MVP. They are candidates for a follow-up feature; if added, the server MUST emit its own values (e.g., a server-configured filename and homepage), not blindly forward upstream values, to avoid leaking which provider supplied any given served response.
- The Constitution's rules CSV (Principle III) is intentionally NOT in scope for this feature. The MVP merges proxies, merges upstream-supplied `rules` blocks in a configurable priority order, and exposes traffic; classifier/routing customization via the project's own CSV arrives in a follow-up feature.
- Clients consuming the served subscription are Mihomo or Sparkle (the same clients targeted by the existing `example/` sandbox) — no other client format is supported in v1.
- Override mode is deferred. The transformation core MUST nonetheless be split into a transform layer + an output adapter so the override-mode adapter can land later without refactoring the merge logic (Constitution Principle I).
- Per-client tokens live in a token store managed by the server; bootstrapping (how the first token is created, how new tokens are issued) is an operational concern handled in `/speckit-plan` — the spec only fixes the runtime behavior (FR-019, FR-019a).
- The subscriptions CSV (FR-001a) is the v1 configuration surface for *upstream subscriptions only*. Own-proxy declarations live in a separate **own-proxies YAML file** (FR-006) with two top-level keys — `proxies` and `proxy-groups` — so any block from a normal Clash config can be cut-and-pasted in. Both files are secret-bearing per FR-016. Keeping the two files separate keeps each schema simple and validator messages sharp.
- TLS termination is handled by the production deployment's Kubernetes Ingress; the server itself runs plain HTTP behind the Ingress (FR-019c). Local development MAY likewise skip TLS.
- Rate limiting / abuse protection on the served endpoint is **out of scope for this MVP** — it will be handled by an nginx cache layer (or equivalent) in front of the server in the production Kubernetes deployment. This is acceptable because (a) tokens are revocable per FR-019a, (b) all client requests serve from the in-memory cache per FR-003a (no upstream amplification), and (c) the cache layer absorbs any remaining load.
- Customizable proxy-group merging strategies (e.g., merge-by-geo: union all members tagged `JP` across upstreams into one `JP-Auto` group regardless of source-side group names) are **deferred to a follow-up feature**. The MVP merges by group name only (FR-008a).
- The served-config template (FR-005c) is a committed artifact stored at `templates/served-config.template.yaml` in the implementation tree. A reference draft scoped to this spec is at `specs/001-subscription-aggregator/templates/served-config.template.yaml`; planning may move/rename it.
- Determinism (FR-004, SC-002) is asserted on the *transform* layer against fixed inputs; upstream fetches themselves are inherently nondeterministic and are isolated behind the cache layer per Constitution Principle II. The daily-allowance figure (FR-011b) is a pure function of aggregated traffic numbers and the current timestamp — for snapshot tests, the timestamp MUST be injectable so the derived value is reproducible.
