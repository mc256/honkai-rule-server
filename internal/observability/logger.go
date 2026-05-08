// Package observability provides the project's logger and security helpers.
// Per spec FR-013, FR-014, SC-011.
package observability

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
)

// New returns a JSON-handler slog.Logger writing to stdout (production) or w
// (tests). If w is nil, os.Stdout is used.
func New(level slog.Level, w io.Writer) *slog.Logger {
	if w == nil {
		w = os.Stdout
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}

// SanitizeToken returns a stable short fingerprint of a token, suitable for
// log fields. Format: "sha256:" + first 12 hex chars of SHA-256(token).
//
// FR-014 / SC-011: token plaintext MUST NOT appear in logs. Every code path
// that wants to log "which token did this" MUST go through this helper.
func SanitizeToken(token string) string {
	if token == "" {
		return "sha256:empty"
	}
	sum := sha256.Sum256([]byte(token))
	return "sha256:" + hex.EncodeToString(sum[:6])
}
