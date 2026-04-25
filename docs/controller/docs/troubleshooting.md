# Troubleshooting Guide

Common issues and solutions for the HAProxy Template Ingress Controller.

!!! note "Namespace"
    All `kubectl` commands below assume the default installation namespace `haptic`. Replace `-n haptic` with your namespace if you installed elsewhere.

## Quick Symptom Reference

| Symptom | Section |
|---------|---------|
| Pod in CrashLoopBackOff | [Controller Not Starting](#controller-not-starting) |
| Pods running, no reconciliation activity | [Controller Running But Not Processing](#controller-running-but-not-processing) |
| "template rendering failed" in logs | [Invalid Template Syntax](#invalid-template-syntax) |
| "validation failed" / HAProxy errors | [Configuration Validation Failures](#configuration-validation-failures) |
| "connection refused" to Dataplane API | [Cannot Connect to Dataplane API](#cannot-connect-to-dataplane-api) |
| Controller reports success but HAProxy unchanged | [Configuration Not Updating](#configuration-not-updating) |
| 503 errors / no servers in HAProxy stats | [Requests Not Reaching Backend](#requests-not-reaching-backend) |
| SSL handshake failures | [SSL/TLS Issues](#ssltls-issues) |
| High CPU or slow reconciliation | [Slow Reconciliation](#slow-reconciliation) |
| OOMKilled / gradual memory growth | [High Memory Usage](#high-memory-usage) |
| "shm-stats-file-max-objects" / reload failures | [Shared Memory Stats Limit](#shared-memory-stats-limit) |

## Controller Issues

### Controller Not Starting

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
| Missing HAProxyTemplateConfig | `kubectl get haproxytemplateconfig` | Reinstall Helm chart |
| Invalid credentials Secret | `kubectl get secret haproxy-credentials -o jsonpath='{.data}'` | Recreate secret with correct keys |
| RBAC permissions | `kubectl auth can-i list ingresses --all-namespaces --as=system:serviceaccount:<ns>:<sa>` | Verify ClusterRole/ClusterRoleBinding |

### Controller Running But Not Processing

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
| Leader election (HA) | `kubectl get lease -n haptic` (the Lease is named after the Helm release) | Ensure one pod shows `is_leader=1` |

## Configuration Issues

### Invalid Template Syntax

**Symptoms**: "template rendering failed" errors

**Diagnosis**:

```bash
kubectl logs -n haptic -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller | grep -i "template\|render"
```

**Solution**:

1. Check template syntax in HAProxyTemplateConfig
2. Inspect the last rendered output via the debug server — port-forward first (see [Debugging Guide](./operations/debugging.md)):

   ```bash
   kubectl port-forward -n haptic deployment/haptic-controller 8080:8080
   curl http://localhost:8080/debug/vars/rendered
   ```

3. See [Templating Guide](./templating.md)

### Configuration Validation Failures

**Symptoms**: "validation failed", HAProxy errors

**Common Errors**:

| Error | Cause | Solution |
|-------|-------|----------|
| `backend expects <name>` | Invalid HAProxy syntax | Fix template, test with `haproxy -c -f config.cfg` |
| `unable to load file` | Missing map/cert file | Define in `maps` section, use `pathResolver.GetPath()` |
| `invalid address` | Bad server address | Verify EndpointSlices exist, check service names |

### Validation Test Failures

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

## HAProxy Pod Issues

### Cannot Connect to Dataplane API

**Symptoms**: "connection refused", "timeout", deployment failures

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
| Wrong credentials | Compare secret vs dataplaneapi.yaml | Update credentials secret, restart controller |
| Network policy | `kubectl get networkpolicy` | Update egress rules for controller → HAProxy |

### Configuration Not Updating

**Symptoms**: Controller shows success but HAProxy has old config

**Diagnosis**:

```bash
kubectl exec -n haptic $HAPROXY_POD -c haproxy -- ls -lh /etc/haproxy/haproxy.cfg
kubectl logs -n haptic -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller | grep -i "deployment.*succeeded"
```

**Common Causes**:

| Cause | Check | Solution |
|-------|-------|----------|
| Volume mount issue | `kubectl get pod $HAPROXY_POD -o yaml \| grep -A5 volumeMounts` | Ensure both containers share config volume |
| HAProxy not reloading | `kubectl logs $HAPROXY_POD -c dataplane` | Check reload command, master socket access |

### Shared Memory Stats Limit

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

Each HAProxy frontend, backend, and server directive counts as one shm-stats object. The file is fixed-size and cannot be resized on reload. Increase `haproxy.shmStats.maxObjects` in your Helm values:

```yaml
haproxy:
  shmStats:
    enabled: true
    maxObjects: 100000  # default: 50000
```

**Sizing formula**: `(number of backends + number of servers) × 1.2 safety margin`. Each object uses ~4KB of shared memory. For example, 100,000 objects require ~430Mi in `/dev/shm`, which counts against the pod's memory limit.

!!! warning
    After changing `maxObjects`, verify that `haproxy.resources.limits.memory` is large enough to accommodate the increased `/dev/shm` usage. The shm volume is memory-backed and counts against the pod's memory limit.

## Routing Issues

### Requests Not Reaching Backend

**Symptoms**: 503 errors, timeouts, no servers in HAProxy stats

**Diagnosis**:

```bash
kubectl exec -n haptic $HAPROXY_POD -c haproxy -- cat /etc/haproxy/haproxy.cfg | grep -A10 "backend"
kubectl get endpointslices -l kubernetes.io/service-name=<service>
```

**Common Causes**:

| Cause | Check | Solution |
|-------|-------|----------|
| No endpoints | `kubectl get endpointslices` | Verify backend pods running and ready |
| Backend not created | Controller logs for backend errors | Review template logic, check Ingress references |
| Routing not matching | Test with `curl -H "Host: ..."` | Verify Host header, check ACLs and map files |

### SSL/TLS Issues

**Symptoms**: SSL handshake failures, certificate errors

**Diagnosis**:

```bash
kubectl exec -n haptic $HAPROXY_POD -c haproxy -- ls -lh /etc/haproxy/ssl/
openssl s_client -connect localhost:443 -servername your-host.example.com < /dev/null
```

**Common Causes**:

| Cause | Check | Solution |
|-------|-------|----------|
| Certificate not deployed | Check `sslCertificates` section | Define template, watch secret, use `b64decode` |
| Wrong cert path | `grep "bind.*ssl.*crt" haproxy.cfg` | Use `pathResolver.GetPath("cert.pem", "cert")` |

## Performance Issues

### Slow Reconciliation

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

### High Memory Usage

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

## Getting Help

### Collect Diagnostic Information

```bash
# Controller version
kubectl get deployment -n haptic haptic-controller -o jsonpath='{.spec.template.spec.containers[0].image}'

# Controller logs
kubectl logs -n haptic -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller --tail=500 > controller-logs.txt

# Configuration
kubectl get haproxytemplateconfig -n haptic haptic-config -o yaml > config.yaml

# HAProxy config (sanitize sensitive data!)
kubectl exec -n haptic $HAPROXY_POD -c haproxy -- cat /etc/haproxy/haproxy.cfg > haproxy.cfg
```

### Enable Debug Logging

The controller supports multiple log levels via the `LOG_LEVEL` environment variable (case-insensitive):

| Level | Description |
|-------|-------------|
| ERROR | Errors only |
| WARN | Warnings and errors |
| INFO | Important state changes (default) |
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
    TRACE level produces extremely verbose output, including per-resource iteration logs, HTTP fetch retries, and test runner details. Use only when debugging specific issues.

### Access the Debug Server

The Helm chart enables the debug server on port `8080` by default (same port as `/healthz`). Port-forward to reach it:

```bash
kubectl port-forward -n haptic deployment/haptic-controller 8080:8080
```

To disable it in production, set `controller.debugPort: 0`. To move it to a dedicated port, set `controller.debugPort: <port>` (and update the forward accordingly).

**Available endpoints**:

- `/debug/vars` — internal state (config, credentials metadata, rendered output, resources, events, uptime)
- `/debug/vars/<name>` — a single variable; supports `?field={.jsonpath}` for subselection
- `/debug/pprof/` — Go profiling

See the [Debugging Guide](./operations/debugging.md) for the full endpoint catalogue.

## See Also

- [Getting Started](./getting-started.md)
- [CRD Reference](./crd-reference.md)
- [Validation Tests](./validation-tests.md)
- [Templating Guide](./templating.md)
