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

### Requirement: Release Secret Size Mitigations

The chart SHALL keep the Helm release under the Kubernetes 1 MiB Secret cap through three size reductions. (1) Disabled template-library subcharts SHALL be pruned from the release entirely via subchart conditions, so their source never ships in the release Secret. (2) At merge time, `description` fields SHALL be stripped from every validationTest and from every assertion — documentation-only metadata (~120 KB across the bundled libraries) that the test name and assertion type/target/pattern fully replace. (3) Scriggo documentation comments SHALL be stripped from every templateSnippet in the MERGED output only, via three whole-line-constrained regex passes: the leading `{#- … -#}` doc header, standalone `{# … #}` comment lines, and standalone Go `//` comment lines inside statement blocks. Inline whitespace-control markers that share a line with rendered content SHALL NOT be touched, and library source files SHALL keep their full inline documentation.

#### Scenario: Disabled subchart source pruned from the release

- **WHEN** controller.templateLibraries.gateway.enabled is false
- **THEN** the gateway subchart's library source SHALL be absent from the Helm release Secret, not merely skipped during merge.

#### Scenario: Doc comments stripped from merged output only

- **WHEN** a library snippet begins with a `{#- … -#}` documentation header
- **THEN** the rendered HAProxyTemplateConfig's copy of the snippet SHALL omit the header while the library source file keeps it.

#### Scenario: Test descriptions stripped at merge

- **WHEN** a validationTest defines a `description` on itself and on its assertions
- **THEN** the merged HAProxyTemplateConfig SHALL contain that test without any `description` fields.

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

### Requirement: Frontend Routing Logic and Runtime Map Strategy

The base library SHALL encode all host/path-to-backend routing in runtime-updatable map files resolved by map converters in a fixed `http-request set-var` cascade, so a route change that only alters map contents applies without an HAProxy reload. The host_match cascade SHALL try, in order: full Host header including port (when the header carries a port), `<host>:<listener_port>`, bare host, single-label wildcard (leading label stripped via regsub), host-regex.map, and finally the `:<listener_port>` catch-all. The port-scoped exact lookup SHALL precede the bare-host lookup: a hostname bound on several listener ports has a distinct `host:<port>` key per non-default port plus a bare key for the default port, and trying bare-host first would shadow the port-specific backends. Map values SHALL carry a qualifier: `BACKEND:<name>` routes directly to the named backend; `MULTIBACKEND:<totalWeight>:<key>` selects a backend by computing `rand() % totalWeight` and looking up `<randomWeight>:<key>` in weighted-multi-backend.map. After backend selection, the owning resource's `<namespace>_<name>` prefix SHALL be decomposed into `txn.resource_namespace`, `txn.resource_name`, and `txn.resource_id` (`<namespace>/<name>`), which key the per-resource feature maps (auth, WAF, body-size, header overrides) so per-route policy changes are also reload-free map updates.

#### Scenario: Port-scoped host lookup precedes bare host

- **WHEN** host.map contains keys for both `foo.example.com:8080` and `foo.example.com` and a request for foo.example.com arrives on listener port 8080
- **THEN** the routing SHALL resolve the `foo.example.com:8080` entry, not the default-port one.

#### Scenario: Weighted MULTIBACKEND selection

- **WHEN** a path map entry resolves to `MULTIBACKEND:100:<key>`
- **THEN** the frontend SHALL compute a random weight modulo 100, concatenate it with the key, and resolve the backend from weighted-multi-backend.map.

#### Scenario: Per-route policy keyed by resource identity

- **WHEN** a request resolves to a backend owned by Ingress default/my-ingress
- **THEN** `txn.resource_id` SHALL be `default/my-ingress` and per-resource feature maps SHALL be consulted with that key.

### Requirement: Peers Section and Stick-Table Persistence

The base library SHALL always emit `localpeer local` in the global section and an always-on `peers localinstance` section containing the single peer `peer local unix@<baseDir>/peers.sock`. Stick-tables opt in to reload persistence by appending `peers localinstance` to their definition (the bundled rate-limit tables do); on a master-worker reload the old worker then teaches the new worker the table contents over the local peer, so per-source counters such as `http_req_rate` survive reloads instead of resetting to zero. The peer name SHALL be the static `local` — not a `$HOSTNAME` expansion — so HAProxy recognises the entry as this process and listens on the socket, and so offline `haproxy -c` validation stays deterministic. The peer listener SHALL be a UNIX socket, not a TCP port, so it can never collide with a frontend or dynamically allocated Gateway listener bind. The single local peer never connects to other replicas; stick-table scope is per-replica.

