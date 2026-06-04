package merge

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/mc256/honkai-rule-server/internal/clock"
	"github.com/mc256/honkai-rule-server/internal/config"
	"github.com/mc256/honkai-rule-server/internal/customrules"
	"github.com/mc256/honkai-rule-server/internal/dailyspend"
	"github.com/mc256/honkai-rule-server/internal/fetcher"
)

// Snapshotter is the contract Pipeline uses to read/write the today-zero
// snapshot per 011. Both dailyspend.FileSnapshotter (production) and
// dailyspend.MapSnapshotter (test) satisfy it structurally; declaring it
// here at the use-site avoids a merge → dailyspend import cycle for the
// interface itself (the *dailyspend.Snapshot value type is fine to import).
type Snapshotter interface {
	Load() (*dailyspend.Snapshot, error)
	Save(*dailyspend.Snapshot) error
}

// CacheReader is the read-only cache surface the pipeline needs. The fetcher
// package's *Cache satisfies it; tests can stub it without standing up a
// full cache + fetcher.
type CacheReader interface {
	Get(name string) (*fetcher.UpstreamCachedPayload, bool)
}

// MergedConfig is the internal representation of the merged subscription.
// Output adapters (subscription mode for v1) consume this to produce the
// served body + headers.
type MergedConfig struct {
	Proxies     []*yaml.Node
	ProxyGroups []*yaml.Node

	// RuleProviders is the merged, namespaced `rule-providers:` mapping node
	// (016), containing only providers referenced by a surviving RULE-SET rule.
	// nil → no surviving RULE-SET rule referenced any provider; the output
	// adapter then emits no `rule-providers:` key (016 FR-006).
	RuleProviders *yaml.Node

	// Rules, RulePriorities, RuleContributors are three parallel slices of
	// equal length. RulePriorities[i] is the priority of the contributor that
	// supplied Rules[i]; RuleContributors[i] is the contributor's name. The
	// trailing entry is the server-emitted MATCH fallback with priority 0
	// and contributor "".
	Rules            []string
	RulePriorities   []int
	RuleContributors []string

	ContributingSources []string
	Collisions          []ProxyCollision
	GroupConflicts      []GroupConflict

	// Filled by US3's traffic.go via Pipeline; nil/zero in US1.
	AggregatedSubscriptionUserinfo       *fetcher.SubscriptionUserinfo
	AggregatedProfileUpdateIntervalHours int

	// ServedTrafficHeader is the value the output adapter writes into the
	// public Subscription-Userinfo response header (010 FR-001/FR-002). nil
	// = no source contributed userinfo, omit the header (010 FR-006). The
	// raw aggregates above remain available for the /health surface.
	ServedTrafficHeader *ServedTrafficHeader
}

