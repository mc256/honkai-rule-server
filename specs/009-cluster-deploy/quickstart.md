# Quickstart: Deploy honkai-rule-server to the cluster

This is the operator-facing guide for feature 009. Prerequisites: `docker` (with `buildx`), `kubectl`, and a kubeconfig with a context for your cluster already configured.

## 0. Repos involved

- **`honkai-rule-server`** (this repo) — image build, Makefile sync targets, helper pod manifest.
- **`<your-iac-repo>`** — chart at `charts/honkai-rule-server/`, Argo CD Application at `applications/honkai-rule-server.yaml`.

The two repos coordinate by SHA: this repo produces an image; the IaC repo's `values.yaml` pins `image.tag` to that SHA.

## 1. Build & push the image

From this repo's root:

```sh
make docker-push
```

This builds a Linux/amd64 image, tags it with the current git short SHA (e.g., `abc1234`), and pushes to `registry.example.com/library/honkai-rule-server:abc1234`. Pre-existing `docker login registry.example.com` credentials are required.

For a coordinated `:latest` advance (rare):

```sh
make docker-push-latest
```

After the push, note the SHA — you'll use it in step 2.

## 2. First-time deploy via Argo CD

In the `<your-iac-repo>` repo:

1. Create a chart at `charts/honkai-rule-server/` (the implementation phase will land this — see `specs/009-cluster-deploy/data-model.md` for the canonical values shape).
2. Set `values.yaml` `image.tag: abc1234` (the SHA from step 1).
3. Set `values.yaml` `ingress.pathPrefix: <random-32-char-hex>` (run `openssl rand -hex 16` once, commit the result).
4. Add `applications/honkai-rule-server.yaml` (Argo CD Application pointing at `charts/honkai-rule-server/`).
5. Commit and push to `master`. Argo CD's automated sync picks it up within ~30 s.

Verify Argo CD synced:

```sh
kubectl get application honkai-rule-server -n argocd -o jsonpath='{.status.sync.status}{"\n"}{.status.health.status}{"\n"}'
# Expected: Synced \n Healthy
```

## 3. Seed config (subscriptions, own-proxies, tokens)

The chart's initial deploy creates a ConfigMap with default placeholder content. Replace it with your operator-managed values:

```sh
make config-sync KUBE_CONTEXT=<your-context> NAMESPACE=cms
```

This atomically replaces the ConfigMap. The pod observes the change via fsnotify within ~30 s.

## 4. Seed custom rules

```sh
make rules-sync KUBE_CONTEXT=<your-context> NAMESPACE=cms
```

This launches a short-lived helper pod that mounts the same PVC, copies your local `config/custom-rules/` directory into `/data/custom-rules/`, and deletes itself. The application pod observes the change via fsnotify within ~250 ms.

## 5. Verify the deployment

```sh
PREFIX=$(kubectl get configmap honkai-rule-server-info -n cms -o jsonpath='{.data.pathPrefix}' 2>/dev/null \
       || echo "<read from chart values>")
TOKEN=$(kubectl get configmap honkai-rule-server-config -n cms -o jsonpath='{.data.tokens\.json}' \
       | jq -r '.[0].token')

curl -fsS "https://example.com/${PREFIX}/${TOKEN}/config" | head -20
# Expect: a YAML document beginning with `port: ...` / `proxies: ...` etc.
```

If the response is HTTP 401/403, your token is wrong (or `tokens.json` content was not synced).

If the response is HTTP 404, your `pathPrefix` does not match what the chart deployed.

If the response is HTTP 502/503, the pod is starting or unhealthy — check `kubectl logs deploy/honkai-rule-server -n cms`.

## 6. Day-2 iteration

Edit any of:

- `config/subscriptions.csv` → re-run `make config-sync KUBE_CONTEXT=<your-context> NAMESPACE=cms`
- `config/own-proxies.yaml` → same
- `config/tokens.json` → same
- `config/custom-rules/<priority>-<name>.yaml` (add or modify) → run `make rules-sync KUBE_CONTEXT=<your-context> NAMESPACE=cms`

The pod hot-reloads within 60 s. Verify with another `curl` against the served URL.

## 7. Image rollout for code changes

For changes to the Go source (or Dockerfile, or the served-config template):

1. Merge the change to this repo's `master`.
2. From a clean checkout of `master`: `make docker-push` — note the new SHA.
3. In the IaC repo: edit `charts/honkai-rule-server/values.yaml`'s `image.tag` to the new SHA; commit; push.
4. Argo CD detects drift and rolls the new pod (Recreate strategy: previous pod terminates, new pod starts; ≤30 s downtime).

## 8. Rollback

If a new image misbehaves:

1. In the IaC repo: edit `charts/honkai-rule-server/values.yaml`'s `image.tag` to a known-good prior SHA; commit; push.
2. Argo CD rolls back automatically.

## 9. Troubleshooting

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| `kubectl get application honkai-rule-server` shows `OutOfSync` | image SHA mismatch or new commit hasn't synced yet | wait 30 s; if persistent, check `kubectl describe application` for sync error |
| Pod stuck in `CrashLoopBackOff` with "required env var ... unset" | chart values changed without redeploy | check `kubectl describe pod`; chart should set every required env var |
| Pod stuck in `ImagePullBackOff` | image SHA pinned doesn't exist in registry | verify SHA in `values.yaml`; rebuild with `make docker-push` |
| Pod is `Running` but readiness fails | bootstrap window or upstream-fetch failure | check `kubectl logs`; first-time fetches can take 15+ s |
| Pod is healthy but `curl /<prefix>/<token>/config` returns 404 | path prefix mismatch | confirm `values.yaml`'s `ingress.pathPrefix` matches the URL |
| Pod is healthy but TLS fails | cert-manager hasn't issued | `kubectl describe certificate -n cms`; check letsencrypt rate-limit status |
| `make rules-sync` hangs | helper pod not scheduled | `kubectl get pod -n cms` to find it; check nodeSelector vs node taints |
| Custom rule added but not appearing in served body | fsnotify lag, ConfigMap mount lag, or rule file has a parse error | wait 60 s; check pod logs for "rule load" errors; if persistent, run `make rules-sync` again to ensure the file is in the PVC |

## 10. Tear down

```sh
# In the IaC repo:
git rm applications/honkai-rule-server.yaml
git rm -r charts/honkai-rule-server/
git commit -m "Remove honkai-rule-server deployment"
git push
```

Argo CD removes the Deployment, Service, Ingress, ConfigMap, PVC. The TLS Secret persists (cert-manager owns it) — clean up manually if the namespace is being deleted entirely.
