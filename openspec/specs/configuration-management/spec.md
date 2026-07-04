# Configuration Management

## Purpose

HAProxyTemplateConfig CRD schema, credentials management, environment variables, CLI flags, and Helm chart values for controller deployment configuration.

## Requirements

### Requirement: HAProxyTemplateConfig CRD Schema

The controller SHALL be configured via an HAProxyTemplateConfig Custom Resource Definition with typed fields. The CRD SHALL serve as the primary configuration mechanism defining templates, watched resources, and auxiliary configuration.

#### Scenario: Controller reads configuration from CRD

WHEN the controller starts and an HAProxyTemplateConfig resource exists in the configured namespace
THEN the controller SHALL parse the CRD and configure itself accordingly.

### Requirement: Credentials from Kubernetes Secret

Dataplane API credentials (username and password) SHALL be read from a Kubernetes Secret referenced by the SECRET_NAME environment variable. The controller SHALL NOT accept credentials via CRD fields, CLI flags, or environment variables directly.

#### Scenario: Credentials loaded from Secret

WHEN SECRET_NAME is set to "haptic-credentials" and the Secret exists with username and password keys
THEN the controller SHALL use those credentials to authenticate with the Dataplane API.

#### Scenario: Missing Secret prevents startup

WHEN the referenced Secret does not exist
THEN the controller SHALL fail to start with an error indicating the missing Secret.

### Requirement: Environment Variables

The controller SHALL support configuration via environment variables: CRD_NAME (name of the HAProxyTemplateConfig resource), SECRET_NAME (name of the credentials Secret), WEBHOOK_CERT_DIR (directory holding the validating webhook's TLS certificate files; empty disables the webhook), and LOG_LEVEL (logging verbosity).

#### Scenario: CRD_NAME selects the configuration resource

WHEN CRD_NAME is set to "my-config"
THEN the controller SHALL watch and use the HAProxyTemplateConfig resource named "my-config".

#### Scenario: LOG_LEVEL controls verbosity

WHEN LOG_LEVEL is set to "DEBUG"
THEN the controller SHALL emit log messages at DEBUG level and above.

### Requirement: CLI Flags Override Environment Variables

CLI flags SHALL take precedence over environment variables, which SHALL take precedence over default values. This three-tier precedence chain SHALL apply to all configurable parameters.

#### Scenario: CLI flag overrides environment variable

WHEN LOG_LEVEL environment variable is set to "INFO"
THEN the effective log level SHALL be INFO (there is no `--log-level` CLI flag; log level is controlled exclusively via the LOG_LEVEL environment variable).

#### Scenario: Environment variable overrides default

WHEN LOG_LEVEL environment variable is set to "WARN" and no CLI flag is provided
THEN the effective log level SHALL be WARN.

### Requirement: WatchedResources Configuration

The CRD SHALL define WatchedResources as a map keyed by a name (e.g., "ingresses", "services"), where each entry specifies: apiVersion (e.g., "networking.k8s.io/v1") or apiVersions (an ordered list of candidate API versions, resolved at runtime to the first version the cluster serves; the singular apiVersion is equivalent to a one-element list), resources (e.g., "ingresses" — a singular string, the plural resource name), indexBy (JSONPath expressions for store keys), store ("full" or "on-demand"), debounceInterval (per-watcher debounce window override), and optional (boolean; when true, an entry with no served candidate version is dropped instead of failing startup). Specifying both apiVersion and apiVersions SHALL be a validation error, as SHALL an empty apiVersions list.

#### Scenario: WatchedResources with memory store

- **WHEN** a WatchedResources entry specifies store "full"
- **THEN** the controller SHALL store those resources in a full in-memory store.

#### Scenario: WatchedResources with cached store

- **WHEN** a WatchedResources entry specifies store "on-demand"
- **THEN** the controller SHALL store those resources in a cached store that fetches resources on demand with caching (slower, lower memory usage).

#### Scenario: Default store is full

- **WHEN** a WatchedResources entry does not specify store
- **THEN** the controller SHALL default to "full".

#### Scenario: Ordered candidate list

- **WHEN** a WatchedResources entry specifies apiVersions with multiple candidates
- **THEN** configuration validation SHALL accept the entry and the controller SHALL resolve it per the runtime-version-detection capability.

#### Scenario: Conflicting version fields rejected

- **WHEN** a WatchedResources entry specifies both apiVersion and apiVersions
- **THEN** configuration validation SHALL reject the config with an error naming the entry.

### Requirement: HAProxyConfig Main Template

The CRD SHALL include an HAProxyConfig field specifying the main HAProxy configuration template. This template SHALL be rendered to produce the primary `haproxy.cfg` file.

#### Scenario: Main template rendered to haproxy.cfg

WHEN the HAProxyConfig field contains a valid template
THEN the controller SHALL render it to produce the HAProxy configuration file.

### Requirement: Auxiliary Configuration Fields

The CRD SHALL support auxiliary configuration fields: TemplateSnippets (reusable template fragments), Maps (HAProxy map files), Files (general files deployed alongside the config), and SSLCertificates (TLS certificate specifications).

#### Scenario: TemplateSnippets available during rendering

WHEN TemplateSnippets are defined in the CRD
THEN those snippets SHALL be available for inclusion by the main template and other templates.

#### Scenario: Maps rendered as HAProxy map files

WHEN Maps entries are defined in the CRD
THEN the controller SHALL render and deploy them as HAProxy map files.

#### Scenario: SSLCertificates deployed to HAProxy

WHEN SSLCertificates are defined referencing Kubernetes Secrets
THEN the controller SHALL extract certificate data and deploy it to the HAProxy pod.

### Requirement: ValidationTests

The CRD SHALL support a ValidationTests field containing embedded test definitions. These tests SHALL validate rendered configuration using assertion types such as haproxy_valid, contains, not_contains, equals, and jsonpath.

#### Scenario: Embedded tests validate rendered config

WHEN ValidationTests are defined in the CRD and the controller renders a configuration
THEN the validation tests SHALL be executed against the rendered output.

### Requirement: Helm Chart Deployment Configuration

The Helm chart values.yaml SHALL provide deployment configuration including replica count, resource limits, image references, service account settings, and controller-specific settings. Template libraries SHALL be enabled or disabled via the `controller.templateLibraries` values path.

#### Scenario: Template libraries enabled via Helm values

WHEN controller.templateLibraries.ingress is set to true in values.yaml
THEN the ingress template library SHALL be loaded and available during rendering.

#### Scenario: Template libraries disabled via Helm values

WHEN controller.templateLibraries.ingress is set to false in values.yaml
THEN the ingress template library SHALL NOT be loaded.

### Requirement: Requires Declarations on Config Elements

TemplateSnippets entries and ValidationTests entries MAY declare `requires`, a list of watched-resource names the element depends on. Configuration validation SHALL reject a `requires` entry that names a watched resource not present in WatchedResources. Elements without `requires` SHALL be unaffected by resource availability.

#### Scenario: Valid requires accepted

- **WHEN** a snippet declares `requires: [tcproutes]` and `tcproutes` is a WatchedResources key
- **THEN** configuration validation SHALL accept the config.

#### Scenario: Dangling requires rejected

- **WHEN** a snippet declares `requires: [nonexistent]` and no such WatchedResources key exists
- **THEN** configuration validation SHALL reject the config with an error naming the snippet and the unknown resource.
