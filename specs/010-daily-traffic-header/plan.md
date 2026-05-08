# Implementation Plan: Daily-Available Traffic in Served Subscription Header

**Branch**: `010-daily-traffic-header` | **Date**: 2026-05-01 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/010-daily-traffic-header/spec.md`

## Summary

Replace the values carried by the served `Subscription-Userinfo` HTTP response header with a daily-spendable encoding while preserving the wire format. The remaining-bytes figure (`total − upload − download`) becomes the **daily allowance** already computed by `merge.ComputeDailyAllowance` (per 001 FR-011b: per-day-rate sum over expiring sources + no-expiry remaining sum). The `expire` field becomes the next 00:00 UTC strictly after the current request time. The math is unchanged; only the output adapter's header values change. Raw aggregates (sum-of-remaining + earliest non-zero expire) remain on the `/health` surface for operator debugging — no fields are removed there.

Implementation lands as one new pre-computed field on `merge.MergedConfig` (a `*ServedTrafficHeader{DailyAllowanceBytes, ExpireUnix}` populated by `Pipeline.Build()` using the existing injected clock), an updated `internal/output/subscription_mode.go::Render` reading that field, the omit-when-no-userinfo case (which the current code does not honor), and a couple of new log fields per FR-008. Snapshot fixtures get one deliberate update — the served `Subscription-Userinfo` header bytes change; the body bytes do not.

## Technical Context

**Language/Version**: Go 1.25 toolchain (declared 1.22+) — unchanged
**Primary Dependencies**: existing — no new Go deps; no new tools
**Storage**: N/A — no persistent change. The header is per-request and stateless.
**Testing**: existing — `go test`, `bradleyjkemp/cupaloy/v2` snapshots; tests stay fixture-driven with a fixed clock injected for determinism
**Target Platform**: same as the rest of the server (Linux, k8s) — no platform-specific bits
**Project Type**: Single Go module
**Performance Goals**: header construction is O(sources) integer arithmetic — well below any hot-path budget; no new performance considerations
**Constraints**: must be deterministic given fixed inputs **and** fixed request time (FR-005); the served `Subscription-Userinfo` header value within the same UTC calendar day MUST be byte-identical for the same input snapshot (SC-006)
**Scale/Scope**: same handful of upstream subscriptions as today (≤10s); no fan-out

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Justification |
|-----------|--------|---------------|
| **I. Unified Transformation Core** | PASS | The change touches the subscription-mode adapter only (override mode adapter does not yet exist). Crucially, the daily-allowance figure is precomputed on `MergedConfig` rather than inside `subscription_mode.go::Render`, so when override-mode lands it consumes the same field — no forked classifier, no mode-specific arithmetic. |
| **II. Deterministic Transformation** | PASS | The figure is a pure function of (per-source userinfo, request time). The request time is sourced from the existing `clock.Clock` interface that already feeds `ComputeDailyAllowance`; tests inject a fixed clock. The "tomorrow" rule (next 00:00 UTC strictly after now) is single-valued for every input. Snapshot tests assert byte-stability against fixed-clock fixtures. |
| **III. CSV Rules** | PASS — N/A | Feature does not touch routing-rule loading. The CSV schema, validators, and loud-fail semantics are untouched. |
| **IV. Test-First, Real-Input Integration (NON-NEGOTIABLE)** | PASS | Unit tests for the new "next-midnight-UTC" helper; output-layer table tests for the new header values across the canonical FR-011b fixture, the no-expiry case, the all-expired case, the no-userinfo-at-all case, and the deterministic-within-day case; integration test in `internal/integration/headers_test.go` asserting the served header against a multi-source fixture. Snapshot suites for both subscription mode and (when override mode arrives) the override mode are updated together — this PR ships the subscription-mode snapshot diff only because override mode does not yet exist. |
| **V. Observable Routing & Source-Merge Decisions** | PASS — extended | FR-008 adds two new structured log fields per served-subscription request: `served_daily_allowance_bytes` (int64) and `served_expire_unix` (int64). Per-source contributions to the daily allowance are loggable at debug verbosity. No new credential surface. |
| **Routing — Corporate isolation** | PASS — N/A | No routing change. |
| **Routing — multi-subscription collision resolution** | PASS — N/A | No collision-resolution change. |
| **Routing — fetch failure modes** | PASS — preserved | The bootstrap gate at `subscription.go:38–52` still applies; this feature does not change when the server returns 503 vs. 200. The header omission rule (FR-006) is purely about a 200 response that has no userinfo to advertise — it is *not* a fail-closed boundary. |
| **Security — Secrets boundary** | PASS — N/A | No new credential or token handling. |
| **Security — Sanitized output** | PASS — preserved | Header values are integers derived from already-sanitized aggregates. No upstream URL or credential leaks. |
| **Security — CSV is reviewable, not secret** | PASS — N/A | No CSV change. |
| **Snapshot stability gate** | PASS | Snapshot diffs are deliberate and isolated to the served header bytes (subscription-mode); body bytes do not change. PR description will state the intended diff. |
| **Diff-reviewable changes** | PASS | One PR. Files affected listed in Project Structure below. |
| **Both modes covered, every change** | PASS — scope-limited | Override mode's adapter does not yet exist in the repo; the precomputed `MergedConfig` field is mode-agnostic so when override mode arrives it inherits the same figure without further work. |
| **Simplicity bias** | PASS | One pre-computed pointer field on `MergedConfig`, one ~5-line helper for "next-midnight-UTC", one branch in the output adapter. No new packages, no abstractions, no plugin layer. |

### Complexity Tracking

No violations to track. The plan follows the existing precompute-on-MergedConfig + thin-output-adapter pattern.

## Project Structure

### Documentation (this feature)

```text
specs/010-daily-traffic-header/
├── plan.md              # This file
├── research.md          # Phase 0 — design decisions for the header encoding & "tomorrow" rule
├── data-model.md        # Phase 1 — MergedConfig field shape + ServedTrafficHeader type
├── contracts/
│   └── served-subscription.changes.md  # Phase 1 — header semantics change vs. 001 FR-011
├── quickstart.md        # Phase 1 — operator-facing verification + troubleshooting
├── checklists/
│   └── requirements.md  # already created by /speckit-specify
└── tasks.md             # Phase 2 output (/speckit-tasks — NOT created here)
```

### Source Code (repository root)

```text
honkai-rule-server/
├── internal/
│   ├── merge/
│   │   ├── pipeline.go         # MODIFY — add ServedTrafficHeader field; populate in Build()
│   │   ├── traffic.go          # MODIFY — add NextMidnightUTC helper + ServedTrafficHeader type;
│   │   │                       #          add a thin "compose served figures" helper that returns
│   │   │                       #          *ServedTrafficHeader (nil when no source contributed)
│   │   └── traffic_test.go     # MODIFY — unit tests for NextMidnightUTC + composer
│   ├── output/
│   │   ├── subscription_mode.go      # MODIFY — read ServedTrafficHeader; omit header when nil
│   │   └── subscription_mode_test.go # MODIFY — table-test all FR-001..FR-007 cases
│   ├── server/routes/
│   │   └── subscription.go     # MODIFY — add the two new log fields per FR-008
│   └── integration/
│       ├── headers_test.go     # MODIFY — assert served header values against fixture
│       └── testdata/snapshots/ # MODIFY — refresh subscription-mode snapshots that include the header
└── specs/010-daily-traffic-header/  # documentation tree above
```

**Structure Decision**: Single project, no new packages. The change is small and lands inside the existing layering: pure helpers + types in `internal/merge/`, behavior in `internal/output/`, structured logging in the route handler. No new contracts file is generated; the served-subscription contract is amended via a `contracts/served-subscription.changes.md` delta document (matching the 002 / 008 precedent in this repo).

## Phase 0: Outline & Research

The spec leaves no `[NEEDS CLARIFICATION]` markers. The Phase 0 deliverable documents six narrow design decisions:

1. **Encoding split between `upload`/`download`/`total`**: choose `upload=0; download=0; total=daily_allowance_bytes`. Rationale: the simplest encoding that satisfies FR-003's invariant; matches the user's phrasing ("daily available traffic" reads cleanly as "you have N bytes total, none used yet today"). Rejected: keeping aggregated historical upload/download (visually preserves cumulative usage but doesn't reset daily, so the displayed "used" figure would be misleading in a daily-budget UX).

2. **"Tomorrow" computation**: `time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day()+1, 0, 0, 0, 0, time.UTC).Unix()`. Rationale: deterministic, single-valued for any request time, no DST/leap-second hazard (UTC), aligns with the day-boundary semantic SC-006 requires. Rejected: `now + 86400` (would make `expire` advance second-by-second, breaking SC-006's "same UTC calendar day → identical bytes"); local-timezone midnight (introduces operator config, no value today).

3. **Where to compute**: precompute in `Pipeline.Build()` and stash on `MergedConfig`. Rationale: matches the existing pattern (`AggregatedSubscriptionUserinfo`, `AggregatedProfileUpdateIntervalHours`); keeps `output/subscription_mode.go::Render` a pure renderer; future override-mode adapter inherits the same field. Rejected: compute inside the output adapter (would couple the adapter to the clock and the per-source userinfo map, which it does not currently see).

4. **Header omission rule**: omit `Subscription-Userinfo` entirely when no source contributed userinfo (every source's `Subscription-Userinfo` was missing or unparseable per 001 FR-012). Rationale: explicit FR-006 + matches the "no fake-zero" rule already in 001 FR-012. Implementation: `*ServedTrafficHeader` is nil when no source contributed; output adapter's existing nil-check pattern handles it. Note: the current code's nil-check at `subscription_mode.go:137` is structurally there but never trips because `AggregateSubscriptionUserinfo` returns a non-nil zero struct; the new field uses a tighter "any source contributed?" predicate.

5. **Per-source debug logging**: emit at debug verbosity from the route handler after rendering — `slog.Debug("served daily allowance breakdown", "per_day_rate_bytes", X, "no_expiry_remaining_bytes", Y, "expired_source_flags", […])`. Rationale: keeps the merge layer free of logging side effects (Constitution Principle II's "pure transform" boundary); the route handler already has access to both `MergedConfig` and the recomputed daily-allowance breakdown via `Pipeline.ComputeDailyAllowance()`. Rejected: logging from inside `ComputeDailyAllowance` (introduces a logger dependency in a pure function).

6. **Test fixture for SC-001's 21 GB/day case**: build a small fixture-driven table test in `internal/output/subscription_mode_test.go` with two `fetcher.SubscriptionUserinfo` records matching the 001 FR-011b worked example, fixed clock, assert `total − upload − download = 21*1024^3` ± 1 byte (rounding tolerance). Rationale: ties the served header directly to the constitution-anchored math.

**Output**: `research.md` documenting the six decisions with rationale + rejected alternatives.

## Phase 1: Design & Contracts

**Prerequisites**: `research.md` complete

### Data Model

`data-model.md` covers:

- **`merge.ServedTrafficHeader`** (new type in `internal/merge/traffic.go`):
  ```go
  type ServedTrafficHeader struct {
      DailyAllowanceBytes int64 // = total - upload - download
      ExpireUnix          int64 // = next 00:00 UTC strictly after now
  }
  ```
  Pointer-typed when stored (nil = "no source contributed userinfo, omit the header").

- **`merge.MergedConfig`** (new field):
  ```go
  ServedTrafficHeader *ServedTrafficHeader
  ```
  Populated in `Pipeline.Build()` after the existing `aggregatedUI := AggregateSubscriptionUserinfo(...)` step. Non-nil when at least one source contributed `Subscription-Userinfo`; nil otherwise.

- **`merge.NextMidnightUTC(now time.Time) time.Time`** (new pure helper):
  Returns the next 00:00 UTC strictly after `now`. Defined as `time.Date(y, m, d+1, 0, 0, 0, 0, time.UTC)` where `(y, m, d)` are taken from `now.UTC()`.

- **`merge.ComposeServedTrafficHeader(perSource map[string]fetcher.SubscriptionUserinfo, clk clock.Clock) *ServedTrafficHeader`** (new pure helper):
  Returns nil when `len(perSource) == 0`. Otherwise computes `da := ComputeDailyAllowance(perSource, clk)` and returns `&ServedTrafficHeader{DailyAllowanceBytes: da.PerDayRateBytes + da.NoExpiryRemainingBytes, ExpireUnix: NextMidnightUTC(clk.Now()).Unix()}`. (`da.ExpiredSourceFlags` is informational only on the health surface — it does not contribute to the served figure, which is FR-007's "exactly 0" case when all sources are expired.)

### Contracts

`contracts/served-subscription.changes.md` covers:

- **Wire format unchanged**: the response header remains `Subscription-Userinfo: upload=<bytes>; download=<bytes>; total=<bytes>; expire=<unix_seconds>` (integers, semicolon-space separated), per 001 FR-011 + 001 FR-005b.
- **Semantic change vs. 001 FR-011**: the values inside that wire format are no longer the per-source-summed raw aggregates; they're the daily-spendable encoding defined by FR-001 + FR-002 of this feature.
- **Omission rule**: when no source contributed userinfo, the response carries no `Subscription-Userinfo` header. Previous behavior (which de-facto emitted `upload=0; download=0; total=0; expire=0` because `AggregateSubscriptionUserinfo` returned a non-nil zero struct) is removed.
- **`Profile-Update-Interval`**: unchanged.
- **`Content-Type` / `Cache-Control`**: unchanged.
- **Health-surface contract**: unchanged. The `/health` JSON continues to expose both the raw aggregated `Subscription-Userinfo` figures and the three-component `DailyAllowance` from 001 FR-011b. Operators reading `/health` see the same JSON they see today; no removed fields.

### Quickstart

`quickstart.md` covers (operator-facing):

1. **Verify the served header**: `curl -fsS -A "Bronya/1.0" -D /tmp/h.txt -o /tmp/body.yaml "https://example.com/<prefix>/?token=<TOKEN>"` then `grep -i subscription-userinfo /tmp/h.txt`. Expected: `upload=0; download=0; total=<daily_allowance_in_bytes>; expire=<next_midnight_utc_unix>`.
2. **Confirm the math against the canonical fixture**: walk the FR-011b worked example (5 GB/day + 16 GB/day = 21 GB/day) and show the equivalent header.
3. **Cross-check via /health**: `curl … /health` shows `dailyAllowance.perDayRateBytes`, `dailyAllowance.noExpiryRemainingBytes`, `dailyAllowance.expiredSourceFlags` (operator visibility) plus the raw aggregates (debugging).
4. **Edge cases the operator should know about**: when does the header omit? when does `total` read 0?
5. **Troubleshoot mismatched figures**: header says one thing, /health says another → check which clock (each is recomputed per request from current time; identical input snapshot may produce different `expire` across the day boundary).

### Agent context update

Update the lines between `<!-- SPECKIT START -->` and `<!-- SPECKIT END -->` in `CLAUDE.md`:

- Mark **008 (dialer-proxy-fanout)** as fully implemented (it is — the recent merge commit confirms it).
- Mark **009 (cluster-deploy)** as fully implemented (it is — the live cluster ConfigMap was updated this session and the deployment serves a 200).
- Add **010 (daily-traffic-header)** as the active feature, with a one-line summary pointing at this plan.
- Add a key-reading bullet pointing at `specs/010-daily-traffic-header/plan.md`.

## Phases (after this command)

This command stops here. Next: `/speckit-tasks` produces `tasks.md` with the dependency-ordered task list (unit-test-first per Constitution Principle IV, then implementation, then snapshot refresh, then integration test, then logging, then doc updates).
