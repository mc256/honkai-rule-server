# Quickstart: Dialer-Proxy Fan-Out for Own Proxies

This is the operator-facing guide for feature 008. The 001/002/003 quickstarts remain authoritative for setup; this document only covers what changes after 008 lands.

## What changed for the operator

If you have entries in `config/own-proxies.yaml`, the served subscription YAML now contains additional auto-generated proxies that let any of your own exits be reached "via" any region/continent pool emitted by the merge — plus an AUTO variant per own-proxy that follows whatever the user has currently selected in the global `Proxies` selector. Concurrently, your own-proxies are no longer members of the `Proxies` selector group; you reach them through your own-groups, custom rules, or the new `via_*` copies.

## Worked example

Given `config/own-proxies.yaml`:

```yaml
proxies:
  - name: montreal
    type: ss
    server: node.example.com
    port: 10755
    cipher: chacha20-ietf-poly1305
    password: '<your-password>'
    udp: true
    ip-version: dual
  - name: markham
    type: ss
    server: 173.32.232.215
    port: 8080
    cipher: xchacha20-ietf-poly1305
    password: 'JZPZ146HNR1xmIx20JzJ'
    udp: true
    ip-version: ipv4

proxy-groups:
  - name: Canada-Exit-Proxies
    type: select
    proxies:
      - montreal
      - markham
```

And an upstream classification that produces `_region_HK`, `_region_JP`, `_region_US`, plus `_continent_AS`, the served `proxies:` block now contains (in addition to upstream and own-proxies):

```yaml
proxies:
  # ... upstream-prefixed proxies (unchanged) ...
  - name: _montreal           # original own-proxy (002 rewrite)
    type: ss
    server: node.example.com
    # ... (rest unchanged) ...
  - name: _markham            # original own-proxy (002 rewrite)
    # ... (rest unchanged) ...
  - name: via_AUTO__montreal              # AUTO copy of montreal
    type: ss
    server: node.example.com
    port: 10755
    cipher: chacha20-ietf-poly1305
    password: '<your-password>'
    udp: true
    ip-version: dual
    dialer-proxy: Proxies
  - name: via_region_HK__montreal
    # ... (same fields as above, but) ...
    dialer-proxy: _region_HK
  - name: via_region_JP__montreal
    dialer-proxy: _region_JP
  - name: via_region_US__montreal
    dialer-proxy: _region_US
  - name: via_continent_AS__montreal
    dialer-proxy: _continent_AS
  - name: via_AUTO__markham
    dialer-proxy: Proxies
  - name: via_region_HK__markham
    dialer-proxy: _region_HK
  - name: via_region_JP__markham
    dialer-proxy: _region_JP
  - name: via_region_US__markham
    dialer-proxy: _region_US
  - name: via_continent_AS__markham
    dialer-proxy: _continent_AS
```

## How to use the new `via_*` proxies

### From a custom rule (003)

Any custom rule whose target is a fan-out proxy name routes matching traffic through the chain. Example: route Google Chrome's update domain through Markham via Japan:

```yaml
# config/custom-rules/early-exit-google-chrome.yaml
priority: 200
rules:
  - DOMAIN-SUFFIX,update.googleapis.com,via_region_JP__markham
```

When the upstream pool changes (e.g., a new Japanese node appears), the operator does nothing — the same rule continues to match, and `_region_JP`'s membership refreshes automatically on the next reload.

### From an own-group (`own-proxies.yaml`)

Operators who want a UI-pickable pool of fan-out copies can declare a select group:

```yaml
# config/own-proxies.yaml
proxy-groups:
  - name: Markham-Anywhere
    type: select
    proxies:
      - via_AUTO__markham
      - via_region_HK__markham
      - via_region_JP__markham
      - via_region_US__markham
      - via_continent_AS__markham
```

After 002's rewrite, the group becomes `_Markham-Anywhere`, and it appears as a selectable pool in Mihomo's UI alongside upstream pools (own-groups already appear in `proxy-groups:`).

### Via the AUTO variant

`via_AUTO__<own>`'s `dialer-proxy` is set to the literal global `Proxies` selector. The user picks once at the global selector ("any HK exit", "Japan", an upstream proxy, anything), and every `via_AUTO__<own>` rule routes through that pick. No config reload required when the user re-selects.

## How to opt out (per own-proxy)

If you want one specific own-proxy to keep an explicit chain choice and NOT receive any fan-out copies, set `dialer-proxy:` on it in `own-proxies.yaml`:

```yaml
proxies:
  - name: special-direct-exit
    type: ss
    # ... fields ...
    dialer-proxy: DIRECT     # operator's explicit chain choice
```

The merge layer detects the explicit field and skips both per-group and AUTO fan-out for `special-direct-exit`. The original own-proxy is emitted unchanged.

## Why your own-proxies are no longer in the global `Proxies` selector

The global `Proxies` selector group used to contain every upstream proxy AND every own-proxy. After 008, own-proxies are removed from that list (and `via_*` fan-out copies are never added to it). Reasons:

1. With many region/continent groups (10–30+), the fan-out adds ≈10 × 17 = 170 entries per own-proxy. That dwarfs the upstream entries and makes the global selector unusable as a UI picker.
2. Operators already address own-proxies through:
   - their own-groups (the `Canada-Exit-Proxies` group above, post-rewrite `_Canada-Exit-Proxies`, is a member of `proxy-groups:` and appears as a selectable pool in Mihomo's UI),
   - custom rules (with `_<own>` or `via_*` as targets),
   - or the explicit AUTO variant.

If you want your own-proxies back in `Proxies`, declare a select group in `own-proxies.yaml` whose members are `_<own1>, _<own2>, ...` and reference *that* group from wherever you'd previously have used `Proxies` to select an own-exit.

## Verification commands

After a deploy, sanity-check:

```sh
# Fetch the served body
curl -sS https://your-server/<token>/config > /tmp/served.yaml

# Count fan-out copies
yq '.proxies | map(select(.name | test("^via_"))) | length' /tmp/served.yaml

# List AUTO variants
yq '.proxies | map(select(.name | test("^via_AUTO__"))) | .[].name' /tmp/served.yaml

# Confirm own-proxies are NOT in the global Proxies group
yq '.proxy-groups | map(select(.name == "Proxies")) | .[0].proxies | map(select(test("^_") and (test("^_(region|continent)_") | not)))' /tmp/served.yaml
# Expected: empty list (no own-proxies in the global selector).

# Confirm a fan-out copy carries dialer-proxy
yq '.proxies | map(select(.name == "via_region_JP__markham")) | .[0].dialer-proxy' /tmp/served.yaml
# Expected: _region_JP
```

## Determinism check

```sh
# Two consecutive fetches should be byte-identical
curl -sS https://your-server/<token>/config | sha256sum
curl -sS https://your-server/<token>/config | sha256sum
# Expected: identical hashes.
```

(This is the same Constitution Principle II invariant covered by the existing snapshot-stability tests.)
