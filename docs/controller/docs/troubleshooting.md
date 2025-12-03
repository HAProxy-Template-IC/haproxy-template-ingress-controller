# Troubleshooting Guide

This guide covers common issues and their solutions when running the HAProxy Template Ingress Controller.

## Controller Issues

### Controller Not Starting

**Symptoms:**
- Controller pods in CrashLoopBackOff
- Pod repeatedly restarting
- Logs show initialization errors

**Diagnosis:**

```bash
# Check pod status
kubectl get pods -l app.kubernetes.io/name=haproxy-template-ic

# View recent logs
kubectl logs -l app.kubernetes.io/name=haproxy-template-ic --tail=100

# Describe pod for events
kubectl describe pod -l app.kubernetes.io/name=haproxy-template-ic
```

**Common Causes:**

**1. Missing HAProxyTemplateConfig CRD resource:**
```bash
# Check if config exists
kubectl get haproxytemplateconfig

# Check config content
kubectl get haproxytemplateconfig haproxy-template-ic-config -o yaml
```

**Solution:**
```bash
# Reinstall Helm chart or manually create config
helm upgrade haproxy-ic ./charts/haproxy-template-ic --reuse-values
```

**2. Missing or invalid credentials Secret:**
```bash
# Check if secret exists
kubectl get secret haproxy-credentials

# Verify secret has required keys
kubectl get secret haproxy-credentials -o jsonpath='{.data}' | jq 'keys'
# Should include: dataplane_username, dataplane_password
```

**Solution:**
```bash
# Recreate secret with correct credentials
kubectl create secret generic haproxy-credentials \
  --from-literal=dataplane_username=admin \
  --from-literal=dataplane_password=your-password \
  --dry-run=client -o yaml | kubectl apply -f -
```

**3. RBAC permissions missing:**
```bash
# Check if controller can list resources
kubectl auth can-i list haproxytemplateconfigs \
  --as=system:serviceaccount:default:haproxy-template-ic

kubectl auth can-i list ingresses --all-namespaces \
  --as=system:serviceaccount:default:haproxy-template-ic
```

**Solution:**
```bash
# Verify ClusterRole and ClusterRoleBinding exist
kubectl get clusterrole haproxy-template-ic
kubectl get clusterrolebinding haproxy-template-ic

# Reinstall RBAC if missing
helm upgrade haproxy-ic ./charts/haproxy-template-ic --reuse-values
```

### Controller Running But Not Processing Resources

**Symptoms:**
- Controller pods running (not restarting)
- No reconciliation happening
- Logs show controller started but no activity

**Diagnosis:**

```bash
# Check controller logs for "watching" messages
kubectl logs -l app.kubernetes.io/name=haproxy-template-ic | grep -i "watch"

# Check if initial sync completed
kubectl logs -l app.kubernetes.io/name=haproxy-template-ic | grep -i "sync complete"
```

**Common Causes:**

**1. Informers not syncing:**

Check logs for errors like:
```
failed to sync cache for ingresses
timeout waiting for cache sync
```

**Solution:**
- Verify API server connectivity
- Check network policies aren't blocking controller → API server
- Increase timeout if cluster is slow

**2. No watched resources match configuration:**

```bash
# Check what resources exist
kubectl get ingresses -A
kubectl get services -A

# Compare with watched resources configuration
kubectl get haproxytemplateconfig haproxy-template-ic-config \
  -o jsonpath='{.spec.watchedResources}'
```

**Solution:**
- Ensure resources exist in watched namespaces
- Verify label selectors match actual resource labels
- Check namespace configuration isn't too restrictive

**3. Leader election preventing action (HA mode):**

```bash
# Check which pod is leader
kubectl get lease haproxy-template-ic-leader -o yaml

# Check leader election metrics
kubectl port-forward deployment/haproxy-template-ic 9090:9090
curl http://localhost:9090/metrics | grep leader_election_is_leader
```

