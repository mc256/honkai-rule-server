# Contract: Served-Config Proxy-Group Invariant

The project's external interface is the subscription-mode Clash/Mihomo YAML served
to clients. This feature strengthens one invariant of that contract. No HTTP
endpoint, header, or request shape changes.

## Scope

Applies to the `proxy-groups:` and `rules:` blocks of the served subscription-mode
YAML body. The `proxies:` block, headers (`Subscription-Userinfo`,
`Profile-Update-Interval`, `Content-Type`, `Cache-Control`), status codes, and all
other top-level keys are unchanged.

## Invariant (post-feature)

For every served subscription-mode response:

1. **No empty proxy-group** — every item in `proxy-groups:` has a `proxies:` member
   list with at least one entry, **except** the always-present `Proxies` selector,
   which is always present even if its member list is empty.
2. **No dangling member reference** — no item in `proxy-groups:` lists, in its
   `proxies:` member list, a name that is neither a defined proxy nor a proxy-group
   present in the same served `proxy-groups:` block.
3. **No dangling rule target** — no entry in `rules:` names, as its target, a
   proxy-group that is absent from the served `proxy-groups:` block. A rule whose
   target group was removed names the configured fallback rule target instead.
4. **Stability** — when the merged configuration contains no empty proxy-group, the
   served `proxy-groups:` and `rules:` blocks are byte-for-byte identical to the
   pre-feature output.
5. **Order & attributes preserved** — surviving proxy-groups keep their pre-feature
   relative order and every attribute (`name`, `type`, `url`, `interval`, `lazy`,
   `strategy`, `timeout`, `max-failed-times`, …).

## Consumer expectation (Mihomo client)

A Mihomo client loading the served body:

- MUST NOT encounter an "empty proxy-group" / "proxy-group has no proxies"
  validation error (other than for the always-present `Proxies` selector, which is
  expected to be non-empty in normal operation — see spec Assumptions).
- MUST NOT encounter an "unknown proxy" / "rule references undefined proxy" error
  caused by a member reference or rule target that points at a removed group.

## Verification

| Invariant | Verified by |
|-----------|-------------|
| 1, 2, 3, 5 | Integration snapshot `served-config-prune.snap.yaml` (new fixture with an empty operator group) + parse-and-assert in `internal/integration/prune_test.go` |
| 4 | Existing `served-config.snap.yaml` shows zero drift (its fixtures yield no empty group) |
| 1, 2, 3, 5 (unit level) | `internal/merge/prune_test.go` against hand-built `[]*yaml.Node` inputs |

## Non-goals (explicitly outside this contract)

- Cascading removal: a proxy-group emptied solely by reference cleanup is not itself
  removed (single-pass — spec FR-005).
- The override-mode JS payload (adapter not yet implemented; inherits the behavior
  from the shared core when it lands).
- Any change to how the always-present `Proxies` selector is built or populated.
