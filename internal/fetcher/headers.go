// Package fetcher implements upstream subscription fetching, header parsing,
// caching, and per-source background scheduling. The HTTP/cache/scheduler
// layers are the only sources of nondeterminism; they are kept isolated from
// the merge layer (Constitution Principle II).
package fetcher

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// SubscriptionUserinfo holds the parsed Subscription-Userinfo header values.
// Wire format (FR-005b): upload=N; download=N; total=N; expire=N
// expire == 0 conventionally means "no expiry" and MUST NOT be misinterpreted
// as "expired in 1970" (FR-010).
//
// JSON tags use lowercase to match contracts/health.openapi.yaml.
type SubscriptionUserinfo struct {
	Upload   int64 `json:"upload"`
	Download int64 `json:"download"`
	Total    int64 `json:"total"`
	Expire   int64 `json:"expire"` // unix seconds; 0 means no expiry
}

// ErrSubscriptionUserinfoUnparseable signals the header was present but
// did not match the expected wire format.
var ErrSubscriptionUserinfoUnparseable = errors.New("subscription-userinfo header unparseable")

// ParseSubscriptionUserinfo parses a Subscription-Userinfo header value.
// Returns (parsed, missing, error). `missing` lists field names that were
// absent from the input (so the caller can decide whether to discard the
// source per FR-012). `error` is non-nil only if the input is structurally
// malformed beyond recovery.
//
// Real-world example (from upstream.example.com):
//
//	upload=23398198706; download=203036431271; total=654791671808; expire=1804180937
func ParseSubscriptionUserinfo(s string) (*SubscriptionUserinfo, []string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil, fmt.Errorf("%w: empty header", ErrSubscriptionUserinfoUnparseable)
	}

	out := &SubscriptionUserinfo{}
	seen := map[string]bool{}

	// Fields are semicolon-separated; whitespace around the separator is permitted.
	for _, raw := range strings.Split(s, ";") {
		field := strings.TrimSpace(raw)
		if field == "" {
			continue
		}
		eq := strings.IndexByte(field, '=')
		if eq <= 0 {
			return nil, nil, fmt.Errorf("%w: malformed pair %q", ErrSubscriptionUserinfoUnparseable, field)
		}
		key := strings.ToLower(strings.TrimSpace(field[:eq]))
		val := strings.TrimSpace(field[eq+1:])
		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %s value %q is not an integer", ErrSubscriptionUserinfoUnparseable, key, val)
		}
		switch key {
		case "upload":
			out.Upload = n
			seen["upload"] = true
		case "download":
			out.Download = n
			seen["download"] = true
		case "total":
			out.Total = n
			seen["total"] = true
		case "expire":
			out.Expire = n
			seen["expire"] = true
		}
		// Unknown keys are silently ignored — the spec does not pin them, and
		// providers occasionally invent new ones (e.g., "reset").
	}

	var missing []string
	for _, want := range []string{"upload", "download", "total", "expire"} {
		if !seen[want] {
			missing = append(missing, want)
		}
	}

	// If everything is missing, the input was structurally a header but
	// contained no recognizable fields — treat as unparseable.
	if len(missing) == 4 {
		return nil, nil, fmt.Errorf("%w: no recognized fields in %q", ErrSubscriptionUserinfoUnparseable, s)
	}

	return out, missing, nil
}

// ParseProfileUpdateInterval parses a Profile-Update-Interval header value.
// Returns (hours, ok). ok is false if the input is empty or not an integer.
// FR-005b: unit is integer hours.
func ParseProfileUpdateInterval(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}
