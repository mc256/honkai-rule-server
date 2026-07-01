package fetcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mc256/honkai-rule-server/internal/clock"
	"github.com/mc256/honkai-rule-server/internal/config"
)

func defaultSchedulerCfg() SchedulerConfig {
	return SchedulerConfig{
		DefaultTTL:                    time.Hour,
		DefaultStaleOnError:           24 * time.Hour,
		BootstrapMaxAttemptsPerSource: 2,
		BootstrapAttemptDelay:         10 * time.Millisecond,
	}
}

// Bootstrap succeeds → Ready closes; AllSucceeded() true.
func TestScheduler_BootstrapSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Subscription-Userinfo", "upload=1; download=2; total=100; expire=1804180937")
		_, _ = w.Write([]byte(minimalUpstreamYAML))
	}))
	defer srv.Close()

	clk := clock.NewFakeClock(time.Now())
	cache := NewCache(clk, "")
	fetcher := newFetcher()
	rows := []config.SubscriptionRow{
		{Name: "src1", Link: srv.URL, Priority: 1, Enable: true},
	}
	co := NewCoordinator(cache, fetcher, clk, defaultSchedulerCfg(), discardLogger(), rows)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go co.Start(ctx)

	select {
	case <-co.Ready():
	case <-time.After(2 * time.Second):
		t.Fatal("ready never closed")
	}
	if !co.AllSucceeded() {
		t.Errorf("AllSucceeded = false")
	}
	if _, ok := cache.Get("src1"); !ok {
		t.Errorf("payload not in cache after bootstrap")
	}

	cancel()
	co.Wait()
}

// Bootstrap fails on every attempt → Ready closes; AllSucceeded() false.
func TestScheduler_BootstrapFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()

	clk := clock.NewFakeClock(time.Now())
	cache := NewCache(clk, "")
	fetcher := newFetcher()
	rows := []config.SubscriptionRow{
		{Name: "src1", Link: srv.URL, Priority: 1, Enable: true},
	}
	co := NewCoordinator(cache, fetcher, clk, defaultSchedulerCfg(), discardLogger(), rows)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go co.Start(ctx)

	select {
	case <-co.Ready():
	case <-time.After(2 * time.Second):
		t.Fatal("ready never closed")
	}
	if co.AllSucceeded() {
		t.Errorf("AllSucceeded = true, want false (every bootstrap attempt failed)")
	}

	states := co.SourceStates()
	if len(states) != 1 || states[0].Bootstrap != BootstrapFailed {
		t.Errorf("SourceStates = %+v, want one entry with Bootstrap=failed", states)
	}

	cancel()
	co.Wait()
}

// Disabled sources don't block bootstrap and don't get fetched.
func TestScheduler_DisabledSourceSkipped(t *testing.T) {
	var fetchCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&fetchCount, 1)
		_, _ = w.Write([]byte(minimalUpstreamYAML))
	}))
	defer srv.Close()

	clk := clock.NewFakeClock(time.Now())
	cache := NewCache(clk, "")
	fetcher := newFetcher()
	rows := []config.SubscriptionRow{
		{Name: "off", Link: srv.URL, Priority: 1, Enable: false},
	}
	co := NewCoordinator(cache, fetcher, clk, defaultSchedulerCfg(), discardLogger(), rows)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go co.Start(ctx)

	<-co.Ready()
	if !co.AllSucceeded() {
		t.Errorf("AllSucceeded = false; disabled-only configurations should be ready")
	}
	if got := atomic.LoadInt32(&fetchCount); got != 0 {
		t.Errorf("disabled source was fetched %d times, want 0", got)
	}

	cancel()
	co.Wait()
}

// ttlFor honors the tri-state refresh column: 0 → default, >0 → interval,
// <0 → effectively-infinite (never refresh).
func TestScheduler_ttlFor(t *testing.T) {
	clk := clock.NewFakeClock(time.Now())
	co := NewCoordinator(NewCache(clk, ""), newFetcher(), clk, defaultSchedulerCfg(), discardLogger(), nil)

	cases := []struct {
		refresh int
		want    time.Duration
	}{
		{0, time.Hour},           // default (DefaultTTL from defaultSchedulerCfg)
		{300, 300 * time.Second}, // explicit interval
		{-1, neverRefreshTTL},    // never refresh
	}
	for _, tc := range cases {
		got := co.ttlFor(config.SubscriptionRow{RefreshSeconds: tc.refresh})
		if got != tc.want {
			t.Errorf("ttlFor(refresh=%d) = %v, want %v", tc.refresh, got, tc.want)
		}
	}

	// fetchTTLFor mirrors ttlFor except never-refresh uses DefaultTTL so the
	// bootstrap fetch still refreshes a stale disk snapshot (FR-017f).
	fetchCases := []struct {
		refresh int
		want    time.Duration
	}{
		{0, time.Hour},
		{300, 300 * time.Second},
		{-1, time.Hour}, // never-refresh → DefaultTTL for the fetch decision
	}
	for _, tc := range fetchCases {
		got := co.fetchTTLFor(config.SubscriptionRow{RefreshSeconds: tc.refresh})
		if got != tc.want {
			t.Errorf("fetchTTLFor(refresh=%d) = %v, want %v", tc.refresh, got, tc.want)
		}
	}
}

