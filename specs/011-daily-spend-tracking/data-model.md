# Data Model: Today's-Spend Tracking in Served Subscription-Userinfo

**Feature**: 011-daily-spend-tracking
**Date**: 2026-05-02

This document captures the new `dailyspend.Snapshot` type, the file format on the PVC, the helper functions, and the merge-layer integration. All additions are pure (no I/O) except `FileSnapshotter` which intentionally owns the disk boundary.

---

## New package: `internal/dailyspend/`

Self-contained package owning the snapshot type, the file-backed and in-memory snapshotter implementations, and the rollover-detection / spend-computation pure helpers.

### `dailyspend.Snapshot`

```go
// Snapshot is the persistent record of "where the upstream counters were
// at today's local midnight" plus "what today's pinned allowance is" plus
// "the upload/download ratio per source at midnight" per 011 FR-005/FR-006.
//
// One Snapshot covers one local-day. The first served request after the
// local-midnight boundary regenerates it (lazy rollover per 011 FR-004).
type Snapshot struct {
    LocalDate            string             `json:"snapshot_local_date"`     // "YYYY-MM-DD" in budget timezone
    SnapshotUnix         int64              `json:"snapshot_unix"`           // unix seconds when captured
    AllowanceTodayBytes  int64              `json:"allowance_today_bytes"`   // pinned daily allowance for the day
    Baselines            map[string]int64   `json:"baselines"`               // per-source cumulative_used (= upload + download) at midnight
    BaselineUploadRatios map[string]float64 `json:"baseline_upload_ratios"`  // per-source upload / (upload + download) at midnight, [0..1]
}
```

JSON keys match the schema documented in spec FR-006 verbatim.

### `dailyspend.Snapshotter` (interface — declared at use-site via structural typing)

The interface lives implicitly in the consumer (`merge.Pipeline`) — declared inline as `interface { Load() (*Snapshot, error); Save(*Snapshot) error }`. Both implementations below satisfy it structurally without an explicit `var _ Snapshotter = ...` declaration.

**Why structural**: avoids a `merge → dailyspend` import cycle while keeping the type signature self-documenting at the point of use.

### `dailyspend.FileSnapshotter`

```go
type FileSnapshotter struct {
    Path string  // typically /data/today-zero.json
}

func NewFileSnapshotter(path string) *FileSnapshotter

// Load reads the snapshot file. Returns:
//   (nil, nil)  — file doesn't exist; caller should treat as "rollover needed"
//   (*Snapshot, nil) — successful read + parse + validation
//   (nil, nil) + INFO log — file exists but parse / validation failed; caller treats as fresh init (per R6)
//   (nil, err) — real I/O failure (permission denied, etc.); caller bails or alerts
func (f *FileSnapshotter) Load() (*Snapshot, error)

// Save writes the snapshot atomically: write to Path+".tmp", fsync, close, rename to Path.
func (f *FileSnapshotter) Save(s *Snapshot) error
```

**Validation in Load**: `LocalDate` parses as YYYY-MM-DD; `Baselines` and `BaselineUploadRatios` have matching key sets; `AllowanceTodayBytes >= 0`; ratios in `[0.0, 1.0]`.

### `dailyspend.MapSnapshotter`

```go
type MapSnapshotter struct {
    mu      sync.Mutex
    current *Snapshot
}

func NewMapSnapshotter(initial *Snapshot) *MapSnapshotter
func (m *MapSnapshotter) Load() (*Snapshot, error)
func (m *MapSnapshotter) Save(*Snapshot) error
func (m *MapSnapshotter) Current() *Snapshot  // test-only assertion helper
```

### Pure helpers (no I/O, fully testable)

```go
// IsRolloverNeeded reports whether Pipeline.Build should capture a fresh
// snapshot. True when the snapshot is nil (first run / corruption recovery)
// or when its LocalDate doesn't match today in the budget timezone.
func IsRolloverNeeded(s *Snapshot, now time.Time, loc *time.Location) bool

// ComputeUsedToday sums per-source clamped deltas:
//   used_today = Σ_i max(0, perSource[i].(upload+download) - snapshot.Baselines[i])
// Sources missing from snapshot.Baselines (newly appeared since midnight)
// contribute 0. Per 011 FR-001 / FR-008.
func ComputeUsedToday(perSource map[string]fetcher.SubscriptionUserinfo, s *Snapshot) int64

// SplitUsedToday computes weighted-average upload split. Per 011 FR-002:
//   For each source with non-zero usedToday contribution, weight by its
//   contribution × baseline ratio. Aggregate, round, return (upload, download)
//   such that upload + download == usedToday (±1 byte rounding).
func SplitUsedToday(usedToday int64, perSource map[string]fetcher.SubscriptionUserinfo, s *Snapshot) (upload, download int64)

// CaptureSnapshot builds a fresh Snapshot from current state at the
// rollover instant. Called from Pipeline.Build when IsRolloverNeeded
// returns true.
func CaptureSnapshot(perSource map[string]fetcher.SubscriptionUserinfo, allowance int64, now time.Time, loc *time.Location) *Snapshot
```

