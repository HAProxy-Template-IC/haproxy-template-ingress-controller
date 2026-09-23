# Troubleshooting

Find your symptom in the quick reference below, then follow its diagnosis and fix.

The commands use Helm release `haptic`. Set its namespace once for this shell:

```bash
HAPTIC_NAMESPACE=haptic
```

For an installed release, start with pod status and controller logs:

```bash
kubectl get pods --namespace "$HAPTIC_NAMESPACE" \
  --selector app.kubernetes.io/instance=haptic
kubectl logs --namespace "$HAPTIC_NAMESPACE" deployment/haptic-controller \
  --container controller --tail=100
```

HAPTIC 0.2.0 also provides [fleet diagnostics](operations/diagnostics.md) with
`haptic doctor`. That command isn't available in `0.2.0-alpha.3` or earlier.
For a specific symptom, use the table below.

## Quick symptom reference

| Symptom | Section |
|---------|---------|
| Pods stuck in ImagePullBackOff | [Image Pull Errors](#image-pull-errors) |
| "no kind HAProxyTemplateConfig is registered" | [CRD Not Found](#crd-not-found) |
| No DNS or API connectivity on a kind cluster | [NetworkPolicy Issues in kind](#networkpolicy-issues-in-kind) |
| Pod in CrashLoopBackOff | [Controller Not Starting](#controller-not-starting) |
| Pod stuck Running but not Ready (for example `1/2` or `3/4`) | [Pods stuck not Ready](#pods-stuck-not-ready) |
| Pods running, no reconciliation activity | [Controller Running But Not Processing](#controller-running-but-not-processing) |
| "template rendering failed" in logs | [Invalid Template Syntax](#invalid-template-syntax) |
| "validation failed" / HAProxy errors | [Configuration Validation Failures](#configuration-validation-failures) |
| `kubectl apply` denied by an admission webhook | [Admission webhook denied the apply](#admission-webhook-denied-the-apply) |
| "connection refused" to an HAProxy pod | [Can't reach the agent](#cant-reach-the-agent) |
| Controller reports success but HAProxy unchanged | [Configuration Not Updating](#configuration-not-updating) |
| 503 errors / no servers in HAProxy stats | [Requests Not Reaching Backend](#requests-not-reaching-backend) |
| 404 for a host or path that should route | [404: no route matched](#404-no-route-matched) |
| SSL handshake failures | [SSL/TLS Issues](#ssltls-issues) |
| High CPU or slow reconciliation | [Slow Reconciliation](#slow-reconciliation) |
| OOMKilled / gradual memory growth | [High Memory Usage](#high-memory-usage) |
| "shm-stats-file-max-objects" / reload failures | [Shared Memory Stats Limit](#shared-memory-stats-limit) |

## Install issues

Problems that surface while the Helm chart installs, before the controller does any work.

### Image pull errors

If pods are stuck in `ImagePullBackOff`:

```bash
kubectl describe pod -n "$HAPTIC_NAMESPACE" -l app.kubernetes.io/name=haptic
```

Verify the `haproxyVersion` value matches an available image tag:

```bash
helm get values haptic -n "$HAPTIC_NAMESPACE" --all | grep haproxyVersion
```

The controller image tag is derived from both the chart `version` and `haproxyVersion`. If pulling from a private registry, configure `controller.podSpec.imagePullSecrets` (and `haproxy.podSpec.imagePullSecrets` if the chart's HAProxy pods need the same registry).

### CRD not found

If the controller fails with "no kind HAProxyTemplateConfig is registered":

```bash
kubectl get crd haproxytemplateconfigs.haproxy-haptic.org
```

The chart installs its CRDs through a hook. Inspect the installation Jobs for a
failed CRD update, then retry the [installation or upgrade](deploying-with-helm.md)
with your pinned chart version and complete values:

```bash
kubectl get jobs --namespace "$HAPTIC_NAMESPACE"
helm status haptic --namespace "$HAPTIC_NAMESPACE"
```

### NetworkPolicy issues in kind

Kind's default network doesn't enforce NetworkPolicy. If you installed a network
plugin that does, such as Calico or Cilium, check that DNS is allowed and
`controller.networkPolicy.egress.kubernetesApi` covers the API-server address.
See [Networking](./operations/networking.md).

Debug NetworkPolicy:

```bash
# Check controller can resolve DNS
kubectl exec -n "$HAPTIC_NAMESPACE" deployment/haptic-controller -c controller -- \
  nslookup kubernetes.default

# Check controller can reach HAProxy pod
HAPROXY_IP=$(kubectl get pods -n "$HAPTIC_NAMESPACE" -l app.kubernetes.io/instance=haptic,app.kubernetes.io/component=loadbalancer -o jsonpath='{.items[0].status.podIP}')
kubectl exec -n "$HAPTIC_NAMESPACE" deployment/haptic-controller -c controller -- \
  haptic agent state --url "https://$HAPROXY_IP:5555"
```

## Controller issues

### Controller not starting

For repeated restarts or initialization errors, inspect the pod and its logs:

```bash
kubectl get pods -n "$HAPTIC_NAMESPACE" -l app.kubernetes.io/instance=haptic,app.kubernetes.io/component=controller
kubectl logs -n "$HAPTIC_NAMESPACE" -c controller -l app.kubernetes.io/instance=haptic,app.kubernetes.io/component=controller --tail=100
kubectl describe pod -n "$HAPTIC_NAMESPACE" -l app.kubernetes.io/instance=haptic,app.kubernetes.io/component=controller
```

| Cause | Check | Solution |
|-------|-------|----------|
| Missing configuration or library | `kubectl get haproxytemplateconfig,haproxytemplatelibrary -n "$HAPTIC_NAMESPACE"` | Check failed Helm or GitOps Jobs; restore the configuration through the release workflow. |
| Invalid credentials | Controller logs name a missing Secret or key | Restore the Secret from your credential source; don't generate a replacement password independently of the agent. |
| Permission denied | Logs name a verb and resource | Compare your ServiceAccount grants with the [required permissions](operations/security.md#rbac). |

### Pods stuck not ready

A running container can still fail its readiness probe. Inspect the pod Events
and per-container state:

```bash
kubectl get pods -n "$HAPTIC_NAMESPACE" -o wide
kubectl describe pods -n "$HAPTIC_NAMESPACE" -l app.kubernetes.io/instance=haptic
```

| Container | Next check |
| --- | --- |
| Controller | Logs for configuration-load, template, or validation errors; the controller must load a valid configuration before it can become ready. |
| HAProxy | Agent and HAProxy logs for a failed first deployment. The bootstrap configuration returns `503` on `/ready` until a rendered configuration runs. |
| Validator | Its logs and socket configuration; see [custom validators](operations/pluggable-validators.md#troubleshooting). |
| Custom sidecar | Its own readiness probe, logs, resource limits, and mounts. |

For a stopped or repeatedly restarted container, use the image-pull or startup
checks above. See [pod readiness](haproxy-deployment.md#pod-readiness-and-restarts)
for the chart's probe behavior.

### Controller running but not processing

Check whether the controller has synchronized its watched resources:

```bash
kubectl logs -n "$HAPTIC_NAMESPACE" -c controller -l app.kubernetes.io/instance=haptic,app.kubernetes.io/component=controller | grep -i "watch\|sync complete"
```

| Cause | Check | Solution |
|-------|-------|----------|
| Informers not syncing | Logs show "timeout waiting for cache sync" | Check API server connectivity, network policies |
| No matching resources | `kubectl get ingresses -A` | Check the watch's namespace, label, and class filters |
| Ingress class mismatch | `kubectl get ingress --all-namespaces` | The Ingress must reference the class the chart created; also check watch namespace restrictions and `watchedResources.*.fieldSelector` |
| Leader election (HA) | `kubectl get lease -n "$HAPTIC_NAMESPACE"` (the Lease is named after the Helm release) | Ensure one pod shows `is_leader=1` |

## Configuration issues

### Invalid template syntax

Find the failing template and line in the controller logs:

```bash
kubectl logs -n "$HAPTIC_NAMESPACE" -c controller -l app.kubernetes.io/instance=haptic,app.kubernetes.io/component=controller | grep -i "template\|render"
```

Fix the named template in your Helm values or configuration source. Use the
[debugging guide](operations/debugging.md#resolve-a-rejected-configuration)
to inspect rejected output and [validate the template](validation-tests.md)
before applying the fix.

HAPTIC retains the last valid configuration when rendering or validation fails.
New routing and endpoint changes wait until the error is fixed; traffic depends
on the backends in that retained configuration.

### Configuration validation failures

| Error | Cause | Solution |
|-------|-------|----------|
| `backend expects <name>` | Invalid HAProxy syntax | Fix the template and run [template validation](validation-tests.md) with its files and fixtures |
| `unable to load file` | Missing map/cert file | Check the matching map, file, or certificate declaration and `pathResolver.GetPath()` |
| `invalid address` | Bad server address | Verify EndpointSlices exist, check service names |

### Validation test failures

Prepare [offline schemas](validation-tests.md#prepare-schemas), then inspect
the failing test:

```bash
# Step 1: Run with verbose output
haptic validate -f config.yaml --schema-dir ./schemas --verbose

# Step 2: See full rendered content
haptic validate -f config.yaml --schema-dir ./schemas --dump-rendered

# Step 3: Check template execution
haptic validate -f config.yaml --schema-dir ./schemas --trace-templates
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

Fix the template or resource named in the denial, then retry with
`kubectl apply --dry-run=server -f ingress.yaml` before applying it. Admission
uses your live resources. Local `haptic validate` runs the fixtures in your
validation tests, so it only reproduces the problem if those fixtures include
the triggering resource and its dependencies. See [validation tests](validation-tests.md).

If the webhook is unreachable rather than rejecting the content, inspect the
controller pods, network access, and [webhook certificate](operations/webhook-certificates.md).
Keep admission validation enabled while repairing it.

A `HAProxyTemplateConfig` is checked when the controller loads it. Kubernetes
can store an invalid configuration, but the controller refuses to use it. Inspect
the object's `Validated` condition and controller logs. Use
[preflight validation](operations/validate-before-deploy.md) before changing Helm
values.

## HAProxy pod issues

<a id="can't-reach-the-agent"></a>

<a id="cannot-reach-the-agent"></a>

### Can't reach the agent

For connection failures or timeouts, first check the agent locally:

```bash
HAPROXY_POD=$(kubectl get pods -n "$HAPTIC_NAMESPACE" -l app.kubernetes.io/instance=haptic,app.kubernetes.io/component=loadbalancer -o jsonpath='{.items[0].metadata.name}')
kubectl exec -n "$HAPTIC_NAMESPACE" "$HAPROXY_POD" -c agent -- haptic agent state
```

`/v1/state` answers with the plan the pod applied, the plan its worker is
running, the digest of every file it holds, and what it last did with an apply.
The local command uses the pod's read-only Unix socket. To test the encrypted
network connection, run the controller-to-agent command above. For certificate
errors, check [agent certificate management](./operations/agent-certificates.md).

| Cause | Check | Solution |
|-------|-------|----------|
| Agent not running | `kubectl logs -n "$HAPTIC_NAMESPACE" "$HAPROXY_POD" -c agent` | Verify the container started, check port conflicts |
| Certificate rejected or expired | Inspect the controller and agent logs for TLS errors | Check [certificate expiry and renewal Jobs](operations/agent-certificates.md#check-expiry); repaired identities reload automatically |
| Network policy | `kubectl get networkpolicy -n "$HAPTIC_NAMESPACE"` | Update egress rules for controller → HAProxy |

### Configuration not updating

Inspect configuration conditions for rejected output or failed deployments:

```bash
kubectl get haproxycfg --namespace "$HAPTIC_NAMESPACE" -o yaml
```

With a build that supports it, `haptic doctor` compares the desired configuration
with every pod. See [fleet diagnostics](operations/diagnostics.md). A successful
attempt on one pod doesn't establish that every pod applied the change.

For a specific pod, [agent state](operations/debugging.md#common-recipes) reports
its applied files, running plan, and pending reload. File timestamps alone don't
show what HAProxy is serving; supported changes can apply without a reload.

### Shared memory stats limit

This applies to HAProxy 3.3+ with `haproxy.shmStats.enabled: true` (off by
default). Look for `shm-stats-file-max-objects` errors when a reload fails:

```bash
kubectl logs -n "$HAPTIC_NAMESPACE" -c controller -l app.kubernetes.io/instance=haptic,app.kubernetes.io/component=controller | grep "shm-stats"
```

Look for:

```
[ALERT] memory error while setting up shared counters for .../SRV_N server:
Cannot add additional object to '/dev/shm/haproxy-stats' file,
maximum number already reached (50000).
```

Each HAProxy frontend, backend, and server directive counts as one shm-stats object. The file is fixed-size and can't be resized on reload. Increase `haproxy.shmStats.maxObjects` in your Helm values:

```yaml
haproxy:
  shmStats:
    enabled: true
    maxObjects: 100000  # default: 50000
```

Size for `(frontends + backends + servers) × 1.2`. Each object uses about
4 KiB; 100,000 objects need about 390 MiB in `/dev/shm`.

!!! warning
    After changing `maxObjects`, verify that `haproxy.resources.limits.memory` is large enough to accommodate the increased `/dev/shm` usage. The shm volume is memory-backed and counts against the pod's memory limit.

## Routing issues

### Requests not reaching backend

Start with the [access log](operations/access-logging.md). Check the selected
backend, `denied_by`, and termination state. A `503` can mean no ready endpoints,
a policy failure, or an application response; status alone doesn't distinguish them.

Inspect the Service and its EndpointSlices. Enter the application's namespace
and Service name:

```bash
read -r -p "Application namespace: " app_namespace
read -r -p "Service name: " service_name
kubectl get service "$service_name" --namespace "$app_namespace" -o yaml
kubectl get endpointslices --namespace "$app_namespace" \
  --selector "kubernetes.io/service-name=$service_name" -o yaml
```

Check that the route refers to an existing Service port and that its
EndpointSlices contain ready backend addresses. Correct Service selectors or
unready application pods when those are the cause. For policy denials, follow
the Event or condition that names the rejected policy.

### 404: No route matched

HAPTIC's default backend returns `404` for an unmatched HTTP request and
gRPC status `12` for an unmatched gRPC request. Applications can return these
codes too; use the access log to establish where the response came from.

Check the route's class, host, path, and attachment:

| Route type | Check |
| --- | --- |
| Ingress | `spec.ingressClassName` matches HAPTIC's class, and the resource passes any custom watch filters. A legacy class annotation alone doesn't match the default filter. |
| Gateway API | The route's parent conditions report `Accepted=True` and `ResolvedRefs=True`; use the [Gateway's Service](gateway-api.md#step-4-test-the-routing) when testing. |
| Either | The request hostname and path match the declared route. An `Exact` path doesn't match paths below it; `Prefix` matches path segments. |

For an Ingress, bypass the external load balancer with a local port forward:

```bash
kubectl port-forward --namespace "$HAPTIC_NAMESPACE" service/haptic-haproxy 8080:80
```

In another terminal, enter the hostname and path declared by the route:

```bash
read -r -p "Route hostname: " route_hostname
read -r -p "Request path, starting with /: " request_path
curl -i --header "Host: $route_hostname" "http://127.0.0.1:8080$request_path"
```

Stop the forward when finished. If this works but the public address fails,
check DNS, load-balancer forwarding, and network access.

### SSL/TLS issues

For an Ingress, forward HTTPS to an unprivileged local port:

```bash
kubectl port-forward --namespace "$HAPTIC_NAMESPACE" service/haptic-haproxy 8443:443
```

In another terminal, inspect the certificate offered for your hostname:

```bash
read -r -p "TLS hostname: " tls_hostname
openssl s_client -connect 127.0.0.1:8443 -servername "$tls_hostname" < /dev/null
```

For a Gateway, use its [dedicated Service](gateway-api.md#step-4-test-the-routing)
instead. Stop the forward after the check.

| Symptom | Next check |
| --- | --- |
| Wrong certificate | The route's hostname, TLS Secret reference, and certificate DNS names. |
| Secret missing | The Secret's name and namespace; check cert-manager's Certificate conditions if it owns the Secret. |
| Expired certificate | The certificate issuer's renewal status; follow [certificate rotation](ssl-certificates.md#certificate-rotation). |
| Updated Secret but old certificate still served | Controller validation and deployment errors, then fleet convergence. |
| Backend TLS handshake fails | The backend CA, server name, and client-certificate settings; frontend certificates don't configure backend TLS. |

See [certificate setup](ssl-certificates.md) for cert-manager, manual Secrets,
and the chart's default certificate.

## Performance issues

### Slow reconciliation

Compare controller CPU and memory with the [sizing guide](operations/performance.md).
Check reconciliation duration, queue wait, and fleet convergence in the
[monitoring dashboard](operations/monitoring.md). A delayed render and a failed
deployment need different fixes.

For custom templates, use [template tracing](operations/performance.md#template-debugging)
to find expensive snippets. Narrow watches only when the removed resources
aren't needed for routing.

### Frequent renders without configuration changes

Compare successive versions of the watched resources. Annotation or status updates can trigger reconciliation even when the rendered HAProxy configuration stays identical. Add changing fields that your templates don't read to that watch's `ignoreFields`; see [Database operator annotations](./watching-resources.md#database-operator-annotations) for a Patroni example.

### High memory usage

For `OOMKilled` restarts, compare `controller.resources` with the
[resource sizing estimates](operations/performance.md#controller-resource-sizing)
and check the pod's Events. Startup can need more memory than steady operation.
Give each replica the same memory request and limit.

If HAPTIC watches resources it doesn't route, narrow the watch. For example, these
Helm values keep the existing Ingress-class filter and add a label selector:

```yaml
controller:
  config:
    watchedResources:
      ingresses:
        labelSelector: "app=myapp"
```

Only labeled Ingresses contribute routes. Don't apply this selector unless
it includes every Ingress this installation must serve. See
[watch selectors](watching-resources.md#narrowing-the-watch) for namespace filters.
The chart already fetches Secret contents on demand.

## Getting help

### Collect diagnostic information

This command requires HAPTIC 0.2.0 or a development build containing it. With an older release,
collect the pod status and logs described at the top of this page.

```bash
haptic doctor --namespace "$HAPTIC_NAMESPACE" \
  --bundle "haptic-support-$(date -u +%Y%m%dT%H%M%SZ).zip"
```

The bundle includes validation, deployment comparisons, pod versions, and
resource conditions. It omits Secret values, rendered configuration, and logs.
Review its resource names and identifiers before sharing it. See
[Diagnose a HAPTIC fleet](operations/diagnostics.md) for permissions, limits,
custom installations, and deeper private investigation.

### Enable debug logging

Set the runtime level through your Helm values:

```yaml
controller:
  config:
    logging:
      level: DEBUG
```

Apply your [complete values file](deploying-with-helm.md#change-settings).
The level changes without a pod restart and takes precedence over the startup
`LOG_LEVEL` environment variable. Use `TRACE` only for a short investigation;
restore `INFO` afterward to reduce log volume.

### Access the debug server

Follow the [debugging guide](operations/debugging.md) to inspect configuration,
compare pods, or investigate a rejected update. Debug output can include
credentials; restrict `pods/portforward` and `pods/exec` permissions.

## See also

- [Getting Started](./getting-started.md)
- [Debugging](./operations/debugging.md)
- [Monitoring](./operations/monitoring.md)
- [CRD Reference](./crd-reference.md)
- [Validation Tests](./validation-tests.md)
- [Templating Guide](./templating.md)
