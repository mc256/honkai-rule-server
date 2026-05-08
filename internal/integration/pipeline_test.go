package integration

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mc256/honkai-rule-server/internal/config"
	"github.com/mc256/honkai-rule-server/internal/clock"
	"github.com/mc256/honkai-rule-server/internal/customrules"
	"github.com/mc256/honkai-rule-server/internal/fetcher"
	"github.com/mc256/honkai-rule-server/internal/merge"
	"github.com/mc256/honkai-rule-server/internal/output"
	"gopkg.in/yaml.v3"
)

// stubMergeCache satisfies merge.CacheReader from static YAML payloads.
type stubMergeCache struct {
	payloads map[string]*fetcher.UpstreamCachedPayload
}

func (s *stubMergeCache) Get(name string) (*fetcher.UpstreamCachedPayload, bool) {
	p, ok := s.payloads[name]
	return p, ok
}

const (
	validToken   = "valid-test-token"
	revokedToken = "revoked-test-token"
)

// TC-I-01: happy path — both upstreams reachable; 200 with merged body whose
// proxies/groups/rules counts make sense and rule order respects priority.
func TestI_01_HappyPath(t *testing.T) {
	tc := newTestCluster(t)

	resp := tc.Get(t, "/", validToken)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/yaml; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}

	body := bodyOf(t, resp)
	var doc map[string]any
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("body does not parse as YAML: %v", err)
	}

	proxies, _ := doc["proxies"].([]any)
	if len(proxies) < 100 {
		t.Errorf("merged proxies count = %d, want >= 100", len(proxies))
	}
	groups, _ := doc["proxy-groups"].([]any)
	if len(groups) < 5 {
		t.Errorf("merged proxy-groups count = %d, want >= 5", len(groups))
	}
	rules, _ := doc["rules"].([]any)
	if len(rules) < 50 {
		t.Errorf("merged rules count = %d, want >= 50", len(rules))
	}

	// Priority order under ascending sort (feature 007): alpha priority 1000
	// emits before beta priority 2000. Search the first ~30 rules for
	// alpha's group name (🔰国外流量) — under ascending, alpha's rules lead.
	firstChunk := ""
	for i := 0; i < 30 && i < len(rules); i++ {
		firstChunk += rules[i].(string) + "\n"
	}
	if !strings.Contains(firstChunk, "🔰国外流量") {
		t.Errorf("alpha rules not in the first 30 under ascending sort:\n%s", firstChunk)
	}
}

// TC-I-04: cold start — both upstreams return 503 from the first attempt;
// bootstrap fails; the served endpoint returns 503 with `bootstrap_failed`.
func TestI_04_ColdStartFailClosed(t *testing.T) {
	opts := defaultOpts()
	// Stubs return 503 from the very first request; bootstrap retries and
	// then transitions to BootstrapFailed.
	opts.failingUpstreams = map[string]int{"alpha": 503, "beta": 503}
	tc := newTestClusterWithOpts(t, opts)

	// Wait for the served endpoint to settle into bootstrap_failed.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp := tc.Get(t, "/", validToken)
		if resp.StatusCode == http.StatusServiceUnavailable {
			body := bodyOf(t, resp)
			var doc map[string]any
			_ = json.Unmarshal(body, &doc)
			if doc["error"] == "bootstrap_failed" {
				resp.Body.Close()
				return
			}
		}
		resp.Body.Close()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("never observed 503 with bootstrap_failed")
}

// TC-I-06: synthetic same-named proxy across upstreams → both appear with
// provider prefix (FR-004). Cross-source collision is now structurally impossible.
func TestI_06_ProxyNameCollision(t *testing.T) {
	const collidingA = "proxies:\n  - {name: shared, type: trojan, server: a.test, port: 443, password: pw}\nproxy-groups:\n  - {name: G, type: select, proxies: [shared]}\nrules:\n  - MATCH,DIRECT\n"
	const collidingB = "proxies:\n  - {name: shared, type: trojan, server: b.test, port: 443, password: pw}\nproxy-groups:\n  - {name: G, type: select, proxies: [shared]}\nrules:\n  - MATCH,DIRECT\n"

	opts := defaultOpts()
	opts.customPayloads = map[string]string{
		"alpha":    collidingA,
		"beta": collidingB,
	}
	tc := newTestClusterWithOpts(t, opts)

	resp := tc.Get(t, "/", validToken)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, body=%s", resp.StatusCode, string(bodyOf(t, resp)))
	}
	body := bodyOf(t, resp)

	// After 002 FR-004, both proxies appear with provider prefix.
	// No @source suffix needed — cross-source collision is impossible.
	type proxyName struct {
		Name string `yaml:"name"`
	}
	type doc struct {
		Proxies []proxyName `yaml:"proxies"`
	}
	var d doc
	if err := yaml.Unmarshal(body, &d); err != nil {
		t.Fatal(err)
	}
	hasBetaShared, hasAlphaShared := false, false
	for _, p := range d.Proxies {
		if p.Name == "beta_shared" {
			hasBetaShared = true
		}
		if p.Name == "alpha_shared" {
			hasAlphaShared = true
		}
	}
	if !hasBetaShared || !hasAlphaShared {
		t.Errorf("got proxy names %v, want both 'beta_shared' and 'alpha_shared'", d.Proxies)
	}
}

// TC-I-07: synthetic same-named group across upstreams → both appear with
// provider prefix (FR-005). After 002, same-name union is dead for cross-source
// groups because each group is prefixed with its source name.
func TestI_07_ProxyGroupSameNameUnion(t *testing.T) {
	const a = "proxies:\n  - {name: A1, type: trojan, server: a.test, port: 443, password: pw}\nproxy-groups:\n  - {name: Auto, type: select, proxies: [A1]}\nrules:\n  - MATCH,DIRECT\n"
	const b = "proxies:\n  - {name: B1, type: trojan, server: b.test, port: 443, password: pw}\nproxy-groups:\n  - {name: Auto, type: select, proxies: [B1]}\nrules:\n  - MATCH,DIRECT\n"

	opts := defaultOpts()
	opts.customPayloads = map[string]string{"alpha": a, "beta": b}
	tc := newTestClusterWithOpts(t, opts)

	resp := tc.Get(t, "/", validToken)
	defer resp.Body.Close()
	body := bodyOf(t, resp)

	type group struct {
		Name    string   `yaml:"name"`
		Type    string   `yaml:"type"`
		Proxies []string `yaml:"proxies"`
	}
	type doc struct {
		ProxyGroups []group `yaml:"proxy-groups"`
	}
	var d doc
	if err := yaml.Unmarshal(body, &d); err != nil {
		t.Fatal(err)
	}
	// After 002 FR-005, each source's group is prefixed: alpha_Auto, beta_Auto.
	// They are no longer unioned because the names differ.
	autoCount := 0
	var foundAlphaAuto, foundBetaAuto bool
	for _, g := range d.ProxyGroups {
		if g.Name == "alpha_Auto" {
			autoCount++
			foundAlphaAuto = true
		}
		if g.Name == "beta_Auto" {
			autoCount++
			foundBetaAuto = true
		}
	}
	if !foundAlphaAuto || !foundBetaAuto {
		t.Errorf("got groups %v, want alpha_Auto and beta_Auto", func() []string {
			out := []string{}
			for _, g := range d.ProxyGroups {
				out = append(out, g.Name)
			}
			return out
		}())
	}
}

