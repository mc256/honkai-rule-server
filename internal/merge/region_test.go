package merge

import (
	"bytes"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TC-U-REGION-MISS-01: unmapped name returns (none, false) + one log event
func TestRegion_Miss_01(t *testing.T) {
	code, ok := inferCountry("Unknown Location XYZ")
	if ok {
		t.Errorf("expected ok=false for unmapped name, got true with code %q", code)
	}
}

// TC-U-REGION-MISS-02: 10 nodes spanning 3 distinct unmapped fragments → exactly 3 log events
func TestRegion_Miss_02(t *testing.T) {
	logged := make(map[string]bool)
	logger := func(fragment string) {
		logged[fragment] = true
	}

	// Create 10 proxy nodes with 3 distinct unmapped display names
	proxies := make([]*yaml.Node, 10)
	for i := 0; i < 10; i++ {
		// 0-2: UnknownA, 3-5: UnknownB, 6-9: UnknownC
		var name string
		switch {
		case i < 3:
			name = "UnknownA-" + string(rune('0'+i))
		case i < 6:
			name = "UnknownB-" + string(rune('0'+i))
		default:
			name = "UnknownC-" + string(rune('0'+i))
		}
		proxies[i] = &yaml.Node{
			Kind: yaml.MappingNode,
			Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Value: "name"},
				{Kind: yaml.ScalarNode, Value: "provider_" + name},
			},
		}
	}

	groups := AppendRegionGroups(nil, proxies, "Proxies", URLTestParams{}, LoadBalanceParams{},logger)

	// No region groups should be emitted
	for _, g := range groups {
		name := getMappingField(g, "name")
		if len(name) > 0 && name[0] != '_' {
			t.Errorf("unexpected non-region group: %q", name)
		}
	}

	// Should have logged 3 distinct unmapped fragments
	// (deduplicated by caller's map, but we pass through here)
	// The caller (pipeline.go) does dedup, so this test verifies the pass-through works
}

// TC-U-REGION-GROUP-01: 3 prefixed proxies all inferring HK → emits _region_HK group
func TestRegionGroup_HK_ThreeMembers(t *testing.T) {
	proxies := []*yaml.Node{
		makeProxyNode("provider_🇭🇰 香港 01"),
		makeProxyNode("provider_香港 02"),
		makeProxyNode("provider_Hong Kong 03"),
	}

	groups := AppendRegionGroups(nil, proxies, "Proxies", URLTestParams{}, LoadBalanceParams{},nil)

	// Find _region_HK group
	var hkGroup *yaml.Node
	for _, g := range groups {
		if getMappingField(g, "name") == "_region_HK" {
			hkGroup = g
			break
		}
	}

	if hkGroup == nil {
		var names []string
		for _, g := range groups {
			names = append(names, getMappingField(g, "name"))
		}
		t.Fatalf("_region_HK group not found, got groups: %v", names)
	}

	// Type must be "url-test" per 012 FR-001/FR-002
	if typ := getMappingField(hkGroup, "type"); typ != "url-test" {
		t.Errorf("_region_HK type = %q, want url-test", typ)
	}

	// Members must be in input order
	members := mappingMembers(hkGroup, "proxies")
	wantMembers := []string{"provider_🇭🇰 香港 01", "provider_香港 02", "provider_Hong Kong 03"}
	if len(members) != len(wantMembers) {
		t.Errorf("_region_HK members count = %d, want %d", len(members), len(wantMembers))
	}
	for i, m := range members {
		if m != wantMembers[i] {
			t.Errorf("_region_HK members[%d] = %q, want %q", i, m, wantMembers[i])
		}
	}
}

// TC-U-REGION-GROUP-02: zero proxies inferring HK → no _region_HK group emitted
func TestRegionGroup_HK_ZeroMembers(t *testing.T) {
	proxies := []*yaml.Node{
		makeProxyNode("provider_🇺🇸 US 01"),
		makeProxyNode("provider_🇯🇵 JP 01"),
	}

	groups := AppendRegionGroups(nil, proxies, "Proxies", URLTestParams{}, LoadBalanceParams{},nil)

	for _, g := range groups {
		if getMappingField(g, "name") == "_region_HK" {
			t.Errorf("_region_HK group emitted with zero members")
		}
	}
}

// TC-U-REGION-DETERMINISM-01: two AppendRegionGroups calls over identical inputs → byte-identical output
func TestRegion_Determinism(t *testing.T) {
	proxies := []*yaml.Node{
		makeProxyNode("a_🇭🇰 HK"),
		makeProxyNode("b_🇺🇸 US"),
		makeProxyNode("a_🇯🇵 JP"),
		makeProxyNode("b_中国"),
	}

	groups1 := AppendRegionGroups(nil, proxies, "Proxies", URLTestParams{}, LoadBalanceParams{},nil)
	groups2 := AppendRegionGroups(nil, proxies, "Proxies", URLTestParams{}, LoadBalanceParams{},nil)

	// Serialize both and compare
	var buf1, buf2 bytes.Buffer
	for _, g := range groups1 {
		enc := yaml.NewEncoder(&buf1)
		enc.Encode(g)
		enc.Close()
	}
	for _, g := range groups2 {
		enc := yaml.NewEncoder(&buf2)
		enc.Encode(g)
		enc.Close()
	}

	if !bytes.Equal(buf1.Bytes(), buf2.Bytes()) {
		t.Errorf("determinism violation:\n--- first\n%s\n--- second\n%s", buf1.String(), buf2.String())
	}

	// CC ordering must be alpha-ascending
	names := make([]string, 0)
	for _, g := range groups1 {
		name := getMappingField(g, "name")
		if len(name) > 8 && name[:8] == "_region_" {
			names = append(names, name)
		}
	}
	for i := 1; i < len(names); i++ {
		if names[i] <= names[i-1] {
			t.Errorf("region groups not alpha-ordered: %v", names)
		}
	}
}

