package merge

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// proxy is a small builder for test mapping nodes.
func proxy(t *testing.T, name string) *yaml.Node {
	t.Helper()
	return mustParseYAMLNode("name: " + name + "\ntype: trojan\nserver: " + name + ".example.test\nport: 443\npassword: pw\n")
}

// TC-U-MERGE-PROXY-01: two upstreams with same-named proxy → output has both
// with `<name>@<source>` suffix; collision recorded.
func TestMERGE_PROXY_01_UpstreamCollision(t *testing.T) {
	per := map[string][]*yaml.Node{
		"beta": {proxy(t, "auto"), proxy(t, "us-1")},
		"alpha":      {proxy(t, "auto"), proxy(t, "jp-1")},
	}
	// beta priority 2000, alpha priority 1000 → sorted desc
	sorted := []string{"beta", "alpha"}

	merged, collisions := MergeProxies(per, sorted, nil)

	if len(merged) != 4 {
		t.Fatalf("got %d proxies, want 4", len(merged))
	}

	names := make([]string, 0, len(merged))
	for _, m := range merged {
		names = append(names, getMappingField(m, "name"))
	}
	want := map[string]bool{
		"auto":         true, // first claim by beta keeps the name
		"us-1":         true,
		"auto@alpha":   true, // alpha's collides → suffixed
		"jp-1":         true,
	}
	for _, n := range names {
		if !want[n] {
			t.Errorf("unexpected proxy name %q in merged output (got %v)", n, names)
		}
		delete(want, n)
	}
	for n := range want {
		t.Errorf("missing expected proxy name %q in merged output (got %v)", n, names)
	}

	if len(collisions) != 1 {
		t.Fatalf("got %d collisions, want 1", len(collisions))
	}
	c := collisions[0]
	if c.ProxyName != "auto" || c.Resolution != "auto@alpha" {
		t.Errorf("collision = %+v, want {ProxyName:auto Resolution:auto@alpha}", c)
	}
	if len(c.Sources) != 2 || c.Sources[0] != "beta" || c.Sources[1] != "alpha" {
		t.Errorf("collision Sources = %v, want [beta alpha]", c.Sources)
	}
}

// TC-U-MERGE-PROXY-02: own-proxy named identically to an upstream proxy →
// upstream gets the @source suffix; own-proxy keeps its name.
func TestMERGE_PROXY_02_OwnProxyPrecedence(t *testing.T) {
	per := map[string][]*yaml.Node{
		"alpha": {proxy(t, "my-home-server"), proxy(t, "us-1")},
	}
	own := []*yaml.Node{proxy(t, "my-home-server")}

	merged, collisions := MergeProxies(per, []string{"alpha"}, own)

	if len(merged) != 3 {
		t.Fatalf("got %d proxies, want 3", len(merged))
	}

	// First entry MUST be the own-proxy (kept name).
	if got := getMappingField(merged[0], "name"); got != "my-home-server" {
		t.Errorf("merged[0].name = %q, want my-home-server (own-proxy first)", got)
	}

	// The upstream's `my-home-server` must have been suffixed.
	foundSuffixed := false
	for _, m := range merged {
		if getMappingField(m, "name") == "my-home-server@alpha" {
			foundSuffixed = true
		}
	}
	if !foundSuffixed {
		t.Errorf("expected upstream proxy renamed to my-home-server@alpha; merged names: %v",
			func() []string {
				out := []string{}
				for _, m := range merged {
					out = append(out, getMappingField(m, "name"))
				}
				return out
			}())
	}

	if len(collisions) != 1 || collisions[0].Sources[0] != "own" {
		t.Errorf("collision = %+v, want first source 'own'", collisions[0])
	}
}

// MergeProxies must clone nodes so cache nodes are not mutated.
func TestMergeProxies_DoesNotMutateInput(t *testing.T) {
	original := proxy(t, "shared")
	originalName := getMappingField(original, "name")

	per := map[string][]*yaml.Node{
		"alpha": {original},
	}
	own := []*yaml.Node{proxy(t, "shared")}

	_, _ = MergeProxies(per, []string{"alpha"}, own)

	if got := getMappingField(original, "name"); got != originalName {
		t.Errorf("original input mutated: name = %q, want %q", got, originalName)
	}
}

// Empty inputs → empty output.
func TestMergeProxies_Empty(t *testing.T) {
	merged, collisions := MergeProxies(nil, nil, nil)
	if len(merged) != 0 || len(collisions) != 0 {
		t.Errorf("got merged=%v collisions=%v, want empty", merged, collisions)
	}
}
