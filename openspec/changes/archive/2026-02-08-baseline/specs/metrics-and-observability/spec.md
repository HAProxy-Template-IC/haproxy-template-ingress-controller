# Metrics and Observability

Prometheus metrics, health endpoints, and structured logging for monitoring controller behavior and diagnosing issues.

## ADDED Requirements

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

### Requirement: Health Endpoint

The controller SHALL expose a `/health` endpoint for Kubernetes liveness probes. The endpoint SHALL return HTTP 200 when the controller process is running.

#### Scenario: Health endpoint returns 200

WHEN a GET request is made to the `/health` endpoint
THEN the response status code SHALL be 200.

### Requirement: Structured JSON Logging

The controller SHALL use the `slog` structured logging package. Log output SHALL be formatted as JSON. Each log entry SHALL include structured fields (component, resource identifiers, durations) as key-value attributes rather than interpolated strings.

#### Scenario: Log entries formatted as JSON

WHEN the controller emits a log message
THEN the output SHALL be a valid JSON object containing at minimum a level, message, and timestamp.

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
