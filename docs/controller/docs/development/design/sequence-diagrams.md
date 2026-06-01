# Sequence Diagrams

## Startup and Initialization

The controller uses a **reinitialization loop** pattern where it responds to configuration changes by restarting with the new configuration. Each iteration follows these initialization steps:

```mermaid
sequenceDiagram
    participant Main
    participant Iteration as runIteration()
    participant EventBus
    participant Components
    participant ResourceWatcher as Resource<br/>Watcher
    participant CRDSingleWatcher as CRD/Secret<br/>SingleWatcher
    participant Reconciler

    Main->>Main: Reinitialization Loop

    loop Until Context Cancelled
        Main->>Iteration: Run iteration

        Note over Iteration: 1. Fetch & Validate Initial Config
        Iteration->>Iteration: Fetch HAProxyTemplateConfig CRD & credentials Secret
        Iteration->>Iteration: Parse & Validate

        Note over Iteration,EventBus: 2. Setup Components
        Iteration->>EventBus: Create EventBus(100)
        Iteration->>Components: Start validators, loaders, commentator

        Note over Iteration,ResourceWatcher: 3. Setup Resource Watchers
        Iteration->>ResourceWatcher: Create & Start
        Iteration->>ResourceWatcher: WaitForAllSync()

        Note over Iteration,CRDSingleWatcher: 4. Setup CRD + Secret SingleWatchers
        Iteration->>CRDSingleWatcher: Create & Start
        Iteration->>CRDSingleWatcher: WaitForSync()

        Note over Iteration,EventBus: 5. Start EventBus
        Iteration->>EventBus: Start() (replay buffered events)

        Note over Iteration,Reconciler: Stage 5: Reconciliation & Observability Components
        Iteration->>Reconciler: Start Reconciler, Coordinator, DeploymentScheduler, Deployer, Discovery, ConfigPublisher, StatusApplier, Metrics
        Iteration->>EventBus: Publish initial ReconciliationTriggeredEvent

        Note over Iteration: 6. Event Loop
        Iteration->>Iteration: Wait for config change or cancellation

        alt Config Change Detected
            CRDSingleWatcher->>EventBus: ConfigValidatedEvent (new CRD spec)
            Iteration->>Iteration: Cancel iteration context
            Iteration-->>Main: Return nil (reinitialize)
        else Context Cancelled
            Iteration-->>Main: Return nil (shutdown)
        end
    end
```

**Reinitialization Loop Pattern:**

The controller runs iterations that respond to configuration changes:

1. **Initial Config Fetch**: Fetch and validate the `HAProxyTemplateConfig` CRD named by `--crd-name` (env `CRD_NAME`, default `haproxy-config`) and the credentials `Secret` referenced via `spec.credentialsSecretRef`, synchronously, before starting components.
2. **Component Setup**: Create EventBus and start config-management components (validators, loaders, commentator).
3. **Resource Watchers**: Create bulk watchers for every `spec.watchedResources` entry and wait for initial sync.
4. **Config/Secret SingleWatchers**: Create `pkg/k8s/watcher.SingleWatcher`s for the CRD and credentials Secret. These use immediate callbacks (no debouncing) so configuration updates reinitialize with no artificial delay.
5. **EventBus Start**: Call `EventBus.Start()` to replay buffered events and begin normal operation.
6. **Stage 5 — Reconciliation & Observability**: Start reconciliation components (Reconciler, Coordinator, DeploymentScheduler, Deployer, Discovery, ConfigPublisher, StatusApplier, DriftPreventionMonitor) and observability components (Metrics, Debug HTTP server). Rendering and three-phase HAProxy validation run synchronously inside `Pipeline.Execute` from the Coordinator's call stack (ADR-0001) — neither has its own goroutine or event subscription. The config validators (Basic, Template, JSONPath) are Stage 1 scatter-gather participants over `ConfigValidationRequest`, not Stage 5 components.
7. **Event Loop**: Wait for configuration changes or context cancellation.
8. **Reinitialization**: When the CRD or Secret changes, cancel the iteration context to stop all components, then restart with the new settings.

