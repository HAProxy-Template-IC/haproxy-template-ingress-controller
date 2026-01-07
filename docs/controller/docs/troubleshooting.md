# Troubleshooting Guide

Common issues and solutions for the HAProxy Template Ingress Controller.

## Controller Issues

### Controller Not Starting

**Symptoms**: CrashLoopBackOff, repeated restarts, initialization errors

**Diagnosis**:

```bash
kubectl get pods -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller
kubectl logs -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller --tail=100
kubectl describe pod -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller
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
kubectl logs -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller | grep -i "watch\|sync complete"
```

**Common Causes**:

| Cause | Check | Solution |
|-------|-------|----------|
| Informers not syncing | Logs show "timeout waiting for cache sync" | Check API server connectivity, network policies |
| No matching resources | `kubectl get ingresses -A` | Verify resources exist in watched namespaces |
| Leader election (HA) | `kubectl get lease haptic-leader -o yaml` | Ensure one pod shows `is_leader=1` |

## Configuration Issues

### Invalid Template Syntax

**Symptoms**: "template rendering failed" errors

**Diagnosis**:

```bash
kubectl logs -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller | grep -i "template\|render"
```

**Solution**:

1. Check template syntax in HAProxyTemplateConfig
2. Use debug server: `curl http://localhost:6060/debug/vars/rendered`
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

**Symptoms**: `controller validate` fails

**Quick Debugging**:

```bash
# Step 1: Run with verbose output
controller validate -f config.yaml --verbose

# Step 2: See full rendered content
controller validate -f config.yaml --dump-rendered

# Step 3: Check template execution
controller validate -f config.yaml --trace-templates
```

See [Validation Tests](./validation-tests.md#debugging-failed-tests) for detailed debugging.

## HAProxy Pod Issues

### Cannot Connect to Dataplane API

**Symptoms**: "connection refused", "timeout", deployment failures

**Diagnosis**:

```bash
HAPROXY_POD=$(kubectl get pods -l app.kubernetes.io/component=loadbalancer -o jsonpath='{.items[0].metadata.name}')
kubectl port-forward $HAPROXY_POD 5555:5555
curl -u admin:password http://localhost:5555/v2/info
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
kubectl exec $HAPROXY_POD -c haproxy -- ls -lh /etc/haproxy/haproxy.cfg
kubectl logs -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller | grep -i "deployment.*succeeded"
```

**Common Causes**:

| Cause | Check | Solution |
|-------|-------|----------|
| Volume mount issue | `kubectl get pod $HAPROXY_POD -o yaml \| grep -A5 volumeMounts` | Ensure both containers share config volume |
| HAProxy not reloading | `kubectl logs $HAPROXY_POD -c dataplane` | Check reload command, master socket access |

## Routing Issues

### Requests Not Reaching Backend

**Symptoms**: 503 errors, timeouts, no servers in HAProxy stats

**Diagnosis**:

```bash
kubectl exec $HAPROXY_POD -c haproxy -- cat /etc/haproxy/haproxy.cfg | grep -A10 "backend"
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
kubectl exec $HAPROXY_POD -c haproxy -- ls -lh /etc/haproxy/ssl/
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
kubectl port-forward deployment/haptic-controller 9090:9090
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

# Use cached store for secrets
watchedResources:
  secrets:
    store: on-demand
    cacheTTL: 2m

# Limit watch scope
watchedResources:
  ingresses:
    namespace: production
    labelSelector:
      app: myapp
```

## Getting Help

### Collect Diagnostic Information

```bash
# Controller version
kubectl get deployment haptic-controller -o jsonpath='{.spec.template.spec.containers[0].image}'

# Controller logs
kubectl logs -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller --tail=500 > controller-logs.txt

# Configuration
kubectl get haproxytemplateconfig haptic-config -o yaml > config.yaml

# HAProxy config (sanitize sensitive data!)
kubectl exec $HAPROXY_POD -c haproxy -- cat /etc/haproxy/haproxy.cfg > haproxy.cfg
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
kubectl set env deployment/haptic-controller LOG_LEVEL=DEBUG

# Enable trace logging (very verbose)
kubectl set env deployment/haptic-controller LOG_LEVEL=TRACE
```

The log level can also be configured via the ConfigMap's `logging.level` field. When set, the ConfigMap value takes precedence over the `LOG_LEVEL` environment variable:

```yaml
# In values.yaml
controller:
  logLevel: INFO  # Initial level (LOG_LEVEL env var)
  config:
    logging:
      level: DEBUG  # Overrides env var at runtime
```

!!! note
    TRACE level produces extremely verbose output, including per-resource iteration logs, HTTP fetch retries, and test runner details. Use only when debugging specific issues.

### Enable Debug Server

```bash
helm upgrade haproxy-ic ./charts/haptic --reuse-values --set controller.debugPort=6060
kubectl port-forward deployment/haptic-controller 6060:6060
```

**Available endpoints**:

- `/debug/vars` - Internal state
- `/debug/vars/rendered` - Last rendered config
- `/debug/pprof/` - Go profiling

## See Also

- [Getting Started](./getting-started.md)
- [CRD Reference](./crd-reference.md)
- [Validation Tests](./validation-tests.md)
- [Templating Guide](./templating.md)
