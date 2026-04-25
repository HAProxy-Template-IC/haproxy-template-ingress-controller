# Package Structure

The controller is split into small Go packages with one of three roles:

- **Infrastructure** — domain-agnostic building blocks (`pkg/events`, `pkg/introspection`, `pkg/metrics`, `pkg/compression`, `pkg/lifecycle`).
- **Domain libraries** — pure business logic with no coupling to the event bus (`pkg/k8s`, `pkg/dataplane`, `pkg/templating`, `pkg/stores`, `pkg/httpstore`, `pkg/webhook`).
- **Coordination** — event adapters that wire the libraries together (`pkg/controller` and its subpackages).

This split keeps the libraries reusable and independently testable; all event choreography is confined to `pkg/controller`.

## Repository Layout

```
haptic/
├── cmd/controller/          # Entry point (main.go)
├── pkg/
│   ├── apis/                # CRD type definitions (HAProxyTemplateConfig, HAProxyCfg, ...)
│   ├── compression/         # zstd helpers used by CRD content compression
│   ├── core/                # Shared primitives (config parsing, logging setup)
│   ├── events/              # Generic EventBus and request/response plumbing
│   │   └── ringbuffer/      # Thread-safe generic ring buffer (event history)
│   ├── generated/           # Code generation output (clientset, informers, listers,
│   │                        #   DataPlane API clients per HAProxy version, zero-alloc
│   │                        #   OpenAPI validators)
│   ├── httpstore/           # Fetches and caches HTTP resources referenced from templates
│   ├── introspection/       # /debug/vars HTTP infrastructure (registry, JSONPath, pprof)
│   ├── k8s/                 # Pure Kubernetes integration library
│   │   ├── client/          # Dynamic client + namespace auto-detection
│   │   ├── configpublisher/ # Publishes HAProxyCfg and HAProxyGeneralFile CRDs
│   │   ├── indexer/         # JSONPath extraction + metadata trimming
│   │   ├── leaderelection/  # Pure leader election (no event bus dependency)
│   │   ├── store/           # MemoryStore and CachedStore
│   │   ├── types/           # Store, WatcherConfig, SingleWatcherConfig, ChangeStats
│   │   └── watcher/         # Bulk Watcher and SingleWatcher with debouncing
│   ├── lifecycle/           # Component lifecycle registry (dependencies, leader-only,
│   │                        #   startup ordering, health tracking)
│   ├── metrics/             # Instance-based Prometheus registry + /metrics server
│   ├── stores/              # Store overlays/providers used for webhook dry-run validation
│   ├── templating/          # Scriggo-based template engine (pure)
│   ├── dataplane/           # HAProxy Dataplane API integration (pure)
│   │   ├── auxiliaryfiles/  # 3-phase sync of general files, SSL, map files
│   │   ├── client/          # HTTP client with transaction lifecycle
│   │   ├── comparator/      # Fine-grained config comparison (per-section logic)
│   │   │   └── sections/    # Section-specific comparators
│   │   ├── discovery/       # Endpoint discovery helpers
│   │   ├── parser/          # client-native wrapper for syntax parsing
│   │   ├── synchronizer/    # Operation execution with retries
│   │   ├── types/           # Endpoint, SyncOptions, Result
│   │   └── validators/      # Per-model validators generated from OpenAPI specs
│   ├── webhook/             # HTTP server shared by validating webhook and health probes
│   └── controller/          # Event-driven orchestration (adapters + components)
│       ├── component/       # Shared event-loop scaffold embedded by most components
│       ├── buffers/         # Byte-buffer pool used by render/validation hot paths
│       ├── certloader/      # Parses TLS certificates from referenced Secrets
│       ├── coalesce/        # Coalesces bursts of events into a single work item
│       ├── commentator/     # Structured domain-aware log producer
│       ├── configchange/    # Reacts to config changes and coordinates reloads
│       ├── configloader/    # Parses HAProxyTemplateConfig CRD into internal config
│       ├── configpublisher/ # Publishes rendered config to HAProxyCfg CRD
│       ├── conversion/      # Converts CRD types <-> internal config structs
│       ├── credentialsloader/ # Parses dataplane credentials from Secret
│       ├── currentconfigstore/ # Caches the last-deployed HAProxy config for templates
│       ├── debug/           # Controller-specific introspection Vars
│       ├── deployer/        # Scheduler, executor, drift-prevention monitor
│       ├── discovery/       # HAProxy pod discovery
│       ├── dryrunvalidator/ # Webhook dry-run validator
│       ├── events/          # Domain event type catalog
│       ├── executor/        # Reconciliation orchestrator
│       ├── indextracker/    # Tracks initial-sync completion across resources
│       ├── leaderelection/  # Event adapter around pkg/k8s/leaderelection
│       ├── leadership/      # Gating utilities for leader-only components
│       ├── metrics/         # Controller-domain metrics adapter (reconciliation,
│       │                    #   deployment, validation, event-bus counters)
│       ├── pipeline/        # Chains stages into a composable reconciliation pipeline
│       ├── proposalvalidator/ # Validates proposed configs from the webhook
│       ├── reconciler/      # Debounces resource changes, triggers reconciliation
│       ├── rendercontext/   # Builds the template context from stores and HTTP resources
│       ├── renderer/        # Template rendering adapter
│       ├── resourceloader/  # Thin wrapper over component.Base for loader components
│       │                    #   (configloader, credentialsloader, certloader)
│       ├── resourcestore/   # Store manager (wraps typed k8s stores for overlays)
│       ├── resourcewatcher/ # Lifecycle manager for all configured resource watchers
│       ├── statusapplier/   # Applies status subresources on CRDs
│       ├── testrunner/      # Runs embedded validation tests from template libraries
│       ├── timeouts/        # Timeout helpers used by components
│       ├── timers/          # Periodic event emitters (drift prevention, etc.)
│       ├── validation/      # Shared validation helpers
│       └── validator/       # Syntax/semantic validators (scatter-gather + HAProxy)
├── tests/
│   ├── acceptance/          # End-to-end tests with debug endpoint + metrics assertions
│   └── integration/         # Cross-component integration tests
└── tools/linters/
    └── eventimmutability/   # Custom golangci-lint analyzer (enforces pointer receivers on Event)
```

