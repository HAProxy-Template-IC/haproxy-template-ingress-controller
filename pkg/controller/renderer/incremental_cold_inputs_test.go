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

package renderer

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type coldInputStore struct {
	mu        sync.Mutex
	items     []any
	getCalls  [][]string
	listCalls int
}

func (s *coldInputStore) Get(keys ...string) ([]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getCalls = append(s.getCalls, slices.Clone(keys))
	return slices.Clone(s.items), nil
}

func (s *coldInputStore) List() ([]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listCalls++
	return slices.Clone(s.items), nil
}

func (s *coldInputStore) Add(resource any, _ []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = append(s.items, resource)
	return nil
}

func (s *coldInputStore) Update(resource any, _ []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = []any{resource}
	return nil
}

func (s *coldInputStore) Delete(_, _ string, _ []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = nil
	return nil
}

func (s *coldInputStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = nil
	return nil
}

func (s *coldInputStore) calls() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([][]string, len(s.getCalls))
	for index := range s.getCalls {
		result[index] = slices.Clone(s.getCalls[index])
	}
	return result
}

func (s *coldInputStore) listCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listCalls
}

type coldInputHTTPFetcher struct {
	calls int
	args  []any
}

func (f *coldInputHTTPFetcher) Fetch(args ...any) (any, error) {
	f.calls++
	f.args = slices.Clone(args)
	return "content", nil
}

type coldInputStringer struct {
	calls *atomic.Int32
}

func (v coldInputStringer) String() string {
	v.calls.Add(1)
	return "native"
}

type coldInputJSONMarshaler struct {
	calls *atomic.Int32
}

func (v coldInputJSONMarshaler) MarshalJSON() ([]byte, error) {
	v.calls.Add(1)
	return []byte("\"native\""), nil
}

type coldInputTextMarshaler struct {
	calls *atomic.Int32
}

func (v coldInputTextMarshaler) MarshalText() ([]byte, error) {
	v.calls.Add(1)
	return []byte("native"), nil
}

type coldInputNativeResource struct {
	calls *atomic.Int32
}

func (v coldInputNativeResource) GetName() string {
	v.calls.Add(1)
	return "native"
}

func TestColdIncrementalHTTPCanonicalizationMatchesWarmExecution(t *testing.T) {
	options := map[string]any{
		"timeout":  "3s",
		"retries":  int8(2),
		"critical": true,
	}
	headers := map[string]any{"X-Test": "value"}
	auth := map[string]any{
		"type":    "bearer",
		"token":   "secret",
		"headers": headers,
	}
	args := []any{"https://example.test/data", options, auth}
	want, err := templating.CanonicalIncrementalHTTPArgs(args...)
	require.NoError(t, err)
	base := &coldInputHTTPFetcher{}
	fetcher := &strictIncrementalHTTPFetcher{base: base}

	content, err := fetcher.Fetch(args...)
	require.NoError(t, err)
	assert.Equal(t, "content", content)
	assert.Equal(t, want, base.args)
	assert.Equal(t, 1, base.calls)

	options["critical"] = false
	auth["token"] = "changed"
	headers["X-Test"] = "changed"
	assert.Equal(t, want, base.args)
}

func TestColdIncrementalHTTPRejectsNativeArgumentsWithoutCallingMethods(t *testing.T) {
	var stringCalls atomic.Int32
	var marshalCalls atomic.Int32
	base := &coldInputHTTPFetcher{}
	fetcher := &strictIncrementalHTTPFetcher{base: base}

	_, err := fetcher.Fetch(coldInputStringer{calls: &stringCalls})
	require.Error(t, err)
	_, err = fetcher.Fetch("https://example.test", coldInputJSONMarshaler{calls: &marshalCalls})
	require.Error(t, err)

	assert.Zero(t, base.calls)
	assert.Zero(t, stringCalls.Load())
	assert.Zero(t, marshalCalls.Load())
}

