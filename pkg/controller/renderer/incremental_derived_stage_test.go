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
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"sync"
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

const derivedStageOwnerTemplate = `{%%
var namespace = item | dig_string("", "metadata", "namespace")
var name = item | dig_string("", "metadata", "name")
deriveResource(source, item, "metadata.annotations.governed", "yes")
var current = resources.routes.GetSingle(namespace, name)
if (current | dig_string("<missing>", "metadata", "annotations", "governed")) != "yes" {
  fail("derived owner self-read did not observe its current transformation")
}
%%}`

const derivedStageConsumerTemplate = `{%%
var namespace = item | dig_string("", "metadata", "namespace")
var name = item | dig_string("", "metadata", "name")
var current = resources.routes.GetSingle(namespace, name)
var governed = current | dig_string("<missing>", "metadata", "annotations", "governed")
if governed != "yes" { fail("derived projection was not prepared before its consumer") }
show name + "=" + governed + "\n"
%%}`

const derivedStageDirectItemConsumerTemplate = `{%%
var name = item | dig_string("", "metadata", "name")
var governed = item | dig_string("<missing>", "metadata", "annotations", "governed")
show name + "=" + governed + "\n"
%%}`

const derivedStageDirectItemParityConsumerTemplate = `{%%
var namespace = item | dig_string("", "metadata", "namespace")
var name = item | dig_string("", "metadata", "name")
var current = resources.routes.GetSingle(namespace, name)
if toJSON(item) != toJSON(current) {
  fail("projected item differs from its same-source GetSingle result")
}
show namespace + "/" + name + "=" +
  (item | dig_string("", "spec", "version")) + "/" +
  (item | dig_string("<missing>", "metadata", "annotations", "governed")) + "\n"
%%}`

const derivedStageAssertionTemplate = `{%%
var namespace = item | dig_string("", "metadata", "namespace")
var name = item | dig_string("", "metadata", "name")
var current = resources.routes.GetSingle(namespace, name)
if (current | dig_string("<missing>", "metadata", "annotations", "governed")) != "yes" {
  fail("derived projection was not prepared before its consumer root")
}
%%}`

func TestDerivedStageProjectionPrecedesOwnerCall(t *testing.T) {
	for _, cold := range []bool{false, true} {
		name := "warm"
		if cold {
			name = "cold"
		}
		t.Run(name, func(t *testing.T) {
			cfg := derivedStageConfig(false, derivedStageConsumerTemplate)
			result := renderDerivedStage(t, cfg, cold, derivedStageRoute("route", "v1"), nil)
			assert.Equal(t, "route=yes\n", result.HAProxyConfig)
		})
	}
}

func TestDerivedStageProjectionWithinOneGroup(t *testing.T) {
	for _, cold := range []bool{false, true} {
		name := "warm"
		if cold {
			name = "cold"
		}
		t.Run(name, func(t *testing.T) {
			cfg := derivedStageConfig(true, derivedStageConsumerTemplate)
			result := renderDerivedStage(t, cfg, cold, derivedStageRoute("route", "v1"), nil)
			assert.Equal(t, "route=yes\n", result.HAProxyConfig)
		})
	}
}

func TestDerivedStageProjectsDirectItem(t *testing.T) {
	for _, cold := range []bool{false, true} {
		name := "warm"
		if cold {
			name = "cold"
		}
		t.Run(name, func(t *testing.T) {
			cfg := derivedStageConfig(false, derivedStageDirectItemConsumerTemplate)
			store := derivedStageLegacyStore(t, cold, derivedStageRoute("route", "v1"))
			original := encodedDerivedStageStore(t, store)
			provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": store})
			service := newDerivedStageService(t, cfg, nil)

			result := renderAndCommitDerivedStageModeCacheReady(t, cold, service, provider)
			assert.Equal(t, "route=yes\n", result.HAProxyConfig)
			assert.Equal(t, original, encodedDerivedStageStore(t, store))
		})
	}
}

