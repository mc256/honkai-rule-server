package merge

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// AppendFanoutProxies generates fan-out copies of operator-declared own-proxies
// (post-RewriteOwn `_<original>` form) for use as Mihomo dialer chains.
//
// For every own-proxy that does NOT already declare a `dialer-proxy` field,
// the function emits:
//   - One AUTO copy named `via_AUTO__<P>` whose `dialer-proxy` is the literal
//     string `proxiesGroupName` (default "Proxies" — the always-present
//     global selector group from 001 FR-009a).
//   - One per-target-group copy named `via_<G>__<P>` per `mergedGroups` entry
//     whose `name` starts with `_region_` or `_continent_`. `<G>` is the
//     target group's name with its single leading `_` stripped; `<P>` is the
//     own-proxy's name with its leading `_` stripped. Each per-group copy's
//     `dialer-proxy` is the FULL target group name (with leading `_`).
//
// Own-proxies that already declare `dialer-proxy` are skipped entirely (no
// AUTO, no per-group) per FR-005, and counted toward the returned `skipped`.
//
// Ordering (FR-006): outer = `ownProxies` slice order; inner = AUTO first,
// then `mergedGroups` in slice order (filtered to `_region_*`/`_continent_*`).
//
// All returned nodes are deep clones of the source own-proxy with `name` and
// `dialer-proxy` rewritten via `setMappingValue`. Source nodes are not
// mutated. Field order in the clone follows `setMappingValue`'s "replace in
// place if present, else append at end" semantics; for own-proxies that lack
// a `dialer-proxy` field (the only kind that reach this loop) the new
// `dialer-proxy` is appended at the tail.
func AppendFanoutProxies(
	ownProxies []*yaml.Node,
	mergedGroups []*yaml.Node,
	proxiesGroupName string,
) (fanout []*yaml.Node, skipped int) {
	if proxiesGroupName == "" {
		proxiesGroupName = "Proxies"
	}

	// Pre-collect target groups (region/continent + lb variants) in
	// mergedGroups order. Per 014 FR-014a, the predicate widens from 008's
	// original `_region_` / `_continent_` to also include the lb-prefixed
	// siblings emitted by 014 (`_lb_region_` / `_lb_continent_`), so the
	// existing fan-out machinery produces `via_lb_region_<CC>__<own>` and
	// `via_lb_continent_<CONT>__<own>` copies.
	targetGroupNames := make([]string, 0, len(mergedGroups))
	for _, g := range mergedGroups {
		name := getMappingField(g, "name")
		if strings.HasPrefix(name, "_region_") ||
			strings.HasPrefix(name, "_continent_") ||
			strings.HasPrefix(name, "_lb_region_") ||
			strings.HasPrefix(name, "_lb_continent_") {
			targetGroupNames = append(targetGroupNames, name)
		}
	}

	for _, own := range ownProxies {
		if own == nil || own.Kind != yaml.MappingNode {
			continue
		}
		ownName := getMappingField(own, "name")
		if ownName == "" {
			continue
		}
		// FR-005: own-proxy already chose its dialer chain — skip fan-out.
		if getMappingField(own, "dialer-proxy") != "" {
			skipped++
			continue
		}
		strippedOwn := stripUnderscore(ownName)

		// AUTO copy first (FR-004a).
		auto := cloneNode(own)
		setMappingValue(auto, "name", scalarString("via_AUTO__"+strippedOwn))
		setMappingValue(auto, "dialer-proxy", scalarString(proxiesGroupName))
		fanout = append(fanout, auto)

		// Per-target-group copies in mergedGroups order (FR-006).
		for _, groupName := range targetGroupNames {
			clone := cloneNode(own)
			setMappingValue(clone, "name", scalarString("via_"+stripUnderscore(groupName)+"__"+strippedOwn))
			setMappingValue(clone, "dialer-proxy", scalarString(groupName))
			fanout = append(fanout, clone)
		}
	}
	return fanout, skipped
}

// stripUnderscore removes a single leading "_" from s, or returns s unchanged.
func stripUnderscore(s string) string {
	if strings.HasPrefix(s, "_") {
		return s[1:]
	}
	return s
}

// scalarString returns a yaml.Node representing a plain string scalar.
func scalarString(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Value: v, Tag: "!!str"}
}
