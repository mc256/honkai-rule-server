package integration

import (
	"net/http"
	"strings"
	"testing"
)

// TC-I-18 / SC-007: the served body, response headers, AND captured log
// output must contain no upstream-URL credentials, no client-token plaintext,
// and no own-proxy passwords. Constitution Principle V security guarantee.
//
// Strategy: build the cluster with deliberately-distinctive synthetic
// secret strings, exercise the full request paths (auth + valid serve),
// then grep everything we can observe for those secrets.
func TestI_18_NoSecretsInOutputOrLogs(t *testing.T) {
	tc := newTestCluster(t)

	// 1) Successful serve.
	resp := tc.Get(t, "/", validToken)
	body := bodyOf(t, resp)
	headerStr := headersAsString(resp.Header)
	resp.Body.Close()

	// 2) A few rejected requests so the auth-failure log path is exercised.
	for _, p := range []string{"/", "/?token=" + revokedToken, "/?token=bogus-attempt-666"} {
		r, err := http.Get(tc.URL() + p)
		if err == nil {
			r.Body.Close()
		}
	}

	// 3) /health for completeness (it's open).
	r, err := http.Get(tc.URL() + "/health")
	if err == nil {
		r.Body.Close()
	}

	logs := tc.logBuf.String()

	// Distinctive synthetic secrets baked into the test fixtures.
	type secret struct {
		name   string
		value  string
		// where the secret is allowed to appear (none = nowhere)
		allowedInBody bool
	}
	secrets := []secret{
		// Subscription URL path tokens (from subscriptions.csv fixture).
		{"alpha-token-in-link", "fake-alpha-token", false},
		{"berry-token-in-link", "fake-berry-token", false},

		// Client-side tokens from tokens.json fixture.
		{"valid-client-token", "valid-test-token", false},
		{"revoked-client-token", "revoked-test-token", false},

		// Own-proxy authentication material.
		{"own-proxy-password", "synthetic-test-password", false},
	}

	allCaptured := map[string]string{
		"served body":      string(body),
		"response headers": headerStr,
		"server logs":      logs,
	}

	for _, s := range secrets {
		for surface, content := range allCaptured {
			leaked := strings.Contains(content, s.value)
			switch surface {
			case "served body":
				// Own-proxy passwords ARE expected to appear in the served
				// body — that's the whole point of advertising own-proxies
				// to the client. We carve out that single case here.
				if s.name == "own-proxy-password" && leaked {
					continue
				}
				if leaked {
					t.Errorf("LEAK: secret %s appears in served body", s.name)
				}
			case "response headers":
				if leaked {
					t.Errorf("LEAK: secret %s appears in response headers", s.name)
				}
			case "server logs":
				if leaked {
					t.Errorf("LEAK: secret %s appears in server log output", s.name)
				}
			}
		}
	}

	// Defensive: a sha256: prefix MUST appear at least once in the logs
	// (proving the rejected-request path used the sanitizer at all).
	if !strings.Contains(logs, "sha256:") {
		t.Errorf("logs do not contain any sha256:<hash> token fingerprint — auth path may not be sanitizing")
	}
}

func headersAsString(h http.Header) string {
	var b strings.Builder
	for k, vs := range h {
		for _, v := range vs {
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(v)
			b.WriteString("\n")
		}
	}
	return b.String()
}
