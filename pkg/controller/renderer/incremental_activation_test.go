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
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

const activationPath = `metadata.annotations['example.test/enabled']`
const inactiveActivationOutput = "\n"

type activationFixture struct {
	config   *config.Config
	service  *RenderService
	engine   *activationCountingEngine
	store    *k8sstore.MemoryStore
	provider stores.StoreProvider
}

// TestIncrementalActivationUnmountedGroupValidation pins what decides whether a
// silent group is a torn render: participation, not activation. A root that
// never renders the group leaves it silent whether or not its instances are
// active -- the chart's own conditions excluded the consumer, the way a
// frontend-filter library renders nothing when no frontend exists. A consumer
// that reads the group's values without it having run is the torn case, and
// still fails.
func TestIncrementalActivationUnmountedGroupValidation(t *testing.T) {
	for _, cold := range []bool{false, true} {
		mode := "warm"
		if cold {
			mode = "cold"
		}
		for _, active := range []bool{false, true} {
			state := "inactive"
			if active {
				state = "active"
			}
			t.Run("unrendered "+state+"/"+mode, func(t *testing.T) {
				cfg := activationConfig(false)
				delete(cfg.TemplateSnippets, "governance")
				cfg.HAProxyConfig.Template = "static\n"
				service, engine := newActivationService(t, cfg)
				store := k8sstore.NewMemoryStore(2)
				require.NoError(t, store.Add(
					activationResource("route", "v1", active), []string{"default", "route"},
				))
				provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": store})

				result, err := renderUnmountedActivation(t, cold, service, provider)
				require.NoError(t, err)
				assert.Equal(t, "static\n", result.HAProxyConfig)
				assert.Zero(t, engine.totalExecutions())
				require.NoError(t, result.InputTransaction.Commit(t.Context()))
			})
		}

		t.Run("empty early read/"+mode, func(t *testing.T) {
			cfg := activationConfig(false)
			delete(cfg.TemplateSnippets, "governance")
			feature := cfg.TemplateSnippets["feature"]
			feature.Incremental.Effects = []config.IncrementalEffect{config.IncrementalEffectPublishValue}
			feature.Template = `{%% shared.Publish("values", "route", item) %%}`
			cfg.TemplateSnippets["feature"] = feature
			cfg.HAProxyConfig.Template = `{{ len(incremental_values("feature", "values")) }}`
			service, engine := newActivationService(t, cfg)
			store := k8sstore.NewMemoryStore(2)
			require.NoError(t, store.Add(activationResource("route", "v1", false), []string{"default", "route"}))
			provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": store})

			result, err := renderUnmountedActivation(t, cold, service, provider)
			require.ErrorContains(t, err, "got 0 calls")
			assert.Nil(t, result)
			assert.Zero(t, engine.totalExecutions())
		})
	}
}

func TestIncrementalActivationUnmountedGroupRetiresCachedResults(t *testing.T) {
	cfg := activationConfig(false)
	delete(cfg.TemplateSnippets, "governance")
	cfg.TemplatingSettings.ExtraContext["mount"] = true
	cfg.HAProxyConfig.Template = `{% if tostring(extraContext["mount"]) == "true" %}{{ render "feature" }}{% end %}`
	service, engine := newActivationService(t, cfg)
	store := k8sstore.NewMemoryStore(2)
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": store})
	require.NoError(t, store.Add(activationResource("route", "v1", true), []string{"default", "route"}))

	active, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	assert.Equal(t, "feature:route\n", active.HAProxyConfig)
	require.NoError(t, active.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, service)
	assert.Equal(t, 1, engine.executions("feature", "route"))

	require.NoError(t, store.Update(activationResource("route", "v2", false), []string{"default", "route"}))
	cfg.TemplatingSettings.ExtraContext["mount"] = false
	inactive, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	assert.Equal(t, inactiveActivationOutput, inactive.HAProxyConfig)
	require.NoError(t, inactive.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, service)
	assert.Equal(t, 1, engine.executions("feature", "route"))
	assert.Zero(t, service.incremental.snapshot.groupIndexes["feature"].instances.Len())
	featureComponent := service.incremental.components["feature"]
	_, cached := service.incremental.snapshot.results.Get(
		resultKey(&featureComponent, "routes", "default", "route"),
	)
	assert.False(t, cached)
}

func renderUnmountedActivation(
	t *testing.T,
	cold bool,
	service *RenderService,
	provider stores.StoreProvider,
) (*RenderResult, error) {
	t.Helper()
	if cold {
		return renderServiceStaticCold(t, service, provider)
	}
	return service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
}

func TestIncrementalActivationColdWarmParity(t *testing.T) {
	tests := map[string]struct {
		item       map[string]any
		governance bool
		want       string
		wantRuns   int
	}{
		"inactive": {
			item: activationResource("route", "v1", false),
			want: inactiveActivationOutput,
		},
		"raw path": {
			item:     activationResource("route", "v1", true),
			want:     "feature:route\n",
			wantRuns: 1,
		},
		"governance path": {
			item:       activationResource("route", "v1", false),
			governance: true,
			want:       "feature:route\n",
			wantRuns:   1,
		},
	}
	for name, test := range tests {
		for _, cold := range []bool{false, true} {
			mode := "warm"
			if cold {
				mode = "cold"
			}
			t.Run(name+"/"+mode, func(t *testing.T) {
				cfg := activationConfig(test.governance)
				service, engine := newActivationService(t, cfg)
				var provider stores.StoreProvider
				if cold {
					provider = stores.NewRealStoreProvider(map[string]stores.Store{
						"routes": &derivedStageColdStore{items: []any{test.item}},
					})
				} else {
					store := k8sstore.NewMemoryStore(2)
					require.NoError(t, store.Add(test.item, []string{"default", "route"}))
					provider = stores.NewRealStoreProvider(map[string]stores.Store{"routes": store})
				}
				result := renderActivation(t, cold, service, provider, rendercontext.RenderModeReconcile)
				assert.Equal(t, test.want, result.HAProxyConfig)
				assert.Equal(t, test.wantRuns, engine.executions("feature", "route"))
			})
		}
	}
}

