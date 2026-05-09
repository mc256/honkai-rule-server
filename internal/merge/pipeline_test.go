package merge

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/mc256/honkai-rule-server/internal/clock"
	"github.com/mc256/honkai-rule-server/internal/config"
	"github.com/mc256/honkai-rule-server/internal/fetcher"
)

// stubCache satisfies CacheReader from a static map; lets us avoid spinning
// up a real fetcher.Cache + UpstreamFetcher in unit tests.
type stubCache struct {
	payloads map[string]*fetcher.UpstreamCachedPayload
}

func (s *stubCache) Get(name string) (*fetcher.UpstreamCachedPayload, bool) {
	p, ok := s.payloads[name]
	return p, ok
}

const upstreamA = `port: 7890
proxies:
  - {name: A1, type: trojan, server: a.test, port: 443, password: pw}
  - {name: A2, type: trojan, server: a.test, port: 443, password: pw}
proxy-groups:
  - {name: Auto, type: select, proxies: [A1, A2]}
rules:
  - DOMAIN,a.test,Auto
  - MATCH,DIRECT
`

const upstreamB = `port: 7890
proxies:
  - {name: B1, type: trojan, server: b.test, port: 443, password: pw}
  - {name: A1, type: trojan, server: collide.test, port: 443, password: pw}
proxy-groups:
  - {name: Auto, type: select, proxies: [B1]}
  - {name: B-only, type: select, proxies: [B1]}
rules:
  - DOMAIN,b.test,Auto
`

func makePayload(name, body string) *fetcher.UpstreamCachedPayload {
	return &fetcher.UpstreamCachedPayload{
		SourceName:   name,
		BodyYAML:     []byte(body),
		PayloadBytes: len(body),
	}
}

func TestPipeline_BuildHappyPath(t *testing.T) {
	cache := &stubCache{payloads: map[string]*fetcher.UpstreamCachedPayload{
		"src_a": makePayload("src_a", upstreamA),
		"src_b": makePayload("src_b", upstreamB),
	}}

	rows := []config.SubscriptionRow{
		{Name: "src_a", Link: "http://a.test", Priority: 1000, Enable: true},
		{Name: "src_b", Link: "http://b.test", Priority: 2000, Enable: true},
	}

	p := NewPipeline(cache, rows, nil, nil, clock.NewFakeClock(time.Now()), 12)
	mc, err := p.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Contributing sources sorted by priority desc → src_b first.
	if len(mc.ContributingSources) != 2 ||
		mc.ContributingSources[0] != "src_b" ||
		mc.ContributingSources[1] != "src_a" {
		t.Errorf("ContributingSources = %v, want [src_b src_a]", mc.ContributingSources)
	}

	// Proxies: each upstream name is now prefixed with source name.
	// src_b: B1, A1 → src_b_B1, src_b_A1
	// src_a: A1, A2 → src_a_A1, src_a_A2
	// Cross-source collision is now impossible (FR-004 prefix prevents it).
	// No @source suffix needed; no collision records.
	if len(mc.Proxies) != 4 {
		t.Errorf("Proxies count = %d, want 4", len(mc.Proxies))
	}
	names := proxyNames(mc.Proxies)
	wantSet := map[string]bool{"src_b_B1": true, "src_b_A1": true, "src_a_A1": true, "src_a_A2": true}
	for _, n := range names {
		if !wantSet[n] {
			t.Errorf("unexpected proxy name %q (got %v)", n, names)
		}
		delete(wantSet, n)
	}
	for n := range wantSet {
		t.Errorf("missing expected proxy name %q (got %v)", n, names)
	}

	// Proxy-groups: each group is prefixed per FR-005.
	// src_b: Auto → src_b_Auto, B-only → src_b_B-only
	// src_a: Auto → src_a_Auto
	// plus the always-present Proxies group (FR-009a, never prefixed).
	groupNames := proxyNames(mc.ProxyGroups)
	wantGroupSet := map[string]bool{"src_b_Auto": true, "src_b_B-only": true, "src_a_Auto": true, "Proxies": true, "_region_UNKNOWN": true, "_lb_region_UNKNOWN": true}
	for _, n := range groupNames {
		if !wantGroupSet[n] {
			t.Errorf("unexpected group name %q (got %v)", n, groupNames)
		}
		delete(wantGroupSet, n)
	}
	for n := range wantGroupSet {
		t.Errorf("missing expected group %q (got %v)", n, groupNames)
	}

	// Rules: trailing rule drop is unconditional (FR-008).
	// upstreamA: [DOMAIN,a.test,Auto, MATCH,DIRECT] → drop last → [DOMAIN,a.test,Auto]
	// upstreamB: [DOMAIN,b.test,Auto] → drop last → [] (single rule dropped)
	// Output: [src_a's rules], then server-emitted MATCH,auto fallback.
	if len(mc.Rules) != 2 ||
		mc.Rules[0] != "DOMAIN,a.test,src_a_Auto" ||
		mc.Rules[1] != "MATCH,auto" {
		t.Errorf("Rules = %v, want [DOMAIN,a.test,src_a_Auto, MATCH,auto]", mc.Rules)
	}

	// Collisions: after 002's prefix pass, cross-source collisions are impossible.
	// No collision records expected.
	if len(mc.Collisions) != 0 {
		t.Errorf("Collisions = %+v, want empty (cross-source collision impossible after prefix)", mc.Collisions)
	}
}

