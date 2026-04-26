# pkg/controller/leadership

Helpers for the controller's leader-only / all-replica component split.

## Overview

The controller has a small but recurring problem: leader-only components subscribe to the EventBus *after* all-replica components have already published critical state (config validated, HAProxy pods discovered, last template rendered). When a new leader is elected, those leader-only components have no events queued — they'd sit idle until the next reconciliation.

`StateReplayer[T]` solves this. All-replica components cache their most recent event of type `T` here; on `BecameLeaderEvent` the new leader's components ask the replayer to re-publish the cached event so they get current state immediately instead of waiting.

## Quick Start

```go
import (
    "gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
    "gitlab.com/haproxy-haptic/haptic/pkg/controller/leadership"
)

// All-replica side (e.g. configchange.ConfigChangeHandler):
configReplayer := leadership.NewStateReplayer[*events.ConfigValidatedEvent](bus)

// On every successful validation, cache the event:
configReplayer.Cache(validatedEvent)

// Leader-only side (e.g. configpublisher.Component):
//   When BecameLeaderEvent fires, ask the replayer to re-publish.
//   The leader-only component gets the same event the rest of the
//   cluster saw most recently.
configReplayer.Replay()
```

The cache is a single slot — only the most recent event is retained. That's deliberate: a new leader needs the *current* state, not a history.

## API

```go
type StateReplayer[T busevents.Event] struct { /* opaque */ }

func NewStateReplayer[T busevents.Event](bus *busevents.EventBus) *StateReplayer[T]

func (r *StateReplayer[T]) Cache(event T)            // store the event
func (r *StateReplayer[T]) Replay() bool             // re-publish; false if nothing cached
func (r *StateReplayer[T]) HasState() bool           // peek without replay
func (r *StateReplayer[T]) Get() (T, bool)           // read the cached event without replay
```

All methods are safe for concurrent access via an internal `RWMutex`.

## Where It's Used

Grep for `leadership.NewStateReplayer[` to find the canonical list. Currently:

| Replayed event | Cache site | Replay trigger |
|----------------|-----------|----------------|
| `*ConfigValidatedEvent` | `pkg/controller/configchange.ConfigChangeHandler` | `BecameLeaderEvent` consumed by leader-only configpublisher / deployer |
| `*HAProxyPodsDiscoveredEvent` | `pkg/controller/discovery.Component` | same |
| `*ValidationCompletedEvent` | `pkg/controller/validator.HAProxyValidatorComponent` | same |

`pkg/controller/renderer` is itself leader-only — it has no replayer because
the Reconciler triggers a fresh reconciliation on `BecameLeaderEvent` instead
of replaying a stale render. See the comment on `Component` in
`renderer/component.go` for the design note.

See `pkg/controller/LEADER_ONLY_COMPONENTS.md` for the full inventory of
leader-only components and the cache/replay contract every one of them
implements.

## See Also

- [`pkg/controller/leaderelection`](../leaderelection/) — the event adapter that produces `BecameLeaderEvent` / `LostLeadershipEvent`
- [`pkg/k8s/leaderelection`](../../k8s/leaderelection/) — pure leader election library underneath
- `pkg/controller/LEADER_ONLY_COMPONENTS.md` — the full leader-only contract

## License

Apache-2.0 — see root `LICENSE`.