func TestIncrementalActivationBackdatesUnchangedPredicate(t *testing.T) {
	fixture := newActivationFixture(t)
	fixture.add(t, activationResource("a", "v1", false))
	fixture.add(t, activationResource("b", "v1", false))

	assert.Equal(t, inactiveActivationOutput, fixture.renderAndCommit(t).HAProxyConfig)
	component := fixture.service.incremental.components["feature"]
	componentA := componentQueryKey(&component, "routes", "default", "a")
	predicateA := activationQueryKey("routes", "default", "a")
	componentB := componentQueryKey(&component, "routes", "default", "b")
	predicateB := activationQueryKey("routes", "default", "b")
	assert.Zero(t, fixture.service.incremental.graph.Counters(componentA).Executions)
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(predicateA).Executions)
	assert.Zero(t, fixture.engine.executions("feature", "a"))
	assert.Zero(t, fixture.engine.executions("feature", "b"))

	fixture.update(t, activationResource("a", "v2", false))
	assert.Equal(t, inactiveActivationOutput, fixture.renderAndCommit(t).HAProxyConfig)
	assert.Zero(t, fixture.service.incremental.graph.Counters(componentA).Executions)
	assert.Equal(t, uint64(2), fixture.service.incremental.graph.Counters(predicateA).Executions)
	assert.Zero(t, fixture.service.incremental.graph.Counters(componentB).Executions)
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(predicateB).Executions)
	assert.Zero(t, fixture.engine.executions("feature", "a"))
	assert.Zero(t, fixture.engine.executions("feature", "b"))

	fixture.update(t, activationResource("a", "v3", true))
	assert.Equal(t, "feature:a\n", fixture.renderAndCommit(t).HAProxyConfig)
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(componentA).Executions)
	assert.Equal(t, uint64(3), fixture.service.incremental.graph.Counters(predicateA).Executions)
	assert.Equal(t, 1, fixture.engine.executions("feature", "a"))

	fixture.update(t, activationResource("a", "v4", false))
	assert.Equal(t, inactiveActivationOutput, fixture.renderAndCommit(t).HAProxyConfig)
	assert.Zero(t, fixture.service.incremental.graph.Counters(componentA).Executions)
	assert.Equal(t, uint64(4), fixture.service.incremental.graph.Counters(predicateA).Executions)
	assert.Equal(t, 1, fixture.engine.executions("feature", "a"))

	fixture.update(t, activationResource("a", "v5", false))
	assert.Equal(t, inactiveActivationOutput, fixture.renderAndCommit(t).HAProxyConfig)
	assert.Zero(t, fixture.service.incremental.graph.Counters(componentA).Executions)
	assert.Equal(t, uint64(5), fixture.service.incremental.graph.Counters(predicateA).Executions)
	assert.Equal(t, 1, fixture.engine.executions("feature", "a"))
}

func TestIncrementalActivationSharesOneSignatureAcrossComponents(t *testing.T) {
	cfg := activationConfig(false)
	delete(cfg.TemplateSnippets, "governance")
	second := cfg.TemplateSnippets["feature"]
	second.Name = "second"
	second.Incremental = &config.IncrementalTemplate{
		Source:            "routes",
		WhenAnyPathExists: []string{`metadata.annotations['example.test/second']`},
	}
	second.Template = `second:{{ item | dig_string("", "metadata", "name") }}
`
	cfg.TemplateSnippets["second"] = second
	cfg.HAProxyConfig.Template = `{{ render "feature" }}{{ render "second" }}`
	service, engine := newActivationService(t, cfg)
	store := k8sstore.NewMemoryStore(2)
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": store})
	item := activationResource("route", "v1", true)
	require.NoError(t, store.Add(item, []string{"default", "route"}))

	first := renderActivation(t, false, service, provider, rendercontext.RenderModeReconcile)
	assert.Equal(t, "feature:route\n", first.HAProxyConfig)
	assert.Equal(t, 1, engine.executions("feature", "route"))
	assert.Zero(t, engine.executions("second", "route"))
	signature := activationQueryKey("routes", "default", "route")
	assert.Equal(t, uint64(1), service.incremental.graph.Counters(signature).Executions)
	secondComponent := service.incremental.components["second"]
	assert.Zero(t, service.incremental.graph.Counters(
		componentQueryKey(&secondComponent, "routes", "default", "route"),
	))

	item = activationResource("route", "v2", true)
	item["metadata"].(map[string]any)["annotations"].(map[string]any)["example.test/second"] = nil
	require.NoError(t, store.Update(item, []string{"default", "route"}))
	secondActive := renderActivation(t, false, service, provider, rendercontext.RenderModeReconcile)
	assert.Equal(t, "feature:route\nsecond:route\n", secondActive.HAProxyConfig)
	assert.Equal(t, uint64(2), service.incremental.graph.Counters(signature).Executions)
	assert.Equal(t, 2, engine.executions("feature", "route"))
	assert.Equal(t, 1, engine.executions("second", "route"))

	item = activationResource("route", "v3", true)
	require.NoError(t, store.Update(item, []string{"default", "route"}))
	secondInactive := renderActivation(t, false, service, provider, rendercontext.RenderModeReconcile)
	assert.Equal(t, "feature:route\n", secondInactive.HAProxyConfig)
	assert.Equal(t, uint64(3), service.incremental.graph.Counters(signature).Executions)
	assert.Equal(t, 3, engine.executions("feature", "route"))
	assert.Equal(t, 1, engine.executions("second", "route"))
	assert.Zero(t, service.incremental.graph.Counters(
		componentQueryKey(&secondComponent, "routes", "default", "route"),
	))
}

