# Security

This page covers only the security settings HAPTIC itself owns. Anything that isn't HAPTIC-specific (how to issue certs with cert-manager, how to wire External Secrets Operator (ESO), etc.) is left to the upstream project's docs.

## What the controller needs

### RBAC

The Helm chart provisions a `ServiceAccount`, a `ClusterRole`, and a namespace-scoped `Role` (names derive from the Helm release `fullname`). The ClusterRole grants:

| Resource | Verbs | Why |
|----------|-------|-----|
| `pods`, `namespaces` | get, list, watch | Discover HAProxy pods, target namespaces |
| `<each watched resource>` | get, list, watch | Generated per `watchedResources` entry — Ingress, Service, EndpointSlice, Secret, etc. depending on the enabled libraries |
| `<watched resource>/status` | patch | Generated for watched resources with `statusPatch: true` (for example, Ingress LoadBalancer status, Gateway / HTTPRoute conditions) |
| `leases` (`coordination.k8s.io`) | get, create, update | Leader election |
| `customresourcedefinitions` (`apiextensions.k8s.io`) | get, list, watch | Fetch watched-resource OpenAPI schemas from their CRDs so typed template access stays full-fidelity (degrades to the public OpenAPI endpoint otherwise) |
| `haproxytemplateconfigs.haproxy-haptic.org` | get, list, watch | Primary config CRD |
| `haproxytemplateconfigs/status` | update, patch | Report validation status back onto the CRD |
| `haproxycfgs`, `haproxygeneralfiles`, `haproxycrtlistfiles`, `haproxymapfiles` (.haproxy-haptic.org) | get, list, watch, create, update, patch, delete | Publish rendered config + auxiliary files as observable CRDs (full read-write access because the controller owns these resources and prunes stale entries) |
| `<above CRDs>/status` | update, patch | Report deployment status on the published artifacts |
| `services` | get, list, watch, create, update, patch, delete | **Gateway library only** — cluster-wide Service writes for Gateway-API templates that emit owned Services into a Gateway's own namespace (for example, the per-Gateway infrastructure-propagation marker Service) |
| `gatewayclasses` (`gateway.networking.k8s.io`) | create, update, patch, delete | **Gateway library only** — the GatewayClass is applied at runtime via Server-Side Apply, not by Helm (read verbs come from the watched-resource rules) |
| `events` (core) | create, update, patch, delete | **Ingress library only** — Warning Events on Ingresses whose backend Service is missing |

Anything else referenced from `watchedResources` needs matching RBAC. The Helm chart auto-generates the watched-resource rules from `controller.config.watchedResources` and the enabled libraries; if you manage RBAC yourself (`rbac.create: false`), keep it in sync. The full template is `charts/haptic/templates/clusterrole.yaml`.

