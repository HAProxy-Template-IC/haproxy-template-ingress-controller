# Troubleshooting the Install

## Overview

Install-time issues with the Helm chart — image pulls, CRD installation, and kind NetworkPolicy setup.

For anything after the controller pod starts — a crashing or idle controller, credentials and RBAC checks, Dataplane API connectivity, routing, 503s, config that won't update, SSL, and reconciliation stalls — see the [Troubleshooting guide](../troubleshooting.md).

!!! note "Namespace"
    The examples below assume the chart is installed into the `haptic` namespace. Substitute your release namespace if you installed elsewhere.

## Image Pull Errors

If pods are stuck in `ImagePullBackOff`:

```bash
kubectl describe pod -n haptic -l app.kubernetes.io/name=haptic
```

Verify the `haproxyVersion` value matches an available image tag:

```bash
helm get values haptic -n haptic | grep haproxyVersion
```

The controller image tag is derived from both the chart `version` and `haproxyVersion`. If pulling from a private registry, configure `controller.podSpec.imagePullSecrets` (and `haproxy.podSpec.imagePullSecrets` if the chart's HAProxy pods need the same registry).

## CRD Not Found

If the controller fails with "no kind HAProxyTemplateConfig is registered":

```bash
kubectl get crd haproxytemplateconfigs.haproxy-haptic.org
```

CRDs are installed by the chart. If missing, reinstall:

```bash
helm upgrade --install haptic oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version <version> --namespace haptic
```

## NetworkPolicy Issues in kind

For kind clusters, ensure:

- Calico or Cilium CNI is installed
- DNS access is allowed
- Kubernetes API CIDR is correct

Debug NetworkPolicy:

```bash
# Check controller can resolve DNS
kubectl exec -n haptic <controller-pod> -- nslookup kubernetes.default

# Check controller can reach HAProxy pod
kubectl exec -n haptic <controller-pod> -- curl http://<haproxy-pod-ip>:5555/v3/info
```

For NetworkPolicy configuration details, see [Networking](./networking.md).
