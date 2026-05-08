package fetcher

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mc256/honkai-rule-server/internal/config"
)

const minimalUpstreamYAML = `port: 7890
proxies:
  - {name: test-proxy-1, type: trojan, server: a.example.test, port: 443, password: pw}
proxy-groups:
  - {name: Auto, type: select, proxies: [test-proxy-1]}
rules:
  - DOMAIN,example.test,Auto
  - MATCH,DIRECT
`

func newFetcher() *UpstreamFetcher {
	return NewUpstreamFetcher(2*time.Second, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
}

func TestUpstreamFetcher_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Subscription-Userinfo", "upload=10; download=20; total=100; expire=1804180937")
		w.Header().Set("Profile-Update-Interval", "12")
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte(minimalUpstreamYAML))
	}))
	defer srv.Close()

	row := config.SubscriptionRow{Name: "test", Link: srv.URL, Priority: 1, Enable: true}
	payload, res, err := newFetcher().Fetch(context.Background(), row)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Outcome != OutcomeSuccess {
		t.Errorf("Outcome = %s, want success", res.Outcome)
	}
	if res.HTTPStatus != 200 {
		t.Errorf("HTTPStatus = %d", res.HTTPStatus)
	}
	if payload.PayloadBytes != len(minimalUpstreamYAML) {
		t.Errorf("PayloadBytes = %d, want %d", payload.PayloadBytes, len(minimalUpstreamYAML))
	}
	if payload.Headers.SubscriptionUserinfo == nil || payload.Headers.SubscriptionUserinfo.Total != 100 {
		t.Errorf("SubscriptionUserinfo = %+v, want non-nil with Total=100", payload.Headers.SubscriptionUserinfo)
	}
	if payload.Headers.ProfileUpdateIntervalHours == nil || *payload.Headers.ProfileUpdateIntervalHours != 12 {
		t.Errorf("ProfileUpdateIntervalHours = %v, want 12", payload.Headers.ProfileUpdateIntervalHours)
	}

	// Body parses as YAML
	if _, err := payload.Parse(); err != nil {
		t.Errorf("Parse: %v", err)
	}
}

func TestUpstreamFetcher_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		_, _ = w.Write([]byte("upstream down"))
	}))
	defer srv.Close()

	row := config.SubscriptionRow{Name: "test", Link: srv.URL, Priority: 1, Enable: true}
	payload, res, err := newFetcher().Fetch(context.Background(), row)
	if err == nil {
		t.Fatal("expected error on 503")
	}
	if payload != nil {
		t.Errorf("payload should be nil on error")
	}
	if res.Outcome != OutcomeHTTPError || res.HTTPStatus != 503 {
		t.Errorf("Outcome=%s status=%d, want http_error/503", res.Outcome, res.HTTPStatus)
	}
}

func TestUpstreamFetcher_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		_, _ = w.Write([]byte(minimalUpstreamYAML))
	}))
	defer srv.Close()

	f := NewUpstreamFetcher(50*time.Millisecond, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	row := config.SubscriptionRow{Name: "test", Link: srv.URL, Priority: 1, Enable: true}
	_, res, err := f.Fetch(context.Background(), row)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if res.Outcome != OutcomeTimeout {
		t.Errorf("Outcome = %s, want timeout", res.Outcome)
	}
}

func TestUpstreamFetcher_ParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Not valid YAML — a tab where YAML disallows it inside a flow mapping.
		_, _ = w.Write([]byte("proxies: [\n\t- broken: {{}}\n"))
	}))
	defer srv.Close()

	row := config.SubscriptionRow{Name: "test", Link: srv.URL, Priority: 1, Enable: true}
	payload, res, err := newFetcher().Fetch(context.Background(), row)
	if err == nil {
		t.Fatalf("expected YAML parse error; got payload %+v res %+v", payload, res)
	}
	if res.Outcome != OutcomeParseError {
		t.Errorf("Outcome = %s, want parse_error", res.Outcome)
	}
}

func TestUpstreamFetcher_SanitizesURLInErrors(t *testing.T) {
	const secretToken = "supersecrettokenstring1234567890"
	link := "http://nonexistent.invalid:1/sub?token=" + secretToken

	row := config.SubscriptionRow{Name: "test", Link: link, Priority: 1, Enable: true}
	_, res, err := newFetcher().Fetch(context.Background(), row)
	if err == nil {
		t.Fatal("expected network error fetching invalid host")
	}
	if strings.Contains(res.Error, secretToken) {
		t.Errorf("FetchResult.Error contains token plaintext: %q", res.Error)
	}
}