func TestDerivedStageDirectItemMatchesSameSourceGetSingleAcrossMutations(t *testing.T) {
	cfg := derivedStageConfig(false, derivedStageDirectItemParityConsumerTemplate)
	store := k8sstore.NewMemoryStore(2)
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": store})
	service := newDerivedStageService(t, cfg, nil)

	route := derivedStageRoute("route", "v1")
	require.NoError(t, store.Add(route, []string{"default", "route"}))
	assert.Equal(t, "default/route=v1/yes\n", renderAndCommitDerivedStageModeCacheReady(t, false, service, provider).HAProxyConfig)

	route = derivedStageRoute("route", "v2")
	require.NoError(t, store.Update(route, []string{"default", "route"}))
	assert.Equal(t, "default/route=v2/yes\n", renderAndCommitDerivedStageModeCacheReady(t, false, service, provider).HAProxyConfig)

	require.NoError(t, store.Delete("default", "route", []string{"default", "route"}))
	route = derivedStageRoute("renamed", "v3")
	require.NoError(t, store.Add(route, []string{"default", "renamed"}))
	assert.Equal(t, "default/renamed=v3/yes\n", renderAndCommitDerivedStageModeCacheReady(t, false, service, provider).HAProxyConfig)

	require.NoError(t, store.Delete("default", "renamed", []string{"default", "renamed"}))
	route = derivedStageRoute("renamed", "v4")
	route["metadata"].(map[string]any)["namespace"] = "other"
	require.NoError(t, store.Add(route, []string{"other", "renamed"}))
	assert.Equal(t, "other/renamed=v4/yes\n", renderAndCommitDerivedStageModeCacheReady(t, false, service, provider).HAProxyConfig)
}

func TestDerivedStageDirectItemTracksOwnerProps(t *testing.T) {
	cfg := derivedStageConfig(false, derivedStageDirectItemConsumerTemplate)
	cfg.TemplatingSettings.ExtraContext = map[string]any{"value": "yes"}
	owner := cfg.TemplateSnippets["20-owner"]
	owner.Incremental.Source = ""
	owner.Incremental.BindingsTemplate = `{{- toJSON(map[string]any{"routes": map[string]any{"value": extraContext | dig("value") | fallback("")}}) -}}`
	owner.Template = `{%%
var value = tostring(props["value"])
if value != "" {
  deriveResource(source, item, "metadata.annotations.governed", value)
}
%%}`
	cfg.TemplateSnippets["20-owner"] = owner

	store := k8sstore.NewMemoryStore(2)
	require.NoError(t, store.Add(derivedStageRoute("route", "v1"), []string{"default", "route"}))
	original := encodedDerivedStageStore(t, store)
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": store})
	service := newDerivedStageService(t, cfg, nil)

	first := renderAndCommitDerivedStageModeCacheReady(t, false, service, provider)
	assert.Equal(t, "route=yes\n", first.HAProxyConfig)
	assert.Equal(t, original, encodedDerivedStageStore(t, store))
	ownerExecutions, consumerExecutions := derivedStageExecutionCounts(service, false)

	cfg.TemplatingSettings.ExtraContext["value"] = "changed"
	second := renderAndCommitDerivedStageModeCacheReady(t, false, service, provider)
	assert.Equal(t, "route=changed\n", second.HAProxyConfig)
	assert.Equal(t, original, encodedDerivedStageStore(t, store))
	assertDerivedStageExecutionCounts(t, service, false, ownerExecutions+1, consumerExecutions+1)

	third := renderAndCommitDerivedStageModeCacheReady(t, false, service, provider)
	assert.Equal(t, "route=changed\n", third.HAProxyConfig)
	assert.Equal(t, original, encodedDerivedStageStore(t, store))
	assertDerivedStageExecutionCounts(t, service, false, ownerExecutions+1, consumerExecutions+1)

	cfg.TemplatingSettings.ExtraContext["value"] = ""
	fourth := renderAndCommitDerivedStageModeCacheReady(t, false, service, provider)
	assert.Equal(t, "route=<missing>\n", fourth.HAProxyConfig)
	assert.Equal(t, original, encodedDerivedStageStore(t, store))
	assertDerivedStageExecutionCounts(t, service, false, ownerExecutions+2, consumerExecutions+2)

	cfg.TemplatingSettings.ExtraContext["value"] = "yes"
	fifth := renderAndCommitDerivedStageModeCacheReady(t, false, service, provider)
	assert.Equal(t, "route=yes\n", fifth.HAProxyConfig)
	assert.Equal(t, original, encodedDerivedStageStore(t, store))
	assertDerivedStageExecutionCounts(t, service, false, ownerExecutions+3, consumerExecutions+3)
}

