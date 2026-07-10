# Security

HAPTIC exposes a focused set of security-relevant knobs. Anything that isn't HAPTIC-specific (how to issue certs with cert-manager, how to wire External Secrets Operator (ESO), etc.) is left to the upstream project's docs.

## What the Controller Needs

### RBAC

The Helm chart provisions a `ServiceAccount`, a `ClusterRole`, and a namespace-scoped `Role` (names derive from the Helm release fullname). The ClusterRole grants:

| Resource | Verbs | Why |
|----------|-------|-----|
| `pods`, `namespaces` | get, list, watch | Discover HAProxy pods, target namespaces |
| `<each watched resource>` | get, list, watch | Generated per `watchedResources` entry — Ingress, Service, EndpointSlice, Secret, etc. depending on the enabled libraries |
| `<watched resource>/status` | patch | Generated for watched resources with `statusPatch: true` (e.g. Ingress LoadBalancer status, Gateway / HTTPRoute conditions) |
| `leases` (coordination.k8s.io) | get, create, update | Leader election |
| `customresourcedefinitions` (apiextensions.k8s.io) | get, list, watch | Fetch watched-resource OpenAPI schemas from their CRDs so typed template access stays full-fidelity (degrades to the public OpenAPI endpoint otherwise) |
| `haproxytemplateconfigs.haproxy-haptic.org` | get, list, watch | Primary config CRD |
| `haproxytemplateconfigs/status` | update, patch | Report validation status back onto the CRD |
| `haproxycfgs`, `haproxygeneralfiles`, `haproxycrtlistfiles`, `haproxymapfiles` (.haproxy-haptic.org) | get, list, watch, create, update, patch, delete | Publish rendered config + auxiliary files as observable CRDs (full CRUD because the controller owns these resources and prunes stale entries) |
| `<above CRDs>/status` | update, patch | Report deployment status on the published artifacts |
| `services` | get, list, watch, create, update, patch, delete | **Gateway library only** — cluster-wide Service writes for Gateway-API templates that emit owned Services into a Gateway's own namespace (e.g. the per-Gateway infrastructure-propagation marker Service) |

Anything else referenced from `watchedResources` needs matching RBAC. The Helm chart auto-generates the watched-resource rules from `controller.config.watchedResources` and the enabled libraries; if you manage RBAC yourself (`rbac.create: false`), keep it in sync. The full template is `charts/haptic/templates/clusterrole.yaml`.

Narrow the cluster-wide watch to a single namespace by pinning `namespace:` on each watched-resource entry — see [Watching Resources](../watching-resources.md). For multi-namespace filtering by labels, fall back to per-namespace `Role`/`RoleBinding` instead of a `ClusterRole`, or filter inside the template against a watched `namespaces` resource.