#### Scenario: Peers section always present

- **WHEN** only the base library is enabled
- **THEN** the rendered config SHALL contain `localpeer local` in the global section and a `peers localinstance` section with a single UNIX-socket peer named `local`.

#### Scenario: Rate-limit counters survive a reload

- **WHEN** a stick-table declares `peers localinstance` and HAProxy performs a master-worker reload
- **THEN** the table contents SHALL be replicated from the old worker to the new worker, preserving per-source counters across the reload.

### Requirement: Graceful Reload Cap

The base library SHALL emit `hard-stop-after` in the global section with the value of `extraContext.hardStopAfter`, defaulting to `10s`; an empty-string value SHALL suppress the directive. The cap bounds how long an old worker keeps draining after a reload: without it, a worker pinned by a persistent keep-alive connection survives every reload and accumulates as a stale draining generation, and a request looped through the http-tcp, UNIX-socket, http_frontend path can land on a stale worker mid-reload and be corrupted into a `<BADREQ>` 400. The 10s default is deliberately tighter than the community (10m) and official (30m) HAProxy ingress controllers because structural reload frequency is already throttled by the deployment scheduler's minDeploymentInterval (controller default 2s, chart default 5s) while endpoint changes bypass reloads via the runtime fast path.

#### Scenario: Default cap emitted

- **WHEN** extraContext.hardStopAfter is not set
- **THEN** the global section SHALL contain `hard-stop-after 10s`.

#### Scenario: Explicit empty value suppresses the directive

- **WHEN** extraContext.hardStopAfter is set to the empty string
- **THEN** no `hard-stop-after` directive SHALL be emitted.

### Requirement: Version-Gated SHM Stats

When `extraContext.shmStatsEnabled` is true AND the injected haproxyVersion is 3.3 or newer, the base library SHALL emit `expose-experimental-directives`, `shm-stats-file` (default `/dev/shm/haproxy-stats`, overridable via extraContext.shmStatsPath), and `shm-stats-file-max-objects` (default 50000, overridable via extraContext.shmStatsMaxObjects) in the global section. On older HAProxy versions, or when disabled, it SHALL emit none of these directives.

#### Scenario: Enabled on HAProxy 3.3

- **WHEN** shmStatsEnabled is true and haproxyVersion is 3.3
- **THEN** the global section SHALL contain `shm-stats-file /dev/shm/haproxy-stats` and `shm-stats-file-max-objects 50000`.

#### Scenario: Silently absent on older HAProxy

- **WHEN** shmStatsEnabled is true and haproxyVersion is 3.2
- **THEN** no shm-stats directives SHALL be emitted.

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

The SSL library SHALL watch Secret resources. It SHALL initialize shared state (sslPassthroughBackends and tlsCertificates arrays) in the globalFeatures map via features-050-ssl-initialization using ComputeIfAbsent for atomic initialization. It SHALL generate an HTTPS frontend with SSL termination using a crt-list file. It SHALL generate the crt-list (certificate-list.txt) from registered TLS certificates with per-certificate OCSP stapling configuration ("[ocsp-update on]"). It SHALL include a default certificate entry in the crt-list. It SHALL provide SSL passthrough infrastructure with SNI-based backend routing. The crt-list SHALL be byte-deterministic across renders of identical inputs: each certificate's SNIs are grouped by client-cert-verification config and emitted one line per group with group keys sorted (Scriggo map iteration order is unstable, and a reordered crt-list triggers an HAProxy reload even when the inputs are identical); when multiple Gateway default-certificate candidates exist, the winner SHALL be chosen by lexical (namespace, name) order.

#### Scenario: SSL initialization runs exactly once

WHEN features-050-ssl-initialization is rendered (even if rendered multiple times)
THEN the globalFeatures map SHALL be initialized exactly once via ComputeIfAbsent.