// Pipeline is the unified transformation core (Constitution Principle I).
// Build() is pure given (cache state, config rows, own-proxies, clock).
type Pipeline struct {
	cache         CacheReader
	subscriptions []config.SubscriptionRow
	ownProxies    []*yaml.Node // nil-able
	ownGroups     []*yaml.Node // nil-able
	clock         clock.Clock

	// Default profile-update-interval (hours) when no upstream supplies one.
	// Used by US3's traffic aggregation; harmless in US1.
	defaultProfileUpdateIntervalHours int

	// customRules holds loaded custom rule sets (FR-003), sorted by priority.
	customRules []customrules.CustomRuleSet

	// proxiesGroupName is the name of the always-present `select`-type
	// proxy-group appended after the merge so the client UI is selectable
	// regardless of what upstream groups exist (FR-009a). Empty defaults
	// to "Proxies".
	proxiesGroupName string

	// fallbackRuleTarget is the target of the server-emitted MATCH rule
	// appended at the end of the merged rules block (FR-010).
	// Defaults to "auto".
	fallbackRuleTarget string

	// urlTestParams is the health-check parameter set written into every
	// auto-emitted _region_* / _continent_* proxy group per 012 FR-001..
	// FR-004. Set via WithURLTestParams; zero-value yields the FR-003
	// defaults at config-load time, so a Pipeline constructed without
	// WithURLTestParams renders groups with empty/zero fields (caller's
	// responsibility to set this from cfg.URLTestParams).
	urlTestParams URLTestParams

	// loadBalanceParams is the load-balance parameter set written into every
	// auto-emitted _lb_region_* / _lb_continent_* proxy group per 014
	// FR-001..FR-006. Set via WithLoadBalanceParams; same caller contract as
	// urlTestParams (a Pipeline constructed without the builder renders lb
	// groups with empty/zero fields, so callers MUST set this from
	// cfg.LoadBalanceParams).
	loadBalanceParams LoadBalanceParams

	// snapshotter persists today-zero state across pod restarts (011
	// FR-005). nil → fall back to 010 behavior (no spend tracking;
	// ComposeServedTrafficHeader without a snapshot).
	snapshotter Snapshotter

	// budgetLocation is the timezone used to compute "today" / "tomorrow"
	// for the daily-budget boundary (011 FR-010). nil → time.UTC (010
	// behavior).
	budgetLocation *time.Location
}

// NewPipeline constructs a Pipeline. ownProxies / ownGroups may be nil for
// callers that don't have an own-proxies file (none in production v1, but
// useful for tests). proxiesGroupName "" defaults to "Proxies".
func NewPipeline(
	cache CacheReader,
	subs []config.SubscriptionRow,
	ownProxies, ownGroups []*yaml.Node,
	clk clock.Clock,
	defaultUpdateIntervalHours int,
) *Pipeline {
	return &Pipeline{
		cache:                             cache,
		subscriptions:                     subs,
		ownProxies:                        ownProxies,
		ownGroups:                         ownGroups,
		clock:                             clk,
		defaultProfileUpdateIntervalHours: defaultUpdateIntervalHours,
		proxiesGroupName:                  "Proxies",
		fallbackRuleTarget:                "auto",
	}
}

// WithProxiesGroupName sets the name used by the always-present `select`
// group (FR-009a). Returns the receiver for chaining.
func (p *Pipeline) WithProxiesGroupName(name string) *Pipeline {
	if name != "" {
		p.proxiesGroupName = name
	}
	return p
}

// WithCustomRules sets custom rule sets to insert between upstream rules
// and the MATCH fallback (FR-003). Returns the receiver for chaining.
func (p *Pipeline) WithCustomRules(rules []customrules.CustomRuleSet) *Pipeline {
	p.customRules = rules
	return p
}

// WithFallbackRuleTarget sets the target of the server-emitted MATCH rule
// (FR-010). Returns the receiver for chaining.
func (p *Pipeline) WithFallbackRuleTarget(target string) *Pipeline {
	if target != "" {
		p.fallbackRuleTarget = target
	}
	return p
}

// WithURLTestParams sets the health-check parameter set for emitted
// _region_* / _continent_* groups (012 FR-001..FR-004). Returns the
// receiver for chaining.
func (p *Pipeline) WithURLTestParams(params URLTestParams) *Pipeline {
	p.urlTestParams = params
	return p
}

// WithLoadBalanceParams sets the load-balance parameter set for emitted
// _lb_region_* / _lb_continent_* groups (014 FR-001..FR-006). Returns the
// receiver for chaining.
func (p *Pipeline) WithLoadBalanceParams(params LoadBalanceParams) *Pipeline {
	p.loadBalanceParams = params
	return p
}

// WithSnapshotter sets the Snapshotter used by 011's spend-aware header
// composition. nil disables spend tracking (010 behavior). Returns the
// receiver for chaining.
func (p *Pipeline) WithSnapshotter(s Snapshotter) *Pipeline {
	p.snapshotter = s
	return p
}

