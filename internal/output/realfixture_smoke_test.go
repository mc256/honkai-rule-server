package output

import (
	"os"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/mc256/honkai-rule-server/internal/clock"
	"github.com/mc256/honkai-rule-server/internal/config"
	"github.com/mc256/honkai-rule-server/internal/fetcher"
	"github.com/mc256/honkai-rule-server/internal/merge"
)

// Smoke test: load the real example fixtures, run them through the full
// merge + output pipeline, verify the result is valid Clash YAML.
func TestRealFixtures_FullPipelineSmoke(t *testing.T) {
	alphaBody, err := os.ReadFile("../integration/testdata/fixtures/upstream/alpha.yaml")
	if err != nil {
		t.Skipf("alpha fixture missing: %v", err)
	}
	betaBody, err := os.ReadFile("../integration/testdata/fixtures/upstream/beta.yaml")
	if err != nil {
		t.Skipf("beta fixture missing: %v", err)
	}
	templateBody, err := os.ReadFile("../../templates/served-config.template.yaml")
	if err != nil {
		t.Skipf("template missing: %v", err)
	}

	cache := &stubCache{p: map[string]*fetcher.UpstreamCachedPayload{
		"alpha":      {SourceName: "alpha", BodyYAML: alphaBody, PayloadBytes: len(alphaBody)},
		"beta": {SourceName: "beta", BodyYAML: betaBody, PayloadBytes: len(betaBody)},
	}}
	rows := []config.SubscriptionRow{
		{Name: "alpha", Link: "http://x.test/e", Priority: 1000, Enable: true},
		{Name: "beta", Link: "http://x.test/b", Priority: 2000, Enable: true},
	}
	pipeline := merge.NewPipeline(cache, rows, nil, nil, clock.NewFakeClock(time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)), 12)
	mc, err := pipeline.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	t.Logf("merged: %d proxies, %d proxy-groups, %d rules; %d collisions, %d group conflicts; sources %v",
		len(mc.Proxies), len(mc.ProxyGroups), len(mc.Rules),
		len(mc.Collisions), len(mc.GroupConflicts), mc.ContributingSources)

	if len(mc.Proxies) < 100 {
		t.Errorf("expected >= 100 proxies from real fixtures, got %d", len(mc.Proxies))
	}
	if len(mc.Rules) < 50 {
		t.Errorf("expected >= 50 rules from real fixtures, got %d", len(mc.Rules))
	}

	adapter, err := NewSubscriptionModeFromBytes(templateBody)
	if err != nil {
		t.Fatalf("NewSubscriptionModeFromBytes: %v", err)
	}
	rendered, err := adapter.Render(mc)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	var got map[string]any
	if err := yaml.Unmarshal(rendered.Body, &got); err != nil {
		t.Fatalf("rendered body does not parse: %v", err)
	}
	if proxies, _ := got["proxies"].([]any); len(proxies) < 100 {
		t.Errorf("rendered proxies count = %d, want >= 100", len(proxies))
	}

	for _, sentinel := range []string{"__MERGED_PROXIES__", "__MERGED_PROXY_GROUPS__", "__MERGED_RULES__"} {
		if strings.Contains(string(rendered.Body), sentinel) {
			t.Errorf("rendered body still contains placeholder %s", sentinel)
		}
	}

	// Emoji / Chinese proxy names round-trip *functionally*. The wire form
	// may use raw UTF-8 or \UXXXXXXXX escapes (yaml.v3 escapes some
	// supplementary-plane codepoints in double-quoted form); both decode
	// to the same string after parsing.
	type group struct {
		Name string `yaml:"name"`
	}
	type clashCfg struct {
		ProxyGroups []group `yaml:"proxy-groups"`
	}
	var parsed clashCfg
	if err := yaml.Unmarshal(rendered.Body, &parsed); err != nil {
		t.Fatalf("parse rendered body for group-name check: %v", err)
	}
	hasBeta, hasAlphaShield := false, false
	for _, g := range parsed.ProxyGroups {
		if strings.Contains(g.Name, "synthetic-pool") {
			hasBeta = true
		}
		if strings.Contains(g.Name, "🔰") {
			hasAlphaShield = true
		}
	}
	if !hasBeta {
		t.Errorf("parsed proxy-groups missing synthetic-pool; got %d groups", len(parsed.ProxyGroups))
	}
	if !hasAlphaShield {
		t.Errorf("parsed proxy-groups missing 🔰-prefixed group; got %d groups", len(parsed.ProxyGroups))
	}

	t.Logf("rendered body: %d bytes", len(rendered.Body))
}
