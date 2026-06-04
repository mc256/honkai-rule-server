package merge

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TC-U-NS-PROXY-01: proxy name rewritten with provider prefix.
func TestNS_PROXY_01_ProxyNameRewrite(t *testing.T) {
	proxies := []*yaml.Node{mustParseYAMLNode("name: Node1\ntype: trojan\nserver: a.test\nport: 443\npassword: pw\n")}
	groups := []*yaml.Node{}
	rules := []string{}

	newProxies, _, newRules := RewriteSource("alpha", proxies, groups, rules)

	if len(newProxies) != 1 {
		t.Fatalf("got %d proxies, want 1", len(newProxies))
	}
	name := getMappingField(newProxies[0], "name")
	if name != "alpha_Node1" {
		t.Errorf("proxy name = %q, want alpha_Node1", name)
	}
	if len(newRules) != 0 {
		t.Errorf("got rules %v, want empty", newRules)
	}
}

// TC-U-NS-PROXY-02: non-ASCII original name preserved in suffix.
func TestNS_PROXY_02_NonASCIIOriginal(t *testing.T) {
	proxies := []*yaml.Node{mustParseYAMLNode("name: 香港 01\ntype: trojan\nserver: a.test\nport: 443\npassword: pw\n")}
	groups := []*yaml.Node{}
	rules := []string{}

	newProxies, _, _ := RewriteSource("alpha", proxies, groups, rules)

	name := getMappingField(newProxies[0], "name")
	if name != "alpha_香港 01" {
		t.Errorf("proxy name = %q, want alpha_香港 01", name)
	}
}

// TC-U-NS-GROUP-01: select group with proxy members rewritten.
func TestNS_GROUP_01_SelectGroupMembersRewritten(t *testing.T) {
	groups := []*yaml.Node{mustParseYAMLNode("name: Auto\ntype: select\nproxies:\n  - Node1\n  - Node2\n")}
	proxies := []*yaml.Node{}

	newProxies, newGroups, _ := RewriteSource("alpha", proxies, groups, []string{})

	if len(newGroups) != 1 {
		t.Fatalf("got %d groups, want 1", len(newGroups))
	}
	name := getMappingField(newGroups[0], "name")
	if name != "alpha_Auto" {
		t.Errorf("group name = %q, want alpha_Auto", name)
	}
	members := mappingMembers(newGroups[0], "proxies")
	wantMembers := []string{"alpha_Node1", "alpha_Node2"}
	if len(members) != len(wantMembers) {
		t.Errorf("members = %v, want %v", members, wantMembers)
		return
	}
	for i, m := range members {
		if m != wantMembers[i] {
			t.Errorf("members[%d] = %q, want %q", i, m, wantMembers[i])
		}
	}
	_ = newProxies
}

// TC-U-NS-GROUP-02: built-in targets inside group member list untouched.
func TestNS_GROUP_02_BuiltinsUntouched(t *testing.T) {
	groups := []*yaml.Node{mustParseYAMLNode("name: Auto\ntype: select\nproxies:\n  - Node1\n  - DIRECT\n  - REJECT\n")}

	_, newGroups, _ := RewriteSource("alpha", nil, groups, []string{})

	members := mappingMembers(newGroups[0], "proxies")
	wantMembers := []string{"alpha_Node1", "DIRECT", "REJECT"}
	for i, m := range members {
		if m != wantMembers[i] {
			t.Errorf("members[%d] = %q, want %q", i, m, wantMembers[i])
		}
	}
}

// TC-U-NS-GROUP-03: relay group member list rewritten.
func TestNS_GROUP_03_RelayGroupMembersRewritten(t *testing.T) {
	groups := []*yaml.Node{mustParseYAMLNode("name: Chain\ntype: relay\nproxies:\n  - NodeA\n  - NodeB\n")}

	_, newGroups, _ := RewriteSource("alpha", nil, groups, []string{})

	name := getMappingField(newGroups[0], "name")
	if name != "alpha_Chain" {
		t.Errorf("group name = %q, want alpha_Chain", name)
	}
	members := mappingMembers(newGroups[0], "proxies")
	wantMembers := []string{"alpha_NodeA", "alpha_NodeB"}
	for i, m := range members {
		if m != wantMembers[i] {
			t.Errorf("members[%d] = %q, want %q", i, m, wantMembers[i])
		}
	}
}

