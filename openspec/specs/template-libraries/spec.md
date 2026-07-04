# Template Libraries

## Purpose

YAML-based library system providing composable HAProxy configuration through a strict merge order, extension point pattern, shared state communication, and embedded validation tests.

## Requirements

### Requirement: Library Merge Order

Libraries SHALL be merged using mustMergeOverwrite in the following fixed order: base, ssl, ingress, gateway, ingress-annotations-compat, haproxytech, haproxy-ingress, nginx-ingress, spoa-hub, values.yaml. The base, ssl, ingress, and ingress-annotations-compat libraries live as files (or split-library directories) under `libraries/`; the gateway, haproxytech, haproxy-ingress, and nginx-ingress libraries are conditional SUBCHARTS (`charts/haptic/charts/<name>/`) referenced as `subchart:<name>` entries in `haptic.mergeLibraries`. A disabled subchart is pruned from the release Secret, so its `.Subcharts.<name>` is absent and the entry is skipped during merge. Later libraries SHALL override earlier ones for the same keys. Each library SHALL be independently enableable via values.yaml under controller.templateLibraries.<name>.enabled. The gateway library SHALL additionally require Gateway API CRDs to be present (Capabilities.APIVersions.Has "gateway.networking.k8s.io/v1/GatewayClass"). The regex-last path matching order is NOT a separate library; it is selected via controller.config.routing.regexMatchOrder ("default" or "last") which swaps a snippet inside base.yaml at Helm render time.

#### Scenario: Later library overrides earlier for same snippet name

WHEN an earlier library and a later library both define a templateSnippet with the same name
THEN the merged result SHALL contain the later library's version because it is merged last.

#### Scenario: Disabled library excluded from merge

WHEN controller.templateLibraries.ingress.enabled is set to false
THEN no snippets, watchedResources, or maps from ingress.yaml SHALL appear in the merged output.

#### Scenario: Gateway library requires CRD presence

WHEN controller.templateLibraries.gateway.enabled is true but Gateway API CRDs are not available
THEN the gateway library SHALL NOT be merged.

#### Scenario: Values.yaml overrides all libraries

WHEN a user defines a templateSnippet in values.yaml with the same name as one in a library
THEN the user's version SHALL take precedence over all library versions.

### Requirement: Base Library

The base library SHALL always be enabled and SHALL be completely resource-agnostic (no access to Ingress, HTTPRoute, or any other specific resource fields). It SHALL define the haproxyConfig entry-point template containing the full HAProxy configuration structure: global section with "default-path origin" and crt-base directives, defaults section with error files and `balance roundrobin`, a status frontend on port 8404, an HTTP frontend with routing logic, and a default_backend returning 404. The base library SHALL define all extension points using render_glob and SHALL provide utility macros (CalculateShardCount, HostMatchCondition, BackendServers, BuildServerOptions). It SHALL define map file templates (host.map, path-exact.map, path-prefix-exact.map, path-prefix.map, path-regex.map, weighted-multi-backend.map) and static error page files (400, 403, 408, 500, 502, 503, 504). The haproxyConfig SHALL apply a regex_replace post-processor normalizing indentation to 2 spaces.

#### Scenario: Base library renders valid HAProxy config with no resources

WHEN only the base library is enabled and no Kubernetes resources exist
THEN the rendered haproxyConfig SHALL be valid HAProxy syntax containing global, defaults, frontend status, frontend http_frontend, and backend default_backend sections.

#### Scenario: Defaults section includes balance roundrobin

WHEN the base library defaults section is rendered
THEN it SHALL contain `balance roundrobin` to ensure consistent load-balancing behavior across all HAProxy versions including 3.3+ where the default changed to `random`.

#### Scenario: Base library does not reference resource-specific fields

WHEN the base library templates are examined
THEN no template content SHALL reference ingress, httproute, grpcroute, or any resource-specific dot paths.

#### Scenario: Map files populated via render_glob

WHEN the host.map template is rendered
THEN it SHALL execute render_glob "map-host-*" to collect host mappings from all contributing libraries.

### Requirement: Extension Point Pattern

