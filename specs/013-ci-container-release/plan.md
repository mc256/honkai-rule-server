# Implementation Plan: CI Container Builds, SemVer Releases via GHCR, Dependabot Auto-Patch

**Branch**: `013-ci-container-release` | **Date**: 2026-05-07 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/013-ci-container-release/spec.md`

## Summary

This feature is meta-infrastructure: it does not touch the Go transformation core, the served subscription body, or any routing logic. Three workflow files and one config file land in `.github/`, plus four new Make targets in `Makefile`, plus a new `RELEASING.md`. The `.github/workflows/ci.yml` already in place is extended with a `publish-master` job; a new `release.yml` triggers on `v*` tag pushes and publishes versioned images to `ghcr.io/<owner>/honkai-rule-server`; `.github/dependabot.yml` enables three ecosystems (gomod, github-actions, docker); `dependabot-auto-merge.yml` auto-merges non-major Dependabot PRs after CI passes; `auto-patch-release.yml` is a daily cron that detects merged Dependabot commits since the last release tag and creates the next patch tag, triggering the release workflow.

The Makefile's existing `IMAGE_REPO` default flips from `registry.example.com/library/honkai-rule-server` to `ghcr.io/<owner>/honkai-rule-server` (with `<owner>` derived from `git remote get-url origin`). Three new `release-{patch,minor,major}` targets parse the most recent `vX.Y.Z` tag, validate clean tree + branch, create an annotated tag, and `git push` it. A fourth target `release VERSION=` accepts an explicit version for RC tags / hotfix backports / version-skip cases.

Determinism applies to the image build (FR-015 in spec): `Dockerfile`'s existing `-trimpath -ldflags="-s -w"` flags + pinned `golang:1.25-alpine` base produce reproducible layer digests across re-runs of the same SHA. The non-essential `created` timestamp differs but does not affect the layer content digest.

## Technical Context

**Language/Version**: Workflows are GitHub Actions YAML; Make targets are POSIX shell within the existing Makefile; Dependabot config is YAML. No Go code is added.
**Primary Dependencies**: `docker/login-action@v3`, `docker/setup-buildx-action@v3`, `docker/build-push-action@v5`, `docker/metadata-action@v5`, `softprops/action-gh-release@v2`, `dependabot/fetch-metadata@v2`. All standard, all GHCR-friendly. (Specific `@vN` pins set during implementation; subsequently tracked by Dependabot's `github-actions` ecosystem.)
**Storage**: N/A — no persistence on this feature. Annotated git tags ARE the persistent artifact and live in the git server's tag namespace.
**Testing**: No new Go tests. The release workflow re-runs the existing `go vet`, `staticcheck`, `go test -race`, and `git diff --exit-code` (snapshot drift) gate against the tagged commit. New: a small shell-script test for the Make-target tag-bump logic (Phase 0 §3) verifying `v1.4.7` → `v1.4.8` / `v1.5.0` / `v2.0.0` transitions, plus a regex-validation test for the explicit-version path (`v2.0.0` accepted, `2.0.0` / `v2` / `v.2.0.0` rejected).
**Target Platform**: GitHub Actions (`ubuntu-latest` runners), GHCR (`ghcr.io`). Image platform: `linux/amd64` only for v1, matching cluster (FR-014).
**Project Type**: Operations / build infrastructure on top of an existing Go module.
**Performance Goals**: End-to-end release (tag push → image on GHCR) under 10 minutes (test ~3 min + build ~3 min + push ~1 min + overhead). Master push → `:master` image under 10 minutes from same gate. Dependabot PR merge → patch release within 24 hours (gated by daily cron).
**Constraints**: Loud-fail at the Make-target boundary on dirty tree, wrong branch, malformed `VERSION`, or missing prior tag (Constitution Principle III applied to operator UX). No `GITHUB_TOKEN` or registry credential ever echoed to logs (Routing & Security — Secrets boundary).
**Scale/Scope**: Three ecosystems × ~5 PRs/week max under group-rule = ≤15 Dependabot PRs/week realistic upper bound. Release tags are operator-paced and Dependabot-paced; expect 1–5 patch releases/week + rare minor/major from feature work.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Justification |
|-----------|--------|---------------|
| **I. Unified Transformation Core** | PASS — N/A | This feature does not touch the transformation pipeline. Both delivery modes (subscription, future override) get the same image because the image is the binary, not a per-mode artifact. |
| **II. Deterministic Transformation** | PASS — extended | The principle binds the transform; this feature extends a determinism-equivalent guarantee to the container image (FR-015): two runs against the same SHA produce identical layer digests via the existing `-trimpath -ldflags="-s -w"` flags. The non-essential `created` timestamp on the image manifest is the only divergent byte. |
| **III. CSV Rules — Strict Schema, Loud Failure** | PASS — applied to operator UX | Make targets fail loudly on: dirty tree, wrong branch, malformed `VERSION`, no prior tag for `release-patch`/`release-minor`, tag-already-exists. No silent best-effort. The `dependabot-auto-merge.yml` workflow loud-skips on major bumps via `dependabot/fetch-metadata`. The `auto-patch-release.yml` workflow loud-skips with a logged reason on zero Dependabot commits since last tag. |
| **IV. Test-First, Real-Input Integration (NON-NEGOTIABLE)** | PASS — scope-limited | The release workflow runs the existing test suite (which already enforces test-first for the transformation core) as a gate before any image is built. New shell logic in the Make targets is small (≤30 lines per target) but gets a focused shell-script test (Phase 0 §3 of research) covering the tag-bump arithmetic and the version-regex validator. No transformation-core tests are added or removed. |
| **V. Observable Routing & Source-Merge Decisions** | PASS — N/A | No routing or merge decisions in this feature. Workflow observability is delegated to GitHub Actions' native logs + the `RELEASING.md` doc that points operators at the run URL. Make targets print the next-tag / GHCR-URL on completion. |
| **Routing — Corporate isolation** | PASS — N/A | No routing-rule change. |
| **Routing — multi-subscription collision resolution** | PASS — N/A | No collision-resolution change. |
| **Routing — fetch failure modes** | PASS — N/A | No fetch-layer change. |
| **Security — Secrets boundary** | PASS — strict | All GHCR auth uses `${{ secrets.GITHUB_TOKEN }}` scoped with `packages: write` at the job level. No PAT, no static credential. The auto-merge workflow uses the same token (with `pull-requests: write`). The `RELEASING.md` doc explicitly tells operators: "no credential setup needed; the workflow's auto-injected `GITHUB_TOKEN` does it all." Make targets that call `git push origin <tag>` rely on the operator's existing local SSH/HTTPS credentials — no new credential surface. |
| **Security — Sanitized output** | PASS — strict | Image labels carry only public, non-sensitive data (commit SHA, repo URL, version tag, RFC-3339 build timestamp). No subscription URL, exit-proxy credential, or third-party token enters the image build context. |
| **Security — CSV is reviewable, not secret** | PASS — N/A | No CSV change. |
| **Snapshot stability gate** | PASS | Snapshot tests run unchanged in the release workflow. Snapshot drift fails the release before image push. |
| **Diff-reviewable changes** | PASS | This feature lands as one PR with seven new/modified files (4 workflow YAMLs + 1 Dependabot YAML + Makefile + RELEASING.md), each diffable in isolation. |
| **Both modes covered, every change** | PASS — N/A | No transformation-core change; both modes (subscription, future override) inherit the same binary from the same image build. |
| **Simplicity bias** | PASS | No new abstractions, no new packages, no new languages. Workflow YAML is declarative; Make-target shell is direct (no template generation, no script-of-scripts). The most complex pieces are the moving-tag-precedence detection (~10-line `git tag --sort=-v:refname` invocation) and the version-bump shell (`awk -F.` arithmetic). Both are small enough to read in one screen. |

### Complexity Tracking

No violations. The plan adds no abstractions, no new languages, no new packages. All seven affected files are touched at the smallest scope necessary to satisfy the spec's FRs.

## Project Structure

### Documentation (this feature)

```text
specs/013-ci-container-release/
├── plan.md                                    # This file
├── research.md                                # Phase 0 — design decisions
├── data-model.md                              # Phase 1 — tag precedence rules, label schema
├── contracts/
│   ├── ghcr-tag-contract.md                   # Phase 1 — which tags exist for which trigger
│   └── make-targets-contract.md               # Phase 1 — Make target inputs/outputs/exit codes
├── quickstart.md                              # Phase 1 — operator: cutting a release + verifying + disabling Dependabot
├── checklists/
│   └── requirements.md                        # already created by /speckit-specify
└── tasks.md                                   # Phase 2 output (/speckit-tasks — NOT created here)
```

### Source Code (repository root)

```text
honkai-rule-server/
├── .github/
│   ├── workflows/
│   │   ├── ci.yml                             # MODIFY — add publish-master job (depends on existing test job)
│   │   ├── release.yml                        # CREATE — tag push v* → test → build → multi-tag GHCR push + GitHub Release
│   │   ├── dependabot-auto-merge.yml          # CREATE — auto-merge non-major Dependabot PRs after CI passes
│   │   └── auto-patch-release.yml             # CREATE — daily cron; if Dependabot commits since last tag, create vX.Y.(Z+1)
│   └── dependabot.yml                         # CREATE — gomod + github-actions + docker ecosystems, weekly, grouped non-major
├── .env.example                               # MODIFY — IMAGE_REPO comment update; new RELEASE_FROM_BRANCH note
├── Makefile                                   # MODIFY — flip IMAGE_REPO default; add release-{patch,minor,major}, release VERSION=, DRY_RUN=1; extend help
├── RELEASING.md                               # CREATE — operator-facing release doc
├── CLAUDE.md                                  # MODIFY — mark 012 fully implemented; add 013 active-feature line
└── specs/013-ci-container-release/            # documentation tree above
```

**Structure Decision**: Single project, no new code packages. All changes are in `.github/`, root-level config files, and one new doc. No Go source files are touched.

## Phase 0: Outline & Research

The spec leaves no `[NEEDS CLARIFICATION]` markers. Phase 0 documents nine narrow design decisions:

1. **Action choice for build/push**: Use the official `docker/*` actions (`login-action@v3`, `setup-buildx-action@v3`, `metadata-action@v5`, `build-push-action@v5`) plus `softprops/action-gh-release@v2` for the GitHub Release entry, plus `dependabot/fetch-metadata@v2` for the auto-merge guard. Rejected alternatives: `goreleaser` (overkill — handles tag→build→release as one config-file pipeline, but pulls a binary toolchain and a non-trivial config; the docker/* + Make split is simpler and lets the Make helpers stay platform-agnostic). Custom shell scripts pushing to GHCR (rejected — `docker/build-push-action` handles cache, multi-tag, multi-arch, attestation hooks for free).

2. **`latest` precedence detection**: After a tag push, the release workflow runs `git tag --sort=-v:refname --list 'v[0-9]*.[0-9]*.[0-9]*' | grep -v -- '-' | head -1` to find the highest-precedence non-pre-release tag in the repo. If that tag equals the just-pushed tag, the workflow advances `latest`; otherwise it does not. Same logic for `vMAJOR` (filter by major prefix before head -1). This handles hotfix backports correctly (FR-010 / SC-004).

3. **Make-target tag-bump shell**: Use POSIX shell arithmetic + `awk`-style field-splitting on the most-recent tag. Reference shell:

   ```sh
   LAST=$$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null || echo "")
   M=$$(echo $$LAST | sed 's/^v//' | cut -d. -f1)
   m=$$(echo $$LAST | sed 's/^v//' | cut -d. -f2)
   p=$$(echo $$LAST | sed 's/^v//' | cut -d. -f3 | cut -d- -f1)
   # then: NEXT=v$${M}.$${m}.$$((p+1))  for release-patch
   ```

   The full target body wraps this with: dirty-tree check, branch check, `DRY_RUN` guard, `git tag -a -m "Release $$NEXT" $$NEXT`, `git push origin $$NEXT`. Phase 0's research.md captures the full reference implementation.

4. **Version-regex validation for explicit path**: `make release VERSION=` validates with `echo "$VERSION" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$'`. Loud-fail on mismatch with a message naming the offending input and the expected regex. Rejected alternatives: accepting `vX.Y` or `vX` (no — pre-release semantics get muddy and FR-008 already specifies the trigger pattern is exactly the three-component form).

5. **Dependabot grouping**: One group per ecosystem named `all-non-major` matching `update-types: ["minor", "patch"]`. Major bumps fall outside the group → one PR per major bump, untouched by auto-merge (FR-016 + FR-017's skip-on-major guard double-locks this). Rationale: minor + patch are the routine "drip" updates Dependabot is for; major bumps deserve human review per project safety norm.

6. **Auto-merge safety on major bumps**: `dependabot-auto-merge.yml` runs `dependabot/fetch-metadata@v2` and gates the merge step on `update-type == 'version-update:semver-minor' || update-type == 'version-update:semver-patch'`. Major bumps → no merge → PR stays open. Rationale: FR-017 enforces this; the `fetch-metadata` action is the official path.

7. **Auto-patch-release commit-author filter**: `git log <last-tag>..HEAD --author='dependabot\[bot\]' --pretty=format:'%H' | wc -l` returns the count of Dependabot commits. Non-zero → cut the patch tag. Zero → log "no dependabot commits since <last-tag>; skipping" and exit 0. Rationale: avoid polluting `git log` parsing with regex-on-message; author filter is unambiguous.

8. **GHCR visibility**: Public on first push (no workflow control needed; GitHub defaults new packages in public repos to inheriting repo visibility, which is public). Rationale: open-source repo + public registry = no operator action. Private toggle is a one-click GitHub UI change later.

9. **Schedule choice**:
   - Dependabot: weekly (Mondays 06:00 UTC, GitHub default). Rationale: weekly batches give one PR per ecosystem per week with grouped non-major bumps — review cadence matches the maintainer's cadence.
   - Auto-patch-release: daily at 14:00 UTC (≈ morning America/Toronto, matching feature 011's local-day boundary). Rationale: once-per-day is the right cadence for "release Dependabot accumulations promptly without spamming". Configurable in the workflow file.

**Output**: `research.md` documenting all nine decisions with rationale + rejected alternatives + reference shell snippets.

## Phase 1: Design & Contracts

**Prerequisites**: `research.md` complete

### Data Model

`data-model.md` covers (no Go types — this is operations data):

- **Release-tag namespace**: pattern `v<MAJOR>.<MINOR>.<PATCH>(-<PRERELEASE>)?`. Three immutable per-release tags (`vX.Y.Z`) + three moving tags (`vX.Y`, `vX`, `latest`) on the GHCR side. The mapping {git tag → set of GHCR tags} is the authoritative data model for this feature.

- **Image label schema** (per FR-003): six OCI annotation labels stamped at build time. Source values:
  | Label | Value at master push | Value at release tag |
  |---|---|---|
  | `org.opencontainers.image.source` | `https://github.com/${{ github.repository }}` | same |
  | `org.opencontainers.image.revision` | `${{ github.sha }}` (full SHA) | same |
  | `org.opencontainers.image.version` | `master` | tag (e.g., `v1.4.8`) |
  | `org.opencontainers.image.created` | RFC 3339 build start time | same |
  | `org.opencontainers.image.title` | `honkai-rule-server` | same |
  | `org.opencontainers.image.licenses` | from repo `LICENSE` (if present) | same |

- **Make-target input grammar**:
  - `make release-{patch,minor,major}` accepts no positional args; reads `DRY_RUN` and `RELEASE_FROM_BRANCH` env-style overrides.
  - `make release VERSION=vX.Y.Z[-PRERELEASE]` requires `VERSION`; same env-style overrides apply.

- **Auto-patch-release decision table**:
  | Commits since last tag | Dependabot commits in that range | Action |
  |---|---|---|
  | 0 | 0 | no-op (logged "no commits since v...; skipping") |
  | ≥1 | 0 | no-op (logged "no dependabot commits since v...; skipping") |
  | ≥1 | ≥1 | cut `vX.Y.(Z+1)`, push, trigger release workflow |

### Contracts

`contracts/ghcr-tag-contract.md` covers:

- **Triggers** → **Tags published**:
  - Push to `master` (test passes) → `master`, `sha-<7short>`
  - Push to `master` (test fails) → no push
  - PR opened (any state) → no push
  - Tag push `vX.Y.Z` (no pre-release suffix) → `vX.Y.Z`, `vX.Y`, `vX`, `latest` (latest+vX advance only if highest-precedence non-pre-release in repo)
  - Tag push `vX.Y.Z-PRERELEASE` → `vX.Y.Z-PRERELEASE` only
  - Tag push `not-vX.Y.Z` → no trigger (release workflow ignores non-matching tags)
- **Same-digest invariant**: For one tag-push event, all published tags MUST point at the same image digest (verifiable via `docker manifest inspect`).
- **Hotfix behavior**: pushing `v1.4.8` after `v1.5.0` exists → `v1.4.8` published, `v1.4` advances, `v1` and `latest` unchanged.

`contracts/make-targets-contract.md` covers:

- **Preconditions** for each Make target (clean tree, branch == master unless overridden, prior tag for patch/minor, no-prior-tag-OK for major).
- **Postconditions** (annotated tag created locally, pushed to origin, run URL printed).
- **Exit codes**: 0 on success, non-zero on any precondition failure with a stable error-message-prefix per failure type.
- **`DRY_RUN=1`**: prints exactly one line `Would create tag: vX.Y.Z at <SHA>` to stdout, exits 0, makes no git changes.

### Quickstart

`quickstart.md` covers (operator-facing):

1. **First-time setup**: nothing required. The workflows ship in this PR; on first push to `master` after merge, the publish-master job runs and lands the first `:master` image. No GitHub-side credential configuration; no GHCR-package-creation step (created automatically on first push).

2. **Cut a patch release**: from a clean checkout on `master`:
   ```sh
   make release-patch         # creates and pushes vX.Y.(Z+1)
   ```
   Wait ~10 min, watch the run URL printed by the make target, verify the image lands at `ghcr.io/<owner>/honkai-rule-server:vX.Y.(Z+1)` plus moving tags.

3. **Cut a minor / major release**: same pattern with `make release-minor` / `make release-major`.

4. **Cut an RC tag for testing**: `make release VERSION=v1.5.0-rc.1`. Only the exact tag publishes; moving tags don't advance.

5. **Hotfix backport**: from a checkout of an old commit, `make release VERSION=v1.4.8 RELEASE_FROM_BRANCH=hotfix/1.4`. `latest` is unaffected if `v1.5.0` is already shipped.

6. **Verify GHCR**: `docker pull ghcr.io/<owner>/honkai-rule-server:latest && docker inspect --format '{{.Config.Labels}}' ghcr.io/<owner>/honkai-rule-server:latest`. Labels should show the release tag in `org.opencontainers.image.version`.

7. **Update the deployment chart**: in `<your-iac-repo>/charts/honkai-rule-server/values.yaml`, set:
   ```yaml
   image:
     repository: ghcr.io/<owner>/honkai-rule-server
     tag: v1.4.8        # or :master, or :sha-<short>, or pin to a digest
   ```
   This replaces the old `registry.example.com/library/...` placeholder. Argo CD picks up the change on its next sync.

8. **Disable Dependabot auto-patch-release**: change the cron in `.github/workflows/auto-patch-release.yml` to `0 0 31 2 *` (never fires) and commit. Re-enable: revert.

9. **Disable Dependabot entirely**: delete `.github/dependabot.yml`. PRs stop opening within 24 hours.

10. **Troubleshoot a stuck Dependabot PR**: if a PR's CI fails repeatedly, the auto-merge workflow leaves it open. Maintainer either fixes the breakage manually, adds the dep to `ignore:` in `dependabot.yml`, or closes the PR.

### Agent context update

Update the lines between `<!-- SPECKIT START -->` and `<!-- SPECKIT END -->` in `CLAUDE.md`:
- Mark **012 (url-test-region-groups)** as fully implemented (it is — per the existing CLAUDE.md status block already lists it as fully implemented).
- Add **013 (ci-container-release)** as the active feature, with a one-line summary pointing at this plan.
- Add a key-reading bullet pointing at `specs/013-ci-container-release/plan.md`.
- Update the existing "registry.example.com/library/honkai-rule-server" reference (in the 009 line) with a follow-on note: "→ replaced by `ghcr.io/<owner>/honkai-rule-server` per 013."

## Phases (after this command)

This command stops here. Next: `/speckit-tasks` produces `tasks.md` with the dependency-ordered task list.

Suggested task ordering (test-first per Constitution Principle IV — applied where applicable):

1. **Shell-script test for tag-bump arithmetic** (US1 / FR-012): a small `test/release-bump.sh` (or equivalent) that asserts the `awk`-style version-bump produces the correct next tag for a curated input set. Test-first: write the assertions, watch them fail (no Make target yet), then implement.

2. **Shell-script test for VERSION regex** (US4 / FR-013a): assertions for `v1.5.0` / `v1.5.0-rc.1` accepted; `1.5.0` / `v1.5` / `v.1.5.0` / `v1.5.0_rc1` rejected. Test-first.

3. **Add the three `release-{patch,minor,major}` Make targets** (US1 / FR-012). Tests from #1 pass. Add the help-line per FR-021.

4. **Add the `release VERSION=` Make target** (US4 / FR-013a). Test from #2 passes.

5. **Add the `DRY_RUN=1` mode** to all four targets (US4 / FR-013). Manual verification.

6. **Flip Makefile `IMAGE_REPO` default to GHCR** (FR-002). Update `.env.example` comment.

7. **Extend `.github/workflows/ci.yml` with `publish-master` job** (US2 / FR-005). Job depends on the existing `test` job; uses `docker/login-action` + `docker/build-push-action` with `tags: master,sha-<short>`. Smoke-test by pushing a no-op commit to a throwaway branch and observing the workflow on a fork.

8. **Create `.github/workflows/release.yml`** (US1 / FR-008..FR-011a). Triggers on `refs/tags/v*`. Re-runs the test job, then build-push step with the four-tag matrix logic + the latest-precedence detection. Smoke-test by pushing a `v0.0.1-rc.1` tag and observing the workflow.

9. **Create `.github/dependabot.yml`** (US3 / FR-016). Three ecosystems, weekly, group rules. Verify by waiting for the first scheduled run (or trigger a manual sync via `gh api repos/.../dependabot/secrets` equivalent).

10. **Create `.github/workflows/dependabot-auto-merge.yml`** (US3 / FR-017). Listen for `pull_request` from `dependabot[bot]`, gate on non-major via `dependabot/fetch-metadata`, call `gh pr merge --auto --squash`.

11. **Create `.github/workflows/auto-patch-release.yml`** (US3 / FR-018). Cron `0 14 * * *`; checks for Dependabot commits since last tag, creates and pushes the patch tag.

12. **Create `RELEASING.md`** (FR-020). Cover: three Make targets + DRY_RUN + explicit-version + Dependabot flow + GHCR coordinates + disable-Dependabot path.

13. **Update `CLAUDE.md`** to mark 013 active and add the key-reading bullet.

14. **End-to-end smoke** (after all of the above): cut `v0.1.0` (the first real release) via `make release-major`, verify the four GHCR tags land within 10 minutes, verify the image runs and serves traffic identically to the local `bin/server`.

15. **Manual one-time GHCR visibility check**: confirm the published package is public via the GitHub UI (Settings → Packages). If private, toggle to public.

Phase 2 (`/speckit-tasks`) will refine these into a TASK file with concrete file paths, test names, and dependency edges.