#### Scenario: CRT-list includes registered certificates with OCSP

WHEN the ingress library registers a TLS certificate with SNI patterns ["example.com", "www.example.com"]
THEN the generated crt-list SHALL contain a line with the sanitized certificate filename, "[ocsp-update on]", and the SNI patterns.

#### Scenario: CRT-list is byte-deterministic

- **WHEN** the same certificate registrations are rendered twice
- **THEN** the generated crt-list SHALL be byte-identical, with per-certificate option groups emitted in sorted key order.

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

### Requirement: Per-Gateway Pod-Port Allocation

The gateway library SHALL allocate a unique container pod port to each (Gateway, port) that carries an HTTP or HTTPS listener, so each Gateway's bind carries isolated SSL configuration without OS-level bind collisions; TLS-Passthrough listeners and TLS-Terminate listeners on non-default ports SHALL be excluded (they render through the chart-static ssl-tcp frontend and per-port mode-tcp frontends respectively). Allocation SHALL be a pure hash-and-probe function of the current Gateway set: each key `<gwNs>/<gwName>:<port>` hashes to a slot via the first 7 hex characters of its sha256 modulo the range size, collisions between keys present in the same render are resolved by linear probing in sorted-key order, and the result is the base port plus the slot. Defaults SHALL be perGatewayPodPortBase 18000 and perGatewayPodPortRange 1000. The allocator SHALL NOT read committed Services back from the cluster cache to stabilise allocations — a prior read-back design produced permanent collision lock-in and sustained Service-update oscillation (~50 flips per second) under parallel Gateway churn, because read-back state lags the allocator's own output. The allocator SHALL fail() the render when the key count exceeds the range size, and SHALL be initialised once per render via shared state (ComputeIfAbsent).

#### Scenario: Allocation is deterministic across renders and replicas

- **WHEN** the same set of Gateways is rendered in any order, on any replica, against any cache snapshot
- **THEN** each (Gateway, port) key SHALL receive the same pod port.

#### Scenario: TLS-Passthrough listener excluded

- **WHEN** a Gateway listener has protocol TLS with passthrough mode
- **THEN** no pod-port allocation SHALL be made for it.

#### Scenario: Range exhaustion fails loudly

- **WHEN** the number of (Gateway, port) keys exceeds perGatewayPodPortRange
- **THEN** the render SHALL fail with an error naming the perGatewayPodPortRange knob instead of probing forever.

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

The base library SHALL define map file templates that aggregate entries from all contributing libraries via render_glob. The map files SHALL be: host.map (host to normalized host mapping), path-exact.map (exact path matches using map function), path-prefix-exact.map (prefix boundary matches without trailing slash), path-prefix.map (prefix matches with trailing slash using map_beg), path-regex.map (regex matches using map_reg), and weighted-multi-backend.map (weighted routing entries mapping random_weight:route_key to backend names). Map file paths SHALL be resolved via pathResolver.GetPath with type "map". Every generated map file whose entries aggregate from unordered map state — including the fileRegistry-registered feature maps (ssl-redirect-`<code>`.map, redirect-loc-`<code>`.map, app-root.map, mtls-error.map, hsts.map) — SHALL emit entries in sorted key order with first-writer-wins deduplication per key: Scriggo map iteration order is unstable, and a reordered file is a content change to the sync layer that triggers a spurious HAProxy reload for identical inputs.

#### Scenario: Map file path resolved via PathResolver

WHEN the host.map template is rendered with PathResolver.MapsDir set to "maps"
THEN references to the map file in haproxyConfig SHALL use the path "maps/host.map".

#### Scenario: Generated map files are byte-deterministic

- **WHEN** the same resources are rendered twice
- **THEN** every generated map file SHALL be byte-identical, with entries in sorted key order.

#### Scenario: First writer wins on duplicate keys

- **WHEN** two entries register the same host key in a feature map (for example two redirects claiming one host)
- **THEN** the first registered entry SHALL claim the key and later entries for the same key SHALL be ignored.

### Requirement: Backend Server Generation

