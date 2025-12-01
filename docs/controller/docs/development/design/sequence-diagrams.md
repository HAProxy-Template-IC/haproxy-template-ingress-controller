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
    participant ConfigWatcher as Config<br/>Watcher
    participant Reconciler

    Main->>Main: Reinitialization Loop

    loop Until Context Cancelled
        Main->>Iteration: Run iteration

        Note over Iteration: 1. Fetch & Validate Initial Config
        Iteration->>Iteration: Fetch ConfigMap & Secret
        Iteration->>Iteration: Parse & Validate

        Note over Iteration,EventBus: 2. Setup Components
        Iteration->>EventBus: Create EventBus(100)
        Iteration->>Components: Start validators, loaders, commentator

        Note over Iteration,ResourceWatcher: 3. Setup Resource Watchers
        Iteration->>ResourceWatcher: Create & Start
        Iteration->>ResourceWatcher: WaitForAllSync()

        Note over Iteration,ConfigWatcher: 4. Setup Config/Secret Watchers
        Iteration->>ConfigWatcher: Create & Start
        Iteration->>ConfigWatcher: WaitForSync()

        Note over Iteration,EventBus: 5. Start EventBus
        Iteration->>EventBus: Start() (replay buffered events)

        Note over Iteration,Reconciler: Stage 5: Reconciliation & Observability Components
        Iteration->>Reconciler: Start Reconciler, Renderer, Validator, Executor, Deployer, Discovery, Metrics
        Iteration->>EventBus: Publish initial ReconciliationTriggeredEvent

        Note over Iteration: 6. Event Loop
        Iteration->>Iteration: Wait for config change or cancellation

        alt Config Change Detected
            ConfigWatcher->>EventBus: ConfigValidatedEvent (new config)
            Iteration->>Iteration: Cancel iteration context
            Iteration-->>Main: Return nil (reinitialize)
        else Context Cancelled
            Iteration-->>Main: Return nil (shutdown)
        end
    end
```

**Reinitialization Loop Pattern:**

The controller runs iterations that respond to configuration changes:

1. **Initial Config Fetch**: Fetch and validate ConfigMap and Secret synchronously before starting components
2. **Component Setup**: Create EventBus and start config management components (validators, loaders, commentator)
3. **Resource Watchers**: Create watchers for configured resources and wait for initial sync
4. **Config Watchers**: Create watchers for ConfigMap and Secret, wait for sync
5. **EventBus Start**: Call EventBus.Start() to replay buffered events and begin normal operation
6. **Stage 5 - Reconciliation & Observability**: Start reconciliation components (Reconciler, Renderer, Validator, Executor, Deployer, Discovery) and observability components (Metrics, Debug HTTP servers)
7. **Event Loop**: Wait for configuration changes or context cancellation
8. **Reinitialization**: When config changes, cancel iteration context to stop all components, then restart with new config

This pattern ensures the controller always operates with validated configuration and handles configuration updates by cleanly restarting with the new settings. The Stage 5 label is explicitly used in code for reconciliation components; earlier stages are implicit in the initialization sequence. Metrics collection starts in Stage 5 after EventBus.Start() to ensure all event subscriptions are properly registered before metrics begin tracking events.

## Resource Change Handling

```mermaid
sequenceDiagram
    participant K8S as Kubernetes API
    participant ResourceWatcher as Resource<br/>Watcher
    participant EventBus
    participant Reconciler as Reconciler<br/>(Debouncer)
    participant Executor
    participant Renderer as Renderer
    participant Validator as HAProxy<br/>Validator
    participant Scheduler as Deployment<br/>Scheduler
    participant Deployer as Deployer
    participant HAProxy1 as HAProxy<br/>Instance 1
    participant HAProxy2 as HAProxy<br/>Instance 2

    K8S->>ResourceWatcher: Resource update event
    ResourceWatcher->>ResourceWatcher: Update local index
    ResourceWatcher->>EventBus: Publish(ResourceIndexUpdatedEvent)

    EventBus->>Reconciler: ResourceIndexUpdatedEvent
    Note over Reconciler: Start debounce timer

    Note over Reconciler: Wait for quiet period

    Reconciler->>EventBus: Publish(ReconciliationTriggeredEvent)

    EventBus->>Executor: ReconciliationTriggeredEvent
    Executor->>EventBus: Publish(ReconciliationStartedEvent)

    EventBus->>Renderer: ReconciliationTriggeredEvent
    Note over Renderer: Query indexed resources<br/>Render templates
    Renderer->>EventBus: Publish(TemplateRenderedEvent)

    EventBus->>Validator: TemplateRenderedEvent
    Note over Validator: Phase 1: Syntax (parser)<br/>Phase 2: Semantics (haproxy -c)
    Validator->>EventBus: Publish(ValidationCompletedEvent)

    EventBus->>Scheduler: ValidationCompletedEvent
    Note over Scheduler: Check rate limit<br/>Queue if deployment in progress
    Scheduler->>EventBus: Publish(DeploymentScheduledEvent)

    EventBus->>Deployer: DeploymentScheduledEvent
    Deployer->>EventBus: Publish(DeploymentStartedEvent)

    par Parallel Deployment
        Deployer->>HAProxy1: Deploy via Dataplane API
        HAProxy1-->>Deployer: Success
        Deployer->>EventBus: Publish(InstanceDeployedEvent)
    and
        Deployer->>HAProxy2: Deploy via Dataplane API
        HAProxy2-->>Deployer: Success
        Deployer->>EventBus: Publish(InstanceDeployedEvent)
    end

    Deployer->>EventBus: Publish(DeploymentCompletedEvent)
    EventBus->>Executor: DeploymentCompletedEvent
    Executor->>EventBus: Publish(ReconciliationCompletedEvent)
