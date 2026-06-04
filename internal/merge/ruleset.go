package merge

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// ruleset.go is the pure rule-provider transformation module for feature 016
// (RULE-SET support). It namespaces each upstream source's `rule-providers:`
// mapping (keys, the local cache `path`, and any non-built-in fetch-through
// `proxy`), drops RULE-SET rules whose provider is undefined, and merges the
// surviving, referenced providers into a single block. All functions are pure
// (Constitution Principle I): no I/O, no clock, no map-iteration ordering.

// DroppedRuleSet describes a RULE-SET rule removed because its (namespaced)
// referenced rule-provider is undefined in the source. Carried for the FR-011
// structured log; the source is known by the caller's loop.
type DroppedRuleSet struct {
	Provider string // the namespaced provider name that was missing
	Rule     string // the full rule line that was dropped
}

// SkippedRuleProvider describes a `rule-providers:` entry skipped because its
// value was not a well-formed mapping. Carried for the FR-011 structured log.
type SkippedRuleProvider struct {
	Provider string // the bare (pre-namespacing) provider key
}

// RewriteSourceRuleProviders clones one source's `rule-providers:` mapping and
// namespaces it (016 FR-002/FR-007/FR-008): every key is prefixed
// `<source>_<key>`; a present `path:` is rewritten to a source-distinct path
// derived from the namespaced key; a non-built-in `proxy:` fetch-through
// reference is prefixed `<source>_<proxy>`. All other fields are preserved
// verbatim. A provider whose value is not a mapping is skipped and reported.
// Returns nil for a nil / non-mapping input.
func RewriteSourceRuleProviders(sourceName string, rp *yaml.Node) (*yaml.Node, []SkippedRuleProvider) {
	if rp == nil || rp.Kind != yaml.MappingNode {
		return nil, nil
	}
	out := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Style: rp.Style}
	var skipped []SkippedRuleProvider
	for i := 0; i+1 < len(rp.Content); i += 2 {
		keyNode, valNode := rp.Content[i], rp.Content[i+1]
		if valNode.Kind != yaml.MappingNode {
			// 016 Edge Cases: malformed provider definition — skip + report.
			skipped = append(skipped, SkippedRuleProvider{Provider: keyNode.Value})
			continue
		}
		newKey := cloneNode(keyNode)
		newKey.Value = sourceName + "_" + keyNode.Value
		newKey.Tag = "!!str"

		newVal := cloneNode(valNode)
		// FR-008: source-distinct cache path derived from the namespaced key.
		if orig := getMappingField(newVal, "path"); orig != "" {
			setMappingField(newVal, "path", sourceDistinctPath(orig, newKey.Value))
		}
		// FR-007: prefix a non-built-in fetch-through proxy/group reference.
		if px := getMappingField(newVal, "proxy"); px != "" && !builtinTargets[px] {
			setMappingField(newVal, "proxy", sourceName+"_"+px)
		}

		out.Content = append(out.Content, newKey, newVal)
	}
	return out, skipped
}

// sourceDistinctPath replaces the basename of a rule-provider `path:` with the
// namespaced provider key, preserving the directory prefix and file extension.
// e.g. ("./ruleset/Local-IP.mrs", "alpha_Local-IP") → "./ruleset/alpha_Local-IP.mrs".
// Done with string slicing (not path.Clean) so the directory prefix — including
// a leading "./" — is preserved byte-for-byte.
func sourceDistinctPath(orig, namespacedKey string) string {
	dir, base := "", orig
	if slash := strings.LastIndex(orig, "/"); slash >= 0 {
		dir, base = orig[:slash+1], orig[slash+1:]
	}
	ext := ""
	if dot := strings.LastIndex(base, "."); dot >= 0 {
		ext = base[dot:]
	}
	return dir + namespacedKey + ext
}

// ruleProviderKeys returns the set of provider keys defined in a (namespaced)
// rule-providers mapping node.
func ruleProviderKeys(rp *yaml.Node) map[string]bool {
	out := make(map[string]bool)
	if rp == nil || rp.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(rp.Content); i += 2 {
		out[rp.Content[i].Value] = true
	}
	return out
}

// DropUnbackedRuleSetRules removes every RULE-SET rule whose (already
// namespaced) provider name is not present in providerKeys (016 FR-009). Returns
// the kept rules in original order plus a descriptor per dropped rule. Non
// RULE-SET rules and backed RULE-SET rules are returned untouched.
func DropUnbackedRuleSetRules(rules []string, providerKeys map[string]bool) (kept []string, dropped []DroppedRuleSet) {
	kept = make([]string, 0, len(rules))
	for _, r := range rules {
		parts := strings.Split(r, ",")
		if len(parts) >= 2 && parts[0] == "RULE-SET" {
			if !providerKeys[parts[1]] {
				dropped = append(dropped, DroppedRuleSet{Provider: parts[1], Rule: r})
				continue
			}
		}
		kept = append(kept, r)
	}
	return kept, dropped
}

// ReferencedRuleProviders returns the set of rule-provider names referenced by
// RULE-SET rules in the (final) rule slice — i.e. field[1] of every
// `RULE-SET,<name>,...` line.
func ReferencedRuleProviders(rules []string) map[string]bool {
	out := make(map[string]bool)
	for _, r := range rules {
		parts := strings.Split(r, ",")
		if len(parts) >= 2 && parts[0] == "RULE-SET" {
			out[parts[1]] = true
		}
	}
	return out
}

// MergeRuleProviders appends every source's namespaced provider key/value pairs
// (in the given order) into one mapping node, keeping only keys present in the
// referenced set (016 FR-005/FR-010). Returns nil when nothing is kept so the
// output adapter omits the `rule-providers:` key entirely (016 FR-006).
func MergeRuleProviders(perSource []*yaml.Node, referenced map[string]bool) *yaml.Node {
	out := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, src := range perSource {
		if src == nil || src.Kind != yaml.MappingNode {
			continue
		}
		for i := 0; i+1 < len(src.Content); i += 2 {
			key, val := src.Content[i], src.Content[i+1]
			if referenced[key.Value] {
				out.Content = append(out.Content, key, val)
			}
		}
	}
	if len(out.Content) == 0 {
		return nil
	}
	return out
}
