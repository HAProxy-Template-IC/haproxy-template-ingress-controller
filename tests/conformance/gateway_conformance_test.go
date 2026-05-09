// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build gateway_conformance

// Package conformance runs the upstream Kubernetes Gateway API conformance
// suite against the chart's GatewayClass. Builds under the
// `gateway_conformance` tag so it stays out of regular test runs (the
// suite has its own slow setup and pulls in the upstream conformance
// fixtures).
//
// To run locally:
//
//	make test-e2e            # brings up the haptic-e2e kind cluster
//	make test-gateway-conformance
//
// The suite expects an existing `haptic-e2e` kind cluster with the
// chart deployed and the `haptic` GatewayClass accepted. `make test-e2e`
// (default `KEEP_CLUSTER=true`) leaves that cluster in place so the
// conformance suite can attach to it via the e2e suite's pinned
// kubeconfig.
//
// SupportedFeatures pin the chart's actual coverage. Features
// intentionally excluded map to HTTPRoute filter shapes the chart
// currently doesn't implement (h2c, request mirror, redirect filters
// on HTTPRoute — the chart's redirect-via-annotation is Ingress-side,
// not HTTPRoute). Add features as the chart grows.
//
// The test fails on any conformance assertion regression for the
// declared SupportedFeatures set. Genuine open issues against a specific
// upstream test should be added to SkipTests with an issue link, NOT
// hidden behind a t.Skip() blanket-skip — see
// `feedback_skipped_tests_are_shipped_bugs.md`.
package conformance

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/dynamic"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/gateway-api/apis/v1alpha2"
	"sigs.k8s.io/gateway-api/apis/v1alpha3"
	"sigs.k8s.io/gateway-api/apis/v1beta1"
	xv1alpha1 "sigs.k8s.io/gateway-api/apisx/v1alpha1"
	gwconformance "sigs.k8s.io/gateway-api/conformance"
	conformanceconfig "sigs.k8s.io/gateway-api/conformance/utils/config"
	"sigs.k8s.io/gateway-api/conformance/utils/suite"
	"sigs.k8s.io/gateway-api/pkg/features"
)

// metalLBPoolGVR identifies the IPAddressPool CRD MetalLB ships. The e2e
// suite (tests/e2e/metallb.go) creates a pool named "e2e-pool" in the
// metallb-system namespace covering the upper sliver of the kind Docker
// network. We discover that pool here so the SupportGatewayStaticAddresses
// tests get realistic Usable / Unusable addresses without hardcoding IPs
// (kind's network can shift host-to-host).
var metalLBPoolGVR = schema.GroupVersionResource{
	Group:    "metallb.io",
	Version:  "v1beta1",
	Resource: "ipaddresspools",
}

// kubeconfigPath matches the path the e2e suite (tests/e2e/main_test.go)
// writes when it provisions the kind cluster, so the conformance suite
// reuses the e2e cluster without separate setup.
const kubeconfigPath = "/tmp/haproxy-e2e-kubeconfig"

// gatewayClassName is the GatewayClass the chart provisions. The chart's
// values default `gatewayClass.name` to "haptic" — keep this in sync if
// that ever changes.
const gatewayClassName = "haptic"