func TestColdIncrementalResourceLookupCanonicalizationMatchesWarmExecution(t *testing.T) {
	cfg := &config.Config{WatchedResources: map[string]config.WatchedResource{
		"routes": {IndexBy: []string{
			"metadata.namespace",
			"metadata.name",
			"spec.enabled",
			"spec.negative",
			"spec.unsigned",
			"spec.ratio",
		}},
	}}
	store := &coldInputStore{items: []any{incrementalTestResource("", "route", map[string]any{
		"enabled":  true,
		"negative": int8(-8),
		"unsigned": uint16(16),
		"ratio":    float32(1.25),
	})}}
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": store})
	resources, resourceErrors := newColdInputResourceSurface(
		t,
		cfg,
		provider,
		map[string]struct{}{"routes": {}},
	)
	keys := []any{nil, "route", true, int8(-8), uint16(16), float32(1.25)}
	want, err := (&incrementalResourceView{}).NormalizeLookupKeys("routes", keys)
	require.NoError(t, err)

	fetched := callColdResourceOperation(t, resources, "Fetch", keys).([]any)
	single := callColdResourceOperation(t, resources, "GetSingle", keys)

	assert.Len(t, fetched, 1)
	assert.Equal(t, "route", fetched[0].(map[string]any)["metadata"].(map[string]any)["name"])
	assert.Equal(t, fetched[0], single)
	assert.Equal(t, []string{"", "route", "true", "-8", "16", "1.25"}, want)
	assert.Empty(t, store.calls())
	assert.Equal(t, 1, store.listCallCount())
	require.NoError(t, resourceErrors.Err())
}

func TestColdIncrementalResourceReadsShareOneImmutableSnapshot(t *testing.T) {
	cfg := &config.Config{WatchedResources: map[string]config.WatchedResource{
		"routes": {IndexBy: []string{"metadata.namespace", "metadata.name"}},
	}}
	source := incrementalTestResource("default", "route", map[string]any{"value": "original"})
	store := &coldInputStore{items: []any{source}}
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": store})
	resources, resourceErrors := newColdInputResourceSurface(
		t,
		cfg,
		provider,
		map[string]struct{}{"routes": {}},
	)

	source["spec"].(map[string]any)["value"] = "source mutation"
	require.NoError(t, store.Update(
		incrementalTestResource("default", "route", map[string]any{"value": "store mutation"}),
		nil,
	))
	listed := callColdResourceList(t, resources)
	require.Len(t, listed, 1)
	listed[0].(map[string]any)["spec"].(map[string]any)["value"] = "caller mutation"
	fetched := callColdResourceOperation(t, resources, "Fetch", []any{"default", "route"}).([]any)
	single := callColdResourceOperation(t, resources, "GetSingle", []any{"default", "route"})
	listedAgain := callColdResourceList(t, resources)

	assert.Equal(t, "original", fetched[0].(map[string]any)["spec"].(map[string]any)["value"])
	assert.Equal(t, "original", single.(map[string]any)["spec"].(map[string]any)["value"])
	assert.Equal(t, "original", listedAgain[0].(map[string]any)["spec"].(map[string]any)["value"])
	assert.Equal(t, 1, store.listCallCount())
	assert.Empty(t, store.calls())
	require.NoError(t, resourceErrors.Err())
}

func TestColdIncrementalResourceViewSnapshotsAvailableUndeclaredResources(t *testing.T) {
	cfg := &config.Config{WatchedResources: map[string]config.WatchedResource{
		"routes":   {IndexBy: []string{"metadata.namespace", "metadata.name"}},
		"services": {IndexBy: []string{"metadata.namespace", "metadata.name"}},
	}}
	routes := &coldInputStore{items: []any{
		incrementalTestResource("default", "route", map[string]any{"backend": "service"}),
	}}
	services := &coldInputStore{items: []any{
		incrementalTestResource("default", "service", map[string]any{"value": "available"}),
	}}
	view, storesByName, err := newColdIncrementalResourceView(
		t.Context(),
		cfg,
		map[string]struct{}{"routes": {}},
		stores.NewRealStoreProvider(map[string]stores.Store{
			"routes":   routes,
			"services": services,
		}),
	)
	require.NoError(t, err)

	fetched, err := view.Get("services", storesByName["services"], "default", "service")
	require.NoError(t, err)
	require.Len(t, fetched, 1)
	assert.Equal(t, "available", fetched[0].(map[string]any)["spec"].(map[string]any)["value"])
	assert.Equal(t, 1, routes.listCallCount())
	assert.Equal(t, 1, services.listCallCount())
}