`CaptureSnapshot` populates `BaselineUploadRatios` per R3: `ratio = upload / (upload + download)` when denominator > 0; `ratio = 0.0` when denominator is 0.

---

## Merge-layer integration

### `merge.Pipeline` (new fields + builders)

```go
type Pipeline struct {
    // ... existing fields ...

    // 011 FR-004/FR-005: snapshot of today's baselines + pinned allowance.
    // Set via WithSnapshotter; nil means "fall back to 010 behavior" (no
    // spend tracking; ComposeServedTrafficHeader without snapshot).
    snapshotter interface {
        Load() (*dailyspend.Snapshot, error)
        Save(*dailyspend.Snapshot) error
    }

    // 011 FR-010: timezone for "today" / "tomorrow" computation.
    // Set via WithBudgetLocation; nil means time.UTC (preserves 010 behavior).
    budgetLocation *time.Location
}

func (p *Pipeline) WithSnapshotter(s interface { Load() (*dailyspend.Snapshot, error); Save(*dailyspend.Snapshot) error }) *Pipeline
func (p *Pipeline) WithBudgetLocation(loc *time.Location) *Pipeline
```

**Note**: the `Pipeline` struct CAN import `internal/dailyspend/` for the `*dailyspend.Snapshot` type — that's a pure data type with no merge imports, so no cycle. The structural-typed interface field on `Pipeline` avoids needing to import a `Snapshotter` interface from anywhere.

### `merge.ComposeServedTrafficHeaderWithSpend` (new pure helper)

```go
// ComposeServedTrafficHeaderWithSpend is the spend-aware variant of 010's
// ComposeServedTrafficHeader. Per 011 FR-001/FR-002/FR-003:
//   - upload + download = used_today
//   - total = allowance_today + used_today
//   - expire = unix(next 00:00 in loc)
//
// Returns the served header value AND the (possibly new) snapshot. When
// the input snapshot is nil OR IsRolloverNeeded(snapshot, clk.Now(), loc),
// captures a fresh snapshot via CaptureSnapshot and returns it as the
// second value (the caller MUST persist it via snapshotter.Save).
//
// When the input snapshot is non-nil and current, the second return is
// the same pointer (no save needed).
//
// Returns (nil, nil) when no source contributed userinfo (per 010 FR-006).
//
// Pure: no I/O. Determinism per 011 FR-013.
func ComposeServedTrafficHeaderWithSpend(
    perSource map[string]fetcher.SubscriptionUserinfo,
    clk clock.Clock,
    snapshot *dailyspend.Snapshot,
    loc *time.Location,
) (header *ServedTrafficHeader, newSnapshot *dailyspend.Snapshot)
```

### `merge.ComposeServedTrafficHeader` (existing 010 helper — unchanged)

Stays as a thin wrapper for callers that don't need spend tracking:

```go
// ComposeServedTrafficHeader (010) returns the daily-allowance encoding
// without spend tracking. New callers should prefer
// ComposeServedTrafficHeaderWithSpend; this wrapper is preserved for
// backward compatibility and for the test-friendly path that doesn't
// need a snapshot.
func ComposeServedTrafficHeader(perSource map[string]fetcher.SubscriptionUserinfo, clk clock.Clock) *ServedTrafficHeader {
    h, _ := ComposeServedTrafficHeaderWithSpend(perSource, clk, nil, time.UTC)
    return h
}
```

This change is internal — the function signature is identical. Callers (currently `Pipeline.Build`) get the same behavior.

### `merge.Pipeline.Build()` (updated)

After the existing aggregations, replace the current `ComposeServedTrafficHeader` call with the spend-aware path when a snapshotter is configured:

```go
var servedTrafficHeader *ServedTrafficHeader
if p.snapshotter != nil {
    snapshot, _ := p.snapshotter.Load()  // err logged inside FileSnapshotter; nil treated as rollover
    loc := p.budgetLocation
    if loc == nil {
        loc = time.UTC
    }
    var newSnapshot *dailyspend.Snapshot
    servedTrafficHeader, newSnapshot = ComposeServedTrafficHeaderWithSpend(userinfoPerSource, p.clock, snapshot, loc)
    if newSnapshot != nil && newSnapshot != snapshot {
        if err := p.snapshotter.Save(newSnapshot); err != nil {
            // log but don't fail; the served header is already correct for this request
            slog.Warn("dailyspend snapshot save failed", "err", err.Error())
        }
    }
} else {
    servedTrafficHeader = ComposeServedTrafficHeader(userinfoPerSource, p.clock)
}
```

