# Feature Specification: CI Container Builds, SemVer Releases via GHCR, Dependabot Auto-Patch

**Feature Branch**: `013-ci-container-release`
**Created**: 2026-05-07
**Status**: Draft
**Input**: User description: "please implement CI and build container images then uploads to Github packages. Please also figure out a way so that we can do releases with SemVer versioning. And let me know how to easily tag major, minor, patch releases. Btw, I also want to enable dependabot to automatically patch, test and release (patched version)."

**Anchors**:
- [`009-cluster-deploy/spec.md`](../009-cluster-deploy/spec.md) introduced the container image and Helm chart pinning a SHA-tagged image in `<your-iac-repo>/charts/honkai-rule-server/values.yaml`. The image registry placeholder `registry.example.com/library/honkai-rule-server` referenced there is replaced by this feature with a canonical, public GitHub Container Registry (GHCR) repository.
- The existing CI workflow at [`.github/workflows/ci.yml`](../../.github/workflows/ci.yml) already runs `go vet`, `staticcheck`, `go test -race`, snapshot-drift check, and a smoke `docker build` on every push and PR. This feature **extends** that pipeline (adds publish + release steps) rather than replacing it. The pre-existing checks remain the merge gate.
- The Makefile already has `docker-build`, `docker-push`, and `docker-push-latest` targets driven by `IMAGE_REPO` / `IMAGE_TAG` make variables. This feature **extends** the Makefile with release-tagging helpers (`release-patch`, `release-minor`, `release-major`) and **changes the default `IMAGE_REPO` value** to point at GHCR.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Cut a SemVer release with a single command (Priority: P1)

The maintainer is on `master` after merging a feature. They want to publish a new release. Today they have no automation: they would have to manually pick a version number, manually `git tag`, manually `docker build`/`docker push`, manually update the chart's `image.tag`. That's error-prone — easy to forget a step, easy to push an inconsistent tag/image pair, easy to clobber `:latest`.

The maintainer wants to run **one command** locally — e.g., `make release-patch` — and have everything else happen automatically: the next SemVer version is computed from the most recent release tag, an annotated git tag (`vMAJOR.MINOR.PATCH`) is created and pushed, a CI workflow runs the existing test suite against that tag, builds the image, and publishes it to GHCR with a full set of SemVer-precedence tags (`vX.Y.Z`, `vX.Y`, `vX`, plus `latest` for non-pre-release tags). The maintainer's job ends at running the make target.

**Why this priority**: This is the core of the user request. Without it, the rest of the project's deployment story (009 cluster-deploy + downstream chart pin) has no canonical, reproducible source of versioned images. P1 because everything else (master-branch builds, Dependabot auto-patches) is plumbing that supports this primary release flow.

**Independent Test**: From a clean checkout on `master`, run `make release-patch`. Verify: (a) a new annotated tag of the form `vX.Y.(Z+1)` exists locally and on `origin`, (b) the GitHub Actions release workflow runs to completion against that tag, (c) GHCR shows a freshly-pushed image at `ghcr.io/<owner>/honkai-rule-server` with exactly these tags pointing at the same digest: `vX.Y.(Z+1)`, `vX.Y`, `vX`, `latest`, (d) the image's `org.opencontainers.image.revision` label matches the tagged commit's full SHA and `org.opencontainers.image.version` matches `vX.Y.(Z+1)`, (e) pulling `ghcr.io/<owner>/honkai-rule-server:vX.Y.(Z+1)` and running it serves traffic identically to the local `bin/server` built from the same commit.

**Acceptance Scenarios**:

1. **Given** the most recent release tag is `v1.4.7` on `master` and the working tree is clean, **When** the maintainer runs `make release-patch`, **Then** an annotated tag `v1.4.8` is created at `HEAD`, pushed to `origin`, and the release workflow publishes `ghcr.io/<owner>/honkai-rule-server` tagged with `v1.4.8`, `v1.4`, `v1`, and `latest` — all four tags pointing at the same image digest.
2. **Given** the most recent release tag is `v1.4.7`, **When** the maintainer runs `make release-minor`, **Then** the new tag is `v1.5.0` (NOT `v1.4.8`) and the published image carries `v1.5.0`, `v1.5`, `v1`, `latest`.
3. **Given** the most recent release tag is `v1.4.7`, **When** the maintainer runs `make release-major`, **Then** the new tag is `v2.0.0` and the published image carries `v2.0.0`, `v2.0`, `v2`, `latest`.
4. **Given** there are no existing release tags in the repo, **When** the maintainer runs `make release-patch`, **Then** the command refuses (no prior tag to compute the next patch from) and instructs the maintainer to first run `make release-major` (which creates `v1.0.0` from a hardcoded zero baseline).
5. **Given** the working tree is dirty (uncommitted changes), **When** the maintainer runs any `make release-*` command, **Then** the command refuses with a clear error and exits non-zero — releases must be cut from clean checkouts so the resulting tag is reproducible.
6. **Given** the maintainer is not on `master`, **When** they run any `make release-*` command, **Then** the command refuses unless `RELEASE_FROM_BRANCH=<branch>` is explicitly set (escape hatch for hotfix branches).
7. **Given** the maintainer pushes a tag like `v1.5.0-rc.1` (manually, bypassing the make helper, or via `make release VERSION=v1.5.0-rc.1`), **When** the release workflow runs, **Then** the published image carries ONLY the pre-release tag `v1.5.0-rc.1` — `v1.5.0`, `v1.5`, `v1`, and `latest` are NOT updated. Pre-release tags must never advance moving tags.
8. **Given** a release tag was pushed but the existing CI test suite (vet / staticcheck / race / snapshot drift) fails on that commit, **When** the release workflow runs, **Then** it fails before the image push step — no broken image ever reaches GHCR with a release tag.

