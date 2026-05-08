// Package customrules loads operator-defined routing rules from YAML files.
package customrules

// CustomRuleSet represents a single custom rule file after loading and defaults applied.
type CustomRuleSet struct {
	Name     string   // Operator-visible identifier; defaults to filename sans .yaml
	Priority int      // Ordering key; lower = earlier in output; defaults to 1000
	Rules    []string // Mihomo rule strings, preserved verbatim
}

// customRuleSetFile is the YAML deserialization target with pointer fields
// to distinguish "field missing" from "field present with zero value".
type customRuleSetFile struct {
	Name     *string  `yaml:"name"`
	Priority *int     `yaml:"priority"`
	Rules    []string `yaml:"rules"`
}