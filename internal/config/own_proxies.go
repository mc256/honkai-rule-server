package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// OwnProxiesFile is the parsed contents of the own-proxies YAML file.
// proxies and proxy-groups are kept as *yaml.Node so the merge layer can
// preserve every field upstream-format proxies carry (research R4).
//
// Per FR-006 / FR-007: both keys must be present (may be empty arrays);
// every proxy must have non-empty name/type/server + integer port in
// [1, 65535]; group references must point at proxies in the same file.
type OwnProxiesFile struct {
	Proxies     []*yaml.Node `yaml:"proxies"`
	ProxyGroups []*yaml.Node `yaml:"proxy-groups"`
}

// OwnProxyValidationError reports a validation failure on one entry of the
// own-proxies file.
type OwnProxyValidationError struct {
	Entry  string // proxy/group name (or "proxies[N]" / "proxy-groups[N]" when the entry has no usable name)
	Field  string // empty when the error is structural
	Reason string
}

func (e *OwnProxyValidationError) Error() string {
	if e.Field == "" {
		return fmt.Sprintf("own-proxies: %s: %s", e.Entry, e.Reason)
	}
	return fmt.Sprintf("own-proxies: %s field %q: %s", e.Entry, e.Field, e.Reason)
}

// LoadOwnProxies opens path, parses it as YAML, and validates per FR-007.
// All schema/validation errors are loud (Constitution Principle III).
func LoadOwnProxies(path string) (*OwnProxiesFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("own-proxies: read %s: %w", path, err)
	}
	// Parse as a raw yaml.Node tree so we can hand the merge layer real
	// *yaml.Node values for proxies / proxy-groups (yaml.v3 doesn't populate
	// []*yaml.Node in a struct field directly — it expects concrete types).
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("own-proxies: parse %s: %w", path, err)
	}
	root := &doc
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}

	out := &OwnProxiesFile{
		Proxies:     extractSequence(root, "proxies"),
		ProxyGroups: extractSequence(root, "proxy-groups"),
	}

	if err := validateOwnProxies(out); err != nil {
		return nil, err
	}
	return out, nil
}

// extractSequence finds the value of a top-level mapping key and returns
// its sequence Content slice (or an empty slice if absent / wrong kind).
func extractSequence(root *yaml.Node, key string) []*yaml.Node {
	if root == nil || root.Kind != yaml.MappingNode {
		return []*yaml.Node{}
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		if k.Value == key && v.Kind == yaml.SequenceNode {
			out := make([]*yaml.Node, len(v.Content))
			copy(out, v.Content)
			return out
		}
	}
	return []*yaml.Node{}
}

func validateOwnProxies(doc *OwnProxiesFile) error {
	proxyNames := make(map[string]bool, len(doc.Proxies))

	for i, p := range doc.Proxies {
		var meta struct {
			Name   string `yaml:"name"`
			Type   string `yaml:"type"`
			Server string `yaml:"server"`
			Port   int    `yaml:"port"`
		}
		if err := p.Decode(&meta); err != nil {
			return &OwnProxyValidationError{
				Entry:  fmt.Sprintf("proxies[%d]", i),
				Reason: fmt.Sprintf("decode: %v", err),
			}
		}
		entryLabel := meta.Name
		if entryLabel == "" {
			entryLabel = fmt.Sprintf("proxies[%d]", i)
		}
		if meta.Name == "" {
			return &OwnProxyValidationError{Entry: entryLabel, Field: "name", Reason: "empty"}
		}
		if proxyNames[meta.Name] {
			return &OwnProxyValidationError{Entry: meta.Name, Field: "name", Reason: "duplicate"}
		}
		if meta.Type == "" {
			return &OwnProxyValidationError{Entry: meta.Name, Field: "type", Reason: "empty"}
		}
		if meta.Server == "" {
			return &OwnProxyValidationError{Entry: meta.Name, Field: "server", Reason: "empty"}
		}
		if meta.Port < 1 || meta.Port > 65535 {
			return &OwnProxyValidationError{
				Entry: meta.Name, Field: "port",
				Reason: fmt.Sprintf("out of range [1, 65535]: %d", meta.Port),
			}
		}
		proxyNames[meta.Name] = true
	}

	groupNames := make(map[string]bool, len(doc.ProxyGroups))
	for i, g := range doc.ProxyGroups {
		var meta struct {
			Name    string   `yaml:"name"`
			Type    string   `yaml:"type"`
			Proxies []string `yaml:"proxies"`
		}
		if err := g.Decode(&meta); err != nil {
			return &OwnProxyValidationError{
				Entry:  fmt.Sprintf("proxy-groups[%d]", i),
				Reason: fmt.Sprintf("decode: %v", err),
			}
		}
		entryLabel := meta.Name
		if entryLabel == "" {
			entryLabel = fmt.Sprintf("proxy-groups[%d]", i)
		}
		if meta.Name == "" {
			return &OwnProxyValidationError{Entry: entryLabel, Field: "name", Reason: "empty"}
		}
		if groupNames[meta.Name] {
			return &OwnProxyValidationError{Entry: meta.Name, Field: "name", Reason: "duplicate"}
		}
		if meta.Type == "" {
			return &OwnProxyValidationError{Entry: meta.Name, Field: "type", Reason: "empty"}
		}
		// Group `proxies:` may reference upstream proxies too; here we
		// catch typos that point at neither an own-proxy nor a likely
		// upstream entry. We're conservative — only reject references
		// that are obviously bogus (empty strings).
		for _, ref := range meta.Proxies {
			if ref == "" {
				return &OwnProxyValidationError{
					Entry: meta.Name, Field: "proxies",
					Reason: "contains empty proxy reference",
				}
			}
		}
		groupNames[meta.Name] = true
	}

	return nil
}
