<!-- SPECKIT START -->
**001 (subscription-aggregator)** is fully implemented; production code
lives under `cmd/server/` and `internal/`. **002 (namespacing-and-regions)**
is fully implemented. **003 (custom-rules-access-control)** is fully
implemented. **004 (yaml-output-formatting)** is fully implemented.
**005 (unified-rule-priority)** is fully implemented.
**006 (fix-emoji-yaml-escape)** is fully implemented (post-encode byte
transform replacing yaml.v3's `\Uxxxxxxxx` escapes with literal UTF-8).
**007 (ascending-priority-sort)** is fully implemented (reversed 005's
sort direction so lower priority numbers emit earlier in the served
`rules:` block).
**008 (dialer-proxy-fanout)** is fully implemented (per-region/per-continent
+ AUTO fan-out copies of own-proxies; own-proxies and `via_*` excluded
from the always-present `Proxies` selector group).
**009 (cluster-deploy)** is fully implemented (Helm chart at
`charts/honkai-rule-server/` extends with a `honkai` block; image at
`registry.example.com/library/honkai-rule-server` — **superseded by
`ghcr.io/<owner>/honkai-rule-server` per 013**; ConfigMap +
PVC + Argo CD app on the cluster).
**010 (daily-traffic-header)** is fully implemented (served
`Subscription-Userinfo` header carries the daily-allowance figure
`upload=0; download=0; total=<allowance>; expire=<next 00:00 UTC>`;
header omitted entirely when no source contributed userinfo; raw
aggregates remain on `/health`).
**012 (url-test-region-groups)** is fully implemented (auto-emitted
`_region_*` and `_continent_*` proxy groups are `type: url-test`
with operator-configurable health-check fields via 5 `URL_TEST_*`
env vars; always-present `Proxies` selector and operator-defined
custom proxy groups untouched).
**011 (daily-spend-tracking)** is in-progress in
`specs/011-daily-spend-tracking/` (reactivated from parked status to
extend 010 with within-day spend tracking via a persistent
`/data/today-zero.json` snapshot, midnight rollover in
America/Toronto, and a ratio-aware upload/download split).
**013 (ci-container-release)** is the active build/CI infra feature
being designed/implemented in `specs/013-ci-container-release/`
(GitHub Actions publishing images to `ghcr.io/<owner>/honkai-rule-server`
on `master` push and on `v*` SemVer tag push; new Makefile targets
`release-{patch,minor,major}` + `release VERSION=` + `DRY_RUN=1`;
Dependabot config for gomod/github-actions/docker with auto-merge
of non-major bumps and a daily auto-patch-release workflow that cuts
`vX.Y.(Z+1)` whenever Dependabot commits accumulate on master since
the last release tag; canonical image registry replaces 009's
`registry.example.com/library/honkai-rule-server` placeholder).
**014 (load-balance-region-groups)** is the active routing feature being
designed/implemented in `specs/014-load-balance-region-groups/` (additive
`_lb_region_<CC>` / `_lb_continent_<CONT>` proxy groups of `type:
load-balance` emitted in parallel to 012's url-test groups, sharing the
same membership; new `LOAD_BALANCE_*` env-var namespace — six knobs incl.
`LOAD_BALANCE_STRATEGY` accepting `round-robin`/`consistent-hashing`/
`sticky-sessions`; existing 008 fan-out predicate widens to also accept
`_lb_region_` / `_lb_continent_` prefixes, producing
`via_lb_region_<CC>__<own>` / `via_lb_continent_<CONT>__<own>` copies; lb
groups join the always-present `Proxies` selector alongside url-test
groups; no AUTO_lb variant; existing 012 url-test groups byte-unchanged).
**015 (remove-empty-proxy-groups)** is the active feature being
designed/implemented in `specs/015-remove-empty-proxy-groups/` (final prune
pass in `Pipeline.Build()` removing every proxy-group whose `proxies:` list
is empty so the served config loads in Mihomo; single removal pass, no
cascade; the always-present `Proxies` selector is exempt and always kept;
member references to removed groups are dropped; a routing rule whose target
group was removed is redirected to the configured fallback rule target; new
pure fn `PruneEmptyProxyGroups` in `internal/merge/prune.go`; byte-unchanged
output when no group is empty).
**016 (rule-set-support)** is the active feature being designed/implemented in
`specs/016-rule-set-support/` (read each upstream source's `rule-providers:`
mapping, namespace provider keys with the `<source>_` prefix — `srcA_Local-IP`,
`srcB_Local-IP` — and namespace the `RULE-SET` rule's provider-name field
(field[1]) to match; provider defs get a source-distinct `path:` and a prefixed
non-builtin `proxy:`; per-source drop of `RULE-SET` rules with no backing
provider before the unified merge; emit a merged `rule-providers:` block holding
only providers referenced by a surviving `RULE-SET` rule, omitting the key
entirely when none; new pure fns in `internal/merge/ruleset.go` +
`findChildMapping` in `yamlutil.go` + `MergedConfig.RuleProviders *yaml.Node`;
`RULE-SET` rules still priority-ordered (014-style) and subject to 002 trailing
drop + 015 prune/retarget; byte-unchanged output when no source has
`rule-providers`/`RULE-SET`).
**017 (per-source-refresh-interval)** is fully implemented (renames 001's
optional `ttl_seconds` subscriptions-CSV column to `refresh` with tri-state
semantics: `0`/absent → server default interval `DEFAULT_TTL_SECONDS`,
positive → that many seconds, negative → never refresh; `SubscriptionRow.TTLSeconds`
renamed to `RefreshSeconds`; scheduler gains `refreshDisabled`/`neverRefreshTTL`
so a negative-refresh source bootstraps once then skips the `runSteady` ticker
and its cache never reports stale; non-integer `refresh` is a loud validation
error, but `0`/negative are now valid; `ttl_seconds` is no longer a recognized
column; supersedes 001 FR-001a/FR-001b for that column; scheduling-only, so
served bytes and integration snapshots are unchanged).

Key reading for any change:
- `specs/001-subscription-aggregator/spec.md` + `plan.md` — what the service does today (27 FRs, 15 SCs) and how it's structured
- `specs/002-namespacing-and-regions/spec.md` + `plan.md` — per-source `<provider>_` prefixing, trailing-rule drop, `region_<CC>` proxy-groups via ISO 3166-1 alpha-2 inference
- `specs/003-custom-rules-access-control/spec.md` + `plan.md` — custom rules with priorities (YAML files in folder), `_continent_<CONT>` proxy groups via country-to-continent mapping, `_region_UNKNOWN` catch-all group, User-Agent access control via `HONKAI_RULE_CLIENT_UA`
- `specs/004-yaml-output-formatting/spec.md` + `plan.md` — proxy-groups block format with field ordering (name, type, proxies first), rule priority comments in served YAML
- `specs/005-unified-rule-priority/spec.md` + `plan.md` — unified priority sort across upstream sources and custom rule sets; `# --- priority N (contributor-list) ---` headers replace the `# --- upstream ---` divider. Sort direction was reversed to ascending by feature 007.
- `specs/007-ascending-priority-sort/spec.md` + `plan.md` — flipped 005's rule comparator to ascending so lower priority numbers win routing precedence (matches Mihomo's top-to-bottom evaluation)
- `specs/006-fix-emoji-yaml-escape/spec.md` + `plan.md` — post-encode byte transform that replaces yaml.v3's `\Uxxxxxxxx` escapes with literal UTF-8 so emoji proxy names render readably in served YAML
- `specs/008-dialer-proxy-fanout/spec.md` + `plan.md` — for each operator-declared own-proxy emit fan-out copies (`via_<group>__<own>` and `via_AUTO__<own>`) carrying the source own-proxy's connection fields plus a `dialer-proxy:` field set to a server-emitted region/continent group (or to the always-present `Proxies` selector for AUTO); also exclude own-proxies and `via_*` copies from the global `Proxies` selector's member list
- `specs/009-cluster-deploy/spec.md` + `plan.md` — build+push the container image to `registry.example.com/library/honkai-rule-server:<sha>` (registry now `ghcr.io/<owner>/honkai-rule-server` per 013), deploy to the cluster via a new Helm chart in `<your-iac-repo>/charts/honkai-rule-server/` and a new Argo CD Application, served on `example.com` behind a 32-char hex path prefix; PVC for custom-rules + cache, ConfigMap for subscriptions/own-proxies/tokens; new Makefile targets `docker-push`, `docker-push-latest`, `config-sync`, `rules-sync` (the last via a busybox helper pod since the runtime image is `FROM scratch`)
- `specs/010-daily-traffic-header/spec.md` + `plan.md` — replace the served `Subscription-Userinfo` header's raw aggregates with the daily-allowance figure from 001 FR-011b (`total - upload - download = per-day-rate + no-expiry-remaining`, `expire = next 00:00 UTC`); wire format unchanged; raw aggregates remain on `/health`; header omitted entirely when no source supplied userinfo
- `specs/012-url-test-region-groups/spec.md` + `plan.md` — convert auto-emitted `_region_*` and `_continent_*` proxy groups from `select` to `url-test` with operator-configurable health-check params (5 env vars: `URL_TEST_URL`, `URL_TEST_INTERVAL_SECONDS`, `URL_TEST_TIMEOUT_MS`, `URL_TEST_MAX_FAILED_TIMES`, `URL_TEST_LAZY`); always-present `Proxies` selector and operator-defined custom groups untouched
- `specs/011-daily-spend-tracking/spec.md` + `plan.md` — track today's spend in the served `Subscription-Userinfo` header so the client UI's bar fills as the user consumes (010 left it flat at 0%); persistent `/data/today-zero.json` snapshot of per-source midnight baselines + pinned allowance, lazy request-driven rollover, America/Toronto local-day boundary; new `internal/dailyspend/` package owns file I/O outside the pure-merge boundary
- `specs/013-ci-container-release/spec.md` + `plan.md` — active feature: GitHub Actions workflows publishing the container image to `ghcr.io/<owner>/honkai-rule-server` on `master` push (tags `master` + `sha-<short>`) and on `v*` SemVer tag push (tags `vX.Y.Z` + `vX.Y` + `vX` + `latest`, with hotfix-vs-`latest` precedence rule); Makefile gains `release-{patch,minor,major}` + `release VERSION=` + `DRY_RUN=1` helpers; `.github/dependabot.yml` covers gomod / github-actions / docker ecosystems; `dependabot-auto-merge.yml` skips major bumps via `dependabot/fetch-metadata`; `auto-patch-release.yml` runs daily and cuts `vX.Y.(Z+1)` when Dependabot commits accumulate on master since the last release tag; replaces 009's `registry.example.com/library/honkai-rule-server` placeholder with GHCR as canonical registry
- `specs/014-load-balance-region-groups/spec.md` + `plan.md` — active feature: additive `_lb_region_<CC>` / `_lb_continent_<CONT>` proxy groups of `type: load-balance` emitted alongside 012's url-test groups (same membership, paired adjacency in `proxy-groups:`); operator-configurable via six new `LOAD_BALANCE_*` env vars (`LOAD_BALANCE_URL`, `LOAD_BALANCE_INTERVAL_SECONDS=300`, `LOAD_BALANCE_TIMEOUT_MS=1500`, `LOAD_BALANCE_MAX_FAILED_TIMES=3`, `LOAD_BALANCE_LAZY=true`, `LOAD_BALANCE_STRATEGY=round-robin` accepting also `consistent-hashing`/`sticky-sessions`); 008 fan-out predicate widens to include `_lb_region_`/`_lb_continent_` prefixes producing `via_lb_region_<CC>__<own>` / `via_lb_continent_<CONT>__<own>` copies; lb groups join the always-present `Proxies` selector; no AUTO_lb variant (008's `via_AUTO__<own>` unchanged); url-test groups (012) byte-unchanged; field order on lb groups is `name, type, proxies, url, interval, lazy, strategy, timeout, max-failed-times` — distinct from url-test order
- `specs/015-remove-empty-proxy-groups/spec.md` + `plan.md` — active feature: a final prune pass at the end of `Pipeline.Build()` removes every proxy-group with an empty `proxies:` member list so the served config passes Mihomo validation; single removal pass (no cascading — FR-005); the always-present `Proxies` selector is exempt and retained even when empty (FR-007); dangling member references to removed groups are dropped (FR-006); a routing rule whose target group was pruned is redirected to the configured fallback rule target (FR-008); new pure fn `PruneEmptyProxyGroups` in `internal/merge/prune.go`; output byte-unchanged when no group is empty (FR-010); auto-emitted `_region_*`/`_continent_*`/`_lb_*` groups are non-empty by construction so only operator/upstream groups are real prune candidates
- `specs/016-rule-set-support/spec.md` + `plan.md` — active feature: read upstream `rule-providers:` mappings and serve a merged, namespaced block. Provider keys + the `RULE-SET` rule's provider-name field (field[1]) get the `<source>_` prefix; provider defs get a source-distinct `path:` and a prefixed non-builtin `proxy:` (FR-007/008); unbacked `RULE-SET` rules dropped per-source before the unified merge (FR-009, logged); only referenced providers emitted, key omitted when none (FR-006/010); new `internal/merge/ruleset.go` + `findChildMapping` in `yamlutil.go` + `MergedConfig.RuleProviders *yaml.Node` + one guarded emit in `internal/output/subscription_mode.go`; byte-unchanged output when no source has `rule-providers`/`RULE-SET` (FR-013)
- `specs/017-per-source-refresh-interval/spec.md` + `plan.md` — renames 001's optional `ttl_seconds` CSV column to `refresh` with tri-state semantics (`0`/absent → server default interval, positive → interval seconds, negative → never refresh); `SubscriptionRow.RefreshSeconds` + scheduler `refreshDisabled`/`neverRefreshTTL`; never-refresh sources bootstrap once then skip the `runSteady` ticker and never report stale; scheduling-only, so served bytes / snapshots unchanged
- `specs/003-custom-rules-access-control/quickstart.md` — operator guide (custom rules YAML schema, continent groups, UA filtering setup)
- `internal/merge/` — pure-functional transformation core (Constitution Principle I; do not reach into it from outside the module)
- `internal/customrules/` — custom rule file loading and `CustomRuleSet` type
- `internal/integration/testdata/snapshots/` — deterministic snapshots; CI fails on drift (Constitution Principle II)

Tech: Go 1.25 toolchain (declared 1.22+), stdlib `net/http`, `log/slog`,
`gopkg.in/yaml.v3`, `golang.org/x/sync/singleflight`, `fsnotify`,
`bradleyjkemp/cupaloy/v2` for snapshots. `make check` runs vet +
staticcheck + tests + snapshot-drift check.

002 deviation from Constitution Principle III (CSV strict-schema-loud-fail):
the `^[a-z]+$` rule on `SubscriptionRow.Name` is enforced as **warn +
skip the offending row** rather than loud-fail-abort. Justified in
`specs/002-namespacing-and-regions/plan.md` Complexity Tracking.
<!-- SPECKIT END -->