func TestIncrementalActivationInactiveScale(t *testing.T) {
	for _, size := range []int{300, 1000, 3000} {
		t.Run(fmt.Sprintf("objects=%d", size), func(t *testing.T) {
			cfg := activationConfig(false)
			delete(cfg.TemplateSnippets, "governance")
			cfg.HAProxyConfig.Template = `{{ render "feature" }}`
			service, engine := newActivationService(t, cfg)
			items := make([]any, 0, size)
			for index := range size {
				items = append(items, activationResource(fmt.Sprintf("route-%04d", index), "v1", false))
			}

			coldProvider := stores.NewRealStoreProvider(map[string]stores.Store{
				"routes": &derivedStageColdStore{items: items},
			})
			assert.Equal(t, inactiveActivationOutput,
				renderActivation(t, true, service, coldProvider, rendercontext.RenderModeReconcile).HAProxyConfig)
			assert.Zero(t, engine.totalExecutions())

			store := k8sstore.NewMemoryStore(2)
			for _, item := range items {
				resource := item.(map[string]any)
				_, name, ok := resourceIdentity(resource)
				require.True(t, ok)
				require.NoError(t, store.Add(resource, []string{"default", name}))
			}
			provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": store})
			assert.Equal(t, inactiveActivationOutput,
				renderActivation(t, false, service, provider, rendercontext.RenderModeReconcile).HAProxyConfig)
			assert.Zero(t, engine.totalExecutions())

			component := service.incremental.components["feature"]
			componentExecutions := activationComponentQueryExecutions(service, &component, size)
			predicateExecutions := activationPredicateQueryExecutions(service, size)
			assert.Zero(t, componentExecutions)
			assert.Equal(t, uint64(size), predicateExecutions)

			revised := activationResource("route-0000", "v2", false)
			require.NoError(t, store.Update(revised, []string{"default", "route-0000"}))
			assert.Equal(t, inactiveActivationOutput,
				renderActivation(t, false, service, provider, rendercontext.RenderModeReconcile).HAProxyConfig)
			assert.Zero(t, engine.totalExecutions())
			assert.Equal(t, componentExecutions,
				activationComponentQueryExecutions(service, &component, size))
			assert.Equal(t, predicateExecutions+1,
				activationPredicateQueryExecutions(service, size))
		})
	}
}

func TestIncrementalActiveGroupProofRejectsPoisonedCache(t *testing.T) {
	for _, active := range []bool{false, true} {
		name := "inactive"
		if active {
			name = "active"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newActivationFixture(t)
			fixture.add(t, activationResource("route", "v1", active))
			fixture.renderAndCommit(t)

			component := fixture.service.incremental.components["feature"]
			key := incrementalActiveGroupInstanceKey(&component, "routes", "default", "route")
			committed := fixture.service.incremental.snapshot
			poisoned := *committed.activeGroups
			txn := poisoned.instances.Txn()
			if active {
				txn.Delete(key)
			} else {
				txn.Insert(key, struct{}{})
			}
			poisoned.instances = txn.Commit()
			committed.activeGroups = &poisoned

			result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
			require.ErrorContains(t, err, "incremental state snapshot active-group root changed")
			assert.Nil(t, result)
			assert.Same(t, committed, fixture.service.incremental.snapshot)
		})
	}
}

func TestIncrementalActiveGroupProofConcurrentCandidatesStayIsolated(t *testing.T) {
	fixture := newActivationFixture(t)
	fixture.add(t, activationResource("route", "v1", false))
	assert.Equal(t, inactiveActivationOutput, fixture.renderAndCommit(t).HAProxyConfig)
	committedInactive := fixture.service.incremental.snapshot
	assert.False(t, incrementalActiveGroupExists(committedInactive.activeGroups.instances.Root(), "feature"))

	fixture.update(t, activationResource("route", "v2", true))
	activeCandidates := renderActivationCandidates(t, fixture, 8)
	assert.Same(t, committedInactive, fixture.service.incremental.snapshot)
	abortActivationCandidates(activeCandidates)
	assert.Same(t, committedInactive, fixture.service.incremental.snapshot)
	assert.False(t, incrementalActiveGroupExists(committedInactive.activeGroups.instances.Root(), "feature"))

	assert.Equal(t, "feature:route\n", fixture.renderAndCommit(t).HAProxyConfig)
	committedActive := fixture.service.incremental.snapshot
	assert.True(t, incrementalActiveGroupExists(committedActive.activeGroups.instances.Root(), "feature"))

	fixture.update(t, activationResource("route", "v3", false))
	inactiveCandidates := renderActivationCandidates(t, fixture, 8)
	assert.Same(t, committedActive, fixture.service.incremental.snapshot)
	abortActivationCandidates(inactiveCandidates)
	assert.Same(t, committedActive, fixture.service.incremental.snapshot)
	assert.True(t, incrementalActiveGroupExists(committedActive.activeGroups.instances.Root(), "feature"))

	assert.Equal(t, inactiveActivationOutput, fixture.renderAndCommit(t).HAProxyConfig)
	assert.False(t, incrementalActiveGroupExists(
		fixture.service.incremental.snapshot.activeGroups.instances.Root(), "feature",
	))
}

