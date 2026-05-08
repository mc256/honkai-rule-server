package integration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mc256/honkai-rule-server/internal/auth"
	"github.com/mc256/honkai-rule-server/internal/clock"
	"github.com/mc256/honkai-rule-server/internal/config"
	"github.com/mc256/honkai-rule-server/internal/dailyspend"
	"github.com/mc256/honkai-rule-server/internal/fetcher"
	"github.com/mc256/honkai-rule-server/internal/merge"
	"github.com/mc256/honkai-rule-server/internal/output"
	routes "github.com/mc256/honkai-rule-server/internal/server/routes"
)

// upstreamStub is one mock upstream server. The stub's handler can be swapped
// at runtime to simulate failures (set 503), changes (return new YAML), etc.
type upstreamStub struct {
	name      string
	server    *httptest.Server
	handler   atomic.Value // http.HandlerFunc
	fetchHits atomic.Int64
}

func newUpstreamStub(name string, initial http.HandlerFunc) *upstreamStub {
	s := &upstreamStub{name: name}
	s.handler.Store(initial)
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.fetchHits.Add(1)
		s.handler.Load().(http.HandlerFunc)(w, r)
	}))
	return s
}

func (s *upstreamStub) URL() string                       { return s.server.URL }
func (s *upstreamStub) Hits() int64                       { return s.fetchHits.Load() }
func (s *upstreamStub) ResetHits()                        { s.fetchHits.Store(0) }
func (s *upstreamStub) SetHandler(h http.HandlerFunc)     { s.handler.Store(h) }
func (s *upstreamStub) Close()                            { s.server.Close() }

// yamlOK returns a handler that responds with the given YAML body and the
// standard Subscription-Userinfo + Profile-Update-Interval headers.
func yamlOK(body string, userinfo string, intervalHours int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if userinfo != "" {
			w.Header().Set("Subscription-Userinfo", userinfo)
		}
		if intervalHours > 0 {
			w.Header().Set("Profile-Update-Interval", fmt.Sprintf("%d", intervalHours))
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte(body))
	}
}

// status returns a handler that always responds with the given HTTP status.
func status(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }
}

// testCluster is the integration-test playground: real fetcher / cache /
// coordinator / pipeline / output / HTTP server, with stub upstreams instead
// of real ones. Construct via newTestCluster; tear down via Close (or just
// rely on t.Cleanup).
type testCluster struct {
	t *testing.T

	upstreams map[string]*upstreamStub
	tokens    *config.TokenStore
	cache     *fetcher.Cache
	coord     *fetcher.Coordinator
	pipeline  *merge.Pipeline
	adapter   *output.SubscriptionMode

	httpSrv *httptest.Server
	logBuf  *bytes.Buffer
	logger  *slog.Logger
	clk     *clock.FakeClock

	stop context.CancelFunc
	wg   sync.WaitGroup
}

// clusterOpts configures the cluster construction.
type clusterOpts struct {
	tokens          string // tokens.json content; default = single valid token
	bootstrapWait   time.Duration
	disableSources  []string
	defaultUserinfo string
	defaultInterval int
	clockNow        time.Time
	// customPayloads, when set for a source name, replaces the fixture body
	// returned by that upstream's stub — used by collision/synthetic tests.
	customPayloads map[string]string
	// perSourceUserinfo, when set, overrides the Subscription-Userinfo
	// header value for that source.
	perSourceUserinfo map[string]string
	// failingUpstreams, when set for a source name, causes that stub to
	// respond with the given HTTP status code from its very first request
	// — used by cold-start fail-closed tests.
	failingUpstreams map[string]int
	// pathPrefix mirrors the runtime URL_PATH_PREFIX env var (009 FR-023).
	// Empty (default) means subscription is mounted at "/".
	pathPrefix string
}

func defaultOpts() clusterOpts {
	return clusterOpts{
		tokens: `{
			"tokens": [
				{"token": "valid-test-token", "label": "test", "issued_at": "2026-04-30T00:00:00Z", "revoked": false},
				{"token": "revoked-test-token", "label": "lost", "issued_at": "2026-04-29T00:00:00Z", "revoked": true}
			]
		}`,
		bootstrapWait:   2 * time.Second,
		defaultUserinfo: "upload=10; download=20; total=100; expire=1804180937",
		defaultInterval: 12,
		clockNow:        time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC),
	}
}

// newTestCluster spins up: two upstream stubs (alpha + beta returning
// the real fixture YAMLs), a cache + fetcher + coordinator, the merge
// pipeline + output adapter, and an httptest server hosting the rule-server
// HTTP layer (auth + GET /). Bootstrap completes synchronously before
// returning, so the caller can immediately make requests.
func newTestCluster(t *testing.T) *testCluster {
	return newTestClusterWithOpts(t, defaultOpts())
}