The base library's BackendServers macro SHALL resolve service endpoints from EndpointSlices, perform targetPort resolution via Service port name lookup, assign endpoints to numbered server slots (SRV_1 through SRV_N by default), and fill unused slots with disabled placeholder servers at 192.0.2.1:1 (the RFC 5737 TEST-NET-1 sentinel). The placeholder address SHALL be unroutable — not 127.0.0.1 — so a slot that ever combined a placeholder address with an active bind port cannot loop requests back through the proxy's own bind; the worst case is a fast connect timeout plus `option redispatch` failover instead of an unbounded self-loop. When currentConfig is available, the macro SHALL preserve existing slot assignments (keyed by address:port) to enable zero-reload updates via the HAProxy runtime API; when reading slots back, placeholder entries — both the current 192.0.2.1:1 sentinel and the legacy 127.0.0.1:1 one — SHALL be skipped so preservation stays correct across a rolling chart upgrade whose live pods still carry legacy placeholders. The default slot count SHALL be 10, overridable via macro parameter or serverOpts.serverSlotsValue.

#### Scenario: Server slots with placeholder padding

WHEN a backend has 2 active endpoints and 10 server slots
THEN the output SHALL contain 2 enabled server lines and 8 disabled placeholder lines.

#### Scenario: Slot preservation during rolling update

WHEN currentConfig contains a backend with SRV_3 assigned to 10.0.0.3:8080 and that endpoint still exists
THEN the new render SHALL assign the same endpoint to SRV_3 to preserve the slot mapping.

#### Scenario: Legacy placeholders skipped during read-back

- **WHEN** the current config still carries 127.0.0.1:1 placeholder servers from a pre-upgrade chart
- **THEN** slot preservation SHALL treat those slots as unoccupied rather than preserving the placeholder address.

### Requirement: EndpointSlice Condition Filtering

BackendServers SHALL include an endpoint only when it is actually serving. An endpoint SHALL be skipped when `conditions.ready` is explicitly false — kubelet adds new pods to slices well before the readiness probe passes, and dispatching to them produces 503s. An endpoint SHALL be skipped when `conditions.terminating` is explicitly true — most applications stop accepting connections on SIGTERM, well before the grace period elapses. Nil semantics follow the Kubernetes EndpointSlice contract: a nil `ready` means ready (only explicit false skips), and a nil `terminating` means not terminating (only explicit true skips).

#### Scenario: Explicitly not-ready endpoint excluded

- **WHEN** an EndpointSlice endpoint has conditions.ready set to false
- **THEN** no server slot SHALL be assigned to it.

#### Scenario: Nil ready condition counts as ready

- **WHEN** an EndpointSlice endpoint has no conditions.ready value
- **THEN** the endpoint SHALL be included and assigned a slot.

#### Scenario: Terminating endpoint excluded

- **WHEN** an EndpointSlice endpoint has conditions.terminating set to true
- **THEN** no server slot SHALL be assigned to it.

### Requirement: Fresh-Slot Allocation for New Endpoints

Slot assignment SHALL run in two passes: endpoints already present in the prior config keep their slot, and only genuinely new endpoints consume fresh slots. New endpoints SHALL be allocated strictly after the highest slot still occupied in the prior config, wrapping from the last slot back to slot 1 (skipping occupied slots) when needed. A slot vacated in the same render SHALL NOT be reused for a new endpoint: it renders as a disabled placeholder instead, so when a rolling restart's old-pod-terminating and new-pod-ready EndpointSlice updates merge into a single render, the replacement pod lands on a fresh slot and `option redispatch` has a healthy fallback rather than a just-replaced server (prevents 503 SC-- during merged endpoint churn).

#### Scenario: New endpoint takes a slot after the highest used one

- **WHEN** the prior config occupies SRV_1 through SRV_3 and one new endpoint appears
- **THEN** the new endpoint SHALL be assigned SRV_4.

#### Scenario: Vacated slot not reused within the same render

- **WHEN** the pod at SRV_2 disappears and a replacement pod becomes ready in the same render
- **THEN** the replacement SHALL take a slot after the highest used slot, and SRV_2 SHALL render as a disabled placeholder.

#### Scenario: Allocation wraps past the last slot

- **WHEN** the highest used slot equals the slot count and a new endpoint appears
- **THEN** allocation SHALL wrap to slot 1 and probe forward past occupied slots.

