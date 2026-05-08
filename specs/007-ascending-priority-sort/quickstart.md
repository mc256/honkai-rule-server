# Operator Quickstart: Ascending Priority Sort

**Feature**: [spec.md](./spec.md) | **Plan**: [plan.md](./plan.md) | **Date**: 2026-05-01

## What's changing

The order of priority buckets in the served `rules:` block is reversed.
Smaller priority numbers now appear *before* larger ones, so a
custom rule at priority 200 will be matched by Mihomo / Clash *before* an
upstream rule at priority 1000.

No environment variables change. No CSV columns change. No custom-rule
YAML fields change. No HTTP headers change. The `priority` field's data
type and validation are unchanged. The only change is the order in which
rule-block headers and their rules appear inside the served `rules:`
sequence.

## Before / after

Suppose `subscriptions.csv`:

```csv
name,link,priority,enable
alpha,https://alpha.example.com/...,1000,Enable
beta,https://berry.example.com/...,2000,Enable
```

And `config/custom-rules/`:

```yaml
# corporate-block.yaml
priority: 500
rules:
  - DOMAIN-SUFFIX,blocked.example.com,REJECT

# early-exit-google-chrome.yaml
priority: 200
rules:
  - DOMAIN-SUFFIX,chrome.google.com,DIRECT
```

### Today (post-005, descending — wrong)

```yaml
rules:
  # --- priority 2000 (beta) ---
  - <beta rules>
  # --- priority 1000 (alpha) ---
  - <alpha rules>
  # --- priority 500 (corporate-block) ---
  - DOMAIN-SUFFIX,blocked.example.com,REJECT
  # --- priority 200 (early-exit-google-chrome) ---
  - DOMAIN-SUFFIX,chrome.google.com,DIRECT
  - MATCH,auto
```

In this layout, **alpha's rules match Chrome traffic before** the
operator-supplied early-exit rule does — the operator's intent is
violated.

### After feature 007 (ascending — correct)

```yaml
rules:
  # --- priority 200 (early-exit-google-chrome) ---
  - DOMAIN-SUFFIX,chrome.google.com,DIRECT
  # --- priority 500 (corporate-block) ---
  - DOMAIN-SUFFIX,blocked.example.com,REJECT
  # --- priority 1000 (alpha) ---
  - <alpha rules>
  # --- priority 2000 (beta) ---
  - <beta rules>
  - MATCH,auto
```

The operator's early-exit rule (priority 200) is now matched first.
`alpha` (priority 1000) is matched only when no lower-priority rule has
matched. The MATCH fallback remains last, with no preceding header.

## Mental model

Think of priority as **routing precedence**, not z-index:

| Domain | Lower number | Higher number |
|---|---|---|
| Mihomo rule order | matches first | matches last |
| Linux `nice` | runs first | runs later |
| Priority queue (min-heap) | dequeued first | dequeued later |
| `priority: 1` in any task list | top of the list | bottom of the list |

The descending-order behavior shipped in feature 005 was inherited from
a CSS z-index analogy ("higher = on top of the stack") that does not
match how operators reason about rule precedence. Feature 007 corrects
this.

## What does NOT change

- Mihomo / Clash routing semantics: the proxy client still evaluates
  rules top-to-bottom, first match wins. The set of valid rule strings
  is unchanged. Only their order in the file changes.
- Tie-break for contributors sharing a priority: still alphabetical by
  contributor name.
- Header comment format: still `# --- priority N (contributor-list) ---`.
- The MATCH fallback: still last, no header.
- `subscriptions.csv` schema, custom-rule YAML schema, environment
  variables, HTTP headers, proxy/proxy-group ordering.
- 100-fetch determinism (SC-002).

## Migrating existing operator configurations

If you previously **set high priority numbers (e.g., 5000) on custom
rules expecting them to override upstream rules** — under feature 005's
descending sort, that worked. Under feature 007's ascending sort, that
configuration now does the *opposite*: priority 5000 emits last, after
every upstream source. To preserve the override-upstream behavior, set
custom-rule priority to a number *smaller* than the smallest upstream
priority you want to override.

For example, with upstream priorities of 1000 and 2000:
- To override both, use custom priority 1–999.
- To override only `alpha` (priority 1000), use custom priority 1001–1999.

## Verification

```bash
# After the fix lands, restart the server and curl:
curl -s "http://localhost:8080/?token=<your-token>" \
  -H "User-Agent: clash-meta/v1.18.0" \
  | grep -E '^\s*# --- priority' \
  | head -10
```

Should print priority headers in **ascending** order — smallest first,
largest last (and no header line for the trailing MATCH).