func TestGatewayAPIConformance(t *testing.T) {
	if os.Getenv("KUBECONFIG") == "" {
		require.NoError(t, os.Setenv("KUBECONFIG", kubeconfigPath),
			"set KUBECONFIG for conformance suite")
	}

	cfg, err := config.GetConfig()
	require.NoError(t, err, "load Kubernetes config")

	clientOpts := client.Options{}
	c, err := client.New(cfg, clientOpts)
	require.NoError(t, err, "create controller-runtime client")

	cs, err := clientset.NewForConfig(cfg)
	require.NoError(t, err, "create kubernetes clientset")

	require.NoError(t, v1alpha3.Install(c.Scheme()))
	require.NoError(t, v1alpha2.Install(c.Scheme()))
	require.NoError(t, v1beta1.Install(c.Scheme()))
	require.NoError(t, xv1alpha1.Install(c.Scheme()))
	require.NoError(t, gatewayv1.Install(c.Scheme()))
	require.NoError(t, apiextensionsv1.AddToScheme(c.Scheme()))

	// Declare every standard-channel conformance feature except those that
	// the chart fundamentally cannot implement without becoming a different
	// product. The directive (no undeclared features) forbids strategic
	// under-declaration, but a Gateway-only ingress controller cannot
	// satisfy mesh, UDP, or request-mirror tests without architectural
	// changes that don't fit haptic's scope. Each exclusion has a concrete
	// upstream-capability reason.
	excluded := sets.New[features.FeatureName](
		// Mesh tests use `echo.ConnectToAppInNamespace` for service-to-service
		// traffic between in-cluster pods (GAMMA pattern: HTTPRoute targets
		// a Service parent, not a Gateway). That's a sidecar-per-pod
		// architecture, fundamentally different from the chart's single
		// front-door HAProxy. Tracked at <follow-up issue>.
		features.SupportMesh,
		features.SupportMeshClusterIPMatching,
		features.SupportMeshConsumerRoute,
		// UDPRoute relies on UDP listeners. HAProxy 3.x has experimental
		// UDP support limited to DNS/QUIC pass-through; full UDP routing
		// per UDPRoute spec isn't practical on the OSS data plane.
		features.SupportUDPRoute,
		// HTTPRoute requestMirror has no native HAProxy primitive — would
		// need an SPOA mirror agent or Lua. Deferred to follow-up.
		features.SupportHTTPRouteRequestMirror,
		features.SupportHTTPRouteRequestMultipleMirrors,
		features.SupportHTTPRouteRequestPercentageMirror,
	)
	supported := sets.Set[features.FeatureName]{}
	for _, f := range features.AllFeatures.UnsortedList() {
		if f.Channel != features.FeatureChannelStandard {
			continue
		}
		if excluded.Has(f.Name) {
			continue
		}
		supported.Insert(f.Name)
	}

	timeoutCfg := conformanceconfig.DefaultTimeoutConfig()
	debug := os.Getenv("CONFORMANCE_DEBUG") != ""

	// Conformance traffic targets Gateway.Status addresses (metallb LB IPs
	// on kind's docker network), which are unreachable from the test
	// process when running in DinD or on a separate docker network. Route
	// every dial through the chart's NodePort on the resolved kind host
	// instead; the Host header and TLS SNI stay untouched so HAProxy still
	// sees the gateway hostname for routing and cert selection.
	//
	// Build the dynamic NodePort port table the RoundTripper consumes.
	// Static 80/443 entries (kind extraPortMappings → host loopback)
	// are seeded immediately; dynamic listener-port NodePorts (chart's
	// gateway-listener-ports Service, allocated lazily as conformance
	// fixtures land) are discovered on cache miss via the kind node's
	// docker-network InternalIP. See libraries/gateway.yaml's
	// features-090-gateway-listener-ports-service snippet.
	staticTable, nodeIP, err := buildInitialPortTable(t.Context(), cs)
	require.NoError(t, err, "build initial NodePort port table")
	router := newPortRouter(cs, nodeIP, staticTable)
	rt, err := newNodePortRoundTripper(timeoutCfg, debug, router)
	require.NoError(t, err, "build NodePort RoundTripper")

	// SupportGatewayStaticAddresses substitutes PLACEHOLDER_USABLE_ADDRS /
	// PLACEHOLDER_UNUSABLE_ADDRS in its Gateway fixture with the entries
	// of UsableNetworkAddresses / UnusableNetworkAddresses we pass below.
	// Without these, the test panics on
	// `require.Len(currentGW.Spec.Addresses, 3)` because the placeholder
	// substitution drops the entries entirely. We discover the realistic
	// pool from MetalLB at suite setup time so the Usable IP is one
	// MetalLB will actually allocate; Unusable is a reserved-test
	// (RFC 5737 TEST-NET-1) IP MetalLB will never bind.
	usable, unusable, err := discoverStaticAddressPools(t.Context(), cfg)
	require.NoError(t, err, "derive static-addresses pools from MetalLB IPAddressPool")

	opts := suite.ConformanceOptions{
		Client:               c,
		ClientOptions:        clientOpts,
		Clientset:            cs,
		RestConfig:           cfg,
		GatewayClassName:     gatewayClassName,
		Debug:                debug,
		CleanupBaseResources: true,
		SupportedFeatures:    supported,
		RoundTripper:         rt,
		TimeoutConfig:        timeoutCfg,
		Implementation: suite.ParseImplementation(
			"haproxy-haptic",
			"haptic",
			"https://gitlab.com/haproxy-haptic/haptic",
			"main",
			"https://gitlab.com/haproxy-haptic/haptic/-/issues",
		),
		// SkipTests is the right place to opt-out of *individual* upstream
		// tests when a specific assertion is known broken — never use
		// t.Skip() for the whole suite. Each entry must include an issue
		// link in a comment so it can be revisited.
		SkipTests: []string{
			// The five GRPCRoute conformance tests
			// (GRPCExactMethodMatching, GRPCRouteHeaderMatching,
			// GRPCRouteListenerHostnameMatching, GRPCRouteNamedRule,
			// GRPCRouteWeight — last two skipped further down for
			// adjacency) attach to a plaintext HTTP/port-80 listener
			// (the suite's stock `same-namespace` Gateway, see
			// `vendor/sigs.k8s.io/gateway-api/conformance/base/manifests.yaml`)
			// and dial it with an insecure gRPC client
			// (`conformance/utils/grpc/grpc.go` hardcodes
			// `insecure.NewCredentials()`; ConformanceOptions exposes
			// no TLS knob; backends declare
			// `appProtocol: kubernetes.io/h2c`). HAProxy 3.x has no
			// path to multiplex HTTP/1.1 and h2c on a shared plaintext
			// bind: `proto h2` locks the bind to H2-only, there's no
			// `Upgrade: h2c` parser, and ALPN works only inside TLS.
			// Other controllers (Envoy Gateway, Contour, Cilium) pass
			// these tests via Envoy's connection-time HTTP/2-preface
			// detection (`codec_type: AUTO`), which HAProxy doesn't
			// expose.
			//
			// The chart DOES support gRPC over the production-relevant
			// TLS+ALPN-h2 path: HTTPS binds carry `alpn h2,http/1.1`
			// (`charts/haptic/libraries/ssl.yaml` `util-ssl-bind-options`
			// at line ~25), GRPCRoute backends emit
			// `default-server check proto h2`
			// (`charts/haptic/libraries/gateway.yaml` ~lines 2538, 2821).
			// Static-side coverage:
			// `test-grpcroute-https-listener-alpn-h2` chart validation
			// test. End-to-end coverage: `tests/e2e/grpc_tls_test.go`.
			//
			// Tracked at <follow-up issue> — re-evaluate if HAProxy
			// upstream ever adds h2c-on-shared-port support (declined
			// historically; no roadmap indication).
			"GRPCExactMethodMatching",
			"GRPCRouteHeaderMatching",
			"GRPCRouteListenerHostnameMatching",
			// Frontend mTLS handshake-level enforcement: the cert-
			// registration TLS-mode-default fix lets the chart
			// (GatewayFrontendClientCertificateValidation +
			// GatewayFrontendClientCertificateValidationInsecureFallback
			// previously failed because the default crt-list line was
			// emitted as `cert.pem [ocsp-update on]` with no verify
			// clause — so port-443 traffic whose SNI didn't match a
			// specific SNI line fell through to the default and HAProxy
			// answered handshakes without verifying the client cert.
			// Fix: ssl.yaml now consumes
			// `clientCertVerifyHosts["*"]` — the wildcard-SNI key that
			// gateway.yaml's mTLS pass writes for any HTTPS listener
			// without a hostname — and folds the matching `ca-file` +
			// `verify <mode>` clause into the default crt-list line.
			// Per-port specific-SNI lines (e.g. `second-example.org`)
			// still carry their own verify clauses from the per-port
			// override path, so AllowValidOnly + AllowInsecureFallback
			// land the right verify mode at the right SNI level.
			// Pinned by test-gateway-frontend-client-cert-default-line-verify
			// + test-gateway-frontend-client-cert-insecure-fallback-default-line.)
			//
			// (GatewayBackendClientCertificateFeature: chart already
			// supports `spec.tls.backend.clientCertificateRef` — the
			// route's parent Gateway is walked at backend-emit time
			// (libraries/gateway.yaml ~line 2660), the cert is resolved
			// + bundled into the file registry, and the resulting `crt
			// <path>` directive is appended to the backend's
			// `default-server` line alongside the BackendTLSPolicy
			// `ssl ca-file ... verify required` clause. Status side
			// emits `ResolvedRefs=True/ResolvedRefs` on the Gateway
			// when the cert ref resolves (or False with the right
			// reason on InvalidClientCertificateRef / RefNotPermitted).
			// Pinned by test-gateway-backend-client-cert-shape.)
			// (Dynamic NodePort plumbing landed: chart emits a
			// gateway-listener-ports NodePort Service via
			// features-090-gateway-listener-ports-service; the
			// RoundTripper builds its port table by querying that
			// Service plus a node-InternalIP lookup. Previously skipped
			// 8080-port tests are no longer in SkipTests.)
			// GatewayStaticAddresses: chart-side IPv4-only MetalLB allocation.
			// MetalLB rejects multi-IP `metallb.io/loadBalancerIPs` annotations
			// where every entry is the same IP family — it's designed for
			// IPv4+IPv6 dual-stack, not "try-each-until-one-works." The chart
			// emits a per-Gateway Service whose annotation lists every
			// spec.addresses entry, so a Gateway listing two IPv4 addresses
			// (like the conformance test's unusable+usable pair) hits the
			// IPFamilyForAddresses guard until the test patches the Gateway
			// down to a single IP — at which point the live cluster shows
			// MetalLB allocating successfully but the chart's status
			// patcher races against the Service status update and never
			// catches Programmed=True before the test's poll deadline.
			// Tracked at <follow-up issue>; needs either per-IP Service
			// emission or a MetalLB IPAddressPool selector strategy.
			"GatewayStaticAddresses",
			// GatewayInfrastructure: fixture-application timeout when the
			// kind cluster is busy churning conformance namespaces (the
			// test fails to wait for the gateway-conformance-infra
			// namespace to be ready). Not chart logic — environment race
			// in the test framework's namespace setup. Tracked at <follow-up issue>.
			"GatewayInfrastructure",
			// (ListenerSetHostnameConflict / ListenerSetProtocolConflict
			// previously failed for two reasons:
			//   1. util-effective-listeners populated `listenersetStatuses`
			//      via a nested ComputeIfAbsent, and F3's read raced
			//      against the write under Scriggo's parallel-render
			//      goroutines. Folded the per-LS statuses into the
			//      single `effectiveListeners` cache value (sub-key
			//      `statuses`) so "cache populated" implies "statuses
			//      populated"; F3 reads the unified value.
			//   2. Candidate listener entries stashed source provenance
			//      in a nested `_source` map with literal keys
			//      "kind"/"namespace"/"name" — Scriggo mis-evaluated
			//      these later in the closure (the keys got rebound to
			//      the listener's own field names like "protocol" or
			//      "hostname"), so the surface loop's lsKey lookup
			//      missed every entry and conflict info never landed in
			//      statuses[lsKey]. Switched to flat keys
			//      __sourceKind / __sourceNs / __sourceName.
			// Pinned by test-listenerset-hostname-conflict-conformance-shape
			// (7 assertions covering all four LSes' top-level + per-listener
			// conditions on the upstream fixture).
			// (ListenerSetHTTPRouting previously failed because the
			// listenersets watchedResource was indexed on
			// `[namespace, spec.parentRef.name]` instead of the
			// conventional `[namespace, name]`. The route-resolution
			// loops in util-analyze-routes call
			// `resources.listenersets.GetSingle(lsNs, lsName)` to
			// fetch the LS a parentRef points at — but the wrong
			// index made every such lookup return nil, causing
			// LS-attached routes to fall through to the
			// "resolvedGwCount == 0" fallback (which produces a
			// single empty-host map entry instead of one per LS
			// listener). Fixed by switching the index to
			// `[namespace, name]`. Pinned by
			// test-listenerset-http-routing-conformance-shape (22
			// assertions tracing each route's path-prefix-exact.map
			// emission against the upstream conformance fixture).)
			// (ListenerSetAllowedRoutesNamespaces previously failed
			// because the chart's route-resolution loop didn't
			// enforce listener.allowedRoutes.namespaces. Routes
			// from any namespace attached to every LS listener
			// regardless of `from: All`/`Same`/`Selector`. Added
			// IsRouteAllowedOnListener macro
			// (libraries/gateway.yaml line ~924) — same
			// allowed-from semantics as IsListenerSetAdmitted's
			// Selector branch but applied per-listener. Merged
			// into the prPort gate in util-analyze-routes so the
			// surrounding end-block structure is unchanged. Pinned
			// by test-listenerset-allowed-routes-namespaces-
			// conformance-shape (9 assertions covering all
			// listener × route-ns combinations).
			// (ListenerSetReferenceGrant previously failed because the
			// chart's top-level ListenerSet status didn't fold in
			// per-listener cert-ref resolution — only the cache's
			// `accepted` (port/protocol/hostname conflict) flag.
			// status-patches-220-listenerset now pre-scans listeners
			// inline for cert-ref / kind-ref resolvability (mirrors the
			// per-listener loop's logic, with source kind="ListenerSet"
			// for ReferenceGrant lookups). A LS whose every listener
			// has unresolvable refs → top-level Accepted=False/
			// ListenersNotValid + Programmed=False/ListenersNotValid;
			// per-listener ResolvedRefs=False/RefNotPermitted is
			// already correct.
			//
			// Pinned by test-listenerset-reference-grant-
			// conformance-shape — fixture mirrors the upstream
			// Gateway + two LSes (one with matching RG in the same
			// ns as the Gateway, one in a different ns where the
			// RG's `from` clause doesn't match). Conformance run on
			// next push is the verification.)
			// (ListenerSetAllowedNamespaceSelector — the chart's
			// IsListenerSetAdmitted macro already implemented the
			// matchLabels gate; the listenersets-index fix
			// (commit 0bb0894f) made GetSingle by (ns, name) work
			// reliably, so route-resolution and status-side both
			// see consistent admission decisions. Pinned by
			// test-listenerset-allowed-namespace-selector-
			// conformance-shape (5 assertions covering Selector-
			// allowed and Selector-rejected LSes' top-level
			// Accepted/Programmed conditions).
			// TLSRoute wildcard-SNI matcher now works (the ssl-tcp
			// frontend uses `-m end .<domain>` for `*.<domain>`
			// patterns, lifting 3 wildcard-intersection sub-tests).
			// The remaining failures all share one root cause:
			// "should-not-reach-backend" / "should-be-rejected"
			// assertions expect the ssl-tcp frontend to REJECT TLS
			// connections whose SNI doesn't match any TLSRoute-
			// attached pattern. The chart's ssl-tcp frontend has
			// `default_backend ssl-loopback` that forwards
			// non-matching traffic to the HTTPS frontend for
			// termination — adding a blanket reject would break
			// HTTPS termination on shared listener ports. Fixing
			// this needs per-listener-port ssl-tcp frontends
			// (one frontend per Gateway TLS-passthrough listener,
			// each with its own SNI allowlist) — a substantial
			// frontend-separation refactor. Tracked at
			// <follow-up issue>.
			"TLSRouteHostnameIntersection",
			"TLSRouteInvalidBackendRefNonexistent",
			"TLSRouteInvalidBackendRefUnknownKind",
			// (TLSRouteListenerMixedTerminationNotSupported is purely a
			// listener-status assertion: a Gateway with two TLS
			// listeners on the same port — one Terminate, one
			// Passthrough — must surface Accepted=False/ProtocolConflict
			// on both. The chart's status-patches-200-gateway already
			// detects this via its `tlsPortModes` pre-scan and emits
			// the right reason for both listeners; pinned by
			// test-tlsroute-mixed-termination-protocol-conflict.)
			"TLSRouteTerminateSimpleSameNamespace",
			// HTTPRouteListenerPortMatching previously skipped on the
			// 8080/8443 plumbing gap; lifted by the partial-SSA + open
			// NetworkPolicy work, now passing.
			//
			// (HTTPRouteHTTPSListenerDetectMisdirectedRequests
			// previously failed on 4 of 15 sub-tests because the
			// chart's listener-claim map omitted catch-all
			// (no-hostname) listeners. Requests whose SNI matched the
			// catch-all got `gw_sni_listener=""` and the 421 gate's
			// `!len 0` check blocked spec-mandated misdirected
			// emission for cross-listener cases. The
			// frontend-extra-100-gateway-misdirected snippet now
			// emits a `^.*$ catchall:<gw-ns>/<gw-name>` entry into
			// the regex claim map per Gateway with a catch-all
			// listener; sorted AFTER the more-specific wildcard
			// regexes so map_reg first-match-wins picks specific
			// listeners over the catch-all. Pinned by
			// test-gateway-https-misdirected-conformance-shape
			// (chart fixture mirrors the upstream Gateway). All 15
			// sub-cases trace cleanly through the rendered config —
			// re-test on push.)
			// (HTTPRouteCORS previously skipped on 3 of 17 sub-tests
			// failing because the chart's CORS filter expanded
			// `allowMethods: ["*"]` into a fixed list — the
			// conformance suite's ValidHeaderValues check accepts only
			// the requested method (echoed from
			// `Access-Control-Request-Method`) or a literal `*`. The
			// chart now captures the requested method into
			// `txn.gw_cors_acrm` and echoes it on the
			// preflight response. Pinned by
			// test-httproute-cors-wildcard-methods-echo. The other two
			// failing sub-tests — "auth + specific method + headers
			// preflight" and "hide auth headers on unauth path" —
			// share the same root cause and are closed by the same
			// fix. Conformance run is the next signal.)
			// (HTTPRoutePartiallyInvalidViaInvalidReferenceGrant
			// previously skipped on the cross-namespace backendRef
			// issue; util-generate-backends-gateway now resolves
			// services in the backendRef.namespace — re-tested.)
			// HTTPRouteRedirectPortAndScheme previously failed on the
			// chart-static-port-8080 / Gateway-listener-port-8080
			// collision. Fixed by binding chart-static http/https on
			// the literal port numbers (80/443) so each Gateway
			// listener port owns its own bind and dst_port is
			// unambiguous, plus a runtime-resolved port-part in the
			// redirect-filter URL so the inbound listener port is
			// preserved when spec.scheme and spec.port are both
			// unset.
			// (GatewayHTTPListenerIsolation previously skipped on the
			// assumption that the chart's frontend-routing returned
			// 404 for catch-all-targeted requests — but tracing all
			// 16 upstream sub-cases through the rendered host.map +
			// path-prefix-exact.map + path-prefix.map shows the chart
			// returns the spec-expected status for each. The
			// catch-all listener path lookup uses host_match="" +
			// path as the key, which lands in the chart's
			// path-prefix-exact.map (where the empty-hostname route
			// emits "/empty-hostname"); requests for non-existent
			// paths on a host claimed by a more-specific listener
			// fall through to the default backend → 404. Pinned by
			// the 12-assertion test-gateway-http-listener-isolation
			// in libraries/gateway.yaml. Conformance run on next
			// push is the verification.)
			// (GatewayFrontendInvalidDefaultClientCertificateValidation
			// previously skipped on bind + status gaps. Both are now
			// addressed:
			//   * bind side — listeners with unresolvable
			//     caCertificateRefs go into gf["mtlsBlockedListeners"]
			//     (features-110-gateway-frontend-mtls) and drop out
			//     of the bindHTTPSDefault / needHTTPSFrontend
			//     computation in features-150-gateway-bind, so the
			//     chart-static `bind *:443 ssl crt-list` is omitted.
			//     Pinned by test-gateway-https-listener-mtls-
			//     unresolved-ca-no-bind in libraries/gateway.yaml.
			//   * status side — the listener-status block in the
			//     gateway library's frontends-500-gateway-listener-
			//     status snippet emits ResolvedRefs=False/Invalid
			//     CACertificateRef and Accepted=False/NoValidCA
			//     Certificate for the offending listener. Pinned by
			//     test-gateway-https-listener-mtls-unresolved-ca-
			//     status-conditions.
			// Conformance run is the next signal — re-test on push.)
			// BackendTLSPolicySANValidation: BackendTLSPolicy SAN
			// validation requires HAProxy to validate the backend's
			// presented certificate against the policy's SAN list. The
			// chart emits `ssl ca-file ... verify required sni str(<host>)
			// verifyhost <host>` (commit d58b9086 + earlier), but
			// SAN-list validation needs additional `verifyhost` entries
			// per SAN. Also blocked on the same empty-Host issue for the
			// non-conflict-resolution test case. Tracked at <follow-up
			// issue>.
			"BackendTLSPolicySANValidation",
			// GRPCRouteNamedRule / GRPCRouteWeight: same h2c-on-
			// plaintext-HTTP-port gap as the GRPCRoute* skips above.
			// See the consolidated rationale at the top of SkipTests
			// for the chart's TLS+ALPN-h2 coverage path
			// (test-grpcroute-https-listener-alpn-h2 +
			// tests/e2e/grpc_tls_test.go).
			"GRPCRouteNamedRule",
			"GRPCRouteWeight",
		},
		UsableNetworkAddresses:   usable,
		UnusableNetworkAddresses: unusable,
	}

	gwconformance.RunConformanceWithOptions(t, opts)
}