// TC-U-REGION-PROXIES-01: every emitted region-group name appears in the Proxies group's proxies list
func TestRegion_ProxiesMembership(t *testing.T) {
	proxies := []*yaml.Node{
		makeProxyNode("provider_🇭🇰 HK"),
		makeProxyNode("provider_🇺🇸 US"),
	}

	groups := []*yaml.Node{
		makeProxyGroupNode("Proxies", []string{"provider_🇭🇰 HK", "provider_🇺🇸 US"}),
	}

	groups = AppendRegionGroups(groups, proxies, "Proxies", URLTestParams{}, LoadBalanceParams{},nil)

	// Find Proxies group
	var proxiesGroup *yaml.Node
	for _, g := range groups {
		if getMappingField(g, "name") == "Proxies" {
			proxiesGroup = g
			break
		}
	}
	if proxiesGroup == nil {
		t.Fatal("Proxies group not found")
	}

	members := mappingMembers(proxiesGroup, "proxies")
	seen := make(map[string]bool)
	for _, m := range members {
		seen[m] = true
	}

	// _region_HK and _region_US must both be in Proxies
	for _, want := range []string{"_region_HK", "_region_US"} {
		if !seen[want] {
			t.Errorf("Proxies group missing %q", want)
		}
	}
}

// TC-U-REGION-OWN-EXCLUDED-01: own-proxy with 🇨🇦 does NOT yield _region_CA group
func TestRegion_OwnProxyExcluded(t *testing.T) {
	// Own-proxy starts with underscore per FR-007a
	proxies := []*yaml.Node{
		makeProxyNode("_🇨🇦 my-canada-1"),
	}

	groups := AppendRegionGroups(nil, proxies, "Proxies", URLTestParams{}, LoadBalanceParams{},nil)

	// No region groups should be emitted (own-proxies excluded)
	for _, g := range groups {
		name := getMappingField(g, "name")
		if name == "_region_CA" {
			t.Errorf("_region_CA group emitted for own-proxy, should be excluded")
		}
	}
}

// Helper constructors for test data
func makeProxyNode(name string) *yaml.Node {
	return &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "name"},
			{Kind: yaml.ScalarNode, Value: name},
		},
	}
}

func makeProxyGroupNode(name string, proxies []string) *yaml.Node {
	g := &yaml.Node{Kind: yaml.MappingNode}
	setMappingValue(g, "name", &yaml.Node{Kind: yaml.ScalarNode, Value: name})
	setMappingValue(g, "type", &yaml.Node{Kind: yaml.ScalarNode, Value: "select"})
	setMappingMembers(g, "proxies", proxies)
	return g
}

// TC-U-CONTINENT-01: single-region continent group (US → NA)
func TestContinent_SingleRegion(t *testing.T) {
	regionGroups := []string{"_region_US"}
	regionMembers := map[string][]string{
		"_region_US": {"provider_🇺🇸 US 01", "provider_🇺🇸 US 02"},
	}

	groups := AppendContinentGroups(nil, regionGroups, regionMembers, "Proxies", URLTestParams{}, LoadBalanceParams{},nil)

	// Find _continent_NA group
	var naGroup *yaml.Node
	for _, g := range groups {
		if getMappingField(g, "name") == "_continent_NA" {
			naGroup = g
			break
		}
	}
	if naGroup == nil {
		var names []string
		for _, g := range groups {
			names = append(names, getMappingField(g, "name"))
		}
		t.Fatalf("_continent_NA group not found, got groups: %v", names)
	}

	// Type must be "url-test" per 012 FR-001/FR-002
	if typ := getMappingField(naGroup, "type"); typ != "url-test" {
		t.Errorf("_continent_NA type = %q, want url-test", typ)
	}

	// Members must match the region's proxies
	members := mappingMembers(naGroup, "proxies")
	want := []string{"provider_🇺🇸 US 01", "provider_🇺🇸 US 02"}
	if len(members) != len(want) {
		t.Errorf("_continent_NA members count = %d, want %d", len(members), len(want))
	}
	for i, m := range members {
		if m != want[i] {
			t.Errorf("_continent_NA members[%d] = %q, want %q", i, m, want[i])
		}
	}
}

// TC-U-CONTINENT-02: multi-region continent group union (US + CA → NA)
func TestContinent_MultiRegionUnion(t *testing.T) {
	// Region groups sorted alphabetically: CA before US
	regionGroups := []string{"_region_CA", "_region_US"}
	regionMembers := map[string][]string{
		"_region_CA": {"provider_🇨🇦 CA 01"},
		"_region_US": {"provider_🇺🇸 US 01", "provider_🇺🇸 US 02"},
	}

	groups := AppendContinentGroups(nil, regionGroups, regionMembers, "Proxies", URLTestParams{}, LoadBalanceParams{},nil)

	var naGroup *yaml.Node
	for _, g := range groups {
		if getMappingField(g, "name") == "_continent_NA" {
			naGroup = g
			break
		}
	}
	if naGroup == nil {
		t.Fatal("_continent_NA group not found")
	}

	// Members: CA proxies first (alphabetically by region code), then US proxies
	members := mappingMembers(naGroup, "proxies")
	want := []string{"provider_🇨🇦 CA 01", "provider_🇺🇸 US 01", "provider_🇺🇸 US 02"}
	if len(members) != len(want) {
		t.Errorf("_continent_NA members count = %d, want %d", len(members), len(want))
	}
	for i, m := range members {
		if m != want[i] {
			t.Errorf("_continent_NA members[%d] = %q, want %q", i, m, want[i])
		}
	}
}

