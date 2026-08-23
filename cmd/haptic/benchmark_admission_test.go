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

package main

import (
	"fmt"
	"maps"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// admissionSubjectName and admissionSubjectService name the new ingress the
// benchmark admits and the Service it routes to (which lives in the base store,
// so the route resolves — the overlay carries only the new object).
const (
	admissionSubjectName    = "app-admission"
	admissionSubjectService = "svc-admission"
)

// BenchmarkAdmissionRender measures what a webhook admission does: render the
// bundled chart against a base store of N watched objects, once as-is and once
// with a new ingress layered on through the same stores.CompositeStore + overlay
// the webhook builds. The /base and /overlay sub-benchmarks isolate the overlay's
// marginal cost from the store-size-driven admission-render latency; N sweeps the
// same 100/1000/5000/20000 as BenchmarkRender, at the same 1:1:2 mix, so the two
// are directly comparable and their ratio is the admission amplification a burst
// of admitted objects pays under failurePolicy: Fail. Each render gets a fresh
// context (the #165 fix) so first_seen() never suppresses a backend a prior
// render already emitted.
func BenchmarkAdmissionRender(b *testing.B) {
	cfg, setup, logger, cleanup := bundledChartSetup(b)
	b.Cleanup(cleanup)

	httpStore := createHTTPStoreForBenchmark(nil, logger)
	subject := &unstructured.Unstructured{
		Object: benchIngressContent(admissionSubjectName, admissionSubjectService),
	}

	for _, n := range benchObjectCounts {
		storeMap, err := createStoresForBenchmark(cfg, setup.Engine, benchAdmissionFixtures(cfg, n/objectsPerApp))
		require.NoError(b, err)

		b.Run(fmt.Sprintf("n=%d/base", n), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				benchFullRender(b, cfg, setup, logger, httpStore, storeMap)
			}
		})
		b.Run(fmt.Sprintf("n=%d/overlay", n), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				benchFullRender(b, cfg, setup, logger, httpStore, admissionOverlayStores(storeMap, subject))
			}
		})
	}
}

// admissionOverlayStores clones the store map and wraps its ingress store in the
// webhook's CompositeStore + create overlay, exactly as dry-run admission does
// per request. The base store is never mutated, so one map serves both the /base
// and /overlay sub-benchmarks.
func admissionOverlayStores(base map[string]stores.Store, subject *unstructured.Unstructured) map[string]stores.Store {
	overlaid := maps.Clone(base)
	overlaid[ingressStoreName] = stores.NewCompositeStore(
		base[ingressStoreName], stores.NewStoreOverlayForCreate(subject))
	return overlaid
}

// benchAdmissionFixtures builds the scale corpus plus the admission subject's
// Service and EndpointSlices in the BASE store — the overlay carries only the
// new Ingress, so its backend must already resolve, matching a live cluster
// where a burst creates Ingresses against Services that already exist.
func benchAdmissionFixtures(cfg *config.Config, apps int) map[string][]any {
	base := benchScaleFixtures(cfg, apps)
	base["services"] = append(base["services"], benchServiceContent(admissionSubjectService))
	base["endpoints"] = append(base["endpoints"],
		benchEndpointSliceContent(admissionSubjectService, apps, 0),
		benchEndpointSliceContent(admissionSubjectService, apps, 1))
	return base
}
