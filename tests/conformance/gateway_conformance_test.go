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
// Execution model: the test binary runs as a sibling container on the
// kind docker network (see `make test-conformance` / Dockerfile.
// conformance-test). Inside that container, MetalLB-allocated LoadBalancer
// IPs from Gateway.Status.Addresses are directly routable — the test
// dials them with the stock upstream RoundTripper + gRPC client, no
// NodePort tunneling and no DinD-aware dialer required. Same code path
// in CI (sibling container under the DinD daemon) and on a developer
// laptop (sibling container under the host docker daemon).
//
// To run locally:
//
//	make test-e2e            # brings up the haptic-e2e kind cluster
//	make test-conformance    # builds the test image, runs it as a sibling container
//
// The suite expects an existing `haptic-e2e` kind cluster with the chart
// deployed and the `haptic` GatewayClass accepted. `make test-e2e`
// (default `KEEP_CLUSTER=true`) leaves that cluster in place so the
// conformance container can attach to its kube apiserver via the kind
// docker-DNS hostname (e.g. `https://haptic-e2e-control-plane:6443`).
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
	"sigs.k8s.io/gateway-api/conformance/utils/roundtripper"
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

// gatewayClassName is the GatewayClass the chart provisions. The chart's
// values default `gatewayClass.name` to "haptic" — keep this in sync if
// that ever changes.
const gatewayClassName = "haptic"

