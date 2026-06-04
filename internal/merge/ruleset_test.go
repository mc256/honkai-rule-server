package merge

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// providerFixture is a single-provider rule-providers mapping used by the unit
// tests. The value carries path + proxy + verbatim fields.
func providerFixture(t *testing.T, raw string) *yaml.Node {
	t.Helper()
	root := mustParseYAMLNode(raw)
	m := findChildMapping(root, "rule-providers")
	if m == nil {
		t.Fatalf("fixture has no rule-providers mapping:\n%s", raw)
	}
	return m
}

// TC-U-RS-REWRITE-01 (US1 FR-002/FR-007): keys prefixed; non-built-in proxy
// prefixed, built-in proxy left alone; verbatim fields preserved.
func TestRS_RewriteSourceRuleProviders_KeysProxyVerbatim(t *testing.T) {
	rp := providerFixture(t, `rule-providers:
  Local-IP:
    type: http
    behavior: ipcidr
    format: mrs
    url: 'https://cdn.example.test/Local-IP.mrs'
    path: ./ruleset/Local-IP.mrs
    proxy: DIRECT
    interval: 86400
  Via-Group:
    type: http
    behavior: domain
    url: 'https://cdn.example.test/Via.mrs'
    path: ./ruleset/Via.mrs
    proxy: SomeGroup
    interval: 3600
`)
	out, skipped := RewriteSourceRuleProviders("alpha", rp)
	if len(skipped) != 0 {
		t.Fatalf("unexpected skipped: %+v", skipped)
	}
	keys := ruleProviderKeys(out)
	if !keys["alpha_Local-IP"] || !keys["alpha_Via-Group"] {
		t.Fatalf("keys not namespaced: %v", keys)
	}
	local := getMappingNode(out, "alpha_Local-IP")
	if got := getMappingField(local, "proxy"); got != "DIRECT" {
		t.Errorf("built-in proxy mutated: %q, want DIRECT", got)
	}
	if got := getMappingField(local, "behavior"); got != "ipcidr" {
		t.Errorf("verbatim field changed: behavior=%q, want ipcidr", got)
	}
	if got := getMappingField(local, "url"); got != "https://cdn.example.test/Local-IP.mrs" {
		t.Errorf("verbatim url changed: %q", got)
	}
	via := getMappingNode(out, "alpha_Via-Group")
	if got := getMappingField(via, "proxy"); got != "alpha_SomeGroup" {
		t.Errorf("non-built-in proxy not prefixed: %q, want alpha_SomeGroup", got)
	}
}

// TC-U-RS-PATH-01 (US2 FR-008): present path rewritten to a source-distinct
// path derived from the namespaced key; dir + extension preserved.
func TestRS_RewriteSourceRuleProviders_PathDistinct(t *testing.T) {
	rp := providerFixture(t, `rule-providers:
  Local-IP:
    type: http
    path: ./ruleset/Local-IP.mrs
  NoPath:
    type: http
`)
	out, _ := RewriteSourceRuleProviders("alpha", rp)
	if got := getMappingField(getMappingNode(out, "alpha_Local-IP"), "path"); got != "./ruleset/alpha_Local-IP.mrs" {
		t.Errorf("path = %q, want ./ruleset/alpha_Local-IP.mrs", got)
	}
	// Absent path stays absent (no injection).
	if got := getMappingField(getMappingNode(out, "alpha_NoPath"), "path"); got != "" {
		t.Errorf("path injected where absent: %q", got)
	}
}

// TC-U-RS-MALFORMED-01 (US3): a provider whose value is not a mapping is
// skipped and reported, not emitted.
func TestRS_RewriteSourceRuleProviders_MalformedSkipped(t *testing.T) {
	rp := providerFixture(t, `rule-providers:
  Good:
    type: http
    path: ./ruleset/Good.mrs
  Bad: "not-a-mapping"
`)
	out, skipped := RewriteSourceRuleProviders("alpha", rp)
	if len(skipped) != 1 || skipped[0].Provider != "Bad" {
		t.Fatalf("skipped = %+v, want one entry for Bad", skipped)
	}
	keys := ruleProviderKeys(out)
	if keys["alpha_Bad"] {
		t.Errorf("malformed provider emitted: %v", keys)
	}
	if !keys["alpha_Good"] {
		t.Errorf("good provider missing: %v", keys)
	}
}