// TC-I-08: token authentication — 4 sub-cases.
func TestI_08_TokenAuthentication(t *testing.T) {
	tc := newTestCluster(t)

	cases := []struct {
		name         string
		url          string
		wantStatus   int
		wantBodyPart string
	}{
		{"no token", "/", http.StatusUnauthorized, ""},
		{"unknown token", "/?token=totally-bogus-token", http.StatusUnauthorized, ""},
		{"revoked token", "/?token=" + revokedToken, http.StatusUnauthorized, ""},
		{"valid token", "/?token=" + validToken, http.StatusOK, "proxies"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, err := http.Get(tc.URL() + c.url)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != c.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, c.wantStatus)
			}
			body := bodyOf(t, resp)
			if c.wantStatus == http.StatusUnauthorized && len(body) != 0 {
				t.Errorf("401 body = %q, want empty", string(body))
			}
			if c.wantBodyPart != "" && !strings.Contains(string(body), c.wantBodyPart) {
				t.Errorf("body missing %q", c.wantBodyPart)
			}
		})
	}

	// Verify hashed token in logs, no plaintext.
	if tc.LogContains("totally-bogus-token") {
		t.Errorf("log leaked unknown token plaintext")
	}
	if !tc.LogContains("sha256:") {
		t.Errorf("log missing token_hash field for rejected request")
	}
}

// TC-I-09: 100 sequential requests → byte-identical response bodies (sha256).
func TestI_09_Determinism(t *testing.T) {
	tc := newTestCluster(t)

	const N = 100
	var firstHash [32]byte
	for i := 0; i < N; i++ {
		resp := tc.Get(t, "/", validToken)
		body := bodyOf(t, resp)
		hash := sha256.Sum256(body)
		if i == 0 {
			firstHash = hash
			continue
		}
		if hash != firstHash {
			t.Fatalf("request #%d body hash differs from #0 (deterministic-output regression)", i)
		}
	}
}

