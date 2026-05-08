package fetcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
	"gopkg.in/yaml.v3"

	"github.com/mc256/honkai-rule-server/internal/clock"
)

// CacheState describes the freshness of a cached entry as the scheduler sees it.
type CacheState string

const (
	StateMissing CacheState = "missing"
	StateFresh   CacheState = "fresh"
	StateStale   CacheState = "stale" // payload exists but TTL expired
)

// cacheEntry holds the payload plus per-source bookkeeping that drives the
// stale-on-error logic. Not exported.
type cacheEntry struct {
	payload      *UpstreamCachedPayload
	storedAt     time.Time
	lastError    string
	lastFailedAt time.Time
}

// Cache is the in-memory cache of upstream payloads, optionally backed by
// disk JSON snapshots so a pod restart doesn't re-hammer upstreams (R8).
//
// FR-003: per-source TTL + stale-on-error window. The cache holds the data;
// the scheduler decides when to refresh.
//
// All accessors are safe for concurrent use.
type Cache struct {
	mu     sync.RWMutex
	byName map[string]*cacheEntry
	clock  clock.Clock
	dir    string // disk persistence dir; "" disables disk writes
	sf     singleflight.Group
}

// FetchFunc is the closure passed by the scheduler to RefreshIfStale.
type FetchFunc func(ctx context.Context) (*UpstreamCachedPayload, *FetchResult, error)

// NewCache returns an empty cache. Pass dir="" to disable disk persistence.
func NewCache(clk clock.Clock, dir string) *Cache {
	return &Cache{
		byName: make(map[string]*cacheEntry),
		clock:  clk,
		dir:    dir,
	}
}

// Get returns the most recent cached payload for name, regardless of
// staleness. The HTTP route uses this — it never triggers a fetch (FR-003a).
func (c *Cache) Get(name string) (*UpstreamCachedPayload, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.byName[name]
	if !ok || e.payload == nil {
		return nil, false
	}
	return e.payload, true
}

// State reports the freshness of the cached entry given a TTL.
func (c *Cache) State(name string, ttl time.Duration) CacheState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.byName[name]
	if !ok || e.payload == nil {
		return StateMissing
	}
	if c.clock.Now().Sub(e.storedAt) < ttl {
		return StateFresh
	}
	return StateStale
}

// Names returns the sorted set of source names currently in the cache.
func (c *Cache) Names() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.byName))
	for n := range c.byName {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// set stores in memory and (if dir is set) writes a JSON snapshot to disk.
// Disk write failures are logged but do not fail the in-memory store.
func (c *Cache) set(name string, payload *UpstreamCachedPayload, storedAt time.Time) error {
	c.mu.Lock()
	e, ok := c.byName[name]
	if !ok {
		e = &cacheEntry{}
		c.byName[name] = e
	}
	e.payload = payload
	e.storedAt = storedAt
	e.lastError = ""
	e.lastFailedAt = time.Time{}
	c.mu.Unlock()

	if c.dir == "" {
		return nil
	}
	return c.persist(name, payload, storedAt)
}

// markFailed records that a refresh attempt failed; the previous payload
// remains in cache and may be served stale within the stale-on-error window.
func (c *Cache) markFailed(name string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.byName[name]
	if !ok {
		// First-ever attempt failed and we have no prior payload; record
		// the failure so callers can introspect.
		e = &cacheEntry{}
		c.byName[name] = e
	}
	e.lastError = err.Error()
	e.lastFailedAt = c.clock.Now()
}

// LastFailure returns the most recent error and timestamp for source name,
// or zero values if there has been no failure recorded.
func (c *Cache) LastFailure(name string) (string, time.Time) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if e, ok := c.byName[name]; ok {
		return e.lastError, e.lastFailedAt
	}
	return "", time.Time{}
}

// Drop removes a source from the cache (used when a source falls past its
// stale-on-error window).
func (c *Cache) Drop(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.byName, name)
	if c.dir != "" {
		_ = os.Remove(c.diskPath(name))
	}
}

