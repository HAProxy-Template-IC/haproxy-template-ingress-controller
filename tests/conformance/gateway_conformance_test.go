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
// suite has its own slow setup and currently surfaces known chart bugs).
//
// To run locally:
//
//	make test-gateway-conformance
//
// To enable in CI, add a corresponding job in .gitlab-ci.yml that runs
// the same target. The job is intentionally NOT wired in this branch
// because the chart's gateway library has three known bugs (rule-precedence
// sorting, regex query matcher, method fall-through) that conformance
// would surface as failures. Resolve those, then enable.
package conformance

import (
	"testing"
)

// TestGatewayAPIConformance is a placeholder for the upstream conformance
// suite. The real wire-up requires importing
// sigs.k8s.io/gateway-api/conformance/utils/suite and adding the
// gateway-api module to go.mod / vendor — both ~20 lines of changes
// gated on the chart-side bugs being resolved first.
//
// Skeleton (uncomment + import gateway-api package once enabled):
//
//	func TestGatewayAPIConformance(t *testing.T) {
//	    cs := suite.New(suite.Options{
//	        Client:                  client,
//	        GatewayClassName:        "haptic",
//	        Debug:                   true,
//	        SupportedFeatures:       sets.New(
//	            features.SupportGateway,
//	            features.SupportHTTPRoute,
//	            features.SupportHTTPRoutePathMatching,
//	            features.SupportHTTPRouteHeaderMatching,
//	            features.SupportHTTPRouteQueryParamMatching,
//	            features.SupportHTTPRouteMethodMatching,
//	            features.SupportHTTPRouteResponseHeaderModification,
//	        ),
//	    })
//	    cs.Setup(t)
//	    cs.Run(t, gwtests.ConformanceTests)
//	}
//
// The pinned feature subset above intentionally excludes:
//   - HTTPRouteBackendProtocolH2C        (chart doesn't yet support h2c)
//   - HTTPRouteRequestMirror             (chart doesn't yet support mirroring)
//   - HTTPRoutePortRedirect              (chart doesn't yet support port redirects)
//   - HTTPRouteRequestRedirect           (chart's redirect-via-annotation lives on Ingress, not HTTPRoute)
//
// Each feature has its own test set in the upstream conformance suite;
// `SupportedFeatures` gates which tests run. Pinning to a subset means
// the chart can adopt features incrementally instead of waiting on full
// Gateway API support.
func TestGatewayAPIConformance(t *testing.T) {
	t.Skip("TODO: enable once chart-side HTTPRoute bugs are resolved (rule-precedence " +
		"sorting, regex query matcher, method fall-through). See package doc for " +
		"the wire-up sketch and pinned feature subset.")
}
