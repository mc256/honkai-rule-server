package merge

import (
	"sort"
	"testing"

	"gopkg.in/yaml.v3"
)

// fanoutTestProxyYAML is the canonical test fixture for an own-proxy
// post-RewriteOwn (carries the `_` prefix on its name).
const fanoutTestProxyYAML = `
name: _markham
type: ss
server: 173.32.232.215
port: 8080
cipher: xchacha20-ietf-poly1305
password: pw
udp: true
udp-over-tcp: false
udp-over-tcp-version: 2
ip-version: ipv4
`

// regionGroupNode constructs a minimal proxy-group mapping node with the
// given name and a (placeholder) members list — fan-out only reads `name`.
func regionGroupNode(name string) *yaml.Node {
	g := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	setMappingValue(g, "name", &yaml.Node{Kind: yaml.ScalarNode, Value: name, Tag: "!!str"})
	setMappingValue(g, "type", &yaml.Node{Kind: yaml.ScalarNode, Value: "select", Tag: "!!str"})
	return g
}

// findFanoutByName scans a fan-out result slice for the unique entry whose
// `name` field equals target; returns nil if absent. Asserts no duplicates.
func findFanoutByName(t *testing.T, fanout []*yaml.Node, target string) *yaml.Node {
	t.Helper()
	var found *yaml.Node
	for _, n := range fanout {
		if getMappingField(n, "name") == target {
			if found != nil {
				t.Fatalf("duplicate fan-out entry named %q", target)
			}
			found = n
		}
	}
	return found
}

// fanoutNames returns the ordered list of `name` values from a fan-out slice.
func fanoutNames(fanout []*yaml.Node) []string {
	out := make([]string, 0, len(fanout))
	for _, n := range fanout {
		out = append(out, getMappingField(n, "name"))
	}
	return out
}

// TestAppendFanoutProxies_BasicRegionFanout — TC-U-FANOUT-01.
// One own-proxy + two `_region_*` groups → expect two per-region fan-out
// entries plus one AUTO entry (US2 augmented count).
func TestAppendFanoutProxies_BasicRegionFanout(t *testing.T) {
	own := mustParseYAMLNode(fanoutTestProxyYAML)
	groups := []*yaml.Node{regionGroupNode("_region_HK"), regionGroupNode("_region_JP")}

	fanout, skipped := AppendFanoutProxies([]*yaml.Node{own}, groups, "Proxies")

	if skipped != 0 {
		t.Errorf("skipped count = %d, want 0", skipped)
	}
	// 1 AUTO + 2 per-region.
	if len(fanout) != 3 {
		t.Fatalf("len(fanout) = %d, want 3 (AUTO + 2 per-region); names=%v", len(fanout), fanoutNames(fanout))
	}
	for _, target := range []struct {
		name        string
		dialerProxy string
	}{
		{"via_AUTO__markham", "Proxies"},
		{"via_region_HK__markham", "_region_HK"},
		{"via_region_JP__markham", "_region_JP"},
	} {
		entry := findFanoutByName(t, fanout, target.name)
		if entry == nil {
			t.Errorf("missing fan-out entry %q", target.name)
			continue
		}
		if got := getMappingField(entry, "dialer-proxy"); got != target.dialerProxy {
			t.Errorf("entry %q dialer-proxy = %q, want %q", target.name, got, target.dialerProxy)
		}
	}
}

// TestAppendFanoutProxies_ContinentAndUnknown — TC-U-FANOUT-02.
// `_region_UNKNOWN` and `_continent_AS` are valid fan-out targets.
func TestAppendFanoutProxies_ContinentAndUnknown(t *testing.T) {
	own := mustParseYAMLNode(fanoutTestProxyYAML)
	groups := []*yaml.Node{regionGroupNode("_region_UNKNOWN"), regionGroupNode("_continent_AS")}

	fanout, _ := AppendFanoutProxies([]*yaml.Node{own}, groups, "Proxies")
	// 1 AUTO + 2 per-group.
	if len(fanout) != 3 {
		t.Fatalf("len(fanout) = %d, want 3 (AUTO + UNKNOWN + AS); names=%v", len(fanout), fanoutNames(fanout))
	}
	for _, target := range []struct {
		name        string
		dialerProxy string
	}{
		{"via_region_UNKNOWN__markham", "_region_UNKNOWN"},
		{"via_continent_AS__markham", "_continent_AS"},
	} {
		entry := findFanoutByName(t, fanout, target.name)
		if entry == nil {
			t.Errorf("missing fan-out entry %q", target.name)
			continue
		}
		if got := getMappingField(entry, "dialer-proxy"); got != target.dialerProxy {
			t.Errorf("entry %q dialer-proxy = %q, want %q", target.name, got, target.dialerProxy)
		}
	}
}