func TestPipeline_DisabledSourceSkipped(t *testing.T) {
	cache := &stubCache{payloads: map[string]*fetcher.UpstreamCachedPayload{
		"on":  makePayload("on", upstreamA),
		"off": makePayload("off", upstreamB),
	}}
	rows := []config.SubscriptionRow{
		{Name: "on", Link: "http://a.test", Priority: 1, Enable: true},
		{Name: "off", Link: "http://b.test", Priority: 2, Enable: false},
	}
	p := NewPipeline(cache, rows, nil, nil, clock.RealClock{}, 12)
	mc, err := p.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(mc.ContributingSources) != 1 || mc.ContributingSources[0] != "on" {
		t.Errorf("ContributingSources = %v, want [on]", mc.ContributingSources)
	}
}

func TestPipeline_MissingCacheEntrySkipped(t *testing.T) {
	cache := &stubCache{payloads: map[string]*fetcher.UpstreamCachedPayload{}} // empty
	rows := []config.SubscriptionRow{
		{Name: "missing", Link: "http://x.test", Priority: 1, Enable: true},
	}
	p := NewPipeline(cache, rows, nil, nil, clock.RealClock{}, 12)
	mc, err := p.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(mc.ContributingSources) != 0 || len(mc.Proxies) != 0 {
		t.Errorf("expected empty merged config when no cache; got contributing=%v proxies=%d",
			mc.ContributingSources, len(mc.Proxies))
	}
}

func TestPipeline_NoIO(t *testing.T) {
	// Verifies the pipeline reads only from the injected cache and the
	// injected clock — no goroutines, no network. (If anyone introduces
	// I/O later, this test still passes silently; this is here as a
	// living comment + sanity check.)
	cache := &stubCache{payloads: map[string]*fetcher.UpstreamCachedPayload{
		"a": makePayload("a", upstreamA),
	}}
	p := NewPipeline(cache, []config.SubscriptionRow{
		{Name: "a", Link: "http://x.test", Priority: 1, Enable: true},
	}, nil, nil, clock.RealClock{}, 12)
	for i := 0; i < 50; i++ {
		if _, err := p.Build(); err != nil {
			t.Fatal(err)
		}
	}
}

// TestPipeline_BuildOwnProxyUnderscore covers FR-007a/b: own-proxies and
// own-groups are rewritten with leading underscore prefix.
func TestPipeline_BuildOwnProxyUnderscore(t *testing.T) {
	cache := &stubCache{payloads: map[string]*fetcher.UpstreamCachedPayload{
		"src_a": makePayload("src_a", upstreamA),
	}}

	ownProxy := mustParseYAMLNode("name: my-server\ntype: trojan\nserver: a.test\nport: 443\npassword: pw\n")
	ownGroup := mustParseYAMLNode("name: my-pool\ntype: select\nproxies:\n  - my-server\n  - DIRECT\n")

	rows := []config.SubscriptionRow{
		{Name: "src_a", Link: "http://a.test", Priority: 1000, Enable: true},
	}
	p := NewPipeline(cache, rows, []*yaml.Node{ownProxy}, []*yaml.Node{ownGroup}, clock.RealClock{}, 12)
	mc, err := p.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Own-proxy must appear with underscore prefix.
	foundOwnProxy := false
	for _, n := range mc.Proxies {
		name := getMappingField(n, "name")
		if name == "_my-server" {
			foundOwnProxy = true
		}
	}
	if !foundOwnProxy {
		t.Errorf("own-proxy _my-server not found in merged proxies; names: %v", proxyNames(mc.Proxies))
	}

	// Own-group must appear with underscore prefix.
	foundOwnGroup := false
	for _, g := range mc.ProxyGroups {
		name := getMappingField(g, "name")
		if name == "_my-pool" {
			foundOwnGroup = true
			members := mappingMembers(g, "proxies")
			// Member ref to own-proxy must be underscore-prefixed; DIRECT untouched.
			wantMembers := []string{"_my-server", "DIRECT"}
			if len(members) != len(wantMembers) {
				t.Errorf("_my-pool members = %v, want %v", members, wantMembers)
			}
			for i, m := range members {
				if m != wantMembers[i] {
					t.Errorf("_my-pool members[%d] = %q, want %q", i, m, wantMembers[i])
				}
			}
		}
	}
	if !foundOwnGroup {
		t.Errorf("own-group _my-pool not found in merged groups; names: %v", proxyNames(mc.ProxyGroups))
	}
}

func proxyNames(nodes []*yaml.Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, getMappingField(n, "name"))
	}
	return out
}
