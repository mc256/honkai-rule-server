# Phase 1 Data Model: Deploy honkai-rule-server to the cluster

## Overview

Deployment surface introduces no new persistent entities at the application level. It does introduce four new infrastructure objects (Helm-managed): a Deployment, a Service, an Ingress, a ConfigMap, and a PVC. Below is the canonical shape of each, the values.yaml schema that parameterizes them, and the env-var matrix that the runtime container consumes.

## Entities

### Helm chart values (`charts/honkai-rule-server/values.yaml`)

```yaml
image:
  repository: registry.example.com/library/honkai-rule-server
  tag: ""                            # SET PER DEPLOY: git short SHA
  pullPolicy: IfNotPresent

imagePullSecrets:
  - name: registry-local              # matches existing chart pattern

nameOverride: ""
fullnameOverride: ""

service:
  type: ClusterIP
  port: 80                            # external; targets containerPort 8080

ingress:
  enabled: true
  className: contour
  annotations:
    cert-manager.io/cluster-issuer: "letsencrypt-prod"
    acme.cert-manager.io/http01-ingress-class: "contour"
  pathPrefix: ""                      # SET ONCE AT CHART AUTHORING: openssl rand -hex 16
  hosts:
    - example.com
    - www.example.com
  tls:
    - secretName: honkai-rule-server-tls
      hosts:
        - example.com
        - www.example.com

persistence:
  enabled: true
  storageClass: local-path
  accessMode: ReadWriteOnce
  size: 1Gi

env:
  port: 8080
  logLevel: info
  proxiesGroupName: Proxies
  fallbackRuleTarget: auto
  customRulesPath: /data/custom-rules
  cacheDir: /data/cache
  servedConfigTemplatePath: /etc/honkai/served-config.template.yaml
  honkaiRuleClientUA: ""              # empty = no UA filtering (003 FR-016)

probes:
  initialDelaySeconds: 15
  periodSeconds: 10
  timeoutSeconds: 3
  failureThreshold: 3

resources: {}
nodeSelector: {}
tolerations: []
affinity: {}

podAnnotations: {}
podSecurityContext: {}
securityContext: {}
```

### ConfigMap (`{{ include "honkai-rule-server.fullname" . }}-config`)