func TestDerivedStageOrdinaryProjectionPrecedesOwnerCall(t *testing.T) {
	for _, cold := range []bool{false, true} {
		name := "warm"
		if cold {
			name = "cold"
		}
		t.Run(name, func(t *testing.T) {
			cfg := derivedStageConfig(false, "")
			cfg.TemplateSnippets["10-ordinary"] = config.TemplateSnippet{
				Name:     "10-ordinary",
				Requires: []string{"routes"},
				Template: `{%%
var current = resources.routes.GetSingle("default", "route")
show "route=" + (current | dig_string("<missing>", "metadata", "annotations", "governed")) + "\n"
%%}`,
			}
			cfg.HAProxyConfig.Template = `{{ render "10-ordinary" }}{{ render "20-owner" }}`
			result := renderDerivedStage(t, cfg, cold, derivedStageRoute("route", "v1"), nil)
			assert.Equal(t, "route=yes\n", result.HAProxyConfig)
		})
	}
}

func TestDerivedStageProjectionAcrossConcurrentRoots(t *testing.T) {
	rootKinds := []string{"auxiliary", "k8s"}
	for _, rootKind := range rootKinds {
		for _, cold := range []bool{false, true} {
			name := rootKind + "-warm"
			if cold {
				name = rootKind + "-cold"
			}
			t.Run(name, func(t *testing.T) {
				cfg := derivedStageConfig(false, derivedStageAssertionTemplate)
				cfg.HAProxyConfig.Template = ""
				switch rootKind {
				case "auxiliary":
					cfg.Maps = map[string]config.MapFile{
						"consumer.map": {Template: `{{ render "10-consumer" }}`},
						"owner.map":    {Template: `{{ render "20-owner" }}`},
					}
				case "k8s":
					cfg.K8sResources = map[string]config.K8sResource{
						"consumer.yaml": {Template: `{{ render "10-consumer" }}`},
						"owner.yaml":    {Template: `{{ render "20-owner" }}`},
					}
				}
				ordered := &derivedStageOrderedEngine{
					consumer: "consumer.map",
					owner:    "owner.map",
					done:     make(chan struct{}),
				}
				if rootKind == "k8s" {
					ordered.consumer = "consumer.yaml"
					ordered.owner = "owner.yaml"
				}
				renderDerivedStage(t, cfg, cold, derivedStageRoute("route", "v1"), ordered)
			})
		}
	}
}

