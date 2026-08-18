# HAPTIC Helm Chart

HAPTIC (HAProxy Template Ingress Controller) ships as a single Helm chart that installs the controller, its CRDs, an `HAProxyTemplateConfig` resource, and (optionally) the HAProxy pods it manages. The controller watches Ingress / Gateway API / CRD resources, renders [Scriggo](https://scriggo.com/) templates to HAProxy configuration, and pushes the result to a HAPTIC agent sidecar in each HAProxy pod, which writes the files and reloads or applies them at runtime.

Full documentation: [haproxy-haptic.org/docs](https://haproxy-haptic.org/docs/dev/) (this chart's pages live under *Deploying with Helm*).

## Prerequisites

- Kubernetes **1.21+** (default `PodDisruptionBudget` is `policy/v1`; watches `discovery.k8s.io/v1` EndpointSlices)
- Helm **3.8+** — the `oci://` chart reference needs OCI registry support, generally available since Helm 3.8
- **HAProxy 3.0+** — the chart deploys HAProxy by default and the SSL library requires 3.0+. Pin a specific series via `haproxyVersion`.
- **cert-manager** (optional but recommended for production) — with its API present, the default HTTPS certificate is issued by [cert-manager](https://cert-manager.io/docs/installation/). Without it, the chart creates a long-lived self-signed development certificate; production users should provide a trusted certificate — see [SSL Certificates](https://haproxy-haptic.org/docs/dev/ssl-certificates/).

## Installation

```bash
helm install my-controller oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.2.0-alpha.1
```

With custom values:

```bash
helm install my-controller oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.2.0-alpha.1 \
  -f my-values.yaml
```

Uninstall removes everything the chart created:

```bash
helm uninstall my-controller
```

## Key Values

The full values reference lives in [Chart Values Reference](https://haproxy-haptic.org/docs/dev/reference/). The ones operators most commonly change:

| Parameter | Default | Notes |
|-----------|---------|-------|
| `controller.replicaCount` | `2` | Controller replicas; 2+ runs with leader election |
| `haproxyVersion` | `3.4` | Major.minor HAProxy series (`3.0` / `3.1` / `3.2` / `3.3` / `3.4`); pairs the controller image tag (`-haproxy3.4`) with the HAProxy pod image |
| `haproxy.image.tag` | derived from `haproxyPatchVersions` | Override to pin a specific patch |
| `haproxy.enabled` | `true` | Disable to manage HAProxy pods separately |
| `haproxy.enterprise.enabled` | `false` | Switch to HAProxy Enterprise images |
| `controller.templateLibraries.ingress.enabled` | `true` | Kubernetes Ingress support |
| `controller.templateLibraries.gateway.enabled` | `true` | Gateway API (HTTPRoute / GRPCRoute / TLSRoute) support |
| `controller.templateLibraries.haproxytech.enabled` | `false` | `haproxy.org/*` annotation compatibility |
| `controller.templateLibraries.haproxyIngress.enabled` | `false` | `haproxy-ingress.github.io/*` annotation compatibility |
| `controller.templateLibraries.nginxIngress.enabled` | `false` | `nginx.ingress.kubernetes.io/*` annotation compatibility |
| `controller.ports.healthz` | `8080` | Single listener for `/healthz` + `/debug/vars` + `/debug/pprof`; drives the process, pod, Service, probes, and NetworkPolicy together |
| `controller.logLevel` | `INFO` | Initial level — `TRACE` / `DEBUG` / `INFO` / `WARN` / `ERROR` (case-insensitive); runtime-adjustable via the `HAProxyTemplateConfig` CRD's `spec.logging.level` |
| `controller.monitoring.serviceMonitor.enabled` | `false` | Prometheus Operator `ServiceMonitor` |
| `controller.networkPolicy.enabled` | `true` | NetworkPolicy allowing controller ↔ HAProxy ↔ API server |
| `cache.varnish.networkPolicy.enabled` | `true` | When the Varnish tier is enabled, isolate it to same-release HAProxy cache traffic and loopback origin requests |
| `ingressClass.name` / `gatewayClass.name` | `haptic` | Class names the controller matches against — deliberately distinct from `haproxy` so HAPTIC can run side-by-side with other HAProxy-based ingress controllers; set to `haproxy` when replacing an incumbent |
| `credentials.dataplane.username` / `credentials.dataplane.password` | `admin` / generated | Empty `password` generates a random 32-char password, preserved across upgrades by reading the existing Secret. GitOps tools that render without cluster access regenerate it every sync — **set explicitly there and in production**. See [Credentials](https://haproxy-haptic.org/docs/dev/reference/#credentials). |

## Template Libraries

Templates are merged at Helm render time in a fixed priority order (later libraries override earlier ones):

| Library | Default | Covers |
|---------|---------|--------|
| `base` | on | Core `haproxyConfig`, extension points (`render_glob` patterns) — must stay resource-agnostic; disabling drops the haproxyConfig the other libraries plug into |
| `ssl` | on | Terminate TLS, crt-list management, SSL passthrough |
| `ingress` | on | Kubernetes `networking.k8s.io/v1` Ingress |
| `gateway` | on | Gateway API `HTTPRoute` / `GRPCRoute` / `TLSRoute` (requires Gateway CRDs installed) |
| `ingressAnnotationsCompat` | on | Shared scaffold consumed by the Ingress vendor annotation libraries below (level 2.5) |
| `governance` | on | Declarative constraints over any watched resource; inert until you define `controller.config.templatingSettings.extraContext.governance.rules` |
| `hapticAnnotations` | on | `haproxy-haptic.org/*` — HAPTIC's own annotation vocabulary, and the only annotation library on by default. A superset of the three vendor libraries below |
| `haproxytech` | off | `haproxy.org/*` annotation compatibility ([haproxytech/kubernetes-ingress](https://github.com/haproxytech/kubernetes-ingress)) |
| `haproxy-ingress` | off | `haproxy-ingress.github.io/*` annotation compatibility ([jcmoraisjr/haproxy-ingress](https://haproxy-ingress.github.io/)) |
| `nginx-ingress` | off | `nginx.ingress.kubernetes.io/*` annotation compatibility |
| `spoaHub` | off, auto-loads | HAProxy-side wiring for the SPOA hub sidecar. Loads automatically when `spoaHub.enabled: true` or any `spoaHub.plugins.<X>.enabled` is truthy; set `controller.templateLibraries.spoaHub.enabled: true` only to force-load it with no plugins on |

Each library contributes entries under `watchedResources`, `templateSnippets`, `maps`, `files`, `sslCertificates`, `haproxyConfig`, and `validationTests` — user-provided values in `controller.config` override library defaults. See [Template Libraries](https://haproxy-haptic.org/docs/dev/template-libraries/) for the library-merging design, extension points, and snippet priority ranges.

## Documentation

| Area | Where to look |
|------|---------------|
| Getting started | [Getting Started](https://haproxy-haptic.org/docs/dev/getting-started/), [Deploying with Helm](https://haproxy-haptic.org/docs/dev/deploying-with-helm/) |
| Ingress & Gateway setup | [IngressClass](https://haproxy-haptic.org/docs/dev/ingress-class/), [GatewayClass](https://haproxy-haptic.org/docs/dev/gateway-class/) |
| SSL and annotations | [SSL Certificates](https://haproxy-haptic.org/docs/dev/ssl-certificates/), [Annotations](https://haproxy-haptic.org/docs/dev/annotations/) |
| Running HAProxy | [HAProxy Deployment](https://haproxy-haptic.org/docs/dev/haproxy-deployment/) |
| Library reference | [Template Libraries](https://haproxy-haptic.org/docs/dev/template-libraries/) |
| Day-two operations | [High Availability](https://haproxy-haptic.org/docs/dev/operations/high-availability/), [Monitoring](https://haproxy-haptic.org/docs/dev/operations/monitoring/), [Networking](https://haproxy-haptic.org/docs/dev/operations/networking/), [Debugging](https://haproxy-haptic.org/docs/dev/operations/debugging/), [Troubleshooting](https://haproxy-haptic.org/docs/dev/troubleshooting/) |
| Full values reference | [Chart Values Reference](https://haproxy-haptic.org/docs/dev/reference/) |

## Upgrading

```bash
helm upgrade my-controller oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.2.0-alpha.1 -f my-values.yaml
```

Helm itself never upgrades CRDs it installed from a chart's `crds/` directory. The chart closes that gap with a `pre-install`/`pre-upgrade` hook Job that server-side applies the bundled CRDs, enabled by default (`crds.upgradeJob.enabled`), so the command above is all you need.

If you manage CRDs out-of-band and set `crds.upgradeJob.enabled: false`, apply them yourself before upgrading:

```bash
helm show crds oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.2.0-alpha.1 | kubectl apply --server-side --force-conflicts -f -
```

## Examples

Ready-to-adapt configurations live in the repository's top-level [examples/](https://gitlab.com/haproxy-haptic/haptic/-/tree/main/examples) directory.

## License

Apache-2.0 — see root `LICENSE`.
