# HAProxy Config Generation

## Purpose

Transforms resource stores into rendered HAProxy configuration and auxiliary files using a template engine with parallel rendering, path resolution, and dynamic file registration.

## Requirements

### Requirement: RenderService Transforms Stores to HAProxy Configuration

The RenderService SHALL accept a StoreProvider and produce a RenderResult containing the rendered HAProxy configuration string, auxiliary files (maps, general files, SSL certificates, CRT-list files), the render duration in milliseconds, and a total auxiliary file count. Resources in stores SHALL be pre-converted at storage time (floats to ints) and the RenderService SHALL NOT perform additional conversion during rendering.

#### Scenario: Successful render produces complete RenderResult

WHEN the RenderService renders with a StoreProvider containing valid stores
THEN the RenderResult SHALL contain a non-empty HAProxyConfig string, an AuxiliaryFiles structure, a positive DurationMs value, and an AuxFileCount equal to the sum of map files, general files, SSL certificates, and CRT-list files.

#### Scenario: Render failure propagates error

WHEN the template engine fails to render the main template
THEN the RenderService SHALL return an error wrapping the template engine error with the template name.

### Requirement: Template Context Construction

The RenderService SHALL build a template rendering context containing the following keys: `resources` (a dynamically-constructed typed struct value with one field per watched resource, accessed via field access `resources.<name>` rather than map indexing, per ADR-0010), `controller` (map containing `haproxy_pods` StoreWrapper when available), `pathResolver` (PathResolver instance), `templateSnippets` (sorted list of snippet names), `fileRegistry` (FileRegistry for dynamic file registration), `capabilities` (pre-computed map of HAProxy version capabilities), `dataplane` (dataplane configuration), `shared` (cross-template shared context), `runtimeEnvironment` (containing GOMAXPROCS), `http` (HTTP fetcher when available), and `extraContext` (user-defined variables from config). The `extraContext` key SHALL be present when the config contains extra context entries. In the production renderer (`pkg/controller/renderer/service.go`), `extraContext` is only populated when `ExtraContext` is non-nil; when not configured, the key is absent. In the test runner and benchmark paths (via `rendercontext.MergeExtraContextInto`), an empty map is always injected so templates can safely chain `extraContext | dig("k") | fallback("v")`.

#### Scenario: Resources struct exposes a field per watched resource

WHEN the StoreProvider exposes stores named "ingresses", "services", and "secrets"
THEN the template context `resources` value SHALL be a typed struct with one field per store name, so templates reach each store via field access (`resources.ingresses`, `resources.services`, `resources.secrets`) rather than map indexing, per ADR-0010.

#### Scenario: Capabilities map pre-computed at construction time

WHEN a RenderService is constructed with Capabilities
THEN the capabilities map SHALL be computed once during construction and reused for every subsequent render call without recomputation.

#### Scenario: ExtraContext present when configured

WHEN the RenderService builds a template context with extraContext configured
THEN the template context SHALL contain an `extraContext` key with the configured map value.

#### Scenario: RuntimeEnvironment exposes GOMAXPROCS

WHEN the RenderService builds a template context
THEN `runtimeEnvironment.GOMAXPROCS` SHALL equal the value returned by `runtime.GOMAXPROCS(0)`.

### Requirement: Path Resolution

The PathResolver SHALL resolve file paths for HAProxy auxiliary files based on file type. It SHALL support four file types: "map", "cert", "file", and "crt-list". The RenderService SHALL derive directory names using `filepath.Base()` from the absolute paths in the dataplane configuration and SHALL derive the BaseDir using `filepath.Dir()` from the MapsDir configuration path. CRT-list files SHALL always use the general file storage directory regardless of HAProxy version.

#### Scenario: Map file path resolution

WHEN GetPath is called with filename "host.map" and type "map"
THEN the returned path SHALL be the MapsDir joined with "host.map".

#### Scenario: SSL certificate path resolution

WHEN GetPath is called with filename "cert.pem" and type "cert"
THEN the returned path SHALL be the SSLDir joined with "cert.pem".

#### Scenario: General file path resolution