func TestDerivedStageWarmEvaluatesOnlyAffectedOwners(t *testing.T) {
	cfg := derivedStageConfig(false, derivedStageConsumerTemplate)
	store := k8sstore.NewMemoryStore(2)
	require.NoError(t, store.Add(derivedStageRoute("a", "v1"), []string{"default", "a"}))
	require.NoError(t, store.Add(derivedStageRoute("b", "v1"), []string{"default", "b"}))
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": store})
	service := newDerivedStageService(t, cfg, nil)

	commitDerivedStageCacheReady(t, service, provider)
	owner := service.incremental.components["20-owner"]
	queryA := componentQueryKey(&owner, "routes", "default", "a")
	queryB := componentQueryKey(&owner, "routes", "default", "b")
	assert.Equal(t, uint64(1), service.incremental.graph.Counters(queryA).Executions)
	assert.Equal(t, uint64(1), service.incremental.graph.Counters(queryB).Executions)

	commitDerivedStageCacheReady(t, service, provider)
	assert.Equal(t, uint64(1), service.incremental.graph.Counters(queryA).Executions)
	assert.Equal(t, uint64(1), service.incremental.graph.Counters(queryB).Executions)

	require.NoError(t, store.Update(derivedStageRoute("a", "v2"), []string{"default", "a"}))
	commitDerivedStageCacheReady(t, service, provider)
	assert.Equal(t, uint64(2), service.incremental.graph.Counters(queryA).Executions)
	assert.Equal(t, uint64(1), service.incremental.graph.Counters(queryB).Executions)
}

func TestDerivedStageRejectsLegacyDerivationAfterPreparation(t *testing.T) {
	for _, cold := range []bool{false, true} {
		name := "warm"
		if cold {
			name = "cold"
		}
		t.Run(name, func(t *testing.T) {
			testDerivedStageRejectsLegacyDerivationAfterPreparation(t, cold)
		})
	}
}

func testDerivedStageRejectsLegacyDerivationAfterPreparation(t *testing.T, cold bool) {
	t.Helper()
	cfg := derivedStageConfig(false, `{%%
var namespace = item | dig_string("", "metadata", "namespace")
var name = item | dig_string("", "metadata", "name")
var current = resources.routes.GetSingle(namespace, name)
if (current | dig_string("<missing>", "metadata", "annotations", "governed")) != "yes" {
  fail("derived projection was not prepared before its consumer")
}
if (current | dig_string("<missing>", "metadata", "annotations", "legacy")) != "<missing>" {
  fail("legacy derivation affected a cached consumer")
}
show name + "=yes\n"
%%}`)
	cfg.TemplateSnippets["30-legacy"] = config.TemplateSnippet{
		Name:     "30-legacy",
		Requires: []string{"routes"},
		Template: `{%%
if tostring(extraContext | dig("attemptLegacy") | fallback(false)) == "true" {
  var current = resources.routes.GetSingle("default", "route")
  deriveResource("routes", current, "metadata.annotations.legacy", "bad")
}
%%}`,
	}
	cfg.HAProxyConfig.Template = `{{ render "10-consumer" }}{{ render "20-owner" }}{{ render "30-legacy" }}`
	cfg.TemplatingSettings.ExtraContext = map[string]any{"attemptLegacy": false}
	store := derivedStageLegacyStore(t, cold, derivedStageRoute("route", "v1"))
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": store})
	service := newDerivedStageService(t, cfg, nil)
	original := encodedDerivedStageStore(t, store)

	first := renderAndCommitDerivedStageModeCacheReady(t, cold, service, provider)
	assert.Equal(t, "route=yes\n", first.HAProxyConfig)
	assert.Equal(t, original, encodedDerivedStageStore(t, store))
	ownerExecutions, consumerExecutions := derivedStageExecutionCounts(service, cold)

	cfg.TemplatingSettings.ExtraContext["attemptLegacy"] = true
	failed, err := renderDerivedStageMode(t, cold, service, provider)
	require.ErrorIs(t, err, rendercontext.ErrDerivedResourceViewFrozen)
	assert.Nil(t, failed)
	assert.Equal(t, original, encodedDerivedStageStore(t, store))

	cfg.TemplatingSettings.ExtraContext["attemptLegacy"] = false
	recovered := renderAndCommitDerivedStageModeCacheReady(t, cold, service, provider)
	assert.Equal(t, "route=yes\n", recovered.HAProxyConfig)
	assert.Equal(t, original, encodedDerivedStageStore(t, store))
	assertDerivedStageExecutionCounts(t, service, cold, ownerExecutions, consumerExecutions)
}