func TestRS_RewriteSourceRuleProviders_NilInput(t *testing.T) {
	if out, _ := RewriteSourceRuleProviders("alpha", nil); out != nil {
		t.Errorf("nil input → %v, want nil", out)
	}
}

// TC-U-RS-REF-01 (US1): ReferencedRuleProviders collects field[1] of RULE-SET
// rules only.
func TestRS_ReferencedRuleProviders(t *testing.T) {
	ref := ReferencedRuleProviders([]string{
		"RULE-SET,alpha_Local-IP,DIRECT",
		"RULE-SET,alpha_China-Site,alpha_Group,no-resolve",
		"DOMAIN,a.test,alpha_Group",
		"MATCH,DIRECT",
	})
	if !ref["alpha_Local-IP"] || !ref["alpha_China-Site"] {
		t.Errorf("missing referenced providers: %v", ref)
	}
	if len(ref) != 2 {
		t.Errorf("ref = %v, want exactly 2 entries", ref)
	}
}

// TC-U-RS-MERGE-01 (US1 FR-005/FR-006/FR-010): merge keeps only referenced
// providers, in source order; nil when nothing referenced.
func TestRS_MergeRuleProviders_FilterAndNil(t *testing.T) {
	a, _ := RewriteSourceRuleProviders("alpha", providerFixture(t, `rule-providers:
  Local-IP: {type: http, path: ./ruleset/Local-IP.mrs}
  Unused: {type: http, path: ./ruleset/Unused.mrs}
`))
	merged := MergeRuleProviders([]*yaml.Node{a}, map[string]bool{"alpha_Local-IP": true})
	if merged == nil {
		t.Fatal("merged = nil, want one provider")
	}
	keys := ruleProviderKeys(merged)
	if !keys["alpha_Local-IP"] || keys["alpha_Unused"] || len(keys) != 1 {
		t.Errorf("merged keys = %v, want only alpha_Local-IP", keys)
	}
	// FR-006: nothing referenced → nil.
	if MergeRuleProviders([]*yaml.Node{a}, map[string]bool{}) != nil {
		t.Errorf("MergeRuleProviders with empty referenced set, want nil")
	}
}

// TC-U-RS-MERGE-02 (US2 FR-012): two sources defining the same bare name merge
// to two distinct keys with distinct paths, no collision.
func TestRS_MergeRuleProviders_CrossSourceNoCollision(t *testing.T) {
	a, _ := RewriteSourceRuleProviders("alpha", providerFixture(t, `rule-providers:
  Local-IP: {type: http, path: ./ruleset/Local-IP.mrs}
`))
	b, _ := RewriteSourceRuleProviders("beta", providerFixture(t, `rule-providers:
  Local-IP: {type: http, path: ./ruleset/Local-IP.mrs}
`))
	merged := MergeRuleProviders([]*yaml.Node{a, b}, map[string]bool{
		"alpha_Local-IP": true, "beta_Local-IP": true,
	})
	keys := ruleProviderKeys(merged)
	if !keys["alpha_Local-IP"] || !keys["beta_Local-IP"] || len(keys) != 2 {
		t.Fatalf("merged keys = %v, want both prefixed keys", keys)
	}
	pa := getMappingField(getMappingNode(merged, "alpha_Local-IP"), "path")
	pb := getMappingField(getMappingNode(merged, "beta_Local-IP"), "path")
	if pa == pb {
		t.Errorf("paths collide: %q == %q", pa, pb)
	}
}

// TC-U-RS-DROP-01 (US3 FR-009): RULE-SET rules with an undefined provider are
// dropped + reported; backed RULE-SET rules and non-RULE-SET rules survive.
func TestRS_DropUnbackedRuleSetRules(t *testing.T) {
	keys := map[string]bool{"alpha_Local-IP": true}
	kept, dropped := DropUnbackedRuleSetRules([]string{
		"RULE-SET,alpha_Local-IP,DIRECT", // backed → kept
		"RULE-SET,alpha_Missing,DIRECT",  // unbacked → dropped
		"DOMAIN,a.test,alpha_Group",      // not RULE-SET → kept
	}, keys)
	if len(kept) != 2 || kept[0] != "RULE-SET,alpha_Local-IP,DIRECT" || kept[1] != "DOMAIN,a.test,alpha_Group" {
		t.Errorf("kept = %v", kept)
	}
	if len(dropped) != 1 || dropped[0].Provider != "alpha_Missing" || dropped[0].Rule != "RULE-SET,alpha_Missing,DIRECT" {
		t.Errorf("dropped = %+v", dropped)
	}
}
