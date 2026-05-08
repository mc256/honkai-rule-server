package auth

import (
	"log/slog"
	"net/http"
	"strings"
)

// RequireUserAgent returns middleware that validates the User-Agent header
// against a list of allowed prefixes. If prefixes is nil or empty, all
// requests pass through. Non-matching requests receive 403 Forbidden.
// Matching is case-sensitive using strings.HasPrefix.
func RequireUserAgent(prefixes []string, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// If no prefixes configured, pass through
			if len(prefixes) == 0 {
				next.ServeHTTP(w, r)
				return
			}

			ua := r.Header.Get("User-Agent")
			if ua == "" {
				log.Info("ua-rejected",
					"user_agent", "",
					"remote", r.RemoteAddr,
					"path", r.URL.Path,
				)
				w.WriteHeader(http.StatusForbidden)
				return
			}

			// Check if UA matches any prefix
			for _, prefix := range prefixes {
				if strings.HasPrefix(ua, prefix) {
					next.ServeHTTP(w, r)
					return
				}
			}

			// No match
			log.Info("ua-rejected",
				"user_agent", ua,
				"remote", r.RemoteAddr,
				"path", r.URL.Path,
			)
			w.WriteHeader(http.StatusForbidden)
		})
	}
}