func renderActivationCandidates(t *testing.T, fixture *activationFixture, count int) []*RenderResult {
	t.Helper()
	results := make([]*RenderResult, count)
	errs := make([]error, count)
	var wait sync.WaitGroup
	for index := range count {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results[index], errs[index] = fixture.service.Render(
				t.Context(), fixture.provider, rendercontext.RenderModeReconcile,
			)
		}()
	}
	wait.Wait()
	for index := range count {
		require.NoError(t, errs[index])
		require.NotNil(t, results[index])
	}
	return results
}

func abortActivationCandidates(results []*RenderResult) {
	var wait sync.WaitGroup
	for index := range results {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results[index].InputTransaction.Abort()
		}()
	}
	wait.Wait()
}

func BenchmarkIncrementalActivationUnmountedGroupProof(b *testing.B) {
	for _, size := range []int{300, 1000, 3000} {
		b.Run(fmt.Sprintf("no-change-%d", size), func(b *testing.B) {
			benchmarkInactiveActivationNoChange(b, size)
		})
		b.Run(fmt.Sprintf("one-change-%d", size), func(b *testing.B) {
			benchmarkInactiveActivationOneChange(b, size)
		})
	}
}

func benchmarkInactiveActivationNoChange(b *testing.B, size int) {
	b.Helper()
	service, engine, _, provider := benchmarkInactiveActivationFixture(b, size)
	before := engine.totalExecutions()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		result, err := service.Render(b.Context(), provider, rendercontext.RenderModeReconcile)
		if err != nil {
			b.Fatal(err)
		}
		if err := result.InputTransaction.Commit(b.Context()); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(engine.totalExecutions()-before)/float64(b.N), "component-executions/op")
}

func benchmarkInactiveActivationOneChange(b *testing.B, size int) {
	b.Helper()
	service, engine, store, provider := benchmarkInactiveActivationFixture(b, size)
	before := engine.totalExecutions()
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := range b.N {
		b.StopTimer()
		resource := activationResource("route-0000", fmt.Sprintf("v%d", iteration), false)
		if err := store.Update(resource, []string{"default", "route-0000"}); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		result, err := service.Render(b.Context(), provider, rendercontext.RenderModeReconcile)
		if err != nil {
			b.Fatal(err)
		}
		if err := result.InputTransaction.Commit(b.Context()); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(engine.totalExecutions()-before)/float64(b.N), "component-executions/op")
}

