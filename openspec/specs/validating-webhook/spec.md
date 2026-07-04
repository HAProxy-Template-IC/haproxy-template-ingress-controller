# Validating Webhook

## Purpose

Admission webhook that validates Kubernetes resources (HAProxyTemplateConfig and watched resources) by performing dry-run rendering and HAProxy syntax validation before admitting changes to the cluster.

## Requirements

### Requirement: Webhook Server

The webhook SHALL be served over HTTPS using TLS certificates loaded from a Kubernetes Secret. The server SHALL listen on port 9443 by default at the `/validate` path. Read and write timeouts SHALL both be 10 seconds. The server SHALL expose a `/healthz` endpoint returning HTTP 200. The server SHALL handle concurrent requests in a thread-safe manner. When the context is cancelled, the server SHALL perform graceful shutdown with a 30-second timeout.

#### Scenario: TLS certificate validation on startup

WHEN the webhook component starts with empty CertPEM or KeyPEM
THEN startup SHALL fail with an error indicating the certificate or key is empty.

#### Scenario: Health endpoint available

WHEN an HTTP GET request is sent to `/healthz`
THEN the server SHALL respond with HTTP 200 and body `"ok"`.

#### Scenario: Non-POST request rejected

WHEN a non-POST HTTP request is sent to the validation endpoint
THEN the server SHALL respond with HTTP 405 Method Not Allowed.

### Requirement: Admission Request Handling

The webhook SHALL accept AdmissionReview v1 requests from the Kubernetes API server. It SHALL parse the resource object as `unstructured.Unstructured` (consistent with resource store types). For UPDATE and DELETE operations, it SHALL also parse the old object. The response SHALL set the UID from the request for correlation.

#### Scenario: CREATE request with new resource

WHEN an AdmissionReview with operation CREATE arrives
THEN the webhook SHALL parse `request.Object.Raw` as unstructured, pass it to the validator with operation `"CREATE"`, and return an AdmissionResponse with the matching UID.

#### Scenario: UPDATE request with old and new objects

WHEN an AdmissionReview with operation UPDATE arrives
THEN the webhook SHALL parse both `request.Object.Raw` and `request.OldObject.Raw` as unstructured, making both available in the ValidationContext.

#### Scenario: No validator registered for resource type

WHEN an AdmissionReview arrives for a GVK with no registered validator
THEN the webhook SHALL return `Allowed: true` (fail-open for unregistered types).

### Requirement: Intercepted Operations

The webhook SHALL intercept CREATE and UPDATE operations on resources configured in the webhook rules. Webhook rules SHALL be extracted from the controller configuration based on watched resources with `webhookValidation` enabled. Each rule SHALL specify API groups, API versions, resource types, and operations.

#### Scenario: Watched resource with webhook enabled

WHEN a watched resource has `webhookValidation: true` in the configuration
THEN the webhook SHALL register a validator for that resource's GVK and intercept CREATE and UPDATE operations.

#### Scenario: Watched resource without webhook disabled

WHEN a watched resource does not have `webhookValidation` enabled
THEN the webhook SHALL NOT register a validator for that resource type.

### Requirement: GVK Resolution

The webhook component SHALL use a Kubernetes RESTMapper (backed by API server discovery) to resolve plural resource names (e.g., `ingresses`) to their singular Kind (e.g., `Ingress`). The GVK string SHALL use the format `"group/version.Kind"` for named groups or `"version.Kind"` for core API resources.

#### Scenario: Core resource GVK format

WHEN a webhook rule references core API resource `services` with version `v1`
THEN the GVK SHALL be formatted as `"v1.Service"`.

#### Scenario: Named group GVK format

WHEN a webhook rule references `ingresses` in group `networking.k8s.io` version `v1`
THEN the GVK SHALL be formatted as `"networking.k8s.io/v1.Ingress"`.

### Requirement: Three-Phase Dry-Run Validation

The DryRunValidator SHALL perform validation in phases: (1) Template rendering using an overlay store that simulates the proposed resource change, (2) HAProxy syntax validation of the rendered configuration, and (3) Embedded test execution if validation tests are configured. A failure in any phase SHALL reject the admission request with a user-readable reason. At admission, the render-validate pipeline (phases 1 and 2) SHALL run synchronously via the DryRunValidator on the caller's context, which the webhook bounds to 9 seconds. The 30-second `DefaultValidationTimeout` applies only to the event-driven proposal path (ProposalValidator's `ProcessProposal`), which the webhook does NOT use.

#### Scenario: Rendering failure rejects admission

WHEN template rendering fails because the proposed Ingress references a nonexistent Service
THEN the admission response SHALL be `Allowed: false` with the simplified rendering error as the reason (e.g., `Service 'api' not found`).

#### Scenario: HAProxy validation failure rejects admission

WHEN the rendered configuration fails HAProxy syntax validation
THEN the admission response SHALL be `Allowed: false` with the simplified validation error as the reason.

