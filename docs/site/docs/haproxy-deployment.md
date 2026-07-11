# HAProxy Deployment

## Overview

The chart can deploy HAProxy pods alongside the controller, or you can manage HAProxy separately.

## Resource Limits and Container Awareness

The controller automatically detects and respects container resource limits:

### CPU Limits (GOMAXPROCS)

**Native cgroup-aware GOMAXPROCS** (added upstream in Go 1.25; the controller currently builds with Go 1.26): The Go runtime automatically:

- Detects cgroup CPU limits (v1 and v2)
- Sets GOMAXPROCS to match the container's CPU limit (not the host's core count)
- Dynamically adjusts if CPU limits change at runtime

No configuration needed - this works automatically when you set CPU limits in the deployment.

### Memory Limits (GOMEMLIMIT)

**automemlimit Library**: The controller uses the `automemlimit` library to automatically set GOMEMLIMIT based on cgroup memory limits. By default:

- Sets GOMEMLIMIT to 90% of the container memory limit
- Leaves 10% headroom for non-heap memory sources
- Works with both cgroups v1 and v2

### Configuration

Set resource limits in your values file. Top-level `resources:` applies to the controller pod; HAProxy and the Dataplane API sidecar have their own blocks under `haproxy.resources` and `haproxy.dataplane.resources`:

```yaml
# Controller pod
resources:
  requests:
    cpu: 100m
    memory: 512Mi
  limits:
    memory: 512Mi   # No CPU limit by default to avoid throttling
```

