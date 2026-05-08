package merge

import (
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// URLTestParams mirrors config.URLTestParams locally so internal/merge/
// stays free of internal/config imports per Constitution Principle I.
// Field semantics match 012 FR-003 / FR-004.
type URLTestParams struct {
	URL             string
	IntervalSeconds int
	TimeoutMS       int
	MaxFailedTimes  int
	Lazy            bool
}

// newURLTestGroup constructs a url-test-type proxy group with the five
// health-check fields populated from params per 012 FR-001..FR-004.
// Field-emission order matches 004's block-style convention plus 012
// FR-007's url-test extension; the output formatter's
// reorderProxyGroupFields pass enforces the final on-the-wire order.
func newURLTestGroup(name string, members []string, params URLTestParams) *yaml.Node {
	g := &yaml.Node{Kind: yaml.MappingNode}
	setMappingValue(g, "name", &yaml.Node{Kind: yaml.ScalarNode, Value: name})
	setMappingValue(g, "type", &yaml.Node{Kind: yaml.ScalarNode, Value: "url-test"})
	setMappingMembers(g, "proxies", members)
	setMappingValue(g, "url", &yaml.Node{Kind: yaml.ScalarNode, Value: params.URL})
	setMappingValue(g, "interval", &yaml.Node{Kind: yaml.ScalarNode, Value: strconv.Itoa(params.IntervalSeconds), Tag: "!!int"})
	setMappingValue(g, "timeout", &yaml.Node{Kind: yaml.ScalarNode, Value: strconv.Itoa(params.TimeoutMS), Tag: "!!int"})
	setMappingValue(g, "max-failed-times", &yaml.Node{Kind: yaml.ScalarNode, Value: strconv.Itoa(params.MaxFailedTimes), Tag: "!!int"})
	setMappingValue(g, "lazy", &yaml.Node{Kind: yaml.ScalarNode, Value: strconv.FormatBool(params.Lazy), Tag: "!!bool"})
	return g
}

// AppendRegionGroups examines upstream-prefixed proxies, infers country codes
// from their original display names, and emits _region_<CC> proxy-groups.
// Own-proxies (names starting with "_") are excluded per FR-012.
// Each emitted region group is also added to the proxiesGroupName group's
// member list so it appears in the client UI.
// Unclassified upstream proxies are collected into _region_UNKNOWN (FR-014).
// Per 012 FR-001, emitted groups are type=url-test with health-check fields
// from params.
func AppendRegionGroups(
	groups []*yaml.Node,
	upstreamPrefixedProxies []*yaml.Node,
	proxiesGroupName string,
	urlTestParams URLTestParams,
	unmappedLogger func(fragment string),
) []*yaml.Node {
	ccToProxies := make(map[string][]string)
	unclassifiedProxies := make([]string, 0)

	for _, p := range upstreamPrefixedProxies {
		name := getMappingField(p, "name")
		if name == "" || strings.HasPrefix(name, "_") {
			continue
		}

		originalDisplayName := stripSourcePrefix(name)
		code, ok := inferCountry(originalDisplayName)
		if ok {
			ccToProxies[code] = append(ccToProxies[code], name)
		} else {
			unclassifiedProxies = append(unclassifiedProxies, name)
			if unmappedLogger != nil {
				unmappedLogger(originalDisplayName)
			}
		}
	}

	// Sort CCs for deterministic output (FR-016)
	ccs := make([]string, 0, len(ccToProxies))
	for cc := range ccToProxies {
		ccs = append(ccs, cc)
	}
	sort.Strings(ccs)

	regionGroupNames := make([]string, 0, len(ccs))

	for _, cc := range ccs {
		members := ccToProxies[cc]
		groupName := "_region_" + cc
		regionGroupNames = append(regionGroupNames, groupName)

		groups = append(groups, newURLTestGroup(groupName, members, urlTestParams))
	}

	// Emit _region_UNKNOWN for unclassified upstream proxies (FR-014).
	// Per 012 FR-001's prefix rule, this group is also type=url-test.
	if len(unclassifiedProxies) > 0 {
		unknownGroupName := "_region_UNKNOWN"
		regionGroupNames = append(regionGroupNames, unknownGroupName)

		groups = append(groups, newURLTestGroup(unknownGroupName, unclassifiedProxies, urlTestParams))
	}

	// Add region group names to the Proxies group's member list (FR-015)
	if len(regionGroupNames) > 0 && proxiesGroupName != "" {
		for _, g := range groups {
			if getMappingField(g, "name") == proxiesGroupName {
				existing := mappingMembers(g, "proxies")
				seen := make(map[string]bool, len(existing))
				for _, m := range existing {
					seen[m] = true
				}
				for _, name := range regionGroupNames {
					if !seen[name] {
						existing = append(existing, name)
						seen[name] = true
					}
				}
				setMappingMembers(g, "proxies", existing)
				break
			}
		}
	}

	return groups
}

// AppendContinentGroups creates _continent_<CONT> proxy groups by aggregating
// _region_<CC> groups via a country-to-continent mapping (FR-013).
// Each continent group contains all proxies from its constituent region groups,
// ordered by region code alphabetical, then proxy order within region.
// Continent group names are appended to the proxiesGroupName group's member list.
// Per 012 FR-002, emitted groups are type=url-test with health-check fields
// from urlTestParams.
func AppendContinentGroups(
	groups []*yaml.Node,
	regionGroupNames []string,
	regionGroupMembers map[string][]string,
	proxiesGroupName string,
	urlTestParams URLTestParams,
	unmappedContinentLogger func(cc string),
) []*yaml.Node {
	// Collect proxies per continent
	contToProxies := make(map[string][]string)
	contToRegions := make(map[string][]string) // track which regions contributed

	for _, regionName := range regionGroupNames {
		// Extract CC from "_region_<CC>"
		if len(regionName) < 8 || regionName[:8] != "_region_" {
			continue
		}
		cc := regionName[8:]
		// Skip _region_UNKNOWN — not a real country code
		if cc == "UNKNOWN" {
			continue
		}
		members := regionGroupMembers[regionName]

		cont, ok := continentOf(cc)
		if !ok {
			if unmappedContinentLogger != nil {
				unmappedContinentLogger(cc)
			}
			continue
		}

		// Track regions for this continent (for deterministic ordering)
		if !contains(contToRegions[cont], cc) {
			contToRegions[cont] = append(contToRegions[cont], cc)
		}
		// Add proxies from this region to continent
		contToProxies[cont] = append(contToProxies[cont], members...)
	}

	// Sort continents alphabetically for deterministic output
	conts := make([]string, 0, len(contToProxies))
	for cont := range contToProxies {
		conts = append(conts, cont)
	}
	sort.Strings(conts)

	continentGroupNames := make([]string, 0, len(conts))

	for _, cont := range conts {
		groupName := "_continent_" + cont
		continentGroupNames = append(continentGroupNames, groupName)

		// Rebuild member list with region-grouped ordering:
		// For each region alphabetically, append that region's proxies
		regions := contToRegions[cont]
		sort.Strings(regions)

		membersOrdered := make([]string, 0)
		for _, cc := range regions {
			regionName := "_region_" + cc
			membersOrdered = append(membersOrdered, regionGroupMembers[regionName]...)
		}

		groups = append(groups, newURLTestGroup(groupName, membersOrdered, urlTestParams))
	}

	// Add continent group names to the Proxies group's member list
	if len(continentGroupNames) > 0 && proxiesGroupName != "" {
		for _, g := range groups {
			if getMappingField(g, "name") == proxiesGroupName {
				existing := mappingMembers(g, "proxies")
				seen := make(map[string]bool, len(existing))
				for _, m := range existing {
					seen[m] = true
				}
				for _, name := range continentGroupNames {
					if !seen[name] {
						existing = append(existing, name)
						seen[name] = true
					}
				}
				setMappingMembers(g, "proxies", existing)
				break
			}
		}
	}

	return groups
}

// contains checks if a string is in a slice
func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// stripSourcePrefix removes the provider prefix from a prefixed proxy name.
// Given "alpha_🇭🇰 香港 01", returns "🇭🇰 香港 01".
// Splits on the first underscore only.
func stripSourcePrefix(prefixedName string) string {
	idx := strings.Index(prefixedName, "_")
	if idx < 0 {
		return prefixedName
	}
	return prefixedName[idx+1:]
}
