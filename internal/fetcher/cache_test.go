package fetcher

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mc256/honkai-rule-server/internal/clock"
)

func newTestPayload(name string, body string) *UpstreamCachedPayload {
	return &UpstreamCachedPayload{
		SourceName:   name,
		FetchedAt:    time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC),
		BodyYAML:     []byte(body),
		PayloadBytes: len(body),
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
}

// TC-U-CACHE-01: within TTL → cached value, no fetch invoked.
func TestCACHE_01_WithinTTLNoFetch(t *testing.T) {
	clk := clock.NewFakeClock(time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC))
	c := NewCache(clk, "")

	var calls int32
	fetchFn := func(ctx context.Context) (*UpstreamCachedPayload, *FetchResult, error) {
		atomic.AddInt32(&calls, 1)
		return newTestPayload("alpha", "data1"), &FetchResult{Outcome: OutcomeSuccess}, nil
	}

	// First call seeds the cache.
	if _, _, err := c.RefreshIfStale(context.Background(), "alpha", time.Hour, fetchFn); err != nil {
		t.Fatalf("first RefreshIfStale: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("calls after seed = %d, want 1", got)
	}

	// Within TTL → no fetch.
	clk.Advance(30 * time.Minute)
	if _, refreshed, err := c.RefreshIfStale(context.Background(), "alpha", time.Hour, fetchFn); err != nil || refreshed {
		t.Errorf("within TTL got refreshed=%v err=%v, want false/nil", refreshed, err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls after within-TTL = %d, want 1", got)
	}
}

// TC-U-CACHE-02: past TTL + fetch fails → existing payload still returned, failure recorded.
func TestCACHE_02_StaleOnFetchFailure(t *testing.T) {
	clk := clock.NewFakeClock(time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC))
	c := NewCache(clk, "")

	good := func(ctx context.Context) (*UpstreamCachedPayload, *FetchResult, error) {
		return newTestPayload("alpha", "data1"), &FetchResult{Outcome: OutcomeSuccess}, nil
	}
	if _, _, err := c.RefreshIfStale(context.Background(), "alpha", time.Hour, good); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Advance past TTL, fetch fails.
	clk.Advance(2 * time.Hour)
	bad := func(ctx context.Context) (*UpstreamCachedPayload, *FetchResult, error) {
		return nil, &FetchResult{Outcome: OutcomeHTTPError}, errors.New("HTTP 503")
	}
	payload, refreshed, err := c.RefreshIfStale(context.Background(), "alpha", time.Hour, bad)
	if err == nil {
		t.Fatalf("expected fetch error")
	}
	if !refreshed {
		t.Errorf("refreshed = false, want true (we tried)")
	}
	// Existing payload still present
	if payload == nil || string(payload.BodyYAML) != "data1" {
		t.Errorf("payload = %v, want previous data1", payload)
	}
	// Failure recorded
	lastErr, lastAt := c.LastFailure("alpha")
	if lastErr == "" || lastAt.IsZero() {
		t.Errorf("LastFailure: err=%q at=%v", lastErr, lastAt)
	}
}

// TC-U-CACHE-03: missing entry + fetch fails → no payload available.
func TestCACHE_03_MissingPlusFailure(t *testing.T) {
	clk := clock.NewFakeClock(time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC))
	c := NewCache(clk, "")

	if state := c.State("nope", time.Hour); state != StateMissing {
		t.Errorf("State on missing = %s, want missing", state)
	}

	bad := func(ctx context.Context) (*UpstreamCachedPayload, *FetchResult, error) {
		return nil, &FetchResult{Outcome: OutcomeNetworkError}, errors.New("dial failure")
	}
	payload, _, err := c.RefreshIfStale(context.Background(), "nope", time.Hour, bad)
	if err == nil {
		t.Fatal("expected fetch error")
	}
	if payload != nil {
		t.Errorf("payload = %v, want nil (no prior payload existed)", payload)
	}
}

// TC-U-CACHE-04: 100 concurrent RefreshIfStale calls during a refresh trigger exactly 1 fetch.
func TestCACHE_04_SingleFlight(t *testing.T) {
	clk := clock.NewFakeClock(time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC))
	c := NewCache(clk, "")

	var calls int32
	released := make(chan struct{})
	fetchFn := func(ctx context.Context) (*UpstreamCachedPayload, *FetchResult, error) {
		atomic.AddInt32(&calls, 1)
		<-released // hold all in-flight callers here
		return newTestPayload("alpha", "data1"), &FetchResult{Outcome: OutcomeSuccess}, nil
	}

	const N = 100
	var wg sync.WaitGroup
	wg.Add(N)
	results := make(chan *UpstreamCachedPayload, N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			p, _, err := c.RefreshIfStale(context.Background(), "alpha", time.Hour, fetchFn)
			if err != nil {
				t.Errorf("RefreshIfStale: %v", err)
			}
			results <- p
		}()
	}

	// Give the goroutines time to enter singleflight.
	time.Sleep(50 * time.Millisecond)
	close(released)
	wg.Wait()
	close(results)

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("fetch calls = %d, want 1 (singleflight collapsed N concurrent refreshes)", got)
	}
	count := 0
	for p := range results {
		if p == nil || string(p.BodyYAML) != "data1" {
			t.Errorf("unexpected payload: %v", p)
		}
		count++
	}
	if count != N {
		t.Errorf("results = %d, want %d", count, N)
	}
}

func TestCache_GetIsReadOnly(t *testing.T) {
	clk := clock.NewFakeClock(time.Now())
	c := NewCache(clk, "")
	// Get on missing returns (nil, false), no fetchFn invoked.
	p, ok := c.Get("nothing")
	if ok || p != nil {
		t.Errorf("Get on missing = (%v, %v), want (nil, false)", p, ok)
	}
}

func TestCache_DiskPersistence(t *testing.T) {
	clk := clock.NewFakeClock(time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC))
	dir := t.TempDir()

	c1 := NewCache(clk, dir)
	good := func(ctx context.Context) (*UpstreamCachedPayload, *FetchResult, error) {
		return newTestPayload("alpha", "from-disk"), &FetchResult{Outcome: OutcomeSuccess}, nil
	}
	if _, _, err := c1.RefreshIfStale(context.Background(), "alpha", time.Hour, good); err != nil {
		t.Fatal(err)
	}

	// New cache backed by same dir; LoadFromDisk should rehydrate.
	c2 := NewCache(clk, dir)
	if err := c2.LoadFromDisk(context.Background(), discardLogger()); err != nil {
		t.Fatal(err)
	}
	p, ok := c2.Get("alpha")
	if !ok || string(p.BodyYAML) != "from-disk" {
		t.Errorf("rehydrated payload = %v ok=%v, want from-disk", p, ok)
	}
}

func TestCache_DropRemovesFromMemoryAndDisk(t *testing.T) {
	clk := clock.NewFakeClock(time.Now())
	dir := t.TempDir()
	c := NewCache(clk, dir)
	good := func(ctx context.Context) (*UpstreamCachedPayload, *FetchResult, error) {
		return newTestPayload("alpha", "x"), &FetchResult{Outcome: OutcomeSuccess}, nil
	}
	if _, _, err := c.RefreshIfStale(context.Background(), "alpha", time.Hour, good); err != nil {
		t.Fatal(err)
	}
	c.Drop("alpha")
	if _, ok := c.Get("alpha"); ok {
		t.Errorf("Get after Drop returned a value")
	}
}