func TestColdIncrementalResourceViewSnapshotsControllerResources(t *testing.T) {
	cfg := &config.Config{WatchedResources: map[string]config.WatchedResource{
		"routes": {IndexBy: []string{"metadata.namespace", "metadata.name"}},
	}}
	routes := &coldInputStore{}
	pod := incrementalTestResource("haptic-system", "haproxy-0", map[string]any{"phase": "Running"})
	pods := &coldInputStore{items: []any{pod}}
	view, storesByName, err := newColdIncrementalResourceView(
		t.Context(),
		cfg,
		map[string]struct{}{"routes": {}},
		stores.NewRealStoreProvider(map[string]stores.Store{"routes": routes}),
	)
	require.NoError(t, err)
	baseContext := map[string]any{"controller": map[string]templating.ResourceStore{
		"haproxy_pods": &rendercontext.StoreWrapper{
			Store: pods, ResourceType: "haproxy-pods",
			IndexBy: []string{"metadata.namespace", "metadata.name"},
		},
	}}

	require.NoError(t, addColdIncrementalControllerResources(t.Context(), baseContext, view, storesByName))
	require.NoError(t, pods.Update(
		incrementalTestResource("haptic-system", "haproxy-0", map[string]any{"phase": "Failed"}),
		nil,
	))
	pod["spec"].(map[string]any)["phase"] = "mutated"

	listed, err := view.List("haproxy-pods", storesByName["haproxy-pods"])
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, "Running", listed[0].(map[string]any)["spec"].(map[string]any)["phase"])
	fetched, err := view.Get("haproxy-pods", storesByName["haproxy-pods"], "haptic-system", "haproxy-0")
	require.NoError(t, err)
	assert.Equal(t, listed, fetched)
	assert.Equal(t, 1, pods.listCallCount())
}

func TestColdIncrementalResourceViewRejectsControllerAliasConflict(t *testing.T) {
	cfg := &config.Config{WatchedResources: map[string]config.WatchedResource{
		"haproxy-pods": {IndexBy: []string{"metadata.namespace", "metadata.name"}},
	}}
	view, storesByName, err := newColdIncrementalResourceView(
		t.Context(),
		cfg,
		map[string]struct{}{},
		stores.NewRealStoreProvider(map[string]stores.Store{"haproxy-pods": &coldInputStore{}}),
	)
	require.NoError(t, err)
	baseContext := map[string]any{"controller": map[string]templating.ResourceStore{
		"haproxy_pods": &rendercontext.StoreWrapper{
			Store: &coldInputStore{}, ResourceType: "haproxy-pods",
			IndexBy: []string{"metadata.namespace", "metadata.name"},
		},
	}}

	err = addColdIncrementalControllerResources(t.Context(), baseContext, view, storesByName)
	require.ErrorContains(t, err, `controller resource "haproxy_pods" conflicts with watched resource "haproxy-pods"`)
}

func TestColdIncrementalResourceViewKeepsRequiresAsAvailabilityMetadata(t *testing.T) {
	cfg := &config.Config{WatchedResources: map[string]config.WatchedResource{
		"routes":   {IndexBy: []string{"metadata.namespace", "metadata.name"}},
		"services": {IndexBy: []string{"metadata.namespace", "metadata.name"}},
	}}
	provider := stores.NewRealStoreProvider(map[string]stores.Store{
		"routes": &coldInputStore{},
	})

	view, _, err := newColdIncrementalResourceView(
		t.Context(),
		cfg,
		map[string]struct{}{"routes": {}},
		provider,
	)
	require.NoError(t, err)
	assert.NotContains(t, view.snapshots, "services")

	_, _, err = newColdIncrementalResourceView(
		t.Context(),
		cfg,
		map[string]struct{}{"routes": {}, "services": {}},
		provider,
	)
	require.ErrorContains(t, err, `requires unavailable resource "services"`)
}

