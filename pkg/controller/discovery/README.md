# pkg/controller/discovery

HAProxy pod discovery component.

## Overview

Tracks the current set of HAProxy pods and the dataplane endpoints reachable on them. The component does not watch pods directly — it consumes the pod store filled by the resource watcher (via `ResourceIndexUpdatedEvent` for the configured `haproxy-pods` resource type), enriches each pod with credentials and an HAProxy version probe, and emits `HAProxyPodsDiscoveredEvent` containing the validated endpoints. Pods that disappear cause a `HAProxyPodTerminatedEvent`.

This component runs on **all replicas** — discovery is a read-only HAProxy probe and there's no reason to gate it on leadership. It caches the most recent `HAProxyPodsDiscoveredEvent` via `leadership.StateReplayer` and re-publishes it on `BecameLeaderEvent`, so a freshly-elected leader's (leader-only) deployer/scheduler get current pod state without waiting for the next pod-watcher tick.

## Quick Start

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/controller/discovery"

component, err := discovery.New(bus, logger)
if err != nil { /* ... */ }
go component.Start(ctx)
```

The component's effective configuration (pod-selector labels, dataplane port, basic-auth credentials) is provided through the events it subscribes to, not via constructor arguments.

## Events

- Subscribes: `ConfigValidatedEvent` (dataplane port + selector), `CredentialsUpdatedEvent`, `ResourceIndexUpdatedEvent` (haproxy-pods), `ResourceSyncCompleteEvent`, `BecameLeaderEvent`
- Publishes: `HAProxyPodsDiscoveredEvent`, `HAProxyPodTerminatedEvent`

## License

Apache-2.0 — see root `LICENSE`.