// TC-U-NS-RULE-01: rule target rewritten.
func TestNS_RULE_01_TargetRewritten(t *testing.T) {
	rules := []string{"DOMAIN,a.test,Auto"}

	_, _, newRules := RewriteSource("alpha", nil, nil, rules)

	if len(newRules) != 1 {
		t.Fatalf("got %d rules, want 1", len(newRules))
	}
	if newRules[0] != "DOMAIN,a.test,alpha_Auto" {
		t.Errorf("rule = %q, want DOMAIN,a.test,alpha_Auto", newRules[0])
	}
}

// TC-U-NS-RULE-02: built-in target untouched.
func TestNS_RULE_02_BuiltinTargetUntouched(t *testing.T) {
	rules := []string{"DOMAIN,a.test,DIRECT"}

	_, _, newRules := RewriteSource("alpha", nil, nil, rules)

	if newRules[0] != "DOMAIN,a.test,DIRECT" {
		t.Errorf("rule = %q, want DOMAIN,a.test,DIRECT", newRules[0])
	}
}

// TC-U-NS-RULE-03: MATCH with REJECT-DROP untouched.
func TestNS_RULE_03_MatchRejectDropUntouched(t *testing.T) {
	rules := []string{"MATCH,REJECT-DROP"}

	_, _, newRules := RewriteSource("alpha", nil, nil, rules)

	if newRules[0] != "MATCH,REJECT-DROP" {
		t.Errorf("rule = %q, want MATCH,REJECT-DROP", newRules[0])
	}
}

// TC-U-NS-RULE-04: rule with modifier, target rewritten.
func TestNS_RULE_04_ModifierPreserved(t *testing.T) {
	rules := []string{"DOMAIN-SUFFIX,foo,Auto,no-resolve"}

	_, _, newRules := RewriteSource("alpha", nil, nil, rules)

	if newRules[0] != "DOMAIN-SUFFIX,foo,alpha_Auto,no-resolve" {
		t.Errorf("rule = %q, want DOMAIN-SUFFIX,foo,alpha_Auto,no-resolve", newRules[0])
	}
}

// TC-U-NS-RULE-05: rule with comma in matcher value parses correctly.
func TestNS_RULE_05_CommaInMatcherValue(t *testing.T) {
	rules := []string{"IP-CIDR,10.0.0.0/8,Proxy"}

	_, _, newRules := RewriteSource("alpha", nil, nil, rules)

	if newRules[0] != "IP-CIDR,10.0.0.0/8,alpha_Proxy" {
		t.Errorf("rule = %q, want IP-CIDR,10.0.0.0/8,alpha_Proxy", newRules[0])
	}
}

// TC-U-NS-RULESET-01: RULE-SET with a built-in target — provider field[1]
// prefixed, built-in target left unchanged (016 FR-003/FR-004).
func TestNS_RULESET_01_BuiltinTarget(t *testing.T) {
	_, _, newRules := RewriteSource("alpha", nil, nil, []string{"RULE-SET,Local-IP,DIRECT"})
	if newRules[0] != "RULE-SET,alpha_Local-IP,DIRECT" {
		t.Errorf("rule = %q, want RULE-SET,alpha_Local-IP,DIRECT", newRules[0])
	}
}

// TC-U-NS-RULESET-02: RULE-SET with a non-built-in proxy-group target AND a
// modifier (the real-upstream shape) — both field[1] and the group target are
// prefixed; the modifier is preserved in place (016 FR-003/FR-004).
func TestNS_RULESET_02_GroupTargetWithModifier(t *testing.T) {
	_, _, newRules := RewriteSource("alpha", nil, nil, []string{"RULE-SET,Local-IP,SomeGroup,no-resolve"})
	if newRules[0] != "RULE-SET,alpha_Local-IP,alpha_SomeGroup,no-resolve" {
		t.Errorf("rule = %q, want RULE-SET,alpha_Local-IP,alpha_SomeGroup,no-resolve", newRules[0])
	}
}

// TC-U-NS-RULESET-03: RULE-SET with a non-built-in target, no modifier — both
// the provider and the group target are prefixed.
func TestNS_RULESET_03_GroupTargetNoModifier(t *testing.T) {
	_, _, newRules := RewriteSource("alpha", nil, nil, []string{"RULE-SET,China-Site,SomeGroup"})
	if newRules[0] != "RULE-SET,alpha_China-Site,alpha_SomeGroup" {
		t.Errorf("rule = %q, want RULE-SET,alpha_China-Site,alpha_SomeGroup", newRules[0])
	}
}

// TC-U-NS-RULESET-04 (I1 guard): a malformed 2-field RULE-SET (no target) has
// its provider field prefixed exactly once and is never double-prefixed.
func TestNS_RULESET_04_TwoFieldGuard(t *testing.T) {
	_, _, newRules := RewriteSource("alpha", nil, nil, []string{"RULE-SET,Local-IP"})
	if newRules[0] != "RULE-SET,alpha_Local-IP" {
		t.Errorf("rule = %q, want RULE-SET,alpha_Local-IP (single prefix)", newRules[0])
	}
}

