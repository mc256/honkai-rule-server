# Phase 1 Data Model: CI Container Builds, SemVer Releases via GHCR, Dependabot Auto-Patch

**Feature**: [spec.md](./spec.md)
**Plan**: [plan.md](./plan.md)
**Date**: 2026-05-07

This feature has no Go data model — it does not introduce new types, structs, or persistence. The "data" here is *operations data*: the relationships between git tags, GHCR tags, image labels, Make-target inputs, and workflow trigger conditions. This document captures those relationships authoritatively so the eventual workflow YAML / Makefile transcribes them without ambiguity.

---

## 1. Release-tag namespace (git side)

**Pattern**: `v<MAJOR>.<MINOR>.<PATCH>(-<PRERELEASE>)?`

**Regex**: `^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$`

**Examples**:

| Input | Valid? | Triggers release workflow? | Type |
|---|---|---|---|
| `v1.0.0` | yes | yes | release |
| `v0.0.1` | yes | yes | release |
| `v1.5.0-rc.1` | yes | yes | pre-release |
| `v1.5.0-beta.2` | yes | yes | pre-release |
| `v0.0.1-dev.abc123` | yes | yes | pre-release |
| `v10.20.30` | yes | yes | release |
| `1.5.0` | no (missing `v`) | no | — |
| `v1.5` | no (missing patch) | no | — |
| `v.1.5.0` | no (extra dot) | no | — |
| `v1.5.0_rc1` | no (underscore not in regex) | no | — |
| `v1.5.0+build.1` | no (build metadata not in scope for v1) | no | — |
| `demo-2026` | no (no `v` prefix, not numeric) | no | — |

**Lifecycle**:
- Annotated git tag at a clean checkout's `HEAD` commit.
- Created either by the `make release-*` helper (FR-012/FR-013a) or by the `auto-patch-release.yml` workflow (FR-018).
- Pushed to `origin` immediately on creation.
- Source of truth for the release version. Once pushed, NEVER deleted, NEVER moved (immutable convention; force-push to a tag is destructive and not part of any normal flow).

---

## 2. GHCR tag namespace

**Image base**: `ghcr.io/<github.repository_owner>/honkai-rule-server`

**Tag set per trigger**:

| Trigger | Tags published | Notes |
|---|---|---|
| Push to `master` (test passes) | `master`, `sha-<7short>` | `sha-*` is immutable; `master` advances |
| Push to `master` (test fails) | (none) | Build/push job depends on test job success |
| Pull request (any state) | (none) | Smoke `docker build` runs but no push |
| Tag push `vX.Y.Z` (no pre-release suffix) | `vX.Y.Z`, `vX.Y`, `vX`, `latest` | `latest` and `vX` advance only if highest non-pre-release in repo |
| Tag push `vX.Y.Z-PRERELEASE` | `vX.Y.Z-PRERELEASE` | Only the exact tag; no movers |
| Tag push not matching `v*.*.* ` regex | (none) | Workflow trigger filter `refs/tags/v[0-9]+.[0-9]+.[0-9]+*` ignores |

**Same-digest invariant**: For one tag-push event (or one master-push event), all published GHCR tags MUST point at the same image digest. Verifiable via:

```sh
docker manifest inspect ghcr.io/<owner>/honkai-rule-server:vX.Y.Z | jq -r '.config.digest'
docker manifest inspect ghcr.io/<owner>/honkai-rule-server:vX.Y     | jq -r '.config.digest'
docker manifest inspect ghcr.io/<owner>/honkai-rule-server:vX       | jq -r '.config.digest'
docker manifest inspect ghcr.io/<owner>/honkai-rule-server:latest   | jq -r '.config.digest'
# all four MUST print the same string
```

**Hotfix-vs-`latest` rule** (from FR-010 / SC-004):
- Pushing `v1.4.8` AFTER `v1.5.0` already exists → `v1.4.8` published, `v1.4` advances to `v1.4.8`'s digest, but `v1` and `latest` remain pointing at `v1.5.0`'s digest.
- Implemented by `docker/metadata-action`'s `flavor: latest=auto` and `type=semver,pattern={{major}}` patterns — both apply the precedence check internally.