```

**Event-Driven Flow:**

1. **Resource Change**: ResourceWatcher receives Kubernetes event, updates local index, publishes ResourceIndexUpdatedEvent
2. **Debouncing**: Reconciler subscribes to index events, starts debounce timer to batch rapid changes
3. **Reconciliation Trigger**: After quiet period, Reconciler publishes ReconciliationTriggeredEvent
4. **Orchestration Start**: Executor subscribes to ReconciliationTriggeredEvent and publishes ReconciliationStartedEvent for observability
5. **Template Rendering**: Renderer component subscribes to ReconciliationTriggeredEvent, queries indexed resources, renders templates using pkg/templating, and publishes TemplateRenderedEvent with rendered configuration and auxiliary files
6. **Validation**: HAProxyValidator component subscribes to TemplateRenderedEvent, performs two-phase validation (syntax with client-native parser, semantics with haproxy binary via pkg/dataplane), and publishes ValidationCompletedEvent or ValidationFailedEvent
7. **Deployment Scheduling**: DeploymentScheduler subscribes to ValidationCompletedEvent, enforces minimum deployment interval (default 2s) for rate limiting, implements "latest wins" queueing if deployment is in progress, and publishes DeploymentScheduledEvent when ready
8. **Deployment Execution**: Deployer component subscribes to DeploymentScheduledEvent, executes parallel deployments to all discovered HAProxy endpoints using pkg/dataplane, publishes InstanceDeployedEvent for each instance and DeploymentCompletedEvent when all complete
9. **Completion**: Executor subscribes to DeploymentCompletedEvent and publishes ReconciliationCompletedEvent with duration metrics

All coordination happens via EventBus pub/sub. Components are fully event-driven with no direct function calls between them, enabling clean separation of concerns and independent testability.

## Configuration Validation Process

```mermaid
sequenceDiagram
    participant EventBus
    participant Validator as HAProxy<br/>Validator
    participant Parser as client-native Parser
    participant Binary as haproxy Binary
    participant EventBus2 as EventBus

    EventBus->>Validator: TemplateRenderedEvent
    Note over Validator: Extract config and<br/>auxiliary files from event

    Validator->>Validator: Acquire validation mutex
    Note over Validator: Single-threaded validation

    Validator->>Parser: ParseConfiguration(config)

    alt Syntax Error
        Parser-->>Validator: Parse error
        Validator->>EventBus2: Publish(ValidationFailedEvent)
    else Valid Syntax
        Parser-->>Validator: Parsed structure

        Validator->>Binary: Execute haproxy -c -f config
        Note over Binary: Write aux files to directories<br/>Validate with -c flag

        alt Semantic Error
            Binary-->>Validator: Exit code 1 + error msg
            Validator->>EventBus2: Publish(ValidationFailedEvent)
        else Valid Config
            Binary-->>Validator: Exit code 0
            Validator->>EventBus2: Publish(ValidationCompletedEvent)
        end
    end
```

**Validation Steps:**

1. **Event Subscription**: HAProxyValidator component subscribes to TemplateRenderedEvent and receives rendered configuration and auxiliary files
   - Event-driven trigger - no direct function calls from Renderer
   - Decouples rendering from validation

2. **Mutex Acquisition**: Acquire validation mutex to ensure single-threaded validation
   - Prevents concurrent writes to HAProxy directories
   - Ensures consistent validation state

3. **Syntax Validation**: client-native library (pkg/dataplane) parses config structure
   - Checks grammar and syntax rules
   - Validates section structure
   - Returns parsing errors if invalid

4. **Semantic Validation**: haproxy binary performs full validation
   - Writes auxiliary files to configured HAProxy directories (maps, certs, general files)
   - Writes main configuration to configured path
   - Executes `haproxy -c -f /etc/haproxy/haproxy.cfg`
   - Checks resource availability (files referenced in config must exist)
   - Validates directive combinations
   - Verifies configuration coherence
   - Returns detailed error messages if invalid

5. **Event Publishing**: Validator publishes ValidationCompletedEvent or ValidationFailedEvent
   - Other components (Executor, DeploymentScheduler) subscribe to these events
   - Event-driven coordination continues the reconciliation workflow

## Zero-Reload Deployment Strategy

```mermaid
sequenceDiagram
    participant Sync as Synchronizer
    participant Client as Dataplane Client
    participant DP as Dataplane API
    participant HAProxy

    Sync->>Client: DeployConfiguration(new_config)
    Client->>DP: GET /configuration (current)
    DP-->>Client: Current config

    Client->>Client: Compare structures

    alt Only runtime changes
        Note over Client: Servers, maps, ACLs only
        Client->>DP: POST /runtime/servers
        DP->>HAProxy: Runtime API command
        HAProxy-->>DP: Updated
        DP-->>Client: Success (no reload)
    else Mixed changes
        Note over Client: Runtime + config changes
        Client->>DP: POST /runtime/servers
        Client->>DP: POST /configuration
        DP->>HAProxy: Apply config
        HAProxy-->>DP: Reload triggered
        DP-->>Client: Success (reload)
    else Structural changes
        Note over Client: Backends, frontends, etc.
        Client->>DP: POST /configuration
        DP->>HAProxy: Replace config + reload
        HAProxy-->>DP: Reload complete
        DP-->>Client: Success (reload)
    end

    Client-->>Sync: DeploymentResult
```

**Deployment Optimization:**

The synchronizer analyzes configuration changes to determine the optimal deployment strategy:

1. **Runtime-Only Updates**: Server additions/removals, map updates, ACL changes → No reload
2. **Mixed Updates**: Apply runtime changes first, then config changes → Single reload
3. **Structural Updates**: Backend/frontend changes → Full reload required

This minimizes service disruption by avoiding unnecessary HAProxy process reloads.