Narrow the cluster-wide watch to a single namespace with `fieldSelector: "metadata.namespace=<ns>"` on each watched-resource entry — see [Watching Resources](../watching-resources.md#narrowing-the-watch). For label-based namespace filtering, see [Performance — Resource Watching Optimization](./performance.md#resource-watching-optimization).

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

The controller watches the Secret and picks up rotations live — no pod restart needed. Use whatever secret-management tool you already run (ESO, Vault agent, `SOPS`, …); the controller just reads the Secret.

!!! warning "Set the Dataplane password explicitly under GitOps"
    If you install via the Helm chart and leave `credentials.dataplane.password` empty, the chart generates a **random** 32-char password and preserves it across upgrades by reading the existing Secret via `lookup`. GitOps tools that render without cluster access (ArgoCD/Flux) can't `lookup`, so an empty value regenerates on every sync and churns the credential — set `credentials.dataplane.password` explicitly (SealedSecret / external secret) for those deployments.

Debug endpoints expose credential *metadata* only (version, `has_dataplane_creds: true`), never passwords — `pkg/controller/debug/setup.go` enforces that. See [Debugging](./debugging.md#accessing-the-server) for access control if you run with the debug port enabled.

## Pod hardening

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

## Network exposure

The controller pod exposes three HTTP ports (all chart defaults):

| Port | Endpoint | Notes |
|------|----------|-------|
| `8080` | `/healthz`, `/debug/vars`, `/debug/events`, `/debug/pprof/` | `/healthz` and `/debug/*` share the same listener; setting `controller.debugPort: 0` disables both and breaks the liveness/readiness probes. To shield `/debug/*` in production, restrict access with a NetworkPolicy (example below) instead of disabling the port |
| `9090` | `/metrics` | Disable by setting the `METRICS_PORT=0` env var on the controller container (for example, `extraEnv` in Helm); the `controller.config.controller.metricsPort` Helm value is display-only — the chart strips it before serializing |
| `9443` | Validating webhook | Required when the webhook is enabled |

Outbound, the controller talks to the Kubernetes API server and to each HAProxy pod's Dataplane API (default port `5555`). Dataplane API traffic is plain HTTP over the pod network — the controller has no TLS client configuration for the Dataplane API. Rely on pod-network protection (NetworkPolicy, service mesh, Container Network Interface (CNI) encryption) rather than transport-level authentication for that hop.

The Dataplane API is authenticated with a basic-auth password stored in the `<release>-haptic-credentials` Secret (the release `fullname`, which collapses to `<release>-credentials` only when the release name already contains `haptic`). Password generation and the GitOps caveat are covered in the warning box above.

**The chart already ships default-on `NetworkPolicy` resources** for both the controller (`networkPolicy.enabled`) and HAProxy (`haproxy.networkPolicy.enabled`) pods — both default `true`. Know what the defaults actually allow before relying on them:

- The controller policy restricts ingress to the exposed ports (metrics ingress only opens when `networkPolicy.ingress.monitoring.enabled: true` — it's off by default, so enable it for Prometheus). Egress covers DNS, the Kubernetes API server, and the HAProxy Dataplane/stats ports, **plus a default `networkPolicy.egress.additionalRules` entry allowing every in-cluster pod** (so template helpers like `http.Fetch()` work) — set it to `[]` to lock egress down (see [Networking](./networking.md#production-hardening)).
- The HAProxy policy defaults to `allowExternal: true`, which renders a permissive all-port ingress rule — deliberate, because Gateway listeners bind dynamic ports.

To tighten, replace, or debug these policies — including a copy-pastable replacement policy and its selector caveat — see [Networking](./networking.md#replacing-the-shipped-policies). If you keep the debug port enabled, pair it with a NetworkPolicy that restricts ingress to your observability namespace.

## Config injection

Can an annotation smuggle extra HAProxy directives into the rendered config? For most annotations, no. Two categories behave differently.

### Snippet annotations inject by design

These annotations exist to pass HAProxy directives straight into a config section, verbatim:

| Annotation | Library | Default |
|------------|---------|---------|
| `haproxy.org/backend-config-snippet` | `haproxytech` | On |
| `haproxy-ingress.github.io/config-frontend`, `config-global`, `config-defaults` | `haproxyIngress` | On |
| `nginx.ingress.kubernetes.io/configuration-snippet` | `nginxIngress` | Off |

Whoever can set annotations on an Ingress can inject any directive through an enabled snippet library. The only gate is Kubernetes RBAC on the Ingress resource — restrict who can create or edit Ingresses if that reach is too broad, or disable the snippet libraries you don't use.

### Every other annotation value is validated

Annotation values that HAPTIC interpolates onto a config line pass through injection guards before they render:

- **CIDR-list annotations** (source allow and deny lists, rate-limit whitelists) accept only comma-separated IPv4/IPv6 addresses and CIDR ranges. Any other character — whitespace, `;`, `{`, a newline, a letter — fails the whole render.
- **Single-value annotations** (header values, hostnames, ciphers, cookie domain and path, rewrite targets) reject control characters, so a newline can't split the line and append a second directive. They also reject spaces where the field is a single token.

A value that trips a guard fails the render with a diagnostic instead of deploying, and the rendered config is checked by `haproxy -c` before it reaches HAProxy. The validating webhook and the daemon fail differently:

- **Watched resources** (Ingress, Gateway, HTTPRoute) use `failurePolicy: Fail` — fail-closed. A resource whose render trips a guard is rejected at apply, and if the webhook is unreachable the apply is rejected too.
- **The `HAProxyTemplateConfig` CRD** webhook uses `failurePolicy: Ignore` — fail-open by design, so a degraded controller never blocks you from applying a config fix. When it's bypassed, the daemon's load gate still runs `haproxy -c` server-side before deploying and reports the failure on `HAProxyCfg.status`.

## Secrets in templates

Templates read watched Secrets like any other resource. Decode with `b64decode` (values in `.data` are base64-encoded by Kubernetes):

!!! note "Templates read every watched Secret"
    A template runs with the controller's read privileges, so it can render **any** Secret in the watched scope into the config — or into logs. There's no per-template Secret allowlist, so two levers bound the exposure:

    - **Restrict who can write `HAProxyTemplateConfig`.** Whoever edits the templates chooses which Secrets get rendered — keep [RBAC](#rbac) on the CRD tight.
    - **Narrow the Secret watch** with a `fieldSelector`, so the controller never caches Secrets outside the namespaces you serve — see [Watching Resources](../watching-resources.md#narrowing-the-watch).

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

HAPTIC doesn't check hash strength or format by default: `password_hash_validation_regex` defaults to `^.*$`, which accepts any string. Set it to the allowed format to reject weak or unsupported hashes at render time — see [`password_hash_validation_regex`](../reference.md#logging-and-templating). HAProxy's own `haproxy -c` parse rejects some malformed hashes, but formats like `$apr1$` or `{SHA}` pass the parse and then fail every login, so the regex is what stops those before they deploy.

### Secret reference namespaces

Where a Secret reference may point depends on the mechanism:

| Reference | Reachable namespaces |
|-----------|----------------------|
| Ingress `spec.tls.secretName` | The Ingress's own namespace only |
| Ingress `auth-secret` / `auth-tls-secret` annotations | Any watched namespace — the value accepts `namespace/name` with no cross-namespace gate |
| Gateway API `certificateRefs` / `backendRefs` | Cross-namespace only with a matching `ReferenceGrant` in the target namespace (see [Gateway API](../libraries/gateway.md#cross-namespace-routes-referencegrant)) |

Unlike Gateway API references, the Ingress `auth-secret` annotations have no `ReferenceGrant` equivalent: an Ingress in one namespace can name a Secret in any watched namespace. Narrow the Secret watch with a `fieldSelector` to bound which namespaces those annotations can reach.

## Audit trail

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

Replace `<namespace>`/`<release>` with your Helm release. The SA name is the release `fullname` `<release>-haptic` (collapsing to `<release>` only when the release name already contains `haptic`) unless you overrode `serviceAccount.name` — get the exact value with `kubectl -n <namespace> get sa`. A rule keyed on the wrong SA name silently never matches, so the controller's Secret reads go unaudited.

## Checklist

Before exposing a HAPTIC deployment to production traffic:

- [ ] Random, rotated passwords in `credentialsSecretRef`.
- [ ] NetworkPolicy that pins `/debug/*` ingress to trusted namespaces (the port also serves `/healthz`, so don't set `controller.debugPort: 0`).
- [ ] Watched-resource selectors scoped to the namespaces you intend to serve.
- [ ] RBAC restricting who can write `HAProxyTemplateConfig` (and, with snippet annotations enabled, who can create Ingresses).
- [ ] Release namespace labelled with `pod-security.kubernetes.io/enforce=restricted`.
- [ ] NetworkPolicy allowing only kube-apiserver + Dataplane-API egress.
- [ ] Audit policy in place for `HAProxyTemplateConfig` changes.
- [ ] Image signature verification (`cosign verify …`) wired into your admission policy — see [Releasing](../development/releasing.md#supply-chain-security).

## See also

- [Networking](./networking.md) — NetworkPolicy mechanics: default rules, hardening, replacement policies
- [Monitoring](./monitoring.md) — signals for auth failures, webhook drops, leader flaps
- [Debugging](./debugging.md) — accessing `/debug/*` safely
- [High Availability](./high-availability.md) — leader election RBAC and lease ownership
