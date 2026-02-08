# Validation and Testing

HAProxy configuration syntax validation using the HAProxy binary, and an embedded validation test framework in HAProxyTemplateConfig CRDs with fixture-based resource simulation, multiple assertion types, and parallel test execution.

## ADDED Requirements

### Requirement: HAProxy Syntax Validation

The system SHALL validate rendered HAProxy configuration by invoking the HAProxy binary with the `-c` flag. Validation SHALL write the configuration and all auxiliary files (maps, general files, SSL certificates, CRT lists) to a temporary directory before invoking the binary. A validation cache SHALL treat `ErrValidationCacheHit` as a pass (configuration already validated successfully). On failure, validation errors SHALL be simplified using `dataplane.SimplifyValidationError()` to produce user-readable messages.

#### Scenario: Valid configuration passes syntax check

WHEN a rendered HAProxy configuration is syntactically valid
THEN the `haproxy_valid` assertion SHALL pass and no error message SHALL be produced.

#### Scenario: Invalid configuration fails with simplified error

WHEN a rendered HAProxy configuration contains a syntax error (e.g., `maxconn` set to a non-integer)
THEN the `haproxy_valid` assertion SHALL fail and the error message SHALL contain the simplified error (e.g., `maxconn: integer expected`) rather than raw HAProxy output with timestamps and process IDs.

#### Scenario: Validation cache hit treated as pass

WHEN the configuration has already been validated successfully and `ValidateConfiguration` returns `ErrValidationCacheHit`
THEN the `haproxy_valid` assertion SHALL pass.

### Requirement: Test Fixtures

Test fixtures SHALL simulate Kubernetes resources for template rendering without requiring a live cluster. Fixtures SHALL be defined per-test inside the HAProxyTemplateConfig CRD under `validationTests.<testName>.fixtures`. The fixture map SHALL be keyed by resource type name (e.g., `services`, `ingresses`, `endpoints`, `secrets`, `configmaps`, `haproxy-pods`). Each resource SHALL be an unstructured Kubernetes resource object. Custom resource types defined in `watchedResources` SHALL also be supported as fixture keys.

#### Scenario: Fixture resources populate stores for rendering

WHEN a validation test defines fixtures with 3 services and 2 ingresses
THEN the test runner SHALL create stores containing those resources, indexed using the same `IndexBy` configuration as production watchers, and templates SHALL be able to iterate over them.

#### Scenario: TypeMeta inferred for fixtures missing apiVersion/kind

WHEN a fixture resource omits `apiVersion` or `kind` fields
THEN the test runner SHALL infer them from the corresponding `watchedResources` entry (apiVersion from the watcher config, kind singularized from the plural resource type name).

#### Scenario: Empty stores created for unwatched resource types

WHEN a validation test does not provide fixtures for a watched resource type
THEN the test runner SHALL create an empty store for that resource type so templates can safely call `.List()` without errors.

#### Scenario: Fixture resource type not in watchedResources is rejected

WHEN a fixture references a resource type that does not exist in `watchedResources` and is not `haproxy-pods`
THEN the test runner SHALL return an error identifying the unknown resource type.

### Requirement: Global Fixtures

A special test named `_global` SHALL provide fixtures shared across all validation tests. Global fixtures SHALL be merged with test-specific fixtures before execution. Test-specific fixtures SHALL override global fixtures when they share the same resource identity (apiVersion + kind + namespace + name). The `_global` test SHALL NOT be executed as a standalone test.

#### Scenario: Global fixtures merged with test fixtures

WHEN `_global` defines a Service "default/backend" and a test also defines a Service "default/backend"
THEN the test-specific Service SHALL replace the global one, and other global fixtures not overridden SHALL be included.

#### Scenario: Global test excluded from execution

WHEN `benchmarkTestNames` is empty and all tests run
THEN the `_global` test SHALL be excluded from the execution set.

### Requirement: HTTP Fixtures

HTTP fixtures SHALL simulate external HTTP resource content for templates that use `http.Fetch()`. Fixtures SHALL be defined under `validationTests.<testName>.httpFixtures` as a list of URL-to-content mappings. The fixture HTTP store SHALL return fixture content for known URLs and fail with a descriptive error for unknown URLs (no real network requests). Global HTTP fixtures SHALL be merged with test-specific fixtures, with test-specific entries overriding global entries for the same URL.

#### Scenario: HTTP fixture returns content for known URL

WHEN a template calls `http.Fetch("https://example.com/config")` and the test defines an HTTP fixture for that URL
THEN the fixture store SHALL return the configured content.

#### Scenario: HTTP fixture fails for unknown URL

WHEN a template calls `http.Fetch("https://unknown.example.com/data")` and no fixture exists for that URL
THEN the fixture store SHALL return a descriptive error indicating the URL is not defined in fixtures.

### Requirement: Assertion Types

The test runner SHALL support the following assertion types: `haproxy_valid`, `contains`, `not_contains`, `match_count`, `equals`, `jsonpath`, `match_order`, and `deterministic`. Unknown assertion types SHALL cause the assertion to fail with an error identifying the unknown type.

#### Scenario: contains assertion matches regex pattern

WHEN a `contains` assertion specifies pattern `"backend api-.*"` and target `"haproxy.cfg"`
THEN the assertion SHALL pass if the rendered haproxy.cfg contains text matching the regex, and fail otherwise with an error including target size and a hint to use `--verbose`.

