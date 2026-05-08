# Phase 0 Research: CI Container Builds, SemVer Releases via GHCR, Dependabot Auto-Patch

**Feature**: [spec.md](./spec.md)
**Plan**: [plan.md](./plan.md)
**Date**: 2026-05-07

This document records the nine narrow design decisions surfaced during plan generation. Each decision is paired with rationale and rejected alternatives. Reference shell snippets are inline so the eventual Makefile / workflow YAML can be transcribed verbatim.

---

## 1. Action choice for build/push

**Decision**: Use the official `docker/*` GitHub Actions:
- `docker/login-action@v3` — authenticates to GHCR with `${{ secrets.GITHUB_TOKEN }}`.
- `docker/setup-buildx-action@v3` — sets up buildx for layer caching.
- `docker/metadata-action@v5` — generates the multi-tag list and the OCI annotation labels declaratively.
- `docker/build-push-action@v5` — builds + pushes in one step with caching.

For the GitHub Release entry: `softprops/action-gh-release@v2`.
For the Dependabot auto-merge guard: `dependabot/fetch-metadata@v2`.

**Rationale**:
- `docker/metadata-action` natively understands SemVer tag inputs and emits `vMAJOR.MINOR.PATCH`, `vMAJOR.MINOR`, `vMAJOR`, `latest` automatically with the right precedence rules (it has the hotfix-vs-latest logic built in via the `flavor: latest=auto` directive). This eliminates the need for a custom shell script doing `git tag --sort=-v:refname` precedence detection.
- The official actions are the most-maintained path and inherit fixes for new GHCR auth quirks without us having to track them.
- All five actions are themselves Dependabot-tracked via the `github-actions` ecosystem we're enabling — the actions stay current automatically.

**Rejected alternatives**:
- **`goreleaser`** — overkill. It bundles tag-detection, multi-platform build, GitHub Release creation, and registry push in one config-file pipeline, but introduces a binary toolchain and a non-trivial `.goreleaser.yml`. Our flow is "single Go binary, single image, no cross-compilation matrix" — `docker/*` actions are simpler and stay closer to the existing Makefile vocabulary. Goreleaser shines when you have OS package builds (apt, brew, scoop) or signed binaries; we have neither.
- **Custom shell scripts pushing via `crane` or raw `docker push`** — rejected. `docker/build-push-action` handles cache, multi-tag, attestation hooks, and platform fan-out for free; rolling our own would re-implement these poorly.

---

## 2. `latest` precedence detection (and `vMAJOR` precedence)

**Decision**: Delegate to `docker/metadata-action`'s built-in SemVer logic. Its `tags:` input grammar handles this:

```yaml
tags: |
  type=semver,pattern={{version}}
  type=semver,pattern={{major}}.{{minor}}
  type=semver,pattern={{major}}
  type=raw,value=latest,enable={{is_default_branch}}
```

For tag-pushed releases (which is the only case where `latest` semantics matter), the action's `type=semver` patterns automatically:
- Skip moving tags (`{{major}}`, `{{major}}.{{minor}}`, `latest`) when the input is a pre-release tag (e.g., `v1.5.0-rc.1` only emits the exact tag, no movers).
- Optionally use `flavor: latest=auto` to advance `latest` only when the new tag is the highest-precedence non-pre-release tag in the repo. This handles the hotfix-vs-latest rule (FR-010) without custom logic — when `v1.4.8` is pushed after `v1.5.0` exists, `latest` is not advanced.

The `vMAJOR` (e.g., `v1`) advancement is handled the same way: `{{major}}` is emitted only when the new tag is the highest-precedence within its major line.

**Rationale**:
- Putting this logic in the action's input declaration (instead of a shell script) means the workflow YAML is the single source of truth for "which tags get published when." A reviewer reads the YAML and sees the rule.
- The action's `enable=` filter is built precisely for the "advance-only-if-highest" semantics required by FR-010 / SC-004.

**Rejected alternative**: Custom shell `git tag --sort=-v:refname --list 'v*' | grep -v -- '-' | head -1` to detect "is this the highest tag?" Workable but reinvents what the action already does; a future-us debugging the workflow has more to read.

**Reference snippet (release workflow)**:

```yaml
- uses: docker/metadata-action@v5
  id: meta
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
      org.opencontainers.image.created=${{ steps.now.outputs.rfc3339 }}
```

---

## 3. Make-target tag-bump shell

