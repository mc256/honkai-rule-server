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

// rulesetAlphaYAML is shaped after a real upstream that routes via RULE-SET
// (016). It exercises: a backed RULE-SET with a non-built-in proxy-group target
// + a no-resolve modifier (US1 FR-003/FR-004); a backed RULE-SET with a
// built-in target; an UNBACKED reference (`Missing`) dropped per FR-009 (US3);
// a backed RULE-SET whose TARGET group (`EmptyTarget`) is empty and pruned by
// 015, so the rule is retargeted while keeping its provider field (FR-015); a
// defined-but-unreferenced provider (`Unused`) pruned per FR-010; and a
// malformed provider entry (`Bad`) skipped per FR-011.
const rulesetAlphaYAML = `proxies:
  - {name: A-Node, type: ss, server: a.test, port: 443, cipher: aes-256-gcm, password: synthetic-pw, udp: true}
proxy-groups:
  - {name: CN-Direct, type: select, proxies: [A-Node]}
  - {name: EmptyTarget, type: select, proxies: []}
rule-providers:
  Local-IP:
    type: http
    behavior: ipcidr
    format: mrs
    url: 'https://cdn.example.test/Local-IP.mrs'
    path: ./ruleset/Local-IP.mrs
    proxy: DIRECT
    interval: 86400
  China-Site:
    type: http
    behavior: domain
    format: mrs
    url: 'https://cdn.example.test/China-Site.mrs'
    path: ./ruleset/China-Site.mrs
    proxy: DIRECT
    interval: 86400
  Unused:
    type: http
    behavior: domain
    format: mrs
    url: 'https://cdn.example.test/Unused.mrs'
    path: ./ruleset/Unused.mrs
    proxy: DIRECT
    interval: 86400
  Bad: "not-a-mapping"
rules:
  - RULE-SET,Local-IP,CN-Direct,no-resolve
  - RULE-SET,China-Site,DIRECT
  - RULE-SET,Missing,DIRECT
  - RULE-SET,Local-IP,EmptyTarget
  - DOMAIN-SUFFIX,direct.test,CN-Direct
  - MATCH,DIRECT
`

// rulesetBetaYAML defines a provider with the SAME bare name (`Local-IP`) as
// alpha to exercise cross-source collision-safety (US2 FR-008/FR-012).
const rulesetBetaYAML = `proxies:
  - {name: B-Node, type: ss, server: b.test, port: 443, cipher: aes-256-gcm, password: synthetic-pw, udp: true}
proxy-groups:
  - {name: CN-Direct, type: select, proxies: [B-Node]}
rule-providers:
  Local-IP:
    type: http
    behavior: ipcidr
    format: mrs
    url: 'https://cdn.example.test/Local-IP.mrs'
    path: ./ruleset/Local-IP.mrs
    proxy: DIRECT
    interval: 86400
rules:
  - RULE-SET,Local-IP,CN-Direct
  - DOMAIN-SUFFIX,beta.test,CN-Direct
  - MATCH,DIRECT
`

// buildRulesetPipeline feeds the two crafted upstreams (alpha priority 1000,
// beta priority 2000) with the fallback rule target set to the always-present
// `Proxies` selector so a 015-retargeted rule lands on a present group.
func buildRulesetPipeline(t *testing.T) *merge.Pipeline {
	t.Helper()
	cache := &stubMergeCache{
		payloads: map[string]*fetcher.UpstreamCachedPayload{
			"alpha": {SourceName: "alpha", BodyYAML: []byte(rulesetAlphaYAML), PayloadBytes: len(rulesetAlphaYAML)},
			"beta":  {SourceName: "beta", BodyYAML: []byte(rulesetBetaYAML), PayloadBytes: len(rulesetBetaYAML)},
		},
	}
	rows := []config.SubscriptionRow{
		{Name: "alpha", Link: "http://a.test", Priority: 1000, Enable: true},
		{Name: "beta", Link: "http://b.test", Priority: 2000, Enable: true},
	}
	return merge.NewPipeline(cache, rows, nil, nil, clock.RealClock{}, 12).
		WithFallbackRuleTarget("Proxies").
		WithURLTestParams(merge.URLTestParams{
			URL: "https://www.gstatic.com/generate_204", IntervalSeconds: 10, TimeoutMS: 3000, MaxFailedTimes: 3, Lazy: true,
		}).
		WithLoadBalanceParams(merge.LoadBalanceParams{
			URL: "https://www.gstatic.com/generate_204", IntervalSeconds: 300, TimeoutMS: 1500, MaxFailedTimes: 3, Lazy: true, Strategy: "round-robin",
		})
}

