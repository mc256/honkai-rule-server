package fetcher

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/mc256/honkai-rule-server/internal/clock"
	"github.com/mc256/honkai-rule-server/internal/config"
)

// neverRefreshTTL is an effectively-infinite TTL used for sources configured to
// never refresh (RefreshSeconds < 0): the cached snapshot is never treated as
// stale and no steady-state ticker is scheduled.
const neverRefreshTTL = time.Duration(math.MaxInt64)

// refreshDisabled reports whether a source is configured to never refresh after
// its initial bootstrap fetch (RefreshSeconds < 0).
func refreshDisabled(row config.SubscriptionRow) bool {
	return row.RefreshSeconds < 0
}

// BootstrapState is the per-source state machine described in data-model.md.
type BootstrapState string

const (
	BootstrapPending   BootstrapState = "pending"
	BootstrapSucceeded BootstrapState = "succeeded"
	BootstrapFailed    BootstrapState = "failed"
)

// SourceState is the per-source view exposed via /health (FR-015).
type SourceState struct {
	Name             string                `json:"name"`
	Enabled          bool                  `json:"enabled"`
	Bootstrap        BootstrapState        `json:"bootstrapState"`
	LastFetchedAt    *time.Time            `json:"lastFetchedAt,omitempty"`
	LastOutcome      FetchOutcome          `json:"lastFetchOutcome,omitempty"`
	LastError        string                `json:"lastFetchError,omitempty"`
	ServingFromCache bool                  `json:"servingFromCache"`
	Userinfo         *SubscriptionUserinfo `json:"cachedSubscriptionUserinfo,omitempty"`
	PayloadBytes     *int                  `json:"cachedPayloadBytes,omitempty"`
}

// SchedulerConfig bundles per-source operating parameters.
type SchedulerConfig struct {
	DefaultTTL                    time.Duration
	DefaultStaleOnError           time.Duration
	BootstrapMaxAttemptsPerSource int
	BootstrapAttemptDelay         time.Duration
}

// Coordinator owns per-source goroutines that periodically refresh upstream
// data via the cache. It also tracks the bootstrap state machine: the Ready
// channel closes when every enabled source's bootstrap is terminal.
//
// FR-003a: client requests never trigger a fetch. The coordinator's goroutines
// are the only callers of cache.RefreshIfStale.
//
// FR-003b: the coordinator's bootstrap window gates the HTTP server.
type Coordinator struct {
	cache    *Cache
	fetcher  *UpstreamFetcher
	clock    clock.Clock
	cfg      SchedulerConfig
	log      *slog.Logger

	mu      sync.RWMutex
	sources map[string]*sourceCtx

	readyOnce sync.Once
	ready     chan struct{}

	wg sync.WaitGroup
}

type sourceCtx struct {
	row          config.SubscriptionRow
	bootstrap    BootstrapState
	lastResult   *FetchResult
}

// NewCoordinator constructs a Coordinator. Sources is the full set from the
// CSV; disabled rows are tracked but not fetched.
func NewCoordinator(
	cache *Cache,
	fetcher *UpstreamFetcher,
	clk clock.Clock,
	cfg SchedulerConfig,
	log *slog.Logger,
	sources []config.SubscriptionRow,
) *Coordinator {
	c := &Coordinator{
		cache:   cache,
		fetcher: fetcher,
		clock:   clk,
		cfg:     cfg,
		log:     log,
		sources: make(map[string]*sourceCtx, len(sources)),
		ready:   make(chan struct{}),
	}
	for _, row := range sources {
		bs := BootstrapPending
		if !row.Enable {
			// Disabled rows skip bootstrap entirely; they neither contribute to
			// the merged output nor block readiness.
			bs = BootstrapSucceeded
		}
		c.sources[row.Name] = &sourceCtx{row: row, bootstrap: bs}
	}
	return c
}

// Ready returns a channel that closes when bootstrap is complete (every
// enabled source is in a terminal state — either Succeeded or Failed).
func (c *Coordinator) Ready() <-chan struct{} { return c.ready }

// AllSucceeded reports whether every enabled source bootstrapped successfully.
// Call this after Ready closes; if false, the server should fail closed (503).
func (c *Coordinator) AllSucceeded() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, s := range c.sources {
		if !s.row.Enable {
			continue
		}
		if s.bootstrap != BootstrapSucceeded {
			return false
		}
	}
	return true
}

