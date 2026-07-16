# HAProxy Deployment

## Overview

The chart can deploy HAProxy pods alongside the controller, or you can manage HAProxy separately.

## Resource limits

Controller-pod sizing — the chart's request/limit defaults, the sizing table, and the GOMAXPROCS/GOMEMLIMIT container awareness — is covered in [Performance — Controller Resource Sizing](operations/performance.md#controller-resource-sizing). HAProxy and the Dataplane API sidecar have their own resource blocks in the chart values: `haproxy.resources` and `haproxy.dataplane.resources`.

## Service Architecture

The chart deploys separate Services for the controller and HAProxy so data-plane traffic and operational endpoints never cross. The controller Service is for cluster-internal monitoring only; the HAProxy Service is what external traffic hits.

### Controller Service

A single `ClusterIP` Service named after the chart's `fullname` (for example `<release>-haptic`) that exposes the controller's ports defined in `controller.ports`:

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

A Service (`<fullname>-haproxy`, for example `<release>-haptic-haproxy`, `NodePort` by default) that fronts the HAProxy pods. Port structure comes from `haproxy.service.*` and container ports from `haproxy.ports.*`:

| Name | Service port | Container port | nodePort default |
|------|--------------|----------------|------------------|
| `http` | 80 | 80 | 30080 |
| `https` | 443 | 443 | 30443 |
| `stats` | 8404 | 8404 | 30404 |

The Dataplane API sidecar gets its own internal-only `ClusterIP` Service (`<fullname>-haproxy-dataplane`, for example `<release>-haptic-haproxy-dataplane`) on port 5555. Its type comes from `haproxy.dataplane.service.type`.

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

### Full HAProxy Service reference

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

## Replicas and autoscaling

The chart runs 2 HAProxy replicas by default. Set `haproxy.replicaCount` to change the fixed count:

```yaml
haproxy:
  replicaCount: 3
```

For traffic-driven autoscaling, enable [KEDA](https://keda.sh/) under `haproxy.keda`. When `haproxy.keda.enabled` is true, the chart creates a `ScaledObject` and stops writing a fixed `replicas` onto the Deployment (KEDA owns it), scaling between `minReplicaCount` and `maxReplicaCount` from the triggers you define:

```yaml
haproxy:
  keda:
    enabled: true
    minReplicaCount: 2
    maxReplicaCount: 10
    triggers:
      - type: cpu
        metricType: Utilization
        metadata:
          value: "70"
```

KEDA must be installed in the cluster, and `haproxy.keda.triggers` must list at least one trigger — it's empty by default. Any [KEDA scaler](https://keda.sh/docs/latest/scalers/) works; the block above uses CPU utilization.

## Initial bootstrap config

When the chart manages HAProxy, the pod boots with a minimal `haproxy.cfg` rendered from `haproxy.initialConfig` into the `<release>-haptic-haproxy-config` ConfigMap. The controller replaces this config via the Dataplane API on its first reconcile, so the bootstrap only matters during the seconds between pod start and controller handoff (and on pod restart before the controller reconciles again).

The default keeps `/healthz` returning 200 on the stats port and `/ready` returning 503 ("waiting for controller config"), so the pod stays NotReady until the controller pushes its first real config. To customise (for example, to add cluster-internal ACLs, an extra logging directive, or pre-bind a port the controller doesn't manage), copy the default from `values.yaml` into your own values file and edit it:

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
    An override that returns 200 on `/ready` lets the Service route traffic to HAProxy before any backends exist — clients see 404 responses. Replicate the 503 behaviour, or accept the gap.

## Access logging

HAProxy writes its access logs to the container's stdout, so `kubectl logs` shows them directly:

```bash
kubectl logs -n haptic -l app.kubernetes.io/component=loadbalancer -c haproxy
```

The default template libraries emit `log stdout len 4096 local0 info` in the
`global` section and `option httplog` in the `defaults` section, which produces
HAProxy's standard HTTP log line per request. Generated TCP frontends override
that inherited format with `option tcplog`; the status frontend keeps its log
target but uses `option dontlog-normal`, avoiding both probe noise and the
HAProxy warning caused by combining `no log` with an inherited log format. To
use a custom HTTP format, add a `log-format` directive through a
`defaults-settings-*` snippet — it runs after the built-in
`defaults-settings-100-options` and overrides `option httplog`:

```yaml
controller:
  config:
    templateSnippets:
      defaults-settings-150-log-format:
        template: |
          log-format "%ci:%cp [%tr] %ft %b/%s %TR/%Tw/%Tc/%Tr/%Ta %ST %B %CC %CS %tsc %ac/%fc/%bc/%sc/%rc %sq/%bq %hr %hs %{+Q}r"
```

To change the log destination or facility instead, override `global-settings-100-logging`.

!!! note "This isn't the Dataplane API log"
    `haproxy.dataplane.aclFormat` configures the **Dataplane API sidecar's own** access log, not HAProxy's traffic logs. HAProxy request logging is controlled by the `log`, `option httplog`, and `log-format` directives above.

## HAProxy Pod requirements

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

<a id="example-haproxy-pod-deployment-byo-haproxy"></a>

### Example HAProxy Pod Deployment (bring-your-own HAProxy)

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
