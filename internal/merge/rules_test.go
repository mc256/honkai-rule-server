package merge

import (
	"reflect"
	"testing"

	"github.com/mc256/honkai-rule-server/internal/config"
	"github.com/mc256/honkai-rule-server/internal/customrules"
)

// TC-U-MERGE-UNIFIED-01: two upstream sources at priorities 1000 and 2000 with no
// custom rules → rules emitted in priority-1000-then-2000 order (ascending sort,
// lower priority emits first) with parallel Contributors populated. Trailing
// rule of each source dropped per FR-008.
func TestMERGE_UNIFIED_01_UpstreamOnly(t *testing.T) {
	per := map[string][]string{
		"alpha":     {"RULE-E1", "RULE-E2", "EXTRA"},
		"beta": {"RULE-B1", "EXTRA"},
	}
	rows := []config.SubscriptionRow{
		{Name: "alpha", Priority: 1000, Enable: true},
		{Name: "beta", Priority: 2000, Enable: true},
	}
	got := MergeUnifiedRules(per, rows, nil, "auto")

	wantRules := []string{"RULE-E1", "RULE-E2", "RULE-B1", "MATCH,auto"}
	wantPrios := []int{1000, 1000, 2000, 0}
	wantContribs := []string{"alpha", "alpha", "beta", ""}
	if !reflect.DeepEqual(got.Rules, wantRules) {
		t.Errorf("rules: got %v, want %v", got.Rules, wantRules)
	}
	if !reflect.DeepEqual(got.Priorities, wantPrios) {
		t.Errorf("priorities: got %v, want %v", got.Priorities, wantPrios)
	}
	if !reflect.DeepEqual(got.Contributors, wantContribs) {
		t.Errorf("contributors: got %v, want %v", got.Contributors, wantContribs)
	}
}

// TC-U-MERGE-UNIFIED-02: two custom rule sets at priorities 300 and 1500 with no
// upstream sources → rules emitted in priority-300-then-1500 order (ascending).
func TestMERGE_UNIFIED_02_CustomOnly(t *testing.T) {
	custom := []customrules.CustomRuleSet{
		{Name: "low", Priority: 300, Rules: []string{"L1"}},
		{Name: "high", Priority: 1500, Rules: []string{"H1", "H2"}},
	}
	got := MergeUnifiedRules(nil, nil, custom, "auto")

	wantRules := []string{"L1", "H1", "H2", "MATCH,auto"}
	wantPrios := []int{300, 1500, 1500, 0}
	wantContribs := []string{"low", "high", "high", ""}
	if !reflect.DeepEqual(got.Rules, wantRules) {
		t.Errorf("rules: got %v, want %v", got.Rules, wantRules)
	}
	if !reflect.DeepEqual(got.Priorities, wantPrios) {
		t.Errorf("priorities: got %v, want %v", got.Priorities, wantPrios)
	}
	if !reflect.DeepEqual(got.Contributors, wantContribs) {
		t.Errorf("contributors: got %v, want %v", got.Contributors, wantContribs)
	}
}

// TC-U-MERGE-UNIFIED-03: upstream priority 1000 + custom priority 2000 →
// upstream rule appears first (under ascending, the lower priority emits first;
// alpha=1000 < corporate=2000, so alpha is matched before corporate).
func TestMERGE_UNIFIED_03_LowerUpstreamBeatsHigherCustom(t *testing.T) {
	per := map[string][]string{
		"alpha": {"U1", "EXTRA"},
	}
	rows := []config.SubscriptionRow{
		{Name: "alpha", Priority: 1000, Enable: true},
	}
	custom := []customrules.CustomRuleSet{
		{Name: "corporate", Priority: 2000, Rules: []string{"C1"}},
	}
	got := MergeUnifiedRules(per, rows, custom, "auto")

	wantRules := []string{"U1", "C1", "MATCH,auto"}
	wantPrios := []int{1000, 2000, 0}
	wantContribs := []string{"alpha", "corporate", ""}
	if !reflect.DeepEqual(got.Rules, wantRules) {
		t.Errorf("rules: got %v, want %v", got.Rules, wantRules)
	}
	if !reflect.DeepEqual(got.Priorities, wantPrios) {
		t.Errorf("priorities: got %v, want %v", got.Priorities, wantPrios)
	}
	if !reflect.DeepEqual(got.Contributors, wantContribs) {
		t.Errorf("contributors: got %v, want %v", got.Contributors, wantContribs)
	}
}

