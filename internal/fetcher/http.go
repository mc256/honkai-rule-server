package fetcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/mc256/honkai-rule-server/internal/config"
)

// FetchOutcome enumerates the possible results of one fetch attempt.
type FetchOutcome string

const (
	OutcomeSuccess      FetchOutcome = "success"
	OutcomeHTTPError    FetchOutcome = "http_error"
	OutcomeTimeout      FetchOutcome = "timeout"
	OutcomeParseError   FetchOutcome = "parse_error"
	OutcomeNetworkError FetchOutcome = "network_error"
)

// FetchResult is the outcome record for one fetch attempt; emitted to logs
// per FR-013 and consumed by the bootstrap state machine.
type FetchResult struct {
	SourceName   string
	AttemptedAt  time.Time
	Outcome      FetchOutcome
	HTTPStatus   int
	PayloadBytes int
	Error        string // sanitized; empty on success
	Duration     time.Duration
}

// UpstreamHeaders is the parsed-headers subset we care about per FR-005b.
type UpstreamHeaders struct {
	SubscriptionUserinfo       *SubscriptionUserinfo
	ProfileUpdateIntervalHours *int
}

// UpstreamCachedPayload holds one source's most recent successful fetch.
// Persisted to disk per FR-003 / research R8.
type UpstreamCachedPayload struct {
	SourceName   string          `json:"source_name"`
	FetchedAt    time.Time       `json:"fetched_at"`
	BodyYAML     []byte          `json:"body_yaml"`
	Headers      UpstreamHeaders `json:"headers"`
	PayloadBytes int             `json:"payload_bytes"`

	// ParsedConfig is reconstructed from BodyYAML on first access via Parse.
	// Not serialized to disk (BodyYAML is the source of truth).
	parsedConfig *yaml.Node
}

// Parse returns the pre-parsed yaml.Node tree. The parsing happens eagerly
// at construction time (in Fetch and LoadFromDisk) so concurrent readers
// from the merge layer never write to parsedConfig — a race the -race
// detector flagged when many client requests hit the cache simultaneously.
//
// If parsedConfig is somehow nil (defensive — should not happen after
// the constructors), Parse falls back to a one-shot parse that mutates
// the field; this path is not concurrency-safe and returns an error
// callers should treat as a programmer error.
func (p *UpstreamCachedPayload) Parse() (*yaml.Node, error) {
	if p.parsedConfig != nil {
		return p.parsedConfig, nil
	}
	var node yaml.Node
	if err := yaml.Unmarshal(p.BodyYAML, &node); err != nil {
		return nil, err
	}
	p.parsedConfig = &node
	return p.parsedConfig, nil
}

// SetParsed eagerly stores the parsed yaml.Node so concurrent Parse()
// callers (e.g., from N concurrent HTTP requests landing on Build()) can
// read parsedConfig without racing on its assignment.
func (p *UpstreamCachedPayload) SetParsed(n *yaml.Node) {
	p.parsedConfig = n
}

// HTTPClient is satisfied by *http.Client; injected so tests can substitute.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// UpstreamFetcher fetches one source's subscription payload + headers.
type UpstreamFetcher struct {
	Client    HTTPClient
	Timeout   time.Duration
	UserAgent string // sent as the User-Agent header on every fetch; must identify as a Clash client (see DefaultUserAgent)
	Logger    *slog.Logger
}

// DefaultUserAgent is what we send on upstream fetches by default.
//
// Most providers content-negotiate by UA: with a generic UA they serve
// V2Ray / Sing-box format (base64-encoded URL list); with a Clash-flavored
// UA they serve the Clash YAML config + Subscription-Userinfo header.
//
// `clash-meta` is the standard identifier the Mihomo client uses internally
// to stay compatible with naive provider classifiers that just substring-
// match for "clash". The bare `mihomo` name is rejected by some providers
// (e.g., provider.example.com's classifier doesn't recognize it). Operators can
// override via UPSTREAM_USER_AGENT env var if their provider needs
// something specific (e.g., "ClashforWindows/0.20.39").
const DefaultUserAgent = "clash-meta/v1.18.0"

// NewUpstreamFetcher returns a fetcher with sensible defaults.
func NewUpstreamFetcher(timeout time.Duration, log *slog.Logger) *UpstreamFetcher {
	return &UpstreamFetcher{
		Client: &http.Client{
			// Per-request timeout via context; this is a backstop.
			Timeout: timeout + 5*time.Second,
		},
		Timeout:   timeout,
		UserAgent: DefaultUserAgent,
		Logger:    log,
	}
}