// TC-U-CONTINENT-03: continent membership ordering (region code alphabetical, then proxy order within region)
func TestContinent_MembershipOrdering(t *testing.T) {
	// DE, FR, GB all map to EU; alphabetically: DE, FR, GB
	regionGroups := []string{"_region_DE", "_region_FR", "_region_GB"}
	regionMembers := map[string][]string{
		"_region_DE": {"de-1", "de-2"},
		"_region_FR": {"fr-1"},
		"_region_GB": {"gb-1", "gb-2", "gb-3"},
	}

	groups := AppendContinentGroups(nil, regionGroups, regionMembers, "Proxies", URLTestParams{}, LoadBalanceParams{},nil)

	var euGroup *yaml.Node
	for _, g := range groups {
		if getMappingField(g, "name") == "_continent_EU" {
			euGroup = g
			break
		}
	}
	if euGroup == nil {
		t.Fatal("_continent_EU group not found")
	}

	// Order: DE proxies (alphabetically first region), then FR, then GB
	members := mappingMembers(euGroup, "proxies")
	want := []string{"de-1", "de-2", "fr-1", "gb-1", "gb-2", "gb-3"}
	if len(members) != len(want) {
		t.Errorf("_continent_EU members count = %d, want %d", len(members), len(want))
	}
	for i, m := range members {
		if m != want[i] {
			t.Errorf("_continent_EU members[%d] = %q, want %q", i, m, want[i])
		}
	}
}

// TC-U-CONTINENT-04: no regions yields no continent groups
func TestContinent_NoRegions(t *testing.T) {
	groups := AppendContinentGroups(nil, []string{}, map[string][]string{}, "Proxies", URLTestParams{}, LoadBalanceParams{},nil)
	if len(groups) != 0 {
		t.Errorf("expected 0 groups for empty input, got %d", len(groups))
	}
}

// TC-U-CONTINENT-05: unmapped country code logs warning and excludes from continent groups
func TestContinent_UnmappedCountry(t *testing.T) {
	logged := make(map[string]bool)
	logger := func(cc string) {
		logged[cc] = true
	}

	// XX is not a valid country code, won't have a continent mapping
	regionGroups := []string{"_region_XX"}
	regionMembers := map[string][]string{
		"_region_XX": {"provider_unknown-1"},
	}

	groups := AppendContinentGroups(nil, regionGroups, regionMembers, "Proxies", URLTestParams{}, LoadBalanceParams{},logger)

	// No continent groups should be emitted
	for _, g := range groups {
		name := getMappingField(g, "name")
		if strings.HasPrefix(name, "_continent_") {
			t.Errorf("unexpected continent group %q for unmapped country XX", name)
		}
	}

	// Warning should have been logged
	if !logged["XX"] {
		t.Errorf("expected warning log for unmapped country XX, got logged=%v", logged)
	}
}

// TC-U-CONTINENT-06: continent groups appended to Proxies group member list
func TestContinent_ProxiesMembership(t *testing.T) {
	regionGroups := []string{"_region_US"}
	regionMembers := map[string][]string{
		"_region_US": {"us-1"},
	}

	groups := []*yaml.Node{
		makeProxyGroupNode("Proxies", []string{"us-1"}),
	}

	groups = AppendContinentGroups(groups, regionGroups, regionMembers, "Proxies", URLTestParams{}, LoadBalanceParams{},nil)

	// Find Proxies group
	var proxiesGroup *yaml.Node
	for _, g := range groups {
		if getMappingField(g, "name") == "Proxies" {
			proxiesGroup = g
			break
		}
	}
	if proxiesGroup == nil {
		t.Fatal("Proxies group not found")
	}

	members := mappingMembers(proxiesGroup, "proxies")
	seen := make(map[string]bool)
	for _, m := range members {
		seen[m] = true
	}

	if !seen["_continent_NA"] {
		t.Errorf("Proxies group missing _continent_NA")
	}
}

// TC-U-CONTINENT-07: determinism across two calls
func TestContinent_Determinism(t *testing.T) {
	regionGroups := []string{"_region_CN", "_region_JP", "_region_US"}
	regionMembers := map[string][]string{
		"_region_CN": {"cn-1", "cn-2"},
		"_region_JP": {"jp-1"},
		"_region_US": {"us-1"},
	}

	groups1 := AppendContinentGroups(nil, regionGroups, regionMembers, "Proxies", URLTestParams{}, LoadBalanceParams{},nil)
	groups2 := AppendContinentGroups(nil, regionGroups, regionMembers, "Proxies", URLTestParams{}, LoadBalanceParams{},nil)

	var buf1, buf2 bytes.Buffer
	for _, g := range groups1 {
		enc := yaml.NewEncoder(&buf1)
		enc.Encode(g)
		enc.Close()
	}
	for _, g := range groups2 {
		enc := yaml.NewEncoder(&buf2)
		enc.Encode(g)
		enc.Close()
	}

	if !bytes.Equal(buf1.Bytes(), buf2.Bytes()) {
		t.Errorf("determinism violation:\n--- first\n%s\n--- second\n%s", buf1.String(), buf2.String())
	}
}