func TestGatewayAPIConformance(t *testing.T) {
	// KUBECONFIG must be provided by the caller. When run as a sibling
	// container via `make test-conformance`, the kubeconfig is mounted
	// at /etc/kubeconfig and the env var is set on the docker run command.
	require.NotEmpty(t, os.Getenv("KUBECONFIG"),
		"KUBECONFIG must point at the haptic-e2e cluster's kubeconfig")

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
		// Mesh / GAMMA features are permanently out of scope for haptic.
		// The conformance tests exercise pod-to-pod east-west traffic
		// (`echo.ConnectToApp` / `ConnectToAppInNamespace`) that never
		// traverses the chart's edge HAProxy: HTTPRoutes attach to a
		// Service `parentRef`, ClusterIPMatching requires seeing the
		// original destination IP before NAT, and ConsumerRoute
		// dispatches by the source pod's namespace. All three assume a
		// sidecar-per-pod (or kernel-level interceptor) data plane.
		// haptic is a single front-door HAProxy by design; closing
		// these would mean shipping a different product. Not deferred
		// — abandoned.
		features.SupportMesh,
		features.SupportMeshClusterIPMatching,
		features.SupportMeshConsumerRoute,
		// UDPRoute relies on UDP listeners and backend forwarding.
		// HAProxy OSS has no UDP load balancing — `udp@` listeners
		// exist but are restricted to `log-forward` sections (syslog
		// ingress), QUIC listeners terminate HTTP/3 (not arbitrary
		// UDP), and the resolvers subsystem is outbound DNS only.
		// There is no `mode udp` and no way to forward datagrams to
		// upstream UDP servers. Full UDP routing is a HAPEE-only
		// feature (the `hapee-lb-udp` enterprise module), out of
		// scope for an OSS chart.
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

	// Upstream defaults are sized for "any compliant implementation,
	// even a slow one" — RequestTimeout=10s with 3 consecutive successes
	// and MaxTimeToConsistency=30s adds up to 30+s per failing sub-test,
	// and GatewayMustHaveAddress=180s lets a doomed test linger for 3
	// minutes before giving up. On our chart Gateways are Programmed in
	// ~1-2s and HTTP requests complete in <1s; the only paths that
	// approach the upstream limits are the failure paths. Tighten so
	// fail-mode shards complete in single-digit minutes instead of
	// 12+ minutes (each TLS / HTTPS sub-test that can't reach a backend
	// burns RequestTimeout × RequiredConsecutiveSuccesses worth of
	// budget before the test framework concludes failure).
	//
	// We're aggressive on retry / consistency budgets (5s where upstream
	// says 30s) and modest on Kubernetes-object timeouts (GetTimeout etc.
	// stay at upstream defaults — those are reads against the apiserver,
	// not the chart's data plane, and we don't gain by tightening them).
	timeoutCfg := conformanceconfig.DefaultTimeoutConfig()
	// 10s is the contract ceiling — haptic must complete reconcile →
	// render → validate → deploy → HAProxy reload within this budget.
	// Anything longer is to be treated as a bug and fixed, not papered
	// over by raising the timeout (see CLAUDE-memory
	// feedback_no_blind_timeout_bumps). Tests that fail at 10s point at
	// genuine slowness on the chart or controller side.
	timeoutCfg.MaxTimeToConsistency = 10 * time.Second
	timeoutCfg.RequestTimeout = 10 * time.Second
	timeoutCfg.GatewayMustHaveAddress = 30 * time.Second
	timeoutCfg.GatewayMustHaveCondition = 30 * time.Second
	timeoutCfg.GatewayStatusMustHaveListeners = 30 * time.Second
	timeoutCfg.GatewayListenersMustHaveConditions = 30 * time.Second
	timeoutCfg.ListenerSetMustHaveCondition = 30 * time.Second
	timeoutCfg.ListenerSetListenersMustHaveConditions = 30 * time.Second
	timeoutCfg.HTTPRouteMustHaveCondition = 30 * time.Second
	timeoutCfg.TLSRouteMustHaveCondition = 30 * time.Second
	timeoutCfg.RouteMustHaveParents = 30 * time.Second
	timeoutCfg.NamespacesMustBeReady = 90 * time.Second
	timeoutCfg.LatestObservedGenerationSet = 20 * time.Second
	timeoutCfg.DefaultTestTimeout = 30 * time.Second
	debug := os.Getenv("CONFORMANCE_DEBUG") != ""

	// Sibling-container execution model: this binary runs on the kind
	// docker network, so Gateway.Status addresses (MetalLB LB IPs in
	// kind's network) are directly routable. The stock upstream
	// RoundTripper + gRPC client dial them verbatim — same code path as
	// any real client. No CustomDialContext, no NodePort tunneling, no
	// DinD remap. The `make test-conformance` Makefile target attaches
	// this container to the kind network (`docker run --network kind`),
	// which gives us identical behaviour locally and under GitLab DinD.
	rt := &roundtripper.DefaultRoundTripper{
		Debug:         debug,
		TimeoutConfig: timeoutCfg,
	}
	// Intentionally do NOT set GRPCClient on ConformanceOptions below.
	// Upstream PR #3130 (kubernetes-sigs/gateway-api#3130) makes
	// MakeRequestAndExpectEventuallyConsistentResponse create a fresh
	// *grpc.DefaultClient per call when the suite-level client is nil,
	// avoiding the race where one parallel subtest's `defer c.Close()`
	// tears down a *grpc.ClientConn while siblings are still mid-RPC
	// (upstream issue #3122). Envoy Gateway, Contour, Cilium, Traefik
	// all leave it nil for this reason.

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
		Client:        c,
		ClientOptions: clientOpts,
		Clientset:     cs,
		RestConfig:    cfg,
		GatewayClassName: gatewayClassName,
		Debug:            debug,
		// CleanupBaseResources=false leaves the conformance suite's
		// fixtures (HTTPRoutes, GRPCRoutes, backend Deployments,
		// reference Gateways…) in place after the suite exits, so
		// the after_script captures haproxy.cfg / kubectl get pods /
		// kubectl get httproutes.yaml with the *failing* route still
		// applied. With cleanup=true those artifacts are empty by the
		// time after_script runs, making any failure undiagnosable
		// from CI alone. The kind cluster is per-shard ephemeral so
		// leftover fixtures cost nothing.
		CleanupBaseResources: false,
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
			// (The five upstream GRPCRoute conformance tests
			// (GRPCExactMethodMatching, GRPCRouteHeaderMatching,
			// GRPCRouteListenerHostnameMatching, GRPCRouteNamedRule,
			// GRPCRouteWeight) previously failed because HAProxy 3.x
			// can't multiplex HTTP/1.1 and h2c on a shared plaintext
			// bind. The chart now does the multiplexing itself: a
			// `mode tcp` outer frontend (`frontend http-tcp`) inspects
			// the first 24 bytes of every connection on every HTTP
			// listener port; connections matching the HTTP/2
			// prior-knowledge preface
			// (`PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n`) route to a
			// unix-socket-bound `mode http` frontend with `proto h2`,
			// while HTTP/1.1 connections route to a sibling unix-socket
			// frontend in plain mode http. PROXY-protocol v2 across
			// the hop preserves the original client IP. Mirrors the
			// chart's existing ssl-tcp → ssl-loopback → https chain.
			// Pinned by test-grpcroute-h2c-on-port-80; full chart
			// suite green.)
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
			// (GatewayStaticAddresses previously failed because the
			// chart emitted ONE per-Gateway LoadBalancer Service whose
			// `metallb.universe.tf/loadBalancerIPs` annotation listed
			// every spec.addresses entry comma-separated. MetalLB
			// rejects multi-IPv4 annotations (its IPFamilyForAddresses
			// guard treats same-family multi-IP lists as
			// misconfiguration). The chart now emits ONE Service per
			// IP — each with its own single-IP annotation — so MetalLB
			// allocates each independently. The conformance test's
			// usable+unusable pair lands as: usable Service realized,
			// unusable Service unrealized, status patcher reports
			// Programmed=False/AddressNotUsable. After the test patches
			// out the unusable IP, only the usable Service remains and
			// Programmed flips to True.
			// Pinned by test-gateway-static-addresses-per-ip-services.
			// (GatewayInfrastructure previously failed because the
			// per-Gateway marker Service that carries
			// `spec.infrastructure.{labels,annotations}` landed in the
			// controller's namespace, but the conformance test searches
			// the Gateway's own namespace for a Service / Pod /
			// ServiceAccount labelled `gateway.networking.k8s.io/gateway-name`.
			// Closed by emitting the marker in the Gateway's namespace
			// directly: the chart's ClusterRole now includes cluster-wide
			// `services` write verbs whenever the gateway library is
			// enabled (templates/clusterrole.yaml), the resourceapplier
			// no longer enforces the same-namespace defense-in-depth
			// (RBAC alone gates), and the gateway library writes the
			// marker to `nsStr` rather than `ctrlNs`
			// (charts/haptic/libraries/gateway.yaml). Pinned chart-side
			// by `test-gateway-infrastructure-propagation-labels-only`,
			// `…-annotations-only`, `…-with-static-addresses`, and
			// `…-empty`.)
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
			// (TLSRoute frontend separation lands the architectural
			// foundation for the upstream TLSRoute conformance tests:
			//
			//   * applyListener (gateway.yaml) no longer folds
			//     TLS-Terminate listeners into bindHTTPSDefault —
			//     they always failed silently because the chart's
			//     mode-http HTTPS frontend can't serve L4 TCP.
			//   * util-build-ssl-passthrough (Pass 2) now also
			//     processes Terminate-mode listeners and tags every
			//     entry with port + mode + Gateway identity.
			//   * frontends-500-ssl-tcp (ssl.yaml) filters to
			//     mode=Passthrough — chart-static port keeps
			//     Ingress-passthrough + ssl-loopback fall-through
			//     for HTTPS termination.
			//   * frontends-600-gateway-tls-listener (new): one
			//     mode-tcp frontend per non-chart-static port.
			//     Terminate: `bind ... ssl crt-list ...` + ssl_fc_sni
			//     routing. Passthrough: plain bind + req_ssl_sni
			//     routing. Reject-default `tcp-request content reject`
			//     fires on unmatched SNIs (closes the Invalid*
			//     conformance assertions — Pass 2's ResolvedRefs
			//     gate filters routes with broken backends, so their
			//     SNI never enters the allowlist).
			//
			// Pinned by:
			//   * test-tlsroute-passthrough-nondefault-port-frontend
			//   * test-tlsroute-terminate-nondefault-port-frontend
			//   * test-tlsroute-invalid-backend-rejects-on-frontend
			//
			// (TLSRouteTerminateSimpleSameNamespace previously failed
			// because the fixture's listener uses port 8443 = the
			// chart's default httpsPort, and the chart-static
			// frontends would either bind that port too (collision)
			// or leave it unbound. The new
			// frontends-600-gateway-tls-listener now reads the
			// chart-static-bind state (bindHTTPSDefault + presence of
			// passthrough backends) and only skips httpsPort when
			// those flags would actually emit a chart-static bind.
			// When neither does — exactly the fixture's case
			// (TLS-Terminate listener alone, no Ingress TLS, no
			// HTTPS Gateway listener) — the new TLS frontend claims
			// httpsPort and terminates TLS there.
			// Pinned by
			// test-tlsroute-terminate-on-chart-static-httpsport.)
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
			// BackendTLSPolicySANValidation: HAProxy 3.x has no
			// built-in mechanism for multi-SAN OR matching or URI SAN
			// matching. The `verifyhost <name>` keyword accepts a
			// single hostname and follows RFC 6125 (DNS-only SAN
			// matching); URI SANs (SPIFFE-style identities) are
			// invisible to it. There is no `ssl_bc_*` fetcher that
			// returns the backend cert's SAN list, so post-handshake
			// ACL matching can't extract them either. Repeating
			// `verifyhost` (once per allowed SAN) is rejected by the
			// HAProxy parser. Multiple servers per backend with
			// different `verifyhost` values fail uniformly because
			// the same upstream cert can't satisfy disjoint SAN
			// expectations. CA-chain restriction doesn't apply — CAs
			// validate signatures, not SAN content.
			//
			// Closing this test would require either:
			//   * A SPOA-hub plugin that opens its own TLS probe to
			//     the backend, parses the cert SAN extension, and
			//     returns allow/deny via SPOE (multi-repo work in
			//     gitlab.com/haproxy-haptic/haproxy-spoa-hub plus
			//     chart-side wiring + per-request runtime cost), or
			//   * A native HAProxy fetcher exposing backend cert
			//     SANs (out of scope).
			//
			// The chart's existing `verifyhost <host>` from
			// BackendTLSPolicy.spec.validation.hostname covers the
			// single-hostname case correctly. The
			// `subjectAltNames[]` array is unused on the rendering
			// path — improving partial coverage by reading the
			// first DNS-type entry doesn't close the test (the URI
			// SAN sub-cases stay broken regardless).
			//
			// Tracked at <follow-up issue> — re-evaluate when (a)
			// HAProxy upstream adds a backend-cert-SAN fetcher, or
			// (b) we decide the SPOA-plugin runtime cost is worth
			// closing the conformance gap.
			"BackendTLSPolicySANValidation",
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