// Fetch performs one upstream fetch. Returns (payload, result, err). The
// result is non-nil regardless of err so the caller can record it.
func (f *UpstreamFetcher) Fetch(ctx context.Context, source config.SubscriptionRow) (*UpstreamCachedPayload, *FetchResult, error) {
	start := time.Now()
	res := &FetchResult{
		SourceName:  source.Name,
		AttemptedAt: start,
	}

	ctx, cancel := context.WithTimeout(ctx, f.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source.Link, nil)
	if err != nil {
		res.Outcome = OutcomeNetworkError
		res.Error = sanitizeURLInError(err.Error(), source.Link)
		res.Duration = time.Since(start)
		return nil, res, fmt.Errorf("upstream %q: build request: %w", source.Name, err)
	}

	// Providers content-negotiate by UA — see DefaultUserAgent comment.
	ua := f.UserAgent
	if ua == "" {
		ua = DefaultUserAgent
	}
	req.Header.Set("User-Agent", ua)

	resp, err := f.Client.Do(req)
	if err != nil {
		res.Duration = time.Since(start)
		if errors.Is(err, context.DeadlineExceeded) {
			res.Outcome = OutcomeTimeout
		} else {
			res.Outcome = OutcomeNetworkError
		}
		res.Error = sanitizeURLInError(err.Error(), source.Link)
		return nil, res, fmt.Errorf("upstream %q: %w", source.Name, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		res.Outcome = OutcomeNetworkError
		res.HTTPStatus = resp.StatusCode
		res.Error = err.Error()
		res.Duration = time.Since(start)
		return nil, res, fmt.Errorf("upstream %q: read body: %w", source.Name, err)
	}

	res.HTTPStatus = resp.StatusCode
	res.PayloadBytes = len(body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		res.Outcome = OutcomeHTTPError
		res.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		res.Duration = time.Since(start)
		return nil, res, fmt.Errorf("upstream %q: HTTP %d", source.Name, resp.StatusCode)
	}

	headers := UpstreamHeaders{}
	if v := resp.Header.Get("Subscription-Userinfo"); v != "" {
		ui, _, perr := ParseSubscriptionUserinfo(v)
		if perr != nil {
			f.Logger.Warn("upstream subscription-userinfo unparseable",
				"source", source.Name,
				"raw", v,
				"err", perr.Error(),
			)
		} else {
			headers.SubscriptionUserinfo = ui
		}
	}
	if v := resp.Header.Get("Profile-Update-Interval"); v != "" {
		if hours, ok := ParseProfileUpdateInterval(v); ok {
			h := hours
			headers.ProfileUpdateIntervalHours = &h
		}
	}

	// Parse the body eagerly. A syntax error MUST surface as parse_error
	// (not silently accepted), AND eager parse means concurrent Build()
	// callers never race on lazy initialization of parsedConfig.
	var parsed yaml.Node
	if err := yaml.Unmarshal(body, &parsed); err != nil {
		res.Outcome = OutcomeParseError
		res.Error = err.Error()
		res.Duration = time.Since(start)
		return nil, res, fmt.Errorf("upstream %q: yaml parse: %w", source.Name, err)
	}

	// Verify the parsed payload is a Clash-shaped config (root is a mapping
	// with at least one of proxies/proxy-groups/rules). Without this guard,
	// a V2Ray-style base64 URL list (which yaml.Unmarshal accepts as a
	// single scalar string) would silently cache "successfully" while
	// contributing zero entries to the merged output — the exact bug the
	// stake-holder caught during sanity check on 2026-04-30.
	if err := assertClashShape(&parsed); err != nil {
		res.Outcome = OutcomeParseError
		res.Error = err.Error()
		res.Duration = time.Since(start)
		return nil, res, fmt.Errorf("upstream %q: %w", source.Name, err)
	}

	payload := &UpstreamCachedPayload{
		SourceName:   source.Name,
		FetchedAt:    start,
		BodyYAML:     body,
		Headers:      headers,
		PayloadBytes: len(body),
	}
	payload.SetParsed(&parsed)

	res.Outcome = OutcomeSuccess
	res.Duration = time.Since(start)
	return payload, res, nil
}

// assertClashShape rejects upstream payloads that aren't a Clash YAML
// config. The root must be a mapping containing at least one of
// proxies / proxy-groups / rules. Returns nil on success.
func assertClashShape(parsed *yaml.Node) error {
	root := parsed
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("upstream payload is not a Clash YAML config (root is not a mapping — common cause: provider returned V2Ray-style base64 URL list because our User-Agent wasn't recognized as a Clash client; check UpstreamFetcher.UserAgent)")
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k := root.Content[i].Value
		if k == "proxies" || k == "proxy-groups" || k == "rules" {
			return nil
		}
	}
	return fmt.Errorf("upstream payload has no proxies / proxy-groups / rules at the top level — not a Clash subscription")
}

// sanitizeURLInError replaces an upstream link's userinfo / query string in
// an error message so credentials don't leak into logs.
func sanitizeURLInError(msg, link string) string {
	if link == "" {
		return msg
	}
	u, err := url.Parse(link)
	if err != nil {
		return msg
	}
	clean := u.Scheme + "://" + u.Host + u.Path
	// Replace any occurrence of the full link; drop the query/fragment.
	return replaceAll(msg, link, clean)
}

func replaceAll(s, old, new string) string {
	if old == "" {
		return s
	}
	out := ""
	for {
		i := indexOf(s, old)
		if i < 0 {
			out += s
			return out
		}
		out += s[:i] + new
		s = s[i+len(old):]
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