func derivedStageLegacyStore(t *testing.T, cold bool, item any) stores.Store {
	t.Helper()
	if cold {
		return &derivedStageColdStore{items: []any{item}}
	}
	store := k8sstore.NewMemoryStore(2)
	require.NoError(t, store.Add(item, []string{"default", "route"}))
	return store
}

func renderDerivedStageMode(
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

func renderAndCommitDerivedStageModeCacheReady(
	t *testing.T,
	cold bool,
	service *RenderService,
	provider stores.StoreProvider,
) *RenderResult {
	t.Helper()
	result, err := renderDerivedStageMode(t, cold, service, provider)
	require.NoError(t, err)
	if result.InputTransaction != nil {
		require.NoError(t, result.InputTransaction.Commit(t.Context()))
		if !cold {
			waitForIncrementalCache(t, service)
		}
	}
	return result
}

func derivedStageExecutionCounts(
	service *RenderService,
	cold bool,
) (ownerExecutions, consumerExecutions uint64) {
	if cold {
		return 0, 0
	}
	ownerComponent := service.incremental.components["20-owner"]
	consumerComponent := service.incremental.components["10-consumer"]
	ownerExecutions = service.incremental.graph.Counters(
		componentQueryKey(&ownerComponent, "routes", "default", "route"),
	).Executions
	consumerExecutions = service.incremental.graph.Counters(
		componentQueryKey(&consumerComponent, "routes", "default", "route"),
	).Executions
	return ownerExecutions, consumerExecutions
}

func assertDerivedStageExecutionCounts(
	t *testing.T,
	service *RenderService,
	cold bool,
	wantOwner, wantConsumer uint64,
) {
	t.Helper()
	if cold {
		return
	}
	owner, consumer := derivedStageExecutionCounts(service, false)
	assert.Equal(t, wantOwner, owner)
	assert.Equal(t, wantConsumer, consumer)
}

func derivedStageConfig(sameGroup bool, consumerTemplate string) *config.Config {
	ownerIncremental := &config.IncrementalTemplate{
		Source:  "routes",
		Effects: []config.IncrementalEffect{config.IncrementalEffectDeriveResource},
	}
	consumerIncremental := &config.IncrementalTemplate{Source: "routes"}
	if sameGroup {
		ownerIncremental.Group = "stage"
		consumerIncremental.Group = "stage"
	}
	snippets := map[string]config.TemplateSnippet{
		"20-owner": {
			Name:        "20-owner",
			Requires:    []string{"routes"},
			Incremental: ownerIncremental,
			Template:    derivedStageOwnerTemplate,
		},
	}
	root := `{{ render "20-owner" }}`
	if consumerTemplate != "" {
		snippets["10-consumer"] = config.TemplateSnippet{
			Name:        "10-consumer",
			Requires:    []string{"routes"},
			Incremental: consumerIncremental,
			Template:    consumerTemplate,
		}
		root = `{{ render "10-consumer" }}{{ render "20-owner" }}`
	}
	return &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"routes": {
				APIVersion: "example.test/v1",
				Resources:  "routes",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: snippets,
		HAProxyConfig:    config.HAProxyConfig{Template: root},
	}
}

func derivedStageRoute(name, version string) map[string]any {
	return map[string]any{
		"apiVersion": "example.test/v1",
		"kind":       "Example",
		"metadata": map[string]any{
			"namespace":   "default",
			"name":        name,
			"annotations": map[string]any{},
		},
		"spec": map[string]any{"version": version},
	}
}

func renderDerivedStage(
	t *testing.T,
	cfg *config.Config,
	cold bool,
	item map[string]any,
	ordered *derivedStageOrderedEngine,
) *RenderResult {
	t.Helper()
	var store stores.Store
	if cold {
		store = &derivedStageColdStore{items: []any{item}}
	} else {
		memory := k8sstore.NewMemoryStore(2)
		require.NoError(t, memory.Add(item, []string{"default", item["metadata"].(map[string]any)["name"].(string)}))
		store = memory
	}
	service := newDerivedStageService(t, cfg, ordered)
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": store})
	var result *RenderResult
	var err error
	if cold {
		result, err = renderServiceStaticCold(t, service, provider)
	} else {
		result, err = service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	}
	require.NoError(t, err)
	if result.InputTransaction != nil {
		require.NoError(t, result.InputTransaction.Commit(t.Context()))
	}
	return result
}

func commitDerivedStageCacheReady(t *testing.T, service *RenderService, provider stores.StoreProvider) {
	t.Helper()
	result, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NotNil(t, result.InputTransaction)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, service)
}

