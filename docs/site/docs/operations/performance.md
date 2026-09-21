# Resource sizing and performance

For a default installation, reserve **about 1 CPU core and 5.4 GiB of memory**
across four pods: two controllers and two HAProxy pods, including their sidecars.
These are Kubernetes resource requests, not constant CPU or memory consumption.
The containers can use spare CPU when busy; their combined memory limits are
**7.25 GiB**.

## Controller resource sizing

### Starting budgets {#recommended-resources}

Size each controller for the routes and backend resources it watches. These
are rough planning estimates for the bundled templates, with one or a few
backends per route:

| Routing workload | CPU request per controller | Memory request and limit per controller |
| --- | ---: | ---: |
| Small installation, up to roughly 100 routes | `100m` | `1Gi` (chart default) |
| Hundreds of routes, approaching 1,000 | `500m` | `2Gi` |
| Several thousand routes, around 5,000 | `1` | `4Gi` |

These are starting estimates, not capacity limits.
Many endpoints per Service, large certificates, custom templates, and frequent
configuration changes can need more resources at the same route count.

For an installation with hundreds of routes, start with these Helm values:

```yaml
controller:
  resources:
    requests:
      cpu: 500m
      memory: 2Gi
    limits:
      memory: 2Gi
```

Both controller replicas need this budget. With the other chart defaults
unchanged, this example reserves about **2 CPU cores and 7.4 GiB** across the
installation.

Set memory through the container's Kubernetes requests and limits. Leave CPU
limits unset unless your cluster requires them, so startup and bursts of
configuration changes can use spare node CPU.

Keep the default `1Gi` controller memory budget even for a small route set:
startup also compiles templates and validates the configuration. Setting memory
requests equal to limits reserves that memory when Kubernetes schedules the pod.

## What the default installation reserves

These values include the default Gateway API, request logging, and metrics
features. CPU uses Kubernetes units (`1000m` = one core).

| Container | Copies | CPU request each | Memory request each | Memory limit each |
| --- | ---: | ---: | ---: | ---: |
| Controller | 2 | `100m` | `1Gi` | `1Gi` |
| Configuration validator | 2 | `25m` | `64Mi` | `128Mi` |
| HAProxy | 2 | `250m` | `1Gi` | `1Gi` |
| HAPTIC agent | 2 | `50m` | `256Mi` | `256Mi` |
| Vector log and metrics collector | 2 | `50m` | `256Mi` | `1Gi` |
| SPOA plugin hub | 2 | `50m` | `128Mi` | `256Mi` |
| **Total** | **4 pods** | **`1050m`** | **`5.375Gi`** | **`7.25Gi`** |

Allow room for rolling upgrades. One extra controller pod and one extra HAProxy
pod add about **2.7 GiB of memory requests** with these defaults. The temporary
preflight Job requests another `512Mi`, with a `1Gi` limit. A cluster filled to
its steady-state reservation may have no room to complete an upgrade.

Optional services add to the total:

| Feature | Additional default reservation |
| --- | --- |
| Shared response cache | `100m` CPU and `384Mi` memory per Varnish pod |
| Shared rate-limit store | Three Valkey/Sentinel pods, together `225m` CPU and `576Mi` memory |
| Custom sidecars | The requests you configure for those containers |

