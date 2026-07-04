# Template Libraries — Delta

## MODIFIED Requirements

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