Each package has its own `README.md` (user-facing API) and `CLAUDE.md` (developer context). Prefer those as the authoritative reference — this file only orients new contributors.

## Dependency Rules

The packages form a DAG, enforced at build time by `arch-go.yml`:

1. `pkg/events` depends on nothing in `pkg/` — it is plain pub/sub plumbing.
2. Domain libraries (`pkg/k8s`, `pkg/dataplane`, `pkg/templating`, `pkg/stores`, `pkg/httpstore`, `pkg/webhook`) depend only on `pkg/core`, `pkg/events` (for optional observability hooks), and each other through narrow interfaces. They have no knowledge of the controller.
3. `pkg/controller` is the only package allowed to import everything. It owns the event adapters and the startup/shutdown choreography.
4. Domain event types live in `pkg/controller/events`, never in `pkg/events`.

`pkg/stores` is deliberately isolated from `pkg/k8s` (see `arch-go.yml`); the two declare the same `Store` interface shape, and `pkg/stores.TypesStoreAdapter` bridges across the package boundary.

## Key Patterns

**Shared component scaffold.** `pkg/controller/component.Base` implements the event-loop boilerplate (subscribe-on-construction, single-flight dispatch, panic recovery, ready/done signalling). Components embed `*Base` and implement `EventHandler`. It consolidates what used to be two copies of the same scaffold in `BaseLoader` and `BaseValidator`; those types still exist as thin wrappers for familiarity.

**Pure libraries, event adapters.** Business logic (`pkg/templating`, `pkg/dataplane`, `pkg/k8s`) exposes plain Go APIs. Corresponding adapters in `pkg/controller/renderer`, `pkg/controller/deployer`, `pkg/controller/resourcewatcher`, etc. translate events into calls and publish result events.

**Scatter-gather validation.** Configuration validation uses `pkg/events` request/response to fan a `ConfigValidationRequest` out to independent validators (structural, template syntax, JSONPath, HAProxy config) and aggregate responses.

**Single-resource vs. bulk watching.** `pkg/k8s/watcher` provides `Watcher` (collections, debounced) and `SingleWatcher` (one named resource, immediate callbacks) so the `HAProxyTemplateConfig` CRD and credentials Secret don't pay indexing overhead.

**Three-phase HAProxy validation.** `pkg/dataplane` exposes `ValidateConfiguration(mainConfig, auxFiles, paths, version, skipDNSValidation)` which runs (1) the client-native parser for syntax, (2) OpenAPI schema validation against the version-specific DataPlane API spec, and (3) the `haproxy -c` binary check for semantics. Results are cached by `(configHash, auxHash, versionHash)` so the same rendered output isn't re-validated during drift-prevention. Writes auxiliary files to the real HAProxy paths under a mutex (`haproxyCheckMutex` in `validate_haproxy.go`) so file references resolve exactly like at runtime.

**Component lifecycle.** `pkg/lifecycle` centralises registration, dependency ordering, leader-only flags, and health tracking; the controller registers every component there instead of starting goroutines directly.

## Finding Things

- **Public API of a library** → `pkg/<name>/README.md`.
- **Developer guidance when editing a package** → `pkg/<name>/CLAUDE.md`.
- **Event types and their producers/consumers** → `pkg/controller/events/`.
- **Where a particular event is handled** → grep for the event type name; the event adapter's `HandleEvent` method names the case.
