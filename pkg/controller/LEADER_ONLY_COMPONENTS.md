# Leader-Only Components - Leadership Transition Guidelines

This document describes components that only run on the elected leader and guidelines for preventing event ordering bugs during leadership transitions.

## Background: The "Late Subscriber Problem"

When leadership transitions occur, leader-only components start subscribing to events AFTER critical state events have already been published by all-replica components. This creates a race condition where leader-only components miss essential state.

**Example timeline of the bug:**

```
14:03:29 - Discovery publishes HAProxyPodsDiscoveredEvent (2 pods)
14:03:30 - Renderer publishes TemplateRenderedEvent (config: 5523 bytes)
14:03:31 - Validator publishes ValidationCompletedEvent (success)
         ↓
14:05:04 - Leader election completes, new leader elected
14:05:05 - DeploymentScheduler starts subscribing (LEADER-ONLY)
         ↓
14:05:25 - DeploymentScheduler tries to deploy
         ❌ Has 0 endpoints (missed HAProxyPodsDiscoveredEvent)
         ❌ Deployment deadlocked forever
```

## Leader-Only Components

Components that only run on the elected leader (registered via `registry.Build().LeaderOnly(...)` in `pkg/controller/reconciliation.go`):

| Component | Purpose | Event Dependencies | State Replay | Cleanup |
|-----------|---------|-------------------|--------------|---------|
| **Coordinator** | Drives the synchronous render-validate pipeline | `ReconciliationTriggeredEvent` | N/A (subscribes via `SubscribeTypesLeaderOnly` after lease acquired) | N/A (context cancellation tears down) |
| **DeploymentScheduler** | Schedules HAProxy deployments with rate limiting | `ValidationCompletedEvent`<br>`HAProxyPodsDiscoveredEvent` | N/A (receives replayed events) | ✅ `LostLeadershipEvent` |
| **Deployer** | Executes deployments to HAProxy pods | `DeploymentScheduledEvent` | N/A (stateless) | N/A (stateless) |
| **DriftPreventionMonitor** | Triggers periodic drift prevention deployments | `DeploymentCompletedEvent` | N/A (timer-based) | ✅ `LostLeadershipEvent` |
| **ConfigPublisher** | Creates and updates HAProxyCfg and auxiliary file resources | `ConfigValidatedEvent`<br>`TemplateRenderedEvent`<br>`ValidationCompletedEvent`<br>`ConfigAppliedToPodEvent`<br>`HAProxyPodTerminatedEvent` | N/A (caches state from events) | ✅ `LostLeadershipEvent` |
| **StatusUpdater** | Writes validation results back to the `HAProxyTemplateConfig` CRD's status subresource | `ConfigValidatedEvent`, `ConfigInvalidEvent` | N/A | N/A (context cancellation tears down) |

## All-Replica Components with State Replay

Components that run on all replicas and replay state on leadership transitions via `leadership.NewStateReplayer[T]`. Grep for `leadership.NewStateReplayer[` to confirm the canonical list:

| Component | Purpose | Replays On Leadership | Handler |
|-----------|---------|----------------------|---------|
| **ConfigChangeHandler** | Validation orchestrator + reinit signaller | `BecameLeaderEvent` → `ConfigValidatedEvent` | `configchange/handler.go` (`configReplayer`) |
| **Discovery** | Discovers HAProxy pod endpoints | `BecameLeaderEvent` → `HAProxyPodsDiscoveredEvent` | `discovery/component.go` (`discoveredReplayer`) |

The renderer is leader-only itself, so it has *no* `StateReplayer` — instead, the Reconciler triggers a fresh reconciliation on `BecameLeaderEvent` (`pkg/controller/reconciler/reconciler.go`), so the new leader's pipeline produces a current `TemplateRenderedEvent` rather than replaying a stale one.

## Solution Architecture

### Pattern 1: State Replay on BecameLeaderEvent

All-replica components that maintain state (config, validation results, endpoints) must re-publish their last state when a new leader is elected. Use the `leadership.NewStateReplayer[T]` helper rather than rolling your own `sync.RWMutex` + `lastState`/`hasState` triple — the helper owns the mutex, the single-slot cache, and the `Cache` / `Get` / `HasState` / `Replay` API.