This block replaces the single `servedTrafficHeader := ComposeServedTrafficHeader(...)` line in the current `Build()`.

---

## Configuration

### `config.ServerConfig` (new fields)

```go
type ServerConfig struct {
    // ... existing fields ...

    // 011: persistent snapshot file for today's-spend tracking.
    // Default: "/data/today-zero.json". Override via TODAY_ZERO_PATH.
    TodayZeroPath string

    // 011: timezone for "today" / "tomorrow" daily-budget boundary.
    // Default: "America/Toronto". Override via DAILY_BUDGET_TIMEZONE.
    // Validated via time.LoadLocation at startup; loud-fail on bad input.
    DailyBudgetTimezone string

    // BudgetLocation is the parsed *time.Location (computed at Load time
    // from DailyBudgetTimezone). Convenience field so callers don't
    // re-parse on every request.
    BudgetLocation *time.Location
}
```

### Env-var loading in `config.Load`

```go
// Defaults
cfg.TodayZeroPath = "/data/today-zero.json"
cfg.DailyBudgetTimezone = "America/Toronto"

if v := env.Getenv("TODAY_ZERO_PATH"); v != "" {
    cfg.TodayZeroPath = v
}
if v := env.Getenv("DAILY_BUDGET_TIMEZONE"); v != "" {
    cfg.DailyBudgetTimezone = v
}

// Validate timezone at startup (loud-fail per Constitution Principle III).
loc, err := time.LoadLocation(cfg.DailyBudgetTimezone)
if err != nil {
    return nil, fmt.Errorf("DAILY_BUDGET_TIMEZONE=%q is not a valid IANA timezone: %w", cfg.DailyBudgetTimezone, err)
}
cfg.BudgetLocation = loc
```

### Wiring in `cmd/server/main.go`

After the existing `pipeline := merge.NewPipeline(...).With...` chain:

```go
pipeline := merge.NewPipeline(...).
    WithProxiesGroupName(cfg.ProxiesGroupName).
    WithFallbackRuleTarget(cfg.FallbackRuleTarget).
    WithURLTestParams(merge.URLTestParams{...}).
    WithSnapshotter(dailyspend.NewFileSnapshotter(cfg.TodayZeroPath)).
    WithBudgetLocation(cfg.BudgetLocation).
    WithCustomRules(customRules)
```

---

## Route-handler logging changes

Location: `internal/server/routes/subscription.go::Subscription`

After the existing `served_daily_allowance_bytes` / `served_expire_unix` info-log fields, add four more per 011 FR-011:

```go
fields := []any{
    // ... existing fields including 010's ones ...
    "served_used_today_bytes", servedUsedToday(merged.ServedTrafficHeader, /* lookup somehow */),
    "served_total_bytes", servedTotal(merged.ServedTrafficHeader),
    "snapshot_local_date", servedSnapshotDate(merged),
    "rollover_fired", servedRolloverFired(merged),
}
```

**Implementation note**: the existing `merge.MergedConfig.ServedTrafficHeader` carries `DailyAllowanceBytes` and `ExpireUnix` — we need an extension to carry `UsedTodayBytes`, `TotalBytes`, `SnapshotLocalDate`, and `RolloverFired` for log emission. Plan: extend `ServedTrafficHeader` with these four fields (zero values when no snapshotter is configured).

```go
type ServedTrafficHeader struct {
    DailyAllowanceBytes int64    // 010 FR-001
    ExpireUnix          int64    // 010 FR-002
    UsedTodayBytes      int64    // 011 FR-001
    TotalBytes          int64    // 011 FR-002 (= DailyAllowance + UsedToday)
    UploadBytes         int64    // 011 FR-002 (split via SplitUsedToday)
    DownloadBytes       int64    // 011 FR-002 (= UsedToday - Upload)
    SnapshotLocalDate   string   // 011 FR-011
    RolloverFired       bool     // 011 FR-011 (true if this request triggered rollover)
}
```

The output adapter (`internal/output/subscription_mode.go::Render`) updates its emit line to use the new split:

**Before** (010):
```go
fmt.Sprintf("upload=0; download=0; total=%d; expire=%d", h.DailyAllowanceBytes, h.ExpireUnix)
```