### Requirement: Multi-Service Slot Ranges and Server Identity

BackendServers SHALL accept `serverOpts.slotPrefix` (default `SRV_`) and `serverOpts.weight`. A distinct slotPrefix per Service SHALL let one backend host servers from several Services in disjoint, independently slot-preserved ranges (weighted TCPRoute traffic splitting with `balance roundrobin` across backendRefs): slot preservation reads back only servers whose names carry the caller's own prefix. When `weight` is set, every emitted server line SHALL carry a trailing `weight N` argument. When a backend name is provided, every server line — active and placeholder — SHALL carry a stable GUID derived from make_guid("srv", backend, prefix+slot) for runtime identity. The defaults SHALL keep single-Service output byte-for-byte unchanged.

#### Scenario: Disjoint slot ranges per Service in one backend

- **WHEN** one backend renders two Services with slotPrefixes "A_" and "B_"
- **THEN** each Service's slot preservation SHALL read back only the slots carrying its own prefix, and the ranges SHALL be preserved independently.

#### Scenario: Per-slot GUIDs emitted

- **WHEN** BackendServers is called with a backend name
- **THEN** every server line, including placeholders, SHALL carry a guid derived from the backend name and the prefixed slot number.

### Requirement: Service Port Resolution

The ResolveServicePort macro SHALL be the single source of truth for translating an Ingress or Gateway service-port reference into a concrete port number and port name, and all backend-rendering call sites in the bundled resource and annotation libraries SHALL route through it before invoking BackendServers. It SHALL abort the render via fail() when the port reference is nil or carries neither a number nor a name, when the referenced Service EXISTS but has no port with the referenced name (a typo, not a propagation race), or when a number-based resolution yields a port that is 0 or negative. It SHALL NOT fall back to port 80 — the previous silent fallback produced syntactically valid configs pointing at ports nothing listened on. When the reference is by number and the Service is absent, resolution SHALL succeed with an empty port name: the numeric port is trusted without Service validation. When the reference is by NAME and the Service is absent from the store, resolution SHALL degrade instead of failing — Kubernetes permits an Ingress or Route to exist before its Service (eventual consistency, and admission dry-runs can race the Service watch event) — yielding a placeholder resolution that BackendServers renders as placeholder-only slots (that route serves 503 until the Service propagates and a later reconcile converges); endpoints whose port cannot be resolved SHALL be skipped rather than emitted with port 0, and an EndpointSlice that already carries the named port SHALL yield real servers even before the Service is cached. A genuinely failed render surfaces in the controller logs and in the resource's deployFailed status while the last-known-good config keeps serving.

#### Scenario: Named port with missing Service degrades to placeholders

- **WHEN** a backend references a Service port by name and the Service is not yet in the store
- **THEN** the render SHALL succeed with placeholder-only server slots for that backend (no server with port 0), the rest of the configuration SHALL be unaffected, and a later reconcile SHALL converge once the Service propagates.

#### Scenario: Unknown port name fails with available names listed

- **WHEN** the referenced Service exists but has no port with the referenced name
- **THEN** the render SHALL fail with an error listing the Service's actual port names.

#### Scenario: Numeric port trusted without the Service

- **WHEN** a backend references a Service port by number and the Service is absent from the store
- **THEN** resolution SHALL succeed with the numeric port and an empty port name.

### Requirement: Degraded Backend Visibility

A degraded by-name resolution that leaves a backend placeholder-only (the Service is absent AND no EndpointSlice carries the named port, so the backend serves 503) SHALL be operator-visible on the owning resource — a permanent Service-name typo must not be distinguishable from a propagation race only by silence. BackendServers SHALL record each such reference in the render's shared context under a `degradedBackendRef:<namespace>/<service>/<portName>` key; it SHALL NOT record references that produced at least one real server (an EndpointSlice can resolve the named port before the Service is cached). Because networking.k8s.io/v1 Ingress status has no conditions field, the ingress library SHALL surface the signal as a core/v1 Warning Event per affected Ingress, emitted via `k8sResources` (template `ingress-degraded-backend-events`): reason `BackendUnresolved`, `involvedObject` carrying the Ingress's apiVersion, kind, namespace, name, and uid (kubectl describe matches events by involvedObject.uid), a message naming every degraded Service/portName reference of that Ingress, and a deterministic Event name so Server-Side Apply updates one object per Ingress and orphan pruning deletes it as soon as the reference resolves. The Event's lastTimestamp SHALL be time-bucketed (not per-render) so ongoing degradation periodically re-applies the Event — refreshing the apiserver's event TTL — without churning the applier on every reconcile. Gateway API routes already carry the spec-correct signal (`ResolvedRefs: False` with reason `BackendNotFound` on the route parent status) when a backendRef Service is absent; no Event SHALL be emitted for them.