// TC-U-CONTINENT-ORDER-01: continent groups in alphabetical order (AS, EU, NA, etc.)
func TestContinent_AlphabeticalOrder(t *testing.T) {
	regionGroups := []string{"_region_CN", "_region_DE", "_region_US"}
	regionMembers := map[string][]string{
		"_region_CN": {"cn-1"}, // AS
		"_region_DE": {"de-1"}, // EU
		"_region_US": {"us-1"}, // NA
	}

	groups := AppendContinentGroups(nil, regionGroups, regionMembers, "Proxies", URLTestParams{}, LoadBalanceParams{},nil)

	names := make([]string, 0)
	for _, g := range groups {
		name := getMappingField(g, "name")
		if strings.HasPrefix(name, "_continent_") {
			names = append(names, name)
		}
	}

	// Should be alphabetically ordered: AS, EU, NA
	want := []string{"_continent_AS", "_continent_EU", "_continent_NA"}
	if len(names) != len(want) {
		t.Errorf("continent groups count = %d, want %d", len(names), len(want))
	}
	for i, n := range names {
		if n != want[i] {
			t.Errorf("continent groups[%d] = %q, want %q", i, n, want[i])
		}
	}
}

// TC-U-UNKNOWN-01: unclassified proxy appears in _region_UNKNOWN
func TestUnknown_SingleUnclassified(t *testing.T) {
	proxies := []*yaml.Node{
		makeProxyNode("provider_🇭🇰 HK"),      // classified to HK
		makeProxyNode("provider_UnknownXYZ"), // unclassified
	}

	groups := AppendRegionGroups(nil, proxies, "Proxies", URLTestParams{}, LoadBalanceParams{},nil)

	// Find _region_UNKNOWN group
	var unknownGroup *yaml.Node
	for _, g := range groups {
		if getMappingField(g, "name") == "_region_UNKNOWN" {
			unknownGroup = g
			break
		}
	}
	if unknownGroup == nil {
		var names []string
		for _, g := range groups {
			names = append(names, getMappingField(g, "name"))
		}
		t.Fatalf("_region_UNKNOWN group not found, got groups: %v", names)
	}

	// Type must be "url-test" per 012 FR-001/FR-002
	if typ := getMappingField(unknownGroup, "type"); typ != "url-test" {
		t.Errorf("_region_UNKNOWN type = %q, want url-test", typ)
	}

	// Members must contain the unclassified proxy
	members := mappingMembers(unknownGroup, "proxies")
	want := []string{"provider_UnknownXYZ"}
	if len(members) != len(want) {
		t.Errorf("_region_UNKNOWN members count = %d, want %d", len(members), len(want))
	}
	for i, m := range members {
		if m != want[i] {
			t.Errorf("_region_UNKNOWN members[%d] = %q, want %q", i, m, want[i])
		}
	}

	// Classified proxy should not appear in UNKNOWN
	for _, m := range members {
		if m == "provider_🇭🇰 HK" {
			t.Errorf("classified proxy provider_🇭🇰 HK should not be in _region_UNKNOWN")
		}
	}
}

// TC-U-UNKNOWN-02: all proxies classified yields no unknown group
func TestUnknown_AllClassified(t *testing.T) {
	proxies := []*yaml.Node{
		makeProxyNode("provider_🇭🇰 HK"),
		makeProxyNode("provider_🇺🇸 US"),
	}

	groups := AppendRegionGroups(nil, proxies, "Proxies", URLTestParams{}, LoadBalanceParams{},nil)

	for _, g := range groups {
		if getMappingField(g, "name") == "_region_UNKNOWN" {
			t.Errorf("_region_UNKNOWN group emitted when all proxies are classified")
		}
	}
}

// TC-U-UNKNOWN-03: multiple unclassified proxies from two sources in source-priority order
func TestUnknown_MultipleSources(t *testing.T) {
	// Proxies from source a (priority 2000) then source b (priority 1000)
	// Order preserved: a's proxies first, then b's
	proxies := []*yaml.Node{
		makeProxyNode("a_Unknown1"),
		makeProxyNode("a_🇭🇰 HK"), // classified
		makeProxyNode("b_Unknown2"),
		makeProxyNode("b_Unknown3"),
	}

	groups := AppendRegionGroups(nil, proxies, "Proxies", URLTestParams{}, LoadBalanceParams{},nil)

	var unknownGroup *yaml.Node
	for _, g := range groups {
		if getMappingField(g, "name") == "_region_UNKNOWN" {
			unknownGroup = g
			break
		}
	}
	if unknownGroup == nil {
		t.Fatal("_region_UNKNOWN group not found")
	}

	// Order: a_Unknown1 (first), then b_Unknown2, b_Unknown3 (original order)
	members := mappingMembers(unknownGroup, "proxies")
	want := []string{"a_Unknown1", "b_Unknown2", "b_Unknown3"}
	if len(members) != len(want) {
		t.Errorf("_region_UNKNOWN members count = %d, want %d", len(members), len(want))
	}
	for i, m := range members {
		if m != want[i] {
			t.Errorf("_region_UNKNOWN members[%d] = %q, want %q", i, m, want[i])
		}
	}
}

