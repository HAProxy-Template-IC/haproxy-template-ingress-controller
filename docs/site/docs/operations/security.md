# Security

Configure permissions, credentials, pod security, and network access for your
HAPTIC installation. Review the defaults below before exposing production traffic.

## What the controller needs

### RBAC

The Helm chart creates a ServiceAccount, a ClusterRole, and a Role in the release
namespace. Role-based access control (RBAC) permissions depend on the enabled
libraries and watched resources.

The **ClusterRole** grants:

| Resource | Verbs | Purpose |
|----------|-------|---------|
| Each watched resource | get, list, watch | Read the resources declared by the configuration and enabled libraries |
| Watched resources with `statusPatch: true`, via `/status` | patch | Publish routing and policy conditions |
| `customresourcedefinitions` | get, list, watch | Load schemas and detect CRD changes |
| `events` | create, update, patch, delete | Publish template-generated Kubernetes Events |
| `services` | get, list, watch, create, update, patch, delete | Gateway library only: manage Services in Gateway namespaces |
| `gatewayclasses` | create, update, patch, delete | Gateway library only: manage the configured GatewayClass |

The **namespace Role** grants:

| Resource | Verbs | Purpose |
|----------|-------|---------|
| `pods` | get, list, watch | Discover the HAProxy fleet |
| `secrets` | get, list, watch, create, update, patch, delete | Read agent credentials and manage certificate Secrets |
| `leases` | get, create, update | Coordinate leadership |
| `haproxytemplateconfigs`, `haproxytemplatelibraries` | get, list, watch | Load the configuration and its libraries |
| `haproxytemplatelibraries` | patch | Set ownership references |
| `haproxytemplateconfigs/status` | update, patch | Report configuration validation status |
| `haproxycfgs`, `haproxymapfiles`, `haproxygeneralfiles`, `haproxycrtlistfiles` | get, list, watch, create, update, patch, delete | Publish and prune rendered artifacts |
| The four artifact kinds above, via `/status` | update, patch | Report deployment status |
| `services` | get, list, watch, create, update, patch, delete | Manage Services in the release namespace |
| `configmaps` | get, list, watch, create, update, patch, delete | Managed Varnish only: publish Varnish Configuration Language (VCL) |
| `statefulsets`, `deployments` | get, list, watch, create, update, patch, delete | Managed Varnish or Valkey only: manage workloads |
| `poddisruptionbudgets` | get, list, watch, create, update, patch, delete | Managed Varnish or Valkey with disruption budgets enabled |
| `horizontalpodautoscalers` | get, list, watch, create, update, patch, delete | Managed Varnish with autoscaling enabled |

Inspect your release's rendered grants with `helm get manifest haptic -n haptic`.
If you set `controller.rbac.create: false`, maintain these permissions yourself.
The chart sources are `templates/clusterrole.yaml` and `templates/role.yaml`.