// FR-017f: a never-refresh source's bootstrap honors the default interval for
// its cache-freshness decision — a stale rehydrated snapshot is re-fetched on
// start, while a still-fresh snapshot is reused (no upstream hammering).
func TestScheduler_NeverRefresh_BootstrapHonorsCacheFreshness(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	run := func(t *testing.T, seedStoredAt time.Time, wantFetches int32) {
		var fetchCount int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&fetchCount, 1)
			_, _ = w.Write([]byte(minimalUpstreamYAML))
		}))
		defer srv.Close()

		clk := clock.NewFakeClock(now)
		cache := NewCache(clk, "")
		// Simulate a rehydrated disk snapshot with the given age.
		_ = cache.set("warm", newTestPayload("warm", minimalUpstreamYAML), seedStoredAt)

		cfg := defaultSchedulerCfg() // DefaultTTL = 1h
		rows := []config.SubscriptionRow{
			{Name: "warm", Link: srv.URL, Priority: 1, Enable: true, RefreshSeconds: -1},
		}
		co := NewCoordinator(cache, newFetcher(), clk, cfg, discardLogger(), rows)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go co.Start(ctx)
		select {
		case <-co.Ready():
		case <-time.After(2 * time.Second):
			t.Fatal("ready never closed")
		}
		if !co.AllSucceeded() {
			t.Errorf("AllSucceeded = false; never-refresh source should be ready from cache or fetch")
		}
		time.Sleep(50 * time.Millisecond) // no ticker should sneak a fetch
		if got := atomic.LoadInt32(&fetchCount); got != wantFetches {
			t.Errorf("fetches = %d, want %d", got, wantFetches)
		}
		cancel()
		co.Wait()
	}

	t.Run("stale snapshot re-fetches on bootstrap", func(t *testing.T) {
		run(t, now.Add(-2*time.Hour), 1) // 2h old > 1h DefaultTTL → stale → fetch
	})
	t.Run("fresh snapshot reused without fetch", func(t *testing.T) {
		run(t, now.Add(-1*time.Minute), 0) // 1m old < 1h DefaultTTL → fresh → no fetch
	})
}

// A source with refresh < 0 is fetched once at bootstrap and never again, even
// when the default TTL would otherwise trigger frequent re-fetches.
func TestScheduler_NeverRefresh(t *testing.T) {
	var fetchCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&fetchCount, 1)
		_, _ = w.Write([]byte(minimalUpstreamYAML))
	}))
	defer srv.Close()

	clk := clock.NewFakeClock(time.Now())
	cache := NewCache(clk, "")
	fetcher := newFetcher()
	// Tiny default TTL: a normally-scheduled source would re-fetch rapidly, so
	// a single fetch proves the never-refresh source skips the ticker entirely.
	cfg := defaultSchedulerCfg()
	cfg.DefaultTTL = 10 * time.Millisecond
	rows := []config.SubscriptionRow{
		{Name: "once", Link: srv.URL, Priority: 1, Enable: true, RefreshSeconds: -1},
	}
	co := NewCoordinator(cache, fetcher, clk, cfg, discardLogger(), rows)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go co.Start(ctx)

	select {
	case <-co.Ready():
	case <-time.After(2 * time.Second):
		t.Fatal("ready never closed")
	}
	if !co.AllSucceeded() {
		t.Errorf("AllSucceeded = false; never-refresh source should still bootstrap")
	}

	// Give a normally-ticking source ample opportunity to re-fetch.
	time.Sleep(100 * time.Millisecond)

	if got := atomic.LoadInt32(&fetchCount); got != 1 {
		t.Errorf("never-refresh source fetched %d times, want exactly 1 (bootstrap only)", got)
	}

	cancel()
	co.Wait()
}

// SourceStates reports per-source view including disabled.
func TestScheduler_SourceStatesIncludesDisabled(t *testing.T) {
	clk := clock.NewFakeClock(time.Now())
	cache := NewCache(clk, "")
	fetcher := newFetcher()
	rows := []config.SubscriptionRow{
		{Name: "off", Link: "http://nope.test", Priority: 1, Enable: false},
		{Name: "on", Link: "http://nope.test", Priority: 2, Enable: true},
	}
	co := NewCoordinator(cache, fetcher, clk, defaultSchedulerCfg(), discardLogger(), rows)

	states := co.SourceStates()
	if len(states) != 2 {
		t.Fatalf("got %d states, want 2", len(states))
	}
	// Sorted alphabetically.
	if states[0].Name != "off" || states[1].Name != "on" {
		t.Errorf("states out of order: %+v", states)
	}
	if states[0].Enabled || !states[1].Enabled {
		t.Errorf("Enabled flags swapped: %+v", states)
	}
}
