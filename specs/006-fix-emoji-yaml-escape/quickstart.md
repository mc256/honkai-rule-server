# Operator Quickstart: Preserve Emoji in Served YAML

**Feature**: [spec.md](./spec.md) | **Plan**: [plan.md](./plan.md) | **Date**: 2026-05-01

## What's changing

The served subscription YAML now contains literal UTF-8 emoji and other
supplementary-plane Unicode characters, instead of the unreadable
`\Uxxxxxxxx` escape sequences.

No environment variables change. No CSV columns change. No custom-rule YAML
fields change. No HTTP headers change. The only difference is the bytes
inside the served `rules:` / `proxies:` / `proxy-groups:` sections.

## Before / after

Suppose your upstream provides a proxy named `🔰 USA-Premium` and a
proxy-group named `🚀 Auto`.

### Today (pre-fix)

```yaml
proxies:
  - {name: "alpha_\U0001F530 USA-Premium", type: trojan, server: ...}

proxy-groups:
  - name: "alpha_\U0001F680 Auto"
    type: select
    proxies:
      - "alpha_\U0001F530 USA-Premium"
```

### After feature 006

```yaml
proxies:
  - {name: alpha_🔰 USA-Premium, type: trojan, server: ...}

proxy-groups:
  - name: alpha_🚀 Auto
    type: select
    proxies:
      - alpha_🔰 USA-Premium
```

The strings are byte-equivalent at the YAML semantic level — Mihomo / Clash
clients see identical names and route identically. The only difference is
that you (the operator) can now read the file in your editor and see
familiar emoji glyphs, not Unicode escape codes.

## What does NOT change

- Mihomo / Clash routing behavior (identical to today; the escape and the
  literal form are semantically equivalent)
- HTTP headers (`Subscription-Userinfo`, `Profile-Update-Interval`,
  `Content-Type`, `Cache-Control`)
- Per-source proxy-name namespacing (`alpha_…`, `beta_…`) — feature 002
- Region / continent / unknown proxy groups — feature 002 / 003
- Custom-rules YAML schema and load behavior — feature 003
- Unified rule priority and per-priority header comments — feature 005
- Rule order and content
- Determinism: 100 sequential fetches still produce 100 byte-identical responses

## Verification

```bash
# Restart the server (or just trust the next bootstrap cycle).
# Then fetch and look for either form:

# Should print zero (no escape sequences left):
curl -s "http://localhost:8080/?token=<your-token>" \
  -H "User-Agent: clash-meta/v1.18.0" \
  | grep -cE '\\U[0-9A-Fa-f]{8}'

# Should print proxy names with literal emoji:
curl -s "http://localhost:8080/?token=<your-token>" \
  -H "User-Agent: clash-meta/v1.18.0" \
  | grep -E 'name:.*🔰' \
  | head -5
```

## Updating downstream snapshots

If you maintain any downstream artifact that captures the served body
(e.g., a test snapshot, a deployment-time validator), refresh it once.
The diff is escape→literal substitutions only; no rule, proxy, or group
content changes. Two specific patterns to expect in the diff:

```diff
-  - name: "alpha_\U0001F530国外流量"
+  - name: alpha_🔰国外流量

-      - "alpha_\U0001F381自动选择"
+      - alpha_🎁自动选择
```

This project's own committed snapshot at
`internal/integration/testdata/snapshots/served-config.snap.yaml` contains
~759 such substitutions and is regenerated as part of feature 006.
