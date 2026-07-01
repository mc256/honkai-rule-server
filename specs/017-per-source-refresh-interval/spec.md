# Feature Specification: Per-Source Refresh Interval Column

**Feature Branch**: `017-per-source-refresh-interval`  
**Created**: 2026-07-01  
**Status**: Implemented  
**Input**: User description: "I want to have a column in config/subscriptions.csv that says
\"refresh\". This column sets the refresh interval of the source, if it is set to 0 or any
negative values, it never refreshes." — clarified in-thread to: 0 (or absent) means use the
default interval provided by the service, a positive value is the interval, and a negative
value means never refresh.

## Summary

Rename the existing optional `ttl_seconds` subscriptions-CSV column to `refresh` and widen its
semantics into a tri-state control of the per-source background refresh interval. This supersedes
the `ttl_seconds` optional column defined in 001 (FR-001a / FR-001b). The server-global default
env var `DEFAULT_TTL_SECONDS` is unchanged.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Operator tunes how often a source is re-fetched (Priority: P1)

An operator edits `config/subscriptions.csv` and sets the `refresh` column on a row to control
how frequently the server re-fetches that upstream, without touching the server-global default.

**Independent Test**: Load a CSV with `refresh` set to a positive value and assert the source's
background ticker uses that interval.

**Acceptance Scenarios**:

1. **Given** a row with `refresh` = `900`, **When** the server schedules that source, **Then**
   the source is re-fetched every 900 seconds.
2. **Given** a row with `refresh` = `0` or the column absent/empty, **When** the server schedules
   that source, **Then** the source is re-fetched on the server default interval
   (`DEFAULT_TTL_SECONDS`, default 3600s).

### User Story 2 - Operator pins a source that must never be refreshed (Priority: P1)

An operator has a source that should be fetched once at startup and then frozen (e.g. a snapshot
that must not drift). They set `refresh` to a negative value.

**Independent Test**: Load a CSV with `refresh` = `-1`, run the scheduler with a tiny default TTL,
and assert exactly one fetch occurs (the bootstrap fetch) and no further re-fetch.

**Acceptance Scenarios**:

1. **Given** a row with `refresh` = `-1`, **When** the server starts, **Then** the source
   bootstraps once and contributes to the merged config.
2. **Given** a never-refresh source, **When** time passes beyond any interval, **Then** the source
   is never re-fetched and its cached snapshot is never treated as stale.

## Requirements *(mandatory)*

- **FR-017a**: The subscriptions CSV schema MUST accept an optional column named `refresh`
  (integer seconds). It replaces the `ttl_seconds` optional column from 001; `ttl_seconds` is no
  longer a recognized column and (like any unknown column) causes loud schema failure at load.
- **FR-017b**: `refresh` is tri-state: `0` (or absent/empty) → use the server default interval;
  a positive value → refresh every that many seconds; a negative value → never refresh.
- **FR-017c**: A non-integer `refresh` value MUST cause a loud per-row validation error (unlike
  `ttl_seconds`, negative and zero values are now valid, not errors).
- **FR-017d**: A never-refresh source (`refresh` < 0) MUST still perform its bootstrap fetch so it
  contributes to the merged output, MUST NOT run a steady-state refresh ticker afterward, and while
  the process runs its cached snapshot MUST never be reported as stale.
- **FR-017e**: The `stale_on_error_seconds` optional column and the `DEFAULT_TTL_SECONDS` /
  `DEFAULT_STALE_ON_ERROR_SECONDS` server-global defaults are unchanged.
- **FR-017f**: The bootstrap fetch of a never-refresh source MUST use the server default interval
  for its cache-freshness decision (not the never-refresh sentinel), so that on process restart a
  disk-cached snapshot older than the default interval is re-fetched (picking up an edited `link`),
  while a still-fresh snapshot is reused to avoid re-hammering upstream. A never-refresh source whose
  bootstrap fetch fails has no ticker to self-heal and remains failed on `/health` until restart.

## Success Criteria *(mandatory)*

- **SC-1**: Existing configs that used no interval column keep their exact prior behavior
  (default interval); output bytes are unchanged.
- **SC-2**: A positive `refresh` value changes only that source's re-fetch cadence.
- **SC-3**: A negative `refresh` value causes exactly one fetch (bootstrap) over the source's
  lifetime and the source still appears in the served config.
