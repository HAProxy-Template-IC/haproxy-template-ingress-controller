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
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	clientset "k8s.io/client-go/kubernetes"
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

	// Conservative SupportedFeatures pin: gateway, HTTPRoute core, plus
	// the matchers this chart genuinely implements. Excluded features
	// (h2c, mirror, HTTPRoute redirect filters) map to filter shapes the
	// chart doesn't yet support; conformance tests for those are skipped
	// by the suite when the feature isn't in this set.
	// SupportedFeatures pin the chart's actual coverage. Each entry must
	// correspond to template logic that's been verified end-to-end against
	// the conformance fixtures — never declare a feature the chart only
	// half-implements (the suite exists to catch the gap).
	//
	// Gateway API v1.5 treats request-header modification (set/add/remove)
	// as a CORE HTTPRoute capability — gated by SupportHTTPRoute alone, no
	// extra feature flag. Tests for it run automatically.
	supported := sets.New[features.FeatureName](
		features.SupportGateway,
		features.SupportHTTPRoute,
		features.SupportHTTPRouteQueryParamMatching,
		features.SupportHTTPRouteMethodMatching,
		features.SupportHTTPRouteResponseHeaderModification,
	)

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
	}

	gwconformance.RunConformanceWithOptions(t, opts)
}