func encodedDerivedStageStore(t *testing.T, store stores.Store) []byte {
	t.Helper()
	items, err := store.List()
	require.NoError(t, err)
	encoded, err := json.Marshal(items)
	require.NoError(t, err)
	return encoded
}

func newDerivedStageService(
	t *testing.T,
	cfg *config.Config,
	ordered *derivedStageOrderedEngine,
) *RenderService {
	t.Helper()
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types:  map[string]reflect.Type{},
		Kinds:  map[string]string{},
		Errors: map[string]error{},
	})
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	selected := engine
	if ordered != nil {
		ordered.Engine = engine
		selected = ordered
	}
	return NewRenderService(&RenderServiceConfig{Engine: selected, Config: cfg, Logger: slog.Default()})
}

type derivedStageOrderedEngine struct {
	templating.Engine
	consumer string
	owner    string
	done     chan struct{}
	once     sync.Once
}

func (e *derivedStageOrderedEngine) Render(
	ctx context.Context,
	templateName string,
	templateContext map[string]any,
) (string, error) {
	if templateName == e.owner {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-e.done:
		}
	}
	if templateName == e.consumer {
		defer e.once.Do(func() { close(e.done) })
	}
	return e.Engine.Render(ctx, templateName, templateContext)
}

func (e *derivedStageOrderedEngine) RenderIncrementalComponent(
	ctx context.Context,
	templateName string,
	templateContext map[string]any,
) (string, error) {
	return e.Engine.(templating.IncrementalComponentExecutor).RenderIncrementalComponent(
		ctx,
		templateName,
		templateContext,
	)
}

func (e *derivedStageOrderedEngine) RenderIncrementalComponents(
	ctx context.Context,
	templateName string,
	items []templating.IncrementalComponentBatchItem,
) ([]string, error) {
	return e.Engine.(templating.IncrementalComponentBatchExecutor).RenderIncrementalComponents(
		ctx,
		templateName,
		items,
	)
}

func (e *derivedStageOrderedEngine) RenderIncrementalBindings(
	ctx context.Context,
	templateName string,
	extraContext map[string]any,
) ([]byte, error) {
	return e.Engine.(templating.IncrementalBindingPlannerExecutor).RenderIncrementalBindings(
		ctx,
		templateName,
		extraContext,
	)
}

type derivedStageColdStore struct {
	items []any
}

func (s *derivedStageColdStore) Get(keys ...string) ([]any, error) {
	var result []any
	for _, item := range s.items {
		namespace, name, identified := resourceIdentity(item)
		if !identified || len(keys) > 0 && namespace != keys[0] || len(keys) > 1 && name != keys[1] {
			continue
		}
		result = append(result, item)
	}
	return result, nil
}

func (s *derivedStageColdStore) List() ([]any, error) {
	return slices.Clone(s.items), nil
}

func (*derivedStageColdStore) Add(any, []string) error {
	return errors.New("derived stage cold store is read-only")
}

func (*derivedStageColdStore) Update(any, []string) error {
	return errors.New("derived stage cold store is read-only")
}

func (*derivedStageColdStore) Delete(string, string, []string) error {
	return errors.New("derived stage cold store is read-only")
}

func (*derivedStageColdStore) Clear() error {
	return errors.New("derived stage cold store is read-only")
}