func TestIncrementalColdAndWarmExposeAvailableUndeclaredResources(t *testing.T) {
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"routes": {
				APIVersion: "example.test/v1",
				Resources:  "routes",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
			"services": {
				APIVersion: "example.test/v1",
				Resources:  "services",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"routes": {
				Name:        "routes",
				Incremental: &config.IncrementalTemplate{Source: "routes"},
				Template: `{{ resources.services.GetSingle(
  item | dig_string("", "metadata", "namespace"),
  item | dig_string("", "spec", "backend"),
) | dig_string("<missing>", "spec", "value") }}`,
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: `{{ render "routes" }}`},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	route := incrementalTestResource("default", "route", map[string]any{"backend": "service"})
	service := incrementalTestResource("default", "service", map[string]any{"value": "available"})

	warmRoutes := k8sstore.NewMemoryStore(2)
	warmServices := k8sstore.NewMemoryStore(2)
	require.NoError(t, warmRoutes.Add(route, []string{"default", "route"}))
	require.NoError(t, warmServices.Add(service, []string{"default", "service"}))
	warmRenderer := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: coldInputTestLogger()})
	warmOutput := renderAndCommitIncrementalCacheReady(t, warmRenderer, stores.NewRealStoreProvider(map[string]stores.Store{
		"routes": warmRoutes, "services": warmServices,
	}))

	coldServices := &coldInputStore{items: []any{service}}
	coldRoutes := k8sstore.NewMemoryStore(2)
	require.NoError(t, coldRoutes.Add(route, []string{"default", "route"}))
	coldProvider := stores.NewRealStoreProvider(map[string]stores.Store{
		"routes":   coldRoutes,
		"services": coldServices,
	})
	_, coldOutput, err := renderStaticColdIncremental(t, cfg, engine, coldProvider)
	require.NoError(t, err)

	assert.Equal(t, "available\n", warmOutput)
	assert.Equal(t, warmOutput, coldOutput)
	assert.Positive(t, warmRenderer.incremental.graph.Generation())
	assert.Equal(t, 1, coldServices.listCallCount())
}

func TestIncrementalColdAndWarmExposeControllerResources(t *testing.T) {
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"routes": {
				APIVersion: "example.test/v1", Resources: "routes",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"routes": {
				Name: "routes", Requires: []string{"routes"},
				Incremental: &config.IncrementalTemplate{Source: "routes"},
				Template:    `pods={{ len(controller.haproxy_pods.List()) }}`,
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: `{{ render "routes" }}`},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	routes := k8sstore.NewMemoryStore(2)
	require.NoError(t, routes.Add(
		incrementalTestResource("default", "route", map[string]any{"value": "present"}),
		[]string{"default", "route"},
	))
	pods := k8sstore.NewMemoryStore(2)
	for _, name := range []string{"haproxy-0", "haproxy-1"} {
		require.NoError(t, pods.Add(
			incrementalTestResource("haptic-system", name, map[string]any{"phase": "Running"}),
			[]string{"haptic-system", name},
		))
	}
	provider := stores.NewRealStoreProvider(map[string]stores.Store{
		"routes": routes, "haproxy-pods": pods,
	})
	service := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: coldInputTestLogger()})

	warm := renderAndCommitIncrementalCacheReady(t, service, provider)
	cold, err := renderServiceStaticCold(t, service, provider)
	require.NoError(t, err)

	assert.Equal(t, "pods=2\n", warm)
	assert.Equal(t, warm, cold.HAProxyConfig)
}

func TestColdIncrementalResourceLookupRejectsNativeArgumentsWithoutCallingMethods(t *testing.T) {
	tests := map[string]func(*atomic.Int32) any{
		"Stringer": func(calls *atomic.Int32) any {
			return coldInputStringer{calls: calls}
		},
		"JSON marshaler": func(calls *atomic.Int32) any {
			return coldInputJSONMarshaler{calls: calls}
		},
		"text marshaler": func(calls *atomic.Int32) any {
			return coldInputTextMarshaler{calls: calls}
		},
	}

	for name, native := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := &config.Config{WatchedResources: map[string]config.WatchedResource{
				"routes": {IndexBy: []string{"metadata.name"}},
			}}
			store := &coldInputStore{items: []any{incrementalTestResource("", "route", nil)}}
			provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": store})
			resources, resourceErrors := newColdInputResourceSurface(
				t,
				cfg,
				provider,
				map[string]struct{}{"routes": {}},
			)
			var calls atomic.Int32

			callColdResourceOperation(t, resources, "Fetch", []any{native(&calls)})

			assert.Empty(t, store.calls())
			require.Error(t, resourceErrors.Err())
			assert.Zero(t, calls.Load())
		})
	}
}

