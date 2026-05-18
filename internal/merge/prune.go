package merge

import (
	"gopkg.in/yaml.v3"
)

// --- Feature 015: prune empty proxy-groups for Mihomo compatibility ---
//
// Mihomo rejects a configuration that contains a proxy-group with an empty
// member list, and rejects references to a proxy-group name that is not
// defined. PruneEmptyProxyGroups removes empty proxy-groups from the merged
// configuration and cleans up the member/rule references the removals leave
// behind, so the served config loads in the Mihomo client.
//
// This file is part of the pure transformation core (Constitution Principle
// I): no I/O, no clock, deterministic given its inputs.

// RuleRetarget records one routing rule whose target proxy-group was pruned
// and was therefore redirected to the fallback rule target (015 FR-008).
type RuleRetarget struct {
	RuleIndex int    // index into the rules slice
	OldTarget string // the removed group name the rule used to name
	NewTarget string // the fallback rule target it was rewritten to
}

// PruneResult records the changes PruneEmptyProxyGroups made, for the
// structured logging required by 015 FR-011 / Constitution Principle V.
type PruneResult struct {
	RemovedGroups []string       // names of proxy-groups removed for being empty
	Retargets     []RuleRetarget // rules redirected to the fallback target
}

// knownRuleOptions are trailing fields that may follow the target field in a
// Clash/Mihomo rule line. When the last comma-separated field is one of
// these, the target is the second-to-last field instead. `no-resolve` is the
// only standard per-rule option Mihomo places after the target.
var knownRuleOptions = map[string]bool{
	"no-resolve": true,
}

// PruneEmptyProxyGroups removes every proxy-group whose `proxies` member list
// is empty (015 FR-001/FR-002), with the single exception of the
// always-present `Proxies` selector and the fallback-rule-target group, which
// are never removed (FR-007). It then drops member references to removed
// groups from every surviving group (FR-006) and redirects any routing rule
// whose target group was removed to fallbackRuleTarget (FR-008). Removal is a
// single pass — emptiness is not re-evaluated after reference cleanup, so
// cascading removal through nested groups is out of scope (FR-005).
//
// When no group is empty the inputs are returned untouched so the served
// output is byte-for-byte unchanged (FR-010). Surviving group nodes are
// mutated in place during member-reference cleanup; rule strings are returned
// in a freshly allocated slice. The function is pure and deterministic.
func PruneEmptyProxyGroups(
	groups []*yaml.Node,
	rules []string,
	proxiesGroupName string,
	fallbackRuleTarget string,
) (prunedGroups []*yaml.Node, prunedRules []string, result PruneResult) {
	if proxiesGroupName == "" {
		proxiesGroupName = "Proxies"
	}

	// Protected names: the always-present `Proxies` selector (FR-007) and,
	// when the fallback rule target names an existing proxy-group, that group
	// too — so the FR-008 retarget always lands on a group present in the
	// served output. Mihomo built-in targets (DIRECT/REJECT/PASS) are not
	// proxy-groups, so they never match a group name here.
	protected := map[string]bool{proxiesGroupName: true}
	for _, g := range groups {
		if getMappingField(g, "name") == fallbackRuleTarget {
			protected[fallbackRuleTarget] = true
			break
		}
	}

	// Pass 1 (FR-001..FR-005): single removal pass. Keep every non-empty
	// group plus every protected group, in original order.
	removed := make(map[string]bool)
	prunedGroups = make([]*yaml.Node, 0, len(groups))
	for _, g := range groups {
		name := getMappingField(g, "name")
		if !protected[name] && len(mappingMembers(g, "proxies")) == 0 {
			removed[name] = true
			result.RemovedGroups = append(result.RemovedGroups, name)
			continue
		}
		prunedGroups = append(prunedGroups, g)
	}

	// No empty groups → return the inputs untouched (FR-010 byte-stability).
	if len(removed) == 0 {
		return groups, rules, result
	}

	// Pass 2 (FR-006): drop member references to removed groups from every
	// surviving group. Emptiness is NOT re-evaluated (single pass, FR-005).
	for _, g := range prunedGroups {
		members := mappingMembers(g, "proxies")
		kept := make([]string, 0, len(members))
		changed := false
		for _, m := range members {
			if removed[m] {
				changed = true
				continue
			}
			kept = append(kept, m)
		}
		if changed {
			setMappingMembers(g, "proxies", kept)
		}
	}

	// Pass 3 (FR-008): redirect any rule whose target group was removed to
	// the fallback rule target. Rewrites in place — rule count is unchanged,
	// so the caller's parallel priority/contributor slices stay aligned.
	prunedRules = make([]string, len(rules))
	copy(prunedRules, rules)
	for i, rule := range prunedRules {
		target, start, end, ok := ruleTarget(rule)
		if !ok || !removed[target] {
			continue
		}
		prunedRules[i] = rule[:start] + fallbackRuleTarget + rule[end:]
		result.Retargets = append(result.Retargets, RuleRetarget{
			RuleIndex: i,
			OldTarget: target,
			NewTarget: fallbackRuleTarget,
		})
	}

	return prunedGroups, prunedRules, result
}

// ruleTarget locates the target field of a Clash/Mihomo rule line and returns
// the target value together with its [start,end) byte range within rule.
//
// Rules are comma-separated `TYPE,PAYLOAD,TARGET[,OPTION...]`. For
// `MATCH,TARGET` the target is field index 1. For every other rule type the
// target is the last comma-separated field, unless that last field is a known
// trailing option (e.g. `no-resolve`), in which case it is the second-to-last
// field. Logical rules (AND/OR/NOT) embed commas inside a parenthesised
// payload, but the target itself never contains a comma, so "last field"
// remains correct for them.
//
// ok is false for a rule with fewer than two fields (no target to locate).
func ruleTarget(rule string) (target string, start, end int, ok bool) {
	// Collect [start,end) byte ranges of every comma-separated field.
	type span struct{ start, end int }
	spans := make([]span, 0, 4)
	fieldStart := 0
	for i := 0; i < len(rule); i++ {
		if rule[i] == ',' {
			spans = append(spans, span{fieldStart, i})
			fieldStart = i + 1
		}
	}
	spans = append(spans, span{fieldStart, len(rule)})

	if len(spans) < 2 {
		return "", 0, 0, false
	}

	if rule[spans[0].start:spans[0].end] == "MATCH" {
		s := spans[1]
		return rule[s.start:s.end], s.start, s.end, true
	}

	last := spans[len(spans)-1]
	if knownRuleOptions[rule[last.start:last.end]] && len(spans) >= 3 {
		s := spans[len(spans)-2]
		return rule[s.start:s.end], s.start, s.end, true
	}
	return rule[last.start:last.end], last.start, last.end, true
}