func benchmarkInactiveActivationFixture(
	b *testing.B,
	size int,
) (*RenderService, *activationCountingEngine, *k8sstore.MemoryStore, stores.StoreProvider) {
	b.Helper()
	cfg := activationConfig(false)
	delete(cfg.TemplateSnippets, "governance")
	cfg.HAProxyConfig.Template = "static\n"
	service, engine := newActivationService(b, cfg)
	store := k8sstore.NewMemoryStore(2)
	for index := range size {
		name := fmt.Sprintf("route-%04d", index)
		require.NoError(b, store.Add(activationResource(name, "v1", false), []string{"default", name}))
	}
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": store})
	result, err := service.Render(b.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(b, err)
	require.NoError(b, result.InputTransaction.Commit(b.Context()))
	require.False(b, incrementalActiveGroupExists(
		service.incremental.snapshot.activeGroups.instances.Root(), "feature",
	))
	require.Zero(b, engine.totalExecutions())
	return service, engine, store, provider
}

func activationComponentQueryExecutions(
	service *RenderService,
	component *incrementalComponent,
	size int,
) uint64 {
	var executions uint64
	for index := range size {
		name := fmt.Sprintf("route-%04d", index)
		executions += service.incremental.graph.Counters(
			componentQueryKey(component, "routes", "default", name),
		).Executions
	}
	return executions
}

func activationPredicateQueryExecutions(service *RenderService, size int) uint64 {
	var executions uint64
	for index := range size {
		name := fmt.Sprintf("route-%04d", index)
		executions += service.incremental.graph.Counters(
			activationQueryKey("routes", "default", name),
		).Executions
	}
	return executions
}

func TestIncrementalActivationTracksGovernanceWithoutMutatingStore(t *testing.T) {
	fixture := newActivationFixture(t)
	fixture.add(t, activationResource("route", "v1", false))
	original := encodedDerivedStageStore(t, fixture.store)

	assert.Equal(t, inactiveActivationOutput, fixture.renderAndCommit(t).HAProxyConfig)
	assert.Equal(t, original, encodedDerivedStageStore(t, fixture.store))
	assert.Zero(t, fixture.engine.executions("feature", "route"))

	fixture.config.TemplatingSettings.ExtraContext["governance"] = true
	assert.Equal(t, "feature:route\n", fixture.renderAndCommit(t).HAProxyConfig)
	assert.Equal(t, original, encodedDerivedStageStore(t, fixture.store))
	assert.Equal(t, 1, fixture.engine.executions("feature", "route"))

	fixture.config.TemplatingSettings.ExtraContext["governance"] = false
	assert.Equal(t, inactiveActivationOutput, fixture.renderAndCommit(t).HAProxyConfig)
	assert.Equal(t, original, encodedDerivedStageStore(t, fixture.store))
	assert.Equal(t, 1, fixture.engine.executions("feature", "route"))
}

func TestIncrementalActivationClearsDeclaredEffects(t *testing.T) {
	fixture := newActivationFixture(t)
	delete(fixture.config.TemplateSnippets, "governance")
	feature := fixture.config.TemplateSnippets["feature"]
	feature.Incremental.Effects = []config.IncrementalEffect{config.IncrementalEffectRecordEvent}
	feature.Template = `{%% recordEvent(item, "Activated", "active") %%}feature:{{ item | dig_string("", "metadata", "name") }}
`
	fixture.config.TemplateSnippets["feature"] = feature
	fixture.config.HAProxyConfig.Template = `{{ render "feature" }}`
	fixture.service, fixture.engine = newActivationService(t, fixture.config)

	fixture.add(t, activationResource("route", "v1", true))
	active := fixture.renderAndCommit(t)
	assert.Equal(t, "feature:route\n", active.HAProxyConfig)
	require.Len(t, requireRenderEvents(t, active), 1)
	assert.Equal(t, 1, fixture.engine.executions("feature", "route"))

	fixture.update(t, activationResource("route", "v2", false))
	inactive := fixture.renderAndCommit(t)
	assert.Equal(t, inactiveActivationOutput, inactive.HAProxyConfig)
	assert.Empty(t, requireRenderEvents(t, inactive))
	assert.Equal(t, 1, fixture.engine.executions("feature", "route"))
}

func TestIncrementalActivationRetiresPredicateWithComponent(t *testing.T) {
	fixture := newActivationFixture(t)
	fixture.add(t, activationResource("route", "v1", false))
	assert.Equal(t, inactiveActivationOutput, fixture.renderAndCommit(t).HAProxyConfig)
	component := fixture.service.incremental.components["feature"]
	componentKey := componentQueryKey(&component, "routes", "default", "route")
	predicateKey := activationQueryKey("routes", "default", "route")
	assert.Zero(t, fixture.service.incremental.graph.Counters(componentKey))
	assert.NotZero(t, fixture.service.incremental.graph.Counters(predicateKey))

	require.NoError(t, fixture.store.Delete("default", "route", []string{"default", "route"}))
	assert.Equal(t, inactiveActivationOutput, fixture.renderAndCommit(t).HAProxyConfig)
	assert.Zero(t, fixture.service.incremental.graph.Counters(componentKey))
	assert.Zero(t, fixture.service.incremental.graph.Counters(predicateKey))
	assert.Zero(t, fixture.engine.executions("feature", "route"))
}

func TestIncrementalActivationInvalidPathFailsClosedWithoutObjects(t *testing.T) {
	cfg := activationConfig(false)
	feature := cfg.TemplateSnippets["feature"]
	feature.Incremental.WhenAnyPathExists = []string{"spec.rules[?(@.host)].host"}
	cfg.TemplateSnippets["feature"] = feature
	service, engine := newActivationService(t, cfg)
	provider := stores.NewRealStoreProvider(map[string]stores.Store{
		"routes": k8sstore.NewMemoryStore(2),
	})

	result, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "incremental.whenAnyPathExists[0]")
	assert.Nil(t, result)
	assert.Zero(t, engine.totalExecutions())
}

func TestIncrementalActivationWildcardTracksAnyArrayElement(t *testing.T) {
	fixture := newActivationFixture(t)
	delete(fixture.config.TemplateSnippets, "governance")
	feature := fixture.config.TemplateSnippets["feature"]
	feature.Incremental.WhenAnyPathExists = []string{"spec.rules[*].filters"}
	fixture.config.TemplateSnippets["feature"] = feature
	fixture.config.HAProxyConfig.Template = `{{ render "feature" }}`
	fixture.service, fixture.engine = newActivationService(t, fixture.config)

	route := activationResource("route", "v1", false)
	route["spec"].(map[string]any)["rules"] = []any{
		map[string]any{"backendRefs": []any{}},
		map[string]any{"matches": []any{}},
	}
	fixture.add(t, route)
	assert.Equal(t, inactiveActivationOutput, fixture.renderAndCommit(t).HAProxyConfig)
	assert.Zero(t, fixture.engine.executions("feature", "route"))

	route = activationResource("route", "v2", false)
	route["spec"].(map[string]any)["rules"] = []any{
		map[string]any{"backendRefs": []any{}},
		map[string]any{"filters": []any{}},
	}
	fixture.update(t, route)
	active := fixture.renderAndCommit(t)
	assert.Equal(t, "feature:route\n", active.HAProxyConfig)
	assert.Equal(t, 1, fixture.engine.executions("feature", "route"))
	assertRenderResultObservablesEqual(t, active, fixture.renderAndCommit(t))
	assert.Equal(t, 1, fixture.engine.executions("feature", "route"))

	route = activationResource("route", "v3", false)
	route["spec"].(map[string]any)["rules"] = []any{
		map[string]any{"filters": []any{map[string]any{"type": "ExtensionRef"}}},
	}
	fixture.update(t, route)
	assert.Equal(t, "feature:route\n", fixture.renderAndCommit(t).HAProxyConfig)
	assert.Equal(t, 2, fixture.engine.executions("feature", "route"))

	route = activationResource("route", "v4", false)
	route["spec"].(map[string]any)["rules"] = []any{map[string]any{"backendRefs": []any{}}}
	fixture.update(t, route)
	assert.Equal(t, inactiveActivationOutput, fixture.renderAndCommit(t).HAProxyConfig)
	assert.Equal(t, 2, fixture.engine.executions("feature", "route"))
}

