// Copyright 2026 Philipp Hossner
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
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

func BenchmarkBundledChartHTTPRouteAdmission(b *testing.B) {
	cfg, setup, logger, cleanup := bundledChartSetup(b)
	b.Cleanup(cleanup)
	for _, routes := range []int{1000, 3000} {
		b.Run(fmt.Sprintf("routes=%d", routes), func(b *testing.B) {
			fixtures := benchHTTPRouteScaleFixturesShaped(cfg, routes, benchRoutePlain)
			fixtures["services"] = append(fixtures["services"], benchServiceContent("svc-admission"))
			fixtures["endpoints"] = append(fixtures["endpoints"], benchEndpointSliceContent("svc-admission", routes, 0))
			storeMap, err := createStoresForBenchmark(cfg, setup.Engine, fixtures)
			require.NoError(b, err)
			provider := stores.NewRealStoreProvider(storeMap)
			lifecycle := newIncrementalBenchmarkCacheLifecycle(nil)
			service := newBundledIncrementalBenchmarkService(cfg, setup, setup.Engine, logger, lifecycle)
			baseline, err := runIncrementalBenchmarkRenderCacheReady(b.Context(), service, provider, lifecycle)
			require.NoError(b, err)
			expectedBaseline := bundledRenderAcrossServices(b, baseline)
			subject := &unstructured.Unstructured{Object: benchHTTPRouteContentShaped("route-admission", "svc-admission", benchRoutePlain)}
			opts := []rendercontext.Option{rendercontext.WithAdmissionSubject("httproutes", "default", "route-admission")}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				overlay := stores.NewOverlayStoreProvider(provider, stores.NewValidationContext(map[string]*stores.StoreOverlay{
					"httproutes": stores.NewStoreOverlayForCreate(subject),
				}))
				result, renderErr := service.Render(b.Context(), overlay, rendercontext.RenderModeAdmission, opts...)
				if renderErr != nil {
					b.Fatal(renderErr)
				}
				_, outputErr := incrementalBenchmarkOutputBytes(result)
				result.InputTransaction.Abort()
				b.StopTimer()
				require.NoError(b, outputErr)
				oracleLifecycle := newIncrementalBenchmarkCacheLifecycle(nil)
				oracleService := newBundledIncrementalBenchmarkService(cfg, setup, setup.Engine, logger, oracleLifecycle)
				oracle, oracleErr := oracleService.Render(b.Context(), overlay, rendercontext.RenderModeAdmission, opts...)
				require.NoError(b, oracleErr)
				oracle.InputTransaction.Abort()
				require.Equal(b, bundledRenderAcrossServices(b, oracle), bundledRenderAcrossServices(b, result))
				b.StartTimer()
			}
			b.StopTimer()
			after, err := runIncrementalBenchmarkRenderResult(service, provider)
			require.NoError(b, err)
			require.Equal(b, expectedBaseline, bundledRenderAcrossServices(b, after))
		})
	}
}