This pattern ensures the controller always operates with validated configuration and handles configuration updates by cleanly restarting with the new settings. The Stage 5 label is explicitly used in code for reconciliation components; earlier stages are implicit in the initialization sequence. Metrics collection starts in Stage 5 after `EventBus.Start()` to ensure all event subscriptions are registered before metrics begin tracking events.

## Resource Change Handling

```mermaid
sequenceDiagram
    participant K8S as Kubernetes API
    participant ResourceWatcher as Resource<br/>Watcher
    participant EventBus
    participant Reconciler as Reconciler<br/>(Debouncer)
    participant Coordinator as Coordinator<br/>(leader-only)
    participant Pipeline as Pipeline<br/>(synchronous)
    participant Scheduler as Deployment<br/>Scheduler<br/>(leader-only)
    participant Deployer as Deployer<br/>(leader-only)
    participant HAProxy1 as HAProxy<br/>Instance 1
    participant HAProxy2 as HAProxy<br/>Instance 2

    K8S->>ResourceWatcher: Resource update event
    ResourceWatcher->>ResourceWatcher: Update local index
    ResourceWatcher->>EventBus: Publish(ResourceIndexUpdatedEvent)

    EventBus->>Reconciler: ResourceIndexUpdatedEvent
    Note over Reconciler: Leading-edge: fire immediately<br/>if no recent reconciliation,<br/>otherwise batch within 5s window

    Reconciler->>EventBus: Publish(ReconciliationTriggeredEvent)

    EventBus->>Coordinator: ReconciliationTriggeredEvent
    Coordinator->>EventBus: Publish(ReconciliationStartedEvent)

    Coordinator->>Pipeline: Execute(ctx, storeProvider) — synchronous call
    Note over Pipeline: 1. RenderService.Render (templates → HAProxy config)<br/>2. ComputeContentChecksum<br/>3. ValidationService.Validate (syntax + schema + haproxy -c)
    Pipeline-->>Coordinator: *PipelineResult or *PipelineError

    alt Pipeline succeeded
        Coordinator->>EventBus: Publish(TemplateRenderedEvent)
        Coordinator->>EventBus: Publish(ValidationCompletedEvent)
    else Pipeline failed
        Coordinator->>EventBus: Publish(ReconciliationFailedEvent)
    end

    EventBus->>Scheduler: ValidationCompletedEvent (+ TemplateRenderedEvent + HAProxyPodsDiscoveredEvent)
    Note over Scheduler: Wait for all three inputs<br/>Apply min interval / latest-wins
    Scheduler->>EventBus: Publish(DeploymentScheduledEvent)

    EventBus->>Deployer: DeploymentScheduledEvent
    Deployer->>EventBus: Publish(DeploymentStartedEvent)

    par Parallel Deployment
        Deployer->>HAProxy1: dataplane.Sync via Dataplane API
        HAProxy1-->>Deployer: Success
        Deployer->>EventBus: Publish(InstanceDeployedEvent)
    and
        Deployer->>HAProxy2: dataplane.Sync via Dataplane API
        HAProxy2-->>Deployer: Success
        Deployer->>EventBus: Publish(InstanceDeployedEvent)
    end

    Deployer->>EventBus: Publish(DeploymentCompletedEvent)
    EventBus->>Coordinator: DeploymentCompletedEvent
    Coordinator->>EventBus: Publish(ReconciliationCompletedEvent)
```

**Event-Driven Flow:**

