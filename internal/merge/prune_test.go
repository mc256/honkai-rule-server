package merge

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// findGroup returns the first group in groups whose `name` equals name, or nil.
func findGroup(groups []*yaml.Node, name string) *yaml.Node {
	for _, g := range groups {
		if getMappingField(g, "name") == name {
			return g
		}
	}
	return nil
}

// groupNames returns the `name` of every group, in slice order.
func groupNames(groups []*yaml.Node) []string {
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		out = append(out, getMappingField(g, "name"))
	}
	return out
}

// --- T004 (Foundational): FR-010 byte-stability guard ---

// TestPruneEmptyProxyGroups_NoEmptyInput: when every group has at least one
// member, groups and rules are returned unchanged and no prune events occur.
func TestPruneEmptyProxyGroups_NoEmptyInput(t *testing.T) {
	groups := []*yaml.Node{
		group(t, "name: Proxies\ntype: select\nproxies:\n  - A\n  - _region_CA\n"),
		group(t, "name: _region_CA\ntype: url-test\nproxies:\n  - A\n"),
	}
	rules := []string{"DOMAIN,a.test,_region_CA", "MATCH,auto"}

	prunedGroups, prunedRules, result := PruneEmptyProxyGroups(groups, rules, "Proxies", "auto")

	if len(prunedGroups) != len(groups) {
		t.Errorf("groups count = %d, want %d (no removal)", len(prunedGroups), len(groups))
	}
	if len(prunedRules) != len(rules) {
		t.Errorf("rules count = %d, want %d", len(prunedRules), len(rules))
	}
	for i := range rules {
		if prunedRules[i] != rules[i] {
			t.Errorf("rule[%d] = %q, want %q (unchanged)", i, prunedRules[i], rules[i])
		}
	}
	if len(result.RemovedGroups) != 0 || len(result.Retargets) != 0 {
		t.Errorf("result = %+v, want empty", result)
	}
}

// --- T005 (US1): empty-group removal ---

// TestPrune_RemovesEmptyGroup: a non-protected group with `proxies: []` is
// removed (FR-001/FR-002).
func TestPrune_RemovesEmptyGroup(t *testing.T) {
	groups := []*yaml.Node{
		group(t, "name: Proxies\ntype: select\nproxies:\n  - A\n"),
		group(t, "name: _Empty\ntype: select\nproxies: []\n"),
		group(t, "name: _Full\ntype: select\nproxies:\n  - A\n"),
	}
	prunedGroups, _, result := PruneEmptyProxyGroups(groups, nil, "Proxies", "auto")

	if findGroup(prunedGroups, "_Empty") != nil {
		t.Errorf("_Empty was not removed; groups = %v", groupNames(prunedGroups))
	}
	if findGroup(prunedGroups, "_Full") == nil {
		t.Errorf("_Full was wrongly removed; groups = %v", groupNames(prunedGroups))
	}
	if len(result.RemovedGroups) != 1 || result.RemovedGroups[0] != "_Empty" {
		t.Errorf("RemovedGroups = %v, want [_Empty]", result.RemovedGroups)
	}
}

// TestPrune_RemovesEmptyGroup_AbsentProxiesKey: a group with no `proxies:` key
// at all counts as empty (FR-002).
func TestPrune_RemovesEmptyGroup_AbsentProxiesKey(t *testing.T) {
	groups := []*yaml.Node{
		group(t, "name: Proxies\ntype: select\nproxies:\n  - A\n"),
		group(t, "name: _NoKey\ntype: select\n"),
	}
	prunedGroups, _, _ := PruneEmptyProxyGroups(groups, nil, "Proxies", "auto")
	if findGroup(prunedGroups, "_NoKey") != nil {
		t.Errorf("_NoKey (absent proxies key) was not removed")
	}
}