// WithBudgetLocation sets the timezone used to compute the daily-budget
// boundary (011 FR-010). nil → time.UTC. Returns the receiver for chaining.
func (p *Pipeline) WithBudgetLocation(loc *time.Location) *Pipeline {
	p.budgetLocation = loc
	return p
}

// Build assembles the MergedConfig from the current cache state. Sources
// without a cached payload are silently skipped (the scheduler logs the
// absence; the merge layer is pure read).
func (p *Pipeline) Build() (*MergedConfig, error) {
	rows := sortSourcesByPriority(p.subscriptions)

	proxiesPerSource := make(map[string][]*yaml.Node, len(rows))
	groupsPerSource := make(map[string][]*yaml.Node, len(rows))
	rulesPerSource := make(map[string][]string, len(rows))
	// 016: raw (pre-namespacing) `rule-providers:` mapping node per source.
	ruleProvidersPerSource := make(map[string]*yaml.Node, len(rows))
	contributing := make([]string, 0, len(rows))

	for _, row := range rows {
		payload, ok := p.cache.Get(row.Name)
		if !ok {
			continue
		}
		root, err := payload.Parse()
		if err != nil {
			return nil, fmt.Errorf("merge: parse cached payload for %q: %w", row.Name, err)
		}
		root = docRoot(root)

		if seq := findChildSequence(root, "proxies"); seq != nil {
			proxiesPerSource[row.Name] = seq.Content
		}
		if seq := findChildSequence(root, "proxy-groups"); seq != nil {
			groupsPerSource[row.Name] = seq.Content
		}
		if seq := findChildSequence(root, "rules"); seq != nil {
			rules := make([]string, 0, len(seq.Content))
			for _, n := range seq.Content {
				if n.Kind == yaml.ScalarNode {
					rules = append(rules, n.Value)
				}
			}
			rulesPerSource[row.Name] = rules
		}
		if m := findChildMapping(root, "rule-providers"); m != nil {
			ruleProvidersPerSource[row.Name] = m
		}
		contributing = append(contributing, row.Name)
	}

	// Collect captured headers in the same pass so US3's traffic
	// aggregation reuses the cache walk done above.
	userinfoPerSource := make(map[string]fetcher.SubscriptionUserinfo, len(rows))
	intervalPerSource := make(map[string]*int, len(rows))
	for _, name := range contributing {
		payload, ok := p.cache.Get(name)
		if !ok {
			continue
		}
		if payload.Headers.SubscriptionUserinfo != nil {
			userinfoPerSource[name] = *payload.Headers.SubscriptionUserinfo
		}
		intervalPerSource[name] = payload.Headers.ProfileUpdateIntervalHours
	}

	// FR-004/FR-005/FR-006: Apply provider-prefix namespacing to each source's
	// proxies, groups, and rules before merge. 016: also namespace the
	// `rule-providers:` block (keys + path + fetch-through proxy) and the
	// RULE-SET rules' provider-name field (handled inside RewriteSource).
	nsRuleProviders := make(map[string]*yaml.Node, len(contributing))
	type droppedRuleSetEvt struct{ source, provider, rule string }
	type skippedProviderEvt struct{ source, provider string }
	var droppedRuleSets []droppedRuleSetEvt
	var skippedProviders []skippedProviderEvt
	for _, name := range contributing {
		proxiesPerSource[name], groupsPerSource[name], rulesPerSource[name] = RewriteSource(
			name,
			proxiesPerSource[name],
			groupsPerSource[name],
			rulesPerSource[name],
		)
		if raw := ruleProvidersPerSource[name]; raw != nil {
			ns, skipped := RewriteSourceRuleProviders(name, raw)
			nsRuleProviders[name] = ns
			for _, s := range skipped {
				skippedProviders = append(skippedProviders, skippedProviderEvt{source: name, provider: s.Provider})
			}
		}
	}

	// 016 FR-009: drop RULE-SET rules whose provider is undefined in their
	// source, BEFORE the unified merge so the priority/contributor parallel
	// slices stay aligned by construction.
	for _, name := range contributing {
		keys := ruleProviderKeys(nsRuleProviders[name])
		kept, dropped := DropUnbackedRuleSetRules(rulesPerSource[name], keys)
		rulesPerSource[name] = kept
		for _, d := range dropped {
			droppedRuleSets = append(droppedRuleSets, droppedRuleSetEvt{source: name, provider: d.Provider, rule: d.Rule})
		}
	}

	// FR-007a/FR-007b: Apply underscore prefix to own-proxies and own-groups.
	rewrittenOwnProxies, rewrittenOwnGroups := RewriteOwn(p.ownProxies, p.ownGroups)

	mergedProxies, collisions := MergeProxies(proxiesPerSource, contributing, rewrittenOwnProxies)
	mergedGroups, conflicts := MergeProxyGroups(groupsPerSource, contributing, rewrittenOwnGroups)
	mergedRulesResult := MergeUnifiedRules(rulesPerSource, rows, p.customRules, p.fallbackRuleTarget)

	// FR-009a: always append (or augment) a `select` group containing every
	// proxy so the client UI has a selectable group even when no upstream
	// contributed one.
	// 008 FR-007/-008: own-proxies (`_<own>`) are excluded from the global
	// Proxies selector. The fan-out `via_*` copies are not yet present in
	// `mergedProxies` at this point (fan-out runs later in Build) so they're
	// naturally excluded with no extra filter.
	allNames := make([]string, 0, len(mergedProxies))
	for _, n := range mergedProxies {
		name := getMappingField(n, "name")
		if name == "" || strings.HasPrefix(name, "_") {
			continue
		}
		allNames = append(allNames, name)
	}
	mergedGroups = AppendProxiesGroup(mergedGroups, allNames, p.proxiesGroupName)

		// FR-012/FR-013: Region grouping — partition proxies into upstream-only
		// (exclude own-proxies whose names start with "_"), infer country codes,
		// and emit _region_<CC> groups.
		upstreamProxies := make([]*yaml.Node, 0, len(mergedProxies))
		for _, n := range mergedProxies {
			name := getMappingField(n, "name")
			if name != "" && name[0] != '_' {
				upstreamProxies = append(upstreamProxies, n)
			}
		}
		unmappedSeen := make(map[string]bool)
		mergedGroups = AppendRegionGroups(mergedGroups, upstreamProxies, p.proxiesGroupName, p.urlTestParams, p.loadBalanceParams, func(fragment string) {
			if !unmappedSeen[fragment] {
				unmappedSeen[fragment] = true
				slog.Info("region-unmapped-indicator",
					"event", "region-unmapped-indicator",
					"fragment", fragment,
				)
			}
		})

		// FR-013b: Continent grouping — aggregate region groups into
		// _continent_<CONT> groups via country-to-continent mapping.
		regionGroupNames := make([]string, 0)
		regionGroupMembers := make(map[string][]string)
		for _, g := range mergedGroups {
			name := getMappingField(g, "name")
			if len(name) > 8 && name[:8] == "_region_" {
				regionGroupNames = append(regionGroupNames, name)
				regionGroupMembers[name] = mappingMembers(g, "proxies")
			}
		}
		unmappedContinentSeen := make(map[string]bool)
		mergedGroups = AppendContinentGroups(mergedGroups, regionGroupNames, regionGroupMembers, p.proxiesGroupName, p.urlTestParams, p.loadBalanceParams, func(cc string) {
			if !unmappedContinentSeen[cc] {
				unmappedContinentSeen[cc] = true
				slog.Info("continent-unmapped-country",
					"event", "continent-unmapped-country",
					"country_code", cc,
				)
			}
		})

	// 008 FR-001..FR-006: emit dialer-proxy fan-out copies for every
	// own-proxy without an explicit dialer-proxy field, one per
	// `_region_*`/`_continent_*` group plus one AUTO copy.
	fanoutProxies, fanoutSkipped := AppendFanoutProxies(rewrittenOwnProxies, mergedGroups, p.proxiesGroupName)
	mergedProxies = append(mergedProxies, fanoutProxies...)
	fanoutTargetGroups := 0
	for _, g := range mergedGroups {
		name := getMappingField(g, "name")
		if strings.HasPrefix(name, "_region_") ||
			strings.HasPrefix(name, "_continent_") ||
			strings.HasPrefix(name, "_lb_region_") ||
			strings.HasPrefix(name, "_lb_continent_") {
			fanoutTargetGroups++
		}
	}
	slog.Info("fanout-emitted",
		"event", "fanout-emitted",
		"own_proxy_count", len(rewrittenOwnProxies),
		"skipped_explicit_dialer", fanoutSkipped,
		"target_group_count", fanoutTargetGroups,
		"emitted_count", len(fanoutProxies),
	)

	aggregatedUI := AggregateSubscriptionUserinfo(userinfoPerSource)
	aggregatedInterval := AggregateProfileUpdateInterval(intervalPerSource, p.defaultProfileUpdateIntervalHours)

	// 011: when a snapshotter is configured, use the spend-aware header
	// path with lazy midnight rollover; otherwise fall back to 010 behavior.
	var servedTrafficHeader *ServedTrafficHeader
	if p.snapshotter != nil {
		loadedSnapshot, _ := p.snapshotter.Load() // err logged inside FileSnapshotter; nil treated as rollover
		var newSnapshot *dailyspend.Snapshot
		servedTrafficHeader, newSnapshot = ComposeServedTrafficHeaderWithSpend(
			userinfoPerSource, p.clock, loadedSnapshot, p.budgetLocation,
		)
		if newSnapshot != nil && newSnapshot != loadedSnapshot {
			if err := p.snapshotter.Save(newSnapshot); err != nil {
				slog.Warn("dailyspend snapshot save failed",
					"event", "dailyspend-save-failed",
					"err", err.Error(),
				)
			}
		}
	} else {
		servedTrafficHeader = ComposeServedTrafficHeader(userinfoPerSource, p.clock)
	}

	// 015 FR-001..FR-011: drop every proxy-group whose `proxies` member list
	// is empty so the served config loads in the Mihomo client, and clean up
	// the member/rule references the removals leave behind. Runs last —
	// `mergedGroups` and the rule slice are final here (after fan-out). The
	// always-present `Proxies` selector and the fallback-rule-target group are
	// exempt from removal (FR-007).
	prunedGroups, prunedRules, pruneResult := PruneEmptyProxyGroups(
		mergedGroups, mergedRulesResult.Rules, p.proxiesGroupName, p.fallbackRuleTarget,
	)
	mergedGroups = prunedGroups
	mergedRulesResult.Rules = prunedRules
	if len(pruneResult.RemovedGroups) > 0 || len(pruneResult.Retargets) > 0 {
		slog.Info("proxy-groups-pruned",
			"event", "proxy-groups-pruned",
			"removed_count", len(pruneResult.RemovedGroups),
			"removed", pruneResult.RemovedGroups,
			"retargeted_rules", len(pruneResult.Retargets),
		)
		for _, rt := range pruneResult.Retargets {
			slog.Info("rule-retargeted",
				"event", "rule-retargeted",
				"rule_index", rt.RuleIndex,
				"old_target", rt.OldTarget,
				"new_target", rt.NewTarget,
			)
		}
	}

	// 016 FR-005/FR-006/FR-010: build the merged `rule-providers:` block from
	// the FINAL rule slice (post trailing-drop, unbacked-drop, and 015 prune),
	// keeping only providers a surviving RULE-SET rule references, in
	// contributing-source order. nil when nothing is referenced.
	referenced := ReferencedRuleProviders(mergedRulesResult.Rules)
	orderedNSRuleProviders := make([]*yaml.Node, 0, len(contributing))
	for _, name := range contributing {
		if ns := nsRuleProviders[name]; ns != nil {
			orderedNSRuleProviders = append(orderedNSRuleProviders, ns)
		}
	}
	ruleProviders := MergeRuleProviders(orderedNSRuleProviders, referenced)

	// 016 FR-011: structured observability for the rule-set merge decisions.
	for _, d := range droppedRuleSets {
		slog.Info("ruleset-rule-dropped",
			"event", "ruleset-rule-dropped",
			"source", d.source,
			"provider", d.provider,
			"rule", d.rule,
		)
	}
	for _, s := range skippedProviders {
		slog.Info("ruleset-provider-skipped",
			"event", "ruleset-provider-skipped",
			"source", s.source,
			"provider", s.provider,
			"reason", "malformed",
		)
	}
	if ruleProviders != nil || len(droppedRuleSets) > 0 || len(skippedProviders) > 0 {
		mergedCount := 0
		if ruleProviders != nil {
			mergedCount = len(ruleProviders.Content) / 2
		}
		slog.Info("ruleset-merged",
			"event", "ruleset-merged",
			"providers_merged", mergedCount,
			"rules_dropped", len(droppedRuleSets),
			"providers_skipped", len(skippedProviders),
		)
	}

	return &MergedConfig{
		Proxies:                              mergedProxies,
		ProxyGroups:                          mergedGroups,
		RuleProviders:                        ruleProviders,
		Rules:                                mergedRulesResult.Rules,
		RulePriorities:                       mergedRulesResult.Priorities,
		RuleContributors:                     mergedRulesResult.Contributors,
		ContributingSources:                  contributing,
		Collisions:                           collisions,
		GroupConflicts:                       conflicts,
		AggregatedSubscriptionUserinfo:       &aggregatedUI,
		AggregatedProfileUpdateIntervalHours: aggregatedInterval,
		ServedTrafficHeader:                  servedTrafficHeader,
	}, nil
}

