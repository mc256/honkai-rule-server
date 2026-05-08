# Quickstart: Provider Namespacing & Region Grouping

**Feature**: `002-namespacing-and-regions` | **Date**: 2026-04-30

This is the operator's "go from your existing 001 deployment to a working 002 deployment" guide once the implementation lands. It documents the one **mandatory** migration step (the source-name rename) and the one **optional** new knob (`FALLBACK_RULE_TARGET`).

---

## What changed for operators (TL;DR)

1. **Source name rule is stricter**: every row's `name` in your `subscriptions.csv` MUST now match `^[a-z]+$` (one or more lowercase ASCII letters; **no underscores, no digits, no uppercase**). Rows that don't match are skipped with a stdout warning at load time — they don't break the server, but their proxies/groups/rules are missing from the merged output. **If you currently have a name like `beta` or `Alpha2024`, rename it.**
2. **Every merged proxy / proxy-group / rule target is now prefixed** with the source name and an underscore. `Node1` from source `alpha` becomes `alpha_Node1`. Built-in identifiers (`DIRECT`, `REJECT`, `REJECT-DROP`, `PASS`) are never prefixed.
3. **Every upstream's last rule is dropped** at merge time (typically the `MATCH,auto` catch-all that would otherwise short-circuit a multi-source merge).
4. **The server appends a single final fallback rule** of the form `MATCH,auto` (or override via `FALLBACK_RULE_TARGET`) so unmatched traffic has somewhere to go.
5. **Your own-proxies and own-proxy-groups are now prefixed with a single underscore** (`_<original-name>`). An own-proxy named `my-canada-1` becomes `_my-canada-1`; an own-group named `my-canada-pool` becomes `_my-canada-pool` (and any `proxies:` entry in that group referring to an own-proxy is rewritten to match). The leading underscore tags these as operator/server-emitted (distinct from upstream-prefixed names that start with a lowercase letter, and from built-ins which are uppercase).
6. **New `_region_<CC>` proxy-groups** appear in the merged output, derived from country indicators (emoji flags / Chinese / English) in **upstream-sourced** proxy display names. Examples: `_region_HK`, `_region_US`, `_region_CN`. The leading underscore matches the own-group convention because region groups are server-emitted, not from any single upstream. These are also pickable from the always-present `Proxies` selector. **Own-proxies are excluded from region groups** — even if your own-proxy is named `🇨🇦 my-canada-1`, it does not get added to `_region_CA`; you address own-proxies through their underscore-prefixed own-groups instead.

---

## 1. Migrate your subscriptions CSV (mandatory)

Open your `${SUBSCRIPTIONS_CSV_PATH}` file. Inspect each row's `name`. If any row's `name` contains anything other than lowercase ASCII letters, rename it.

Common renames:

| Old name | Why it fails | Migration |
|---|---|---|
| `beta` | underscore | rename to `beta` |
| `Alpha2024` | uppercase + digit | rename to `alpha` |
| `provider-1` | hyphen + digit | rename to `provider` (or `providerone` if you have multiple) |
| `测试源` | non-ASCII | rename to `testsource` (or any meaningful lowercase ASCII) |

Example diff for the canonical example:

```diff
 name,link,priority,enable
 alpha,https://upstream.example.com/link/<your-token>?clash=1,1000,Enable
-beta,https://upstream.example.com:8443/<your-path-token>,2000,Enable
+beta,https://upstream.example.com:8443/<your-path-token>,2000,Enable
```

Reload the server (or restart it). At startup, look for log events:
- `name-format-violation` — a row was skipped because its name didn't match `^[a-z]+$`. Fix the CSV.
- No such event — you're good.

**What if you can't rename right now?** The server will continue to serve. The offending rows just won't contribute to the merged output. Other rows are unaffected.

---

## 2. (Optional) Configure the fallback rule target

At the end of every merged subscription, the server emits exactly one `MATCH,<target>` rule. The default target is the literal string `auto` — chosen because Mihomo clients typically have or expect a proxy-group named `auto` for the catch-all case.

If your client doesn't have an `auto` group, set the target to something else via the `FALLBACK_RULE_TARGET` env var. Common values:

- `auto` — default. Routes unmatched traffic through whichever proxy your client's `auto` group points at.
- `Proxies` — routes unmatched traffic through the always-present global selector group from 001's FR-009a (which lists every individual node as well as every region group).
- `DIRECT` — sends unmatched traffic directly without any proxy.
- `REJECT` — drops unmatched traffic entirely (rare; use only for hardened policies).

