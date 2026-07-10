---
description: "Helm chart for deploying HAPTIC, a template-driven HAProxy ingress controller for Kubernetes, with preconfigured template libraries for Ingress and Gateway API."
---

# Deploying with Helm

The Helm chart is the supported way to install HAPTIC. A default install deploys the controller, a 2-replica HAProxy Deployment, the CRDs, and a ready-to-use set of [template libraries](template-libraries.md) covering Ingress and Gateway API — so traffic routes without any template authoring. Cross-namespace HAProxy management, conditional resource watching, and which libraries load are all configured through Helm values.

## Prerequisites

- Kubernetes 1.21+ (the default `PodDisruptionBudget` uses `policy/v1` and the controller watches `discovery.k8s.io/v1` EndpointSlices)
- Helm 3.0+
- **HAProxy 3.0 or newer** (the chart deploys HAProxy by default; template libraries require 3.0+ for SSL/TLS features)

!!! note
    The `haproxyVersion` value controls both the controller image tag and the HAProxy image tag, ensuring version compatibility between the two. See the [configuration reference](./reference.md) for details.

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

## What's in This Chart

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

| I want to… | See |
|------------|-----|
| Configure ingress class or filter namespaces | [Configuration](./configuration.md) |
| Set up TLS/HTTPS | [SSL Certificates](./ssl-certificates.md) |
| Use Ingress annotations (auth, rate limiting, etc.) | [Annotations](./annotations.md) |
| Tune HAProxy resource limits or service type | [HAProxy Deployment](./haproxy-deployment.md) |
| Enable or disable template libraries | [Template Libraries](./template-libraries.md) |
| Run multiple controller replicas | [High Availability](./operations/high-availability.md) |
| Set up Prometheus scraping | [Monitoring](./operations/monitoring.md) |
| Restrict network access with NetworkPolicy | [Networking](./operations/networking.md) |
| Diagnose problems | [Troubleshooting](./operations/troubleshooting.md) |

## Upgrading

```bash
helm upgrade my-controller oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.2.0-alpha.1
```

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