The base library SHALL define extension points as render_glob calls with specific patterns. Each extension point SHALL render all matching template snippets in alphabetical order. Extension points SHALL include: `features-*` (feature registration), `status-patches-*` (resource status patch registration), `global-top-*` (global sections), `frontend-matchers-advanced-*` (advanced route matching), `frontend-filters-*` (request/response filters), `frontends-*` (additional frontends), `backends-*` (backend definitions), `backend-directives-*` (backend configuration), `map-host-*` (host mappings), `map-path-exact-*` (exact path mappings), `map-pfxexact-*` (prefix-exact mappings), `map-path-prefix-*` (prefix mappings), `map-path-regex-*` (regex mappings), and `map-weighted-backend-*` (weighted routing).

#### Scenario: Snippets from multiple libraries rendered in alphabetical order

- **WHEN** backends-500-ingress (from ingress.yaml) and backends-500-gateway (from gateway.yaml) both exist
- **THEN** render_glob "backends-*" SHALL render backends-500-gateway before backends-500-ingress.

#### Scenario: Extension point renders nothing when no snippets match

- **WHEN** no library defines a snippet matching "global-top-*"
- **THEN** render_glob "global-top-*" SHALL produce empty output.

#### Scenario: Status patches extension point renders at priority 200

- **WHEN** `status-patches-200-ingress` and `backends-500-ingress` both exist
- **THEN** render_glob "status-patches-*" SHALL execute before render_glob "backends-*"

### Requirement: Snippet Priority Numbering

Snippets SHALL use numeric prefixes to control execution order within render_glob patterns. The following ranges SHALL be reserved: 000-099 for infrastructure/initialization, 100-199 for feature registration and basic config, 200-299 for access control, 300-399 for CORS and header manipulation, 400-499 for redirects and rewrites, 500-599 for core functionality, 600-699 for compatibility layers, and 900-999 for finalization.

#### Scenario: Lower-numbered snippet executes first

WHEN features-050-ssl-initialization and features-100-ingress-tls both exist
THEN render_glob "features-*" SHALL render features-050-ssl-initialization before features-100-ingress-tls.

### Requirement: SSL Library

The SSL library SHALL watch Secret resources. It SHALL initialize shared state (sslPassthroughBackends and tlsCertificates arrays) in the globalFeatures map via features-050-ssl-initialization using ComputeIfAbsent for atomic initialization. It SHALL generate an HTTPS frontend with SSL termination using a crt-list file. It SHALL generate the crt-list (certificate-list.txt) from registered TLS certificates with per-certificate OCSP stapling configuration ("[ocsp-update on]"). It SHALL include a default certificate entry in the crt-list. It SHALL provide SSL passthrough infrastructure with SNI-based backend routing.

#### Scenario: SSL initialization runs exactly once

WHEN features-050-ssl-initialization is rendered (even if rendered multiple times)
THEN the globalFeatures map SHALL be initialized exactly once via ComputeIfAbsent.

#### Scenario: CRT-list includes registered certificates with OCSP

WHEN the ingress library registers a TLS certificate with SNI patterns ["example.com", "www.example.com"]
THEN the generated crt-list SHALL contain a line with the sanitized certificate filename, "[ocsp-update on]", and the SNI patterns.

### Requirement: Ingress Library

The Ingress library SHALL watch networking.k8s.io/v1 Ingress resources (filtered by spec.ingressClassName injected from Helm values), v1 Services, and discovery.k8s.io/v1 EndpointSlices. It SHALL register TLS certificates from Ingress spec.tls sections with the SSL infrastructure, deduplicated by namespace+secretName using first_seen. It SHALL generate backend names in the format `<namespace>_<name>_svc_<serviceName>_<portIdentifier>`, where `<portIdentifier>` is the Service port NAME resolved via Service lookup when the Ingress references the port by number (falling back to the port number only when no name is available). It SHALL populate host.map, path-exact.map, path-prefix.map, and path-prefix-exact.map with entries derived from Ingress rules using the BACKEND qualifier format. Path types Exact, Prefix, and ImplementationSpecific SHALL be supported. Ingress backends SHALL NOT set a `balance` directive, inheriting the default from the base library's defaults section. Annotation-driven balance overrides from annotation libraries SHALL take precedence when set.

#### Scenario: Ingress backend inherits balance from defaults

