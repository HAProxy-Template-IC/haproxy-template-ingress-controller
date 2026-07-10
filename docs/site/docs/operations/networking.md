# Networking

## Overview

The controller requires network access to the Kubernetes API, HAProxy pods, and DNS.

For all NetworkPolicy-related Helm values, see the [Configuration Reference](../reference.md).

## Requirements

The controller requires network access to:

1. Kubernetes API Server (watch resources)
2. HAProxy Dataplane API on every HAProxy pod the `podSelector` matches — across **any** namespace by default (`networkPolicy.egress.haproxyPods.namespaceSelector` defaults to `{}`); narrow it explicitly when you know which namespaces host HAProxy
3. DNS (CoreDNS/kube-dns)

## Default Configuration

By default, the NetworkPolicy allows:

- **DNS** (kube-system namespace): Required for name resolution
- **Kubernetes API** (0.0.0.0/0, adjust for production): Required for watching Ingress, Gateway, Secret, and other configured resources
- **HAProxy pods** (any namespace, label-matched): The default `networkPolicy.egress.haproxyPods.namespaceSelector` is `{}` — empty selector means *all* namespaces — so the controller can reach the Dataplane API + stats ports on every pod whose labels match `networkPolicy.egress.haproxyPods.podSelector`. Restrict to specific namespaces by setting that field explicitly

## Production Hardening

For production, restrict Kubernetes API access:

```yaml
networkPolicy:
  egress:
    kubernetesApi:
      - cidr: 10.96.0.0/12  # Your cluster's service CIDR
        ports:
          - port: 443
            protocol: TCP
```

## kind Cluster Specifics

For kind clusters with network policy enforcement:

```yaml
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
```

Helm replaces list values wholesale rather than merging them — if you override `kubernetesApi`, you must restate the `ports` array (the chart default exposes both `443` and `6443` because either may host the API server depending on the kind config).

## Allowing Prometheus Scraping

If using NetworkPolicy with [monitoring](./monitoring.md), allow Prometheus to scrape metrics:

```yaml
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