1. **Resource Change**: ResourceWatcher receives Kubernetes events, updates the local index, and coalesces bursts within a per-resource debounce window before publishing one `ResourceIndexUpdatedEvent` per quiet window. The window defaults to 2 s (`pkg/k8s/types.DefaultDebounceInterval`); each watched resource can override it via `spec.watchedResources.<name>.debounceInterval` (the bundled chart sets `"0"` on EndpointSlice). This is the only debounce layer.
2. **Reconciliation Trigger**: Reconciler publishes `ReconciliationTriggeredEvent` immediately on every event it receives — there is no second reconciler-level refractory window. Whole-store events (`IndexSynchronizedEvent`, `BecameLeaderEvent`, `DriftPreventionTriggeredEvent`) trigger the same way; only the initial-sync variant of `ResourceIndexUpdatedEvent` is filtered out. Reload throttling happens downstream in the deployer's `minDeploymentInterval`.
3. **Coordinator (leader-only)**: subscribes to `ReconciliationTriggeredEvent`, publishes `ReconciliationStartedEvent`, then calls `pkg/controller/pipeline.Pipeline.Execute(ctx, storeProvider)` **synchronously** — render and validation are one atomic step, not a multi-hop event chain.
4. **Pipeline**: runs `RenderService.Render` (template engine + auxiliary files), computes the content checksum once, then runs `ValidationService.Validate` (syntax via client-native parser → OpenAPI schema → `haproxy -c` semantic).
5. **Coordinator post-pipeline**: on success, publishes `TemplateRenderedEvent` + `ValidationCompletedEvent` for downstream consumers; on failure, publishes `ReconciliationFailedEvent` carrying a `*PipelineError` (use `errors.AsType[*PipelineError]` to extract the failed phase).
6. **DeploymentScheduler (leader-only)**: subscribes to `TemplateRenderedEvent`, `ValidationCompletedEvent`, and `HAProxyPodsDiscoveredEvent`; only deploys when all three inputs are present. Enforces `minDeploymentInterval` and "latest wins" coalescing.
7. **Deployer (leader-only)**: executes parallel `dataplane.Sync` calls to every endpoint, publishes per-endpoint `InstanceDeployedEvent` / `InstanceDeploymentFailedEvent` and aggregate `DeploymentCompletedEvent`.
8. **Completion**: Coordinator subscribes to `DeploymentCompletedEvent` and publishes `ReconciliationCompletedEvent` with duration metrics.

There is no event-adapter for rendering or HAProxy-config validation — the synchronous Pipeline owns both. Coordination still happens entirely via EventBus pub/sub *between* components; only the render-validate split inside the Coordinator is a direct function call.

## Configuration Validation Process

This is the inside view of step 4 in the previous diagram — `Pipeline.Execute` runs `ValidationService.Validate` after rendering, all inside the leader-only Coordinator's call stack:

```mermaid
sequenceDiagram
    participant Coord as Coordinator<br/>(leader-only)
    participant Pipeline
    participant Render as RenderService
    participant Validate as ValidationService
    participant Parser as client-native Parser
    participant Schema as OpenAPI Schema
    participant Binary as haproxy Binary

    Coord->>Pipeline: Execute(ctx, storeProvider)
    Pipeline->>Render: Render(ctx, storeProvider)
    Render-->>Pipeline: *RenderResult (config + aux files)

    Pipeline->>Pipeline: ComputeContentChecksum(config, aux)
    Pipeline->>Validate: ValidateWithChecksum(ctx, config, aux, checksum)
    Note over Validate: Per-instance cache (cacheMu, RWMutex)<br/>checksum hit → return cached parsed config

    Validate->>Validate: os.MkdirTemp("", "haproxy-validation-*")
    Note over Validate: Per-call sandbox — every Validate gets<br/>its own /tmp/<unique>; file I/O is per-call<br/>but haproxy -c binary is serialised by<br/>a package-global haproxyCheckMutex

    Validate->>Parser: validateSyntax(config)
    alt Syntax error
        Parser-->>Validate: error
        Validate-->>Pipeline: ValidationResult{Valid:false, Phase:"syntax"}
    else
        Parser-->>Validate: *parser.StructuredConfig
        Validate->>Schema: validateAPISchema(parsed, version)
        alt Schema error
            Schema-->>Validate: error
            Validate-->>Pipeline: ValidationResult{Valid:false, Phase:"schema"}
        else
            Schema-->>Validate: ok
            Validate->>Binary: haproxy -c -f /tmp/<unique>/haproxy.cfg
            alt Semantic error
                Binary-->>Validate: exit 1 + stderr
                Validate-->>Pipeline: ValidationResult{Valid:false, Phase:"semantic"}
            else
                Binary-->>Validate: exit 0
                Validate->>Validate: cacheResult(checksum, parsedConfig)
                Validate-->>Pipeline: ValidationResult{Valid:true, ParsedConfig:...}
            end
        end
    end

    Pipeline-->>Coord: *PipelineResult or *PipelineError
```

**Validation Steps:**

