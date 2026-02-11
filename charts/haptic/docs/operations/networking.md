# Networking

## Overview

The controller requires network access to the Kubernetes API, HAProxy pods, and DNS. This page covers NetworkPolicy configuration for securing controller network access.

For all NetworkPolicy-related Helm values, see the [Configuration Reference](../reference.md).

## Requirements

The controller requires network access to:

1. Kubernetes API Server (watch resources)
2. HAProxy Dataplane API pods in ANY namespace
3. DNS (CoreDNS/kube-dns)

## Default Configuration

By default, the NetworkPolicy allows:

- DNS: kube-system namespace
- Kubernetes API: 0.0.0.0/0 (adjust for production)
- HAProxy pods: All namespaces with matching labels

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
```

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