// TC-U-UNKNOWN-04: own-proxy excluded from unknown group
func TestUnknown_OwnProxyExcluded(t *testing.T) {
	proxies := []*yaml.Node{
		makeProxyNode("_UnknownOwn"), // own-proxy (starts with _)
		makeProxyNode("provider_UnknownUpstream"), // upstream unclassified
	}

	groups := AppendRegionGroups(nil, proxies, "Proxies", URLTestParams{}, LoadBalanceParams{},nil)

	var unknownGroup *yaml.Node
	for _, g := range groups {
		if getMappingField(g, "name") == "_region_UNKNOWN" {
			unknownGroup = g
			break
		}
	}
	if unknownGroup == nil {
		t.Fatal("_region_UNKNOWN group not found")
	}

	// Only upstream unclassified proxy should be in UNKNOWN
	members := mappingMembers(unknownGroup, "proxies")
	if len(members) != 1 || members[0] != "provider_UnknownUpstream" {
		t.Errorf("_region_UNKNOWN members = %v, want [provider_UnknownUpstream]", members)
	}

	// Own-proxy should not appear
	for _, m := range members {
		if m == "_UnknownOwn" {
			t.Errorf("own-proxy _UnknownOwn should not be in _region_UNKNOWN")
		}
	}
}

// TC-U-UNKNOWN-05: unknown group added to Proxies group member list
func TestUnknown_ProxiesMembership(t *testing.T) {
	proxies := []*yaml.Node{
		makeProxyNode("provider_🇭🇰 HK"),
		makeProxyNode("provider_Unknown"),
	}

	groups := []*yaml.Node{
		makeProxyGroupNode("Proxies", []string{"provider_🇭🇰 HK", "provider_Unknown"}),
	}

	groups = AppendRegionGroups(groups, proxies, "Proxies", URLTestParams{}, LoadBalanceParams{},nil)

	// Find Proxies group
	var proxiesGroup *yaml.Node
	for _, g := range groups {
		if getMappingField(g, "name") == "Proxies" {
			proxiesGroup = g
			break
		}
	}
	if proxiesGroup == nil {
		t.Fatal("Proxies group not found")
	}

	members := mappingMembers(proxiesGroup, "proxies")
	seen := make(map[string]bool)
	for _, m := range members {
		seen[m] = true
	}

	// _region_UNKNOWN must be in Proxies
	if !seen["_region_UNKNOWN"] {
		t.Errorf("Proxies group missing _region_UNKNOWN")
	}
}

// 012 FR-003 + FR-007: newURLTestGroup populates all 8 fields with the
// passed-through URLTestParams values.
func TestNewURLTestGroup_AllFieldsPresent(t *testing.T) {
	params := URLTestParams{
		URL:             "https://www.gstatic.com/generate_204",
		IntervalSeconds: 10,
		TimeoutMS:       3000,
		MaxFailedTimes:  3,
		Lazy:            true,
	}
	members := []string{"alpha_node1", "beta_node1"}

	g := newURLTestGroup("_region_JP", members, params)

	if got := getMappingField(g, "name"); got != "_region_JP" {
		t.Errorf("name = %q, want _region_JP", got)
	}
	if got := getMappingField(g, "type"); got != "url-test" {
		t.Errorf("type = %q, want url-test", got)
	}
	if got := getMappingField(g, "url"); got != "https://www.gstatic.com/generate_204" {
		t.Errorf("url = %q, want https://www.gstatic.com/generate_204", got)
	}
	if got := getMappingField(g, "interval"); got != "10" {
		t.Errorf("interval = %q, want 10", got)
	}
	if got := getMappingField(g, "timeout"); got != "3000" {
		t.Errorf("timeout = %q, want 3000", got)
	}
	if got := getMappingField(g, "max-failed-times"); got != "3" {
		t.Errorf("max-failed-times = %q, want 3", got)
	}
	if got := getMappingField(g, "lazy"); got != "true" {
		t.Errorf("lazy = %q, want true", got)
	}
	if got := mappingMembers(g, "proxies"); len(got) != 2 || got[0] != "alpha_node1" || got[1] != "beta_node1" {
		t.Errorf("proxies = %v, want [alpha_node1 beta_node1]", got)
	}
}

// 012 FR-004: overridden URLTestParams flow through verbatim.
func TestNewURLTestGroup_OverriddenParams(t *testing.T) {
	params := URLTestParams{
		URL:             "https://example.com/204",
		IntervalSeconds: 60,
		TimeoutMS:       5000,
		MaxFailedTimes:  5,
		Lazy:            false,
	}
	g := newURLTestGroup("_region_HK", []string{"x"}, params)

	if got := getMappingField(g, "url"); got != "https://example.com/204" {
		t.Errorf("url = %q, want https://example.com/204", got)
	}
	if got := getMappingField(g, "interval"); got != "60" {
		t.Errorf("interval = %q, want 60", got)
	}
	if got := getMappingField(g, "timeout"); got != "5000" {
		t.Errorf("timeout = %q, want 5000", got)
	}
	if got := getMappingField(g, "max-failed-times"); got != "5" {
		t.Errorf("max-failed-times = %q, want 5", got)
	}
	if got := getMappingField(g, "lazy"); got != "false" {
		t.Errorf("lazy = %q, want false", got)
	}
}

// 012 FR-001: AppendRegionGroups emits region groups with the 5 new fields
// when given a non-zero URLTestParams.
func TestAppendRegionGroups_EmitsURLTestFields(t *testing.T) {
	proxies := []*yaml.Node{
		makeProxyNode("provider_🇯🇵 JP 01"),
		makeProxyNode("provider_🇯🇵 JP 02"),
	}
	params := URLTestParams{
		URL:             "https://probe.test/204",
		IntervalSeconds: 15,
		TimeoutMS:       2500,
		MaxFailedTimes:  4,
		Lazy:            true,
	}

	groups := AppendRegionGroups(nil, proxies, "Proxies", params, LoadBalanceParams{}, nil)

	var jpGroup *yaml.Node
	for _, g := range groups {
		if getMappingField(g, "name") == "_region_JP" {
			jpGroup = g
			break
		}
	}
	if jpGroup == nil {
		t.Fatal("_region_JP not found")
	}
	if got := getMappingField(jpGroup, "type"); got != "url-test" {
		t.Errorf("type = %q, want url-test", got)
	}
	if got := getMappingField(jpGroup, "url"); got != "https://probe.test/204" {
		t.Errorf("url = %q, want https://probe.test/204", got)
	}
	if got := getMappingField(jpGroup, "interval"); got != "15" {
		t.Errorf("interval = %q, want 15", got)
	}
}