WHEN an Ingress backend is rendered without a load-balance annotation
THEN the backend section SHALL NOT contain a `balance` directive, inheriting `roundrobin` from the defaults section.

#### Scenario: Ingress backend generated with correct name

WHEN an Ingress "my-ingress" in namespace "default" routes to service "my-svc" on a port named "http" (e.g. referenced by number 80, resolved to its port name via Service lookup)
THEN a backend named "default_my-ingress_svc_my-svc_http" SHALL be generated.

#### Scenario: Ingress host.map entry generated

WHEN an Ingress has a rule with host "example.com"
THEN the host.map SHALL contain a line mapping "example.com" to a normalized host identifier.

#### Scenario: TLS certificate deduplication

WHEN two Ingress resources in the same namespace reference the same TLS secret
THEN the certificate SHALL be registered with the SSL infrastructure only once.

### Requirement: Gateway API Library

The Gateway API library SHALL watch gateway.networking.k8s.io GatewayClass, Gateway, HTTPRoute, GRPCRoute, TLSRoute, TCPRoute, ReferenceGrant, BackendTLSPolicy, and ListenerSet resources, plus Services and EndpointSlices, declaring for each gateway kind an ordered apiVersions candidate list covering every served version in the Gateway API release history that is schema-compatible (BackendTLSPolicy v1alpha2 is deliberately excluded as an incompatible shape), and marking every gateway kind optional. Each gateway snippet and validation test SHALL declare `requires` naming the watched resources it depends on, so that on clusters where a kind's CRD is not served the corresponding feature strips atomically instead of failing template compilation or the validation-test load gate. Snippets that survive a strip SHALL reach stripped resources only through compile-safe seams (`render` with a `default` clause, `render_glob` extension points, or shared-state read-backs), never through a direct typed reference or import. Status-patch macros SHALL pass the resolved version (`resources.<name>.APIVersion()`) instead of hardcoded version literals. The GatewayClass object SHALL be created through the library's runtime-rendered `k8sResources` at the resolved version rather than by a Helm template. The library SHALL NOT be gated on Helm `.Capabilities`; the `controller.templateLibraries.gateway.enabled` value remains the operator's intent switch. It SHALL support HTTPRoute path matching types: Exact, PathPrefix, and RegularExpression. It SHALL support advanced matching: Method, Headers (Exact and RegularExpression), and QueryParams. It SHALL support filters: RequestHeaderModifier, ResponseHeaderModifier, RequestRedirect, and URLRewrite. It SHALL support weighted traffic splitting using the MULTIBACKEND qualifier with weighted-multi-backend.map. It SHALL implement automatic sharded parallel processing for large route sets. It SHALL register SSL passthrough backends and TLS Terminate certificates with the SSL infrastructure. Gateway backends SHALL NOT set a `balance` directive, inheriting the default from the base library's defaults section. Typed field accesses to fields absent from the oldest schema generation a kind's candidate list can resolve to SHALL be dig()-guarded.

#### Scenario: HTTPRoute with weighted backends

- **WHEN** an HTTPRoute has two backendRefs with weights 80 and 20
- **THEN** the path map entry SHALL use the MULTIBACKEND qualifier with total weight 100, and weighted-multi-backend.map SHALL contain entries mapping weight ranges to the respective backends.

#### Scenario: Gateway backend inherits balance from defaults

- **WHEN** a Gateway HTTPRoute backend is rendered without a load-balance annotation
- **THEN** the backend section SHALL NOT contain a `balance` directive, inheriting `roundrobin` from the defaults section.

#### Scenario: HTTPRoute method matching

- **WHEN** an HTTPRoute match specifies method "GET"
- **THEN** a frontend-matchers-advanced snippet SHALL generate HAProxy ACL rules that refine the path match based on the HTTP method.

#### Scenario: Gateway TLS Terminate certificate registration

- **WHEN** a Gateway listener has tls.mode "Terminate" with a certificateRef pointing to a Secret
- **THEN** the certificate SHALL be registered with the SSL infrastructure for crt-list generation.

#### Scenario: Old-release cluster degrades per kind instead of bricking

