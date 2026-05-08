package merge

import (
	"gopkg.in/yaml.v3"
)

// ProxyCollision records one same-name collision detected during merge.
// `Sources` lists every source that contributed an entry with that name
// (in order: own → upstream by priority desc); `Resolution` is the new
// name applied to the suffixed entry (e.g., `name@source`).
type ProxyCollision struct {
	ProxyName  string
	Sources    []string
	Resolution string
}

// MergeProxies returns the union of all upstream proxies + own-proxies with
// per-source name-collision suffixes. Per FR-002 (deterministic strategy)
// and FR-008 (own-proxies preserve their identity).
//
// After 002's provider-prefix namespacing (FR-004), cross-source name collisions
// are structurally impossible — every upstream proxy already carries `<provider>_`
// prefix. The collision-suffix path `<name>@<source>` remains live only for:
// (a) own-proxy vs upstream same-name collisions (own-proxies keep their name,
// upstream gets the suffix), and (b) intra-source duplicates (which remain a
// loud-fail condition per 001 FR-001b, not handled here).
//
// `sortedSources` is the per-priority-desc order of source names; the
// merge processes own-proxies first (always keep their names), then each
// source in priority order. A proxy whose name collides with an already-
// used name is suffixed `<name>@<source>`.
//
// Inputs are deep-cloned before mutation so cache nodes remain pristine.
func MergeProxies(
	perSource map[string][]*yaml.Node,
	sortedSources []string,
	own []*yaml.Node,
) ([]*yaml.Node, []ProxyCollision) {
	merged := make([]*yaml.Node, 0)
	used := make(map[string]bool)
	firstSource := make(map[string]string) // name → first source that claimed it
	collisions := make([]ProxyCollision, 0)

	// Own-proxies first; they always keep their name on collision.
	for _, p := range own {
		clone := cloneNode(p)
		name := getMappingField(clone, "name")
		if name == "" {
			continue
		}
		used[name] = true
		firstSource[name] = "own"
		merged = append(merged, clone)
	}

	// Upstream proxies; suffix on collision.
	for _, source := range sortedSources {
		for _, p := range perSource[source] {
			clone := cloneNode(p)
			name := getMappingField(clone, "name")
			if name == "" {
				continue
			}
			finalName := name
			if used[name] {
				finalName = name + "@" + source
				setMappingField(clone, "name", finalName)
				collisions = append(collisions, ProxyCollision{
					ProxyName:  name,
					Sources:    []string{firstSource[name], source},
					Resolution: finalName,
				})
			} else {
				firstSource[name] = source
			}
			used[finalName] = true
			merged = append(merged, clone)
		}
	}

	return merged, collisions
}
