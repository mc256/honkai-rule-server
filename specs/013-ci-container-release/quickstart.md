# Quickstart: Releasing & GHCR Operations

**Feature**: [013-ci-container-release](./spec.md)
**Plan**: [plan.md](./plan.md)
**Audience**: maintainer / operator
**Date**: 2026-05-07

This guide tells you how to cut a release, verify it landed on GHCR, update the deployment chart, and disable / re-enable the Dependabot auto-patch flow. It assumes the implementation from this feature has merged to `master` and the workflows are live.

---

## TL;DR

| You want to… | Run this |
|---|---|
| Cut a patch release | `make release-patch` |
| Cut a minor release | `make release-minor` |
| Cut a major release | `make release-major` |
| Cut an RC | `make release VERSION=v1.5.0-rc.1` |
| Hotfix backport | `make release VERSION=v1.4.8 RELEASE_FROM_BRANCH=hotfix/1.4` |
| Preview without pushing | append `DRY_RUN=1` to any of the above |
| Pull the latest stable image | `docker pull ghcr.io/<owner>/honkai-rule-server:latest` |
| Pin the chart to a release | edit `image.tag: v1.4.8` in `<your-iac-repo>/charts/honkai-rule-server/values.yaml` |
| Disable auto-patch-release | change cron in `.github/workflows/auto-patch-release.yml` to `0 0 31 2 *` |
| Disable Dependabot entirely | delete `.github/dependabot.yml` |

---

## 1. First-time setup

Nothing required on first install. After this PR merges:
- The `publish-master` job in `ci.yml` runs on the merge commit and lands the first `:master` image automatically.
- The first time `master` push happens, GHCR auto-creates the package `ghcr.io/<owner>/honkai-rule-server` with public visibility (inherits from the public open-source repo).
- No GitHub-side credential configuration. The auto-injected `${{ secrets.GITHUB_TOKEN }}` handles GHCR auth.

**One-time post-deploy check**: visit `https://github.com/<owner>/honkai-rule-server/pkgs/container/honkai-rule-server` and confirm visibility is "Public." If not, click "Package settings → Change visibility → Public."

---

## 2. Cut a release

### Patch release

From a clean checkout on `master`:

```sh
make release-patch
```

This computes the next patch version from the most recent `vX.Y.Z` tag, creates an annotated git tag, pushes it to `origin`, and prints the URL of the GitHub Actions release run. Wait ~10 minutes; the release workflow:

1. Checks out the tagged commit.
2. Runs the existing test suite (`go vet`, `staticcheck`, `go test -race`, snapshot drift).
3. Builds the container image.
4. Pushes to `ghcr.io/<owner>/honkai-rule-server` with four tags: `vX.Y.(Z+1)`, `vX.Y`, `vX`, `latest`.
5. Creates a GitHub Release entry with auto-generated commit-list notes.

Verify:

```sh
docker pull ghcr.io/<owner>/honkai-rule-server:vX.Y.(Z+1)
docker inspect --format '{{json .Config.Labels}}' \
    ghcr.io/<owner>/honkai-rule-server:vX.Y.(Z+1) \
    | jq '."org.opencontainers.image.version"'
# expected: "vX.Y.(Z+1)"
```

### Minor / major release

```sh
make release-minor       # v1.4.7 → v1.5.0
make release-major       # v1.4.7 → v2.0.0
```

If no prior `vX.Y.Z` tag exists in the repo, only `release-major` works (creates `v1.0.0` from baseline). `release-patch` and `release-minor` refuse with a clear message.

### RC / pre-release tag

```sh
make release VERSION=v1.5.0-rc.1
```

Only `v1.5.0-rc.1` lands on GHCR — `v1.5.0`, `v1.5`, `v1`, and `latest` are not advanced. The GitHub Release is flagged as a pre-release.

Use this to stage an RC for client smoke-testing before cutting the real `v1.5.0` via `make release-minor`.

### Hotfix backport

When a hotfix is needed for an old line (e.g., `v1.4.x` after `v1.5.0` has shipped):

```sh
git checkout v1.4.7
git checkout -b hotfix/1.4
# ... apply fix, commit ...
make release VERSION=v1.4.8 RELEASE_FROM_BRANCH=hotfix/1.4
```

GHCR will publish `v1.4.8` and advance `v1.4`, but `v1` and `latest` will remain pointing at `v1.5.0`. Clients pinned to `:v1.4` get the fix; clients on `:latest` are unaffected.

### Dry-run

```sh
make release-patch DRY_RUN=1
# → Would create tag: v1.4.8 at a1b2c3d
# (no git operation, no push)
```

---

