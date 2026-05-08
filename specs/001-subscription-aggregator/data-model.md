# Data Model: Subscription Aggregator (rev 2 — Go)

**Feature**: `001-subscription-aggregator` | **Date**: 2026-04-30

This document defines every persistent and in-memory entity the server manipulates. Field types are Go syntax; the actual implementation will mirror these exactly under `internal/` (one struct per entity, in the package whose name matches the file path in plan.md). **Rev 2 (2026-04-30)**: types translated from TypeScript to Go; semantics unchanged.

Module-path placeholder: `github.com/<owner>/honkai-rule-server` (replace `<owner>` per research R-MODULE).

---

## Configuration entities (loaded from disk at startup / reload)

### `SubscriptionRow` — one row of the subscriptions CSV

Loaded by `internal/config/subscriptions.go` from `${SUBSCRIPTIONS_CSV_PATH}`. The CSV header is the schema; columns can appear in any order; unknown columns fail loudly (FR-001b).

```go
package config

// SubscriptionRow is one parsed and validated row of the subscriptions CSV.
// See spec FR-001a / FR-001b.
type SubscriptionRow struct {
    // Required columns (FR-001a)
    Name     string // unique, non-empty; collision-suffixing & log correlation
    Link     string // valid URL; may embed credentials in path/query
    Priority int    // CSS z-index style (higher emits earlier in served `rules`)
    Enable   bool   // parsed from "Enable" / "Disable" (case-insensitive)

    // Optional columns; zero value means "use ServerConfig default"
    TTLSeconds            int // per-source cache TTL
    StaleOnErrorSeconds   int // per-source stale window
}
```

**Validation rules (loud failure at load time, FR-001b):**

- `Name`: non-empty, unique across all rows in the file.
- `Link`: must parse via `net/url.Parse`; `url.Scheme` must be `"http"` or `"https"`.
- `Priority`: integer (positive, negative, or zero permitted; ties broken by row order per FR-005a).
- `Enable`: case-insensitive match for `Enable` or `Disable` via `strings.EqualFold`; any other value is a fatal error (FR-001a §4).
- `TTLSeconds`, `StaleOnErrorSeconds`: positive integers if present.
- File MUST contain a header row matching the schema; missing required columns fail with the missing column names listed in the error.
- Unknown columns fail loudly (catches typos like `enabld`).

**Errors** (typed via `errors.Is` / `errors.As`):

```go
type ConfigLoadError struct{ Path string; Err error }
type ConfigSchemaError struct{ Missing []string; Unknown []string }
type ConfigValidationError struct{ Row int; Field string; Reason string }
```

### `OwnProxiesFile` — contents of the own-proxies YAML

Loaded by `internal/config/own_proxies.go` from `${OWN_PROXIES_YAML_PATH}`. Two top-level keys; both required (may be empty arrays).

```go
package config

import "gopkg.in/yaml.v3"

// OwnProxiesFile mirrors the on-disk YAML.
// proxies and proxy-groups are kept as *yaml.Node so unknown / type-specific
// fields pass through unmodified to the merged output (research R4).
type OwnProxiesFile struct {
    Proxies      []*yaml.Node `yaml:"proxies"`
    ProxyGroups  []*yaml.Node `yaml:"proxy-groups"`
}

// Validation extracts the small surface the merge layer needs:
type proxyMeta struct {
    Name   string `yaml:"name"`
    Type   string `yaml:"type"`
    Server string `yaml:"server"`
    Port   int    `yaml:"port"`
}

type proxyGroupMeta struct {
    Name    string   `yaml:"name"`
    Type    string   `yaml:"type"`
    Proxies []string `yaml:"proxies"`
}
```

**Validation rules (FR-007):**