func newColdInputResourceSurface(
	t *testing.T,
	cfg *config.Config,
	provider stores.StoreProvider,
	required map[string]struct{},
) (any, *rendercontext.ResourceErrorCollector) {
	t.Helper()
	view, storesByName, err := newColdIncrementalResourceView(t.Context(), cfg, required, provider)
	require.NoError(t, err)
	resourceErrors := rendercontext.NewResourceErrorCollector()
	state := &incrementalRenderState{config: cfg}
	resources := state.resourcesValue(
		templating.WithImmutableResourceInputs(t.Context()),
		storesByName,
		resourceErrors,
		view,
		nil,
		incrementalLoggerContext{logger: coldInputTestLogger()},
		false,
	)
	return resources, resourceErrors
}

func callColdResourceOperation(t *testing.T, resources any, operation string, keys []any) any {
	t.Helper()
	root := reflect.ValueOf(resources)
	require.Equal(t, reflect.Pointer, root.Kind())
	require.Equal(t, 1, root.Elem().NumField())
	resource := root.Elem().Field(0)
	require.Equal(t, reflect.Pointer, resource.Kind())
	function := resource.Elem().FieldByName(operation)
	require.True(t, function.IsValid())
	result := function.CallSlice([]reflect.Value{reflect.ValueOf(keys)})
	require.Len(t, result, 1)
	return result[0].Interface()
}

func callColdResourceList(t *testing.T, resources any) []any {
	t.Helper()
	root := reflect.ValueOf(resources)
	resource := root.Elem().Field(0).Elem()
	result := resource.FieldByName("List").Call(nil)
	require.Len(t, result, 1)
	return result[0].Interface().([]any)
}

func coldInputTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestPlainColdIncrementalResourceRejectsNativeValuesWithoutCallingMethods(t *testing.T) {
	tests := map[string]func(*atomic.Int32) any{
		"nested Stringer": func(calls *atomic.Int32) any {
			return incrementalTestResource(
				"default",
				"route",
				map[string]any{"native": coldInputStringer{calls: calls}},
			)
		},
		"nested JSON marshaler": func(calls *atomic.Int32) any {
			return incrementalTestResource(
				"default",
				"route",
				map[string]any{"native": coldInputJSONMarshaler{calls: calls}},
			)
		},
		"nested text marshaler": func(calls *atomic.Int32) any {
			return incrementalTestResource(
				"default",
				"route",
				map[string]any{"native": coldInputTextMarshaler{calls: calls}},
			)
		},
		"native resource": func(calls *atomic.Int32) any {
			return coldInputNativeResource{calls: calls}
		},
	}

	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			_, err := plainColdIncrementalResource(value(&calls))
			require.Error(t, err)
			assert.Zero(t, calls.Load())
		})
	}
}

func TestColdIncrementalResourceViewRejectsNativeSourceValuesWithoutCallingMethods(t *testing.T) {
	tests := map[string]func(*atomic.Int32) any{
		"nested Stringer": func(calls *atomic.Int32) any {
			return incrementalTestResource(
				"default",
				"route",
				map[string]any{"native": coldInputStringer{calls: calls}},
			)
		},
		"nested JSON marshaler": func(calls *atomic.Int32) any {
			return incrementalTestResource(
				"default",
				"route",
				map[string]any{"native": coldInputJSONMarshaler{calls: calls}},
			)
		},
		"nested text marshaler": func(calls *atomic.Int32) any {
			return incrementalTestResource(
				"default",
				"route",
				map[string]any{"native": coldInputTextMarshaler{calls: calls}},
			)
		},
		"native resource": func(calls *atomic.Int32) any {
			return coldInputNativeResource{calls: calls}
		},
	}

	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			cfg := &config.Config{WatchedResources: map[string]config.WatchedResource{
				"routes": {IndexBy: []string{"metadata.namespace", "metadata.name"}},
			}}
			provider := stores.NewRealStoreProvider(map[string]stores.Store{
				"routes": &coldInputStore{items: []any{value(&calls)}},
			})
			_, _, err := newColdIncrementalResourceView(
				t.Context(),
				cfg,
				map[string]struct{}{"routes": {}},
				provider,
			)
			require.Error(t, err)
			assert.Zero(t, calls.Load())
		})
	}
}

