# controller-lifecycle Specification

## Purpose

Defines the controller's iteration-based lifecycle: an infinite reinitialization loop that builds every component from a freshly loaded configuration, tears everything down on a config or credentials change, and rebuilds. This shape makes configuration changes converge without pod restarts while keeping a small set of infrastructure servers stable across rebuilds. It also pins the startup configuration contract (flags, environment variables, defaults) and the shutdown budget.

## Requirements

### Requirement: Reinitialization Loop

The controller entry point SHALL be a package-level `Run` function (no controller struct) that executes iterations in an infinite loop. Each iteration SHALL exit on exactly one of two signals: cancellation of the main context (shutdown — `Run` SHALL return nil, treating graceful shutdown as success even mid-iteration), or a value received on the config-change channel (reinitialization — the loop SHALL immediately start the next iteration). When an iteration fails with an error, the loop SHALL log the error and retry after a `RetryDelay` of 5 seconds. While waiting for the initial HAProxyTemplateConfigs to exist (fresh-install race where the pod starts before the resources are applied), the controller SHALL poll at a `ConfigPollInterval` of 5 seconds. It SHALL wait for EVERY configured resource, not the first: a partial set is as unusable as none, since the libraries an operator's config overrides may not have been applied yet.

#### Scenario: Iteration failure retries after 5 seconds

- **WHEN** an iteration returns an error and the main context is not cancelled
- **THEN** the controller SHALL wait 5 seconds and start a new iteration rather than exiting.

#### Scenario: Config change restarts the iteration immediately

- **WHEN** a validated configuration arrives on the config-change channel
- **THEN** the current iteration SHALL tear down and the next iteration SHALL start immediately, without any retry delay.

#### Scenario: Shutdown during a failing iteration is not an error

- **WHEN** the main context is cancelled while an iteration is running or failing
- **THEN** `Run` SHALL return nil.

### Requirement: Staged Iteration Startup

Each iteration SHALL execute a numbered startup sequence: (0) construct the config-management components and the debug EventBuffer (buffer size 1000) before any config is fetched, with the EventBus created with a pre-start buffer of 100 events; (0.5) start or re-point the early infrastructure servers so `/healthz` answers before config load; (1–2) wait for every configured HAProxyTemplateConfig to exist, then fetch them in parallel, merge them in configured order, and structurally validate the merged result together with the credentials Secret; (2.4) resolve the effective configuration against live API discovery and start the CRD watch; (2.5) run the fail-closed validationTests load gate; (3) create resource watchers and wait for their initial sync; (4) start the CRD and credentials-Secret SingleWatchers and wait for their sync; (4.5) populate the CurrentConfigStore; (5) construct the reconciliation components; (6.x) construct the pluggable-validator manager and the webhook validators; (6.5) call `EventBus.Start()`; (7) initialize leader election; (8) set up the webhook server; (9) register debug variables and install the full health checker; (10) call `EnableReinitialization()`; (11) flip the `initialized` health bit.

Every component SHALL subscribe to the EventBus in its constructor, before `EventBus.Start()` releases the pre-start buffer, so no buffered event is lost; the only exception is leader-only components, which subscribe on leadership (see the leader-election capability). Teardown callbacks registered during setup SHALL run in reverse-registration (LIFO) order exactly once when the iteration exits.

#### Scenario: EventBus starts only after all constructors have subscribed

- **WHEN** the iteration reaches stage 6.5
- **THEN** every all-replica component SHALL already hold a subscription created in its constructor, and events published before `EventBus.Start()` SHALL be delivered to them from the pre-start buffer.

#### Scenario: Cleanups run LIFO on teardown

- **WHEN** an iteration exits (shutdown or reinitialization) after registering multiple cleanup callbacks
- **THEN** the callbacks SHALL run in reverse-registration order, and a second teardown pass SHALL be a no-op.

#### Scenario: Required-but-unserved resource fails the iteration fast

- **WHEN** effective-config resolution at stage 2.4 finds a required watched resource with no served version
- **THEN** the iteration SHALL return an error (retried by the run loop) instead of hanging in informer sync.

### Requirement: Persistent Infrastructure Across Iterations

