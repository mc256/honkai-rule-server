# Contract: Make Release Targets

**Feature**: [013-ci-container-release](../spec.md)
**Plan**: [plan.md](../plan.md)
**Data model**: [data-model.md](../data-model.md)
**Date**: 2026-05-07

This contract specifies the externally-observable behavior of the four new Make targets: `release-patch`, `release-minor`, `release-major`, and `release VERSION=`. It is the authoritative reference for "what each target does, what it requires, and how it fails."

---

## Common preconditions (all four targets)

Every release target enforces these checks in order. The first failed check exits non-zero with a stable error message; later checks do not run.

| # | Check | Failure message prefix |
|---|---|---|
| 1 | Working tree is clean (`git diff --quiet && git diff --cached --quiet`) | `ERROR: working tree is dirty;` |
| 2 | Current branch == `master`, or `RELEASE_FROM_BRANCH=<branch>` is explicitly set and matches | `ERROR: not on master` |
| 3 | (For `release-patch`/`release-minor` only) prior `vX.Y.Z` tag exists | `ERROR: no prior vX.Y.Z tag found;` |
| 4 | (For `release VERSION=` only) `VERSION` is non-empty | `ERROR: VERSION required` |
| 5 | (For `release VERSION=` only) `VERSION` matches `^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$` | `ERROR: VERSION must match` |
| 6 | (For `release VERSION=` only) `VERSION` tag does not exist locally or on origin | `ERROR: tag <VERSION> already exists` |

Failure exit code: non-zero (typically `1`). Success exit code: `0`.

The error-message prefixes are stable: scripts / CI harnesses / future test scripts MAY pattern-match them.

---

## Target: `make release-patch`

**Inputs**:
- `DRY_RUN` (env-style; optional; default unset). Set to `1` to preview only.
- `RELEASE_FROM_BRANCH` (env-style; optional; default `master`). Override the branch check.

**Computed**:
- `LAST` = `git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null` (most recent SemVer tag).
- Strip `v` prefix and `-PRERELEASE` suffix, parse `(M, m, p)`.
- `NEXT = vM.m.(p+1)`.

**Side effects**:
- `DRY_RUN=1` mode: prints exactly one line to stdout: `Would create tag: <NEXT> at <SHORT_SHA>`. No git operation. Exit 0.
- Default mode:
  1. `git tag -a -m "Release <NEXT>" <NEXT>` (annotated tag at `HEAD`).
  2. `git push origin <NEXT>`.
  3. Print:
     ```
     Pushed <NEXT> — watch the release workflow at:
       https://github.com/<owner>/honkai-rule-server/actions
     ```
  4. Exit 0.

**Postconditions**:
- Tag `<NEXT>` exists locally and on `origin`.
- The `release.yml` workflow has been triggered (the make target does not wait for it).

**Examples**:

```sh
# Most recent tag is v1.4.7
$ make release-patch
Pushed v1.4.8 — watch the release workflow at:
  https://github.com/<owner>/honkai-rule-server/actions

$ make release-patch DRY_RUN=1
Would create tag: v1.4.8 at a1b2c3d

# No prior tag in the repo
$ make release-patch
ERROR: no prior vX.Y.Z tag found; run 'make release-major' first to create v1.0.0

# Dirty tree
$ make release-patch
ERROR: working tree is dirty; commit or stash first

# Wrong branch
$ git checkout feature/xyz && make release-patch
ERROR: not on master (currently on feature/xyz); set RELEASE_FROM_BRANCH=feature/xyz if intentional
```

---

## Target: `make release-minor`

Same contract as `release-patch` except:
- Computed: `NEXT = vM.(m+1).0` (resets PATCH to 0).

**Examples**:

```sh
# Most recent tag is v1.4.7
$ make release-minor
Pushed v1.5.0 — watch the release workflow at: ...
```

---

## Target: `make release-major`

