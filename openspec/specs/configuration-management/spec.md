# Configuration Management

## Purpose

HAProxyTemplateConfig CRD schema, credentials management, environment variables, CLI flags, and Helm chart values for controller deployment configuration.

## Requirements

### Requirement: HAProxyTemplateConfig CRD Schema

The controller SHALL be configured via HAProxyTemplateConfig Custom Resources with typed fields, serving as the primary configuration mechanism defining templates, watched resources, and auxiliary configuration.

The controller SHALL accept an ORDERED LIST of such resources and merge them, later wins, before any validation or rendering. The MERGED result — not any single object — SHALL be the unit of completeness: fields the controller requires are optional per object and enforced after the merge. Merge order SHALL come from the configured name list, never from object names, resourceVersions, or creation timestamps. A list of one SHALL behave identically to a single resource. See ADR-0014.

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

The controller SHALL support configuration via environment variables: CRD_NAME (comma-separated names of the HAProxyTemplateConfig resources, in merge order), SECRET_NAME (name of the credentials Secret), WEBHOOK_CERT_DIR (directory holding the validating webhook's TLS certificate files; empty disables the webhook), and LOG_LEVEL (logging verbosity).

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

### Requirement: Structural Validation Rules

Structural validation, running after defaults are applied, SHALL enforce: the pod selector's match_labels map is non-empty with non-empty keys and non-empty values; at least one watched resource is configured, and each watched resource has a non-empty resources name and at least one non-empty index_by expression (the apiVersion/apiVersions exclusivity rules are specified under WatchedResources Configuration); the dataplane port is between 1 and 65535 and the maps directory, SSL certificates directory, general storage directory, and config file path are all non-empty (a zero port or empty path after defaults indicates defaults were not applied); and the main HAProxy configuration template is non-empty. The log level SHALL be either empty (defer to the LOG_LEVEL environment variable or default) or one of TRACE, DEBUG, INFO, WARN, WARNING, or ERROR, matched case-insensitively, with WARNING accepted as an alias for WARN. Credentials validation SHALL require a non-empty dataplane username and a non-empty dataplane password.

#### Scenario: Empty pod selector rejected

- **WHEN** the pod selector's match_labels map is empty
- **THEN** structural validation SHALL reject the config.

#### Scenario: Out-of-range dataplane port rejected

- **WHEN** the dataplane port is 0 or greater than 65535
- **THEN** structural validation SHALL reject the config with an error naming the port field.

#### Scenario: Empty storage directory rejected

- **WHEN** the maps directory is empty after defaults were applied
- **THEN** structural validation SHALL reject the config.

#### Scenario: Empty main template rejected

- **WHEN** the HAProxy configuration template is empty
- **THEN** structural validation SHALL reject the config.

#### Scenario: Log level alias accepted case-insensitively

- **WHEN** the log level is set to "warning" in any letter case
- **THEN** structural validation SHALL accept it as an alias for WARN, while an unknown token SHALL be rejected.

#### Scenario: Empty credentials rejected

- **WHEN** the credentials Secret yields an empty dataplane password
- **THEN** credentials validation SHALL fail with an error naming the missing field.

### Requirement: Chart RBAC Breadth

The Helm chart's ClusterRole SHALL derive its rules from the merged watched-resource set (template libraries plus user overrides). For each watched resource, it SHALL grant get, list, and watch on the resource with apiGroups equal to the deduplicated union of the API groups across ALL candidate apiVersions of that resource, so a multi-version candidate list is watchable regardless of which version the cluster serves. Each watched resource declaring statusPatch SHALL additionally receive a patch grant on the resource's status subresource with the same group union. The role SHALL grant get, list, and watch on apiextensions.k8s.io customresourcedefinitions: read access powers schema resolution for typed watched resources, and the watch verb powers the runtime CRD watch that re-resolves the effective config when a watched resource's CRD is installed, upgraded, or removed.

When the gateway template library is enabled, the role SHALL additionally grant cluster-wide get, list, watch, create, update, patch, and delete on core Services — Gateway API templates emit per-Gateway marker Services into the Gateway's namespace, outside the controller's own namespace-scoped Role — and create, update, patch, and delete on gatewayclasses, because the GatewayClass is created and maintained at runtime by the resource applier via Server-Side Apply rather than by Helm (read verbs come from the watched-resources rules).

#### Scenario: Group union across candidate versions

- **WHEN** a watched resource declares candidate apiVersions spanning two API groups
- **THEN** the generated watch rule SHALL list both groups (deduplicated) for that resource.

#### Scenario: statusPatch adds a status patch rule

- **WHEN** a watched resource declares statusPatch true
- **THEN** the ClusterRole SHALL contain a patch rule on that resource's /status subresource.

#### Scenario: CRD read and watch always granted

- **WHEN** the chart renders with rbac.create enabled
- **THEN** the ClusterRole SHALL grant get, list, and watch on customresourcedefinitions regardless of which template libraries are enabled.

#### Scenario: Gateway library widens Service and GatewayClass grants

- **WHEN** the gateway template library is enabled
- **THEN** the ClusterRole SHALL grant cluster-wide Service write verbs and gatewayclasses create/update/patch/delete; when it is disabled, neither grant SHALL be present.