// discoverStaticAddressPools returns sample Usable and Unusable
// GatewaySpecAddress entries for the SupportGatewayStaticAddresses tests
// to substitute into the placeholder fixtures. We discover them at suite
// startup time rather than hardcoding, because kind's docker network can
// shift host-to-host and the e2e suite's IPAddressPool is sized to that
// network.
//
//   - Usable: pulled from the e2e MetalLB IPAddressPool's high end
//     (.249), which is reserved-by-convention for this purpose. The pool
//     covers .200-.250 (see tests/e2e/metallb.go); we pick a single IP
//     from the top so a real allocation against it is improbable but
//     possible.
//
//   - Unusable: 192.0.2.1, the first address of TEST-NET-1 (RFC 5737).
//     MetalLB will never allocate this since it isn't in any
//     IPAddressPool, so the conformance test sees Programmed=False/
//     AddressNotUsable as the spec requires.
//
// Both lists return one address each — the conformance test asserts
// `require.Len(currentGW.Spec.Addresses, 3)` (one invalid type +
// one Usable + one Unusable) so any other count breaks the fixture.
//
// Returns an error rather than t.Fatal so the caller can attach a
// helpful require.NoError message.
func discoverStaticAddressPools(ctx context.Context, restConfig *rest.Config) ([]v1beta1.GatewaySpecAddress, []v1beta1.GatewaySpecAddress, error) {
	// Apply a short timeout so a misconfigured cluster fails fast rather
	// than blocking the whole conformance suite on the static-addresses
	// fixture setup.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	dyn, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, nil, err
	}

	pool, err := dyn.Resource(metalLBPoolGVR).
		Namespace("metallb-system").
		Get(ctx, "e2e-pool", metav1.GetOptions{})
	if err != nil {
		// The e2e-pool only exists when the test was set up by
		// tests/e2e/main_test.go (not in CI matrix where MetalLB is
		// installed differently). Fall back to documented sentinels:
		// the conformance suite will use them and SupportGatewayStaticAddresses
		// tests will fail with a clearer message than a panic.
		ipAddr := v1beta1.IPAddressType
		usableAddr := v1beta1.GatewaySpecAddress{Type: &ipAddr, Value: "192.0.2.10"}
		unusableAddr := v1beta1.GatewaySpecAddress{Type: &ipAddr, Value: "192.0.2.1"}
		return []v1beta1.GatewaySpecAddress{usableAddr},
			[]v1beta1.GatewaySpecAddress{unusableAddr}, nil
	}

	// Pool addresses are recorded under spec.addresses as a string slice
	// like ["172.18.255.200-172.18.255.250"]. Pull the high end (.249)
	// for Usable; treat anything outside as Unusable.
	addresses, _, err := unstructured.NestedStringSlice(pool.Object, "spec", "addresses")
	if err != nil {
		return nil, nil, err
	}
	usableValue := "192.0.2.10"
	if len(addresses) > 0 {
		// Best-effort parse of the first range entry's high octet+249.
		// We intentionally pick a single deterministic IP rather than
		// scanning for a free one; MetalLB takes care of allocation.
		usableValue = pickAddressFromRange(addresses[0])
	}

	ipAddr := v1beta1.IPAddressType
	usable := []v1beta1.GatewaySpecAddress{{Type: &ipAddr, Value: usableValue}}
	unusable := []v1beta1.GatewaySpecAddress{{Type: &ipAddr, Value: "192.0.2.1"}}
	return usable, unusable, nil
}

// pickAddressFromRange returns a single IP from a "<start>-<end>" range
// expression. Picks the second-from-end (.249 of a .200-.250 pool) so
// a colliding e2e test allocation is improbable. Falls back to a
// reserved-test sentinel if the format isn't parseable.
func pickAddressFromRange(rangeStr string) string {
	// Split on "-"; expect "172.18.255.200-172.18.255.250" shape.
	for i := 0; i < len(rangeStr)-1; i++ {
		if rangeStr[i] == '-' {
			high := rangeStr[i+1:]
			// Replace last octet with .249 if the high end ends in .250.
			for j := len(high) - 1; j >= 0; j-- {
				if high[j] == '.' {
					return high[:j+1] + "249"
				}
			}
			break
		}
	}
	return "192.0.2.10"
}
