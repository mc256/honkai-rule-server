package integration

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TC-I-01 (010): GET /?token=<valid> returns the documented response
// headers — Content-Type, Cache-Control, Subscription-Userinfo (010 FR-001
// /FR-002 daily-spendable encoding), Profile-Update-Interval (001 FR-011a).
// alpha: 30d expiry, 150 GB remaining → 5 GB/day.
// beta: 5d expiry, 80 GB remaining → 16 GB/day.
// Daily allowance = 21 GB = 22548578304 bytes. 011 FR-002: Expire = next
// 00:00 America/Toronto (test cluster's BudgetLocation) — at 2026-04-30
// 00:00 UTC = 2026-04-29 20:00 EDT, the next Toronto midnight is
// 2026-04-30 00:00 EDT = 2026-04-30 04:00 UTC.
func TestI_HeadersOnSuccessfulServe(t *testing.T) {
	now := time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)
	opts := defaultOpts()
	opts.clockNow = now
	opts.perSourceUserinfo = map[string]string{
		"alpha":     fmt.Sprintf("upload=10737418240; download=42949672960; total=214748364800; expire=%d", now.Add(30*24*time.Hour).Unix()),
		"beta": fmt.Sprintf("upload=5368709120; download=16106127360; total=107374182400; expire=%d", now.Add(5*24*time.Hour).Unix()),
	}
	tc := newTestClusterWithOpts(t, opts)

	resp := tc.Get(t, "/", validToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	if ct := resp.Header.Get("Content-Type"); ct != "application/yaml; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store, no-cache, must-revalidate" {
		t.Errorf("Cache-Control = %q", cc)
	}

	const wantAllowance = int64(21) * (1 << 30) // 21 GiB = 22548578304
	tzToronto, _ := time.LoadLocation("America/Toronto")
	wantExpire := time.Date(2026, 4, 30, 0, 0, 0, 0, tzToronto).Unix()
	// 011: snapshotter is configured but no spend has happened yet — used
	// = 0, total = allowance + 0. Encoding remains "upload=0; download=0"
	// at zero spend (SplitUsedToday(0, ...) = (0, 0)).
	wantUI := fmt.Sprintf("upload=0; download=0; total=%d; expire=%d", wantAllowance, wantExpire)
	if got := resp.Header.Get("Subscription-Userinfo"); got != wantUI {
		t.Errorf("Subscription-Userinfo:\n  got:  %q\n  want: %q", got, wantUI)
	}

	// Both stubs return Profile-Update-Interval=12 (the cluster default).
	if got := resp.Header.Get("Profile-Update-Interval"); got != "12" {
		t.Errorf("Profile-Update-Interval = %q, want 12", got)
	}
}

// TC-I-08 (extended for US4 / FR-019b): a 401 response carries no
// Subscription-Userinfo header, so an unauthenticated requester cannot
// harvest the operator's quota numbers from a guess-the-URL probe.
func TestI_UnauthorizedHasNoQuotaHeaders(t *testing.T) {
	tc := newTestCluster(t)

	cases := []string{
		"/",                     // no token at all
		"/?token=bogus-12345",   // unknown token
		"/?token=" + revokedToken,
	}
	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			resp, err := http.Get(tc.URL() + path)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", resp.StatusCode)
			}
			if got := resp.Header.Get("Subscription-Userinfo"); got != "" {
				t.Errorf("401 response leaked Subscription-Userinfo: %q", got)
			}
			if got := resp.Header.Get("Profile-Update-Interval"); got != "" {
				t.Errorf("401 response leaked Profile-Update-Interval: %q", got)
			}
			body := bodyOf(t, resp)
			if len(body) != 0 {
				t.Errorf("401 body = %q, want empty", string(body))
			}
		})
	}
}

// FR-005c sanity: server-side Clash globals (mixed-port, mode, dns) come
// from the template; upstream globals are NOT propagated even if the
// upstream YAML carries them.
func TestI_ServedGlobalsComeFromTemplate(t *testing.T) {
	tc := newTestCluster(t)
	resp := tc.Get(t, "/", validToken)
	defer resp.Body.Close()
	body := bodyOf(t, resp)

	// The template sets mixed-port: 7890 (and definitely doesn't set the
	// `port: 7890` / `redir-port: 7892` / `socks-port: 7891` block that
	// alpha.yaml uses). The merged body should reflect the template's
	// globals, not alpha's.
	if !strings.Contains(string(body), "mixed-port:") {
		t.Errorf("body missing mixed-port (from template):\n%.300s", string(body))
	}
	// alpha.yaml has socks-port:7891; it must NOT appear in the merged body.
	if strings.Contains(string(body), "socks-port:") {
		t.Errorf("body contains upstream-only global socks-port (FR-005c violation)")
	}
}