- File MUST exist and parse as valid YAML; missing-file failure is loud at startup.
- `proxies` and `proxy-groups` MUST both be present (may be `[]`).
- Within `proxies`: each `proxyMeta` must have non-empty `Name`, recognized `Type`, non-empty `Server`, integer `Port` in `[1, 65535]`. Duplicate `Name` within the file is fatal (FR-007).
- Within `proxy-groups`: each `proxyGroupMeta` must have non-empty `Name`, recognized `Type`, non-empty `Proxies` slice. Every name in `Proxies` MUST reference an own-proxy in the same file (catches typos before merge — FR-007).
- The file is secret-bearing per FR-016 (it contains proxy server addresses and credentials).

Errors are reported via `OwnProxyValidationError` containing the offending entry's `Name` and the violated rule.

### `TokenStore` — contents of the tokens JSON file

Loaded by `internal/config/tokens.go` from `${TOKENS_PATH}`. Hot-reloaded via `fsnotify` on file change.

```go
package config

import (
    "sync"
    "time"
)

type TokenStore struct {
    mu     sync.RWMutex
    byToken map[string]*TokenRecord // O(1) lookup; rebuilt on each reload
}

type TokenRecord struct {
    Token     string     `json:"token"`       // opaque value; presented as ?token=...
    Label     string     `json:"label"`       // human-readable identifier
    IssuedAt  time.Time  `json:"issued_at"`   // ISO-8601 in JSON
    Revoked   bool       `json:"revoked"`     // when true, Lookup returns (nil, false)
    ExpiresAt *time.Time `json:"expires_at,omitempty"` // optional; nil = never expires
}

// Lookup returns the record + true if the token is active and unrevoked.
func (s *TokenStore) Lookup(token string) (*TokenRecord, bool) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    rec, ok := s.byToken[token]
    if !ok || rec.Revoked {
        return nil, false
    }
    if rec.ExpiresAt != nil && time.Now().After(*rec.ExpiresAt) {
        return nil, false
    }
    return rec, true
}
```

**Validation rules (FR-019, FR-019a):**

- `Token`: non-empty, unique across the array; long enough to resist brute force (min 32 chars in practice; not enforced in v1 — operator responsibility).
- The token store MUST never be logged in full; only `sha256:<first-12-hex>` of any token may appear in logs (FR-014, SC-011) — see `observability.SanitizeToken`.
- Hot reload: `fsnotify.Watcher` on the file with a 250ms debounce; reload failure (malformed JSON) keeps the previously-loaded store in effect, with a warning log.

### `ServerConfig` — environment-bound runtime config

Loaded once at startup from process env vars by `internal/config/server.go`. Not file-backed; not reloadable.

```go
package config

type ServerConfig struct {
    // File paths (all required)
    SubscriptionsCSVPath     string // SUBSCRIPTIONS_CSV_PATH
    OwnProxiesYAMLPath       string // OWN_PROXIES_YAML_PATH
    TokensPath               string // TOKENS_PATH
    ServedConfigTemplatePath string // SERVED_CONFIG_TEMPLATE_PATH
    CacheDir                 string // CACHE_DIR (per-source JSON cache snapshots)

    // Defaults applied when CSV row omits the corresponding column
    DefaultTTLSeconds                  int // DEFAULT_TTL_SECONDS, default 3600
    DefaultStaleOnErrorSeconds         int // DEFAULT_STALE_ON_ERROR_SECONDS, default 86400
    DefaultProfileUpdateIntervalHours  int // DEFAULT_PROFILE_UPDATE_INTERVAL_HOURS, default 12

    // Bootstrap behavior
    BootstrapMaxAttemptsPerSource int // default 3
    BootstrapAttemptDelaySeconds  int // default 5

    // Server
    Port              int        // PORT, default 8080
    LogLevel          slog.Level // LOG_LEVEL: fatal/error/warn/info/debug/trace; default Info
    ProxiesGroupName  string     // name for the always-present select group, default "Proxies"
}
```

`Load(env Env) (*ServerConfig, error)` returns an error if any required env var is missing or any integer-valued env var fails to parse. `Env` is a small interface (`Getenv(key string) string`) to allow tests to inject a fake environment.

---

## Runtime entities (in-memory state, derived during operation)

### `UpstreamCachedPayload` — one source's most recent successful fetch