**After** (011):
```go
fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d", h.UploadBytes, h.DownloadBytes, h.TotalBytes, h.ExpireUnix)
```

`ComposeServedTrafficHeaderWithSpend` populates all eight fields. `ComposeServedTrafficHeader` (the 010 thin wrapper) populates only the 010 fields, leaving the 011 ones zero — equivalent to the current behavior.

---

## Tests

### `internal/dailyspend/snapshot_test.go`

| Case | Asserts |
|------|---------|
| `TestSnapshot_RoundTrip` | `Save` then `Load` returns identical `*Snapshot` |
| `TestSnapshot_AtomicRename` | Mid-write crash simulation (write `.tmp`, don't rename) leaves the original file intact |
| `TestSnapshot_LoadMissing` | `(nil, nil)` when file doesn't exist (caller treats as rollover) |
| `TestSnapshot_LoadCorrupted` | `(nil, nil)` + INFO log when JSON is malformed (recovery path per FR-007) |
| `TestSnapshot_LoadInvalidLocalDate` | `(nil, nil)` + INFO log when `LocalDate` doesn't parse |
| `TestIsRolloverNeeded_NilSnapshot` | true |
| `TestIsRolloverNeeded_SameDay` | false (snapshot LocalDate matches today in loc) |
| `TestIsRolloverNeeded_NextDay` | true (clock advanced past local midnight) |
| `TestIsRolloverNeeded_DSTSpringForward` | exactly one rollover across the 23-hour day in America/Toronto |
| `TestIsRolloverNeeded_DSTFallBack` | exactly one rollover across the 25-hour day |
| `TestIsRolloverNeeded_YearBoundary` | rollover fires at 00:00 Jan 1 local |
| `TestComputeUsedToday_TwoSources` | sum of clamped deltas matches expected |
| `TestComputeUsedToday_NewSourceNoBaseline` | new source contributes 0 (FR-008) |
| `TestComputeUsedToday_ProviderReset` | source whose current < baseline contributes 0 (clamped, FR-008) |
| `TestSplitUsedToday_AllUpload` | source with ratio 1.0 puts all spend into upload |
| `TestSplitUsedToday_AllDownload` | source with ratio 0.0 puts all spend into download |
| `TestSplitUsedToday_Mixed` | weighted-average ratio across sources; sum equals usedToday ±1 |
| `TestCaptureSnapshot_TwoSourcesFR011b` | the canonical FR-011b fixture produces the expected baselines + ratios |
| `TestCaptureSnapshot_ZeroUploadRatio` | source with `upload+download==0` gets ratio 0.0 (R3) |

### `internal/merge/traffic_test.go` (new tests)

| Case | Asserts |
|------|---------|
| `TestComposeServedTrafficHeaderWithSpend_NoSnapshot` | (nil snapshot input → captured fresh snapshot returned) |
| `TestComposeServedTrafficHeaderWithSpend_AcceptanceScenario1` | midnight → first-of-day request: used ≈ 0, total ≈ allowance |
| `TestComposeServedTrafficHeaderWithSpend_AcceptanceScenario2` | mid-day after N bytes consumed: used == ΣN, total == allowance + ΣN |
| `TestComposeServedTrafficHeaderWithSpend_AcceptanceScenario3` | overspend: total - upload - download may go negative (allowed) |
| `TestComposeServedTrafficHeaderWithSpend_AcceptanceScenario4` | crossing local midnight regenerates snapshot |
| `TestComposeServedTrafficHeaderWithSpend_AcceptanceScenario5` | pod restart: snapshot loaded from disk, used carries forward |
| `TestComposeServedTrafficHeaderWithSpend_AcceptanceScenario6` | provider reset: clamped to 0 |
| `TestComposeServedTrafficHeaderWithSpend_AcceptanceScenario7` | DST transition: exactly one rollover |
| `TestComposeServedTrafficHeaderWithSpend_AcceptanceScenario8` | first boot: initialize from current, used = 0 today |

### `internal/integration/cluster_test.go` (modify)

Update `newTestClusterWithOpts` to inject a `MapSnapshotter` with a sensible initial snapshot (e.g., baselines = current upstream cumulative_used, allowance = 21 GiB matching the canonical fixture) so the integration tests' served `Subscription-Userinfo` reflects the 011 encoding.

### `internal/integration/testdata/snapshots/subscription-userinfo.snap.txt` (regenerate)

Expected diff: `upload=` and `download=` change from 0 to small per-day-spend values; `total=` becomes `allowance + usedToday`; `expire=` shifts from 00:00 UTC to 00:00 America/Toronto on the test fixture's date.