WHEN GetPath is called with filename "500.http" and type "file"
THEN the returned path SHALL be the GeneralDir joined with "500.http".

#### Scenario: CRT-list uses general file directory

WHEN the RenderService is constructed with any dataplane configuration
THEN the PathResolver CRTListDir SHALL equal the GeneralDir value.

#### Scenario: Empty filename returns directory path

WHEN GetPath is called with an empty filename and type "cert"
THEN the returned path SHALL be the SSLDir without a trailing separator.

#### Scenario: Invalid file type rejected

WHEN GetPath is called with an unrecognized file type
THEN GetPath SHALL return an error.

### Requirement: Dynamic File Registration via FileRegistry

The FileRegistry SHALL allow templates to register auxiliary files at render time using the Register method with three arguments: file type ("cert", "map", "file", or "crt-list"), filename, and content. Register SHALL return the predicted path where the file will be located. Registration SHALL be thread-safe. Registering the same filename with identical content SHALL be idempotent and return the same path. Registering the same filename with different content SHALL return an error.

#### Scenario: Register a certificate file

WHEN a template calls `fileRegistry.Register("cert", "ca.pem", "<cert-content>")`
THEN Register SHALL return the predicted path using the PathResolver for type "cert" and SHALL record the file for later retrieval.

#### Scenario: Idempotent registration with same content

WHEN the same filename and file type are registered twice with identical content
THEN the second call SHALL return the same path as the first without error.

#### Scenario: Conflict detection on different content

WHEN the same filename and file type are registered twice with different content
THEN the second call SHALL return an error indicating a content conflict with the existing and new content sizes.

#### Scenario: Invalid argument count rejected

WHEN Register is called with fewer or more than 3 arguments
THEN it SHALL return an error stating the required argument count.

#### Scenario: Invalid file type rejected

WHEN Register is called with a file type other than "cert", "map", "file", or "crt-list"
THEN it SHALL return an error listing the valid file types.

### Requirement: Auxiliary File Rendering

The RenderService SHALL render auxiliary files (maps, general files, SSL certificates) declared in the configuration in parallel using an errgroup. Each auxiliary file template SHALL be rendered with the same template context as the main configuration. The resulting files SHALL be merged with dynamically registered files from the FileRegistry without mutating either input. The merged result SHALL use each Dataplane API storage kind's effective identity, deduplicate identical definitions, reject conflicting definitions, and sort the canonical set for deterministic output.

#### Scenario: Map files rendered in parallel

WHEN the configuration declares 3 map file templates
THEN all 3 map files SHALL be rendered and included in the AuxiliaryFiles.MapFiles slice.

#### Scenario: Static and dynamic files merged

WHEN the configuration declares 2 map templates and a template dynamically registers 1 map file via FileRegistry
THEN AuxiliaryFiles.MapFiles SHALL contain 3 entries after merging.

#### Scenario: Identical auxiliary definitions collapse

WHEN static and dynamically registered files define the same storage identity with identical content and options
THEN the rendered auxiliary set SHALL contain one independent copy of that file.

#### Scenario: Conflicting auxiliary definitions fail rendering

WHEN two auxiliary definitions resolve to the same Dataplane API storage identity but differ in content, path, type, or options
THEN the RenderService SHALL fail before validation or deployment and instruct the operator to keep one definition.

#### Scenario: No auxiliary files configured

WHEN the configuration declares no maps, files, or SSL certificate templates
THEN the auxiliary file rendering step SHALL return an empty AuxiliaryFiles without launching any goroutines.

#### Scenario: Auxiliary file render failure propagates error

WHEN a map file template fails to render
THEN the RenderService SHALL return an error identifying the failed map file template name.

### Requirement: Render Timeout

The RenderService SHALL apply a configurable render timeout to the context passed to the template engine. When the timeout is zero or unset, no timeout SHALL be applied. When the timeout expires during rendering, the engine SHALL return a timeout error.

#### Scenario: Render completes within timeout

WHEN a render timeout of 10 seconds is configured and rendering completes in 1 second
THEN the RenderService SHALL return a successful RenderResult.

#### Scenario: Render exceeds timeout

