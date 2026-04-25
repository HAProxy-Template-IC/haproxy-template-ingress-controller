# Troubleshooting

## Overview

This page covers common issues when deploying and operating the HAPTIC Helm chart.

For controller behavior troubleshooting, see the [controller troubleshooting guide](https://haproxy-haptic.org/controller/latest/troubleshooting/).

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

The controller image tag is derived from both the chart `version` and `haproxyVersion`. If pulling from a private registry, configure `imagePullSecrets`.

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

## Ingress Not Processed

If creating an Ingress produces no HAProxy configuration change:

1. **Verify the IngressClass**: the Ingress must reference the class created by the chart

   ```bash
   kubectl get ingressclass
   kubectl get ingress <name> -o jsonpath='{.spec.ingressClassName}'
   ```

2. **Check namespace filtering**: if `controller.config.watchedResources.ingresses.namespace` is set, the Ingress must be in that namespace

3. **Check controller logs** for watch events:

   ```bash
   kubectl logs -n haptic -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller | grep -i ingress
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

After updating the Secret, restart the controller:

```bash
kubectl rollout restart -n haptic deployment haptic-controller
```

## HAProxy Returning 503

A 503 usually means HAProxy has no healthy servers for the backend:

1. **Check that backend pods are running and ready** (in the application's namespace, not necessarily `haptic`)

   ```bash
   kubectl get pods -l app=<your-app>
   kubectl get endpointslices -l kubernetes.io/service-name=<service-name>
   ```

2. **Verify servers appear in HAProxy config**

   ```bash
   kubectl exec -n haptic <haproxy-pod> -c haproxy -- cat /etc/haproxy/haproxy.cfg | grep -A5 "backend"
   ```

3. **Check HAProxy stats** for server state (UP/DOWN):

   ```bash
   kubectl port-forward -n haptic svc/haptic-haproxy 8404:8404
   curl http://localhost:8404/stats
   ```

## Configuration Not Updating After Ingress Change

If controller logs show successful deployment but HAProxy still serves the old config:

1. **Confirm the config file was written**

   ```bash
   kubectl exec -n haptic <haproxy-pod> -c haproxy -- ls -lh /etc/haproxy/haproxy.cfg
   ```

2. **Check that both containers share the config volume** — HAProxy and Dataplane API must mount the same volume

3. **Check Dataplane API reload logs**

   ```bash
   kubectl logs -n haptic <haproxy-pod> -c dataplane | tail -20
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