## 3. Update the deployment chart

The 009 cluster-deploy chart at `<your-iac-repo>/charts/honkai-rule-server/values.yaml` previously pinned:

```yaml
image:
  repository: registry.example.com/library/honkai-rule-server
  tag: a1b2c3d
```

Update to:

```yaml
image:
  repository: ghcr.io/<owner>/honkai-rule-server
  tag: v1.4.8        # or :master, or a sha-* tag, or a pinned digest
```

Argo CD picks up the change on its next sync. For maximum reproducibility, pin the digest instead of a tag:

```yaml
image:
  repository: ghcr.io/<owner>/honkai-rule-server
  digest: "sha256:abcdef…"
```

Find the digest with: `docker manifest inspect ghcr.io/<owner>/honkai-rule-server:v1.4.8 | jq -r '.config.digest'`.

---

## 4. Verify a release is correct

After the release workflow completes:

```sh
# Same digest across all four moving tags?
for t in v1.4.8 v1.4 v1 latest; do
  docker manifest inspect ghcr.io/<owner>/honkai-rule-server:$t | jq -r '.config.digest'
done
# expected: all four lines identical

# Image labels look right?
docker pull ghcr.io/<owner>/honkai-rule-server:v1.4.8
docker inspect ghcr.io/<owner>/honkai-rule-server:v1.4.8 \
    --format '{{range $k, $v := .Config.Labels}}{{$k}}: {{$v}}{{println}}{{end}}'
# expected: org.opencontainers.image.version = v1.4.8
#           org.opencontainers.image.revision = <full sha of tagged commit>
#           org.opencontainers.image.source = https://github.com/<owner>/honkai-rule-server

# Image runs and serves traffic identically to local bin/server?
docker run --rm -p 8080:8080 ghcr.io/<owner>/honkai-rule-server:v1.4.8 &
curl -fsS http://localhost:8080/health
```

---

## 5. Dependabot lifecycle

### Default behavior (after this PR merges)

- Mondays 06:00 UTC: Dependabot opens up to 3 PRs (one per ecosystem: gomod, github-actions, docker), each batching that week's minor + patch bumps.
- For each Dependabot PR: existing CI workflow runs.
- On green CI + non-major bump: `dependabot-auto-merge.yml` queues an auto-squash-merge. PR lands on `master` once required checks confirm.
- On red CI: PR stays open for human attention.
- On major bump: PR stays open regardless of CI status (human review required).
- Daily 14:00 UTC: `auto-patch-release.yml` checks for Dependabot commits since the last release tag. If found, it cuts `vX.Y.(Z+1)` (triggering the release workflow per §2).

End-to-end: a stale dependency goes from "Dependabot opens PR" → "image at `ghcr.io/<owner>/honkai-rule-server:vX.Y.(Z+1)`" within 24 hours, with zero human keystrokes.

### Disabling auto-patch-release (going on vacation, want to manually pace releases)

Edit `.github/workflows/auto-patch-release.yml`:

```yaml
on:
  schedule:
    - cron: '0 0 31 2 *'    # never fires (Feb 31 doesn't exist)
```

Commit and push. Dependabot still opens PRs and auto-merges them; only the auto-tag step pauses.

To re-enable, revert the change.

### Disabling Dependabot entirely

Delete `.github/dependabot.yml`. PRs stop opening within 24 hours.

To re-enable, restore the file from git history.

### Adjusting the schedule

Edit `.github/workflows/auto-patch-release.yml`'s `cron:` line. The default `0 14 * * *` is daily at 14:00 UTC (≈ morning America/Toronto). Adjust as needed; `0 14 * * 1-5` (weekdays only) is a common alternative.

### Stuck Dependabot PR

If a PR's CI fails repeatedly:

1. Look at the run logs to identify the breakage.
2. If a quick fix is possible: push commits to the Dependabot branch (`gh pr checkout <pr-number>` to check out, fix, push). Note: pushing a commit to a Dependabot branch may break the `dependabot[bot]` author signal for `auto-patch-release.yml`, in which case the maintainer cuts the patch release manually after merge.
3. If the bump is genuinely incompatible: add the dep to `.github/dependabot.yml`'s `ignore:` block and close the PR.
4. If the bump is breaking and intentional (e.g., you're upgrading a major dep on purpose): merge manually after fixing your code, then run `make release-major` or `make release-minor` (NOT auto-patch — major bumps deserve a deliberate version increment).

---

## 6. Common questions

### "Can I push to GHCR locally?"

Yes — the existing `make docker-push` target works as before. Set `IMAGE_REPO=` to override the default:

```sh
echo 'IMAGE_REPO=registry.internal.example.com/library/honkai-rule-server' >> .env
make docker-push
```

You'll need to be logged into the target registry (`docker login registry.internal.example.com`).

For ad-hoc local pushes to GHCR (e.g., testing a workflow change before merging it): `docker login ghcr.io -u <your-github-username> -p <a-PAT-with-write:packages>` then `make docker-push`. Note that this uses your personal credentials, not the workflow's `GITHUB_TOKEN`.

### "Can I customize the release notes?"

Yes — after the GitHub Release entry is auto-created by the workflow, click "Edit release" in the GitHub UI and rewrite the body. The auto-generated commit list is a starting point.

### "What about multi-arch (linux/arm64)?"

Out of scope for v1 (FR-014). When the cluster gains arm64 nodes, change `platforms: linux/amd64` to `platforms: linux/amd64,linux/arm64` in `release.yml` and `ci.yml`'s `publish-master` job. Build time roughly doubles; everything else is identical.

### "How do I tag a non-release commit (e.g., for a demo branch)?"

Use a non-SemVer tag: `git tag demo-2026-05-07 && git push origin demo-2026-05-07`. The release workflow's trigger filter ignores it; no GHCR push happens.

### "What if the release workflow fails after the tag is already pushed?"

The tag stays on `origin`. Re-run the failed workflow from the GitHub UI; it'll re-trigger the build and push. The make targets do NOT delete tags on workflow failure — that would be destructive.

### "Can I delete a release tag if I made a mistake?"

You can, but it's destructive: `git tag -d <tag>` locally and `git push origin :refs/tags/<tag>` remotely. Anyone who already pulled the tag has a dangling reference. Better path: cut a new patch release (e.g., `vX.Y.Z+1`) with the fix and let `latest` advance over the broken one.

### "Does this work for forks?"

Forks inherit the workflows and Dependabot config but use their own `<owner>` namespace on GHCR. A fork at `github.com/forker/honkai-rule-server` publishes images at `ghcr.io/forker/honkai-rule-server`. The `<owner>` is auto-resolved from `${{ github.repository_owner }}`.

---

## 7. Troubleshooting

### `make release-patch` reports "no prior vX.Y.Z tag found"

You haven't cut any release yet. Run `make release-major` first to create `v1.0.0` from baseline.

### `make release-patch` reports "working tree is dirty"

Commit, stash, or revert your local changes. Releases must be cut from clean checkouts so the resulting tag is reproducible.

### `make release-patch` reports "not on master"

You're on a branch other than `master`. Either checkout `master` or set `RELEASE_FROM_BRANCH=<branch>` to acknowledge the override.

### Release workflow run failed

Open the run URL printed by the make target, identify the failing step, fix the underlying issue, and re-run the workflow from the GitHub UI ("Re-run all jobs"). The tag is already on origin and persists across retries.

### GHCR push step says "permission denied"

Check the workflow file has:

```yaml
permissions:
  contents: read
  packages: write
```

at job level (not just workflow level — explicit job-level permissions are required for `GITHUB_TOKEN` to have GHCR write scope).

### Dependabot PR didn't auto-merge

Check `dependabot/fetch-metadata`'s output in the workflow logs. If `update-type` is `version-update:semver-major`, the workflow correctly skipped the merge — major bumps need human review. Merge manually after reviewing.

### `auto-patch-release.yml` ran but didn't cut a tag

Check the workflow logs. The most common reason is "no dependabot commits since vX.Y.Z" — meaning no Dependabot PR has merged since the last release. The workflow only cuts patch releases when there are accumulated Dependabot bumps to ship.

### A Dependabot commit's author isn't `dependabot[bot]`

Probably someone (you?) pushed a commit to the Dependabot branch before merge. The squash-merge author is then the maintainer, not Dependabot, and `auto-patch-release.yml`'s author filter misses it. Cut the patch manually with `make release-patch`.

---

## 8. Reference

- Spec: [spec.md](./spec.md) — what this feature does
- Plan: [plan.md](./plan.md) — how it's structured
- Research: [research.md](./research.md) — why each design choice was made
- Data model: [data-model.md](./data-model.md) — operations data shape
- Contracts:
  - [contracts/ghcr-tag-contract.md](./contracts/ghcr-tag-contract.md) — what GHCR tags appear when
  - [contracts/make-targets-contract.md](./contracts/make-targets-contract.md) — what each Make target does
- Constitution: [.specify/memory/constitution.md](../../.specify/memory/constitution.md)

For ongoing operations, the canonical operator doc is `RELEASING.md` at the repo root (created by this feature; mirrors §1–§7 of this quickstart in a tighter form).