1. **Pipeline call**: `Coordinator.handleReconciliationTriggered` calls `Pipeline.Execute` synchronously. The pipeline first renders, then validates — both in the same call stack, no event hop.
2. **Cache check**: `ValidationService` keys its per-instance cache on a SHA-256 of `(config + aux files)` (`pkg/dataplane.ComputeContentChecksum`). Identical content during drift-prevention cycles returns the cached `*parser.StructuredConfig` without running any phase. Failures are *not* cached — every failure retries.
3. **Sandbox**: each `Validate` call creates its own `os.MkdirTemp("", "haproxy-validation-*")` and rewrites the rendered config's `default-path origin` to point at it. File I/O is fully isolated per call. The `haproxy -c` binary invocation itself is still serialised by a package-global `haproxyCheckMutex` (`pkg/dataplane/validate_haproxy.go`) because concurrent runs of the binary have been observed to interfere even with isolated sandboxes; on top of that, a small per-instance `cacheMu` (`sync.RWMutex`) guards the cached `*parser.StructuredConfig` lookup.
4. **Phase 1 — Syntax**: client-native parser checks grammar and section structure. Cheap.
5. **Phase 1.5 — OpenAPI schema**: parsed structure cross-checked against the version-specific Dataplane API OpenAPI spec via `pkg/generated/validators`. Catches out-of-range values, pattern violations, missing required fields. Also cheap (in-memory, no fork).
6. **Phase 2 — Semantic**: writes the config + auxiliary files into a per-call temp directory, runs `haproxy -c -f <tempdir>/haproxy.cfg`, parses the binary's stderr on failure. The temp directory mirrors the production layout (`maps/`, `ssl/`, `general/`) under `default-path origin <tempdir>` so file references resolve exactly like at runtime.
7. **Result**: Pipeline wraps the `ValidationResult` into a `*PipelineResult` (success) or `*PipelineError` (failure carrying `Phase` for `errors.AsType[*PipelineError]`); the Coordinator then publishes `ValidationCompletedEvent` or `ReconciliationFailedEvent` accordingly.

## Zero-Reload Deployment Strategy

```mermaid
sequenceDiagram
    participant Deployer as Deployer<br/>(pkg/controller/deployer)
    participant Client as dataplane.Client
    participant DP as Dataplane API
    participant HAProxy

    Deployer->>Client: Sync(ctx, desiredConfig, auxFiles, opts)
    Client->>DP: GET /v3/services/haproxy/configuration/raw
    DP-->>Client: Current config

    Client->>Client: Parse + comparator.Compare

    alt Only runtime changes
        Note over Client: Server weight/address/port/maintenance only
        Client->>DP: PUT /v3/services/haproxy/runtime/servers/{name}
        DP->>HAProxy: Runtime API command
        HAProxy-->>DP: Updated
        DP-->>Client: Success (no reload)
    else Mixed changes
        Note over Client: Runtime-supported + config changes
        Client->>DP: PUT /v3/services/haproxy/runtime/servers/{name}
        Client->>DP: POST /v3/services/haproxy/transactions
        DP->>HAProxy: Apply config
        HAProxy-->>DP: Reload triggered
        DP-->>Client: Success (reload)
    else Structural changes
        Note over Client: Backends, frontends, binds, ACLs, etc.
        Client->>DP: POST /v3/services/haproxy/transactions
        DP->>HAProxy: Replace config + reload
        HAProxy-->>DP: Reload complete
        DP-->>Client: Success (reload)
    end

    Client-->>Deployer: *SyncResult
```

**Deployment Optimization:**

`pkg/dataplane.Client.Sync` analyses configuration changes to determine the optimal deployment strategy:

1. **Runtime-Only Updates**: Server weight/address/port/maintenance changes → no reload
2. **Mixed Updates**: Apply runtime-supported changes first, then transactional config changes → single reload
3. **Structural Updates**: Backend/frontend/bind/ACL changes → transactional commit → reload required

The Dataplane API itself decides per-field whether a change is runtime-eligible (see `dataplaneapi/handlers/runtime.go`), so the controller delegates that decision rather than maintaining its own table. This minimises service disruption by avoiding unnecessary HAProxy process reloads.
