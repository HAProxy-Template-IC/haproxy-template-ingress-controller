# Metrics and Observability

## Purpose

Prometheus metrics, health endpoints, and structured logging for monitoring controller behavior and diagnosing issues.

## Requirements

### Requirement: Prometheus Metrics Endpoint

The controller SHALL expose Prometheus metrics on a configurable HTTP port (default 9090). The metrics endpoint SHALL serve at the standard `/metrics` path using the Prometheus client library.

#### Scenario: Metrics served on default port

WHEN the controller starts without explicit metrics port configuration
THEN Prometheus metrics SHALL be available at `http://<pod-ip>:9090/metrics`.

#### Scenario: Metrics served on custom port

WHEN the controller is configured with a custom metrics port
THEN Prometheus metrics SHALL be available on the configured port.

### Requirement: Reconciliation Metrics

The controller SHALL record reconciliation metrics: a histogram for reconciliation duration, a counter for total reconciliation cycles, and a counter for reconciliation errors. These metrics SHALL distinguish between successful and failed reconciliations.

#### Scenario: Reconciliation duration recorded

WHEN a reconciliation cycle completes
THEN the duration SHALL be recorded in the reconciliation duration histogram.

#### Scenario: Reconciliation error counted

WHEN a reconciliation cycle fails with an error
THEN the reconciliation error counter SHALL be incremented.

#### Scenario: Total reconciliation counter incremented

WHEN a reconciliation cycle completes (success or failure)
THEN the total reconciliation counter SHALL be incremented.

### Requirement: Deployment Metrics

The controller SHALL record deployment metrics: a histogram for deployment duration, and counters for successful and failed deployments to HAProxy pods.

#### Scenario: Deployment duration recorded on success

WHEN a configuration deployment to an HAProxy pod succeeds
THEN the deployment duration SHALL be recorded in the deployment duration histogram.

#### Scenario: Failed deployment counted

WHEN a configuration deployment to an HAProxy pod fails
THEN the deployment failure counter SHALL be incremented.

### Requirement: Resource Count Gauges

The controller SHALL maintain gauges for the count of watched Kubernetes resources. Resource count gauges SHALL be updated via delta operations using Created and Deleted counts from ChangeStats, not by recounting all resources.

#### Scenario: Resource gauge incremented on creation

WHEN a ChangeStats with Created=3 is received for a resource type
THEN the gauge for that resource type SHALL be incremented by 3.

#### Scenario: Resource gauge decremented on deletion

WHEN a ChangeStats with Deleted=2 is received for a resource type
THEN the gauge for that resource type SHALL be decremented by 2.

### Requirement: Fleet Discovery Rejection Metric

The controller SHALL expose a counter `haptic_haproxy_pods_rejected_total`, labelled by `reason`, incremented once per HAProxyPodRejectedEvent published by the discovery component. The reason labels SHALL be stable strings: `version_mismatch_older` and `version_mismatch_newer` for permanent major-version rejections, and `version_check_failed` for transient probe failures. Persistent growth of this counter indicates the controller cannot admit its deployed HAProxy pods.

#### Scenario: Rejection increments the counter

- **WHEN** discovery rejects a pod whose Dataplane API major version is newer than the controller's series
- **THEN** `haptic_haproxy_pods_rejected_total{reason="version_mismatch_newer"}` SHALL be incremented by 1.

#### Scenario: Probe failure counted under its own reason

- **WHEN** a pod's version probe fails transiently during a discovery cycle
- **THEN** the counter SHALL be incremented with reason `version_check_failed` for that cycle.

### Requirement: Runtime Fast-Path Metrics

The controller SHALL record every runtime-eligible fast-path apply attempt across four counters: `haptic_runtime_fast_path_fires_total` (every attempt, one per pod per reconcile), `haptic_runtime_fast_path_applies_total` (attempts that applied at least one runtime-eligible server update), `haptic_runtime_fast_path_server_updates_total` (total server updates applied via the fast path), and `haptic_runtime_fast_path_failures_total` (attempts that errored — best-effort, since the scheduled deploy remains the correctness floor). A failed attempt SHALL increment only the fires and failures counters; a successful attempt with zero updates SHALL increment only fires.

#### Scenario: Successful apply recorded

- **WHEN** a fast-path attempt applies 3 server updates successfully
- **THEN** fires SHALL increment by 1, applies by 1, and server updates by 3.

#### Scenario: Failed attempt recorded without applies

- **WHEN** a fast-path attempt errors
- **THEN** fires and failures SHALL each increment by 1 and the applies and server-update counters SHALL be unchanged.

### Requirement: Health Endpoint