// TC-I-10: 100 concurrent client requests → 0 additional upstream fetches
// (per FR-003a — the cache absorbs all client load).
func TestI_10_CacheAbsorbsTraffic(t *testing.T) {
	tc := newTestCluster(t)

	// Reset hit counters AFTER bootstrap so we're measuring only the
	// request-path fetches (which should be zero).
	for _, s := range tc.upstreams {
		s.ResetHits()
	}

	const N = 100
	var wg sync.WaitGroup
	var failures atomic.Int32
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			resp, err := http.Get(tc.URL() + "/?token=" + validToken)
			if err != nil {
				failures.Add(1)
				return
			}
			resp.Body.Close()
			if resp.StatusCode != 200 {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()

	if failures.Load() != 0 {
		t.Errorf("%d requests failed", failures.Load())
	}
	for name, s := range tc.upstreams {
		if hits := s.Hits(); hits != 0 {
			t.Errorf("upstream %s saw %d fetches during %d concurrent client requests; want 0",
				name, hits, N)
		}
	}
}

// TC-I-17 (adapted): tokens-file reload error keeps previous tokens working.
// The spec's TC-I-17 referred to CSV reload; for v1 only tokens are
// hot-reloadable, so this is the same correctness property tested on the
// surface we have.
func TestI_17_TokenReloadErrorPreservesPrevious(t *testing.T) {
	t.Skip("TODO: tokens hot-reload is wired in main.go but not in the test cluster harness; cover in a future iteration once we extract a shared App helper")
}

// TC-I-002-05: region groups — _region_CN and _region_HK exist; members are
// upstream-prefixed only; every region group name appears in Proxies group.
func TestI_002_05_RegionGroups(t *testing.T) {
	tc := newTestCluster(t)

	resp := tc.Get(t, "/", validToken)
	defer resp.Body.Close()
	body := bodyOf(t, resp)

	type group struct {
		Name    string   `yaml:"name"`
		Type    string   `yaml:"type"`
		Proxies []string `yaml:"proxies"`
	}
	type doc struct {
		ProxyGroups []group `yaml:"proxy-groups"`
	}
	var d doc
	if err := yaml.Unmarshal(body, &d); err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Find all _region_* groups and validate membership.
	regionGroups := make(map[string]*group)
	var proxiesGroup *group
	for i, g := range d.ProxyGroups {
		if len(g.Name) > 8 && g.Name[:8] == "_region_" {
			regionGroups[g.Name] = &d.ProxyGroups[i]
		}
		if g.Name == "Proxies" {
			proxiesGroup = &d.ProxyGroups[i]
		}
	}

	if len(regionGroups) < 5 {
		t.Errorf("expected at least 5 region groups, got %d", len(regionGroups))
	}

	// _region_HK and _region_US must exist (providers contribute these).
	for _, want := range []string{"_region_HK", "_region_US"} {
		if _, ok := regionGroups[want]; !ok {
			t.Errorf("missing %s group (got regions: %v)", want, func() []string {
				out := []string{}
				for k := range regionGroups {
					out = append(out, k)
				}
				return out
			}())
		}
	}

	// Every member of every region group must start with an upstream prefix
	// (alpha_ or beta_), never with "_" indicating an own-proxy.
	for rName, rg := range regionGroups {
		// 012 FR-001: region groups are url-test (was select pre-012).
		if rg.Type != "url-test" {
			t.Errorf("%s type = %q, want url-test", rName, rg.Type)
		}
		for _, m := range rg.Proxies {
			if strings.HasPrefix(m, "_") {
				t.Errorf("%s member %q starts with '_' — own-proxy leak into region group", rName, m)
			}
			if !strings.HasPrefix(m, "alpha_") && !strings.HasPrefix(m, "beta_") {
				t.Errorf("%s member %q doesn't start with upstream prefix", rName, m)
			}
		}
		if len(rg.Proxies) == 0 {
			t.Errorf("%s has empty membership", rName)
		}
	}

	// Every region group name must appear in the Proxies group.
	if proxiesGroup == nil {
		t.Fatal("Proxies group not found")
	}
	proxiesMembers := make(map[string]bool)
	for _, m := range proxiesGroup.Proxies {
		proxiesMembers[m] = true
	}
	for rName := range regionGroups {
		if !proxiesMembers[rName] {
			t.Errorf("Proxies group missing region group member %q", rName)
		}
	}
}

// TC-I-002-07: determinism — sha256 of merged body is stable across 100 runs.
func TestI_002_07_MergeDeterminism(t *testing.T) {
	tc := newTestCluster(t)

	resp := tc.Get(t, "/", validToken)
	defer resp.Body.Close()
	body := bodyOf(t, resp)
	reference := sha256.Sum256(body)

	for i := 0; i < 99; i++ {
		resp2 := tc.Get(t, "/", validToken)
		body2 := bodyOf(t, resp2)
		resp2.Body.Close()
		h := sha256.Sum256(body2)
		if h != reference {
			t.Errorf("run %d: hash mismatch", i+2)
		}
	}
}

// TC-I-002-10: own-proxy with country indicator is NOT classified into a
// region group. Tests at the pipeline level to avoid HTTP handler deps issues.
func TestI_002_10_OwnProxyExcludedFromRegion(t *testing.T) {
	upstreamA := `port: 7890
proxies:
  - {name: 🇨🇦 Toronto 01, type: trojan, server: ca.test, port: 443, password: pw}
  - {name: 🇨🇦 Montreal 02, type: trojan, server: ca.test, port: 443, password: pw}
proxy-groups:
  - {name: Auto, type: select, proxies: [🇨🇦 Toronto 01, 🇨🇦 Montreal 02]}
rules:
  - DOMAIN,ca.test,Auto
  - MATCH,DIRECT
`

	ownProxyYAML := `proxies:
  - name: 🇨🇦 my-canada-1
    type: trojan
    server: home.test
    port: 443
    password: pw
proxy-groups: []
`
	ownProxiesPath := filepath.Join(t.TempDir(), "own-proxies.yaml")
	if err := os.WriteFile(ownProxiesPath, []byte(ownProxyYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	own, err := config.LoadOwnProxies(ownProxiesPath)
	if err != nil {
		t.Fatal(err)
	}

	// Build pipeline directly with custom upstream + own-proxies.
	cache := &stubMergeCache{
		payloads: map[string]*fetcher.UpstreamCachedPayload{
			"alpha": {
				SourceName:   "alpha",
				BodyYAML:     []byte(upstreamA),
				PayloadBytes: len(upstreamA),
			},
		},
	}

	rows := []config.SubscriptionRow{
		{Name: "alpha", Link: "http://a.test", Priority: 1000, Enable: true},
	}
	p := merge.NewPipeline(cache, rows, own.Proxies, own.ProxyGroups, clock.RealClock{}, 12)
	mc, err := p.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Own-proxy should appear with underscore prefix.
	foundOwnProxy := false
	for _, n := range mc.Proxies {
		name := ""
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == "name" {
				name = n.Content[i+1].Value
				break
			}
		}
		if name == "_🇨🇦 my-canada-1" {
			foundOwnProxy = true
		}
	}
	if !foundOwnProxy {
		t.Errorf("own-proxy _🇨🇦 my-canada-1 not found in merged proxies")
	}

	// _region_CA should exist (upstream contributes CA-classified proxies).
	var caGroup *yaml.Node
	for _, g := range mc.ProxyGroups {
		name := ""
		for i := 0; i+1 < len(g.Content); i += 2 {
			if g.Content[i].Value == "name" {
				name = g.Content[i+1].Value
				break
			}
		}
		if name == "_region_CA" {
			caGroup = g
			break
		}
	}
	if caGroup == nil {
		t.Fatalf("_region_CA group not found")
	}

	// _region_CA must NOT contain the own-proxy.
	var members []string
	for i := 0; i+1 < len(caGroup.Content); i += 2 {
		if caGroup.Content[i].Value == "proxies" {
			seq := caGroup.Content[i+1]
			for _, m := range seq.Content {
				members = append(members, m.Value)
			}
			break
		}
	}
	for _, m := range members {
		if m == "_🇨🇦 my-canada-1" {
			t.Errorf("_region_CA contains own-proxy _🇨🇦 my-canada-1 — must be excluded")
		}
		if strings.HasPrefix(m, "_") {
			t.Errorf("_region_CA contains underscore-prefixed member %q", m)
		}
	}
}

// minimalIntegrationTemplate is a small valid Clash-config template used by
// the feature-005 integration tests that need to render the served YAML and
// inspect comment text. It mirrors the placeholders the real template uses.
const minimalIntegrationTemplate = `mixed-port: 7890
mode: rule

proxies: __MERGED_PROXIES__

proxy-groups: __MERGED_PROXY_GROUPS__

rules: __MERGED_RULES__
`

// renderViaAdapter builds a Pipeline + SubscriptionMode adapter and returns
// the served body. Used by TC-I-005-02 / TC-I-005-03 which must inspect the
// rendered YAML (not just MergedConfig.Rules) for comment-text assertions
// and byte-identical-determinism.
func renderViaAdapter(t *testing.T, p *merge.Pipeline) []byte {
	t.Helper()
	mc, err := p.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	adapter, err := output.NewSubscriptionModeFromBytes([]byte(minimalIntegrationTemplate))
	if err != nil {
		t.Fatalf("NewSubscriptionModeFromBytes: %v", err)
	}
	rendered, err := adapter.Render(mc)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return rendered.Body
}

// TC-I-005-02: served YAML carries one priority-bucket header comment per
// priority level naming the contributors at that level; legacy
// "# --- upstream ---" string is absent.
func TestI_005_02_PriorityBucketHeaderComments(t *testing.T) {
	upstreamYAML := `port: 7890
proxies:
  - {name: 🇺🇸 NY 01, type: trojan, server: us.test, port: 443, password: pw}
proxy-groups:
  - {name: Auto, type: select, proxies: [🇺🇸 NY 01]}
rules:
  - DOMAIN,upstream-rule.test,Auto
  - MATCH,DIRECT
`

	rulesDir := t.TempDir()
	customYAML := `name: corporate
priority: 1000
rules:
  - DOMAIN,custom-rule.test,REJECT
`
	if err := os.WriteFile(filepath.Join(rulesDir, "corporate.yaml"), []byte(customYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := customrules.Load(rulesDir)
	if err != nil {
		t.Fatalf("customrules.Load: %v", err)
	}

	cache := &stubMergeCache{
		payloads: map[string]*fetcher.UpstreamCachedPayload{
			"beta": {SourceName: "beta", BodyYAML: []byte(upstreamYAML), PayloadBytes: len(upstreamYAML)},
		},
	}
	rows := []config.SubscriptionRow{
		{Name: "beta", Link: "http://b.test", Priority: 2000, Enable: true},
	}
	p := merge.NewPipeline(cache, rows, nil, nil, clock.RealClock{}, 12).
		WithFallbackRuleTarget("auto").
		WithCustomRules(loaded)

	body := string(renderViaAdapter(t, p))

	if strings.Contains(body, "# --- upstream ---") {
		t.Errorf("served YAML must not contain legacy \"# --- upstream ---\" comment; full body:\n%s", body)
	}
	idx2000 := strings.Index(body, "# --- priority 2000 (beta) ---")
	idx1000 := strings.Index(body, "# --- priority 1000 (corporate) ---")
	if idx2000 < 0 {
		t.Errorf("expected \"# --- priority 2000 (beta) ---\" in served YAML; full body:\n%s", body)
	}
	if idx1000 < 0 {
		t.Errorf("expected \"# --- priority 1000 (corporate) ---\" in served YAML; full body:\n%s", body)
	}
	// Ascending sort: priority 1000 header must appear before priority 2000 header.
	if idx2000 >= 0 && idx1000 >= 0 && idx1000 >= idx2000 {
		t.Errorf("ascending sort violated: priority 1000 header at byte %d should appear before priority 2000 header at byte %d",
			idx1000, idx2000)
	}
	// MATCH,auto must not be preceded by a "# --- priority 0" header.
	if strings.Contains(body, "# --- priority 0") {
		t.Errorf("MATCH fallback (priority 0) must not get a header comment; full body:\n%s", body)
	}
}

// TC-I-005-03: 100 sequential renders of the same input produce byte-identical
// served bodies. Validates SC-004 of the spec under the new unified-merge
// function with both upstream and custom contributors at multiple priorities.
func TestI_005_03_UnifiedPriorityDeterminism(t *testing.T) {
	upstreamA := `port: 7890
proxies:
  - {name: 🇺🇸 NY 01, type: trojan, server: us.test, port: 443, password: pw}
proxy-groups:
  - {name: Auto, type: select, proxies: [🇺🇸 NY 01]}
rules:
  - DOMAIN,a-up.test,Auto
  - MATCH,DIRECT
`
	upstreamB := `port: 7890
proxies:
  - {name: 🇩🇪 Berlin 01, type: trojan, server: de.test, port: 443, password: pw}
proxy-groups:
  - {name: Auto, type: select, proxies: [🇩🇪 Berlin 01]}
rules:
  - DOMAIN,b-up.test,Auto
  - MATCH,DIRECT
`
	rulesDir := t.TempDir()
	for name, body := range map[string]string{
		"high.yaml": "name: high\npriority: 1500\nrules:\n  - DOMAIN,h.test,REJECT\n",
		"low.yaml":  "name: low\npriority: 300\nrules:\n  - DOMAIN,l.test,DIRECT\n",
	} {
		if err := os.WriteFile(filepath.Join(rulesDir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := customrules.Load(rulesDir)
	if err != nil {
		t.Fatalf("customrules.Load: %v", err)
	}

	cache := &stubMergeCache{
		payloads: map[string]*fetcher.UpstreamCachedPayload{
			"src-a": {SourceName: "src-a", BodyYAML: []byte(upstreamA), PayloadBytes: len(upstreamA)},
			"src-b": {SourceName: "src-b", BodyYAML: []byte(upstreamB), PayloadBytes: len(upstreamB)},
		},
	}
	rows := []config.SubscriptionRow{
		{Name: "src-a", Link: "http://a.test", Priority: 1000, Enable: true},
		{Name: "src-b", Link: "http://b.test", Priority: 2000, Enable: true},
	}
	mkPipeline := func() *merge.Pipeline {
		return merge.NewPipeline(cache, rows, nil, nil, clock.RealClock{}, 12).
			WithFallbackRuleTarget("auto").
			WithCustomRules(loaded)
	}

	reference := sha256.Sum256(renderViaAdapter(t, mkPipeline()))
	for i := 1; i < 100; i++ {
		got := sha256.Sum256(renderViaAdapter(t, mkPipeline()))
		if got != reference {
			t.Errorf("render %d: hash mismatch (non-deterministic served output)", i)
		}
	}
}

// TC-I-005-01: upstream and custom rule sets interleave by priority (descending),
// with alphabetical tie-break, single number-line. Guards against regression of
// the unified-priority-merge behavior introduced in feature 005.
func TestI_005_01_UnifiedPriorityOrder(t *testing.T) {
	upstreamLow := `port: 7890
proxies:
  - {name: 🇺🇸 NY 01, type: trojan, server: us.test, port: 443, password: pw}
proxy-groups:
  - {name: Auto, type: select, proxies: [🇺🇸 NY 01]}
rules:
  - DOMAIN,low-upstream.test,Auto
  - MATCH,DIRECT
`
	upstreamHigh := `port: 7890
proxies:
  - {name: 🇩🇪 Berlin 01, type: trojan, server: de.test, port: 443, password: pw}
proxy-groups:
  - {name: Auto, type: select, proxies: [🇩🇪 Berlin 01]}
rules:
  - DOMAIN,high-upstream.test,Auto
  - MATCH,DIRECT
`

	rulesDir := t.TempDir()
	customLow := `name: custom-low
priority: 300
rules:
  - DOMAIN,low-custom.test,REJECT
`
	customHigh := `name: custom-high
priority: 1500
rules:
  - DOMAIN,high-custom.test,REJECT
`
	if err := os.WriteFile(filepath.Join(rulesDir, "low.yaml"), []byte(customLow), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "high.yaml"), []byte(customHigh), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := customrules.Load(rulesDir)
	if err != nil {
		t.Fatalf("customrules.Load: %v", err)
	}

	cache := &stubMergeCache{
		payloads: map[string]*fetcher.UpstreamCachedPayload{
			"low-upstream":  {SourceName: "low-upstream", BodyYAML: []byte(upstreamLow), PayloadBytes: len(upstreamLow)},
			"high-upstream": {SourceName: "high-upstream", BodyYAML: []byte(upstreamHigh), PayloadBytes: len(upstreamHigh)},
		},
	}
	rows := []config.SubscriptionRow{
		{Name: "low-upstream", Link: "http://low.test", Priority: 1000, Enable: true},
		{Name: "high-upstream", Link: "http://high.test", Priority: 2000, Enable: true},
	}
	p := merge.NewPipeline(cache, rows, nil, nil, clock.RealClock{}, 12).
		WithFallbackRuleTarget("auto").
		WithCustomRules(loaded)
	mc, err := p.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	pos := func(needle string) int {
		for i, r := range mc.Rules {
			if strings.Contains(r, needle) {
				return i
			}
		}
		return -1
	}
	highUpstreamIdx := pos("high-upstream.test")
	highCustomIdx := pos("high-custom.test")
	lowUpstreamIdx := pos("low-upstream.test")
	lowCustomIdx := pos("low-custom.test")
	matchIdx := pos("MATCH,auto")

	for name, idx := range map[string]int{
		"high-upstream.test (priority 2000)": highUpstreamIdx,
		"high-custom.test (priority 1500)":   highCustomIdx,
		"low-upstream.test (priority 1000)":  lowUpstreamIdx,
		"low-custom.test (priority 300)":     lowCustomIdx,
		"MATCH,auto":                         matchIdx,
	} {
		if idx < 0 {
			t.Errorf("rule %q not found in merged output: %v", name, mc.Rules)
		}
	}

	// Ascending priority order: 300 < 1000 < 1500 < 2000 < MATCH.
	// Lower priority emits first; MATCH fallback always last.
	if !(lowCustomIdx < lowUpstreamIdx &&
		lowUpstreamIdx < highCustomIdx &&
		highCustomIdx < highUpstreamIdx &&
		highUpstreamIdx < matchIdx) {
		t.Errorf("priority ordering violated: low-cust=%d low-up=%d high-cust=%d high-up=%d match=%d (want low-cust < low-up < high-cust < high-up < match)",
			lowCustomIdx, lowUpstreamIdx, highCustomIdx, highUpstreamIdx, matchIdx)
	}

	// Parallel-array invariants on MergedConfig.
	if len(mc.RulePriorities) != len(mc.Rules) || len(mc.RuleContributors) != len(mc.Rules) {
		t.Fatalf("parallel-array length mismatch: rules=%d priorities=%d contributors=%d",
			len(mc.Rules), len(mc.RulePriorities), len(mc.RuleContributors))
	}

	// Spot-check: the high-upstream rule carries priority 2000 and contributor "high-upstream".
	if mc.RulePriorities[highUpstreamIdx] != 2000 {
		t.Errorf("high-upstream priority = %d, want 2000", mc.RulePriorities[highUpstreamIdx])
	}
	if mc.RuleContributors[highUpstreamIdx] != "high-upstream" {
		t.Errorf("high-upstream contributor = %q, want \"high-upstream\"", mc.RuleContributors[highUpstreamIdx])
	}
	// MATCH fallback: priority 0, contributor "".
	if mc.RulePriorities[matchIdx] != 0 || mc.RuleContributors[matchIdx] != "" {
		t.Errorf("MATCH fallback metadata: priority=%d contributor=%q, want priority=0 contributor=\"\"",
			mc.RulePriorities[matchIdx], mc.RuleContributors[matchIdx])
	}
}

// TC-I-003-01: custom rules loaded from a folder are inserted between upstream
// rules and the MATCH fallback. Guards against the cmd/server wire-up gap that
// shipped 003 — Pipeline supported WithCustomRules but main.go never called
// customrules.Load, so custom rules were silently dropped in production.
func TestI_003_01_CustomRulesInOutput(t *testing.T) {
	upstreamYAML := `port: 7890
proxies:
  - {name: 🇺🇸 NY 01, type: trojan, server: us.test, port: 443, password: pw}
proxy-groups:
  - {name: Auto, type: select, proxies: [🇺🇸 NY 01]}
rules:
  - DOMAIN,upstream.test,Auto
  - MATCH,DIRECT
`

	rulesDir := t.TempDir()
	customYAML := `name: corporate-block
priority: 500
rules:
  - DOMAIN,blocked.example.com,REJECT
  - DOMAIN-SUFFIX,corp.example.com,DIRECT
`
	if err := os.WriteFile(filepath.Join(rulesDir, "corporate.yaml"), []byte(customYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := customrules.Load(rulesDir)
	if err != nil {
		t.Fatalf("customrules.Load: %v", err)
	}
	if len(loaded) != 1 || len(loaded[0].Rules) != 2 {
		t.Fatalf("loaded = %+v, want 1 set with 2 rules", loaded)
	}

	cache := &stubMergeCache{
		payloads: map[string]*fetcher.UpstreamCachedPayload{
			"alpha": {
				SourceName:   "alpha",
				BodyYAML:     []byte(upstreamYAML),
				PayloadBytes: len(upstreamYAML),
			},
		},
	}
	rows := []config.SubscriptionRow{
		{Name: "alpha", Link: "http://a.test", Priority: 1000, Enable: true},
	}
	p := merge.NewPipeline(cache, rows, nil, nil, clock.RealClock{}, 12).
		WithFallbackRuleTarget("auto").
		WithCustomRules(loaded)
	mc, err := p.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Find positions of upstream rule, both custom rules, and MATCH fallback.
	pos := func(want string) int {
		for i, r := range mc.Rules {
			if strings.Contains(r, want) {
				return i
			}
		}
		return -1
	}
	upstreamIdx := pos("upstream.test")
	customRejectIdx := pos("blocked.example.com")
	customDirectIdx := pos("corp.example.com")
	matchIdx := pos("MATCH,auto")

	if upstreamIdx < 0 {
		t.Errorf("upstream rule not found in merged rules: %v", mc.Rules)
	}
	if customRejectIdx < 0 {
		t.Errorf("custom rule DOMAIN,blocked.example.com,REJECT not found: %v", mc.Rules)
	}
	if customDirectIdx < 0 {
		t.Errorf("custom rule DOMAIN-SUFFIX,corp.example.com,DIRECT not found: %v", mc.Rules)
	}
	if matchIdx < 0 {
		t.Errorf("MATCH,auto fallback not found: %v", mc.Rules)
	}

	// Position invariant under ascending sort: corporate-block custom (priority 500)
	// emits before alpha upstream (priority 1000). MATCH fallback always last.
	if !(customRejectIdx < upstreamIdx && upstreamIdx < matchIdx) {
		t.Errorf("ordering violated: custom-reject=%d upstream=%d match=%d (want custom < upstream < match under ascending sort)",
			customRejectIdx, upstreamIdx, matchIdx)
	}
	if !(customDirectIdx < upstreamIdx && upstreamIdx < matchIdx) {
		t.Errorf("ordering violated: custom-direct=%d upstream=%d match=%d (want custom < upstream < match under ascending sort)",
			customDirectIdx, upstreamIdx, matchIdx)
	}

	// Priority metadata: under feature 005's unified priority, the upstream
	// "alpha" rule carries the source's priority (1000), not 0. The custom set
	// "corporate-block" carries its declared priority (500). MATCH is 0.
	if len(mc.RulePriorities) != len(mc.Rules) {
		t.Fatalf("RulePriorities length %d != Rules length %d", len(mc.RulePriorities), len(mc.Rules))
	}
	if mc.RulePriorities[upstreamIdx] != 1000 {
		t.Errorf("upstream rule priority = %d, want 1000", mc.RulePriorities[upstreamIdx])
	}
	if mc.RulePriorities[customRejectIdx] != 500 {
		t.Errorf("custom rule priority = %d, want 500", mc.RulePriorities[customRejectIdx])
	}
	if mc.RulePriorities[matchIdx] != 0 {
		t.Errorf("MATCH rule priority = %d, want 0", mc.RulePriorities[matchIdx])
	}
}

// TC-I-006-01: served body contains literal UTF-8 emoji (no \Uxxxxxxxx
// escape) for proxy and proxy-group names whose source contains
// supplementary-plane characters. Round-trip safety: yaml-parsing the
// served body recovers the original namespaced string byte-for-byte.
func TestI_006_01_RoundTripEmoji(t *testing.T) {
	upstreamYAML := `port: 7890
proxies:
  - {name: 🔰 USA-Premium, type: trojan, server: us.test, port: 443, password: pw}
  - {name: 🎁 NY-Auto, type: trojan, server: ny.test, port: 443, password: pw}
proxy-groups:
  - {name: 🚀 Auto, type: select, proxies: [🔰 USA-Premium, 🎁 NY-Auto]}
rules:
  - DOMAIN,a.test,🚀 Auto
  - MATCH,DIRECT
`

	cache := &stubMergeCache{
		payloads: map[string]*fetcher.UpstreamCachedPayload{
			"alpha": {SourceName: "alpha", BodyYAML: []byte(upstreamYAML), PayloadBytes: len(upstreamYAML)},
		},
	}
	rows := []config.SubscriptionRow{
		{Name: "alpha", Link: "http://e.test", Priority: 1000, Enable: true},
	}
	p := merge.NewPipeline(cache, rows, nil, nil, clock.RealClock{}, 12).
		WithFallbackRuleTarget("auto")

	body := renderViaAdapter(t, p)

	// (a) Zero \Uxxxxxxxx escape sequences anywhere in the body.
	escapeRE := regexp.MustCompile(`\\U[0-9A-Fa-f]{8}`)
	if matches := escapeRE.FindAll(body, -1); len(matches) > 0 {
		t.Errorf("served body contains %d \\Uxxxxxxxx escape(s); want zero. First match: %q",
			len(matches), string(matches[0]))
	}

	// (b) Literal namespaced names appear as UTF-8 substrings.
	for _, want := range []string{
		"alpha_🔰 USA-Premium",
		"alpha_🎁 NY-Auto",
		"alpha_🚀 Auto",
	} {
		if !bytes.Contains(body, []byte(want)) {
			t.Errorf("served body missing literal substring %q", want)
		}
	}

	// (c) Round-trip: parse the body and confirm the proxy / proxy-group
	// names match what we constructed (with the per-source prefix applied).
	type nameOnly struct {
		Name string `yaml:"name"`
	}
	type doc struct {
		Proxies     []nameOnly `yaml:"proxies"`
		ProxyGroups []nameOnly `yaml:"proxy-groups"`
	}
	var d doc
	if err := yaml.Unmarshal(body, &d); err != nil {
		t.Fatalf("rendered body does not parse as YAML: %v\n  body: %s", err, body)
	}
	gotProxies := make(map[string]bool, len(d.Proxies))
	for _, p := range d.Proxies {
		gotProxies[p.Name] = true
	}
	for _, want := range []string{"alpha_🔰 USA-Premium", "alpha_🎁 NY-Auto"} {
		if !gotProxies[want] {
			t.Errorf("parsed proxies missing %q; got: %v", want, d.Proxies)
		}
	}
	gotGroups := make(map[string]bool, len(d.ProxyGroups))
	for _, g := range d.ProxyGroups {
		gotGroups[g.Name] = true
	}
	if !gotGroups["alpha_🚀 Auto"] {
		t.Errorf("parsed proxy-groups missing %q; got: %v", "alpha_🚀 Auto", d.ProxyGroups)
	}
}

// build008Pipeline returns a Pipeline configured with one upstream that
// contributes two CN-classifiable proxies and an own-proxies set with two
// own-proxies (no `dialer-proxy`) plus one own-group. Used by all
// TestI_008_* integration tests for the dialer-proxy fan-out feature.
func build008Pipeline(t *testing.T) (*merge.Pipeline, []string) {
	t.Helper()

	upstreamYAML := `port: 7890
proxies:
  - {name: 🇭🇰 HK Direct 01, type: trojan, server: hk.test, port: 443, password: pw}
  - {name: 🇯🇵 JP Direct 02, type: trojan, server: jp.test, port: 443, password: pw}
proxy-groups:
  - {name: Auto, type: select, proxies: [🇭🇰 HK Direct 01, 🇯🇵 JP Direct 02]}
rules:
  - DOMAIN,hk.test,Auto
  - MATCH,DIRECT
`

	ownProxyYAML := `proxies:
  - name: montreal
    type: ss
    server: node.test
    port: 10755
    cipher: chacha20-ietf-poly1305
    password: pw
    udp: true
  - name: markham
    type: ss
    server: 173.32.232.215
    port: 8080
    cipher: xchacha20-ietf-poly1305
    password: pw
    udp: true
proxy-groups:
  - name: Canada-Exit-Proxies
    type: select
    proxies:
      - montreal
      - markham
`
	ownPath := filepath.Join(t.TempDir(), "own-proxies.yaml")
	if err := os.WriteFile(ownPath, []byte(ownProxyYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	own, err := config.LoadOwnProxies(ownPath)
	if err != nil {
		t.Fatal(err)
	}

	cache := &stubMergeCache{
		payloads: map[string]*fetcher.UpstreamCachedPayload{
			"alpha": {SourceName: "alpha", BodyYAML: []byte(upstreamYAML), PayloadBytes: len(upstreamYAML)},
		},
	}
	rows := []config.SubscriptionRow{
		{Name: "alpha", Link: "http://a.test", Priority: 1000, Enable: true},
	}
	p := merge.NewPipeline(cache, rows, own.Proxies, own.ProxyGroups, clock.RealClock{}, 12)
	return p, []string{"montreal", "markham"}
}

// proxyName extracts a proxy mapping's `name` field, or "" if absent.
func proxyName(n *yaml.Node) string {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == "name" {
			return n.Content[i+1].Value
		}
	}
	return ""
}

// proxyField extracts a proxy mapping's scalar field by key.
func proxyField(n *yaml.Node, key string) string {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key && n.Content[i+1].Kind == yaml.ScalarNode {
			return n.Content[i+1].Value
		}
	}
	return ""
}

// groupMembers extracts the `proxies` member list from a proxy-group mapping.
func groupMembers(n *yaml.Node) []string {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == "proxies" && n.Content[i+1].Kind == yaml.SequenceNode {
			out := make([]string, 0, len(n.Content[i+1].Content))
			for _, c := range n.Content[i+1].Content {
				if c.Kind == yaml.ScalarNode {
					out = append(out, c.Value)
				}
			}
			return out
		}
	}
	return nil
}

// findProxyGroup returns the proxy-group mapping with the given `name`, or nil.
func findProxyGroup(groups []*yaml.Node, name string) *yaml.Node {
	for _, g := range groups {
		if proxyName(g) == name {
			return g
		}
	}
	return nil
}

// TC-I-008-01: every emitted `_region_*`/`_continent_*` group has a
// corresponding `via_<group>__<own>` entry in MergedConfig.Proxies for each
// own-proxy without an explicit dialer-proxy.
func TestI_008_01_PerGroupFanoutInServedBody(t *testing.T) {
	p, ownNames := build008Pipeline(t)
	mc, err := p.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Collect the set of region/continent groups emitted.
	targetGroupNames := []string{}
	for _, g := range mc.ProxyGroups {
		name := proxyName(g)
		if strings.HasPrefix(name, "_region_") || strings.HasPrefix(name, "_continent_") {
			targetGroupNames = append(targetGroupNames, name)
		}
	}
	if len(targetGroupNames) == 0 {
		t.Fatal("expected at least one _region_* or _continent_* group in merged output")
	}

	// Build a name set of MergedConfig.Proxies for fast lookup.
	gotProxyNames := make(map[string]*yaml.Node, len(mc.Proxies))
	for _, n := range mc.Proxies {
		gotProxyNames[proxyName(n)] = n
	}

	// For each (own-proxy, target-group) pair, expect one via_<G>__<P>.
	for _, ownStripped := range ownNames {
		// Original own-proxy retained.
		if _, ok := gotProxyNames["_"+ownStripped]; !ok {
			t.Errorf("original own-proxy _%s not in served proxies", ownStripped)
		}
		for _, group := range targetGroupNames {
			expected := "via_" + strings.TrimPrefix(group, "_") + "__" + ownStripped
			entry, ok := gotProxyNames[expected]
			if !ok {
				t.Errorf("missing fan-out entry %q (own=%s, group=%s)", expected, ownStripped, group)
				continue
			}
			if got := proxyField(entry, "dialer-proxy"); got != group {
				t.Errorf("entry %q dialer-proxy = %q, want %q", expected, got, group)
			}
			// Field copy verbatim: server should match the source own-proxy.
			source, ok := gotProxyNames["_"+ownStripped]
			if !ok {
				continue
			}
			if proxyField(entry, "server") != proxyField(source, "server") {
				t.Errorf("entry %q server differs from source", expected)
			}
		}
	}
}

// TC-I-008-02: every own-proxy receives exactly one via_AUTO__<own> with
// dialer-proxy: Proxies (literal); AUTO precedes its per-region/per-continent
// peers in the slice.
func TestI_008_02_AutoCopyInServedBody(t *testing.T) {
	p, ownNames := build008Pipeline(t)
	mc, err := p.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	for _, ownStripped := range ownNames {
		autoName := "via_AUTO__" + ownStripped
		var autoIdx, firstViaIdx = -1, -1
		for i, n := range mc.Proxies {
			name := proxyName(n)
			if name == autoName {
				autoIdx = i
			}
			if firstViaIdx == -1 && strings.HasPrefix(name, "via_") && strings.HasSuffix(name, "__"+ownStripped) && name != autoName {
				firstViaIdx = i
			}
		}
		if autoIdx == -1 {
			t.Errorf("missing AUTO entry %q", autoName)
			continue
		}
		auto := mc.Proxies[autoIdx]
		if got := proxyField(auto, "dialer-proxy"); got != "Proxies" {
			t.Errorf("entry %q dialer-proxy = %q, want Proxies (literal)", autoName, got)
		}
		// AUTO must precede this own-proxy's per-region/per-continent peers.
		if firstViaIdx != -1 && firstViaIdx < autoIdx {
			t.Errorf("AUTO entry %q at index %d appears AFTER per-group peer at index %d (want AUTO first)", autoName, autoIdx, firstViaIdx)
		}
	}
}

// TC-I-008-03: the always-present `Proxies` selector group's `proxies:`
// member list MUST NOT contain own-proxies (`_<own>`) or fan-out copies
// (`via_*`). Upstream-prefixed proxies and `_region_*`/`_continent_*` group
// names remain present.
func TestI_008_03_ProxiesGroupExcludesOwnAndViaProxies(t *testing.T) {
	p, _ := build008Pipeline(t)
	mc, err := p.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	proxiesGroup := findProxyGroup(mc.ProxyGroups, "Proxies")
	if proxiesGroup == nil {
		t.Fatal("always-present Proxies group not found in MergedConfig.ProxyGroups")
	}
	members := groupMembers(proxiesGroup)
	if len(members) == 0 {
		t.Fatal("Proxies group has empty member list")
	}

	upstreamCount, regionGroupCount, continentGroupCount := 0, 0, 0
	for _, m := range members {
		if strings.HasPrefix(m, "via_") {
			t.Errorf("Proxies group member %q must not be a fan-out copy", m)
		}
		if strings.HasPrefix(m, "_") &&
			!strings.HasPrefix(m, "_region_") &&
			!strings.HasPrefix(m, "_continent_") {
			t.Errorf("Proxies group member %q must not be an own-proxy or own-group", m)
		}
		switch {
		case strings.HasPrefix(m, "_region_"):
			regionGroupCount++
		case strings.HasPrefix(m, "_continent_"):
			continentGroupCount++
		case !strings.HasPrefix(m, "_"):
			upstreamCount++
		}
	}
	if upstreamCount == 0 {
		t.Error("Proxies group has zero upstream-prefixed members; expected at least one")
	}
	if regionGroupCount == 0 {
		t.Error("Proxies group has zero _region_* members; expected at least one")
	}
	if continentGroupCount == 0 {
		t.Error("Proxies group has zero _continent_* members; expected at least one")
	}
}

// TC-I-008-04: two consecutive Build() calls produce byte-identical fan-out.
func TestI_008_04_FanoutDeterminism(t *testing.T) {
	p, _ := build008Pipeline(t)

	mc1, err := p.Build()
	if err != nil {
		t.Fatalf("Build #1: %v", err)
	}
	mc2, err := p.Build()
	if err != nil {
		t.Fatalf("Build #2: %v", err)
	}

	if len(mc1.Proxies) != len(mc2.Proxies) {
		t.Fatalf("len(mc1.Proxies)=%d, len(mc2.Proxies)=%d", len(mc1.Proxies), len(mc2.Proxies))
	}
	for i := range mc1.Proxies {
		if proxyName(mc1.Proxies[i]) != proxyName(mc2.Proxies[i]) {
			t.Errorf("Proxies[%d] name diff: %q vs %q", i, proxyName(mc1.Proxies[i]), proxyName(mc2.Proxies[i]))
		}
		if proxyField(mc1.Proxies[i], "dialer-proxy") != proxyField(mc2.Proxies[i], "dialer-proxy") {
			t.Errorf("Proxies[%d] dialer-proxy diff", i)
		}
	}

	// Stronger determinism check: marshal MergedConfig.Proxies twice and
	// compare SHA-256.
	hash := func(mc *merge.MergedConfig) [32]byte {
		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		for _, n := range mc.Proxies {
			if err := enc.Encode(n); err != nil {
				t.Fatalf("encode: %v", err)
			}
		}
		_ = enc.Close()
		return sha256.Sum256(buf.Bytes())
	}
	h1, h2 := hash(mc1), hash(mc2)
	if h1 != h2 {
		t.Errorf("fan-out section bytes differ across two Builds: %x vs %x", h1, h2)
	}
}

// TC-I-008-05: own-proxy with explicit dialer-proxy is preserved verbatim;
// no via_AUTO/via_region/via_continent copies are generated for it.
// Other own-proxies (no explicit dialer-proxy) continue to receive full
// fan-out.
func TestI_008_05_ExplicitDialerProxyEndToEnd(t *testing.T) {
	upstreamYAML := `port: 7890
proxies:
  - {name: 🇭🇰 HK Direct 01, type: trojan, server: hk.test, port: 443, password: pw}
  - {name: 🇯🇵 JP Direct 02, type: trojan, server: jp.test, port: 443, password: pw}
proxy-groups:
  - {name: Auto, type: select, proxies: [🇭🇰 HK Direct 01, 🇯🇵 JP Direct 02]}
rules:
  - DOMAIN,hk.test,Auto
  - MATCH,DIRECT
`

	ownYAML := `proxies:
  - name: regular-own
    type: ss
    server: a.test
    port: 1
    cipher: chacha20-ietf-poly1305
    password: pw
  - name: explicit-dialer-own
    type: ss
    server: b.test
    port: 2
    cipher: chacha20-ietf-poly1305
    password: pw
    dialer-proxy: DIRECT
proxy-groups: []
`
	ownPath := filepath.Join(t.TempDir(), "own-proxies.yaml")
	if err := os.WriteFile(ownPath, []byte(ownYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	own, err := config.LoadOwnProxies(ownPath)
	if err != nil {
		t.Fatal(err)
	}
	cache := &stubMergeCache{
		payloads: map[string]*fetcher.UpstreamCachedPayload{
			"alpha": {SourceName: "alpha", BodyYAML: []byte(upstreamYAML), PayloadBytes: len(upstreamYAML)},
		},
	}
	rows := []config.SubscriptionRow{
		{Name: "alpha", Link: "http://a.test", Priority: 1000, Enable: true},
	}
	p := merge.NewPipeline(cache, rows, own.Proxies, own.ProxyGroups, clock.RealClock{}, 12)
	mc, err := p.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// _explicit-dialer-own retains its dialer-proxy and produces no fan-out.
	var explicit *yaml.Node
	for _, n := range mc.Proxies {
		name := proxyName(n)
		if name == "_explicit-dialer-own" {
			explicit = n
		}
		if strings.HasPrefix(name, "via_") && strings.HasSuffix(name, "__explicit-dialer-own") {
			t.Errorf("unexpected fan-out copy %q for own-proxy with explicit dialer-proxy", name)
		}
	}
	if explicit == nil {
		t.Fatal("original own-proxy _explicit-dialer-own missing from served body")
	}
	if got := proxyField(explicit, "dialer-proxy"); got != "DIRECT" {
		t.Errorf("_explicit-dialer-own dialer-proxy = %q, want DIRECT (preserved verbatim)", got)
	}

	// _regular-own DOES receive fan-out.
	gotRegularAuto := false
	for _, n := range mc.Proxies {
		if proxyName(n) == "via_AUTO__regular-own" {
			gotRegularAuto = true
			break
		}
	}
	if !gotRegularAuto {
		t.Error("expected via_AUTO__regular-own to be emitted for the non-skipped own-proxy")
	}
}

// TC-I-009-01: with URL_PATH_PREFIX set, the subscription handler is mounted
// at "/<prefix>" (and "/<prefix>/"); requests to root return 404; /health
// stays at root.
func TestI_009_01_PathPrefixRouting(t *testing.T) {
	opts := defaultOpts()
	opts.pathPrefix = "/abc123"
	tc := newTestClusterWithOpts(t, opts)

	cases := []struct {
		name string
		path string
		want int
	}{
		{"root no prefix returns 404", "/?token=" + validToken, http.StatusNotFound},
		{"prefix exact returns 200", "/abc123?token=" + validToken, http.StatusOK},
		{"prefix with trailing slash returns 200", "/abc123/?token=" + validToken, http.StatusOK},
		{"health at root returns 200", "/health", http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, err := http.Get(tc.URL() + c.path)
			if err != nil {
				t.Fatalf("GET %s: %v", c.path, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != c.want {
				t.Errorf("GET %s status = %d, want %d", c.path, resp.StatusCode, c.want)
			}
		})
	}
}