#### Scenario: Placeholder-only by-name backend emits a Warning Event

- **WHEN** an Ingress references a Service port by name, the Service is absent from the store, and no EndpointSlice resolves the named port
- **THEN** the `ingress-degraded-backend-events` template SHALL emit one core/v1 Event with type Warning and reason BackendUnresolved in the Ingress's namespace, whose involvedObject identifies the Ingress (including uid) and whose message names the unresolvable Service and port name.

#### Scenario: EndpointSlice-resolved backend emits no Event

- **WHEN** the Service is absent but an EndpointSlice already carries the named port and yields real servers
- **THEN** no Event SHALL be emitted for that reference.

#### Scenario: Resolved or by-number references emit no Event

- **WHEN** the referenced Service exists, or the reference is by port number
- **THEN** no Event SHALL be emitted (by-number references are trusted without Service validation and never degrade to port 0).

#### Scenario: Event disappears when the Service arrives

- **WHEN** a previously degraded reference resolves on a later reconcile
- **THEN** the Event object SHALL vanish from the rendered k8sResources set, and the resource applier's render-diff orphan pruning SHALL delete it from the cluster.

#### Scenario: HTTPRoute with an absent backend Service carries BackendNotFound

- **WHEN** an HTTPRoute's backendRef names a Service absent from the store
- **THEN** the route's parent status SHALL contain `ResolvedRefs: False` with reason `BackendNotFound` naming the missing Service.

### Requirement: Config-Driven Reloads Without Server State File

The chart SHALL NOT emit `server-state-file` or `load-server-state-from-file`, and the HAProxy pod's reload command SHALL be a plain master-socket reload with no server-state dump. HAProxy only restores a server's address from state via the init-addr resolution chain, which runs solely for FQDN and DNS-SRV servers; pod servers are IP literals, so the parsed config address always wins on reload — the state machinery preserved nothing for pod addresses while its port restoration could mint stale-slot hybrids. Endpoint correctness across reloads SHALL rely on the deploy pipeline rendering the current endpoints into every pushed config, plus the runtime fast path for reload-free convergence.

#### Scenario: No server-state directives in rendered config

- **WHEN** the base library renders with any combination of libraries enabled
- **THEN** the output SHALL contain neither `server-state-file` nor `load-server-state-from-file`.

#### Scenario: Reload takes addresses from the rendered config

- **WHEN** HAProxy reloads
- **THEN** every server's address SHALL come from the rendered configuration, not from a state file.

### Requirement: Library Enable/Disable via Values

Each library SHALL be independently toggleable via values.yaml at controller.templateLibraries.<name>.enabled. The base and ssl libraries SHALL be enabled by default. The ingress library SHALL be enabled by default. The gateway library SHALL be enabled by default (subject to CRD availability). The haproxytech, haproxy-ingress, nginx-ingress, ingress-annotations-compat, and spoa-hub libraries SHALL have their default enabled state defined in values.yaml. The path-regex-last library no longer exists; the regex-last routing variant is activated via controller.config.routing.regexMatchOrder.

#### Scenario: All libraries disabled except base

WHEN only controller.templateLibraries.base.enabled is true and all others are false
THEN the merged config SHALL contain only base library content and the HAProxy config SHALL be valid (returning 404 for all requests).

### Requirement: Embedded Validation Tests