// TestSnapshot_RuleSetServedConfig snapshots the served body for the RULE-SET
// scenario and asserts the served-config rule-providers invariants from
// contracts/served-config-rule-providers.md (016 US1/US2/US3).
//
// Drift fails the test. Update with:
//
//	UPDATE_SNAPSHOTS=true go test ./internal/integration/ -run TestSnapshot_RuleSetServedConfig
func TestSnapshot_RuleSetServedConfig(t *testing.T) {
	body := renderViaAdapter(t, buildRulesetPipeline(t))

	var doc struct {
		ProxyGroups   []map[string]any          `yaml:"proxy-groups"`
		Rules         []string                  `yaml:"rules"`
		RuleProviders map[string]map[string]any `yaml:"rule-providers"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("rendered body does not parse: %v", err)
	}

	// C1/C2 + FR-010: only referenced, namespaced providers present.
	rp := doc.RuleProviders
	for _, want := range []string{"alpha_Local-IP", "alpha_China-Site", "beta_Local-IP"} {
		if _, ok := rp[want]; !ok {
			t.Errorf("rule-providers missing %q; got keys %v", want, keysOf(rp))
		}
	}
	for _, notWant := range []string{"alpha_Unused", "alpha_Missing", "alpha_Bad", "Local-IP"} {
		if _, ok := rp[notWant]; ok {
			t.Errorf("rule-providers contains unexpected %q (FR-009/FR-010/FR-011/namespacing)", notWant)
		}
	}

	// C2/FR-012 + FR-008: same-named providers from two sources have distinct
	// keys AND distinct cache paths.
	pa, _ := rp["alpha_Local-IP"]["path"].(string)
	pb, _ := rp["beta_Local-IP"]["path"].(string)
	if pa != "./ruleset/alpha_Local-IP.mrs" {
		t.Errorf("alpha_Local-IP path = %q, want ./ruleset/alpha_Local-IP.mrs", pa)
	}
	if pb != "./ruleset/beta_Local-IP.mrs" {
		t.Errorf("beta_Local-IP path = %q, want ./ruleset/beta_Local-IP.mrs", pb)
	}
	// FR-007: built-in fetch-through proxy left unchanged.
	if px, _ := rp["alpha_Local-IP"]["proxy"].(string); px != "DIRECT" {
		t.Errorf("alpha_Local-IP proxy = %q, want DIRECT", px)
	}

	// C4/FR-003/FR-004: RULE-SET provider field + non-built-in group target
	// both namespaced; modifier preserved.
	assertRule(t, doc.Rules, "RULE-SET,alpha_Local-IP,alpha_CN-Direct,no-resolve")
	assertRule(t, doc.Rules, "RULE-SET,alpha_China-Site,DIRECT")
	assertRule(t, doc.Rules, "RULE-SET,beta_Local-IP,beta_CN-Direct")

	// FR-009: the unbacked rule is gone, with no dangling reference.
	for i, r := range doc.Rules {
		if strings.Contains(r, "alpha_Missing") {
			t.Errorf("rule[%d]=%q references dropped/unbacked provider alpha_Missing", i, r)
		}
	}

	// FR-015: the RULE-SET whose target group was pruned by 015 survives with
	// its provider field intact, retargeted to the fallback (Proxies).
	assertRule(t, doc.Rules, "RULE-SET,alpha_Local-IP,Proxies")
	for i, r := range doc.Rules {
		if strings.HasSuffix(r, ",alpha_EmptyTarget") {
			t.Errorf("rule[%d]=%q still targets pruned group alpha_EmptyTarget", i, r)
		}
	}

	// FR-014: RULE-SET rules participate in priority ordering — alpha (priority
	// 1000) emits before beta (priority 2000); no relocation to a separate block.
	idxAlpha := ruleIndex(doc.Rules, "RULE-SET,alpha_China-Site,DIRECT")
	idxBeta := ruleIndex(doc.Rules, "RULE-SET,beta_Local-IP,beta_CN-Direct")
	if idxAlpha < 0 || idxBeta < 0 || !(idxAlpha < idxBeta) {
		t.Errorf("priority ordering violated: alpha RULE-SET idx=%d, beta RULE-SET idx=%d", idxAlpha, idxBeta)
	}

	snapshotPath := filepath.Join(snapshotsDir, "served-config-ruleset.snap.yaml")
	compareOrUpdate(t, snapshotPath, body)
}

func keysOf(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func ruleIndex(rules []string, want string) int {
	for i, r := range rules {
		if r == want {
			return i
		}
	}
	return -1
}

func assertRule(t *testing.T, rules []string, want string) {
	t.Helper()
	if ruleIndex(rules, want) < 0 {
		t.Errorf("served rules missing %q", want)
	}
}
