#!/bin/sh
# Test bump_patch / bump_minor / bump_major from scripts/release-bump.sh.
# Run: sh tests/release/test-bump.sh
# Exit non-zero on any failure.

set -u

# Locate the helper relative to this test file (works whether run from repo root or elsewhere).
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

# Assert: function $1 with input $2 produces stdout $3 and exit code $4.
# Usage: assert_bump bump_patch v1.4.7 v1.4.8 0
assert_bump() {
    fn=$1
    input=$2
    expected_stdout=$3
    expected_exit=$4

    actual_stdout=$("$fn" "$input" 2>/dev/null)
    actual_exit=$?

    if [ "$actual_stdout" = "$expected_stdout" ] && [ "$actual_exit" = "$expected_exit" ]; then
        echo "PASS: $fn '$input' -> '$actual_stdout' (exit $actual_exit)"
        PASS_COUNT=$((PASS_COUNT + 1))
    else
        echo "FAIL: $fn '$input'"
        echo "  expected: stdout='$expected_stdout' exit=$expected_exit"
        echo "  actual:   stdout='$actual_stdout' exit=$actual_exit"
        FAIL_COUNT=$((FAIL_COUNT + 1))
    fi
}

# Assert: function $1 with input $2 exits non-zero with stderr containing $3.
assert_bump_fail() {
    fn=$1
    input=$2
    expected_stderr_contains=$3

    actual_stderr=$("$fn" "$input" 2>&1 >/dev/null)
    actual_exit=$?

    if [ "$actual_exit" -ne 0 ] && echo "$actual_stderr" | grep -q "$expected_stderr_contains"; then
        echo "PASS: $fn '$input' -> exit $actual_exit, stderr matches '$expected_stderr_contains'"
        PASS_COUNT=$((PASS_COUNT + 1))
    else
        echo "FAIL: $fn '$input' (expected non-zero exit + stderr containing '$expected_stderr_contains')"
        echo "  actual: exit=$actual_exit stderr='$actual_stderr'"
        FAIL_COUNT=$((FAIL_COUNT + 1))
    fi
}

# bump_patch
assert_bump bump_patch v1.4.7 v1.4.8 0
assert_bump bump_patch v0.0.0 v0.0.1 0
assert_bump bump_patch v10.20.30 v10.20.31 0
assert_bump bump_patch v1.4.7-rc.1 v1.4.8 0
assert_bump_fail bump_patch "" "no prior"

# bump_minor
assert_bump bump_minor v1.4.7 v1.5.0 0
assert_bump bump_minor v0.0.5 v0.1.0 0
assert_bump bump_minor v1.4.7-rc.1 v1.5.0 0
assert_bump_fail bump_minor "" "no prior"

# bump_major
assert_bump bump_major v1.4.7 v2.0.0 0
assert_bump bump_major v0.9.9 v1.0.0 0
assert_bump bump_major v10.20.30 v11.0.0 0
assert_bump bump_major "" v1.0.0 0   # baseline case

echo ""
echo "Total: $((PASS_COUNT + FAIL_COUNT)) | Passed: $PASS_COUNT | Failed: $FAIL_COUNT"
[ "$FAIL_COUNT" -eq 0 ]