**Implementation pattern:**

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/controller/leadership"

type Component struct {
    // ... existing fields ...

    // Single-slot replayer for the leader-only late-subscriber problem.
    stateReplayer *leadership.StateReplayer[*events.MyStateEvent]
}

func New(bus *busevents.EventBus, ...) *Component {
    return &Component{
        // ... existing wiring ...
        stateReplayer: leadership.NewStateReplayer[*events.MyStateEvent](bus),
    }
}

// On every successful state production, cache for replay:
func (c *Component) onStateProduced(event *events.MyStateEvent) {
    c.eventBus.Publish(event)
    c.stateReplayer.Cache(event)
}

func (c *Component) handleEvent(event busevents.Event) {
    switch e := event.(type) {
    case *events.BecameLeaderEvent:
        c.handleBecameLeader(e)
    // ... other cases ...
    }
}

func (c *Component) handleBecameLeader(_ *events.BecameLeaderEvent) {
    if !c.stateReplayer.Replay() {
        c.logger.Debug("became leader but no state available yet, skipping state replay")
    }
}
```

The hand-rolled `sync.RWMutex` + `lastState` + `hasState` triple is the older pre-StateReplayer pattern and shouldn't be used in new code; grep `leadership.NewStateReplayer[` for the canonical examples (`configchange/handler.go`, `discovery/component.go`).

### Pattern 2: State Cleanup on LostLeadershipEvent

Leader-only components must clean up state when losing leadership to prevent deadlocks or stale state issues.

**Implementation pattern:**

```go
func (c *Component) handleEvent(event busevents.Event) {
    switch e := event.(type) {
    case *events.LostLeadershipEvent:
        c.handleLostLeadership(e)
    // ... other cases ...
    }
}

func (c *Component) handleLostLeadership(_ *events.LostLeadershipEvent) {
    c.mu.Lock()
    defer c.mu.Unlock()

    c.logger.Info("lost leadership, clearing component state")

    // Clear in-progress flags to prevent deadlocks
    c.inProgress = false
    c.pendingWork = nil

    // Stop timers to prevent leaked goroutines
    if c.timer != nil {
        c.timer.Stop()
        c.timerActive = false
    }

    // Clear transient state (NOT historical data like lastCompletionTime)
    c.currentWork = nil
}
```

## Checklist for New Leader-Only Components

When creating a new component that only runs on the leader, ensure:

- [ ] **Event dependencies documented**: List all events the component subscribes to
- [ ] **State management**:
  - [ ] If component maintains state, implement mutex protection
  - [ ] If component depends on all-replica component state, verify those components replay on `BecameLeaderEvent`
- [ ] **Leadership transition handling**:
  - [ ] Subscribe to `LostLeadershipEvent` if component has in-progress work
  - [ ] Clear in-progress flags in `LostLeadershipEvent` handler
  - [ ] Stop timers/goroutines in `LostLeadershipEvent` handler
- [ ] **Testing**:
  - [ ] Test component behavior during leadership transitions
  - [ ] Verify no deadlocks when leadership changes mid-operation
  - [ ] Verify state is properly replayed to new leader
- [ ] **Documentation**:
  - [ ] Add component to table in this file
  - [ ] Document state dependencies in component CLAUDE.md
  - [ ] Log state replay and cleanup events for debugging

## Checklist for New All-Replica Components

When creating a new component that maintains state used by leader-only components:

- [ ] **State caching**:
  - [ ] Cache last successful state with mutex protection
  - [ ] Include `hasState` boolean to distinguish "no state yet" from "zero state"
- [ ] **BecameLeaderEvent handling**:
  - [ ] Subscribe to `BecameLeaderEvent`
  - [ ] Re-publish last state in handler
  - [ ] Log state replay with relevant metrics
  - [ ] Check `hasState` before replaying (avoid publishing uninitialized state)
- [ ] **Documentation**:
  - [ ] Add component to "All-Replica Components with State Replay" table
  - [ ] Document what event is replayed
  - [ ] Reference handler location in code

## Testing Leadership Transitions

**Manual testing with dev cluster:**

```bash
# Deploy with 2 replicas
kubectl -n haptic scale deployment haptic-controller --replicas=2

# Watch logs for leadership events
kubectl -n haptic logs -f -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller

# Delete current leader pod to trigger election
LEADER_POD=$(kubectl -n haptic get pods -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller -o jsonpath='{.items[0].metadata.name}')
kubectl -n haptic delete pod $LEADER_POD

# Verify:
# - New leader logs "became leader, re-publishing..."
# - Deployments continue successfully after transition
# - No "no endpoints available yet" messages
```

**Expected log pattern after leadership transition:**

```
14:05:04.123 | INFO  | Became leader
14:05:04.124 | INFO  | became leader, re-discovering HAProxy pods for deployment scheduler | count=2
14:05:04.125 | INFO  | became leader, re-publishing last rendered config for DeploymentScheduler | config_bytes=5523
14:05:04.126 | INFO  | became leader, re-publishing last validation result (success) for DeploymentScheduler
14:05:04.127 | INFO  | HAProxy pods discovered | count=2
14:05:04.128 | INFO  | scheduling deployment | reason=pod_discovery endpoint_count=2
```

## Common Pitfalls

### Pitfall 1: Not Checking hasState Before Replay

```go
// BAD - publishes zero values if no state yet
func (c *Component) handleBecameLeader(_ *events.BecameLeaderEvent) {
    c.mu.RLock()
    state := c.lastState  // Could be zero value!
    c.mu.RUnlock()

    c.eventBus.Publish(events.NewStateEvent(state))  // ❌ Wrong
}

// GOOD - only replays if state exists
func (c *Component) handleBecameLeader(_ *events.BecameLeaderEvent) {
    c.mu.RLock()
    hasState := c.hasState
    state := c.lastState
    c.mu.RUnlock()

    if !hasState {  // ✅ Correct
        c.logger.Debug("no state yet, skipping replay")
        return
    }

    c.eventBus.Publish(events.NewStateEvent(state))
}
```

### Pitfall 2: Not Clearing In-Progress Flags

```go
// BAD - deploymentInProgress stays true forever
func (s *Scheduler) handleLostLeadership(_ *events.LostLeadershipEvent) {
    s.logger.Info("lost leadership")
    // ❌ Missing cleanup!
}

// GOOD - clears flags to prevent deadlock
func (s *Scheduler) handleLostLeadership(_ *events.LostLeadershipEvent) {
    s.mu.Lock()
    defer s.mu.Unlock()

    s.deploymentInProgress = false  // ✅ Correct
    s.pendingDeployment = nil
}
```

### Pitfall 3: Replaying Failed Validation

```go
// BAD - replays validation failures
func (v *Validator) handleBecameLeader(_ *events.BecameLeaderEvent) {
    v.mu.RLock()
    succeeded := v.lastValidationSucceeded
    v.mu.RUnlock()

    if succeeded {
        v.eventBus.Publish(events.NewValidationCompletedEvent(...))
    } else {
        v.eventBus.Publish(events.NewValidationFailedEvent(...))  // ❌ Unnecessary
    }
}

// GOOD - only replays successful validations
func (v *Validator) handleBecameLeader(_ *events.BecameLeaderEvent) {
    v.mu.RLock()
    hasResult := v.hasValidationResult
    succeeded := v.lastValidationSucceeded
    v.mu.RUnlock()

    if !hasResult || !succeeded {  // ✅ Correct
        return  // DeploymentScheduler only needs successful validations
    }

    v.eventBus.Publish(events.NewValidationCompletedEvent(...))
}
```

## References

- Initial bug discovery: Discovery component missing HAProxyPodsDiscoveredEvent (fixed in discovery/component.go:278)
- Leader election implementation: pkg/controller/leaderelection/CLAUDE.md
- Event coordination: pkg/events/CLAUDE.md
- Controller architecture: pkg/controller/CLAUDE.md