The controller SHALL expose a fail-closed health endpoint at `/healthz` (with `/health` as an alias) on the introspection/debug port — there is no separate health listener, so a debug port of 0 (the binary default; the chart sets 8080) disables the endpoint along with the rest of the debug server. The endpoint SHALL return HTTP 503 until BOTH gates pass: the iteration's staged startup has finished (the `initialized` bit set at the very end of the iteration), AND every component in the lifecycle registry has left the transient Pending/Starting states. Both StatusRunning and StatusStandby SHALL count as healthy, so a follower replica reports 200 as soon as its staged startup completes (its leader-only components sit in Standby), while the leader reports 200 only after its leader-only components reach Running.

The response body SHALL be a JSON object with a `status` field (`ok` or `degraded`) and a `components` map containing one entry per registered component plus an `initialized` entry; when pluggable validators are configured, a `pluggable-validators` entry SHALL summarize them (healthy when all sockets respond; otherwise its error lists every failing `<name>: <reason>` pair semicolon-joined), and the entry SHALL be omitted entirely when none are configured. While `initialized` is unhealthy its error SHALL name the gate — `controller still initializing` during staged startup, or the first still-pending component (for example noting that leader election may not have acquired the lease yet). Before staged startup installs the full checker, an early checker SHALL serve the endpoint reporting `initialized` false plus a `config` entry describing config-load progress.

#### Scenario: 503 until staged startup completes

- **WHEN** a GET request hits `/healthz` before the iteration has finished its staged startup
- **THEN** the response SHALL be HTTP 503 with an `initialized` entry whose error explains what is still pending.

#### Scenario: Follower reports healthy in standby

- **WHEN** a non-leader replica finishes staged startup and its leader-only components are in Standby
- **THEN** `/healthz` SHALL return HTTP 200.

#### Scenario: Per-component detail in the body

- **WHEN** one registered component is unhealthy after initialization
- **THEN** the response SHALL be HTTP 503 with `status` `degraded` and that component's entry carrying `healthy: false` and its error, while healthy components keep their own entries.

#### Scenario: Failing validator socket named

- **WHEN** pluggable validators are configured and one socket is unreachable
- **THEN** the `pluggable-validators` entry SHALL be unhealthy and its error SHALL name the failing validator and reason.

### Requirement: Reinitialization Grace Window

After the controller has been fully initialized at least once in its process lifetime, a voluntary iteration restart (config, CRD, or credentials change) SHALL enter a 90-second grace window during which the health checker rewrites unhealthy component entries as healthy with the annotation `reinitializing (grace period): <original error>` — softening only the aggregate HTTP status so the kubelet does not kill the pod mid-rebuild, while preserving the underlying detail for operators. The grace SHALL end at the earlier of window expiry or the iteration being observed fully healthy once; after settling, any later unhealthiness SHALL surface immediately for the rest of the iteration. The grace SHALL apply only after a first successful initialization, so a fresh pod with a bad configuration still crash-loops under the fail-closed startup contract.

#### Scenario: Reinit does not flip liveness

- **WHEN** a config change restarts the iteration on a previously initialized controller and components are still rebuilding 30 seconds in
- **THEN** `/healthz` SHALL return HTTP 200 with the affected entries annotated as reinitializing.

#### Scenario: Settling ends the grace early

- **WHEN** the rebuilt iteration is observed fully healthy once and a component then fails within the original 90-second window
- **THEN** the failure SHALL surface as HTTP 503 immediately rather than being masked for the remainder of the window.

#### Scenario: Fresh pod stays fail-closed

- **WHEN** a controller pod that has never completed initialization is unhealthy
- **THEN** no grace SHALL apply and `/healthz` SHALL report HTTP 503.

### Requirement: Structured JSON Logging

The controller SHALL use the `slog` structured logging package. Log output SHALL be formatted as logfmt (key=value text). Each log entry SHALL include structured fields (component, resource identifiers, durations) as key-value attributes rather than interpolated strings.

#### Scenario: Log entries formatted as logfmt

WHEN the controller emits a log message
THEN the output SHALL be a logfmt-formatted line containing at minimum a level, message, and timestamp as key=value pairs.

#### Scenario: Structured fields included in log entries

WHEN a component logs a reconciliation event
THEN the log entry SHALL include structured attributes such as component name and relevant resource identifiers.

### Requirement: Configurable Log Levels

The controller SHALL support configurable log levels: TRACE, DEBUG, INFO, WARN, and ERROR. The log level SHALL be configurable via environment variable or CLI flag. Messages below the configured level SHALL be suppressed.

#### Scenario: Default log level is INFO

WHEN the controller starts without explicit log level configuration
THEN only INFO, WARN, and ERROR messages SHALL be emitted.

#### Scenario: DEBUG level enables verbose output

WHEN the log level is set to DEBUG
THEN DEBUG, INFO, WARN, and ERROR messages SHALL be emitted.

### Requirement: Asynchronous Metrics Updates

Metrics SHALL be updated asynchronously via the event-driven architecture. Metrics recording SHALL NOT block the event processing loop.

#### Scenario: Metrics update does not block event processing

WHEN a metrics-relevant event is published on the event bus
THEN the metrics component SHALL process the event without blocking the publisher.