See [response caching](response-cache.md) and
[shared rate limiting](spoa-hub.md#managed-shared-rate-limit-store) before enabling
those services. Their memory use depends on cache size and active keys.

## Size HAProxy for traffic {#haproxy-optimization}

Route count chiefly affects the controller. Requests per second, concurrent
connections, TLS handshakes, compression, and WAF inspection determine traffic
capacity. Start with the chart's **two HAProxy replicas**, each requesting
`250m` CPU and `1Gi` memory, plus the sidecars listed above.

For a busy edge service, reserve more CPU for HAProxy and add replicas as traffic
grows. For example:

```yaml
haproxy:
  replicaCount: 3
  resources:
    requests:
      cpu: 1
      memory: 1Gi
    limits:
      memory: 1Gi
```

There is no measured requests-per-second guarantee for these budgets. A short
plaintext request and a WAF-inspected upload have different costs. Normal
production metrics show when to increase capacity; a custom benchmark isn't an
installation prerequisite.

### Scaling strategies

Increase `haproxy.replicaCount` for more traffic capacity, or configure
[HAProxy autoscaling](../haproxy-deployment.md#replicas-and-autoscaling).
Keep at least two replicas for availability. Include the agent, Vector, and SPOA
hub in the budget for every additional HAProxy pod.

Increase `controller.resources` when configuration changes become slow or a
controller runs out of memory. Adding controller replicas provides failover and
more admission capacity; it doesn't divide the rendering workload between them.
See [high availability](high-availability.md).

### Response compression

Response compression is off by default. It saves bandwidth and costs HAProxy
CPU. Set `haproxy-haptic.org/compress-enable: "true"` on an Ingress to enable it
for eligible responses.
See [compression settings](../libraries/haptic-annotations.md#compression)
for content types, CPU limits, and response-safety considerations.

### Password hash performance

Large basic-auth user lists and expensive password hashes increase
authentication, configuration validation, and reload costs. Keep the password
protection your security policy requires. For large user sets, consider
[external authentication](spoa-hub.md#what-each-plugin-does) instead of weakening
hashes to fit a CPU budget.

## When to adjust the budget

Enable the [bundled monitoring](monitoring.md) and watch these symptoms during
ordinary operation:

| Symptom | First action |
| --- | --- |
| Controller is `OOMKilled`, or memory repeatedly approaches its limit | Increase `controller.resources.requests.memory` and `limits.memory` together. |
| Configuration changes lag while controller CPU stays busy | Increase its CPU request; check CPU throttling if you set a limit. |
| Traffic latency rises while HAProxy CPU stays busy | Increase HAProxy's CPU request or replica count. |
| Vector is `OOMKilled` or drops access-log records | Increase `vector.resources`; reduce unused metric labels as described in [monitoring](monitoring.md#controlling-cardinality). |
| WAF processing is saturated | Increase `spoaHub.resources`; inspect hub timeout and queue metrics before raising timeouts. |

CPU requests reserve scheduling capacity; they don't cap usage. Memory limits
cap each container, so available memory elsewhere in the pod doesn't prevent an
individual container from being killed. Include startup and rolling upgrades
when checking memory headroom.

### Resource watching optimization

The bundled chart already fetches Secrets on demand and ignores common noisy
metadata. For custom watches, use on-demand storage for large objects you read
occasionally and indexes for lookups. Preserve every field your templates need.
See [watching resources](../watching-resources.md) for settings and examples.

### Template debugging

If a custom template makes configuration changes slow, use
`haptic validate --file config.yaml --trace-templates` to identify expensive
snippets. This needs your complete configuration and the
[validation tools](validate-before-deploy.md). For a running installation,
start with [fleet diagnostics](diagnostics.md).

## Reconciliation tuning

Keep the chart's timing defaults unless configuration delivery is missing your
latency target. Most watches batch updates within `100ms`; the bundled
EndpointSlice watch has no debounce delay so backend address changes arrive
promptly. A render already in progress can still delay a subsequent change.

### Deployment pacing

The chart spaces reloads of an individual HAProxy pod at least **5 seconds**
apart. Runtime updates don't wait for this reload interval. Raising it reduces
reload frequency but delays changes that require a reload.

```yaml
controller:
  config:
    dataplane:
      minDeploymentInterval: 5s
```

See [supported runtime updates](../supported-configuration.md) for which changes
need a reload and the [dataplane reference](../crd-reference.md#dataplane)
for other timing controls.

### Graceful reload drain bound

After a reload, HAProxy lets the old worker finish existing connections for up
to **10 seconds** by default, then closes those still open. Long-lived streams
need a longer drain window if they must survive reloads:

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        hardStopAfter: 30s
```

Longer windows retain old workers and their memory for longer. An empty string
disables the bound. If you replace `haproxy.initialConfig`, also set
`hard-stop-after` in that custom bootstrap configuration.