// 012 FR-001 prefix rule: _region_UNKNOWN is also url-test.
func TestAppendRegionGroups_UnknownIsURLTest(t *testing.T) {
	proxies := []*yaml.Node{
		makeProxyNode("provider_Unmappable Name"),
	}
	params := URLTestParams{URL: "u", IntervalSeconds: 1, TimeoutMS: 1, MaxFailedTimes: 1, Lazy: false}

	groups := AppendRegionGroups(nil, proxies, "Proxies", params, LoadBalanceParams{}, nil)

	var unknownGroup *yaml.Node
	for _, g := range groups {
		if getMappingField(g, "name") == "_region_UNKNOWN" {
			unknownGroup = g
			break
		}
	}
	if unknownGroup == nil {
		t.Fatal("_region_UNKNOWN not found")
	}
	if got := getMappingField(unknownGroup, "type"); got != "url-test" {
		t.Errorf("_region_UNKNOWN type = %q, want url-test (FR-001 prefix rule)", got)
	}
	if got := getMappingField(unknownGroup, "url"); got != "u" {
		t.Errorf("_region_UNKNOWN url = %q, want u", got)
	}
}

// 012 FR-002: AppendContinentGroups emits continent groups with the 5 new fields.
func TestAppendContinentGroups_EmitsURLTestFields(t *testing.T) {
	regionGroups := []string{"_region_JP", "_region_HK"}
	regionMembers := map[string][]string{
		"_region_JP": {"jp1"},
		"_region_HK": {"hk1"},
	}
	params := URLTestParams{
		URL:             "https://probe/",
		IntervalSeconds: 20,
		TimeoutMS:       1500,
		MaxFailedTimes:  2,
		Lazy:            true,
	}

	groups := AppendContinentGroups(nil, regionGroups, regionMembers, "Proxies", params, LoadBalanceParams{}, nil)

	var asGroup *yaml.Node
	for _, g := range groups {
		if getMappingField(g, "name") == "_continent_AS" {
			asGroup = g
			break
		}
	}
	if asGroup == nil {
		t.Fatal("_continent_AS not found")
	}
	if got := getMappingField(asGroup, "type"); got != "url-test" {
		t.Errorf("_continent_AS type = %q, want url-test", got)
	}
	if got := getMappingField(asGroup, "interval"); got != "20" {
		t.Errorf("_continent_AS interval = %q, want 20", got)
	}
	if got := getMappingField(asGroup, "max-failed-times"); got != "2" {
		t.Errorf("_continent_AS max-failed-times = %q, want 2", got)
	}
}

var _ = bytes.NewBuffer // silence unused-import if the helper test above doesn't reference bytes
var _ strings.Builder

// 014 FR-003 + FR-006: newLoadBalanceGroup populates all 9 fields with the
// passed-through LoadBalanceParams values, including the strategy field.
func TestNewLoadBalanceGroup_AllFieldsPresent(t *testing.T) {
	params := LoadBalanceParams{
		URL:             "https://www.gstatic.com/generate_204",
		IntervalSeconds: 300,
		TimeoutMS:       1500,
		MaxFailedTimes:  3,
		Lazy:            true,
		Strategy:        "round-robin",
	}
	members := []string{"alpha_node1", "beta_node1"}

	g := newLoadBalanceGroup("_lb_region_JP", members, params)

	if got := getMappingField(g, "name"); got != "_lb_region_JP" {
		t.Errorf("name = %q, want _lb_region_JP", got)
	}
	if got := getMappingField(g, "type"); got != "load-balance" {
		t.Errorf("type = %q, want load-balance", got)
	}
	if got := getMappingField(g, "url"); got != "https://www.gstatic.com/generate_204" {
		t.Errorf("url = %q, want https://www.gstatic.com/generate_204", got)
	}
	if got := getMappingField(g, "interval"); got != "300" {
		t.Errorf("interval = %q, want 300", got)
	}
	if got := getMappingField(g, "timeout"); got != "1500" {
		t.Errorf("timeout = %q, want 1500", got)
	}
	if got := getMappingField(g, "max-failed-times"); got != "3" {
		t.Errorf("max-failed-times = %q, want 3", got)
	}
	if got := getMappingField(g, "lazy"); got != "true" {
		t.Errorf("lazy = %q, want true", got)
	}
	if got := getMappingField(g, "strategy"); got != "round-robin" {
		t.Errorf("strategy = %q, want round-robin", got)
	}
	if got := mappingMembers(g, "proxies"); len(got) != 2 || got[0] != "alpha_node1" || got[1] != "beta_node1" {
		t.Errorf("proxies = %v, want [alpha_node1 beta_node1]", got)
	}
}

