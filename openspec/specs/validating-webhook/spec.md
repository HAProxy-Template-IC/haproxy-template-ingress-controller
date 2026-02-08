# Validating Webhook

Admission webhook that validates Kubernetes resources (HAProxyTemplateConfig and watched resources) by performing dry-run rendering and HAProxy syntax validation before admitting changes to the cluster.

## ADDED Requirements

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

The DryRunValidator SHALL perform validation in phases: (1) Template rendering using an overlay store that simulates the proposed resource change, (2) HAProxy syntax validation of the rendered configuration, and (3) Embedded test execution if validation tests are configured. A failure in any phase SHALL reject the admission request with a user-readable reason. The render-validate pipeline (phases 1 and 2) SHALL be executed via ProposalValidator with a 30-second timeout.

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

When the controller configuration includes embedded validation tests, the DryRunValidator SHALL execute them after successful rendering and syntax validation. Test execution SHALL use `Workers: 1` (sequential) in the webhook context. The test execution timeout SHALL be 60 seconds. Failed tests SHALL produce an error message listing the number of failed tests, their names, rendering errors, and failed assertion descriptions.

#### Scenario: Sequential test execution in webhook

WHEN the DryRunValidator runs validation tests for an admission request
THEN tests SHALL execute with a single worker (sequential) to minimize resource usage in the webhook context.

#### Scenario: Test execution timeout

WHEN validation test execution exceeds 60 seconds
THEN the test run SHALL be cancelled and the admission request SHALL be rejected with a timeout error.

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

The webhook component SHALL enforce a 10-second read/write timeout on the HTTPS server. The dry-run validation call to ValidateDirect SHALL use a 5-second context timeout. The ProposalValidator pipeline SHALL use a 30-second timeout for the render-validate pipeline.

#### Scenario: Server read/write timeout

WHEN an admission request takes longer than 10 seconds to read or the response takes longer than 10 seconds to write
THEN the server SHALL terminate the connection.

#### Scenario: Validation timeout

WHEN dry-run validation exceeds 5 seconds
THEN the context SHALL be cancelled and the validation SHALL fail.
