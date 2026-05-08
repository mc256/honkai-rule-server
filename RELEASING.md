# Releasing

Day-to-day operator reference for cutting releases, managing GHCR, and the Dependabot auto-patch flow. The deeper rationale lives in `specs/013-ci-container-release/`; this doc is the lookup-while-shipping page.

## TL;DR

| You want to… | Run this |
|---|---|
| Cut a patch release | `make release-patch` |
| Cut a minor release | `make release-minor` |
| Cut a major release | `make release-major` |
| Cut an RC / pre-release | `make release VERSION=v1.5.0-rc.1` |
| Hotfix backport | `make release VERSION=v1.4.8 RELEASE_FROM_BRANCH=hotfix/1.4` |
| Preview without pushing | append `DRY_RUN=1` to any of the above |
| Pull the latest stable image | `docker pull ghcr.io/<owner>/honkai-rule-server:latest` |
| Pin the chart to a release | edit `image.tag: v1.4.8` in `<your-iac-repo>/charts/honkai-rule-server/values.yaml` |
| Disable Dependabot auto-patch | change cron in `.github/workflows/auto-patch-release.yml` to `0 0 31 2 *` |
| Disable Dependabot entirely | delete `.github/dependabot.yml` |

## First-time setup

Nothing required. After this PR merges:

- The `publish-master` job in `.github/workflows/ci.yml` runs on every `master` push and lands `:master` + `:sha-<short>` images on GHCR.
- The first push auto-creates the GHCR package `ghcr.io/<owner>/honkai-rule-server` with public visibility (inherits from the public open-source repo).
- No GitHub-side credential setup. The auto-injected `${{ secrets.GITHUB_TOKEN }}` handles GHCR auth.

**One-time post-deploy check**: visit `https://github.com/<owner>/honkai-rule-server/pkgs/container/honkai-rule-server` and confirm visibility is "Public." If not, click "Package settings → Change visibility → Public."

## Cut a release

### Patch / minor / major

From a clean checkout on `master`:

```sh
make release-patch         # vX.Y.7 → vX.Y.8
make release-minor         # vX.4.7 → vX.5.0
make release-major         # v1.4.7 → v2.0.0
```

If no prior `vX.Y.Z` tag exists, only `release-major` works (creates `v1.0.0` from baseline). `release-patch` and `release-minor` refuse with a clear message.

The make target:
1. Verifies clean tree + on `master`.
2. Computes the next version from `git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*'`.
3. Creates an annotated tag and `git push origin <tag>`.
4. Prints the GitHub Actions run URL.

After ~10 minutes, the release workflow:
1. Runs `go vet`, `staticcheck`, `go test -race`, snapshot drift.
2. Builds the container image.
3. Pushes to `ghcr.io/<owner>/honkai-rule-server` with four tags: `vX.Y.Z`, `vX.Y`, `vX`, `latest`.
4. Creates a GitHub Release entry.

### RC / pre-release

```sh
make release VERSION=v1.5.0-rc.1
```

Only the exact tag lands on GHCR — `vX.Y`, `vX`, and `latest` do NOT advance. The GitHub Release is flagged as a pre-release. Use this to stage an RC for client smoke-testing before cutting the real `v1.5.0` via `make release-minor`.

### Hotfix backport

When a fix is needed for an old line (e.g., `v1.4.x` after `v1.5.0` shipped):

```sh
git checkout v1.4.7
git checkout -b hotfix/1.4
# ... apply fix, commit ...
make release VERSION=v1.4.8 RELEASE_FROM_BRANCH=hotfix/1.4
```

GHCR publishes `v1.4.8` and advances `v1.4`, but `v1` and `latest` remain pointing at `v1.5.0`. Clients pinned to `:v1.4` get the fix; clients on `:latest` are unaffected.

### Dry-run

```sh
make release-patch DRY_RUN=1
# → Would create tag: v1.4.8 at a1b2c3d
# (no git operation, no push)
```