// RefreshIfStale runs fetchFn (single-flighted across concurrent callers)
// when the cache entry is missing or older than ttl. Returns the (possibly
// pre-existing) payload, whether a refresh was actually performed, and any
// error from the fetch attempt.
//
// On a fetch error, the previous payload (if any) stays in cache and is
// returned; the error is also recorded via markFailed so the scheduler can
// decide whether the stale-on-error window has elapsed.
//
// FR-003a, TC-U-CACHE-04, TC-I-12: 100 concurrent calls collapse to 1 fetch.
func (c *Cache) RefreshIfStale(
	ctx context.Context,
	name string,
	ttl time.Duration,
	fetchFn FetchFunc,
) (*UpstreamCachedPayload, bool, error) {
	if c.State(name, ttl) == StateFresh {
		p, _ := c.Get(name)
		return p, false, nil
	}

	type result struct {
		payload  *UpstreamCachedPayload
		refreshed bool
	}
	v, err, _ := c.sf.Do(name, func() (any, error) {
		// Re-check under the singleflight gate in case a sibling call already refreshed.
		if c.State(name, ttl) == StateFresh {
			p, _ := c.Get(name)
			return result{payload: p, refreshed: false}, nil
		}
		now := c.clock.Now()
		p, _, ferr := fetchFn(ctx)
		if ferr != nil {
			c.markFailed(name, ferr)
			existing, _ := c.Get(name)
			return result{payload: existing, refreshed: true}, ferr
		}
		if err := c.set(name, p, now); err != nil {
			return result{payload: p, refreshed: true}, err
		}
		return result{payload: p, refreshed: true}, nil
	})
	r := v.(result)
	return r.payload, r.refreshed, err
}

// LoadFromDisk rehydrates the in-memory cache from JSON snapshots in dir.
// Files that fail to parse are skipped with a warning log; missing dir is
// not an error (cold start with no prior cache).
func (c *Cache) LoadFromDisk(ctx context.Context, log *slog.Logger) error {
	if c.dir == "" {
		return nil
	}
	entries, err := os.ReadDir(c.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cache: read dir %s: %w", c.dir, err)
	}
	for _, ent := range entries {
		if ent.IsDir() || filepath.Ext(ent.Name()) != ".json" {
			continue
		}
		name := ent.Name()[:len(ent.Name())-len(".json")]
		full := filepath.Join(c.dir, ent.Name())
		b, err := os.ReadFile(full)
		if err != nil {
			log.Warn("cache: read disk snapshot failed", "path", full, "err", err)
			continue
		}
		var payload UpstreamCachedPayload
		if err := json.Unmarshal(b, &payload); err != nil {
			log.Warn("cache: parse disk snapshot failed", "path", full, "err", err)
			continue
		}
		// Eagerly parse so concurrent Build() readers don't race on lazy init.
		var parsed yaml.Node
		if err := yaml.Unmarshal(payload.BodyYAML, &parsed); err != nil {
			log.Warn("cache: cached body fails YAML re-parse", "source", name, "err", err)
			continue
		}
		payload.SetParsed(&parsed)
		c.mu.Lock()
		c.byName[name] = &cacheEntry{
			payload:  &payload,
			storedAt: payload.FetchedAt,
		}
		c.mu.Unlock()
	}
	return nil
}

func (c *Cache) diskPath(name string) string {
	return filepath.Join(c.dir, name+".json")
}

func (c *Cache) persist(name string, payload *UpstreamCachedPayload, storedAt time.Time) error {
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return fmt.Errorf("cache: mkdir %s: %w", c.dir, err)
	}
	tmp, err := os.CreateTemp(c.dir, name+".*.tmp")
	if err != nil {
		return fmt.Errorf("cache: temp file: %w", err)
	}
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(payload); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("cache: encode: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("cache: close: %w", err)
	}
	if err := os.Rename(tmp.Name(), c.diskPath(name)); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("cache: rename: %w", err)
	}
	_ = storedAt // reserved for future use (e.g., per-file mtime control)
	return nil
}