// CollectPerSourceUserinfo returns the captured Subscription-Userinfo header
// values for every enabled source that has a cached payload. Used by the
// /health handler to drive ComputeDailyAllowance.
func (p *Pipeline) CollectPerSourceUserinfo() map[string]fetcher.SubscriptionUserinfo {
	out := make(map[string]fetcher.SubscriptionUserinfo)
	for _, row := range p.subscriptions {
		if !row.Enable {
			continue
		}
		payload, ok := p.cache.Get(row.Name)
		if !ok || payload.Headers.SubscriptionUserinfo == nil {
			continue
		}
		out[row.Name] = *payload.Headers.SubscriptionUserinfo
	}
	return out
}

// ComputeDailyAllowance is a thin wrapper that pairs CollectPerSourceUserinfo
// with the pure ComputeDailyAllowance function so /health gets a single
// recomputed-from-current-state figure (FR-011b).
func (p *Pipeline) ComputeDailyAllowance() DailyAllowance {
	return ComputeDailyAllowance(p.CollectPerSourceUserinfo(), p.clock)
}

// sortSourcesByPriority returns the enabled rows sorted by priority desc,
// ties broken by original (CSV) row order via stable sort.
func sortSourcesByPriority(rows []config.SubscriptionRow) []config.SubscriptionRow {
	enabled := make([]config.SubscriptionRow, 0, len(rows))
	for _, r := range rows {
		if r.Enable {
			enabled = append(enabled, r)
		}
	}
	sort.SliceStable(enabled, func(i, j int) bool {
		return enabled[i].Priority > enabled[j].Priority
	})
	return enabled
}

// Now is the pipeline's view of "current time" (injected via Clock).
// Exposed so US3's traffic computations can stay deterministic in tests.
func (p *Pipeline) Now() time.Time { return p.clock.Now() }
