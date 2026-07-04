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

### Requirement: Field-Level Validation-Test Stripping

A `validationTests` entry MAY declare `requiresFields`: a list of schema field paths, each in the form `<watchedResourceKey>.<field.path>` whose first dot-segment SHALL name a `watchedResources` key (a dangling or malformed entry SHALL be rejected at structural validation). During effective-config resolution, the controller SHALL probe each referenced field against the RESOLVED schema generation of its watched resource — the CRD's `openAPIV3Schema` or the aggregated OpenAPI v3 schema, fetched live at runtime and from `--schema-dir` offline — and SHALL strip every test with at least one absent field from the effective configuration, reported separately from resource-level stripping. This covers clusters that serve a resource at the same version string as newer releases while its schema generation lacks individual fields (Gateway API v1.1 serves `httproutes` at `v1` without the CORS filter), where resource-level `requires` stripping can never fire and the fail-closed load gate would otherwise crash-loop the controller. The probe SHALL descend into array `items` transparently (`spec.rules.filters.cors` matches the field inside `rules[].filters[]`), SHALL treat `x-kubernetes-preserve-unknown-fields` subtrees as containing any field, and SHALL be generic — paths come from configuration and the walk contains no knowledge of specific kinds. A field referencing an unavailable optional resource counts as absent. A schema-fetch error SHALL fail the whole resolution instead of stripping, mirroring the transient-discovery-error rule. The live config-change validation path SHALL apply the same stripping via the shared effective resolver.

#### Scenario: Absent field strips the test instead of failing the load gate

- **WHEN** a validation test declares `requiresFields: [httproutes.spec.rules.filters.cors]` and the cluster serves `httproutes` at a schema generation without `spec.rules.filters.cors`
- **THEN** the test SHALL be stripped from the effective configuration at load time, the controller SHALL become Ready, and the stripped test SHALL be reported in the resolution's field-stripped list (visible at `/debug/vars/effectiveConfigResolution`).

#### Scenario: Present field keeps the test

- **WHEN** every field named in a test's `requiresFields` exists in the resolved schema generation
- **THEN** the test SHALL be retained and SHALL run.

#### Scenario: In-place CRD upgrade adding the fields reloads and un-strips

- **WHEN** a watched resource's CRD is upgraded in place such that its served versions are unchanged but the schema now contains previously-absent `requiresFields` fields
- **THEN** the CRD watch's re-resolution SHALL produce a resolution that differs from the running iteration's, an iteration reload SHALL fire, and the previously-stripped tests SHALL run in the new iteration.

#### Scenario: Schema-fetch error fails resolution instead of stripping

- **WHEN** probing a `requiresFields` entry fails because the resource's schema cannot be fetched
- **THEN** the whole resolution SHALL fail with an error naming the field, and no test SHALL be silently stripped.

#### Scenario: Dangling requiresFields entry rejected at load

- **WHEN** a test's `requiresFields` entry's first dot-segment does not name a `watchedResources` key, or the entry carries no field path
- **THEN** structural validation SHALL reject the configuration with an error naming the test and the entry.

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

### Requirement: Transient Discovery Errors Fail Resolution Instead of Stripping

Only an authoritative NotFound answer from apiserver discovery SHALL count as "unserved". A transient discovery error (apiserver blip, aggregated-API hiccup) SHALL fail the whole resolution with an error instead of being treated as unserved — silently treating it as unserved would strip optional features and bounce the controller through a spurious reinitialization on every blip. At iteration start, a failed resolution retries through the existing iteration retry loop. During CRD-change re-resolution, a resolution error SHALL skip the reload; the debounced watch re-evaluates on subsequent CRD events.

#### Scenario: Discovery blip does not strip optional features

- **WHEN** discovery returns a transient (non-NotFound) error for a group/version during resolution
- **THEN** resolution SHALL fail with an error naming the group/version, no optional resource SHALL be marked unserved, and no feature SHALL be stripped.

#### Scenario: CRD-change re-resolution failure skips the reload

- **WHEN** a relevant CRD change triggers re-resolution and the re-resolution fails
- **THEN** no iteration reload SHALL fire; the watch SHALL re-evaluate on the next CRD event.

#### Scenario: Authoritative NotFound is unserved

- **WHEN** discovery authoritatively reports a candidate group/version as NotFound
- **THEN** that candidate SHALL count as unserved and the existing stripping and fail-fast behavior SHALL apply.

### Requirement: CRD-Watch Filtering, Debounce, and Reload Subsumption

