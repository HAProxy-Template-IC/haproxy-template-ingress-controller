## MODIFIED Requirements

### Requirement: Base Library

The base library SHALL always be enabled and SHALL be completely resource-agnostic (no access to Ingress, HTTPRoute, or any other specific resource fields). It SHALL define the haproxyConfig entry-point template containing the full HAProxy configuration structure: global section with "default-path origin" and crt-base directives, defaults section with error files and `balance roundrobin`, a status frontend on port 8404, an HTTP frontend with routing logic, and a default_backend returning 404. The base library SHALL define all extension points using render_glob and SHALL provide utility macros (SanitizeRegex, CalculateShardCount, HostMatchCondition, BackendServers, BuildServerOptions). It SHALL define map file templates (host.map, path-exact.map, path-prefix-exact.map, path-prefix.map, path-regex.map, weighted-multi-backend.map) and static error page files (400, 403, 408, 500, 502, 503, 504). The haproxyConfig SHALL apply a regex_replace post-processor normalizing indentation to 2 spaces.

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

### Requirement: Ingress Library

The Ingress library SHALL watch networking.k8s.io/v1 Ingress resources (filtered by spec.ingressClassName injected from Helm values), v1 Services, and discovery.k8s.io/v1 EndpointSlices. It SHALL register TLS certificates from Ingress spec.tls sections with the SSL infrastructure, deduplicated by namespace+secretName using first_seen. It SHALL generate backend names in the format `ing_<namespace>_<name>_<serviceName>_<port>`. It SHALL populate host.map, path-exact.map, path-prefix.map, and path-prefix-exact.map with entries derived from Ingress rules using the BACKEND qualifier format. Path types Exact, Prefix, and ImplementationSpecific SHALL be supported. Ingress backends SHALL NOT set a `balance` directive, inheriting the default from the base library's defaults section. Annotation-driven balance overrides from annotation libraries SHALL take precedence when set.

#### Scenario: Ingress backend generated with correct name

WHEN an Ingress "my-ingress" in namespace "default" routes to service "my-svc" port 80
THEN a backend named "ing_default_my-ingress_my-svc_80" SHALL be generated.

#### Scenario: Ingress backend inherits balance from defaults

WHEN an Ingress backend is rendered without a load-balance annotation
THEN the backend section SHALL NOT contain a `balance` directive, inheriting `roundrobin` from the defaults section.

#### Scenario: Ingress host.map entry generated

WHEN an Ingress has a rule with host "example.com"
THEN the host.map SHALL contain a line mapping "example.com" to a normalized host identifier.

#### Scenario: TLS certificate deduplication

WHEN two Ingress resources in the same namespace reference the same TLS secret
THEN the certificate SHALL be registered with the SSL infrastructure only once.

### Requirement: Gateway API Library

The Gateway API library SHALL watch gateway.networking.k8s.io/v1 Gateway, HTTPRoute, and GRPCRoute resources, plus Services and EndpointSlices. It SHALL support HTTPRoute path matching types: Exact, PathPrefix, and RegularExpression. It SHALL support advanced matching: Method, Headers (Exact and RegularExpression), and QueryParams. It SHALL support filters: RequestHeaderModifier, ResponseHeaderModifier, RequestRedirect, and URLRewrite. It SHALL support weighted traffic splitting using the MULTIBACKEND qualifier with weighted-multi-backend.map. It SHALL implement automatic sharded parallel processing for large route sets. It SHALL register SSL passthrough backends and TLS Terminate certificates with the SSL infrastructure. Gateway backends SHALL NOT set a `balance` directive, inheriting the default from the base library's defaults section.

#### Scenario: HTTPRoute with weighted backends

WHEN an HTTPRoute has two backendRefs with weights 80 and 20
THEN the path map entry SHALL use the MULTIBACKEND qualifier with total weight 100, and weighted-multi-backend.map SHALL contain entries mapping weight ranges to the respective backends.

#### Scenario: HTTPRoute method matching

WHEN an HTTPRoute match specifies method "GET"
THEN a frontend-matchers-advanced snippet SHALL generate HAProxy ACL rules that refine the path match based on the HTTP method.

#### Scenario: Gateway backend inherits balance from defaults

WHEN a Gateway HTTPRoute backend is rendered without a load-balance annotation
THEN the backend section SHALL NOT contain a `balance` directive, inheriting `roundrobin` from the defaults section.

#### Scenario: Gateway TLS Terminate certificate registration

WHEN a Gateway listener has tls.mode "Terminate" with a certificateRef pointing to a Secret
THEN the certificate SHALL be registered with the SSL infrastructure for crt-list generation.
