# Validating Webhook

## Purpose

Admission webhook that validates watched Kubernetes resources (Ingress, HTTPRoute, …) by performing dry-run rendering and HAProxy syntax validation before admitting changes to the cluster.

HAProxyTemplateConfig admission does not exist (ADR-0016): Kubernetes admits objects one at a time, so a per-object webhook structurally cannot judge a multi-object config change — it would validate the mid-batch state `A(new)+B(old)` and deny coupled same-version edits. Config validation happens where the complete set is visible instead: the pre-upgrade preflight hook (before any object is applied), the apiserver's CEL completeness rule on standalone (non-`spec.partial`) objects, the strict first render of each iteration, and the fail-closed load gate.

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

### Requirement: No Test Execution at Admission

The DryRunValidator SHALL NOT run validation tests: the suite is a property of the whole configuration and runs at the load gate, the live gate, and the pre-upgrade preflight hook, all of which see the complete merged set. (Config admission, which used to run a bounded subset of the suite, no longer exists — ADR-0016.)

#### Scenario: Watched-resource admission runs no validation tests

WHEN a watched-resource admission request is validated
THEN the dry-run render and `haproxy -c` SHALL run, and the configuration's validationTests SHALL NOT.

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

The webhook server SHALL run if and only if a TLS certificate directory is configured (the WEBHOOK_CERT_DIR environment variable or the corresponding flag is non-empty) AND at least one watched resource sets `enableValidationWebhook: true`. The server port SHALL be 9443.

#### Scenario: Empty cert dir disables the webhook

- **WHEN** WEBHOOK_CERT_DIR is empty
- **THEN** no webhook server SHALL be started, regardless of the configuration's webhook rules.

#### Scenario: No watched-resource rules disables the webhook

- **WHEN** WEBHOOK_CERT_DIR is set and no watched resource enables admission validation
- **THEN** no webhook validators SHALL be constructed and webhook setup SHALL be skipped.

### Requirement: Legacy Config Webhook Removal at Upgrade

The `apply-crds` pre-install/pre-upgrade hook SHALL delete every webhook entry whose name begins with `haproxytemplateconfig.` from live ValidatingWebhookConfigurations before any manifest is applied. Manifest apply order is not guaranteed (measured: the config object applied before the webhook configuration), so without this the RUNNING old controller's webhook judges each new per-library config object standalone and denies it as incomplete, failing the upgrade. Only the matching entry SHALL be removed — the watched-resource webhook in the same configuration and third-party configurations SHALL be untouched. The removal SHALL be best-effort: any failure degrades to a retryable denied apply, never an upgrade the operator cannot run.

#### Scenario: Old config webhook removed before shards apply

- **WHEN** apply-crds runs during an upgrade from a release whose ValidatingWebhookConfiguration carries a `haproxytemplateconfig.*` entry
- **THEN** that entry SHALL be deleted from the live configuration before the release's manifests are applied, and the configuration's other entries SHALL be preserved.

### Requirement: Internal Admission Deadlines Under the Chart Timeout

The internal watched-resource admission deadline (schema bootstrap plus render plus `haproxy -c`) SHALL be 9 seconds, under the chart's 10-second `timeoutSeconds`, so the controller returns a structured admission decision before the API server gives up and applies the failurePolicy to a transport failure.

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
