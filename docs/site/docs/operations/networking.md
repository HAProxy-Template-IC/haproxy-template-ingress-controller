# Networking

## Overview

This page covers the chart's `NetworkPolicy` configuration: what the default policies allow, how to harden them, and how to replace them with your own. For exposing HAProxy traffic to the outside world (Services, ports, LoadBalancer setup), see [HAProxy Deployment](../haproxy-deployment.md).

The controller requires network access to the Kubernetes API, HAProxy pods, and DNS. For all NetworkPolicy-related Helm values, see the [Configuration Reference](../reference.md); for the security rationale behind these policies, see [Security — Network Exposure](./security.md#network-exposure).

## Default configuration

By default, the NetworkPolicy allows egress to four targets:

- **DNS** (kube-system namespace): lets the controller resolve hostnames, for example `http.Fetch()` targets in templates.
- **Kubernetes API** (`0.0.0.0/0` and `::/0`, adjust for production): Required for watching Ingress, Gateway, Secret, and other configured resources. The default ships both an IPv4 and an IPv6 catch-all so the controller can reach an apiserver dialed over either family.
- **HAProxy pods** (release namespace, label-matched): The controller reaches the Dataplane API and stats ports on every pod whose labels match `controller.networkPolicy.egress.haproxyPods.podSelector`. With the default empty `controller.networkPolicy.egress.haproxyPods.namespaceSelector: {}`, no namespace selector is emitted, which in NetworkPolicy semantics restricts the rule to the policy's own namespace — set a non-empty selector to reach HAProxy pods in other namespaces.
- **All in-cluster pods**: `controller.networkPolicy.egress.additionalRules` ships a default rule allowing egress to every pod in every namespace on any port, so template helpers like `http.Fetch()` reach cluster services out of the box.

Helm replaces list values wholesale rather than merging them. When you override `kubernetesApi`, you restate the entire list — every `cidr` entry and its `ports` array — because your value fully replaces the default. The examples below are complete on purpose.

When an auxiliary edge tier is enabled, the chart adds a separate default-on
policy for that tier:

- `cache.varnish.networkPolicy.enabled` admits port 6081 only from
  the same release's HAProxy pods. Varnish egress is limited to cluster DNS and
  the same HAProxy pods' HTTP container port for cache-miss loopback requests.
  The HAProxy policy contains the reciprocal ingress rule, including when
  `haproxy.networkPolicy.allowExternal` is false.
- The HAProxy policy admits the metrics ports Prometheus needs without extra
  configuration, including when `haproxy.networkPolicy.allowExternal` is false:
  HAProxy's own stats port, and — while the Vector sidecar is enabled — its
  exporter ports (`vector.metricsPort`, plus `vector.sizeMetricsPort` when a
  [request-metrics](./monitoring.md#request-metrics) size family is on). With the
  sidecar on, HAProxy's own `/metrics` answers over loopback only, so allowing the
  stats port alone would leave Prometheus nothing to reach.
- `rateLimit.shared.managedStore.networkPolicy.enabled` admits Valkey and Sentinel
  only from the same release's HAProxy/SPOA pods and from the managed store pods
  themselves. Store egress is limited to DNS and store-internal replication,
  quorum, and failover traffic.

These policies select release-scoped labels, so two HAPTIC releases in one
namespace don't gain access to each other's cache or limiter tiers. As with all
Kubernetes NetworkPolicies, enforcement requires a compatible Container Network
Interface (CNI) plugin.

## Production hardening

For production, clear the default allow-all egress rule (only needed when templates call `http.Fetch()` against in-cluster services) and restrict Kubernetes API access to the CIDRs your apiserver actually uses:

```yaml
controller:
  networkPolicy:
    egress:
      additionalRules: []  # drop the default all-pods rule
      kubernetesApi:
        - cidr: 10.96.0.0/12  # Your cluster's service CIDR
          ports:
            - port: 443
              protocol: TCP
```

On an IPv6 or dual-stack cluster, add the matching IPv6 CIDR — the IPv4 entry alone won't reach an apiserver dialed over IPv6:

```yaml
controller:
  networkPolicy:
    egress:
      kubernetesApi:
        - cidr: 10.96.0.0/12  # Your cluster's IPv4 service CIDR
          ports:
            - port: 443
              protocol: TCP
        - cidr: fd00:10:96::/112  # Your cluster's IPv6 service CIDR
          ports:
            - port: 443
              protocol: TCP
```

## `kind` cluster specifics

For kind clusters with network policy enforcement, keep the broad CIDRs and both ports. The chart default exposes `443` and `6443` because either may host the API server depending on the kind config, and it restates both the IPv4 and IPv6 catch-alls so the wholesale replacement (see [Default configuration](#default-configuration)) doesn't drop IPv6:

```yaml
controller:
  networkPolicy:
    enabled: true
    egress:
      allowDNS: true
      kubernetesApi:
        - cidr: 0.0.0.0/0  # kind requires broader access
          ports:
            - port: 443
              protocol: TCP
            - port: 6443
              protocol: TCP
        - cidr: "::/0"
          ports:
            - port: 443
              protocol: TCP
            - port: 6443
              protocol: TCP
```

## Replacing the shipped policies

Set `controller.networkPolicy.enabled: false` (controller), `haproxy.networkPolicy.enabled: false` (HAProxy), `cache.varnish.networkPolicy.enabled: false` (Varnish), or `rateLimit.shared.managedStore.networkPolicy.enabled: false` (managed Valkey/Sentinel) only for the policies you replace yourself. The example below is a narrowed, controller-only variant — the shipped policy's selector matches **every** release pod (name + instance labels, no component discriminator) and therefore also carries the Dataplane port 5555 ingress allowance for the HAProxy pods; if you replace it, cover the HAProxy pods separately:

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
            name: monitoring
```
