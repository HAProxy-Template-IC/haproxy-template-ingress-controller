# Architecture Overview

This page maps the controller's runtime components and how a Kubernetes change flows through rendering, validation, and deployment. For what HAPTIC is and why, see the [design landing page](../design.md).

**Operational Model:**

The controller operates through event-driven coordination, with one synchronous service inside the leader: rendering and validation are *not* a multi-hop event chain, they're a single Pipeline call.

1. **Resource Watchers** (`pkg/k8s/watcher`, all-replica) monitor Kubernetes resources and publish `ResourceIndexUpdatedEvent` / `IndexSynchronizedEvent` to EventBus
2. **Reconciler** (`pkg/controller/reconciler.Reconciler`, all-replica) subscribes to those events and publishes `ReconciliationTriggeredEvent` immediately on every one — no reconciler-level debounce (also fires immediately on `BecameLeaderEvent` to bootstrap the new leader). Coalescing of bursts is done upstream in the per-watcher debounce window; reload throttling is done downstream in the deployer.
3. **Coordinator** (`pkg/controller/reconciler.Coordinator`, **leader-only**) subscribes to `ReconciliationTriggeredEvent`, calls `Pipeline.Execute` synchronously — no event hop. The pipeline runs `RenderService.Render` + the fast `ValidationService.Validate` (client-native parser syntax + OpenAPI schema; the `haproxy -c` semantic phase is skipped on the leader path — it already ran at admission via the `haproxytemplateconfigs` webhook, and the Dataplane API re-validates on push) in one shot. The Coordinator then publishes `TemplateRenderedEvent` + `ValidationCompletedEvent` for downstream observers, and finally `ReconciliationCompletedEvent` (or `ReconciliationFailedEvent`) for metrics/commentator.
4. **DeploymentScheduler** (`pkg/controller/deployer.DeploymentScheduler`, **leader-only**) subscribes to those events plus `HAProxyPodsDiscoveredEvent`, enforces rate limiting (`minDeploymentInterval`), implements latest-wins coalescing, and publishes `DeploymentScheduledEvent`
5. **Deployer** (`pkg/controller/deployer.Component`, **leader-only**) subscribes to `DeploymentScheduledEvent`, executes parallel `dataplane.Sync` calls against every HAProxy endpoint, and publishes `DeploymentCompletedEvent` plus per-endpoint `InstanceDeployedEvent` / `InstanceDeploymentFailedEvent`
6. **All-replica observers** (Discovery, StatusApplier, ProposalValidator, HTTPStore, Metrics, Commentator) subscribe to relevant events for their specific purposes and either react locally or — if leader-only writes are involved — let the leader-only sister component pick up the work

There is no event-adapter for rendering or HAProxy-config validation in production: the leader's synchronous `pkg/controller/pipeline.Pipeline` runs `RenderService` + three-phase `ValidationService` in one shot, with no event hop between them.

**Key Design Principles:**

- **Fail-Safe**: Invalid configurations are rejected before reaching production
- **Performance**: Debouncing prevents rapid successive renders, indexing enables fast lookups
- **Observability**: Prometheus metrics, structured logging, and a `/debug/vars` introspection endpoint
- **Flexibility**: Templates provide complete control over HAProxy configuration, no annotation limitations

## Component Diagrams

### High-Level System Components

```mermaid
graph TB
    subgraph "Kubernetes Cluster"
        K8S[Kubernetes API Server]

        subgraph "Controller Pod"
            CTRL[Controller<br/>- Resource Watching<br/>- Template Rendering<br/>- Config Validation<br/>- Deployment Orchestration]
            VAL[Validation Module<br/>- client-native Parser<br/>- haproxy Binary Check]
        end

        subgraph "HAProxy Pod 1"
            HAP1[HAProxy<br/>Load Balancer]
            DP1[Dataplane API<br/>:5555]
        end

        subgraph "HAProxy Pod 2"
            HAP2[HAProxy<br/>Load Balancer]
            DP2[Dataplane API<br/>:5555]
        end

        CONFIG[HAProxyTemplateConfig CRD<br/>Controller Configuration]
        RES[Resources<br/>Ingress, Service, etc.]
    end

    K8S -->|Watch Events| CTRL
    CONFIG -->|Watch + Read| CTRL
    RES -->|Watch Events| CTRL
    CTRL -->|Render & Validate| VAL
    VAL -->|Deploy Config| DP1
    VAL -->|Deploy Config| DP2
    DP1 -->|Configure| HAP1
    DP2 -->|Configure| HAP2
    HAP1 -->|Stats/Health| DP1
    HAP2 -->|Stats/Health| DP2

    style CTRL fill:#4CAF50
    style VAL fill:#2196F3
    style HAP1 fill:#FF9800
    style HAP2 fill:#FF9800
```

