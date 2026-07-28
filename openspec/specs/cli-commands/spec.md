# CLI Commands

## Purpose

The `haptic-controller` binary provides the following subcommands: `run` (main controller daemon), `validate` (local template/config validation), `benchmark` (rendering performance analysis), `config` (inspect the live published HAProxy config), and `version` (build info).

## Requirements

### Requirement: Run Command

The `haptic-controller run` command SHALL start the main controller daemon that watches Kubernetes resources, renders HAProxy configurations from templates, and synchronizes them to HAProxy instances. The command SHALL accept the following flags: `--crd-name` (HAProxyTemplateConfig names, repeatable or comma-separated, merged in the order given), `--secret-name` (credentials Secret name), `--webhook-cert-dir` (directory holding the webhook TLS certificate files; empty disables the webhook), `--kubeconfig` (path to kubeconfig for out-of-cluster development), and `--debug-port` (debug HTTP server port, 0 to disable).

#### Scenario: Flag and environment variable defaults

WHEN the `run` command is invoked without flags
THEN `--crd-name` SHALL default to the comma-separated `CRD_NAME` env var or `["haproxy-config"]`, `--secret-name` SHALL default to `SECRET_NAME` env var or `"haproxy-credentials"`, `--webhook-cert-dir` SHALL default to `WEBHOOK_CERT_DIR` env var or `""` (empty, disabling webhooks), and `--debug-port` SHALL default to `DEBUG_PORT` env var or `0`.

#### Scenario: Configuration priority order

WHEN both a CLI flag and an environment variable are set for the same parameter
THEN the CLI flag value SHALL take precedence over the environment variable, and both SHALL take precedence over the default value.

#### Scenario: Graceful shutdown on SIGTERM

WHEN the controller receives a SIGTERM or SIGINT signal
THEN it SHALL initiate graceful shutdown, wait up to 25 seconds for goroutines to finish, and log `"Controller shutdown complete"` on success.

#### Scenario: Eight-stage startup sequence

WHEN the controller starts successfully
THEN it SHALL execute an 8-stage startup: (1) Config management components start, (2) block until a valid configuration is received, (3) resource watchers start and initial indexes sync, (4) config watchers (CRD and credentials SingleWatchers) start, (5) reconciliation and observability components start (EventBus.Start() runs immediately after this stage, once all components have subscribed), (6) leader election initializes, (7) webhook validation server starts (if enabled), (8) debug variables and health checker are wired.

### Requirement: Run Command Logging

The `run` command SHALL configure structured logging with a dynamic level. The log level SHALL be read from the `LOG_LEVEL` environment variable (values: TRACE, DEBUG, INFO, WARN, ERROR, case-insensitive) with a default of INFO. On startup, the controller SHALL log its version, source hash, CRD name, secret name, debug port, log level, GOMAXPROCS, and GOMEMLIMIT.

#### Scenario: Log level from environment variable

WHEN `LOG_LEVEL=DEBUG` is set
THEN the controller SHALL produce debug-level log output.

### Requirement: Validate Command

The `haptic-controller validate` command SHALL load HAProxyTemplateConfigs from YAML files, compile their templates, and execute embedded validation tests. The `-f`/`--file` flag SHALL be required, SHALL be repeatable, and each file MAY contain several YAML documents; every HAProxyTemplateConfig across all files SHALL be merged in order using the same merge the controller performs. `--dump-merged` SHALL print the merged spec and exit without running tests. Optional flags SHALL include: `--test` (run a specific test by name), `--verbose` (show content preview for failed assertions, first 200 characters), `--dump-rendered` (dump all rendered content: haproxy.cfg, maps, files, certs), `--trace-templates` (show template execution trace, top-level only), `--debug-filters` (show filter operation debugging), `--profile-includes` (show include timing statistics, top 20 slowest), `--workers` (parallel test workers, 0 = auto-detect CPUs, 1 = sequential), and `-o`/`--output` (output format: `summary`, `json`, or `yaml`). The `haproxy` binary is discovered from `PATH` (no `--haproxy-binary` flag); version selection is done by running the matching per-version controller image.