Held in the in-memory cache (`map[string]*UpstreamCachedPayload` guarded by `sync.RWMutex`) and persisted to `${CACHE_DIR}/${name}.json` after each successful fetch.

```go
package fetcher

import (
    "time"
    "gopkg.in/yaml.v3"
)

type UpstreamCachedPayload struct {
    SourceName    string          `json:"source_name"`
    FetchedAt     time.Time       `json:"fetched_at"`
    BodyYAML      []byte          `json:"body_yaml"`     // raw upstream YAML, verbatim
    ParsedConfig  *yaml.Node      `json:"-"`             // re-parsed on load; not persisted in JSON form
    Headers       UpstreamHeaders `json:"headers"`
    PayloadBytes  int             `json:"payload_bytes"`
}

type UpstreamHeaders struct {
    SubscriptionUserinfo       *SubscriptionUserinfo `json:"subscription_userinfo,omitempty"` // nil if header missing/unparseable
    ProfileUpdateIntervalHours *int                  `json:"profile_update_interval_hours,omitempty"` // nil if header missing/unparseable
}

type SubscriptionUserinfo struct {
    Upload   int64 `json:"upload"`   // bytes
    Download int64 `json:"download"` // bytes
    Total    int64 `json:"total"`    // bytes
    Expire   int64 `json:"expire"`   // unix seconds; 0 = no expiry (FR-010); negative is invalid
}
```

**Wire format (FR-005b):** `Subscription-Userinfo: upload=<bytes>; download=<bytes>; total=<bytes>; expire=<unix_seconds>` (semicolon-space separated, integer values). `Profile-Update-Interval: <integer_hours>`.

`int64` for byte fields because real values exceed `int32` (the alpha example reports `total=654791671808` ≈ 654GB).

### `FetchResult` — outcome record for one fetch attempt

Emitted to logs after every fetch attempt (FR-013); also feeds the bootstrap state machine and the health surface.

```go
package fetcher

import "time"

type FetchOutcome string

const (
    OutcomeSuccess      FetchOutcome = "success"
    OutcomeHTTPError    FetchOutcome = "http_error"
    OutcomeTimeout      FetchOutcome = "timeout"
    OutcomeParseError   FetchOutcome = "parse_error"
    OutcomeNetworkError FetchOutcome = "network_error"
)

type CacheState string

const (
    CacheFreshAfterFetch CacheState = "fresh_after_fetch"
    CacheStaleServed     CacheState = "stale_served"
    CacheFailClosed      CacheState = "fail_closed_no_cache"
)

type FetchResult struct {
    SourceName   string
    AttemptedAt  time.Time
    Outcome      FetchOutcome
    HTTPStatus   int           // 0 if not applicable
    PayloadBytes int           // 0 if not applicable
    CacheState   CacheState
    Error        string        // sanitized error message; "" on success
    Duration     time.Duration
}
```

### `SourceState` — per-source state machine view (drives `/health`)

```go
package fetcher

type BootstrapState string

const (
    BootstrapPending   BootstrapState = "pending"
    BootstrapSucceeded BootstrapState = "succeeded"
    BootstrapFailed    BootstrapState = "failed"
)

type SourceState struct {
    SourceName                 string                `json:"name"`
    Enabled                    bool                  `json:"enabled"`
    LastFetchedAt              *time.Time            `json:"lastFetchedAt,omitempty"`
    LastFetchOutcome           FetchOutcome          `json:"lastFetchOutcome,omitempty"`
    LastFetchError             string                `json:"lastFetchError,omitempty"`
    ServingFromCache           bool                  `json:"servingFromCache"`
    BootstrapState             BootstrapState        `json:"bootstrapState"`
    CachedSubscriptionUserinfo *SubscriptionUserinfo `json:"cachedSubscriptionUserinfo,omitempty"`
    CachedPayloadBytes         *int                  `json:"cachedPayloadBytes,omitempty"`
}
```

JSON tags match the OpenAPI contract (`contracts/health.openapi.yaml`).

