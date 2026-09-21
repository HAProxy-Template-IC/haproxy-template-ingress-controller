# Networking

## Overview

This page covers the chart's `NetworkPolicy` configuration: what the default policies allow, how to harden them, and how to replace them with your own. For exposing HAProxy traffic to the outside world (Services, ports, LoadBalancer setup), see [HAProxy Deployment](../haproxy-deployment.md).

The controller requires network access to the Kubernetes API, HAProxy pods, and DNS. For all NetworkPolicy-related Helm values, see the [Configuration Reference](../reference.md); for the security rationale behind these policies, see [Security — Network Exposure](./security.md#network-exposure).

## Default configuration

By default, the NetworkPolicy allows egress to four targets:

- **DNS** (kube-system namespace): lets the controller resolve hostnames, for example `http.Fetch()` targets in templates.
- **Kubernetes API** (`0.0.0.0/0` and `::/0`, adjust for production): Required for watching Ingress, Gateway, Secret, and other configured resources. The default ships both an IPv4 and an IPv6 catch-all so the controller can reach an apiserver dialed over either family.
- **HAProxy pods** (release namespace, label-matched): The controller reaches the agent and stats ports on every pod whose labels match `controller.networkPolicy.egress.haproxyPods.podSelector`. With the default empty `controller.networkPolicy.egress.haproxyPods.namespaceSelector: {}`, no namespace selector is emitted, which in NetworkPolicy semantics restricts the rule to the policy's own namespace — set a non-empty selector to reach HAProxy pods in other namespaces.
- **All in-cluster pods**: `controller.networkPolicy.egress.additionalRules` ships a default rule allowing egress to every pod in every namespace on any port, so template helpers like `http.Fetch()` reach cluster services out of the box.

Helm replaces list values wholesale rather than merging them. When you override `kubernetesApi`, you restate the entire list — every `cidr` entry and its `ports` array — because your value fully replaces the default.

When an auxiliary edge tier is enabled, the chart adds a separate default-on
policy for that tier:

- `cache.varnish.networkPolicy.enabled` admits port 6081 only from
  the same release's HAProxy pods. Varnish egress is limited to cluster DNS and
  the same HAProxy pods' dedicated backend-fetch port (`cache.varnish.loopbackPort`, default `8090`) for cache-miss origin requests — never the client-facing HTTP/HTTPS ports.
  The HAProxy policy contains the reciprocal ingress rule, including when
  `haproxy.networkPolicy.allowExternal` is false.
- The HAProxy policy admits the metrics ports Prometheus needs without extra
  configuration, including when `haproxy.networkPolicy.allowExternal` is false:
  HAProxy's own stats port, and — while the Vector sidecar is enabled — its
  exporter ports (`vector.metricsPort`, plus `vector.sizeMetricsPort` when a
  [request-metrics](./monitoring.md#request-metrics) size family is on). Prometheus
  scrapes HAProxy and the agent directly; Vector re-exports SPOA hub metrics.
- `rateLimit.shared.managedStore.networkPolicy.enabled` admits Valkey and Sentinel
  only from the same release's HAProxy/SPOA pods and from the managed store pods
  themselves. Store egress is limited to DNS and store-internal replication,
  quorum, and failover traffic.

These policies select release-scoped labels, so two HAPTIC releases in one
namespace don't gain access to each other's cache or limiter tiers. As with all
Kubernetes NetworkPolicies, enforcement requires a compatible Container Network
Interface (CNI) plugin.

## Production hardening

Restrict API-server egress to your cluster's actual API endpoints. Ask your
cluster administrator which addresses and ports the network plugin sees: policy
can apply before or after Service address translation. Allowing only the Service
CIDR can block the controller from watching resources.

Inspect the Service and its backing endpoints:

```bash
kubectl get service kubernetes --namespace default -o wide
kubectl get endpointslice --namespace default \
  -l kubernetes.io/service-name=kubernetes -o wide
```

For example, this values file allows an API endpoint at `192.0.2.10:6443` and
removes the default rule allowing connections to all cluster pods. Replace the
example address with the endpoints your cluster administrator identifies before
applying it:

```yaml
controller:
  networkPolicy:
    egress:
      additionalRules: []
      kubernetesApi:
        - cidr: 192.0.2.10/32
          ports:
            - port: 6443
              protocol: TCP
```

Include every required endpoint, including IPv6 addresses on a dual-stack
cluster. Helm replaces the entire `kubernetesApi` list when you set it. If your
templates use `http.Fetch()`, add rules for those destinations under
`additionalRules` before removing the default allow-all rule.

<a id="kind-cluster-specifics"></a>

The defaults allow API ports `443` and `6443` over IPv4 and IPv6, including local
Kind clusters. You don't need a separate Kind-specific values file.

## Replacing the shipped policies

To supply your own policies, disable the corresponding chart policy:

| Component | Helm value |
|-----------|------------|
| Controller | `controller.networkPolicy.enabled: false` |
| HAProxy | `haproxy.networkPolicy.enabled: false` |
| Varnish | `cache.varnish.networkPolicy.enabled: false` |
| Managed Valkey/Sentinel | `rateLimit.shared.managedStore.networkPolicy.enabled: false` |

The controller policy selects only controller pods using release and component
labels. Preserve DNS, API-server, and agent egress, plus health-probe, webhook,
and monitoring ingress in a replacement. API-server egress may need the API
server's endpoint IP and port rather than its Service IP, depending on where
your network plugin enforces policy.

Start from the rendered policy for your release so configured ports and
selectors stay consistent:

```bash
helm get manifest haptic --namespace haptic > haptic-manifest.yaml
```

Edit the `NetworkPolicy` documents you intend to manage separately, then disable
only those chart policies in your Helm values. See
[Kubernetes NetworkPolicy](https://kubernetes.io/docs/concepts/services-networking/network-policies/)
for policy semantics.

## Allowing Prometheus scraping

If using NetworkPolicy with [monitoring](./monitoring.md), allow Prometheus to scrape metrics:

```yaml
controller:
  networkPolicy:
    enabled: true
    ingress:
      monitoring:
        enabled: true
        podSelector:
          matchLabels:
            app: prometheus
        namespaceSelector:
          matchLabels:
            kubernetes.io/metadata.name: monitoring
```