**Solution:**
- Verify exactly one pod shows `is_leader=1`
- If no leader, check logs for election failures
- See [High Availability Troubleshooting](./operations/high-availability.md#troubleshooting)

## Configuration Issues

### Invalid Template Syntax

**Symptoms:**
- Error logs mentioning "template rendering failed"
- Reconciliation errors
- Configuration not deploying

**Diagnosis:**

```bash
# Check logs for template errors
kubectl logs -l app.kubernetes.io/name=haproxy-template-ic | grep -i "template\|render"
```

**Example error:**
```
template rendering failed: function "unknown_function" not defined
```

**Solution:**
1. Review template syntax in HAProxyTemplateConfig
2. Check for typos in filter names or function calls
3. Use debug server to see rendered output:
   ```bash
   kubectl port-forward deployment/haproxy-template-ic 6060:6060
   curl http://localhost:6060/debug/vars/rendered
   ```
4. See [Templating Guide](./templating.md) for correct syntax

### Configuration Validation Failures

**Symptoms:**
- Error logs showing "validation failed"
- HAProxy binary errors in logs
- Config not applying to HAProxy pods

**Diagnosis:**

```bash
# Check for validation errors in logs
kubectl logs -l app.kubernetes.io/name=haproxy-template-ic | grep -i "validation"
```

**Common validation errors:**

**1. Invalid HAProxy syntax:**
```
line 42, 'backend' expects <name> as an argument
```

**Solution:**
- Fix syntax in haproxyConfig template
- Refer to [HAProxy Documentation](https://docs.haproxy.org/2.9/configuration.html)
- Test configuration locally with `haproxy -c -f config.cfg`

**2. Missing map files or certificates:**
```
unable to load file '/etc/haproxy/maps/host.map'
```

**Solution:**
- Ensure map files are defined in `maps` section
- Use `pathResolver.GetPath()` method correctly: `{{ pathResolver.GetPath("host.map", "map") }}`
- Check paths match dataplane configuration

**3. Invalid server addresses:**
```
'server' : invalid address: 'invalid-hostname'
```

**Solution:**
- Verify EndpointSlice resources exist
- Check service names are correct
- Ensure DNS resolution works for service names

### Validation Test Failures

**Symptoms:**
- `controller validate` command fails
- Webhook rejecting HAProxyTemplateConfig updates
- Assertion failures in validation tests
- Template rendering errors in tests

**Diagnosis:**

```bash
# Run validation tests with default output
controller validate -f config.yaml
```

**Common Causes:**

**1. Pattern not found in rendered output:**

```
✗ Backend pattern test
  Error: pattern "backend api-.*" not found in haproxy.cfg (target size: 1234 bytes).
         Hint: Use --verbose to see content preview
```

**Solution:**
```bash
# Step 1: See what was actually rendered
controller validate -f config.yaml --verbose

# Example output:
# ✗ Backend pattern test
#   Error: pattern "backend api-.*" not found in haproxy.cfg (target size: 1234 bytes)
#   Target: haproxy.cfg (1234 bytes)
#   Content preview:
#     global
#       daemon
#     defaults
#       mode http

# Step 2: If preview isn't enough, see full content
controller validate -f config.yaml --dump-rendered

# Step 3: Check which templates executed
controller validate -f config.yaml --trace-templates

# Example trace:
# Rendering: haproxy.cfg
# Completed: haproxy.cfg (0.007ms)
# Rendering: backends.cfg
# Completed: backends.cfg (0.005ms)
```

**2. Empty map files or missing content:**

```
✗ Map contains routing entry
  Error: pattern "api.example.com" not found in map:host-routing.map (target size: 0 bytes)
```

**Solution:**
```bash
# Check what was rendered in the map
controller validate -f config.yaml --dump-rendered

# Look for the map file section:
# ### Map Files
# #### map:host-routing.map
# (empty)

# Common causes of empty maps:
# - Loop condition never true (no resources match)
# - Missing `| default([])` on array variables
# - Incorrect template logic
```

**3. Template execution errors:**

```
✗ Basic rendering
  Error: Service 'nonexistent' not found in namespace 'default'
```

**Solution:**
```bash
# Check test fixtures - ensure required resources are defined
# In config.yaml:
validationTests:
  - name: "basic rendering"
    fixtures:
      services:
        - metadata:
            name: nonexistent
            namespace: default
          spec:
            clusterIP: 10.0.0.1
```

**4. Slow templates affecting validation:**

```bash
# Identify slow templates
controller validate -f config.yaml --trace-templates

# Example output showing slow template:
# Rendering: haproxy.cfg (0.005ms)
# Rendering: complex-backends.cfg (45.123ms)  ← Needs optimization
```

**Solution:**
- Simplify loops in slow templates
- Reduce nested includes
- Cache repeated computations with `{% set %}`
- See [Templating Guide](./templating.md#tips--tricks)

**5. HAProxy syntax validation failures:**

```
✗ Config must be syntactically valid
  Error: HAProxy validation failed (config size: 1234 bytes):
         maxconn: integer expected, got 'invalid' (line 15)
```

**Solution:**
```bash
# See the problematic line
controller validate -f config.yaml --dump-rendered | grep -A2 -B2 "line 15"

# Test HAProxy config locally
haproxy -c -f /tmp/haproxy.cfg
```

**Debugging Workflow:**

```bash
# 1. Start with enhanced error messages (no flags)
controller validate -f config.yaml
# Output: "pattern X not found in map:foo.map (target size: 61 bytes). Hint: Use --verbose"

# 2. Enable verbose for content preview
controller validate -f config.yaml --verbose
# Output: Shows first 200 chars of failing target

# 3. Dump full content if preview isn't enough
controller validate -f config.yaml --dump-rendered
# Output: Complete haproxy.cfg, all maps, files, certs

# 4. Check template execution
controller validate -f config.yaml --trace-templates
# Output: Template names and timing

# 5. Combine flags for comprehensive debugging
controller validate -f config.yaml --verbose --dump-rendered --trace-templates
```

**Output Formats:**

For CI/CD integration, use structured output:

```bash
# JSON output
controller validate -f config.yaml --output json > results.json

# YAML output
controller validate -f config.yaml --output yaml > results.yaml

# Both formats include:
# - Test results (pass/fail)
# - Assertion details
# - Rendered content
# - Error messages
```

## HAProxy Pod Issues

### Cannot Connect to HAProxy Dataplane API

**Symptoms:**
- Controller logs show "connection refused" or "timeout"
- Deployment operations failing
- Configuration not reaching HAProxy

**Diagnosis:**

```bash
# Test direct connection to Dataplane API
HAPROXY_POD=$(kubectl get pods -l app=haproxy -o jsonpath='{.items[0].metadata.name}')
kubectl port-forward $HAPROXY_POD 5555:5555

# In another terminal
curl -u admin:password http://localhost:5555/v2/info
```

**Common Causes:**

**1. Dataplane API not running:**

```bash
# Check both containers are running
kubectl get pod $HAPROXY_POD -o jsonpath='{.status.containerStatuses[*].name}'
# Should show: haproxy dataplane

# Check dataplane container logs
kubectl logs $HAPROXY_POD -c dataplane
```

**Solution:**
- Verify dataplane container started correctly
- Check for port conflicts
- Ensure master socket exists (see HAProxy container logs)

**2. Wrong credentials:**

```bash
# Compare credentials in controller Secret and HAProxy config
kubectl get secret haproxy-credentials -o jsonpath='{.data.dataplane_username}' | base64 -d
kubectl exec $HAPROXY_POD -c dataplane -- cat /etc/haproxy/dataplaneapi.yaml | grep -A2 user
```

**Solution:**
```bash
# Update credentials to match
kubectl create secret generic haproxy-credentials \
  --from-literal=dataplane_username=admin \
  --from-literal=dataplane_password=adminpass \
  --dry-run=client -o yaml | kubectl apply -f -

# Restart controller to reload credentials
kubectl rollout restart deployment haproxy-template-ic
```

**3. Network policy blocking access:**

```bash
# Check if network policy exists
kubectl get networkpolicy

# Test connectivity from controller pod
kubectl exec -it deployment/haproxy-template-ic -- \
  wget -qO- http://$HAPROXY_POD_IP:5555/v2/info
```

**Solution:**
- Update NetworkPolicy to allow controller → HAProxy traffic
- Check NetworkPolicy egress rules include HAProxy pod selector

### HAProxy Configuration Not Updating

**Symptoms:**
- Controller logs show successful deployment
- `kubectl exec` shows old configuration in HAProxy pod
- Changes not taking effect

**Diagnosis:**

```bash
# Check HAProxy config file timestamp
kubectl exec $HAPROXY_POD -c haproxy -- ls -lh /etc/haproxy/haproxy.cfg

# Compare with last deployment time in controller logs
kubectl logs -l app.kubernetes.io/name=haproxy-template-ic | grep -i "deployment.*succeeded"
```

**Common Causes:**

**1. Volume mount issues:**

Verify HAProxy and Dataplane share the same volume:

```bash
# Check volume mounts
kubectl get pod $HAPROXY_POD -o yaml | grep -A5 volumeMounts
```

**Solution:**
- Ensure both containers mount the same `haproxy-config` volume
- Restart pod if volume mount is missing

**2. HAProxy not reloading:**

```bash
# Check HAProxy master process
kubectl exec $HAPROXY_POD -c haproxy -- ps aux | grep haproxy

# Try manual reload
kubectl exec $HAPROXY_POD -c haproxy -- \
  sh -c "echo 'reload' | socat stdio unix-connect:/etc/haproxy/haproxy-master.sock"
```

**Solution:**
- Verify master socket is accessible
- Check reload command in dataplaneapi.yaml
- Review dataplane logs for reload failures

## Routing Issues

### Requests Not Reaching Backend

**Symptoms:**
- 503 Service Unavailable errors
- Requests timing out
- No backend servers in HAProxy stats

**Diagnosis:**

```bash
# Check HAProxy configuration for backend
kubectl exec $HAPROXY_POD -c haproxy -- cat /etc/haproxy/haproxy.cfg | grep -A10 "backend"

# Check if backend has servers
kubectl exec $HAPROXY_POD -c haproxy -- \
  echo "show servers state" | socat stdio /etc/haproxy/haproxy-master.sock
```

**Common Causes:**

**1. No endpoints for service:**

```bash
# Check EndpointSlices exist
kubectl get endpointslices -l kubernetes.io/service-name=echo

# Check pods are ready
kubectl get pods -l app=echo
```

**Solution:**
- Verify backend pods are running and ready
- Check service selector matches pod labels
- Ensure ports are correctly configured

**2. Backend not created in HAProxy:**

Check controller logs for errors during backend creation:

```bash
kubectl logs -l app.kubernetes.io/name=haproxy-template-ic | grep -i "backend.*error"
```

**Solution:**
- Review template logic for backend generation
- Ensure Ingress references valid service
- Check watched resources include Services and EndpointSlices

**3. Routing rules not matching:**

```bash
# Test routing manually
kubectl port-forward $HAPROXY_POD 8080:80

# Try request with different Host headers
curl -v -H "Host: your-host.example.com" http://localhost:8080/
```

**Solution:**
- Verify Host header matches Ingress rules
- Check ACL rules in HAProxy config
- Review map files if using map-based routing

### SSL/TLS Issues

**Symptoms:**
- SSL handshake failures
- Certificate errors
- HTTPS not working

**Diagnosis:**

```bash
# Check SSL certificates exist
kubectl exec $HAPROXY_POD -c haproxy -- ls -lh /etc/haproxy/ssl/

# Test SSL connection
openssl s_client -connect localhost:443 -servername your-host.example.com < /dev/null
```

**Common Causes:**

**1. Certificate not deployed:**

```bash
# Check if certificate template is defined
kubectl get haproxytemplateconfig haproxy-template-ic-config \
  -o jsonpath='{.spec.sslCertificates}'

# Check Secret exists
kubectl get secret your-tls-secret
```

**Solution:**
- Define certificate template in `sslCertificates` section
- Ensure Secret is watched (configure in `watchedResources.secrets`)
- Use `b64decode` filter for Secret data

**2. Wrong certificate path in bind:**

```bash
# Check bind line in config
kubectl exec $HAPROXY_POD -c haproxy -- \
  grep -i "bind.*ssl.*crt" /etc/haproxy/haproxy.cfg
```

**Solution:**
- Use absolute paths: `/etc/haproxy/ssl/cert.pem`
- Use `pathResolver.GetPath()` method: `{{ pathResolver.GetPath("cert.pem", "cert") }}`
- Verify path matches `dataplane.sslCertsDir`

## Performance Issues

### Slow Reconciliation

**Symptoms:**
- Configuration changes taking minutes to apply
- High CPU usage in controller
- Template rendering timeouts

**Diagnosis:**

```bash
# Check reconciliation duration metrics
kubectl port-forward deployment/haproxy-template-ic 9090:9090
curl http://localhost:9090/metrics | grep reconciliation_duration_seconds

# Enable debug logging
kubectl set env deployment/haproxy-template-ic VERBOSE=2
kubectl logs -f deployment/haproxy-template-ic
```

**Common Causes:**

**1. Large number of resources:**

```bash
# Count watched resources
kubectl get ingresses --all-namespaces | wc -l
kubectl get services --all-namespaces | wc -l
```

**Solution:**
- Use namespace restrictions in `watchedResources`
- Add label selectors to filter resources
- Consider memory store vs cached store (see [Watching Resources](./watching-resources.md))

**2. Inefficient templates:**

Look for:
- Nested loops over large collections
- Repeated expensive operations
- Missing use of variables for cached values

**Solution:**
- Use template snippets for reusable logic
- Cache computed values with `{% set %}`
- See [Templating Guide](./templating.md#tips--tricks)

**3. Memory constraints:**

```bash
# Check memory usage
kubectl top pod -l app.kubernetes.io/name=haproxy-template-ic

# Check for OOMKilled events
kubectl get events --sort-by='.lastTimestamp' | grep OOM
```

**Solution:**
```bash
# Increase memory limits
helm upgrade haproxy-ic ./charts/haproxy-template-ic \
  --reuse-values \
  --set resources.limits.memory=1Gi
```

### High Memory Usage

**Symptoms:**
- Controller pod using excessive memory
- OOMKilled events
- Gradual memory growth

**Diagnosis:**

```bash
# Monitor memory over time
kubectl top pod -l app.kubernetes.io/name=haproxy-template-ic --containers

# Enable memory profiling (if debug server enabled)
kubectl port-forward deployment/haproxy-template-ic 6060:6060
curl http://localhost:6060/debug/pprof/heap > heap.prof
go tool pprof heap.prof
```

**Solution:**

**1. Use field filtering:**
```yaml
# In HAProxyTemplateConfig
spec:
  watchedResourcesIgnoreFields:
    - metadata.managedFields
    - metadata.annotations["kubectl.kubernetes.io/last-applied-configuration"]
```

**2. Switch to cached store for large resources:**
```yaml
spec:
  watchedResources:
    secrets:
      store: on-demand
      cacheTTL: 2m
```

**3. Limit watch scope:**
```yaml
spec:
  watchedResources:
    ingresses:
      namespace: production  # Watch only one namespace
      labelSelector:
        app: myapp  # Filter by labels
```

## Getting Help

### Collect Diagnostic Information

When reporting issues, gather this information:

```bash
# Controller version
kubectl get deployment haproxy-template-ic \
  -o jsonpath='{.spec.template.spec.containers[0].image}'

# Controller logs (last 500 lines)
kubectl logs -l app.kubernetes.io/name=haproxy-template-ic --tail=500 > controller-logs.txt

# Configuration
kubectl get haproxytemplateconfig haproxy-template-ic-config -o yaml > config.yaml

# Resource counts
kubectl get ingresses,services,endpointslices --all-namespaces | wc -l

# Metrics
kubectl port-forward deployment/haproxy-template-ic 9090:9090
curl http://localhost:9090/metrics > metrics.txt

# HAProxy config (sanitize sensitive data!)
kubectl exec $HAPROXY_POD -c haproxy -- cat /etc/haproxy/haproxy.cfg > haproxy.cfg
```

### Enable Debug Logging

```bash
# Temporary: Update environment variable
kubectl set env deployment/haproxy-template-ic VERBOSE=2

# Permanent: Update Helm values
helm upgrade haproxy-ic ./charts/haproxy-template-ic \
  --reuse-values \
  --set controller.config.logging.verbose=2
```

Debug logging includes:
- Detailed reconciliation steps
- Template rendering context
- Dataplane API requests/responses
- Configuration diff details

### Enable Debug Server

For deeper investigation, enable the debug HTTP server:

```bash
helm upgrade haproxy-ic ./charts/haproxy-template-ic \
  --reuse-values \
  --set controller.debugPort=6060

# Access debug endpoints
kubectl port-forward deployment/haproxy-template-ic 6060:6060
```

Available endpoints:
- `/debug/vars` - Internal state
- `/debug/vars/rendered` - Last rendered config
- `/debug/vars/resources` - Resource counts
- `/debug/vars/events` - Recent events
- `/debug/pprof/` - Go profiling

See [Debugging Guide](./operations/debugging.md) for details.

## See Also

- [Getting Started Guide](./getting-started.md) - Initial setup and verification
- [Configuration Reference](./configuration.md) - Complete configuration options
- [High Availability Guide](./operations/high-availability.md) - HA-specific troubleshooting
- [Debugging Guide](./operations/debugging.md) - Advanced debugging techniques
- [GitLab Issues](https://gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/-/issues) - Report bugs and request features