Libraries SHALL support embedded validation tests in a validationTests section. Each test SHALL specify a description, fixtures (Kubernetes resource manifests), and assertions. The _global test SHALL apply to all tests (providing shared fixtures and assertions). The haproxy_valid assertion type SHALL verify the rendered output is valid HAProxy configuration. The deterministic assertion type SHALL verify repeated renders produce identical output. The contains assertion type SHALL verify the rendered output contains a pattern. A test MAY additionally carry a `_helm_skip_test` template expression: at chart render time the merge loader SHALL drop the test when the expression evaluates to "true" and SHALL strip the marker otherwise. Two chart predicates SHALL gate bundled tests: nginx-annotation tests are skipped when the nginx-ingress library is disabled (their fixtures rely on annotations only that library scans), and experimental-channel HTTPRoute field tests (sessionPersistence per GEP-1619, retry per GEP-1731) are skipped unless controller.templateLibraries.gateway.experimentalChannel is true. The channel MUST be operator-declared: as of Gateway API v1.6 the Standard and Experimental installs ship an identical CRD set, so GVK-level Capabilities detection cannot distinguish channels, and an un-skipped experimental-field test on a Standard-channel cluster fails and crash-loops the controller under the fatal load gate.

#### Scenario: Validation test with fixtures and assertions

WHEN a library defines a validation test with Ingress fixtures and a contains assertion for a backend name
THEN running the validation suite SHALL render templates with those fixtures and verify the backend name appears in the output.

#### Scenario: Global test assertions apply to all tests

WHEN the _global test defines a haproxy_valid assertion
THEN every test in the suite SHALL also verify its output is valid HAProxy configuration.

#### Scenario: Experimental-channel tests skipped on Standard channel

- **WHEN** controller.templateLibraries.gateway.experimentalChannel is false (the default)
- **THEN** validation tests asserting sessionPersistence or retry directives SHALL be absent from the merged HAProxyTemplateConfig.

#### Scenario: Skip marker stripped from retained tests

- **WHEN** a test's _helm_skip_test expression does not evaluate to "true"
- **THEN** the merged output SHALL contain the test without the _helm_skip_test key.

### Requirement: Migration Coverage Declarations

Each vendor annotation library (nginx-ingress, haproxy-ingress, haproxytech) SHALL declare a top-level `migrationCoverage` list documenting how it handles the annotations of the source controller it emulates. Each list entry SHALL carry a `source` name, a `detect` block (the source's conventional `ingressClasses` and `annotationPrefixes`), and an `annotations` map keyed by full annotation key. Each annotation entry SHALL carry a `status` of `supported`, `different`, `dropped`, or `fails`, a plain-language `note`, and an optional `doc` anchor. The declarations are opaque data consumed by migration tooling; they SHALL NOT influence rendering or reconciliation. The merge loader SHALL concatenate the `migrationCoverage` lists of all ENABLED libraries into the rendered HAProxyTemplateConfig spec (like templateSnippets), and a DISABLED library SHALL contribute nothing. The CRD SHALL expose `spec.migrationCoverage` as structured data (a list keyed by `source`), and the core config loader SHALL carry it through unchanged.

The declared coverage SHALL be kept in lock-step with the templates two ways, enforced at lint time: every annotation key a library's templates READ (as a quoted string literal, plus the CORS suffixes read via the shared macro) SHALL be declared, and every declared non-`dropped` annotation SHALL be read by some template (`dropped` documents annotations that are intentionally inert or unread). The per-source annotation-support tables in the migration guide SHALL be GENERATED from the coverage data between marker comments, and a lint check SHALL fail if regeneration would change the guide.

#### Scenario: Enabled library contributes its migration coverage

- **WHEN** the nginx-ingress library is enabled
- **THEN** the rendered HAProxyTemplateConfig `spec.migrationCoverage` SHALL contain an entry with `source: ingress-nginx` whose `detect.annotationPrefixes` includes `nginx.ingress.kubernetes.io/`.

#### Scenario: Disabled library contributes nothing

- **WHEN** all three vendor annotation libraries are disabled
- **THEN** the rendered HAProxyTemplateConfig SHALL NOT contain a `spec.migrationCoverage` field.

#### Scenario: Coverage drift fails the lint gate

- **WHEN** a library template reads an annotation key that its `migrationCoverage` does not declare, or declares a non-`dropped` annotation no template reads
- **THEN** the migration-coverage drift check SHALL fail with an actionable message naming the offending keys.
