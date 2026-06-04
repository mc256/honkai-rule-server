package merge

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// builtinTargets are the identifiers exempt from prefixing in group member lists
// and rule targets. Per R2, comparison is case-sensitive.
var builtinTargets = map[string]bool{
	"DIRECT":       true,
	"REJECT":       true,
	"REJECT-DROP":  true,
	"PASS":         true,
}

// ruleModifiers are the optional trailing fields in Mihomo rules that are NOT
// the target. Per R3, the target is the rightmost comma-separated field that is
// NOT a known modifier.
var ruleModifiers = map[string]bool{
	"no-resolve": true,
	"src":        true,
	"dport":      true,
}

// RewriteSource applies provider-prefix namespacing to every proxy, proxy-group,
// and rule target from a single upstream source (FR-004/FR-005/FR-006).
// Returns cloned nodes; original inputs are not mutated.
func RewriteSource(
	sourceName string,
	proxies, groups []*yaml.Node,
	rules []string,
) (newProxies, newGroups []*yaml.Node, newRules []string) {
	// Rewrite proxies: clone each, rewrite name field.
	newProxies = make([]*yaml.Node, 0, len(proxies))
	for _, p := range proxies {
		clone := cloneNode(p)
		origName := getMappingField(clone, "name")
		if origName != "" {
			setMappingField(clone, "name", sourceName+"_"+origName)
		}
		newProxies = append(newProxies, clone)
	}

	// Rewrite groups: clone each, rewrite name + member lists.
	newGroups = make([]*yaml.Node, 0, len(groups))
	for _, g := range groups {
		clone := cloneNode(g)
		origName := getMappingField(clone, "name")
		if origName != "" {
			setMappingField(clone, "name", sourceName+"_"+origName)
		}
		// Rewrite member lists (proxies field for select/url-test/fallback/load-balance/relay).
		rewriteGroupMembers(clone, sourceName)
		newGroups = append(newGroups, clone)
	}

	// Rewrite rule targets.
	newRules = make([]string, 0, len(rules))
	for _, r := range rules {
		newRules = append(newRules, rewriteRuleTarget(r, sourceName))
	}

	return newProxies, newGroups, newRules
}

// RewriteOwn applies leading underscore prefix to own-proxies and own-groups
// (FR-007a/FR-007b). Returns cloned nodes; original inputs are not mutated.
func RewriteOwn(
	ownProxies, ownGroups []*yaml.Node,
) (newProxies, newGroups []*yaml.Node) {
	// Build lookup maps for own-proxy/own-group names (used for member ref rewrite).
	ownProxyNames := make(map[string]bool, len(ownProxies))
	for _, p := range ownProxies {
		name := getMappingField(p, "name")
		if name != "" {
			ownProxyNames[name] = true
		}
	}
	ownGroupNames := make(map[string]bool, len(ownGroups))
	for _, g := range ownGroups {
		name := getMappingField(g, "name")
		if name != "" {
			ownGroupNames[name] = true
		}
	}

	// Rewrite own-proxies: clone each, prefix name with single underscore.
	newProxies = make([]*yaml.Node, 0, len(ownProxies))
	for _, p := range ownProxies {
		clone := cloneNode(p)
		origName := getMappingField(clone, "name")
		if origName != "" {
			setMappingField(clone, "name", "_"+origName)
		}
		newProxies = append(newProxies, clone)
	}

	// Rewrite own-groups: clone each, prefix name + rewrite member refs.
	newGroups = make([]*yaml.Node, 0, len(ownGroups))
	for _, g := range ownGroups {
		clone := cloneNode(g)
		origName := getMappingField(clone, "name")
		if origName != "" {
			setMappingField(clone, "name", "_"+origName)
		}
		rewriteOwnGroupMembers(clone, ownProxyNames, ownGroupNames)
		newGroups = append(newGroups, clone)
	}

	return newProxies, newGroups
}

// rewriteGroupMembers rewrites every entry in a group's `proxies` member list
// to the prefixed form, except built-in identifiers.
func rewriteGroupMembers(group *yaml.Node, sourceName string) {
	members := mappingMembers(group, "proxies")
	if len(members) == 0 {
		return
	}
	newMembers := make([]string, 0, len(members))
	for _, m := range members {
		if builtinTargets[m] {
			newMembers = append(newMembers, m)
		} else {
			newMembers = append(newMembers, sourceName+"_"+m)
		}
	}
	setMappingMembers(group, "proxies", newMembers)
}

// rewriteOwnGroupMembers rewrites member refs inside an own-group.
// Entries that refer to own-proxies or other own-groups get underscore prefix.
// Built-ins and upstream refs are passed through unchanged.
func rewriteOwnGroupMembers(group *yaml.Node, ownProxyNames, ownGroupNames map[string]bool) {
	members := mappingMembers(group, "proxies")
	if len(members) == 0 {
		return
	}
	newMembers := make([]string, 0, len(members))
	for _, m := range members {
		if builtinTargets[m] {
			newMembers = append(newMembers, m)
		} else if ownProxyNames[m] || ownGroupNames[m] {
			newMembers = append(newMembers, "_"+m)
		} else {
			// Upstream ref or unknown name — pass through unchanged.
			newMembers = append(newMembers, m)
		}
	}
	setMappingMembers(group, "proxies", newMembers)
}

// rewriteRuleTarget rewrites the target field of a Mihomo rule, plus — for
// RULE-SET rules (016 FR-003) — the rule-provider name field (parts[1]).
// The target is the rightmost comma-separated field that is NOT a known modifier.
func rewriteRuleTarget(rule, sourceName string) string {
	parts := strings.Split(rule, ",")
	if len(parts) < 2 {
		return rule // malformed; pass through unchanged
	}

	// 016 FR-003: RULE-SET's field[1] is the rule-provider name, never the
	// target. Prefix it unconditionally (provider names are never built-ins).
	// The target (a later field) is still handled by the scan below.
	isRuleSet := parts[0] == "RULE-SET"
	if isRuleSet {
		parts[1] = sourceName + "_" + parts[1]
	}

	// Find the target: last field that is NOT a modifier.
	targetIdx := len(parts) - 1
	for i := len(parts) - 1; i >= 1; i-- {
		if ruleModifiers[parts[i]] {
			targetIdx = i - 1
		} else {
			break
		}
	}

	// 016 I1 guard: for RULE-SET, never treat field[1] (the provider) as the
	// target. A malformed 2-field `RULE-SET,Name` has no target to rewrite, so
	// return with only the provider prefix applied (no double prefix).
	if isRuleSet && targetIdx <= 1 {
		return strings.Join(parts, ",")
	}

	target := parts[targetIdx]
	if builtinTargets[target] {
		// Built-in target: don't prefix it. Re-join rather than returning the
		// original `rule` so a RULE-SET provider-field prefix is preserved.
		return strings.Join(parts, ",")
	}

	// Rewrite the target field.
	parts[targetIdx] = sourceName + "_" + target
	return strings.Join(parts, ",")
}