func TestIncrementalActivationTransactionsDoNotPoisonCache(t *testing.T) {
	fixture := newActivationFixture(t)
	fixture.add(t, activationResource("route", "v1", false))
	assert.Equal(t, inactiveActivationOutput, fixture.renderAndCommit(t).HAProxyConfig)
	component := fixture.service.incremental.components["feature"]
	query := componentQueryKey(&component, "routes", "default", "route")
	committed := fixture.service.incremental.graph.Counters(query)

	fixture.config.TemplatingSettings.ExtraContext["governance"] = true
	fixture.config.TemplatingSettings.ExtraContext["failAfter"] = true
	failed, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "forced activation failure")
	assert.Nil(t, failed)
	assert.Equal(t, committed, fixture.service.incremental.graph.Counters(query))
	assert.Equal(t, 1, fixture.engine.executions("feature", "route"))

	fixture.config.TemplatingSettings.ExtraContext["failAfter"] = false
	assert.Equal(t, "feature:route\n", fixture.renderAndCommit(t).HAProxyConfig)
	assert.Equal(t, 2, fixture.engine.executions("feature", "route"))

	fixture.config.TemplatingSettings.ExtraContext["governance"] = false
	assert.Equal(t, inactiveActivationOutput, fixture.renderAndCommit(t).HAProxyConfig)
	executions := fixture.engine.executions("feature", "route")
	proposed := activationResource("route", "admission", true)
	admissionProvider := stores.NewOverlayStoreProvider(
		fixture.provider,
		stores.NewValidationContext(map[string]*stores.StoreOverlay{
			"routes": stores.NewStoreOverlayForUpdate(&unstructured.Unstructured{Object: proposed}),
		}),
	)
	admission := renderActivation(
		t,
		false,
		fixture.service,
		admissionProvider,
		rendercontext.RenderModeAdmission,
		rendercontext.WithAdmissionSubject("routes", "default", "route"),
	)
	assert.Equal(t, "feature:route\n", admission.HAProxyConfig)
	assert.Equal(t, executions+1, fixture.engine.executions("feature", "route"))
	assert.Equal(t, inactiveActivationOutput, fixture.renderAndCommit(t).HAProxyConfig)
	assert.Equal(t, executions+1, fixture.engine.executions("feature", "route"))

	fixture.update(t, proposed)
	assert.Equal(t, "feature:route\n", fixture.renderAndCommit(t).HAProxyConfig)
	assert.Equal(t, executions+2, fixture.engine.executions("feature", "route"))
}

type activationTransitionCase struct {
	cfg            *config.Config
	service        *RenderService
	engine         *activationCountingEngine
	store          *k8sstore.MemoryStore
	provider       stores.StoreProvider
	signatureKey   incremental.QueryKey
	componentKey   incremental.QueryKey
	resultCacheKey []byte
}

func newActivationTransitionCase(t *testing.T) *activationTransitionCase {
	t.Helper()
	cfg := activationConfig(false)
	delete(cfg.TemplateSnippets, "governance")
	feature := cfg.TemplateSnippets["feature"]
	feature.Incremental.Source = ""
	feature.Incremental.BindingsTemplate = `{{ toJSON(extraContext["bindings"]) }}`
	feature.Template = `{{ props | dig_string("", "label") }}/{{ item | dig_string("", "metadata", "name") }}@{{ renderMode }}
`
	cfg.TemplateSnippets["feature"] = feature
	cfg.TemplatingSettings.ExtraContext["bindings"] = map[string]any{}
	cfg.HAProxyConfig.Template = `{{ render "feature" }}{%- if tostring(extraContext["failAfter"]) == "true" -%}{{- fail("forced activation failure") -}}{%- end -%}`
	service, engine := newActivationService(t, cfg)
	store := k8sstore.NewMemoryStore(2)
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": store})
	inactive := activationResource("route", "v1", false)
	require.NoError(t, store.Add(inactive, []string{"default", "route"}))
	component := service.incremental.components["feature"]
	return &activationTransitionCase{
		cfg:            cfg,
		service:        service,
		engine:         engine,
		store:          store,
		provider:       provider,
		signatureKey:   activationQueryKey("routes", "default", "route"),
		componentKey:   componentQueryKey(&component, "routes", "default", "route"),
		resultCacheKey: resultKey(&component, "routes", "default", "route"),
	}
}

func (c *activationTransitionCase) render(t *testing.T) string {
	t.Helper()
	return renderActivation(t, false, c.service, c.provider, rendercontext.RenderModeReconcile).HAProxyConfig
}

func (c *activationTransitionCase) hasCachedResult() bool {
	_, hasResult := c.service.incremental.snapshot.results.Get(c.resultCacheKey)
	return hasResult
}

func (c *activationTransitionCase) bindWithLabel(label string) {
	c.cfg.TemplatingSettings.ExtraContext["bindings"] = map[string]any{
		"routes": map[string]any{"label": label},
	}
}