A namespace-scoped `Role` (bound only in the controller's own namespace) additionally grants the writes the controller performs locally — kept off the `ClusterRole` to tighten the blast radius:

| Resource | Verbs | Why |
|----------|-------|-----|
| `secrets` | get, list, watch, create, update, patch, delete | Read Dataplane API credentials; read/write SSL certificate Secrets |
| `haproxycfgs`, `haproxymapfiles` | get, list, watch, create, update, patch, delete | Publish rendered config + map files as observable CRDs in the controller's own namespace |
| `haproxycfgs/status`, `haproxymapfiles/status` | get, update, patch | Status on the published artifacts |
| `services` | get, list, watch, create, update, patch, delete | Namespace-scoped counterpart to the gateway Service grant above — Gateway StaticAddresses LoadBalancer Services emitted into the controller's own namespace |

The full template is `charts/haptic/templates/role.yaml`.

### Credentials

The CRD references a `Secret` via `spec.credentialsSecretRef`. It must contain two keys:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: haproxy-credentials
type: Opaque
stringData:
  dataplane_username: admin
  dataplane_password: <random>
```

The controller watches the Secret and picks up rotations live — no pod restart needed. Use whatever secret-management tool you already run (ESO, Vault agent, SOPS, …); the controller just reads the Secret.

!!! warning "Set the Dataplane password explicitly under GitOps"
    If you install via the Helm chart and leave `credentials.dataplane.password` empty, the chart generates a **random** 32-char password and preserves it across upgrades by reading the existing Secret via `lookup`. GitOps tools that render without cluster access (ArgoCD/Flux) can't `lookup`, so an empty value regenerates on every sync and churns the credential — set `credentials.dataplane.password` explicitly (SealedSecret / external secret) for those deployments.

Debug endpoints expose credential *metadata* only (version, `has_dataplane_creds: true`), never passwords — `pkg/controller/debug/vars.go` enforces that. See [Debugging](./debugging.md#accessing-the-server) for access control if you run with the debug port enabled.

## Pod Hardening

The chart ships with a restrictive default pod spec. The relevant `securityContext` (container-level) / `controller.podSpec.podSecurityContext` (pod-level) defaults:

| Setting | Default |
|---------|---------|
| `runAsNonRoot` | `true` |
| `runAsUser` / `runAsGroup` / `fsGroup` | `65532` (`nonroot`) |
| `readOnlyRootFilesystem` | `true` |
| `allowPrivilegeEscalation` | `false` |
| `capabilities.drop` | `[ALL]` |
| `seccompProfile.type` | `RuntimeDefault` |

The controller writes temporary files (for `haproxy -c` validation) to `/tmp`, which is mounted as an `emptyDir`. Everything else is read-only.

The chart is compatible with the "restricted" Pod Security Standard out of the box:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: haptic
  labels:
    pod-security.kubernetes.io/enforce: restricted
    pod-security.kubernetes.io/warn: restricted
```

## Network Exposure

The controller pod exposes three HTTP ports (all chart defaults):

| Port | Endpoint | Notes |
|------|----------|-------|
| `8080` | `/healthz`, `/debug/vars`, `/debug/pprof/` | `/healthz` and `/debug/*` share the same listener; setting `controller.debugPort: 0` disables both and breaks the liveness/readiness probes. To shield `/debug/*` in production, restrict access with a NetworkPolicy (example below) instead of disabling the port |
| `9090` | `/metrics` | Disable by setting the `METRICS_PORT=0` env var on the controller container (e.g. `extraEnv` in Helm); the `metricsPort` field on the CRD is *not* read by the controller — the chart strips it before serializing |
| `9443` | Validating webhook | Required when the webhook is enabled |

Outbound, the controller talks to the Kubernetes API server and to each HAProxy pod's Dataplane API (default port `5555`). Dataplane API traffic is plain HTTP over the pod network — the controller has no TLS client configuration for the Dataplane API. Rely on pod-network protection (NetworkPolicy, service mesh, CNI encryption) rather than transport-level authentication for that hop.

The Dataplane API is authenticated with a basic-auth password stored in the `<release>-haptic-credentials` Secret (the release fullname, which collapses to `<release>-credentials` only when the release name already contains `haptic`). When `credentials.dataplane.password` is left empty the chart generates a **random** 32-char password and preserves it across upgrades via `lookup`. Under GitOps tools that render without cluster access (ArgoCD/Flux), `lookup` is unavailable, so an empty password regenerates on every sync — set `credentials.dataplane.password` explicitly (e.g. via a SealedSecret / external secret) for those setups, which is the recommended pattern for any generated credential under GitOps.

**The chart already ships default-on `NetworkPolicy` resources** for both the controller (`networkPolicy.enabled`) and HAProxy (`haproxy.networkPolicy.enabled`) pods — both default `true` — restricting ingress to the exposed ports and egress to DNS, the Kubernetes API server, and the HAProxy Dataplane/stats ports. Set the relevant flag to `false` to manage your own; the example below mirrors the controller policy's shape:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: haptic-controller
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: haptic
      app.kubernetes.io/component: controller
  policyTypes: [Ingress, Egress]
  ingress:
    - ports:
        - port: 8080   # /healthz, /debug/*
        - port: 9090   # /metrics
        - port: 9443   # webhook
  egress:
    - to:
        - namespaceSelector: {}   # kube-apiserver is in every cluster, tighten if you know the selector
      ports:
        - port: 443
    - to:
        - podSelector:
            matchLabels:
              app.kubernetes.io/component: loadbalancer
      ports:
        - port: 5555   # Dataplane API
```

If you keep the debug port enabled, pair it with a NetworkPolicy that restricts ingress to your observability namespace.

## Secrets in Templates

Templates read watched Secrets like any other resource. Decode with `b64decode` (values in `.data` are base64-encoded by Kubernetes):

<div class="pg-embed" markdown data-tab="haproxy.cfg" data-controls="tabs,resources" data-focus="20" data-title="Watched Secret → userlist" data-height="480">

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: secret-userlist-demo
spec:
  watchedResources:
    secrets:
      apiVersion: v1
      resources: secrets
      indexBy:
        - metadata.namespace
        - metadata.name
  haproxyConfig:
    template: |
      global
        log stdout format raw local0
      defaults
        mode http
        timeout connect 5s
        timeout client 30s
        timeout server 30s
      {%- var secret = resources.secrets.GetSingle("auth", "basic-auth") %}
      {%- if secret != nil %}
      userlist authenticated_users
          user admin password {{ secret.data.password_hash | b64decode() }}
      {%- end %}
```

```yaml
apiVersion: v1
kind: List
items:
  - apiVersion: v1
    kind: Secret
    metadata:
      name: basic-auth
      namespace: auth
    data:
      password_hash: JDJ5JDA1JFp0MENrMXFZdzhwMXNRbTltUjNuUWVKOHlxM3ZKaEY1eEo4ZFEwb1YyYk43Y1gxa0w5bVNl
```

</div>

Store *hashes*, not plaintext. For HAProxy basic auth:

```bash
htpasswd -nbB admin mypassword | cut -d: -f2
kubectl create secret generic basic-auth -n auth \
  --from-literal=password_hash='$2y$05$...'
```

Bcrypt is slow to verify on every request; for large userbases use `htpasswd -n -5` (SHA-512 crypt) and see [Performance](./performance.md#password-hash-performance) for the trade-off.

## Audit Trail

A minimal audit policy that records who touched `HAProxyTemplateConfig` and which Secrets the controller reads:

```yaml
apiVersion: audit.k8s.io/v1
kind: Policy
rules:
  - level: RequestResponse
    resources:
      - group: haproxy-haptic.org
        resources: ["haproxytemplateconfigs"]
  - level: Metadata
    users: ["system:serviceaccount:<namespace>:<release>-haptic"]
    resources:
      - group: ""
        resources: ["secrets"]
```

Replace `<namespace>`/`<release>` with your Helm release. The SA name is the release fullname `<release>-haptic` (collapsing to `<release>` only when the release name already contains `haptic`) unless you overrode `serviceAccount.name` — get the exact value with `kubectl -n <namespace> get sa`. A rule keyed on the wrong SA name silently never matches, so the controller's Secret reads go unaudited.

## Checklist

Before exposing a HAPTIC deployment to production traffic:

- [ ] Random, rotated passwords in `credentialsSecretRef`.
- [ ] NetworkPolicy that pins `/debug/*` ingress to trusted namespaces (the port also serves `/healthz`, so don't set `controller.debugPort: 0`).
- [ ] Watched-resource selectors scoped to the namespaces you intend to serve.
- [ ] Release namespace labelled with `pod-security.kubernetes.io/enforce=restricted`.
- [ ] NetworkPolicy allowing only kube-apiserver + Dataplane-API egress.
- [ ] Audit policy in place for `HAProxyTemplateConfig` changes.
- [ ] Image signature verification (`cosign verify …`) wired into your admission policy — see [Releasing](../development/releasing.md#supply-chain-security).

## See Also

- [Monitoring](./monitoring.md) — signals for auth failures, webhook drops, leader flaps
- [Debugging](./debugging.md) — accessing `/debug/*` safely
- [High Availability](./high-availability.md) — leader election RBAC and lease ownership