The CRD watch SHALL derive its relevant API groups from the RAW configuration's candidate lists, so the groups of currently-unavailable optional resources are watched too — an unavailable resource's CRD appearing is exactly the event the watch exists for. A CRD update SHALL trigger evaluation only when the CRD's spec changed (`metadata.generation` bumped) — covering served-version changes AND in-place schema-content upgrades, which field-level stripping depends on; status and metadata churn SHALL be ignored. Change bursts SHALL be debounced by 2 seconds (an install applies many CRDs at once). After the debounce window, the watch SHALL re-resolve against fresh discovery and a fresh schema fetcher, and request a reload only when the fresh resolution differs from the running iteration's resolution. A single inconclusive re-resolution SHALL NOT be accepted as final: the apiserver's discovery endpoint propagates a CRD apply asynchronously, and the CRD's later Established flip bumps no generation, so no further informer event arrives. The watch SHALL therefore re-check at a bounded cadence (5 seconds) after any re-resolution that yields no change or errors, until EITHER a re-resolution differs from the running one (reload, as above) OR the answer has been stable-equal for 3 consecutive checks since the last observed CRD event (accept "no change" and go quiet). Errored re-resolutions SHALL never trigger a reload or feature stripping directly, SHALL NOT count toward — and SHALL reset — the stability streak, and SHALL schedule the next recheck; a new CRD event SHALL restart the debounce window and both streaks. A PERSISTENT failure SHALL NOT idle in the recheck loop indefinitely: after 6 consecutive failed re-resolutions the watch SHALL escalate by triggering the reload anyway, so the iteration restart re-resolves on the startup path where a genuinely lost required resource fails fast and surfaces through the health endpoint and the iteration retry loop. Reload requests SHALL be posted non-blockingly onto the capacity-1 config-change channel: a reload already queued subsumes further requests.

#### Scenario: Status-only CRD churn ignored

- **WHEN** a watched-group CRD's status is updated without changing its served versions
- **THEN** no re-resolution SHALL be scheduled.

#### Scenario: Install burst coalesces into one reload

- **WHEN** a Gateway API install applies its full CRD set within the debounce window
- **THEN** the watch SHALL re-resolve once after the window and trigger at most one reload.

#### Scenario: Equal resolution suppresses the reload

- **WHEN** a relevant CRD changes and re-resolution yields a resolution equal to the running one for 3 consecutive bounded rechecks
- **THEN** no reload SHALL fire and the watch SHALL go quiet until the next CRD event.

#### Scenario: Recheck catches discovery-propagation lag

- **WHEN** a relevant CRD is re-applied and the first re-resolution races the apiserver's discovery-propagation lag (still seeing the resource unserved), and no further CRD event arrives
- **THEN** a bounded recheck SHALL observe the propagated resolution difference and trigger the reload without requiring another CRD event; a transient re-resolution error during the cycle SHALL only schedule the next recheck.

#### Scenario: Persistent re-resolution failure escalates

