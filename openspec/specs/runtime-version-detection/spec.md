# runtime-version-detection Specification

## Purpose

Define how the controller adapts to whatever API versions the cluster actually serves, at runtime and without redeployment: each watched resource resolves to the first cluster-served candidate from its ordered `apiVersions` list, features that require unserved optional resources are stripped at config load, unserved required resources fail fast, and CRD install/upgrade/removal triggers reinitialization with the new resolution.

## Requirements

### Requirement: Served-Version Resolution

The controller SHALL resolve each watched resource to a concrete API version at iteration start by testing the entry's ordered `apiVersions` candidates against live apiserver discovery and selecting the first candidate the cluster serves. The resolved version — never the config literal — SHALL be used for the informer GVR, the on-demand store GVR, typed-schema fetching, admission-webhook GVK registration, dry-run overlay resource mapping, and validation-test fixture defaulting. Resolution SHALL be resource-agnostic: it consumes only the group/version strings and plural resource name from configuration and SHALL contain no knowledge of specific kinds.

#### Scenario: First served candidate wins

- **WHEN** a watched resource lists `apiVersions: [example.io/v1, example.io/v1beta1]` and the cluster serves only `example.io/v1beta1`
- **THEN** the controller SHALL watch `example.io/v1beta1` and all version-derived consumers (schema fetch, webhook registration, fixture defaulting) SHALL use `example.io/v1beta1`.

#### Scenario: Single-version entries behave as today

- **WHEN** a watched resource specifies only the singular `apiVersion` field
- **THEN** the controller SHALL treat it as a one-element candidate list, preserving existing semantics.

#### Scenario: Offline resolution against a schema directory

- **WHEN** the controller runs offline validation with `--schema-dir` and a watched resource lists multiple candidates
- **THEN** resolution SHALL select the first candidate served according to the CRD manifests in the schema directory, using the same selection logic as live discovery.

### Requirement: Optional Resources and Feature Stripping

A watched resource MAY declare `optional: true`. When no candidate version of an optional resource is served, the controller SHALL drop the watch for that resource and SHALL strip, from the effective configuration at load time, every `templateSnippets` entry and every `validationTests` entry whose `requires` list names that resource. Stripping SHALL happen before template compilation and before validation tests run. The stripping rule SHALL be generic: it matches `requires` names against unavailable resources and SHALL contain no knowledge of specific kinds or libraries.

#### Scenario: Unavailable optional resource strips its feature atomically

- **WHEN** an optional watched resource has no served candidate and snippets and validation tests declare `requires: [<that resource>]`
- **THEN** the controller SHALL start without watching the resource, without compiling those snippets, and without running those validation tests, and SHALL become Ready.

#### Scenario: Available optional resource activates its feature

- **WHEN** an optional watched resource has a served candidate
- **THEN** the controller SHALL watch it at the resolved version and SHALL retain all elements requiring it.

#### Scenario: Elements without requires are never stripped

- **WHEN** a snippet or validation test declares no `requires` list
- **THEN** it SHALL be retained regardless of resource availability.

### Requirement: Fail-Fast on Required Unserved Resource

When a non-optional watched resource has no served candidate version, the controller SHALL fail the startup iteration with an error naming the resource and its candidate versions, instead of blocking indefinitely on informer cache sync. The failure SHALL be surfaced through the health endpoint and logs, and the controller SHALL retry via its existing iteration retry loop.

#### Scenario: Required resource missing fails fast with a named error

- **WHEN** a required watched resource's CRD is absent from the cluster
- **THEN** iteration startup SHALL fail within the resolution step with an error identifying the resource and candidates, and `/healthz` SHALL report the cause instead of a silent 503.

#### Scenario: Convergence once the CRD appears

- **WHEN** the missing CRD is subsequently installed
- **THEN** a following iteration SHALL resolve the resource and the controller SHALL become Ready without operator intervention.

### Requirement: CRD Change Reinitialization

The controller SHALL watch CustomResourceDefinitions (apiextensions.k8s.io), filtered to the API groups referenced by `watchedResources`, and SHALL trigger the existing configuration-reload iteration restart (debounced) when a relevant CRD's served versions change — including installation, in-place upgrade, and serving removal. The rebuilt iteration SHALL re-run resolution and stripping against current discovery. The CRD watch is operational plumbing for the controller's own watch set and SHALL NOT special-case any resource group in code.

#### Scenario: In-place CRD upgrade converges without helm or pod restart

- **WHEN** a watched resource's CRD is upgraded in place such that its previously resolved version is no longer served but a higher-preference candidate now is
- **THEN** the controller SHALL reinitialize and watch the newly resolved version, with no helm operation and no pod restart.

#### Scenario: Late CRD installation activates optional features

- **WHEN** the CRDs backing optional watched resources are installed after the controller started
- **THEN** the controller SHALL reinitialize, watch the new resources, and un-strip the elements requiring them.

#### Scenario: Irrelevant CRD changes are ignored

- **WHEN** a CRD outside every watched resource's API group changes
- **THEN** the controller SHALL NOT reinitialize.