### `MergedConfig` — internal representation of the merged subscription

Produced by `internal/merge/pipeline.go`. The output adapter (`internal/output/subscription_mode.go`) consumes this plus the served-config template to render the final response body.

```go
package merge

import "gopkg.in/yaml.v3"

type MergedConfig struct {
    Proxies                              []*yaml.Node        // union of upstream + own; collision-suffixed (FR-002, FR-008)
    ProxyGroups                          []*yaml.Node        // same-name unioned, attributes from highest priority (FR-008a) + always-present `Proxies` group (FR-009a)
    Rules                                []string            // priority-ordered concatenation (FR-005a)
    AggregatedSubscriptionUserinfo       fetcher.SubscriptionUserinfo // sum of upload/download/total; earliest non-zero expire (FR-011)
    AggregatedProfileUpdateIntervalHours int                          // minimum non-zero across sources, falling back to default (FR-011a)
    ContributingSources                  []string            // names of sources that contributed (logged per FR-013)
    Collisions                           []ProxyCollision    // collisions detected during this merge (consumed by logger)
    GroupConflicts                       []GroupConflict     // proxy-group attribute conflicts (consumed by logger)
}

type ProxyCollision struct {
    ProxyName  string
    Sources    []string // sources that contributed an entry with this name
    Resolution string   // e.g., "<name>@<source>" suffix applied
}

type GroupConflict struct {
    GroupName string
    Attribute string                  // e.g., "type" or "interval"
    Values    []GroupConflictValue    // the conflicting values
    Chosen    string                  // source whose value won (highest priority)
}

type GroupConflictValue struct {
    Source string
    Value  any
}
```

### `DailyAllowance` — derived figure exposed via `/health` (FR-011b)

Recomputed per request from current inputs.

```go
package merge

type DailyAllowance struct {
    PerDayRateBytes        int64    `json:"perDayRateBytes"`        // Σ_i remaining_i / days_until_expire_i for sources where expire_i > now
    NoExpiryRemainingBytes int64    `json:"noExpiryRemainingBytes"` // Σ_j remaining_j for sources where expire_j == 0
    ExpiredSourceFlags     []string `json:"expiredSourceFlags"`     // names of sources whose expire > 0 but is now in the past
}

// ComputeDailyAllowance is pure: given the per-source headers and a Clock, returns the figure.
func ComputeDailyAllowance(
    headers map[string]fetcher.SubscriptionUserinfo,
    clk clock.Clock,
) DailyAllowance
```

### `Clock` — injected time source (research R17)

```go
package clock

import "time"

type Clock interface {
    Now() time.Time
}

type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

// FakeClock is for tests. Concurrent Advance + Now is safe.
type FakeClock struct {
    mu  sync.RWMutex
    now time.Time
}

func NewFakeClock(t time.Time) *FakeClock                  { return &FakeClock{now: t} }
func (c *FakeClock) Now() time.Time                        { c.mu.RLock(); defer c.mu.RUnlock(); return c.now }
func (c *FakeClock) Advance(d time.Duration)               { c.mu.Lock(); defer c.mu.Unlock(); c.now = c.now.Add(d) }
```

---

## State transitions

### Bootstrap state machine (per source)

```
        +-------------+
        |  pending    |    initial state at startup
        +-------------+
              |
              | first fetch attempt
              v
     +------------------+         +------------+
     |  fetching        |  --->   | succeeded  |   --> the "ready" channel for this source closes;
     +------------------+         +------------+        Coordinator counts down toward the global Ready signal
              |  fetch failure
              v
     +------------------+
     |  retrying        |  (up to BootstrapMaxAttemptsPerSource)
     +------------------+
              |  exhausted
              v
     +------------------+
     |  failed          |  --> server keeps returning 503; /health flags this source
     +------------------+
```

When the global `Coordinator.Ready` channel closes (every enabled source either `succeeded` or `failed`), the HTTP server's `Listen` accepts client traffic. If any source ended in `failed`, `Coordinator.AllSucceeded()` returns false and the subscription handler returns 503 even though the listener is open.

