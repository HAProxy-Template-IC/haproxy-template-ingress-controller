# pkg/controller/discovery

HAProxy pod discovery component.

## Overview

Tracks the current set of HAProxy pods and the agent endpoints reachable on them. The component does not watch pods directly — it consumes the pod store filled by the resource watcher (via `ResourceIndexUpdatedEvent` for the configured `haproxy-pods` resource type) and emits `HAProxyPodsDiscoveredEvent` with the endpoints it admitted. A removed or replaced endpoint authority causes a `HAProxyPodTerminatedEvent` carrying the predecessor pod UID before the new fleet publication.

Admission is **one rule**: the pod has an IP, its `agent` container is running, and `GET /v1/state` answers. Neither the container's ready flag nor pod Ready is consulted — HAProxy's `/ready` only turns 200 after the first apply lands, so requiring it would never admit a fresh pod. The HAProxy version the agent reports travels with the endpoint; the deploy side derives the fleet's template capabilities and each pod's runtime capabilities from it.

The admission cache is keyed on an exact pod namespace, name, UID, container execution epoch and URL, so a replacement pod, a container restart, an image change or a changed URL is probed again while an unchanged pod costs no round trip — discovery re-runs on every drift tick. Every discovery rebuilds the endpoint from the current pod and credentials, so a rotated credential reaches the deployer without another probe. Probes run concurrently (16 at a time): a hung agent must not delay the pass that retires a dead endpoint. Publications are serialized with credential, port and store-reference updates, so a pass that started earlier cannot replace newer endpoint state.

This component runs on **all replicas** — discovery is a read-only probe and there's no reason to gate it on leadership. It caches the most recent `HAProxyPodsDiscoveredEvent` via `leadership.StateReplayer` and re-publishes it on `BecameLeaderEvent`, so a freshly-elected leader's (leader-only) deployer/scheduler get current pod state without waiting for the next pod-watcher tick.

## Quick Start

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/controller/discovery"

component := discovery.New(bus, logger)
go component.Start(ctx)
```

The component's effective configuration (pod-selector labels, agent port, basic-auth credentials) is provided through the events it subscribes to, not via constructor arguments.

## Events

- Subscribes: `ConfigValidatedEvent` (agent port + selector), `CredentialsUpdatedEvent`, `ResourceIndexUpdatedEvent` (haproxy-pods), `ResourceSyncCompleteEvent`, `BecameLeaderEvent`, `DriftPreventionTriggeredEvent`
- Publishes: `HAProxyPodsDiscoveredEvent`, `HAProxyPodTerminatedEvent`, `HAProxyPodRejectedEvent` (one per pod that fails admission, labelled `agent_container_not_running` or `agent_unreachable` — the metrics component turns these into `haptic_haproxy_pods_rejected_total`)

## License

Apache-2.0 — see root `LICENSE`.