The introspection HTTP server (debug port) and the metrics server SHALL be created once in `Run`, before the reinitialization loop, and reused by every iteration. Both SHALL be served with the main context — not the iteration context — so their listeners never rebind during rapid reinitializations. The first iteration SHALL set up routes and start serving; subsequent iterations SHALL only re-point the health checker and swap the metrics registry. At the start of every iteration the controller SHALL clear the persistent introspection registry, install a fresh per-iteration Prometheus registry via `SetRegistry` (so the previous iteration's metrics are garbage-collected), and create a fresh per-iteration health state. All other state — the EventBus, every component, all watchers, leader election, and the webhook server — SHALL be torn down and rebuilt per iteration.

#### Scenario: Reinitialization does not rebind ports

- **WHEN** a configuration change triggers an iteration restart
- **THEN** the introspection and metrics listeners SHALL keep serving on their existing sockets without closing or rebinding.

#### Scenario: Metrics registry swapped per iteration

- **WHEN** a new iteration begins on a controller whose metrics server is already running
- **THEN** the server SHALL serve the new iteration's registry and the previous iteration's collectors SHALL no longer be scraped.

### Requirement: Fail-Closed validationTests Load Gate

On every iteration load — fresh pod, helm upgrade, or reinitialization — the controller SHALL run the configuration's embedded validationTests synchronously with a suite budget of 120 seconds before proceeding past stage 2.5. If the suite fails, does not complete within the budget, or cannot be set up, the iteration SHALL return an error, leaving the controller un-initialized so `/healthz` reports 503, the liveness probe restarts the pod, and the bad configuration surfaces as CrashLoopBackOff — a rolling upgrade therefore stalls on the old, healthy pods instead of rolling out the break. A configuration with no validationTests SHALL pass this gate at zero cost.

This load-path budget is deliberately distinct from and larger than the live-change gate: a config change on a running controller is validated via scatter-gather with a 45-second envelope, inside which the validationtests validator applies its own 25-second run cap.

#### Scenario: Failing tests on a fresh pod crash-loop

- **WHEN** a fresh controller pod loads a HAProxyTemplateConfig whose embedded validationTests fail
- **THEN** the iteration SHALL error out, `/healthz` SHALL stay 503, and the pod SHALL be restarted by its liveness probe rather than serving the config.

#### Scenario: Suite timeout counts as failure

- **WHEN** the validationTests suite does not complete within 120 seconds
- **THEN** the load SHALL fail with an incompleteness error rather than admitting the config.

#### Scenario: Load budget exceeds the live-change run cap

- **WHEN** a suite legitimately needs more than the live-change validator's 25-second run cap (e.g. a cold, contended node)
- **THEN** the load-path gate SHALL still allow it up to 120 seconds.

### Requirement: Config and Credentials Change Funnel

Each configured HAProxyTemplateConfig and the Dataplane credentials Secret SHALL each be watched by their own SingleWatcher, with a change to any one config re-merging the whole set, and every change SHALL funnel through the ConfigChangeHandler into a single config-change channel of capacity 1 — there SHALL be no per-component hot-reload path. A credentials rotation SHALL restart the iteration through this same channel, so the new iteration re-fetches the rotated Secret from the API server before any component (notably the webhook server) starts. Sends onto the channel SHALL be non-blocking: with a reinitialization already queued, further signals are subsumed by it.

Bootstrap suppression SHALL prevent reinitialization loops: the initial CRD resourceVersion and the initial Secret resourceVersion recorded at load time SHALL be filtered out when the watchers re-observe them, and the synthetic bootstrap version literal `initial` SHALL always be ignored. Reinitialization signaling SHALL be disabled entirely until `EnableReinitialization()` is called at the end of staged startup. Reinitialization signals SHALL be debounced by 2 seconds (the default reinit debounce interval) so rapid successive edits coalesce into one restart.

#### Scenario: Credentials rotation restarts the iteration

- **WHEN** the credentials Secret's resourceVersion changes after startup completes
- **THEN** the ConfigChangeHandler SHALL signal reinitialization through the config-change channel, and the new iteration SHALL load the rotated Secret before starting any component.

#### Scenario: Bootstrap events do not loop

- **WHEN** the CRD or Secret watcher fires its initial observation carrying the same resourceVersion recorded at iteration load, or an event carries the synthetic version `initial`
- **THEN** no reinitialization SHALL be signalled.

#### Scenario: Rapid edits coalesce into one restart

- **WHEN** several validated config changes arrive within the 2-second debounce window
- **THEN** exactly one reinitialization signal SHALL be sent, carrying the latest validated config.

#### Scenario: Changes during startup are ignored

- **WHEN** a ConfigValidatedEvent arrives before `EnableReinitialization()` has been called
- **THEN** the handler SHALL skip it without signalling reinitialization.

### Requirement: Startup Configuration Contract

The `run` command SHALL resolve its configuration with the precedence CLI flag > environment variable > default:

| Flag | Env var | Default |
|------|---------|---------|
| `--crd-name` | `CRD_NAME` | `haproxy-config` |
| `--secret-name` | `SECRET_NAME` | `haproxy-credentials` |
| `--webhook-cert-dir` | `WEBHOOK_CERT_DIR` | empty (webhook disabled) |
| `--debug-port` | `DEBUG_PORT` | `0` (introspection server disabled) |

The metrics port SHALL be read only from the `METRICS_PORT` environment variable (default 9090, `0` disables); the CRD's `controller.metricsPort` field SHALL NOT be read by the controller. The `LOG_LEVEL` environment variable SHALL set the initial dynamic log level, overridable at runtime by the CRD's `spec.logging.level`. The controller SHALL set `GOMEMLIMIT` from the cgroup memory limit at a 0.9 ratio and route klog output through slog. `SIGTERM`/`SIGINT` SHALL cancel the root context for graceful shutdown, and a context-cancellation error from `Run` SHALL NOT be surfaced as a process failure.

#### Scenario: Flag overrides environment variable

- **WHEN** both `--crd-name` and `CRD_NAME` are set to different values
- **THEN** the controller SHALL use the flag value.

#### Scenario: Metrics port ignores the CRD field

- **WHEN** the HAProxyTemplateConfig sets `controller.metricsPort` but `METRICS_PORT` is unset
- **THEN** the metrics server SHALL listen on 9090.

#### Scenario: Signal-driven shutdown exits cleanly

- **WHEN** the process receives SIGTERM
- **THEN** the controller SHALL shut down gracefully and exit without reporting a failure.

### Requirement: Shutdown Budget

Iteration teardown (on shutdown or reinitialization) SHALL first stop the leader-only components, then cancel the iteration context, then wait for all iteration goroutines to finish with a `ShutdownTimeout` of 25 seconds — deliberately below the Kubernetes default 30-second termination grace period so the process exits cleanly before the kubelet's SIGKILL — logging progress every 5 seconds while waiting, and finally run the registered cleanups.

#### Scenario: Goroutine wait is bounded

- **WHEN** iteration goroutines have not finished 25 seconds after teardown began
- **THEN** the controller SHALL log a timeout warning and proceed with teardown instead of waiting indefinitely.
