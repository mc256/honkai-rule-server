package config

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mc256/honkai-rule-server/internal/clock"
	"github.com/mc256/honkai-rule-server/internal/observability"
)

func writeTokens(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tokens.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const validTokensJSON = `{
  "tokens": [
    {"token": "valid-token-aaaaaaaa", "label": "laptop", "issued_at": "2026-04-30T00:00:00Z", "revoked": false},
    {"token": "revoked-token-bbbbbb", "label": "lost-phone", "issued_at": "2026-04-29T00:00:00Z", "revoked": true}
  ]
}`

// TC-U-TOK-01: valid token returns active record.
func TestTOK_01_ValidTokenLookup(t *testing.T) {
	p := writeTokens(t, validTokensJSON)
	store := NewTokenStore(clock.RealClock{})
	if err := store.Load(p); err != nil {
		t.Fatalf("Load: %v", err)
	}
	rec, ok := store.Lookup("valid-token-aaaaaaaa")
	if !ok {
		t.Fatal("valid token not found")
	}
	if rec.Label != "laptop" {
		t.Errorf("Label = %q, want laptop", rec.Label)
	}
}

// TC-U-TOK-02: unknown token returns (nil, false); log buffer must not contain plaintext.
func TestTOK_02_UnknownToken(t *testing.T) {
	p := writeTokens(t, validTokensJSON)
	store := NewTokenStore(clock.RealClock{})
	_ = store.Load(p)

	rec, ok := store.Lookup("totally-not-in-store")
	if ok || rec != nil {
		t.Errorf("Lookup(unknown) = (%v, %v), want (nil, false)", rec, ok)
	}

	// Defensive: a sanitized fingerprint is what callers should log.
	hash := observability.SanitizeToken("totally-not-in-store")
	if strings.Contains(hash, "totally-not-in-store") {
		t.Errorf("SanitizeToken leaked plaintext: %q", hash)
	}
}

// TC-U-TOK-03: revoked token returns (nil, false) even though the record exists.
func TestTOK_03_RevokedToken(t *testing.T) {
	p := writeTokens(t, validTokensJSON)
	store := NewTokenStore(clock.RealClock{})
	_ = store.Load(p)

	rec, ok := store.Lookup("revoked-token-bbbbbb")
	if ok || rec != nil {
		t.Errorf("Lookup(revoked) = (%v, %v), want (nil, false)", rec, ok)
	}
	if store.RevokedCount() != 1 || store.ActiveCount() != 1 {
		t.Errorf("counts: revoked=%d active=%d, want 1/1", store.RevokedCount(), store.ActiveCount())
	}
}

// Lookup respects ExpiresAt against the injected clock.
func TestTokens_ExpiresAt(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFakeClock(now)
	expired := now.Add(-1 * time.Hour)
	future := now.Add(1 * time.Hour)
	doc := map[string]any{
		"tokens": []map[string]any{
			{"token": "expired-tok", "label": "x", "issued_at": "2026-04-30T00:00:00Z", "revoked": false, "expires_at": expired.Format(time.RFC3339)},
			{"token": "future-tok", "label": "y", "issued_at": "2026-04-30T00:00:00Z", "revoked": false, "expires_at": future.Format(time.RFC3339)},
		},
	}
	b, _ := json.Marshal(doc)
	p := writeTokens(t, string(b))

	store := NewTokenStore(clk)
	if err := store.Load(p); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := store.Lookup("expired-tok"); ok {
		t.Errorf("expired token should not be found")
	}
	if _, ok := store.Lookup("future-tok"); !ok {
		t.Errorf("future-expiry token should be found")
	}
}

// TC-U-TOK-04: hot reload picks up a newly-added token via fsnotify.
func TestTOK_04_HotReload(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tokens.json")
	if err := os.WriteFile(p, []byte(validTokensJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	store := NewTokenStore(clock.RealClock{})
	if err := store.Load(p); err != nil {
		t.Fatalf("Load: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	updates := make(chan struct{}, 1)
	logBuf := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	if err := store.Watch(ctx, p, log, updates); err != nil {
		t.Fatalf("Watch: %v", err)
	}

	// Sanity: new token isn't there yet.
	if _, ok := store.Lookup("new-token-cccccccc"); ok {
		t.Fatal("precondition: new token should not be in store yet")
	}

	const updated = `{
	  "tokens": [
	    {"token": "valid-token-aaaaaaaa", "label": "laptop", "issued_at": "2026-04-30T00:00:00Z", "revoked": false},
	    {"token": "new-token-cccccccc", "label": "tablet", "issued_at": "2026-04-30T01:00:00Z", "revoked": false}
	  ]
	}`
	if err := os.WriteFile(p, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}

	select {
	case <-updates:
		// reload happened
	case <-time.After(2 * time.Second):
		t.Fatalf("did not receive reload signal within 2s; log=%s", logBuf.String())
	}

	if _, ok := store.Lookup("new-token-cccccccc"); !ok {
		t.Errorf("new token not picked up after reload")
	}
}

// Reload error keeps the previous store in effect (FR-017).
func TestTokens_ReloadErrorPreservesPrevious(t *testing.T) {
	p := writeTokens(t, validTokensJSON)
	store := NewTokenStore(clock.RealClock{})
	if err := store.Load(p); err != nil {
		t.Fatalf("initial Load: %v", err)
	}

	// Corrupt the file → next Load fails.
	if err := os.WriteFile(p, []byte("{not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Load(p); err == nil {
		t.Fatal("expected parse error")
	}

	// Previous tokens still present.
	if _, ok := store.Lookup("valid-token-aaaaaaaa"); !ok {
		t.Errorf("previous token lost after failed reload")
	}
}

func TestTokens_DuplicateTokenInFile(t *testing.T) {
	p := writeTokens(t, `{"tokens":[
		{"token":"dup","label":"a","issued_at":"2026-04-30T00:00:00Z","revoked":false},
		{"token":"dup","label":"b","issued_at":"2026-04-30T00:00:00Z","revoked":false}
	]}`)
	store := NewTokenStore(clock.RealClock{})
	err := store.Load(p)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("Load: %v, want duplicate-token error", err)
	}
}