func (c *activationTransitionCase) activateBinding(t *testing.T) (
	signature, component incremental.NodeCounters,
) {
	t.Helper()
	assert.Equal(t, inactiveActivationOutput, c.render(t))
	assert.Zero(t, c.service.incremental.graph.Counters(c.signatureKey))
	assert.Zero(t, c.service.incremental.graph.Counters(c.componentKey))
	assert.False(t, c.hasCachedResult())

	c.bindWithLabel("one")
	assert.Equal(t, inactiveActivationOutput, c.render(t))
	assert.Equal(t, uint64(1), c.service.incremental.graph.Counters(c.signatureKey).Executions)
	assert.Zero(t, c.service.incremental.graph.Counters(c.componentKey))
	assert.Zero(t, c.engine.executions("feature", "route"))

	active := activationResource("route", "v2", true)
	require.NoError(t, c.store.Update(active, []string{"default", "route"}))
	assert.Equal(t, "one/route@reconcile\n", c.render(t))
	assert.Equal(t, uint64(2), c.service.incremental.graph.Counters(c.signatureKey).Executions)
	assert.Equal(t, uint64(1), c.service.incremental.graph.Counters(c.componentKey).Executions)
	assert.Equal(t, 1, c.engine.executions("feature", "route"))
	assert.True(t, c.hasCachedResult())

	c.bindWithLabel("two")
	assert.Equal(t, "two/route@reconcile\n", c.render(t))
	committedSignature := c.service.incremental.graph.Counters(c.signatureKey)
	committedComponent := c.service.incremental.graph.Counters(c.componentKey)
	assert.Equal(t, uint64(3), committedSignature.Executions)
	assert.Equal(t, uint64(2), committedComponent.Executions)
	assert.Equal(t, 2, c.engine.executions("feature", "route"))
	return committedSignature, committedComponent
}

func (c *activationTransitionCase) requireFailedRenderKeepsCache(
	t *testing.T,
	committedSignature, committedComponent incremental.NodeCounters,
) {
	t.Helper()
	c.cfg.TemplatingSettings.ExtraContext["bindings"] = map[string]any{}
	c.cfg.TemplatingSettings.ExtraContext["failAfter"] = true
	failed, err := c.service.Render(t.Context(), c.provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "forced activation failure")
	assert.Nil(t, failed)
	assert.Equal(t, committedSignature, c.service.incremental.graph.Counters(c.signatureKey))
	assert.Equal(t, committedComponent, c.service.incremental.graph.Counters(c.componentKey))
	assert.True(t, c.hasCachedResult())

	c.bindWithLabel("two")
	c.cfg.TemplatingSettings.ExtraContext["failAfter"] = false
	assert.Equal(t, "two/route@reconcile\n", c.render(t))
	assert.Equal(t, committedSignature, c.service.incremental.graph.Counters(c.signatureKey))
	assert.Equal(t, committedComponent, c.service.incremental.graph.Counters(c.componentKey))
}

func (c *activationTransitionCase) requireAdmissionKeepsCache(
	t *testing.T,
	committedSignature, committedComponent incremental.NodeCounters,
) {
	t.Helper()
	proposed := activationResource("route", "admission", false)
	overlay := stores.NewOverlayStoreProvider(
		c.provider,
		stores.NewValidationContext(map[string]*stores.StoreOverlay{
			"routes": stores.NewStoreOverlayForUpdate(&unstructured.Unstructured{Object: proposed}),
		}),
	)
	admission, err := c.service.Render(
		t.Context(), overlay, rendercontext.RenderModeAdmission,
		rendercontext.WithAdmissionSubject("routes", "default", "route"),
	)
	require.NoError(t, err)
	assert.Equal(t, inactiveActivationOutput, admission.HAProxyConfig)
	admission.InputTransaction.Abort()
	assert.Equal(t, committedSignature, c.service.incremental.graph.Counters(c.signatureKey))
	assert.Equal(t, committedComponent, c.service.incremental.graph.Counters(c.componentKey))
	assert.True(t, c.hasCachedResult())
	assert.Equal(t, "two/route@reconcile\n", c.render(t))
}

func (c *activationTransitionCase) requireUnbindAndRebind(t *testing.T) {
	t.Helper()
	c.cfg.TemplatingSettings.ExtraContext["bindings"] = map[string]any{}
	assert.Equal(t, inactiveActivationOutput, c.render(t))
	assert.Zero(t, c.service.incremental.graph.Counters(c.signatureKey))
	assert.Zero(t, c.service.incremental.graph.Counters(c.componentKey))
	assert.False(t, c.hasCachedResult())
	assert.Equal(t, 2, c.engine.executions("feature", "route"))

	c.bindWithLabel("three")
	assert.Equal(t, "three/route@reconcile\n", c.render(t))
	assert.Equal(t, uint64(1), c.service.incremental.graph.Counters(c.signatureKey).Executions)
	assert.Equal(t, uint64(1), c.service.incremental.graph.Counters(c.componentKey).Executions)
	assert.True(t, c.hasCachedResult())
	assert.Equal(t, 3, c.engine.executions("feature", "route"))
}

func TestIncrementalActivationDynamicBindingTransitionsAreTransactional(t *testing.T) {
	transition := newActivationTransitionCase(t)
	committedSignature, committedComponent := transition.activateBinding(t)
	transition.requireFailedRenderKeepsCache(t, committedSignature, committedComponent)
	transition.requireAdmissionKeepsCache(t, committedSignature, committedComponent)
	transition.requireUnbindAndRebind(t)
}

func newActivationFixture(t *testing.T) *activationFixture {
	t.Helper()
	cfg := activationConfig(false)
	service, engine := newActivationService(t, cfg)
	store := k8sstore.NewMemoryStore(2)
	return &activationFixture{
		config:   cfg,
		service:  service,
		engine:   engine,
		store:    store,
		provider: stores.NewRealStoreProvider(map[string]stores.Store{"routes": store}),
	}
}

