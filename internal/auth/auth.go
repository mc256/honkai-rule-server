// Package auth provides token-validation middleware and a context helper
// for retrieving the validated token record inside handlers. Lives in its
// own package so server/ and server/routes/ can both depend on it without
// creating an import cycle.
package auth

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/mc256/honkai-rule-server/internal/config"
	"github.com/mc256/honkai-rule-server/internal/observability"
)

// contextKey is unexported so callers must use TokenFromContext.
type contextKey struct{ name string }

var tokenCtxKey = contextKey{name: "token"}

// RequireToken returns middleware that:
//   - Extracts ?token=<value> from the URL query.
//   - Looks it up in the TokenStore (rejects revoked / expired records).
//   - On rejection: 401, EMPTY body, no quota-bearing headers (FR-019b).
//   - On success: injects the *TokenRecord into the request context so
//     handlers can log which token was used.
//
// Per FR-014 / SC-011, the token plaintext NEVER reaches log output —
// observability.SanitizeToken produces the field value used in logs.
func RequireToken(store *config.TokenStore, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenStr := r.URL.Query().Get("token")
			if tokenStr == "" {
				log.Info("auth rejected: missing token",
					"remote", r.RemoteAddr,
					"path", r.URL.Path,
				)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			rec, ok := store.Lookup(tokenStr)
			if !ok {
				log.Info("auth rejected: invalid/revoked/expired token",
					"token_hash", observability.SanitizeToken(tokenStr),
					"remote", r.RemoteAddr,
					"path", r.URL.Path,
				)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), tokenCtxKey, rec)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// TokenFromContext returns the *TokenRecord injected by RequireToken, or
// (nil, false) if the request did not pass through that middleware.
func TokenFromContext(ctx context.Context) (*config.TokenRecord, bool) {
	rec, ok := ctx.Value(tokenCtxKey).(*config.TokenRecord)
	return rec, ok
}