**Component Descriptions:**

- **Controller**: Main controller process that watches Kubernetes resources, renders templates, and orchestrates configuration deployment
- **Validation Module**: Integrated validation using haproxytech/client-native library for parsing and haproxy binary for configuration checks
- **Dataplane API**: HAProxy's management interface for receiving configuration updates and performing runtime operations
- **HAProxy**: Production load balancer instances that serve traffic

### Controller Internal Architecture

```mermaid
graph TB
    subgraph ext["External Systems"]
        K8S["Kubernetes API<br/>(Resource Events)"]
        HAP["HAProxy Instances<br/>(Dataplane API)"]
    end

    subgraph controller["Controller Process - Event-Driven Architecture"]
        direction TB

        EB["EventBus<br/>Central Pub/Sub Coordinator<br/>~50 Event Types"]

        subgraph watchers["Resource Watchers"]
            direction LR
            CW["Config<br/>Watcher"]
            RW["Resource<br/>Watcher"]
        end

        subgraph reconciliation["Reconciliation Components"]
            direction LR
            RC["Reconciler<br/>(immediate fire)"]
            COORD["Coordinator<br/>(leader-only<br/>pipeline driver)"]
        end

        subgraph pipeline["Synchronous Pipeline (no event hop)"]
            direction LR
            REND["RenderService"]
            VAL["ValidationService<br/>(syntax + schema<br/>+ haproxy -c)"]
        end

        subgraph deploy["Event-Driven Deployment"]
            direction LR
            SCHED["Deployment<br/>Scheduler"]
            DEPL["Deployer"]
        end

        subgraph support["Support Components"]
            direction LR
            DISC["Discovery"]
            METR["Metrics"]
            COMM["Commentator"]
        end

        CW & RW -->|Publish| EB
        EB -->|Subscribe| RC
        RC -->|Publish| EB
        EB -->|Subscribe| COORD
        COORD -.->|direct call| REND
        REND -.->|return| COORD
        COORD -.->|direct call| VAL
        VAL -.->|return| COORD
        COORD -->|Publish| EB
        EB -->|Subscribe| SCHED
        SCHED -->|Publish| EB
        EB -->|Subscribe| DEPL
        DEPL -->|Publish| EB
        EB -->|Subscribe| DISC & METR & COMM
        DISC -->|Publish| EB
    end

    K8S -->|Watch| RW
    DEPL -->|Deploy| HAP

    style EB fill:#FFC107,stroke:#F57C00,stroke-width:4px
    style watchers fill:#E3F2FD
    style reconciliation fill:#F3E5F5
    style pipeline fill:#C8E6C9
    style deploy fill:#C8E6C9
    style support fill:#FFF9C4
    style ext fill:#F5F5F5
```

