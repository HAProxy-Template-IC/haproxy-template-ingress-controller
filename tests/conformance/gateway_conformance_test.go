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
	rt, err := newNodePortRoundTripper(timeoutCfg, debug)
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
			// (none yet — populate as conformance reveals genuine gaps)
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
	addresses, found, _ := unstructuredNestedSlice(pool.Object, "spec", "addresses")
	usableValue := "192.0.2.10"
	if found && len(addresses) > 0 {
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

// unstructuredNestedSlice lifts nested string-slice access from
// unstructured.Unstructured without introducing a hard dep on the
// helper package. Returns (slice, found, error-not-applicable).
func unstructuredNestedSlice(obj map[string]any, fields ...string) ([]string, bool, error) {
	cur := any(obj)
	for _, f := range fields {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false, nil
		}
		cur, ok = m[f]
		if !ok {
			return nil, false, nil
		}
	}
	raw, ok := cur.([]any)
	if !ok {
		return nil, false, nil
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		s, ok := e.(string)
		if !ok {
			continue
		}
		out = append(out, s)
	}
	return out, true, nil
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