**Decision**: Use POSIX shell + `sed` + `cut` arithmetic. Reference body for `release-patch`:

```makefile
release-patch:
	@if ! git diff --quiet || ! git diff --cached --quiet; then \
		echo "ERROR: working tree is dirty; commit or stash first"; exit 1; \
	fi
	@CURRENT_BRANCH=$$(git rev-parse --abbrev-ref HEAD); \
	EXPECTED_BRANCH=$${RELEASE_FROM_BRANCH:-master}; \
	if [ "$$CURRENT_BRANCH" != "$$EXPECTED_BRANCH" ]; then \
		echo "ERROR: not on $$EXPECTED_BRANCH (currently on $$CURRENT_BRANCH); set RELEASE_FROM_BRANCH=$$CURRENT_BRANCH if intentional"; exit 1; \
	fi
	@LAST=$$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null || true); \
	if [ -z "$$LAST" ]; then \
		echo "ERROR: no prior vX.Y.Z tag found; run 'make release-major' first to create v1.0.0"; exit 1; \
	fi; \
	M=$$(echo $$LAST | sed 's/^v//' | cut -d. -f1); \
	m=$$(echo $$LAST | sed 's/^v//' | cut -d. -f2); \
	p=$$(echo $$LAST | sed 's/^v//' | cut -d. -f3 | cut -d- -f1); \
	NEXT=v$${M}.$${m}.$$((p+1)); \
	if [ "$(DRY_RUN)" = "1" ]; then \
		echo "Would create tag: $$NEXT at $$(git rev-parse --short HEAD)"; exit 0; \
	fi; \
	git tag -a -m "Release $$NEXT" $$NEXT && \
	git push origin $$NEXT && \
	echo "" && \
	echo "Pushed $$NEXT — watch the release workflow at:" && \
	echo "  https://github.com/$$(git remote get-url origin | sed -E 's#.*github.com[:/]([^/]+/[^/.]+)(\.git)?#\1#')/actions"
```

`release-minor`: same skeleton, but `NEXT=v$${M}.$$((m+1)).0`. Drop the "no prior tag" guard's strictness — `release-minor` also requires a prior tag (or operator runs `release-major` first).

`release-major`: same skeleton, but `NEXT=v$$((M+1)).0.0`. If `LAST` is empty, default to `v1.0.0` instead of erroring (FR-012 step 3).

**Rationale**:
- POSIX shell + `sed`/`cut` is portable across maintainer environments (macOS BSD `sed` and GNU `sed` both work for this regex).
- No `awk`, no `python`, no Node — keeps the dependency surface at "anything with a Bourne shell."
- `git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*'` filters out non-SemVer tags (e.g., demo tags) automatically.

**Rejected alternatives**:
- **Bash arrays + `[[ =~ ]]` regex** — works on Linux/macOS bash but not POSIX sh. The Makefile's `@... ; \` continuation runs under `/bin/sh` per `SHELL := /bin/sh` (default). Bash-isms would force a `SHELL := /bin/bash` declaration.
- **Python script in `scripts/release.py`** — pulls a Python toolchain dependency. The shell version is small enough that a script file is over-organization.
- **Use `docker/metadata-action`'s SemVer parser** — that's only available inside a workflow; we need the bump to happen locally on the maintainer's machine (the workflow is triggered BY the tag push, not before it).

**Test for the tag-bump arithmetic**: `tests/release/test-bump.sh` (new) — a shell script asserting:
- `bump_patch v1.4.7` → `v1.4.8`
- `bump_minor v1.4.7` → `v1.5.0`
- `bump_major v1.4.7` → `v2.0.0`
- `bump_patch v1.4.7-rc.1` → `v1.4.8` (pre-release suffix stripped before bump)
- `bump_patch ` (empty) → exit 1
- `bump_major ` (empty) → `v1.0.0` (special baseline case)

The `bump_*` functions are factored out of the Make targets into `scripts/release-bump.sh` so they're sourceable from the test. The Make targets call into them via `. scripts/release-bump.sh && NEXT=$$(bump_patch "$$LAST")`.

---

## 4. Version-regex validation for `release VERSION=`

**Decision**: Validate `VERSION` against `^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$` using `grep -qE`:

