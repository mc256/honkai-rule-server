# config/

Operator-managed configuration files. **All three files in this directory are gitignored** (per FR-016 — they carry provider tokens and proxy credentials). Only `.gitignore` and this README are committed.

## Files the server expects

| File | Env var pointing at it | Format |
|---|---|---|
| `subscriptions.csv` | `SUBSCRIPTIONS_CSV_PATH` | CSV — `name,link,priority,enable` (+ optional `ttl_seconds`, `stale_on_error_seconds`) |
| `own-proxies.yaml` | `OWN_PROXIES_YAML_PATH` | YAML — `proxies` + `proxy-groups` keys (Clash native shape) |
| `tokens.json` | `TOKENS_PATH` | JSON — array of per-client tokens |

The example fixtures committed under [`internal/integration/testdata/fixtures/`](../internal/integration/testdata/fixtures/) match the schemas exactly — copy them as a starting point.

## Generating a new client token

```bash
TOKEN=$(openssl rand -hex 32)
echo "Subscription URL: http://localhost:8080/?token=$TOKEN"
# then add to tokens.json:
jq --arg t "$TOKEN" --arg label "$(hostname)" --arg now "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
   '.tokens += [{"token": $t, "label": $label, "issued_at": $now, "revoked": false}]' \
   tokens.json > tokens.json.new && mv tokens.json.new tokens.json
```

The server hot-reloads `tokens.json` within ~250ms via `fsnotify` (FR-017).

## Revoking a token

Set `"revoked": true` on the offending entry in `tokens.json`. The next `Lookup` call returns `(nil, false)` and the client gets a 401 on its next refresh. No restart needed.

## Disabling an upstream temporarily

Edit `subscriptions.csv`, change the row's `enable` column from `Enable` to `Disable`. Currently the subscriptions CSV is loaded once at startup, so this requires a server restart in v1 (CSV hot-reload is a follow-up). The disabled source is loaded, validated, and surfaced in `/health` with `enabled: false` — but never fetched.

## Production secret rotation

In Kubernetes, all three files live in a single `Secret` mounted at `/secret/`. Rotate via:

```bash
kubectl create secret generic honkai-rule-server-config \
  --from-file=subscriptions.csv=./config/subscriptions.csv \
  --from-file=own-proxies.yaml=./config/own-proxies.yaml \
  --from-file=tokens.json=./config/tokens.json \
  --dry-run=client -o yaml | kubectl apply -f -
```

Kubernetes atomically replaces the mounted file and `fsnotify` picks it up — no pod restart needed for token changes. Subscriptions CSV changes still need a restart per the v1 limitation noted above.

## Securing this directory locally

```bash
chmod 700 config/
chmod 600 config/*.csv config/*.yaml config/*.json
```

The `.gitignore` in this directory keeps the files out of version control by default. Verify before each commit:

```bash
git status -- config/
# should show only .gitignore + README.md as tracked
```