Example deployment:

```yaml
# k8s deployment env
- name: FALLBACK_RULE_TARGET
  value: "Proxies"
```

The chosen value is recorded at startup in a structured log line:

```json
{"time":"...","level":"INFO","msg":"fallback-rule-target resolved","target":"Proxies"}
```

---

## 3. (Optional) Read the new region groups

After this feature lands, fetch your subscription endpoint and inspect the `proxy-groups:` block. New entries will look like:

```yaml
proxy-groups:
  # ... your prefixed per-source groups (alpha_Auto, beta_Auto, etc.) ...
  - name: _region_CN
    type: select
    proxies:
      - alpha_中国移动 01
      - alpha_中国电信 02
      - beta_China-1
  - name: _region_HK
    type: select
    proxies:
      - alpha_🇭🇰 香港 01
      - beta_HK-Premium-2
  # ... etc ...
  # Your own-groups (each prefixed with a single underscore):
  - name: _my-canada-pool
    type: select
    proxies:
      - _my-canada-1   # your own-proxy reference, also prefixed
      - DIRECT         # built-ins are NOT prefixed
  - name: Proxies
    type: select
    proxies:
      - alpha_中国移动 01     # upstream individual nodes
      - beta_HK-Premium-2
      - _my-canada-1        # own-proxies (underscore-prefixed)
      - _region_CN           # region groups also pickable from here
      - _region_HK
      # ...
```

Pick `_region_HK` from your client's UI to route through any HK exit (regardless of which provider it came from), or `_region_US` for any US exit, etc.

---

## 4. (Optional) Extend the country mapping table

If your provider uses a country/region indicator that isn't recognized, the server logs:

```json
{"time":"...","level":"INFO","msg":"region-unmapped-indicator","fragment":"中国上海"}
```

To add coverage:

1. Open `internal/merge/region_table.go`.
2. Add a row to `regionTable`. Place specific entries (e.g., `中国上海`) before more general ones (`中国`):

```go
{Indicator: "中国上海", Code: "CN", Lang: "zh"},
```

3. Add a unit test in `region_table_test.go`.
4. Rebuild, redeploy, restart.

The PR review checklist for table extensions: every new row's Code is exactly two uppercase ASCII letters, Lang is `"zh"` or `"en"`, and the row's placement respects the specificity ordering (more specific before more general).

---

## 5. Sanity check (post-deploy)

After upgrading, run the following from your client machine:

```bash
# Replace with your actual subscription URL
curl -sS "https://your-subscription-url/?token=<your-token>" \
  | yq '.proxies[].name' \
  | head -5
```

Expected output: every name starts with one of your CSV `name` values plus `_`. If you see anything that doesn't, file an issue with the bare names.

```bash
# Verify the trailing rule is the server-emitted fallback
curl -sS "https://your-subscription-url/?token=<your-token>" \
  | yq '.rules[-1]'
```

Expected output: `MATCH,auto` (or whatever you set `FALLBACK_RULE_TARGET` to).

```bash
# Verify region groups exist
curl -sS "https://your-subscription-url/?token=<your-token>" \
  | yq '.proxy-groups[] | select(.name | startswith("_region_")) | .name'
```

Expected output: a list of region groups derived from your upstream providers' nodes (e.g., `_region_CN`, `_region_HK`, `_region_US`, ...). Note: own-proxies are excluded from region groups — they live only in their underscore-prefixed own-groups.

---

## 6. Roll back

If something goes wrong, the rollback path is the inverse of the migration:

1. Deploy the pre-002 binary (the `001` release tag or the previous git SHA).
2. Optionally revert the CSV `name` rename — pre-002 binaries accept names with underscores / digits / uppercase.
3. Restart.

The merged output reverts to the 001 form (suffix-on-collision instead of always-prefix; no region groups; no server-emitted trailing MATCH).

---

## 7. CI / dev workflow notes

If you're a contributor (not a deployer):

- `make check` will fail until snapshots are regenerated. Run `UPDATE_SNAPSHOTS=true go test ./internal/integration/...` once after pulling, then commit the snapshot diffs together with your code change.
- Snapshot diffs in this PR are large and intentional. Reviewer attention should focus on:
  - Every new prefixed name is sensible.
  - The very last rule is `MATCH,auto`.
  - Every emitted `_region_<CC>` group has plausible membership (only upstream-prefixed proxies, never own-proxies).
  - `health.snap.json` shows the renamed source name (`beta` → `beta`) — no other drift.