```makefile
release:
	@if [ -z "$(VERSION)" ]; then echo "ERROR: VERSION required (e.g., make release VERSION=v2.0.0)"; exit 1; fi
	@if ! echo "$(VERSION)" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$$'; then \
		echo "ERROR: VERSION must match vMAJOR.MINOR.PATCH[-PRERELEASE], got: $(VERSION)"; exit 1; \
	fi
	# … rest of clean-tree / branch / tag-already-exists / push logic …
```

**Rationale**:
- Aligns with FR-013a's exact regex.
- `grep -qE` is in every POSIX system; same dependency surface as #3.
- Loud-fail with the offending input named back (Constitution Principle III applied to the operator UX boundary).

**Rejected alternative**: Skip validation and let `git tag` itself reject malformed names. Rejected because git tag accepts almost anything (e.g., `v-1`, `vfoo`, `1.0.0` without `v`-prefix); we want the convention enforced at the helper layer, not papered over with downstream errors.

**Test** (`tests/release/test-regex.sh`): assert accept = `v1.5.0`, `v1.5.0-rc.1`, `v1.5.0-beta.2`, `v10.20.30`, `v0.0.1-dev.abc123`. Assert reject = `1.5.0`, `v1.5`, `v.1.5.0`, `v1.5.0_rc1`, `v1.5.0+build.1` (build metadata not in scope), empty string.

---

## 5. Dependabot grouping

**Decision**: One group per ecosystem named `all-non-major`, matching `update-types: ["minor", "patch"]`. Reference `.github/dependabot.yml`:

```yaml
version: 2
updates:
  - package-ecosystem: "gomod"
    directory: "/"
    schedule:
      interval: "weekly"
    open-pull-requests-limit: 5
    groups:
      all-non-major:
        update-types: ["minor", "patch"]
  - package-ecosystem: "github-actions"
    directory: "/"
    schedule:
      interval: "weekly"
    open-pull-requests-limit: 5
    groups:
      all-non-major:
        update-types: ["minor", "patch"]
  - package-ecosystem: "docker"
    directory: "/"
    schedule:
      interval: "weekly"
    open-pull-requests-limit: 5
    groups:
      all-non-major:
        update-types: ["minor", "patch"]
```

