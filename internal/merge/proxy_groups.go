package merge

import (
	"gopkg.in/yaml.v3"
)

// GroupConflict records an attribute-value disagreement when merging two
// proxy-groups with the same name. The value from the highest-priority
// source wins (`Chosen`); the conflict is logged so operators can investigate.
type GroupConflict struct {
	GroupName string
	Attribute string
	Values    []GroupConflictValue // ordered: existing (chosen), incoming
	Chosen    string               // source name whose value won
}

// GroupConflictValue is one (source, value) pair in a conflict.
type GroupConflictValue struct {
	Source string
	Value  string
}

// AppendProxiesGroup ensures the merged proxy-group list contains a
// `select`-type group named groupName whose members include every entry in
// proxyNames. If a group with that name already exists (e.g., an upstream
// or own-proxies file declared it), its member list is augmented (union,
// dedup, original ordering preserved + new members appended). Otherwise
// a new group is appended to the end.
//
// Per FR-009a: this guarantees client UI selectability even when no
// upstream contributed a select group containing every proxy.
func AppendProxiesGroup(groups []*yaml.Node, proxyNames []string, groupName string) []*yaml.Node {
	if groupName == "" {
		groupName = "Proxies"
	}
	for _, g := range groups {
		if getMappingField(g, "name") == groupName {
			existing := mappingMembers(g, "proxies")
			seen := make(map[string]bool, len(existing))
			for _, m := range existing {
				seen[m] = true
			}
			for _, n := range proxyNames {
				if !seen[n] {
					existing = append(existing, n)
					seen[n] = true
				}
			}
			setMappingMembers(g, "proxies", existing)
			return groups
		}
	}
	g := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	setMappingValue(g, "name", &yaml.Node{Kind: yaml.ScalarNode, Value: groupName, Tag: "!!str"})
	setMappingValue(g, "type", &yaml.Node{Kind: yaml.ScalarNode, Value: "select", Tag: "!!str"})
	setMappingMembers(g, "proxies", proxyNames)
	return append(groups, g)
}

// attributesToCompare lists the proxy-group fields we check for cross-source
// conflicts. The list is intentionally small; rare attributes are tolerated
// silently — operators can extend the list if false negatives appear.
var attributesToCompare = []string{"type", "url", "interval", "tolerance", "lazy"}

// MergeProxyGroups returns the merged list of proxy-groups across own +
// every upstream source. Same-named groups are collapsed into a single
// output group whose member list is the deduplicated union of all members
// (own first, then sources in priority desc); group attributes (`type`,
// `url`, `interval`, etc.) come from the first (highest-priority) source
// to define the group, with conflicts recorded for logging (FR-008a).
//
// After 002's provider-prefix namespacing (FR-005), cross-source same-name
// groups are structurally impossible — every upstream group already carries
// `<provider>_` prefix. The same-name union path remains live for own-groups
// vs upstream groups that happen to share a name after prefixing.
//
// The always-present `Proxies` group required by FR-009a is added in US2;
// this function performs only the union-by-name merge.
func MergeProxyGroups(
	perSource map[string][]*yaml.Node,
	sortedSources []string,
	own []*yaml.Node,
) ([]*yaml.Node, []GroupConflict) {
	merged := make([]*yaml.Node, 0)
	byName := make(map[string]*yaml.Node)
	chosenSource := make(map[string]string)
	conflicts := make([]GroupConflict, 0)

	add := func(g *yaml.Node, source string) {
		clone := cloneNode(g)
		name := getMappingField(clone, "name")
		if name == "" {
			return
		}
		existing, present := byName[name]
		if !present {
			merged = append(merged, clone)
			byName[name] = clone
			chosenSource[name] = source
			return
		}
		// Same-name → union members + log attribute conflicts.
		existingMembers := mappingMembers(existing, "proxies")
		seen := make(map[string]bool, len(existingMembers))
		for _, m := range existingMembers {
			seen[m] = true
		}
		for _, m := range mappingMembers(clone, "proxies") {
			if !seen[m] {
				existingMembers = append(existingMembers, m)
				seen[m] = true
			}
		}
		setMappingMembers(existing, "proxies", existingMembers)

		for _, attr := range attributesToCompare {
			ev := getMappingField(existing, attr)
			nv := getMappingField(clone, attr)
			if ev == "" || nv == "" || ev == nv {
				continue
			}
			conflicts = append(conflicts, GroupConflict{
				GroupName: name,
				Attribute: attr,
				Values: []GroupConflictValue{
					{Source: chosenSource[name], Value: ev},
					{Source: source, Value: nv},
				},
				Chosen: chosenSource[name],
			})
		}
	}

	for _, g := range own {
		add(g, "own")
	}
	for _, source := range sortedSources {
		for _, g := range perSource[source] {
			add(g, source)
		}
	}

	return merged, conflicts
}