`DRY_RUN=1` works on all four release targets.

## Update the deployment chart

The chart at `<your-iac-repo>/charts/honkai-rule-server/values.yaml` previously pinned:

```yaml
image:
  repository: registry.example.com/library/honkai-rule-server
  tag: a1b2c3d
```

Update to:

```yaml
image:
  repository: ghcr.io/<owner>/honkai-rule-server
  tag: v1.4.8        # or :master, or :sha-<short>, or pin to a digest
```

Argo CD picks up the change on its next sync. For maximum reproducibility, pin the digest:

```yaml
image:
  repository: ghcr.io/<owner>/honkai-rule-server
  digest: "sha256:abcdef…"
```

Find the digest with: `docker manifest inspect ghcr.io/<owner>/honkai-rule-server:v1.4.8 | jq -r '.config.digest'`.

## Verify a release

```sh
# Same digest across all four moving tags?
for t in v1.4.8 v1.4 v1 latest; do
  docker manifest inspect ghcr.io/<owner>/honkai-rule-server:$t | jq -r '.config.digest'
done
# expected: all four lines identical

# Image labels look right?
docker inspect ghcr.io/<owner>/honkai-rule-server:v1.4.8 \
    --format '{{range $k, $v := .Config.Labels}}{{$k}}: {{$v}}{{println}}{{end}}'
# expected: org.opencontainers.image.version = v1.4.8
#           org.opencontainers.image.revision = <full sha of tagged commit>
#           org.opencontainers.image.source = https://github.com/<owner>/honkai-rule-server

# Image runs?
docker run --rm -p 8080:8080 ghcr.io/<owner>/honkai-rule-server:v1.4.8 &
curl -fsS http://localhost:8080/health
```

## Dependabot lifecycle

### Default behavior

- **Mondays 06:00 UTC**: Dependabot opens up to 3 PRs (one per ecosystem: `gomod`, `github-actions`, `docker`), each batching that week's minor + patch bumps.
- **For each Dependabot PR**: existing CI workflow runs.
- **On green CI + non-major bump**: `dependabot-auto-merge.yml` queues an auto-squash-merge. PR lands on `master` once required checks confirm.
- **On red CI**: PR stays open for human attention.
- **On major bump**: PR stays open regardless of CI status (human review required).
- **Daily 14:00 UTC**: `auto-patch-release.yml` checks for Dependabot commits since the last release tag. If found, it cuts `vX.Y.(Z+1)` (triggering the release workflow).

End-to-end: a stale dependency goes from "Dependabot opens PR" → "image at `ghcr.io/<owner>/honkai-rule-server:vX.Y.(Z+1)`" within 24 hours, with zero human keystrokes.

### Disable auto-patch-release (vacation mode)

Edit `.github/workflows/auto-patch-release.yml`:

```yaml
on:
  schedule:
    - cron: '0 0 31 2 *'   # never fires (Feb 31 doesn't exist)
```

Commit and push. Dependabot still opens PRs and auto-merges them; only the auto-tag step pauses.

To re-enable, revert the change.

### Disable Dependabot entirely

Delete `.github/dependabot.yml`. PRs stop opening within 24 hours.

To re-enable, restore the file from git history.

### Adjust the schedule

Edit `.github/workflows/auto-patch-release.yml`'s `cron:` line. The default `0 14 * * *` is daily at 14:00 UTC (≈ morning America/Toronto). Common alternatives: `0 14 * * 1-5` (weekdays only), `0 14 * * 1` (weekly Monday).

### Stuck Dependabot PR

If a PR's CI fails repeatedly:

1. Look at the run logs to identify the breakage.
2. If a quick fix is possible: `gh pr checkout <pr-number>` to check out, fix, push. Note: pushing to a Dependabot branch may break the `dependabot[bot]` author signal for `auto-patch-release.yml`, in which case cut the patch manually with `make release-patch`.
3. If the bump is genuinely incompatible: add the dep to `dependabot.yml`'s `ignore:` block and close the PR.
4. If the bump is breaking and intentional (you're upgrading a major dep on purpose): merge manually after fixing your code, then run `make release-major` or `make release-minor` (NOT auto-patch — major bumps deserve a deliberate version increment).

## Common questions

### "Can I push to GHCR locally?"

Yes — the existing `make docker-push` target works. Set `IMAGE_REPO=` to override the default:

```sh
echo 'IMAGE_REPO=registry.internal.example.com/library/honkai-rule-server' >> .env
make docker-push
```

For ad-hoc pushes to GHCR: `docker login ghcr.io -u <github-username> -p <PAT-with-write:packages>` then `make docker-push`.

### "Can I customize release notes?"

Yes — after the GitHub Release entry is auto-created, click "Edit release" in the GitHub UI and rewrite the body.

### "What about multi-arch (linux/arm64)?"

Out of scope for v1. When the cluster gains arm64 nodes, change `platforms: linux/amd64` to `platforms: linux/amd64,linux/arm64` in `release.yml` and `ci.yml`'s `publish-master` job. Build time roughly doubles.

### "Tag naming conventions"

The release workflow only triggers on tags matching `v[0-9]+.[0-9]+.[0-9]+*`. Non-SemVer tags (e.g., `demo-2026-05-07`) don't trigger any workflow — useful for archival pinning without polluting GHCR.

### "Workflow failed after the tag was pushed"

The tag stays on `origin`. Re-run the failed workflow from the GitHub UI; it'll re-trigger build + push. The make targets do NOT delete tags on workflow failure.

### "Forks?"

Forks at `github.com/forker/honkai-rule-server` publish to `ghcr.io/forker/honkai-rule-server`. The owner is auto-resolved from `${{ github.repository_owner }}`.

## Troubleshooting

| Error | Fix |
|---|---|
| `ERROR: working tree is dirty;` | Commit, stash, or revert local changes. Releases must be cut from clean checkouts. |
| `ERROR: not on master` | Either `git checkout master` or set `RELEASE_FROM_BRANCH=<branch>` to acknowledge the override (used for hotfix branches). |
| `ERROR: no prior vX.Y.Z tag found;` | Run `make release-major` first to create `v1.0.0` from baseline. |
| `ERROR: VERSION must match vMAJOR.MINOR.PATCH[-PRERELEASE]` | Check your `VERSION=` value: must start with `v`, must have all three numeric components, optional `-` pre-release suffix only. |
| `ERROR: VERSION required` | Pass `VERSION=v...` to the `make release` target. |
| `ERROR: tag <X> already exists` | Pick a different version or delete the existing tag (destructive — see below). |
| GHCR push step says "permission denied" | Check the workflow file has `permissions: contents: read; packages: write` at the **job** level (not just workflow level). |
| Dependabot PR didn't auto-merge | Check `dependabot/fetch-metadata`'s output. If `update-type` is `version-update:semver-major`, the workflow correctly skipped — major bumps need human review. |
| `auto-patch-release.yml` ran but didn't cut a tag | Check the workflow logs. Most likely "no dependabot commits since vX.Y.Z" — meaning no Dependabot PR has merged since the last release. |

### Deleting a release tag (destructive)

If you absolutely must delete a tag:

```sh
git tag -d vX.Y.Z
git push origin :refs/tags/vX.Y.Z
```

Anyone who already pulled the tag has a dangling reference. **Better path**: cut a new patch release (`vX.Y.Z+1`) with the fix and let `latest` advance over the broken one.

## Reference

- Spec: `specs/013-ci-container-release/spec.md`
- Plan: `specs/013-ci-container-release/plan.md`
- Contracts:
  - `specs/013-ci-container-release/contracts/ghcr-tag-contract.md` — what GHCR tags appear when
  - `specs/013-ci-container-release/contracts/make-targets-contract.md` — what each Make target does
- Quickstart (longer onboarding doc): `specs/013-ci-container-release/quickstart.md`