---

## 3. Image label schema (OCI annotations)

Six labels stamped at build time via `docker/metadata-action`'s `labels:` input. Per FR-003:

| Label | Source for `master` push | Source for release tag push |
|---|---|---|
| `org.opencontainers.image.source` | `${{ github.server_url }}/${{ github.repository }}` | same |
| `org.opencontainers.image.revision` | `${{ github.sha }}` (full 40-char SHA) | same |
| `org.opencontainers.image.version` | `master` | tag name (e.g., `v1.4.8`) |
| `org.opencontainers.image.created` | `metadata-action` auto-fills with build start RFC 3339 | same |
| `org.opencontainers.image.title` | static `honkai-rule-server` | same |
| `org.opencontainers.image.licenses` | from repo `LICENSE` if present (SPDX) | same |

The `metadata-action` populates `created`, `revision`, `source`, and `version` automatically via its `type=semver` and event-context inputs; `title` and `licenses` are passed as explicit `labels:` lines.

**Verification**: `docker inspect ghcr.io/<owner>/honkai-rule-server:vX.Y.Z` returns these six labels under `.Config.Labels`.

---

## 4. Make-target input grammar

### `make release-patch` / `make release-minor` / `make release-major`

**Positional args**: none.
**Env-style overrides**:
- `DRY_RUN=1` — preview-only mode; no git side effects.
- `RELEASE_FROM_BRANCH=<branch>` — relax the "must be on master" check. Used for hotfix branches.

**Computed inputs**:
- `LAST` = `git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null` (most recent SemVer tag).
- `(M, m, p)` = SemVer components parsed from `LAST` (pre-release suffix stripped).

**Computed output**:
- `release-patch`: `NEXT = v${M}.${m}.$((p+1))`
- `release-minor`: `NEXT = v${M}.$((m+1)).0`
- `release-major`: `NEXT = v$((M+1)).0.0` if `LAST` non-empty, else `v1.0.0` (baseline).

### `make release VERSION=<v>`

**Positional args**: none.
**Required env-style argument**: `VERSION=vX.Y.Z[-PRERELEASE]`.
**Optional env-style overrides**: `DRY_RUN=1`, `RELEASE_FROM_BRANCH=<branch>` (same as above).

**Validation** (loud-fail):
- `VERSION` non-empty.
- `VERSION` matches `^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$`.
- Tag `$VERSION` does not yet exist locally (`git rev-parse "$VERSION" 2>/dev/null` returns non-zero).
- Tag `$VERSION` does not yet exist on origin (`git ls-remote --tags origin "refs/tags/$VERSION" | wc -l` returns 0).

---

## 5. Auto-patch-release decision table

Run by `auto-patch-release.yml` on its cron schedule (default `0 14 * * *`).

| Last release tag | Commits on master since last tag | Dependabot commits among them | Action |
|---|---|---|---|
| (none) | any | any | no-op (logged: "no prior release tag; run make release-major first") |
| `vX.Y.Z` | 0 | 0 | no-op (logged: "no commits since vX.Y.Z; skipping") |
| `vX.Y.Z` | ≥1 | 0 | no-op (logged: "no dependabot commits since vX.Y.Z; skipping — feature commits go through make release-*") |
| `vX.Y.Z` | ≥1 | ≥1 | cut `vX.Y.(Z+1)` (annotated tag), push to origin, release workflow picks it up via FR-008 trigger |