[Watch selectors](../watching-resources.md#narrowing-the-watch) limit what templates
consume; they don't narrow the ServiceAccount's RBAC permissions.

### Credentials

The chart encrypts controller-to-agent requests with mutual TLS (mTLS). Each end
checks its peer's certificate authority and distinct DNS subject alternative name.
Agent control requests require the controller identity; a Basic-auth password
doesn't grant access in TLS mode. Both peers require TLS 1.3.

A bootstrap Job and hourly CronJob manage the default 365-day CA and identity
lifetimes, renewing 30 days before expiry. The renewal ServiceAccount can create
Secrets in the namespace and get or update only the three named certificate
Secrets; Kubernetes RBAC can't restrict creation by resource name. The issuer
Secret contains the private CA key and must be backed up securely. It isn't
mounted in application pods. See [Agent certificates](./agent-certificates.md)
for cert-manager selection, expiry monitoring, and failed-renewal recovery.

Set `haproxy.agent.tls.managed: false` to use externally managed Secrets.

The controller mounts only its client identity; the agent mounts only its server
identity. The CA private key isn't stored in either Secret. Mounted certificate
updates take effect without restarting the controller or agent. Old CA trust
requires an explicit expiry, at most 24 hours away. Removing that trust revokes
old clients on their next request, including on an existing connection.

The chart still creates the Secret named by `--secret-name` or `SECRET_NAME` for
controller bootstrap compatibility. Its `dataplane_username` and
`dataplane_password` authenticate the agent only when
`haproxy.agent.tls.enabled: false`. That mode uses plain HTTP. The controller
reloads password changes live; legacy agents read them at startup, so password
rotation requires a coordinated pod rollout and can interrupt configuration
updates to a mixed fleet.

!!! warning "Secrets with offline Helm rendering"
    `helm template` and Argo CD can't preserve generated Secrets through `lookup`.
    Supply a stable `credentials.dataplane.password` through your deployment's
    secret management. Agent TLS identities are generated at runtime and remain
    stable across offline renders.

`/debug/vars/credentials` returns the credential version and `has_dataplane_creds`, without credential values. Other debug endpoints expose configuration and rendered files. See [Debugging](./debugging.md#accessing-the-server) for access controls.

Watcher logs record resource identities, versions, and index-key counts. Resource contents and index values stay out of those logs. HTTP source logs and errors omit URL user information, query strings, and fragments, which can contain credentials.

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

### HAProxy pod hardening

The HAProxy pods the chart deploys also run restricted: `runAsNonRoot: true`, `allowPrivilegeEscalation: false`, `capabilities.drop: [ALL]`, and `seccompProfile.type: RuntimeDefault` — compatible with the restricted Pod Security Standard. The chart auto-derives the UID from `haproxy.enterprise.enabled` and applies it identically as `runAsUser`, `runAsGroup`, and `fsGroup`: community images use `99` (the `haproxy` user), enterprise images use `1000` (the `hapee-lb` user).

!!! warning "HAProxy binds privileged ports 80 and 443"
    HAProxy binds the literal `:80` and `:443`. Community images drop all capabilities and run non-root, so binding these privileged ports relies on the node permitting unprivileged low-port binds — the kernel `sysctl` `net.ipv4.ip_unprivileged_port_start` must be `<= 80` (the default in kind and Docker). On a cluster that keeps the kernel default of `1024`, either lower that `sysctl` or add `CAP_NET_BIND_SERVICE` to the HAProxy container's capabilities. Enterprise images run as UID `1000` and their binaries carry `CAP_NET_BIND_SERVICE` file capabilities, so the chart adds that capability automatically when `haproxy.enterprise.enabled: true`.

## Network exposure

The controller pod exposes three HTTP ports (all chart defaults):

| Port | Endpoint | Notes |
|------|----------|-------|
| `8080` | `/healthz`, `/debug/vars`, `/debug/events`, `/debug/pprof/` | `controller.ports.healthz` configures the process, pod, Service, probes, and policy together. `/healthz` serves probes; `/debug/*` accepts loopback connections only. Restrict `pods/portforward` with RBAC |
| `9090` | `/metrics` | `controller.ports.metrics` configures the process, pod, Service, and monitors together; set it to `0` to disable metrics |
| `9443` | Validating webhook | Required when the webhook is enabled |

Outbound, the controller talks to the Kubernetes API server and to the agent on
each HAProxy pod (default port `5555`). Mutual TLS protects deployment requests,
including the TLS private keys carried as auxiliary files. Disabling agent TLS
requires equivalent network encryption, such as an encrypted Container Network
Interface (CNI) or service mesh.

Agent liveness probes use the local Unix socket, so certificate expiry doesn't restart the agent. The control endpoints
still require one. `haptic agent state` inside the agent container uses a local
Unix socket that exposes state without accepting deployments.

NetworkPolicies are enabled by default for the controller and HAProxy pods.
Managed Varnish and Valkey tiers also receive release-scoped policies when
enabled. Review their allowed traffic:

- The controller policy restricts ingress to the exposed ports (metrics ingress only opens when `controller.networkPolicy.ingress.monitoring.enabled: true` — it's off by default, so enable it for Prometheus). Egress covers DNS, the Kubernetes API server, and the HAProxy agent/stats ports, **plus a default `controller.networkPolicy.egress.additionalRules` entry allowing every in-cluster pod** (so template helpers like `http.Fetch()` work) — set it to `[]` to lock egress down (see [Networking](./networking.md#production-hardening)).
- The HAProxy policy defaults to `allowExternal: true`, which renders a permissive all-port ingress rule — deliberate, because Gateway listeners bind dynamic ports.
- The Varnish policy admits only same-release HAProxy cache requests and permits egress only to DNS and the same HAProxy HTTP origin. The managed Valkey/Sentinel policy admits only same-release HAProxy/SPOA and store-internal traffic.

To tighten, replace, or debug these policies — including the required traffic and selectors — see [Networking](./networking.md#replacing-the-shipped-policies). NetworkPolicy doesn't grant remote access to loopback-only diagnostics. Use `kubectl port-forward` and restrict that permission with RBAC.

## Secrets in templates

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

Store password hashes in the Secret. To generate a bcrypt hash interactively:

```bash
htpasswd -n -B -C 10 admin | cut -d: -f2
```

Hash checks cost CPU on authentication and configuration parsing. Choose a work factor for your authentication policy and measure it; see [Password hash performance](./performance.md#password-hash-performance).

## Annotation input as a trust boundary

Most annotation values reach the config as validated or escaped data: CIDR-list annotations are parsed as CIDRs (an invalid entry fails the render), and header, cookie, SNI, and rewrite-target values are checked against a strict character set that rejects control characters, so they can't break out of their directive and inject arbitrary HAProxy config.

The `*-config-snippet` annotations (`haproxy.org/backend-config-snippet`, `nginx.ingress.kubernetes.io/configuration-snippet`, and the like) are the deliberate exception: their value is inserted into the rendered config verbatim. Anyone who can create or edit an Ingress in a watched namespace can therefore inject arbitrary HAProxy directives. Treat Ingress edit permission in watched namespaces as equivalent to HAProxy config access, and restrict it with RBAC accordingly.

## Admission validation coverage

The admission webhook renders the proposed state and runs `haproxy -c` before
accepting creates and updates for watched resources with
`enableValidationWebhook: true`. The bundled libraries enable this for Ingress
and Gateway routing resources and BackendTLSPolicy. See
[Webhook integration](../watching-resources.md#validating-webhook-scope).

The Gateway library leaves GatewayClass admission disabled because the controller
manages the configured class itself. GatewayClass changes therefore reach
reconciliation without a HAPTIC admission check; config-load tests don't validate
arbitrary future changes to live resources. For checks during reconciliation, see
[Render validation](./debugging.md#haproxy-refused-the-config-the-fleet-was-given-configvalidatedfalse).

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

Replace `<namespace>`/`<release>` with your Helm release. The SA name is the release `fullname` `<release>-haptic` (collapsing to `<release>` only when the release name already contains `haptic`) unless you overrode `controller.serviceAccount.name` — get the exact value with `kubectl -n <namespace> get sa`. A rule keyed on the wrong SA name silently never matches, so the controller's Secret reads go unaudited.

## Checklist

Before exposing a HAPTIC deployment to production traffic:

- [ ] Mutual TLS for agent traffic, with certificate expiry monitored and CA rotation completed before its overlap deadline.
- [ ] RBAC that limits `pods/portforward` access to loopback-only `/debug/*` endpoints.
- [ ] Watched-resource selectors scoped to the namespaces you intend to serve.
- [ ] Release namespace labelled with `pod-security.kubernetes.io/enforce=restricted`.
- [ ] NetworkPolicy allowing DNS, Kubernetes API, agent traffic, and any configured HTTP resources.
- [ ] Audit policy in place for `HAProxyTemplateConfig` changes.
- [ ] Image signature verification (`cosign verify …`) wired into your admission policy — see [Releasing](../development/releasing.md#supply-chain-security).

## See also

- [Networking](./networking.md) — NetworkPolicy mechanics: default rules, hardening, replacement policies
- [Monitoring](./monitoring.md) — signals for auth failures, webhook drops, leader flaps
- [Debugging](./debugging.md) — accessing `/debug/*` safely
- [High Availability](./high-availability.md) — leader election RBAC and lease ownership