#### Scenario: Validation tests fail rejects admission

WHEN the configuration passes rendering and syntax validation but an embedded validation test fails
THEN the admission response SHALL be `Allowed: false` with a detailed message listing failed test names and assertion failures.

#### Scenario: All phases pass allows admission

WHEN template rendering succeeds, HAProxy syntax validation passes, and all embedded tests pass
THEN the admission response SHALL be `Allowed: true`.

### Requirement: Overlay Store Pattern

The DryRunValidator SHALL create a temporary store overlay to simulate the proposed resource change without modifying actual resource stores. For CREATE operations, the overlay SHALL add the new resource. For UPDATE operations, the overlay SHALL replace the existing resource. For DELETE operations, the overlay SHALL mark the resource for removal. The overlay SHALL reference actual stores for all other resource types. Overlays SHALL be discarded after validation completes.

#### Scenario: CREATE overlay adds resource

WHEN a CREATE admission request arrives for an Ingress
THEN the overlay store for `ingresses` SHALL contain all existing Ingresses plus the new one being created.

#### Scenario: UPDATE overlay replaces resource

WHEN an UPDATE admission request arrives for an Ingress
THEN the overlay store for `ingresses` SHALL contain the updated version of the Ingress, replacing the existing one.

#### Scenario: Other resource types unaffected

WHEN a CREATE admission request arrives for an Ingress
THEN the overlay stores for `services`, `endpoints`, and other resource types SHALL read directly from actual stores without modification.

### Requirement: Error Simplification for Webhook Responses

Rendering errors SHALL be simplified by extracting `fail()` messages and removing stack traces. HAProxy validation errors SHALL be simplified by extracting meaningful error descriptions and removing timestamps, process IDs, and file paths. The simplification phase SHALL be determined by the `phase` field of the validation result (`render`, `syntax`, `schema`, or `semantic`).

#### Scenario: Render error simplified

WHEN the render phase fails with a full template stack trace containing `fail("Service 'api' not found")`
THEN the webhook reason SHALL be `Service 'api' not found`.

#### Scenario: Syntax validation error simplified

WHEN the syntax phase fails with a raw HAProxy error
THEN the webhook reason SHALL contain only the meaningful error description without timestamps or temp file paths.

### Requirement: Webhook Test Execution

When a prospective HAProxyTemplateConfig includes embedded validation tests, the webhook ConfigValidator SHALL execute them (via `configtest.RunValidationTests`) after successful rendering and syntax validation. The DryRunValidator SHALL NOT run validation tests. The run SHALL be bounded by a fixed budget (`min(budget, time left on the admission context)`) so it cannot approach the webhook timeout. Failed tests SHALL deny admission with an error message listing the failed test names and assertion descriptions. A run that cannot start or does not finish within the budget SHALL admit with a warning, deferring authoritative enforcement to the controller's load gate.

#### Scenario: Failed validation test denies admission

WHEN the ConfigValidator runs validation tests for a prospective config and a test fails
THEN the admission request SHALL be denied with a reason listing the failed tests.

#### Scenario: Incomplete test run admits with warning

WHEN validation test execution cannot finish within the admission budget
THEN the admission request SHALL be admitted with a warning, leaving the controller's load gate to enforce the tests on load.

### Requirement: Fail-Open Without Validator

When the DryRunValidator is nil (not configured), the webhook component SHALL allow all requests (fail-open) and log a warning. This occurs when no webhook rules are extracted from the configuration.

#### Scenario: Fail-open when no validator configured

WHEN a validation request arrives and the DryRunValidator is nil
THEN the webhook SHALL return `Allowed: true` and log a warning about the missing validator.

### Requirement: Basic Structural Validation

Before dry-run validation, the webhook SHALL perform basic structural checks on the resource object. The object MUST be a valid `unstructured.Unstructured` resource. Either `metadata.name` or `metadata.generateName` MUST be present. Failure of basic validation SHALL reject the request without invoking the dry-run validator.

#### Scenario: Missing name and generateName rejected

WHEN an admission request contains an object with neither `metadata.name` nor `metadata.generateName`
THEN the webhook SHALL return `Allowed: false` with reason `"metadata.name or metadata.generateName is required"`.

### Requirement: Webhook Timeout