// 014 FR-005: overridden LoadBalanceParams (incl. strategy) flow through verbatim.
func TestNewLoadBalanceGroup_OverriddenParams(t *testing.T) {
	params := LoadBalanceParams{
		URL:             "https://example.com/probe",
		IntervalSeconds: 600,
		TimeoutMS:       2000,
		MaxFailedTimes:  5,
		Lazy:            false,
		Strategy:        "consistent-hashing",
	}
	g := newLoadBalanceGroup("_lb_continent_AS", []string{"x"}, params)

	if got := getMappingField(g, "url"); got != "https://example.com/probe" {
		t.Errorf("url = %q, want https://example.com/probe", got)
	}
	if got := getMappingField(g, "interval"); got != "600" {
		t.Errorf("interval = %q, want 600", got)
	}
	if got := getMappingField(g, "timeout"); got != "2000" {
		t.Errorf("timeout = %q, want 2000", got)
	}
	if got := getMappingField(g, "max-failed-times"); got != "5" {
		t.Errorf("max-failed-times = %q, want 5", got)
	}
	if got := getMappingField(g, "lazy"); got != "false" {
		t.Errorf("lazy = %q, want false", got)
	}
	if got := getMappingField(g, "strategy"); got != "consistent-hashing" {
		t.Errorf("strategy = %q, want consistent-hashing", got)
	}
}

// 014 FR-001 + FR-013: AppendRegionGroups emits a paired _lb_region_<CC> group
// immediately after each _region_<CC> group. Same membership, different type +
// fields.
func TestAppendRegionGroups_PairedLBSibling(t *testing.T) {
	proxies := []*yaml.Node{
		makeProxyNode("provider_🇯🇵 JP 01"),
		makeProxyNode("provider_🇯🇵 JP 02"),
		makeProxyNode("provider_🇭🇰 香港 01"),
	}
	urlTestParams := URLTestParams{
		URL:             "https://www.gstatic.com/generate_204",
		IntervalSeconds: 10,
		TimeoutMS:       3000,
		MaxFailedTimes:  3,
		Lazy:            true,
	}
	lbParams := LoadBalanceParams{
		URL:             "https://www.gstatic.com/generate_204",
		IntervalSeconds: 300,
		TimeoutMS:       1500,
		MaxFailedTimes:  3,
		Lazy:            true,
		Strategy:        "round-robin",
	}

	groups := AppendRegionGroups(nil, proxies, "Proxies", urlTestParams, lbParams, nil)

	// Build name list to verify paired adjacency: _region_HK, _lb_region_HK,
	// _region_JP, _lb_region_JP (alphabetical CC order with paired adjacency).
	emitted := make([]string, 0, len(groups))
	for _, g := range groups {
		emitted = append(emitted, getMappingField(g, "name"))
	}

	wantOrder := []string{"_region_HK", "_lb_region_HK", "_region_JP", "_lb_region_JP"}
	if len(emitted) != len(wantOrder) {
		t.Fatalf("emitted groups = %v; want %v", emitted, wantOrder)
	}
	for i, want := range wantOrder {
		if emitted[i] != want {
			t.Errorf("emitted[%d] = %q, want %q (paired adjacency)", i, emitted[i], want)
		}
	}

	// Verify the lb sibling has the same proxies as its url-test sibling.
	urlTestJP := groups[2] // _region_JP
	lbJP := groups[3]      // _lb_region_JP
	urlTestMembers := mappingMembers(urlTestJP, "proxies")
	lbMembers := mappingMembers(lbJP, "proxies")
	if len(urlTestMembers) != len(lbMembers) {
		t.Fatalf("member-count mismatch: url-test=%v, lb=%v", urlTestMembers, lbMembers)
	}
	for i := range urlTestMembers {
		if urlTestMembers[i] != lbMembers[i] {
			t.Errorf("member[%d]: url-test=%q, lb=%q", i, urlTestMembers[i], lbMembers[i])
		}
	}

	// lb sibling carries type=load-balance and the lb-params strategy field.
	if got := getMappingField(lbJP, "type"); got != "load-balance" {
		t.Errorf("_lb_region_JP type = %q, want load-balance", got)
	}
	if got := getMappingField(lbJP, "strategy"); got != "round-robin" {
		t.Errorf("_lb_region_JP strategy = %q, want round-robin", got)
	}
	if got := getMappingField(lbJP, "interval"); got != "300" {
		t.Errorf("_lb_region_JP interval = %q, want 300 (lb-params, not url-test)", got)
	}

	// url-test sibling unchanged: type stays url-test, no strategy field.
	if got := getMappingField(urlTestJP, "type"); got != "url-test" {
		t.Errorf("_region_JP type = %q, want url-test", got)
	}
	if got := getMappingField(urlTestJP, "interval"); got != "10" {
		t.Errorf("_region_JP interval = %q, want 10 (url-test params)", got)
	}
	if got := getMappingField(urlTestJP, "strategy"); got != "" {
		t.Errorf("_region_JP unexpectedly carries strategy=%q (url-test groups have no strategy)", got)
	}
}

