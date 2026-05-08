#!/bin/sh
# Sourceable shell helpers for SemVer release tagging.
# Used by Makefile (release-{patch,minor,major} + release VERSION=) and
# .github/workflows/auto-patch-release.yml.
#
# All functions accept a tag name like vMAJOR.MINOR.PATCH[-PRERELEASE] and
# print the next tag on stdout. Errors go to stderr; exit code is 0 on
# success, 1 on failure.

# Strip the leading 'v' and any trailing pre-release suffix from a tag, then
# print MAJOR, MINOR, PATCH on stdout (one per line). Used internally.
_split_tag() {
    raw=$(echo "$1" | sed 's/^v//' | cut -d- -f1)
    echo "$raw" | cut -d. -f1
    echo "$raw" | cut -d. -f2
    echo "$raw" | cut -d. -f3
}

# bump_patch <last-tag>: print vM.m.(p+1).
# Empty input → exit 1 with "ERROR: no prior...".
bump_patch() {
    if [ -z "${1:-}" ]; then
        echo "ERROR: no prior vX.Y.Z tag found; run 'make release-major' first to create v1.0.0" >&2
        return 1
    fi
    set -- $(_split_tag "$1")
    M=$1; m=$2; p=$3
    echo "v${M}.${m}.$((p + 1))"
}

# bump_minor <last-tag>: print vM.(m+1).0.
# Empty input → exit 1 with "ERROR: no prior...".
bump_minor() {
    if [ -z "${1:-}" ]; then
        echo "ERROR: no prior vX.Y.Z tag found; run 'make release-major' first to create v1.0.0" >&2
        return 1
    fi
    set -- $(_split_tag "$1")
    M=$1; m=$2
    echo "v${M}.$((m + 1)).0"
}

# bump_major <last-tag>: print v(M+1).0.0.
# Empty input → print v1.0.0 (baseline case).
bump_major() {
    if [ -z "${1:-}" ]; then
        echo "v1.0.0"
        return 0
    fi
    set -- $(_split_tag "$1")
    M=$1
    echo "v$((M + 1)).0.0"
}

# validate_version <candidate>: exit 0 if matches vMAJOR.MINOR.PATCH[-PRERELEASE], 1 otherwise.
validate_version() {
    candidate=${1:-}
    echo "$candidate" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.][A-Za-z0-9.-]*)?$'
}