The chart defaults to **Guaranteed QoS for memory** (`requests.memory == limits.memory`) and deliberately omits the CPU limit; see [Robusta on Kubernetes memory limits](https://home.robusta.dev/blog/kubernetes-memory-limit) for the rationale.

At startup the controller logs the detected limits, for example:

```
INFO HAPTIC starting ... gomaxprocs=8 gomemlimit="483183820 bytes (460.80 MiB)"
```

`gomemlimit` is 90% of the 512Mi memory limit (≈460.8 MiB). Because the example omits a CPU limit, `gomaxprocs` matches the node's core count (8 here) rather than a container CPU limit — you only see `gomaxprocs=1` when you set a 1-CPU limit.

### Fine-Tuning Memory Limits

The `AUTOMEMLIMIT` environment variable can adjust the memory limit ratio (default: 0.9). Set it via the chart's top-level `extraEnv` list, which is injected into the controller container:

```yaml
extraEnv:
  - name: AUTOMEMLIMIT
    value: "0.8"   # Set GOMEMLIMIT to 80% of container memory limit
```

Valid range: `0.0 < AUTOMEMLIMIT <= 1.0`.

### Why This Matters

- **Prevents OOM kills**: GOMEMLIMIT helps the Go GC keep heap memory under control
- **Reduces CPU throttling**: Proper GOMAXPROCS prevents over-scheduling goroutines

## Service Architecture

The chart deploys separate Services for the controller and HAProxy so data-plane traffic and operational endpoints never cross. The controller Service is for cluster-internal monitoring only; the HAProxy Service is what external traffic hits.

### Controller Service

A single `ClusterIP` Service named after the chart's fullname (e.g. `<release>-haptic`) that exposes the controller's ports defined in `controller.ports`:

| Name | Container port | Values key | Purpose |
|------|----------------|------------|---------|
| `healthz` | 8080 | `controller.ports.healthz` | Liveness/readiness probes and the `/debug/*` introspection endpoints (also served on `controller.debugPort`, which defaults to the same value) |
| `metrics` | 9090 | `controller.ports.metrics` | Prometheus metrics |
| `webhook` | 9443 | `controller.ports.webhook` | Admission-webhook HTTPS endpoint |

Override Service type, annotations, etc. under the top-level `service:` block:

```yaml
service:
  type: ClusterIP
  annotations: {}
```

### HAProxy Service

A Service (`<fullname>-haproxy`, e.g. `<release>-haptic-haproxy`, `NodePort` by default) that fronts the HAProxy pods. Port structure comes from `haproxy.service.*` and container ports from `haproxy.ports.*`:

| Name | Service port | Container port | nodePort default |
|------|--------------|----------------|------------------|
| `http` | 80 | 80 | 30080 |
| `https` | 443 | 443 | 30443 |
| `stats` | 8404 | 8404 | 30404 |

The Dataplane API sidecar gets its own internal-only `ClusterIP` Service (`<fullname>-haproxy-dataplane`, e.g. `<release>-haptic-haproxy-dataplane`) on port 5555. Its type comes from `haproxy.dataplane.service.type`.

**Development (kind cluster)** — NodePort default works out of the box; switch to LoadBalancer if you want `localhost` mapping via kind's port-forward:

```yaml
haproxy:
  service:
    type: LoadBalancer
```

**Cloud provider LoadBalancer**:

```yaml
haproxy:
  service:
    type: LoadBalancer
    annotations:
      service.beta.kubernetes.io/aws-load-balancer-type: "nlb"
```

**External / self-managed HAProxy** — turn off the chart's HAProxy deployment and manage pods yourself (see [HAProxy Pod Requirements](#haproxy-pod-requirements)):

```yaml
haproxy:
  enabled: false
```

### Full HAProxy Service Reference

```yaml
haproxy:
  enabled: true
  ports:
    http: 80         # HAProxy container HTTP bind
    https: 443       # HAProxy container HTTPS bind
    stats: 8404      # Stats/health page
    dataplane: 5555  # Dataplane API
  service:
    type: NodePort   # ClusterIP, NodePort, or LoadBalancer
    annotations: {}
    loadBalancerIP: ""
    loadBalancerSourceRanges: []
    externalTrafficPolicy: ""   # Cluster | Local
    http:
      port: 80
      nodePort: 30080           # Only honored for NodePort/LoadBalancer
    https:
      port: 443
      nodePort: 30443
    stats:
      port: 8404
      nodePort: 30404
```

## Initial Bootstrap Config

When the chart manages HAProxy, the pod boots with a minimal `haproxy.cfg` rendered from `haproxy.initialConfig` into the `<release>-haptic-haproxy-config` ConfigMap. The controller replaces this config via the Dataplane API on its first reconcile, so the bootstrap only matters during the seconds between pod start and controller handoff (and on pod restart before the controller reconciles again).

The default keeps `/healthz` returning 200 on the stats port and `/ready` returning 503 ("waiting for controller config"), so the pod stays NotReady until the controller pushes its first real config. To customise (for example, to add a cluster-internal ACL, an extra logging directive, or pre-bind a port the controller doesn't manage), copy the default from `values.yaml` into your own values file and edit it:

```yaml
haproxy:
  initialConfig: |
    global
        log stdout len 4096 local0 info
        {{- with include "haptic.haproxy.nbthread" . }}
        nbthread {{ . }}
        {{- end }}
    defaults
        mode http
        timeout connect 5s
    frontend status
        bind *:{{ .Values.haproxy.ports.stats }}
        http-request return status 200 content-type text/plain string "OK" if { path /healthz }
        http-request return status 503 content-type text/plain string "Not ready" if { path /ready }
    frontend http_frontend
        bind *:{{ .Values.haproxy.ports.http }}
        default_backend default_backend
    backend default_backend
        http-request return status 404
```

The string is processed through Helm's `tpl`, so chart helpers and `.Values` references are available. Editing this value bumps the bootstrap-config checksum on the HAProxy Deployment, which rolls HAProxy pods on the next `helm upgrade`.

!!! warning "Keep /ready returning 503 until the controller takes over"
    An override that returns 200 on `/ready` will let the Service route traffic to HAProxy before any backends exist — clients will see 404s. Replicate the 503 behaviour, or accept the gap.

## HAProxy Pod Requirements

When `haproxy.enabled: false`, you're responsible for deploying HAProxy pods yourself. The controller discovers them via the pod selector at `controller.config.podSelector`, which defaults to:

```yaml
controller:
  config:
    podSelector:
      matchLabels:
        app.kubernetes.io/component: loadbalancer
        app.kubernetes.io/name: haptic        # set dynamically by the chart
        app.kubernetes.io/instance: <release> # set dynamically by the chart
```

If your existing HAProxy pods don't have those exact labels, either relabel them or override `controller.config.podSelector.matchLabels` to match.

Each discovered pod must:

1. **Carry labels matching `podSelector.matchLabels`**
2. **Run HAProxy in master-worker mode** with an admin socket the Dataplane API sidecar can connect to
3. **Run the Dataplane API sidecar** in the same pod, sharing the config volume with HAProxy
4. **Expose Dataplane API** on `haproxy.ports.dataplane` (default 5555)

### Example HAProxy Pod Deployment (BYO HAProxy)

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: haproxy
spec:
  replicas: 2
  selector:
    matchLabels:
      app.kubernetes.io/component: loadbalancer
      app.kubernetes.io/name: haptic
      app.kubernetes.io/instance: haptic
  template:
    metadata:
      labels:
        app.kubernetes.io/component: loadbalancer
        app.kubernetes.io/name: haptic
        app.kubernetes.io/instance: haptic
    spec:
      containers:
      - name: haproxy
        image: haproxytech/haproxy-debian:3.2
        command: ["/bin/sh", "-c"]
        args:
          - |
            mkdir -p /etc/haproxy/maps /etc/haproxy/ssl /etc/haproxy/general
            cat > /etc/haproxy/haproxy.cfg <<EOF
            global
                log stdout len 4096 local0 info
            defaults
                timeout connect 5s
            frontend status
                bind *:8404
                http-request return status 200 if { path /healthz }
                # Note: /ready endpoint intentionally omitted - added by controller
            EOF
            exec haproxy -W -db -S "/etc/haproxy/haproxy-master.sock,level,admin" -- /etc/haproxy/haproxy.cfg
        volumeMounts:
        - name: haproxy-config
          mountPath: /etc/haproxy
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8404
          initialDelaySeconds: 10
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /ready
            port: 8404
          initialDelaySeconds: 5
          periodSeconds: 5

      - name: dataplane
        image: haproxytech/haproxy-debian:3.2
        command: ["/bin/sh", "-c"]
        args:
          - |
            # Wait for HAProxy to create the socket
            while [ ! -S /etc/haproxy/haproxy-master.sock ]; do
              echo "Waiting for HAProxy master socket..."
              sleep 1
            done

            # Create Dataplane API config
            cat > /etc/haproxy/dataplaneapi.yaml <<'EOF'
            config_version: 2
            name: haproxy-dataplaneapi
            dataplaneapi:
              host: 0.0.0.0
              port: 5555
              user:
                - name: admin
                  password: adminpass
                  insecure: true
              transaction:
                transaction_dir: /var/lib/dataplaneapi/transactions
                backups_number: 10
                backups_dir: /var/lib/dataplaneapi/backups
              resources:
                maps_dir: /etc/haproxy/maps
                ssl_certs_dir: /etc/haproxy/ssl
                general_storage_dir: /etc/haproxy/general
            haproxy:
              config_file: /etc/haproxy/haproxy.cfg
              haproxy_bin: /usr/local/sbin/haproxy
              master_worker_mode: true
              master_runtime: /etc/haproxy/haproxy-master.sock
              reload:
                reload_delay: 1
                reload_cmd: /bin/sh -c "echo 'reload' | socat stdio unix-connect:/etc/haproxy/haproxy-master.sock"
                restart_cmd: /bin/sh -c "echo 'reload' | socat stdio unix-connect:/etc/haproxy/haproxy-master.sock"
                reload_strategy: custom
            log_targets:
              - log_to: stdout
                log_level: info
            EOF

            # Start Dataplane API
            exec dataplaneapi -f /etc/haproxy/dataplaneapi.yaml
        volumeMounts:
        - name: haproxy-config
          mountPath: /etc/haproxy

      volumes:
      - name: haproxy-config
        emptyDir: {}
```