- **WHEN** the cluster serves a Gateway API release where some kinds are absent (for example v1.1: no TLSRoute, TCPRoute, BackendTLSPolicy, or ListenerSet CRDs)
- **THEN** the controller SHALL become Ready with HTTP and GRPC routing active at the resolved versions, and every feature requiring an absent kind SHALL be stripped.

#### Scenario: Status patches follow the resolved version

- **WHEN** HTTPRoute resolves to v1beta1 on a pre-v1.0 Gateway API install
- **THEN** HTTPRoute status patches SHALL target gateway.networking.k8s.io/v1beta1.

### Requirement: HAProxyTech Annotations Library

The HAProxyTech library SHALL process `haproxy.org/*` annotations on Ingress resources. It SHALL read annotation values via direct typed access on the Ingress (`ingress.Metadata.Annotations[...]`) inline within its snippets, rather than through dedicated helper macros. It SHALL support SSL passthrough via haproxy.org/ssl-passthrough annotation. It SHALL provide backend-directives extension point snippets for backend configuration (config-snippet, server options). It SHALL provide frontend-filters extension point snippets for request/response manipulation (headers, access control, CORS, SSL redirect). It SHALL support userlist generation from auth secrets via global-top-* snippets.

#### Scenario: Backend config snippet annotation applied

WHEN an Ingress has annotation haproxy.org/backend-config-snippet with value "option httpchk GET /health"
THEN the generated backend SHALL contain the literal snippet content.

#### Scenario: SSL passthrough annotation

WHEN an Ingress has annotation haproxy.org/ssl-passthrough set to "true"
THEN the ingress SHALL be registered as an SSL passthrough backend with the SSL infrastructure.

### Requirement: HAProxy Ingress Compatibility Library

