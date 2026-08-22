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
	"context"
	"fmt"
	"maps"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testrunner"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// ingressStoreName is the watched-resource store the bundled chart reads
// ingresses from; it is also the store the admission overlay wraps.
const ingressStoreName = "ingresses"

// admissionSubjectName and admissionSubjectService name the new ingress the
// benchmark admits and the Service it routes to (which lives in the base store,
// so the route resolves — the overlay carries only the new object).
const (
	admissionSubjectName    = "app-admission"
	admissionSubjectService = "svc-admission"
)

// BenchmarkAdmissionRender measures what a webhook admission does: render the
// bundled chart against a base store of N ingresses, once as-is and once with a
// new ingress layered on through the same stores.CompositeStore + overlay the
// webhook builds. The /base and /overlay sub-benchmarks isolate the overlay's
// marginal cost from the store-size-driven admission-render latency; N sweeps
// 100/1000/5000. Each render gets a fresh context (SharedContext + PlanRegistry
// reset per iteration, the #165 fix) so first_seen() never suppresses a backend
// a prior render already emitted.
func BenchmarkAdmissionRender(b *testing.B) {
	cfg, setup, logger, cleanup := bundledChartSetup(b)
	b.Cleanup(cleanup)

	httpStore := createHTTPStoreForBenchmark(nil, logger)
	subject := &unstructured.Unstructured{
		Object: benchIngressContent(admissionSubjectName, admissionSubjectService),
	}

	render := func(b *testing.B, storeMap map[string]stores.Store) {
		b.Helper()
		bctx := freshBenchmarkContext(cfg, nil, storeMap, setup.ValidationPaths, httpStore, setup.TypedResourceTypes, logger)
		if _, _, err := renderMainConfig(setup.Engine, bctx); err != nil {
			b.Fatalf("render failed: %v", err)
		}
		if err := bctx.Err(context.Background()); err != nil {
			b.Fatalf("render resource error: %v", err)
		}
	}

	for _, n := range []int{100, 1000, 5000} {
		storeMap, err := createStoresForBenchmark(cfg, setup.Engine, benchAdmissionFixtures(cfg, n))
		require.NoError(b, err)

		b.Run(fmt.Sprintf("n=%d/base", n), func(b *testing.B) {
			for range b.N {
				render(b, storeMap)
			}
		})
		b.Run(fmt.Sprintf("n=%d/overlay", n), func(b *testing.B) {
			for range b.N {
				render(b, admissionOverlayStores(storeMap, subject))
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

// benchAdmissionFixtures builds a self-consistent corpus: N ingresses, each with
// its own Service, plus the admission subject's Service. The _global fixtures
// (the isolated default-certificate baseline the load gate renders against) are
// merged in so the render matches what admission sees.
func benchAdmissionFixtures(cfg *config.Config, n int) map[string][]any {
	services := make([]any, 0, n+1)
	ingresses := make([]any, 0, n)
	for i := range n {
		name := fmt.Sprintf("app-%d", i)
		svc := fmt.Sprintf("svc-%d", i)
		services = append(services, benchServiceContent(svc))
		ingresses = append(ingresses, benchIngressContent(name, svc))
	}
	services = append(services, benchServiceContent(admissionSubjectService))

	synthetic := map[string][]any{"services": services, ingressStoreName: ingresses}
	return testrunner.MergeFixtures(cfg.ValidationTests["_global"].Fixtures, synthetic)
}

// benchIngressContent is one class-haproxy Ingress routing host <name>.example.com
// to service svc on port 80 — the minimal shape the base library turns into a
// backend.
func benchIngressContent(name, svc string) map[string]any {
	return map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "Ingress",
		"metadata":   map[string]any{"name": name, "namespace": "default"},
		"spec": map[string]any{
			"ingressClassName": "haproxy",
			"rules": []any{map[string]any{
				"host": name + ".example.com",
				"http": map[string]any{"paths": []any{map[string]any{
					"path":     "/",
					"pathType": "Prefix",
					"backend": map[string]any{"service": map[string]any{
						"name": svc,
						"port": map[string]any{"number": int64(80)},
					}},
				}}},
			}},
		},
	}
}

// benchServiceContent is one Service exposing port 80 → 8080, the target the
// benchmark's ingresses route to.
func benchServiceContent(name string) map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata":   map[string]any{"name": name, "namespace": "default"},
		"spec": map[string]any{
			"ports": []any{map[string]any{"port": int64(80), "targetPort": int64(8080)}},
		},
	}
}
