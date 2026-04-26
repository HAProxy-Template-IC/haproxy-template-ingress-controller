# pkg/controller/configpublisher

Event adapter that turns the controller's `TemplateRenderedEvent` / `ValidationCompletedEvent` / deployment lifecycle into CRD writes via `pkg/k8s/configpublisher.Publisher`.

## Overview

This is the **leader-only** component that publishes the rendered HAProxy configuration as observable Kubernetes CRDs (`HAProxyCfg`, `HAProxyMapFile`, `HAProxyGeneralFile`, `HAProxyCRTListFile`) plus the SSL Secrets that auxiliary files reference. It also writes per-pod deployment status back onto the published `HAProxyCfg` so operators can `kubectl get haproxycfg <name>` and see exactly which pods accepted which version.

The component subscribes only on the leader, holds short-lived per-correlation-ID state to pair `TemplateRenderedEvent` with the matching `ValidationCompletedEvent`, and runs three async worker goroutines so K8s API latency doesn't block the event loop.

## Quick Start

```go
import (
    "time"

    k8spublisher "gitlab.com/haproxy-haptic/haptic/pkg/k8s/configpublisher"
    "gitlab.com/haproxy-haptic/haptic/pkg/controller/configpublisher"
)

basePublisher := k8spublisher.NewWithListers(k8sClient, crdClient, listers, logger)

component := configpublisher.New(
    basePublisher,
    eventBus,
    logger,
    configpublisher.WithPublishInterval(5 * time.Second), // optional throttle
)
go component.Start(ctx) // blocks; subscribes only when leadership is held
```

`WithPublishInterval` enables a leading-edge refractory throttle on CRD writes — every reconciliation still pushes config to HAProxy pods through the deployer, but this throttles the etcd-side observability writes (which carry the full ~500 KB rendered config) to keep API-server pressure manageable during endpoint churn. Pass `0` (the default) for no throttling.

## Event Flow

The component is leader-only: the lifecycle registry only invokes `Start()` once
leadership is held, and `Start()` then subscribes (via `SubscribeTypesLeaderOnly`,
which suppresses the "late subscriber" warning) and spins up the three worker
goroutines. There is no subscription to `BecameLeaderEvent` itself — the leader
contract drives the start, not an event handler.

| Subscribed event | What the component does |
|------------------|-------------------------|
| `ConfigValidatedEvent` | Cache the validated config (CRD + secret resourceVersions) for upcoming publishes |
| `TemplateRenderedEvent` | Cache the rendered config + aux files, keyed by correlation ID |
| `ValidationCompletedEvent` | Match against the cached render; queue a `publishWorkItem` for the publish worker |
| `ValidationFailedEvent` | Queue a `validationFailedWorkItem` so the failure shows up as an `-invalid` `HAProxyCfg` |
| `ConfigAppliedToPodEvent` | Coalesce per-pod status updates (last-wins) and signal the status worker |
| `HAProxyPodTerminatedEvent` | Same channel — clears stale per-pod status when a pod goes away |
| `HAProxyPodsDiscoveredEvent` | Same channel — refreshes the known-pod set used to scope status writes |
| `LostLeadershipEvent` | Clear cached configuration state (`templateConfig`, the per-correlation `renderedConfigs` map, `lastPublishedChecksum`). Pending work items in the publish / validation-failed / status channels are *not* drained here — the workers stop when the lifecycle cancels Start's context, and unprocessed items go with it. |

## Three Workers, Three Throttles

| Worker | Channel | Purpose |
|--------|---------|---------|
| `publishWorker` | `publishWork` | `Publisher.PublishConfig` (creates/updates the CRDs and Secrets) |
| `validationFailedWorker` | `validationFailedWork` | Publishes the failure as an *invalid* `HAProxyCfg` (suffixed `-invalid`) so operators can inspect what was rejected |
| `statusWorker` | triggered via `statusWorkTrigger`; pending writes coalesce in `statusWorkPending` keyed by `namespace/runtimeConfig/podName` | Per-pod deployment status updates |

Each worker is a single goroutine. The publish worker enforces the leading-edge refractory throttle (when `WithPublishInterval` is set); the status worker reuses the same interval to throttle the (also expensive) status subresource writes.

## See Also

- [`pkg/k8s/configpublisher`](../../k8s/configpublisher/) — the underlying `Publisher` that does the actual CRD writes
- [`pkg/controller/deployer`](../deployer/) — the parallel path that pushes config to HAProxy pods (this package writes the observability artefacts; the deployer writes the live HAProxy configuration)
- [`pkg/controller/events`](../events/) — `TemplateRenderedEvent`, `ValidationCompletedEvent`, `InstanceDeployedEvent`, etc.

## License

Apache-2.0 — see root `LICENSE`.