func TestRenderServiceColdIncrementalRejectsNativeSourceValuesWithoutCallingMethods(t *testing.T) {
	tests := map[string]func(*atomic.Int32) any{
		"nested Stringer": func(calls *atomic.Int32) any {
			return incrementalTestResource(
				"default",
				"route",
				map[string]any{"native": coldInputStringer{calls: calls}},
			)
		},
		"nested JSON marshaler": func(calls *atomic.Int32) any {
			return incrementalTestResource(
				"default",
				"route",
				map[string]any{"native": coldInputJSONMarshaler{calls: calls}},
			)
		},
		"nested text marshaler": func(calls *atomic.Int32) any {
			return incrementalTestResource(
				"default",
				"route",
				map[string]any{"native": coldInputTextMarshaler{calls: calls}},
			)
		},
	}

	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := coldInputRenderConfig()
			declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
				Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
			})
			engine, err := helpers.NewEngineFromConfigWithOptions(
				cfg,
				nil,
				nil,
				declarations,
				helpers.EngineOptions{},
			)
			require.NoError(t, err)
			service := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: coldInputTestLogger()})
			var calls atomic.Int32
			provider := stores.NewRealStoreProvider(map[string]stores.Store{
				"routes": &coldInputStore{items: []any{value(&calls)}},
			})

			_, err = service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
			assert.Error(t, err)
			assert.Zero(t, calls.Load())
		})
	}
}

func coldInputRenderConfig() *config.Config {
	return &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"routes": {
				APIVersion: "example.test/v1",
				Resources:  "routes",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"component": {
				Name:        "component",
				Incremental: &config.IncrementalTemplate{Source: "routes"},
				Template:    `{{ item | dig_string("", "metadata", "name") }}`,
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: `{{ render "component" }}`},
	}
}

func TestColdIncrementalRenderReplaysDerivationAndEvent(t *testing.T) {
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"routes": {
				APIVersion: "example.test/v1",
				Resources:  "routes",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"governance": {
				Name:     "governance",
				Requires: []string{"routes"},
				Incremental: &config.IncrementalTemplate{
					Source: "routes",
					Effects: []config.IncrementalEffect{
						config.IncrementalEffectDeriveResource,
						config.IncrementalEffectRecordEvent,
					},
				},
				Template: "{%%\n" +
					"var current = deriveResource(source, item, \"metadata.annotations.governed\", \"yes\")\n" +
					"recordEvent(current, \"GovernanceApplied\", \"cold replay\")\n" +
					"%%}",
			},
		},
		HAProxyConfig: config.HAProxyConfig{
			Template: "{{ render \"governance\" }}" +
				"{{ resources.routes.GetSingle(\"default\", \"route\")" +
				" | dig_string(\"<missing>\", \"metadata\", \"annotations\", \"governed\") }}",
		},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	source := incrementalTestResource("default", "route", map[string]any{})
	source["metadata"].(map[string]any)["annotations"] = map[string]any{}
	store := &coldInputStore{items: []any{source}}
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": store})

	bctx, output, err := renderStaticColdIncremental(t, cfg, engine, provider)
	require.NoError(t, err)
	assert.Equal(t, "yes\n", output)
	events := bctx.EventCollector.Events()
	require.Len(t, events, 1)
	assert.Equal(t, templating.RenderedEvent{
		Namespace:  "default",
		Name:       "route",
		APIVersion: "example.test/v1",
		Kind:       "Example",
		Type:       templating.EventTypeWarning,
		Reason:     "GovernanceApplied",
		Message:    "cold replay",
	}, events[0])
	assert.Empty(t, source["metadata"].(map[string]any)["annotations"].(map[string]any))
}

type coldMutablePlannerEngine struct {
	templating.Engine

	mu       sync.Mutex
	bindings []byte
}

func (e *coldMutablePlannerEngine) RenderIncrementalBindings(
	_ context.Context,
	_ string,
	_ map[string]any,
) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.bindings), nil
}

func (*coldMutablePlannerEngine) RenderIncrementalComponent(
	_ context.Context,
	_ string,
	_ map[string]any,
) (string, error) {
	return "", nil
}

func (e *coldMutablePlannerEngine) setBindings(bindings string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.bindings = []byte(bindings)
}

