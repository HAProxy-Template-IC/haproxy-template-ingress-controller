# Troubleshooting the Install

## Overview

Common issues when installing and operating HAPTIC via the Helm chart — missing CRDs, credentials, image pulls, and RBAC.

Once HAPTIC is installed and running, runtime symptoms — routing, 503s, config that won't update, SSL, and reconciliation stalls — live in the [Troubleshooting guide](../troubleshooting.md).

!!! note "Namespace"
    The examples below assume the chart is installed into the `haptic` namespace. Substitute your release namespace if you installed elsewhere.

## Controller Not Starting

Check logs:

```bash
kubectl logs -n haptic -f -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller
```

Common issues:

- **HAProxyTemplateConfig missing**: `kubectl get haproxytemplateconfig -n haptic` — reinstall the Helm chart if absent
- **Credentials Secret missing**: `kubectl get secret -n haptic haptic-credentials` — recreate with the correct keys
- **RBAC permissions incorrect**: `kubectl auth can-i list ingresses --all-namespaces --as=system:serviceaccount:haptic:<serviceaccount>`
- **NetworkPolicy blocking access**: see [Networking](./networking.md)

## Image Pull Errors

If pods are stuck in `ImagePullBackOff`:

```bash
kubectl describe pod -n haptic -l app.kubernetes.io/name=haptic
```

Verify the `haproxyVersion` value matches an available image tag:

```bash
helm get values haptic | grep haproxyVersion
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

## Cannot Connect to HAProxy Pods

1. **Check HAProxy pod labels** match `podSelector`

   ```bash
   kubectl get pods -n haptic --show-labels
   ```

2. **Verify Dataplane API is accessible**

   ```bash
   kubectl port-forward -n haptic <haproxy-pod> 5555:5555
   # Substitute the actual dataplane password from the credentials Secret
   curl -u admin:<password> http://localhost:5555/v3/info
   ```

3. **Check NetworkPolicy**

   ```bash
   kubectl describe networkpolicy -n haptic
   ```

## Dataplane API Authentication Failure

If the controller logs show "401 Unauthorized" or "403 Forbidden" when connecting to HAProxy, decode the credentials from the Secret and confirm they match what the Dataplane API was configured with:

```bash
for key in dataplane_username dataplane_password; do
  echo "$key: $(kubectl get secret haptic-credentials -n haptic -o jsonpath="{.data.$key}" | base64 -d)"
done
```

(`kubectl get secret … -o jsonpath='{.data}'` alone returns the keys map verbatim — the values come out as base64-encoded strings, which is why each key is decoded individually.)

The controller watches the credentials Secret via `pkg/controller/credentialsloader` and picks up updates live — no pod restart is needed. If 401/403 errors persist after the Secret has been corrected, also confirm that the `dataplaneapi.yaml` mounted into the HAProxy pod was rotated to match (the Dataplane API on the HAProxy side reads its credentials from a sidecar config, not from the controller's Secret).

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