// SourceStates returns a sorted snapshot of every source's state for /health.
func (c *Coordinator) SourceStates() []SourceState {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]SourceState, 0, len(c.sources))
	for name, s := range c.sources {
		state := SourceState{
			Name:      name,
			Enabled:   s.row.Enable,
			Bootstrap: s.bootstrap,
		}
		if s.lastResult != nil {
			at := s.lastResult.AttemptedAt
			state.LastFetchedAt = &at
			state.LastOutcome = s.lastResult.Outcome
			state.LastError = s.lastResult.Error
		}
		if payload, ok := c.cache.Get(name); ok {
			ttl := c.ttlFor(s.row)
			if c.cache.State(name, ttl) == StateStale {
				state.ServingFromCache = true
			}
			state.Userinfo = payload.Headers.SubscriptionUserinfo
			pb := payload.PayloadBytes
			state.PayloadBytes = &pb
		}
		out = append(out, state)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Start launches the bootstrap pass + per-source background goroutines.
// Returns when the bootstrap pass for every enabled source has completed
// (Succeeded or Failed). Background refreshers continue until ctx is canceled.
func (c *Coordinator) Start(ctx context.Context) {
	enabled := c.enabledSources()

	if len(enabled) == 0 {
		// Nothing to bootstrap; signal ready immediately.
		c.signalReady()
		return
	}

	var bootstrapWG sync.WaitGroup
	bootstrapWG.Add(len(enabled))

	for _, row := range enabled {
		row := row
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			c.bootstrapAndRun(ctx, row, &bootstrapWG)
		}()
	}

	bootstrapWG.Wait()
	c.signalReady()
}

// Wait blocks until every per-source goroutine has exited (typically after
// the parent context is canceled). Useful for clean shutdown in tests.
func (c *Coordinator) Wait() { c.wg.Wait() }

func (c *Coordinator) enabledSources() []config.SubscriptionRow {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]config.SubscriptionRow, 0, len(c.sources))
	for _, s := range c.sources {
		if s.row.Enable {
			out = append(out, s.row)
		}
	}
	return out
}

func (c *Coordinator) signalReady() {
	c.readyOnce.Do(func() { close(c.ready) })
}

func (c *Coordinator) ttlFor(row config.SubscriptionRow) time.Duration {
	if refreshDisabled(row) {
		// Never refresh: report an effectively-infinite TTL so the cached
		// snapshot is never considered stale (steady-state + /health view).
		return neverRefreshTTL
	}
	if row.RefreshSeconds > 0 {
		return time.Duration(row.RefreshSeconds) * time.Second
	}
	return c.cfg.DefaultTTL
}

// fetchTTLFor is the TTL used to decide whether a fetch is needed. It mirrors
// ttlFor except that a never-refresh source uses the default interval here, so
// its one-shot bootstrap fetch still refreshes a stale disk-cached snapshot on
// process start (e.g. after the operator edited the source's link). Steady-state
// staleness/ticking still uses ttlFor's neverRefreshTTL, so the source is never
// re-fetched on a schedule within a running process.
func (c *Coordinator) fetchTTLFor(row config.SubscriptionRow) time.Duration {
	if refreshDisabled(row) {
		return c.cfg.DefaultTTL
	}
	return c.ttlFor(row)
}

func (c *Coordinator) staleWindowFor(row config.SubscriptionRow) time.Duration {
	if row.StaleOnErrorSeconds > 0 {
		return time.Duration(row.StaleOnErrorSeconds) * time.Second
	}
	return c.cfg.DefaultStaleOnError
}

// bootstrapAndRun handles the initial fetch attempts for one source then
// transitions to the steady-state ticker loop.
func (c *Coordinator) bootstrapAndRun(ctx context.Context, row config.SubscriptionRow, bootstrapWG *sync.WaitGroup) {
	bootstrapDone := false
	defer func() {
		if !bootstrapDone {
			bootstrapWG.Done()
		}
	}()

	maxAttempts := c.cfg.BootstrapMaxAttemptsPerSource
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	delay := c.cfg.BootstrapAttemptDelay

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return
		}
		_, _, err := c.refresh(ctx, row)
		if err == nil {
			c.markBootstrap(row.Name, BootstrapSucceeded)
			bootstrapDone = true
			bootstrapWG.Done()
			c.runSteady(ctx, row)
			return
		}
		c.log.Warn("bootstrap fetch failed",
			"source", row.Name,
			"attempt", attempt,
			"max", maxAttempts,
			"err", err.Error(),
		)
		if attempt == maxAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}

	c.markBootstrap(row.Name, BootstrapFailed)
	bootstrapDone = true
	bootstrapWG.Done()
	// Continue ticking — maybe upstream comes back. The server will fail
	// closed for the served subscription endpoint until /health flips green
	// (operator must restart, or wait for AllSucceeded() to be true on a
	// future tick — but that requires manual rebootstrap; for v1 we just
	// keep retrying on schedule).
	c.runSteady(ctx, row)
}