### Per-source steady-state cycle (after bootstrap success)

```
   +-----------+   ttl_seconds elapsed (time.Ticker tick)   +-----------+
   |  fresh    |  ---------------------------------------> |  stale    |
   +-----------+                                            +-----------+
        ^                                                       |
        |                                                       | scheduled refresh
        |                                                       v
        |                                                  +-----------+
        |                       success                    | refreshing|
        +------------------------------------------------- +-----------+
                                                                |
                                                                | failure within stale_on_error_seconds
                                                                v
                                                         +------------------+
                                                         |  stale_serving   |  /health: degraded=true
                                                         +------------------+
                                                                |
                                                                | stale_on_error_seconds elapsed without success
                                                                v
                                                         +------------------+
                                                         |  unavailable     |  source dropped from merged output
                                                         +------------------+
```

### Token lifecycle

```
  issued ──(operator revokes)──> revoked       (Lookup returns nil, false)
  issued ──(ExpiresAt passes)──> expired       (Lookup returns nil, false)
  issued ──(file removed)─────> not_loaded     (Lookup returns nil, false)
```

---

## Relationships

```
SubscriptionRow (1) -------- fetched by --------> UpstreamCachedPayload (1)
   |
   | Name (FK)
   v
SourceState (1) ----------- exposes via ---------> /health JSON

OwnProxiesFile.Proxies      -+
UpstreamCachedPayload.Proxies +--> merge.Proxies()      -----> MergedConfig.Proxies
                              |
OwnProxiesFile.ProxyGroups    +--> merge.ProxyGroups()  -----> MergedConfig.ProxyGroups
UpstreamCachedPayload.ProxyGroups

UpstreamCachedPayload.Rules ----> merge.Rules() (priority-ordered) -> MergedConfig.Rules

UpstreamCachedPayload.Headers --> merge.Traffic() -> MergedConfig.Aggregated*  +
                                                                                |
                                  merge.ComputeDailyAllowance(headers, clock) -> DailyAllowance --> /health JSON

ServerConfig.ServedConfigTemplatePath -> output.SubscriptionMode.Render()  --+
                                                                              +---> response body + headers
MergedConfig --------------------------- output.SubscriptionMode.Render()  ---+

TokenStore  -- Lookup() -- server.AuthMiddleware (validates URL ?token=) -- per-request gate
```

All entities above are in-memory only except `UpstreamCachedPayload` (also persisted to disk at `${CACHE_DIR}/${SourceName}.json`) and the configuration files that load each `*Row` / `*File` / `TokenStore`. There is no relational database in this MVP.

---

## Package layout summary

| Entity | Package | File |
|---|---|---|
| `SubscriptionRow`, `OwnProxiesFile`, `TokenStore`, `ServerConfig` | `internal/config` | `subscriptions.go`, `own_proxies.go`, `tokens.go`, `server.go` |
| `UpstreamCachedPayload`, `UpstreamHeaders`, `SubscriptionUserinfo`, `FetchResult`, `SourceState`, `BootstrapState`, `Coordinator` | `internal/fetcher` | `cache.go`, `headers.go`, `scheduler.go`, `state.go` |
| `MergedConfig`, `ProxyCollision`, `GroupConflict`, `DailyAllowance` | `internal/merge` | `pipeline.go`, `proxies.go`, `proxy_groups.go`, `rules.go`, `traffic.go` |
| `Clock`, `RealClock`, `FakeClock` | `internal/clock` | `clock.go` |
| Routes, auth middleware | `internal/server`, `internal/server/routes` | `app.go`, `auth.go`, `subscription.go`, `health.go` |
| Logger, `SanitizeToken` | `internal/observability` | `logger.go` |

Cross-package imports flow only **inward** (no cycles): `cmd/server` → `internal/server` → `internal/output` → `internal/merge` → `internal/fetcher` → `internal/config`. `internal/clock` is a leaf package importable from anywhere.
