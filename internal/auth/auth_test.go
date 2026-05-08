package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mc256/honkai-rule-server/internal/clock"
	"github.com/mc256/honkai-rule-server/internal/config"
)

// helper: write a tokens.json fixture and return a loaded store.
func newStoreWithTokens(t *testing.T) *config.TokenStore {
	t.Helper()
	const doc = `{
		"tokens": [
			{"token": "valid-token-aaaa", "label": "laptop", "issued_at": "2026-04-30T00:00:00Z", "revoked": false},
			{"token": "revoked-token-bbb", "label": "lost-phone", "issued_at": "2026-04-29T00:00:00Z", "revoked": true}
		]
	}`
	p := filepath.Join(t.TempDir(), "tokens.json")
	if err := os.WriteFile(p, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	store := config.NewTokenStore(clock.RealClock{})
	if err := store.Load(p); err != nil {
		t.Fatal(err)
	}
	return store
}

// shouldNotReachHandler is a sentinel handler used to assert that the
// auth middleware blocked the request before delegating downstream.
func shouldNotReachHandler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("downstream handler should not have been reached")
		w.WriteHeader(500)
	}
}

func TestAuth_NoToken(t *testing.T) {
	store := newStoreWithTokens(t)
	logBuf := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	mw := RequireToken(store, log)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	mw(shouldNotReachHandler(t)).ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	body, _ := readBody(resp)
	if len(body) != 0 {
		t.Errorf("body = %q, want empty", body)
	}
	if !strings.Contains(logBuf.String(), "missing token") {
		t.Errorf("log missing rejection reason: %s", logBuf.String())
	}
}

func TestAuth_UnknownToken_HashedInLogs(t *testing.T) {
	store := newStoreWithTokens(t)
	logBuf := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	mw := RequireToken(store, log)

	const bogus = "bogus-token-xyzzy12345678901234567890"
	req := httptest.NewRequest("GET", "/?token="+bogus, nil)
	w := httptest.NewRecorder()
	mw(shouldNotReachHandler(t)).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}

	logOut := logBuf.String()
	if strings.Contains(logOut, bogus) {
		t.Errorf("log leaked token plaintext: %s", logOut)
	}
	// Verify the structured log carries token_hash with sha256: prefix.
	dec := json.NewDecoder(strings.NewReader(logOut))
	for dec.More() {
		var ev map[string]any
		if err := dec.Decode(&ev); err != nil {
			break
		}
		if hash, ok := ev["token_hash"].(string); ok {
			if !strings.HasPrefix(hash, "sha256:") {
				t.Errorf("token_hash = %q, want sha256: prefix", hash)
			}
			return
		}
	}
	t.Errorf("no log line carried token_hash field; log=%s", logOut)
}

func TestAuth_RevokedToken(t *testing.T) {
	store := newStoreWithTokens(t)
	log := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	mw := RequireToken(store, log)

	req := httptest.NewRequest("GET", "/?token=revoked-token-bbb", nil)
	w := httptest.NewRecorder()
	mw(shouldNotReachHandler(t)).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for revoked", w.Code)
	}
}

func TestAuth_ValidToken_DelegatesToHandler(t *testing.T) {
	store := newStoreWithTokens(t)
	log := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	mw := RequireToken(store, log)

	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		rec, ok := TokenFromContext(r.Context())
		if !ok {
			t.Errorf("TokenFromContext returned no record")
		}
		if rec.Label != "laptop" {
			t.Errorf("record label = %q, want laptop", rec.Label)
		}
		w.WriteHeader(200)
	})

	req := httptest.NewRequest("GET", "/?token=valid-token-aaaa", nil)
	w := httptest.NewRecorder()
	mw(handler).ServeHTTP(w, req)

	if !called {
		t.Fatal("handler not called")
	}
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestTokenFromContext_AbsentReturnsFalse(t *testing.T) {
	if rec, ok := TokenFromContext(context.Background()); ok || rec != nil {
		t.Errorf("TokenFromContext on bare ctx = (%v, %v), want (nil, false)", rec, ok)
	}
}

func readBody(r *http.Response) ([]byte, error) {
	defer r.Body.Close()
	buf := &bytes.Buffer{}
	if _, err := buf.ReadFrom(r.Body); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