func TestColdIncrementalValidateCallsRejectsBindingPlanDrift(t *testing.T) {
	cfg := &config.Config{
		WatchedResources: map[string]config.WatchedResource{"routes": {}},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"dynamic": {
				Name: "dynamic",
				Incremental: &config.IncrementalTemplate{
					BindingsTemplate: "{{ \"{}\" }}",
				},
			},
		},
	}
	engine := &coldMutablePlannerEngine{}
	engine.setBindings("{\"routes\":{\"version\":\"one\"}}")
	state := newIncrementalRenderState(cfg, engine)
	renderer, err := newColdIncrementalRenderer(
		t.Context(),
		state,
		stores.NewRealStoreProvider(map[string]stores.Store{"routes": &coldInputStore{}}),
		rendercontext.RenderModeReconcile,
		map[string]any{
			templating.ResourceDeriverContextName: rendercontext.NewDerivedResourceView(),
		},
		rendercontext.NewResourceErrorCollector(),
		incrementalLoggerContext{logger: coldInputTestLogger()},
	)
	require.NoError(t, err)
	renderer.calls["dynamic"] = []incrementalCall{{scope: "haproxy.cfg", component: "dynamic"}}
	require.NoError(t, renderer.ValidateIncrementalCalls())

	engine.setBindings("{\"routes\":{\"version\":\"two\"}}")
	require.ErrorIs(t, renderer.ValidateIncrementalCalls(), incremental.ErrRevisionConflict)

	engine.setBindings("{\"routes\":{\"version\":\"one\"}}")
	require.NoError(t, renderer.ValidateIncrementalCalls())
}

func TestColdIncrementalRenderSubject(t *testing.T) {
	tests := []struct {
		name        string
		mode        rendercontext.RenderMode
		baseContext map[string]any
		source      string
		namespace   string
		resource    string
		wantMode    string
	}{
		{
			name:     "reconcile",
			mode:     rendercontext.RenderModeReconcile,
			source:   "routes",
			resource: "route",
			wantMode: string(rendercontext.RenderModeReconcile),
		},
		{
			name:     "offline admission applies to every fixture",
			mode:     rendercontext.RenderModeAdmission,
			source:   "routes",
			resource: "route",
			wantMode: string(rendercontext.RenderModeAdmission),
		},
		{
			name: "matching admission subject",
			mode: rendercontext.RenderModeAdmission,
			baseContext: map[string]any{"admissionSubject": map[string]any{
				"store": "routes", "namespace": "default", "name": "route",
			}},
			source:    "routes",
			namespace: "default",
			resource:  "route",
			wantMode:  string(rendercontext.RenderModeAdmission),
		},
		{
			name: "non-subject fixture remains reconcile",
			mode: rendercontext.RenderModeAdmission,
			baseContext: map[string]any{"admissionSubject": map[string]any{
				"store": "routes", "namespace": "default", "name": "other",
			}},
			source:    "routes",
			namespace: "default",
			resource:  "route",
			wantMode:  string(rendercontext.RenderModeReconcile),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			subject, err := coldIncrementalRenderSubject(
				test.baseContext,
				test.mode,
				test.source,
				test.namespace,
				test.resource,
			)
			require.NoError(t, err)
			assert.Equal(t, test.wantMode, subject["mode"])
		})
	}
}

// The resources facade — one adapter per watched resource, each carrying
// reflect.MakeFunc trampolines — costs ~30% of a cold render's allocations to
// build. It depends only on the stores and the view, so it is built once per
// render and re-pointed at each instance. Rebuilding it per instance is the
// regression this guards.
func TestSharedResourcesFacadeIsBuiltOncePerRender(t *testing.T) {
	view := &coldIncrementalResourceView{}
	built := new(int)
	renderer := &coldIncrementalRenderer{
		resourceView:   view,
		resourcesValue: built,
	}

	first := context.WithValue(t.Context(), coldFacadeTestKey{}, "first")
	if got := renderer.sharedResourcesValue(first, nil, false); got != any(built) {
		t.Fatalf("expected the cached facade, got a fresh one")
	}
	if view.instanceContext != first {
		t.Fatal("the view must point at the rendering instance's context")
	}

	second := context.WithValue(t.Context(), coldFacadeTestKey{}, "second")
	if got := renderer.sharedResourcesValue(second, nil, false); got != any(built) {
		t.Fatalf("the facade was rebuilt for the second instance")
	}
	if view.instanceContext != second {
		t.Fatal("the view still points at the previous instance's context")
	}
}

type coldFacadeTestKey struct{}
