---
description: "Helm chart for deploying HAPTIC, a template-driven HAProxy ingress controller for Kubernetes, with preconfigured template libraries for Ingress and Gateway API."
---

# Deploying with Helm

Install HAPTIC with the Helm chart. It deploys the controller, two HAProxy
replicas, custom resource definitions (CRDs), and the [template libraries](template-libraries.md)
for Ingress and Gateway API. Configure the installation through Helm values.
For a first installation with a sample app, follow [Getting started](getting-started.md).

## Prerequisites

- Kubernetes 1.33 or newer; see [Kubernetes compatibility checks](./operations/kubernetes-versions.md)
- Helm 3.8 or newer

!!! note
    Set `haproxyVersion` to select matching controller and HAProxy images. The default is 3.4; see [HAProxy versions](./operations/haproxy-versions.md) for supported alternatives.

## Installation

```bash
helm install my-controller oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic --version 0.2.0-alpha.3
```

With custom values:

```bash
helm install my-controller oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.2.0-alpha.3 \
  -f my-values.yaml
```

## What's in this chart

The chart deploys:

- **Controller Deployment** -- the controller that watches resources and generates configurations
- **HAProxy Deployment** (optional, on by default) -- the load balancers that serve your traffic, each with the HAPTIC agent alongside
- **CRDs** -- six resource types for configuration, libraries, and rendered output; see the [CRD reference](./crd-reference.md). Helm preserves these definitions during uninstall.
- **`HAProxyTemplateConfig` custom resource** -- built from `controller.config`, listing the enabled libraries in merge order via `spec.libraryRefs`
- **`HAProxyTemplateLibrary` custom resources** -- one per enabled `controller.templateLibraries.*` entry, each carrying that library's snippets, templating settings, maps, files, and tests
- **IngressClass** and **GatewayClass** -- routing API integration for Ingress and Gateway API resources
- **RBAC**, **NetworkPolicy**, and **ServiceAccount** -- permissions and network security
- **Vector sidecar** (on by default) -- processes access logs and exposes request and SPOA hub metrics
- **Pre-rollout validation hook** and **CRD upgrade hook** (both on by default) -- `pre-install`/`pre-upgrade` Jobs that run `haptic preflight` against your values and server-side apply the bundled CRDs, so a bad configuration or a stale CRD schema fails the release instead of the running fleet
- Optional **ServiceMonitor** and **PodMonitors** -- Prometheus integration for the controller and the HAProxy pods
- **Admission webhook** (on by default) -- checks proposed changes to watched resources before Kubernetes accepts them

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

To run separate HAPTIC installations for different teams or routing resources,
give each release distinct names and controller identifiers:

| Setting | Values key | Why it must differ |
|---------|-----------|--------------------|
| Release name and namespace | `helm install <name> --namespace <ns>` | Scopes every Kubernetes object the chart creates, and the leader-election lease |
| Ingress class | `ingressClass.name` | The controller watches only Ingresses whose `spec.ingressClassName` equals this value. Two releases sharing it would both process the same Ingresses |
| Gateway class | `gatewayClass.name` | The controller watches only Gateways whose `spec.gatewayClassName` equals this value |
| Controller identifier | `ingressClass.controllerName` and `gatewayClass.controllerName` | The `GatewayClass` watch is filtered to `spec.controllerName`; two releases sharing it would fight over the same GatewayClasses' status. Default: `haproxy-haptic.org/controller` |

The chart derives watch filters from `ingressClass.name` and `gatewayClass.name`.
Set the class names in values; the chart updates the filters to match.

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
  --version 0.2.0-alpha.3 \
  --namespace haptic-team-b --create-namespace \
  -f team-b-values.yaml
```

Ingress and Gateway authors then select this release with `ingressClassName: haptic-team-b` or `gatewayClassName: haptic-team-b`.

## Upgrading

For an upgrade from 0.1.0 or a 0.2.0 alpha, read the [0.2 upgrade guide](upgrading-to-0.2.md) before applying your values.

If you installed with a values file, re-pass it so your custom values survive the upgrade:

```bash
helm upgrade my-controller oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.2.0-alpha.3 \
  -f my-values.yaml
```

Otherwise, upgrade without it:

```bash
helm upgrade my-controller oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.2.0-alpha.3
```

!!! warning "The chart owns the `HAProxyTemplateConfig`"
    Put persistent configuration changes under `controller.config` in your values file. Edits made with `kubectl edit` or `kubectl patch` take effect immediately, but a later `helm upgrade` can overwrite them. Helm also owns the library objects it creates.

## Uninstalling

```bash
helm uninstall my-controller
```

Replace `my-controller` with whatever release name you used at install time. `helm uninstall` removes all resources created by the chart; the chart's CRDs are preserved so a reinstall picks up existing custom resources. To remove the CRDs as well, delete the whole `haproxy-haptic.org` API group explicitly:

```bash
kubectl delete crd \
  haproxytemplateconfigs.haproxy-haptic.org \
  haproxytemplatelibraries.haproxy-haptic.org \
  haproxycfgs.haproxy-haptic.org \
  haproxygeneralfiles.haproxy-haptic.org \
  haproxycrtlistfiles.haproxy-haptic.org \
  haproxymapfiles.haproxy-haptic.org
```