| Key | Source | Notes |
|-----|--------|-------|
| `subscriptions.csv` | operator-supplied (initial via chart's seed values OR via `make config-sync`) | CSV rules per 001 FR-001a |
| `own-proxies.yaml` | operator-supplied | Per 001 FR-006 |
| `tokens.json` | operator-supplied | Per 001 FR-019 |

Lifecycle: chart owns the *existence* of the ConfigMap (seeds it on initial deploy with files included from a chart-level `files/` directory or from `Values.configMap.contents`). Day-2 updates use `make config-sync`, which atomically replaces the ConfigMap content but leaves the chart-managed metadata intact. If the chart is uninstalled, the ConfigMap is deleted.

### PVC (`{{ include "honkai-rule-server.fullname" . }}-data`)

| Field | Value |
|-------|-------|
| `accessModes` | `[ReadWriteOnce]` |
| `storageClassName` | `local-path` |
| `resources.requests.storage` | `1Gi` |
| Mount path in container | `/data` |

Subdirectories (created on first use):
- `/data/cache/` — server-managed; receives upstream payload caches per 001 FR-017.
- `/data/custom-rules/` — operator-managed via `make rules-sync`; loaded by 003.

### Deployment (`{{ include "honkai-rule-server.fullname" . }}`)

```yaml
spec:
  replicas: 1
  strategy:
    type: Recreate                    # RWO PVC; no rolling update possible
  selector: {matchLabels: {...}}      # standard helm pattern
  template:
    spec:
      imagePullSecrets: [...]
      containers:
        - name: honkai-rule-server
          image: "{{ .Values.image.repository }}:{{ .Values.image.tag }}"
          imagePullPolicy: {{ .Values.image.pullPolicy }}
          ports:
            - name: http
              containerPort: 8080
              protocol: TCP
          env:
            - name: SUBSCRIPTIONS_CSV_PATH
              value: /etc/honkai/subscriptions.csv
            - name: OWN_PROXIES_YAML_PATH
              value: /etc/honkai/own-proxies.yaml
            - name: TOKENS_PATH
              value: /etc/honkai/tokens.json
            - name: SERVED_CONFIG_TEMPLATE_PATH
              value: {{ .Values.env.servedConfigTemplatePath | quote }}
            - name: CACHE_DIR
              value: {{ .Values.env.cacheDir | quote }}
            - name: CUSTOM_RULES_PATH
              value: {{ .Values.env.customRulesPath | quote }}
            - name: PORT
              value: {{ .Values.env.port | quote }}
            - name: LOG_LEVEL
              value: {{ .Values.env.logLevel | quote }}
            - name: PROXIES_GROUP_NAME
              value: {{ .Values.env.proxiesGroupName | quote }}
            - name: FALLBACK_RULE_TARGET
              value: {{ .Values.env.fallbackRuleTarget | quote }}
            {{- if .Values.env.honkaiRuleClientUA }}
            - name: HONKAI_RULE_CLIENT_UA
              value: {{ .Values.env.honkaiRuleClientUA | quote }}
            {{- end }}
          livenessProbe:
            httpGet:
              path: /health
              port: http
            initialDelaySeconds: {{ .Values.probes.initialDelaySeconds }}
            periodSeconds: {{ .Values.probes.periodSeconds }}
            timeoutSeconds: {{ .Values.probes.timeoutSeconds }}
            failureThreshold: {{ .Values.probes.failureThreshold }}
          readinessProbe:
            httpGet:
              path: /health
              port: http
            initialDelaySeconds: {{ .Values.probes.initialDelaySeconds }}
            periodSeconds: {{ .Values.probes.periodSeconds }}
            timeoutSeconds: {{ .Values.probes.timeoutSeconds }}
            failureThreshold: {{ .Values.probes.failureThreshold }}
          volumeMounts:
            - name: config
              mountPath: /etc/honkai/subscriptions.csv
              subPath: subscriptions.csv
              readOnly: true
            - name: config
              mountPath: /etc/honkai/own-proxies.yaml
              subPath: own-proxies.yaml
              readOnly: true
            - name: config
              mountPath: /etc/honkai/tokens.json
              subPath: tokens.json
              readOnly: true
            - name: data
              mountPath: /data
          resources: {{ ... }}
      volumes:
        - name: config
          configMap:
            name: {{ include "honkai-rule-server.fullname" . }}-config
        - name: data
          persistentVolumeClaim:
            claimName: {{ include "honkai-rule-server.fullname" . }}-data
```

### Ingress (`{{ include "honkai-rule-server.fullname" . }}`)

```yaml
spec:
  ingressClassName: contour
  tls:
    - hosts: [example.com, www.example.com]
      secretName: honkai-rule-server-tls
  rules:
    - host: example.com
      http:
        paths:
          - path: {{ printf "/%s" .Values.ingress.pathPrefix }}
            pathType: Prefix
            backend:
              service:
                name: {{ include "honkai-rule-server.fullname" . }}
                port:
                  number: 80
    - host: www.example.com
      http:
        paths: [...same as above...]
```

### Argo CD Application (`applications/honkai-rule-server.yaml`)

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: honkai-rule-server
  namespace: argocd
spec:
  destination:
    namespace: cms
    server: https://kubernetes.default.svc
  project: default
  source:
    path: charts/honkai-rule-server/
    repoURL: <your-iac-repo-url>
    targetRevision: master
  syncPolicy:
    automated: {}
```

Matches the existing `honkai-rule-server` Application shape verbatim — only the chart path, application name, and namespace differ.

## Relationships

```text
operator's local working tree (this repo)
    │
    ├─ config/subscriptions.csv ───────┐
    ├─ config/own-proxies.yaml ────────┼──> `make config-sync` ──> ConfigMap (atomic apply)
    ├─ config/tokens.json ─────────────┘
    │
    ├─ config/custom-rules/*.yaml ─────────> `make rules-sync` ──> helper pod ──> PVC `/data/custom-rules/`
    │
    └─ Dockerfile + binary + template ─────> `make docker-push` ──> registry.example.com/library/honkai-rule-server:<sha>

operator's IaC repo (<your-iac-repo>)
    │
    ├─ charts/honkai-rule-server/ ─────────> Argo CD Application
    └─ applications/honkai-rule-server.yaml ──> sync to the target cluster

cluster
    │
    ├─ ConfigMap (subscriptions/own-proxies/tokens)
    ├─ PVC (cache + custom-rules)
    ├─ Deployment (1 pod, mounts both)
    ├─ Service (ClusterIP)
    └─ Ingress (contour, /<prefix>/* → Service:80)
```

## Validation Rules

### Pod startup

The container reads env vars per the table in research §R6. Each of `SUBSCRIPTIONS_CSV_PATH`, `OWN_PROXIES_YAML_PATH`, `TOKENS_PATH`, `SERVED_CONFIG_TEMPLATE_PATH`, `CACHE_DIR` is required (per 001 `Load()`); a missing value causes the pod to exit non-zero on startup, which fails the readiness probe and surfaces the misconfiguration in Argo CD.

### Probe responses

`/health` on port 8080 returns a JSON object with HTTP 200 when the server is healthy. The probe accepts any 2xx response. If `/health` returns 503 (e.g., during cold-start fetches), readiness fails — the Service drops the pod from its endpoint list — until the bootstrap completes.

### Sync target prerequisites (FR-018)

`make config-sync` requires:
- Valid `KUBE_CONTEXT` and `NAMESPACE` make variables (refuse otherwise)
- `kubectl` and `kubeconfig` configured for the named context
- The target ConfigMap exists in-cluster (chart deploys it; first-deploy seed comes from chart values)
- All three local files (`config/subscriptions.csv`, `config/own-proxies.yaml`, `config/tokens.json`) present in the working tree

`make rules-sync` requires:
- Valid `KUBE_CONTEXT` and `NAMESPACE` make variables
- `kubectl` configured
- The target PVC exists in-cluster
- The helper pod manifest (`deploy/rules-sync-pod.yaml`) is parseable

## Counts

For a typical deployment:

| Object | Count | Notes |
|--------|------:|-------|
| Argo CD Applications | +1 | new |
| Helm charts | +1 | new |
| Deployments | +1 | new |
| Services | +1 | new |
| Ingresses | +1 | new |
| ConfigMaps | +1 | new |
| PVCs | +1 | new |
| Pods running steady-state | +1 | the application replica |
| Pods running during sync | +0 or +1 | helper pod is short-lived; 0 in steady state, 1 during sync |
