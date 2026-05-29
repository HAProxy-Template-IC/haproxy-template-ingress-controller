# HAPTIC Helm Chart

HAPTIC (HAProxy Template Ingress Controller) ships as a single Helm chart that installs the controller, an `HAProxyTemplateConfig` CRD, and (optionally) the HAProxy pods it manages. The controller watches Ingress / Gateway API / CRD resources, renders [Scriggo](https://scriggo.com/) templates to HAProxy configuration, and pushes the result to HAProxy via the [Dataplane API](https://github.com/haproxytech/dataplaneapi).

Full documentation: see [`docs/`](./docs/index.md) in this directory.

## Prerequisites

- Kubernetes **1.19+**
- Helm **3.0+**
- **HAProxy 3.0+** — the chart deploys HAProxy by default and the SSL library requires 3.0+. Pin a specific series via `haproxyVersion`.

## Installation

```bash
helm install my-controller oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.1.0
```

With custom values:

```bash
helm install my-controller oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.1.0 \
  -f my-values.yaml
```

Uninstall removes everything the chart created:

```bash
helm uninstall my-controller
```

## Key Values

The full values reference lives in [`docs/reference.md`](./docs/reference.md). The ones operators most commonly change:

| Parameter | Default | Notes |
|-----------|---------|-------|
| `replicaCount` | `2` | Controller replicas; 2+ runs with leader election |
| `haproxyVersion` | `3.2` | Major.minor HAProxy series (`3.0` / `3.1` / `3.2` / `3.3`); pairs the controller image tag (`-haproxy3.2`) with the HAProxy pod image |
| `haproxy.image.tag` | derived from `haproxyPatchVersions` | Override to pin a specific patch |
| `haproxy.enabled` | `true` | Disable to manage HAProxy pods separately |
| `haproxy.enterprise.enabled` | `false` | Switch to HAProxy Enterprise images |
| `controller.templateLibraries.ingress.enabled` | `true` | Kubernetes Ingress support |
| `controller.templateLibraries.gateway.enabled` | `true` | Gateway API (HTTPRoute / GRPCRoute) support |
| `controller.templateLibraries.haproxytech.enabled` | `true` | `haproxy.org/*` annotation compatibility |
| `controller.templateLibraries.haproxyIngress.enabled` | `true` | `haproxy-ingress.github.io/*` annotation compatibility |
| `controller.templateLibraries.nginxIngress.enabled` | `false` | `nginx.ingress.kubernetes.io/*` annotation compatibility |
| `controller.debugPort` | `8080` | Listener for `/healthz` + `/debug/vars` + `/debug/pprof`. Setting to `0` disables both `/debug/*` and `/healthz` (and breaks Kubernetes probes) — restrict `/debug/*` via NetworkPolicy instead |
| `controller.logLevel` | `INFO` | Initial level — `TRACE` / `DEBUG` / `INFO` / `WARN` / `ERROR` (case-insensitive); runtime-adjustable via the `HAProxyTemplateConfig` CRD's `spec.logging.level` |
| `monitoring.serviceMonitor.enabled` | `false` | Prometheus Operator `ServiceMonitor` |
| `networkPolicy.enabled` | `true` | NetworkPolicy allowing controller ↔ HAProxy ↔ API server |
| `ingressClass.name` / `gatewayClass.name` | `haptic` | Class names the controller matches against — deliberately distinct from `haproxy` so HAPTIC can run side-by-side with other HAProxy-based ingress controllers; set to `haproxy` when replacing an incumbent |
| `credentials.dataplane.username` / `credentials.dataplane.password` | `admin` / sha256-of-release-name | Empty `password` falls back to a deterministic 32-char SHA256 hash (preserved across upgrades from the existing Secret); **set explicitly in production**. See [`docs/reference.md`](./docs/reference.md#credentials). |

## Template Libraries

Templates are merged at Helm render time in a fixed priority order (later libraries override earlier ones):

| Library | Default | Covers |
|---------|---------|--------|
| `base` | on | Core `haproxyConfig`, extension points (`render_glob` patterns) — must stay resource-agnostic; disabling drops the haproxyConfig the other libraries plug into |
| `ssl` | on | Terminate TLS, crt-list management, SSL passthrough |
| `ingress` | on | Kubernetes `networking.k8s.io/v1` Ingress |
| `gateway` | on | Gateway API `HTTPRoute` / `GRPCRoute` (requires Gateway CRDs installed) |
| `ingressAnnotationsCompat` | on | Shared scaffold consumed by the Ingress vendor annotation libraries below (level 2.5) |
| `haproxytech` | on | `haproxy.org/*` annotation compatibility ([haproxytech/kubernetes-ingress](https://github.com/haproxytech/kubernetes-ingress)) |
| `haproxy-ingress` | on | `haproxy-ingress.github.io/*` annotation compatibility ([jcmoraisjr/haproxy-ingress](https://haproxy-ingress.github.io/)) |
| `nginx-ingress` | off | `nginx.ingress.kubernetes.io/*` annotation compatibility |
| `spoaHub` | auto | HAProxy-side wiring for the SPOA hub sidecar (auto-loaded when `spoaHub.enabled: true` or any `spoaHub.plugins.<X>.enabled` is truthy) |

Each library contributes entries under `watchedResources`, `templateSnippets`, `maps`, `files`, `sslCertificates`, `haproxyConfig`, and `validationTests` — user-provided values in `controller.config` override library defaults. See [`docs/template-libraries.md`](./docs/template-libraries.md) and [`CLAUDE.md`](../CLAUDE.md) for the library-merging design, extension points, and snippet priority ranges.

## Documentation

| Area | Where to look |
|------|---------------|
| Getting started | [`docs/index.md`](./docs/index.md), [`docs/configuration.md`](./docs/configuration.md) |
| Ingress & Gateway setup | [`docs/ingress-class.md`](./docs/ingress-class.md), [`docs/gateway-class.md`](./docs/gateway-class.md) |
| SSL and annotations | [`docs/ssl-certificates.md`](./docs/ssl-certificates.md), [`docs/annotations.md`](./docs/annotations.md) |
| Running HAProxy | [`docs/haproxy-deployment.md`](./docs/haproxy-deployment.md) |
| Library reference | [`docs/template-libraries.md`](./docs/template-libraries.md) + [`docs/libraries/`](./docs/libraries/) |
| Day-two operations | [`docs/operations/`](./docs/operations/) (HA, monitoring, networking, debugging, troubleshooting) |
| Full values reference | [`docs/reference.md`](./docs/reference.md) |
| Chart development | [`CLAUDE.md`](../CLAUDE.md) |

## Upgrading

```bash
helm upgrade my-controller oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.1.0 -f my-values.yaml
```

CRDs are not upgraded by `helm upgrade` — see [`docs/operations/`](./docs/operations/) if the CRD schema changed between versions.

## Examples

Ready-to-adapt configurations live in the repository's top-level [`examples/`](../../examples/) directory.

## License

Apache-2.0 — see root `LICENSE`.
