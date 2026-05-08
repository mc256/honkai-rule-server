package merge

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func group(t *testing.T, raw string) *yaml.Node {
	t.Helper()
	return mustParseYAMLNode(raw)
}

// TC-U-MERGE-GROUP-01: same-name `select` groups across upstreams → single
// group with member-list union, deduped, order from highest-priority source
// first then appended.
func TestMERGE_GROUP_01_SameNameUnion(t *testing.T) {
	per := map[string][]*yaml.Node{
		"beta": {group(t, "name: Auto\ntype: select\nproxies:\n  - A\n  - B\n")},
		"alpha":      {group(t, "name: Auto\ntype: select\nproxies:\n  - B\n  - C\n")},
	}
	merged, conflicts := MergeProxyGroups(per, []string{"beta", "alpha"}, nil)

	if len(merged) != 1 {
		t.Fatalf("got %d groups, want 1 (same-name unioned)", len(merged))
	}
	members := mappingMembers(merged[0], "proxies")
	want := []string{"A", "B", "C"}
	if len(members) != len(want) {
		t.Fatalf("members = %v, want %v", members, want)
	}
	for i, m := range members {
		if m != want[i] {
			t.Errorf("members[%d] = %q, want %q", i, m, want[i])
		}
	}
	if len(conflicts) != 0 {
		t.Errorf("conflicts = %v, want []", conflicts)
	}
}

// TC-U-MERGE-GROUP-02: type conflict (select vs url-test) → highest-priority
// source wins; conflict recorded.
func TestMERGE_GROUP_02_TypeConflict(t *testing.T) {
	per := map[string][]*yaml.Node{
		"beta": {group(t, "name: Auto\ntype: select\nproxies:\n  - A\n")},
		"alpha":      {group(t, "name: Auto\ntype: url-test\nproxies:\n  - B\nurl: https://example.test/204\ninterval: 300\n")},
	}
	merged, conflicts := MergeProxyGroups(per, []string{"beta", "alpha"}, nil)

	if len(merged) != 1 {
		t.Fatalf("got %d groups, want 1", len(merged))
	}
	if got := getMappingField(merged[0], "type"); got != "select" {
		t.Errorf("type = %q, want select (beta priority 2000 wins)", got)
	}
	if len(conflicts) == 0 {
		t.Fatal("expected at least one conflict")
	}

	var typeConflict *GroupConflict
	for i := range conflicts {
		if conflicts[i].Attribute == "type" {
			typeConflict = &conflicts[i]
			break
		}
	}
	if typeConflict == nil {
		t.Fatalf("no type conflict in %v", conflicts)
	}
	if typeConflict.Chosen != "beta" {
		t.Errorf("Chosen = %q, want beta", typeConflict.Chosen)
	}
	if len(typeConflict.Values) != 2 {
		t.Fatalf("Values = %v, want 2 entries", typeConflict.Values)
	}
	if typeConflict.Values[0].Source != "beta" || typeConflict.Values[0].Value != "select" {
		t.Errorf("Values[0] = %+v, want {beta select}", typeConflict.Values[0])
	}
	if typeConflict.Values[1].Source != "alpha" || typeConflict.Values[1].Value != "url-test" {
		t.Errorf("Values[1] = %+v, want {alpha url-test}", typeConflict.Values[1])
	}
}

// Own-group with same name as an upstream group → own's attributes win,
// members union. Confirms own-precedence parallels MergeProxies' FR-008.
func TestMergeProxyGroups_OwnGroupPrecedence(t *testing.T) {
	per := map[string][]*yaml.Node{
		"alpha": {group(t, "name: My-Own\ntype: url-test\nproxies:\n  - upstream-a\n")},
	}
	own := []*yaml.Node{group(t, "name: My-Own\ntype: select\nproxies:\n  - my-home-trojan\n")}

	merged, conflicts := MergeProxyGroups(per, []string{"alpha"}, own)

	if len(merged) != 1 {
		t.Fatalf("got %d groups, want 1", len(merged))
	}
	if got := getMappingField(merged[0], "type"); got != "select" {
		t.Errorf("type = %q, want select (own takes precedence)", got)
	}
	members := mappingMembers(merged[0], "proxies")
	wantContains := []string{"my-home-trojan", "upstream-a"}
	for _, w := range wantContains {
		found := false
		for _, m := range members {
			if m == w {
				found = true
			}
		}
		if !found {
			t.Errorf("members = %v, want includes %q", members, w)
		}
	}
	if len(conflicts) == 0 {
		t.Errorf("expected type conflict to be logged (own=select vs alpha=url-test)")
	}
	if conflicts[0].Chosen != "own" {
		t.Errorf("Chosen = %q, want own", conflicts[0].Chosen)
	}
}

