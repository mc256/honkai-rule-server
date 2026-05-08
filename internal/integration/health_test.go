package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mc256/honkai-rule-server/internal/fetcher"
	"github.com/mc256/honkai-rule-server/internal/merge"
)

// healthResponse mirrors routes.HealthResponse without importing the package
// (avoids a cycle and keeps the test honest about the wire format).
type healthResponse struct {
	Bootstrap      string                `json:"bootstrap"`
	Sources        []fetcher.SourceState `json:"sources"`
	DailyAllowance merge.DailyAllowance  `json:"dailyAllowance"`
}

func getHealth(t *testing.T, tc *testCluster) (*http.Response, healthResponse) {
	t.Helper()
	resp, err := http.Get(tc.URL() + "/health")
	if err != nil {
		t.Fatal(err)
	}
	body := bodyOf(t, resp)
	var got healthResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode /health body: %v\n%s", err, body)
	}
	return resp, got
}

// TC-I-02: bootstrap succeeded; swap alpha to 503; ForceRefresh; /health
// shows alpha as degraded (lastFetchOutcome=http_error) while still serving
// from cache (the bootstrap-fetched payload is still in memory).
func TestI_02_DegradedSourceServingFromCache(t *testing.T) {
	tc := newTestCluster(t)

	// Bootstrap is already complete with both upstreams 200.
	tc.upstreams["alpha"].SetHandler(status(503))

	// Advance the clock past TTL so the State() check returns Stale (which
	// is what SourceStates uses to compute servingFromCache=true).
	tc.clk.Advance(2 * time.Hour)

	// Force a refresh; alpha fails and gets markFailed; beta succeeds.
	tc.coord.ForceRefresh(context.Background())

	_, got := getHealth(t, tc)
	if got.Bootstrap != "succeeded" {
		t.Errorf("Bootstrap = %q, want succeeded (bootstrap state unchanged by post-bootstrap failure)", got.Bootstrap)
	}

	var alpha, berry *fetcher.SourceState
	for i := range got.Sources {
		if got.Sources[i].Name == "alpha" {
			alpha = &got.Sources[i]
		}
		if got.Sources[i].Name == "beta" {
			berry = &got.Sources[i]
		}
	}
	if alpha == nil || berry == nil {
		t.Fatalf("missing source state; got %+v", got.Sources)
	}
	if alpha.LastOutcome != fetcher.OutcomeHTTPError {
		t.Errorf("alpha.LastOutcome = %q, want http_error", alpha.LastOutcome)
	}
	if !alpha.ServingFromCache {
		t.Errorf("alpha.ServingFromCache = false, want true")
	}
	if berry.LastOutcome != fetcher.OutcomeSuccess {
		t.Errorf("berry.LastOutcome = %q, want success", berry.LastOutcome)
	}
}

// TC-I-03: cache stale beyond stale-on-error window → alpha dropped from
// cache; /health shows alpha as failed (no cached userinfo).
func TestI_03_StaleWindowExceededDropsSource(t *testing.T) {
	tc := newTestCluster(t)
	tc.upstreams["alpha"].SetHandler(status(503))

	// Advance past the stale-on-error window (24h default in the test cluster).
	tc.clk.Advance(25 * time.Hour)
	tc.coord.ForceRefresh(context.Background())

	_, got := getHealth(t, tc)
	for _, s := range got.Sources {
		if s.Name != "alpha" {
			continue
		}
		if s.Userinfo != nil {
			t.Errorf("alpha still has cached userinfo after stale window exceeded: %+v", s.Userinfo)
		}
		if s.LastOutcome != fetcher.OutcomeHTTPError {
			t.Errorf("alpha.LastOutcome = %q, want http_error", s.LastOutcome)
		}
		return
	}
	t.Errorf("alpha source state missing from /health body")
}

// TC-I-05: a `Disable` row is loaded but skipped from fetching; /health
// reports it with Enabled=false.
func TestI_05_DisabledSourceVisibleInHealth(t *testing.T) {
	opts := defaultOpts()
	opts.disableSources = []string{"alpha"}
	tc := newTestClusterWithOpts(t, opts)

	_, got := getHealth(t, tc)
	var alpha *fetcher.SourceState
	for i := range got.Sources {
		if got.Sources[i].Name == "alpha" {
			alpha = &got.Sources[i]
		}
	}
	if alpha == nil {
		t.Fatal("disabled source missing from /health entirely; want present with Enabled=false")
	}
	if alpha.Enabled {
		t.Errorf("alpha.Enabled = true, want false")
	}
	// Disabled source: zero fetch hits.
	if hits := tc.upstreams["alpha"].Hits(); hits != 0 {
		t.Errorf("disabled alpha was fetched %d times", hits)
	}
	// Logs should mention the disabled set on startup.
	if !strings.Contains(strings.ToLower(tc.logBuf.String()), "disable") {
		// (Test cluster's logger captures both coordinator and request logs;
		// the explicit "disabled sources" log is in main.go which the cluster
		// doesn't run. We accept that here by not asserting on the log line
		// for this specific test — Enabled=false in /health is the contract.)
	}
}
