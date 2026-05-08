package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mc256/honkai-rule-server/internal/clock"
	"github.com/mc256/honkai-rule-server/internal/config"
	"github.com/mc256/honkai-rule-server/internal/fetcher"
	"github.com/mc256/honkai-rule-server/internal/merge"
	"github.com/mc256/honkai-rule-server/internal/output"
)

// minimal Clash config payload for one upstream stub.
const minimalUpstreamYAML = `port: 7890
proxies:
  - {name: P1, type: trojan, server: a.test, port: 443, password: pw}
proxy-groups:
  - {name: Auto, type: select, proxies: [P1]}
rules:
  - MATCH,DIRECT
`

const minimalTemplate = `mixed-port: 7890
mode: rule
proxies: __MERGED_PROXIES__
proxy-groups: __MERGED_PROXY_GROUPS__
rules: __MERGED_RULES__
`

// readyCoordinator constructs a Coordinator backed by a successful upstream
// stub and waits for bootstrap to complete. Returns the live coordinator
// + the cache it shares with the pipeline.
func readyCoordinator(t *testing.T, upstreamHandler http.Handler) (*fetcher.Coordinator, *fetcher.Cache, func()) {
	t.Helper()
	srv := httptest.NewServer(upstreamHandler)
	t.Cleanup(srv.Close)

	clk := clock.NewFakeClock(time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC))
	cache := fetcher.NewCache(clk, "")
	httpFetcher := fetcher.NewUpstreamFetcher(2*time.Second, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))

	rows := []config.SubscriptionRow{
		{Name: "src", Link: srv.URL, Priority: 1, Enable: true},
	}
	co := fetcher.NewCoordinator(cache, httpFetcher, clk, fetcher.SchedulerConfig{
		DefaultTTL:                    time.Hour,
		DefaultStaleOnError:           24 * time.Hour,
		BootstrapMaxAttemptsPerSource: 2,
		BootstrapAttemptDelay:         10 * time.Millisecond,
	}, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), rows)

	ctx, cancel := context.WithCancel(context.Background())
	go co.Start(ctx)

	// Wait for bootstrap.
	select {
	case <-co.Ready():
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("coordinator never became ready")
	}

	cleanup := func() { cancel(); co.Wait() }
	return co, cache, cleanup
}

func makeDeps(t *testing.T, co *fetcher.Coordinator, cache *fetcher.Cache, logBuf *bytes.Buffer) SubscriptionDeps {
	t.Helper()
	pipeline := merge.NewPipeline(cache,
		[]config.SubscriptionRow{{Name: "src", Link: "http://x.test", Priority: 1, Enable: true}},
		nil, nil,
		clock.NewFakeClock(time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)), 12,
	)
	adapter, err := output.NewSubscriptionModeFromBytes([]byte(minimalTemplate))
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return SubscriptionDeps{
		Coordinator: co,
		Pipeline:    pipeline,
		Adapter:     adapter,
		Logger:      log,
	}
}

func TestSubscription_HappyPath(t *testing.T) {
	co, cache, cleanup := readyCoordinator(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(minimalUpstreamYAML))
	}))
	defer cleanup()

	logBuf := &bytes.Buffer{}
	h := Subscription(makeDeps(t, co, cache, logBuf))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/yaml; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if !strings.Contains(w.Body.String(), "P1") {
		t.Errorf("body missing proxy P1:\n%s", w.Body.String())
	}
	if !strings.Contains(logBuf.String(), "served subscription") {
		t.Errorf("missing served-subscription log line: %s", logBuf.String())
	}
}

// 503 + warming_up while coordinator is still bootstrapping.
func TestSubscription_WarmingUp(t *testing.T) {
	// Build a coordinator that hasn't been Started yet → Ready never closes.
	clk := clock.NewFakeClock(time.Now())
	cache := fetcher.NewCache(clk, "")
	httpFetcher := fetcher.NewUpstreamFetcher(2*time.Second, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	co := fetcher.NewCoordinator(cache, httpFetcher, clk, fetcher.SchedulerConfig{
		DefaultTTL: time.Hour,
	}, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)),
		[]config.SubscriptionRow{{Name: "src", Link: "http://x.test", Priority: 1, Enable: true}})

	logBuf := &bytes.Buffer{}
	h := Subscription(makeDeps(t, co, cache, logBuf))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q", w.Header().Get("Content-Type"))
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "warming_up" {
		t.Errorf("error = %v, want warming_up", body["error"])
	}
}

// 503 + bootstrap_failed when coordinator finished but a source failed.
func TestSubscription_BootstrapFailed(t *testing.T) {
	co, cache, cleanup := readyCoordinator(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503) // every fetch attempt fails → BootstrapFailed
	}))
	defer cleanup()

	logBuf := &bytes.Buffer{}
	h := Subscription(makeDeps(t, co, cache, logBuf))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "bootstrap_failed" {
		t.Errorf("error = %v, want bootstrap_failed", body["error"])
	}
}
