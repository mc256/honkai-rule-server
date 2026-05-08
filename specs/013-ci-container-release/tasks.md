---
description: "Task list for 013-ci-container-release"
---

# Tasks: CI Container Builds, SemVer Releases via GHCR, Dependabot Auto-Patch

**Input**: Design documents from `/specs/013-ci-container-release/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/ghcr-tag-contract.md, contracts/make-targets-contract.md, quickstart.md

**Tests**: REQUIRED for the new shell helpers (bump arithmetic + version regex). Constitution Principle IV (Test-First) is non-negotiable; even though this feature does not touch the Go transformation core, the new operator-facing shell logic in the Make targets is the same kind of "small but load-bearing" code that benefits from a test-first discipline. Workflow YAML files are inherently tested by being run on the platform; smoke runs in Phase 7 cover their behavior end-to-end.

**Organization**: Four user stories — US1 (manual SemVer release, P1), US2 (master-branch images, P1), US3 (Dependabot auto-patch, P2), US4 (dry-run + explicit-version, P3). Phases follow the test-first cadence inside US1; later stories extend the foundation laid by US1's helpers.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: User story (US1, US2, US3, US4) — applied to Phase 3+ tasks only
- All paths are absolute from repo root: `/home/maverick/development/honkai-rule-server/`

## Path Conventions

- **Single Go module**: existing source under `internal/`, but this feature touches no Go code.
- **CI / build infra**: `.github/workflows/`, `.github/dependabot.yml`, `Makefile`, root-level docs.
- **Shell helpers + tests**: `scripts/` (new directory) for sourceable functions; `tests/release/` (new directory) for shell-script tests.
- **Spec artifacts**: `specs/013-ci-container-release/`

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Create the new directories that house the shell helpers and their tests. No new tooling — the test scripts run under stock POSIX `sh` (already present on every CI runner and developer machine).

- [X] T001 Create directory `/home/maverick/development/honkai-rule-server/scripts/` for sourceable shell helpers. Add a one-line `scripts/.keep` file or a brief `scripts/README.md` if the directory would otherwise be empty after Phase 2.

- [X] T002 Create directory `/home/maverick/development/honkai-rule-server/tests/release/` for shell-script tests. Add a brief `tests/release/README.md` documenting how to run the tests (`sh tests/release/test-bump.sh && sh tests/release/test-regex.sh`) and what they cover.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Land the test-first shell helpers (`scripts/release-bump.sh`) that both US1 and US4 depend on. Also flip the Makefile's `IMAGE_REPO` default to GHCR so subsequent workflow tasks land against a coherent registry value. These have no consumers until US1 / US2 land, but those stories cannot proceed until these exist.

**⚠️ CRITICAL**: US1 cannot begin until Phase 2 is complete and all foundational shell tests pass.

- [X] T003 Write failing shell-script test `tests/release/test-bump.sh` covering `bump_patch`, `bump_minor`, `bump_major` per `research.md` §3 and `contracts/make-targets-contract.md`. Test cases:
  - `bump_patch v1.4.7` → `v1.4.8`
  - `bump_patch v0.0.0` → `v0.0.1`
  - `bump_patch v10.20.30` → `v10.20.31`
  - `bump_patch v1.4.7-rc.1` → `v1.4.8` (pre-release suffix stripped before bump)
  - `bump_minor v1.4.7` → `v1.5.0` (resets patch)
  - `bump_minor v0.0.5` → `v0.1.0`
  - `bump_major v1.4.7` → `v2.0.0` (resets minor + patch)
  - `bump_major v0.9.9` → `v1.0.0`
  - `bump_major ` (empty input) → `v1.0.0` (baseline case)
  - `bump_patch ` (empty input) → exit 1 with stderr containing "no prior"
  - `bump_minor ` (empty input) → exit 1 with stderr containing "no prior"

  Test pattern: source `scripts/release-bump.sh` (which doesn't exist yet — that's the point), invoke each function, assert stdout / exit code. Print PASS / FAIL per case and exit non-zero on any FAIL.

- [X] T004 Write failing shell-script test `tests/release/test-regex.sh` covering the version regex per `research.md` §4 and `contracts/make-targets-contract.md`. Test cases:
  - Accept: `v1.5.0`, `v1.5.0-rc.1`, `v1.5.0-beta.2`, `v10.20.30`, `v0.0.1-dev.abc123`, `v1.0.0-alpha`
  - Reject: `1.5.0` (missing v), `v1.5` (missing patch), `v.1.5.0` (extra dot), `v1.5.0_rc1` (underscore), `v1.5.0+build.1` (build metadata not in scope), empty string, `vfoo`, `v-1.0.0`

  Test pattern: source `scripts/release-bump.sh`, invoke `validate_version "$candidate"` for each case, assert exit 0 (accept) or exit 1 (reject). Print PASS / FAIL per case.

- [X] T005 Run `sh tests/release/test-bump.sh` and `sh tests/release/test-regex.sh` and confirm both fail with errors like "scripts/release-bump.sh: No such file or directory" or "function bump_patch: not found". Record the failing case names.

- [X] T006 Create `scripts/release-bump.sh` with three sourceable shell functions:
  - `bump_patch <last>` — parses `vM.m.p[-prerelease]`, prints `vM.m.(p+1)`, exits 1 with "ERROR: no prior vX.Y.Z tag found; run 'make release-major' first to create v1.0.0" on empty input. Use `sed 's/^v//'` + `cut -d. -f1/2/3` + `cut -d- -f1` for parsing per `research.md` §3.
  - `bump_minor <last>` — same parse, prints `vM.(m+1).0`, same empty-input error.
  - `bump_major <last>` — same parse, prints `v(M+1).0.0`; on empty input prints `v1.0.0` (baseline) and exits 0.
  - `validate_version <candidate>` — pipes through `grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$'`, exits 0 on match, 1 on mismatch.

  File starts with `#!/bin/sh` (sourceable; no executable bit required). All four functions are POSIX-only (no bashisms).

