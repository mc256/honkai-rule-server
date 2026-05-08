#!/bin/sh
# Test validate_version from scripts/release-bump.sh.
# Run: sh tests/release/test-regex.sh
# Exit non-zero on any failure.

set -u

SCRIPT_DIR=$(dirname "$0")
HELPER="$SCRIPT_DIR/../../scripts/release-bump.sh"

if [ ! -f "$HELPER" ]; then
    echo "FAIL: helper not found at $HELPER"
    exit 1
fi

# shellcheck source=../../scripts/release-bump.sh
. "$HELPER"

PASS_COUNT=0
FAIL_COUNT=0

assert_accept() {
    candidate=$1
    if validate_version "$candidate" 2>/dev/null; then
        echo "PASS: validate_version '$candidate' -> accept"
        PASS_COUNT=$((PASS_COUNT + 1))
    else
        echo "FAIL: validate_version '$candidate' was rejected (expected accept)"
        FAIL_COUNT=$((FAIL_COUNT + 1))
    fi
}

assert_reject() {
    candidate=$1
    if validate_version "$candidate" 2>/dev/null; then
        echo "FAIL: validate_version '$candidate' was accepted (expected reject)"
        FAIL_COUNT=$((FAIL_COUNT + 1))
    else
        echo "PASS: validate_version '$candidate' -> reject"
        PASS_COUNT=$((PASS_COUNT + 1))
    fi
}

# Accept cases
assert_accept v1.5.0
assert_accept v0.0.1
assert_accept v10.20.30
assert_accept v1.5.0-rc.1
assert_accept v1.5.0-beta.2
assert_accept v0.0.1-dev.abc123
assert_accept v1.0.0-alpha
assert_accept v1.0.0-rc.1.2.3

# Reject cases
assert_reject 1.5.0          # missing v
assert_reject v1.5           # missing patch
assert_reject v1             # missing minor + patch
assert_reject v.1.5.0        # extra dot after v
assert_reject v1.5.0_rc1     # underscore not in regex
assert_reject v1.5.0+build.1 # build metadata not in scope
assert_reject ""             # empty
assert_reject vfoo           # non-numeric
assert_reject v-1.0.0        # negative major
assert_reject v1.5.0-        # empty pre-release
assert_reject "v1.5.0 rc1"   # space in middle

echo ""
echo "Total: $((PASS_COUNT + FAIL_COUNT)) | Passed: $PASS_COUNT | Failed: $FAIL_COUNT"
[ "$FAIL_COUNT" -eq 0 ]
