// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package renderer

import (
	"context"
	"log/slog"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type failClosedCountingEngine struct {
	templating.Engine
	componentExecutor templating.IncrementalComponentExecutor
	batchExecutor     templating.IncrementalComponentBatchExecutor
	bindingPlanner    templating.IncrementalBindingPlannerExecutor
	rootCalls         atomic.Int32
	componentCalls    atomic.Int32
}

func (e *failClosedCountingEngine) Render(
	ctx context.Context,
	templateName string,
	templateContext map[string]any,
) (string, error) {
	e.rootCalls.Add(1)
	return e.Engine.Render(ctx, templateName, templateContext)
}

func (e *failClosedCountingEngine) RenderWithProfiling(
	ctx context.Context,
	templateName string,
	templateContext map[string]any,
) (string, []templating.IncludeStats, error) {
	e.rootCalls.Add(1)
	return e.Engine.RenderWithProfiling(ctx, templateName, templateContext)
}

func (e *failClosedCountingEngine) RenderIncrementalComponent(
	ctx context.Context,
	templateName string,
	templateContext map[string]any,
) (string, error) {
	e.componentCalls.Add(1)
	return e.componentExecutor.RenderIncrementalComponent(ctx, templateName, templateContext)
}

func (e *failClosedCountingEngine) RenderIncrementalComponents(
	ctx context.Context,
	templateName string,
	items []templating.IncrementalComponentBatchItem,
) ([]string, error) {
	e.componentCalls.Add(int32(len(items)))
	return e.batchExecutor.RenderIncrementalComponents(ctx, templateName, items)
}

func (e *failClosedCountingEngine) RenderIncrementalBindings(
	ctx context.Context,
	templateName string,
	templateContext map[string]any,
) ([]byte, error) {
	return e.bindingPlanner.RenderIncrementalBindings(ctx, templateName, templateContext)
}

type verificationlessSnapshotStore struct {
	*coldInputStore
	snapshot stores.ReadSnapshot
}

func (s *verificationlessSnapshotStore) Pin() (stores.ReadSnapshot, error) {
	return s.snapshot, nil
}

type unfencedSnapshotStore struct {
	*verificationlessSnapshotStore
	journal stores.RevisionJournal
}

func (s *unfencedSnapshotStore) ListSnapshot() (items []any, revision uint64, err error) {
	return s.journal.ListSnapshot()
}

func (s *unfencedSnapshotStore) ChangesSince(sequence uint64) (uint64, []stores.RevisionChange, bool) {
	return s.journal.ChangesSince(sequence)
}

func (s *unfencedSnapshotStore) ExactRevisionJournalSource() stores.RevisionSource {
	return s.snapshot.RevisionSource()
}

type unsupportedHTTPFetcher struct {
	calls atomic.Int32
}

func (f *unsupportedHTTPFetcher) Fetch(...any) (any, error) {
	f.calls.Add(1)
	return "unverified", nil
}

func TestRenderServiceIncrementalFailsClosedForUnsupportedLiveInputs(t *testing.T) {
	tests := map[string]struct {
		componentTemplate string
		prepare           func(*testing.T, *RenderService, *config.Config) (stores.StoreProvider, []rendercontext.Option, func())
		wantError         string
	}{
		"watched resource": {
			componentTemplate: failClosedComponentTemplate(""),
			prepare: func(t *testing.T, _ *RenderService, _ *config.Config) (stores.StoreProvider, []rendercontext.Option, func()) {
				t.Helper()
				store := newVerificationlessSnapshotStore(t, failClosedResource())
				return stores.NewRealStoreProvider(map[string]stores.Store{"routes": store}), nil, func() {
					assert.Zero(t, store.listCallCount())
				}
			},
			wantError: `watched resource "routes" has no exact change journal`,
		},
		"controller resource": {
			componentTemplate: failClosedComponentTemplate(""),
			prepare: func(t *testing.T, service *RenderService, _ *config.Config) (stores.StoreProvider, []rendercontext.Option, func()) {
				t.Helper()
				routes := failClosedMemoryStore(t)
				pods := newVerificationlessSnapshotStore(t, incrementalTestResource("default", "haproxy", nil))
				service.haproxyPodStore = pods
				return stores.NewRealStoreProvider(map[string]stores.Store{"routes": routes}), nil, func() {
					assert.Zero(t, pods.listCallCount())
				}
			},
			wantError: `controller resource "haproxy_pods" has no exact change journal`,
		},
		"watched resource commit fence": {
			componentTemplate: failClosedComponentTemplate(""),
			prepare: func(t *testing.T, _ *RenderService, _ *config.Config) (stores.StoreProvider, []rendercontext.Option, func()) {
				t.Helper()
				store := newUnfencedSnapshotStore(t, failClosedResource())
				adapter := &stores.TypesStoreAdapter{Inner: store}
				return stores.NewRealStoreProvider(map[string]stores.Store{"routes": adapter}), nil, func() {
					assert.Zero(t, store.listCallCount())
				}
			},
			wantError: `watched resource "routes" has no atomic commit fence`,
		},
		"HTTP store": {
			componentTemplate: failClosedComponentTemplate(`{{ http.Fetch("https://unsupported.test/input") }}`),
			prepare: func(t *testing.T, _ *RenderService, _ *config.Config) (stores.StoreProvider, []rendercontext.Option, func()) {
				t.Helper()
				fetcher := &unsupportedHTTPFetcher{}
				return stores.NewRealStoreProvider(map[string]stores.Store{"routes": failClosedMemoryStore(t)}),
					[]rendercontext.Option{rendercontext.WithHTTPFetcher(fetcher)}, func() {
						assert.Zero(t, fetcher.calls.Load())
					}
			},
			wantError: "template HTTP fetcher is not revisioned",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cfg, engine := failClosedConfig(t, tt.componentTemplate)
			service := NewRenderService(&RenderServiceConfig{
				Engine: engine,
				Config: cfg,
				Logger: slog.Default(),
			})
			provider, options, verifySourceUnused := tt.prepare(t, service, cfg)
			base := service.incremental.snapshot

			result, err := service.Render(
				t.Context(), provider, rendercontext.RenderModeReconcile, options...,
			)

			require.ErrorIs(t, err, errIncrementalUnsupported)
			assert.ErrorContains(t, err, tt.wantError)
			assert.Nil(t, result)
			assert.Zero(t, engine.rootCalls.Load())
			assert.Zero(t, engine.componentCalls.Load())
			assert.Same(t, base, service.incremental.snapshot)
			assert.Zero(t, service.incremental.graph.Generation())
			assert.Zero(t, base.results.Len())
			assert.Zero(t, base.derived.Len())
			for _, index := range base.groupIndexes {
				hasOutput, outputErr := index.hasOutput()
				require.NoError(t, outputErr)
				assert.False(t, hasOutput)
			}
			assert.Nil(t, service.lastPlan)
			verifySourceUnused()
		})
	}
}

