# Deployment diagrams

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
                    HAP1[HAProxy<br/>:80, :443, :8404]
                    DP1[HAPTIC agent<br/>:5555]
                end
                subgraph "haproxy pod B"
                    HAP2[HAProxy<br/>:80, :443, :8404]
                    DP2[HAPTIC agent<br/>:5555]
                end
            end

            HAP_SVC[HAProxy Service<br/>NodePort by default<br/>:80 → :80<br/>:443 → :443]
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

```

**Deployment Components:**

1. **Controller Deployment** — defaults to 2 replicas with leader election
    - All replicas watch Kubernetes resources, run admission webhooks, and discover HAProxy pods (hot standby — keeps caches warm so failover is instant)
    - Only the elected leader runs the render-validate Pipeline and applies configuration through each pod's HAPTIC agent
    - See [High Availability](../../operations/high-availability.md) for tuning failover and [Leader Election](./leader-election.md) for the full all-replica vs leader-only component split

2. **Controller Service** (ClusterIP) — operational endpoints only
    - `:8080` → healthz probes and `/debug/*` introspection
    - `:9090` → Prometheus metrics
    - `:9443` → validating webhook

3. **HAProxy Deployment** (not StatefulSet) — scales horizontally
    - Each pod runs HAProxy plus the HAPTIC agent, which owns the config volume and HAProxy's runtime socket
    - Pods are auto-discovered via `controller.config.podSelector`; a pod is admitted once it has an IP, its `agent` container is running, and its `GET /v1/state` answers

4. **HAProxy Service** — NodePort by default; set `haproxy.service.type: LoadBalancer` for cloud providers
    - Service port 80 maps to HAProxy container port 80, service port 443 maps to 443 (the chart binds HAProxy on the literal 80/443; set `haproxy.ports.http`/`https` to override)

5. **HAProxyTemplateConfig CRD** — holds every piece of configuration the controller needs
    - Template bodies (`haproxyConfig`, `templateSnippets`, `maps`, `files`, `sslCertificates`)
    - `watchedResources` (what to subscribe to and how to index it)
    - Apply tuning (`minDeploymentInterval`, `driftPreventionInterval`, `syncTimeout`, storage paths)
    - Validation tests shipped alongside the templates

6. **Credentials Secret** referenced by `spec.credentialsSecretRef` — holds the agent's basic-auth username and password. Watched live, so rotations don't require a restart.

## Container Architecture

```mermaid
graph TB
    subgraph "Controller Pod"
        CTRL_MAIN[Controller Process<br/>:8080 healthz + /debug<br/>:9090 metrics<br/>:9443 webhook]
        CTRL_TMP["/tmp emptyDir<br/>haproxy -c validation"]
    end

    subgraph "HAProxy Pod (Deployment member)"
        HAP_PROC[HAProxy Process<br/>:80 HTTP<br/>:443 HTTPS<br/>:8404 Stats]
        DP_PROC[HAPTIC agent<br/>:5555 API<br/>Unix master + worker sockets]
        HAP_VOL[Shared config emptyDir<br/>/etc/haproxy<br/>maps/, ssl/, general/]
    end

    HTPLCFG[HAProxyTemplateConfig CRD] -. watch .-> CTRL_MAIN
    CREDS_SECRET[Credentials Secret] -. watch .-> CTRL_MAIN
    CTRL_TMP --> CTRL_MAIN

    DP_PROC <-. master socket .-> HAP_PROC
    HAP_VOL --> HAP_PROC
    HAP_VOL --> DP_PROC

```

**Resource Requirements**: chart defaults, the sizing table, and the GOMAXPROCS/GOMEMLIMIT container-awareness mechanics live in [Performance — Controller Resource Sizing](../../operations/performance.md#controller-resource-sizing). Diagram-relevant specifics: the controller writes transient `haproxy -c` validation files to a `/tmp` emptyDir (root filesystem is read-only), and both HAProxy-pod containers share the config `emptyDir` mounted at `/etc/haproxy`.

## Network topology

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
                    HAP1[HAProxy Process<br/>:80, :443, :8404]
                    DP1[HAPTIC agent<br/>:5555]
                end

                subgraph "haproxy pod B<br/>10.0.1.11"
                    HAP2[HAProxy Process<br/>:80, :443, :8404]
                    DP2[HAPTIC agent<br/>:5555]
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

```

**Network Flow:**

1. **Ingress Traffic**: Internet → HAProxy Service → HAProxy Pods → Application Pods (the diagram shows the `haproxy.service.type: LoadBalancer` variant; the chart default is NodePort)
2. **Control Plane**: Controller → Kubernetes API (resource watching)
3. **Configuration Deployment**: Controller → each pod's agent (HTTP `POST /v1/apply`)
4. **Service Discovery**: Controller watches HAProxy pods via Kubernetes API
5. **Monitoring**: Prometheus → Controller Service (ClusterIP) → Controller Pod (metrics endpoint)
6. **Health Checks**: Kubernetes → Controller Service → Controller Pod (healthz endpoint)

**Scaling Considerations**: HAProxy scales horizontally via `haproxy.replicaCount` (pods are auto-discovered through `controller.config.podSelector`); the controller scales for availability, not throughput — see [Performance — Scaling Strategies](../../operations/performance.md#scaling-strategies) and [High Availability](../../operations/high-availability.md). NetworkPolicy must allow the controller to reach the agent port 5555 on each HAProxy pod ([Networking](../../operations/networking.md)).

## How one deployment reaches a pod

The controller never pushes a configuration and diffs it back. Each render
produces an immutable plan (`pkg/dataplane/renderplan`) describing the sections,
the backend records and the file set it emitted; the deploy side compares that
plan with what each pod reports and sends the difference.

```mermaid
sequenceDiagram
    participant S as DeploymentScheduler
    participant D as Deployer
    participant A as HAPTIC agent (pod)
    participant H as HAProxy

    S->>D: DeploymentScheduledEvent (config + plan)
    D->>A: GET /v1/state
    A-->>D: applied/running plan ids, file digests, inventory, HAProxy version
    Note over D: deployplan.Diff(render, baseline)
    D->>A: POST /v1/apply (manifest + changed files + ops)
    A->>H: runtime commands, or write + reload
    A-->>D: ACK: applied/running plan ids, mode, op results
```

**Per pod, per deployment:**

1. `GET /v1/state` reports the plan the pod applied, the plan its worker runs,
   the digest of every file it holds, its runtime inventory and its HAProxy
   version. The drift pass asks for `?verify=1`, which re-hashes the tree, so a
   file changed behind the controller's back shows up as a digest difference.
2. The baseline is that applied plan. The controller keeps the plans the fleet
   still refers to; on a miss it decodes the opaque blob the pod stored, which
   is what makes a leader change cost no reload. A blob it cannot vouch for —
   foreign schema version, wrong plan id — is no baseline at all.
3. `deployplan.Diff` compares the render with the baseline and answers
   `runtime`, `file_only` or `reload`, with the reasons for each change it could
   not take at runtime. Pods reporting the same baseline share one answer.
4. The manifest carries the complete desired file set at digest granularity and
   a part only for a file the agent does not already hold; `haproxy.cfg` always
   travels whole. Ops beyond `api.MaxOpsPerApply` are split into chunks, each
   fenced on what the previous chunk applied.
5. The ACK reports what the pod applied and what it runs. Both land in
   `HAProxyCfg.status.deployedToPods[]`, together with the mode and the reasons.

At most 16 pods are applied to concurrently, each bounded by `syncTimeout`.

**Fencing.** Every apply carries a token: the leader epoch — a counter on the
leader Lease that each leadership term claims before it dispatches — and a
per-term apply sequence. The agent accepts an apply only when the baseline it
names is the one the pod has and the epoch is not older than the one it last
accepted. The three refusals:

| 409 reason | What it means | What the controller does |
|---|---|---|
| `prev_mismatch` | the pod's applied plan moved | re-read its state and diff again, once |
| `unknown_baseline` | the pod dropped its baseline | send the complete file set with `mode: reload` |
| `stale_epoch` | a newer leader owns the fleet | stand down; losing the epoch race is losing leadership |

A `409` listing missing file parts is answered by resending exactly those files.

**Refusals.** An apply the agent judged and refused (a NACK) counts
`haptic_apply_rejected_total{pod}`, reports HAProxy's own words through the
pod's status, and drops that pod's baseline so its next apply is the complete
state plus a reload. An agent speaking a different API major, or missing an op
kind the controller composes, gets the same treatment plus
`haptic_agent_version_skew_total` — never a refusal, because a fleet-correlated
refusal would fence the repair path.

**Convergence.** A deployment's `Succeeded` count is the pods *running* the
render: `applied_plan_id == desired`, the apply was accepted, and no reload is
pending. A pod whose paced reload is still scheduled has the files on disk but
does not serve them, so it is neither converged nor a failure.

**Pacing.** Reload pacing belongs to the agent (`--reload-interval-min`), which
coalesces reloads without holding back applies that need none. The scheduler
keeps one deployment in flight at a time and a single latest-wins pending slot,
so a burst of renders collapses into one follow-up deployment.

## Build optimizations (contributors)

Controller images are built with Go's Profile-Guided Optimization (PGO), which typically provides 2-7% CPU improvement by optimizing frequently called functions. A baseline CPU profile (`cmd/haptic/default.pgo`) is committed to the repository; Go automatically uses it during builds to optimize hot paths.

**Updating the profile** from the development environment:

1. Start the dev environment:

    ```bash
    ./scripts/start-dev-env.sh
    ```

2. Port-forward to the controller's debug port:

    ```bash
    kubectl -n haptic port-forward deploy/haptic-controller 8080:8080
    ```

3. Generate workload (trigger reconciliation by modifying resources).

4. Collect a 30-second CPU profile:

    ```bash
    make pgo-profile
    # Or manually:
    curl -o cmd/haptic/default.pgo http://localhost:8080/debug/pprof/profile?seconds=30
    ```

5. Rebuild with the new profile:

    ```bash
    make build
    ```

For optimal results, collect profiles from production during representative workloads and merge multiple profiles for broader coverage:

```bash
make pgo-merge PROFILES='profile1.pgo profile2.pgo'
```