// Different-named groups across upstreams → both appear, no conflict.
func TestMergeProxyGroups_DistinctNamesAllAppear(t *testing.T) {
	per := map[string][]*yaml.Node{
		"beta": {group(t, "name: Auto\ntype: select\nproxies:\n  - A\n")},
		"alpha":      {group(t, "name: Manual\ntype: select\nproxies:\n  - B\n")},
	}
	merged, conflicts := MergeProxyGroups(per, []string{"beta", "alpha"}, nil)
	if len(merged) != 2 {
		t.Fatalf("got %d groups, want 2", len(merged))
	}
	if len(conflicts) != 0 {
		t.Errorf("conflicts = %v, want []", conflicts)
	}
}

// TC-U-MERGE-GROUP-03: AppendProxiesGroup adds a select-type group with
// every merged proxy name. If a group with the same name already exists,
// its member list is augmented (not duplicated).
func TestMERGE_GROUP_03_AlwaysPresentProxiesGroup(t *testing.T) {
	t.Run("appends fresh when absent", func(t *testing.T) {
		merged := []*yaml.Node{
			group(t, "name: Auto\ntype: url-test\nproxies: [A, B]\n"),
		}
		out := AppendProxiesGroup(merged, []string{"A", "B", "C"}, "Proxies")
		if len(out) != 2 {
			t.Fatalf("got %d groups, want 2 (Auto + appended Proxies)", len(out))
		}
		last := out[len(out)-1]
		if getMappingField(last, "name") != "Proxies" {
			t.Errorf("appended group name = %q, want Proxies", getMappingField(last, "name"))
		}
		if getMappingField(last, "type") != "select" {
			t.Errorf("type = %q, want select", getMappingField(last, "type"))
		}
		members := mappingMembers(last, "proxies")
		if strings.Join(members, ",") != "A,B,C" {
			t.Errorf("members = %v, want [A B C]", members)
		}
	})

	t.Run("augments existing same-named group", func(t *testing.T) {
		merged := []*yaml.Node{
			group(t, "name: Proxies\ntype: select\nproxies: [A]\n"),
			group(t, "name: Other\ntype: select\nproxies: [Z]\n"),
		}
		out := AppendProxiesGroup(merged, []string{"A", "B", "C"}, "Proxies")
		if len(out) != 2 {
			t.Errorf("got %d groups, want 2 (no duplicate)", len(out))
		}
		members := mappingMembers(out[0], "proxies")
		if strings.Join(members, ",") != "A,B,C" {
			t.Errorf("augmented members = %v, want [A B C]", members)
		}
	})

	t.Run("custom group name", func(t *testing.T) {
		out := AppendProxiesGroup(nil, []string{"X"}, "All-Nodes")
		if len(out) != 1 || getMappingField(out[0], "name") != "All-Nodes" {
			t.Errorf("got %v, want one group named All-Nodes", out)
		}
	})

	t.Run("empty proxyNames still appends an empty group", func(t *testing.T) {
		out := AppendProxiesGroup(nil, nil, "Proxies")
		if len(out) != 1 {
			t.Fatalf("got %d, want 1 (always-present even when no proxies)", len(out))
		}
	})
}

// Members deduplicate across collisions; order: existing first, then new.
func TestMergeProxyGroups_MemberDedupOrdered(t *testing.T) {
	per := map[string][]*yaml.Node{
		"src1": {group(t, "name: G\ntype: select\nproxies: [a, b]\n")},
		"src2": {group(t, "name: G\ntype: select\nproxies: [b, c, a]\n")},
	}
	merged, _ := MergeProxyGroups(per, []string{"src1", "src2"}, nil)
	got := strings.Join(mappingMembers(merged[0], "proxies"), ",")
	if got != "a,b,c" {
		t.Errorf("members = %q, want %q", got, "a,b,c")
	}
}
