package integration

import (
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mc256/honkai-rule-server/internal/clock"
	"github.com/mc256/honkai-rule-server/internal/config"
	"github.com/mc256/honkai-rule-server/internal/fetcher"
	"github.com/mc256/honkai-rule-server/internal/merge"
)

// pruneUpstreamYAML is a crafted upstream subscription whose merged output
// exercises feature 015 end-to-end: `EmptyG` is a proxy-group with no
// members (→ removed); `NodeSelect` lists `EmptyG` among its members (→ that
// dangling reference is dropped); and a rule targets `EmptyG` (→ that rule is
// redirected to the fallback rule target). The trailing `MATCH` rule is
// dropped by feature 002's per-source trailing-rule drop, so the
// EmptyG-targeting rule is not last and survives the merge.
const pruneUpstreamYAML = `proxies:
  - {name: HK-1, type: ss, server: hk1.test, port: 443, cipher: aes-256-gcm, password: synthetic-pw, udp: true}
proxy-groups:
  - {name: NodeSelect, type: select, proxies: [HK-1, EmptyG]}
  - {name: EmptyG, type: select, proxies: []}
rules:
  - DOMAIN-SUFFIX,empty.test,EmptyG
  - DOMAIN-SUFFIX,node.test,NodeSelect
  - MATCH,NodeSelect
`

// buildPrunePipeline constructs a pipeline fed solely by the crafted
// pruneUpstreamYAML, with the fallback rule target set to the always-present
// `Proxies` selector so a retargeted rule lands on a present group.
func buildPrunePipeline(t *testing.T) *merge.Pipeline {
	t.Helper()
	cache := &stubMergeCache{
		payloads: map[string]*fetcher.UpstreamCachedPayload{
			"alpha": {
				SourceName:   "alpha",
				BodyYAML:     []byte(pruneUpstreamYAML),
				PayloadBytes: len(pruneUpstreamYAML),
			},
		},
	}
	rows := []config.SubscriptionRow{
		{Name: "alpha", Link: "http://a.test", Priority: 1000, Enable: true},
	}
	return merge.NewPipeline(cache, rows, nil, nil, clock.RealClock{}, 12).
		WithFallbackRuleTarget("Proxies").
		WithURLTestParams(merge.URLTestParams{
			URL:             "https://www.gstatic.com/generate_204",
			IntervalSeconds: 10,
			TimeoutMS:       3000,
			MaxFailedTimes:  3,
			Lazy:            true,
		}).
		WithLoadBalanceParams(merge.LoadBalanceParams{
			URL:             "https://www.gstatic.com/generate_204",
			IntervalSeconds: 300,
			TimeoutMS:       1500,
			MaxFailedTimes:  3,
			Lazy:            true,
			Strategy:        "round-robin",
		})
}

// TestSnapshot_PruneServedConfig snapshots the served body for a configuration
// containing an empty proxy-group, and asserts the served-config proxy-group
// invariant from contracts/served-config-proxy-groups.md (015 US1/US2/US3).
//
// Drift fails the test. Update with:
//
//	UPDATE_SNAPSHOTS=true go test ./internal/integration/ -run TestSnapshot_PruneServedConfig
func TestSnapshot_PruneServedConfig(t *testing.T) {
	body := renderViaAdapter(t, buildPrunePipeline(t))

	// Sanity: body parses as Clash YAML.
	var doc struct {
		ProxyGroups []map[string]any `yaml:"proxy-groups"`
		Rules       []string         `yaml:"rules"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("rendered body does not parse: %v", err)
	}

	// Collect the names of every served proxy-group.
	present := make(map[string]bool, len(doc.ProxyGroups))
	for _, g := range doc.ProxyGroups {
		name, _ := g["name"].(string)
		present[name] = true
	}

	// FR-001/FR-007: the empty group is gone; no served group is empty
	// except the always-present `Proxies` selector.
	if present["alpha_EmptyG"] {
		t.Errorf("empty group alpha_EmptyG was not pruned")
	}
	for _, g := range doc.ProxyGroups {
		name, _ := g["name"].(string)
		members, _ := g["proxies"].([]any)
		if len(members) == 0 && name != "Proxies" {
			t.Errorf("served proxy-group %q has an empty member list", name)
		}
	}

	// FR-006: no surviving group lists a name that is not a present group or
	// a defined proxy. We only assert the removed group is not referenced.
	for _, g := range doc.ProxyGroups {
		name, _ := g["name"].(string)
		members, _ := g["proxies"].([]any)
		for _, m := range members {
			if ms, _ := m.(string); ms == "alpha_EmptyG" {
				t.Errorf("group %q still lists removed group alpha_EmptyG", name)
			}
		}
	}

	// FR-008: no rule targets the removed group.
	for i, r := range doc.Rules {
		if strings.HasSuffix(r, ",alpha_EmptyG") {
			t.Errorf("rule[%d] = %q still targets removed group alpha_EmptyG", i, r)
		}
	}

	snapshotPath := filepath.Join(snapshotsDir, "served-config-prune.snap.yaml")
	compareOrUpdate(t, snapshotPath, body)
}
