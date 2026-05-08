# Research: Today's-Spend Tracking in Served Subscription-Userinfo

**Feature**: 011-daily-spend-tracking
**Date**: 2026-05-02

Seven design decisions for adding within-day spend tracking on top of 010's daily-allowance encoding. Resolves the two open items noted in `checklists/requirements.md` (R3 and R5 below).

---

## R1 — `Snapshotter` interface vs. concrete file ops in merge layer

**Decision**: Define a small `Snapshotter` interface used inside the merge layer via Go's structural typing — no explicit type, just an inline interface in the field declaration. Production: `dailyspend.FileSnapshotter`. Tests: `dailyspend.MapSnapshotter`. Both satisfy the structural shape automatically.

**Rationale**:
- Keeps `internal/merge/` free of `internal/dailyspend/` imports (which would risk a cycle if dailyspend ever needs merge types — even though it doesn't today, this preserves the option).
- Mirrors the existing `clock.Clock` injection pattern (small interface, in-memory test impl, file-backed production impl).
- Snapshot file I/O cleanly outside the pure-merge boundary (Constitution Principle I).
- Test setup is hermetic: `pipeline.WithSnapshotter(dailyspend.NewMapSnapshotter(initialSnapshot))` — no temp dirs, no file cleanup.

**Alternatives considered**:
- *Pass the snapshot directly into `Build()` as a value, no interface*: rejected. The route handler would need to know about file paths, defeating the layering goal.
- *Define `Snapshotter` in a third shared package*: rejected. Premature abstraction; structural typing solves it for free.

---

## R2 — Lazy rollover trigger (request-driven, no background timer)

**Decision**: First served request after the local-day boundary triggers rollover. `Pipeline.Build()` calls `snapshotter.Load()`, computes `IsRolloverNeeded(snapshot, clk.Now(), budgetLocation)`. If true, captures fresh state from current per-source userinfo + `ComputeDailyAllowance` at `clk.Now()`, calls `snapshotter.Save()`, returns the new snapshot. Otherwise returns the loaded snapshot.

**Rationale**:
- No background goroutine to manage / leak / lose track of on pod restart.
- Scales to zero-requests-per-day cleanly (no spurious rollover when the cluster is idle).
- One file read per served request is negligible at the project's request volume (tens / day per client × handful of clients).
- Multi-day-gap case (no requests for several days) handled correctly: the date comparison fires once on the next request, and the captured baselines reflect upstream's current cumulative state — which is exactly what the operator wants ("from now on, count today's spend from this point").

**Alternatives considered**:
- *Background ticker firing at midnight*: rejected. Adds a goroutine to babysit, surfaces edge cases at pod restart (the goroutine may have just fired — does the route handler need to wait?), and is overkill for the request volume.
- *Compute rollover on every request without persisting*: rejected. Defeats the persistence goal — pod restart loses the baselines, the bar resets to 0 mid-day.

---

## R3 — Upload-ratio when source has zero historical upload (resolves checklist open item)

**Decision**: When a source's captured baseline has `upload + download == 0` (brand-new source with no consumption yet), set `BaselineUploadRatio = 0.0` (everything attributed to download). When `upload + download > 0`, compute `ratio = upload / (upload + download)`. Persist this ratio in the snapshot per source.

**Rationale**:
- Avoids divide-by-zero at midnight capture time.
- Matches the typical proxy-client traffic shape (download-heavy: web browsing, video streaming dominate).
- A source whose `upload == 0` but `download > 0` (typical) gets ratio 0, which means served `upload = 0` for that source's contribution. Stock client UIs render `upload + download` as "used", so the visual is unaffected.
- A source whose `download == 0` but `upload > 0` (rare — perhaps a backup-uploader use case) gets ratio 1.0, which puts that source's spend into `upload` only. Also rendered correctly.

**Alternatives considered**:
- *Always force ratio = 0.5*: rejected. Loses information when the upstream provider gives realistic ratios; misleading display when the user is heavy-uploader or heavy-downloader.
- *Skip the source from the served header until it has consumption*: rejected. Adds complexity for a corner case that mostly resolves itself within minutes of first traffic.

---

## R4 — America/Toronto timezone loading

**Decision**: Load once at server startup via `time.LoadLocation(cfg.DailyBudgetTimezone)`, hold the resulting `*time.Location` on the `Pipeline` (via `WithBudgetLocation` builder). If `LoadLocation` fails, fail loud at startup with a clear error naming the requested timezone (Constitution Principle III).

**Rationale**:
- Loading once and holding immutable for the pod's lifetime keeps the per-request path zero-allocation.
- Loud-fail on bad config makes any future failure operator-visible (vs. silently falling back to UTC, which would shift the daily boundary in confusing ways).

**⚠ Build requirement** (correction post-first-deploy): the runtime image is `FROM scratch` and ships no `/usr/share/zoneinfo`. Without an explicit `import _ "time/tzdata"` somewhere in the binary, `time.LoadLocation("America/Toronto")` returns `unknown time zone America/Toronto` and the pod fails startup. The correct setup:

```go
// cmd/server/main.go
import (
    // ... other imports ...
    _ "time/tzdata"  // embed the IANA timezone database into the binary
                     // so time.LoadLocation works in FROM-scratch images
                     // (no /usr/share/zoneinfo). Required for 011 FR-010.
)
```

Stdlib's `time` package does NOT include a fallback DB out of the box — the fallback ships in the separate `time/tzdata` package which must be blank-imported to take effect. The original draft of this research note incorrectly claimed otherwise; that was caught at first-deploy time when the pod crash-looped with the message above. The fix landed as commit `01c2dd6` ("[011 hot-fix] Embed time/tzdata so DAILY_BUDGET_TIMEZONE works in scratch image").

**Alternatives considered**:
- *Soft-fall to UTC on `LoadLocation` failure*: rejected. Constitution Principle III; the operator deserves a loud error, not a silent timezone shift.
- *Use a numeric offset like `-05:00` instead of an IANA name*: rejected. Doesn't handle DST correctly; in spring/fall the daily boundary would land at 1 AM or 11 PM local time half the year.
- *Ship `/usr/share/zoneinfo` in the runtime image* (e.g., switch base from `scratch` to `alpine`): rejected. The blank import is one line and ~700KB binary growth; switching base images would be a much larger trade-off (loses the scratch-image security/size benefit, complicates the Dockerfile, requires re-validating the existing 009 deploy contract).

---

## R5 — Snapshot file atomicity (resolves checklist open item)

**Decision**: Write to `path + ".tmp"`, call `(*os.File).Sync()` to flush kernel buffers, close the file, then `os.Rename` to the final path. Standard POSIX atomic-rename pattern.

**Rationale**:
- POSIX guarantees `rename(2)` is atomic within the same filesystem. Concurrent readers see either the old or the new file — never a partial one.
- The `.Sync()` call is conservative — without it, a kernel crash between rename and writeback could leave the renamed file with stale content. Adding a few ms of latency to the once-per-day rollover is a fine trade-off.
- No file-locking needed: the rollover writer is naturally serialized at this project's request volume; if true contention ever happens, two writers race the rename and one wins — both writes contained correct (possibly slightly different by microseconds) content, so the result is still correct.

**Alternatives considered**:
- *flock(2) on the snapshot file*: rejected. Adds complexity for a contention scenario that doesn't occur at this scale; harder to reason about in tests.
- *Compare-and-swap via etag-style version check*: rejected. Massively over-engineered for one-write-per-day on a single-replica deployment.
- *Skip the `Sync()`*: rejected. The kernel-crash window is small but real, and the cost is tiny.

---

## R6 — Snapshot corruption recovery: log INFO, reinitialize, continue

**Decision**: When `snapshotter.Load()` returns parse-error or the loaded data fails validation (e.g., `LocalDate` not a valid YYYY-MM-DD), `Pipeline.Build` treats it identically to "no snapshot found" — captures fresh state, persists, continues serving. The recovery is logged at INFO (not ERROR) per FR-007.

**Rationale**:
- Snapshot is a derived persistent cache, not a source of truth. The upstream counters are.
- The operator has no meaningful "fix" for a corrupted snapshot — they'd just delete it and let it regenerate, which is exactly what this code path does.
- Failing closed (return 503 until operator manually deletes the file) would over-protect a usability feature at the cost of breaking the user's connectivity.
- INFO (not ERROR) avoids alerting on a recoverable degradation; the log line still gives an audit trail if it matters later.

**Documented as a Constitution Principle III deviation** in plan.md's Complexity Tracking section.

**Alternatives considered**:
- *Loud-fail at startup if the snapshot is corrupted*: rejected per the rationale above.
- *Move corrupted snapshot to `path + ".corrupted-<ts>"` for forensic analysis*: deferred. Useful if corruption ever becomes a recurring problem in the field; trivial to add later. Not worth the complexity now.

---

## R7 — Two new env vars (one for path, one for timezone)

**Decision**: Add two env vars matching the project's existing one-env-per-knob pattern (`HONKAI_RULE_CLIENT_UA`, `FALLBACK_RULE_TARGET`, `URL_TEST_*`):

| Env var | Default | Type | Purpose |
|---|---|---|---|
| `TODAY_ZERO_PATH` | `/data/today-zero.json` | string | Snapshot file location on the PVC |
| `DAILY_BUDGET_TIMEZONE` | `America/Toronto` | string (IANA name) | Timezone for "today" / "tomorrow" computation |

**Rationale**:
- Forward-compat for operators in other timezones without code changes.
- Forward-compat for alternative volume layouts (a non-default PVC mount path).
- Matches the existing env-var idiom — operators can `kubectl describe deploy` to see all knobs in one place.
- Both have sensible defaults so the chart needs zero new env entries unless an override is desired.

**Validation**:
- `TODAY_ZERO_PATH`: parsed as-is (no path-format validation; absolute paths assumed).
- `DAILY_BUDGET_TIMEZONE`: validated at startup via `time.LoadLocation(...)`. Invalid value → loud-fail per Constitution Principle III, with a clear error naming the offending value.

**Alternatives considered**:
- *No env vars; hard-code the values*: rejected. Forward-compat matters even at single-operator scale; the cost of an env var is one Load() line.
- *One composite env var (JSON-shaped)*: rejected. Same reasoning as 012's R1 — splits into more failure modes than separate env vars provide.
