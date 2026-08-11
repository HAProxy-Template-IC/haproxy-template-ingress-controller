# pkg/controller/discovery

HAProxy pod discovery component.

## Overview

Tracks the current set of HAProxy pods and the dataplane endpoints reachable on them. The component does not watch pods directly — it consumes the pod store filled by the resource watcher (via `ResourceIndexUpdatedEvent` for the configured `haproxy-pods` resource type), enriches each pod with credentials and independent Dataplane API and HAProxy binary version proofs, and emits `HAProxyPodsDiscoveredEvent` containing the validated endpoints. A removed or replaced endpoint authority causes a `HAProxyPodTerminatedEvent` carrying the predecessor pod UID before the new fleet publication.

The admission cache stores both version proofs for an exact pod namespace, name, UID, container execution epoch, and URL. Every discovery rebuilds the endpoint from the current pod and credentials, so rotated credentials don't require another version probe while a replacement pod, container restart, image change, or changed URL does. Discovery publications are serialized with credential, port, and store-reference updates, preventing a retry that started earlier from replacing newer endpoint state. Version mismatches are cached for that exact identity; transient failures wait for their per-identity backoff deadline, which a credentials change resets.

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
- Publishes: `HAProxyPodsDiscoveredEvent`, `HAProxyPodTerminatedEvent`, `HAProxyPodRejectedEvent` (one per candidate that fails version probing — the metrics component turns these into `haptic_haproxy_pods_rejected_total`)

## License

Apache-2.0 — see root `LICENSE`.