// runSteady is the background ticker loop for one source.
func (c *Coordinator) runSteady(ctx context.Context, row config.SubscriptionRow) {
	if refreshDisabled(row) {
		// RefreshSeconds < 0: the source is fetched once during bootstrap and
		// never refreshed again. Hold the goroutine until shutdown so cleanup
		// (Wait) still works.
		if _, ok := c.cache.Get(row.Name); ok {
			c.log.Info("source configured to never refresh", "source", row.Name)
		} else {
			// Bootstrap produced no usable payload; a never-refresh source has
			// no ticker to self-heal, so this stays failed until a restart.
			c.log.Warn("never-refresh source has no cached payload after bootstrap; will not retry until restart",
				"source", row.Name)
		}
		<-ctx.Done()
		return
	}

	ttl := c.ttlFor(row)
	ticker := time.NewTicker(ttl)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, _, err := c.refresh(ctx, row); err != nil {
				c.log.Warn("scheduled refresh failed",
					"source", row.Name,
					"err", err.Error(),
				)
				// If we've now exceeded the stale-on-error window with no
				// usable cache, drop the source so it stops contributing.
				if c.shouldDrop(row) {
					c.cache.Drop(row.Name)
					c.log.Warn("dropping source from cache (stale window exceeded)",
						"source", row.Name,
					)
				}
				// If a bootstrap-failed source came back, promote it.
			} else {
				c.maybePromoteBootstrap(row.Name)
			}
		}
	}
}

func (c *Coordinator) refresh(ctx context.Context, row config.SubscriptionRow) (*UpstreamCachedPayload, bool, error) {
	ttl := c.fetchTTLFor(row)
	payload, refreshed, err := c.cache.RefreshIfStale(ctx, row.Name, ttl, func(ctx context.Context) (*UpstreamCachedPayload, *FetchResult, error) {
		p, res, ferr := c.fetcher.Fetch(ctx, row)
		c.recordResult(row.Name, res)
		return p, res, ferr
	})
	if err == nil && refreshed {
		c.log.Info("upstream fetch ok",
			"source", row.Name,
			"payloadBytes", payload.PayloadBytes,
		)
	}
	return payload, refreshed, err
}

func (c *Coordinator) recordResult(name string, res *FetchResult) {
	if res == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.sources[name]; ok {
		s.lastResult = res
	}
}

func (c *Coordinator) markBootstrap(name string, state BootstrapState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.sources[name]; ok {
		s.bootstrap = state
	}
}

func (c *Coordinator) maybePromoteBootstrap(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.sources[name]; ok && s.bootstrap == BootstrapFailed {
		s.bootstrap = BootstrapSucceeded
		c.log.Info("source recovered after bootstrap failure", "source", name)
	}
}

// shouldDrop reports whether a source's last-failure window now exceeds the
// stale-on-error window — i.e., we have neither a fresh cache nor a fallback.
func (c *Coordinator) shouldDrop(row config.SubscriptionRow) bool {
	_, lastFailedAt := c.cache.LastFailure(row.Name)
	if lastFailedAt.IsZero() {
		return false
	}
	// We drop only if there's no usable cache (the entry is past its
	// stale-on-error window relative to the last successful fetch).
	c.cache.mu.RLock()
	defer c.cache.mu.RUnlock()
	e, ok := c.cache.byName[row.Name]
	if !ok || e.payload == nil {
		return false
	}
	staleWindow := c.staleWindowFor(row)
	return c.clock.Now().Sub(e.storedAt) > staleWindow
}

// ErrCoordinatorNotReady is returned by callers that try to read state
// before bootstrap completes (informational; the HTTP route uses Ready).
var ErrCoordinatorNotReady = errors.New("coordinator not ready")

// ForceRefresh fetches every enabled source NOW, bypassing the cache TTL
// check. Updates the cache and per-source state. Drops a source from cache
// if its stale-on-error window is exceeded.
//
// Primarily for tests that need to exercise the degraded / failed-no-cache
// code paths without manipulating the time.Ticker schedule. Production
// wiring uses runSteady's ticker loop.
func (c *Coordinator) ForceRefresh(ctx context.Context) {
	var wg sync.WaitGroup
	for _, row := range c.enabledSources() {
		row := row
		wg.Add(1)
		go func() {
			defer wg.Done()
			payload, res, err := c.fetcher.Fetch(ctx, row)
			c.recordResult(row.Name, res)
			if err != nil {
				c.cache.markFailed(row.Name, err)
				if c.shouldDrop(row) {
					c.cache.Drop(row.Name)
					c.log.Warn("dropping source from cache after force refresh (stale window exceeded)",
						"source", row.Name)
				}
				return
			}
			_ = c.cache.set(row.Name, payload, c.clock.Now())
			c.maybePromoteBootstrap(row.Name)
		}()
	}
	wg.Wait()
}
