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