Same contract as `release-patch` except:
- Computed: `NEXT = v(M+1).0.0` if `LAST` is non-empty, else `v1.0.0` (baseline).
- Precondition #3 (prior tag exists) is RELAXED: an empty `LAST` is allowed; the target creates `v1.0.0` from baseline.

**Examples**:

```sh
# No prior tag
$ make release-major
Pushed v1.0.0 — watch the release workflow at: ...

# Most recent tag is v1.4.7
$ make release-major
Pushed v2.0.0 — watch the release workflow at: ...
```

---

## Target: `make release VERSION=<v>`

**Inputs** (positional or env-style):
- `VERSION` (required; e.g., `VERSION=v2.0.0` or `VERSION=v1.5.0-rc.1`).
- `DRY_RUN` (optional).
- `RELEASE_FROM_BRANCH` (optional).

**Validation**:
- Preconditions 1, 2, 4, 5, 6 from the common list (skips 3 — explicit `VERSION` does not require a prior tag).

**Side effects**:
- `DRY_RUN=1` mode: prints `Would create tag: <VERSION> at <SHORT_SHA>`. Exit 0.
- Default mode:
  1. `git tag -a -m "Release <VERSION>" <VERSION>` at `HEAD`.
  2. `git push origin <VERSION>`.
  3. Print run URL.
  4. Exit 0.

**Use cases**:
- **Skip-ahead**: `make release VERSION=v2.0.0` after `v1.9.42` (skip `v1.10.0`+).
- **RC tag**: `make release VERSION=v1.5.0-rc.1`.
- **Hotfix backport**: `make release VERSION=v1.4.8 RELEASE_FROM_BRANCH=hotfix/1.4` (from a hotfix branch checkout, after the fix commit lands there).

**Examples**:

```sh
$ make release VERSION=v2.0.0
Pushed v2.0.0 — watch the release workflow at: ...

$ make release VERSION=v2.0.0 DRY_RUN=1
Would create tag: v2.0.0 at a1b2c3d

$ make release VERSION=2.0.0
ERROR: VERSION must match vMAJOR.MINOR.PATCH[-PRERELEASE], got: 2.0.0

$ make release VERSION=v1.5
ERROR: VERSION must match vMAJOR.MINOR.PATCH[-PRERELEASE], got: v1.5

$ make release
ERROR: VERSION required (e.g., make release VERSION=v2.0.0)

$ make release VERSION=v1.4.7    # tag already exists
ERROR: tag v1.4.7 already exists
```

---

## Negative contract (what these targets do NOT do)

- Do NOT delete tags (force-push to a tag is destructive; not part of this feature's flow).
- Do NOT update the Makefile-resident `IMAGE_TAG` variable (it remains the SHA-derived dev tag for `make docker-push` use).
- Do NOT push images directly (image push happens in the workflow triggered by the tag push).
- Do NOT build images locally (no `docker build` step inside any release target).
- Do NOT update CHANGELOG.md or any release notes file (release notes are auto-generated by `softprops/action-gh-release`).
- Do NOT update the downstream Helm chart values (`<your-iac-repo>/charts/honkai-rule-server/values.yaml` is a separate operator step).
- Do NOT wait for the workflow to complete (synchronous waiting is `gh run watch <id>`, which the operator may run separately).

---

## Help-line contract (FR-021)

`make help` MUST list these new targets. Reference output:

```
release-patch         Cut a patch release (vX.Y.(Z+1)) from the most recent tag
release-minor         Cut a minor release (vX.(Y+1).0) from the most recent tag
release-major         Cut a major release (v(X+1).0.0); creates v1.0.0 if no prior tag
release VERSION=v...  Cut an explicit-version release (RC / hotfix / skip-ahead)
                      Use DRY_RUN=1 to preview without creating/pushing the tag
                      Use RELEASE_FROM_BRANCH=<branch> for hotfix branches
```

Format matches the existing `make help` output's column-aligned target/description style.