- [X] T007 Run `sh tests/release/test-bump.sh` and `sh tests/release/test-regex.sh` and confirm both pass. If any case fails, debug `scripts/release-bump.sh` until green; do NOT modify the test cases.

- [X] T008 Modify `Makefile` to flip the default `IMAGE_REPO` value from `registry.example.com/library/honkai-rule-server` to `ghcr.io/<owner>/honkai-rule-server`, where `<owner>` is computed from the git remote at make-target invocation time. Reference logic:

  ```makefile
  IMAGE_OWNER := $(shell git remote get-url origin 2>/dev/null | sed -E 's#.*github.com[:/]([^/]+)/.*#\1#' | head -1)
  IMAGE_REPO ?= ghcr.io/$(IMAGE_OWNER)/honkai-rule-server
  ```

  Hard-fail-safe: if the regex doesn't match, `IMAGE_OWNER` is empty and `IMAGE_REPO` becomes `ghcr.io//honkai-rule-server` (clearly broken — operator notices immediately). The `?=` operator allows operator override via `IMAGE_REPO=...` env / `.env` to keep working.

- [X] T009 Modify `.env.example` to update the comment line near `IMAGE_REPO=` to reflect the new default and the override-for-private-mirror use case. Reference content:

  ```
  # Default IMAGE_REPO is ghcr.io/<your-org>/honkai-rule-server, derived from
  # `git remote get-url origin`. Override here for private mirror / custom registry:
  # IMAGE_REPO=registry.internal.example.com/library/honkai-rule-server
  ```

  Remove the old `IMAGE_REPO=registry.example.com/library/honkai-rule-server` line (it's no longer the canonical default).

**Checkpoint**: Foundation ready. The shell helpers are tested and work. `make` invocations now derive `IMAGE_REPO` from the git remote. US1 + US2 + US3 + US4 can begin in parallel.

---

## Phase 3: User Story 1 — Cut a SemVer release with a single command (Priority: P1) 🎯 MVP

**Goal**: Three Make targets (`release-patch`, `release-minor`, `release-major`) cut a SemVer git tag, push it, and trigger the `release.yml` workflow which publishes the image to GHCR with the four-tag set.

**Independent Test**: From a clean checkout on `master` with at least one prior `vX.Y.Z` tag, run `make release-patch`. Verify (a) `vX.Y.(Z+1)` exists locally and on origin, (b) the release workflow triggers and runs to completion, (c) `ghcr.io/<owner>/honkai-rule-server` shows `vX.Y.(Z+1)`, `vX.Y`, `vX`, `latest` all pointing at the same digest, (d) image labels are populated per `data-model.md` §3.

### Implementation for User Story 1

- [X] T010 [US1] Add the three `release-{patch,minor,major}` targets to `Makefile` per `contracts/make-targets-contract.md` and `research.md` §3. Each target:
  1. Runs the dirty-tree precondition check (`git diff --quiet && git diff --cached --quiet || (echo "ERROR: working tree is dirty;..."; exit 1)`).
  2. Runs the branch precondition check (compare `git rev-parse --abbrev-ref HEAD` to `${RELEASE_FROM_BRANCH:-master}`).
  3. Sources `scripts/release-bump.sh` (`. scripts/release-bump.sh`).
  4. Resolves `LAST=$$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null || true)`.
  5. Computes `NEXT=$$(bump_patch "$$LAST")` (or `bump_minor` / `bump_major` per target). Captures the function's exit code; on non-zero, propagate the error message and exit 1.
  6. If `DRY_RUN=1`: print `Would create tag: $$NEXT at $$(git rev-parse --short HEAD)` and exit 0. Otherwise:
  7. `git tag -a -m "Release $$NEXT" $$NEXT && git push origin $$NEXT`.
  8. Print the run URL: `https://github.com/$$(git remote get-url origin | sed -E 's#.*github.com[:/]([^/]+/[^/.]+)(\.git)?#\1#')/actions`.

  Add the three targets to `.PHONY` line at the top of the Makefile.

- [X] T011 [US1] Add the help-line entries for `release-patch`, `release-minor`, `release-major` to the `make help` target per FR-021 / `contracts/make-targets-contract.md` §"Help-line contract". Match the existing column-aligned format.

- [X] T012 [US1] Manually verify the three Make targets locally:
  - `make release-patch DRY_RUN=1` from clean tree on master → prints `Would create tag: vX.Y.(Z+1) at <SHA>`.
  - `make release-patch DRY_RUN=1` from dirty tree → exits 1 with "working tree is dirty".
  - `make release-patch DRY_RUN=1` from a non-master branch → exits 1 with "not on master".
  - `make release-patch DRY_RUN=1 RELEASE_FROM_BRANCH=$(git rev-parse --abbrev-ref HEAD)` from a non-master branch → succeeds (override accepted).
  - `make release-major DRY_RUN=1` from a repo with no prior tag → prints `Would create tag: v1.0.0 at <SHA>`.
  - `make release-patch DRY_RUN=1` from a repo with no prior tag → exits 1 with "no prior".

- [X] T013 [US1] Create `.github/workflows/release.yml` per `contracts/ghcr-tag-contract.md` Trigger C and `research.md` §1, §2. Reference structure:

  ```yaml
  name: release
  on:
    push:
      tags: ['v[0-9]+.[0-9]+.[0-9]+*']
  permissions:
    contents: write     # for action-gh-release
    packages: write     # for GHCR push
  jobs:
    test:
      runs-on: ubuntu-latest
      steps:
        - uses: actions/checkout@v4
        - uses: actions/setup-go@v5
          with:
            go-version-file: go.mod
            cache: true
        - run: go install honnef.co/go/tools/cmd/staticcheck@latest
        - run: go vet ./...
        - run: $(go env GOPATH)/bin/staticcheck ./...
        - run: go test -race ./...
        - run: git diff --exit-code
    build-and-push:
      needs: test
      runs-on: ubuntu-latest
      steps:
        - uses: actions/checkout@v4
        - uses: docker/setup-buildx-action@v3
        - uses: docker/login-action@v3
          with:
            registry: ghcr.io
            username: ${{ github.actor }}
            password: ${{ secrets.GITHUB_TOKEN }}
        - id: meta
          uses: docker/metadata-action@v5
          with:
            images: ghcr.io/${{ github.repository_owner }}/honkai-rule-server
            flavor: |
              latest=auto
            tags: |
              type=semver,pattern={{version}}
              type=semver,pattern={{major}}.{{minor}}
              type=semver,pattern={{major}}
            labels: |
              org.opencontainers.image.title=honkai-rule-server
              org.opencontainers.image.source=${{ github.server_url }}/${{ github.repository }}
              org.opencontainers.image.revision=${{ github.sha }}
        - uses: docker/build-push-action@v5
          with:
            context: .
            platforms: linux/amd64
            push: true
            tags: ${{ steps.meta.outputs.tags }}
            labels: ${{ steps.meta.outputs.labels }}
            cache-from: type=gha
            cache-to: type=gha,mode=max
    github-release:
      needs: build-and-push
      runs-on: ubuntu-latest
      steps:
        - uses: actions/checkout@v4
          with:
            fetch-depth: 0
        - uses: softprops/action-gh-release@v2
          with:
            generate_release_notes: true
            prerelease: ${{ contains(github.ref_name, '-') }}
  ```

  The `prerelease: ${{ contains(github.ref_name, '-') }}` condition flags pre-release tags (those containing a hyphen after the version) per FR-011a / SC-003.

- [X] T014 [US1] Pin specific action versions in `release.yml` to known-good releases (`@v4`, `@v5`, `@v3`, `@v2` as shown). Do NOT use `@main` or floating refs. Dependabot's `github-actions` ecosystem (added in US3) will keep them current automatically.

**Checkpoint**: US1 complete. After cutting `make release-patch` / `release-minor` / `release-major` against a real `master` HEAD, the release workflow runs end-to-end and lands the four-tag image on GHCR. The end-to-end smoke is deferred to Phase 7 (polish) so US2/US3/US4 can be implemented in parallel.

---

## Phase 4: User Story 2 — Master-branch images are continuously available (Priority: P1)

**Goal**: Every push to `master` (after merging) builds and pushes an image to GHCR tagged `master` and `sha-<7short>`. PR pushes do NOT push to GHCR.

**Independent Test**: Push a no-op commit to `master` (or merge a small PR). Verify (a) the existing CI test job runs and passes, (b) the new `publish-master` job runs after the test job, (c) `ghcr.io/<owner>/honkai-rule-server:master` and `:sha-<7short>` appear within ~10 minutes of the push, (d) on a separate PR, the smoke `docker build` job runs but no `:pr-*` or any other tag appears on GHCR.

### Implementation for User Story 2

- [X] T015 [US2] Modify `.github/workflows/ci.yml` to add a `publish-master` job per `contracts/ghcr-tag-contract.md` Trigger A and `research.md` §1. Reference structure (added to the existing file alongside `test` and `build-image`):

  ```yaml
    publish-master:
      name: publish image to GHCR (master only)
      needs: test
      if: github.ref == 'refs/heads/master' && github.event_name == 'push'
      runs-on: ubuntu-latest
      permissions:
        contents: read
        packages: write
      steps:
        - uses: actions/checkout@v4
        - uses: docker/setup-buildx-action@v3
        - uses: docker/login-action@v3
          with:
            registry: ghcr.io
            username: ${{ github.actor }}
            password: ${{ secrets.GITHUB_TOKEN }}
        - id: meta
          uses: docker/metadata-action@v5
          with:
            images: ghcr.io/${{ github.repository_owner }}/honkai-rule-server
            tags: |
              type=ref,event=branch
              type=sha,prefix=sha-,format=short
            labels: |
              org.opencontainers.image.title=honkai-rule-server
              org.opencontainers.image.source=${{ github.server_url }}/${{ github.repository }}
              org.opencontainers.image.revision=${{ github.sha }}
              org.opencontainers.image.version=master
        - uses: docker/build-push-action@v5
          with:
            context: .
            platforms: linux/amd64
            push: true
            tags: ${{ steps.meta.outputs.tags }}
            labels: ${{ steps.meta.outputs.labels }}
            cache-from: type=gha
            cache-to: type=gha,mode=max
  ```

  Key details:
  - `needs: test` makes the job dependent on the existing test job's success (FR-005 + the spec's "test failure must block image publication").
  - `if: github.ref == 'refs/heads/master' && github.event_name == 'push'` ensures the job runs ONLY on direct `master` pushes (no PR runs, no other-branch runs).
  - `type=ref,event=branch` emits the `:master` tag; `type=sha,prefix=sha-,format=short` emits `:sha-<7short>`.