#### Scenario: not_contains assertion rejects matching pattern

WHEN a `not_contains` assertion specifies a pattern that exists in the target content
THEN the assertion SHALL fail with an error stating the pattern was unexpectedly found.

#### Scenario: match_count assertion verifies exact count

WHEN a `match_count` assertion specifies pattern `"server "` with expected count `"3"` and the target contains 2 matches
THEN the assertion SHALL fail reporting `expected 3 matches, got 2 matches`.

#### Scenario: equals assertion performs exact comparison

WHEN an `equals` assertion specifies an expected value that differs from the target content
THEN the assertion SHALL fail with a message showing both expected and actual values, truncated to 100 characters for long values.

#### Scenario: jsonpath assertion queries template context

WHEN a `jsonpath` assertion specifies `{.resources.services}` with an expected value
THEN the assertion SHALL evaluate the JSONPath expression against the template rendering context (not the rendered output) and compare the result to the expected value.

#### Scenario: match_order assertion verifies pattern ordering

WHEN a `match_order` assertion specifies patterns `["frontend https", "backend api", "backend web"]` and the target contains them in a different order
THEN the assertion SHALL fail identifying which patterns are out of order with their byte positions.

#### Scenario: deterministic assertion detects non-deterministic output

WHEN a `deterministic` assertion is present and the template is rendered a second time with identical inputs
THEN the assertion SHALL compare main config and all auxiliary files (maps, general files, SSL certificates, CRT lists) between both renders and fail with a unified diff if any differ.

### Requirement: Assertion Target Resolution

Assertions that accept a `target` field SHALL resolve the target to content using the following mapping: `haproxy.cfg` resolves to the rendered main configuration, `map:<name>` resolves to the named map file, `file:<name>` resolves to the named general file, `cert:<name>` resolves to the named SSL certificate, and `rendering_error` resolves to the template rendering error message. Tests with `rendering_error` target assertions SHALL be treated as negative tests that expect rendering to fail.

#### Scenario: Map file target resolved

WHEN an assertion specifies target `"map:path-prefix.map"`
THEN the assertion SHALL evaluate against the content of the rendered map file named `path-prefix.map`.

#### Scenario: Rendering error target for negative test

WHEN a test's template rendering fails and the test has assertions targeting `rendering_error`
THEN those assertions SHALL be evaluated against the rendering error message, and the test SHALL NOT automatically fail due to the rendering error.

### Requirement: Parallel Test Execution

The test runner SHALL support parallel test execution using a configurable number of workers. When `--workers` is 0, the worker count SHALL default to the number of CPUs (`runtime.NumCPU()`). When `--workers` is 1, execution SHALL be sequential. Each worker SHALL receive its own isolated temporary directory for HAProxy validation paths to prevent file conflicts. The pre-compiled template engine SHALL be shared across workers (thread-safe for concurrent renders).

#### Scenario: Auto-detect worker count

WHEN `--workers 0` is specified on a machine with 4 CPUs
THEN the test runner SHALL use 4 parallel workers.

#### Scenario: Sequential execution for webhook context

WHEN the test runner is created with `Workers: 1` (as in the webhook DryRunValidator)
THEN tests SHALL execute sequentially on a single worker.

#### Scenario: Worker count capped by test count

WHEN 3 tests are to be run and 8 workers are configured
THEN the test runner SHALL use only 3 workers (one per test).

### Requirement: Test Result Reporting

The test runner SHALL produce structured test results containing: total tests, passed count, failed count, per-test results with duration and assertion details, and overall duration. Results SHALL be formattable in three output modes: `summary` (human-readable with pass/fail symbols), `json`, and `yaml`.

#### Scenario: Summary output shows pass/fail indicators

WHEN results are formatted in `summary` mode
THEN each assertion SHALL be prefixed with a pass or fail symbol, and the overall result SHALL show counts of passed and failed tests.

#### Scenario: Exit code reflects test outcome

WHEN all tests pass, the process SHALL exit with code 0. When any test fails, the process SHALL exit with a non-zero code.

### Requirement: Verbose Mode

When verbose mode is enabled, failed assertions SHALL include a content preview of the assertion target. The preview SHALL show the target name, its size in bytes, and the first 200 characters of content. For targets exceeding 200 characters, a hint SHALL suggest using `--dump-rendered` for full content.

#### Scenario: Verbose preview for failed assertion

WHEN `--verbose` is enabled and a `contains` assertion fails on `map:path-prefix.map` (61 bytes)
THEN the output SHALL include the target name, size, and a content preview of up to 200 characters.

### Requirement: Error Simplification

Template rendering errors SHALL be simplified by extracting `fail()` function messages and removing stack traces. HAProxy validation errors SHALL be simplified by extracting the meaningful error description and removing timestamps, process IDs, and file paths. Simplified errors SHALL be used in both CLI output and webhook responses.

#### Scenario: Template fail() message extracted

WHEN a template calls `fail("Service 'api' not found")` and rendering fails
THEN the simplified error SHALL be `Service 'api' not found` rather than the full stack trace.

#### Scenario: HAProxy validation error simplified

WHEN HAProxy reports `[ALERT] parsing [/tmp/haproxy.cfg:15] : 'maxconn' : integer expected`
THEN the simplified error SHALL contain `maxconn: integer expected` without the timestamp, process ID, or temp file path.