// TC-U-MERGE-UNIFIED-04: upstream priority 2000 + custom priority 1000 →
// custom rule appears first under ascending (corporate=1000 < beta=2000).
func TestMERGE_UNIFIED_04_LowerCustomBeatsHigherUpstream(t *testing.T) {
	per := map[string][]string{
		"beta": {"U1", "EXTRA"},
	}
	rows := []config.SubscriptionRow{
		{Name: "beta", Priority: 2000, Enable: true},
	}
	custom := []customrules.CustomRuleSet{
		{Name: "corporate", Priority: 1000, Rules: []string{"C1"}},
	}
	got := MergeUnifiedRules(per, rows, custom, "auto")

	wantRules := []string{"C1", "U1", "MATCH,auto"}
	wantPrios := []int{1000, 2000, 0}
	wantContribs := []string{"corporate", "beta", ""}
	if !reflect.DeepEqual(got.Rules, wantRules) {
		t.Errorf("rules: got %v, want %v", got.Rules, wantRules)
	}
	if !reflect.DeepEqual(got.Priorities, wantPrios) {
		t.Errorf("priorities: got %v, want %v", got.Priorities, wantPrios)
	}
	if !reflect.DeepEqual(got.Contributors, wantContribs) {
		t.Errorf("contributors: got %v, want %v", got.Contributors, wantContribs)
	}
}

// TC-U-MERGE-UNIFIED-05: upstream `delta` (priority 1000) + custom `corporate`
// (priority 1000) → tie broken alphabetically by Name: corporate < delta.
// This test is INVARIANT under sort-direction flips (all priorities are equal,
// so only the alphabetical tie-break matters); it exercises the tie-break path
// independently of the priority comparator.
func TestMERGE_UNIFIED_05_TieBreakAlphabetical(t *testing.T) {
	per := map[string][]string{
		"delta": {"U1", "EXTRA"},
	}
	rows := []config.SubscriptionRow{
		{Name: "delta", Priority: 1000, Enable: true},
	}
	custom := []customrules.CustomRuleSet{
		{Name: "corporate", Priority: 1000, Rules: []string{"C1", "C2"}},
	}
	got := MergeUnifiedRules(per, rows, custom, "auto")

	// "corporate" < "delta" alphabetically, so corporate's rules come first.
	wantRules := []string{"C1", "C2", "U1", "MATCH,auto"}
	wantPrios := []int{1000, 1000, 1000, 0}
	wantContribs := []string{"corporate", "corporate", "delta", ""}
	if !reflect.DeepEqual(got.Rules, wantRules) {
		t.Errorf("rules: got %v, want %v", got.Rules, wantRules)
	}
	if !reflect.DeepEqual(got.Priorities, wantPrios) {
		t.Errorf("priorities: got %v, want %v", got.Priorities, wantPrios)
	}
	if !reflect.DeepEqual(got.Contributors, wantContribs) {
		t.Errorf("contributors: got %v, want %v", got.Contributors, wantContribs)
	}
}

// TC-U-MERGE-UNIFIED-06: MATCH fallback always last with Priorities[len-1] == 0
// and Contributors[len-1] == "". Skip empty post-drop sources and empty custom sets.
func TestMERGE_UNIFIED_06_MatchFallbackAndSkipEmpty(t *testing.T) {
	per := map[string][]string{
		"single-rule-source": {"ONLY-RULE"},        // becomes empty after trailing drop
		"normal":             {"RULE-N1", "EXTRA"}, // contributes RULE-N1
	}
	rows := []config.SubscriptionRow{
		{Name: "single-rule-source", Priority: 5000, Enable: true},
		{Name: "normal", Priority: 1000, Enable: true},
	}
	custom := []customrules.CustomRuleSet{
		{Name: "empty-set", Priority: 9000, Rules: []string{}}, // skipped (empty rules)
		{Name: "real", Priority: 500, Rules: []string{"R1"}},
	}
	got := MergeUnifiedRules(per, rows, custom, "DIRECT")

	// single-rule-source: dropped to empty → no entries
	// empty-set: empty rules → no entries
	// Ascending order: real (500) → normal (1000), then MATCH at 0
	wantRules := []string{"R1", "RULE-N1", "MATCH,DIRECT"}
	wantPrios := []int{500, 1000, 0}
	wantContribs := []string{"real", "normal", ""}
	if !reflect.DeepEqual(got.Rules, wantRules) {
		t.Errorf("rules: got %v, want %v", got.Rules, wantRules)
	}
	if !reflect.DeepEqual(got.Priorities, wantPrios) {
		t.Errorf("priorities: got %v, want %v", got.Priorities, wantPrios)
	}
	if !reflect.DeepEqual(got.Contributors, wantContribs) {
		t.Errorf("contributors: got %v, want %v", got.Contributors, wantContribs)
	}

	// Final element: MATCH fallback invariants
	if got.Priorities[len(got.Priorities)-1] != 0 {
		t.Errorf("MATCH fallback priority: got %d, want 0", got.Priorities[len(got.Priorities)-1])
	}
	if got.Contributors[len(got.Contributors)-1] != "" {
		t.Errorf("MATCH fallback contributor: got %q, want \"\"", got.Contributors[len(got.Contributors)-1])
	}

	// Parallel-array length invariant
	if len(got.Rules) != len(got.Priorities) || len(got.Rules) != len(got.Contributors) {
		t.Errorf("length invariant violated: rules=%d priorities=%d contributors=%d",
			len(got.Rules), len(got.Priorities), len(got.Contributors))
	}
}