**Rationale**:
- One PR per ecosystem per week with all minor+patch bumps batched is the cadence the maintainer can review (or auto-merge) without fatigue.
- Major bumps fall outside the group → one PR per major bump → human review (FR-016 + FR-017's skip-on-major guard form the safety contract).
- `open-pull-requests-limit: 5` caps PR floods if a group rule fails (sanity backstop).

**Rejected alternatives**:
- **Single group across all ecosystems** — Dependabot doesn't support cross-ecosystem groups, and even if it did, mixing `gomod` deps with `github-actions` versions in one PR would muddle review.
- **Daily schedule** — too noisy; weekly + grouped + auto-merge is the right cadence for a single-maintainer project.
- **No groups (one PR per dep)** — generates ~10–20 PRs/week as the project ages. Human review devolves into rubber-stamping; auto-merge becomes the actual gate. Better to admit that upfront and group accordingly.

---

## 6. Auto-merge safety on major bumps

**Decision**: `dependabot-auto-merge.yml` uses `dependabot/fetch-metadata@v2` to read the bump's `update-type` and gates the merge step with a conditional. Reference workflow:

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

**Rationale**:
- `dependabot/fetch-metadata` is the official path for reading bump metadata in CI; bypassing it (e.g., parsing the PR title regex-style) is fragile.
- `gh pr merge --auto --squash` queues the merge so it lands once required checks pass — not immediately on workflow run. This is the correct semantics: CI gates the merge.
- The `if:` guard on the job (`user.login == 'dependabot[bot]'`) is the first defense; the inner `if:` on the merge step (filter on `update-type`) is the second. Major bumps fall through to no-op.

**Rejected alternative**: Use `peter-evans/enable-pull-request-automerge` action instead of `gh pr merge --auto`. Both work; the GH CLI form is one less third-party dep. We're already using the CLI elsewhere (in `auto-patch-release.yml`), so consistency wins.

---

## 7. Auto-patch-release commit-author filter

**Decision**: `git log <last-tag>..HEAD --author='dependabot\[bot\]' --pretty=format:'%H'` returns the SHAs of Dependabot commits since the last release tag. Non-empty output → cut the patch tag. Empty → no-op.

Reference workflow body:

```yaml
- name: Detect Dependabot commits since last release
  id: detect
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
```

**Rationale**:
- `--author='dependabot\[bot\]'` is unambiguous: GitHub-Bot-authored commits all carry `dependabot[bot] <support@github.com>` in the author field; no risk of a false positive from a human spoofing the name (committer-vs-author + the merge-squash convention preserves the dependabot author on the squash commit).
- The squash-merge pattern (used by FR-017's auto-merge step) DOES preserve the original PR author as the commit author (verified by GitHub's own docs); the merger is `web-flow` but the author is `dependabot[bot]`.

**Edge case**: if a maintainer manually edits a Dependabot PR before merging (adds a commit, force-pushes), the squash author depends on git's behavior — typically the merger becomes the author. This means a manual-touched Dependabot PR may not trigger auto-patch-release. That's acceptable: manual touches mean human judgment is in the loop; the maintainer can run `make release-patch` themselves.

**Rejected alternative**: Parse commit messages for `build(deps):` prefix. Rejected because (a) PR title is the squash subject only if the maintainer didn't override it, (b) a future Dependabot rename of the prefix would break this silently, (c) author filter is more semantically direct.

---

## 8. GHCR visibility

**Decision**: Public on first push. No workflow control needed.

**Rationale**:
- GitHub's default behavior: a new package created by a public repo's workflow is itself public.
- The repo is open-source ("Initial open-source release" per the recent commit).
- Operators with private mirror needs use the existing Makefile escape hatch: set `IMAGE_REPO=` to their internal registry and run `make docker-push` manually.

**Rejected alternative**: Set the package to private by default and have a one-time toggle. Rejected because (a) the mismatched-default would surprise contributors expecting `docker pull` to "just work," (b) toggling later is one click in the GitHub UI if a security need emerges.

**One-time post-deploy check**: after the first `master` push lands the first GHCR package, the maintainer verifies on `https://github.com/<owner>/honkai-rule-server/pkgs/container/honkai-rule-server` that visibility is "Public." If not, click "Package settings → Change visibility → Public."

---

## 9. Schedule choice

**Decision**:
- **Dependabot** ecosystems: `interval: weekly` (default fires Mondays 06:00 UTC).
- **`auto-patch-release.yml`** cron: `0 14 * * *` (daily at 14:00 UTC ≈ 09:00–10:00 EST/EDT, matching America/Toronto).

**Rationale**:
- Weekly Dependabot lets the maintainer review (or let auto-merge land) Monday-morning batches without daily noise.
- Daily auto-patch-release means a Monday Dependabot batch lands `vX.Y.(Z+1)` by Tuesday at the latest — fast enough to ship security patches promptly, slow enough that a chain of unrelated Dependabot PRs across the week doesn't cut three releases in three days.
- 14:00 UTC = morning America/Toronto = within the maintainer's working hours. If the auto-patch release hits a bug, the maintainer is awake to react. (Compare a midnight-UTC schedule firing at 8 PM EST — late evening, less attention available.)

**Rejected alternatives**:
- **Daily Dependabot + daily auto-patch** — too noisy; maintainer churns on Monday-Friday on each ecosystem's drip.
- **Weekly auto-patch (e.g., Friday 14:00 UTC)** — releases lag dependency landings by up to a week, which is too slow for security drips.
- **On-merge auto-patch (workflow trigger on push to master)** — possible, but then every merged Dependabot batch produces a separate release. With grouped Dependabot rules this is mostly fine, but a multi-ecosystem week (gomod + actions + docker all bumping) would still produce 3 releases. Daily batching collapses these into one.

---

## Summary

All nine decisions are inputs to the eventual `tasks.md` (Phase 2). They're documented here so the implementer can transcribe the reference snippets directly without re-deriving the design choices.

The plan introduces:
- **Zero new Go code** (this is build/CI infra).
- **Zero new abstractions** in the codebase (the transformation core stays untouched).
- **Three new workflow YAMLs**, **one Dependabot config**, **one Make-target group**, **one operator doc**. All in one PR; all reviewable as a unified diff.

Constitutional principles I/IV (transformation core / test-first) bind in their existing form — the release workflow runs the existing test suite as a gate, ensuring no broken transformation ever ships under a release tag. Principle II (determinism) extends naturally to image-build reproducibility (FR-015). Principle III (loud-fail) applies to the Make-target operator boundary (clean-tree, branch, regex, tag-already-exists guards). Principles V (observability) and Routing/Security constraints are non-applicable or strictly observed (no credential surface beyond `${{ secrets.GITHUB_TOKEN }}`).