// TestAppendFanoutProxies_DeterministicOrder — TC-U-FANOUT-03.
// Outer = own-proxy declaration order; inner = AUTO first, then mergedGroups
// in slice order (filtered to `_region_*`/`_continent_*`).
func TestAppendFanoutProxies_DeterministicOrder(t *testing.T) {
	first := mustParseYAMLNode("name: _first\ntype: ss\nserver: a\nport: 1\ncipher: c\npassword: pw\n")
	second := mustParseYAMLNode("name: _second\ntype: ss\nserver: a\nport: 1\ncipher: c\npassword: pw\n")

	groups := []*yaml.Node{
		regionGroupNode("_region_JP"),
		regionGroupNode("_region_HK"), // intentionally non-alphabetical to test slice-order preservation
		regionGroupNode("_continent_AS"),
	}

	fanout1, _ := AppendFanoutProxies([]*yaml.Node{first, second}, groups, "Proxies")
	fanout2, _ := AppendFanoutProxies([]*yaml.Node{first, second}, groups, "Proxies")

	want := []string{
		"via_AUTO__first",
		"via_region_JP__first",
		"via_region_HK__first",
		"via_continent_AS__first",
		"via_AUTO__second",
		"via_region_JP__second",
		"via_region_HK__second",
		"via_continent_AS__second",
	}
	got := fanoutNames(fanout1)
	if len(got) != len(want) {
		t.Fatalf("len(fanout) = %d, want %d; got=%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("fanout[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// Determinism across two calls.
	if got2 := fanoutNames(fanout2); !equalStringSlice(got, got2) {
		t.Errorf("two consecutive calls produced different order:\n  first: %v\n  second: %v", got, got2)
	}
}

// TestAppendFanoutProxies_FieldCopyVerbatim — TC-U-FANOUT-04.
// Every source-own field except `name` is preserved verbatim; `dialer-proxy`
// is added at the end.
func TestAppendFanoutProxies_FieldCopyVerbatim(t *testing.T) {
	own := mustParseYAMLNode(fanoutTestProxyYAML)
	groups := []*yaml.Node{regionGroupNode("_region_X")}

	fanout, _ := AppendFanoutProxies([]*yaml.Node{own}, groups, "Proxies")

	entry := findFanoutByName(t, fanout, "via_region_X__markham")
	if entry == nil {
		t.Fatalf("missing fan-out entry via_region_X__markham; names=%v", fanoutNames(fanout))
	}

	// Every field present on `own` except `name` must match on `entry`.
	for i := 0; i+1 < len(own.Content); i += 2 {
		key := own.Content[i].Value
		if key == "name" {
			continue
		}
		want := getMappingField(own, key)
		got := getMappingField(entry, key)
		if got != want {
			t.Errorf("field %q: entry=%q want=%q", key, got, want)
		}
	}
	// `name` and `dialer-proxy` are the synthesized fields.
	if got := getMappingField(entry, "name"); got != "via_region_X__markham" {
		t.Errorf("name = %q, want via_region_X__markham", got)
	}
	if got := getMappingField(entry, "dialer-proxy"); got != "_region_X" {
		t.Errorf("dialer-proxy = %q, want _region_X", got)
	}
}

// TestAppendFanoutProxies_SkipsExplicitDialerProxy — TC-U-FANOUT-05.
// FR-005: own-proxies that already declare `dialer-proxy` are skipped
// entirely (no AUTO, no per-group); the skipped count reflects the bypass.
func TestAppendFanoutProxies_SkipsExplicitDialerProxy(t *testing.T) {
	a := mustParseYAMLNode("name: _a\ntype: ss\nserver: x\nport: 1\ncipher: c\npassword: pw\n")
	b := mustParseYAMLNode("name: _b\ntype: ss\nserver: y\nport: 2\ncipher: c\npassword: pw\ndialer-proxy: DIRECT\n")

	groups := []*yaml.Node{regionGroupNode("_region_JP"), regionGroupNode("_region_HK")}

	fanout, skipped := AppendFanoutProxies([]*yaml.Node{a, b}, groups, "Proxies")

	if skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}
	// `_a` produces 1 AUTO + 2 per-region = 3 entries; `_b` produces 0.
	if len(fanout) != 3 {
		t.Fatalf("len(fanout) = %d, want 3 (only `_a`'s copies); names=%v", len(fanout), fanoutNames(fanout))
	}
	names := fanoutNames(fanout)
	sort.Strings(names)
	want := []string{"via_AUTO__a", "via_region_HK__a", "via_region_JP__a"}
	if !equalStringSlice(names, want) {
		t.Errorf("names = %v, want %v", names, want)
	}
	// And explicitly: nothing for `_b`.
	for _, n := range fanout {
		if name := getMappingField(n, "name"); contains([]string{"via_AUTO__b", "via_region_JP__b", "via_region_HK__b"}, name) {
			t.Errorf("entry for skipped own-proxy should not exist: %q", name)
		}
	}
}

// TestAppendFanoutProxies_AutoOnlyWhenNoRegionGroups — TC-U-FANOUT-06.
// AUTO is unconditional on region/continent group counts.
func TestAppendFanoutProxies_AutoOnlyWhenNoRegionGroups(t *testing.T) {
	own := mustParseYAMLNode(fanoutTestProxyYAML)

	fanout, skipped := AppendFanoutProxies([]*yaml.Node{own}, nil, "Proxies")

	if skipped != 0 {
		t.Errorf("skipped = %d, want 0", skipped)
	}
	if len(fanout) != 1 {
		t.Fatalf("len(fanout) = %d, want 1 (AUTO only); names=%v", len(fanout), fanoutNames(fanout))
	}
	auto := fanout[0]
	if got := getMappingField(auto, "name"); got != "via_AUTO__markham" {
		t.Errorf("name = %q, want via_AUTO__markham", got)
	}
	if got := getMappingField(auto, "dialer-proxy"); got != "Proxies" {
		t.Errorf("dialer-proxy = %q, want Proxies", got)
	}
	// Source-own fields preserved verbatim (except name).
	for i := 0; i+1 < len(own.Content); i += 2 {
		key := own.Content[i].Value
		if key == "name" {
			continue
		}
		if got, want := getMappingField(auto, key), getMappingField(own, key); got != want {
			t.Errorf("field %q: auto=%q want=%q", key, got, want)
		}
	}
}

// TestAppendFanoutProxies_AutoEmittedBeforePerGroup — TC-U-FANOUT-07.
// Within a single own-proxy's block, AUTO precedes per-region/per-continent.
func TestAppendFanoutProxies_AutoEmittedBeforePerGroup(t *testing.T) {
	own := mustParseYAMLNode(fanoutTestProxyYAML)
	groups := []*yaml.Node{regionGroupNode("_region_JP")}

	fanout, _ := AppendFanoutProxies([]*yaml.Node{own}, groups, "Proxies")
	if len(fanout) != 2 {
		t.Fatalf("len(fanout) = %d, want 2 (AUTO + JP); names=%v", len(fanout), fanoutNames(fanout))
	}
	if got := getMappingField(fanout[0], "name"); got != "via_AUTO__markham" {
		t.Errorf("fanout[0].name = %q, want via_AUTO__markham", got)
	}
	if got := getMappingField(fanout[1], "name"); got != "via_region_JP__markham" {
		t.Errorf("fanout[1].name = %q, want via_region_JP__markham", got)
	}
}

// TestAppendFanoutProxies_DefaultProxiesGroupName — empty proxiesGroupName
// argument falls back to literal "Proxies" for AUTO's dialer-proxy.
func TestAppendFanoutProxies_DefaultProxiesGroupName(t *testing.T) {
	own := mustParseYAMLNode(fanoutTestProxyYAML)
	fanout, _ := AppendFanoutProxies([]*yaml.Node{own}, nil, "")
	if len(fanout) != 1 {
		t.Fatalf("len(fanout) = %d, want 1", len(fanout))
	}
	if got := getMappingField(fanout[0], "dialer-proxy"); got != "Proxies" {
		t.Errorf("dialer-proxy fallback = %q, want Proxies", got)
	}
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
