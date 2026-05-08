package merge

import (
	"sort"

	"github.com/mc256/honkai-rule-server/internal/config"
	"github.com/mc256/honkai-rule-server/internal/customrules"
)

// MergeResult carries rules plus parallel priority and contributor metadata.
//
// Invariant: len(Rules) == len(Priorities) == len(Contributors).
//
// The trailing entry of every MergeResult is the server-emitted MATCH fallback
// rule, with Priorities[last] == 0 and Contributors[last] == "". No
// operator-supplied bucket can produce that combination because contributor
// names are non-empty by load-time validation.
type MergeResult struct {
	Rules        []string
	Priorities   []int
	Contributors []string
}

// contributor unifies upstream sources and custom rule sets for the
// priority-merge step. Unexported; constructed inside MergeUnifiedRules.
type contributor struct {
	Name     string
	Priority int
	Rules    []string
}

// MergeUnifiedRules merges upstream rules and custom rule sets into a single
// priority-ascending stream. Each upstream source contributes its rules
// (with the trailing rule dropped per FR-008 of feature 002) at the priority
// declared in subscriptions.csv. Each custom rule set contributes its rules
// at its declared priority. Sort key: (Priority asc, Name asc) — lower
// priority numbers emit earlier in the served rules block, matching
// Mihomo's top-to-bottom rule evaluation (lower number = higher precedence).
//
// Empty contributors (upstream sources whose only rule was the trailing drop,
// custom sets with empty rule lists) are skipped — they produce no rules and
// no header comment in the served output (spec FR-007).
//
// The MATCH,<fallback> rule is always last with priority 0 and contributor "".
// See feature 005 spec FR-001..FR-010.
func MergeUnifiedRules(
	upstreamPerSource map[string][]string,
	upstreamSources []config.SubscriptionRow,
	customs []customrules.CustomRuleSet,
	fallbackRuleTarget string,
) MergeResult {
	contributors := make([]contributor, 0, len(upstreamSources)+len(customs))

	for _, row := range upstreamSources {
		rules := upstreamPerSource[row.Name]
		if len(rules) == 0 {
			continue
		}
		// Trailing-rule drop (FR-008 of feature 002)
		rules = rules[:len(rules)-1]
		if len(rules) == 0 {
			continue
		}
		contributors = append(contributors, contributor{
			Name:     row.Name,
			Priority: row.Priority,
			Rules:    rules,
		})
	}

	for _, cs := range customs {
		if len(cs.Rules) == 0 {
			continue
		}
		contributors = append(contributors, contributor{
			Name:     cs.Name,
			Priority: cs.Priority,
			Rules:    cs.Rules,
		})
	}

	sort.SliceStable(contributors, func(i, j int) bool {
		if contributors[i].Priority != contributors[j].Priority {
			return contributors[i].Priority < contributors[j].Priority
		}
		return contributors[i].Name < contributors[j].Name
	})

	totalRules := 1 // MATCH fallback
	for _, c := range contributors {
		totalRules += len(c.Rules)
	}

	rules := make([]string, 0, totalRules)
	priorities := make([]int, 0, totalRules)
	names := make([]string, 0, totalRules)

	for _, c := range contributors {
		for _, r := range c.Rules {
			rules = append(rules, r)
			priorities = append(priorities, c.Priority)
			names = append(names, c.Name)
		}
	}

	rules = append(rules, "MATCH,"+fallbackRuleTarget)
	priorities = append(priorities, 0)
	names = append(names, "")

	return MergeResult{Rules: rules, Priorities: priorities, Contributors: names}
}
