# Deployment Diagrams

## Kubernetes Deployment Architecture

```mermaid
graph TB
    subgraph "Kubernetes Cluster"
        API[Kubernetes API Server]

        subgraph "haptic Namespace (release namespace)"
            subgraph "Controller Deployment (2 replicas, leader-elected)"
                CTRL1[Controller Pod 1<br/>leader]
                CTRL2[Controller Pod 2<br/>hot standby]
            end

            HTPLCFG[HAProxyTemplateConfig CRD<br/>Templates, watched resources, settings]
            CREDS[Secret<br/>dataplane credentials]

            CTRL_SVC[Controller Service<br/>ClusterIP<br/>:8080 healthz + /debug<br/>:9090 metrics<br/>:9443 webhook]

            subgraph "HAProxy Deployment (2+ replicas)"
                subgraph "haproxy pod A"
                    HAP1[HAProxy<br/>:8080, :8443, :8404]
                    DP1[Dataplane API<br/>:5555]
                end
                subgraph "haproxy pod B"
                    HAP2[HAProxy<br/>:8080, :8443, :8404]
                    DP2[Dataplane API<br/>:5555]
                end
            end

            HAP_SVC[HAProxy Service<br/>NodePort by default<br/>:80 → :8080<br/>:443 → :8443]
        end

        subgraph "Application Namespaces"
            ING[Ingress / HTTPRoute / GRPCRoute]
            APPSVC[Services + EndpointSlices]
            PODS[Application Pods]
        end
    end

    USERS[External Users] --> HAP_SVC
    HAP_SVC --> HAP1
    HAP_SVC --> HAP2

    CTRL_SVC --> CTRL1 & CTRL2
    API --> CTRL1 & CTRL2
    HTPLCFG --> CTRL1 & CTRL2
    CREDS --> CTRL1 & CTRL2
    ING -.Watch.-> CTRL1 & CTRL2
    APPSVC -.Watch.-> CTRL1 & CTRL2

    CTRL1 --> DP1 & DP2
    DP1 --> HAP1
    DP2 --> HAP2

    HAP1 --> PODS
    HAP2 --> PODS

    style CTRL1 fill:#4CAF50
    style CTRL2 fill:#A5D6A7
    style HAP1 fill:#FF9800
    style HAP2 fill:#FF9800
    style HTPLCFG fill:#2196F3
```

**Deployment Components:**

1. **Controller Deployment** — defaults to 2 replicas with leader election
   - All replicas watch Kubernetes resources, run admission webhooks, and discover HAProxy pods (hot standby — keeps caches warm so failover is instant)
   - Only the elected leader runs the render-validate Pipeline and pushes configuration to HAProxy via Dataplane API
   - See [High Availability](../../operations/high-availability.md) for tuning failover and [Leader Election](./leader-election.md) for the full all-replica vs leader-only component split

2. **Controller Service** (ClusterIP) — operational endpoints only
   - `:8080` → healthz probes and `/debug/*` introspection
   - `:9090` → Prometheus metrics
   - `:9443` → validating webhook

3. **HAProxy Deployment** (not StatefulSet) — scales horizontally
   - Each pod runs HAProxy + the Dataplane API as a sidecar, sharing the config volume
   - Ready pods are auto-discovered via `controller.config.podSelector`

4. **HAProxy Service** — NodePort by default; set `haproxy.service.type: LoadBalancer` for cloud providers
   - Service port 80 maps to HAProxy container port 8080, service port 443 maps to 8443

5. **HAProxyTemplateConfig CRD** — holds every piece of configuration the controller needs
   - Template bodies (`haproxyConfig`, `templateSnippets`, `maps`, `files`, `sslCertificates`)
   - `watchedResources` (what to subscribe to and how to index it)
   - Dataplane tuning (`minDeploymentInterval`, `driftPreventionInterval`, storage paths)
   - Validation tests shipped alongside the templates

6. **Credentials Secret** referenced by `spec.credentialsSecretRef` — holds Dataplane API usernames/passwords. Watched live, so rotations don't require a restart.

## Container Architecture

