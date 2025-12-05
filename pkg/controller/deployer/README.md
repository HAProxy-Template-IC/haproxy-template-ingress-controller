# pkg/controller/deployer

Deployment orchestration components for HAProxy configuration deployment.

## Overview

The deployer package implements a three-component architecture that provides smart deployment scheduling with rate limiting, drift prevention, and stateless execution.

## Architecture

### Three-Component Design

```
ValidationCompletedEvent ──────┐
HAProxyPodsDiscoveredEvent ────┤
DriftPreventionTriggeredEvent ─┤
                               ↓
                    DeploymentScheduler
                    (Rate Limiting & State)
                               ↓
                    DeploymentScheduledEvent
                               ↓
                         Deployer
                    (Stateless Executor)
                               ↓
                    DeploymentCompletedEvent
                               ↓
                    DriftPreventionMonitor
                    (Periodic Triggering)
```

### Components

#### 1. DeploymentScheduler

**Purpose**: Coordinates WHEN deployments happen

**Responsibilities**:

- Maintains state (last validated config, current endpoints)
- Enforces minimum deployment interval (rate limiting)
- Implements "latest wins" queueing for concurrent changes
- Publishes DeploymentScheduledEvent when ready to deploy

**Events**:

- Subscribes: `TemplateRenderedEvent`, `ValidationCompletedEvent`, `HAProxyPodsDiscoveredEvent`, `DriftPreventionTriggeredEvent`, `DeploymentCompletedEvent`
- Publishes: `DeploymentScheduledEvent`

**Configuration**:

- `min_deployment_interval`: Minimum time between consecutive deployments (default: 2s)

#### 2. Deployer

**Purpose**: Executes deployments to HAProxy instances

**Responsibilities**:

- Stateless deployment execution
- Parallel deployment to multiple endpoints
- Per-instance success/failure tracking
- Publishes detailed deployment events

**Events**:

- Subscribes: `DeploymentScheduledEvent`
- Publishes: `DeploymentStartedEvent`, `InstanceDeployedEvent`, `InstanceDeploymentFailedEvent`, `DeploymentCompletedEvent`

#### 3. DriftPreventionMonitor

**Purpose**: Prevents configuration drift from external changes

**Responsibilities**:

- Monitors deployment activity via timer
- Triggers periodic deployments after idle period
- Resets timer on each successful deployment

**Events**:

- Subscribes: `DeploymentCompletedEvent`
- Publishes: `DriftPreventionTriggeredEvent`

**Configuration**:

- `drift_prevention_interval`: Interval for periodic deployments (default: 60s)

## Quick Start

```go
// Create deployment scheduler with rate limiting
minInterval := 2 * time.Second
scheduler := deployer.NewDeploymentScheduler(bus, logger, minInterval)
go scheduler.Start(ctx)

// Create stateless deployer
deployer := deployer.New(bus, logger)
go deployer.Start(ctx)

// Create drift prevention monitor
driftInterval := 60 * time.Second
monitor := deployer.NewDriftPreventionMonitor(bus, logger, driftInterval)
go monitor.Start(ctx)
```

## Key Features

### Rate Limiting

Prevents rapid-fire deployments during high-frequency changes:

- Configurable minimum interval between deployments
- Enforced by DeploymentScheduler before publishing events
- Prevents version conflicts in HAProxy Dataplane API

### "Latest Wins" Queueing

Only the most recent configuration change is queued:

- Single pending deployment value (not a queue)
- New deployment requests overwrite pending deployments
- Ensures eventual consistency without unnecessary work

### Drift Prevention

Detects and corrects external configuration changes:

- Periodic deployment triggers if system is idle
- Helps identify drift from other Dataplane API clients
- Configurable interval (default: 60s)

### Concurrent Deployment Protection

Prevents overlapping deployments:

- Scheduler tracks deployment in-progress state
- Pending deployments queued until current completes
- Recursive processing ensures all changes are applied

## License

See main repository for license information.
