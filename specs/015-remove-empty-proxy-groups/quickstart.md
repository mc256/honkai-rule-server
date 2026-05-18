# Quickstart: Prune Empty Proxy-Groups

Operator- and developer-facing notes for feature 015.

## What changes for operators

Nothing to configure. Empty proxy-group pruning is always-on server behavior — there
is no env var, flag, or CSV column to enable or disable it (spec Assumptions).

**Before**: if the merged configuration produced a proxy-group with no members — for
example an own-group you declared with an empty `proxies:` list, or an upstream group
whose members were all dropped during namespacing — the Mihomo client rejected the
*entire* served profile with an "empty proxy-group" error.

**After**: the server removes any such empty group before serving. The rest of the
configuration loads normally. The always-present `Proxies` selector is never removed.

### How to tell what was pruned

The server logs a structured event whenever it prunes. Look for:

```
event=proxy-groups-pruned   removed_count=<n>  removed=[<group names>]  retargeted_rules=<m>
event=rule-retargeted       rule_index=<i>  old_target=<removed group>  new_target=<fallback>
```

If a routing rule pointed at a group that got pruned, the rule is **redirected to the
configured fallback rule target** (the target of the trailing `MATCH` rule) so the
served config stays internally consistent. Each redirect is logged individually — if
you see a `rule-retargeted` event for a rule you care about (e.g. a corporate-routing
rule), investigate why its target group ended up empty.

### When you see an empty group disappear

That group had no members. Common causes:

- An own-group in `own-proxies.yaml` declared with an empty `proxies:` list.
- An upstream-contributed group whose every member was a namespacing/trailing-rule
  drop.

Add members to the group upstream if you expected it to appear.

## What changes for developers

- New pure function `PruneEmptyProxyGroups` in `internal/merge/prune.go`, called once
  at the end of `Pipeline.Build()` after fan-out. It removes empty, non-protected
  proxy-groups, drops member references to removed groups, and retargets orphaned
  rules to the fallback target.
- Protected from removal: the `Proxies` selector, and the fallback-rule-target group
  when the fallback names a group.
- Single removal pass — no cascading (spec FR-005).

### Running the tests

```sh
make check                                              # vet + lint + tests + snapshot drift
go test ./internal/merge/ -run 'TestPrune|TestRuleTarget'   # prune-function unit tests
go test ./internal/integration/ -run TestSnapshot_PruneServedConfig   # end-to-end scenario
```

The integration scenario builds the pipeline directly against a crafted upstream
payload (`pruneUpstreamYAML` in `internal/integration/prune_test.go`) — an upstream
that contributes a proxy-group with no members, a group that references it, and a
rule that targets it. The merge harness hardcodes its two upstream stubs, so the
prune scenario uses the `stubMergeCache` direct-build path rather than a fixture file
registered in `subscriptions.csv`.

### Updating snapshots (deliberate, reviewed action)

The existing `served-config.snap.yaml` MUST NOT drift — its fixtures contain no empty
group. If it does, the prune step changed something it should not have.

The `served-config-prune.snap.yaml` baseline is regenerated with:

```sh
UPDATE_SNAPSHOTS=true go test ./internal/integration/ -run TestSnapshot_PruneServedConfig
```

Per the Constitution snapshot-stability gate, any snapshot change must be called out
and justified in the PR description.
