# pkg/controller

Event-driven coordination layer for HAPTIC. This package is the only one in the tree that knows about the EventBus — it wraps the pure libraries (`pkg/templating`, `pkg/k8s`, `pkg/dataplane`, `pkg/stores`, `pkg/webhook`, `pkg/httpstore`) in event adapters and orchestrates startup, reconciliation, and shutdown.

Most day-to-day work in this package does not need this README; `pkg/controller/CLAUDE.md` has the detailed developer context and the source is the authoritative interface. This page is a short map for newcomers.

## Mental Model

Three layers:

```
Pure library          Event adapter                         EventBus consumers
(pkg/templating,      (pkg/controller/renderer,             (commentator, metrics,
 pkg/dataplane,        pkg/controller/validator,             debug, pipeline)
 pkg/k8s, ...)         pkg/controller/deployer, ...)
```

- Pure libraries expose plain Go APIs with no event dependencies.
- Event adapters embed `*component.Base` (the shared event-loop scaffold) and translate events to pure-library calls and back.
- Consumers (EventCommentator, metrics adapter, debug server) subscribe without publishing — they observe.

The sub-package tree in this directory mirrors those three layers. For the canonical list see `docs/controller/docs/development/design/package-structure.md`.

## Startup Sequence

The controller runs a **reinitialization loop** (`iteration.go`). Each iteration:

1. Fetch and validate the `HAProxyTemplateConfig` CRD and its credentials Secret.
2. Build a fresh `EventBus` and register every component via `pkg/lifecycle`.
3. Start resource watchers and wait for initial sync.
4. `EventBus.Start()` releases buffered events.
5. Start reconciliation components (renderer, validator, deployer scheduler, drift monitor, metrics adapter, commentator).
6. Wait for a config change or context cancellation. On config change the iteration context is cancelled and the loop restarts with the new config.

This is why the docs consistently say "no pod restart on config change" — the CRD watcher triggers a fresh iteration inside the same process.

## Key Sub-Packages

| Purpose | Package |
|---------|---------|
| Shared event-loop scaffold (embedded by nearly every component) | `component/` |
| Observability (domain-aware logs, ring-buffered event history) | `commentator/`, `debug/`, `metrics/` |
| Configuration ingestion (CRD + Secret + cert loading) | `configloader/`, `credentialsloader/`, `certloader/`, `resourceloader/` |
| Reconciliation pipeline (debounce → render → validate → publish) | `reconciler/`, `renderer/`, `validator/`, `pipeline/`, `rendercontext/` |
| Deployment orchestration (scheduler, executor, drift prevention) | `deployer/`, `discovery/`, `configpublisher/`, `statusapplier/` |
| Webhook & validation bridges | `webhook/`, `dryrunvalidator/`, `proposalvalidator/`, `testrunner/` |
| Leader election + leader-only gating | `leaderelection/`, `leadership/` |
| Store management and overlay handling | `resourcestore/`, `resourcewatcher/`, `indextracker/`, `currentconfigstore/` |
| Event catalogue (≈50 domain events) | `events/` |

`component/` is the biggest reusable abstraction: new components embed `*component.Base`, implement `HandleEvent(event)`, and get subscribe-on-construction, single-flight dispatch, panic recovery, and ready/done signalling for free. See `component/base.go` and the existing consumers for examples.

## Event Patterns

Two coordination modes via `pkg/events`:

- **Publish/Subscribe** — fire-and-forget, buffered per subscriber. Used for everything on the main reconciliation path (resource index updates → reconciliation trigger → rendered → validated → deployed).
- **Request/Response (scatter-gather)** — synchronous with timeout and expected-responder list. Used for admission-time validation, where multiple validators must independently approve a proposed config.

Domain event types live in `pkg/controller/events`. `pkg/events` itself is domain-agnostic.

## Writing a New Component

The short version:

1. Decide whether it's a pure library (goes to a top-level `pkg/<name>`) or coordination (goes here as an event adapter).
2. If it's coordination, embed `*component.Base` and implement `HandleEvent`.
3. Subscribe to the events you need in the constructor — **not** in `Start()` — so buffered events aren't lost when `EventBus.Start()` releases them. *Exception:* leader-only components subscribe in `Start()` using `SubscribeTypesLeaderOnly()`, which suppresses the late-subscriber warning. The lifecycle registry only invokes `Start()` for those after leadership is held; all-replica components replay their last state on `BecameLeaderEvent` so the late-subscribing leader-only components still see current state. See `renderer/component.go` and `configpublisher/component.go` for the canonical leader-only pattern, and `LEADER_ONLY_COMPONENTS.md` for the contract.
4. Register with `pkg/lifecycle` (mark leader-only, declare dependencies, add a health source).
5. Add a log case to `commentator/` for every new event type so it lands in the ring-buffered history.

`pkg/controller/CLAUDE.md` has the long version plus leadership-transition patterns (state replay on `BecameLeaderEvent`, cleanup on `LostLeadershipEvent`) that every new leader-only component must implement.

## Testing

```bash
go test ./pkg/controller/...             # unit + adapter tests
go test ./pkg/controller/... -race       # race detector
```

Event adapters are typically tested by wiring up a real `EventBus`, publishing a trigger event, and asserting on the resulting events. See `pkg/controller/renderer/component_test.go` for the canonical pattern; `component.Base` has its own unit tests in `component/base_test.go`.

End-to-end tests that actually spin up controllers against a Kind cluster live under [`tests/integration`](../../tests/integration/) (build-tagged `//go:build integration`), not here. Run them with `make test-integration` or `go test -tags=integration ./tests/integration/...`.

## See Also

- [`pkg/events`](../events/README.md) — EventBus infrastructure
- [`pkg/lifecycle`](../lifecycle/) — component registry, dependency ordering, leader-only gating
- `pkg/controller/CLAUDE.md` — developer context, leadership-transition patterns, pitfalls
- `pkg/controller/LEADER_ONLY_COMPONENTS.md` — checklist for leader-only components
- `docs/controller/docs/development/design/package-structure.md` — whole-repo orientation
- `docs/controller/docs/development/design/sequence-diagrams.md` — reconciliation and validation flows

## License

Apache-2.0 — see root `LICENSE`.