func newTestClusterWithOpts(t *testing.T, opts clusterOpts) *testCluster {
	t.Helper()

	alphaBody, err := os.ReadFile(filepath.Join(fixturesDir, "upstream/alpha.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	betaBody, err := os.ReadFile(filepath.Join(fixturesDir, "upstream/beta.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	pickBody := func(name, defaultBody string) string {
		if b, ok := opts.customPayloads[name]; ok {
			return b
		}
		return defaultBody
	}
	pickUserinfo := func(name string) string {
		if v, ok := opts.perSourceUserinfo[name]; ok {
			return v
		}
		return opts.defaultUserinfo
	}
	pickHandler := func(name, defaultBody string) http.HandlerFunc {
		if code, ok := opts.failingUpstreams[name]; ok {
			return status(code)
		}
		return yamlOK(pickBody(name, defaultBody), pickUserinfo(name), opts.defaultInterval)
	}
	stubs := map[string]*upstreamStub{
		"alpha":      newUpstreamStub("alpha", pickHandler("alpha", string(alphaBody))),
		"beta": newUpstreamStub("beta", pickHandler("beta", string(betaBody))),
	}
	t.Cleanup(func() {
		for _, s := range stubs {
			s.Close()
		}
	})

	disabledSet := map[string]bool{}
	for _, n := range opts.disableSources {
		disabledSet[n] = true
	}
	rows := []config.SubscriptionRow{
		{Name: "alpha", Link: stubs["alpha"].URL(), Priority: 1000, Enable: !disabledSet["alpha"]},
		{Name: "beta", Link: stubs["beta"].URL(), Priority: 2000, Enable: !disabledSet["beta"]},
	}

	tokensPath := filepath.Join(t.TempDir(), "tokens.json")
	if err := os.WriteFile(tokensPath, []byte(opts.tokens), 0o600); err != nil {
		t.Fatal(err)
	}
	tokens := config.NewTokenStore(clock.RealClock{})
	if err := tokens.Load(tokensPath); err != nil {
		t.Fatal(err)
	}

	// Load the committed own-proxies fixture — TC-I-16 expects own-proxies
	// to appear in the merged output.
	ownProxies, err := config.LoadOwnProxies(filepath.Join(fixturesDir, "own-proxies.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	templateBytes, err := os.ReadFile("../../templates/served-config.template.yaml")
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := output.NewSubscriptionModeFromBytes(templateBytes)
	if err != nil {
		t.Fatal(err)
	}

	clk := clock.NewFakeClock(opts.clockNow)
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	cache := fetcher.NewCache(clk, "")
	httpFetcher := fetcher.NewUpstreamFetcher(2*time.Second, logger)
	coord := fetcher.NewCoordinator(cache, httpFetcher, clk, fetcher.SchedulerConfig{
		DefaultTTL:                    time.Hour,
		DefaultStaleOnError:           24 * time.Hour,
		BootstrapMaxAttemptsPerSource: 2,
		BootstrapAttemptDelay:         10 * time.Millisecond,
	}, logger, rows)

	ctx, cancel := context.WithCancel(context.Background())
	tc := &testCluster{
		t:         t,
		upstreams: stubs,
		tokens:    tokens,
		cache:     cache,
		coord:     coord,
		adapter:   adapter,
		logBuf:    logBuf,
		logger:    logger,
		clk:       clk,
		stop:      cancel,
	}
	// 011: inject in-memory snapshotter + America/Toronto budget timezone
	// so the spend-tracking encoding exercises end-to-end. Tests with
	// upstream userinfo will see used_today / total reflect spend; tests
	// without userinfo continue to omit the header (010 FR-006).
	tzToronto, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatalf("LoadLocation(America/Toronto): %v", err)
	}
	tc.pipeline = merge.NewPipeline(cache, rows, ownProxies.Proxies, ownProxies.ProxyGroups, clk, 12).
		WithURLTestParams(merge.URLTestParams{
			URL:             "https://www.gstatic.com/generate_204",
			IntervalSeconds: 10,
			TimeoutMS:       3000,
			MaxFailedTimes:  3,
			Lazy:            true,
		}).
		WithSnapshotter(dailyspend.NewMapSnapshotter(nil)).
		WithBudgetLocation(tzToronto)

	tc.wg.Add(1)
	go func() {
		defer tc.wg.Done()
		coord.Start(ctx)
	}()

	// Wait for bootstrap.
	select {
	case <-coord.Ready():
	case <-time.After(opts.bootstrapWait):
		// Don't fail the test here — some tests want to observe the warming-up
		// state. The HTTP server still starts.
	}

	deps := routes.SubscriptionDeps{
		Coordinator: coord,
		Pipeline:    tc.pipeline,
		Adapter:     adapter,
		Logger:      logger,
	}
	mux := http.NewServeMux()
	subWrapped := auth.RequireToken(tokens, logger)(routes.Subscription(deps))
	if p := opts.pathPrefix; p != "" {
		mux.Handle("GET "+p, http.StripPrefix(p, subWrapped))
		mux.Handle("GET "+p+"/{$}", http.StripPrefix(p, subWrapped))
	} else {
		mux.Handle("GET /{$}", subWrapped)
	}
	mux.Handle("GET /health", routes.Health(routes.HealthDeps{
		Coordinator: coord,
		Pipeline:    tc.pipeline,
		Logger:      logger,
	}))

	tc.httpSrv = httptest.NewServer(mux)
	t.Cleanup(tc.Close)

	return tc
}

// Close stops the cluster: HTTP server, coordinator, all upstream stubs.
func (tc *testCluster) Close() {
	if tc.httpSrv != nil {
		tc.httpSrv.Close()
		tc.httpSrv = nil
	}
	if tc.stop != nil {
		tc.stop()
		tc.stop = nil
	}
	tc.wg.Wait()
	tc.coord.Wait()
}

// URL returns the rule-server's base URL.
func (tc *testCluster) URL() string { return tc.httpSrv.URL }

// Get makes a GET request to the rule-server with optional ?token=.
func (tc *testCluster) Get(t *testing.T, path string, token string) *http.Response {
	t.Helper()
	url := tc.URL() + path
	if token != "" {
		if path == "" || path == "/" {
			url = tc.URL() + "/?token=" + token
		} else {
			url = tc.URL() + path + "?token=" + token
		}
	}
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// Body reads + closes a response body.
func bodyOf(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// LogContains reports whether the cluster's logger has emitted a line
// containing substr. Useful for assertion of structured-log side effects.
func (tc *testCluster) LogContains(substr string) bool {
	return bytes.Contains(tc.logBuf.Bytes(), []byte(substr))
}
