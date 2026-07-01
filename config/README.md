# config/

Operator-managed configuration files. **All three files in this directory are gitignored** (per FR-016 — they carry provider tokens and proxy credentials). Only `.gitignore` and this README are committed.

## Files the server expects

| File | Env var pointing at it | Format |
|---|---|---|
| `subscriptions.csv` | `SUBSCRIPTIONS_CSV_PATH` | CSV — `name,link,priority,enable` (+ optional `refresh`, `stale_on_error_seconds`) |
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

## Controlling a source's refresh interval

The optional `refresh` column on each `subscriptions.csv` row sets how often the
server re-fetches that upstream (integer **seconds**):

| `refresh` value | Behavior |
|---|---|
| omitted / empty / `0` | Use the server default interval (`DEFAULT_TTL_SECONDS`, default 3600s) |
| positive (e.g. `900`) | Re-fetch every that many seconds |
| negative (e.g. `-1`) | **Never refresh** — fetch once at startup, then never again |

```csv
name,link,priority,enable,refresh
alpha,"https://…",1000,Enable,0        # default interval
bravo,"https://…",1500,Enable,900     # refresh every 15 minutes
static,"https://…",2000,Enable,-1     # fetch once, never refresh
```

A never-refresh source still bootstraps at startup (so it contributes to the
merged config) and, while the process runs, its snapshot is never treated as
stale and is never re-fetched on a schedule. Two things to know:

- **On restart** it behaves like any other source at bootstrap: if the persisted
  disk cache for that source is older than the default interval
  (`DEFAULT_TTL_SECONDS`) it re-fetches fresh (so an edited `link` is picked up);
  a still-fresh cache is reused to avoid re-hammering upstream.
- **If the bootstrap fetch fails** (upstream down at startup), a never-refresh
  source has no ticker to self-heal, so it stays failed on `/health` until the
  next restart.

Like all CSV changes, editing `refresh` takes effect on the next server restart
(v1 limitation).

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