func activationConfig(governance bool) *config.Config {
	return &config.Config{
		Dataplane: testDataplaneConfig(),
		TemplatingSettings: config.TemplatingSettings{ExtraContext: map[string]any{
			"governance": governance,
			"failAfter":  false,
		}},
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
					BindingsTemplate: `{{ toJSON(map[string]any{"routes": map[string]any{"enabled": extraContext["governance"]}}) }}`,
					Effects:          []config.IncrementalEffect{config.IncrementalEffectDeriveResource},
				},
				Template: `{%%
if tostring(props["enabled"]) == "true" {
  deriveResource(source, item, "` + activationPath + `", "yes")
}
%%}`,
			},
			"feature": {
				Name:     "feature",
				Requires: []string{"routes"},
				Incremental: &config.IncrementalTemplate{
					Source:            "routes",
					WhenAnyPathExists: []string{activationPath},
				},
				Template: `feature:{{ item | dig_string("", "metadata", "name") }}
`,
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: `{{ render "feature" }}{{ render "governance" }}{%- if tostring(extraContext["failAfter"]) == "true" -%}{{- fail("forced activation failure") -}}{%- end -%}`},
	}
}

func newActivationService(tb testing.TB, cfg *config.Config) (*RenderService, *activationCountingEngine) {
	tb.Helper()
	require.NoError(tb, config.ValidateTemplateStructure(cfg))
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types:  map[string]reflect.Type{},
		Kinds:  map[string]string{},
		Errors: map[string]error{},
	})
	base, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(tb, err)
	engine := newActivationCountingEngine(tb, base)
	return NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()}), engine
}

func activationResource(name, version string, enabled bool) map[string]any {
	annotations := map[string]any{}
	if enabled {
		annotations["example.test/enabled"] = nil
	}
	return map[string]any{
		"apiVersion": "example.test/v1",
		"kind":       "Route",
		"metadata": map[string]any{
			"namespace":   "default",
			"name":        name,
			"annotations": annotations,
		},
		"spec": map[string]any{"version": version},
	}
}

func (f *activationFixture) add(t *testing.T, item map[string]any) {
	t.Helper()
	_, name, ok := resourceIdentity(item)
	require.True(t, ok)
	require.NoError(t, f.store.Add(item, []string{"default", name}))
}

func (f *activationFixture) update(t *testing.T, item map[string]any) {
	t.Helper()
	_, name, ok := resourceIdentity(item)
	require.True(t, ok)
	require.NoError(t, f.store.Update(item, []string{"default", name}))
}

func (f *activationFixture) renderAndCommit(t *testing.T) *RenderResult {
	t.Helper()
	return renderActivation(t, false, f.service, f.provider, rendercontext.RenderModeReconcile)
}

func renderActivation(
	t *testing.T,
	cold bool,
	service *RenderService,
	provider stores.StoreProvider,
	mode rendercontext.RenderMode,
	options ...rendercontext.Option,
) *RenderResult {
	t.Helper()
	var (
		result *RenderResult
		err    error
	)
	if cold {
		result, err = renderServiceStaticCold(t, service, provider)
	} else {
		result, err = service.Render(t.Context(), provider, mode, options...)
	}
	require.NoError(t, err)
	if result.InputTransaction != nil {
		require.NoError(t, result.InputTransaction.Commit(t.Context()))
	}
	if !cold && mode == rendercontext.RenderModeReconcile {
		waitForIncrementalCache(t, service)
	}
	return result
}

type activationCountingEngine struct {
	templating.Engine
	executor      templating.IncrementalComponentExecutor
	batchExecutor templating.IncrementalComponentBatchExecutor
	planner       templating.IncrementalBindingPlannerExecutor

	mu     sync.Mutex
	counts map[string]int
}

func newActivationCountingEngine(tb testing.TB, engine templating.Engine) *activationCountingEngine {
	tb.Helper()
	executor, ok := engine.(templating.IncrementalComponentExecutor)
	require.True(tb, ok)
	batchExecutor, ok := engine.(templating.IncrementalComponentBatchExecutor)
	require.True(tb, ok)
	planner, ok := engine.(templating.IncrementalBindingPlannerExecutor)
	require.True(tb, ok)
	return &activationCountingEngine{
		Engine: engine, executor: executor, batchExecutor: batchExecutor,
		planner: planner, counts: map[string]int{},
	}
}

func (e *activationCountingEngine) RenderIncrementalBindings(
	ctx context.Context,
	templateName string,
	extraContext map[string]any,
) ([]byte, error) {
	return e.planner.RenderIncrementalBindings(ctx, templateName, extraContext)
}

func (e *activationCountingEngine) RenderIncrementalComponent(
	ctx context.Context,
	templateName string,
	templateContext map[string]any,
) (string, error) {
	e.recordExecution(templateName, templateContext)
	return e.executor.RenderIncrementalComponent(ctx, templateName, templateContext)
}

func (e *activationCountingEngine) RenderIncrementalComponents(
	ctx context.Context,
	templateName string,
	items []templating.IncrementalComponentBatchItem,
) ([]string, error) {
	for index := range items {
		e.recordExecution(templateName, items[index].TemplateContext)
	}
	return e.batchExecutor.RenderIncrementalComponents(ctx, templateName, items)
}

func (e *activationCountingEngine) recordExecution(templateName string, templateContext map[string]any) {
	item, _ := templateContext["item"].(map[string]any)
	_, name, _ := resourceIdentity(item)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.counts[templateName+"/"+name]++
}

func (e *activationCountingEngine) executions(component, name string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.counts[helpers.IncrementalEntryPointName(component)+"/"+name]
}

func (e *activationCountingEngine) totalExecutions() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	var total int
	for _, count := range e.counts {
		total += count
	}
	return total
}