// Regression test for the bug found during sanity-check on 2026-04-30:
// an upstream that returns a V2Ray-style base64 URL list (because our UA
// wasn't recognized as a Clash client) was silently accepted as "success"
// because base64 parses as a valid YAML scalar string. The merge layer
// then found no proxies/proxy-groups/rules in the cached payload and
// contributed zero from that source.
func TestUpstreamFetcher_RejectsNonClashPayload(t *testing.T) {
	const v2rayBase64 = "dHJvamFuOi8vMmZkY2FmY2ItNGJkNC00ZDFkQGV4YW1wbGUudGVzdDoyMDk2DQp2bGVzczovLzJmZGNhZmNiLTRiZDQtNGQxZEBleGFtcGxlLnRlc3Q6MTcwMDENCg=="
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(v2rayBase64))
	}))
	defer srv.Close()

	row := config.SubscriptionRow{Name: "test", Link: srv.URL, Priority: 1, Enable: true}
	payload, res, err := newFetcher().Fetch(context.Background(), row)
	if err == nil {
		t.Fatalf("expected parse error on non-Clash payload; got payload %+v", payload)
	}
	if res.Outcome != OutcomeParseError {
		t.Errorf("Outcome = %s, want parse_error", res.Outcome)
	}
	if !strings.Contains(strings.ToLower(res.Error), "clash") {
		t.Errorf("error %q should mention Clash to help operator diagnose UA issues", res.Error)
	}
}

// A YAML mapping that doesn't have any of proxies / proxy-groups / rules
// is also not a Clash subscription — also fail loudly.
func TestUpstreamFetcher_RejectsMappingWithoutClashKeys(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("foo: bar\nbaz: 123\n"))
	}))
	defer srv.Close()

	row := config.SubscriptionRow{Name: "test", Link: srv.URL, Priority: 1, Enable: true}
	_, res, err := newFetcher().Fetch(context.Background(), row)
	if err == nil {
		t.Fatal("expected parse error on YAML without proxies/proxy-groups/rules")
	}
	if res.Outcome != OutcomeParseError {
		t.Errorf("Outcome = %s, want parse_error", res.Outcome)
	}
}

// The fetcher MUST send a Clash-flavored User-Agent by default (see
// DefaultUserAgent comment) because most providers content-negotiate by UA.
func TestUpstreamFetcher_SendsClashUserAgent(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(minimalUpstreamYAML))
	}))
	defer srv.Close()

	row := config.SubscriptionRow{Name: "test", Link: srv.URL, Priority: 1, Enable: true}
	if _, _, err := newFetcher().Fetch(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "mihomo") && !strings.HasPrefix(got, "clash") && !strings.HasPrefix(got, "Clash") {
		t.Errorf("User-Agent = %q, want mihomo/clash/Clash prefix (Clash-compatible identifier)", got)
	}
}

// Operators can override the User-Agent for providers that need a specific identifier.
func TestUpstreamFetcher_UserAgentOverride(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(minimalUpstreamYAML))
	}))
	defer srv.Close()

	f := newFetcher()
	f.UserAgent = "ClashforWindows/0.20.39"
	row := config.SubscriptionRow{Name: "test", Link: srv.URL, Priority: 1, Enable: true}
	if _, _, err := f.Fetch(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	if got != "ClashforWindows/0.20.39" {
		t.Errorf("User-Agent = %q, want ClashforWindows/0.20.39", got)
	}
}

func TestUpstreamFetcher_MissingHeadersDoesNotFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(minimalUpstreamYAML))
	}))
	defer srv.Close()

	row := config.SubscriptionRow{Name: "test", Link: srv.URL, Priority: 1, Enable: true}
	payload, res, err := newFetcher().Fetch(context.Background(), row)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Outcome != OutcomeSuccess {
		t.Errorf("Outcome = %s, want success", res.Outcome)
	}
	if payload.Headers.SubscriptionUserinfo != nil {
		t.Errorf("SubscriptionUserinfo should be nil when header absent")
	}
	if payload.Headers.ProfileUpdateIntervalHours != nil {
		t.Errorf("ProfileUpdateIntervalHours should be nil when header absent")
	}
}