The HAProxy Ingress library SHALL process haproxy-ingress.github.io/* annotations. It SHALL support path-type annotations (regex, exact, prefix, begin). It SHALL support backend configuration (timeouts, balance-algorithm, maxconn, health checks, proxy-protocol, secure backends). It SHALL support frontend filters (allowlist, denylist, SSL redirect, HSTS, app-root, CORS). It SHALL support session affinity via cookie-based persistence. It SHALL support authentication via auth-secret and auth-realm.

#### Scenario: Regex path type annotation

WHEN an Ingress has annotation haproxy-ingress.github.io/path-type set to "regex" with path "/api/v[0-9]+"
THEN the path SHALL be added to path-regex.map instead of path-prefix.map.

### Requirement: Path Regex Last Library

The regex-last routing order SHALL change path matching from Exact > Regex > Prefix-exact > Prefix (default) to Exact > Prefix-exact > Prefix > Regex. This SHALL be opt-in via the Helm value controller.config.routing.regexMatchOrder set to "last" (default is "default"). The alternate snippet is defined in base.yaml and swapped in at Helm render time; there is no separate path-regex-last library file. The overriding snippet SHALL preserve all other routing logic (host extraction, wildcard matching, MULTIBACKEND/BACKEND qualifier handling).

#### Scenario: Regex evaluated after prefix when enabled

WHEN controller.config.routing.regexMatchOrder is set to "last"
THEN the frontend-routing-logic SHALL evaluate path-prefix.map before path-regex.map.

#### Scenario: Default order without library

WHEN controller.config.routing.regexMatchOrder is set to "default" (or left unset)
THEN the frontend-routing-logic SHALL evaluate path-regex.map before path-prefix-exact.map and path-prefix.map.

### Requirement: Cross-Library Shared State

Libraries SHALL communicate across boundaries using a globalFeatures map stored in the SharedContext (accessible via shared.Get("globalFeatures")). All shared state keys SHALL use camelCase naming. The SSL library SHALL initialize the structure. Resource libraries (ingress, gateway, haproxytech) SHALL append to sslPassthroughBackends and tlsCertificates arrays. The SSL library SHALL consume these arrays to generate HTTPS frontends and crt-list content.

#### Scenario: camelCase key consistency enforced

WHEN a library writes to gf["tlsCertificates"] and another reads gf["tlsCertificates"]
THEN the reader SHALL see the values written by the writer because they use the identical key.

#### Scenario: Multiple libraries contribute to shared arrays

WHEN both the ingress and gateway libraries register TLS certificates
THEN the SSL library's crt-list generation SHALL include certificates from both libraries.

### Requirement: Macro Definition and Import

Template snippets SHALL support macro definitions with uppercase names for cross-file visibility. Macros SHALL be importable using the import statement with a for clause to select specific macros. Macros SHALL support typed parameters. Utility snippet files (prefixed "util-") SHALL contain only macro definitions (no standalone output).

#### Scenario: Macro imported from utility snippet

WHEN a template contains {% import "util-backend-name-ingress" for BackendNameIngress %}
THEN the BackendNameIngress macro SHALL be available for use in that template.

#### Scenario: Lowercase macros are file-local

WHEN a macro with a lowercase first letter is defined in a snippet
THEN it SHALL NOT be importable by other snippets.

### Requirement: Map File Generation

The base library SHALL define map file templates that aggregate entries from all contributing libraries via render_glob. The map files SHALL be: host.map (host to normalized host mapping), path-exact.map (exact path matches using map function), path-prefix-exact.map (prefix boundary matches without trailing slash), path-prefix.map (prefix matches with trailing slash using map_beg), path-regex.map (regex matches using map_reg), and weighted-multi-backend.map (weighted routing entries mapping random_weight:route_key to backend names). Map file paths SHALL be resolved via pathResolver.GetPath with type "map".

#### Scenario: Map file path resolved via PathResolver

WHEN the host.map template is rendered with PathResolver.MapsDir set to "maps"
THEN references to the map file in haproxyConfig SHALL use the path "maps/host.map".

### Requirement: Backend Server Generation

The base library's BackendServers macro SHALL resolve service endpoints from EndpointSlices, perform targetPort resolution via Service port name lookup, assign endpoints to numbered server slots (SRV_1 through SRV_N), and fill unused slots with disabled placeholder servers (192.0.2.1:1, the RFC 5737 TEST-NET-1 sentinel). When currentConfig is available, the macro SHALL preserve existing slot assignments to enable zero-reload updates via HAProxy runtime API. The default slot count SHALL be 10, overridable via macro parameter or serverOpts.serverSlotsValue.

#### Scenario: Server slots with placeholder padding

WHEN a backend has 2 active endpoints and 10 server slots
THEN the output SHALL contain 2 enabled server lines and 8 disabled placeholder lines.

#### Scenario: Slot preservation during rolling update

WHEN currentConfig contains a backend with SRV_3 assigned to 10.0.0.3:8080 and that endpoint still exists
THEN the new render SHALL assign the same endpoint to SRV_3 to preserve the slot mapping.

### Requirement: Library Enable/Disable via Values

Each library SHALL be independently toggleable via values.yaml at controller.templateLibraries.<name>.enabled. The base and ssl libraries SHALL be enabled by default. The ingress library SHALL be enabled by default. The gateway library SHALL be enabled by default (subject to CRD availability). The haproxytech, haproxy-ingress, nginx-ingress, ingress-annotations-compat, and spoa-hub libraries SHALL have their default enabled state defined in values.yaml. The path-regex-last library no longer exists; the regex-last routing variant is activated via controller.config.routing.regexMatchOrder.

#### Scenario: All libraries disabled except base

WHEN only controller.templateLibraries.base.enabled is true and all others are false
THEN the merged config SHALL contain only base library content and the HAProxy config SHALL be valid (returning 404 for all requests).

### Requirement: Embedded Validation Tests

Libraries SHALL support embedded validation tests in a validationTests section. Each test SHALL specify a description, fixtures (Kubernetes resource manifests), and assertions. The _global test SHALL apply to all tests (providing shared fixtures and assertions). The haproxy_valid assertion type SHALL verify the rendered output is valid HAProxy configuration. The deterministic assertion type SHALL verify repeated renders produce identical output. The contains assertion type SHALL verify the rendered output contains a pattern.

#### Scenario: Validation test with fixtures and assertions

WHEN a library defines a validation test with Ingress fixtures and a contains assertion for a backend name
THEN running the validation suite SHALL render templates with those fixtures and verify the backend name appears in the output.

#### Scenario: Global test assertions apply to all tests

WHEN the _global test defines a haproxy_valid assertion
THEN every test in the suite SHALL also verify its output is valid HAProxy configuration.