// TestPrune_KeepsProxiesSelectorWhenEmpty: the always-present Proxies selector
// is exempt from removal even when empty (FR-007).
func TestPrune_KeepsProxiesSelectorWhenEmpty(t *testing.T) {
	groups := []*yaml.Node{
		group(t, "name: Proxies\ntype: select\nproxies: []\n"),
		group(t, "name: _Full\ntype: select\nproxies:\n  - A\n"),
	}
	prunedGroups, _, result := PruneEmptyProxyGroups(groups, nil, "Proxies", "auto")
	if findGroup(prunedGroups, "Proxies") == nil {
		t.Errorf("empty Proxies selector was wrongly removed")
	}
	for _, n := range result.RemovedGroups {
		if n == "Proxies" {
			t.Errorf("Proxies appears in RemovedGroups")
		}
	}
}

// TestPrune_KeepsFallbackTargetGroupWhenEmpty: when the fallback rule target
// names a proxy-group, that group is protected so FR-008 retargets always
// land on a present group.
func TestPrune_KeepsFallbackTargetGroupWhenEmpty(t *testing.T) {
	groups := []*yaml.Node{
		group(t, "name: Proxies\ntype: select\nproxies:\n  - A\n"),
		group(t, "name: auto\ntype: url-test\nproxies: []\n"),
	}
	prunedGroups, _, _ := PruneEmptyProxyGroups(groups, nil, "Proxies", "auto")
	if findGroup(prunedGroups, "auto") == nil {
		t.Errorf("fallback-target group \"auto\" was wrongly removed")
	}
}

