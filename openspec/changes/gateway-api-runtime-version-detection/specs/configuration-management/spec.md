# Configuration Management — Delta

## MODIFIED Requirements

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

## ADDED Requirements

### Requirement: Requires Declarations on Config Elements

TemplateSnippets entries and ValidationTests entries MAY declare `requires`, a list of watched-resource names the element depends on. Configuration validation SHALL reject a `requires` entry that names a watched resource not present in WatchedResources. Elements without `requires` SHALL be unaffected by resource availability.

#### Scenario: Valid requires accepted

- **WHEN** a snippet declares `requires: [tcproutes]` and `tcproutes` is a WatchedResources key
- **THEN** configuration validation SHALL accept the config.

#### Scenario: Dangling requires rejected

- **WHEN** a snippet declares `requires: [nonexistent]` and no such WatchedResources key exists
- **THEN** configuration validation SHALL reject the config with an error naming the snippet and the unknown resource.
