# Release-helper shell tests

Tests for the SemVer bump arithmetic and version-regex validator in `scripts/release-bump.sh`. The helpers back the `make release-{patch,minor,major}` and `make release VERSION=` targets (feature 013); the workflow `auto-patch-release.yml` also sources them.

## Run

```sh
sh tests/release/test-bump.sh
sh tests/release/test-regex.sh
```

Each script prints `PASS` / `FAIL` per test case and exits non-zero on any failure.

## What's covered

- `test-bump.sh` — `bump_patch`, `bump_minor`, `bump_major` against valid / pre-release / empty inputs (per `specs/013-ci-container-release/research.md` §3).
- `test-regex.sh` — `validate_version` accept / reject cases for the SemVer regex `^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$` (per `specs/013-ci-container-release/research.md` §4).

The tests are pure POSIX `sh`; no test framework, no dependencies beyond `grep`, `sed`, `cut`.
