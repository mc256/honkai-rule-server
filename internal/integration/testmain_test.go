// Package integration holds cross-package integration tests + snapshots.
// Test files live alongside this one; testdata/fixtures and testdata/snapshots
// hold the inputs and committed snapshot baselines respectively.
package integration

import (
	"os"
	"testing"
)

// fixturesDir resolves to internal/integration/testdata/fixtures from any test
// in this package; Go runs tests with the package directory as CWD.
const fixturesDir = "testdata/fixtures"

func TestMain(m *testing.M) {
	// Currently a passthrough; reserved for future global setup
	// (e.g., enabling pprof, setting GOMAXPROCS for race-detector runs).
	os.Exit(m.Run())
}
