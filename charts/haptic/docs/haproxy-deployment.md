# HAProxy Deployment

## Overview

The chart can deploy HAProxy pods alongside the controller, or you can manage HAProxy separately. This page covers resource limits, the service architecture, and HAProxy pod requirements.

## Resource Limits and Container Awareness

The controller automatically detects and respects container resource limits:

### CPU Limits (GOMAXPROCS)

**Go 1.25+ Native Support**: The controller uses Go 1.25, which includes built-in container-aware GOMAXPROCS. The Go runtime automatically:

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

Set resource limits in your values file:

```yaml
resources:
  limits:
    cpu: 500m
    memory: 512Mi
  requests:
    cpu: 100m
    memory: 128Mi
```

The controller will automatically log the detected limits at startup:

```
INFO HAPTIC starting ... gomaxprocs=1 gomemlimit="461373644 bytes (440.00 MiB)"
```

### Fine-Tuning Memory Limits

The `AUTOMEMLIMIT` environment variable can adjust the memory limit ratio (default: 0.9):

```yaml
# In deployment.yaml or via Helm values
env:
  - name: AUTOMEMLIMIT
    value: "0.8"  # Set GOMEMLIMIT to 80% of container limit
```

Valid range: 0.0 < AUTOMEMLIMIT <= 1.0

### Why This Matters

- **Prevents OOM kills**: GOMEMLIMIT helps the Go GC keep heap memory under control
- **Reduces CPU throttling**: Proper GOMAXPROCS prevents over-scheduling goroutines
- **Improves performance**: Better GC tuning and reduced context switching

## Service Architecture

The chart deploys two separate Kubernetes Services:

### Controller Service

Exposes the controller's operational endpoints:

- **healthz** (8080): Liveness and readiness probes
- **metrics** (9090): Prometheus metrics endpoint

This service is for cluster-internal monitoring only. Default configuration:

```yaml
service:
  type: ClusterIP
  healthzPort: 8080
  metricsPort: 9090
```

### HAProxy Service

Exposes the HAProxy load balancer for ingress traffic:

- **http** (80): HTTP traffic routing
- **https** (443): HTTPS/TLS traffic routing
- **stats** (8404): Health and statistics page

This service routes external traffic to HAProxy pods. You can configure it based on your deployment environment:

**Development (kind cluster)**:

```yaml
haproxy:
  enabled: true
  service:
    type: LoadBalancer  # kind maps to localhost
```

**Production (cloud provider)**:

```yaml
haproxy:
  enabled: true
  service:
    type: LoadBalancer
    annotations:
      service.beta.kubernetes.io/aws-load-balancer-type: "nlb"
```

**Production (NodePort for external LB)**:

```yaml
haproxy:
  enabled: true
  service:
    type: NodePort
    http:
      nodePort: 30080
    https:
      nodePort: 30443
```

**Production (managed externally)**:

```yaml
haproxy:
  enabled: false  # Manage HAProxy deployment separately
```

### HAProxy Service Configuration

```yaml
haproxy:
  enabled: true
  service:
    type: NodePort  # ClusterIP, NodePort, or LoadBalancer
    annotations: {}  # Cloud provider annotations
    http:
      port: 80
      nodePort: 30080  # Only for NodePort/LoadBalancer
    https:
      port: 443
      nodePort: 30443  # Only for NodePort/LoadBalancer
    stats:
      port: 8404
      nodePort: 30404  # Only for NodePort/LoadBalancer
```

### Why Separate Services?

Separating the controller and HAProxy services provides:

- **Clear separation of concerns**: Operational metrics vs data plane traffic
- **Independent scaling**: Controller runs as single replica, HAProxy scales independently
- **Security**: Controller endpoints remain internal, only HAProxy exposed externally
- **Flexibility**: Different service types for different purposes (ClusterIP for controller, LoadBalancer for HAProxy)

## HAProxy Pod Requirements

The controller manages HAProxy pods deployed separately. Each HAProxy pod must:

1. **Have matching labels** as defined in `podSelector`
2. **Run HAProxy with Dataplane API sidecar**
3. **Share config volume** between HAProxy and Dataplane containers
4. **Expose Dataplane API** on port 5555

### Example HAProxy Pod Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: haproxy
spec:
  replicas: 2
  selector:
    matchLabels:
      app: haproxy
      component: loadbalancer
  template:
    metadata:
      labels:
        app: haproxy
        component: loadbalancer
    spec:
      containers:
      - name: haproxy
        image: haproxytech/haproxy-debian:3.2
        command: ["/bin/sh", "-c"]
        args:
          - |
            mkdir -p /etc/haproxy/maps /etc/haproxy/certs
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
                ssl_certs_dir: /etc/haproxy/certs
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