// TC-U-NS-IDEMPOTENT-01: applying rewriter twice is NOT idempotent.
func TestNS_IDEMPOTENT_01_NotIdempotent(t *testing.T) {
	proxies := []*yaml.Node{mustParseYAMLNode("name: Node1\ntype: trojan\nserver: a.test\nport: 443\npassword: pw\n")}

	// First pass
	p1, _, _ := RewriteSource("alpha", proxies, nil, nil)
	// Second pass on already-rewritten proxies
	p2, _, _ := RewriteSource("alpha", p1, nil, nil)

	name := getMappingField(p2[0], "name")
	if name != "alpha_alpha_Node1" {
		t.Errorf("second-pass name = %q, want alpha_alpha_Node1 (NOT idempotent)", name)
	}
}

// TC-U-NS-OWN-PROXY-01: own-proxy gets leading underscore prefix.
func TestNS_OWN_PROXY_01_UnderscorePrefix(t *testing.T) {
	proxies := []*yaml.Node{mustParseYAMLNode("name: my-server\ntype: trojan\nserver: a.test\nport: 443\npassword: pw\n")}

	newProxies, _ := RewriteOwn(proxies, nil)

	if len(newProxies) != 1 {
		t.Fatalf("got %d proxies, want 1", len(newProxies))
	}
	name := getMappingField(newProxies[0], "name")
	if name != "_my-server" {
		t.Errorf("own-proxy name = %q, want _my-server", name)
	}
}

// TC-U-NS-OWN-PROXY-02: own-proxy name already starting with underscore gets double underscore.
func TestNS_OWN_PROXY_02_AlreadyHasUnderscore(t *testing.T) {
	proxies := []*yaml.Node{mustParseYAMLNode("name: _legacy\ntype: trojan\nserver: a.test\nport: 443\npassword: pw\n")}

	newProxies, _ := RewriteOwn(proxies, nil)

	name := getMappingField(newProxies[0], "name")
	if name != "__legacy" {
		t.Errorf("own-proxy name = %q, want __legacy", name)
	}
}

// TC-U-NS-OWN-GROUP-01: own-group renamed and member refs rewritten.
func TestNS_OWN_GROUP_01_GroupRenameAndMembers(t *testing.T) {
	ownProxies := []*yaml.Node{mustParseYAMLNode("name: my-server\ntype: trojan\nserver: a.test\nport: 443\npassword: pw\n")}
	ownGroups := []*yaml.Node{mustParseYAMLNode("name: my-pool\ntype: select\nproxies:\n  - my-server\n  - DIRECT\n")}

	newProxies, newGroups := RewriteOwn(ownProxies, ownGroups)

	// Check proxy renamed
	proxyName := getMappingField(newProxies[0], "name")
	if proxyName != "_my-server" {
		t.Errorf("own-proxy name = %q, want _my-server", proxyName)
	}

	// Check group renamed
	groupName := getMappingField(newGroups[0], "name")
	if groupName != "_my-pool" {
		t.Errorf("own-group name = %q, want _my-pool", groupName)
	}

	// Check member refs rewritten
	members := mappingMembers(newGroups[0], "proxies")
	wantMembers := []string{"_my-server", "DIRECT"}
	for i, m := range members {
		if m != wantMembers[i] {
			t.Errorf("members[%d] = %q, want %q", i, m, wantMembers[i])
		}
	}
}

// TC-U-NS-OWN-GROUP-02: own-group referencing another own-group.
func TestNS_OWN_GROUP_02_CrossOwnGroupReference(t *testing.T) {
	ownProxies := []*yaml.Node{}
	ownGroups := []*yaml.Node{
		mustParseYAMLNode("name: pool-a\ntype: select\nproxies:\n  - pool-b\n"),
		mustParseYAMLNode("name: pool-b\ntype: select\nproxies:\n  - DIRECT\n"),
	}

	_, newGroups := RewriteOwn(ownProxies, ownGroups)

	// Find pool-a
	var poolA *yaml.Node
	for _, g := range newGroups {
		if getMappingField(g, "name") == "_pool-a" {
			poolA = g
			break
		}
	}
	if poolA == nil {
		t.Fatal("could not find _pool-a in output")
	}

	members := mappingMembers(poolA, "proxies")
	if len(members) != 1 || members[0] != "_pool-b" {
		t.Errorf("pool-a members = %v, want [_pool-b]", members)
	}
}