```mermaid
graph TB
    subgraph "Controller Pod"
        CTRL_MAIN[Controller Process<br/>:8080 healthz + /debug<br/>:9090 metrics<br/>:9443 webhook]
        CTRL_TMP[/tmp emptyDir<br/>haproxy -c validation]
    end

    subgraph "HAProxy Pod (Deployment member)"
        HAP_PROC[HAProxy Process<br/>:8080 HTTP<br/>:8443 HTTPS<br/>:8404 Stats]
        DP_PROC[Dataplane API<br/>:5555 API<br/>Unix master socket]
        HAP_VOL[Shared config emptyDir<br/>/etc/haproxy<br/>maps/, ssl/, files/]
    end

    HTPLCFG[HAProxyTemplateConfig CRD] -. watch .-> CTRL_MAIN
    CREDS_SECRET[Credentials Secret] -. watch .-> CTRL_MAIN
    CTRL_TMP --> CTRL_MAIN

    DP_PROC <-. master socket .-> HAP_PROC
    HAP_VOL --> HAP_PROC
    HAP_VOL --> DP_PROC

    style CTRL_MAIN fill:#4CAF50
    style HAP_PROC fill:#FF9800
    style DP_PROC fill:#FFB74D
```

**Resource Requirements** (chart defaults; see [Performance Guide](../../operations/performance.md) for sizing by ingress count):

Controller Pod:

- CPU request `100m` (no CPU limit — avoids throttling; GOMAXPROCS auto-derived)
- Memory `512Mi` request = limit (Guaranteed QoS; GOMEMLIMIT auto-derived via automemlimit)
- `/tmp` emptyDir for transient `haproxy -c` validation; root filesystem is read-only

HAProxy Pod:

- HAProxy container: tuned by the chart; the main consumer is connection buffers
- Dataplane API sidecar: `50m` CPU request, `256Mi` memory request = limit
- Shared config volume (`emptyDir`) mounted at `/etc/haproxy`; both containers read/write it

## Network Topology

```mermaid
graph LR
    subgraph "External Network"
        INET[Internet]
    end

    subgraph "Kubernetes Cluster Network"
        HAP_LB[HAProxy Service<br/>LoadBalancer<br/>External IP]
        CTRL_SVC_NET[Controller Service<br/>ClusterIP<br/>10.96.0.10]

        subgraph "Pod Network"
            subgraph "Controller Pod<br/>10.0.0.10"
                CTRL[Controller Process<br/>:8080, :9090]
            end

            subgraph "HAProxy Instances"
                subgraph "haproxy pod A<br/>10.0.1.10"
                    HAP1[HAProxy Process<br/>:8080, :8443, :8404]
                    DP1[Dataplane API<br/>:5555]
                end

                subgraph "haproxy pod B<br/>10.0.1.11"
                    HAP2[HAProxy Process<br/>:8080, :8443, :8404]
                    DP2[Dataplane API<br/>:5555]
                end
            end

            subgraph "Application Pods"
                APP1[app-pod-1<br/>10.0.2.10]
                APP2[app-pod-2<br/>10.0.2.11]
            end
        end

        KUBE_API[Kubernetes API<br/>443]
        PROM_NET[Prometheus]
    end

    INET --> HAP_LB
    HAP_LB --> HAP1
    HAP_LB --> HAP2

    CTRL_SVC_NET --> CTRL
    PROM_NET --> CTRL_SVC_NET

    CTRL --> KUBE_API
    CTRL --> DP1
    CTRL --> DP2

    HAP1 --> APP1
    HAP1 --> APP2
    HAP2 --> APP1
    HAP2 --> APP2

    DP1 -.API.-> HAP1
    DP2 -.API.-> HAP2

    style CTRL fill:#4CAF50
    style HAP1 fill:#FF9800
    style HAP2 fill:#FF9800
    style HAP_LB fill:#2196F3
    style CTRL_SVC_NET fill:#4CAF50
```

**Network Flow:**

1. **Ingress Traffic**: Internet → HAProxy Service (LoadBalancer) → HAProxy Pods → Application Pods
2. **Control Plane**: Controller → Kubernetes API (resource watching)
3. **Configuration Deployment**: Controller → Dataplane API endpoints (HTTP)
4. **Service Discovery**: Controller watches HAProxy pods via Kubernetes API
5. **Monitoring**: Prometheus → Controller Service (ClusterIP) → Controller Pod (metrics endpoint)
6. **Health Checks**: Kubernetes → Controller Service → Controller Pod (healthz endpoint)

**Scaling Considerations:**

- **HAProxy horizontal scaling**: `kubectl scale deployment <release>-haproxy --replicas=N` (or set `haproxy.replicaCount`). Pods are auto-discovered via `controller.config.podSelector`.
- **Controller horizontal scaling**: Run 2+ replicas with leader election (the chart default). Only the leader pushes configuration; followers are hot standby.
- **Resource sizing**: Scales with the number of watched resources and template complexity — see [Performance Guide](../../operations/performance.md) for per-size profiles.
- **Network topology**: Ensure LoadBalancer/NodePort can distribute traffic across all HAProxy replicas and that NetworkPolicy allows the controller to reach Dataplane API port 5555 on each HAProxy pod.
