# Route traffic

Use the bundled Ingress and Gateway API support to route traffic to your
Kubernetes Services. You don't need to write templates to use these routes.
[Install HAPTIC](getting-started.md#install-with-helm) first, or explore the
live examples in the guides without a cluster.

## Choose how to define routes

| Your setup | Start here |
| --- | --- |
| You use Ingress resources for host and path routing | [Ingress routing](libraries/ingress.md) |
| You use Gateway API, with listeners and routes managed separately | [Gateway API walkthrough](gateway-api.md) |
| You need a specific Gateway route type, filter, or policy | [Gateway API reference](libraries/gateway.md) |
| You are replacing another ingress controller | [Migration guide](migrating.md) |
| Your routing model uses a custom resource | [Write templates](templating.md) and [watch your resources](watching-resources.md) |

The default installation creates the `haptic` IngressClass and, when the
Gateway API CRDs are available, the `haptic` GatewayClass. Select HAPTIC with
`spec.ingressClassName: haptic` on an Ingress or `spec.gatewayClassName: haptic`
on a Gateway. Application routes then attach to that Gateway.

## Configure traffic behavior

| Task | Guide |
| --- | --- |
| Serve HTTPS with your certificates | [TLS certificates](ssl-certificates.md) |
| Change Ingress timeouts, headers, redirects, or load balancing | [Ingress annotations](annotations.md) and [native annotation reference](libraries/haptic-annotations.md) |
| Authenticate Gateway requests, set rate limits, or select a firewall policy | [Gateway route policies](operations/gateway-policies.md) |
| Reuse web application firewall (WAF) rules across applications | [WAF policies](operations/waf-policies.md) |
| Cache repeated responses | [Response caching](operations/response-cache.md) |
| Use external authentication, request inspection, or traffic mirroring | [Traffic processing plugins](operations/spoa-hub.md) |
| Use SPIFFE identities for backend TLS | [SPIFFE/SPIRE mTLS](operations/spiffe-mtls.md) |

For Ingress authentication, rate limits, and request validation, use the
[native annotations](libraries/haptic-annotations.md). Gateway API extensions
use [HAProxyRoutePolicy](route-policy.md) resources. Read each guide's prerequisites
before applying a policy; some features require an optional service or plugin.

## Check a route

Send a request with the hostname and path your route matches. If it doesn't
reach the expected application, follow [routing troubleshooting](troubleshooting.md#routing-issues).
Use [access logs](operations/access-logging.md) to identify the selected route,
backend, and any policy that denied the request.

To change which resources an installation handles, see [IngressClass settings](ingress-class.md)
or [GatewayClass settings](gateway-class.md). For behavior the bundled settings
don't cover, [extend a template](templating.md).
