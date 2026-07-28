# Troubleshooting

Find your symptom in the quick reference below, then follow its diagnosis and fix.

!!! note "Namespace"
    All `kubectl` commands below assume the default installation namespace `haptic`. Replace `-n haptic` with your namespace if you installed elsewhere.

## Quick symptom reference

| Symptom | Section |
|---------|---------|
| Pods stuck in ImagePullBackOff | [Image Pull Errors](#image-pull-errors) |
| "no kind HAProxyTemplateConfig is registered" | [CRD Not Found](#crd-not-found) |
| No DNS or API connectivity on a kind cluster | [NetworkPolicy Issues in kind](#networkpolicy-issues-in-kind) |
| Pod in CrashLoopBackOff | [Controller Not Starting](#controller-not-starting) |
| Pod stuck Running but not Ready (0/1, 1/2, 2/3) | [Pods stuck not Ready](#pods-stuck-not-ready) |
| Pods running, no reconciliation activity | [Controller Running But Not Processing](#controller-running-but-not-processing) |
| "template rendering failed" in logs | [Invalid Template Syntax](#invalid-template-syntax) |
| "validation failed" / HAProxy errors | [Configuration Validation Failures](#configuration-validation-failures) |
| `kubectl apply` denied by an admission webhook | [Admission webhook denied the apply](#admission-webhook-denied-the-apply) |
| "connection refused" to Dataplane API | [Can't connect to Dataplane API](#cannot-connect-to-dataplane-api) |
| Controller reports success but HAProxy unchanged | [Configuration Not Updating](#configuration-not-updating) |
| 503 errors / no servers in HAProxy stats | [Requests Not Reaching Backend](#requests-not-reaching-backend) |
| 404 for a host or path that should route | [404: no route matched](#404-no-route-matched) |
| SSL handshake failures | [SSL/TLS Issues](#ssltls-issues) |
| High CPU or slow reconciliation | [Slow Reconciliation](#slow-reconciliation) |
| OOMKilled / gradual memory growth | [High Memory Usage](#high-memory-usage) |
| "shm-stats-file-max-objects" / reload failures | [Shared Memory Stats Limit](#shared-memory-stats-limit) |

## Install Issues

Problems that surface while the Helm chart installs, before the controller does any work.

### Image pull errors

If pods are stuck in `ImagePullBackOff`:

```bash
kubectl describe pod -n haptic -l app.kubernetes.io/name=haptic
```

Verify the `haproxyVersion` value matches an available image tag:

```bash
helm get values haptic -n haptic | grep haproxyVersion
```

The controller image tag is derived from both the chart `version` and `haproxyVersion`. If pulling from a private registry, configure `controller.podSpec.imagePullSecrets` (and `haproxy.podSpec.imagePullSecrets` if the chart's HAProxy pods need the same registry).

### CRD not found

If the controller fails with "no kind HAProxyTemplateConfig is registered":

```bash
kubectl get crd haproxytemplateconfigs.haproxy-haptic.org
```

CRDs are installed by the chart. If missing, reinstall the chart at the version you run:

```bash
helm upgrade --install haptic oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.2.0-alpha.1 --namespace haptic
```

### NetworkPolicy Issues in kind

For kind clusters, ensure:

- Calico or Cilium Container Network Interface (CNI) is installed
- DNS access is allowed
- The `controller.networkPolicy.egress.kubernetesApi` CIDRs cover your API server (see [Networking](./operations/networking.md))

Debug NetworkPolicy:

```bash
# Check controller can resolve DNS
kubectl exec -n haptic <controller-pod> -- nslookup kubernetes.default

# Check controller can reach HAProxy pod
kubectl exec -n haptic <controller-pod> -- curl http://<haproxy-pod-ip>:5555/v3/info
```

For NetworkPolicy configuration details, see [Networking](./operations/networking.md).

## Controller Issues

### Controller not starting

**Symptoms**: CrashLoopBackOff, repeated restarts, initialization errors

**Diagnosis**:

```bash
kubectl get pods -n haptic -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller
kubectl logs -n haptic -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller --tail=100
kubectl describe pod -n haptic -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller
```

**Common Causes**:

| Cause | Check | Solution |
|-------|-------|----------|
| Missing HAProxyTemplateConfig | `kubectl get haproxytemplateconfig` — a Helm install creates one per enabled template library plus your own config; the controller waits for **all the** names in the Deployment's `CRD_NAME` before it starts | Reinstall Helm chart |
| Invalid credentials Secret | `kubectl get secret -n haptic haptic-credentials -o jsonpath='{.data}'` (Helm names it `<release>-credentials`) | Recreate secret with correct keys |
| RBAC permissions | `kubectl auth can-i list ingresses --all-namespaces --as=system:serviceaccount:<ns>:<sa>` | Verify ClusterRole/ClusterRoleBinding |

### Pods stuck not ready

**Symptoms**: A pod shows `0/1`, `1/2`, or `2/3` in the `READY` column and never reaches full readiness, but it isn't in `CrashLoopBackOff` or `ImagePullBackOff`.

First branch on the pod's actual state — "not Ready" is a readiness-probe outcome, not a single cause:

```bash
kubectl get pods -n haptic -o wide
kubectl describe pod -n haptic <pod>   # read the Events and per-container State
```

- **A container is in `Waiting` with `CrashLoopBackOff` or `ImagePullBackOff`**: the pod never starts, so it can't be Ready. Follow [Controller not starting](#controller-not-starting) for crashes, or [Image pull errors](#image-pull-errors) for pull failures.
- **Every container is `Running` but the pod stays not Ready**: a readiness probe is failing. Branch by which pod:
    - **Controller pod** (`0/1`): the readiness probe hits `/healthz` on `controller.ports.healthz` (`8080` by default), which returns ready only once the controller has loaded a valid `HAProxyTemplateConfig` and rendered its first config. A render or config-load failure keeps it not Ready — check `kubectl logs -n haptic <pod>` for template or validation errors and follow [Invalid template syntax](#invalid-template-syntax). `/healthz` shares the `/debug/*` listener and is required by the probe (see [Debugging](./operations/debugging.md)).
    - **HAProxy pod** (`1/2` or `2/3`, depending on sidecars): the HAProxy or Dataplane API container is up but not passing its probe. The controller also can't converge config onto an unreachable Dataplane API — follow [Can't connect to Dataplane API](#cannot-connect-to-dataplane-api).

### Controller running but not processing

**Symptoms**: Pods running, no reconciliation activity

**Diagnosis**:

```bash
kubectl logs -n haptic -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller | grep -i "watch\|sync complete"
```

**Common Causes**:

| Cause | Check | Solution |
|-------|-------|----------|
| Informers not syncing | Logs show "timeout waiting for cache sync" | Check API server connectivity, network policies |
| No matching resources | `kubectl get ingresses -A` | Verify resources exist in watched namespaces |
| Ingress class mismatch | `kubectl get ingress <name> -o jsonpath='{.spec.ingressClassName}'` | The Ingress must reference the class the chart created; also check any `watchedResources.*.fieldSelector` namespace filter |
| Leader election (HA) | `kubectl get lease -n haptic` (the Lease is named after the Helm release) | Ensure one pod shows `is_leader=1` |

## Configuration Issues

### Invalid template syntax

**Symptoms**: "template rendering failed" errors

**Diagnosis**:

```bash
kubectl logs -n haptic -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller | grep -i "template\|render"
```

**Solution**:

1. Read the render error in the controller logs — it names the failing template and line — then open that template in your HAProxyTemplateConfig
2. Inspect the last rendered output via the debug server — port-forward first (see [Debugging Guide](./operations/debugging.md)):

    ```bash
    kubectl port-forward -n haptic deployment/haptic-controller 8080:8080
    curl http://localhost:8080/debug/vars/rendered
    ```

3. See [Templating Guide](./templating.md)

!!! note "Live traffic keeps flowing"
    A render or validation failure never drops requests. The leader refuses to deploy the broken output and HAProxy keeps serving the last good config, so the failure surfaces only in the controller logs and the `haptic_reconciliation_errors_total` metric — nothing changes in the data plane until a render succeeds again.

### Configuration validation failures

**Symptoms**: `validation failed`, HAProxy errors

**Common Errors**:

| Error | Cause | Solution |
|-------|-------|----------|
| `backend expects <name>` | Invalid HAProxy syntax | Fix template, test with `haproxy -c -f config.cfg` |
| `unable to load file` | Missing map/cert file | Define in `maps` section, use `pathResolver.GetPath()` |
| `invalid address` | Bad server address | Verify EndpointSlices exist, check service names |

### Validation test failures

**Symptoms**: `haptic-controller validate` fails

**Quick Debugging**:

```bash
# Step 1: Run with verbose output
haptic-controller validate -f config.yaml --verbose

# Step 2: See full rendered content
haptic-controller validate -f config.yaml --dump-rendered

# Step 3: Check template execution
haptic-controller validate -f config.yaml --trace-templates
```

See [Validation Tests](./validation-tests.md#debugging-failed-tests) for detailed debugging.

### Admission webhook denied the apply

**Symptoms**: `kubectl apply` fails with `admission webhook "...validation..." denied the request`, followed by rendered-config errors.

The chart installs a validating admission webhook that renders your templates against the resource being applied and runs `haproxy -c` before the object reaches the cluster. When the render or that check fails, the apply is rejected and the denial message carries the line-numbered `haproxy -c` output pointing at the offending config line:

```text
Error from server: error when creating "ingress.yaml": admission webhook
"ingress.validation.haptic-webhook" denied the request:
rendered config invalid: [ALERT] config: parsing [/etc/haproxy/haproxy.cfg:214]:
'http-request' expects ...
```

Fix the reported line in the template or resource, then re-apply. To reproduce and iterate locally without a cluster, run the same render-and-validate over your `HAProxyTemplateConfig` — it prints the identical line-numbered errors and runs the config's `validationTests`:

```bash
haptic-controller validate -f config.yaml --verbose
```

Two webhooks with different failure behavior sit behind this:

- **Watched resources** (Ingress, Gateway, and every other `watchedResources` entry with `enableValidationWebhook: true`) use `failurePolicy: Fail` — a render-breaking apply is rejected, and if the webhook itself is unreachable the apply is blocked.
- **The `HAProxyTemplateConfig` CRD** uses `failurePolicy: Ignore` — a render-breaking config is still rejected while the webhook is up, but an unreachable webhook lets the apply through so you can push a fix while the controller is degraded. The daemon's load gate then rejects a bad config and keeps serving the last-good one (`haptic_config_rejected_total` increments — see [Monitoring](./operations/monitoring.md#alerting-rules)).

## HAProxy Pod Issues

<a id="cannot-connect-to-dataplane-api"></a>

### Can't connect to Dataplane API

**Symptoms**: `connection refused`, `timeout`, deployment failures

**Diagnosis**:

```bash
HAPROXY_POD=$(kubectl get pods -n haptic -l app.kubernetes.io/component=loadbalancer -o jsonpath='{.items[0].metadata.name}')
kubectl port-forward -n haptic $HAPROXY_POD 5555:5555
# Substitute your actual dataplane password; see spec.credentialsSecretRef
curl -u admin:<password> http://localhost:5555/v3/info
```

**Common Causes**:

| Cause | Check | Solution |
|-------|-------|----------|
| Dataplane not running | `kubectl logs $HAPROXY_POD -c dataplane` | Verify container started, check port conflicts |
| Wrong credentials | Compare secret vs dataplaneapi.yaml | Update the credentials Secret — `credentialsloader` picks it up live; also rotate the matching `dataplaneapi.yaml` on the HAProxy sidecar |
| Network policy | `kubectl get networkpolicy` | Update egress rules for controller → HAProxy |

### Configuration not updating

**Symptoms**: Controller shows success but HAProxy has old config

**Diagnosis**:

```bash
HAPROXY_POD=$(kubectl get pods -n haptic -l app.kubernetes.io/component=loadbalancer -o jsonpath='{.items[0].metadata.name}')
kubectl exec -n haptic $HAPROXY_POD -c haproxy -- ls -lh /etc/haproxy/haproxy.cfg
kubectl logs -n haptic -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller | grep -i "deployment.*succeeded"
```

**Common Causes**:

| Cause | Check | Solution |
|-------|-------|----------|
| Volume mount issue | `kubectl get pod $HAPROXY_POD -o yaml \| grep -A5 volumeMounts` | Ensure both containers share config volume |
| HAProxy not reloading | `kubectl logs $HAPROXY_POD -c dataplane` | Check reload command, master socket access |

### Shared memory stats limit

!!! note "Opt-in feature"
    This only applies when `haproxy.shmStats.enabled: true` is set in Helm values (the default is `false`) and HAProxy is 3.3+ — the shm-stats file is gated by `semver_gte` in the chart templates. If you're on the default config you won't see these errors; this section is for operators who turned shm-stats on for performance.

**Symptoms**: 100% deployment error rate, HAProxy reload failures with `shm-stats-file-max-objects` errors

**Diagnosis**:

```bash
kubectl logs -n haptic -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller | grep "shm-stats"
```

Look for:

```
[ALERT] memory error while setting up shared counters for .../SRV_N server:
Cannot add additional object to '/dev/shm/haproxy-stats' file,
maximum number already reached (50000).
```

**Common Causes**:

| Cause | Check | Solution |
|-------|-------|----------|
| Too many HAProxy objects for the configured limit | Count ingresses/services: `kubectl get ingresses -A --no-headers \| wc -l` | Increase `haproxy.shmStats.maxObjects` in Helm values |
| Cluster grew beyond initial sizing | Compare object count to `maxObjects` value | Recalculate using the formula below |

**Solution**:

Each HAProxy frontend, backend, and server directive counts as one shm-stats object. The file is fixed-size and can't be resized on reload. Increase `haproxy.shmStats.maxObjects` in your Helm values:

```yaml
haproxy:
  shmStats:
    enabled: true
    maxObjects: 100000  # default: 50000
```

**Sizing formula**: `(number of backends + number of servers) × 1.2 safety margin`. Each object uses ~4KiB of shared memory. For example, 100,000 objects require ~390Mi in `/dev/shm`, which counts against the pod's memory limit.

!!! warning
    After changing `maxObjects`, verify that `haproxy.resources.limits.memory` is large enough to accommodate the increased `/dev/shm` usage. The shm volume is memory-backed and counts against the pod's memory limit.

## Routing Issues

### Requests not reaching backend

**Symptoms**: 503 errors, timeouts, no servers in HAProxy stats

**Diagnosis**:

```bash
HAPROXY_POD=$(kubectl get pods -n haptic -l app.kubernetes.io/component=loadbalancer -o jsonpath='{.items[0].metadata.name}')
kubectl exec -n haptic $HAPROXY_POD -c haproxy -- cat /etc/haproxy/haproxy.cfg | grep -A10 "backend"
kubectl get endpointslices -l kubernetes.io/service-name=<service>
```

**Common Causes**:

| Cause | Check | Solution |
|-------|-------|----------|
| No endpoints | `kubectl get endpointslices` | Verify backend pods running and ready |
| Backend not created | Controller logs for backend errors | Review template logic, check Ingress references |
| Routing not matching | Test with `curl -H "Host: ..."` | Verify Host header, check ACLs and map files |

### 404: No route matched

**Symptoms**: HAProxy answers `404 Not Found` (not `503`) for a host or path you expect to route.

A `404` is distinct from a `503`: a `503` means a route matched but its backend has no ready servers ([Requests not reaching backend](#requests-not-reaching-backend)), whereas a `404` means *no route matched at all*. The request falls through to HAProxy's `default_backend`, which returns `404` (a gRPC request gets `grpc-status 12 Unimplemented` instead). Unless you configured a catch-all default backend, every unmatched request lands here.

Check the three things that stop a route from matching:

- **The Ingress was never adopted.** HAPTIC only serves Ingresses whose `ingressClassName` (or the legacy `kubernetes.io/ingress.class` annotation) references the class the chart created. An Ingress with a different class produces no HAProxy route at all.

    ```bash
    kubectl get ingress <name> -o jsonpath='{.spec.ingressClassName}'
    # Compare against the class HAPTIC created:
    kubectl get ingressclass
    ```

    See [Migrating — Existing Ingresses aren't being routed](./migrating.md#troubleshooting).

- **The Host header doesn't match a rule host.** Routing keys on the request's `Host`. Send the exact host the Ingress declares:

    ```bash
    HAPROXY_POD=$(kubectl get pods -n haptic -l app.kubernetes.io/component=loadbalancer -o jsonpath='{.items[0].metadata.name}')
    kubectl port-forward -n haptic $HAPROXY_POD 8080:80 &
    curl -i -H "Host: app.example.com" http://localhost:8080/
    ```

- **The path or `pathType` doesn't match.** An `Exact` path matches only the exact request path; `Prefix` matches path segments. Confirm the request path falls under a declared path, and inspect the generated routing maps:

    ```bash
    kubectl exec -n haptic $HAPROXY_POD -c haproxy -- cat /etc/haproxy/maps/host.map
    ```

### SSL/TLS Issues

**Symptoms**: SSL handshake failures, certificate errors

**Diagnosis**:

```bash
HAPROXY_POD=$(kubectl get pods -n haptic -l app.kubernetes.io/component=loadbalancer -o jsonpath='{.items[0].metadata.name}')
kubectl exec -n haptic $HAPROXY_POD -c haproxy -- ls -lh /etc/haproxy/ssl/

# Port-forward HAProxy's HTTPS port, then probe the handshake.
# Stop the forward with `kill %1` (or Ctrl+C) when done.
kubectl port-forward -n haptic $HAPROXY_POD 443:443 &
openssl s_client -connect localhost:443 -servername your-host.example.com < /dev/null
```

**Common Causes**:

| Cause | Check | Solution |
|-------|-------|----------|
| Certificate not deployed | Check `sslCertificates` section | Define template, watch secret, use `b64decode` |
| Wrong cert path | `grep "bind.*ssl.*crt" haproxy.cfg` | Use `pathResolver.GetPath("cert.pem", "cert")` |

**"Secret not found" errors:**

Check that the Secret exists in the correct namespace:

```bash
kubectl get secret default-ssl-cert -n haptic
```

**HAProxy fails to start with SSL errors:**

Verify the certificate and key are valid:

```bash
# Extract and verify certificate
kubectl get secret default-ssl-cert -n haptic -o jsonpath='{.data.tls\.crt}' | base64 -d | openssl x509 -text -noout

# Verify key
kubectl get secret default-ssl-cert -n haptic -o jsonpath='{.data.tls\.key}' | base64 -d | openssl rsa -check -noout
```

**Certificate not being updated:**

The controller watches the Secret and deploys certificate changes automatically within seconds. If HAProxy keeps serving the old certificate, check the controller logs for render or deployment errors.

By default the chart watches Secrets with an **on-demand** store (`controller.config.watchedResources.secrets.store: on-demand`), so cert bodies aren't kept resident in memory. Override it to `full` if you'd rather hold Secrets in the in-memory store.

For certificate provisioning and rotation (cert-manager, manual Secrets, the chart-generated default), see [SSL Certificates](./ssl-certificates.md).

## Performance Issues

### Slow reconciliation

**Symptoms**: Changes take minutes, high CPU

**Diagnosis**:

```bash
kubectl port-forward -n haptic deployment/haptic-controller 9090:9090
curl http://localhost:9090/metrics | grep reconciliation_duration_seconds
```

**Solutions**:

- Use namespace restrictions in `watchedResources`
- Add label selectors to filter resources
- Use cached store for large resources
- Optimize templates: cache values with `{% var %}`, reduce nested loops

### High memory usage

**Symptoms**: OOMKilled events, gradual memory growth

**Solutions**:

```yaml
# Filter large fields
watchedResourcesIgnoreFields:
  - metadata.managedFields
  - metadata.annotations['kubectl.kubernetes.io/last-applied-configuration']

# Use cached store for secrets (fetches on-demand; TTL is auto-derived
# from driftPreventionInterval, not user-configurable)
watchedResources:
  secrets:
    store: on-demand

# Limit watch scope
watchedResources:
  ingresses:
    namespace: production
    labelSelector: "app=myapp"
```

## Getting help

### Collect diagnostic information

```bash
# Controller version
kubectl get deployment -n haptic haptic-controller -o jsonpath='{.spec.template.spec.containers[0].image}'

# Controller logs
kubectl logs -n haptic -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller --tail=500 > controller-logs.txt

# Configuration — every object, plus the merged result the controller assembles
kubectl get haproxytemplateconfig -n haptic -o yaml > config-objects.yaml
haptic-controller config view --input -n haptic > config-merged.yaml

# HAProxy config (sanitize sensitive data!)
kubectl exec -n haptic $HAPROXY_POD -c haproxy -- cat /etc/haproxy/haproxy.cfg > haproxy.cfg
```

### Enable debug logging

The controller supports multiple log levels via the `LOG_LEVEL` environment variable (case-insensitive):

| Level | Description |
|-------|-------------|
| `ERROR` | Errors only |
| `WARN` (or `WARNING`) | Warnings and errors |
| `INFO` | Important state changes (default) |
| DEBUG | Detailed debugging information |
| TRACE | Very verbose, per-item iteration logs |

```bash
# Enable debug logging
kubectl set env -n haptic deployment/haptic-controller LOG_LEVEL=DEBUG

# Enable trace logging (very verbose)
kubectl set env -n haptic deployment/haptic-controller LOG_LEVEL=TRACE
```

The log level can also be configured via the HAProxyTemplateConfig CRD's `spec.logging.level` field. When set, the CRD value takes precedence over the `LOG_LEVEL` environment variable, and changes take effect without a pod restart:

```yaml
# In values.yaml
controller:
  logLevel: INFO  # Initial LOG_LEVEL env var (used until the CRD is loaded)
  config:
    logging:
      level: DEBUG  # Written to spec.logging.level — overrides env var at runtime
```

!!! note
    TRACE level produces extremely verbose output, including per-resource iteration logs, HTTP fetch retries, and test runner details. Enable it only for short, targeted sessions and set the level back to `INFO` afterwards — TRACE volume drowns everything else.

### Access the debug server

The Helm chart enables the debug server on port `8080` by default (same port as `/healthz`). Port-forward to reach it:

```bash
kubectl port-forward -n haptic deployment/haptic-controller 8080:8080
```

The listener is configured by `controller.ports.healthz` and also serves
`/healthz`, so it's required by the liveness/readiness probes. Restrict access
via NetworkPolicy instead of disabling it. See the [Debugging Guide](./operations/debugging.md)
for the endpoint catalogue and usage.

## See also

- [Getting Started](./getting-started.md)
- [Debugging](./operations/debugging.md)
- [Monitoring](./operations/monitoring.md)
- [CRD Reference](./crd-reference.md)
- [Validation Tests](./validation-tests.md)
- [Templating Guide](./templating.md)
