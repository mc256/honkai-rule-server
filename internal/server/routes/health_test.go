package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mc256/honkai-rule-server/internal/clock"
	"github.com/mc256/honkai-rule-server/internal/config"
	"github.com/mc256/honkai-rule-server/internal/fetcher"
	"github.com/mc256/honkai-rule-server/internal/merge"
)

// readyHealthDeps spins up a Coordinator + Pipeline behind a successful
// upstream stub and returns deps + cleanup.
func readyHealthDeps(t *testing.T, upstreamHandler http.Handler) (HealthDeps, func()) {
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

	select {
	case <-co.Ready():
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("coordinator never became ready")
	}

	pipeline := merge.NewPipeline(cache, rows, nil, nil, clk, 12)
	deps := HealthDeps{
		Coordinator: co,
		Pipeline:    pipeline,
		Logger:      slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)),
	}
	cleanup := func() { cancel(); co.Wait() }
	return deps, cleanup
}

func decodeHealth(t *testing.T, w *httptest.ResponseRecorder) HealthResponse {
	t.Helper()
	var got HealthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\nbody=%s", err, w.Body.String())
	}
	return got
}

func TestHealth_BootstrapSucceeded(t *testing.T) {
	deps, cleanup := readyHealthDeps(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Subscription-Userinfo", "upload=10; download=20; total=100; expire=1804180937")
		w.Header().Set("Profile-Update-Interval", "12")
		_, _ = w.Write([]byte("proxies:\n  - {name: P1, type: trojan, server: a.test, port: 443, password: pw}\nproxy-groups:\n  - {name: G, type: select, proxies: [P1]}\nrules:\n  - MATCH,DIRECT\n"))
	}))
	defer cleanup()

	w := httptest.NewRecorder()
	Health(deps).ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}

	got := decodeHealth(t, w)
	if got.Bootstrap != "succeeded" {
		t.Errorf("Bootstrap = %q, want succeeded", got.Bootstrap)
	}
	if len(got.Sources) != 1 || got.Sources[0].Name != "src" {
		t.Errorf("Sources = %+v, want one entry named src", got.Sources)
	}
	if got.Sources[0].Bootstrap != fetcher.BootstrapSucceeded {
		t.Errorf("Sources[0].BootstrapState = %v", got.Sources[0].Bootstrap)
	}
	if got.Sources[0].Userinfo == nil || got.Sources[0].Userinfo.Total != 100 {
		t.Errorf("Sources[0].Userinfo = %+v", got.Sources[0].Userinfo)
	}
}

func TestHealth_BootstrapFailedReturns503(t *testing.T) {
	deps, cleanup := readyHealthDeps(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503) // bootstrap fails on every retry
	}))
	defer cleanup()

	w := httptest.NewRecorder()
	Health(deps).ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
	got := decodeHealth(t, w)
	if got.Bootstrap != "failed" {
		t.Errorf("Bootstrap = %q, want failed", got.Bootstrap)
	}
}

func TestHealth_WarmingUpReturns503(t *testing.T) {
	// Build a coordinator that never starts → Ready never closes → warming_up.
	clk := clock.NewFakeClock(time.Now())
	cache := fetcher.NewCache(clk, "")
	httpFetcher := fetcher.NewUpstreamFetcher(2*time.Second, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	rows := []config.SubscriptionRow{{Name: "src", Link: "http://x.test", Priority: 1, Enable: true}}
	co := fetcher.NewCoordinator(cache, httpFetcher, clk, fetcher.SchedulerConfig{DefaultTTL: time.Hour},
		slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), rows)
	pipeline := merge.NewPipeline(cache, rows, nil, nil, clk, 12)

	deps := HealthDeps{Coordinator: co, Pipeline: pipeline, Logger: slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))}
	w := httptest.NewRecorder()
	Health(deps).ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
	got := decodeHealth(t, w)
	if got.Bootstrap != "warming_up" {
		t.Errorf("Bootstrap = %q, want warming_up", got.Bootstrap)
	}
}
