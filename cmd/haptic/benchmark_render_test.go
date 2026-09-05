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
	"io"
	"log/slog"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testrunner"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// ingressStoreName is the watched-resource store the bundled chart reads
// ingresses from; benchScaleFixtures populates it and the admission overlay
// wraps it.
const ingressStoreName = "ingresses"

// objectsPerApp is the watched-object count one routing unit contributes under
// the issue #145 mix: 1 Ingress, 1 Service, 2 EndpointSlices.
const objectsPerApp = 4

// benchObjectCounts sweeps total watched-object count. Each N renders
// N/objectsPerApp backends; a linear slope across these confirms the O(N) claim.
// 20000 is memory-heavy — run a subset with -bench 'n=(100|1000|5000)' to skip it.
var benchObjectCounts = []int{100, 1000, 5000, 20000}

// BenchmarkRender measures a full reconcile-path render of the bundled chart
// (haproxy.cfg plus every configured render root) against a synthetic store of N watched
// objects, at the issue #145 mix and sizes. Each render gets a fresh context so
// first_seen() never suppresses a subtree a prior render already emitted (#165),
// matching production, which builds one context per reconcile. ns/op is the
// per-render CPU cost the controller pays each reconcile; the slope across N
// answers whether render is linear in object count.
func BenchmarkRender(b *testing.B) {
	cfg, setup, logger, cleanup := bundledChartSetup(b)
	b.Cleanup(cleanup)

	httpStore := createHTTPStoreForBenchmark(nil, logger)

	for _, n := range benchObjectCounts {
		storeMap, err := createStoresForBenchmark(cfg, setup.Engine, benchScaleFixtures(cfg, n/objectsPerApp))
		require.NoError(b, err)

		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				benchFullRender(b, cfg, setup, logger, httpStore, storeMap)
			}
		})
	}
}

// benchFullRender renders every configured root — the full set a reconcile and
// a webhook admission both pay —
// against a fresh per-render context. It fatals on any render or resource error
// so a broken render can never masquerade as a fast one.
func benchFullRender(
	b *testing.B,
	cfg *config.Config,
	setup *ValidationSetup,
	logger *slog.Logger,
	httpStore *testrunner.FixtureHTTPStoreWrapper,
	storeMap map[string]stores.Store,
) {
	b.Helper()
	bctx := freshBenchmarkContext(cfg, nil, storeMap, setup.ValidationPaths, httpStore, setup.TypedResourceTypes, logger)
	if _, err := renderAllFiles(
		setup.Engine,
		cfg,
		bctx,
		storeMap,
		setup.TypedResourceTypes,
		logger,
	); err != nil {
		b.Fatalf("render failed: %v", err)
	}
	if err := bctx.Err(context.Background()); err != nil {
		b.Fatalf("render resource error: %v", err)
	}
}

func TestRenderAllFilesIncludesK8sResourceRoots(t *testing.T) {
	cfg := &config.Config{
		WatchedResources: map[string]config.WatchedResource{
			"routes": {
				APIVersion: "example.test/v1",
				Resources:  "routes",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"route-lines": {
				Requires:    []string{"routes"},
				Incremental: &config.IncrementalTemplate{Source: "routes"},
				Template:    `# {{ item | dig_string("", "metadata", "name") }}`,
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: "global\n"},
		K8sResources: map[string]config.K8sResource{
			"objects": {Template: `{{ render "route-lines" }}`},
		},
	}
	typedResult := &typebootstrap.Result{
		Types:  map[string]reflect.Type{},
		Kinds:  map[string]string{},
		Errors: map[string]error{},
	}
	engine, err := compileTemplatesForBenchmark(cfg, typedResult)
	require.NoError(t, err)
	storeMap, err := createStoresForBenchmark(cfg, engine, map[string][]any{
		"routes": {
			map[string]any{
				"apiVersion": "example.test/v1",
				"kind":       "Route",
				"metadata": map[string]any{
					"namespace": "default",
					"name":      "route",
				},
			},
		},
	})
	require.NoError(t, err)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	httpStore := createHTTPStoreForBenchmark(nil, logger)
	bctx := freshBenchmarkContext(
		cfg,
		nil,
		storeMap,
		&dataplane.ValidationPaths{},
		httpStore,
		typedResult.Types,
		logger,
	)

	result, err := renderAllFiles(engine, cfg, bctx, storeMap, typedResult.Types, logger)

	require.NoError(t, err)
	require.NoError(t, bctx.Err(t.Context()))
	names := make([]string, len(result.FileResults))
	for index := range result.FileResults {
		names[index] = result.FileResults[index].Name
	}
	require.Equal(t, []string{"haproxy.cfg", "k8s:objects"}, names)
}

// benchScaleFixtures builds a self-consistent corpus of `apps` routing units,
// each a class-haproxy Ingress, its Service, and two EndpointSlices (the 1:1:2
// mix a real cluster has). The EndpointSlices are what give each backend real
// server lines; without them every backend renders empty and the per-object
// cost — which lives in server emission — vanishes. Merged with the _global
// baseline (the isolated default certificate) so the render matches the load gate.
func benchScaleFixtures(cfg *config.Config, apps int) map[string][]any {
	services := make([]any, 0, apps)
	ingresses := make([]any, 0, apps)
	endpoints := make([]any, 0, apps*2)
	for i := range apps {
		svc := fmt.Sprintf("svc-%d", i)
		services = append(services, benchServiceContent(svc))
		ingresses = append(ingresses, benchIngressContent(fmt.Sprintf("app-%d", i), svc))
		endpoints = append(endpoints,
			benchEndpointSliceContent(svc, i, 0),
			benchEndpointSliceContent(svc, i, 1))
	}

	synthetic := map[string][]any{
		"services":       services,
		ingressStoreName: ingresses,
		"endpoints":      endpoints,
	}
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

// benchServiceContent is one Service exposing port 80 -> 8080, the target the
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

// benchEndpointSliceContent is one EndpointSlice carrying a single ready pod
// address on port 8080 (the Service's targetPort), labelled for svc so the
// ingress library indexes it as a backend server. app varies the address so
// distinct backends get distinct servers; slice names the pod within a backend.
func benchEndpointSliceContent(svc string, app, slice int) map[string]any {
	addr := fmt.Sprintf("10.%d.%d.%d", (app>>8)&0xff, app&0xff, slice+1)
	pod := fmt.Sprintf("%s-pod-%d", svc, slice)
	return map[string]any{
		"apiVersion": "discovery.k8s.io/v1",
		"kind":       "EndpointSlice",
		"metadata": map[string]any{
			"name":      fmt.Sprintf("%s-%d", svc, slice),
			"namespace": "default",
			"labels":    map[string]any{"kubernetes.io/service-name": svc},
		},
		"addressType": "IPv4",
		"endpoints": []any{map[string]any{
			"addresses": []any{addr},
			"targetRef": map[string]any{"kind": "Pod", "name": pod},
		}},
		"ports": []any{map[string]any{"port": int64(8080), "protocol": "TCP"}},
	}
}