---

### User Story 2 - Master-branch images are continuously available (Priority: P1)

A second engineer is staging a deploy from `master` to a test cluster ahead of cutting an actual release. They need an image that reflects the current `master` HEAD without having to build and push it themselves. Today the Makefile's `docker-push` target works, but it requires the engineer to have local Docker, network access to the registry, and the right credentials — friction every team member has to redo individually.

The engineer wants every push to `master` (after PR merge) to automatically build and publish an image to GHCR tagged with the commit's short SHA and a moving `master` tag. The existing test suite still gates the push: a test failure must block image publication. The engineer pulls `ghcr.io/<owner>/honkai-rule-server:master` (or pins the SHA) without local build steps.

**Why this priority**: P1 (tied with US1) — without continuous master images, the maintainer-cut releases (US1) have no incremental verification path. Staging a master image, observing it on a real cluster, then cutting `make release-patch` is the standard pre-release validation flow this story enables.

**Independent Test**: Push a commit (or have a PR merge) to `master`. Verify: (a) the GitHub Actions CI workflow includes a job that builds and pushes `ghcr.io/<owner>/honkai-rule-server` tagged with both `master` and the commit's short SHA, (b) the push happens **only after** the existing vet/staticcheck/race/snapshot-drift jobs pass, (c) the SHA-tagged image is immutable (re-running the workflow on the same SHA would no-op or produce an identical digest), (d) the `master` tag advances on each push and points at the most recent `master` commit's image.

**Acceptance Scenarios**:

1. **Given** a PR is merged into `master` (or a direct push to `master`), **When** the CI workflow runs and the test job passes, **Then** an image is built and pushed to GHCR tagged with `master` and `sha-<short>` (where `<short>` is the 7-character abbreviated SHA of the merge/push commit).
2. **Given** the test job fails on a master push, **When** the workflow runs, **Then** the build/push job does NOT execute (dependency on test job success), and no image is published.
3. **Given** the same `master` commit is built twice (e.g., the workflow is re-run), **When** the build/push job runs, **Then** the resulting image digests are byte-identical (reproducible builds via the existing `Dockerfile`'s deterministic flags `-trimpath -ldflags="-s -w"`).
4. **Given** an open pull request, **When** CI runs on the PR head, **Then** the existing smoke `docker build` job continues to run for validation, but does NOT push to GHCR (no credentials exposure, no unreviewed code in the registry). PR images stay local to the runner.

---

### User Story 3 - Dependabot keeps dependencies fresh and ships patch releases automatically (Priority: P2)

The maintainer doesn't want to babysit dependency upgrades. Today, when `go.mod` or a Docker base image gets a security patch, nothing happens automatically — the maintainer has to notice and manually run `go get -u`, validate, commit, and release. Stale dependencies accumulate and the eventual update is a bigger, riskier change than a steady drip would be.

The maintainer wants Dependabot configured to open PRs against `master` for: (a) Go module dependencies (`gomod` ecosystem), (b) GitHub Actions versions used in CI workflows (`github-actions` ecosystem), (c) the Docker base image referenced in `Dockerfile` (`docker` ecosystem). When a Dependabot PR opens, the existing CI workflow runs against it. If CI passes, the PR auto-merges into `master`. After merge, a recurring scheduled workflow checks if there have been any commits authored by `dependabot[bot]` since the last release tag; if so, it cuts a patch release exactly as US1 does — same `vX.Y.(Z+1)` tag, same image-publish flow, same moving-tag advancement.

**Why this priority**: P2 because the manual flow (US1 + US2) is sufficient on its own; Dependabot adds an automation layer on top that reduces toil. The maintainer can ship without it; with it, they ship more often without thinking about it.

**Independent Test**: Configure Dependabot, then introduce a deliberately-stale dependency (or wait for a real one). Verify: (a) Dependabot opens a PR for the stale dependency, (b) the existing CI gate runs on the PR's head commit, (c) on green CI, the PR auto-merges to `master`, (d) the scheduled "auto-patch-release" workflow detects the dependabot commit on `master` and creates `vX.Y.(Z+1)`, (e) the existing release workflow (US1) picks up the new tag and publishes the image as usual. End-to-end: no human keystrokes between Dependabot opening the PR and the new image landing in GHCR.

**Acceptance Scenarios**:

1. **Given** `.github/dependabot.yml` is configured for `gomod`, `github-actions`, and `docker` ecosystems on a weekly schedule, **When** a stale Go module dependency exists, **Then** Dependabot opens a PR titled per its standard convention (e.g., `build(deps): bump foo from 1.2.3 to 1.2.4`).
2. **Given** a Dependabot PR is open, **When** the existing CI workflow runs and all checks pass, **Then** the PR auto-merges to `master` without human review (auto-merge enabled by a workflow that responds to `dependabot[bot]`-authored PRs and uses `gh pr merge --auto --squash`).
3. **Given** a Dependabot PR's CI run fails, **When** the workflow finishes, **Then** the PR does NOT auto-merge — it stays open for human attention. (The maintainer can choose to fix, override with manual merge, or close.)
4. **Given** one or more Dependabot commits have landed on `master` since the most recent release tag, **When** the scheduled `auto-patch-release` workflow runs (e.g., daily at 14:00 UTC, configurable), **Then** it computes the next patch version and creates the annotated tag exactly as `make release-patch` would, triggering the standard release workflow.
5. **Given** ZERO Dependabot commits have landed on `master` since the last release tag (only feature commits, or no commits at all), **When** the scheduled `auto-patch-release` workflow runs, **Then** it does NOT create a tag — auto-patch is dependency-bump-driven, not time-driven, so feature work goes through the manual `make release-*` flow (US1).
6. **Given** a feature commit AND a Dependabot commit have both landed on `master` since the last release tag, **When** the scheduled `auto-patch-release` workflow runs, **Then** it still cuts a patch release containing both — the maintainer is expected to manually cut `make release-minor` BEFORE the next scheduled run if they want the feature to ship as a minor bump. The auto-patch-release workflow does NOT inspect commit semantics; it ships whatever has accumulated.
7. **Given** a security-flagged Dependabot PR (CVE alert, marked by Dependabot as security update), **When** CI passes, **Then** the same auto-merge + auto-patch-release flow applies — security patches don't need a special path, the existing path is fast enough.

---

### User Story 4 - Maintainer can preview and override release commands (Priority: P3)

The maintainer wants to see what a `make release-*` command would do before executing it (dry-run), and they occasionally want to release a specific version (e.g., `v2.0.0` after a long stretch of `v1.x` patches, where they want to skip from `v1.9.42` → `v2.0.0` directly without going through `v1.10.0`).

**Why this priority**: P3 because the default arithmetic (US1's `release-patch`/`release-minor`/`release-major`) covers the vast majority of cases. The override path is a once-or-twice-a-year escape hatch.

**Independent Test**: Run `make release-patch DRY_RUN=1` and verify it prints the tag it WOULD create without actually creating it. Run `make release VERSION=v2.0.0` (explicit version) and verify it creates exactly that tag, refusing if `v2.0.0` already exists.

**Acceptance Scenarios**:

1. **Given** the most recent release tag is `v1.4.7`, **When** the maintainer runs `make release-patch DRY_RUN=1`, **Then** the command prints `Would create tag: v1.4.8 at <SHA>` and exits 0 without creating or pushing anything.
2. **Given** the most recent release tag is `v1.9.42`, **When** the maintainer runs `make release VERSION=v2.0.0`, **Then** an annotated tag `v2.0.0` is created at `HEAD` (skipping over `v1.10.0`) and the standard release flow proceeds.
3. **Given** a tag `v2.0.0` already exists on the remote, **When** the maintainer runs `make release VERSION=v2.0.0`, **Then** the command refuses with a clear "tag already exists" error and exits non-zero.
4. **Given** the maintainer runs `make release VERSION=2.0.0` (missing the `v` prefix), **Then** the command refuses — the project's tag convention is `vX.Y.Z` and the helper enforces it.

### Edge Cases

- **Two release-tag pushes race** (maintainer runs `make release-patch` from two checkouts simultaneously): both compute the same next version, both try to push the same annotated tag. The second push fails with `! [rejected]` from `git push`. The losing checkout reports the failure and exits non-zero. No partial state — the winning push runs the workflow and publishes the image.
- **A release workflow run fails AFTER the tag is already pushed** (e.g., GHCR is down at the moment of push): the tag persists on `origin`, but no image lands on GHCR. Re-running the failed workflow from the GitHub UI re-triggers the build and push. The `make release-*` helper does NOT delete the tag on workflow failure — that would be destructive and confusing. The maintainer's recovery path is "re-run the workflow," not "re-tag."
- **Maintainer manually creates a tag that doesn't match `v[0-9]+\.[0-9]+\.[0-9]+(-.+)?`**: the release workflow's tag-trigger filter ignores it; no image is built. Useful for pinning arbitrary commits with non-release tags (e.g., demos, snapshots) without polluting GHCR.
- **`master` tag is also a SemVer tag**: irrelevant — branch names and tags are different namespaces in git, and the release workflow specifically triggers on tag pushes (`refs/tags/v*`), not branch pushes.
- **GHCR repo visibility** (public vs private): the workflow always pushes; visibility of the published package is set once via the GitHub UI / API at the package level, not per-push. Default for this open-source project is **public** (matches the open-source repo). The spec assumes public; private is a one-click toggle later if needed.
- **`org.opencontainers.image.*` labels** are stamped at build time via `--label` flags in the workflow's docker buildx invocation. They include `revision` (commit SHA), `version` (release tag or `master`/`sha-<short>`), `source` (repo URL), `created` (RFC 3339 build timestamp). The labels make `docker inspect` self-describing and feed downstream provenance tooling.
- **Pre-release / RC tags** (`v1.5.0-rc.1`, `v1.5.0-beta.2`): the release workflow detects the SemVer pre-release suffix and emits ONLY the exact tag — no `vMAJOR.MINOR`, no `vMAJOR`, no `latest`. The make helper does not create RC tags by default; the maintainer creates them manually with `make release VERSION=v1.5.0-rc.1`.
- **`latest` tag on a hotfix release**: if the maintainer cuts `v1.4.8` AFTER `v1.5.0` has already shipped (a backport / hotfix to the v1.4 line), `latest` should NOT regress. The release workflow checks: `latest` advances ONLY when the new tag is the highest-precedence non-pre-release tag in the repo. A hotfix to a previous minor still publishes `v1.4.8` and advances `v1.4`, but leaves `latest` and `v1` alone (since `v1.5.0` is higher).
- **Dependabot opens a PR that fails CI repeatedly** (e.g., a major version bump with breaking changes): no auto-merge happens; PR stays open. The maintainer either fixes the breakage manually, marks the dep version constraint to skip the bump (in `dependabot.yml`'s `ignore` block), or closes the PR. No silent failure.
- **Dependabot batches grouped updates**: configure `groups:` in `dependabot.yml` (e.g., a `prod-deps` group bundling all non-major Go module bumps into one PR). The auto-merge + auto-patch-release flow is identical regardless of how Dependabot batches.
- **Auto-patch-release workflow runs while a `make release-*` is in flight**: harmless — both compute the next version from the same `git describe --tags --abbrev=0` baseline. Whichever pushes the tag first wins; the loser's `git push` is rejected and the run exits cleanly.
- **GHCR token rotation**: the workflow uses `${{ secrets.GITHUB_TOKEN }}` with `packages: write` permission scoped at the workflow level. There is no static PAT to rotate; GitHub manages the token's lifecycle per workflow run.
- **Image registry references in the existing chart values**: out of scope for this feature's implementation. The 009 cluster-deploy chart in `<your-iac-repo>/charts/honkai-rule-server/values.yaml` will need a one-line update from `registry.example.com/library/honkai-rule-server` to `ghcr.io/<owner>/honkai-rule-server` when the operator adopts this feature; the change is documented in the quickstart but not gated by this spec.
- **Major-version Dependabot bumps that auto-merge despite breaking changes**: the default Dependabot group rule (FR-016) bundles `minor` and `patch` only — major bumps open as separate PRs and SHOULD require human review. To enforce this, the auto-merge workflow (FR-017) MUST inspect the Dependabot metadata (e.g., `dependabot/fetch-metadata` action's `update-type` output) and skip auto-merge when `update-type` is `version-update:semver-major`.

## Requirements *(mandatory)*

### Functional Requirements

#### Image registry & naming

- **FR-001**: The canonical container image registry MUST be GitHub Container Registry (GHCR), at `ghcr.io/<github-repository-owner>/honkai-rule-server`. The owner is resolved from `${{ github.repository_owner }}` in workflows and from `git remote get-url origin` (parsing the GitHub owner segment) in the Makefile. The previous placeholder `registry.example.com/library/honkai-rule-server` referenced in 009 is REPLACED by this canonical name.

- **FR-002**: The Makefile's default `IMAGE_REPO` value MUST change from `registry.example.com/library/honkai-rule-server` to `ghcr.io/<owner>/honkai-rule-server` (where `<owner>` is computed from the git remote at make-target invocation time, with a fallback to a hardcoded value if the remote can't be parsed). Operator override via `IMAGE_REPO=<custom>` MUST continue to work for private/mirror registries.

- **FR-003**: Images MUST carry the standard OCI annotation labels at build time:
  - `org.opencontainers.image.source` = repository URL (e.g., `https://github.com/<owner>/honkai-rule-server`)
  - `org.opencontainers.image.revision` = full commit SHA
  - `org.opencontainers.image.version` = release tag (e.g., `v1.4.8`) or `master` for branch builds
  - `org.opencontainers.image.created` = RFC 3339 build timestamp
  - `org.opencontainers.image.title` = `honkai-rule-server`
  - `org.opencontainers.image.licenses` = SPDX identifier matching the repo's `LICENSE` file (omitted if no LICENSE file exists)

  These are emitted via `docker buildx build --label` (or `metadata-action`) in the workflow.

#### CI workflow extensions

- **FR-004**: The existing `.github/workflows/ci.yml` workflow MUST continue to run the four jobs it runs today (`go vet`, `staticcheck`, `go test -race`, snapshot-drift check, plus the `docker build` smoke job) on every push to `master`/`main` and every pull request. No regression in existing CI behavior.

- **FR-005**: On push to `master` (NOT on pull requests), AFTER the existing test job passes, the workflow MUST build the container image and push it to GHCR with these tags:
  - `master` (moving)
  - `sha-<7-char-short-sha>` (immutable)

- **FR-006**: On pull requests, the workflow MUST continue to run the smoke `docker build` to validate the Dockerfile but MUST NOT push to GHCR. PR images never leave the runner.

- **FR-007**: Authentication to GHCR MUST use `${{ secrets.GITHUB_TOKEN }}` scoped with `packages: write` permission at the job level. No additional secrets, no PAT, no manual credential management.

#### Release workflow on tag push

- **FR-008**: A separate workflow (e.g., `.github/workflows/release.yml`) MUST trigger on tag pushes matching the pattern `v[0-9]+.[0-9]+.[0-9]+*` (i.e., `v1.0.0` and `v1.0.0-rc.1` both qualify; arbitrary tags like `demo-2026` do not).

- **FR-009**: The release workflow MUST run the same test suite (`go vet`, `staticcheck`, `go test -race`, snapshot drift) against the tagged commit BEFORE building or pushing any image. A test failure MUST abort the release before the image is built. No broken release ever lands in GHCR.

- **FR-010**: For a tag of the form `v<MAJOR>.<MINOR>.<PATCH>` (NO pre-release suffix), the release workflow MUST publish the image to GHCR with these tags pointing at the same digest:
  - `vMAJOR.MINOR.PATCH` (immutable, exact)
  - `vMAJOR.MINOR` (moving within the minor line)
  - `vMAJOR` (moving within the major line)
  - `latest` (moving across all releases) — ONLY if the new tag is the highest-precedence non-pre-release tag in the repository per SemVer ordering. If the new tag is a backport to an older line (e.g., `v1.4.8` cut after `v1.5.0` has shipped), `latest` and `vMAJOR` MUST NOT regress.

- **FR-011**: For a tag of the form `v<MAJOR>.<MINOR>.<PATCH>-<PRERELEASE>` (e.g., `v1.5.0-rc.1`, `v1.5.0-beta.2`), the release workflow MUST publish the image with EXACTLY ONE tag — the pre-release-suffixed version itself. Moving tags (`vMAJOR.MINOR`, `vMAJOR`, `latest`) MUST NOT advance.

- **FR-011a**: The release workflow MUST also create a corresponding GitHub Release entry (via the `softprops/action-gh-release` action or equivalent) for non-pre-release tags. The release notes default to "Auto-generated" using GitHub's commit-list since previous tag; the maintainer can edit them post-hoc. Pre-release tags MUST be marked as pre-releases on GitHub (via the action's `prerelease: true` flag) so the GitHub UI's "Latest release" badge does not regress.

#### Make helpers for tagging

- **FR-012**: The Makefile MUST provide three release-cutting targets:
  - `make release-patch` → bumps PATCH, creates `vMAJOR.MINOR.(PATCH+1)` annotated tag
  - `make release-minor` → bumps MINOR, resets PATCH, creates `vMAJOR.(MINOR+1).0`
  - `make release-major` → bumps MAJOR, resets MINOR and PATCH, creates `v(MAJOR+1).0.0`

  Each target:
  1. Verifies the working tree is clean (`git diff --quiet && git diff --cached --quiet`); refuses with non-zero exit if dirty.
  2. Verifies the current branch is `master` unless `RELEASE_FROM_BRANCH=<branch>` is set; refuses otherwise.
  3. Computes the next version by parsing the most recent `vX.Y.Z` tag from `git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*'`. If no such tag exists, only `release-major` is allowed (creates `v1.0.0` from baseline); `release-patch` and `release-minor` refuse with a clear message instructing the maintainer to run `make release-major` first.
  4. Creates an annotated tag with message `Release vX.Y.Z` and `git push origin vX.Y.Z`.
  5. Prints the GitHub Actions run URL the maintainer can monitor.

- **FR-013**: The Makefile MUST provide a `DRY_RUN=1` mode for all three release targets that prints the tag the command WOULD create without actually creating or pushing anything. Used for quick verification before pulling the trigger.

- **FR-013a**: The Makefile MUST also provide an explicit-version target `make release VERSION=vX.Y.Z` that:
  1. Validates `VERSION` matches the SemVer regex `^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$`. Refuses on mismatch (e.g., missing `v` prefix, non-numeric components).
  2. Runs the same clean-tree / branch checks as FR-012.
  3. Refuses if the tag already exists locally or remotely.
  4. Creates and pushes the annotated tag.

  Used for skipping over patch numbers (`v1.9.42` → `v2.0.0` directly), cutting RC tags (`v1.5.0-rc.1`), or backporting (cutting `v1.4.8` while `v1.5.0` is already shipped).

#### Multi-architecture & build reproducibility

- **FR-014**: For v1, the published image MUST support `linux/amd64` only — matching the existing Makefile's `--platform linux/amd64` and the cluster's known architecture. Multi-arch (`linux/amd64,linux/arm64`) is NOT in scope for this feature; if the cluster gains arm64 nodes, a follow-up feature can extend the buildx invocation. The Dockerfile is already arch-agnostic; only the workflow's `--platform` flag would need updating.

- **FR-015**: Image builds MUST be byte-reproducible across re-runs of the same commit, leveraging the existing Dockerfile's `-trimpath -ldflags="-s -w"` Go build flags and pinned `golang:1.25-alpine` base image. Two runs of the workflow against the same SHA MUST produce identical image digests (ignoring non-essential metadata-layer differences such as `created` timestamp; the digest of the layers themselves MUST match).

#### Dependabot configuration

- **FR-016**: A `.github/dependabot.yml` file MUST be added with three update ecosystems configured:
  - `gomod` — Go module dependencies in `/go.mod` — weekly schedule
  - `github-actions` — workflow `uses:` versions in `.github/workflows/` — weekly schedule
  - `docker` — `Dockerfile` `FROM` lines — weekly schedule

  Each ecosystem MUST set:
  - `open-pull-requests-limit: 5` (avoid PR flood)
  - `groups: { all-non-major: { update-types: ['minor', 'patch'] } }` so non-major bumps batch into one PR per ecosystem per week. Major bumps open as separate PRs by Dependabot's default behavior.

- **FR-017**: A workflow (e.g., `.github/workflows/dependabot-auto-merge.yml`) MUST listen for `pull_request` events from the `dependabot[bot]` actor. After CI passes, the workflow MUST enable auto-merge with squash strategy via `gh pr merge --auto --squash`. The workflow MUST:
  - Use `${{ secrets.GITHUB_TOKEN }}` (no PAT).
  - Run only when the PR author is `dependabot[bot]`.
  - Use the `dependabot/fetch-metadata` action to read the Dependabot metadata, and SKIP auto-merge if `update-type` is `version-update:semver-major` — major bumps stay open for human review per FR-016 group rules.

- **FR-018**: A scheduled workflow (e.g., `.github/workflows/auto-patch-release.yml`) MUST run on a cron schedule (default `0 14 * * *`, daily at 14:00 UTC; configurable via the workflow file). Each run:
  1. Fetches the most recent release tag (`vX.Y.Z`) via `git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*'`.
  2. Lists commits on `master` between that tag and `HEAD`.
  3. Filters those commits to ones authored by `dependabot[bot]`.
  4. If at least one Dependabot commit is present, computes `vX.Y.(Z+1)`, creates and pushes the annotated tag (triggering the release workflow per FR-008).
  5. If zero Dependabot commits are present, exits with no-op (logs the reason).

- **FR-019**: The auto-patch-release workflow's behavior on mixed commits (some Dependabot, some human-authored) MUST cut the patch release containing both. The maintainer is expected to manually cut `make release-minor` BEFORE the next scheduled run if they want the human commit to ship as a minor bump. The workflow does NOT inspect commit message semantics; it ships whatever has accumulated.

#### Discoverability & operator UX

- **FR-020**: The repository MUST include a brief `RELEASING.md` document (or equivalent section in `README.md`) covering:
  - The three Make targets (`release-patch`, `release-minor`, `release-major`) with one-line examples each.
  - The explicit-version path (`make release VERSION=v2.0.0`) with use cases.
  - The `DRY_RUN=1` flag.
  - The Dependabot auto-patch flow with a note on how to disable it (delete `.github/workflows/auto-patch-release.yml` or change the cron to a never-firing value like `0 0 31 2 *`).
  - The image registry coordinates (`ghcr.io/<owner>/honkai-rule-server`) and the four moving tags (`vX`, `vX.Y`, `vX.Y.Z`, `latest`) plus their semantics.

- **FR-021**: The `make help` target (already present in the Makefile per the existing `.PHONY` list) MUST list the new release targets with one-line descriptions matching the existing help format. Operator discovery without grepping the Makefile.

### Key Entities

- **Release tag**: An annotated git tag matching `v<MAJOR>.<MINOR>.<PATCH>(-<PRERELEASE>)?`, created on `master` (or an explicitly-allowed branch) at a clean checkout. Source of truth for what version a given commit corresponds to.

- **GHCR package**: A container image at `ghcr.io/<owner>/honkai-rule-server`. Tagged with multiple aliases per release: the immutable exact-version tag and three moving tags (`vMAJOR`, `vMAJOR.MINOR`, `latest`). Plus a `master` moving tag and per-commit `sha-<short>` tags from US2.

- **Dependabot configuration**: A YAML file at `.github/dependabot.yml` declaring three ecosystems (gomod, github-actions, docker), schedules, group rules, and PR limits. Owned by GitHub's hosted Dependabot service; this repo only declares the inputs.

- **Auto-merge gate**: The combination of branch protection rules (required `test` job to pass) and the `dependabot-auto-merge.yml` workflow's response to passing PRs. Together they form the "no-human-keystrokes" path from Dependabot PR open → master merge — but only for non-major Dependabot bumps.

- **Auto-patch-release schedule**: The cron expression in `auto-patch-release.yml`. Determines how often Dependabot accumulations get rolled into a release. The schedule is the only knob between "release on every Dependabot merge" (cron `* * * * *`, infeasible) and "release manually" (cron disabled, default for ops who don't want auto-patches).

- **GitHub Release**: A first-class GitHub object created alongside each release-tag image push (FR-011a). Carries auto-generated commit-list release notes; pre-release tags are flagged as such so the "Latest release" badge tracks the actual highest-precedence stable release.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A maintainer can cut a release end-to-end by running exactly one local command (`make release-patch`, `make release-minor`, or `make release-major`) and waiting for the GitHub Actions workflow to finish. No manual `docker push`, no manual chart edits, no manual tag creation. From command invocation to image-available-on-GHCR is under 10 minutes (gated by Go test suite duration plus image build/push, both currently ~3 minutes each).

- **SC-002**: After cutting `vX.Y.Z`, `docker pull ghcr.io/<owner>/honkai-rule-server:vX.Y.Z`, `:vX.Y`, `:vX`, and `:latest` all return the same image digest (verified via `docker inspect --format '{{.Id}}'`). Pulling the image and running it serves traffic identically to the local `bin/server` built from the tagged commit.

- **SC-003**: Cutting a pre-release tag (`vX.Y.Z-rc.1`) publishes ONLY that exact tag on GHCR — `vX.Y`, `vX`, and `latest` are unchanged after the workflow completes. The corresponding GitHub Release is flagged as a pre-release.

- **SC-004**: Cutting a hotfix release (`v1.4.8` after `v1.5.0` has already shipped) publishes `v1.4.8` and advances `v1.4`, but `latest` and `v1` remain pointing at `v1.5.0`'s digest.

- **SC-005**: Every push to `master` results in an image at `ghcr.io/<owner>/honkai-rule-server:master` and `:sha-<short>` within 10 minutes of the push, conditional on the test job passing. A test failure suppresses the push.

- **SC-006**: A pull request opened against `master` runs the existing test suite and a smoke Docker build; it does NOT result in any GHCR push, regardless of whether tests pass.

- **SC-007**: Dependabot opens at least one PR per stale dependency per ecosystem within seven days of the dependency becoming stale (gated by Dependabot's weekly schedule). For a deliberately-stale fixture dependency, this is verifiable end-to-end within one week of merging the `.github/dependabot.yml` file.

- **SC-008**: A non-major Dependabot PR with passing CI auto-merges to `master` within 5 minutes of the last required check turning green, with no human interaction. A Dependabot PR with failing CI does NOT auto-merge and stays open for human attention. A major-version Dependabot PR also does NOT auto-merge regardless of CI status.

- **SC-009**: Within 24 hours of a Dependabot PR auto-merging into `master`, the auto-patch-release workflow runs (per its cron schedule) and creates a `vX.Y.(Z+1)` tag, triggering a full release. The end-to-end path from "Dependabot opens PR" to "new image at `ghcr.io/<owner>/honkai-rule-server:vX.Y.(Z+1)`" takes at most 24 hours plus CI/build duration, with zero human keystrokes.

- **SC-010**: Re-running the release workflow on the same release tag produces an image with the same content digest as the first run (build reproducibility). Verified by manually re-triggering the workflow from the GitHub UI and comparing layer digests.

- **SC-011**: The `make help` output and `RELEASING.md` together let a new contributor cut their first release without external assistance — verifiable by handing the docs to a contributor unfamiliar with the project and timing how long it takes them to ship a no-op patch release. Target: under 15 minutes including reading.

- **SC-012**: When the auto-patch-release workflow is undesired (e.g., the maintainer is on vacation and doesn't want surprise releases), disabling it requires editing exactly one file (the cron in `auto-patch-release.yml`) and a single commit to push. Re-enabling is the same one-file revert.

## Assumptions

- The repository remains hosted at `github.com/<owner>/honkai-rule-server`, and the GitHub-Actions-resolved owner segment is the canonical GHCR namespace. No custom registry domain (no `harbor.example.com`, no internal mirror) is in scope; operators who want a private mirror set `IMAGE_REPO=` in their `.env` and run `make docker-push` manually as today.

- The current cluster is `linux/amd64` only (matches the existing Makefile and Dockerfile). Multi-arch images are explicitly out of scope for v1 to keep build duration short; adding `linux/arm64` later is a one-line change to the workflow's `--platform` flag.

- The maintainer holds push access to `master` (creating annotated tags from local checkouts is allowed). Branch-protection rules on `master` allow `dependabot[bot]` PRs to auto-merge after CI passes (this requires either no required reviewers, or `dependabot[bot]` exempted, or auto-merge configured to skip review requirements — the standard GitHub Dependabot integration handles this).

- The repository is open-source and the GHCR package visibility will be public. Private visibility is a one-time toggle in the GitHub UI ("Package settings → Change visibility"), independent of the workflow logic. The spec does not gate on visibility.

- The existing CI workflow's test suite is the sole quality gate. Additional gates (lint, security scan, vulnerability scan) are out of scope; if added later they become prerequisites for the build/push job in the same dependency graph used here.

- Pre-release tags use SemVer 2.0.0 conventions: `-rc.N`, `-beta.N`, `-alpha.N`, `-dev.SHA`, etc. The release workflow detects them by the presence of a `-` after the third numeric component. The Make helper does NOT generate pre-release tags by default; the maintainer creates them via `make release VERSION=v1.5.0-rc.1`.

- The `master` moving tag advances on every `master` push regardless of whether the underlying commit is a Dependabot commit, a feature commit, or a release tag itself. There is no separation between "Dependabot's `master`" and "feature work's `master`" — they share one history per standard GitHub flow.

- Dependabot's grouping config (one PR per ecosystem per week, batched non-major bumps) is sufficient for this project's dependency volume. If groups grow to where one PR is too large to review, the operator splits the group into smaller groups via `dependabot.yml` edits — out of scope for v1.

- The auto-patch-release schedule defaults to `0 14 * * *` (daily at 14:00 UTC = morning America/Toronto, matching the project's existing local-day boundary convention from feature 011). The exact cron is operator-tunable; the default is "once per day" which is a reasonable trade-off between freshness and release-noise.

- The release workflow uses well-known stable actions for the buildx + push step (`docker/login-action`, `docker/setup-buildx-action`, `docker/build-push-action`, `docker/metadata-action`, `softprops/action-gh-release`). Specific versions will be set during implementation and tracked by Dependabot's `github-actions` ecosystem going forward.

- The `RELEASING.md` document is maintained alongside the Makefile and workflow files; if the workflow logic changes (e.g., a new moving tag is added), `RELEASING.md` is updated in the same commit. This is a documentation discipline, not a tooling enforcement.

- The existing snapshot-drift CI check (Constitution Principle II) covers test correctness; this feature does not introduce new snapshot tests. The release workflow runs the same `go test ./...` and `git diff --exit-code` checks as the existing CI workflow.

- Backward compatibility for the 009 cluster-deploy chart values is downstream and out of scope for this spec. The chart's `image.repository` will need a one-line update from `registry.example.com/library/honkai-rule-server` to `ghcr.io/<owner>/honkai-rule-server` when the operator adopts this feature; the change is documented in the eventual `quickstart.md` for 013 but not gated by this spec.

- Major Dependabot bumps stay open for human review (FR-016 + FR-017 enforce this jointly). The maintainer can choose to manually merge them after review, after which the auto-patch-release flow picks up the merge on its next scheduled run. There is no intent to auto-merge breaking changes silently.