WHEN a render timeout is configured and rendering exceeds the timeout
THEN the RenderService SHALL return an error due to context deadline exceeded.

### Requirement: VM Pool Management

The RenderService SHALL expose a ClearVMPool method that delegates to the template engine's ClearVMPool. This SHALL release pooled template engine VMs to reduce memory after parallel rendering spikes.

#### Scenario: ClearVMPool delegates to engine

WHEN ClearVMPool is called on the RenderService
THEN the underlying template engine's ClearVMPool SHALL be invoked.

### Requirement: Template Engine Pre-compilation

Templates SHALL be compiled once at engine initialization time. Syntax errors SHALL be detected at initialization and reported as CompilationErrors. The compiled engine SHALL be thread-safe for concurrent rendering. Each Render call SHALL accept a context for cancellation and timeout control.

#### Scenario: Compilation error detected at initialization

WHEN a template with invalid syntax is provided during engine creation
THEN engine creation SHALL fail with a CompilationError containing the template name and a snippet of the template content.

#### Scenario: Template not found error

WHEN Render is called with a template name that does not exist
THEN the engine SHALL return a TemplateNotFoundError listing available template names.

#### Scenario: Concurrent rendering is safe

WHEN multiple goroutines call Render on the same engine concurrently
THEN each call SHALL produce correct output without data races.

### Requirement: Pipeline Result Includes Status Patches

The Pipeline SHALL return status patches alongside the HAProxy configuration and auxiliary files in PipelineResult. The PipelineResult SHALL include a `StatusPatches` field containing the slice of StatusPatch entries collected during rendering. The Coordinator SHALL propagate status patches through `TemplateRenderedEvent` so that downstream components (StatusApplier) can access them. The ContentChecksum SHALL continue to cover only HAProxy config and auxiliary files (status patches are not part of the data plane content).

#### Scenario: Pipeline returns status patches

- **WHEN** the render phase completes and templates registered 5 status patches
- **THEN** the PipelineResult SHALL contain a `StatusPatches` slice with 5 entries

#### Scenario: TemplateRenderedEvent carries status patches

- **WHEN** the Coordinator publishes a TemplateRenderedEvent after successful pipeline execution
- **THEN** the event SHALL include the StatusPatches from the PipelineResult

#### Scenario: ContentChecksum excludes status patches

- **WHEN** the same HAProxy config and auxiliary files are produced but status patches differ (e.g., different observedGeneration)
- **THEN** the ContentChecksum SHALL remain the same (status patches do not affect deployment decisions)

### Requirement: Phase-Based Variant Selection in Coordinator

The Coordinator SHALL select the appropriate status patch variant based on pipeline outcome. On successful render (before deployment), the Coordinator SHALL make the `rendered` variants available. On successful deployment, the Coordinator SHALL signal the StatusApplier to apply `deployed` variants. On render failure, the Coordinator SHALL signal the StatusApplier to apply `renderFailed` variants from the last successfully rendered status patches. On deployment failure, the Coordinator SHALL signal the StatusApplier to apply `deployFailed` variants from the current render's status patches. The Coordinator SHALL cache the latest successfully rendered status patches for use in render failure scenarios.

#### Scenario: Render success triggers rendered variant

- **WHEN** the pipeline render phase succeeds
- **THEN** the Coordinator SHALL publish an event or signal causing the StatusApplier to apply `rendered` variants

#### Scenario: Deploy success triggers deployed variant

- **WHEN** deployment completes successfully
- **THEN** the Coordinator SHALL publish an event or signal causing the StatusApplier to apply `deployed` variants (superseding previously applied `rendered` variants)

#### Scenario: Render failure triggers renderFailed from cached patches

- **WHEN** the pipeline render phase fails and previous status patches are cached
- **THEN** the Coordinator SHALL signal the StatusApplier to apply `renderFailed` variants from the cached patches

#### Scenario: Deploy failure triggers deployFailed variant

- **WHEN** deployment fails and the current render produced status patches
- **THEN** the Coordinator SHALL signal the StatusApplier to apply `deployFailed` variants from the current render's patches