- [X] T016 [US2] Add explicit job-level `permissions:` block to the existing `test` job and `build-image` job in `ci.yml`: both should declare `contents: read` only (defense-in-depth — they don't need write access). Existing `permissions: contents: read` at the workflow top is preserved; per-job `permissions` blocks override it where needed (the new `publish-master` job is the only one with `packages: write`).

- [X] T017 [US2] Confirm the existing `build-image` (smoke) job remains in `ci.yml`. It still runs on PRs to validate Dockerfile changes per FR-006 and `contracts/ghcr-tag-contract.md` Trigger B; it does NOT push (no `docker/login-action`, no `docker/build-push-action` with `push: true`). Verify by reading the existing `build-image` job's body.

**Checkpoint**: US2 complete. After the next `master` push, GHCR shows `:master` and `:sha-<short>`; PRs trigger only the smoke build with no GHCR push.

---

## Phase 5: User Story 3 — Dependabot keeps dependencies fresh and ships patch releases automatically (Priority: P2)

**Goal**: Dependabot opens grouped weekly PRs for gomod / github-actions / docker. Auto-merge handles non-major bumps. A daily scheduled workflow detects accumulated Dependabot commits since the last release tag and cuts a patch release, triggering the standard release flow from US1.

**Independent Test**: Wait for (or seed) a stale dependency. Verify (a) Dependabot opens a PR within a week, (b) on green CI, the auto-merge workflow squash-merges the PR within ~5 minutes, (c) on the next 14:00 UTC tick, `auto-patch-release.yml` runs and either creates `vX.Y.(Z+1)` (if Dependabot commits exist since last tag) or no-ops (if not).

### Implementation for User Story 3

- [X] T018 [P] [US3] Create `.github/dependabot.yml` per `data-model.md` §6 and `research.md` §5. Three ecosystems (`gomod`, `github-actions`, `docker`), each with `directory: "/"`, `schedule.interval: weekly`, `open-pull-requests-limit: 5`, and `groups.all-non-major.update-types: ["minor", "patch"]`. File ends with a single `version: 2` at the top.

- [X] T019 [P] [US3] Create `.github/workflows/dependabot-auto-merge.yml` per `research.md` §6 and FR-017. Reference structure:

  ```yaml
  name: dependabot-auto-merge
  on: pull_request
  permissions:
    pull-requests: write
    contents: write
  jobs:
    auto-merge:
      if: github.event.pull_request.user.login == 'dependabot[bot]'
      runs-on: ubuntu-latest
      steps:
        - id: meta
          uses: dependabot/fetch-metadata@v2
          with:
            github-token: ${{ secrets.GITHUB_TOKEN }}
        - name: Enable auto-merge for non-major bumps
          if: |
            steps.meta.outputs.update-type == 'version-update:semver-minor' ||
            steps.meta.outputs.update-type == 'version-update:semver-patch'
          run: gh pr merge --auto --squash "$PR_URL"
          env:
            PR_URL: ${{ github.event.pull_request.html_url }}
            GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
  ```

  Key safety behaviors:
  - The job-level `if:` filters out non-Dependabot PRs (no other PRs ever go through this workflow).
  - The step-level `if:` filters out major bumps (they stay open for human review per FR-016 + FR-017).
  - `gh pr merge --auto --squash` queues the merge; it does NOT bypass branch protection. CI must still pass.

- [X] T020 [US3] Create `.github/workflows/auto-patch-release.yml` per `research.md` §7, `data-model.md` §5, FR-018. Reference structure:

  ```yaml
  name: auto-patch-release
  on:
    schedule:
      - cron: '0 14 * * *'    # daily at 14:00 UTC ≈ morning America/Toronto
    workflow_dispatch:        # allow manual trigger for testing
  permissions:
    contents: write
  jobs:
    detect-and-tag:
      runs-on: ubuntu-latest
      steps:
        - uses: actions/checkout@v4
          with:
            fetch-depth: 0    # need full history to compute git describe
            ref: master
        - id: detect
          run: |
            LAST_TAG=$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null || echo "")
            if [ -z "$LAST_TAG" ]; then
              echo "no prior release tag; skipping (run 'make release-major' first)"
              echo "should_release=false" >> "$GITHUB_OUTPUT"
              exit 0
            fi
            DEPENDABOT_COMMITS=$(git log "$LAST_TAG..HEAD" --author='dependabot\[bot\]' --pretty=format:'%H' | wc -l)
            if [ "$DEPENDABOT_COMMITS" -eq 0 ]; then
              echo "no dependabot commits since $LAST_TAG; skipping"
              echo "should_release=false" >> "$GITHUB_OUTPUT"
              exit 0
            fi
            echo "found $DEPENDABOT_COMMITS dependabot commit(s) since $LAST_TAG; will release"
            echo "last_tag=$LAST_TAG" >> "$GITHUB_OUTPUT"
            echo "should_release=true" >> "$GITHUB_OUTPUT"
        - name: Compute next patch version and push tag
          if: steps.detect.outputs.should_release == 'true'
          run: |
            . scripts/release-bump.sh
            NEXT=$(bump_patch "${{ steps.detect.outputs.last_tag }}")
            git config user.name 'github-actions[bot]'
            git config user.email '41898282+github-actions[bot]@users.noreply.github.com'
            git tag -a -m "Auto-patch release $NEXT (Dependabot accumulation)" "$NEXT"
            git push origin "$NEXT"
            echo "Pushed $NEXT — release.yml will pick it up"
          env:
            GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
  ```

  Key behaviors:
  - Sources `scripts/release-bump.sh` (created in T006) for the `bump_patch` helper — same logic as `make release-patch` to ensure cross-trigger consistency.
  - Uses `git config user.{name,email}` to set the tagger identity for the annotated tag (the workflow's `${{ secrets.GITHUB_TOKEN }}` cannot create commits as a real user, but it can tag as `github-actions[bot]`).
  - `workflow_dispatch:` trigger lets the maintainer test the workflow manually without waiting for the cron.
  - Pushing the tag triggers `release.yml` (created in T013) automatically.

- [ ] T021 [US3] Manually verify the new workflows by triggering `auto-patch-release.yml` via `workflow_dispatch` against a test branch / fork: confirm it logs the right reason ("no prior release tag" or "no dependabot commits" or "found N dependabot commit(s)"). Skip the actual tag-push if running on a fork or test branch — the dry-run aspect is verifying the detect step's logic, not the push.

**Checkpoint**: US3 complete. The next Dependabot weekly run opens grouped PRs; auto-merge handles non-major; auto-patch-release accumulates Dependabot commits into patch releases on its daily schedule.

---

## Phase 6: User Story 4 — Maintainer can preview and override release commands (Priority: P3)

**Goal**: `DRY_RUN=1` previews any release-* target without git side effects. `make release VERSION=vX.Y.Z` accepts an explicit version for RC tags / hotfixes / version-skip cases.

**Independent Test**: Run `make release-patch DRY_RUN=1` from a clean checkout — verify it prints the next-tag-line and exits 0 with no git operations. Run `make release VERSION=v0.0.1-test-skip-me DRY_RUN=1` — verify it prints `Would create tag: v0.0.1-test-skip-me at <SHA>`. Run `make release VERSION=invalid` — verify it exits 1 with the regex error.

### Implementation for User Story 4

- [X] T022 [US4] Confirm the `DRY_RUN=1` mode is wired into all three `release-{patch,minor,major}` targets from T010. The reference in T010's task body already includes the `DRY_RUN=1` guard. If T010 was implemented without it, retrofit per `contracts/make-targets-contract.md` §"`DRY_RUN=1`" — single line printed to stdout, exit 0, no git operations.

- [X] T023 [US4] Add the `release` target (with `VERSION=` argument) to `Makefile` per `contracts/make-targets-contract.md` §"Target: `make release VERSION=<v>`" and `research.md` §4. Reference body:

  ```makefile
  release:
  	@if [ -z "$(VERSION)" ]; then echo "ERROR: VERSION required (e.g., make release VERSION=v2.0.0)"; exit 1; fi
  	@. scripts/release-bump.sh && \
  		validate_version "$(VERSION)" || (echo "ERROR: VERSION must match vMAJOR.MINOR.PATCH[-PRERELEASE], got: $(VERSION)"; exit 1)
  	@if ! git diff --quiet || ! git diff --cached --quiet; then \
  		echo "ERROR: working tree is dirty; commit or stash first"; exit 1; \
  	fi
  	@CURRENT_BRANCH=$$(git rev-parse --abbrev-ref HEAD); \
  	EXPECTED_BRANCH=$${RELEASE_FROM_BRANCH:-master}; \
  	if [ "$$CURRENT_BRANCH" != "$$EXPECTED_BRANCH" ]; then \
  		echo "ERROR: not on $$EXPECTED_BRANCH (currently on $$CURRENT_BRANCH); set RELEASE_FROM_BRANCH=$$CURRENT_BRANCH if intentional"; exit 1; \
  	fi
  	@if git rev-parse "$(VERSION)" >/dev/null 2>&1 || \
  		[ "$$(git ls-remote --tags origin "refs/tags/$(VERSION)" | wc -l)" -ne 0 ]; then \
  		echo "ERROR: tag $(VERSION) already exists"; exit 1; \
  	fi
  	@if [ "$(DRY_RUN)" = "1" ]; then \
  		echo "Would create tag: $(VERSION) at $$(git rev-parse --short HEAD)"; exit 0; \
  	fi; \
  	git tag -a -m "Release $(VERSION)" "$(VERSION)" && \
  	git push origin "$(VERSION)" && \
  	echo "" && \
  	echo "Pushed $(VERSION) — watch the release workflow at:" && \
  	echo "  https://github.com/$$(git remote get-url origin | sed -E 's#.*github.com[:/]([^/]+/[^/.]+)(\.git)?#\1#')/actions"
  ```

  Add `release` to the `.PHONY` line.

- [X] T024 [US4] Add the help-line entry for `release VERSION=v...` to the `make help` target per FR-021 / `contracts/make-targets-contract.md` §"Help-line contract".

- [X] T025 [US4] Manually verify the explicit-version target locally:
  - `make release VERSION=v0.0.1-test-foo DRY_RUN=1` from clean tree on master → prints the would-create line.
  - `make release VERSION=2.0.0 DRY_RUN=1` → exits 1 with the regex error.
  - `make release VERSION=v1.5 DRY_RUN=1` → exits 1 with the regex error.
  - `make release` (no VERSION) → exits 1 with "VERSION required".
  - `make release VERSION=<an-existing-tag> DRY_RUN=1` → exits 1 with "tag already exists" (locally, since `git rev-parse` finds it).

**Checkpoint**: US4 complete. All four Make targets behave per `contracts/make-targets-contract.md`.

---

## Phase 7: Polish & Cross-Cutting Concerns

**Purpose**: Operator-facing documentation (`RELEASING.md`), final agent-context update, and the end-to-end smoke that closes the feature.

- [ ] T026 [P] Create `/home/maverick/development/honkai-rule-server/RELEASING.md` per FR-020 and `quickstart.md`. Cover (in this order):
  - **TL;DR table** of common operations (mirror `quickstart.md` §TL;DR).
  - **First-time setup** (nothing required; one-time GHCR visibility check).
  - **Cut a release** (patch / minor / major / RC / hotfix backport / dry-run).
  - **Verify a release** (`docker pull`, `docker manifest inspect`, label inspection).
  - **Update the deployment chart** (point at `<your-iac-repo>/charts/honkai-rule-server/values.yaml`).
  - **Dependabot lifecycle** (default behavior, disable auto-patch-release, disable Dependabot, adjusting schedules, stuck PR recovery).
  - **Common questions** (local push, custom release notes, multi-arch, non-release tags, workflow failure recovery, tag deletion, forks).
  - **Troubleshooting** (each Make-target error message → fix).

  Aim for ~200–300 lines (vs. `quickstart.md`'s ~400) — `RELEASING.md` is the day-to-day operator reference; `quickstart.md` stays as the one-time onboarding doc.

- [ ] T027 [P] Verify `CLAUDE.md` was updated by the plan to mark **013** as the active feature with a key-reading bullet pointing at `specs/013-ci-container-release/plan.md`. (Already done during `/speckit-plan`; this task is a verification, not a re-write.) Read `CLAUDE.md` and confirm the new lines are present in both the status block and the key-reading list.

- [ ] T028 End-to-end smoke for US1 + US2 covering both pre-release (SC-003) and stable-release (SC-001 / SC-002 / SC-005) paths. Steps:
  1. From clean `master` checkout, run `make release VERSION=v0.1.0-rc.1` — creates the first pre-release tag at `HEAD`. Watch the run URL printed.
  2. After workflow completes, verify pre-release behavior (SC-003 / FR-011 / FR-011a):
     - `docker manifest inspect ghcr.io/<owner>/honkai-rule-server:v0.1.0-rc.1` returns a valid manifest.
     - `docker manifest inspect ghcr.io/<owner>/honkai-rule-server:v0.1` exits non-zero (tag does NOT exist yet).
     - Same for `:v0` and `:latest` (none exist yet — pre-release MUST NOT advance moving tags).
     - The GitHub Release entry for `v0.1.0-rc.1` is flagged as a pre-release in the GitHub UI (badge: "Pre-release").
  3. From the same `master` HEAD (NOT a new commit — same SHA as the RC), run `make release VERSION=v0.1.0` to cut the stable release. Watch the run URL.
  4. After workflow completes, verify stable-release behavior (SC-001 / SC-002):
     - `docker pull` then `docker manifest inspect` for `:v0.1.0`, `:v0.1`, `:v0`, `:latest` — all four MUST return the same `config.digest`.
     - Capture the layer-digest list for use in T030: `docker buildx imagetools inspect --raw ghcr.io/<owner>/honkai-rule-server:v0.1.0 | jq -r '.layers[].digest' > /tmp/v0.1.0.layers.first.txt`.
     - `docker inspect ghcr.io/<owner>/honkai-rule-server:v0.1.0 --format '{{json .Config.Labels}}' | jq` — all six OCI labels populated per `data-model.md` §3 (`org.opencontainers.image.version` = `v0.1.0`).
     - The GitHub Release entry for `v0.1.0` is NOT flagged as pre-release (badge: "Latest").
  5. `docker run --rm -p 8080:8080 ghcr.io/<owner>/honkai-rule-server:v0.1.0 &` then `curl -fsS http://localhost:8080/health` — confirm the binary serves traffic identically to a local `bin/server` from the same commit.
  6. Visit `https://github.com/<owner>/honkai-rule-server/pkgs/container/honkai-rule-server` — confirm visibility is "Public." Toggle to public if needed (one-time post-deploy step).
  7. Push a no-op commit to `master` (e.g., a typo fix in a comment) — confirm the new `publish-master` job in `ci.yml` runs, advances `:master`, and creates `:sha-<short>` for that commit (SC-005).

- [ ] T029 End-to-end smoke for SC-004 (hotfix-vs-`latest` precedence rule). Steps:
  1. After T028 has shipped `v0.1.0`, push a small no-op commit to `master` (e.g., another comment tweak — production code untouched) so `master` HEAD is one commit ahead of `v0.1.0`.
  2. Run `make release-minor` from clean `master` — creates `v0.2.0`. Watch the run URL.
  3. After workflow completes, verify the new minor release advanced the right moving tags:
     - `:v0.2.0`, `:v0.2`, `:v0`, `:latest` all share one `config.digest` (the new image).
     - `:v0.1` STILL points at `v0.1.0`'s digest — minor-line pin doesn't regress when a higher minor ships. Verify via: `docker manifest inspect ghcr.io/<owner>/honkai-rule-server:v0.1 | jq -r '.config.digest'` matches `:v0.1.0`'s digest, NOT `:v0.2.0`'s.
  4. Now create the hotfix branch from the older release: `git checkout v0.1.0 && git checkout -b hotfix/0.1`.
  5. Make a small no-op commit on the hotfix branch (e.g., a doc tweak — production code untouched).
  6. Run `make release VERSION=v0.1.1 RELEASE_FROM_BRANCH=hotfix/0.1` from the hotfix branch. Watch the run URL.
  7. After workflow completes, verify hotfix behavior (SC-004 — the most subtle correctness check in this feature):
     - `:v0.1.1` digest is the new hotfix image.
     - `:v0.1` digest == `:v0.1.1`'s digest (advanced WITHIN the v0.1 line).
     - `:v0` digest == `:v0.2.0`'s digest (UNCHANGED — `v0.2.0` is still the highest tag in the v0 line).
     - `:latest` digest == `:v0.2.0`'s digest (UNCHANGED — `v0.2.0` is still the highest non-pre-release tag in the repo).
  8. Switch back to `master` (`git checkout master`) so subsequent operator work continues on the main line. The `hotfix/0.1` branch persists locally for any future hotfixes; do NOT push it to origin unless a real ongoing hotfix line is needed.

- [ ] T030 Manual reproducibility verification for SC-010. Steps:
  1. Confirm T028 step 4 captured `/tmp/v0.1.0.layers.first.txt` (the layer-digest list from the FIRST `v0.1.0` workflow run). If missing, capture it now: `docker buildx imagetools inspect --raw ghcr.io/<owner>/honkai-rule-server:v0.1.0 | jq -r '.layers[].digest' > /tmp/v0.1.0.layers.first.txt`.
  2. Navigate to the GitHub Actions run for `v0.1.0` (the `release` workflow). Click "Re-run all jobs" to trigger a second build of the same tag.
  3. Wait for the re-run to complete (~5–7 minutes).
  4. Capture the layer-digest list from the re-built image: `docker buildx imagetools inspect --raw ghcr.io/<owner>/honkai-rule-server:v0.1.0 | jq -r '.layers[].digest' > /tmp/v0.1.0.layers.rerun.txt`.
  5. `diff /tmp/v0.1.0.layers.first.txt /tmp/v0.1.0.layers.rerun.txt` — expected: NO output (layer digests are identical, confirming the binary is bit-for-bit reproducible).
  6. The `config.digest` (image manifest config blob) MAY legitimately differ between runs because `org.opencontainers.image.created` is the build timestamp, which changes per run. Per FR-015 ("differ only in non-essential metadata layers"), this is expected and not a failure.
  7. If layer digests DIFFER between runs: investigate the cause. Most likely sources of nondeterminism: (a) the `golang:1.25-alpine` base image was updated upstream between the two runs (pin to a digest if this matters), (b) a Go module dependency moved (less likely with `go mod download`'s deterministic behavior + `go.sum`), (c) a build-cache miss producing differently-ordered intermediate artifacts (the `cache-from: type=gha` step should keep this stable). Document findings in `quickstart.md`'s Troubleshooting section if a real divergence is found.

- [ ] T031 End-to-end smoke for US3 Dependabot path. Steps:
  1. After `.github/dependabot.yml` lands on master, wait up to 7 days for the first scheduled run (Mondays 06:00 UTC) — or trigger a manual sync via the GitHub UI ("Insights → Dependency graph → Dependabot → Last checked").
  2. When a Dependabot PR opens, verify CI runs and the `dependabot-auto-merge.yml` workflow queues an auto-merge (visible in the PR's Merge button area: "Auto-merge enabled").
  3. After the PR squash-merges to master, confirm `:master` advances on GHCR (US2 path).
  4. On the next 14:00 UTC tick (or via `workflow_dispatch` trigger of `auto-patch-release.yml`), confirm the workflow detects the Dependabot commit and creates `vX.Y.(Z+1)`.
  5. The release workflow (US1 path) picks up the new tag and publishes the patch release. Verify the four-tag GHCR images appear.
  6. End-to-end timing: from "Dependabot opens PR" to "new image at `ghcr.io/<owner>/honkai-rule-server:vX.Y.(Z+1)`" should be ≤ 24 hours plus the workflow run times. SC-009.

- [ ] T032 Update `.specify/feature.json`-pointed feature path to remain at `specs/013-ci-container-release` (already set during `/speckit-specify`). No-op verification — this is a guardrail against accidental drift.

- [ ] T033 Verify the CLAUDE.md update from `/speckit-plan` (T027) explicitly notes that 009's `registry.example.com/library/honkai-rule-server` is now superseded by `ghcr.io/<owner>/honkai-rule-server`. If missing, add the note to the 009 line in CLAUDE.md so future `/speckit-*` runs and human readers see the registry change.

- [ ] T034 Run `make check` to confirm no regression in the existing test gate (vet + staticcheck + tests + snapshot drift). This feature should not change any Go code; `make check` should pass identically to before. If it fails, debug (likely cause: a stray edit in Phase 2's Makefile changes that broke an existing target).

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies — can start immediately.
- **Foundational (Phase 2)**: Depends on Setup completion. BLOCKS US1 + US3 + US4 (all use `scripts/release-bump.sh`).
- **US1 (Phase 3)**: Depends on Foundational. Independent of US2/US3/US4.
- **US2 (Phase 4)**: Depends on Foundational (uses the GHCR-canonical IMAGE_REPO conceptually, though the workflow reads `${{ github.repository_owner }}` directly). Independent of US1/US3/US4.
- **US3 (Phase 5)**: Depends on Foundational AND US1 (the auto-patch-release workflow sources `scripts/release-bump.sh` AND triggers `release.yml` via tag push). If US1 is incomplete, the cron will create tags that no workflow processes.
- **US4 (Phase 6)**: Depends on Foundational AND US1 (the `release VERSION=` target shares the precondition checks with US1's `release-*` targets; `DRY_RUN=1` is a wiring on top of US1's targets).
- **Polish (Phase 7)**: Depends on US1 + US2 + US3 + US4 being complete for the smoke tests. T026 (RELEASING.md) and T027 (CLAUDE.md verify) can run in parallel with anything. Within Phase 7, T028 (RC + stable smoke) is the gate for T029 (hotfix smoke needs `v0.1.0` shipped) and T030 (reproducibility re-run needs the first `v0.1.0` workflow run captured). T031 (Dependabot smoke) is independent of T028/T029/T030. T032 / T033 / T034 are no-op verifications and can run anytime.

### User Story Dependencies

- **US1 (P1)**: Foundational only. MVP standalone.
- **US2 (P1)**: Foundational only. Independent of US1 — if US1 is skipped, US2 still works (master images alone, no tagged releases).
- **US3 (P2)**: Foundational + US1. The auto-patch-release tag-push relies on `release.yml` being live to publish images.
- **US4 (P3)**: Foundational + US1. Extends US1's Make targets.

### Within Each User Story

- Tests (Phase 2's T003/T004 for the foundational shell helpers) MUST be written and FAIL before T006 (the helper implementation).
- Workflow YAML files don't have unit tests in this feature; they're tested by being run on the platform during the Phase 7 smoke.
- Commits after each task or logical group (e.g., one commit for T003+T004 tests-failing, one commit for T006 helper-passing, one commit for T010+T011 Make targets, etc.).

### Parallel Opportunities

Within each phase, tasks marked `[P]` operate on different files:
- **Phase 2**: T003 + T004 are independent (different test files); T008 + T009 are different files (Makefile and .env.example).
- **Phase 3 / US1**: T010 (Makefile changes) and T013 (release.yml) touch different files — can be developed in parallel.
- **Phase 5 / US3**: T018 (dependabot.yml) and T019 (dependabot-auto-merge.yml) touch different files — parallelizable.
- **Phase 7**: T026 (RELEASING.md) and T027 (CLAUDE.md verify) are independent. T031 (Dependabot smoke) runs independent of T028/T029/T030. T032/T033/T034 are independent verifications. T028 → T029 (hotfix needs `v0.1.0`) → T030 (reproducibility re-runs the first `v0.1.0` workflow) is a strict chain — they cannot be parallelized.

Across phases, US1 + US2 can be developed in parallel by two engineers (US1 lands `release.yml`, US2 lands `publish-master` job in `ci.yml`). US3 and US4 must wait for US1.

---

## Parallel Example: Phase 2 (Foundational)

```bash
# Launch the two failing tests in parallel:
sh tests/release/test-bump.sh    # fails: scripts/release-bump.sh not found
sh tests/release/test-regex.sh   # fails: function not found

# Implement scripts/release-bump.sh, then re-run:
sh tests/release/test-bump.sh    # passes
sh tests/release/test-regex.sh   # passes

# Implement Makefile changes (T008) and .env.example changes (T009) in parallel.
```

## Parallel Example: Phase 3 + Phase 4 (US1 + US2 by two engineers)

```bash
# Engineer A on US1 branch:
# - edits Makefile (release-{patch,minor,major} targets)
# - creates .github/workflows/release.yml

# Engineer B on US2 branch:
# - edits .github/workflows/ci.yml (adds publish-master job)

# Both branches can land in parallel; no file conflicts.
```

---

## Implementation Strategy

### MVP First (US1 only)

1. Complete Phase 1 (Setup) — directory creation.
2. Complete Phase 2 (Foundational) — shell helpers + Makefile IMAGE_REPO flip. **CRITICAL**: blocks US1 + US3 + US4.
3. Complete Phase 3 (US1) — Make release targets + release.yml workflow.
4. **STOP and VALIDATE**: cut `v0.1.0` via `make release VERSION=v0.1.0`. Verify the four-tag GHCR images appear and are pullable.
5. MVP shipped: maintainer can now cut releases by hand.

### Incremental Delivery

1. MVP (US1) → ship. Maintainer has manual SemVer release.
2. Add US2 (master images) → master pushes get continuous images. Two-engineer team can split US2 in parallel with US1 since neither blocks the other.
3. Add US4 (DRY_RUN + explicit VERSION) → release UX polish for power users. Optional MVP+1.
4. Add US3 (Dependabot) → hands-off dependency rollups. Last because it depends on US1's release.yml being live.
5. Add Polish (RELEASING.md, smoke tests) → shipping doc + end-to-end validation.

### Parallel Team Strategy

With two developers:
1. Both complete Phase 1 + Phase 2 together (~1 hour pair work).
2. Once Foundational done:
   - **Developer A**: US1 (Phase 3) — Make release targets + release.yml.
   - **Developer B**: US2 (Phase 4) — ci.yml publish-master job.
3. After US1 lands:
   - **Developer A**: US4 (Phase 6) — explicit VERSION target.
   - **Developer B**: US3 (Phase 5) — Dependabot config + auto-merge + auto-patch-release.
4. Both: Phase 7 (Polish) — RELEASING.md + smoke tests.

Total wall-clock estimate: ~1–2 days for two developers, ~3–4 days for one developer.

---

## Notes

- [P] tasks = different files, no dependencies on incomplete tasks.
- [Story] label maps task to specific user story for traceability.
- Each user story should be independently completable and testable.
- Verify shell helpers' tests fail BEFORE implementing T006 (Constitution Principle IV applied even though this isn't Go transformation-core code).
- Commit after each task or logical group (e.g., one commit for "Phase 2 foundational tests + helper", one for "US1 Make targets", one for "US1 release.yml", etc.).
- Stop at any checkpoint to validate the story independently.
- Avoid: vague tasks, same file conflicts, cross-story dependencies that break independence beyond the documented US3→US1 / US4→US1 ordering.
- Avoid pushing real release tags during development — use `DRY_RUN=1` for verification until the smoke test (T028) is intentional.