The webhook component SHALL enforce a 10-second read/write timeout on the HTTPS server. The dry-run validation call to ValidateDirect SHALL use a 9-second context timeout, and the render-validate pipeline SHALL run synchronously on that 9-second context at admission. The 30-second `DefaultValidationTimeout` SHALL apply only to the event-driven proposal path (ProposalValidator's `ProcessProposal`), not to admission.

#### Scenario: Server read/write timeout

WHEN an admission request takes longer than 10 seconds to read or the response takes longer than 10 seconds to write
THEN the server SHALL terminate the connection.

#### Scenario: Validation timeout

WHEN dry-run validation exceeds 9 seconds
THEN the context SHALL be cancelled and the validation SHALL fail.

### Requirement: Cert-Directory Gate

The webhook server SHALL run if and only if a TLS certificate directory is configured (the WEBHOOK_CERT_DIR environment variable or the corresponding flag is non-empty; the chart mounts the cert Secret and sets the variable when webhook support is enabled). The cert directory — not the presence of watched-resource webhook rules — SHALL be the operative gate: the ValidatingWebhookConfiguration may route HAProxyTemplateConfig admission to the controller even when no watched resource enables admission validation. The server port SHALL be 9443.

#### Scenario: Empty cert dir disables the webhook

- **WHEN** WEBHOOK_CERT_DIR is empty
- **THEN** no webhook server SHALL be started, regardless of the configuration's webhook rules.

#### Scenario: Cert dir without watched-resource rules still serves config admission

- **WHEN** WEBHOOK_CERT_DIR is set and no watched resource enables admission validation
- **THEN** the webhook server SHALL start on port 9443 serving HAProxyTemplateConfig admission.

### Requirement: Config Validator Always Wired

When webhook validators are constructed, the HAProxyTemplateConfig admission ConfigValidator SHALL ALWAYS be built, independent of watched-resource webhook rules — HAProxyTemplateConfig admission must never fall through the server's fail-open path for unregistered validators. The watched-resource DryRunValidator SHALL be built only when at least one watched resource sets `enableValidationWebhook: true`; without such rules its overlay-store and test-runner setup is skipped.

#### Scenario: Config admission validated with zero watched-resource rules

- **WHEN** no watched resource has enableValidationWebhook enabled and an HAProxyTemplateConfig admission request arrives
- **THEN** the ConfigValidator SHALL validate it (render, syntax check, embedded tests) rather than admitting via fail-open.

#### Scenario: DryRunValidator built only on demand

- **WHEN** at least one watched resource sets enableValidationWebhook: true
- **THEN** the DryRunValidator SHALL be constructed and registered for those resources' GVKs.

### Requirement: Internal Admission Deadlines Under the Chart Timeout

Both internal admission deadlines SHALL be 9 seconds: the watched-resource dry-run deadline (schema bootstrap plus render plus `haproxy -c`) and the HAProxyTemplateConfig deadline (per-admission schema bootstrap of at most 2 seconds, render plus `haproxy -c`, and the 5-second embedded validation-test budget). Both SHALL stay under the chart's 10-second `timeoutSeconds` so the controller returns a structured admission decision before the API server gives up and applies the failurePolicy to a transport failure.

#### Scenario: Slow validation produces a structured decision

- **WHEN** an admission validation approaches the internal 9-second deadline
- **THEN** the controller SHALL return a structured response (deny, or admit-with-warning for an incomplete test run) within the API server's 10-second timeout rather than letting the request fail at the transport level.

### Requirement: Startup Bind Gate

Iteration startup SHALL block for up to 30 seconds on the webhook component's Listening() signal so the controller's readiness does not flip healthy before the TLS listener has bound — otherwise the API server's first AdmissionReview races the listener and bounces with a connection error. If the listener has not bound within 30 seconds, startup SHALL proceed with a warning and the underlying bind error SHALL surface through the component's error group.

#### Scenario: Readiness waits for the TLS bind

- **WHEN** the webhook component is starting
- **THEN** iteration setup SHALL NOT advance past the webhook stage until the TLS listener is bound, the iteration is cancelled, or 30 seconds elapse.

#### Scenario: Bind timeout does not deadlock startup

- **WHEN** the webhook listener fails to bind within 30 seconds
- **THEN** startup SHALL proceed and the bind failure SHALL be reported through the error group instead of blocking the iteration forever.

### Requirement: Fixed Fail-Open Policy for Config Admission

The chart SHALL set `failurePolicy: Ignore` on the HAProxyTemplateConfig admission webhook and SHALL NOT expose it as a configurable value: a down controller must never block operators from applying HAProxyTemplateConfig updates (the first-install chicken-and-egg, and any recovery scenario where the fix lives in a CRD update). This upstream admission gate — validating the config with line-numbered `haproxy -c` diagnostics at apply time — is what lets the leader-side reconcile pipeline skip `haproxy -c` on every render (~94 ms saved per render). When the webhook is unreachable, the Dataplane API still runs `haproxy -c` server-side before accepting a raw configuration push, so an invalid config produces a delayed but clear failure through the published config status. Watched-resource admission remains per-resource opt-in via `enableValidationWebhook`.

#### Scenario: Controller down does not block config updates

- **WHEN** the controller is unavailable and an operator applies an HAProxyTemplateConfig update
- **THEN** the API server SHALL admit the update under failurePolicy Ignore.

#### Scenario: Server-side check backstops an unreachable webhook

- **WHEN** the webhook is unreachable and an invalid config reaches the render pipeline
- **THEN** the Dataplane API's server-side `haproxy -c` SHALL reject the push and the failure SHALL surface through the published config status.