#### Scenario: Required file flag

WHEN the `validate` command is invoked without the `-f` flag
THEN the command SHALL exit with an error indicating that the `--file` flag is required.

#### Scenario: All tests pass with exit code 0

WHEN all validation tests in the config file pass
THEN the command SHALL exit with code 0.

#### Scenario: Any test fails with non-zero exit code

WHEN at least one validation test fails
THEN the command SHALL exit with a non-zero exit code and report the number of passed vs total tests.

#### Scenario: Specific test execution

WHEN `--test "test-frontend-routing"` is specified
THEN only the test named `test-frontend-routing` SHALL be executed. If the named test does not exist, the command SHALL exit with an error.

#### Scenario: No validation tests in config

WHEN the config file contains no `validationTests`
THEN the command SHALL exit with the error `"no validation tests found in config"`.

#### Scenario: Dump rendered content

WHEN `--dump-rendered` is specified
THEN the command SHALL print all rendered content after test results: the main haproxy.cfg, all map files with their names, all general files, and all SSL certificates.

#### Scenario: Template execution trace

WHEN `--trace-templates` is specified
THEN the command SHALL print a template execution trace after test results showing which templates were rendered and their timing.

#### Scenario: Include profiling output

WHEN `--profile-includes` is specified
THEN the command SHALL print a table of the top 20 slowest included templates with columns for name, count, total time, average time, and max time.

### Requirement: Validate Command Config Loading

The validate command SHALL accept both full Kubernetes resource YAML (with `apiVersion`, `kind`, `metadata`) and spec-only YAML. Spec-only YAML SHALL be accepted only for a single file holding a single document, since merge order is meaningful only between identifiable configs. Full resource YAML SHALL be decoded using the Kubernetes codec factory. Spec-only YAML SHALL be decoded as a raw `HAProxyTemplateConfigSpec`. File paths SHALL be cleaned with `filepath.Clean` before reading.

#### Scenario: Full Kubernetes resource YAML loaded

WHEN the input file contains `apiVersion: haproxy-haptic.org/v1alpha1` and `kind: HAProxyTemplateConfig`
THEN the command SHALL parse it as a typed Kubernetes resource and extract the `.spec`.

#### Scenario: Spec-only YAML loaded as fallback

WHEN the input file does not parse as a Kubernetes resource
THEN the command SHALL attempt to parse it as a raw `HAProxyTemplateConfigSpec`.

### Requirement: Benchmark Command

The `haptic-controller benchmark` command SHALL measure template rendering performance. The `-f`/`--file` flag SHALL be required. Optional flags SHALL include: `--test` (repeatable, select specific tests to benchmark; omit to run all tests excluding `_global`), `--iterations` (number of render iterations, default 2), and `--profile-includes` (show include timing statistics). The command SHALL compile templates once (timed separately), perform a warm-up render, then time each iteration individually.

#### Scenario: Default iterations count

WHEN `--iterations` is not specified
THEN the benchmark SHALL run 2 iterations per test.

#### Scenario: All tests benchmarked when no test flag

WHEN `--test` is not specified
THEN all validation tests (excluding `_global`) SHALL be benchmarked in deterministic alphabetical order.

#### Scenario: Specific tests selected

WHEN `--test benchmark-ingress-100 --test benchmark-httproute-100` is specified
THEN only those two tests SHALL be benchmarked.

#### Scenario: Non-existent test rejected

WHEN `--test "nonexistent-test"` is specified and the test does not exist
THEN the command SHALL exit with an error `test "nonexistent-test" not found in config`.

#### Scenario: Compilation time reported separately

WHEN the benchmark completes
THEN the output SHALL report compilation time separately from per-iteration render times.

#### Scenario: Table output with per-file timing

WHEN the benchmark completes
THEN the output SHALL display a table with rows for each rendered file (haproxy.cfg, maps, general files, certificates) and columns for each iteration showing render time in milliseconds, plus a TOTAL row.
