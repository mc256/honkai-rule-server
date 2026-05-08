# Sourceable shell helpers

Shell functions sourced by the project's Makefile and GitHub Actions workflows. These are NOT executable scripts; they are libraries.

## Files

- `release-bump.sh` — SemVer tag-bump helpers (`bump_patch`, `bump_minor`, `bump_major`) and the version-regex validator (`validate_version`). Sourced by `Makefile` (release targets) and `.github/workflows/auto-patch-release.yml`.

## Conventions

- POSIX `sh` only. No bashisms, no GNU-only flags, no Python/Node helpers.
- Functions print exactly one line of output (or nothing) on stdout. Error messages go to stderr.
- Exit code semantics follow `validate_*` (0 = OK, 1 = invalid).
- See `tests/release/` for the unit tests.
