package integration

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// updateSnapshots is set via the env var UPDATE_SNAPSHOTS=true (matches
// cupaloy convention) so snapshot baselines can be regenerated explicitly
// without changing test code.
var updateSnapshots = flag.Bool("update", false, "rewrite snapshot baselines")

func init() {
	if v := os.Getenv("UPDATE_SNAPSHOTS"); v == "1" || strings.EqualFold(v, "true") {
		*updateSnapshots = true
	}
}

const snapshotsDir = "testdata/snapshots"

// TC-S-01: snapshot the served-config body produced by the full pipeline
// against the committed real fixtures + a fixed FakeClock.
//
// Drift fails the test. Update with: `UPDATE_SNAPSHOTS=true go test ./internal/integration/ -run TestSnapshot_ServedConfig`.
// Per the Constitution snapshot-stability gate, snapshot updates require an
// explicit reviewer sign-off in the PR.
func TestSnapshot_ServedConfig(t *testing.T) {
	tc := newTestCluster(t)

	// Build the merged config directly through the pipeline (skipping the
	// HTTP layer) so the snapshot is independent of header set / status
	// code formatting and only captures the merged body. This is also
	// faster than going through HTTP for 100K+ bytes of output.
	mc, err := tc.pipeline.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	rendered, err := tc.adapter.Render(mc)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	// Sanity check: the body parses as Clash YAML (so a corrupted snapshot
	// can't pass even if drift detection misses a structural change).
	var probe map[string]any
	if err := yaml.Unmarshal(rendered.Body, &probe); err != nil {
		t.Fatalf("rendered body does not parse: %v", err)
	}

	snapshotPath := filepath.Join(snapshotsDir, "served-config.snap.yaml")
	compareOrUpdate(t, snapshotPath, rendered.Body)
}

// TC-S-03: snapshot the /health JSON body produced from a fixed FakeClock +
// fixed per-source Subscription-Userinfo headers. This pins the entire
// /health response shape (per-source state + daily-allowance triple) so any
// drift in the contract or aggregation surfaces in PR review.
//
// Drift fails the test. Update with: `UPDATE_SNAPSHOTS=true go test ./internal/integration/ -run TestSnapshot_Health`.
func TestSnapshot_Health(t *testing.T) {
	now := mustTime("2026-04-30T00:00:00Z")
	opts := defaultOpts()
	opts.clockNow = now
	opts.perSourceUserinfo = map[string]string{
		// alpha: 30d expiry, 150GB remaining → 5GB/day
		"alpha":      fmt.Sprintf("upload=10737418240; download=42949672960; total=214748364800; expire=%d", now.Add(30*24*time.Hour).Unix()),
		// beta: 5d expiry, 80GB remaining → 16GB/day
		"beta": fmt.Sprintf("upload=5368709120; download=16106127360; total=107374182400; expire=%d", now.Add(5*24*time.Hour).Unix()),
	}
	tc := newTestClusterWithOpts(t, opts)

	resp, err := http.Get(tc.URL() + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	// Sanity: parses as JSON, is the documented shape, status 200.
	var probe map[string]any
	if err := json.Unmarshal(body, &probe); err != nil {
		t.Fatalf("/health body does not parse as JSON: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("/health status = %d, want 200", resp.StatusCode)
	}

	// /health values that depend on real wall-clock time (lastFetchedAt) or
	// the test's port allocation (none here, but defensive) are nondeterministic
	// across runs. Strip them before snapshot comparison.
	scrubbed := scrubHealthForSnapshot(body)

	snapshotPath := filepath.Join(snapshotsDir, "health.snap.json")
	compareOrUpdate(t, snapshotPath, scrubbed)
}

// TC-S-02: snapshot the exact Subscription-Userinfo wire-format string
// the server emits under fixed inputs. Pins FR-005b / FR-011 against any
// drift in the formatting (separator, field order, integer rendering).
func TestSnapshot_SubscriptionUserinfo(t *testing.T) {
	now := mustTime("2026-04-30T00:00:00Z")
	opts := defaultOpts()
	opts.clockNow = now
	opts.perSourceUserinfo = map[string]string{
		"alpha":      fmt.Sprintf("upload=10737418240; download=42949672960; total=214748364800; expire=%d", now.Add(30*24*time.Hour).Unix()),
		"beta": fmt.Sprintf("upload=5368709120; download=16106127360; total=107374182400; expire=%d", now.Add(5*24*time.Hour).Unix()),
	}
	tc := newTestClusterWithOpts(t, opts)

	resp, err := http.Get(tc.URL() + "/?token=" + validToken)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	ui := resp.Header.Get("Subscription-Userinfo")
	if ui == "" {
		t.Fatalf("Subscription-Userinfo header is empty")
	}
	pui := resp.Header.Get("Profile-Update-Interval")

	// Snapshot is one line per header value.
	out := []byte(fmt.Sprintf("Subscription-Userinfo: %s\nProfile-Update-Interval: %s\n", ui, pui))
	snapshotPath := filepath.Join(snapshotsDir, "subscription-userinfo.snap.txt")
	compareOrUpdate(t, snapshotPath, out)
}

// scrubHealthForSnapshot replaces nondeterministic fields (lastFetchedAt
// timestamps, which use wall-clock time during the integration tests) with
// stable placeholders so the snapshot only captures structure + values
// that depend on inputs.
func scrubHealthForSnapshot(b []byte) []byte {
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		return b
	}
	if sources, ok := doc["sources"].([]any); ok {
		for _, s := range sources {
			if m, ok := s.(map[string]any); ok {
				if _, has := m["lastFetchedAt"]; has {
					m["lastFetchedAt"] = "<scrubbed-timestamp>"
				}
			}
		}
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return b
	}
	out = append(out, '\n')
	return out
}

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// compareOrUpdate compares `got` against the file at path. If the file
// doesn't exist OR -update is set, writes `got` and passes. Otherwise diffs.
func compareOrUpdate(t *testing.T, path string, got []byte) {
	t.Helper()

	if *updateSnapshots {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("snapshot updated: %s (%d bytes)", path, len(got))
		return
	}

	want, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		// First run for this snapshot — write it and remind the developer.
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Errorf("snapshot did not exist; wrote initial baseline at %s. "+
			"Re-run the test to verify it passes against the committed bytes.", path)
		return
	}
	if err != nil {
		t.Fatalf("read snapshot %s: %v", path, err)
	}

	if !bytes.Equal(want, got) {
		// Cap the dump size so test output stays reasonable.
		summary := fmt.Sprintf("snapshot drift: %s\nwant %d bytes, got %d bytes",
			path, len(want), len(got))
		t.Errorf("%s\n--- first divergence point ---\n%s",
			summary, firstDivergence(want, got))
	}
}

// firstDivergence returns a short snippet of the first byte where want and
// got differ, plus ~40 chars of context on each side. Helps the developer
// see what changed without dumping 100KB of YAML.
func firstDivergence(want, got []byte) string {
	n := len(want)
	if len(got) < n {
		n = len(got)
	}
	for i := 0; i < n; i++ {
		if want[i] != got[i] {
			lo := i - 40
			if lo < 0 {
				lo = 0
			}
			hi := i + 40
			if hi > n {
				hi = n
			}
			return fmt.Sprintf("at byte %d:\n  want: %q\n  got:  %q",
				i, string(want[lo:hi]), string(got[lo:hi]))
		}
	}
	if len(want) != len(got) {
		return fmt.Sprintf("trailing length differs at byte %d (want=%d, got=%d)",
			n, len(want), len(got))
	}
	return "no byte-level difference detected (content equal?)"
}
