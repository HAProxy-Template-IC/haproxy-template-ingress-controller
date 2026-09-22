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
| `services` | get, list, watch, create, update, patch, delete | Gateway library only: manage Services for Gateway listeners |
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

The chart renews its agent certificates automatically. Monitor renewal failures
and protect the issuer Secret, which contains the private certificate-authority
key. To supply your own certificates, set `haproxy.agent.tls.managed: false` and
manage their renewal yourself. See [agent certificates](agent-certificates.md)
for Secret formats, rotation, and recovery.

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

Debug endpoints expose configuration and rendered files, which can contain
credentials. Limit `pods/portforward` access to trusted operators. See
[accessing diagnostics](debugging.md#accessing-the-server).

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

The controller pod exposes these ports by default:

| Port | Endpoint | Notes |
|------|----------|-------|
| `8080` | `/healthz`, `/debug/vars`, `/debug/events`, `/debug/pprof/` | `controller.ports.healthz` configures the process, pod, Service, probes, and policy together. `/healthz` serves probes; `/debug/*` accepts loopback connections only. Restrict `pods/portforward` with RBAC |
| `9090` | `/metrics` | `controller.ports.metrics` configures the process, pod, Service, and monitors together; set it to `0` to disable metrics |
| `9443` | Validating webhook over HTTPS | Required when the webhook is enabled |

Outbound, the controller talks to the Kubernetes API server and to the agent on
each HAProxy pod (default port `5555`). Mutual TLS protects deployment requests,
including the TLS private keys carried as auxiliary files. Disabling agent TLS
requires equivalent network encryption, such as an encrypted Container Network
Interface (CNI) or service mesh.

NetworkPolicies are enabled by default. Two defaults need attention when you
restrict access:

- Controller egress allows connections to all cluster pods. Replace
  `controller.networkPolicy.egress.additionalRules` with the destinations your
  templates need.
- `haproxy.networkPolicy.allowExternal: true` allows ingress on all HAProxy pod
  ports, including dynamically configured Gateway listeners.

Controller metrics ingress is disabled until you set
`controller.networkPolicy.ingress.monitoring.enabled: true`. See
[network access](networking.md) for complete rules, cache and rate-limit store
policies, and Prometheus selectors.

## Secrets in templates

Templates can read Secrets included in your watches. Decode `.data` values with
`b64decode`. This example reads a password hash into an HAProxy userlist; it
doesn't enable authentication on a route by itself. For that, use the
[basic-auth setup](../annotations.md#quick-start-basic-authentication).

<div class="pg-embed" markdown data-tab="haproxy.cfg" data-controls="tabs,resources" data-focus="20" data-title="Watched Secret → userlist" data-height="480">

<p class="pg-task">Run the example and find the decoded password hash in the generated userlist.</p>

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

Kubernetes audit logs can record who changes HAPTIC configuration and which
Secrets the controller reads. A cluster administrator must add these rules to
the API server's [audit policy](https://kubernetes.io/docs/tasks/debug/debug-cluster/audit/);
this isn't a resource you apply with `kubectl`.

The following policy uses `Metadata` to record identities and operations without
recording configuration or Secret contents. It targets the default `haptic`
release in namespace `haptic`:

```yaml
apiVersion: audit.k8s.io/v1
kind: Policy
rules:
  - level: Metadata
    resources:
      - group: haproxy-haptic.org
        resources: ["haproxytemplateconfigs", "haproxytemplatelibraries"]
  - level: Metadata
    users: ["system:serviceaccount:haptic:haptic"]
    resources:
      - group: ""
        resources: ["secrets"]
```

Put these rules before broader rules in an existing policy: Kubernetes uses the
first matching rule. If you use a different release name or ServiceAccount,
check the account used by the controller and update the `users` entry:

```bash
kubectl get deployment haptic-controller --namespace haptic \
  -o jsonpath='{.spec.template.spec.serviceAccountName}{"\n"}'
```

## Checklist

Before exposing a HAPTIC deployment to production traffic:

- [ ] Default mutual TLS retained, with failed certificate-renewal Jobs monitored. If you supply external certificates, monitor their expiry and manage CA rotation.
- [ ] RBAC that limits `pods/portforward` access to loopback-only `/debug/*` endpoints.
- [ ] Watched-resource selectors scoped to the namespaces you intend to serve.
- [ ] Release namespace labelled with `pod-security.kubernetes.io/enforce=restricted`.
- [ ] NetworkPolicy allowing DNS, Kubernetes API, agent traffic, and any configured HTTP resources.
- [ ] Audit policy in place for `HAProxyTemplateConfig` changes.

## See also

- [Networking](./networking.md) — NetworkPolicy mechanics: default rules, hardening, replacement policies
- [Monitoring](./monitoring.md) — signals for auth failures, webhook drops, leader flaps
- [Debugging](./debugging.md) — accessing `/debug/*` safely
- [High Availability](./high-availability.md) — leader election RBAC and lease ownership