The dashed arrows between Coordinator and the synchronous pipeline are direct function calls — there is no event hop for rendering or HAProxy validation. This synchronous render-validate design is recorded as an Architecture Decision Record (ADR); see [Design Decisions](design-decisions.md#event-driven-architecture) for the rationale. The Coordinator publishes `TemplateRenderedEvent` and `ValidationCompletedEvent` itself once the synchronous call returns.

**Event-Driven Data Flow:**

1. **Config/Resource Watchers** receive Kubernetes changes, coalesce bursts within a per-resource debounce window (default 2s, overridable via `spec.watchedResources.<name>.debounceInterval`; the bundled chart sets `"0"` on EndpointSlice), and publish one event per quiet window to the EventBus. This is the only debounce layer.
2. **Reconciler** subscribes to change events, filters initial sync events, and publishes `ReconciliationTriggeredEvent` immediately on every change — there is no second reconciler-level debounce or refractory window. Also fires on `BecameLeaderEvent` so a freshly-elected leader produces a current render instead of waiting for the next change.
3. **Coordinator** (leader-only) subscribes to `ReconciliationTriggeredEvent` and calls `pkg/controller/pipeline.Pipeline.Execute(ctx, storeProvider)` synchronously. The pipeline runs `RenderService.Render` + the fast `ValidationService.Validate` (syntax + schema) in one atomic step. On success, the Coordinator publishes `TemplateRenderedEvent` + `ValidationCompletedEvent`; on failure, `ReconciliationFailedEvent` carrying a `*PipelineError` (use `errors.AsType[*PipelineError]` to extract the failed phase, as the Coordinator does in `handlePipelineFailure`). Either path ends with `ReconciliationCompletedEvent` for metrics.
4. **DeploymentScheduler** (leader-only) subscribes to `TemplateRenderedEvent`, `ValidationCompletedEvent`, `HAProxyPodsDiscoveredEvent`, and `ConfigValidatedEvent`; enforces rate limiting (default 2s minimum interval), implements "latest wins" queueing, publishes `DeploymentScheduledEvent`
5. **Deployer** (leader-only) subscribes to `DeploymentScheduledEvent`, executes parallel `dataplane.Sync` calls to all HAProxy endpoints, publishes `InstanceDeployedEvent` / `InstanceDeploymentFailedEvent` per endpoint and `DeploymentCompletedEvent` overall
6. **Discovery** (all-replica) probes HAProxy pods, caches `HAProxyPodsDiscoveredEvent` via `leadership.StateReplayer` so the next leader gets current state on `BecameLeaderEvent`
7. **ConfigPublisher** (leader-only) subscribes to `TemplateRenderedEvent` + `ValidationCompletedEvent`, writes the rendered config + auxiliary files as observable CRDs (`HAProxyCfg`, `HAProxyMapFile`, …)
8. **Support Components** (Metrics, Commentator, StatusApplier) subscribe to relevant events for metrics / logs / status patches

**Key Architecture Properties:**

- **EventBus** is the single coordination mechanism - zero direct component-to-component function calls
- **Event-Driven Components** (Reconciler, Coordinator, Scheduler, Deployer, ConfigPublisher, Discovery, …) wrap pure libraries (pkg/templating, pkg/dataplane, pkg/k8s) in event adapters; the rendering and HAProxy-validation services they call are themselves *not* event-adapter components — they're synchronous services driven from inside Coordinator's `Pipeline.Execute` (see [Design Decisions](design-decisions.md#event-driven-architecture))
- **Pure Libraries** (pkg/templating, pkg/dataplane, pkg/k8s) contain testable business logic with no event dependencies
- **Event Adapters** translate between EventBus pub/sub and pure library function calls
- **Extensibility** - new features can subscribe to existing events without modifying existing code
- **Independent Testing** - pure libraries can be unit tested, event adapters can be integration tested

### Validation Flow

```mermaid
graph TD
    RENDER[Rendered Configuration]
    PARSE[client-native Parser<br/>Syntax & Structure Check]
    SCHEMA[OpenAPI Schema Check<br/>Field Patterns & Ranges]
    BIN[haproxy Binary<br/>Semantic Validation]
    DEPLOY[Deploy to Production]
    ERROR[Reject & Log Error]

    RENDER --> PARSE
    PARSE -->|Valid Syntax| SCHEMA
    PARSE -->|Invalid| ERROR
    SCHEMA -->|Schema OK| BIN
    SCHEMA -->|Invalid| ERROR
    BIN -->|Valid Semantics| DEPLOY
    BIN -->|Invalid| ERROR

    style PARSE fill:#2196F3
    style SCHEMA fill:#9C27B0
    style BIN fill:#4CAF50
    style DEPLOY fill:#FF9800
    style ERROR fill:#F44336
```

**Validation Strategy:**

Three phases run in-process, eliminating the need for a separate validation sidecar container:

1. **Phase 1 — Syntax parsing.** client-native parses the configuration and validates it against the HAProxy config grammar.
2. **Phase 1.5 — OpenAPI schema check.** The parsed structure is cross-checked against the version-specific DataPlane API OpenAPI spec — catches out-of-range values, pattern violations, and missing required fields before they reach HAProxy.
3. **Phase 2 — Semantic validation.** `haproxy -c -f config` performs full semantic validation including resource availability. Each call creates a per-process temp directory mirroring the production layout (`maps/`, `ssl/`, `general/`), writes the auxiliary files there, and rewrites the rendered config's `default-path origin <baseDir>` line to point at the temp dir — so file references resolve exactly like at runtime. File I/O is fully isolated per call, but the actual `haproxy -c` invocation is still serialised by a global `haproxyCheckMutex` (`pkg/dataplane/validate_haproxy.go`) because concurrent binary invocations have been observed to interfere with each other.

Results are cached by an SHA-256 over (config + auxiliary files) per instance — repeat validations during drift-prevention cycles short-circuit before touching disk. This provides the same guarantees as a full HAProxy instance.

Two service instances are wired (`pkg/controller/reconciliation.go`): the **strict** one (all three phases) serves the admission webhook and HTTP-store promotion, so invalid input never reaches the leader; the leader's reconcile pipeline uses the **fast** instance (`SkipSemanticValidation: true`), which runs only Phases 1 and 1.5 — semantic validity was already enforced at admission, and the Dataplane API re-checks server-side on every push.
