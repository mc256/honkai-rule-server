package config

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/mc256/honkai-rule-server/internal/clock"
	"github.com/mc256/honkai-rule-server/internal/observability"
)

// TokenRecord is one entry in the tokens JSON file. See data-model.md §TokenStore.
type TokenRecord struct {
	Token     string     `json:"token"`
	Label     string     `json:"label"`
	IssuedAt  time.Time  `json:"issued_at"`
	Revoked   bool       `json:"revoked"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// TokenStore holds per-client tokens loaded from a JSON file. Hot-reloadable
// via Watch. All accessors are safe for concurrent use.
//
// FR-019, FR-019a: Lookup returns (nil, false) for unknown, revoked, or
// expired tokens. The Token plaintext MUST never reach logs (use
// observability.SanitizeToken).
type TokenStore struct {
	mu      sync.RWMutex
	byToken map[string]*TokenRecord
	clock   clock.Clock
}

// NewTokenStore returns an empty store. Call Load to populate.
func NewTokenStore(clk clock.Clock) *TokenStore {
	return &TokenStore{
		byToken: make(map[string]*TokenRecord),
		clock:   clk,
	}
}

// Load reads the JSON file at path and replaces the in-memory store on
// success. On failure the previous store contents are preserved (FR-017).
func (s *TokenStore) Load(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("token store: read %s: %w", path, err)
	}
	var doc struct {
		Tokens []*TokenRecord `json:"tokens"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("token store: parse %s: %w", path, err)
	}

	next := make(map[string]*TokenRecord, len(doc.Tokens))
	for _, rec := range doc.Tokens {
		if rec.Token == "" {
			return fmt.Errorf("token store: %s contains a record with empty token", path)
		}
		if _, dup := next[rec.Token]; dup {
			return fmt.Errorf("token store: %s contains duplicate token (label %q)", path, rec.Label)
		}
		next[rec.Token] = rec
	}

	s.mu.Lock()
	s.byToken = next
	s.mu.Unlock()
	return nil
}

// Lookup returns (record, true) if the token is present, not revoked, and
// not expired; (nil, false) otherwise. Never logs the token.
func (s *TokenStore) Lookup(token string) (*TokenRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.byToken[token]
	if !ok || rec.Revoked {
		return nil, false
	}
	if rec.ExpiresAt != nil && s.clock.Now().After(*rec.ExpiresAt) {
		return nil, false
	}
	return rec, true
}

// ActiveCount reports the number of non-revoked, non-expired tokens. Useful
// for startup logging.
func (s *TokenStore) ActiveCount() int {
	now := s.clock.Now()
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, rec := range s.byToken {
		if rec.Revoked {
			continue
		}
		if rec.ExpiresAt != nil && now.After(*rec.ExpiresAt) {
			continue
		}
		n++
	}
	return n
}

// RevokedCount reports the number of records marked revoked.
func (s *TokenStore) RevokedCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, rec := range s.byToken {
		if rec.Revoked {
			n++
		}
	}
	return n
}

// Watch starts a goroutine that fsnotify-watches the parent directory of
// path; on relevant events (Create / Write of `path`), it debounces 250ms
// and calls Load. Reload errors are logged but do not stop the watcher.
//
// On every successful reload, an empty struct is sent to `updates` (non-
// blocking — the channel may have buffered or unbuffered consumers; if no
// receiver is ready the signal is dropped). Callers may pass `nil` to
// disable update signaling.
//
// The watcher exits when ctx is canceled.
func (s *TokenStore) Watch(ctx context.Context, path string, log *slog.Logger, updates chan<- struct{}) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("token store watcher: %w", err)
	}
	dir := filepath.Dir(path)
	wantBase := filepath.Base(path)
	if err := w.Add(dir); err != nil {
		w.Close()
		return fmt.Errorf("token store watcher: add %s: %w", dir, err)
	}

	go s.watchLoop(ctx, w, path, wantBase, log, updates)
	return nil
}

const tokenReloadDebounce = 250 * time.Millisecond

func (s *TokenStore) watchLoop(
	ctx context.Context,
	w *fsnotify.Watcher,
	path, wantBase string,
	log *slog.Logger,
	updates chan<- struct{},
) {
	defer w.Close()

	var debounceTimer *time.Timer
	triggerReload := func() {
		if debounceTimer != nil {
			debounceTimer.Stop()
		}
		debounceTimer = time.AfterFunc(tokenReloadDebounce, func() {
			if err := s.Load(path); err != nil {
				log.Warn("token store reload failed (keeping previous)", "err", err)
				return
			}
			log.Info("token store reloaded",
				"active_tokens", s.ActiveCount(),
				"revoked_tokens", s.RevokedCount(),
			)
			if updates != nil {
				select {
				case updates <- struct{}{}:
				default:
				}
			}
		})
	}

	for {
		select {
		case <-ctx.Done():
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			if filepath.Base(ev.Name) != wantBase {
				continue
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}
			triggerReload()
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			log.Warn("token store watcher error", "err", err)
		}
	}
}

// SanitizedToken is a convenience wrapper so callers don't import the
// observability package twice; identical to observability.SanitizeToken.
func SanitizedToken(t string) string { return observability.SanitizeToken(t) }