**Tie-breakers**:
- If `auto-patch-release.yml` runs while a maintainer's `make release-patch` is in flight, both compute the same `NEXT`. Whichever pushes first wins; loser's `git push` is rejected (FR's edge case "Two release-tag pushes race").
- If `auto-patch-release.yml` runs and a maintainer has just merged a feature commit (no Dependabot commit) but plans to cut `make release-minor` later that day, the workflow's no-op path leaves their release window open.

---

## 6. Dependabot configuration shape

`.github/dependabot.yml` (full schema at https://docs.github.com/code-security/dependabot/dependabot-version-updates/configuration-options-for-the-dependabot.yml-file). For this feature:

```yaml
version: 2
updates:
  - package-ecosystem: <one of: gomod | github-actions | docker>
    directory: "/"
    schedule:
      interval: "weekly"
    open-pull-requests-limit: 5
    groups:
      all-non-major:
        update-types: ["minor", "patch"]
```

Three blocks (one per ecosystem). All identical apart from `package-ecosystem`.

**Field semantics** (only the fields this feature uses):

| Field | Value | Meaning |
|---|---|---|
| `package-ecosystem` | `gomod` / `github-actions` / `docker` | which dep tree to scan |
| `directory` | `/` | root of the repo |
| `schedule.interval` | `weekly` | once per week (Mondays 06:00 UTC by default) |
| `open-pull-requests-limit` | `5` | sanity backstop on PR floods |
| `groups.all-non-major.update-types` | `["minor", "patch"]` | non-major bumps batch into one PR per ecosystem per week; major bumps open as separate PRs |

---

## 7. Auto-merge + auto-patch flow state machine

States a Dependabot PR transits through on the happy path:

```
[OPENED]
   ↓  (CI workflow runs against PR HEAD)
[CI_RUNNING]
   ↓  (CI passes; dependabot-auto-merge.yml + fetch-metadata classify update-type)
[CI_PASSED]
   ↓  (if update-type ∈ {minor, patch})
[AUTO_MERGE_QUEUED]   ← gh pr merge --auto --squash queues the merge
   ↓  (required checks confirmed; no review required)
[MERGED_TO_MASTER]   ← squash commit on master, author = dependabot[bot]
   ↓  (publish-master job in ci.yml runs)
[MASTER_IMAGE_PUBLISHED]  ← :master and :sha-<short> tags advance
   ↓  (auto-patch-release.yml runs on next 14:00 UTC tick)
[PATCH_TAG_PUSHED]   ← vX.Y.(Z+1) annotated tag pushed
   ↓  (release.yml triggers on tag push)
[RELEASE_IMAGE_PUBLISHED]  ← vX.Y.Z, vX.Y, vX, latest tags advance on GHCR
```

States on the unhappy path:

```
[OPENED]
   ↓
[CI_RUNNING]
   ↓  (CI fails)
[CI_FAILED] ← stays open; no auto-merge; awaits human attention
```

```
[OPENED]
   ↓
[CI_PASSED]
   ↓  (update-type == 'major'; fetch-metadata reports it)
[MAJOR_BUMP_HELD] ← stays open; no auto-merge; awaits human review per FR-016 + FR-017
```

```
[MASTER_IMAGE_PUBLISHED]
   ↓  (auto-patch-release.yml runs but no dependabot commits since last tag — e.g., human merged a feature first)
[NO_OP_LOGGED] ← workflow logs reason and exits 0; no tag created
```

---

## 8. Constitution principle alignment

This data model does not enumerate the constitution's transformation-core types (those are unaffected). It enumerates *operations data* — the rules-of-the-game between git, GHCR, and workflow triggers. Constitution principles apply as follows:

- **Principle II (Determinism)**: The same-digest invariant in §2 + reproducible build flags ensure two runs of the workflow against the same SHA produce identical layer digests. Operations-side determinism mirrors the transformation-side determinism.
- **Principle III (Loud Failure)**: All Make-target validations in §4 fail loudly with named-input error messages. Auto-patch-release in §5 logs its no-op reason. Auto-merge in §7 logs its skip-on-major reason via `fetch-metadata`.
- **Principle V (Observability)**: GitHub Actions natively logs every step; this feature relies on the platform's built-in observability rather than building its own. The `RELEASING.md` doc points operators at the run URL.
- **Routing & Security — Secrets boundary**: Only credential in play is `${{ secrets.GITHUB_TOKEN }}`, scoped per-job. No secret persistence in image labels or workflow outputs.

This data model is referenced by `contracts/ghcr-tag-contract.md` and `contracts/make-targets-contract.md` (Phase 1 contracts).