// TestPrune_PreservesOrderAndAttributes: surviving groups keep original order
// and every attribute (FR-009).
func TestPrune_PreservesOrderAndAttributes(t *testing.T) {
	groups := []*yaml.Node{
		group(t, "name: Proxies\ntype: select\nproxies:\n  - A\n"),
		group(t, "name: _Empty\ntype: select\nproxies: []\n"),
		group(t, "name: _region_CA\ntype: url-test\nproxies:\n  - A\nurl: https://x.test/204\ninterval: 300\n"),
	}
	prunedGroups, _, _ := PruneEmptyProxyGroups(groups, nil, "Proxies", "auto")

	want := []string{"Proxies", "_region_CA"}
	got := groupNames(prunedGroups)
	if len(got) != len(want) {
		t.Fatalf("groups = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("groups[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	ca := findGroup(prunedGroups, "_region_CA")
	if getMappingField(ca, "type") != "url-test" {
		t.Errorf("_region_CA type changed: %q", getMappingField(ca, "type"))
	}
	if getMappingField(ca, "url") != "https://x.test/204" {
		t.Errorf("_region_CA url changed: %q", getMappingField(ca, "url"))
	}
	if getMappingField(ca, "interval") != "300" {
		t.Errorf("_region_CA interval changed: %q", getMappingField(ca, "interval"))
	}
}

// TestPrune_AllNonProtectedEmpty: when every non-protected group is empty they
// are all removed and only the Proxies selector remains.
func TestPrune_AllNonProtectedEmpty(t *testing.T) {
	groups := []*yaml.Node{
		group(t, "name: Proxies\ntype: select\nproxies: []\n"),
		group(t, "name: _A\ntype: select\nproxies: []\n"),
		group(t, "name: _B\ntype: select\nproxies: []\n"),
	}
	prunedGroups, _, result := PruneEmptyProxyGroups(groups, nil, "Proxies", "auto")
	if got := groupNames(prunedGroups); len(got) != 1 || got[0] != "Proxies" {
		t.Errorf("groups = %v, want [Proxies]", got)
	}
	if len(result.RemovedGroups) != 2 {
		t.Errorf("RemovedGroups = %v, want 2 entries", result.RemovedGroups)
	}
}

// --- T010 (US2): dangling member-reference cleanup ---

// TestPrune_DropsDanglingMemberReference: a surviving group that listed a
// removed group no longer lists it (FR-006).
func TestPrune_DropsDanglingMemberReference(t *testing.T) {
	groups := []*yaml.Node{
		group(t, "name: Proxies\ntype: select\nproxies:\n  - A\n"),
		group(t, "name: _Empty\ntype: select\nproxies: []\n"),
		group(t, "name: _D\ntype: select\nproxies:\n  - A\n  - _Empty\n  - B\n"),
	}
	prunedGroups, _, _ := PruneEmptyProxyGroups(groups, nil, "Proxies", "auto")

	d := findGroup(prunedGroups, "_D")
	if d == nil {
		t.Fatalf("_D was wrongly removed")
	}
	members := mappingMembers(d, "proxies")
	for _, m := range members {
		if m == "_Empty" {
			t.Errorf("_D still lists removed group _Empty: %v", members)
		}
	}
	if len(members) != 2 || members[0] != "A" || members[1] != "B" {
		t.Errorf("_D members = %v, want [A B]", members)
	}
}

// TestPrune_DropsDanglingRefFromProxiesSelector: the protected Proxies
// selector also has references to removed groups dropped (FR-006).
func TestPrune_DropsDanglingRefFromProxiesSelector(t *testing.T) {
	groups := []*yaml.Node{
		group(t, "name: Proxies\ntype: select\nproxies:\n  - A\n  - _Empty\n"),
		group(t, "name: _Empty\ntype: select\nproxies: []\n"),
	}
	prunedGroups, _, _ := PruneEmptyProxyGroups(groups, nil, "Proxies", "auto")
	members := mappingMembers(findGroup(prunedGroups, "Proxies"), "proxies")
	for _, m := range members {
		if m == "_Empty" {
			t.Errorf("Proxies still lists removed group _Empty: %v", members)
		}
	}
}

// TestPrune_KeepsLiveMemberReferences: references to still-present groups and
// to proxies are left untouched.
func TestPrune_KeepsLiveMemberReferences(t *testing.T) {
	groups := []*yaml.Node{
		group(t, "name: Proxies\ntype: select\nproxies:\n  - A\n  - _Full\n"),
		group(t, "name: _Full\ntype: select\nproxies:\n  - A\n"),
		group(t, "name: _Empty\ntype: select\nproxies: []\n"),
	}
	prunedGroups, _, _ := PruneEmptyProxyGroups(groups, nil, "Proxies", "auto")
	members := mappingMembers(findGroup(prunedGroups, "Proxies"), "proxies")
	if len(members) != 2 || members[0] != "A" || members[1] != "_Full" {
		t.Errorf("Proxies members = %v, want [A _Full]", members)
	}
}

// TestPrune_NoCascade: a group emptied solely because the groups it referenced
// were removed is NOT itself removed — single pass, no cascade (FR-005).
func TestPrune_NoCascade(t *testing.T) {
	groups := []*yaml.Node{
		group(t, "name: Proxies\ntype: select\nproxies:\n  - A\n"),
		group(t, "name: _Empty\ntype: select\nproxies: []\n"),
		group(t, "name: _OnlyRefsEmpty\ntype: select\nproxies:\n  - _Empty\n"),
	}
	prunedGroups, _, result := PruneEmptyProxyGroups(groups, nil, "Proxies", "auto")
	if findGroup(prunedGroups, "_OnlyRefsEmpty") == nil {
		t.Errorf("_OnlyRefsEmpty was removed — cascading removal is out of scope (FR-005)")
	}
	if len(result.RemovedGroups) != 1 || result.RemovedGroups[0] != "_Empty" {
		t.Errorf("RemovedGroups = %v, want [_Empty] only", result.RemovedGroups)
	}
}

// --- T012 (US3): rule-target extraction ---

func TestRuleTarget(t *testing.T) {
	cases := []struct {
		rule string
		want string
	}{
		{"MATCH,auto", "auto"},
		{"DOMAIN-SUFFIX,a.com,_region_CA", "_region_CA"},
		{"IP-CIDR,10.0.0.0/8,DIRECT,no-resolve", "DIRECT"},
		{"RULE-SET,my-set,_continent_EU", "_continent_EU"},
		{"AND,((DOMAIN,d.com),(NETWORK,tcp)),Proxies", "Proxies"},
	}
	for _, c := range cases {
		got, start, end, ok := ruleTarget(c.rule)
		if !ok {
			t.Errorf("ruleTarget(%q): ok = false", c.rule)
			continue
		}
		if got != c.want {
			t.Errorf("ruleTarget(%q) = %q, want %q", c.rule, got, c.want)
		}
		if c.rule[start:end] != c.want {
			t.Errorf("ruleTarget(%q) range [%d:%d] = %q, want %q",
				c.rule, start, end, c.rule[start:end], c.want)
		}
	}
}

// --- T013 (US3): rule retargeting ---

// TestPrune_RetargetsRuleToFallback: a rule whose target group was removed is
// redirected to the fallback rule target (FR-008).
func TestPrune_RetargetsRuleToFallback(t *testing.T) {
	groups := []*yaml.Node{
		group(t, "name: Proxies\ntype: select\nproxies:\n  - A\n"),
		group(t, "name: _Empty\ntype: select\nproxies: []\n"),
	}
	rules := []string{
		"DOMAIN-SUFFIX,a.com,_Empty",
		"IP-CIDR,10.0.0.0/8,_Empty,no-resolve",
		"MATCH,auto",
	}
	_, prunedRules, result := PruneEmptyProxyGroups(groups, rules, "Proxies", "auto")

	if prunedRules[0] != "DOMAIN-SUFFIX,a.com,auto" {
		t.Errorf("rule[0] = %q, want DOMAIN-SUFFIX,a.com,auto", prunedRules[0])
	}
	if prunedRules[1] != "IP-CIDR,10.0.0.0/8,auto,no-resolve" {
		t.Errorf("rule[1] = %q, want IP-CIDR,10.0.0.0/8,auto,no-resolve", prunedRules[1])
	}
	if prunedRules[2] != "MATCH,auto" {
		t.Errorf("rule[2] = %q, want MATCH,auto (unchanged)", prunedRules[2])
	}
	if len(result.Retargets) != 2 {
		t.Fatalf("Retargets = %+v, want 2 entries", result.Retargets)
	}
	for _, rt := range result.Retargets {
		if rt.OldTarget != "_Empty" || rt.NewTarget != "auto" {
			t.Errorf("retarget = %+v, want Old=_Empty New=auto", rt)
		}
	}
}

// TestPrune_LeavesLiveRuleTargetUntouched: a rule targeting a surviving group
// or a proxy is not rewritten.
func TestPrune_LeavesLiveRuleTargetUntouched(t *testing.T) {
	groups := []*yaml.Node{
		group(t, "name: Proxies\ntype: select\nproxies:\n  - A\n"),
		group(t, "name: _Empty\ntype: select\nproxies: []\n"),
	}
	rules := []string{"DOMAIN,keep.com,Proxies", "DOMAIN,direct.com,DIRECT"}
	_, prunedRules, result := PruneEmptyProxyGroups(groups, rules, "Proxies", "auto")
	if prunedRules[0] != "DOMAIN,keep.com,Proxies" || prunedRules[1] != "DOMAIN,direct.com,DIRECT" {
		t.Errorf("live rule targets were rewritten: %v", prunedRules)
	}
	if len(result.Retargets) != 0 {
		t.Errorf("Retargets = %+v, want none", result.Retargets)
	}
}

// TestPrune_RetargetPreservesRuleCount: retargeting rewrites in place and never
// drops a rule, so the rule slice length is unchanged.
func TestPrune_RetargetPreservesRuleCount(t *testing.T) {
	groups := []*yaml.Node{
		group(t, "name: Proxies\ntype: select\nproxies:\n  - A\n"),
		group(t, "name: _Empty\ntype: select\nproxies: []\n"),
	}
	rules := []string{"DOMAIN,a.com,_Empty", "DOMAIN,b.com,Proxies", "MATCH,auto"}
	_, prunedRules, _ := PruneEmptyProxyGroups(groups, rules, "Proxies", "auto")
	if len(prunedRules) != len(rules) {
		t.Errorf("rule count = %d, want %d", len(prunedRules), len(rules))
	}
}
