---
description: "Helm chart for deploying HAPTIC, a template-driven HAProxy ingress controller for Kubernetes, with preconfigured template libraries for Ingress and Gateway API."
---

# Deploying with Helm

The Helm chart is the supported way to install HAPTIC. A default install deploys the controller, a 2-replica HAProxy Deployment, the CRDs, and a ready-to-use set of [template libraries](template-libraries.md) covering Ingress and Gateway API — so traffic routes without any template authoring. Cross-namespace HAProxy management, conditional resource watching, and which libraries load are all configured through Helm values.

## Prerequisites

- Kubernetes 1.21+ (the default `PodDisruptionBudget` uses `policy/v1` and the controller watches `discovery.k8s.io/v1` EndpointSlices)
- Helm 3.0+

!!! note
    The `haproxyVersion` value controls both the controller image tag and the HAProxy image tag, ensuring version compatibility between the two. Supported versions start at HAProxy 3.0 — the template libraries require 3.0+ for their SSL/TLS features. See the [Chart Values Reference](./reference.md) for details.

## Installation

```bash
helm install my-controller oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic --version 0.2.0-alpha.1
```

With custom values:

```bash
helm install my-controller oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.2.0-alpha.1 \
  -f my-values.yaml
```

## What's in this chart

The chart deploys:

- **Controller Deployment** -- the controller that watches resources and generates configurations
- **HAProxy Deployment** (optional, on by default) -- the load balancers that serve your traffic, with Dataplane API sidecars
- **CRDs** -- five resource types under the `haproxy-haptic.org` API group: `HAProxyTemplateConfig` (input — templates, watched resources, settings) plus `HAProxyCfg`, `HAProxyGeneralFile`, `HAProxyCRTListFile`, and `HAProxyMapFile` (outputs the controller publishes for observability). Installed from `charts/haptic/crds/`; preserved across `helm uninstall` (delete them explicitly — see [Uninstalling](#uninstalling))
- **`HAProxyTemplateConfig` custom resource** -- the merged template-library configuration that drives config rendering (created from the enabled `controller.templateLibraries.*` at render time)
- **IngressClass** and **GatewayClass** -- routing API integration for Ingress and Gateway API resources
- **RBAC**, **NetworkPolicy**, and **ServiceAccount** -- permissions and network security
- Optional **ServiceMonitor** -- Prometheus integration for metrics scraping
- Optional **admission webhook** -- configuration validation before deployment

New to HAPTIC? [Getting Started](getting-started.md) walks through a first install and a sample app, end to end.

## Where to go next

Jump to what you need:

| Task | See |
|------------|-----|
| Configure or filter the ingress class | [IngressClass](./ingress-class.md) |
| Set up TLS/HTTPS | [SSL Certificates](./ssl-certificates.md) |
| Use Ingress annotations (auth, rate limiting, etc.) | [Annotations](./annotations.md) |
| Tune HAProxy resource limits or service type | [HAProxy Deployment](./haproxy-deployment.md) |
| Enable or disable template libraries | [Template Libraries](./template-libraries.md) |
| Run multiple controller replicas | [High Availability](./operations/high-availability.md) |
| Set up Prometheus scraping | [Monitoring](./operations/monitoring.md) |
| Restrict network access with NetworkPolicy | [Networking](./operations/networking.md) |
| Diagnose problems | [Troubleshooting](./troubleshooting.md) |

## Running multiple HAPTIC instances in one cluster

Running more than one HAPTIC release in the same cluster is supported — for example, one release per team, or one handling `Ingress` while another handles a custom resource. The releases stay independent as long as a few identifiers don't overlap. Give each additional release its own values for all of these:

| Setting | Values key | Why it must differ |
|---------|-----------|--------------------|
| Release name and namespace | `helm install <name> --namespace <ns>` | Scopes every Kubernetes object the chart creates, and the leader-election lease |
| Ingress class | `ingressClass.name` | The controller watches only Ingresses whose `spec.ingressClassName` equals this value. Two releases sharing it would both process the same Ingresses |
| Gateway class | `gatewayClass.name` | The controller watches only Gateways whose `spec.gatewayClassName` equals this value |
| Controller identifier | `ingressClass.controllerName` and `gatewayClass.controllerName` | The `GatewayClass` watch is filtered to `spec.controllerName`; two releases sharing it would fight over the same GatewayClasses' status. Default: `haproxy-haptic.org/controller` |

You don't edit any watch `fieldSelector` by hand — the chart derives each resource's `fieldSelector` from the class names above, so a unique `ingressClass.name` and `gatewayClass.name` is enough to scope a release's watches.

The leader-election lease name (`controller.config.controller.leaderElection.leaseName`) defaults to the release's full name, so distinct release names already produce distinct leases. Set it explicitly only if you deliberately reuse a name.

Example values for a second release with its own classes:

```yaml
# team-b-values.yaml
ingressClass:
  name: haptic-team-b
  controllerName: haproxy-haptic.org/team-b
gatewayClass:
  name: haptic-team-b
  controllerName: haproxy-haptic.org/team-b
```

```bash
helm install haptic-team-b oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.2.0-alpha.1 \
  --namespace haptic-team-b --create-namespace \
  -f team-b-values.yaml
```

Ingress and Gateway authors then select this release with `ingressClassName: haptic-team-b` or `gatewayClassName: haptic-team-b`.

## Upgrading

If you installed with a values file, re-pass it so your custom values survive the upgrade:

```bash
helm upgrade my-controller oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.2.0-alpha.1 \
  -f my-values.yaml
```

Otherwise, upgrade without it:

```bash
helm upgrade my-controller oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.2.0-alpha.1
```

!!! note "Values win — don't hand-edit the live `HAProxyTemplateConfig`"
    Under the chart there's exactly one `HAProxyTemplateConfig`, generated from
    your values: `controller.config.*` is merged last, on top of the template
    libraries' defaults, so it's the highest-priority layer. The chart owns the
    resource, so a `kubectl edit` on the live CRD is reverted on the next `helm
    upgrade`. To change any field, set `controller.config.*` in your values and
    upgrade.

### What an upgrade restarts

A chart upgrade rolls the HAProxy pods only when their pod template changes —
when you change:

- `haproxyVersion` or `haproxy.image.*` (the pod image tag), or
- the Dataplane API credentials (`credentials.dataplane.*`), or
- `haproxy.initialConfig` or `haproxy.shmStats.*` (the bootstrap config).

These feed the HAProxy Deployment's image tag and its `checksum/secret` /
`checksum/config` annotations, so Kubernetes rolls the pods. The roll is hitless:
the strategy is `maxUnavailable: 0`, `maxSurge: 1`, so a new pod passes readiness
before an old one stops — keep `haproxy.replicaCount` at 2 or more.

Every other change — template libraries, `controller.config.*`, watched
resources, annotations — flows through the controller instead: it re-renders and
applies the result over the Dataplane API as an in-place hitless reload (or a
reload-free runtime update), with **no** HAProxy pod restart. See [HAProxy
versions — upgrading to a new series](./operations/haproxy-versions.md#upgrading-to-a-new-series)
for the image-series case.

## Uninstalling

```bash
helm uninstall my-controller
```

Replace `my-controller` with whatever release name you used at install time. `helm uninstall` removes all resources created by the chart; the chart's CRDs are preserved so a reinstall picks up existing custom resources. To remove the CRDs as well, delete the whole `haproxy-haptic.org` API group explicitly:

```bash
kubectl delete crd \
  haproxytemplateconfigs.haproxy-haptic.org \
  haproxycfgs.haproxy-haptic.org \
  haproxygeneralfiles.haproxy-haptic.org \
  haproxycrtlistfiles.haproxy-haptic.org \
  haproxymapfiles.haproxy-haptic.org
```
