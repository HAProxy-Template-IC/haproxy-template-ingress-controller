# Troubleshooting

## Overview

This page covers common issues when deploying and operating the HAPTIC Helm chart.

For controller behavior troubleshooting, see the [controller debugging guide](https://haproxy-haptic.org/controller/latest/operations/debugging/).

## Controller Not Starting

Check logs:

```bash
kubectl logs -f -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller
```

Common issues:

- HAProxyTemplateConfig CRD or Secret missing
- RBAC permissions incorrect
- NetworkPolicy blocking access

## Cannot Connect to HAProxy Pods

1. **Check HAProxy pod labels** match `pod_selector`

   ```bash
   kubectl get pods --show-labels
   ```

2. **Verify Dataplane API is accessible**

   ```bash
   kubectl port-forward <haproxy-pod> 5555:5555
   curl http://localhost:5555/v3/info
   ```

3. **Check NetworkPolicy**

   ```bash
   kubectl describe networkpolicy
   ```

## NetworkPolicy Issues in kind

For kind clusters, ensure:

- Calico or Cilium CNI is installed
- DNS access is allowed
- Kubernetes API CIDR is correct

Debug NetworkPolicy:

```bash
# Check controller can resolve DNS
kubectl exec <controller-pod> -- nslookup kubernetes.default

# Check controller can reach HAProxy pod
kubectl exec <controller-pod> -- curl http://<haproxy-pod-ip>:5555/v3/info
```

For NetworkPolicy configuration details, see [Networking](./networking.md).
