# Security Guide

This page covers the security-relevant knobs the controller actually exposes. Anything that isn't HAPTIC-specific (how to issue certs with cert-manager, how to wire External Secrets Operator, etc.) is left to the upstream project's docs.

## What the Controller Needs

### RBAC

The Helm chart provisions a `ServiceAccount` and `ClusterRole` (names derive from the Helm release fullname). The ClusterRole grants:

| Resource | Verbs | Why |
|----------|-------|-----|
| `pods`, `namespaces` | get, list, watch | Discover HAProxy pods, target namespaces |
| `ingresses` (networking.k8s.io) | get, list, watch | Default watched resource |
| `services`, `endpoints`, `endpointslices` | get, list, watch | Resolve backends |
| `secrets` | get, list, watch | Load TLS certificates referenced from templates and the credentials Secret |
| `leases` (coordination.k8s.io) | get, create, update | Leader election |
| `haproxytemplateconfigs.haproxy-haptic.org` | get, list, watch | Primary config CRD |
| `haproxycfgs.haproxy-haptic.org` | get, list, watch, create, update, patch | Publish rendered config for observability |

Anything else referenced from `watchedResources` needs matching RBAC; if you manage RBAC yourself (`rbac.create: false`), keep it in sync.

Narrow the cluster-wide watch to specific namespaces by pinning `namespace:` or `namespaceSelector:` on each watched-resource entry — see [Watching Resources](../watching-resources.md).

### Credentials

The CRD references a `Secret` via `spec.credentialsSecretRef`. It must contain four keys:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: haproxy-credentials
type: Opaque
stringData:
  dataplane_username: admin
  dataplane_password: <random>
  validation_username: validator   # validation endpoint (if used)
  validation_password: <random>
```

The controller watches the Secret and picks up rotations live — no pod restart needed. Use whatever secret-management tool you already run (ESO, Vault agent, SOPS, …); the controller just reads the Secret.

Debug endpoints expose credential *metadata* only (version, `has_dataplane_creds: true`), never passwords — `pkg/controller/debug/vars.go` enforces that. See [Debugging](./debugging.md#accessing-the-server) for access control if you run with the debug port enabled.

## Pod Hardening

The chart ships with a restrictive default pod spec. The relevant `controller.securityContext` / `controller.podSecurityContext` defaults:

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
| `8080` | `/healthz`, `/debug/vars`, `/debug/pprof/` | Set `controller.debugPort: 0` in production to drop `/debug/*` — `/healthz` is served on the same port, so disabling it also requires moving healthz via `controller.ports.healthz` |
| `9090` | `/metrics` | Disable by setting `controller.config.controller.metricsPort: 0` |
| `9443` | Validating webhook | Required when the webhook is enabled |

Outbound, the controller talks to the Kubernetes API server and to each HAProxy pod's Dataplane API (default port `5555`). Dataplane API traffic is plain HTTP over the pod network — the controller has no TLS client configuration for the Dataplane API. Rely on pod-network protection (NetworkPolicy, service mesh, CNI encryption) rather than transport-level authentication for that hop.

Example egress-restriction NetworkPolicy:

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

```scriggo
{%- var secret = resources.secrets.GetSingle("auth", "basic-auth") %}
{%- if secret != nil %}
userlist authenticated_users
    user admin password {{ secret.data.password_hash | b64decode }}
{%- end %}
```

Store *hashes*, not plaintext. For HAProxy basic auth:

```bash
htpasswd -nbB admin mypassword | cut -d: -f2
kubectl create secret generic basic-auth \
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
    users: ["system:serviceaccount:<namespace>:<release>"]
    resources:
      - group: ""
        resources: ["secrets"]
```

Replace `<namespace>`/`<release>` with your Helm release; the SA name is `<release>` unless you overrode `serviceAccount.name`.

## Checklist

Before exposing a HAPTIC deployment to production traffic:

- [ ] Random, rotated passwords in `credentialsSecretRef`.
- [ ] `controller.debugPort: 0` — or a NetworkPolicy that pins ingress to trusted namespaces.
- [ ] Watched-resource selectors scoped to the namespaces you intend to serve.
- [ ] Release namespace labelled with `pod-security.kubernetes.io/enforce=restricted`.
- [ ] NetworkPolicy allowing only kube-apiserver + Dataplane-API egress.
- [ ] Audit policy in place for `HAProxyTemplateConfig` changes.
- [ ] Image signature verification (`cosign verify …`) wired into your admission policy — see [Releasing](../development/releasing.md#supply-chain-security).

## See Also

- [Monitoring](./monitoring.md) — signals for auth failures, webhook drops, leader flaps
- [Debugging](./debugging.md) — accessing `/debug/*` safely
- [High Availability](./high-availability.md) — leader election RBAC and lease ownership