func TestRenderServiceUnsupportedStoreReplacementKeepsCommittedCache(t *testing.T) {
	cfg, engine := failClosedConfig(t, failClosedComponentTemplate(""))
	service := NewRenderService(&RenderServiceConfig{
		Engine: engine,
		Config: cfg,
		Logger: slog.Default(),
	})
	exactProvider := stores.NewRealStoreProvider(map[string]stores.Store{
		"routes": failClosedMemoryStore(t),
	})
	committed, err := service.Render(t.Context(), exactProvider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NoError(t, committed.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, service)
	base := service.incremental.snapshot
	generation := service.incremental.graph.Generation()
	rootCalls := engine.rootCalls.Load()
	componentCalls := engine.componentCalls.Load()
	lastPlan := service.lastPlan
	unsupported := newVerificationlessSnapshotStore(t, failClosedResource())

	result, err := service.Render(
		t.Context(),
		stores.NewRealStoreProvider(map[string]stores.Store{"routes": unsupported}),
		rendercontext.RenderModeReconcile,
	)

	require.ErrorIs(t, err, errIncrementalUnsupported)
	assert.Nil(t, result)
	assert.Same(t, base, service.incremental.snapshot)
	assert.Equal(t, generation, service.incremental.graph.Generation())
	assert.Equal(t, rootCalls, engine.rootCalls.Load())
	assert.Equal(t, componentCalls, engine.componentCalls.Load())
	assert.Same(t, lastPlan, service.lastPlan)
	assert.Zero(t, unsupported.listCallCount())
}

func failClosedConfig(t *testing.T, componentTemplate string) (*config.Config, *failClosedCountingEngine) {
	t.Helper()
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
			"component": {
				Name:     "component",
				Requires: []string{"routes"},
				Incremental: &config.IncrementalTemplate{
					Source: "routes",
					Effects: []config.IncrementalEffect{
						config.IncrementalEffectDeriveResource,
						config.IncrementalEffectRecordEvent,
					},
				},
				Template: componentTemplate,
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: `{{ render "component" }}`},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	base, err := helpers.NewEngineFromConfigWithOptions(
		cfg, nil, nil, declarations, helpers.EngineOptions{},
	)
	require.NoError(t, err)
	componentExecutor, ok := base.(templating.IncrementalComponentExecutor)
	require.True(t, ok)
	batchExecutor, ok := base.(templating.IncrementalComponentBatchExecutor)
	require.True(t, ok)
	bindingPlanner, ok := base.(templating.IncrementalBindingPlannerExecutor)
	require.True(t, ok)
	return cfg, &failClosedCountingEngine{
		Engine:            base,
		componentExecutor: componentExecutor,
		batchExecutor:     batchExecutor,
		bindingPlanner:    bindingPlanner,
	}
}

func failClosedComponentTemplate(suffix string) string {
	return `{%%
var current = deriveResource(source, item, "metadata.annotations.blocked", "true")
recordEvent(current, "Blocked", "must not publish")
%%}{{ item | dig_string("", "metadata", "name") }}` + suffix
}

func failClosedMemoryStore(t *testing.T) *k8sstore.MemoryStore {
	t.Helper()
	store := k8sstore.NewMemoryStore(2)
	require.NoError(t, store.Add(failClosedResource(), []string{"default", "route"}))
	return store
}

func newVerificationlessSnapshotStore(t *testing.T, resource any) *verificationlessSnapshotStore {
	t.Helper()
	backing := k8sstore.NewMemoryStore(2)
	require.NoError(t, backing.Add(resource, []string{"default", "route"}))
	snapshot, err := backing.Pin()
	require.NoError(t, err)
	return &verificationlessSnapshotStore{
		coldInputStore: &coldInputStore{items: []any{resource}},
		snapshot:       snapshot,
	}
}

func newUnfencedSnapshotStore(t *testing.T, resource any) *unfencedSnapshotStore {
	t.Helper()
	backing := k8sstore.NewMemoryStore(2)
	require.NoError(t, backing.Add(resource, []string{"default", "route"}))
	snapshot, err := backing.Pin()
	require.NoError(t, err)
	return &unfencedSnapshotStore{
		verificationlessSnapshotStore: &verificationlessSnapshotStore{
			coldInputStore: &coldInputStore{items: []any{resource}},
			snapshot:       snapshot,
		},
		journal: backing,
	}
}

func failClosedResource() map[string]any {
	resource := incrementalTestResource("default", "route", nil)
	resource["metadata"].(map[string]any)["annotations"] = map[string]any{}
	return resource
}