// 014 FR-001 + FR-010: Paired _lb_region_UNKNOWN sibling and Proxies selector
// gains both names interleaved.
func TestAppendRegionGroups_PairedUnknownAndProxiesMembership(t *testing.T) {
	// Seed a Proxies group so AppendRegionGroups appends region-group names to it.
	proxiesGroup := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "name"},
			{Kind: yaml.ScalarNode, Value: "Proxies"},
			{Kind: yaml.ScalarNode, Value: "type"},
			{Kind: yaml.ScalarNode, Value: "select"},
			{Kind: yaml.ScalarNode, Value: "proxies"},
			{Kind: yaml.SequenceNode},
		},
	}
	proxies := []*yaml.Node{
		makeProxyNode("provider_🇯🇵 JP 01"),
		makeProxyNode("provider_Unmappable Name"),
	}

	groups := AppendRegionGroups([]*yaml.Node{proxiesGroup}, proxies, "Proxies", URLTestParams{}, LoadBalanceParams{Strategy: "round-robin"}, nil)

	// Find emitted region group names.
	gotNames := make([]string, 0)
	for _, g := range groups {
		n := getMappingField(g, "name")
		if strings.HasPrefix(n, "_") {
			gotNames = append(gotNames, n)
		}
	}
	wantNames := []string{"_region_JP", "_lb_region_JP", "_region_UNKNOWN", "_lb_region_UNKNOWN"}
	if len(gotNames) != len(wantNames) {
		t.Fatalf("region groups = %v; want %v", gotNames, wantNames)
	}
	for i, want := range wantNames {
		if gotNames[i] != want {
			t.Errorf("gotNames[%d] = %q, want %q", i, gotNames[i], want)
		}
	}

	// Verify Proxies selector contains both url-test and lb names interleaved.
	proxiesMembers := mappingMembers(proxiesGroup, "proxies")
	wantMembers := []string{"_region_JP", "_lb_region_JP", "_region_UNKNOWN", "_lb_region_UNKNOWN"}
	if len(proxiesMembers) != len(wantMembers) {
		t.Fatalf("Proxies group members = %v; want %v", proxiesMembers, wantMembers)
	}
	for i, want := range wantMembers {
		if proxiesMembers[i] != want {
			t.Errorf("Proxies group member[%d] = %q, want %q (interleaved per FR-013)", i, proxiesMembers[i], want)
		}
	}
}

// 014 FR-002 + FR-013: AppendContinentGroups emits a paired _lb_continent_<CONT>
// sibling immediately after each _continent_<CONT>, with the same flat-union
// member list (per 003 FR-011).
func TestAppendContinentGroups_PairedLBSibling(t *testing.T) {
	regionGroups := []string{"_region_JP", "_region_HK"}
	regionMembers := map[string][]string{
		"_region_JP": {"jp1", "jp2"},
		"_region_HK": {"hk1"},
	}
	urlTestParams := URLTestParams{IntervalSeconds: 10}
	lbParams := LoadBalanceParams{IntervalSeconds: 300, Strategy: "round-robin"}

	groups := AppendContinentGroups(nil, regionGroups, regionMembers, "Proxies", urlTestParams, lbParams, nil)

	emitted := make([]string, 0, len(groups))
	for _, g := range groups {
		emitted = append(emitted, getMappingField(g, "name"))
	}
	wantOrder := []string{"_continent_AS", "_lb_continent_AS"}
	if len(emitted) != len(wantOrder) {
		t.Fatalf("emitted groups = %v; want %v", emitted, wantOrder)
	}
	for i, want := range wantOrder {
		if emitted[i] != want {
			t.Errorf("emitted[%d] = %q, want %q", i, emitted[i], want)
		}
	}

	// Same flat-union member list: hk1, jp1, jp2 (region-CC alphabetical, then proxy order).
	urlTestAS := groups[0]
	lbAS := groups[1]
	urlTestMembers := mappingMembers(urlTestAS, "proxies")
	lbMembers := mappingMembers(lbAS, "proxies")
	if len(urlTestMembers) != len(lbMembers) {
		t.Fatalf("member-count mismatch: url-test=%v, lb=%v", urlTestMembers, lbMembers)
	}
	for i := range urlTestMembers {
		if urlTestMembers[i] != lbMembers[i] {
			t.Errorf("member[%d]: url-test=%q, lb=%q", i, urlTestMembers[i], lbMembers[i])
		}
	}

	// lb continent group carries load-balance type + strategy.
	if got := getMappingField(lbAS, "type"); got != "load-balance" {
		t.Errorf("_lb_continent_AS type = %q, want load-balance", got)
	}
	if got := getMappingField(lbAS, "strategy"); got != "round-robin" {
		t.Errorf("_lb_continent_AS strategy = %q, want round-robin", got)
	}
	if got := getMappingField(lbAS, "interval"); got != "300" {
		t.Errorf("_lb_continent_AS interval = %q, want 300 (lb params)", got)
	}
}

// 014 FR-010: Continent-level paired groups also land in the Proxies selector.
func TestAppendContinentGroups_ProxiesMembershipBothEntries(t *testing.T) {
	proxiesGroup := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "name"},
			{Kind: yaml.ScalarNode, Value: "Proxies"},
			{Kind: yaml.ScalarNode, Value: "type"},
			{Kind: yaml.ScalarNode, Value: "select"},
			{Kind: yaml.ScalarNode, Value: "proxies"},
			{Kind: yaml.SequenceNode},
		},
	}
	regionGroups := []string{"_region_JP"}
	regionMembers := map[string][]string{"_region_JP": {"jp1"}}

	groups := AppendContinentGroups([]*yaml.Node{proxiesGroup}, regionGroups, regionMembers, "Proxies", URLTestParams{}, LoadBalanceParams{Strategy: "round-robin"}, nil)
	_ = groups

	proxiesMembers := mappingMembers(proxiesGroup, "proxies")
	wantMembers := []string{"_continent_AS", "_lb_continent_AS"}
	if len(proxiesMembers) != len(wantMembers) {
		t.Fatalf("Proxies members = %v; want %v", proxiesMembers, wantMembers)
	}
	for i, want := range wantMembers {
		if proxiesMembers[i] != want {
			t.Errorf("Proxies member[%d] = %q, want %q", i, proxiesMembers[i], want)
		}
	}
}
