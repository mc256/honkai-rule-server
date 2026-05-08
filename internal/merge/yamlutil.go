// Package merge is the unified transformation core (Constitution Principle I).
// All exported functions are pure — no I/O, no time.Now(), no map iteration
// order assumptions. Callers inject any nondeterminism (the cache, the clock).
package merge

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// docRoot returns the root mapping of a parsed yaml.Node, descending through
// a DocumentNode wrapper if present.
func docRoot(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return n.Content[0]
	}
	return n
}

// findChildSequence returns the sequence Node value of a top-level mapping
// key, or nil if absent / the wrong kind.
func findChildSequence(root *yaml.Node, key string) *yaml.Node {
	if root == nil || root.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		if k.Value == key && v.Kind == yaml.SequenceNode {
			return v
		}
	}
	return nil
}

// getMappingField returns the scalar value of a key in a mapping node,
// or "" if absent / the wrong kind.
func getMappingField(n *yaml.Node, key string) string {
	if n == nil || n.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Value == key && v.Kind == yaml.ScalarNode {
			return v.Value
		}
	}
	return ""
}

// setMappingField sets the scalar value of a key in a mapping node. The key
// must already exist; this is used to update a known field (e.g., name suffix).
func setMappingField(n *yaml.Node, key, value string) bool {
	if n == nil || n.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Value == key {
			v.Value = value
			v.Tag = "!!str"
			return true
		}
	}
	return false
}

// getMappingNode returns the value node of a key in a mapping node, or nil.
func getMappingNode(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		if k.Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// setMappingValue sets the value of a key (creates it if absent). The new
// value replaces whatever node was there; the key is added at the end if new.
func setMappingValue(n *yaml.Node, key string, value *yaml.Node) {
	if n == nil || n.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		if k.Value == key {
			n.Content[i+1] = value
			return
		}
	}
	n.Content = append(n.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"},
		value,
	)
}

// cloneNode produces a deep copy of a yaml.Node tree. The merge layer mutates
// nodes (e.g., suffixing names, unioning member lists); the cache's nodes
// must not be touched, so every node entering the pipeline is cloned first.
func cloneNode(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	out := &yaml.Node{
		Kind:        n.Kind,
		Style:       n.Style,
		Tag:         n.Tag,
		Value:       n.Value,
		Anchor:      n.Anchor,
		Alias:       n.Alias,
		HeadComment: n.HeadComment,
		LineComment: n.LineComment,
		FootComment: n.FootComment,
		Line:        n.Line,
		Column:      n.Column,
	}
	if len(n.Content) > 0 {
		out.Content = make([]*yaml.Node, len(n.Content))
		for i, c := range n.Content {
			out.Content[i] = cloneNode(c)
		}
	}
	return out
}

// mappingMembers returns the string members of a sequence-of-strings under
// `key` in a mapping node. Used for proxy-group `proxies:` member lists.
func mappingMembers(n *yaml.Node, key string) []string {
	seq := getMappingNode(n, key)
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]string, 0, len(seq.Content))
	for _, c := range seq.Content {
		if c.Kind == yaml.ScalarNode {
			out = append(out, c.Value)
		}
	}
	return out
}

// setMappingMembers replaces the sequence-of-strings under `key` with the
// given member list. If `key` is absent, it is appended to the mapping.
func setMappingMembers(n *yaml.Node, key string, members []string) {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, m := range members {
		seq.Content = append(seq.Content, &yaml.Node{
			Kind: yaml.ScalarNode, Value: m, Tag: "!!str",
		})
	}
	setMappingValue(n, key, seq)
}

// mustParseYAMLNode parses a single YAML document into the root node of its
// content (skipping the DocumentNode wrapper). Test-helper only — panics on
// error so it's not appropriate for production parsing.
func mustParseYAMLNode(raw string) *yaml.Node {
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &n); err != nil {
		panic(fmt.Sprintf("mustParseYAMLNode: %v\n--input--\n%s", err, raw))
	}
	return docRoot(&n)
}
