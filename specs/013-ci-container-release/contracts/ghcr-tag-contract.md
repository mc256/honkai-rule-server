# Contract: GHCR Tag Publication

**Feature**: [013-ci-container-release](../spec.md)
**Plan**: [plan.md](../plan.md)
**Data model**: [data-model.md](../data-model.md)
**Date**: 2026-05-07

This contract specifies the externally-observable behavior of the CI publish-master job and the release workflow with respect to GHCR tags. It is the authoritative reference for "what tags appear on GHCR for what input."

---

## Image base path

```
ghcr.io/<github.repository_owner>/honkai-rule-server
```

`<github.repository_owner>` is resolved from the `GITHUB_REPOSITORY_OWNER` env var inside workflows, or from `git remote get-url origin` (parsing the GitHub owner segment) inside the Makefile.

---

## Trigger → published tags

### Trigger A: `push` to `master`

**Conditions**:
- Workflow file: `.github/workflows/ci.yml`, job: `publish-master`.
- Job depends on existing `test` job success.
- Runs on `${{ github.ref == 'refs/heads/master' && github.event_name == 'push' }}` only.

**Tags published** (atomic — both or neither):

| Tag | Mutability | Notes |
|---|---|---|
| `master` | moving | Advances on every successful master push |
| `sha-<7short>` | immutable | `<7short>` = first 7 chars of `${{ github.sha }}`; same content always at same tag |

**Same-digest invariant**: `master` and `sha-<7short>` for one push event MUST point at the same digest.

**Failure mode**: if the `test` job fails, the `publish-master` job never runs; no GHCR push. The previous `master` tag continues pointing at the previous successful master push's image.

---

### Trigger B: `pull_request` (any base branch)

**Conditions**:
- Workflow file: `.github/workflows/ci.yml`, existing smoke `docker build` job.
- Smoke build runs unconditionally (validates Dockerfile changes).

**Tags published**: NONE. Smoke build runs `docker build` to validate but does not push.

**Rationale**: PRs may contain unreviewed code; pushing to GHCR before merge would expose unvetted images. The smoke build still catches Dockerfile syntax breaks before merge.

---

### Trigger C: tag push matching `refs/tags/v[0-9]+.[0-9]+.[0-9]+*`

**Conditions**:
- Workflow file: `.github/workflows/release.yml`.
- Trigger: `on.push.tags: ['v[0-9]+.[0-9]+.[0-9]+*']`.
- Workflow first runs the test suite against the tagged commit; on test failure, the build/push step is skipped (FR-009).

**Sub-case C1: tag matches `v<MAJOR>.<MINOR>.<PATCH>` exactly (no pre-release suffix)**:

| Tag | Mutability | Advances? |
|---|---|---|
| `vMAJOR.MINOR.PATCH` | immutable | always (this is the new release) |
| `vMAJOR.MINOR` | moving | yes — within the minor line |
| `vMAJOR` | moving | yes IFF this is the highest non-pre-release tag in `vMAJOR.*` |
| `latest` | moving | yes IFF this is the highest non-pre-release tag in the whole repo |

**Same-digest invariant**: All four tags published in one workflow run MUST point at the same digest.

**Hotfix-vs-`latest` rule** (FR-010 / SC-004): if `v1.4.8` is pushed AFTER `v1.5.0` exists, `v1.4.8` and `v1.4` are published/advanced, but `v1` and `latest` remain pointing at `v1.5.0`'s digest. Implemented by `docker/metadata-action`'s `flavor: latest=auto` + `type=semver` precedence rules.

**Sub-case C2: tag matches `v<MAJOR>.<MINOR>.<PATCH>-<PRERELEASE>`** (e.g., `v1.5.0-rc.1`):

| Tag | Mutability | Advances? |
|---|---|---|
| `vMAJOR.MINOR.PATCH-PRERELEASE` | immutable | yes (this is the new pre-release tag) |
| `vMAJOR.MINOR` | moving | NO |
| `vMAJOR` | moving | NO |
| `latest` | moving | NO |

**Rationale**: pre-release tags MUST NOT advance moving tags (FR-011). A user pinned to `:latest` should never accidentally pull an RC.

---

### Trigger D: tag push NOT matching `refs/tags/v[0-9]+.[0-9]+.[0-9]+*`

**Conditions**: any tag like `demo-2026`, `v2`, `v1.5`, `1.5.0`.

**Tags published**: NONE. The release workflow's trigger filter ignores the tag; no workflow runs.

**Rationale**: maintainers may use non-SemVer tags for archival pinning (e.g., demo branches, snapshot points) without polluting GHCR.

---

## Image labels (per published tag)

Every published image — `master`, `sha-*`, `vX.Y.Z`, etc. — carries six OCI annotation labels per [data-model.md §3](../data-model.md). These are the externally-observable contract for image identity:

```sh
docker inspect --format '{{json .Config.Labels}}' ghcr.io/<owner>/honkai-rule-server:vX.Y.Z
```

returns:

```json
{
  "org.opencontainers.image.source":   "https://github.com/<owner>/honkai-rule-server",
  "org.opencontainers.image.revision": "<full-40-char-sha>",
  "org.opencontainers.image.version":  "vX.Y.Z",
  "org.opencontainers.image.created":  "<rfc3339-timestamp>",
  "org.opencontainers.image.title":    "honkai-rule-server",
  "org.opencontainers.image.licenses": "<spdx-id-or-omitted>"
}
```

For `master` and `sha-*` images, `version` is `master`; all other labels populated identically.

---

## GitHub Release entry (Trigger C only)

For Trigger C1 (non-pre-release tag): `softprops/action-gh-release@v2` creates a GitHub Release with auto-generated commit-list notes. `prerelease: false`.

For Trigger C2 (pre-release tag): same, but `prerelease: true` (so the GitHub UI's "Latest release" badge tracks the highest stable tag, not the RC).

For Triggers A, B, D: no GitHub Release entry.

---

## Authentication & permissions contract

All GHCR pushes use:

```yaml
permissions:
  contents: read
  packages: write
# ...
- uses: docker/login-action@v3
  with:
    registry: ghcr.io
    username: ${{ github.actor }}
    password: ${{ secrets.GITHUB_TOKEN }}
```

`${{ secrets.GITHUB_TOKEN }}` is auto-injected by GitHub Actions; no PAT, no custom secret. The token's lifetime is the workflow run; it expires automatically.

---

## Negative contract (what this feature does NOT do)

- Does NOT publish multi-arch images (linux/amd64 only — FR-014).
- Does NOT sign images (cosign / sigstore out of scope for v1; can be added later).
- Does NOT scan images (Trivy / Grype out of scope for v1).
- Does NOT generate SBOMs (CycloneDX / SPDX out of scope for v1).
- Does NOT manage GHCR package retention (default GHCR retention applies; manual cleanup is operator-driven).
- Does NOT update the downstream Helm chart values (`<your-iac-repo>/charts/honkai-rule-server/values.yaml` is a manual operator step, documented in `quickstart.md`).
