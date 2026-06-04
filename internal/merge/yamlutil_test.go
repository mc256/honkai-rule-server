package merge

import "testing"

// TC-U-YAML-FINDMAP-01: findChildMapping returns the mapping value of a
// top-level key, and nil for absent keys or non-mapping values.
func TestFindChildMapping(t *testing.T) {
	root := mustParseYAMLNode(`top: scalar
rule-providers:
  Local-IP:
    type: http
    behavior: ipcidr
aseq:
  - one
  - two
`)

	if m := findChildMapping(root, "rule-providers"); m == nil {
		t.Fatalf("findChildMapping(rule-providers) = nil, want mapping node")
	} else if getMappingField(getMappingNode(m, "Local-IP"), "behavior") != "ipcidr" {
		t.Errorf("rule-providers mapping not returned correctly: %+v", m)
	}

	if m := findChildMapping(root, "missing"); m != nil {
		t.Errorf("findChildMapping(missing) = %v, want nil", m)
	}
	if m := findChildMapping(root, "top"); m != nil {
		t.Errorf("findChildMapping(top) = non-nil for a scalar value, want nil")
	}
	if m := findChildMapping(root, "aseq"); m != nil {
		t.Errorf("findChildMapping(aseq) = non-nil for a sequence value, want nil")
	}
	if m := findChildMapping(nil, "x"); m != nil {
		t.Errorf("findChildMapping(nil) = %v, want nil", m)
	}
}