- **WHEN** re-resolution fails on 6 consecutive bounded rechecks after a relevant CRD event (for example a required resource's CRD was genuinely removed)
- **THEN** the watch SHALL trigger a reload so the fault surfaces through the iteration restart's fail-fast path instead of hiding behind recheck warnings.

#### Scenario: Queued reload subsumes later requests

- **WHEN** a reload is already queued on the config-change channel and another CRD change requests one
- **THEN** the later request SHALL be dropped; the queued reload covers it.

### Requirement: Live Config Validation Against the Effective Config

On a live HAProxyTemplateConfig change, the config-change handler SHALL transform the parsed config into the effective config — running the same resolution as iteration start, via the installed effective resolver — BEFORE fanning it out to the scatter-gather validators, so validators judge exactly what a reinitialized iteration would load. A resolution failure (a required resource with no served version, or a transient discovery error) SHALL be published as a ConfigInvalidEvent, and the currently-running configuration SHALL keep serving. The scatter-gather envelope SHALL be 45 seconds. Superseded queued config loads SHALL be coalesced to the latest parsed config before validation.

#### Scenario: Validators see the effective config

- **WHEN** a live config change arrives while an optional watched resource is unserved
- **THEN** the validators SHALL receive the config with the dependent snippets and tests already stripped, matching what a reinitialized iteration would load.

#### Scenario: Resolution failure reported as invalid, current config keeps running

- **WHEN** effective-config resolution fails for a live config change
- **THEN** a ConfigInvalidEvent SHALL be published carrying the resolution error and no iteration restart SHALL be signalled.

#### Scenario: Superseded config loads skipped

- **WHEN** several config edits queue while a validation is pending
- **THEN** the handler SHALL validate only the latest queued config.

### Requirement: Machine-Generated Requires Edges

The gateway library's `requires` annotations SHALL be machine-generated from the merged chart configuration by a regeneration script, never hand-maintained. Per snippet, the requirement set SHALL be the transitive dependency closure over four edge types: direct `resources.<kind>` references restricted to the nine gateway kinds; compile-time `{% import %}` references; `render "<name>"` WITHOUT a default clause — a render carrying `default` is deliberately NOT an edge, because it is the compile-safe seam a surviving snippet uses to reference a strippable one; and fileRegistry producer-consumer edges — a snippet consuming a literal file name that another snippet registers inherits the producer's requirements, because the file reference breaks at `haproxy -c` when the producer strips even though no compile-time edge exists. Validation tests SHALL derive their requires from the gateway kinds among their fixture keys. Regeneration SHALL rewrite only gateway fragment files.

#### Scenario: Render with default is not an edge

- **WHEN** a snippet references a strippable snippet via `render "..." default ""`
- **THEN** the referencing snippet SHALL NOT inherit the strippable snippet's requirements.

#### Scenario: File registration creates an edge

- **WHEN** snippet A registers a map file via fileRegistry and snippet B references that literal file name
- **THEN** snippet B SHALL inherit snippet A's requirements, so both strip together.

### Requirement: Degraded-Profile Offline Verification

The template test harness SHALL verify feature stripping against committed old-release Gateway API CRD bundles. After the main pass, it SHALL render the Standard-channel chart once and, for each bundle, build a merged schema directory (the core, discovery, haptic, and networking schemas plus that bundle's gateway CRDs; the "none" bundle contributes no gateway CRDs — the plain-Ingress cluster shape) and run offline validation against it. Every profile SHALL report ZERO failing tests — with field-level stripping in place, a test that would fail on that schema generation must have been stripped instead (a failure is exactly the load-gate crash-loop the stripping exists to prevent). The set of STRIPPED tests (resource-level and field-level, as listed by the validate CLI's stripped-test output) SHALL exactly equal the bundle's expected-stripped allowlist, with comments and blank lines ignored: a NEWLY-STRIPPED test and a STALE allowlist entry SHALL both fail the run. Each allowlisted test depends on a resource or field absent from that schema generation — feature absence, not a bug; the controller still becomes Ready. The degraded pass SHALL be skipped when a single test or a custom schema directory is requested.

#### Scenario: Any failing test fails the profile

- **WHEN** any test fails offline validation against a bundle
- **THEN** the harness SHALL fail, naming the failing tests — the test needed a `requires`/`requiresFields` annotation instead.

#### Scenario: Stale allowlist entry fails

- **WHEN** a test listed in a bundle's expected-stripped allowlist is not stripped against that bundle
- **THEN** the harness SHALL fail with a diff of expected versus actual stripped sets.

#### Scenario: Newly-stripped test fails

- **WHEN** a test not listed in the allowlist is stripped against a bundle
- **THEN** the harness SHALL fail with a diff of expected versus actual stripped sets.

#### Scenario: No-Gateway-API profile leaves the base suite intact

- **WHEN** the Standard-channel chart is validated against the bundle containing no gateway CRDs
- **THEN** every gateway feature SHALL strip atomically with its tests and the base and ingress suites SHALL pass untouched.

### Requirement: Runtime GatewayClass Creation

The GatewayClass SHALL be created and maintained at runtime through the gateway library's k8sResources (Server-Side Apply by the resource applier) rather than by a Helm template, emitted at the resolved apiVersion from a strippable snippet requiring gatewayclasses behind a render-with-default seam. It therefore exists exactly when the gatewayclasses CRD is served: installing Gateway API after HAPTIC — or upgrading it in place — creates the class with no Helm operation, and on clusters without the CRD the entry renders empty and applies nothing. Consumers that need the class immediately after install (for example the Gateway API conformance suite) SHALL poll for its existence rather than assume install order.

#### Scenario: Late Gateway API install creates the class

- **WHEN** the Gateway API CRDs are installed after the controller started
- **THEN** the reinitialized iteration SHALL render and apply the GatewayClass without any Helm operation or pod restart.

#### Scenario: No CRD means nothing applied

- **WHEN** the gatewayclasses CRD is not served
- **THEN** the GatewayClass k8sResources entry SHALL render empty and no apply SHALL be attempted.

#### Scenario: Conformance suite waits for the class

- **WHEN** the conformance suite starts against a fresh install
- **THEN** it SHALL poll for the controller-created GatewayClass (up to 3 minutes) before invoking the upstream suite, whose setup fails immediately on a missing class.
